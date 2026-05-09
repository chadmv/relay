package relayclient

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFetchAllPages_WalksTwoPages(t *testing.T) {
	type item struct {
		ID string `json:"id"`
	}

	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		require.Equal(t, "/v1/things", r.URL.Path)
		require.Equal(t, "200", r.URL.Query().Get("limit"))
		switch calls {
		case 1:
			require.Empty(t, r.URL.Query().Get("cursor"), "first call must have no cursor")
			json.NewEncoder(w).Encode(PageEnvelope[item]{
				Items:      []item{{ID: "a"}, {ID: "b"}},
				NextCursor: "next1",
				Total:      3,
			})
		case 2:
			require.Equal(t, "next1", r.URL.Query().Get("cursor"))
			json.NewEncoder(w).Encode(PageEnvelope[item]{
				Items:      []item{{ID: "c"}},
				NextCursor: "",
				Total:      3,
			})
		}
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "tok")
	got, total, err := FetchAllPages[item](context.Background(), c, "/v1/things", url.Values{}, 0)
	require.NoError(t, err)
	assert.Equal(t, []item{{ID: "a"}, {ID: "b"}, {ID: "c"}}, got)
	assert.EqualValues(t, 3, total)
	assert.Equal(t, 2, calls)
}

func TestFetchAllPages_RespectsUserLimit(t *testing.T) {
	type item struct {
		ID string `json:"id"`
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(PageEnvelope[item]{
			Items:      []item{{ID: "a"}, {ID: "b"}, {ID: "c"}, {ID: "d"}, {ID: "e"}},
			NextCursor: "more",
			Total:      100,
		})
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "tok")
	got, total, err := FetchAllPages[item](context.Background(), c, "/v1/things", url.Values{}, 3)
	require.NoError(t, err)
	assert.Len(t, got, 3, "userLimit=3 caps output at 3 even when more available")
	assert.EqualValues(t, 100, total)
}

func TestFetchAllPages_ForwardsParams(t *testing.T) {
	type item struct {
		ID string `json:"id"`
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "running", r.URL.Query().Get("status"))
		json.NewEncoder(w).Encode(PageEnvelope[item]{Total: 0})
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "tok")
	params := url.Values{"status": []string{"running"}}
	_, _, err := FetchAllPages[item](context.Background(), c, "/v1/things", params, 0)
	require.NoError(t, err)
}
