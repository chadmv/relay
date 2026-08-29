package schedrunner

import (
	"errors"
	"strings"
)

// maxFailureTextBytes bounds what a fire failure writes into
// scheduled_jobs.last_error. 1024 is comfortably above every fixed-format
// message jobspec.Validate emits; the only way to exceed it is an
// operator-chosen task name of roughly a kilobyte.
//
// STORAGE IS BOUNDED BY CONSTRUCTION, not by this number alone: the write is an
// UPDATE of one already-locked row, not an append, so a failing schedule costs
// at most 1 KB, once, forever.
const maxFailureTextBytes = 1024

// truncationMarker is appended when the bound bites, so a reader can tell a
// truncated message from a short one. run-now returns the untruncated message.
const truncationMarker = "... (truncated)"

// failureTextUnavailable is stored when sanitization leaves nothing legible.
//
// STORING AN EMPTY STRING IS NOT AN OPTION. scheduledJobResponse carries
// `omitempty` on last_error, so "" is indistinguishable from absent on the wire,
// and absent must mean "no failure". A fixed fallback keeps the two apart.
const failureTextUnavailable = "fire failed; message unavailable"

// permanentFireError marks a fireOne failure as a fact about the schedule's OWN
// STORED DATA rather than about the infrastructure.
//
// THE PARTITION IS JUSTIFIED TWICE OVER, and both arguments point the same way.
// Semantically: a database blip is not a fact about the schedule, and an
// operator who learns to ignore a noisy field has lost the field. The three
// permanent classes share the property that an identical attempt later gets an
// identical answer - the same partition relayclient.ErrorIsTransient documents
// and the same one handleRunScheduledJobNow reasons about when it chooses 400
// over 500. By disclosure: the permanent messages are derived from data the
// schedule's owner supplied, while the transient ones are wrapped pgx errors,
// which can carry constraint names, column names, connection strings and host
// names. internal/api has a settled convention of not disclosing internals
// (writeError(w, 500, "db error"), never the pgx message), and storing a pgx
// error in a column rendered by the SPA, the CLI, the Python SDK and the MCP
// server would sidestep that convention through the back door. (A COUNT IS NOT
// CARRIED HERE ON PURPOSE. "four clients" was written when there were four, and
// was already an undercount by the time MCP started labelling last_error in the
// same PR. An enumeration goes stale loudly - a reader can check it - where a
// number goes stale silently and has no maintainer.)
//
// Error() delegates to the wrapped error and adds NO PREFIX OF ITS OWN, so the
// stored string is exactly what fireOne composed and nothing about this type
// leaks into an operator's view.
type permanentFireError struct{ err error }

func (e *permanentFireError) Error() string { return e.err.Error() }
func (e *permanentFireError) Unwrap() error { return e.err }

// permanent marks err as a recordable, operator-facing fire failure.
func permanent(err error) error { return &permanentFireError{err: err} }

// recordableFailure reports whether err should be written to
// scheduled_jobs.last_error, and returns the sanitized text to write.
//
// It is a pure function of the error, so it is testable without a database -
// which is the whole reason the classification is a named function rather than
// an inline branch in TickOnce.
//
// The text comes from err.Error(), the OUTERMOST message, not from the wrapped
// one: fireOne wraps as permanent(fmt.Errorf("invalid job_spec: %w", err)), so
// the outermost text is the one carrying the human-readable prefix.
func recordableFailure(err error) (string, bool) {
	var p *permanentFireError
	if err == nil || !errors.As(err, &p) {
		return "", false
	}
	return sanitizeFailureText(err.Error()), true
}

// sanitizeFailureText makes an error message safe to store and to render, at the
// SINGLE write site. One place; the value is served by internal/api and rendered
// by the SPA, the CLI, the Python SDK and the MCP server. Enumerated rather than
// counted, for the reason given on permanentFireError above.
//
// THREE CLASSES BECOME A SPACE, and the second and third are here because the
// first alone did not close the class this comment claims it closes. The text is
// operator-controlled - a task name flows verbatim into "task %s: retries must
// be between 0 and %d" - so every one of them is reachable by a schedule owner
// and rendered to an admin.
//
//   - C0 and DEL (below U+0020, and U+007F). Newlines are not needed by any of
//     the three recorded classes, and stripping ESC is what closes ANSI escape
//     injection into `relay schedules show`'s terminal output.
//   - C1 (U+0080-U+009F). U+009B is the single-byte CSI: a terminal that decodes
//     the stream as Latin-1, or any consumer that narrows the text to bytes,
//     treats it exactly as ESC-[, so leaving C1 in place leaves the escape class
//     half-closed rather than closed.
//   - The bidirectional formatting controls (U+200E, U+200F, U+202A-U+202E,
//     U+2066-U+2069). U+202E RIGHT-TO-LEFT OVERRIDE has no terminator: it
//     reorders every character after it, so one of them in a task name rewrites
//     how the REST of an admin's line renders, in the SPA panel and in the
//     terminal alike. That is a display attack, not an escape sequence, which is
//     why stripping C0 does nothing about it.
//
// TWO OTHER SITES CARRY THE BYTE-IDENTICAL RUNE SET, and both are on the READ
// side rather than this write side:
//
//   - internal/cli/schedules.go, terminalSafeLine - what `relay schedules show`
//     puts on a terminal.
//   - internal/relayclient/sanitize.go, sanitizeServerText - applied in Do to
//     the message of a server error body, which is the un-escaping site for
//     every relayclient consumer and so covers the MCP server for free.
//
// THEY ARE DELIBERATELY NOT SHARED. internal/cli and internal/relayclient are
// client packages; importing internal/schedrunner from either to reach one
// predicate would be a worse coupling than the duplication. IF THIS SET MOVES,
// MOVE BOTH OF THOSE WITH IT.
//
// Invalid UTF-8 in the input is replaced with U+FFFD by the range-over-string
// plus WriteRune round trip, so the output is always valid UTF-8. That matters:
// last_error is a TEXT column and Postgres rejects invalid UTF-8 in TEXT.
func sanitizeFailureText(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if r < 0x20 || (r >= 0x7f && r <= 0x9f) ||
			r == 0x200e || r == 0x200f ||
			(r >= 0x202a && r <= 0x202e) ||
			(r >= 0x2066 && r <= 0x2069) {
			b.WriteRune(' ')
			continue
		}
		b.WriteRune(r)
	}
	out := strings.TrimSpace(b.String())
	if out == "" {
		return failureTextUnavailable
	}
	if len(out) <= maxFailureTextBytes {
		return out
	}
	// TRUNCATE ON A RUNE BOUNDARY. Ranging a string yields the byte index of
	// each rune's FIRST byte, so the largest such index at or below the budget
	// is a safe cut point. A plain out[:limit] would split a multi-byte rune
	// roughly half the time and the UPDATE would fail, not merely look wrong.
	limit := maxFailureTextBytes - len(truncationMarker)
	cut := 0
	for i := range out {
		if i > limit {
			break
		}
		cut = i
	}
	return out[:cut] + truncationMarker
}
