package api

import (
	"fmt"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
)

func testUUIDs(n int) []pgtype.UUID {
	out := make([]pgtype.UUID, n)
	for i := range out {
		var u pgtype.UUID
		u.Valid = true
		u.Bytes[15] = byte(i)
		u.Bytes[14] = byte(i >> 8)
		out[i] = u
	}
	return out
}

// renderedIDs counts the ids in a uuidStrHead result, not counting the
// truncation annotation.
func renderedIDs(s string) int {
	if s == "" {
		return 0
	}
	n := 0
	for _, part := range strings.Split(s, ",") {
		if !strings.HasPrefix(part, "... (+") {
			n++
		}
	}
	return n
}

// The two 409 diagnostic log lines in handleRetryJob take a task-id slice whose
// only lower bound is "not zero" (jobspec bounds task count against zero and
// nothing else), and log.Printf holds a global mutex, so the rendering must stay
// small no matter how many ids it is handed.
//
// Every assertion here is written against logIDHead, the constant the two call
// sites actually pass. An earlier version of this test passed its own literal 8,
// which pinned nothing: raising logIDHead to 5000 left all of it green while the
// production lines went unbounded.
func TestUUIDStrHead_LogIDHeadIsSmall(t *testing.T) {
	// The point of the cap is that the line is the same size for a 3-task job
	// and a 3000-task job. A large cap satisfies "bounded" while defeating it.
	if logIDHead > 32 {
		t.Fatalf("logIDHead is %d: too large to bound a diagnostic log line", logIDHead)
	}
}

func TestUUIDStrHead_TruncatesAtLogIDHead(t *testing.T) {
	const n = 5000
	got := uuidStrHead(testUUIDs(n), logIDHead)

	if renderedIDs(got) != logIDHead {
		t.Fatalf("rendered %d ids, want logIDHead=%d: %q", renderedIDs(got), logIDHead, got)
	}
	want := fmt.Sprintf("... (+%d more)", n-logIDHead)
	if !strings.Contains(got, want) {
		t.Fatalf("truncation must say how many ids were dropped (%q), got %q", want, got)
	}
	// A uuid renders as 36 bytes plus a separator; the annotation is short.
	if max := logIDHead*37 + 32; len(got) > max {
		t.Fatalf("head is %d bytes, want <= %d for logIDHead=%d", len(got), max, logIDHead)
	}
}

// Below the cap nothing is dropped and nothing is annotated: the common case is
// a handful of ids, and that case must read exactly as it did before.
func TestUUIDStrHead_ShortListIsRenderedWhole(t *testing.T) {
	ids := testUUIDs(logIDHead)
	got := uuidStrHead(ids, logIDHead)
	if strings.Contains(got, "more") {
		t.Fatalf("a list at the cap must not be annotated as truncated: %q", got)
	}
	for _, id := range ids {
		if !strings.Contains(got, uuidStr(id)) {
			t.Fatalf("missing id %s in %q", uuidStr(id), got)
		}
	}
}

func TestUUIDStrHead_Empty(t *testing.T) {
	if got := uuidStrHead(nil, logIDHead); got != "" {
		t.Fatalf("nil ids must render empty, got %q", got)
	}
}
