package relayclient

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestSetToken_DoUsesLatest verifies Do attaches the token set most recently via SetToken.
func TestSetToken_DoUsesLatest(t *testing.T) {
	var seen string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "old")
	require.Equal(t, "old", c.Token())

	c.SetToken("new")
	require.Equal(t, "new", c.Token())

	require.NoError(t, c.Do(context.Background(), "GET", "/", nil, nil))
	require.Equal(t, "Bearer new", seen)
}

// TestSetToken_ConcurrentRace fires concurrent SetToken and Do; run under -race.
func TestSetToken_ConcurrentRace(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "t0")
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(2)
		go func(n int) { defer wg.Done(); c.SetToken("t"); _ = c.Token() }(i)
		go func() { defer wg.Done(); _ = c.Do(context.Background(), "GET", "/", nil, nil) }()
	}
	wg.Wait()
}
