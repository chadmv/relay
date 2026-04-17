package cli

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInviteCreate_PrintsToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "POST", r.Method)
		require.Equal(t, "/v1/invites", r.URL.Path)

		var body map[string]string
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		assert.Equal(t, "user@example.com", body["email"])
		assert.Equal(t, "24h", body["expires_in"])

		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]any{
			"id":         "some-uuid",
			"token":      "the-invite-token",
			"expires_at": time.Now().Add(24 * time.Hour),
		})
	}))
	defer srv.Close()

	cfg := &Config{ServerURL: srv.URL, Token: "admin-token"}
	var out strings.Builder
	err := doInviteCreate(context.Background(), []string{"--email", "user@example.com", "--expires", "24h"}, cfg, &out)
	require.NoError(t, err)
	assert.Contains(t, out.String(), "the-invite-token")
}

func TestInviteCreate_NoEmail_DefaultExpiry(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		assert.Equal(t, "", body["email"])
		assert.Equal(t, "72h", body["expires_in"])

		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]any{
			"id":         "some-uuid",
			"token":      "tok-xyz",
			"expires_at": time.Now().Add(72 * time.Hour),
		})
	}))
	defer srv.Close()

	cfg := &Config{ServerURL: srv.URL, Token: "admin-token"}
	var out strings.Builder
	err := doInviteCreate(context.Background(), []string{}, cfg, &out)
	require.NoError(t, err)
	assert.Contains(t, out.String(), "tok-xyz")
}
