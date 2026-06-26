package mcp

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// whoamiHandler returns an http.HandlerFunc that answers GET /v1/users/me with a
// minimal identity payload (is_admin set per the argument) and delegates every
// other request to next. Backed mcp tests wrap their per-tool handler with this so
// the NewServer startup whoami probe succeeds without tripping single-path asserts.
func whoamiHandler(isAdmin bool, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/users/me" {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id": "u1", "email": "t@t", "name": "T", "is_admin": isAdmin,
			})
			return
		}
		next(w, r)
	}
}

// newWhoamiBackend stands up an httptest.Server that only answers /v1/users/me.
// Backendless validation tests point NewServer at it so construction succeeds and
// the client-side validation path is exercised as before. Closed via t.Cleanup.
func newWhoamiBackend(t *testing.T, isAdmin bool) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(whoamiHandler(isAdmin, func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "unexpected path: "+r.URL.Path, http.StatusNotFound)
	}))
	t.Cleanup(srv.Close)
	return srv
}
