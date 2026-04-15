package api

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strings"
	"time"

	"relay/internal/store"
)

// BearerAuth returns middleware that validates the Authorization: Bearer
// header against hashed tokens in the database. On success the AuthUser is
// injected into the request context. Requests without a valid token receive
// 401 Unauthorized.
func BearerAuth(q *store.Queries) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			hdr := r.Header.Get("Authorization")
			if !strings.HasPrefix(hdr, "Bearer ") {
				writeError(w, http.StatusUnauthorized, "missing Bearer token")
				return
			}
			raw := strings.TrimPrefix(hdr, "Bearer ")
			sum := sha256.Sum256([]byte(raw))
			hash := hex.EncodeToString(sum[:])

			row, err := q.GetTokenWithUser(r.Context(), hash)
			if err != nil {
				writeError(w, http.StatusUnauthorized, "invalid token")
				return
			}
			if row.ExpiresAt.Valid && row.ExpiresAt.Time.Before(time.Now()) {
				writeError(w, http.StatusUnauthorized, "token expired")
				return
			}
			u := AuthUser{
				ID:      row.UserID,
				Name:    row.UserName,
				Email:   row.UserEmail,
				IsAdmin: row.UserIsAdmin,
			}
			next.ServeHTTP(w, r.WithContext(ctxWithUser(r.Context(), u)))
		})
	}
}

// AdminOnly returns a 403 for requests whose authenticated user is not an
// admin. Must be chained after BearerAuth.
func AdminOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, ok := UserFromCtx(r.Context())
		if !ok || !u.IsAdmin {
			writeError(w, http.StatusForbidden, "admin access required")
			return
		}
		next.ServeHTTP(w, r)
	})
}
