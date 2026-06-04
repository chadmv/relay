package webui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHandler_ServesIndexForUnknownRoute(t *testing.T) {
	h := Handler()
	req := httptest.NewRequest("GET", "/jobs/deep/link", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Fatalf("content-type = %q, want text/html", ct)
	}
}

func TestHandler_DoesNotHijackAPIPaths(t *testing.T) {
	h := Handler()
	req := httptest.NewRequest("GET", "/v1/unknown", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 for /v1 path", rec.Code)
	}
}
