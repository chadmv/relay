package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
)

// toolBackend wraps the whoami startup handler (so NewServer succeeds) with a
// /v1/test endpoint whose 401/200 behavior depends on the bearer token it sees.
// It records, for each /v1/test request, the token presented.
type toolBackend struct {
	srv       *httptest.Server
	mu        sync.Mutex
	testToks  []string // bearer tokens seen on /v1/test, in order
	goodToken string   // token that yields 200 on /v1/test; others yield 401
}

func newToolBackend(t *testing.T, goodToken string) *toolBackend {
	t.Helper()
	b := &toolBackend{goodToken: goodToken}
	b.srv = httptest.NewServer(whoamiHandler(false, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/test" {
			http.Error(w, "unexpected path: "+r.URL.Path, http.StatusNotFound)
			return
		}
		tok := r.Header.Get("Authorization")
		b.mu.Lock()
		b.testToks = append(b.testToks, tok)
		b.mu.Unlock()
		if tok == "Bearer "+b.goodToken {
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
			return
		}
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "token expired"})
	}))
	t.Cleanup(b.srv.Close)
	return b
}

func (b *toolBackend) testTokens() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]string, len(b.testToks))
	copy(out, b.testToks)
	return out
}

// TestDo_ReloadOn401_RetrySucceeds: in-use token 401s; config-reader returns a
// new token the backend accepts; the retry succeeds and the NEW token byte value
// appears on the retry request. Exactly two /v1/test requests.
func TestDo_ReloadOn401_RetrySucceeds(t *testing.T) {
	b := newToolBackend(t, "newtok")
	s, err := NewServer(b.srv.URL, "oldtok")
	require.NoError(t, err)
	s.reloadToken = func() (string, error) { return "newtok", nil }

	var out map[string]any
	derr := s.do(context.Background(), "GET", "/v1/test", nil, &out)
	require.NoError(t, derr)
	require.Equal(t, true, out["ok"])

	toks := b.testTokens()
	require.Len(t, toks, 2, "expected one original request + one retry")
	require.Equal(t, "Bearer oldtok", toks[0])
	require.Equal(t, "Bearer newtok", toks[1], "retry must carry the reloaded token")
}

// TestDo_ReloadOn401_StillExpired: reloaded token differs but is also bad. One
// retry, then surface the 401 (auth_expired via MapError). Two /v1/test requests.
func TestDo_ReloadOn401_StillExpired(t *testing.T) {
	b := newToolBackend(t, "neveraccepted")
	s, err := NewServer(b.srv.URL, "oldtok")
	require.NoError(t, err)
	s.reloadToken = func() (string, error) { return "alsobad", nil }

	derr := s.do(context.Background(), "GET", "/v1/test", nil, nil)
	require.NotNil(t, derr)
	require.Equal(t, "auth_expired", MapError(derr).Code)
	require.Len(t, b.testTokens(), 2)
}

// TestDo_IdenticalToken_NoRetry: reloaded token equals the in-use token; no retry.
func TestDo_IdenticalToken_NoRetry(t *testing.T) {
	b := newToolBackend(t, "neveraccepted")
	s, err := NewServer(b.srv.URL, "oldtok")
	require.NoError(t, err)
	s.reloadToken = func() (string, error) { return "oldtok", nil }

	derr := s.do(context.Background(), "GET", "/v1/test", nil, nil)
	require.NotNil(t, derr)
	require.Equal(t, "auth_expired", MapError(derr).Code)
	require.Len(t, b.testTokens(), 1, "identical token must not trigger a retry")
}

// TestDo_EmptyReload_NoRetry: reloaded token is empty; no retry.
func TestDo_EmptyReload_NoRetry(t *testing.T) {
	b := newToolBackend(t, "neveraccepted")
	s, err := NewServer(b.srv.URL, "oldtok")
	require.NoError(t, err)
	s.reloadToken = func() (string, error) { return "", nil }

	derr := s.do(context.Background(), "GET", "/v1/test", nil, nil)
	require.NotNil(t, derr)
	require.Equal(t, "auth_expired", MapError(derr).Code)
	require.Len(t, b.testTokens(), 1)
}

// TestDo_NilReader_NoRetry: no config-reader injected; no retry.
func TestDo_NilReader_NoRetry(t *testing.T) {
	b := newToolBackend(t, "neveraccepted")
	s, err := NewServer(b.srv.URL, "oldtok")
	require.NoError(t, err)
	// s.reloadToken left nil

	derr := s.do(context.Background(), "GET", "/v1/test", nil, nil)
	require.NotNil(t, derr)
	require.Equal(t, "auth_expired", MapError(derr).Code)
	require.Len(t, b.testTokens(), 1)
}

// TestDo_Non401_Passthrough: a 404 passes straight through; reader never invoked,
// single request.
func TestDo_Non401_Passthrough(t *testing.T) {
	srv := httptest.NewServer(whoamiHandler(false, func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusNotFound)
	}))
	defer srv.Close()
	s, err := NewServer(srv.URL, "oldtok")
	require.NoError(t, err)
	var readerCalled int32
	s.reloadToken = func() (string, error) { atomic.AddInt32(&readerCalled, 1); return "x", nil }

	derr := s.do(context.Background(), "GET", "/v1/test", nil, nil)
	require.NotNil(t, derr)
	require.Equal(t, "not_found", MapError(derr).Code)
	require.Equal(t, int32(0), atomic.LoadInt32(&readerCalled), "non-401 must not reload")
}

// TestDo_ConcurrentReload_Race: N concurrent calls that 401 on the old token and
// succeed on the new one; config-reader returns the new token. Run under -race.
func TestDo_ConcurrentReload_Race(t *testing.T) {
	b := newToolBackend(t, "newtok")
	s, err := NewServer(b.srv.URL, "oldtok")
	require.NoError(t, err)
	s.reloadToken = func() (string, error) { return "newtok", nil }

	var wg sync.WaitGroup
	errs := make([]error, 20)
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			var out map[string]any
			errs[n] = s.do(context.Background(), "GET", "/v1/test", nil, &out)
		}(i)
	}
	wg.Wait()
	for _, e := range errs {
		require.NoError(t, e)
	}
}
