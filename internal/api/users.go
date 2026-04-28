package api

import (
	"errors"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"
)

// userResponse is the public shape returned by GET /v1/users. Defined as a
// private struct (not the store row) to guarantee the password hash never
// leaks even if the store row type changes.
type userResponse struct {
	ID        string    `json:"id"`
	Email     string    `json:"email"`
	Name      string    `json:"name"`
	IsAdmin   bool      `json:"is_admin"`
	CreatedAt time.Time `json:"created_at"`
}

func (s *Server) handleListUsers(w http.ResponseWriter, r *http.Request) {
	if email := r.URL.Query().Get("email"); email != "" {
		u, err := s.q.GetUserByEmail(r.Context(), email)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				writeJSON(w, http.StatusOK, []userResponse{})
				return
			}
			writeError(w, http.StatusInternalServerError, "failed to look up user")
			return
		}
		writeJSON(w, http.StatusOK, []userResponse{{
			ID:        uuidStr(u.ID),
			Email:     u.Email,
			Name:      u.Name,
			IsAdmin:   u.IsAdmin,
			CreatedAt: u.CreatedAt.Time,
		}})
		return
	}

	rows, err := s.q.ListUsers(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list users")
		return
	}
	out := make([]userResponse, 0, len(rows))
	for _, row := range rows {
		out = append(out, userResponse{
			ID:        uuidStr(row.ID),
			Email:     row.Email,
			Name:      row.Name,
			IsAdmin:   row.IsAdmin,
			CreatedAt: row.CreatedAt.Time,
		})
	}
	writeJSON(w, http.StatusOK, out)
}
