# MCP Token Mid-Session Reload - Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** When the MCP server's auth token expires mid-session, a tool call that 401s reloads the refreshed token from config, swaps it into the shared client, retries the call exactly once, and succeeds - no process restart. If the reloaded token is empty, unchanged, or also expired, the original `auth_expired` is surfaced with no retry loop.

**Architecture:** Make `relayclient.Client.token` mutable and thread-safe (RWMutex + `SetToken`). In `internal/mcp`, introduce a single chokepoint `Server.do` that all tool calls route through; on a 401 it reloads via an injectable config-reader (`func() (string, error)` field on `Server`, defaulting to a real config+env resolver wired from `internal/cli/mcp.go` to avoid an import cycle), compares to the in-use token, swaps via `SetToken` only if non-empty and different, and retries once.

**Tech Stack:** Go, `github.com/modelcontextprotocol/go-sdk`, testify, `net/http/httptest`. Backend Go only - no frontend, no `.sql`, no `.proto`, no `make generate`.

---

## Slice independence

Single backend slice. No frontend work, no API/schema changes, no `.sql`/`.proto`. Not applicable to Phase 3 parallelism.

## Worktree-path constraint (read before any command)

This is a git worktree. The working tree lives at
`D:/dev/relay/.claude/worktrees/happy-mendel-18687f` on branch
`claude/happy-mendel-18687f`. Run every command from that directory (the harness
resets cwd between bash calls, so use the absolute path or `cd` into it within the
same command). **NEVER `cd D:/dev/relay`** - that is a separate checkout on `main`;
committing there lands work on the wrong branch. All command blocks below assume cwd
is the worktree root.

## Is there a single `client.Do` chokepoint for MCP tool calls?

**No.** Each tool calls `s.client.Do(...)` directly. Confirmed call sites (all in `internal/mcp`):

- `cancel.go:27`, `jobs.go:59`, `jobs.go:81`, `reservations.go:42`, `resources.go:56`, `resources.go:64` (tolerated failure, `_ =`), `resources.go:79`, `run_now.go:31`, `schedules_read.go:52`, `schedules_read.go:73`, `schedules_write.go:62`, `schedules_write.go:102`, `schedules_write.go:120`, `submit.go:31`, `tasks.go:36`, `tasks.go:53`, `wait.go:67` (inside a poll loop), `task_logs.go:47`, `whoami.go:20`, `workers.go:52`, `workers.go:73`.

Because there is no chokepoint, this plan introduces one: `Server.do(ctx, method, path, body, out any) error`, a thin wrapper around `s.client.Do` that adds the reload-on-401-retry-once logic. Every call site above changes from `s.client.Do(...)` to `s.do(...)`. `s.client.BaseURL()` (used in `whoami.go:29` and `resources.go:67`) is unchanged - it does not touch the token.

## The change (validated against the code)

Confirmed signatures and facts:

- `internal/relayclient/client.go:24-28`: `type Client struct { base string; token string; http *http.Client }`. The `token` field is read in exactly two places: `Do` (lines 55-56) and `StreamEvents` (lines 104-105), both building `Authorization: Bearer <token>`. There is **no setter and no mutex today.**
- `internal/relayclient/client.go:32-34`: `func NewClient(serverURL, token string) *Client` sets `token: token`.
- `internal/relayclient/client.go:16-21`: `ResponseError{StatusCode int; Message string}` with `Error()`; 401 carries `StatusCode == 401`.
- **Who else uses `Client`:** `internal/cli/config.go:62` (`cfg.NewClient`), `internal/cli/login.go:76`, and many CLI/agent call sites use `relayclient.NewClient`. None read or write the unexported `token` field (it is unexported; only `client.go` touches it). Adding a mutex and a `SetToken` method is therefore backward compatible - existing callers are unaffected and continue to work with the token they passed to `NewClient`.
- `internal/mcp/server.go:18-23`: `type Server struct { client *relayclient.Client; mcp *mcpsdk.Server; waitPoll time.Duration; isAdmin bool }`.
- `internal/mcp/server.go:27`: `func NewServer(serverURL, token string) (*Server, error)`. It validates non-empty url/token, builds `relayclient.NewClient`, builds the SDK server, then calls `s.callWhoami(context.Background())` at line 50 (the startup identity probe), sets `isAdmin`, and registers tools/resources. **The startup probe is mid-process-construction; this plan must not break it - tests use the whoami backend helper so it succeeds.**
- `internal/mcp/errors.go:27-29`: `MapError` turns `re.StatusCode == 401` into `ToolError{Code: "auth_expired", ...}`. Unchanged.
- `internal/cli/config.go:25-42`: `LoadConfig` reads the config file (path from `configFilePathFn`) then applies `RELAY_URL`/`RELAY_TOKEN` env overrides. This is the resolution the reload must reuse.
- `internal/cli/mcp.go:21`: `srv, err := internalmcp.NewServer(cfg.ServerURL, cfg.Token)`. This is where the real config-reader gets wired in.

**Import-cycle note:** `internal/cli` imports `internal/mcp` (`cli/mcp.go:8`), so `internal/mcp` MUST NOT import `internal/cli`. The config-reader is therefore an injected `func() (string, error)` field on `Server`, defaulting to nil (treated as "no reload available"); `cli/mcp.go` supplies the real `LoadConfig`-backed reader via a new exported setter.

## Thread-safety design (from the spec's RWMutex approach)

- `relayclient.Client` gains a `sync.RWMutex` guarding `token`. `Do` and `StreamEvents` read the token under `RLock` (copy the string out, release, then build the header). `SetToken(string)` writes under `Lock`. The token is a value-typed string copied out under the lock - no pointer to mutable client state escapes (satisfies "no interior pointers across locks").
- In `Server.do`, two concurrent tool calls that both 401 near-simultaneously may each reload and each call `SetToken`. Both read the same refreshed config, so the swaps are idempotent (same value); the identical-token short-circuit makes the second a no-op. The retry counter is local to each `do` invocation, so no single originating call retries more than once.
- The config-reader (`s.reloadToken`) is set once at construction and only read thereafter, so it needs no lock.

## Reload-on-401 algorithm (in `Server.do`)

1. Call `s.client.Do(ctx, method, path, body, out)`. If `err == nil`, return nil.
2. If `err` is not a `*relayclient.ResponseError` with `StatusCode == 401`, return `err` unchanged (non-401 and network errors pass straight to `MapError`).
3. If `s.reloadToken == nil`, return the original 401 `err` (no reader configured).
4. Call `newTok, rerr := s.reloadToken()`. If `rerr != nil` or `newTok == ""`, return the original 401 `err`.
5. Read the current token via `s.client.Token()` (new accessor). If `newTok == current`, return the original 401 `err` (identical-token short-circuit, no retry).
6. `s.client.SetToken(newTok)`. Re-issue `s.client.Do(ctx, method, path, body, out)` exactly once and return its result (success or error). A second 401 is returned as-is and `MapError` produces `auth_expired`.

Note: step 5 needs a read accessor on `Client`. Add `func (c *Client) Token() string` (RLock read) alongside `SetToken`.

---

## Task 1: thread-safe mutable token on `relayclient.Client`

**Files:**
- Modify: `internal/relayclient/client.go:24-34` (struct + NewClient), `:55-56` and `:104-105` (token reads)
- Test: `internal/relayclient/client_settoken_test.go` (create)

- [ ] **Step 1: Write the failing test**

Create `internal/relayclient/client_settoken_test.go`:

```go
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd D:/dev/relay/.claude/worktrees/happy-mendel-18687f && go test ./internal/relayclient/... -run TestSetToken -v`
Expected: FAIL - compile error, `c.Token` and `c.SetToken` undefined.

- [ ] **Step 3: Write minimal implementation**

In `internal/relayclient/client.go`, add `"sync"` to the import block, and change the struct + NewClient to:

```go
// Client wraps *http.Client with a base URL and Bearer token.
type Client struct {
	base string
	http *http.Client

	mu    sync.RWMutex
	token string
}

// NewClient returns a Client for the given server URL and token.
// Pass token="" for unauthenticated requests.
func NewClient(serverURL, token string) *Client {
	return &Client{base: strings.TrimRight(serverURL, "/"), token: token, http: &http.Client{}}
}

// Token returns the current bearer token.
func (c *Client) Token() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.token
}

// SetToken atomically replaces the bearer token used by subsequent requests.
// Safe for concurrent use with Do and StreamEvents.
func (c *Client) SetToken(token string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.token = token
}
```

In `Do` (was lines 55-57), replace the direct field read:

```go
	if tok := c.Token(); tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
```

In `StreamEvents` (was lines 104-106), replace the direct field read identically:

```go
	if tok := c.Token(); tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd D:/dev/relay/.claude/worktrees/happy-mendel-18687f && go test ./internal/relayclient/... -run TestSetToken -v`
Expected: PASS (both tests).

- [ ] **Step 5: Commit**

```bash
cd D:/dev/relay/.claude/worktrees/happy-mendel-18687f && \
git add internal/relayclient/client.go internal/relayclient/client_settoken_test.go && \
git commit -m "feat(relayclient): thread-safe mutable token with SetToken/Token"
```

---

## Task 2: `Server.do` chokepoint with reload-on-401-retry-once

This task adds the wrapper and its config-reader field, with the reload-retry tests, but does NOT yet re-route the existing tool call sites (Task 3). The wrapper is exercised directly via a small test tool path: we reuse `callWhoami`, which we re-route here as the first consumer so the behavior is testable end-to-end through a real tool call.

**Files:**
- Modify: `internal/mcp/server.go:18-23` (add `reloadToken` field), and `internal/mcp/whoami.go:20` (route `callWhoami` through `s.do`)
- Create: `internal/mcp/do.go` (the wrapper + setter)
- Test: `internal/mcp/do_test.go` (create)

- [ ] **Step 1: Write the failing tests**

Create `internal/mcp/do_test.go`:

```go
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
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd D:/dev/relay/.claude/worktrees/happy-mendel-18687f && go test ./internal/mcp/... -run TestDo_ -v`
Expected: FAIL - compile error, `s.do` and `s.reloadToken` undefined.

- [ ] **Step 3: Add the `reloadToken` field to `Server`**

In `internal/mcp/server.go`, change the struct (was lines 18-23) to:

```go
// Server wraps the MCP SDK server and a relay API client.
type Server struct {
	client   *relayclient.Client
	mcp      *mcpsdk.Server
	waitPoll time.Duration // overridable in tests; 0 means use defaultWaitPoll
	isAdmin  bool          // resolved once at startup via GET /v1/users/me
	// reloadToken, when non-nil, re-reads the auth token from config (file + env)
	// so a token refreshed out of band (relay login) is picked up on a 401 without
	// restarting the process. Nil means no reload is attempted. Set once at
	// construction; read-only thereafter.
	reloadToken func() (string, error)
}
```

- [ ] **Step 4: Write the `Server.do` wrapper**

Create `internal/mcp/do.go`:

```go
package mcp

import (
	"context"
	"errors"

	"relay/internal/relayclient"
)

// do issues an API request through the shared client and, on an HTTP 401,
// attempts a single token reload-and-retry. The reload reads the current token
// from config via s.reloadToken; the retry runs at most once. A 401 that is not
// recoverable (no reader, empty/identical/still-expired token, or a second 401)
// is returned unchanged so MapError surfaces auth_expired. Non-401 errors pass
// straight through. This is the single chokepoint every tool routes through.
func (s *Server) do(ctx context.Context, method, path string, body, out any) error {
	err := s.client.Do(ctx, method, path, body, out)
	if !is401(err) {
		return err
	}
	if s.reloadToken == nil {
		return err
	}
	newTok, rerr := s.reloadToken()
	if rerr != nil || newTok == "" {
		return err
	}
	if newTok == s.client.Token() {
		return err
	}
	s.client.SetToken(newTok)
	return s.client.Do(ctx, method, path, body, out)
}

// is401 reports whether err is a relayclient.ResponseError with status 401.
func is401(err error) bool {
	var re *relayclient.ResponseError
	return errors.As(err, &re) && re.StatusCode == 401
}
```

- [ ] **Step 5: Route `callWhoami` through `s.do`**

In `internal/mcp/whoami.go`, change line 20 from `s.client.Do(` to `s.do(`:

```go
	if err := s.do(ctx, "GET", "/v1/users/me", nil, &resp); err != nil {
		return nil, MapError(err)
	}
```

(The startup probe in `NewServer` calls `callWhoami` before `s.reloadToken` is set, so it has nil reader and behaves exactly as today - a startup 401 is still fatal. This is the documented harmless interaction with the just-shipped startup probe.)

- [ ] **Step 6: Run tests to verify they pass**

Run: `cd D:/dev/relay/.claude/worktrees/happy-mendel-18687f && go test ./internal/mcp/... -run 'TestDo_|TestWhoami' -v`
Expected: PASS (all `TestDo_*` and the existing `TestWhoami_*`).

- [ ] **Step 7: Commit**

```bash
cd D:/dev/relay/.claude/worktrees/happy-mendel-18687f && \
git add internal/mcp/do.go internal/mcp/do_test.go internal/mcp/server.go internal/mcp/whoami.go && \
git commit -m "feat(mcp): reload-on-401 retry-once chokepoint (Server.do)"
```

---

## Task 3: route all remaining tool call sites through `Server.do`

Mechanical re-routing so the reload-retry applies uniformly. Each site changes
`s.client.Do(` to `s.do(`. `s.client.BaseURL()` stays. The tolerated-failure site
in `resources.go:64` also routes through `s.do` (harmless; failure still ignored).

**Files (modify each listed line):**
- `internal/mcp/cancel.go:27`
- `internal/mcp/jobs.go:59`, `:81`
- `internal/mcp/reservations.go:42`
- `internal/mcp/resources.go:56`, `:64`, `:79`
- `internal/mcp/run_now.go:31`
- `internal/mcp/schedules_read.go:52`, `:73`
- `internal/mcp/schedules_write.go:62`, `:102`, `:120`
- `internal/mcp/submit.go:31`
- `internal/mcp/tasks.go:36`, `:53`
- `internal/mcp/task_logs.go:47`
- `internal/mcp/wait.go:67`
- `internal/mcp/workers.go:52`, `:73`

- [ ] **Step 1: Confirm the only remaining direct `s.client.Do` is gone**

After editing, run a search to verify no tool calls `s.client.Do` directly (only `s.do` and `s.client.BaseURL`/`s.client.Token`/`s.client.SetToken` remain).

Run: `cd D:/dev/relay/.claude/worktrees/happy-mendel-18687f && go run ./... 2>$null; rg "s\.client\.Do\(" internal/mcp`
(Use the Grep tool instead if `rg` is unavailable.)
Expected: no matches.

- [ ] **Step 2: Apply the edits**

For each line listed above, replace `s.client.Do(` with `s.do(` - the argument list is identical. Example for `internal/mcp/submit.go:31`:

```go
	if err := s.do(ctx, "POST", "/v1/jobs", spec, &resp); err != nil {
```

Example for `internal/mcp/resources.go:64` (tolerated failure - keep the `_ =`):

```go
	_ = s.do(ctx, "GET", "/v1/health", nil, &health) // tolerate failure
```

Example for `internal/mcp/wait.go:67` (inside the poll loop):

```go
		if err := s.do(ctx, "GET", path, nil, &lastResp); err != nil {
```

- [ ] **Step 3: Run the full mcp test suite**

Run: `cd D:/dev/relay/.claude/worktrees/happy-mendel-18687f && go test ./internal/mcp/... -v`
Expected: PASS (all existing tool tests still pass; behavior is unchanged on the happy path because `s.do` only diverges on a 401 with a reader set, and existing tests inject no reader).

- [ ] **Step 4: Commit**

```bash
cd D:/dev/relay/.claude/worktrees/happy-mendel-18687f && \
git add internal/mcp/ && \
git commit -m "refactor(mcp): route all tool calls through Server.do chokepoint"
```

---

## Task 4: wire the real config-reader from the CLI

Inject a `LoadConfig`-backed reader into the MCP server so production picks up a
refreshed token. Avoids the import cycle by having `cli` (which already imports
`mcp`) set the reader via a new exported setter on `Server`.

**Files:**
- Modify: `internal/mcp/do.go` (add exported `SetTokenReloader`)
- Modify: `internal/cli/mcp.go:21-24` (set the reader after `NewServer`)
- Test: `internal/cli/mcp_test.go` (create, asserting the wiring)

- [ ] **Step 1: Write the failing test**

Create `internal/cli/mcp_test.go`:

```go
package cli

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestMCPConfigReloader_ResolvesEnv verifies the reloader the CLI builds reads the
// token via LoadConfig (config file + env overrides). RELAY_TOKEN must win.
func TestMCPConfigReloader_ResolvesEnv(t *testing.T) {
	t.Setenv("RELAY_TOKEN", "from-env")
	t.Setenv("RELAY_URL", "http://x")
	tok, err := mcpTokenReloader()
	require.NoError(t, err)
	require.Equal(t, "from-env", tok)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd D:/dev/relay/.claude/worktrees/happy-mendel-18687f && go test ./internal/cli/... -run TestMCPConfigReloader -v`
Expected: FAIL - `mcpTokenReloader` undefined.

- [ ] **Step 3: Add the exported setter on `Server`**

In `internal/mcp/do.go`, append:

```go
// SetTokenReloader installs a config-backed token reloader used to recover from a
// mid-session 401. Call once after NewServer and before Run. Passing nil disables
// reload (the construction default).
func (s *Server) SetTokenReloader(fn func() (string, error)) {
	s.reloadToken = fn
}
```

- [ ] **Step 4: Wire it in the CLI**

In `internal/cli/mcp.go`, add the helper and the wiring. Replace the body of `Run` so it reads:

```go
		Run: func(ctx context.Context, args []string, cfg *Config) error {
			if cfg == nil || cfg.Token == "" || cfg.ServerURL == "" {
				return fmt.Errorf("not logged in — run `relay login` first (or set RELAY_URL and RELAY_TOKEN)")
			}
			srv, err := internalmcp.NewServer(cfg.ServerURL, cfg.Token)
			if err != nil {
				return err
			}
			srv.SetTokenReloader(mcpTokenReloader)
			return srv.Run(ctx, os.Stdin, os.Stdout)
		},
```

And add, in the same file:

```go
// mcpTokenReloader re-reads the auth token from config (file + RELAY_TOKEN env
// override) so a token refreshed out of band by `relay login` is picked up on a
// mid-session 401 without restarting the MCP process.
func mcpTokenReloader() (string, error) {
	cfg, err := LoadConfig()
	if err != nil {
		return "", err
	}
	return cfg.Token, nil
}
```

- [ ] **Step 5: Run the test to verify it passes**

Run: `cd D:/dev/relay/.claude/worktrees/happy-mendel-18687f && go test ./internal/cli/... -run TestMCPConfigReloader -v`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
cd D:/dev/relay/.claude/worktrees/happy-mendel-18687f && \
git add internal/mcp/do.go internal/cli/mcp.go internal/cli/mcp_test.go && \
git commit -m "feat(cli): wire config-backed token reloader into mcp server"
```

---

## Task 5: end-to-end on-disk reload test (no env override)

Proves the spec's headline acceptance: a token refreshed on disk by `relay login`
(via `SaveConfig`) takes effect on the next tool call. This exercises the real
`mcpTokenReloader` reading a config FILE (not env), using the existing
`configFilePathFn` override pattern. Lives in `internal/cli` because the reloader
and `configFilePathFn` are package-private there; it drives the mcp server through
its public surface.

**Files:**
- Test: `internal/cli/mcp_reload_test.go` (create)

- [ ] **Step 1: Write the failing/asserting test**

Create `internal/cli/mcp_reload_test.go`:

```go
package cli

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	internalmcp "relay/internal/mcp"
)

// TestMCP_OnDiskTokenReload_TakesEffect: the MCP server starts with an old token,
// a tool 401s, the config FILE is rewritten with a fresh token (as relay login
// would), and the next call through Server.do reloads from disk and succeeds.
func TestMCP_OnDiskTokenReload_TakesEffect(t *testing.T) {
	// Point config resolution at a temp file.
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	orig := configFilePathFn
	configFilePathFn = func() (string, error) { return path, nil }
	t.Cleanup(func() { configFilePathFn = orig })

	// Ensure no env override shadows the file token.
	t.Setenv("RELAY_TOKEN", "")
	t.Setenv("RELAY_URL", "")

	var mu sync.Mutex
	var testCalls []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/users/me" {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id": "u1", "email": "t@t", "name": "T", "is_admin": false,
			})
			return
		}
		if r.URL.Path == "/v1/jobs/j1" {
			tok := r.Header.Get("Authorization")
			mu.Lock()
			testCalls = append(testCalls, tok)
			mu.Unlock()
			if tok == "Bearer fresh" {
				_ = json.NewEncoder(w).Encode(map[string]any{"id": "j1", "status": "running"})
				return
			}
			w.WriteHeader(http.StatusUnauthorized)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "token expired"})
			return
		}
		http.Error(w, "unexpected: "+r.URL.Path, http.StatusNotFound)
	}))
	defer srv.Close()

	// Seed config file with the OLD token, then build the server with it.
	require.NoError(t, SaveConfig(&Config{ServerURL: srv.URL, Token: "old"}))
	mcpSrv, err := internalmcp.NewServer(srv.URL, "old")
	require.NoError(t, err)
	mcpSrv.SetTokenReloader(mcpTokenReloader)

	// Simulate `relay login` writing a fresh token to disk mid-session.
	require.NoError(t, SaveConfig(&Config{ServerURL: srv.URL, Token: "fresh"}))

	// Drive a tool call through the public test seam on the mcp server.
	var out map[string]any
	derr := mcpSrv.CallForTest(context.Background(), "GET", "/v1/jobs/j1", nil, &out)
	require.NoError(t, derr)
	require.Equal(t, "running", out["status"])

	mu.Lock()
	defer mu.Unlock()
	require.Equal(t, []string{"Bearer old", "Bearer fresh"}, testCalls)
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `cd D:/dev/relay/.claude/worktrees/happy-mendel-18687f && go test ./internal/cli/... -run TestMCP_OnDiskTokenReload -v`
Expected: FAIL - `mcpSrv.CallForTest` undefined (the public seam does not exist yet).

- [ ] **Step 3: Add a minimal exported test seam on `Server`**

`Server.do` is unexported and `internal/cli` cannot call it. Add a thin exported wrapper in a non-test file so the cross-package test can drive a real tool request through the reload path. In `internal/mcp/do.go`, append:

```go
// CallForTest issues a request through the same do chokepoint tools use, exposed
// so cross-package tests (internal/cli) can exercise the reload-on-401 path end to
// end. Production code uses the per-tool wrappers, not this.
func (s *Server) CallForTest(ctx context.Context, method, path string, body, out any) error {
	return s.do(ctx, method, path, body, out)
}
```

(Naming it `CallForTest` keeps its test-only intent explicit while remaining a real exported method - acceptable because there is no `//go:build test` tag mechanism here and the existing codebase already exposes test seams as package vars/methods, e.g. `SetBcryptCostForTest`, `waitPoll`.)

- [ ] **Step 4: Run to verify it passes**

Run: `cd D:/dev/relay/.claude/worktrees/happy-mendel-18687f && go test ./internal/cli/... -run TestMCP_OnDiskTokenReload -v`
Expected: PASS - two requests to `/v1/jobs/j1`, the first with `Bearer old` (401), the retry with `Bearer fresh` (200).

- [ ] **Step 5: Commit**

```bash
cd D:/dev/relay/.claude/worktrees/happy-mendel-18687f && \
git add internal/mcp/do.go internal/cli/mcp_reload_test.go && \
git commit -m "test(mcp): end-to-end on-disk token reload takes effect mid-session"
```

---

## Task 6: full verification

- [ ] **Step 1: Vet and unit tests for both packages**

Run:
```bash
cd D:/dev/relay/.claude/worktrees/happy-mendel-18687f && \
go vet ./internal/relayclient/... ./internal/mcp/... ./internal/cli/... && \
go test ./internal/relayclient/... ./internal/mcp/... ./internal/cli/...
```
Expected: PASS, no vet diagnostics.

- [ ] **Step 2: Race detector on the concurrency tests**

`-race` needs MSYS2 mingw64 gcc on this machine (per project memory
`reference_race_detector_toolchain`); the default Strawberry Perl gcc fails with
`exit 0xc0000139`. Run with the right compiler, or in a Linux Docker container.

Run (Git Bash):
```bash
cd /d/dev/relay/.claude/worktrees/happy-mendel-18687f && \
CC=/c/msys64/mingw64/bin/gcc.exe go test -race \
  ./internal/relayclient/... -run TestSetToken && \
CC=/c/msys64/mingw64/bin/gcc.exe go test -race \
  ./internal/mcp/... -run 'TestDo_ConcurrentReload_Race'
```
Expected: PASS with no `DATA RACE` reports.

- [ ] **Step 3: Confirm no stray direct client.Do in tools**

Use the Grep tool: pattern `s\.client\.Do\(`, path `internal/mcp`.
Expected: zero matches (all routed through `s.do`).

---

## Self-review against the spec

- **Make token mutable + thread-safe (spec "Where the swap lives"):** Task 1 adds `sync.RWMutex`, `SetToken`, `Token`; both reads (`Do`, `StreamEvents`) go through `Token()`. Backward compatible - the field is unexported and only `client.go` touches it; CLI/agent callers via `NewClient` are unaffected. Covered.
- **Single chokepoint vs per-call (spec "Routing the wrapper"):** there is no existing chokepoint (confirmed, 21 direct sites). Task 2 introduces `Server.do`; Task 3 routes every site through it. Covered.
- **Reload reads from config via injectable reader (spec "Where the swap lives"):** `reloadToken func() (string, error)` field, set via `SetTokenReloader` (Task 4); real reader `mcpTokenReloader` reuses `LoadConfig` (file + env), wired in `cli/mcp.go`. Import cycle avoided (cli imports mcp, not the reverse). Covered.
- **Swap only if non-empty AND different; retry exactly once; second 401 -> auth_expired; no loop (spec data flow 2-3):** `Server.do` algorithm steps 3-6; tested by `TestDo_ReloadOn401_RetrySucceeds`, `_StillExpired`, `_IdenticalToken_NoRetry`, `_EmptyReload_NoRetry`, `_NilReader_NoRetry`. Covered.
- **Non-401 passthrough (spec):** `is401` gate; `TestDo_Non401_Passthrough` asserts reader not invoked, single request. Covered.
- **Concurrency / -race (spec test 5, acceptance):** `TestSetToken_ConcurrentRace` and `TestDo_ConcurrentReload_Race`, run under `-race` in Task 6. Covered.
- **Regression test distinguishes the fix (spec note, memory `regression_test_must_distinguish_fix`):** `TestDo_ReloadOn401_RetrySucceeds` and the on-disk Task 5 test assert the NEW token byte value appears on the retry, not merely that a second request happened. Covered.
- **Startup callWhoami still succeeds in tests (spec note):** all mcp tests use `whoamiHandler`/`newWhoamiBackend` so `NewServer`'s startup probe returns 200; the probe runs with nil `reloadToken` (set after construction) so startup behavior is unchanged. Covered.
- **`isAdmin` not re-resolved mid-session (spec scope guard):** `Server.do` never touches `isAdmin`. Covered.
- **Acceptance "relay login mid-session takes effect, no restart":** Task 5 end-to-end on-disk reload test. Covered.

## Verify commands (summary)

```bash
# Unit + vet (no Docker, no race toolchain needed)
go vet ./internal/relayclient/... ./internal/mcp/... ./internal/cli/...
go test ./internal/relayclient/... ./internal/mcp/... ./internal/cli/...

# Race (MSYS2 mingw64 gcc required on Windows, per project memory)
CC=/c/msys64/mingw64/bin/gcc.exe go test -race ./internal/relayclient/... -run TestSetToken
CC=/c/msys64/mingw64/bin/gcc.exe go test -race ./internal/mcp/... -run TestDo_ConcurrentReload_Race
```
