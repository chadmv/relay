package api

import (
	"net/http"

	"relay/internal/store"

	"github.com/jackc/pgx/v5/pgtype"
)

// handleListTokens lives here rather than in auth.go because the house layout
// is one file per resource (invites.go, reservations.go, workers.go) and
// /v1/auth/tokens is a resource. auth.go already carries register, login,
// password change and both logout paths across 350+ lines; it is not otherwise
// refactored by this change.

// TokensSortSpec is the ?sort= allowlist for GET /v1/auth/tokens. parseSort
// strips a leading '-' before checking this map (pagination.go:178-181), so
// created_at is reachable in BOTH directions and both arms exist below. A key
// added here without a dispatch arm reaches the default: panic and 500s on
// ordinary user input.
//
// expires_at is deliberately absent: the column is nullable and would need a
// NULLS-ordered index pair plus cursor-null handling for a single-digit list.
var TokensSortSpec = SortSpec{
	Default: "-created_at",
	Keys: map[string]SortKeyKind{
		"created_at": SortKeyTimestamp,
	},
}

// tokenEntry builds one item of the GET /v1/auth/tokens response.
//
// is_current is a pgtype.UUID comparison against the token id that BearerAuth
// already resolved from the presented credential (middleware.go:25-42).
// NOTHING HERE HASHES ANYTHING: the query does not select token_hash, so the
// handler never holds one, and adding a tokenhash.Hash call to this file would
// be a design regression rather than a detail. TokenID is resolved server-side
// and never read from the wire; both sides of the comparison carry Valid:true,
// so a zero value would fail closed (no row marked current) rather than marking
// an arbitrary row.
//
// is_current is ALWAYS present, never omitted: "this row is not your current
// session" is a positive fact the UI must be able to state.
//
// expires_at is OMITTED when the column is NULL. NULL means the token never
// expires - BearerAuth only rejects on `Valid && Before(now)` (middleware.go:32-35) -
// and the consuming tab renders the absence as "never", not as the "-"
// placeholder it uses for missing optional strings. A non-expiring credential
// is a security fact, not missing data.
func tokenEntry(id pgtype.UUID, createdAt, expiresAt pgtype.Timestamptz, currentTokenID pgtype.UUID) map[string]any {
	entry := map[string]any{
		"id":         uuidStr(id),
		"created_at": createdAt.Time,
		"is_current": id == currentTokenID,
	}
	if expiresAt.Valid {
		entry["expires_at"] = expiresAt.Time
	}
	return entry
}

func (s *Server) handleListTokens(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	authUser, ok := UserFromCtx(ctx)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	pp, ok := parsePage(w, r, TokensSortSpec)
	if !ok {
		return
	}

	var items []map[string]any
	var next string

	switch pp.Sort {
	case "-created_at":
		rows, err := s.q.ListActiveTokensForUserPage(ctx, store.ListActiveTokensForUserPageParams{
			UserID:    authUser.ID,
			CursorSet: pp.Cursor.Set,
			CursorTs:  pp.CursorTs(),
			CursorID:  pp.Cursor.ID,
			PageLimit: pp.Limit,
		})
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to list tokens")
			return
		}
		items, next = buildPage(rows, pp.Limit, pp.Sort,
			func(row store.ListActiveTokensForUserPageRow) map[string]any {
				return tokenEntry(row.ID, row.CreatedAt, row.ExpiresAt, authUser.TokenID)
			},
			func(row store.ListActiveTokensForUserPageRow) (anySortVal, pgtype.UUID) {
				return row.CreatedAt.Time, row.ID
			})

	case "created_at":
		rows, err := s.q.ListActiveTokensForUserPageByCreatedAsc(ctx, store.ListActiveTokensForUserPageByCreatedAscParams{
			UserID:    authUser.ID,
			CursorSet: pp.Cursor.Set,
			CursorTs:  pp.CursorTs(),
			CursorID:  pp.Cursor.ID,
			PageLimit: pp.Limit,
		})
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to list tokens")
			return
		}
		items, next = buildPage(rows, pp.Limit, pp.Sort,
			func(row store.ListActiveTokensForUserPageByCreatedAscRow) map[string]any {
				return tokenEntry(row.ID, row.CreatedAt, row.ExpiresAt, authUser.TokenID)
			},
			func(row store.ListActiveTokensForUserPageByCreatedAscRow) (anySortVal, pgtype.UUID) {
				return row.CreatedAt.Time, row.ID
			})

	default:
		panic("handleListTokens: missing dispatch arm for sort key " + pp.Sort)
	}

	total, err := s.q.CountActiveTokensForUser(ctx, authUser.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to count tokens")
		return
	}
	writeJSON(w, http.StatusOK, page[map[string]any]{Items: items, NextCursor: next, Total: total})
}
