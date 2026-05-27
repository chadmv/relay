package api

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"time"

	"relay/internal/store"
	"relay/internal/tokenhash"

	"github.com/jackc/pgx/v5/pgtype"
)

const (
	defaultEnrollmentTTL = 24 * time.Hour
	minEnrollmentTTL     = time.Minute
	maxEnrollmentTTL     = 7 * 24 * time.Hour
)

func (s *Server) handleCreateAgentEnrollment(w http.ResponseWriter, r *http.Request) {
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
		writeError(w, http.StatusBadRequest, fmt.Sprintf("ttl_seconds must not exceed %d (7 days)", int(maxEnrollmentTTL.Seconds())))
		return
	}

	u, _ := UserFromCtx(r.Context())
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to generate token")
		return
	}
	rawHex := hex.EncodeToString(raw)
	hash := tokenhash.Hash(rawHex)

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

var agentEnrollmentsSortSpec = sortSpec{
	Default: "-created_at",
	Keys: map[string]sortKeyKind{
		"created_at": sortKeyTimestamp,
		"expires_at": sortKeyTimestamp,
	},
}

func enrollmentRowToMap(row store.ListActiveAgentEnrollmentsPageRow) map[string]any {
	entry := map[string]any{
		"id":         uuidStr(row.ID),
		"created_at": row.CreatedAt.Time,
		"expires_at": row.ExpiresAt.Time,
		"created_by": uuidStr(row.CreatedBy),
	}
	if row.HostnameHint != nil {
		entry["hostname_hint"] = *row.HostnameHint
	}
	return entry
}

func enrollmentRowKey(row store.ListActiveAgentEnrollmentsPageRow) (anySortVal, pgtype.UUID) {
	return row.CreatedAt.Time, row.ID
}

func enrollmentByCreatedAscRowToMap(row store.ListActiveAgentEnrollmentsPageByCreatedAscRow) map[string]any {
	entry := map[string]any{
		"id":         uuidStr(row.ID),
		"created_at": row.CreatedAt.Time,
		"expires_at": row.ExpiresAt.Time,
		"created_by": uuidStr(row.CreatedBy),
	}
	if row.HostnameHint != nil {
		entry["hostname_hint"] = *row.HostnameHint
	}
	return entry
}

func enrollmentByCreatedAscRowKey(row store.ListActiveAgentEnrollmentsPageByCreatedAscRow) (anySortVal, pgtype.UUID) {
	return row.CreatedAt.Time, row.ID
}

func enrollmentByExpiresDescRowToMap(row store.ListActiveAgentEnrollmentsPageByExpiresDescRow) map[string]any {
	entry := map[string]any{
		"id":         uuidStr(row.ID),
		"created_at": row.CreatedAt.Time,
		"expires_at": row.ExpiresAt.Time,
		"created_by": uuidStr(row.CreatedBy),
	}
	if row.HostnameHint != nil {
		entry["hostname_hint"] = *row.HostnameHint
	}
	return entry
}

func enrollmentByExpiresDescRowKey(row store.ListActiveAgentEnrollmentsPageByExpiresDescRow) (anySortVal, pgtype.UUID) {
	return row.ExpiresAt.Time, row.ID
}

func enrollmentByExpiresAscRowToMap(row store.ListActiveAgentEnrollmentsPageByExpiresAscRow) map[string]any {
	entry := map[string]any{
		"id":         uuidStr(row.ID),
		"created_at": row.CreatedAt.Time,
		"expires_at": row.ExpiresAt.Time,
		"created_by": uuidStr(row.CreatedBy),
	}
	if row.HostnameHint != nil {
		entry["hostname_hint"] = *row.HostnameHint
	}
	return entry
}

func enrollmentByExpiresAscRowKey(row store.ListActiveAgentEnrollmentsPageByExpiresAscRow) (anySortVal, pgtype.UUID) {
	return row.ExpiresAt.Time, row.ID
}

func (s *Server) handleListAgentEnrollments(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	pp, ok := parsePage(w, r, agentEnrollmentsSortSpec)
	if !ok {
		return
	}

	var items []map[string]any
	var next string

	switch pp.Sort {
	case "-created_at":
		rows, err := s.q.ListActiveAgentEnrollmentsPage(ctx, store.ListActiveAgentEnrollmentsPageParams{
			CursorSet: pp.Cursor.Set,
			CursorTs:  pp.CursorTs(),
			CursorID:  pp.Cursor.ID,
			PageLimit: pp.Limit,
		})
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to list enrollments")
			return
		}
		items, next = buildPage(rows, pp.Limit, pp.Sort, enrollmentRowToMap, enrollmentRowKey)

	case "created_at":
		rows, err := s.q.ListActiveAgentEnrollmentsPageByCreatedAsc(ctx, store.ListActiveAgentEnrollmentsPageByCreatedAscParams{
			CursorSet: pp.Cursor.Set,
			CursorTs:  pp.CursorTs(),
			CursorID:  pp.Cursor.ID,
			PageLimit: pp.Limit,
		})
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to list enrollments")
			return
		}
		items, next = buildPage(rows, pp.Limit, pp.Sort, enrollmentByCreatedAscRowToMap, enrollmentByCreatedAscRowKey)

	case "-expires_at":
		rows, err := s.q.ListActiveAgentEnrollmentsPageByExpiresDesc(ctx, store.ListActiveAgentEnrollmentsPageByExpiresDescParams{
			CursorSet: pp.Cursor.Set,
			CursorTs:  pp.CursorTs(),
			CursorID:  pp.Cursor.ID,
			PageLimit: pp.Limit,
		})
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to list enrollments")
			return
		}
		items, next = buildPage(rows, pp.Limit, pp.Sort, enrollmentByExpiresDescRowToMap, enrollmentByExpiresDescRowKey)

	case "expires_at":
		rows, err := s.q.ListActiveAgentEnrollmentsPageByExpiresAsc(ctx, store.ListActiveAgentEnrollmentsPageByExpiresAscParams{
			CursorSet: pp.Cursor.Set,
			CursorTs:  pp.CursorTs(),
			CursorID:  pp.Cursor.ID,
			PageLimit: pp.Limit,
		})
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to list enrollments")
			return
		}
		items, next = buildPage(rows, pp.Limit, pp.Sort, enrollmentByExpiresAscRowToMap, enrollmentByExpiresAscRowKey)

	default:
		panic("handleListAgentEnrollments: missing dispatch arm for sort key " + pp.Sort)
	}

	total, err := s.q.CountActiveAgentEnrollments(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to count enrollments")
		return
	}
	writeJSON(w, http.StatusOK, page[map[string]any]{Items: items, NextCursor: next, Total: total})
}

func (s *Server) handleDeleteWorkerToken(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUID(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid worker id")
		return
	}
	n, err := s.q.ClearWorkerAgentToken(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to clear worker token")
		return
	}
	if n == 0 {
		writeError(w, http.StatusNotFound, "worker not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
