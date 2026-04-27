package api

import (
	"context"

	"github.com/jackc/pgx/v5/pgtype"
)

type contextKey int

const authUserKey contextKey = iota

// AuthUser holds the authenticated user identity, injected by BearerAuth.
type AuthUser struct {
	ID      pgtype.UUID
	TokenID pgtype.UUID
	Name    string
	Email   string
	IsAdmin bool
}

// UserFromCtx returns the authenticated user from ctx. ok is false if no
// user was injected (i.e., request did not pass through BearerAuth).
func UserFromCtx(ctx context.Context) (AuthUser, bool) {
	u, ok := ctx.Value(authUserKey).(AuthUser)
	return u, ok
}

func ctxWithUser(ctx context.Context, u AuthUser) context.Context {
	return context.WithValue(ctx, authUserKey, u)
}
