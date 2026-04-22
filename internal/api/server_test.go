package api

import (
	"net/http/httptest"
	"testing"
)

func TestServer_AppliesCORSWhenConfigured(t *testing.T) {
	s := &Server{CORSOrigins: []string{"https://dashboard.studio.local"}}
	h := s.Handler()

	req := httptest.NewRequest("OPTIONS", "/v1/health", nil)
	req.Header.Set("Origin", "https://dashboard.studio.local")
	req.Header.Set("Access-Control-Request-Method", "GET")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "https://dashboard.studio.local" {
		t.Fatalf("expected CORS origin echoed, got %q", got)
	}
}

func TestServer_NoCORSByDefault(t *testing.T) {
	s := &Server{}
	h := s.Handler()

	req := httptest.NewRequest("GET", "/v1/health", nil)
	req.Header.Set("Origin", "https://example.com")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("expected no CORS header, got %q", got)
	}
}
