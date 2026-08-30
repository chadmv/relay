package api

import "testing"

// TestCanonicalJobIDFilter is the cheap, exhaustive statement of what
// ?job_id= accepts, and the only place a pgx upgrade that narrowed or widened
// pgtype.UUID.Scan would be caught. It runs on `make test` with no container.
//
// Every row below is exercised by this test, so this table is proof rather than
// prose. The PYTHON half of the acceptance surface - the seven spellings
// uuid.UUID takes and this server does not, three of which uuid.UUID resolves to
// a DIFFERENT uuid than the string names - is deliberately NOT restated here,
// because no Go test runs uuid.UUID and a comment about behaviour nothing checks
// is exactly this repo's dominant defect. It is measured, with its instrument,
// in docs/superpowers/specs/2026-08-30-python-sdk-follow-job-canonical-id.md
// section 4.
//
// This test says NOTHING about whether handleEvents calls the function. That is
// TestEvents_JobIDSpellingIsCanonicalisedNotRejected's job, in the integration
// lane. Both, or neither is worth much.
func TestCanonicalJobIDFilter(t *testing.T) {
	const canonical = "7e660488-1234-4321-8888-abcdefabcdef"

	// pgtype.UUID.Scan succeeds iff the input is 32 bytes of hex, or 36 bytes
	// whose indexes 0-7, 9-12, 14-17, 19-22 and 24-35 are hex. Indexes 8, 13, 18
	// and 23 are sliced out and NEVER EXAMINED, which is what admits the four
	// separator rows - and what no client-side canonicaliser built on Python's
	// uuid.UUID can reproduce. Hex is case-insensitive by table lookup, not by a
	// normalisation step.
	accepted := []struct{ name, in string }{
		{"canonical", canonical},
		{"uppercase", "7E660488-1234-4321-8888-ABCDEFABCDEF"},
		{"dashless", "7e660488123443218888abcdefabcdef"},
		{"dashless uppercase", "7E660488123443218888ABCDEFABCDEF"},
		{"underscore separators", "7e660488_1234_4321_8888_abcdefabcdef"},
		{"colon separators", "7e660488:1234:4321:8888:abcdefabcdef"},
		{"space separators", "7e660488 1234 4321 8888 abcdefabcdef"},
		{"mixed separators", "7e660488-1234*4321-8888-abcdefabcdef"},
	}
	for _, tc := range accepted {
		tc := tc
		t.Run("accepted/"+tc.name, func(t *testing.T) {
			if got := canonicalJobIDFilter(tc.in); got != canonical {
				t.Fatalf("canonicalJobIDFilter(%q) = %q, want %q", tc.in, got, canonical)
			}
		})
	}

	// Everything else is returned BYTE-IDENTICAL. Never "", which would be the
	// broker's broadcast filter - see canonicalJobIDFilter's doc comment. The
	// empty row is the one case where "unchanged" and "broadcast" coincide, and
	// it is today's behaviour for GET /v1/events with no job_id at all.
	passthrough := []struct{ name, in string }{
		{"empty", ""},
		{"brace wrapped", "{" + canonical + "}"},
		{"urn prefixed", "urn:uuid:" + canonical},
		{"trailing hyphen", canonical + "-"},
		{"hyphen at a non-canonical position", "7e6604881234432188-88abcdefabcdef"},
		{"sign prefixed", "+7e660488123443218888abcdefabcde"},
		{"base prefixed", "0x7e660488123443218888abcdefabcd"},
		{"pep515 underscore inside 32 hex chars", "7e660488_23443218888abcdefabcdef"},
		{"not a uuid at all", "not-a-uuid"},
		// 36 BYTES, not 36 characters: two hex positions replaced by one
		// two-byte rune. The length test is over bytes, so this reaches the
		// 36-byte branch and is rejected by the hex table (a continuation byte
		// exceeds 0x7f and maps to 0xff).
		{"multi-byte rune occupying two hex positions", "7e6604é-1234-4321-8888-abcdefabcdef"},
	}
	for _, tc := range passthrough {
		tc := tc
		t.Run("passthrough/"+tc.name, func(t *testing.T) {
			if got := canonicalJobIDFilter(tc.in); got != tc.in {
				t.Fatalf("canonicalJobIDFilter(%q) = %q, want it returned UNCHANGED", tc.in, got)
			}
		})
	}
}
