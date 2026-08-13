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
	if !readJSON(w, r, &req) {
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

// InvitesSortSpec is the ?sort= allowlist for GET /v1/invites. parseSort strips
// a leading '-' before checking this map (pagination.go:178-181), so EVERY key
// here is reachable in BOTH directions and each direction needs its own
// dispatch arm in handleListInvites. A key without an arm reaches the default:
// panic below, which net/http recovers per connection as a 500 plus a dropped
// connection - remotely triggerable by any authenticated admin. If you add a
// key here, add two arms and two queries in the same change.
var InvitesSortSpec = SortSpec{
	Default: "-created_at",
	Keys: map[string]SortKeyKind{
		"created_at": SortKeyTimestamp,
	},
}

// inviteEntry builds one item of the GET /v1/invites response.
//
// It takes loose columns rather than a row struct because sqlc emits one
// structurally identical row type per sort arm, and a single function shared by
// all of them keeps the response shape defined in exactly one place. (The
// enrollments handler duplicates its body four times; do not copy that.)
//
// token_hash is not a parameter and must never become one. No status field is
// returned either: the client derives ACTIVE/EXPIRING/EXPIRED/REDEEMED from
// expires_at and used_at, exactly as web/src/admin/enrollments/enrollmentStatus.ts:7-26
// already does, because a server-asserted "expired" is stale the moment the row
// is on screen and "expiring" needs an invented threshold.
//
// Optional keys are OMITTED, never nulled: an absent email means the invite is
// not bound to an address, and an absent used_at means it has not been redeemed.
func inviteEntry(
	id pgtype.UUID,
	email *string,
	createdBy pgtype.UUID,
	createdByEmail string,
	createdAt, expiresAt, usedAt pgtype.Timestamptz,
) map[string]any {
	entry := map[string]any{
		"id":               uuidStr(id),
		"created_at":       createdAt.Time,
		"expires_at":       expiresAt.Time,
		"created_by":       uuidStr(createdBy),
		"created_by_email": createdByEmail,
	}
	if email != nil {
		entry["email"] = *email
	}
	if usedAt.Valid {
		entry["used_at"] = usedAt.Time
	}
	return entry
}

func (s *Server) handleListInvites(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	pp, ok := parsePage(w, r, InvitesSortSpec)
	if !ok {
		return
	}

	var items []map[string]any
	var next string

	switch pp.Sort {
	case "-created_at":
		rows, err := s.q.ListInvitesPage(ctx, store.ListInvitesPageParams{
			CursorSet: pp.Cursor.Set,
			CursorTs:  pp.CursorTs(),
			CursorID:  pp.Cursor.ID,
			PageLimit: pp.Limit,
		})
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to list invites")
			return
		}
		items, next = buildPage(rows, pp.Limit, pp.Sort,
			func(row store.ListInvitesPageRow) map[string]any {
				return inviteEntry(row.ID, row.Email, row.CreatedBy, row.CreatedByEmail,
					row.CreatedAt, row.ExpiresAt, row.UsedAt)
			},
			func(row store.ListInvitesPageRow) (anySortVal, pgtype.UUID) {
				return row.CreatedAt.Time, row.ID
			})

	case "created_at":
		rows, err := s.q.ListInvitesPageByCreatedAsc(ctx, store.ListInvitesPageByCreatedAscParams{
			CursorSet: pp.Cursor.Set,
			CursorTs:  pp.CursorTs(),
			CursorID:  pp.Cursor.ID,
			PageLimit: pp.Limit,
		})
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to list invites")
			return
		}
		items, next = buildPage(rows, pp.Limit, pp.Sort,
			func(row store.ListInvitesPageByCreatedAscRow) map[string]any {
				return inviteEntry(row.ID, row.Email, row.CreatedBy, row.CreatedByEmail,
					row.CreatedAt, row.ExpiresAt, row.UsedAt)
			},
			func(row store.ListInvitesPageByCreatedAscRow) (anySortVal, pgtype.UUID) {
				return row.CreatedAt.Time, row.ID
			})

	default:
		panic("handleListInvites: missing dispatch arm for sort key " + pp.Sort)
	}

	total, err := s.q.CountInvites(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to count invites")
		return
	}
	writeJSON(w, http.StatusOK, page[map[string]any]{Items: items, NextCursor: next, Total: total})
}
