# Authenticated-route rate limiting Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add one per-user rate-limit bucket, shared across the three HTTP routes that let a non-admin principal buy task execution, keyed on the authenticated user rather than the source address.

**Architecture:** A second constructor beside `RateLimit` in `internal/api/ratelimit.go` reuses the existing unexported `rateLimiter` verbatim and swaps only the key function. It mounts *inside* the auth chain (`auth(userLimit(h))`) because nothing has resolved a principal before `BearerAuth` runs. Sizing reaches `api.Server` as two named fields set by `buildHTTPServer`, not as two more positional arguments to `api.New`.

**Tech Stack:** Go 1.26, stdlib `net/http` and `net/http/httptest`, `testify/require` in `cmd/relay-server` tests and plain `testing` in `internal/api/ratelimit_test.go` (matching each file's existing style), `pgx/v5/pgtype`.

**Source spec:** `docs/superpowers/specs/2026-09-04-authenticated-route-rate-limiting.md` (committed at 21fde15).
**Closes:** `docs/backlog/bug-2026-08-29-post-v1-jobs-is-not-rate-limited.md`

---

## Slice independence declaration

**One sequential backend slice. Nothing here is parallelisable and there is no frontend slice.**

- There is no `web/` work. Two `web/` facts were checked and both mean "no change":
  - `web/src/jobs/JobsPage.tsx`'s comment "GET /v1/jobs carries no rate limit" stays TRUE - this slice covers three write routes only. **Do not edit it.**
  - `web/playwright.config.ts` pins login/register limits for the e2e server. Measured: `web/e2e/fixtures.ts`'s `seedAll` performs exactly ONE `POST /v1/jobs` per run, against a `60:10s` default. No raise is needed and none is added. **Do not add one.**
- Every task below shares `internal/api/ratelimit.go`, `internal/api/server.go` or `cmd/relay-server/`, so two agents working concurrently would collide on the same files. Run tasks in order in one lane.

Sequencing constraint inside the lane: **Task 5 (the `stubAdminRow` UUID arm) MUST land before Tasks 6, 7, 8 and 9.** Every default-lane wiring proof depends on `BearerAuth` producing an `AuthUser` with a renderable id, which the shared stub does not do at HEAD.

---

## What this plan refutes in the spec

The spec is largely correct and its three load-bearing refutations were re-verified. Six things did not survive contact with the tree. Each is a change to what the plan does, not a note.

**R1. `GetTokenWithUserRow` has THREE `pgtype.UUID` fields, not two.** The spec's prerequisite says "`GetTokenWithUserRow`'s two `pgtype.UUID` fields stay invalid" and prescribes "two distinct fixed valid UUIDs". `store.GetTokenWithUserRow` declares `TokenID`, `TokenUserID` and `UserID`, and `GetTokenWithUser`'s `row.Scan` passes all three. **The arity check must be `!= 3`.** Everything else in the prerequisite holds: `Scan` really does fill only `*bool` and `*string`, `pgtype` really is already imported in `counters_wiring_test.go`, and the existing `bools != 1` check really is the shape to mirror. The wiring proof does not collapse; the number in it was wrong.

**R2. The shared-bucket decision does NOT need the integration lane, and the spec's "it can only live here" is false.** The spec puts the one-bucket proof in a lane CI does not run, on the argument that "reaching a success code on three different handlers needs three real handlers and a database". That is true of the SUCCESS codes and irrelevant to the property. A refusal is issued *before* the handler runs, so proving route B draws on route A's bucket needs no handler at all: spend the budget on `POST /v1/jobs` with body `{}` (400 from `ValidateJobSpec`, no pool), then request `POST /v1/jobs/not-a-uuid/retry`. Wrapped and shared, it is 429; unwrapped or separately wrapped, `parseUUID` answers 400 with no database. Two distinct codes, no container. **Task 7 moves this into the default lane.** The integration test survives as confirmation, not as the sole guard.

**R3. The spec's integration test 7 cannot answer 200 on `retry`.** `handleRetryJob` admits only a job whose status is `done` or `failed`; a freshly created job is `pending` and the handler answers **409**. So "each answering its own success code" is not reachable without driving a job and its tasks to a terminal state. Task 10 restructures it: two real 201s (`POST /v1/jobs`, `POST /v1/scheduled-jobs/{id}/run-now`) at limit 2, then `retry` at 429 - and the 409-versus-429 split is what discriminates, since an unbucketed retry on a pending job answers 409.

**R4. The count-bounds SPEC does not become false, and must not be edited.** The spec's refutation 6 says "The count-bounds spec's residual paragraph and the `maxCommandsPerJob` doc comment both become false. Both state that `POST /v1/jobs` carries no rate limit." Searched: `docs/superpowers/specs/2026-08-29-task-and-command-count-bounds.md` does not contain that phrase anywhere. Its four occurrences of "rate limit" are at the resolved-questions block, and what they say is *"half 2 (rate-limiting `POST /v1/jobs`) is DEFERRED, not abandoned ... this ships before rate limiting, not instead of it"*. That is a scheduling decision this slice **fulfils**, not falsifies. It is also a record of a moment, which CLAUDE.md's comment rules say must not drift. **No spec doc is edited.** Only `internal/jobspec/jobspec.go`'s `maxCommandsPerJob` comment is actually false, and Task 12 fixes it.

**R5. The spec has no guard for the `> 0` config guard, and removing that guard PANICS.** `rateLimiter.allow` computes `retry := rl.window - now.Sub(hits[0])` inside `if len(hits) >= rl.limit`. With `limit == 0` that branch is taken on an empty slice and `hits[0]` panics. So `if s.JobSubmitLimitN > 0 && s.JobSubmitLimitWin > 0` is load-bearing, not cosmetic - and the "off for a Go caller" state it produces is exactly what `internal/api/server_test.go`'s `&Server{}`, `newTestServer`'s `api.New(..., 0,0,0,0)` and `internal/cli`'s `relayharness_integration_test.go` all rely on. Nothing in the spec's test set would go red if the guard were deleted. **Task 9 adds `TestBuildHTTPServer_AZeroLimitLeavesTheBucketOff`.**

**R6. Two smaller corrections.**
- The spec's instruction to "order the shared-address pair before any other assertion in the file's new cases" misapplies the poisoned-input-first rule. These are four separate `func Test...` bodies, each constructing its own limiter; Go runs them independently and there is no early exit spanning them. The rule DOES apply where a plan task uses a table, so every table below uses `t.Run` subtests (a sibling's `t.Fatalf` does not skip later rows) and still orders the fail-closed rows first.
- The spec hedges test 6 with "assert 'not 429' rather than a specific code, because the second request's outcome depends on `handleCreateScheduledJob`'s own validation order". Read: with body `{}` that handler answers 400 "name is required" after `readJSON` and before any pool use, deterministically. Task 8 asserts **400** exactly. A loosened assertion loses coverage even when the loosening is correct.

**Refinement, flagged so the conductor can reject it in one line.** The spec prescribes `userRateLimitKey(u AuthUser) string`, with the read bucket calling `rl.allow(userRateLimitKey(u))`. This plan uses `(string, bool)` instead, on one argument: with two return values that one-line call site **does not compile**, so the `?q=` read bucket's author is forced to confront the empty-key case rather than silently pooling every unidentified principal into a `""` bucket. If the conductor prefers the spec's shape, change the signature in Task 2 and add the `== ""` check at every call site.

### Acceptance criteria already true at HEAD

The source item's third criterion - *"If the key is not the authenticated user, the proxy behaviour is stated: what one shared source address does to unrelated callers"* - is **conditional, and its condition is false**. The key IS the authenticated user, so the criterion is vacuously satisfied and requires no edit. Do not invent work for it. (The doc comment in Task 3 states the proxy behaviour anyway, because it is the strongest argument for the key that was chosen; that is the spec's requirement, not this criterion's.)

No other criterion is green at HEAD. The item's first criterion is behaviourally red: Task 1 records it.

### Symbols verified to exist, by reading

`rateLimiter`, `rateLimiter.allow`, `rateLimiter.gcLoop`, `ParseRateLimit`, `RateLimit`, `clientIP` (all `internal/api/ratelimit.go`); `AuthUser`, `UserFromCtx`, `ctxWithUser` (`internal/api/context.go`); `BearerAuth`, `AdminOnly` (`internal/api/middleware.go`); `uuidStr`, `writeError`, `readJSON`, `Server.Handler`, `api.New` (`internal/api/server.go`); `handleCreateJob`, `handleRetryJob` (`internal/api/jobs.go`); `handleCreateScheduledJob`, `handleRunScheduledJobNow`, `minScheduleInterval` (`internal/api/scheduled_jobs.go`); `ValidateJobSpec`, `CreateJobFromSpec` (`internal/api/job_spec.go`); `stubAdminDB`, `stubAdminRow`, `countersAssignmentSources`, `TestServerCountersIsWiredByMain` (`cmd/relay-server/counters_wiring_test.go`); `httpServerDeps`, `buildHTTPServer` (`cmd/relay-server/http_server.go`); `newTestServer`, `createTestUser`, `createTestToken` (`internal/api/api_test.go`, integration-tagged); `maxTasksPerJob`, `maxCommandsPerTask`, `maxCommandsPerJob` (`internal/jobspec/jobspec.go`).

---

## File structure

| File | Action | Responsibility |
|---|---|---|
| `internal/api/ratelimit.go` | Modify | `userRateLimitKey` and `UserRateLimit`, beside `RateLimit` so a reader of either keying argument sees the other. |
| `internal/api/ratelimit_test.go` | Modify | Default-lane middleware behaviour: the key, the fail-closed refusal, the sustainable rate. Runs in CI. |
| `internal/api/server.go` | Modify | Two `Server` fields; one limiter built once in `Handler`; three route wraps. |
| `cmd/relay-server/counters_wiring_test.go` | Modify | Shared-helper prerequisite ONLY: `stubAdminRow.Scan` gains a `*pgtype.UUID` arm plus an arity check. |
| `cmd/relay-server/job_submit_ratelimit_wiring_test.go` | Create | New untagged file. Every executed wiring proof: the configured limit reaches the bucket, the three routes share it, `POST /v1/scheduled-jobs` does not, a zero limit leaves it off. Runs in CI. |
| `cmd/relay-server/http_server.go` | Modify | Two `httpServerDeps` fields, two named-field assignments, and a correction to `buildHTTPServer`'s own doc comment. |
| `cmd/relay-server/main.go` | Modify | Parse `RELAY_JOB_SUBMIT_RATE_LIMIT`; pass the pair at the `httpServerDeps` call site. |
| `internal/api/job_submit_ratelimit_integration_test.go` | Create | `//go:build integration`. End-to-end through the real auth chain with real tokens. Does NOT run in CI. |
| `README.md` | Modify | One new env row; one corrected clause in `tasks[].retries`; one scoping fix in the `?q=` cost paragraph. |
| `internal/jobspec/jobspec.go` | Modify | `maxCommandsPerJob`'s doc comment only. **The three constants do not change.** |

**Do not touch:** `internal/jobspec/count_bounds_test.go` (its numbers are literals on purpose), `internal/store/*.sql.go`, `models.go`, `web/`, any spec doc, `internal/api/ratelimit.go`'s `RateLimit` / `ParseRateLimit` / `clientIP` / `rateLimiter`, `api.New`'s signature.

---

## Lane facts that decide where each test goes

Verified by reading `.github/workflows/go-ci.yml`:

- The `test` job runs `go test -race ./... -timeout 180s` with **no tags**. So every untagged package runs in CI, and every `//go:build integration` file does not.
- `make vet-integration` (`go vet -tags integration ./...`) type-checks the tagged code without running it.
- `internal/api/ratelimit_test.go` and `internal/api/server_test.go` are untagged: they run. `internal/api/api_test.go` and `testhelper_test.go` are integration-tagged, so `internal/api`'s handler harness is unavailable in the default lane.
- `cmd/relay-server/counters_wiring_test.go` is untagged and defines `stubAdminDB`, a `store.DBTX` that makes `BearerAuth` resolve any bearer token to an admin with no Postgres. That is the only Postgres-free seam in the repo that reaches a real route through `Server.Handler`.

**Nothing in this repo blocks a merge; `main` has no branch protection.** The lane facts decide where a guard is *seen*, not what is permitted.

**A second copy of `stubAdminDB` in `internal/api` was considered and rejected.** `internal/api/server_test.go` is `package api` and could set the unexported `q` field with its own `store.New(stub{})`, putting the route-wrap guard in the same package as the code. It is not done: one stub in two packages drifts, and `cmd/relay-server` gives the strictly stronger proof (it also covers `buildHTTPServer`'s assignments). The accepted cost is that `Server.Handler`'s route wraps are guarded from one package away, in a lane that runs on the same commit.

---

## Note to the implementer about every test body below

**These test bodies are guesses written by a planner who did not run them.** Treat them as intent plus the assertion that matters. For each one you MUST:

1. Run it and see it FAIL for the stated reason before you write the implementation. If it fails for a different reason, the plan is wrong and the tree wins - fix the test, and say so in the PR.
2. After it passes, run the named mutation and see it fail again. **Verify the mutation actually applied** (`git diff` the file) before recording a survivor, and run a control that should die.
3. **Never revert a mutation with `git checkout --`** - that discards the uncommitted guard under test. Copy the file aside and restore from the copy.

---

## Task 1: Measure and reproduce at HEAD

**Files:** none. This task commits nothing. Its output is two numbers that go in the PR body and decide Task 9's default value.

- [ ] **Step 1: Bring up a local server at HEAD**

```powershell
cd D:/dev/relay/.claude/worktrees/lane-jobs-ratelimit
./scripts/dev.ps1          # or your usual Postgres at postgres://relay:relay@127.0.0.1:5432
make build
$env:RELAY_DATABASE_URL = "postgres://relay:relay@127.0.0.1:5432/relay?sslmode=disable"
$env:RELAY_BOOTSTRAP_ADMIN = "admin@relay.test"
$env:RELAY_BOOTSTRAP_PASSWORD = "measure-me-please"
./bin/relay-server
```

In a second shell, log the CLI in:

```powershell
cd D:/dev/relay/.claude/worktrees/lane-jobs-ratelimit
./bin/relay login --server http://127.0.0.1:8080
```

- [ ] **Step 2: Time a 200-iteration `relay submit` loop**

This is the shape the default is sized against: a CLI for-loop paced by process start, not by the network. The spec calls the `60:10s` default "a judgement, not a measurement", and this step is what turns it into one.

```powershell
$sw = [System.Diagnostics.Stopwatch]::StartNew()
1..200 | ForEach-Object { ./bin/relay submit --detach examples/hello-windows.json | Out-Null }
$sw.Stop()
"{0} submissions in {1:N2}s = {2:N2}/s" -f 200, $sw.Elapsed.TotalSeconds, (200 / $sw.Elapsed.TotalSeconds)
```

Record the rate AND the machine (CPU, OS) in the PR. **A measurement without its input reads as the typical case.**

- [ ] **Step 3: Apply the settled escalation rule**

- Observed rate **at or below 6/s** -> the default stays `60:10s`.
- Observed rate **above 6/s** -> the default becomes `120:10s`.

Write the decision and the number into the PR body now, before any code exists to argue with it.

- [ ] **Step 4: Record the behavioural RED at HEAD**

The compile errors in Tasks 2 to 9 are a weak RED. This is the real reproduction: at HEAD an authenticated caller repeats a submission without limit.

```powershell
1..8 | ForEach-Object {
  $r = curl.exe -s -o NUL -w "%{http_code}`n" -X POST http://127.0.0.1:8080/v1/jobs `
    -H "Authorization: Bearer $env:RELAY_TOKEN" -H "Content-Type: application/json" `
    --data '{"name":"burst","tasks":[{"name":"t","command":["echo","x"]}]}'
  $r
}
```

Expected at HEAD: **eight `201`s, no `429`.** Paste that output into the PR. (Take `$env:RELAY_TOKEN` from `~/.relay/config.json`, or use `relay submit --detach` eight times and note that none is refused.)

- [ ] **Step 5: No commit**

Nothing changed on disk. Do not commit. Carry the four numbers forward.

---

## Task 2: `userRateLimitKey`

**Files:**
- Modify: `internal/api/ratelimit.go` (add below `clientIP`)
- Test: `internal/api/ratelimit_test.go` (append)

The bucket key is a package-level function, not an expression inside the middleware, because the `?q=` read bucket has to call `rl.allow` with this same value from *inside* a handler - a middleware that re-parsed the query string to decide "does this request carry a needle" would be a second implementation of `parseFilterQ`'s decision.

- [ ] **Step 1: Write the failing test**

Append to `internal/api/ratelimit_test.go`. You will need to add `"github.com/jackc/pgx/v5/pgtype"` to that file's import block.

```go
// TestUserRateLimitKey pins three properties of the bucket key, and the two
// fail-closed rows go first because they are the security half.
//
// THE LAST ROW IS THE TRANSPOSITION GUARD. AuthUser.ID and AuthUser.TokenID are
// adjacent fields of the same type, so `uuidStr(u.TokenID)` compiles and is
// per-token rather than per-user - and issueToken mints a fresh token per login
// without invalidating the previous one, so a token key is a fresh full bucket
// per login. Giving the two fields different values is what makes the assertion
// positional rather than a type coincidence.
func TestUserRateLimitKey(t *testing.T) {
	userID := pgtype.UUID{Bytes: [16]byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16}, Valid: true}
	const userIDStr = "01020304-0506-0708-090a-0b0c0d0e0f10"
	tokenID := pgtype.UUID{Bytes: [16]byte{0xaa, 0xbb, 0xcc, 0xdd}, Valid: true}

	tests := []struct {
		name   string
		u      AuthUser
		want   string
		wantOK bool
	}{
		{"zero AuthUser", AuthUser{}, "", false},
		// Bytes are non-zero and Valid is false: a key function that read Bytes
		// without consulting Valid would render a plausible uuid here.
		{"id present but not Valid", AuthUser{ID: pgtype.UUID{Bytes: [16]byte{1}, Valid: false}}, "", false},
		{"valid id", AuthUser{ID: userID}, userIDStr, true},
		{"key is the user id, not the token id", AuthUser{ID: userID, TokenID: tokenID}, userIDStr, true},
	}
	for _, tt := range tests {
		// t.Run, not a bare loop: a t.Fatalf in one row must not skip the rest.
		t.Run(tt.name, func(t *testing.T) {
			got, ok := userRateLimitKey(tt.u)
			if got != tt.want || ok != tt.wantOK {
				t.Fatalf("got (%q, %v), want (%q, %v)", got, ok, tt.want, tt.wantOK)
			}
		})
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/api/ -run TestUserRateLimitKey -v -timeout 30s`
Expected: **build failure**, `undefined: userRateLimitKey`. That is the honest RED for a symbol absent at HEAD; the behavioural RED is Task 1 Step 4.

- [ ] **Step 3: Write the minimal implementation**

Add to `internal/api/ratelimit.go`, directly after `clientIP`:

```go
// userRateLimitKey renders the bucket key for an authenticated principal.
//
// It is a package-level function rather than an expression inside UserRateLimit
// because a per-request read bucket has to call rl.allow with this same value
// from INSIDE its handler, at the point a needle has already been parsed. A
// middleware deciding that question for itself would be a second implementation
// of parseFilterQ's decision, and the two only have to disagree once for the
// expensive path to go unbudgeted.
//
// ok is false when the principal has no renderable id, and the second return
// value is the point: rl.allow(userRateLimitKey(u)) does not compile, so a
// caller cannot bucket an unidentified principal under "" by omission.
func userRateLimitKey(u AuthUser) (string, bool) {
	key := uuidStr(u.ID)
	return key, key != ""
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./internal/api/ -run TestUserRateLimitKey -v -timeout 30s`
Expected: PASS, four subtests.

- [ ] **Step 5: Run the mutations**

Copy `internal/api/ratelimit.go` aside first (`cp internal/api/ratelimit.go /tmp/rl.bak` or the scratchpad). Then, one at a time:

| Mutation | Expected |
|---|---|
| `key := uuidStr(u.TokenID)` | RED on "key is the user id, not the token id" |
| `return key, true` | RED on both fail-closed rows |
| `return uuidStr(u.ID), u.ID.Valid` | GREEN (behaviourally identical; this is a **control that must NOT die** - a battery where everything dies is a broken harness) |

Restore from the copy after each. Record all three results, including the green one.

- [ ] **Step 6: Commit**

```bash
git add internal/api/ratelimit.go internal/api/ratelimit_test.go
git commit -m "Add userRateLimitKey, the per-principal bucket key that fails closed"
```

---

## Task 3: `UserRateLimit`, the middleware, and the doc comment the item requires

**Files:**
- Modify: `internal/api/ratelimit.go` (add after `userRateLimitKey`)
- Test: `internal/api/ratelimit_test.go` (append)

- [ ] **Step 1: Write the failing test**

You will need `"context"` in the test file's imports.

```go
// TestUserRateLimit_ARequestWithNoRenderablePrincipalIsRefused pins the
// fail-closed half. This middleware is only correct inside the auth chain, so a
// request reaching it without a principal is a wiring fault; a pass-through
// would be a silent hole and a shared "" bucket would pool every such request
// into one budget.
//
// THE LIMIT IS 10, NOT 1, DELIBERATELY. At a limit of 1 the second request would
// be refused by the arithmetic whatever key it used, and the test would go green
// against the very mutation it exists to kill.
//
// `reached` is asserted, not only the status: a mutant that passes through and
// writes 401 afterwards would still have run the handler.
func TestUserRateLimit_ARequestWithNoRenderablePrincipalIsRefused(t *testing.T) {
	reached := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = true
		w.WriteHeader(http.StatusOK)
	})
	h := UserRateLimit(10, time.Minute)(next)

	cases := []struct {
		name string
		with func(context.Context) context.Context
	}{
		{"no AuthUser in context at all", func(ctx context.Context) context.Context { return ctx }},
		{"AuthUser whose id is not Valid", func(ctx context.Context) context.Context {
			return ctxWithUser(ctx, AuthUser{ID: pgtype.UUID{Bytes: [16]byte{9}, Valid: false}})
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reached = false
			req := httptest.NewRequest("POST", "/v1/jobs", nil)
			req = req.WithContext(tc.with(req.Context()))
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("got %d, want 401", rec.Code)
			}
			if reached {
				t.Fatal("the wrapped handler ran: a request with no renderable principal must be " +
					"refused, never passed through and never bucketed under \"\"")
			}
		})
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/api/ -run TestUserRateLimit_ARequestWithNoRenderablePrincipalIsRefused -v -timeout 30s`
Expected: build failure, `undefined: UserRateLimit`.

- [ ] **Step 3: Write the minimal implementation**

Add to `internal/api/ratelimit.go`, immediately after `userRateLimitKey` and therefore directly below `RateLimit`, so a reader of either keying argument sees the other. `strconv` and `time` are already imported.

```go
// UserRateLimit returns middleware that limits each AUTHENTICATED USER to
// `limit` requests per `window`, answering 429 with a Retry-After on breach.
//
// THE KEY IS THE PRINCIPAL BearerAuth RESOLVED, NOT THE SOURCE ADDRESS, and
// RateLimit's RemoteAddr argument above does not transfer. Before
// authentication there is no principal and the address is the one identifier
// the caller cannot choose; after it there is one, resolved server-side from a
// token-hash lookup and unforgeable in the same sense, and it is the unit the
// bounded cost belongs to - task rows and subprocess spawns are charged to a
// user's jobs, not to a network path. Keyed on the address instead, one office
// egress or load balancer collapses a whole studio into a single bucket, while
// one user with a workstation and a laptop gets two.
//
// IT MUST BE MOUNTED INSIDE THE AUTH CHAIN and outside any admin gate:
// auth(userLimit(admin(h))). Inside admin, a non-admin's rejected probes are
// free; outside it they are charged to the prober's own bucket. The cost of
// being inside auth is real and is not hidden: a refused request has already
// paid one GetTokenWithUser round trip, so this bounds repetition of expensive
// work and does not bound the auth lookup or request volume.
//
// A request carrying no renderable principal is REFUSED with 401, never passed
// through and never bucketed under "". This middleware is only correct inside
// the auth chain, so such a request is a wiring fault.
func UserRateLimit(limit int, window time.Duration) func(http.Handler) http.Handler {
	rl := &rateLimiter{
		windows: make(map[string][]time.Time),
		limit:   limit,
		window:  window,
	}
	go rl.gcLoop()
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			u, ok := UserFromCtx(r.Context())
			if !ok {
				writeError(w, http.StatusUnauthorized, "unauthorized")
				return
			}
			key, ok := userRateLimitKey(u)
			if !ok {
				writeError(w, http.StatusUnauthorized, "unauthorized")
				return
			}
			retry, allowed := rl.allow(key)
			if !allowed {
				w.Header().Set("Retry-After", strconv.Itoa(int(retry.Seconds())+1))
				writeError(w, http.StatusTooManyRequests, "rate limit exceeded")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
```

Check the comment against CLAUDE.md's rules before you commit it: no dates, no change history, no counts of anything elsewhere, no census of which routes are wrapped (that lives in `Server.Handler` and in README), no uniqueness claim about other code. The paragraph above states this function's own contract and one hazard about its sibling in the same file, which is allowed.

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./internal/api/ -run TestUserRateLimit -v -timeout 30s`
Expected: PASS, two subtests.

- [ ] **Step 5: Run the mutations**

| Mutation | Expected |
|---|---|
| delete the `if !ok` block after `UserFromCtx` (bind with `u, _ :=`) | RED on "no AuthUser in context at all": `reached` is true and the code is 200 |
| replace the `key, ok := userRateLimitKey(u)` block with `key := uuidStr(u.ID)` | RED on "AuthUser whose id is not Valid": `reached` is true |
| change `http.StatusUnauthorized` to `http.StatusForbidden` | RED on both (control) |

- [ ] **Step 6: Commit**

```bash
git add internal/api/ratelimit.go internal/api/ratelimit_test.go
git commit -m "Add UserRateLimit, a per-principal bucket that refuses an unidentified caller"
```

---

## Task 4: The keying tests and the sustainable rate

**Files:**
- Test only: `internal/api/ratelimit_test.go` (append)

Task 3's implementation already keys on the user, so these tests are green when written. **That does not make them worthless and it does not make them RED-first.** Their RED is produced by mutation, and the discriminating inputs survive into permanent tests, which is what the mutation proof requires. Say so plainly in the commit message: these were added green and their RED was measured by mutation, not by TDD ordering.

- [ ] **Step 1: Write the three tests**

```go
// TestUserRateLimit_TheSameUserFromTwoAddressesSharesOneBucket is the headline
// discriminator: the executable form of the doc comment's claim, and RED against
// the single most likely wrong implementation, which is reusing clientIP.
func TestUserRateLimit_TheSameUserFromTwoAddressesSharesOneBucket(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	h := UserRateLimit(1, time.Minute)(next)

	u := AuthUser{ID: pgtype.UUID{Bytes: [16]byte{7}, Valid: true}}

	first := httptest.NewRequest("POST", "/v1/jobs", nil)
	first.RemoteAddr = "10.0.0.1:1111"
	first = first.WithContext(ctxWithUser(first.Context(), u))
	rec1 := httptest.NewRecorder()
	h.ServeHTTP(rec1, first)
	if rec1.Code != http.StatusOK {
		t.Fatalf("first request: got %d want 200", rec1.Code)
	}

	// A DIFFERENT source address, the same principal. A studio artist moving
	// from a workstation to a laptop, or onto a VPN, must not get a fresh
	// budget: an IPv6 /64 makes that escape unlimited.
	second := httptest.NewRequest("POST", "/v1/jobs", nil)
	second.RemoteAddr = "203.0.113.9:2222"
	second = second.WithContext(ctxWithUser(second.Context(), u))
	rec2 := httptest.NewRecorder()
	h.ServeHTTP(rec2, second)
	if rec2.Code != http.StatusTooManyRequests {
		t.Fatalf("second request from a different address: got %d want 429 - the bucket is keyed on "+
			"the address, not on the principal", rec2.Code)
	}
	if rec2.Header().Get("Retry-After") == "" {
		t.Fatal("a refusal must carry Retry-After")
	}
}

// TestUserRateLimit_TwoUsersFromOneAddressDoNotShareABucket is the mirror
// property and the one an operator feels: a studio behind one office egress is
// not collapsed into a single budget.
//
// THE THIRD REQUEST IS NOT OPTIONAL. Two 200s at a limit of 1 are also what a
// middleware that does nothing produces, so without the third assertion this
// test is vacuous against exactly the implementation it is supposed to describe.
func TestUserRateLimit_TwoUsersFromOneAddressDoNotShareABucket(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	h := UserRateLimit(1, time.Minute)(next)

	const sharedAddr = "10.0.0.1:1111"
	alice := AuthUser{ID: pgtype.UUID{Bytes: [16]byte{1}, Valid: true}}
	bob := AuthUser{ID: pgtype.UUID{Bytes: [16]byte{2}, Valid: true}}

	send := func(u AuthUser) int {
		req := httptest.NewRequest("POST", "/v1/jobs", nil)
		req.RemoteAddr = sharedAddr
		req = req.WithContext(ctxWithUser(req.Context(), u))
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code
	}

	if got := send(alice); got != http.StatusOK {
		t.Fatalf("alice first: got %d want 200", got)
	}
	if got := send(bob); got != http.StatusOK {
		t.Fatalf("bob first, from alice's address: got %d want 200 - one egress must not collapse "+
			"unrelated callers into one bucket", got)
	}
	// The control: the limiter IS running and IS full for alice.
	if got := send(alice); got != http.StatusTooManyRequests {
		t.Fatalf("alice second: got %d want 429 - without this the two 200s above are also what a "+
			"middleware that does nothing produces", got)
	}
}

// TestUserRateLimit_ASustainableRateIsNotRefused is the "a normal submission
// rate is not refused" half of the acceptance criterion, in the only
// non-vacuous form: two requests under a limit of three would be green against a
// limiter that does nothing, while six at this spacing require the window to
// actually slide.
//
// THE TIMING IS SAFE IN ONE DIRECTION ONLY, AND IT IS THE RIGHT ONE. 30ms
// spacing under a 50ms window at limit 2 leaves one hit in the window per
// request. time.Sleep is guaranteed to sleep AT LEAST its duration, so a slow or
// coarse-grained scheduler only widens the gaps, which prunes more and admits
// more. It cannot make this test flaky-red.
func TestUserRateLimit_ASustainableRateIsNotRefused(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	h := UserRateLimit(2, 50*time.Millisecond)(next)

	u := AuthUser{ID: pgtype.UUID{Bytes: [16]byte{3}, Valid: true}}
	for i := 1; i <= 6; i++ {
		if i > 1 {
			time.Sleep(30 * time.Millisecond)
		}
		req := httptest.NewRequest("POST", "/v1/jobs", nil)
		req = req.WithContext(ctxWithUser(req.Context(), u))
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("request %d at a sustainable rate: got %d want 200", i, rec.Code)
		}
	}
}
```

- [ ] **Step 2: Run them and verify they PASS**

Run: `go test ./internal/api/ -run TestUserRateLimit -v -timeout 60s`
Expected: PASS. If any fails, Task 3's implementation is wrong, not these tests.

- [ ] **Step 3: Run the mutations - this is where these tests earn their place**

Restore from a copy each time, never with `git checkout --`.

| Mutation in `UserRateLimit` | Expected |
|---|---|
| `key, ok := clientIP(r), true` | RED on `TheSameUserFromTwoAddressesSharesOneBucket` (second is 200) **and** RED on `TwoUsersFromOneAddressDoNotShareABucket` (bob is 429) |
| `key, ok := "one-bucket", true` | RED on `TwoUsersFromOneAddressDoNotShareABucket` (bob is 429). Note it is GREEN on the two-addresses test - that pair is why both tests exist. |
| `key, ok := u.Email, true` | GREEN. **Control that must not die**: email is also per-principal. A battery with no survivors is a broken harness. |
| delete `next.ServeHTTP(w, r)` at the end | RED everywhere (harness sanity check) |

- [ ] **Step 4: Run the whole default lane for the package**

Run: `go test ./internal/api/ -timeout 120s`
Expected: PASS, including the six pre-existing `TestRateLimit_*` cases, which this slice must not have disturbed.

- [ ] **Step 5: Commit**

```bash
git add internal/api/ratelimit_test.go
git commit -m "Pin the user key: two addresses share a bucket, two users behind one address do not"
```

---

## Task 5: The shared-stub prerequisite

**Files:**
- Modify: `cmd/relay-server/counters_wiring_test.go` (`stubAdminRow.Scan`, plus a new helper and a new test)

At HEAD `stubAdminRow.Scan` fills only `*bool` and `*string`, so `GetTokenWithUserRow`'s **three** `pgtype.UUID` fields stay invalid, `uuidStr(AuthUser.ID)` renders `""`, and Task 3's middleware would refuse every request in Tasks 6 to 9 with a 401. **The spec says two UUID fields. It is three:** `TokenID`, `TokenUserID`, `UserID`.

This is a shared helper. Run the whole `cmd/relay-server` default lane after the change, not only the new test.

- [ ] **Step 1: Write the failing test**

Append to `cmd/relay-server/counters_wiring_test.go`:

```go
// TestStubAdminDB_ResolvesAUserWithARenderableID pins what the stub has to
// produce for any guard that reaches past AdminOnly. Filling only the bool left
// GetTokenWithUserRow's uuid fields invalid, so uuidStr(AuthUser.ID) rendered ""
// - fine for a route that only asks "is this an admin", and fatal for one whose
// behaviour depends on WHICH principal is calling.
//
// The distinctness assertion is the transposition guard: token_id and user_id
// are the same type, and an assertion that passes on either cannot tell a
// per-user control apart from a per-token one.
func TestStubAdminDB_ResolvesAUserWithARenderableID(t *testing.T) {
	row, err := store.New(stubAdminDB{}).GetTokenWithUser(context.Background(), "any")
	require.NoError(t, err)

	require.True(t, row.UserID.Valid, "AuthUser.ID comes from user_id; invalid renders as \"\"")
	require.True(t, row.TokenID.Valid, "AuthUser.TokenID comes from token_id")
	require.True(t, row.TokenUserID.Valid)
	require.NotEqual(t, row.TokenID, row.UserID,
		"token_id and user_id must differ, so an assertion satisfied by one cannot be satisfied by "+
			"the other")
	require.True(t, row.UserIsAdmin, "the admin bool must still be set: every counters test above "+
		"depends on it")
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `go test ./cmd/relay-server/ -run TestStubAdminDB_ResolvesAUserWithARenderableID -v -timeout 60s`
Expected: FAIL on `row.UserID.Valid` being false.

- [ ] **Step 3: Write the implementation**

Add the helper above `stubAdminRow`:

```go
// countersStubUUID is a fixed VALID uuid per destination position. A limiter or
// an authz check keyed on uuidStr(AuthUser.ID) buckets every request under ""
// when these are invalid, which is the state this exists to end.
func countersStubUUID(n byte) pgtype.UUID {
	var raw [16]byte
	raw[0] = 0xc0
	raw[15] = n
	return pgtype.UUID{Bytes: raw, Valid: true}
}
```

Then extend `Scan`. Keep the existing bool arm and its arity check exactly as they are; add the uuid arm and mirror the check.

```go
func (stubAdminRow) Scan(dest ...any) error {
	bools := 0
	uuids := 0
	for _, d := range dest {
		switch v := d.(type) {
		case *bool:
			*v = true
			bools++
		case *string:
			*v = "counters-wiring"
		case *pgtype.UUID:
			uuids++
			*v = countersStubUUID(byte(uuids))
		}
	}
	if bools != 1 {
		return fmt.Errorf("stubAdminDB: GetTokenWithUserRow has %d bool destinations, want exactly 1 "+
			"(user_is_admin); the row shape changed and this stub no longer authenticates an admin", bools)
	}
	if uuids != 3 {
		return fmt.Errorf("stubAdminDB: GetTokenWithUserRow has %d pgtype.UUID destinations, want "+
			"exactly 3 (token_id, token_user_id, user_id); the row shape changed and the ids this "+
			"stub fills no longer line up with the fields BearerAuth reads", uuids)
	}
	return nil
}
```

Note the ordering assumption stated plainly: the arm fills by position, so `TokenID` gets 1, `TokenUserID` gets 2, `UserID` gets 3. The arity check catches a field added or removed; it cannot catch a reorder, and nothing below depends on which of the three a given field got, only that `UserID` is valid and differs from `TokenID`.

- [ ] **Step 4: Run the whole package**

Run: `go test ./cmd/relay-server/ -v -timeout 120s`
Expected: PASS, including every pre-existing `TestBuildHTTPServer_*` and `TestServerCountersIsWiredByMain`. The counters tests are indifferent to these values, but this is a shared helper and that indifference is a claim, not a fact, until the lane is green.

- [ ] **Step 5: Run the mutations**

| Mutation | Expected |
|---|---|
| `if uuids != 2` | GREEN on the new test, and that is the point of running it: the arity number is not self-checking. Confirm by hand that `GetTokenWithUser`'s `row.Scan` passes three `*pgtype.UUID`, then restore `3`. |
| delete the `case *pgtype.UUID:` arm | RED on the new test |
| `*v = countersStubUUID(1)` for every position | RED on the distinctness assertion |

- [ ] **Step 6: Commit**

```bash
git add cmd/relay-server/counters_wiring_test.go
git commit -m "Fill the bearer-auth stub's uuid destinations, so a principal-keyed control can be tested"
```

---

## Task 6: `POST /v1/jobs` in the bucket, and the wiring proof

**Files:**
- Modify: `internal/api/server.go` (`Server` struct, `Handler`)
- Modify: `cmd/relay-server/http_server.go` (`httpServerDeps`, `buildHTTPServer`, and `buildHTTPServer`'s doc comment)
- Create: `cmd/relay-server/job_submit_ratelimit_wiring_test.go`

- [ ] **Step 1: Write the failing test**

Create `cmd/relay-server/job_submit_ratelimit_wiring_test.go`:

```go
package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"relay/internal/store"

	"github.com/stretchr/testify/require"
)

// postAsUser drives one request through the REAL http.Server buildHTTPServer
// returned, authenticated by stubAdminDB with no Postgres.
func postAsUser(t *testing.T, srv *http.Server, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("POST", path, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer any-token-the-stub-resolves")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rec, req)
	return rec
}

// submitBucketServer builds a server whose only configured subsystem is the
// job-submit bucket. pool is nil on purpose: every request below is answered
// before any pool use, and stubAdminDB panics on any statement other than the
// bearer-auth lookup, so a handler that grew a query fails loudly here.
func submitBucketServer(n int, win time.Duration) *http.Server {
	return buildHTTPServer(httpServerDeps{
		addr:              "127.0.0.1:0",
		q:                 store.New(stubAdminDB{}),
		jobSubmitLimitN:   n,
		jobSubmitLimitWin: win,
	})
}

// TestBuildHTTPServer_TheJobSubmitBucketIsWiredWithTheConfiguredLimit is the
// strongest available wiring guard, and it is worth naming what it covers that a
// source scan does not: the route is wrapped, the composition order puts the
// limiter AFTER BearerAuth, and the limiter uses the value buildHTTPServer was
// GIVEN rather than a freshly constructed one.
//
// The 400 is load-bearing. ValidateJobSpec refuses `{}` for a missing name
// before handleCreateJob opens a transaction, so the first two requests prove
// they reached the real handler with no database at all. A 429 there would mean
// the wired count is smaller than the configured one; a 401 would mean the
// limiter sits OUTSIDE the auth chain and never sees a principal.
func TestBuildHTTPServer_TheJobSubmitBucketIsWiredWithTheConfiguredLimit(t *testing.T) {
	srv := submitBucketServer(2, time.Minute)

	for i := 1; i <= 2; i++ {
		rec := postAsUser(t, srv, "/v1/jobs", `{}`)
		require.Equal(t, http.StatusBadRequest, rec.Code,
			"request %d must reach handleCreateJob and be refused by ValidateJobSpec. body: %s",
			i, rec.Body.String())
	}

	rec := postAsUser(t, srv, "/v1/jobs", `{}`)
	require.Equal(t, http.StatusTooManyRequests, rec.Code,
		"the third request must be refused by the bucket buildHTTPServer was GIVEN. A deleted "+
			"assignment, a hard-coded count and an unwrapped route all answer 400 here. body: %s",
		rec.Body.String())
	require.NotEmpty(t, rec.Header().Get("Retry-After"),
		"a refusal must tell the caller when to come back")
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `go test ./cmd/relay-server/ -run TestBuildHTTPServer_TheJobSubmitBucketIsWired -v -timeout 60s`
Expected: build failure, `unknown field jobSubmitLimitN in struct literal`.

- [ ] **Step 3: Write the implementation - three files**

**3a. `internal/api/server.go`, the `Server` struct.** Add after `RegisterLimitWin`:

```go
	// JobSubmitLimitN and JobSubmitLimitWin size the per-user bucket over the
	// routes that let one principal buy task execution. Named fields rather
	// than two more positional arguments to New, which already takes four
	// same-typed arguments in a row; an int and a time.Duration cannot be
	// transposed without a compile error.
	//
	// Zero on EITHER leaves the bucket off, which is what a Go caller that
	// builds a Server directly wants - and the guard in Handler is not
	// cosmetic: rateLimiter.allow indexes hits[0] whenever len(hits) >= limit,
	// so a zero limit panics on the first request.
	JobSubmitLimitN   int
	JobSubmitLimitWin time.Duration
```

**3b. `internal/api/server.go`, `Handler`.** Add after `admin := AdminOnly`:

```go
	// ONE bucket over the routes that let a non-admin buy EXECUTION, and it is
	// shared on purpose: the quantity bounded is how much execution one
	// principal can buy per unit time, and it does not care which verb bought
	// it. Three instances would triple the ceiling and let a caller alternate
	// between the routes to stay under all three.
	//
	// Built once here, not per route: UserRateLimit starts a gc goroutine that
	// is never stopped. The rule for a future wrap is auth(userLimit(admin(h))),
	// so a non-admin's rejected probes are charged to the prober's own bucket
	// instead of being free.
	userLimit := func(h http.Handler) http.Handler { return h }
	if s.JobSubmitLimitN > 0 && s.JobSubmitLimitWin > 0 {
		userLimit = UserRateLimit(s.JobSubmitLimitN, s.JobSubmitLimitWin)
	}
```

and change the `POST /v1/jobs` line only:

```go
	mux.Handle("POST /v1/jobs", auth(userLimit(http.HandlerFunc(s.handleCreateJob))))
```

**3c. `cmd/relay-server/http_server.go`.** Add to `httpServerDeps`, after `registerLimitWin`:

```go
	jobSubmitLimitN   int
	jobSubmitLimitWin time.Duration
```

Add to `buildHTTPServer`, beside the other named-field assignments:

```go
	s.JobSubmitLimitN = d.jobSubmitLimitN
	s.JobSubmitLimitWin = d.jobSubmitLimitWin
```

And correct `buildHTTPServer`'s own doc comment, which currently reads *"Deleting any of the three assignments below - Metrics, StaticHandler, AllowSelfRegister - is likewise green everywhere."* That sentence carries a count of code elsewhere and becomes false the moment two guarded assignments join the three unguarded ones. Replace that bullet with:

```go
//   - Deleting the Metrics, StaticHandler or AllowSelfRegister assignment below
//     is green everywhere. The JobSubmitLimit pair is not:
//     TestBuildHTTPServer_TheJobSubmitBucketIsWiredWithTheConfiguredLimit drives
//     a real request through this function's output at a known limit and is RED
//     on a deleted, hard-coded or substituted value. countersAssignmentSources
//     does NOT cover it - that walk is specific to s.Counters assignments and
//     does not generalize.
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./cmd/relay-server/ -run TestBuildHTTPServer_TheJobSubmitBucketIsWired -v -timeout 60s`
Expected: PASS.

Then the packages this touched:
Run: `go test ./internal/api/ ./cmd/relay-server/ -timeout 180s`
Expected: PASS. In particular `TestServerCountersIsWiredByMain` must stay green - its `countersAssignmentSources` walk only inspects `s.Counters.<X> = ...` assignments, and `s.JobSubmitLimitN` has a plain `*ast.Ident` on the left of the selector, so it is skipped.

- [ ] **Step 5: Run the mutations**

| Mutation | Expected |
|---|---|
| delete `s.JobSubmitLimitN = d.jobSubmitLimitN` | RED: the third request is 400 |
| delete `s.JobSubmitLimitWin = d.jobSubmitLimitWin` | RED: the `> 0` guard fails, no limiter, third request is 400 |
| `UserRateLimit(60, 10*time.Second)` instead of the fields | RED: the third request is 400 |
| `mux.Handle("POST /v1/jobs", userLimit(auth(...)))` (wrap OUTSIDE auth) | RED: the FIRST request is 401, because the limiter runs before any principal exists. This is what Task 3's fail-closed refusal buys - a pass-through would make this mutation silent. |
| `mux.Handle("POST /v1/jobs", auth(http.HandlerFunc(s.handleCreateJob)))` (unwrapped) | RED: the third request is 400 |

- [ ] **Step 6: Commit**

```bash
git add internal/api/server.go cmd/relay-server/http_server.go cmd/relay-server/job_submit_ratelimit_wiring_test.go
git commit -m "Put POST /v1/jobs in a per-user bucket sized by a named field, not a fifth positional argument"
```

---

## Task 7: `retry` and `run-now` draw on the same bucket

**Files:**
- Modify: `internal/api/server.go` (two route lines)
- Modify: `cmd/relay-server/job_submit_ratelimit_wiring_test.go` (append)

This is the task that refutes the spec's placement (R2). The shared-bucket decision is the one that stops a caller alternating between routes, and it belongs in the lane CI runs.

- [ ] **Step 1: Write the failing test**

Append to `cmd/relay-server/job_submit_ratelimit_wiring_test.go`:

```go
// TestBuildHTTPServer_RetryAndRunNowDrawOnTheSubmitBucket makes the
// one-bucket-not-three decision executable in the DEFAULT lane.
//
// IT NEEDS NO DATABASE, WHICH IS WHY IT IS HERE. A refusal is issued before the
// handler runs, so proving route B draws on route A's bucket never reaches a
// handler at all. The id is deliberately NOT a uuid: an unbucketed request is
// then answered 400 by the handler's own parseUUID with no pool, so 429 and 400
// are the two outcomes and they say different things. Reaching a SUCCESS code on
// these routes would need real handlers and a container; reaching the REFUSAL
// does not.
func TestBuildHTTPServer_RetryAndRunNowDrawOnTheSubmitBucket(t *testing.T) {
	cases := []struct{ name, path string }{
		{"retry", "/v1/jobs/not-a-uuid/retry"},
		{"run-now", "/v1/scheduled-jobs/not-a-uuid/run-now"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := submitBucketServer(1, time.Minute)

			spend := postAsUser(t, srv, "/v1/jobs", `{}`)
			require.Equal(t, http.StatusBadRequest, spend.Code,
				"the fixture must actually spend the budget on POST /v1/jobs. body: %s",
				spend.Body.String())

			rec := postAsUser(t, srv, tc.path, "")
			require.Equal(t, http.StatusTooManyRequests, rec.Code,
				"%s must draw on the SAME bucket POST /v1/jobs just spent. A 400 here means the "+
					"route is unwrapped or carries its own UserRateLimit instance, and a caller "+
					"alternating between the two gets twice the ceiling. body: %s",
				tc.path, rec.Body.String())
		})
	}
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `go test ./cmd/relay-server/ -run TestBuildHTTPServer_RetryAndRunNowDraw -v -timeout 60s`
Expected: FAIL on both subtests with `Error: Not equal: expected: 429, actual: 400`, and a body naming `invalid job id` / `invalid id`. That is the real RED: the routes exist, are reachable, and are unbucketed.

- [ ] **Step 3: Write the implementation**

In `internal/api/server.go`, change exactly two lines:

```go
	mux.Handle("POST /v1/jobs/{id}/retry", auth(userLimit(http.HandlerFunc(s.handleRetryJob))))
```

```go
	mux.Handle("POST /v1/scheduled-jobs/{id}/run-now", auth(userLimit(http.HandlerFunc(s.handleRunScheduledJobNow))))
```

Then add one sentence to the Jobs block comment already above the jobs routes, so the route list lives where the wraps are:

```go
	// POST /v1/jobs, POST /v1/jobs/{id}/retry and
	// POST /v1/scheduled-jobs/{id}/run-now share ONE per-user bucket
	// (RELAY_JOB_SUBMIT_RATE_LIMIT). retry carries no body and no spec
	// validation and still re-buys a whole job's execution, which is why the
	// cheapest of the three draws on the same budget as the most expensive.
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./cmd/relay-server/ -run TestBuildHTTPServer_ -v -timeout 120s`
Expected: PASS, all `TestBuildHTTPServer_*`.

- [ ] **Step 5: Run the mutations**

| Mutation | Expected |
|---|---|
| give `retry` its own instance: `auth(UserRateLimit(s.JobSubmitLimitN, s.JobSubmitLimitWin)(http.HandlerFunc(s.handleRetryJob)))` | RED on the `retry` subtest only. **This is the mutation the spec said only the integration lane could kill.** |
| same for `run-now` | RED on the `run-now` subtest only |
| revert the `retry` wrap to bare `auth(...)` | RED on the `retry` subtest |
| revert the `run-now` wrap to bare `auth(...)` | RED on the `run-now` subtest |

- [ ] **Step 6: Commit**

```bash
git add internal/api/server.go cmd/relay-server/job_submit_ratelimit_wiring_test.go
git commit -m "Share one bucket across submit, retry and run-now, so alternating routes does not triple the ceiling"
```

---

## Task 8: `POST /v1/scheduled-jobs` stays OUT, executably

**Files:**
- Modify: `cmd/relay-server/job_submit_ratelimit_wiring_test.go` (append)

**This test is GREEN the moment it is written.** State that in the commit message. It is not a regression guard for a bug; it is a decision guard against a later "consistency" edit that adds the route, and its RED is produced by exactly that mutation. Step 3 is not optional.

- [ ] **Step 1: Write the test**

```go
// TestBuildHTTPServer_ScheduleCreationIsNotInTheSubmitBucket makes the OUT
// verdict executable rather than prose.
//
// The verdict's reason: the hazard that asked for this limit is the SIZE of
// scheduled_jobs at boot, and a creation-rate limit bounds how fast the table
// fills, not how full it gets. And a schedule's own firing runs on the
// schedrunner goroutine and never touches an HTTP route, so no HTTP rate limit
// anywhere can bound it. The control that bounds the table is a per-owner count
// cap, which is a quota and is not this.
//
// THE MIDDLE ASSERTION IS THE CONTROL. Without proving the bucket is FULL, the
// final 400 is also what a fixture whose limiter never ran produces, and the
// test would be green for the wrong reason.
func TestBuildHTTPServer_ScheduleCreationIsNotInTheSubmitBucket(t *testing.T) {
	srv := submitBucketServer(1, time.Minute)

	spend := postAsUser(t, srv, "/v1/jobs", `{}`)
	require.Equal(t, http.StatusBadRequest, spend.Code, "body: %s", spend.Body.String())

	over := postAsUser(t, srv, "/v1/jobs", `{}`)
	require.Equal(t, http.StatusTooManyRequests, over.Code,
		"control: the bucket must be provably full before the assertion below means anything")

	// 400 exactly, not merely "not 429": handleCreateScheduledJob answers
	// `name is required` after readJSON and before any pool use, so the code is
	// determinate and a loosened assertion would lose coverage for nothing.
	rec := postAsUser(t, srv, "/v1/scheduled-jobs", `{}`)
	require.Equal(t, http.StatusBadRequest, rec.Code,
		"POST /v1/scheduled-jobs is deliberately OUT of this bucket and must reach its own "+
			"validation even when the submit budget is spent. body: %s", rec.Body.String())
}
```

- [ ] **Step 2: Run it and verify it PASSES**

Run: `go test ./cmd/relay-server/ -run TestBuildHTTPServer_ScheduleCreationIsNot -v -timeout 60s`
Expected: PASS on the first run.

- [ ] **Step 3: Produce its RED by mutation**

In `internal/api/server.go`, change:

```go
	mux.Handle("POST /v1/scheduled-jobs", auth(userLimit(http.HandlerFunc(s.handleCreateScheduledJob))))
```

Run the test. Expected: **FAIL**, `expected: 400, actual: 429`. Restore from your copy and re-run to confirm green. Record both in the PR: a test that has never been red pins nothing.

- [ ] **Step 4: Commit**

```bash
git add cmd/relay-server/job_submit_ratelimit_wiring_test.go
git commit -m "Guard the scheduled-jobs OUT verdict, which a consistency edit would otherwise reverse silently"
```

---

## Task 9: A zero limit leaves the bucket off, without panicking

**Files:**
- Modify: `cmd/relay-server/job_submit_ratelimit_wiring_test.go` (append)

The spec has no guard here and the gap is not cosmetic. `rateLimiter.allow` runs `retry := rl.window - now.Sub(hits[0])` inside `if len(hits) >= rl.limit`; with `limit == 0` that branch is taken on an empty slice and **`hits[0]` panics on the first request**. Three live callers depend on the off state: `internal/api/server_test.go`'s `&Server{}`, `internal/api`'s `newTestServer` (`api.New(..., 0,0,0,0)`), and `internal/cli`'s `relayharness_integration_test.go`. None of them would go red in CI if the guard were deleted, because none of them POSTs a job in the default lane.

- [ ] **Step 1: Write the test**

```go
// TestBuildHTTPServer_AZeroLimitLeavesTheBucketOff pins the guard in
// Server.Handler, which is NOT cosmetic. rateLimiter.allow indexes hits[0]
// whenever len(hits) >= limit, so constructing a limiter with a zero count
// panics on the first request rather than admitting it. The Go-constructed
// off state is what internal/api's own test server and the CLI harness both
// rely on; the environment cannot reach it, because ParseRateLimit refuses a
// zero count and main fatals on the error.
func TestBuildHTTPServer_AZeroLimitLeavesTheBucketOff(t *testing.T) {
	srv := buildHTTPServer(httpServerDeps{
		addr: "127.0.0.1:0",
		q:    store.New(stubAdminDB{}),
		// jobSubmitLimitN and jobSubmitLimitWin deliberately unset.
	})

	for i := 1; i <= 3; i++ {
		rec := postAsUser(t, srv, "/v1/jobs", `{}`)
		require.Equal(t, http.StatusBadRequest, rec.Code,
			"request %d: with no configured limit every request must reach the handler. A 429 means "+
				"a zero-count limiter was constructed; a panic means it was constructed AND allow "+
				"indexed an empty window. body: %s", i, rec.Body.String())
	}
}
```

- [ ] **Step 2: Run it and verify it PASSES**

Run: `go test ./cmd/relay-server/ -run TestBuildHTTPServer_AZeroLimitLeaves -v -timeout 60s`
Expected: PASS.

- [ ] **Step 3: Produce its RED by mutation**

In `internal/api/server.go`, delete the `if s.JobSubmitLimitN > 0 && s.JobSubmitLimitWin > 0` guard and assign unconditionally:

```go
	userLimit := UserRateLimit(s.JobSubmitLimitN, s.JobSubmitLimitWin)
```

Run the test. Expected: **FAIL with a panic**, `index out of range [0] with length 0` in `rateLimiter.allow`. Record the panic text - it is the evidence that the guard prevents a crash and not merely a behaviour.

Restore from your copy. Re-run and confirm green.

- [ ] **Step 4: Run the whole `cmd/relay-server` and `internal/api` default lanes**

Run: `go test ./internal/api/ ./cmd/relay-server/ -timeout 180s`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add cmd/relay-server/job_submit_ratelimit_wiring_test.go
git commit -m "Pin the zero-limit guard, whose removal panics rateLimiter.allow on the first request"
```

---

## Task 10: `main.go` - the env variable and the default

**Files:**
- Modify: `cmd/relay-server/main.go`

Use Task 1's measurement. The default is `60:10s` unless the measured `relay submit` loop exceeded 6/s, in which case it is `120:10s`.

- [ ] **Step 1: Write the implementation**

In `main`, immediately after the `registerN, registerWin` block:

```go
	jobSubmitN, jobSubmitWin, err := api.ParseRateLimit(
		envOrDefault("RELAY_JOB_SUBMIT_RATE_LIMIT", "60:10s"))
	if err != nil {
		log.Fatalf("parse RELAY_JOB_SUBMIT_RATE_LIMIT: %v", err)
	}
```

In the `buildHTTPServer(httpServerDeps{...})` composite literal, after `registerLimitWin:`:

```go
		jobSubmitLimitN:   jobSubmitN,
		jobSubmitLimitWin: jobSubmitWin,
```

The `log.Fatalf` follows the two existing variables exactly, including that a zero count refuses to boot. **Do not add an "off" token to this variable alone and do not relax `ParseRateLimit`** - `TestParseRateLimit` pins `0:1m` as an error and an off spelling that exists for one of three rate-limit variables is a worse surface than none.

- [ ] **Step 2: Verify the wiring guard is unaffected**

`TestServerCountersIsWiredByMain` requires that every name on the `grpcAdmission` / `agentHandler` / `watchdog` chains, plus the server binding, is assigned exactly once in the whole of `main`. Checked while planning: neither `jobSubmitN` nor `jobSubmitWin` is on any of those chains, and the `err` this statement reassigns is already reassigned several times by the existing multi-value `:=` statements that the test passes with today. Confirm rather than assume:

Run: `go test ./cmd/relay-server/ -run TestServerCountersIsWiredByMain -v -timeout 60s`
Expected: PASS.

- [ ] **Step 3: Run the whole package and build**

Run: `go build ./... && go test ./cmd/relay-server/ -timeout 120s`
Expected: PASS.

- [ ] **Step 4: Verify the variable end to end by hand**

```powershell
$env:RELAY_JOB_SUBMIT_RATE_LIMIT = "2:1m"
./bin/relay-server     # after `make build`
```

Then three `POST /v1/jobs` as one user: two succeed, the third is 429 with `Retry-After`. Then:

```powershell
$env:RELAY_JOB_SUBMIT_RATE_LIMIT = "0:10s"
./bin/relay-server
```

Expected: refuses to boot with `parse RELAY_JOB_SUBMIT_RATE_LIMIT: ratelimit: count must be a positive integer, got "0"`. Paste both into the PR - the default value itself is guarded by nothing in the test suite and this is the only check it gets.

- [ ] **Step 5: Commit**

```bash
git add cmd/relay-server/main.go
git commit -m "Read RELAY_JOB_SUBMIT_RATE_LIMIT, defaulting to a burst ceiling no scripted workflow reaches"
```

---

## Task 11: The integration-lane end-to-end proof

**Files:**
- Create: `internal/api/job_submit_ratelimit_integration_test.go`

**Lane honesty, stated once and repeated in the PR:** this file does NOT run in CI. `make vet-integration` compiles it; only `make test-integration` runs it. That is why Tasks 4 and 6 to 9 pushed every property they could into the default lane. What is left here is the pair of things only a real database can say: real success codes on three real handlers, and per-user isolation through the real auth chain with real tokens.

- [ ] **Step 1: Write the file**

```go
//go:build integration

package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// postAs issues one request against a PRE-BUILT handler. The handler is built
// once by each caller and reused, which is not decoration: srv.Handler()
// constructs a fresh mux AND a fresh UserRateLimit with an empty map, so the
// per-request `srv.Handler().ServeHTTP(...)` idiom the rest of this package uses
// would give every request its own full bucket and make every assertion below
// vacuous.
func postAs(t *testing.T, h http.Handler, token, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("POST", path, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

const rlJobBody = `{"name":"rl","tasks":[{"name":"t","command":["echo","hi"]}]}`

// TestPostJobs_ABurstIsRefusedAndTheNextUserIsNot is the behavioural
// reproduction of the source item, end to end: at HEAD four submissions in a row
// all answer 201.
//
// BOB'S REQUEST IS THE ISOLATION PROOF AND IT IS NOT INCIDENTAL.
// httptest.NewRequest gives every request the same RemoteAddr, so Alice and Bob
// present from one address here by construction. A limiter keyed on the address
// refuses Bob; the one this slice ships does not.
func TestPostJobs_ABurstIsRefusedAndTheNextUserIsNot(t *testing.T) {
	srv, q := newTestServer(t)
	srv.JobSubmitLimitN = 3
	srv.JobSubmitLimitWin = time.Minute
	h := srv.Handler()

	alice := createTestUser(t, q, "Alice", "alice-ratelimit@example.com", false)
	aliceTok := createTestToken(t, q, alice.ID)
	bob := createTestUser(t, q, "Bob", "bob-ratelimit@example.com", false)
	bobTok := createTestToken(t, q, bob.ID)

	for i := 1; i <= 3; i++ {
		rec := postAs(t, h, aliceTok, "/v1/jobs", rlJobBody)
		require.Equal(t, http.StatusCreated, rec.Code,
			"submission %d is inside the budget. body: %s", i, rec.Body.String())
	}

	over := postAs(t, h, aliceTok, "/v1/jobs", rlJobBody)
	require.Equal(t, http.StatusTooManyRequests, over.Code,
		"the fourth submission is over the budget. body: %s", over.Body.String())
	require.NotEmpty(t, over.Header().Get("Retry-After"))

	next := postAs(t, h, bobTok, "/v1/jobs", rlJobBody)
	require.Equal(t, http.StatusCreated, next.Code,
		"bob's FIRST submission, from the same source address alice just filled her budget from, "+
			"must succeed. body: %s", next.Body.String())
}

// TestSubmitRunNowAndRetryShareOneBucket proves the one-bucket decision through
// three real handlers with real success codes.
//
// THE SEQUENCE IS THE ARGUMENT. At a limit of 2: the submit takes hit 1 and the
// run-now takes hit 2, so the retry is refused. If run-now had its own bucket
// the retry would find room and answer 409 ("job is not finished; retry is
// available for a done or failed job"), because a job created a moment ago is
// pending. 429 versus 409 is therefore the discriminator, and it needs no worker
// simulation - which is why this does not drive a job to `failed` first.
//
// The default-lane sibling in cmd/relay-server carries the same decision without
// a container. This one adds what that cannot: two real 201s.
func TestSubmitRunNowAndRetryShareOneBucket(t *testing.T) {
	srv, q := newTestServer(t)
	user := createTestUser(t, q, "Owner", "owner-ratelimit@example.com", false)
	token := createTestToken(t, q, user.ID)

	// Seed through a handler built while the bucket is OFF, so the fixtures do
	// not spend the budget the assertions depend on.
	seed := srv.Handler()

	jobRec := postAs(t, seed, token, "/v1/jobs", rlJobBody)
	require.Equal(t, http.StatusCreated, jobRec.Code, "body: %s", jobRec.Body.String())
	var job struct {
		ID string `json:"id"`
	}
	require.NoError(t, json.Unmarshal(jobRec.Body.Bytes(), &job))
	require.NotEmpty(t, job.ID)

	// "0 3 * * *" is a day apart, comfortably above minScheduleInterval.
	schedRec := postAs(t, seed, token, "/v1/scheduled-jobs", `{
		"name": "rl-schedule",
		"cron_expr": "0 3 * * *",
		"timezone": "UTC",
		"overlap_policy": "skip",
		"job_spec": {"name":"rl-template","tasks":[{"name":"t","command":["echo","x"]}]}
	}`)
	require.Equal(t, http.StatusCreated, schedRec.Code, "body: %s", schedRec.Body.String())
	var sched struct {
		ID string `json:"id"`
	}
	require.NoError(t, json.Unmarshal(schedRec.Body.Bytes(), &sched))
	require.NotEmpty(t, sched.ID)

	srv.JobSubmitLimitN = 2
	srv.JobSubmitLimitWin = time.Minute
	h := srv.Handler()

	first := postAs(t, h, token, "/v1/jobs", rlJobBody)
	require.Equal(t, http.StatusCreated, first.Code, "hit 1. body: %s", first.Body.String())

	second := postAs(t, h, token, "/v1/scheduled-jobs/"+sched.ID+"/run-now", "")
	require.Equal(t, http.StatusCreated, second.Code, "hit 2. body: %s", second.Body.String())

	third := postAs(t, h, token, "/v1/jobs/"+job.ID+"/retry?task=all", "")
	require.Equal(t, http.StatusTooManyRequests, third.Code,
		"the retry must be refused by the budget the submit and the run-now spent between them. A "+
			"409 here means run-now has its own bucket and the retry found room. body: %s",
		third.Body.String())
}
```

- [ ] **Step 2: Compile it in the tagged lane**

Run: `make vet-integration`
Expected: clean. This is the check CI actually performs on this file.

- [ ] **Step 3: Run it**

Requires Docker Desktop. The `internal/api` integration package runs about 9.5 minutes in full; use a generous timeout and a `-run` filter.

Run: `go test -tags integration -p 1 ./internal/api/ -run "TestPostJobs_ABurstIsRefused|TestSubmitRunNowAndRetryShareOneBucket" -v -timeout 1800s`
Expected: PASS on both.

**If Docker is unavailable, say so in the PR rather than substituting.** State plainly that the integration lane did not run, and that the shared-bucket and per-user properties are still covered by the default-lane tests in `cmd/relay-server`. Do not claim coverage you did not execute.

- [ ] **Step 4: Verify the vacuousness trap is real**

Change `h := srv.Handler()` in `TestPostJobs_ABurstIsRefusedAndTheNextUserIsNot` so that `postAs` receives `srv.Handler()` freshly on every call (the idiom the rest of the package uses). Expected: the fourth submission answers **201** and the test FAILS. That is the trap the `h :=` hoist exists to avoid; record it, then restore.

- [ ] **Step 5: Commit**

```bash
git add internal/api/job_submit_ratelimit_integration_test.go
git commit -m "Prove per-user isolation and the shared bucket end to end, in the lane CI does not run"
```

---

## Task 12: README - three edits

**Files:**
- Modify: `README.md`

**CRLF and encoding hazard.** This repo normalizes line endings inconsistently and a programmatic rewrite has previously reclassified `README.md` as binary and turned a two-line change into 1845 insertions. Before committing this task you MUST:

- print the file's line count before and after (the delta should be exactly `+1`, from the new table row);
- check the diffstat against the size of the change you intended;
- run `git ls-files --eol README.md` and confirm it reads `i/lf`;
- confirm the file still decodes as UTF-8, and that every byte you wrote is ASCII. **Use no non-ASCII characters at all in these three edits.**

Prefer exact-anchor replacement over any scripted rewrite.

- [ ] **Step 1: Add the env table row**

Insert immediately after the `RELAY_REGISTER_RATE_LIMIT` row, so the three rate-limit variables are adjacent. Use `60:10s` unless Task 1's measurement escalated it to `120:10s`, in which case use that in both the default cell and the prose.

```
| `RELAY_JOB_SUBMIT_RATE_LIMIT` | `60:10s` | Per-**user** rate limit (format `N:duration`) over the three routes that let one principal buy task execution: `POST /v1/jobs`, `POST /v1/jobs/{id}/retry` and `POST /v1/scheduled-jobs/{id}/run-now`. **One shared bucket keyed on the authenticated user, not on the source address.** The anti-enumeration reasoning that makes `RemoteAddr` right for the two rows above does not transfer once a principal exists: keyed on the address, one office egress or load balancer collapses a whole studio into one bucket while one user with a workstation and a laptop gets two. A refusal is `429` with `Retry-After`. **It bounds repetition of expensive work, not request volume** - the limiter runs inside the auth chain, so every attempt, refused or not, still costs one token lookup; the control for request volume would be an admission bound at the HTTP listener, which relay does not have. **Per replica and in memory**, like the counters section: two replicas give a caller twice the budget and a restart clears every bucket. **There is no off value** - `ParseRateLimit` refuses a zero count and the server refuses to boot on `0:10s`, exactly as the two rows above do; set a large count such as `100000:1s` if you want it effectively disabled. Because `retry` shares the budget, a user whose script has spent it cannot retry a real failed job until the window clears: ten seconds at the default, a full minute if you set a one-minute window. **`POST /v1/scheduled-jobs` is deliberately not in this bucket** - a creation-rate limit bounds how fast `scheduled_jobs` fills, not how full it gets - and **no HTTP rate limit anywhere bounds a schedule's own firing**, so one `@every 1s` schedule remains an uncapped job engine. For a farm whose users are not all trusted, `10:1m` is the recommended value; its cost is that a 200-job `for` loop is refused partway and the workflow becomes one job with 200 tasks, which is the model relay is built around anyway. |
```

- [ ] **Step 2: Correct the `tasks[].retries` row**

That row currently contains: *"**All of these bound ONE request and none of them bounds repetition** - `POST /v1/jobs` carries no rate limit, so the totals above are per-request figures an authenticated caller may repeat."* This slice makes the second clause false. Replace that sentence, and only that sentence, with:

```
**The count caps bound ONE request; `RELAY_JOB_SUBMIT_RATE_LIMIT` bounds how often one principal may repeat it** (default `60:10s`, per authenticated user, shared with `POST /v1/jobs/{id}/retry`). Read the product plainly: 25,000 commands x 11 attempts x 60 submissions is about 16.5 million spawns per ten-second window at the defaults, so these caps are a blast-radius bound and not a DoS control.
```

Leave the rest of the row (the retroactivity sentence and the no-backoff sentence) untouched.

- [ ] **Step 3: Scope the `?q=` cost paragraph to reads**

That paragraph currently says *"The server applies no rate limit and no statement timeout to this today, so the cost is bounded only by the table size and by how often clients ask."* It stays true and becomes ambiguous now that a rate-limit row exists a reader might take as global. Replace that sentence with:

```
The server applies no rate limit and no statement timeout to READS - `RELAY_JOB_SUBMIT_RATE_LIMIT` covers only the three write routes named in its row - so the cost is bounded only by the table size and by how often clients ask.
```

- [ ] **Step 4: Run the hygiene checks**

```bash
git diff --stat README.md
git ls-files --eol README.md
```

Expected: a diffstat proportionate to three edits (roughly `1 file changed, 3 insertions(+), 2 deletions(-)`), and `i/lf w/crlf attr/` or equivalent with `i/lf`. If the diffstat is in the hundreds or thousands, **stop** - the file has been reclassified as binary and must be restored, not fixed forward.

- [ ] **Step 5: Commit**

```bash
git add README.md
git commit -m "Document RELAY_JOB_SUBMIT_RATE_LIMIT, and correct the two prose claims it makes false"
```

---

## Task 13: `internal/jobspec` - the doc comment, and the re-read whose answer is "unchanged"

**Files:**
- Modify: `internal/jobspec/jobspec.go` (`maxCommandsPerJob`'s doc comment only)

- [ ] **Step 1: Replace the false paragraph**

`maxCommandsPerJob`'s comment currently contains:

```go
// IT IS NOT A DoS CONTROL AND MUST NOT BE TIGHTENED AS IF IT WERE. POST /v1/jobs
// carries no rate limit - internal/api/server.go wraps only register and login in
// RateLimit - so every figure above is per-request and an authenticated caller may
// repeat it at whatever rate the network allows. The control for repetition is a
// rate limit. Tightening this to buy a constant factor against an attack that
// repetition makes unbounded anyway costs a refused real render, which has no
// workaround inside the product.
```

Both halves must change: the first clause is now false, and the parenthetical is a census of what `server.go` wraps, which CLAUDE.md's comment rules forbid and which is exactly the sentence that went stale. Replace with:

```go
// IT IS NOT A DoS CONTROL AND MUST NOT BE TIGHTENED AS IF IT WERE. Every figure
// above is per-request; repetition is bounded separately and per authenticated
// user by RELAY_JOB_SUBMIT_RATE_LIMIT, which is a burst ceiling and not a budget
// over time - so a caller submitting at exactly the ceiling rate forever is
// bounded by neither control. Tightening this to buy a constant factor there
// costs a refused real render, which has no workaround inside the product.
```

Seven lines out; six lines in. Print the file's line count before and after and confirm the delta is `-1`.

- [ ] **Step 2: Re-read the three constants - the source item requires it**

Open `internal/jobspec/jobspec.go` and read `maxTasksPerJob`, `maxCommandsPerTask` and `maxCommandsPerJob` with their comments, and the count-bounds spec's "Rejected alternatives" premise: it chose the generous set (5000 / 500 / 25000) on the stated condition that this item is **implemented rather than closed wontfix**, and conditioned the tighter set (2000 / 200 / 10000) on the opposite.

The premise holds - this slice implements it - so the generous set stands. **The expected outcome of this step is "unchanged", and that is a result, not a skip.** Write it in the PR body explicitly:

> Re-read `maxTasksPerJob = 5000`, `maxCommandsPerTask = 500`, `maxCommandsPerJob = 25000` per the source item's fifth acceptance criterion. Outcome: unchanged. The count-bounds spec conditioned the tighter set on this item being closed wontfix; it is being implemented, so its premise holds.

Do not touch the constants and do not touch `internal/jobspec/count_bounds_test.go`, which spells the numbers as literals rather than as the constants, deliberately.

- [ ] **Step 3: Run the package**

Run: `go test ./internal/jobspec/ -timeout 60s`
Expected: PASS. No test reads this comment, so a green here means only that nothing else broke - the comment's correctness is checked by reading, in review.

- [ ] **Step 4: Commit**

```bash
git add internal/jobspec/jobspec.go
git commit -m "Name the rate limit in maxCommandsPerJob's comment, and drop the census that went stale"
```

---

## Task 14: Full verification sweep

**Files:** none.

- [ ] **Step 1: Default lane, whole module**

Run: `make test`
Expected: PASS. This is the lane CI runs (modulo `-race`).

- [ ] **Step 2: Race detector**

`make test-race` is the canonical target, but on Windows the native lane is unreliable in two distinct ways and the container is the route that works:

```bash
MSYS_NO_PATHCONV=1 docker run --rm -v "$(pwd -W):/src" -w /src -e CGO_ENABLED=1 \
  golang:1.26 go test -race ./... -count=1 -timeout 600s
```

Expected: zero data races across all packages. `UserRateLimit` shares `rateLimiter`'s existing mutex and adds no new state, and `TestRateLimit_ConcurrentHitsDontRace` already covers the arithmetic - but this slice puts a second limiter on a hot request path and the lane is cheap.

**If the lane is genuinely unavailable, say so.** Do not substitute `-count=N`: repetition raises confidence in flakiness, not in race-freedom.

- [ ] **Step 3: Integration compile check**

Run: `make vet-integration`
Expected: clean.

- [ ] **Step 4: CLI integration lane**

Run: `make test-cli-integration`
Expected: PASS. Checked while planning: `internal/cli/relayharness_integration_test.go` builds its server with `api.New(pool, q, ..., 0, 0, 0, 0)` and never sets the new fields, so the bucket is off there and the lane is unaffected. Running it confirms that rather than assuming it - and this lane is where a "removing or changing behaviour" break would surface as a test scaffolded on the old shape.

- [ ] **Step 5: Confirm the mutation battery had a green baseline**

Collect every mutation result from Tasks 2 to 9 into one table for the PR. It must contain **at least two survivors** (`u.Email` as a key; `uuidStr(u.ID), u.ID.Valid` as a return). Uniform results mean a broken harness, and a compile error is never a kill.

- [ ] **Step 6: Line-ending and encoding audit on every text file touched**

```bash
git ls-files --eol README.md internal/jobspec/jobspec.go internal/api/ratelimit.go internal/api/server.go cmd/relay-server/main.go cmd/relay-server/http_server.go
git diff --stat origin/main...HEAD
```

Every path must read `i/lf`. The total diffstat must be proportionate to the change described above; anything in the thousands means a file was reclassified as binary. `gofmt -l` is useless as a signal here - it lists hundreds of files on a clean tree purely because of working-copy CRLF.

- [ ] **Step 7: Assemble the PR body**

It must carry, at minimum:

1. **Task 1's measurement**: the `relay submit` loop rate, the machine it was measured on, and the default-value decision the escalation rule produced.
2. **Task 1's behavioural RED at HEAD**: eight `201`s, no `429`.
3. **The count-constant re-read, outcome "unchanged"**, with the premise that makes it hold.
4. **The mutation table**, survivors included.
5. **Task 10 Step 4's two manual checks**: `2:1m` refuses the third request, `0:10s` refuses to boot.
6. **Lane honesty**: which lanes ran, which did not, and what each one covers. Say explicitly that `internal/api/job_submit_ratelimit_integration_test.go` does not run in CI, and that the shared-bucket decision it confirms is ALSO pinned in `cmd/relay-server`'s default-lane tests.
7. **What this slice does not cover**, taken from the spec: no fleet-wide ceiling (N accounts buy N budgets), no budget over time (a caller submitting at exactly the ceiling rate forever is unbounded), per replica and in memory, the auth lookup itself is not bounded, `POST /v1/scheduled-jobs` is not limited and `scheduled_jobs` remains unbounded in size, a schedule's own firing is bounded by nothing here, the `?q=` read bucket is not built, no refusal counter on `GET /v1/server/counters`, and `X-Forwarded-For` is still not trusted anywhere.

- [ ] **Step 8: Close the backlog item**

Use the command, never a hand edit of the `status` field:

```
/backlog close post-v1-jobs-is-not-rate-limited
```

---

## Backlog items this plan needs the conductor to file

None are filed here - a planner writes plan docs and nothing else. Four are specific and high-confidence:

1. **The three spec-proposed items**, unchanged: the login and register limiters collapse behind a proxy and neither README row says so; the `> 0` guards in `Server.Handler` describe a state the environment cannot produce (see the sharpening below); and a refusal counter for the rate limiters on `GET /v1/server/counters`, with the forgeability analysis as its opening constraint.
2. **A sharpening of the second of those, found while planning:** `rateLimiter.allow` panics (`hits[0]` on an empty slice) whenever it is constructed with a zero limit, so those `> 0` guards prevent a crash rather than merely selecting a behaviour, and any future caller who omits one ships a panic on the first request. `TestBuildHTTPServer_AZeroLimitLeavesTheBucketOff` pins it for the job-submit bucket only; login and register have no equivalent guard test. The candidate fix is a guard inside `allow` itself, which is a change to shared code this slice deliberately does not make.
3. **The boot-sweep item's edits**, which the spec proposes and only a human can accept: ship half 1 alone; amend half 2 to drop the rate limit and keep the per-owner cap, citing `docs/superpowers/specs/2026-09-04-authenticated-route-rate-limiting.md` so the decision is findable and is not re-litigated as an oversight; retitle the remaining half to name the cap rather than the pair.
4. **`docs/backlog/feature-2026-09-03-server-side-bound-for-text-search.md`** should record what this slice settles for it: the mechanism reuses verbatim (`rateLimiter`, `userRateLimitKey`, the 429 body and `Retry-After`, `ParseRateLimit` for a second `N:duration` variable), with its own instance so the buckets are genuinely separate - and the middleware FORM must not be copied. The read bucket calls `rl.allow` from **inside** the handler, at the point `parseFilterQ` has already returned a non-nil needle, so the counted set and the expensive set are the same set by construction and there is no second query parse to disagree with the validated one.

---

## Self-review notes

**Spec coverage.** Every section of the spec maps to a task: the key and its argument -> Tasks 2, 3, 4; composition order -> Tasks 3, 6 (including the outside-auth mutation); burst ceiling and the default -> Tasks 1, 10; which routes in -> Tasks 6, 7, 11; `POST /v1/scheduled-jobs` out -> Task 8; memory -> Task 6's build-once comment; reuse for the read bucket -> Task 2's signature and the backlog note; configuration and the threading trade -> Task 6 (fields) and Task 10 (env); the doc comment -> Task 3; testing -> Tasks 2 to 9 and 11; advertisement surfaces -> Tasks 12 and 13; the count-constant re-read -> Task 13 Step 2.

**Deliberately not covered, with reasons in the body:** the count-bounds spec doc is not edited (R4); no e2e config change (slice independence declaration); no `web/` change; no fleet ceiling, quota, refusal counter or `?q=` bucket.

**Type consistency.** `userRateLimitKey(u AuthUser) (string, bool)` is used identically in Tasks 2 and 3. `UserRateLimit(limit int, window time.Duration) func(http.Handler) http.Handler` matches `RateLimit`'s shape and is called identically in Task 6. `JobSubmitLimitN int` / `JobSubmitLimitWin time.Duration` on `api.Server`, `jobSubmitLimitN` / `jobSubmitLimitWin` on `httpServerDeps`, and `jobSubmitN` / `jobSubmitWin` as `main`'s locals are used consistently in Tasks 6, 10 and 11. `postAsUser(t, srv, path, body)` (Task 6) and `postAs(t, h, token, path, body)` (Task 11) are different helpers in different packages with deliberately different names and signatures.
