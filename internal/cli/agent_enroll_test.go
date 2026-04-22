// internal/cli/agent_enroll_test.go
package cli

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAgentEnroll_HappyPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "POST", r.Method)
		require.Equal(t, "/v1/agent-enrollments", r.URL.Path)

		var body map[string]any
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		require.Equal(t, "render-07", body["hostname_hint"])

		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"00000000-0000-0000-0000-000000000001","token":"raw-token-abc","expires_at":"2099-01-01T00:00:00Z"}`)
	}))
	defer srv.Close()

	cfg := &Config{ServerURL: srv.URL, Token: "admin-token"}
	var out, errOut strings.Builder
	err := doAgentEnroll(context.Background(), []string{"--hostname", "render-07", "--ttl", "1h"}, cfg, &out, &errOut)
	require.NoError(t, err)
	require.Contains(t, out.String(), "raw-token-abc")
	require.Contains(t, errOut.String(), "enrollment expires at")
	require.Contains(t, errOut.String(), "RELAY_AGENT_ENROLLMENT_TOKEN=raw-token-abc")
}

func TestAgentEnroll_MissingToken(t *testing.T) {
	cfg := &Config{ServerURL: "http://localhost:9999"}
	var out, errOut strings.Builder
	err := doAgentEnroll(context.Background(), nil, cfg, &out, &errOut)
	require.Error(t, err)
	require.Contains(t, err.Error(), "not logged in")
}
