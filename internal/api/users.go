package api

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"relay/internal/store"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// userResponse is the public shape returned by GET /v1/users and the PATCH
// endpoints. Defined as a private struct (not the store row) to guarantee the
// password hash never leaks even if a store row type changes.
type userResponse struct {
	ID        string    `json:"id"`
	Email     string    `json:"email"`
	Name      string    `json:"name"`
	IsAdmin   bool      `json:"is_admin"`
	CreatedAt time.Time `json:"created_at"`
}

func toUserResponse(id pgtype.UUID, email, name string, isAdmin bool, createdAt pgtype.Timestamptz) userResponse {
	return userResponse{
		ID:        uuidStr(id),
		Email:     email,
		Name:      name,
		IsAdmin:   isAdmin,
		CreatedAt: createdAt.Time,
	}
}

// updateUserRequest is the request body for PATCH /v1/users/me and
// PATCH /v1/users/{id}.
type updateUserRequest struct {
	Name string `json:"name"`
}

// parseUpdateUserRequest reads and validates the JSON body. On failure it
// writes the appropriate error response and returns ok=false. On success it
// returns the trimmed name.
func parseUpdateUserRequest(w http.ResponseWriter, r *http.Request) (string, bool) {
	var req updateUserRequest
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return "", false
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return "", false
	}
	return name, true
}

func (s *Server) handleListUsers(w http.ResponseWriter, r *http.Request) {
	if email := r.URL.Query().Get("email"); email != "" {
		u, err := s.q.GetUserByEmailPublic(r.Context(), email)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				writeJSON(w, http.StatusOK, []userResponse{})
				return
			}
			writeError(w, http.StatusInternalServerError, "failed to look up user")
			return
		}
		writeJSON(w, http.StatusOK, []userResponse{
			toUserResponse(u.ID, u.Email, u.Name, u.IsAdmin, u.CreatedAt),
		})
		return
	}

	rows, err := s.q.ListUsers(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list users")
		return
	}
	out := make([]userResponse, 0, len(rows))
	for _, row := range rows {
		out = append(out, toUserResponse(row.ID, row.Email, row.Name, row.IsAdmin, row.CreatedAt))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleUpdateMe(w http.ResponseWriter, r *http.Request) {
	authUser, _ := UserFromCtx(r.Context())

	name, ok := parseUpdateUserRequest(w, r)
	if !ok {
		return
	}

	row, err := s.q.UpdateUserName(r.Context(), store.UpdateUserNameParams{
		ID:   authUser.ID,
		Name: name,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update user")
		return
	}
	writeJSON(w, http.StatusOK, toUserResponse(row.ID, row.Email, row.Name, row.IsAdmin, row.CreatedAt))
}
