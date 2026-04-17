package api

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strings"
	"time"

	"relay/internal/store"

	"github.com/jackc/pgx/v5/pgtype"
)

func (s *Server) handleCreateToken(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Email       string `json:"email"`
		Name        string `json:"name"`
		InviteToken string `json:"invite_token"`
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

	user, err := s.q.GetUserByEmail(ctx, req.Email)
	if err != nil {
		// Unknown user — require a valid invite token.
		if req.InviteToken == "" {
			writeError(w, http.StatusForbidden, "invite required")
			return
		}

		sum := sha256.Sum256([]byte(req.InviteToken))
		hash := hex.EncodeToString(sum[:])

		invite, err := s.q.GetInviteByTokenHash(ctx, hash)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid invite token")
			return
		}
		if invite.UsedAt.Valid {
			writeError(w, http.StatusBadRequest, "invite already used")
			return
		}
		if time.Now().After(invite.ExpiresAt.Time) {
			writeError(w, http.StatusBadRequest, "invite expired")
			return
		}
		if invite.Email != nil && !strings.EqualFold(*invite.Email, req.Email) {
			writeError(w, http.StatusBadRequest, "invite not valid for this email")
			return
		}

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

		// Mark invite used atomically (WHERE used_at IS NULL prevents double-redemption).
		rows, err := s.q.MarkInviteUsed(ctx, store.MarkInviteUsedParams{
			ID:     invite.ID,
			UsedBy: user.ID,
		})
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to redeem invite")
			return
		}
		if rows == 0 {
			writeError(w, http.StatusBadRequest, "invite already used")
			return
		}
	}

	// Generate a random 32-byte API token.
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
