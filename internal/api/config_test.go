package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHandleConfig_ReportsSelfRegister(t *testing.T) {
	cases := []struct {
		name  string
		allow bool
	}{
		{"enabled", true},
		{"disabled", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := &Server{AllowSelfRegister: tc.allow}
			h := s.Handler()

			req := httptest.NewRequest("GET", "/v1/config", nil)
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200", rec.Code)
			}
			var body struct {
				AllowSelfRegister bool `json:"allow_self_register"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if body.AllowSelfRegister != tc.allow {
				t.Fatalf("allow_self_register = %v, want %v", body.AllowSelfRegister, tc.allow)
			}
		})
	}
}

func TestHandleConfig_IsPublic(t *testing.T) {
	s := &Server{}
	h := s.Handler()
	req := httptest.NewRequest("GET", "/v1/config", nil) // no Authorization header
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code == http.StatusUnauthorized {
		t.Fatalf("config must be public, got 401")
	}
}
