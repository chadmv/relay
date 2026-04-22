package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCORS_EmptyAllowlistEmitsNoHeaders(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	h := CORS(nil)(next)

	req := httptest.NewRequest("GET", "/v1/health", nil)
	req.Header.Set("Origin", "https://example.com")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("expected no Allow-Origin header, got %q", got)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
}


// Regression: empty allowlist must not intercept OPTIONS preflight requests
// — the underlying handler should see them so it can return 405 as it did
// before CORS was wired in.
func TestCORS_EmptyAllowlistPassesPreflightThrough(t *testing.T) {
	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusMethodNotAllowed)
	})
	h := CORS(nil)(next)

	req := httptest.NewRequest("OPTIONS", "/v1/health", nil)
	req.Header.Set("Origin", "https://example.com")
	req.Header.Set("Access-Control-Request-Method", "GET")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if !called {
		t.Fatal("expected next handler to be called")
	}
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405 from underlying handler, got %d", rec.Code)
	}
}


func TestCORS_AllowlistedOriginReceivesHeader(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	h := CORS([]string{"https://dashboard.studio.local"})(next)

	req := httptest.NewRequest("GET", "/v1/health", nil)
	req.Header.Set("Origin", "https://dashboard.studio.local")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "https://dashboard.studio.local" {
		t.Fatalf("expected echoed Allow-Origin, got %q", got)
	}
}

func TestCORS_NonAllowlistedOriginGetsNoHeader(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	h := CORS([]string{"https://dashboard.studio.local"})(next)

	req := httptest.NewRequest("GET", "/v1/health", nil)
	req.Header.Set("Origin", "https://evil.example.com")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("expected no Allow-Origin header, got %q", got)
	}
}

func TestCORS_PreflightReturnsFullHeaders(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("next handler should not be called for preflight")
	})
	h := CORS([]string{"https://dashboard.studio.local"})(next)

	req := httptest.NewRequest("OPTIONS", "/v1/jobs", nil)
	req.Header.Set("Origin", "https://dashboard.studio.local")
	req.Header.Set("Access-Control-Request-Method", "POST")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", rec.Code)
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "https://dashboard.studio.local" {
		t.Fatalf("expected Allow-Origin, got %q", got)
	}
	if got := rec.Header().Get("Access-Control-Allow-Methods"); got == "" {
		t.Fatalf("expected Allow-Methods header")
	}
	if got := rec.Header().Get("Access-Control-Allow-Headers"); got == "" {
		t.Fatalf("expected Allow-Headers header")
	}
	if got := rec.Header().Get("Access-Control-Max-Age"); got != "600" {
		t.Fatalf("expected Max-Age=600, got %q", got)
	}
}

func TestCORS_PreflightNonAllowlistedOrigin(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("next handler should not be called for preflight")
	})
	h := CORS([]string{"https://dashboard.studio.local"})(next)

	req := httptest.NewRequest("OPTIONS", "/v1/jobs", nil)
	req.Header.Set("Origin", "https://evil.example.com")
	req.Header.Set("Access-Control-Request-Method", "POST")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", rec.Code)
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("expected no Allow-Origin, got %q", got)
	}
}

func TestCORS_NeverEmitsAllowCredentials(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	h := CORS([]string{"https://dashboard.studio.local"})(next)

	req := httptest.NewRequest("GET", "/v1/health", nil)
	req.Header.Set("Origin", "https://dashboard.studio.local")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if got := rec.Header().Get("Access-Control-Allow-Credentials"); got != "" {
		t.Fatalf("expected no Allow-Credentials header, got %q", got)
	}
}

func TestParseCORSOrigins(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    []string
		wantErr bool
	}{
		{"empty", "", nil, false},
		{"single", "https://a.example.com", []string{"https://a.example.com"}, false},
		{"multiple", "https://a.example.com,https://b.example.com", []string{"https://a.example.com", "https://b.example.com"}, false},
		{"whitespace trimmed", " https://a.example.com , https://b.example.com ", []string{"https://a.example.com", "https://b.example.com"}, false},
		{"dedup", "https://a.example.com,https://a.example.com", []string{"https://a.example.com"}, false},
		{"empty entries ignored", "https://a.example.com,,https://b.example.com", []string{"https://a.example.com", "https://b.example.com"}, false},
		{"wildcard rejected", "*", nil, true},
		{"wildcard with others rejected", "https://a.example.com,*", nil, true},
		{"non-http scheme rejected", "ftp://a.example.com", nil, true},
		{"no scheme rejected", "example.com", nil, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseCORSOrigins(tt.input)
			if (err != nil) != tt.wantErr {
				t.Fatalf("wantErr=%v got err=%v", tt.wantErr, err)
			}
			if err != nil {
				return
			}
			if len(got) != len(tt.want) {
				t.Fatalf("len mismatch: got %v want %v", got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Fatalf("[%d]: got %q want %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}
