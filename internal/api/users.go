package api

import (
	"context"
	"errors"
	"net/http"
	"net/mail"
	"strings"
	"time"

	"relay/internal/store"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"golang.org/x/crypto/bcrypt"
)

// userResponse is the public shape returned by GET /v1/users and the PATCH
// endpoints. Defined as a private struct (not the store row) to guarantee the
// password hash never leaks even if a store row type changes.
type userResponse struct {
	ID         string     `json:"id"`
	Email      string     `json:"email"`
	Name       string     `json:"name"`
	IsAdmin    bool       `json:"is_admin"`
	CreatedAt  time.Time  `json:"created_at"`
	ArchivedAt *time.Time `json:"archived_at"`
}

func toUserResponse(id pgtype.UUID, email, name string, isAdmin bool, createdAt, archivedAt pgtype.Timestamptz) userResponse {
	var arch *time.Time
	if archivedAt.Valid {
		t := archivedAt.Time
		arch = &t
	}
	return userResponse{
		ID:         uuidStr(id),
		Email:      email,
		Name:       name,
		IsAdmin:    isAdmin,
		CreatedAt:  createdAt.Time,
		ArchivedAt: arch,
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
	if !readJSON(w, r, &req) {
		return "", false
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return "", false
	}
	return name, true
}

var UsersSortSpec = SortSpec{
	Default: "-created_at",
	Keys: map[string]SortKeyKind{
		"created_at": SortKeyTimestamp,
		"name":       SortKeyText,
		"email":      SortKeyText,
	},
}

func usersListRowKey(r store.ListUsersPageRow) (anySortVal, pgtype.UUID) {
	return r.CreatedAt.Time, r.ID
}
func usersListIncArchivedRowKey(r store.ListUsersIncludingArchivedPageRow) (anySortVal, pgtype.UUID) {
	return r.CreatedAt.Time, r.ID
}

func usersListRowKeyByName(r store.ListUsersPageByNameDescRow) (anySortVal, pgtype.UUID) {
	return r.Name, r.ID
}
func usersListRowKeyByNameAsc(r store.ListUsersPageByNameAscRow) (anySortVal, pgtype.UUID) {
	return r.Name, r.ID
}
func usersListRowKeyByEmail(r store.ListUsersPageByEmailDescRow) (anySortVal, pgtype.UUID) {
	return r.Email, r.ID
}
func usersListRowKeyByEmailAsc(r store.ListUsersPageByEmailAscRow) (anySortVal, pgtype.UUID) {
	return r.Email, r.ID
}

func usersListIncArchivedRowKeyByName(r store.ListUsersIncludingArchivedPageByNameDescRow) (anySortVal, pgtype.UUID) {
	return r.Name, r.ID
}
func usersListIncArchivedRowKeyByNameAsc(r store.ListUsersIncludingArchivedPageByNameAscRow) (anySortVal, pgtype.UUID) {
	return r.Name, r.ID
}
func usersListIncArchivedRowKeyByEmail(r store.ListUsersIncludingArchivedPageByEmailDescRow) (anySortVal, pgtype.UUID) {
	return r.Email, r.ID
}
func usersListIncArchivedRowKeyByEmailAsc(r store.ListUsersIncludingArchivedPageByEmailAscRow) (anySortVal, pgtype.UUID) {
	return r.Email, r.ID
}

func usersListRowToResponse(r store.ListUsersPageRow) userResponse {
	return toUserResponse(r.ID, r.Email, r.Name, r.IsAdmin, r.CreatedAt, pgtype.Timestamptz{})
}
func usersListIncArchivedRowToResponse(r store.ListUsersIncludingArchivedPageRow) userResponse {
	return toUserResponse(r.ID, r.Email, r.Name, r.IsAdmin, r.CreatedAt, r.ArchivedAt)
}

func usersListRowByCreatedAscToResponse(r store.ListUsersPageByCreatedAscRow) userResponse {
	return toUserResponse(r.ID, r.Email, r.Name, r.IsAdmin, r.CreatedAt, pgtype.Timestamptz{})
}
func usersListRowByNameDescToResponse(r store.ListUsersPageByNameDescRow) userResponse {
	return toUserResponse(r.ID, r.Email, r.Name, r.IsAdmin, r.CreatedAt, pgtype.Timestamptz{})
}
func usersListRowByNameAscToResponse(r store.ListUsersPageByNameAscRow) userResponse {
	return toUserResponse(r.ID, r.Email, r.Name, r.IsAdmin, r.CreatedAt, pgtype.Timestamptz{})
}
func usersListRowByEmailDescToResponse(r store.ListUsersPageByEmailDescRow) userResponse {
	return toUserResponse(r.ID, r.Email, r.Name, r.IsAdmin, r.CreatedAt, pgtype.Timestamptz{})
}
func usersListRowByEmailAscToResponse(r store.ListUsersPageByEmailAscRow) userResponse {
	return toUserResponse(r.ID, r.Email, r.Name, r.IsAdmin, r.CreatedAt, pgtype.Timestamptz{})
}

func usersListIncArchivedRowByCreatedAscToResponse(r store.ListUsersIncludingArchivedPageByCreatedAscRow) userResponse {
	return toUserResponse(r.ID, r.Email, r.Name, r.IsAdmin, r.CreatedAt, r.ArchivedAt)
}
func usersListIncArchivedRowByNameDescToResponse(r store.ListUsersIncludingArchivedPageByNameDescRow) userResponse {
	return toUserResponse(r.ID, r.Email, r.Name, r.IsAdmin, r.CreatedAt, r.ArchivedAt)
}
func usersListIncArchivedRowByNameAscToResponse(r store.ListUsersIncludingArchivedPageByNameAscRow) userResponse {
	return toUserResponse(r.ID, r.Email, r.Name, r.IsAdmin, r.CreatedAt, r.ArchivedAt)
}
func usersListIncArchivedRowByEmailDescToResponse(r store.ListUsersIncludingArchivedPageByEmailDescRow) userResponse {
	return toUserResponse(r.ID, r.Email, r.Name, r.IsAdmin, r.CreatedAt, r.ArchivedAt)
}
func usersListIncArchivedRowByEmailAscToResponse(r store.ListUsersIncludingArchivedPageByEmailAscRow) userResponse {
	return toUserResponse(r.ID, r.Email, r.Name, r.IsAdmin, r.CreatedAt, r.ArchivedAt)
}

func (s *Server) handleListUsers(w http.ResponseWriter, r *http.Request) {
	includeArchived := r.URL.Query().Get("include_archived") == "true"

	// ?email=<exact> branch — at most one row, but still wrapped in the envelope
	// for shape uniformity (so SPA clients parse one shape per endpoint).
	if email := r.URL.Query().Get("email"); email != "" {
		u, err := s.q.GetUserByEmailPublic(r.Context(), email)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				writeJSON(w, http.StatusOK, page[userResponse]{Items: []userResponse{}, NextCursor: "", Total: 0})
				return
			}
			writeError(w, http.StatusInternalServerError, "failed to look up user")
			return
		}
		if u.ArchivedAt.Valid && !includeArchived {
			writeJSON(w, http.StatusOK, page[userResponse]{Items: []userResponse{}, NextCursor: "", Total: 0})
			return
		}
		writeJSON(w, http.StatusOK, page[userResponse]{
			Items:      []userResponse{toUserResponse(u.ID, u.Email, u.Name, u.IsAdmin, u.CreatedAt, u.ArchivedAt)},
			NextCursor: "",
			Total:      1,
		})
		return
	}

	pp, ok := parsePage(w, r, UsersSortSpec)
	if !ok {
		return
	}

	ctx := r.Context()

	if includeArchived {
		total, err := s.q.CountUsersIncludingArchived(ctx)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to count users")
			return
		}
		items, next := s.listUsersIncludingArchivedBySort(w, ctx, pp)
		if items == nil {
			return
		}
		writeJSON(w, http.StatusOK, page[userResponse]{Items: items, NextCursor: next, Total: total})
		return
	}

	total, err := s.q.CountUsers(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to count users")
		return
	}
	items, next := s.listUsersBySort(w, ctx, pp)
	if items == nil {
		return
	}
	writeJSON(w, http.StatusOK, page[userResponse]{Items: items, NextCursor: next, Total: total})
}

// listUsersBySort dispatches to the correct active-only query for pp.Sort.
// Returns (nil, "") and writes a 500 on DB error. Panics for unknown sort keys
// (indicates a mismatch between UsersSortSpec and this switch, which is a
// programmer error).
func (s *Server) listUsersBySort(w http.ResponseWriter, ctx context.Context, pp pageParams) ([]userResponse, string) { //nolint:contextcheck
	switch pp.Sort {
	case "-created_at":
		rows, err := s.q.ListUsersPage(ctx, store.ListUsersPageParams{
			CursorSet: pp.Cursor.Set,
			CursorTs:  pp.CursorTs(),
			CursorID:  pp.Cursor.ID,
			PageLimit: pp.Limit,
		})
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to list users")
			return nil, ""
		}
		items, next := buildPage(rows, pp.Limit, pp.Sort, usersListRowToResponse, usersListRowKey)
		return items, next

	case "created_at":
		rows, err := s.q.ListUsersPageByCreatedAsc(ctx, store.ListUsersPageByCreatedAscParams{
			CursorSet: pp.Cursor.Set,
			CursorTs:  pp.CursorTs(),
			CursorID:  pp.Cursor.ID,
			PageLimit: pp.Limit,
		})
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to list users")
			return nil, ""
		}
		items, next := buildPage(rows, pp.Limit, pp.Sort, usersListRowByCreatedAscToResponse, func(r store.ListUsersPageByCreatedAscRow) (anySortVal, pgtype.UUID) {
			return r.CreatedAt.Time, r.ID
		})
		return items, next

	case "-name":
		rows, err := s.q.ListUsersPageByNameDesc(ctx, store.ListUsersPageByNameDescParams{
			CursorSet: pp.Cursor.Set,
			CursorV:   pp.Cursor.StrVal,
			CursorID:  pp.Cursor.ID,
			PageLimit: pp.Limit,
		})
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to list users")
			return nil, ""
		}
		items, next := buildPage(rows, pp.Limit, pp.Sort, usersListRowByNameDescToResponse, usersListRowKeyByName)
		return items, next

	case "name":
		rows, err := s.q.ListUsersPageByNameAsc(ctx, store.ListUsersPageByNameAscParams{
			CursorSet: pp.Cursor.Set,
			CursorV:   pp.Cursor.StrVal,
			CursorID:  pp.Cursor.ID,
			PageLimit: pp.Limit,
		})
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to list users")
			return nil, ""
		}
		items, next := buildPage(rows, pp.Limit, pp.Sort, usersListRowByNameAscToResponse, usersListRowKeyByNameAsc)
		return items, next

	case "-email":
		rows, err := s.q.ListUsersPageByEmailDesc(ctx, store.ListUsersPageByEmailDescParams{
			CursorSet: pp.Cursor.Set,
			CursorV:   pp.Cursor.StrVal,
			CursorID:  pp.Cursor.ID,
			PageLimit: pp.Limit,
		})
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to list users")
			return nil, ""
		}
		items, next := buildPage(rows, pp.Limit, pp.Sort, usersListRowByEmailDescToResponse, usersListRowKeyByEmail)
		return items, next

	case "email":
		rows, err := s.q.ListUsersPageByEmailAsc(ctx, store.ListUsersPageByEmailAscParams{
			CursorSet: pp.Cursor.Set,
			CursorV:   pp.Cursor.StrVal,
			CursorID:  pp.Cursor.ID,
			PageLimit: pp.Limit,
		})
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to list users")
			return nil, ""
		}
		items, next := buildPage(rows, pp.Limit, pp.Sort, usersListRowByEmailAscToResponse, usersListRowKeyByEmailAsc)
		return items, next

	default:
		panic("listUsersBySort: missing dispatch arm for sort key " + pp.Sort)
	}
}

// listUsersIncludingArchivedBySort dispatches to the correct including-archived
// query for pp.Sort. Returns (nil, "") and writes a 500 on DB error. Panics for
// unknown sort keys.
func (s *Server) listUsersIncludingArchivedBySort(w http.ResponseWriter, ctx context.Context, pp pageParams) ([]userResponse, string) { //nolint:contextcheck
	switch pp.Sort {
	case "-created_at":
		rows, err := s.q.ListUsersIncludingArchivedPage(ctx, store.ListUsersIncludingArchivedPageParams{
			CursorSet: pp.Cursor.Set,
			CursorTs:  pp.CursorTs(),
			CursorID:  pp.Cursor.ID,
			PageLimit: pp.Limit,
		})
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to list users")
			return nil, ""
		}
		items, next := buildPage(rows, pp.Limit, pp.Sort, usersListIncArchivedRowToResponse, usersListIncArchivedRowKey)
		return items, next

	case "created_at":
		rows, err := s.q.ListUsersIncludingArchivedPageByCreatedAsc(ctx, store.ListUsersIncludingArchivedPageByCreatedAscParams{
			CursorSet: pp.Cursor.Set,
			CursorTs:  pp.CursorTs(),
			CursorID:  pp.Cursor.ID,
			PageLimit: pp.Limit,
		})
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to list users")
			return nil, ""
		}
		items, next := buildPage(rows, pp.Limit, pp.Sort, usersListIncArchivedRowByCreatedAscToResponse, func(r store.ListUsersIncludingArchivedPageByCreatedAscRow) (anySortVal, pgtype.UUID) {
			return r.CreatedAt.Time, r.ID
		})
		return items, next

	case "-name":
		rows, err := s.q.ListUsersIncludingArchivedPageByNameDesc(ctx, store.ListUsersIncludingArchivedPageByNameDescParams{
			CursorSet: pp.Cursor.Set,
			CursorV:   pp.Cursor.StrVal,
			CursorID:  pp.Cursor.ID,
			PageLimit: pp.Limit,
		})
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to list users")
			return nil, ""
		}
		items, next := buildPage(rows, pp.Limit, pp.Sort, usersListIncArchivedRowByNameDescToResponse, usersListIncArchivedRowKeyByName)
		return items, next

	case "name":
		rows, err := s.q.ListUsersIncludingArchivedPageByNameAsc(ctx, store.ListUsersIncludingArchivedPageByNameAscParams{
			CursorSet: pp.Cursor.Set,
			CursorV:   pp.Cursor.StrVal,
			CursorID:  pp.Cursor.ID,
			PageLimit: pp.Limit,
		})
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to list users")
			return nil, ""
		}
		items, next := buildPage(rows, pp.Limit, pp.Sort, usersListIncArchivedRowByNameAscToResponse, usersListIncArchivedRowKeyByNameAsc)
		return items, next

	case "-email":
		rows, err := s.q.ListUsersIncludingArchivedPageByEmailDesc(ctx, store.ListUsersIncludingArchivedPageByEmailDescParams{
			CursorSet: pp.Cursor.Set,
			CursorV:   pp.Cursor.StrVal,
			CursorID:  pp.Cursor.ID,
			PageLimit: pp.Limit,
		})
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to list users")
			return nil, ""
		}
		items, next := buildPage(rows, pp.Limit, pp.Sort, usersListIncArchivedRowByEmailDescToResponse, usersListIncArchivedRowKeyByEmail)
		return items, next

	case "email":
		rows, err := s.q.ListUsersIncludingArchivedPageByEmailAsc(ctx, store.ListUsersIncludingArchivedPageByEmailAscParams{
			CursorSet: pp.Cursor.Set,
			CursorV:   pp.Cursor.StrVal,
			CursorID:  pp.Cursor.ID,
			PageLimit: pp.Limit,
		})
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to list users")
			return nil, ""
		}
		items, next := buildPage(rows, pp.Limit, pp.Sort, usersListIncArchivedRowByEmailAscToResponse, usersListIncArchivedRowKeyByEmailAsc)
		return items, next

	default:
		panic("listUsersIncludingArchivedBySort: missing dispatch arm for sort key " + pp.Sort)
	}
}

func (s *Server) handleGetMe(w http.ResponseWriter, r *http.Request) {
	authUser, _ := UserFromCtx(r.Context())
	row, err := s.q.GetUser(r.Context(), authUser.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to look up user")
		return
	}
	writeJSON(w, http.StatusOK, toUserResponse(row.ID, row.Email, row.Name, row.IsAdmin, row.CreatedAt, row.ArchivedAt))
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
	writeJSON(w, http.StatusOK, toUserResponse(row.ID, row.Email, row.Name, row.IsAdmin, row.CreatedAt, row.ArchivedAt))
}

func (s *Server) handleAdminUpdateUser(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUID(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid user id")
		return
	}

	name, ok := parseUpdateUserRequest(w, r)
	if !ok {
		return
	}

	row, err := s.q.UpdateUserName(r.Context(), store.UpdateUserNameParams{
		ID:   id,
		Name: name,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "user not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to update user")
		return
	}
	writeJSON(w, http.StatusOK, toUserResponse(row.ID, row.Email, row.Name, row.IsAdmin, row.CreatedAt, row.ArchivedAt))
}

func (s *Server) handleAdminArchiveUser(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUID(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid user id")
		return
	}

	authUser, _ := UserFromCtx(r.Context())
	if authUser.ID == id {
		writeError(w, http.StatusBadRequest, "cannot archive yourself")
		return
	}

	ctx := r.Context()

	target, err := s.q.GetUser(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "user not found")
		} else {
			writeError(w, http.StatusInternalServerError, "failed to look up user")
		}
		return
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to start transaction")
		return
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	txq := s.q.WithTx(tx)

	if target.IsAdmin && !target.ArchivedAt.Valid {
		n, err := txq.CountActiveAdmins(ctx)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to count admins")
			return
		}
		if n <= 1 {
			writeError(w, http.StatusBadRequest, "cannot archive the last active admin")
			return
		}
	}

	row, err := txq.ArchiveUser(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusConflict, "user is already archived")
		} else {
			writeError(w, http.StatusInternalServerError, "failed to archive user")
		}
		return
	}

	if _, err := txq.DeleteUserAPITokens(ctx, id); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to revoke tokens")
		return
	}

	if _, err := txq.DisableScheduledJobsByOwner(ctx, id); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to disable schedules")
		return
	}

	if err := tx.Commit(ctx); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to commit archive")
		return
	}

	writeJSON(w, http.StatusOK, toUserResponse(row.ID, row.Email, row.Name, row.IsAdmin, row.CreatedAt, row.ArchivedAt))
}

func (s *Server) handleAdminUnarchiveUser(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUID(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid user id")
		return
	}

	authUser, _ := UserFromCtx(r.Context())
	if authUser.ID == id {
		writeError(w, http.StatusBadRequest, "cannot unarchive yourself")
		return
	}

	ctx := r.Context()

	if _, err := s.q.GetUser(ctx, id); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "user not found")
		} else {
			writeError(w, http.StatusInternalServerError, "failed to look up user")
		}
		return
	}

	row, err := s.q.UnarchiveUser(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusConflict, "user is not archived")
		} else {
			writeError(w, http.StatusInternalServerError, "failed to unarchive user")
		}
		return
	}

	writeJSON(w, http.StatusOK, toUserResponse(row.ID, row.Email, row.Name, row.IsAdmin, row.CreatedAt, row.ArchivedAt))
}

// createUserRequest is the request body for POST /v1/users (admin-only).
type createUserRequest struct {
	Email    string `json:"email"`
	Name     string `json:"name"`
	Password string `json:"password"`
	IsAdmin  bool   `json:"is_admin"`
}

func (s *Server) handleAdminCreateUser(w http.ResponseWriter, r *http.Request) {
	var req createUserRequest
	if !readJSON(w, r, &req) {
		return
	}
	if req.Email == "" {
		writeError(w, http.StatusBadRequest, "email is required")
		return
	}
	if _, err := mail.ParseAddress(req.Email); err != nil {
		writeError(w, http.StatusBadRequest, "invalid email address")
		return
	}
	if len(req.Password) < 8 {
		writeError(w, http.StatusBadRequest, "password must be at least 8 characters")
		return
	}

	passwordHash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcryptCost)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to hash password")
		return
	}

	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = req.Email
	}

	user, err := s.q.CreateUserWithPassword(r.Context(), store.CreateUserWithPasswordParams{
		Name:         name,
		Email:        req.Email,
		IsAdmin:      req.IsAdmin,
		PasswordHash: string(passwordHash),
	})
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			writeError(w, http.StatusConflict, "email already registered")
		} else {
			writeError(w, http.StatusInternalServerError, "failed to create user")
		}
		return
	}

	writeJSON(w, http.StatusCreated, toUserResponse(user.ID, user.Email, user.Name, user.IsAdmin, user.CreatedAt, user.ArchivedAt))
}
