package mcp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

// listedToolNames connects an in-memory MCP client to s and returns the set of
// tool names the server advertises - the real discovery surface a non-admin sees.
func listedToolNames(t *testing.T, s *Server) map[string]bool {
	t.Helper()
	cs := connectClient(t, s)
	res, err := cs.ListTools(context.Background(), nil)
	require.NoError(t, err)
	names := make(map[string]bool, len(res.Tools))
	for _, tool := range res.Tools {
		names[tool.Name] = true
	}
	return names
}

func TestRegistration_AdminSeesReservations(t *testing.T) {
	backend := newWhoamiBackend(t, true)
	s, err := NewServer(backend.URL, "t")
	require.NoError(t, err)

	names := listedToolNames(t, s)
	require.True(t, names["relay_list_reservations"], "admin must see relay_list_reservations")
	// A non-admin tool stays present for everyone.
	require.True(t, names["relay_whoami"], "relay_whoami must always be present")
}

func TestRegistration_NonAdminHidesReservations(t *testing.T) {
	backend := newWhoamiBackend(t, false)
	s, err := NewServer(backend.URL, "t")
	require.NoError(t, err)

	names := listedToolNames(t, s)
	require.False(t, names["relay_list_reservations"], "non-admin must NOT see relay_list_reservations")
	// Non-admin tools remain registered.
	require.True(t, names["relay_whoami"], "relay_whoami must always be present")
	require.True(t, names["relay_run_schedule_now"], "run_now is owner-or-admin and stays registered")
}

func TestNewServer_WhoamiFailureAtStartup(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"token expired"}`))
	}))
	defer backend.Close()

	s, err := NewServer(backend.URL, "t")
	require.Error(t, err)
	require.Nil(t, s)
}
