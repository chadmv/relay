package api

import "net/http"

// handleConfig exposes server configuration the web UI needs before
// authentication. Public — must not require a bearer token.
func (s *Server) handleConfig(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]bool{
		"allow_self_register": s.AllowSelfRegister,
	})
}
