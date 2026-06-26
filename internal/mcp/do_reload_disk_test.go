package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestDo_OnDiskTokenReload_TakesEffect proves the spec's headline acceptance: a
// token refreshed on disk (as `relay login` does via SaveConfig) takes effect on
// the next tool call without a process restart. The reloadToken func reads the
// token from a real config FILE the test rewrites mid-session, mirroring the
// production mcpTokenReloader -> LoadConfig resolution, but kept white-box in
// package mcp so no test-only method is exported on the production Server API.
func TestDo_OnDiskTokenReload_TakesEffect(t *testing.T) {
	// Real on-disk config file the "relay login" step rewrites mid-session.
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")

	// reloadToken reads the token field from the config file on disk, exactly as
	// the CLI's mcpTokenReloader does via LoadConfig (file resolution).
	reloadFromDisk := func() (string, error) {
		data, err := os.ReadFile(configPath)
		if err != nil {
			return "", err
		}
		var cfg struct {
			Token string `json:"token"`
		}
		if err := json.Unmarshal(data, &cfg); err != nil {
			return "", err
		}
		return cfg.Token, nil
	}
	writeConfig := func(token string) {
		data, err := json.Marshal(map[string]string{"token": token})
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(configPath, data, 0600))
	}

	var mu sync.Mutex
	var jobCalls []string
	srv := httptest.NewServer(whoamiHandler(false, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/jobs/j1" {
			http.Error(w, "unexpected: "+r.URL.Path, http.StatusNotFound)
			return
		}
		tok := r.Header.Get("Authorization")
		mu.Lock()
		jobCalls = append(jobCalls, tok)
		mu.Unlock()
		if tok == "Bearer fresh" {
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "j1", "status": "running"})
			return
		}
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "token expired"})
	}))
	defer srv.Close()

	// Seed the config file with the OLD token, then build the server with it.
	writeConfig("old")
	s, err := NewServer(srv.URL, "old")
	require.NoError(t, err)
	s.SetTokenReloader(reloadFromDisk)

	// Simulate `relay login` writing a fresh token to disk mid-session.
	writeConfig("fresh")

	// Drive a tool call through the do chokepoint: it 401s on "old", reloads
	// "fresh" from disk, retries, and succeeds.
	var out map[string]any
	derr := s.do(context.Background(), "GET", "/v1/jobs/j1", nil, &out)
	require.NoError(t, derr)
	require.Equal(t, "running", out["status"])

	mu.Lock()
	defer mu.Unlock()
	require.Equal(t, []string{"Bearer old", "Bearer fresh"}, jobCalls,
		"first request carries the stale token, the retry the on-disk refreshed one")
}
