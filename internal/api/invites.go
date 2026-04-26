package api

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"net/mail"
	"time"

	"relay/internal/store"
	"relay/internal/tokenhash"

	"github.com/jackc/pgx/v5/pgtype"
)

func (s *Server) handleCreateInvite(w http.ResponseWriter, r *http.Request) {
	u, ok := UserFromCtx(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	var req struct {
		Email     string `json:"email"`
		ExpiresIn string `json:"expires_in"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	dur := 72 * time.Hour
	if req.ExpiresIn != "" {
		var err error
		dur, err = time.ParseDuration(req.ExpiresIn)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid expires_in: expected a duration string such as '24h' or '72h'")
			return
		}
		if dur <= 0 {
			writeError(w, http.StatusBadRequest, "expires_in must be positive")
			return
		}
		const maxInviteDuration = 30 * 24 * time.Hour
		if dur > maxInviteDuration {
			writeError(w, http.StatusBadRequest, "expires_in exceeds maximum of 720h")
			return
		}
	}

	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to generate token")
		return
	}
	rawHex := hex.EncodeToString(raw)
	hash := tokenhash.Hash(rawHex)

	expiresAt := pgtype.Timestamptz{Time: time.Now().Add(dur), Valid: true}

	params := store.CreateInviteParams{
		TokenHash: hash,
		CreatedBy: u.ID,
		ExpiresAt: expiresAt,
	}
	if req.Email != "" {
		if _, err := mail.ParseAddress(req.Email); err != nil {
			writeError(w, http.StatusBadRequest, "invalid email address")
			return
		}
		params.Email = &req.Email
	}

	invite, err := s.q.CreateInvite(r.Context(), params)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create invite")
		return
	}

	resp := map[string]any{
		"id":         uuidStr(invite.ID),
		"token":      rawHex,
		"expires_at": invite.ExpiresAt.Time,
	}
	if invite.Email != nil {
		resp["email"] = *invite.Email
	}
	writeJSON(w, http.StatusCreated, resp)
}
