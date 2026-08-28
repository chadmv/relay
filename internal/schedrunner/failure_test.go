package schedrunner

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"
)

// TestRecordableFailure is the DEFAULT-LANE sibling for the failure
// classification, and it exists because the end-to-end proof
// (internal/api/scheduled_jobs_failure_visibility_integration_test.go) is
// integration-tagged and CI runs no tags. See that file's header for the full
// decision.
//
// THE TWO DISCRIMINATING CASES ARE FIRST AND SECOND, not last. A poisoned input
// placed at the end of a table cannot distinguish a function that examined it
// from one that returned before reaching it. Case 1 kills a mutant that always
// returns (`", false`); case 2 kills a mutant that always returns
// (`err.Error(), true`).
func TestRecordableFailure(t *testing.T) {
	cases := []struct {
		name     string
		err      error
		wantOK   bool
		wantText string
	}{
		{
			name:     "a stored spec that fails jobspec.Validate IS recordable",
			err:      permanent(errors.New("task t: retries must be between 0 and 10")),
			wantOK:   true,
			wantText: "task t: retries must be between 0 and 10",
		},
		{
			name:   "a database fault is NOT recordable",
			err:    fmt.Errorf("count active jobs: %w", errors.New("conn closed by peer")),
			wantOK: false,
		},
		{
			name:   "a create-job insert fault is NOT recordable",
			err:    fmt.Errorf("create job: %w", errors.New("duplicate key value violates unique constraint \"jobs_pkey\"")),
			wantOK: false,
		},
		{
			name:   "nil is NOT recordable",
			err:    nil,
			wantOK: false,
		},
		{
			name:     "an undecodable job_spec IS recordable, and keeps fireOne's prefix",
			err:      permanent(fmt.Errorf("invalid job_spec: %w", errors.New("unexpected end of JSON input"))),
			wantOK:   true,
			wantText: "invalid job_spec: unexpected end of JSON input",
		},
		{
			name:     "an unparseable cron IS recordable",
			err:      permanent(fmt.Errorf("parse cron: %w", errors.New("expected 5 fields, found 3: \"0 2 *\""))),
			wantOK:   true,
			wantText: "parse cron: expected 5 fields, found 3: \"0 2 *\"",
		},
		{
			name:     "a permanent error wrapped further is still recordable, and the OUTER text is stored",
			err:      fmt.Errorf("outer: %w", permanent(errors.New("inner"))),
			wantOK:   true,
			wantText: "outer: inner",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := recordableFailure(tc.err)
			if ok != tc.wantOK {
				t.Fatalf("recordableFailure ok = %v, want %v (err = %v)", ok, tc.wantOK, tc.err)
			}
			if got != tc.wantText {
				t.Errorf("recordableFailure text = %q, want %q", got, tc.wantText)
			}
		})
	}
}

// TestSanitizeFailureText is the second default-lane sibling. Every property
// here is a real constraint, not a style preference:
//   - control characters: the text is operator-controlled (a task name flows
//     verbatim into "task %s: retries must be between 0 and %d") and four
//     clients render it, one of them a terminal.
//   - rune-boundary truncation: last_error is a TEXT column and Postgres rejects
//     invalid UTF-8 in TEXT, so a byte-slicing truncation is a genuine write
//     failure, not a cosmetic bug.
//   - never empty: scheduledJobResponse's `omitempty` makes "" indistinguishable
//     from absent, and absent must mean "no failure".
func TestSanitizeFailureText(t *testing.T) {
	t.Run("control characters become spaces, closing terminal escape injection", func(t *testing.T) {
		got := sanitizeFailureText("task \x1b[31mred\x1b[0m: bad\nvalue\there\x07")
		if strings.ContainsAny(got, "\x00\x07\x09\x0a\x0d\x1b\x7f") {
			t.Fatalf("sanitizeFailureText left a control character in %q", got)
		}
		if !strings.Contains(got, "red") || !strings.Contains(got, "value") {
			t.Errorf("sanitizeFailureText dropped legible content: %q", got)
		}
	})

	t.Run("a message that sanitizes to nothing becomes the fixed fallback", func(t *testing.T) {
		got := sanitizeFailureText("\x00\x01 \t\r\n")
		if got != failureTextUnavailable {
			t.Fatalf("sanitizeFailureText = %q, want the fallback %q", got, failureTextUnavailable)
		}
		if got == "" {
			t.Fatal("sanitizeFailureText must never return an empty string")
		}
	})

	t.Run("a long ASCII message is truncated with the marker", func(t *testing.T) {
		got := sanitizeFailureText(strings.Repeat("x", 4000))
		if len(got) > maxFailureTextBytes {
			t.Fatalf("sanitizeFailureText returned %d bytes, want <= %d", len(got), maxFailureTextBytes)
		}
		if !strings.HasSuffix(got, truncationMarker) {
			t.Errorf("a truncated message must end with %q, got %q", truncationMarker, got[max(0, len(got)-40):])
		}
	})

	t.Run("truncation cuts on a RUNE boundary, not a byte boundary", func(t *testing.T) {
		// Every rune is 2 bytes, so a byte-boundary cut has a 50% chance of
		// splitting one and producing invalid UTF-8 that Postgres refuses.
		got := sanitizeFailureText(strings.Repeat("é", 2000))
		if !utf8.ValidString(got) {
			t.Fatalf("sanitizeFailureText produced invalid UTF-8: %q", got)
		}
		if len(got) > maxFailureTextBytes {
			t.Fatalf("sanitizeFailureText returned %d bytes, want <= %d", len(got), maxFailureTextBytes)
		}
		if !strings.HasSuffix(got, truncationMarker) {
			t.Errorf("a truncated message must end with %q", truncationMarker)
		}
	})

	t.Run("a short clean message is returned unchanged", func(t *testing.T) {
		const in = "task t: retries must be between 0 and 10"
		if got := sanitizeFailureText(in); got != in {
			t.Errorf("sanitizeFailureText(%q) = %q, want it unchanged", in, got)
		}
	})
}
