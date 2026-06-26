# MCP Discovery-Time Role Filtering - Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A non-admin MCP session must not see the admin-only tool `relay_list_reservations` in its tool list; an admin session sees it and can call it. The server-side `forbidden` enforcement stays untouched as the authoritative fallback.

**Architecture:** `NewServer` resolves the caller's identity once at startup with a single `GET /v1/users/me` (reusing the existing `callWhoami` logic), stores `is_admin` on the `Server`, and registers `relay_list_reservations` only when `is_admin == true`. A failed startup whoami makes `NewServer` return the error (the CLI already treats a `NewServer` error as a clean fatal exit). Adding the startup fetch means every test that builds a `Server` now needs its backend to answer `/v1/users/me`; a shared test helper supplies that so the change is not copy-pasted across 16 files.

**Tech Stack:** Go, `github.com/modelcontextprotocol/go-sdk` v1.6.0, testify. Backend Go only - no frontend, no `.sql`, no `.proto`, no `make generate`.

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

## The change (validated against the code)

Confirmed signatures and facts:

- `internal/mcp/server.go:26` `func NewServer(serverURL, token string) (*Server, error)`. It validates non-empty url/token, builds `relayclient.NewClient(serverURL, token)`, builds the SDK server, then calls `s.registerTools()` and `s.registerResources()`. **Today it does zero network I/O.**
- `internal/mcp/server.go:18` `type Server struct { client *relayclient.Client; mcp *mcpsdk.Server; waitPoll time.Duration }`.
- `internal/mcp/server.go:61` `registerTools()` calls `s.registerReservations()` at line 69, unconditionally, among the other 11 registrations.
- `internal/mcp/whoami.go:18` `func (s *Server) callWhoami(ctx context.Context) (map[string]any, *ToolError)` already does `GET /v1/users/me` and returns a map whose `is_admin` key is `resp["is_admin"]` (the raw JSON value, a `bool` when the backend sends a JSON bool). **This is the exact function to reuse for the startup fetch.**
- `internal/mcp/reservations.go:18` `func (s *Server) registerReservations()` and `callListReservations` - the `forbidden` path (via `MapError` on a 403) stays unchanged; it is covered by `TestListReservations_Forbidden` (`reservations_test.go:30`) and `TestIntegration_ForbiddenAsNonAdmin` (`mcp_integration_test.go:225`).
- `internal/cli/mcp.go:21-24`: `srv, err := internalmcp.NewServer(...); if err != nil { return err }`. A `NewServer` error already exits the command cleanly. No CLI change required.
- `relayclient` (`internal/relayclient/client.go`): `NewClient(serverURL, token)`, `(c *Client) BaseURL() string`, `(c *Client) Do(ctx, method, path, body, out any) error`.

go-sdk v1.6.0 (verified in `C:/Users/chadv/go/pkg/mod/github.com/modelcontextprotocol/go-sdk@v1.6.0/mcp`):

- `func NewInMemoryTransports() (Transport, Transport)`, `(*Server).Connect(ctx, Transport, *ServerSessionOptions)`, `NewClient(*Implementation, *ClientOptions)`, `(*Client).Connect(...)`, `(*ClientSession).ListTools(ctx, *ListToolsParams) (*ListToolsResult, error)`. `ListToolsResult.Tools` is `[]*mcp.Tool`, each with a `.Name`.
- There is **no exported lister on the relay-owned `*mcpsdk.Server`** (`listTools` is unexported). The real discovery surface is the client `ListTools` call over an in-memory transport. **`connectClient(t *testing.T, s *Server) *mcpsdk.ClientSession` already exists** in `internal/mcp/delivery_test.go:16` and does exactly this wiring. We reuse it; we do NOT add a production test-only accessor.

## Test impact (required scope, not optional)

Adding a startup whoami to `NewServer` breaks every test that builds a `Server`, because their `httptest` backends only answer one tool path (and several use no backend at all). There are two distinct breakage classes:

1. **Backed tests** - construct `NewServer(srv.URL, ...)` against an `httptest.Server` whose handler strictly asserts a single path (e.g. `require.Equal(t, "/v1/reservations", r.URL.Path)`). The startup `/v1/users/me` probe would trip that assertion and fail construction. Affected files: `reservations_test.go`, `whoami_test.go`, `jobs_test.go`, `tasks_test.go`, `task_logs_test.go`, `workers_test.go`, `submit_test.go`, `cancel_test.go`, `wait_test.go`, `schedules_read_test.go`, `schedules_write_test.go`, `run_now_test.go`, `resources_test.go`, `delivery_test.go`.
2. **Backendless validation tests** - `s, _ := NewServer("http://x", "t")` then call a `callXxx` that returns a client-side `validation` error before any HTTP. These ignore the `NewServer` error today. Once `NewServer` does a startup whoami against the unroutable `http://x`, it returns `nil, err`, the test's `s` is nil, and the subsequent `s.callXxx(...)` panics. Exact sites: `jobs_test.go:66` (`TestGetJob_MissingID`), `tasks_test.go:30` (`TestListTasks_MissingJobID`), `task_logs_test.go:34` and `:40`, `wait_test.go:65` and `:71`, `submit_test.go:39`, `schedules_write_test.go:39` (`TestCreateSchedule_BadCron`).
3. **`server_test.go`** - `TestNewServer_ValidCredentials` (`:18`) builds `NewServer("http://localhost:8080", "tok")` with no backend; it must move to a whoami-backed `httptest.Server`. `TestNewServer_MissingCredentials` stays offline (empty url/token are rejected before any I/O).

**Shared helper to avoid N copies (Task 1).** We add one test helper file `internal/mcp/whoami_test_helper_test.go` exposing:

- `whoamiHandler(isAdmin bool, next http.HandlerFunc) http.HandlerFunc` - returns a handler that answers `GET /v1/users/me` with `{"id":"u1","email":"t@t","name":"T","is_admin":<isAdmin>}` and delegates every other request to `next`. Backed tests wrap their existing per-tool handler with this.
- `newWhoamiBackend(t *testing.T, isAdmin bool) *httptest.Server` - a standalone `httptest.Server` that only answers `/v1/users/me`. The backendless validation tests point `NewServer` at this server's URL instead of `http://x`, so construction succeeds and the client-side validation path is exercised exactly as before. The helper registers `t.Cleanup(srv.Close)`.

Both helpers live in `_test.go` (package `mcp`), so no production surface is added.

## File structure

- Modify: `internal/mcp/server.go` - add `isAdmin bool` field to `Server`; add the startup whoami fetch in `NewServer`; make `registerTools` conditional on `s.isAdmin` for reservations.
- Create: `internal/mcp/whoami_test_helper_test.go` - the two shared test helpers above.
- Create: `internal/mcp/registration_test.go` - the new admin/non-admin discovery tests and the startup-whoami-failure test.
- Modify (test backends): `reservations_test.go`, `whoami_test.go`, `jobs_test.go`, `tasks_test.go`, `task_logs_test.go`, `workers_test.go`, `submit_test.go`, `cancel_test.go`, `wait_test.go`, `schedules_read_test.go`, `schedules_write_test.go`, `run_now_test.go`, `resources_test.go`, `delivery_test.go`, `server_test.go`.
- Modify (integration): `mcp_integration_test.go` - add discovery-filtering assertions.

---

## Task 1: Shared whoami test helpers

**Files:**
- Create: `internal/mcp/whoami_test_helper_test.go`

- [ ] **Step 1: Write the helper file**

```go
package mcp

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// whoamiHandler returns an http.HandlerFunc that answers GET /v1/users/me with a
// minimal identity payload (is_admin set per the argument) and delegates every
// other request to next. Backed mcp tests wrap their per-tool handler with this so
// the NewServer startup whoami probe succeeds without tripping single-path asserts.
func whoamiHandler(isAdmin bool, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/users/me" {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id": "u1", "email": "t@t", "name": "T", "is_admin": isAdmin,
			})
			return
		}
		next(w, r)
	}
}

// newWhoamiBackend stands up an httptest.Server that only answers /v1/users/me.
// Backendless validation tests point NewServer at it so construction succeeds and
// the client-side validation path is exercised as before. Closed via t.Cleanup.
func newWhoamiBackend(t *testing.T, isAdmin bool) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(whoamiHandler(isAdmin, func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "unexpected path: "+r.URL.Path, http.StatusNotFound)
	}))
	t.Cleanup(srv.Close)
	return srv
}
```

- [ ] **Step 2: Verify it compiles (the package test build will fail later refs, but this file alone must be syntactically valid)**

Run: `cd D:/dev/relay/.claude/worktrees/happy-mendel-18687f && go vet ./internal/mcp/...`
Expected: At this point the package will FAIL to build only because of unused helpers / not-yet-written production change. That is acceptable here; the helpers themselves must not be the cause. If vet complains specifically about `whoami_test_helper_test.go` syntax, fix it. (The helpers become used in Task 3 onward.)

- [ ] **Step 3: Commit**

```bash
cd D:/dev/relay/.claude/worktrees/happy-mendel-18687f && \
git add internal/mcp/whoami_test_helper_test.go && \
git commit -m "test(mcp): add shared whoami test backend helpers"
```

---

## Task 2: Failing discovery + startup-failure tests

This task writes the new behavioral tests against the not-yet-changed `NewServer`. They must compile and FAIL (admin/non-admin filtering not implemented; startup-failure not implemented). Reuse the existing `connectClient` helper in `delivery_test.go` for the real ListTools surface.

**Files:**
- Create: `internal/mcp/registration_test.go`

- [ ] **Step 1: Write the failing tests**

```go
package mcp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

// listedToolNames connects an in-memory MCP client to s and returns the set of
// tool names the server advertises - the real discovery surface a non-admin sees.
func listedToolNames(t *testing.T, s *Server) map[string]bool {
	t.Helper()
	cs := connectClient(t, s)
	res, err := cs.ListTools(context.Background(), nil)
	require.NoError(t, err)
	names := make(map[string]bool, len(res.Tools))
	for _, tool := range res.Tools {
		names[tool.Name] = true
	}
	return names
}

func TestRegistration_AdminSeesReservations(t *testing.T) {
	backend := newWhoamiBackend(t, true)
	s, err := NewServer(backend.URL, "t")
	require.NoError(t, err)

	names := listedToolNames(t, s)
	require.True(t, names["relay_list_reservations"], "admin must see relay_list_reservations")
	// A non-admin tool stays present for everyone.
	require.True(t, names["relay_whoami"], "relay_whoami must always be present")
}

func TestRegistration_NonAdminHidesReservations(t *testing.T) {
	backend := newWhoamiBackend(t, false)
	s, err := NewServer(backend.URL, "t")
	require.NoError(t, err)

	names := listedToolNames(t, s)
	require.False(t, names["relay_list_reservations"], "non-admin must NOT see relay_list_reservations")
	// Non-admin tools remain registered.
	require.True(t, names["relay_whoami"], "relay_whoami must always be present")
	require.True(t, names["relay_run_schedule_now"], "run_now is owner-or-admin and stays registered")
}

func TestNewServer_WhoamiFailureAtStartup(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"token expired"}`))
	}))
	defer backend.Close()

	s, err := NewServer(backend.URL, "t")
	require.Error(t, err)
	require.Nil(t, s)
}
```

- [ ] **Step 2: Run the new tests to verify they fail**

Run: `cd D:/dev/relay/.claude/worktrees/happy-mendel-18687f && go test ./internal/mcp/... -run "TestRegistration_|TestNewServer_WhoamiFailureAtStartup" -v -timeout 60s`
Expected: FAIL. `TestRegistration_NonAdminHidesReservations` fails because `relay_list_reservations` is still registered unconditionally. `TestNewServer_WhoamiFailureAtStartup` fails because `NewServer` does no I/O and returns a non-nil `s` with nil error. (`TestRegistration_AdminSeesReservations` may already pass - that is fine.)

- [ ] **Step 3: Commit the failing tests**

```bash
cd D:/dev/relay/.claude/worktrees/happy-mendel-18687f && \
git add internal/mcp/registration_test.go && \
git commit -m "test(mcp): failing discovery-time role-filtering tests"
```

---

## Task 3: Production change - startup whoami + conditional registration

**Files:**
- Modify: `internal/mcp/server.go:18-47` (Server struct + NewServer) and `:61-74` (registerTools)

- [ ] **Step 1: Add the `isAdmin` field to the Server struct**

Replace `internal/mcp/server.go:17-22`:

```go
// Server wraps the MCP SDK server and a relay API client.
type Server struct {
	client   *relayclient.Client
	mcp      *mcpsdk.Server
	waitPoll time.Duration // overridable in tests; 0 means use defaultWaitPoll
	isAdmin  bool          // resolved once at startup via GET /v1/users/me
}
```

- [ ] **Step 2: Fetch identity in NewServer and register conditionally**

Replace `internal/mcp/server.go:34-46` (from `impl := ...` through `return s, nil`):

```go
	impl := &mcpsdk.Implementation{
		Name:    "relay",
		Version: "0.1.0",
	}
	mcpServer := mcpsdk.NewServer(impl, nil)

	s := &Server{
		client: relayclient.NewClient(serverURL, token),
		mcp:    mcpServer,
	}

	// Resolve the caller identity once at startup so admin-only tools can be
	// filtered at registration time. A failure here (unreachable backend, expired
	// token) is fatal: the server is useless without an authenticated backend, and
	// internal/cli/mcp.go already exits cleanly on a NewServer error.
	who, terr := s.callWhoami(context.Background())
	if terr != nil {
		return nil, terr
	}
	if v, ok := who["is_admin"].(bool); ok {
		s.isAdmin = v
	}

	s.registerTools()
	s.registerResources()
	return s, nil
```

Note: `callWhoami` returns a `*ToolError`, which satisfies the `error` interface (`errors.go` defines `(*ToolError).Error()`). Returning `nil, terr` is correct - the CLI surfaces it as the command's exit error.

Add `"context"` to the import block if not already present. (`server.go` already imports `"context"` at line 7, so no import change is needed.)

- [ ] **Step 3: Make reservations registration conditional**

Replace `internal/mcp/server.go:61-74` (`registerTools`):

```go
// registerTools wires relay operations as MCP tools.
func (s *Server) registerTools() {
	s.registerWhoami()
	s.registerJobs()
	s.registerTasks()
	s.registerTaskLogs()
	s.registerWorkers()
	s.registerSchedules()
	s.registerSchedulesWrite()
	if s.isAdmin {
		// Admin-only: hidden from non-admin sessions at discovery time. The
		// server-side AdminOnly check and the forbidden ToolError in
		// callListReservations remain the authoritative enforcement.
		s.registerReservations()
	}
	s.registerSubmit()
	s.registerCancel()
	s.registerWait()
	s.registerRunNow()
}
```

- [ ] **Step 4: Run the new tests to verify they pass**

Run: `cd D:/dev/relay/.claude/worktrees/happy-mendel-18687f && go test ./internal/mcp/... -run "TestRegistration_|TestNewServer_WhoamiFailureAtStartup" -v -timeout 60s`
Expected: PASS (all three).

- [ ] **Step 5: Commit the production change**

```bash
cd D:/dev/relay/.claude/worktrees/happy-mendel-18687f && \
git add internal/mcp/server.go && \
git commit -m "feat(mcp): hide admin-only tools from non-admin sessions at discovery"
```

---

## Task 4: Repair backed unit tests (wrap handlers with whoami)

The production change broke the existing per-tool tests. This task makes them pass again by wrapping each strict-path handler with `whoamiHandler`. Do this file-by-file. The pattern is mechanical: change

```go
srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { ... }))
```

into

```go
srv := httptest.NewServer(whoamiHandler(true, func(w http.ResponseWriter, r *http.Request) { ... }))
```

Use `whoamiHandler(true, ...)` everywhere here except where a test specifically needs the constructed server to be admin or non-admin - for these per-tool call tests the admin flag does not affect the `callXxx` method under test, so `true` is fine and keeps reservations registered for the admin-only call tests.

**Files:** `reservations_test.go`, `whoami_test.go`, `jobs_test.go`, `tasks_test.go`, `task_logs_test.go`, `workers_test.go`, `submit_test.go`, `cancel_test.go`, `wait_test.go`, `schedules_read_test.go`, `schedules_write_test.go`, `run_now_test.go`, `resources_test.go`, `delivery_test.go`

- [ ] **Step 1: Wrap every backed handler**

For each file, for every `httptest.NewServer(http.HandlerFunc(func(w, r) {...}))` whose inner handler strictly asserts a single path, change the outer wrapper to `httptest.NewServer(whoamiHandler(true, func(w, r) {...}))`. Drop the now-redundant `http.HandlerFunc(...)` cast since `whoamiHandler` already returns an `http.HandlerFunc`. Example for `reservations_test.go:16-21`:

```go
	srv := httptest.NewServer(whoamiHandler(true, func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/v1/reservations", r.URL.Path)
		_ = json.NewEncoder(w).Encode(relayclient.PageEnvelope[map[string]any]{
			Items: []map[string]any{{"id": "r1", "name": "vfx"}},
		})
	}))
```

For `whoami_test.go:14`, the handler already asserts `/v1/users/me`. The startup probe and the explicit `callWhoami` call both hit that path, so the handler's `require.Equal(t, "/v1/users/me", r.URL.Path)` still holds for both. **Do not wrap `whoami_test.go` with `whoamiHandler`** - its handler already serves `/v1/users/me`; wrapping would shadow the test's own assertions. Leave `whoami_test.go` as-is and confirm in Step 2 it still passes (the startup probe is satisfied by its existing handler).

For `delivery_test.go:37`, the backend returns 401 unconditionally to drive a ToolError. That 401 would now also be the startup whoami response, making `NewServer` fail before the test can run. Fix by serving a successful whoami and 401 only for the tool path under test:

```go
	backend := httptest.NewServer(whoamiHandler(true, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "token expired"})
	}))
```

This serves a normal whoami at startup, then the 401 for `relay_whoami`'s later tool call. The test's CallTool of `relay_whoami` still hits the non-`/v1/users/me`... wait: `relay_whoami` itself calls `/v1/users/me`. For `delivery_test.go` the tool exercised is `relay_whoami`, so its tool-call path equals the startup path. Wrapping with `whoamiHandler(true, ...)` would make the tool call also succeed, defeating the test. Instead, switch the delivery test to drive a different tool whose path is NOT `/v1/users/me` (e.g. `relay_list_jobs` -> `/v1/jobs`) so the 401-on-tool-path assertion remains meaningful while the startup whoami succeeds:

```go
	backend := httptest.NewServer(whoamiHandler(true, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "token expired"})
	}))
	...
	res, err := cs.CallTool(context.Background(), &mcpsdk.CallToolParams{
		Name:      "relay_list_jobs",
		Arguments: map[string]any{},
	})
```

Keep the remaining assertions in `TestDelivery_ToolErrorReachesClient` unchanged (they assert the structured `auth_expired` ToolError reaches the client). Read `delivery_test.go` in full before editing to preserve its assertion block.

- [ ] **Step 2: Run each repaired file**

Run: `cd D:/dev/relay/.claude/worktrees/happy-mendel-18687f && go test ./internal/mcp/... -timeout 120s`
Expected: The backendless validation tests (Task 5) still fail/panic, but every backed test in this task passes. If a specific backed test fails on an unexpected `/v1/users/me`, its handler was not wrapped - wrap it.

- [ ] **Step 3: Commit**

```bash
cd D:/dev/relay/.claude/worktrees/happy-mendel-18687f && \
git add internal/mcp/reservations_test.go internal/mcp/jobs_test.go \
  internal/mcp/tasks_test.go internal/mcp/task_logs_test.go \
  internal/mcp/workers_test.go internal/mcp/submit_test.go \
  internal/mcp/cancel_test.go internal/mcp/wait_test.go \
  internal/mcp/schedules_read_test.go internal/mcp/schedules_write_test.go \
  internal/mcp/run_now_test.go internal/mcp/resources_test.go \
  internal/mcp/delivery_test.go && \
git commit -m "test(mcp): serve /v1/users/me in backed tool tests for startup whoami"
```

---

## Task 5: Repair backendless validation tests

These call `NewServer("http://x", "t")` and ignore the error. They must point at a whoami-only backend so `NewServer` succeeds, then exercise the same client-side validation.

**Files:** `jobs_test.go:66`, `tasks_test.go:30`, `task_logs_test.go:34` and `:40`, `wait_test.go:65` and `:71`, `submit_test.go:39`, `schedules_write_test.go:39`

- [ ] **Step 1: Replace each `http://x` construction**

For each listed site, change:

```go
	s, _ := NewServer("http://x", "t")
```

into:

```go
	backend := newWhoamiBackend(t, true)
	s, err := NewServer(backend.URL, "t")
	require.NoError(t, err)
```

Leave the subsequent `callXxx` validation assertion unchanged - the validation error is produced client-side before any HTTP request, so it still fires. Example for `tasks_test.go:29-33` (`TestListTasks_MissingJobID`):

```go
func TestListTasks_MissingJobID(t *testing.T) {
	backend := newWhoamiBackend(t, true)
	s, err := NewServer(backend.URL, "t")
	require.NoError(t, err)
	_, terr := s.callListTasks(context.Background(), listTasksArgs{})
	require.Equal(t, "validation", terr.Code)
}
```

Ensure `require` is imported in each file (all of these already import testify `require`; confirm by reading the import block before editing).

- [ ] **Step 2: Run the full unit suite**

Run: `cd D:/dev/relay/.claude/worktrees/happy-mendel-18687f && go test ./internal/mcp/... -timeout 120s`
Expected: PASS for the whole non-integration `internal/mcp` suite (server_test.go's `TestNewServer_ValidCredentials` is still the offline `http://localhost:8080` case and fails - fixed in Task 6).

- [ ] **Step 3: Commit**

```bash
cd D:/dev/relay/.claude/worktrees/happy-mendel-18687f && \
git add internal/mcp/jobs_test.go internal/mcp/tasks_test.go \
  internal/mcp/task_logs_test.go internal/mcp/wait_test.go \
  internal/mcp/submit_test.go internal/mcp/schedules_write_test.go && \
git commit -m "test(mcp): back validation tests with a whoami backend for startup probe"
```

---

## Task 6: Fix server_test.go construction test

`TestNewServer_ValidCredentials` builds `NewServer("http://localhost:8080", "tok")` with no backend; the startup whoami now fails. Point it at a whoami backend. `TestNewServer_MissingCredentials` stays offline (empty url/token are rejected before any I/O).

**Files:**
- Modify: `internal/mcp/server_test.go:18-22`

- [ ] **Step 1: Rewrite the valid-credentials test**

Replace `internal/mcp/server_test.go:18-22`:

```go
func TestNewServer_ValidCredentials(t *testing.T) {
	backend := newWhoamiBackend(t, true)
	s, err := NewServer(backend.URL, "tok")
	require.NoError(t, err)
	require.NotNil(t, s)
}
```

Leave `TestNewServer_MissingCredentials` unchanged - its three cases use empty url/token, which `NewServer` rejects before any network call.

- [ ] **Step 2: Run server_test.go**

Run: `cd D:/dev/relay/.claude/worktrees/happy-mendel-18687f && go test ./internal/mcp/... -run "TestNewServer_" -v -timeout 60s`
Expected: PASS (both `TestNewServer_MissingCredentials` and `TestNewServer_ValidCredentials`).

- [ ] **Step 3: Commit**

```bash
cd D:/dev/relay/.claude/worktrees/happy-mendel-18687f && \
git add internal/mcp/server_test.go && \
git commit -m "test(mcp): back NewServer valid-credentials test with whoami server"
```

---

## Task 7: Integration assertions for discovery filtering

The integration harness already seeds admin and non-admin tokens against a real relay server with a real `/v1/users/me`. Add an admin and a non-admin discovery assertion. Reuse `connectClient` (delivery_test.go) and the `listedToolNames` helper (registration_test.go) - both are in package `mcp` and compile under the `integration` build tag because the non-tagged `_test.go` files are part of the same test binary.

**Files:**
- Modify: `internal/mcp/mcp_integration_test.go` (append two tests)

- [ ] **Step 1: Add the integration tests**

Append to `internal/mcp/mcp_integration_test.go`:

```go
// TestIntegration_NonAdminHidesReservations verifies a non-admin session does not
// list relay_list_reservations against a real /v1/users/me.
func TestIntegration_NonAdminHidesReservations(t *testing.T) {
	baseURL, _, userToken, teardown := startRelayForMCP(t)
	defer teardown()

	s, err := NewServer(baseURL, userToken)
	require.NoError(t, err)

	names := listedToolNames(t, s)
	require.False(t, names["relay_list_reservations"],
		"non-admin must not list relay_list_reservations")
	require.True(t, names["relay_whoami"], "relay_whoami must always be present")
}

// TestIntegration_AdminListsAndCallsReservations verifies an admin session lists
// relay_list_reservations and can call it against a real backend.
func TestIntegration_AdminListsAndCallsReservations(t *testing.T) {
	baseURL, adminToken, _, teardown := startRelayForMCP(t)
	defer teardown()

	s, err := NewServer(baseURL, adminToken)
	require.NoError(t, err)

	names := listedToolNames(t, s)
	require.True(t, names["relay_list_reservations"],
		"admin must list relay_list_reservations")

	_, terr := s.callListReservations(context.Background(), listReservationsArgs{})
	require.Nil(t, terr, "admin call to reservations must succeed: %v", terr)
}
```

`context` and `require` are already imported in `mcp_integration_test.go`. Confirm `listedToolNames` is in scope (it is defined in `registration_test.go`, same package).

- [ ] **Step 2: Run the integration suite (requires Docker)**

Run: `cd D:/dev/relay/.claude/worktrees/happy-mendel-18687f && go test -tags integration -p 1 ./internal/mcp/... -run "TestIntegration_NonAdminHidesReservations|TestIntegration_AdminListsAndCallsReservations|TestIntegration_ForbiddenAsNonAdmin" -v -timeout 300s`
Expected: PASS. (`TestIntegration_ForbiddenAsNonAdmin` re-run confirms the server-side fallback is still intact: a non-admin direct `callListReservations` still gets `forbidden`.) If Docker is unavailable, note it; the non-integration suite (Task 5/6) covers the behavior with fakes.

- [ ] **Step 3: Commit**

```bash
cd D:/dev/relay/.claude/worktrees/happy-mendel-18687f && \
git add internal/mcp/mcp_integration_test.go && \
git commit -m "test(mcp): integration assertions for discovery-time role filtering"
```

---

## Task 8: Full verification

- [ ] **Step 1: Vet and full unit suite**

Run: `cd D:/dev/relay/.claude/worktrees/happy-mendel-18687f && go vet ./internal/mcp/... && go test ./internal/mcp/... -timeout 180s`
Expected: vet clean; all non-integration `internal/mcp` tests PASS. (These are not `//go:build`-gated, so they run on Windows and in Docker.)

- [ ] **Step 2: Confirm no unintended edits outside internal/mcp**

Run: `cd D:/dev/relay/.claude/worktrees/happy-mendel-18687f && git status --short`
Expected: only files under `internal/mcp/` and this plan doc are modified. `internal/cli/mcp.go` is NOT modified (it already propagates the NewServer error).

- [ ] **Step 3: Final integration run (Docker, optional but preferred)**

Run: `cd D:/dev/relay/.claude/worktrees/happy-mendel-18687f && go test -tags integration -p 1 ./internal/mcp/... -timeout 600s`
Expected: PASS.

---

## Verify commands (summary)

- `go vet ./internal/mcp/...`
- `go test ./internal/mcp/... -timeout 180s` (non-gated; runs on Windows and in Docker)
- `go test -tags integration -p 1 ./internal/mcp/... -timeout 600s` (requires Docker Desktop)

## Acceptance-criteria mapping

| Spec acceptance criterion | Covered by |
|---|---|
| 1. Non-admin: `relay_list_reservations` absent | `TestRegistration_NonAdminHidesReservations` (Task 2), `TestIntegration_NonAdminHidesReservations` (Task 7) |
| 2. Admin: present and functional | `TestRegistration_AdminSeesReservations` (Task 2), `TestIntegration_AdminListsAndCallsReservations` (Task 7) |
| 3. Whoami failure at startup -> `NewServer` errors, clean exit | `TestNewServer_WhoamiFailureAtStartup` (Task 2); CLI exit path unchanged in `internal/cli/mcp.go` |
| 4. All non-admin tools registered for both sessions | `relay_whoami`/`relay_run_schedule_now` asserts in `TestRegistration_*` (Task 2) |
| 5. Server-side `forbidden` path unchanged | `TestListReservations_Forbidden` (unchanged), `TestIntegration_ForbiddenAsNonAdmin` re-run (Task 7) |

## Invariants check

- Single job-spec pipeline: not touched (read-side tool registration only).
- Auth authority stays server-side: `AdminOnly` middleware and the `forbidden` ToolError in `callListReservations` are unchanged; discovery filtering is cosmetic.
- One bounded sender / epoch fence / identity-checked teardown / no interior pointers / single JSON entry point: none in this change's path. The only new behavior is one client-side `GET /v1/users/me` at MCP startup.
- No `.sql`/`.proto` edits, so no `make generate` step.
