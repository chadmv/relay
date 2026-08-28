//go:build integration

package cli

import (
	"bytes"
	"errors"
	"testing"

	"relay/internal/relayclient"

	"github.com/stretchr/testify/require"
)

func TestIntegration_AdminUsersListGet(t *testing.T) {
	s := startRelayServer(t)

	var listOut bytes.Buffer
	require.NoError(t, doAdminUsers(testCtx(t), s.adminCfg(), []string{"list"}, &listOut))
	list := listOut.String()
	require.Contains(t, list, "Total: 2")
	require.Contains(t, list, s.AdminEmail)
	require.Contains(t, list, s.UserEmail)

	var getOut bytes.Buffer
	require.NoError(t, doAdminUsers(testCtx(t), s.adminCfg(),
		[]string{"get", s.UserEmail}, &getOut))
	got := getOut.String()
	require.Contains(t, got, "Email:    "+s.UserEmail)
	require.Contains(t, got, "Admin:    no")
	require.Contains(t, got, "Archived: no")

	// The admin column for the OTHER user, read through the same detail view,
	// so `is_admin` is asserted in both directions rather than only in the one
	// a default-valued bool would satisfy.
	var adminOut bytes.Buffer
	require.NoError(t, doAdminUsers(testCtx(t), s.adminCfg(),
		[]string{"get", s.AdminEmail}, &adminOut))
	require.Contains(t, adminOut.String(), "Admin:    yes")
}

// TestIntegration_AdminUsersList_NonAdmin_Is403 pins that GET /v1/users is
// auth(admin(...)) and that the CLI surfaces the status rather than an empty
// list.
func TestIntegration_AdminUsersList_NonAdmin_Is403(t *testing.T) {
	s := startRelayServer(t)

	var out bytes.Buffer
	err := doAdminUsers(testCtx(t), s.userCfg(), []string{"list"}, &out)
	require.Error(t, err)

	var re *relayclient.ResponseError
	require.True(t, errors.As(err, &re), "want a ResponseError, got %T: %v", err, err)
	require.Equal(t, 403, re.StatusCode)
	require.Contains(t, err.Error(), "admin access required")
}
