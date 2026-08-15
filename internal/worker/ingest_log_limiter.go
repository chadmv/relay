package worker

import "time"

// ingestLogLimiter bounds caller-driven log volume for ONE agent connection.
//
// It is two things stacked, and the split is the whole point.
//
//   - `seen` is a DEDUPLICATOR. It collapses a repeating failure to one line,
//     and it is keyed on wire-supplied values on purpose, because a caller that
//     varies the key only makes its own diagnostics noisier.
//   - `tokens` is the BOUND. It is keyed on nothing, so no wire value can
//     enlarge it.
//
// The predecessor (taskLogErrs, removed 2026-08-15) used the dedupe map AS the
// bound, so one wire field defeated both: it stored one entry per task id with
// the epoch as the VALUE, so a caller varying chunk.Epoch on a single task id
// overwrote that one entry forever, never reached the capacity cap, and got one
// log line per message with a map of exactly one entry. Do not merge these two
// back together, and in particular do not delete the bucket on the grounds that
// the map already limits things - the map deliberately does not.
//
// Note what the composite key does and does not buy. It is required because one
// map now holds four kinds of key, NOT because it closes the flood. Moving the
// epoch back into the value would still be bounded, because the bucket is the
// bound. The key shape is a diagnostics decision; the bucket is the security
// control.
//
// Owned by ONE goroutine: allocated in Connect and used only from that
// connection's recv loop. No mutex, by design - adding one would be the second
// lock on a path whose whole complaint is lock contention. If a caller ever
// appears off that goroutine, that caller is the finding, not the missing lock.
// It is a stack local in Connect, so it dies with the frame: there is no
// teardown to get wrong and no way for one connection to reach another's.
type ingestLogLimiter struct {
	seen   map[logKey]struct{}
	tokens int
	last   time.Time
	now    func() time.Time // injectable for the deterministic refill tests only
}

// logKind partitions the budget's dedupe keys. Values are never persisted or
// sent anywhere, so they may be renumbered freely.
type logKind uint8

const (
	kindTaskLogPersist logKind = iota + 1 // handleTaskLog's non-ErrNoRows persist failure
	kindBadTaskID                         // an unparseable task id, SHARED by both handlers
	kindStatusGetTask                     // handleTaskStatus's non-ErrNoRows GetTask failure
	kindInventory                         // handleInventoryUpdate's persist failure
)

// logKey is the dedupe key. Only kindTaskLogPersist populates id and epoch; the
// other three kinds deliberately carry NO wire value, so the caller cannot vary
// them (see the per-site table in the spec's section 6.4).
//
// The only wire string that ever reaches this struct is a task id that has
// already passed pgtype.UUID.Scan, so it is at most 36 characters. The bad-id
// kind, whose string has NOT been validated, stores nothing at all.
type logKey struct {
	kind  logKind
	id    string
	epoch int64
}

// All per connection, all untunable. THERE IS DELIBERATELY NO ENV KNOB: an
// operator raising the budget re-opens the vector this type exists to close, and
// no value here has an operational reason to move. Do not add one.
const (
	// Dedupe capacity. Far above any realistic count of distinct concurrently
	// failing tasks on one agent, so the honest case never clears.
	ingestLogSeenMax = 128

	// Tokens at connection start. Covers several tasks failing at once at
	// connection start without waiting on a refill.
	ingestLogBurst = 16

	// Steady state: 6 lines per minute per connection. A genuine repeating infra
	// failure still reports continuously; a flood becomes 6 lines/min instead of
	// one line per message.
	ingestLogRefill = 10 * time.Second

	// Longest prefix of an UNPARSED, caller-supplied identifier that may reach
	// the log. Both bad-task-id sites run AFTER pgtype.UUID.Scan has failed, so
	// no length constraint applies to the string: it is a proto string bounded
	// only by gRPC's 4 MiB receive limit, and %q escapes without truncating. The
	// budget alone would still permit ingestLogBurst multi-megabyte lines.
	ingestLogIDClip = 64
)

func newIngestLogLimiter() *ingestLogLimiter {
	return &ingestLogLimiter{
		seen:   make(map[logKey]struct{}),
		tokens: ingestLogBurst,
		last:   time.Now(),
		now:    time.Now,
	}
}

// allow reports whether this log line may be emitted, recording the key if so.
//
// The ORDER of the four steps is load-bearing; each one has a mutation in the
// plan's battery that only this ordering survives.
func (l *ingestLogLimiter) allow(k logKey) bool {
	// Fail CLOSED rather than panic. Connect has no recover and grpc-go does not
	// recover handler panics, so a nil dereference here would kill the whole
	// server process. Losing a diagnostic is the cheaper failure. Production has
	// exactly one allocation site (Connect) so this is unreachable there.
	if l == nil {
		return false
	}

	// 1. REFILL. Advance `last` by WHOLE CONSUMED INTERVALS, never to now:
	// setting last = now on a call that added zero tokens means a connection
	// calling more often than the refill interval never refills at all. That is
	// the most likely way to get this wrong.
	//
	// time.Now carries a monotonic reading and Sub uses it, so a wall-clock
	// adjustment cannot move this bucket.
	if elapsed := l.now().Sub(l.last); elapsed >= ingestLogRefill {
		n := int64(elapsed / ingestLogRefill)
		l.last = l.last.Add(time.Duration(n) * ingestLogRefill)
		if got := int64(l.tokens) + n; got >= int64(ingestLogBurst) {
			l.tokens = ingestLogBurst
		} else {
			l.tokens = int(got)
		}
	}

	// 2. DEDUPE, BEFORE the spend. An honest repeating failure - one task
	// streaming binary output - costs exactly ONE token no matter how many chunks
	// it produces.
	if _, ok := l.seen[k]; ok {
		return false
	}

	// 3. SPEND. A key suppressed for lack of a token is deliberately NOT
	// recorded, so the diagnostic reappears when tokens refill rather than being
	// swallowed for the connection's whole lifetime.
	if l.tokens == 0 {
		return false
	}

	// 4. CAPACITY. Clearing re-arms every key, which is exactly the 2026-08-12
	// defect this type replaces - BUT ONLY WHEN THE MAP IS THE BOUND. With the
	// bucket as the bound, re-arming can at worst produce another burst and then
	// 6 lines/min. The alternative, permanent suppression, is also bounded but
	// has NO TIME-BASED RECOVERY: a connection that once tripped 128 distinct
	// failures would lose the diagnostic for its whole lifetime. Deleting the
	// bucket is therefore visibly the thing that re-opens the original bug.
	if len(l.seen) >= ingestLogSeenMax {
		clear(l.seen)
	}

	l.tokens--
	l.seen[k] = struct{}{}
	return true
}

// clipID bounds an UNPARSED caller-supplied identifier before it reaches a log
// line. Callers must still use %q on the result: clipping is a volume control,
// %q is the injection control, and neither substitutes for the other. Slicing
// may split a UTF-8 rune; %q renders the partial bytes as \xNN escapes, which is
// safe.
func clipID(s string) string {
	if len(s) <= ingestLogIDClip {
		return s
	}
	return s[:ingestLogIDClip] + "...(truncated)"
}
