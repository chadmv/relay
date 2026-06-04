package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestServer_StaticHandlerServesNonAPIPaths(t *testing.T) {
	static := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("INDEX"))
	})
	s := &Server{StaticHandler: static}
	h := s.Handler()

	req := httptest.NewRequest("GET", "/jobs", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK || rec.Body.String() != "INDEX" {
		t.Fatalf("static path: code=%d body=%q, want 200 INDEX", rec.Code, rec.Body.String())
	}
}

func TestServer_APIRoutesWinOverStatic(t *testing.T) {
	static := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("INDEX"))
	})
	s := &Server{StaticHandler: static}
	h := s.Handler()

	req := httptest.NewRequest("GET", "/v1/health", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Body.String() == "INDEX" {
		t.Fatalf("/v1/health should hit the API handler, not the static handler")
	}
}
