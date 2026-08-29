package relayclient

import "strings"

// sanitizeServerText makes a server-supplied message safe for a client to
// render, by mapping every control and bidi-formatting rune to a space.
//
// WHY THIS LIVES IN THE TRANSPORT AND NOT IN internal/cli. The obvious home is
// the renderer, and it is the wrong one for a reason that is about provenance
// rather than about layering:
//
//   - THIS IS THE UN-ESCAPING SITE. An ESC crosses the wire as the JSON escape
//     \u001b, which is inert; it becomes a real ESC byte in Do's
//     json.Decoder.Decode and nowhere else. Sanitizing where the bytes are
//     created is the same argument that puts readJSON in one place in
//     internal/api and tokenhash.Hash in one place everywhere.
//   - IT PARTITIONS BY PROVENANCE, WHICH A RENDERER CANNOT. Applied here it
//     covers exactly the strings a remote endpoint composed and leaves relay's
//     own locally built error text alone. Applied at internal/cli's single
//     error print it would also mangle relay's own multi-line messages, and it
//     would have no way to tell the two apart.
//   - TWO CONSUMERS NEED THE SAME ANSWER. internal/cli prints ResponseError to
//     a terminal; internal/mcp hands it to a model through MapError. Neither
//     wants an escape sequence or a bidi override, and a second copy of the
//     policy in one of them would drift from the other. ErrorIsTransient in
//     client.go is here for the same reason and says so.
//
// IT DOES NOT TRUNCATE, and that is a contract, not an omission. README tells an
// operator that `relay schedules run-now` returns the stored 1 KB failure
// message IN FULL, so length and control characters have to stay independent
// properties. The mapping is one rune to one rune, so rune count is preserved.
//
// The rune set is byte-identical to internal/cli's terminalSafeLine and
// internal/schedrunner's sanitizeFailureText. THE THREE MUST STAY IN STEP; they
// are three functions because internal/cli must not import internal/schedrunner
// and because each runs on a different subject. See terminalSafeLine's comment
// for what each family of runes does and why printable non-ASCII is left alone.
func sanitizeServerText(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r < 0x20, r >= 0x7f && r <= 0x9f:
			return ' '
		case r == 0x200e, r == 0x200f:
			return ' '
		case r >= 0x202a && r <= 0x202e:
			return ' '
		case r >= 0x2066 && r <= 0x2069:
			return ' '
		}
		return r
	}, s)
}
