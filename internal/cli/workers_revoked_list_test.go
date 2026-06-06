package cli

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWorkersList_Revoked_HitsRevokedEndpoint(t *testing.T) {
	var gotPath string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_ = json.NewEncoder(w).Encode(map[string]any{
			"items": []map[string]any{
				{"id": "w1", "name": "gone-1", "status": "revoked", "revoked_at": "2026-01-02T03:04:05Z"},
			},
			"next_cursor": "",
			"total":       1,
		})
	}))
	defer ts.Close()

	cfg := &Config{ServerURL: ts.URL, Token: "t"}
	var buf strings.Builder
	err := doWorkers(context.Background(), cfg, []string{"list", "--revoked"}, &buf)
	if err != nil {
		t.Fatalf("doWorkers: %v", err)
	}
	if gotPath != "/v1/workers/revoked" {
		t.Fatalf("expected /v1/workers/revoked, got %s", gotPath)
	}
	out := buf.String()
	if !strings.Contains(out, "REVOKED AT") {
		t.Fatalf("expected REVOKED AT column header, got:\n%s", out)
	}
	if !strings.Contains(out, "2026-01-02T03:04:05Z") {
		t.Fatalf("expected revoked_at value in output, got:\n%s", out)
	}
}
