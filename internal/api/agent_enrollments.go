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

const (
	defaultEnrollmentTTL = 24 * time.Hour
	minEnrollmentTTL     = time.Minute
	maxEnrollmentTTL     = 7 * 24 * time.Hour
)

func (s *Server) handleCreateAgentEnrollment(w http.ResponseWriter, r *http.Request) {
	u, ok := UserFromCtx(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	var req struct {
		HostnameHint string `json:"hostname_hint"`
		TTLSeconds   int64  `json:"ttl_seconds"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	ttl := defaultEnrollmentTTL
	if req.TTLSeconds != 0 {
		ttl = time.Duration(req.TTLSeconds) * time.Second
	}
	if ttl < minEnrollmentTTL {
		writeError(w, http.StatusBadRequest, "ttl_seconds must be at least 60")
		return
	}
	if ttl > maxEnrollmentTTL {
		writeError(w, http.StatusBadRequest, "ttl_seconds must not exceed 604800 (7 days)")
		return
	}

	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to generate token")
		return
	}
	rawHex := hex.EncodeToString(raw)
	sum := sha256.Sum256([]byte(rawHex))
	hash := hex.EncodeToString(sum[:])

	params := store.CreateAgentEnrollmentParams{
		TokenHash: hash,
		CreatedBy: u.ID,
		ExpiresAt: pgtype.Timestamptz{Time: time.Now().Add(ttl), Valid: true},
	}
	if req.HostnameHint != "" {
		params.HostnameHint = &req.HostnameHint
	}

	row, err := s.q.CreateAgentEnrollment(r.Context(), params)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create enrollment")
		return
	}

	writeJSON(w, http.StatusCreated, map[string]any{
		"id":         uuidStr(row.ID),
		"token":      rawHex,
		"expires_at": row.ExpiresAt.Time,
	})
}

func (s *Server) handleListAgentEnrollments(w http.ResponseWriter, r *http.Request) {
	rows, err := s.q.ListActiveAgentEnrollments(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list enrollments")
		return
	}
	out := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		entry := map[string]any{
			"id":         uuidStr(row.ID),
			"created_at": row.CreatedAt.Time,
			"expires_at": row.ExpiresAt.Time,
			"created_by": uuidStr(row.CreatedBy),
		}
		if row.HostnameHint != nil {
			entry["hostname_hint"] = *row.HostnameHint
		}
		out = append(out, entry)
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleDeleteWorkerToken(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUID(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid worker id")
		return
	}
	if err := s.q.ClearWorkerAgentToken(r.Context(), id); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to clear worker token")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
