// internal/cli/cancel_test.go
package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCancel_DefaultDoesNotSendForceQuery(t *testing.T) {
	var (
		mu      sync.Mutex
		gotURL  string
		gotMeth string
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		gotURL = r.URL.RequestURI()
		gotMeth = r.Method
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"id":     "00000000-0000-0000-0000-000000000001",
			"status": "cancelled",
		})
	}))
	t.Cleanup(srv.Close)

	cfg := &Config{ServerURL: srv.URL, Token: "tkn"}
	var out bytes.Buffer
	require.NoError(t, doCancelJob(context.Background(), cfg, []string{"00000000-0000-0000-0000-000000000001"}, &out))

	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, "DELETE", gotMeth)
	assert.Equal(t, "/v1/jobs/00000000-0000-0000-0000-000000000001", gotURL,
		"default cancel must NOT include ?force= in the URL")
}

func TestCancel_ForceFlagAddsQuery(t *testing.T) {
	var (
		mu     sync.Mutex
		gotURL string
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		gotURL = r.URL.RequestURI()
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"id":     "00000000-0000-0000-0000-000000000002",
			"status": "cancelled",
		})
	}))
	t.Cleanup(srv.Close)

	cfg := &Config{ServerURL: srv.URL, Token: "tkn"}
	var out bytes.Buffer
	require.NoError(t, doCancelJob(context.Background(), cfg, []string{"--force", "00000000-0000-0000-0000-000000000002"}, &out))

	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, "/v1/jobs/00000000-0000-0000-0000-000000000002?force=true", gotURL)
}

func TestCancel_ForceFlagAfterPositional(t *testing.T) {
	// Verifies reorderArgs handles --force placed after the positional arg.
	var (
		mu     sync.Mutex
		gotURL string
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		gotURL = r.URL.RequestURI()
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"id":     "00000000-0000-0000-0000-000000000003",
			"status": "cancelled",
		})
	}))
	t.Cleanup(srv.Close)

	cfg := &Config{ServerURL: srv.URL, Token: "tkn"}
	var out bytes.Buffer
	require.NoError(t, doCancelJob(context.Background(), cfg, []string{"00000000-0000-0000-0000-000000000003", "--force"}, &out))

	mu.Lock()
	defer mu.Unlock()
	assert.Contains(t, gotURL, "?force=true")
}
