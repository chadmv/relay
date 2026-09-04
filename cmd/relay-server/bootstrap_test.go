//go:build integration

package main

import (
	"testing"

	"relay/internal/store"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"
)

func newTestQueries(t *testing.T) *store.Queries {
	t.Helper()
	_, q := newPgdsnPoolAndQueries(t)
	return q
}

func createUserWithTestPassword(t *testing.T, q *store.Queries, name, email string, isAdmin bool) store.User {
	t.Helper()
	ph, err := bcrypt.GenerateFromPassword([]byte("testpass"), bcrypt.MinCost)
	require.NoError(t, err)
	user, err := q.CreateUserWithPassword(t.Context(), store.CreateUserWithPasswordParams{
		Name: name, Email: email, IsAdmin: isAdmin, PasswordHash: string(ph),
	})
	require.NoError(t, err)
	return user
}

func TestBootstrapAdmin_NoUsers_CreatesAdmin(t *testing.T) {
	q := newTestQueries(t)
	ctx := t.Context()

	require.NoError(t, bootstrapAdmin(ctx, q, "admin@example.com", "bootstrappassword"))

	user, err := q.GetUserByEmail(ctx, "admin@example.com")
	require.NoError(t, err)
	assert.True(t, user.IsAdmin)
	assert.Equal(t, "admin@example.com", user.Email)
	assert.NotEmpty(t, user.PasswordHash)
}

func TestBootstrapAdmin_ExistingUser_Promotes(t *testing.T) {
	q := newTestQueries(t)
	ctx := t.Context()

	createUserWithTestPassword(t, q, "Bob", "admin@example.com", false)

	require.NoError(t, bootstrapAdmin(ctx, q, "admin@example.com", "bootstrappassword"))

	user, err := q.GetUserByEmail(ctx, "admin@example.com")
	require.NoError(t, err)
	assert.True(t, user.IsAdmin)
}

func TestBootstrapAdmin_AdminAlreadyExists_Skips(t *testing.T) {
	q := newTestQueries(t)
	ctx := t.Context()

	createUserWithTestPassword(t, q, "Existing Admin", "other@example.com", true)

	require.NoError(t, bootstrapAdmin(ctx, q, "new@example.com", "bootstrappassword"))

	_, err := q.GetUserByEmail(ctx, "new@example.com")
	require.Error(t, err, "expected no user created when an admin already exists")
}
