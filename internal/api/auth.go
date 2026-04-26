package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/http"
	"net/mail"
	"strings"
	"sync"
	"time"

	"relay/internal/store"
	"relay/internal/tokenhash"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"golang.org/x/crypto/bcrypt"
)

var bcryptCost = 12

var (
	dummyHashOnce   sync.Once
	dummyBcryptHash []byte
)

func getDummyHash() []byte {
	dummyHashOnce.Do(func() {
		dummyBcryptHash, _ = bcrypt.GenerateFromPassword([]byte("relay-auth-sentinel"), bcryptCost)
	})
	return dummyBcryptHash
}

func (s *Server) issueToken(ctx context.Context, q *store.Queries, userID pgtype.UUID) (string, time.Time, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", time.Time{}, err
	}
	rawHex := hex.EncodeToString(raw)
	hash := tokenhash.Hash(rawHex)
	expires := time.Now().Add(30 * 24 * time.Hour)
	if _, err := q.CreateToken(ctx, store.CreateTokenParams{
		UserID:    userID,
		TokenHash: hash,
		ExpiresAt: pgtype.Timestamptz{Time: expires, Valid: true},
	}); err != nil {
		return "", time.Time{}, err
	}
	return rawHex, expires, nil
}

func (s *Server) handleRegister(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Email       string `json:"email"`
		Name        string `json:"name"`
		Password    string `json:"password"`
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
	if _, err := mail.ParseAddress(req.Email); err != nil {
		writeError(w, http.StatusBadRequest, "invalid email address")
		return
	}
	if len(req.Password) < 8 {
		writeError(w, http.StatusBadRequest, "password must be at least 8 characters")
		return
	}
	if req.InviteToken == "" {
		writeError(w, http.StatusBadRequest, "invite_token is required")
		return
	}

	ctx := r.Context()

	tokenHash := tokenhash.Hash(req.InviteToken)
	invite, err := s.q.GetInviteByTokenHash(ctx, tokenHash)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusBadRequest, "invalid invite token")
		} else {
			writeError(w, http.StatusInternalServerError, "failed to look up invite")
		}
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

	passwordHash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcryptCost)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to hash password")
		return
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to start transaction")
		return
	}
	defer tx.Rollback(ctx)
	txq := s.q.WithTx(tx)

	name := req.Name
	if name == "" {
		name = req.Email
	}
	user, err := txq.CreateUserWithPassword(ctx, store.CreateUserWithPasswordParams{
		Name:         name,
		Email:        req.Email,
		IsAdmin:      false,
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

	rowsAffected, err := txq.MarkInviteUsed(ctx, store.MarkInviteUsedParams{
		ID:     invite.ID,
		UsedBy: user.ID,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to redeem invite")
		return
	}
	if rowsAffected == 0 {
		writeError(w, http.StatusBadRequest, "invite already used")
		return
	}

	token, expires, err := s.issueToken(ctx, txq, user.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to generate token")
		return
	}

	if err := tx.Commit(ctx); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to commit registration")
		return
	}

	writeJSON(w, http.StatusCreated, map[string]any{
		"token":      token,
		"expires_at": expires,
	})
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	ctx := r.Context()
	user, err := s.q.GetUserByEmail(ctx, req.Email)

	var hashToCompare []byte
	if err == nil {
		hashToCompare = []byte(user.PasswordHash)
	} else if errors.Is(err, pgx.ErrNoRows) {
		hashToCompare = getDummyHash()
	} else {
		writeError(w, http.StatusInternalServerError, "failed to look up user")
		return
	}

	bcryptErr := bcrypt.CompareHashAndPassword(hashToCompare, []byte(req.Password))
	if bcryptErr != nil || errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusUnauthorized, "invalid email or password")
		return
	}

	token, expires, err := s.issueToken(ctx, s.q, user.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to generate token")
		return
	}

	writeJSON(w, http.StatusCreated, map[string]any{
		"token":      token,
		"expires_at": expires,
	})
}

func (s *Server) handleChangePassword(w http.ResponseWriter, r *http.Request) {
	var req struct {
		CurrentPassword string `json:"current_password"`
		NewPassword     string `json:"new_password"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if len(req.NewPassword) < 8 {
		writeError(w, http.StatusBadRequest, "password must be at least 8 characters")
		return
	}

	authUser, _ := UserFromCtx(r.Context())
	ctx := r.Context()

	user, err := s.q.GetUser(ctx, authUser.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to look up user")
		return
	}

	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(req.CurrentPassword)); err != nil {
		writeError(w, http.StatusForbidden, "current password is incorrect")
		return
	}

	newHash, err := bcrypt.GenerateFromPassword([]byte(req.NewPassword), bcryptCost)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to hash password")
		return
	}

	if err := s.q.SetPasswordHash(ctx, store.SetPasswordHashParams{
		ID:           authUser.ID,
		PasswordHash: string(newHash),
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update password")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
