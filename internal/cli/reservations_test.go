// internal/cli/reservations_test.go
package cli

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestReservations_ListCreateDelete(t *testing.T) {
	now := time.Now().Truncate(time.Second)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && r.URL.Path == "/v1/reservations":
			json.NewEncoder(w).Encode([]reservationResp{
				{ID: "res-1", Name: "film-x-weekend", StartsAt: &now},
			})

		case r.Method == "POST" && r.URL.Path == "/v1/reservations":
			var body map[string]any
			json.NewDecoder(r.Body).Decode(&body)
			require.Equal(t, "film-x-weekend", body["name"])
			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(reservationResp{ID: "res-1", Name: "film-x-weekend"})

		case r.Method == "DELETE" && r.URL.Path == "/v1/reservations/res-1":
			w.WriteHeader(http.StatusNoContent)
		}
	}))
	defer srv.Close()

	cfg := &Config{ServerURL: srv.URL, Token: "tok"}

	t.Run("list", func(t *testing.T) {
		var out strings.Builder
		err := doReservations(context.Background(), cfg, []string{"list"}, &out)
		require.NoError(t, err)
		require.Contains(t, out.String(), "film-x-weekend")
	})

	t.Run("create", func(t *testing.T) {
		f, err := os.CreateTemp("", "res*.json")
		require.NoError(t, err)
		t.Cleanup(func() { os.Remove(f.Name()) })
		json.NewEncoder(f).Encode(map[string]any{"name": "film-x-weekend"})
		f.Close()

		var out strings.Builder
		err = doReservations(context.Background(), cfg, []string{"create", f.Name()}, &out)
		require.NoError(t, err)
		require.Contains(t, out.String(), "res-1")
	})

	t.Run("delete", func(t *testing.T) {
		var out strings.Builder
		err := doReservations(context.Background(), cfg, []string{"delete", "res-1"}, &out)
		require.NoError(t, err)
		require.Contains(t, out.String(), "deleted")
	})
}
