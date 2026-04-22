package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
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

func TestServer_RateLimitsLogin(t *testing.T) {
	s := &Server{
		LoginLimitN:   2,
		LoginLimitWin: time.Minute,
	}
	h := s.Handler()

	// Three login attempts from the same IP; the third should 429.
	body := `{"email":"a@b.co","password":"x"}`
	for i := 0; i < 2; i++ {
		req := httptest.NewRequest("POST", "/v1/auth/login", strings.NewReader(body))
		req.RemoteAddr = "10.0.0.1:12345"
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()

		// Requests may panic due to missing DB; catch and verify it's not a 429.
		func() {
			defer func() { recover() }()
			h.ServeHTTP(rec, req)
		}()

		// Response code doesn't matter — the handler will fail DB lookup. We
		// only care that it's NOT 429.
		if rec.Code == http.StatusTooManyRequests {
			t.Fatalf("request %d: unexpected 429", i+1)
		}
	}

	req := httptest.NewRequest("POST", "/v1/auth/login", strings.NewReader(body))
	req.RemoteAddr = "10.0.0.1:12345"
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429 on third login, got %d", rec.Code)
	}
}
