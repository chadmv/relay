package api

import (
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

// The two 409 diagnostic log lines in handleRetryJob take a task-id slice whose
// only lower bound is "not zero" (jobspec bounds task count against zero and
// nothing else). A blocked retry is a PERMANENT condition, so an operator
// pressing Retry again re-emits the same line, each time holding log's global
// mutex for however long the line takes to write. The line must therefore stay
// bounded no matter how many ids it is handed.
func TestUUIDStrHead_IsBoundedRegardlessOfInput(t *testing.T) {
	const max = 8

	got := uuidStrHead(testUUIDs(5000), max)
	if strings.Count(got, ",") >= 5000 {
		t.Fatalf("head rendered every id: %d commas", strings.Count(got, ","))
	}
	if len(got) > 512 {
		t.Fatalf("head is %d bytes; a diagnostic line must stay small", len(got))
	}
	if !strings.Contains(got, "+4992 more") {
		t.Fatalf("truncation must say how many ids were dropped, got %q", got)
	}
}

// Below the cap nothing is dropped and nothing is annotated: the common case is
// a handful of ids, and that case must read exactly as it did before.
func TestUUIDStrHead_ShortListIsRenderedWhole(t *testing.T) {
	ids := testUUIDs(3)
	got := uuidStrHead(ids, 8)
	if strings.Contains(got, "more") {
		t.Fatalf("a short list must not be annotated as truncated: %q", got)
	}
	for _, id := range ids {
		if !strings.Contains(got, uuidStr(id)) {
			t.Fatalf("missing id %s in %q", uuidStr(id), got)
		}
	}
}

func TestUUIDStrHead_Empty(t *testing.T) {
	if got := uuidStrHead(nil, 8); got != "" {
		t.Fatalf("nil ids must render empty, got %q", got)
	}
}
