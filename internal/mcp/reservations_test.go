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

func TestListReservations_HappyPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/v1/reservations", r.URL.Path)
		_ = json.NewEncoder(w).Encode(relayclient.PageEnvelope[map[string]any]{
			Items: []map[string]any{{"id": "r1", "name": "vfx"}},
		})
	}))
	defer srv.Close()

	s, _ := NewServer(srv.URL, "t")
	out, terr := s.callListReservations(context.Background(), listReservationsArgs{})
	require.Nil(t, terr)
	require.Len(t, out["items"], 1)
}

func TestListReservations_Forbidden(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "admin only"})
	}))
	defer srv.Close()

	s, _ := NewServer(srv.URL, "t")
	_, terr := s.callListReservations(context.Background(), listReservationsArgs{})
	require.Equal(t, "forbidden", terr.Code)
}
