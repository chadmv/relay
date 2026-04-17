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
			writeError(w, http.StatusBadRequest, "invalid expires_in: use a Go duration like '72h' or '24h'")
			return
		}
	}

	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to generate token")
		return
	}
	rawHex := hex.EncodeToString(raw)
	sum := sha256.Sum256([]byte(rawHex))
	hash := hex.EncodeToString(sum[:])

	expiresAt := pgtype.Timestamptz{Time: time.Now().Add(dur), Valid: true}

	params := store.CreateInviteParams{
		TokenHash: hash,
		CreatedBy: u.ID,
		ExpiresAt: expiresAt,
	}
	if req.Email != "" {
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
