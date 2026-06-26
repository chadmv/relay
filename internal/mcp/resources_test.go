package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"relay/internal/relayclient"

	"github.com/stretchr/testify/require"
)

func TestResource_ServerInfo(t *testing.T) {
	// This handler already serves /v1/users/me (which the startup probe also hits),
	// so it is not wrapped with whoamiHandler.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/users/me":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id": "u1", "email": "a@b.com", "name": "A", "is_admin": true,
			})
		case "/v1/health":
			_ = json.NewEncoder(w).Encode(map[string]any{"status": "ok", "version": "0.1.2"})
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	defer srv.Close()

	s, _ := NewServer(srv.URL, "t")
	body, terr := s.readServerInfo(context.Background())
	require.Nil(t, terr)
	var got map[string]any
	require.NoError(t, json.Unmarshal(body, &got))
	require.Equal(t, srv.URL, got["server_url"])
	require.Equal(t, "0.1.2", got["server_version"])
	require.Equal(t, "a@b.com", got["user"].(map[string]any)["email"])
}

func TestResource_ServerInfo_HealthFails(t *testing.T) {
	// This handler already serves /v1/users/me, so it is not wrapped.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/users/me":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id": "u1", "email": "a@b.com", "name": "A", "is_admin": false,
			})
		case "/v1/health":
			w.WriteHeader(http.StatusInternalServerError)
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	defer srv.Close()

	s, _ := NewServer(srv.URL, "t")
	body, terr := s.readServerInfo(context.Background())
	require.Nil(t, terr)
	var got map[string]any
	require.NoError(t, json.Unmarshal(body, &got))
	require.Equal(t, "", got["server_version"])
}

func TestResource_RecentJobs(t *testing.T) {
	srv := httptest.NewServer(whoamiHandler(true, func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/v1/jobs", r.URL.Path)
		require.Equal(t, "20", r.URL.Query().Get("limit"))
		_ = json.NewEncoder(w).Encode(relayclient.PageEnvelope[map[string]any]{
			Items: []map[string]any{{"id": "j1", "status": "done"}},
			Total: 1,
		})
	}))
	defer srv.Close()

	s, _ := NewServer(srv.URL, "t")
	body, terr := s.readRecentJobs(context.Background())
	require.Nil(t, terr)
	require.Contains(t, string(body), `"j1"`)
	var got map[string]any
	require.NoError(t, json.Unmarshal(body, &got))
	require.Equal(t, float64(1), got["total"])
}
