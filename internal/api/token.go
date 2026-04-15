package api

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"time"

	"relay/internal/store"

	"github.com/jackc/pgx/v5/pgtype"
)

func (s *Server) handleCreateToken(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Email string `json:"email"`
		Name  string `json:"name"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Email == "" {
		writeError(w, http.StatusBadRequest, "email is required")
		return
	}

	ctx := r.Context()

	// Find or create user.
	user, err := s.q.GetUserByEmail(ctx, req.Email)
	if err != nil {
		name := req.Name
		if name == "" {
			name = req.Email
		}
		user, err = s.q.CreateUser(ctx, store.CreateUserParams{
			Name:    name,
			Email:   req.Email,
			IsAdmin: false,
		})
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to create user")
			return
		}
	}

	// Generate a random 32-byte token.
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to generate token")
		return
	}
	rawHex := hex.EncodeToString(raw)
	sum := sha256.Sum256([]byte(rawHex))
	hash := hex.EncodeToString(sum[:])

	expires := pgtype.Timestamptz{Time: time.Now().Add(30 * 24 * time.Hour), Valid: true}
	if _, err := s.q.CreateToken(ctx, store.CreateTokenParams{
		UserID:    user.ID,
		TokenHash: hash,
		ExpiresAt: expires,
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create token")
		return
	}

	writeJSON(w, http.StatusCreated, map[string]any{
		"token":      rawHex,
		"expires_at": expires.Time,
	})
}
