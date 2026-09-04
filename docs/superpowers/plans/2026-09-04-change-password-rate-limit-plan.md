# Change-password rate limit Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Bound `PUT /v1/users/me/password` with one per-authenticated-user rate-limit bucket, so a burst is refused before either cost-12 bcrypt operation runs.

**Architecture:** No new mechanism. A third instance of the existing `UserRateLimit` middleware, constructed once in `Server.Handler` beside the existing `userLimit` and mounted INSIDE the auth chain as `auth(passwordLimit(h))`. Sizing reaches `api.Server` as two named exported fields set by `buildHTTPServer` from two new `httpServerDeps` fields, fed by one new env variable parsed in `main`. No handler changes.

**Tech Stack:** Go 1.26.2, stdlib `net/http` / `net/http/httptest` / `go/ast` / `go/parser`, `testify/require` in `cmd/relay-server` tests, `golang.org/x/crypto/bcrypt` (measurement only).

**Source spec:** `docs/superpowers/specs/2026-09-04-change-password-rate-limit.md` (committed at `abf8119`).
**Closes:** `docs/backlog/bug-2026-09-04-change-password-runs-bcrypt-cost-12-unlimited.md` (the conductor runs `/backlog close change-password-runs-bcrypt`; do not hand-edit the item's `status`).

---

## Slice independence declaration

**One sequential backend slice. There is no frontend slice, and nothing here is parallelisable.**

- **No `web/` work at all.** `web/src/profile/PasswordTab.tsx` was read: one PUT per click, `disabled={change.isPending}`, three guards that `return` before `change.mutate()`, no retry anywhere in the mutation. It needs no change and must not get one. `web/playwright.config.ts` pins login/register limits for the e2e server; the e2e suite issues no `PUT /v1/users/me/password`, so no new pin is needed and none is added.
- Every task touches `internal/api/server.go`, `internal/api/ratelimit.go`, `cmd/relay-server/*` or `README.md`. Two agents would collide on the same files. Run the tasks in order in one lane.
- **This is a single-PR unit.** It does not divide into stages, so `/backlog phases` is not needed for it.

Sequencing constraints inside the lane:

- **Task 1 (the measurement) runs before any code.** It is a precondition of the README row in Task 16, and its result decides which of two documented regimes that row is written in.
- **Task 4 (the vertical: fields + wrap) must land before Tasks 6, 7, 8, 9.** Every default-lane proof needs the two `api.Server` fields and the two `httpServerDeps` fields to exist.
- **Task 10 (the AST guard) must be written before Task 11 (main's parse).** Its RED is that `main` has no such literal.

---

## What this plan refutes or corrects in the spec

The spec was read once, whole, for self-contradiction and contradiction with the tree before any task was written. It is largely accurate: every load-bearing claim I was asked to verify held. Seven things did not survive contact with the tree, and each is a change to what this plan does, not a footnote.

### Verified, load-bearing, unchanged

- **`stubAdminDB` authenticates with no Postgres.** `cmd/relay-server/counters_wiring_test.go` declares `stubAdminDB` (a `store.DBTX` whose `QueryRow` returns `stubAdminRow`, whose `Scan` fills three `pgtype.UUID` destinations by position, one `*bool` true and any `*string`), and `TestStubAdminDB_ResolvesAUserWithARenderableID` asserts `row.UserID.Valid` and `row.TokenID != row.UserID`. `Exec` and `Query` panic. Confirmed.
- **`handleChangePassword` refuses `{}` before `GetUser` and before either bcrypt call.** The body order in `internal/api/auth.go` is `readJSON` -> `if len(req.NewPassword) < 8 { 400 }` -> `UserFromCtx` -> `s.q.GetUser` -> `bcrypt.CompareHashAndPassword` -> `bcrypt.GenerateFromPassword`. `{}` is valid JSON, so `readJSON` succeeds and `NewPassword` is `""`. **The seam holds: 400 means the handler ran, 429 means it did not.**
- **A request with an unrenderable principal 401s before `rl.allow`.** In `UserRateLimit` (`internal/api/ratelimit.go`) the order is `UserFromCtx` -> `userRateLimitKey` -> `if !ok { writeError(401); return }` -> `rl.allow(key)`. Confirmed, and `TestUserRateLimit_ARequestWithNoRenderablePrincipalIsRefused` already pins it with a limit of 10 and a `reached` flag.
- **`TestUserRateLimit_RetryAfterNamesWhenTheWindowActuallyClears` exists and pins the value with a 55..61 band** under a one-minute window, in `internal/api/ratelimit_test.go`. Confirmed by reading the assertion.
- **The composition order.** `UserRateLimit` reads the principal off the request context, which only `BearerAuth` puts there. **The plan specifies `auth(passwordLimit(http.HandlerFunc(s.handleChangePassword)))` - limiter INSIDE auth.** Written the other way round, `passwordLimit(auth(h))`, the limiter runs first, `UserFromCtx` yields the zero `AuthUser`, `userRateLimitKey` returns `ok == false`, and **the FIRST request answers 401**, not the third. The spec's self-correction is right. Task 5 makes this an explicit named task with its own mutation run, because getting it backwards is an authorization-shaped bug that a ceiling test alone reports as "wrong number".
- **`searchLimiterOnce`'s reasoning.** `internal/api/server.go`'s field comment says every limiter constructor starts a `gcLoop` goroutine that is never stopped, so a second instance is a second budget AND a leaked goroutine; `internal/api/search_ratelimit.go` builds the search limiter lazily under a `sync.Once` because its limits arrive as fields set after `New`. `TestSearchLimiter_IsConstructedOncePerServer` pins it by identity. All confirmed. The spec's argument for eager construction in `Handler` (nothing else orders a lazily-built limiter; `Handler` is ordered by definition) is sound - **with one hazard the spec does not state, see R4 below.**

### R1. The AST guard must NOT lift `TestServerCountersIsWiredByMain`'s `from` builder. It would be unconditionally RED.

`TestServerCountersIsWiredByMain` builds its derivation map with this filter:

```go
for i, l := range as.Lhs {
    id, ok := l.(*ast.Ident)
    if !ok || len(as.Lhs) != len(as.Rhs) {
        continue
    }
```

Every rate-limit parse in `main` is `n, win, err := api.ParseRateLimit(...)` - **three names on the left, one call on the right**. That walk skips the statement entirely, so `from["passwordChangeN"]` would be empty, the reachability BFS would find nothing, and the guard would fail on correct code. The arity-tolerant walk in `TestWatchdogIsStartedByMain` (append every RHS identifier and unquoted string literal to every LHS name) is the one to lift, restricted to statements that are direct children of `main`'s body. **Task 10 spells the walk out in full so this cannot be got wrong by copying the nearest neighbour.**

### R2. "The same sentence `TestTrailingLogWindowIsWiredIntoTheHandler` carries" - it does not carry it, and CLAUDE.md forbids half of it.

What that test's comment actually says is *"so this guard is worth generalizing rather than pasting a third time. The conductor is filing that as its own item - do not generalize here."* It names no item file. So the sentence must be written fresh, not copied.

And **"do not paste a twelfth" cannot be written in a comment.** CLAUDE.md's Comments section forbids "Counts of anything elsewhere ('16 sites', 'four other copies')" - a count of other code, pinned by nothing, that rots the moment a copy is added or removed. The item's own tally is also not a clean "ten": it reads nine as of its 2026-08-21 slice-3 update, then a 2026-08-24 heading announcing a tenth, and the spec's own provenance section says **"Do not carry this number into any comment."** The spec's section 8 then prescribes carrying it. Task 10 resolves this in favour of the provenance section and CLAUDE.md: the comment names the item by path and says do not paste another copy, with no number.

### R3. "A one-row table" undercounts. It is one row per wired FIELD, and there are two.

`passwordChangeLimitN` and `passwordChangeLimitWin` are separately omittable and separately crossable (`searchN` is an `int`, `searchWin` a `time.Duration`), so each needs its own reachability and assignment-count check. The columns are the ones `idea-2026-08-14` prescribes (field, derived-from) plus the env-var literal the 2026-08-24 watchdog slice proved is required wherever two wired values share a type. Two rows over three columns, liftable as-is.

### R4. Eager construction in `Handler` DOES risk a second limiter, and the tree already does it - which changes how the integration tests must be written.

`Handler`'s doc comment already warns that each call allocates a fresh bucket. What the spec does not say is that **`internal/api`'s integration tests call `srv.Handler()` once per request** (`TestChangePassword_HappyPath` calls it six times). With the password limiter constructed eagerly in `Handler`, each of those calls mints a fresh limiter with an empty map. Consequences, both handled:

- **Existing tests are unaffected**, because they all build the server with zero limits and the enable guard leaves the bucket unarmed.
- **The new integration tests would be silently vacuous** if written in the same style: every request would get a fresh budget and the burst test could never see a 429. **Tasks 14 and 15 bind `h := srv.Handler()` once and reuse it**, and their comments say why. The 429 assertion is itself the proof the binding held.

This is the honest form of the spec's "one-per-Server comes from `Handler` being called once per server": in production that is true (`buildHTTPServer` calls it once, in the `&http.Server{...}` literal it returns), in tests it is a caller obligation, and the executable pin is the ceiling test, not a `sync.Once`. **Do not add a `sync.Once` for this limiter.** The lazy limiter needed one because nothing ordered its construction; this one is ordered by `Handler`.

### R5. The four rate-limit rows in README are ALREADY not adjacent, so "so the four rate-limit variables are adjacent" is false as a reason.

`RELAY_DB_STATEMENT_TIMEOUT` sits between `RELAY_JOB_SUBMIT_RATE_LIMIT` and `RELAY_JOB_SEARCH_RATE_LIMIT` in the env table. Inserting the new row immediately after `RELAY_JOB_SEARCH_RATE_LIMIT` is still the right placement - it puts the new row beside the other per-user bucket - but the plan does not claim an adjacency the table does not have, and **does not reorder the table** to manufacture one. That would be an unrelated diff over four long rows.

### R6. A behavioural default-lane test CANNOT kill the four `main` mutations. The parser guard is not a preference here; it is the only instrument.

CLAUDE.md says to reach for the cheaper rung first, so this was checked rather than assumed:

- `main()` is not callable from a test, and nothing in `cmd/relay-server` constructs it. That is the whole premise of `idea-2026-08-14`.
- `main` opens the pgx pool, runs migrations and `log.Fatalf`s on failure **before** it reaches the `httpServerDeps` literal, so an `os/exec` test of the built binary would need a Postgres, which no lane in this package has.
- The only behavioural route is extracting env parsing into a testable function - a refactor across all four rate-limit variables that `idea-2026-08-14` explicitly owns (its "constructor-versus-guard decision") and that this slice's scope fence does not buy.

**Verdict: keep the parser guard, and record this evaluation in the PR body** (not in a comment - it is an argument, and arguments go in the commit message). Task 12 proves it against all four mutations by running them, which is what CLAUDE.md's `finishRegister` lesson actually demands: a parser guard that has not been evaded on purpose is not yet a guard.

### R7. Small corrections carried into the tasks

- The spec's `go test -bench` instrument **cannot produce a median or a p95**: `ns/op` is a mean over `b.N`. Task 1 therefore adds a throwaway timing test that measures individual operations and sorts them, and reports the mean from the benchmark beside it. A p95 quoted from `ns/op` would be a fabricated number.
- Every line citation in the spec was checked and all were accurate at `abf8119` (`auth.go:308`/`313`, `server.go:173`, `main.go:201-220`, `tokens.sql:44`, README 288-292 and 1561-1589). **This plan still cites by symbol only**, per the brief: line numbers rot and a rotted citation reads as a missing symbol.
- The spec's `auth.go:400` and `users.go:601` are `handleAdminPasswordReset` and `handleAdminCreateUser`. Both stay out of scope, as the spec decided; the plan names them by symbol in the README row's scope sentence so the omission stays a decision.

---

## File structure

| File | Action | Responsibility |
|---|---|---|
| `internal/api/server.go` | Modify | Two exported `Server` fields; one limiter built once in `Handler`; one route wrap; `Handler`'s doc comment. |
| `internal/api/ratelimit.go` | Modify | **`UserRateLimit`'s doc comment only.** No signature, no behaviour, no new symbol. |
| `cmd/relay-server/http_server.go` | Modify | Two `httpServerDeps` fields, two assignments in `buildHTTPServer`, two doc-comment edits. |
| `cmd/relay-server/main.go` | Modify | Parse `RELAY_PASSWORD_CHANGE_RATE_LIMIT`; two named fields at the `httpServerDeps` call site. |
| `cmd/relay-server/password_ratelimit_wiring_test.go` | Create | New, untagged, runs in CI. `putAsUser`, `passwordBucketServer`, four executed wiring tests plus the AST guard. |
| `internal/api/password_ratelimit_integration_test.go` | Create | `//go:build integration`. Does NOT run in CI. The success path and per-user isolation through the real auth chain. |
| `README.md` | Modify | One new env row; one sentence in the Session section. |
| `docs/backlog/idea-2026-08-14-generalize-the-env-to-field-wiring-guard.md` | Modify | One progress note; a priority-bump PROPOSAL, not a unilateral edit. |

**Do not touch:** `internal/api/ratelimit.go`'s `RateLimit`, `ParseRateLimit`, `userRateLimitKey`, `UserRateLimit`'s signature or body, `rateLimiter`, `clientIP`; `api.New`'s signature; any handler body including `handleChangePassword`; `internal/api/search_ratelimit.go`; `bcryptCost`; `internal/store/**`; `web/**`; `web/dist/**`; any spec or retro doc.

---

## Lane facts that decide where each test goes

Verified by reading `.github/workflows/go-ci.yml` in this worktree:

- The `test` job (`race + integration-build`) runs `go test -race ./... -timeout 180s` with **no tags**. Every untagged package runs in CI; every `//go:build integration` file does not.
- The two `services: postgres` jobs are `cli-integration` (`internal/cli`) and `pg-integration` (`internal/store`, `internal/schedrunner`, `internal/testsupport/pgdsn`). **Neither covers `internal/api`.**
- `internal/api/api_test.go` and `internal/api/testhelper_test.go` are integration-tagged, so `internal/api`'s handler harness is unavailable in the default lane. `newTestPool` starts **one Postgres container per call** via testcontainers - there is no `RELAY_TEST_DATABASE_URL` path in this package, unlike `internal/cli` and `internal/store`. Budget one container per new integration test.
- `cmd/relay-server/counters_wiring_test.go` and `job_submit_ratelimit_wiring_test.go` are untagged and run in CI. `stubAdminDB` there is the only Postgres-free seam in the repo that reaches a real route through `Server.Handler`.

**Nothing in this repo blocks a merge; `main` has no branch protection.** The lane decides where a guard is seen, not what is permitted.

---

## Task 1: Measure the cost-12 bcrypt pair on this machine

**No production code. Nothing here is committed.** The spec's section 10 makes this a precondition of merge and section 11 marks "roughly a quarter second" as quoted-and-unverified. Everything downstream that quotes a figure (Task 16's README row) depends on this task's output.

**Files:**
- Create then DELETE: `internal/api/zzz_bcrypt_cost_bench_test.go`

- [ ] **Step 1: Write the throwaway harness**

`package api`, not `api_test`, so it reads the shipped `bcryptCost` rather than a literal 12. Untagged, so it runs with no container.

```go
package api

// THROWAWAY. Delete before committing. A permanent benchmark of a third-party
// library's cost factor is a change detector on somebody else's code, and this
// repo has no benchmark lane to run it in.

import (
	"fmt"
	"runtime"
	"sort"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"
)

const benchPassword = "correct-horse-battery-staple" // 28 bytes, ASCII
const benchWrong = "correct-horse-battery-stapleX"  // 29 bytes, ASCII, does not match

var benchSink []byte

func BenchmarkBcryptCompare(b *testing.B) {
	h, err := bcrypt.GenerateFromPassword([]byte(benchPassword), bcryptCost)
	if err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := bcrypt.CompareHashAndPassword(h, []byte(benchWrong)); err == nil {
			b.Fatal("the wrong password must not verify: this is measuring the wrong path")
		}
	}
}

func BenchmarkBcryptGenerate(b *testing.B) {
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		out, err := bcrypt.GenerateFromPassword([]byte(benchPassword), bcryptCost)
		if err != nil {
			b.Fatal(err)
		}
		benchSink = out
	}
}

// TestBcryptPairDistribution times INDIVIDUAL operations, because ns/op is a
// mean over b.N and a median or a p95 cannot be read out of it.
func TestBcryptPairDistribution(t *testing.T) {
	const iterations = 50

	h, err := bcrypt.GenerateFromPassword([]byte(benchPassword), bcryptCost)
	if err != nil {
		t.Fatal(err)
	}

	measure := func(op func()) (median, p95, mean time.Duration) {
		d := make([]time.Duration, iterations)
		var total time.Duration
		for i := range d {
			start := time.Now()
			op()
			d[i] = time.Since(start)
			total += d[i]
		}
		sort.Slice(d, func(i, j int) bool { return d[i] < d[j] })
		return d[len(d)/2], d[(len(d)*95)/100], total / time.Duration(len(d))
	}

	cMed, cP95, cMean := measure(func() {
		if err := bcrypt.CompareHashAndPassword(h, []byte(benchWrong)); err == nil {
			t.Fatal("the wrong password must not verify")
		}
	})
	gMed, gP95, gMean := measure(func() {
		out, err := bcrypt.GenerateFromPassword([]byte(benchPassword), bcryptCost)
		if err != nil {
			t.Fatal(err)
		}
		benchSink = out
	})

	fmt.Printf("INPUT: cost=%d passwordBytes=%d iterations=%d go=%s GOMAXPROCS=%d GOOS=%s GOARCH=%s\n",
		bcryptCost, len(benchPassword), iterations, runtime.Version(), runtime.GOMAXPROCS(0),
		runtime.GOOS, runtime.GOARCH)
	fmt.Printf("COMPARE (wrong password): median=%s p95=%s mean=%s\n", cMed, cP95, cMean)
	fmt.Printf("GENERATE:                 median=%s p95=%s mean=%s\n", gMed, gP95, gMean)
	fmt.Printf("PAIR (compare+generate):  median=%s\n", cMed+gMed)
}
```

- [ ] **Step 2: Run it and capture the numbers**

```powershell
go test ./internal/api/ -run TestBcryptPairDistribution -v -timeout 300s
go test ./internal/api/ -run '^$' -bench 'BenchmarkBcrypt' -benchtime 20x -count 3 -timeout 300s
```

Expected: PASS, with the three `INPUT:` / `COMPARE` / `GENERATE` lines printed, and six benchmark lines.

- [ ] **Step 3: Capture the CPU model**

```powershell
(Get-CimInstance Win32_Processor).Name
```

- [ ] **Step 4: Record the result WITH ITS INPUT**

Write into the PR body draft (scratchpad file `lane-a-plan-measurement.md`, not the repo) a block of exactly this shape, filled from Step 2 and Step 3:

```
bcrypt cost 12, 28-byte ASCII password, 50 iterations each, single-threaded,
<CPU model>, go1.26.2, windows/amd64:
  compare (wrong password): median <X> ms, p95 <Y> ms
  generate:                 median <X> ms, p95 <Y> ms
  pair (success path):      median <X> ms
```

**A number without its input reads as the typical case.** The password byte length, the cost factor, the iteration count and the CPU must travel with the figure everywhere it is quoted, including in the README row.

- [ ] **Step 5: Apply the escalation rule and write down what the measurement changed**

Read the measured pair against the item's unverified "roughly a quarter second of a CPU core per request", and record one of these in the PR draft:

- **Pair > 1 s:** Task 16's README row must spell the aggregate arithmetic explicitly (`M accounts x 5 per minute x 2 operations x <pair>`) and point at `3:1m` for an untrusted farm.
- **Pair under 50 ms:** say so plainly in the README row instead of the word "expensive", and move the row's emphasis from CPU cost to the fact that the bound is free for every real user. **The item's conclusion still holds and must not be softened**: at 50 ms one principal saturates one core at 20 requests per second sustained, which any loop reaches trivially. State the arithmetic; do not repeat the adjective.
- **Between the two:** the row quotes the measured pair as-is with no extra emphasis either way.

**In every case the default stays `5:1m`.** It is bounded above by how many times a human retypes a password in a minute, not by a CPU budget, so no measurement can move it. If the measured figure materially contradicts the item's quarter-second, that contradiction is recorded in the PR body and in the `/backlog close` resolution note - **not** in any code comment, and not by editing the item's Summary in place.

- [ ] **Step 6: Delete the harness and prove it is gone**

```powershell
Remove-Item internal/api/zzz_bcrypt_cost_bench_test.go
git status --porcelain
```

Expected: `git status --porcelain` shows no `internal/api/zzz_bcrypt_cost_bench_test.go` entry at all.

- [ ] **Step 7: No commit.** This task commits nothing. Its whole output is the recorded measurement.

---

## Task 2: Record the behavioural RED at HEAD (integration lane)

Tasks 3 to 12 are on symbols absent at HEAD, so their RED is non-compilation - a weak RED. This task takes the real reproduction first: at HEAD, four consecutive `PUT /v1/users/me/password` from one user all reach the handler and all run the bcrypt compare.

**Files:**
- Create: `internal/api/password_ratelimit_integration_test.go`

**Lane:** integration only (`make test-integration` / `go test -tags integration`). Needs Docker Desktop running. **If Docker is unavailable, say so plainly in the PR - "the behavioural RED could not be taken, only the compile RED" - and do not substitute a default-lane test for it.** A default-lane substitute cannot reach the handler at all.

- [ ] **Step 1: Write the test in its HEAD-compilable form**

The server is built with the ordinary four-zero `api.New`, so no new symbol is referenced and the file compiles at HEAD. The 429 assertion is what fails.

```go
//go:build integration

package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"relay/internal/api"
	"relay/internal/events"
	"relay/internal/store"
	"relay/internal/worker"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// putPassword drives one PUT /v1/users/me/password through the handler h.
//
// h IS BOUND ONCE BY THE CALLER, never re-derived per request: Server.Handler
// allocates a fresh bucket for every armed user-keyed limiter on every call, so
// a test that calls srv.Handler() per request gives each request its own empty
// budget and can never observe a ceiling.
func putPassword(t *testing.T, h http.Handler, token, current, next string) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(map[string]string{"current_password": current, "new_password": next})
	require.NoError(t, err)
	req := httptest.NewRequest("PUT", "/v1/users/me/password", strings.NewReader(string(body)))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// TestChangePassword_ABurstIsRefusedAndAnotherUserIsNot is the behavioural
// reproduction and then the regression guard: at a limit of 3, a fourth attempt
// from one principal is refused before the handler runs, and a second
// principal's first attempt is not.
//
// EVERY ATTEMPT USES A WRONG CURRENT PASSWORD, so each one that reaches the
// handler answers 403 having run the compare and changed nothing. That makes
// 403 and 429 the two outcomes, and they say different things: 403 is "the
// bcrypt compare ran", 429 is "it did not".
func TestChangePassword_ABurstIsRefusedAndAnotherUserIsNot(t *testing.T) {
	pool := newTestPool(t)
	q := store.New(pool)
	srv := api.New(pool, q, events.NewBroker(), worker.NewRegistry(), nil, 0, 0, 0, 0)
	h := srv.Handler()

	tokenA := registerAndLogin(t, srv, q, "burst-a@test.com", "correctpassword")
	tokenB := registerAndLogin(t, srv, q, "burst-b@test.com", "correctpassword")

	for i := 1; i <= 3; i++ {
		rec := putPassword(t, h, tokenA, "wrongpassword", "newpassword1")
		require.Equal(t, http.StatusForbidden, rec.Code,
			"attempt %d must reach the handler and be refused by the compare. body: %s",
			i, rec.Body.String())
	}

	over := putPassword(t, h, tokenA, "wrongpassword", "newpassword1")
	require.Equal(t, http.StatusTooManyRequests, over.Code,
		"the fourth attempt must be refused BEFORE the bcrypt compare. A 403 here means the route "+
			"is unbounded and the compare ran. body: %s", over.Body.String())
	// PRESENCE ONLY. The header's VALUE is pinned for this exact middleware by
	// TestUserRateLimit_RetryAfterNamesWhenTheWindowActuallyClears, whose 55..61
	// band under a one-minute window is what kills a constant-retry mutation.
	// This slice adds no rate-limit arithmetic, so a second band assertion would
	// kill nothing.
	assert.NotEmpty(t, over.Header().Get("Retry-After"),
		"a refusal must tell the caller when to come back")

	other := putPassword(t, h, tokenB, "wrongpassword", "newpassword1")
	assert.Equal(t, http.StatusForbidden, other.Code,
		"the bucket is keyed on the authenticated user, so a second principal has its own budget")
}
```

Note `registerAndLogin` already exists in `internal/api/auth_integration_test.go` (same `package api_test`), and that file's `init()` already calls `api.SetBcryptCostForTest()`, so every hash in this lane is `bcrypt.MinCost`.

- [ ] **Step 2: Run it against HEAD and record the failure**

```powershell
go test -tags integration -p 1 ./internal/api/ -run TestChangePassword_ABurstIsRefusedAndAnotherUserIsNot -v -timeout 600s
```

Expected: FAIL, at the fourth attempt, with `Error: Not equal: expected: 429, actual: 403` and the body `{"error":"current password is incorrect"}`. **Paste that exact failure into the PR body.** That is the reproduction the item describes: four cost-12 compares from one authenticated principal, nothing refused.

- [ ] **Step 3: Commit the red test**

```bash
git add internal/api/password_ratelimit_integration_test.go
git commit -m "test: reproduce the unbounded bcrypt compare on PUT /v1/users/me/password

Four consecutive attempts from one authenticated principal all reach
handleChangePassword and all run bcrypt.CompareHashAndPassword. RED at
HEAD with 403 where 429 is required."
```

---

## Task 3: The RED for the wiring - the configured limit reaches the bucket

**Files:**
- Create: `cmd/relay-server/password_ratelimit_wiring_test.go`

**Lane:** default (untagged), runs in CI. No Docker, no Postgres.

- [ ] **Step 1: Write the file with its two helpers and the first test**

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

// putAsUser drives one PUT through the REAL http.Server buildHTTPServer
// returned, authenticated by stubAdminDB with no Postgres. Sibling of
// postAsUser in job_submit_ratelimit_wiring_test.go.
func putAsUser(t *testing.T, srv *http.Server, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("PUT", path, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer any-token-the-stub-resolves")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rec, req)
	return rec
}

// passwordBucketServer builds a server whose ONLY configured subsystem is the
// password-change bucket.
//
// LEAVING EVERY OTHER LIMIT FIELD ZERO IS LOAD-BEARING, not incidental: it is
// what makes a crossed assignment in buildHTTPServer
// (s.PasswordChangeLimitN = d.searchLimitN) produce a zero limit, an unarmed
// bucket and a RED test rather than a plausible one.
//
// pool is nil on purpose: every request below is answered before any pool use,
// and stubAdminDB panics on Exec and Query, so a handler that grew a write or a
// multi-row read fails loudly here.
func passwordBucketServer(n int, win time.Duration) *http.Server {
	return buildHTTPServer(httpServerDeps{
		addr:                   "127.0.0.1:0",
		q:                      store.New(stubAdminDB{}),
		passwordChangeLimitN:   n,
		passwordChangeLimitWin: win,
	})
}

// TestBuildHTTPServer_ThePasswordBucketIsWiredWithTheConfiguredLimit is the
// strongest available wiring guard for this route, and it covers four separate
// properties that a source scan does not:
//
//   - the route is wrapped at all;
//   - the composition order puts the limiter AFTER BearerAuth. Written
//     passwordLimit(auth(h)) the limiter runs before anything has put a
//     principal in the context, userRateLimitKey fails closed, and the FIRST
//     request answers 401 instead of 400 - the first assertion below, and the
//     failure names the composition order rather than the ceiling;
//   - the limiter uses the value buildHTTPServer was GIVEN, not a fresh or
//     hard-coded one;
//   - the limiter is constructed ONCE, at Handler() time. Built inside a route
//     closure or per request, every request carries its own empty map and the
//     third answers 400.
//
// THE 400 IS LOAD-BEARING. handleChangePassword refuses `{}` at
// len(NewPassword) < 8, before GetUser and before either bcrypt call, so the
// first two requests prove they reached the real handler with no database at
// all. A 429 there would mean the wired count is smaller than the configured
// one.
func TestBuildHTTPServer_ThePasswordBucketIsWiredWithTheConfiguredLimit(t *testing.T) {
	srv := passwordBucketServer(2, time.Minute)

	for i := 1; i <= 2; i++ {
		rec := putAsUser(t, srv, "/v1/users/me/password", `{}`)
		require.Equal(t, http.StatusBadRequest, rec.Code,
			"request %d must reach handleChangePassword and be refused by its length guard. "+
				"A 401 here means the limiter sits OUTSIDE the auth chain. body: %s",
			i, rec.Body.String())
	}

	rec := putAsUser(t, srv, "/v1/users/me/password", `{}`)
	require.Equal(t, http.StatusTooManyRequests, rec.Code,
		"the third request must be refused by the bucket buildHTTPServer was GIVEN. An unwrapped "+
			"route, a hard-coded count, a deleted or crossed assignment and a per-request limiter "+
			"all answer 400 here. body: %s", rec.Body.String())
	// PRESENCE ONLY. The header's VALUE is pinned for this exact middleware by
	// internal/api's TestUserRateLimit_RetryAfterNamesWhenTheWindowActuallyClears,
	// whose 55..61 band under a one-minute window kills a constant-retry
	// mutation. This slice adds no rate-limit arithmetic, so a second band
	// assertion here would kill nothing.
	require.NotEmpty(t, rec.Header().Get("Retry-After"),
		"a refusal must tell the caller when to come back")
}
```

- [ ] **Step 2: Run it and confirm it does not compile**

```powershell
go test ./cmd/relay-server/ -run TestBuildHTTPServer_ThePasswordBucketIsWiredWithTheConfiguredLimit -v -timeout 120s
```

Expected: FAIL to build, `unknown field passwordChangeLimitN in struct literal of type httpServerDeps` (and the same for `passwordChangeLimitWin`).

**This is a weak RED and the plan says so.** The behavioural RED for this slice is Task 2, already recorded.

- [ ] **Step 3: Do not commit yet.** Task 4 makes it green in the same commit; a commit that does not build is not useful to bisect.

---

## Task 4: The vertical - two `Server` fields, one limiter, one wrap, two deps fields

This is the minimum that makes Task 3's test compile and pass. It cannot be split smaller: the test drives a real request through `buildHTTPServer`'s output, so every hop has to exist.

**Files:**
- Modify: `internal/api/server.go` (`Server` struct, `Handler`)
- Modify: `cmd/relay-server/http_server.go` (`httpServerDeps`, `buildHTTPServer`)

- [ ] **Step 1: Add the two `Server` fields**

In `internal/api/server.go`, immediately after the `SearchLimitN` / `SearchLimitWin` pair and before the `searchLimiterOnce` field:

```go
	// PasswordChangeLimitN and PasswordChangeLimitWin bound how many
	// PUT /v1/users/me/password requests ONE AUTHENTICATED PRINCIPAL may issue
	// per window. Set by cmd/relay-server's buildHTTPServer from
	// RELAY_PASSWORD_CHANGE_RATE_LIMIT.
	//
	// A SMALLER CEILING THAN THE OTHER BUCKETS, because the handler runs a
	// bcrypt compare at the shipped cost on every request and a second bcrypt
	// operation on success, and the legitimate pattern is a human retyping a
	// credential into a form.
	//
	// Zero on EITHER field leaves the bucket off, which is what a Go caller
	// building a Server directly wants, and the guard in Handler is not
	// cosmetic: rateLimiter.allow indexes hits[0] whenever len(hits) >= limit,
	// so a zero limit panics on the first request. The environment cannot reach
	// that state - ParseRateLimit refuses a zero count and main is fatal on the
	// error - so the escape from a too-tight bound is a large number.
	PasswordChangeLimitN   int
	PasswordChangeLimitWin time.Duration
```

- [ ] **Step 2: Build the limiter in `Handler`, beside `userLimit`**

In `internal/api/server.go`'s `Handler`, immediately after the `userLimit` block:

```go
	// A SEPARATE bucket from userLimit, not a fourth route on it. That one
	// bounds how much task EXECUTION a principal buys, at a burst sized for job
	// submission; this bounds how much CPU in a key-derivation function it
	// buys, at a burst sized for a human retyping a password. Folded together,
	// either this route inherits a ceiling it can never reach or the submit
	// ceiling drops to password-change rates.
	//
	// Built here, not per route and not per request: UserRateLimit starts a gc
	// goroutine that is never stopped, so a second instance is a second budget
	// and a leak. TestBuildHTTPServer_ThePasswordBucketIsWiredWithTheConfigured-
	// Limit is what pins that, at a ceiling of two.
	passwordLimit := func(h http.Handler) http.Handler { return h }
	if s.PasswordChangeLimitN > 0 && s.PasswordChangeLimitWin > 0 {
		passwordLimit = UserRateLimit(s.PasswordChangeLimitN, s.PasswordChangeLimitWin)
	}
```

- [ ] **Step 3: Wrap the route - limiter INSIDE auth**

Replace the existing registration line in `Handler`:

```go
	mux.Handle("PUT /v1/users/me/password", auth(http.HandlerFunc(s.handleChangePassword)))
```

with:

```go
	// auth(passwordLimit(h)), never passwordLimit(auth(h)): UserRateLimit reads
	// the principal off the request context, which only BearerAuth puts there,
	// so the outer form refuses every request with a 401 it has no business
	// issuing. RELAY_PASSWORD_CHANGE_RATE_LIMIT.
	mux.Handle("PUT /v1/users/me/password", auth(passwordLimit(http.HandlerFunc(s.handleChangePassword))))
```

- [ ] **Step 4: Add the two `httpServerDeps` fields**

In `cmd/relay-server/http_server.go`, immediately after the `searchLimitN` / `searchLimitWin` pair:

```go
	// passwordChangeLimitN and passwordChangeLimitWin bound
	// PUT /v1/users/me/password per authenticated principal. They reach
	// api.Server as exported FIELDS, never as two more arguments on api.New,
	// whose tail is already four same-typed arguments in a row and whose
	// transposition this file's own header records as measured green.
	passwordChangeLimitN   int
	passwordChangeLimitWin time.Duration
```

- [ ] **Step 5: Forward them in `buildHTTPServer`**

Immediately after the `s.SearchLimitWin = d.searchLimitWin` line:

```go
	s.PasswordChangeLimitN = d.passwordChangeLimitN
	s.PasswordChangeLimitWin = d.passwordChangeLimitWin
```

- [ ] **Step 6: Run the test**

```powershell
go test ./cmd/relay-server/ -run TestBuildHTTPServer_ThePasswordBucketIsWiredWithTheConfiguredLimit -v -timeout 120s
```

Expected: PASS.

- [ ] **Step 7: Run both affected packages**

```powershell
go test ./internal/api/ ./cmd/relay-server/ -count=1 -timeout 300s
```

Expected: ok for both. No existing test changes behaviour: every `&Server{}` and every `api.New(..., 0,0,0,0)` leaves the new pair zero and the bucket unarmed.

- [ ] **Step 8: Commit**

```bash
git add internal/api/server.go cmd/relay-server/http_server.go cmd/relay-server/password_ratelimit_wiring_test.go
git commit -m "feat: bound PUT /v1/users/me/password with a per-user bucket

One UserRateLimit instance built once in Server.Handler and mounted inside
the auth chain. Sizing arrives as two named api.Server fields set by
buildHTTPServer."
```

---

## Task 5: Pin the composition order by mutation (named task, not an implementation detail)

Getting `auth(passwordLimit(h))` backwards is an authorization-shaped bug: every request answers 401 regardless of credentials, and a ceiling test that only counted requests would report it as a wrong number. This task proves the assertion that catches it actually catches it.

**Files:** none modified. This is a measurement.

- [ ] **Step 1: Copy the file before mutating it**

```powershell
Copy-Item internal/api/server.go $env:TEMP/lane-a-plan-server.go.bak
```

**Never `git checkout --` to revert a mutation**: it would discard the uncommitted work in the tree. Restore from this copy.

- [ ] **Step 2: Apply the mutation**

In `Handler`, change the registration to:

```go
	mux.Handle("PUT /v1/users/me/password", passwordLimit(auth(http.HandlerFunc(s.handleChangePassword))))
```

- [ ] **Step 3: Run and read WHICH assertion failed**

```powershell
go test ./cmd/relay-server/ -run TestBuildHTTPServer_ThePasswordBucketIsWiredWithTheConfiguredLimit -v -timeout 120s
```

Expected: FAIL on **request 1** with `expected: 400, actual: 401`, and the message naming the auth chain. **A failure on the third request instead would mean the test is catching something else and the composition property is unpinned** - if that happens, stop and report it.

- [ ] **Step 4: Restore and re-run the control**

```powershell
Copy-Item $env:TEMP/lane-a-plan-server.go.bak internal/api/server.go -Force
go test ./cmd/relay-server/ -run TestBuildHTTPServer_ThePasswordBucketIsWiredWithTheConfiguredLimit -v -timeout 120s
```

Expected: PASS. A mutation battery with no green control proves nothing.

- [ ] **Step 5: Verify the restore is byte-clean**

```powershell
git diff --stat internal/api/server.go
git ls-files --eol internal/api/server.go
```

Expected: the diffstat matches Task 4's change only, and the eol reads `i/lf`.

- [ ] **Step 6: Record the kill in the PR draft.** No commit.

---

## Task 6: A human's retry run is not refused

**Files:**
- Modify: `cmd/relay-server/password_ratelimit_wiring_test.go`

- [ ] **Step 1: Write the test**

```go
// TestBuildHTTPServer_AHumansRetryRunIsNotRefused is the executable form of "a
// normal password change is unaffected" for the case that actually produces a
// burst: a user who mistypes their current password and retries. Five attempts
// inside one minute is more than the two shipped clients can produce by hand -
// the SPA disables its button while the mutation is pending and the CLI asks
// three masked prompts per attempt - so the default ceiling is above anything a
// person reaches.
//
// THE SIXTH REQUEST IS NOT OPTIONAL. Five 400s under a limit of five are also
// what a limiter that does nothing produces, so without it this test is vacuous
// against exactly the implementation it describes.
func TestBuildHTTPServer_AHumansRetryRunIsNotRefused(t *testing.T) {
	srv := passwordBucketServer(5, time.Minute)

	for i := 1; i <= 5; i++ {
		rec := putAsUser(t, srv, "/v1/users/me/password", `{}`)
		require.Equal(t, http.StatusBadRequest, rec.Code,
			"attempt %d of a five-attempt retry run must reach the handler. body: %s",
			i, rec.Body.String())
	}

	over := putAsUser(t, srv, "/v1/users/me/password", `{}`)
	require.Equal(t, http.StatusTooManyRequests, over.Code,
		"the sixth attempt must be refused: without this the five 400s above are also what an "+
			"unwrapped route produces. body: %s", over.Body.String())
}
```

- [ ] **Step 2: Run it**

```powershell
go test ./cmd/relay-server/ -run TestBuildHTTPServer_AHumansRetryRunIsNotRefused -v -timeout 120s
```

Expected: PASS.

- [ ] **Step 3: Prove it is not vacuous** - temporarily change `passwordBucketServer(5, time.Minute)` to `passwordBucketServer(5, 0)` (an unarmed bucket), re-run, and confirm it FAILS on the sixth request with `expected: 429, actual: 400`. Restore the `time.Minute` and re-run to green.

- [ ] **Step 4: Commit**

```bash
git add cmd/relay-server/password_ratelimit_wiring_test.go
git commit -m "test: a five-attempt retype run is not refused, the sixth is"
```

---

## Task 7: The password bucket is separate from the submit bucket, both directions

**Files:**
- Modify: `cmd/relay-server/password_ratelimit_wiring_test.go`

- [ ] **Step 1: Write the test**

```go
// TestBuildHTTPServer_ThePasswordBucketIsSeparateFromTheSubmitBucket makes the
// two-buckets decision executable rather than prose, in the lane CI runs.
//
// THE MIDDLE ASSERTION IN EACH DIRECTION IS THE CONTROL. Without proving the
// first bucket is FULL, the final 400 is also what a fixture whose limiter never
// ran produces, and the test would be green for the wrong reason.
//
// Both buckets are set to 1 so a single spend fills either one.
func TestBuildHTTPServer_ThePasswordBucketIsSeparateFromTheSubmitBucket(t *testing.T) {
	build := func() *http.Server {
		return buildHTTPServer(httpServerDeps{
			addr:                   "127.0.0.1:0",
			q:                      store.New(stubAdminDB{}),
			jobSubmitLimitN:        1,
			jobSubmitLimitWin:      time.Minute,
			passwordChangeLimitN:   1,
			passwordChangeLimitWin: time.Minute,
		})
	}

	t.Run("a spent submit budget does not refuse a password change", func(t *testing.T) {
		srv := build()

		spend := postAsUser(t, srv, "/v1/jobs", `{}`)
		require.Equal(t, http.StatusBadRequest, spend.Code, "body: %s", spend.Body.String())
		over := postAsUser(t, srv, "/v1/jobs", `{}`)
		require.Equal(t, http.StatusTooManyRequests, over.Code,
			"control: the submit bucket must be provably full before the assertion below means anything")

		rec := putAsUser(t, srv, "/v1/users/me/password", `{}`)
		require.Equal(t, http.StatusBadRequest, rec.Code,
			"a spent submit budget must not refuse a password change: the two buckets bound "+
				"different quantities and sharing one would trade the wrong direction. body: %s",
			rec.Body.String())
	})

	t.Run("a spent password budget does not refuse a job submission", func(t *testing.T) {
		srv := build()

		spend := putAsUser(t, srv, "/v1/users/me/password", `{}`)
		require.Equal(t, http.StatusBadRequest, spend.Code, "body: %s", spend.Body.String())
		over := putAsUser(t, srv, "/v1/users/me/password", `{}`)
		require.Equal(t, http.StatusTooManyRequests, over.Code,
			"control: the password bucket must be provably full before the assertion below means anything")

		rec := postAsUser(t, srv, "/v1/jobs", `{}`)
		require.Equal(t, http.StatusBadRequest, rec.Code,
			"a spent password budget must not refuse a job submission. body: %s", rec.Body.String())
	})
}
```

- [ ] **Step 2: Run it**

```powershell
go test ./cmd/relay-server/ -run TestBuildHTTPServer_ThePasswordBucketIsSeparateFromTheSubmitBucket -v -timeout 120s
```

Expected: PASS, both subtests.

- [ ] **Step 3: Prove it kills the shared-bucket mutation** - in `internal/api/server.go`, temporarily change the password route's wrap to use `userLimit` instead of `passwordLimit`. Re-run: expected FAIL in direction A's final assertion with `expected: 400, actual: 429`. Restore from a copy (never `git checkout --`) and re-run to green.

- [ ] **Step 4: Commit**

```bash
git add cmd/relay-server/password_ratelimit_wiring_test.go
git commit -m "test: the password bucket and the submit bucket are independent, both directions"
```

---

## Task 8: A half-configured pair leaves the bucket off

**Files:**
- Modify: `cmd/relay-server/password_ratelimit_wiring_test.go`

- [ ] **Step 1: Write the test**

```go
// TestBuildHTTPServer_AHalfConfiguredPasswordLimitLeavesTheBucketOff pins the
// field pair's promise: zero on EITHER field leaves the bucket off, which is a
// conjunction and not a disjunction.
//
// THE ZERO-COUNT ROW IS THE DISCRIMINATING ONE. Relaxing the guard in
// Server.Handler to an OR constructs a limiter whose limit is 0, and
// rateLimiter.allow takes its over-limit branch on an empty window and indexes
// hits[0], so that row fails loudly on the first request. The zero-WINDOW row
// cannot discriminate against the same relaxation - a limiter with a zero window
// prunes every hit before it counts them and admits everything, exactly as no
// limiter does - and it is here to state the contract on the field the count row
// does not exercise.
func TestBuildHTTPServer_AHalfConfiguredPasswordLimitLeavesTheBucketOff(t *testing.T) {
	cases := []struct {
		name string
		n    int
		win  time.Duration
	}{
		{"window set, count zero", 0, time.Minute},
		{"count set, window zero", 5, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := passwordBucketServer(tc.n, tc.win)

			for i := 1; i <= 3; i++ {
				rec := putAsUser(t, srv, "/v1/users/me/password", `{}`)
				require.Equal(t, http.StatusBadRequest, rec.Code,
					"request %d: a half-configured pair must leave the bucket off, so every request "+
						"reaches the handler. body: %s", i, rec.Body.String())
			}
		})
	}
}
```

The discriminating row is placed FIRST: a decoy or a poisoned input read after its target is read by neither the code nor the mutant.

- [ ] **Step 2: Run it**

```powershell
go test ./cmd/relay-server/ -run TestBuildHTTPServer_AHalfConfiguredPasswordLimitLeavesTheBucketOff -v -timeout 120s
```

Expected: PASS, both rows.

- [ ] **Step 3: Prove the zero-count row kills the `||` relaxation** - in `internal/api/server.go` change `if s.PasswordChangeLimitN > 0 && s.PasswordChangeLimitWin > 0` to `||`. Re-run: expected FAIL on the `window set, count zero` row with a panic (`index out of range [0] with length 0`) inside `rateLimiter.allow`. Restore from a copy and re-run to green.

- [ ] **Step 4: Commit**

```bash
git add cmd/relay-server/password_ratelimit_wiring_test.go
git commit -m "test: zero on either password-limit field leaves the bucket unarmed"
```

---

## Task 9: The `Handler`-time construction property, stated where it is relied on

The one-limiter-per-`Server` property is pinned executably by Task 3's ceiling test (a per-request limiter gives every request an empty map and the third answers 400). What is left is the contract, which lives in `Handler`'s doc comment and is now incomplete.

**Files:**
- Modify: `internal/api/server.go` (`Handler`'s doc comment only)

- [ ] **Step 1: Replace the doc comment**

Replace:

```go
// Handler returns an http.Handler with all routes registered. Call it once per
// Server: each call allocates a fresh job-submit bucket and starts the gc
// goroutine that prunes it, and that goroutine is never stopped.
```

with:

```go
// Handler returns an http.Handler with all routes registered.
//
// CALL IT ONCE PER Server. Each call allocates a fresh bucket for every armed
// user-keyed limiter and starts a gc goroutine per bucket that nothing stops, so
// a second call is a second budget as well as a leak. Build every limiter here,
// never inside a route closure and never inside a handler: that is what makes
// "once per Server" the same statement as "once per Handler call".
//
// A test that drives more than one request through a limiter must bind the
// result once and reuse it; re-deriving it per request gives each request its
// own empty window.
```

- [ ] **Step 2: Verify the surviving claim against the code**

Re-read `Handler`'s body and confirm both armed limiters (`userLimit`, `passwordLimit`) are constructed in the function body and neither is constructed inside a closure. The lazily-built search limiter is not affected by this comment - it has its own `sync.Once` and its own doc comment in `internal/api/search_ratelimit.go`, which stays true and must not be edited.

- [ ] **Step 3: Run the package**

```powershell
go test ./internal/api/ ./cmd/relay-server/ -count=1 -timeout 300s
```

Expected: ok for both.

- [ ] **Step 4: Commit**

```bash
git add internal/api/server.go
git commit -m "docs: Handler allocates one bucket per armed limiter, not one job-submit bucket"
```

---

## Task 10: The RED for the wiring guard - `TestMain_PassesThePasswordChangeLimitItParsed`

**Files:**
- Modify: `cmd/relay-server/password_ratelimit_wiring_test.go`

**Read this before writing it:** `TestServerCountersIsWiredByMain`'s derivation walk skips any assignment where `len(as.Lhs) != len(as.Rhs)`, and the rate-limit parse binds three names from one call. Lifting that walk gives an empty `from` map and a guard that is RED on correct code. The walk below is the arity-tolerant one from `TestWatchdogIsStartedByMain`, restricted to direct children of `main`'s body.

- [ ] **Step 1: Add the imports**

Add to the file's import block: `"go/ast"`, `"go/parser"`, `"go/token"`, `"os"`, `"path/filepath"`, `"strconv"`, `"strings"`.

- [ ] **Step 2: Write the package-parsing helper**

```go
// mainBodyOfPackage returns the body of func main, found by parsing every
// non-test .go file in this directory rather than one hardcoded name.
//
// PARSE THE PACKAGE, NOT THE FILE, per the 2026-08-21 constraint in
// docs/backlog/idea-2026-08-14-generalize-the-env-to-field-wiring-guard.md: a
// guard written against "main.go" reports clean after the thing it guards moves
// to a sibling file, which has already happened once in this package.
func mainBodyOfPackage(t *testing.T) *ast.BlockStmt {
	t.Helper()
	entries, err := os.ReadDir(".")
	require.NoError(t, err)

	fset := token.NewFileSet()
	var body *ast.BlockStmt
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || filepath.Ext(name) != ".go" || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, 0)
		require.NoError(t, err, "parse %s", name)
		for _, d := range f.Decls {
			fd, ok := d.(*ast.FuncDecl)
			if !ok || fd.Recv != nil || fd.Name.Name != "main" || fd.Body == nil {
				continue
			}
			require.Nil(t, body, "this package declares func main more than once")
			body = fd.Body
		}
	}
	require.NotNil(t, body, "no func main with a body in any non-test file of this package")
	return body
}
```

- [ ] **Step 3: Write the guard**

```go
// TestMain_PassesThePasswordChangeLimitItParsed closes the one gap the executed
// tests above cannot reach: they supply the limits themselves, so they say
// nothing about what main puts in the httpServerDeps literal. Zeroing that
// literal, or trading it for another of main's same-typed locals, leaves this
// whole package green while the control is off in production - which is the
// worst available failure for a security control and is not stopped by a
// sentence.
//
// A PARSER GUARD IS THE EXPENSIVE FALLBACK, and it was taken here only because
// the cheaper rung does not exist: main is not callable from a test, and it
// opens the pool and can log.Fatalf before it reaches the literal, so no
// behavioural test in any lane this package has can observe that literal.
//
// DO NOT PASTE ANOTHER COPY OF THIS GUARD FAMILY. These rows belong in the table
// prescribed by
// docs/backlog/idea-2026-08-14-generalize-the-env-to-field-wiring-guard.md, and
// they are written in that table's shape - one row per wired field, columns for
// the field, the function its value must derive from, and the env-var literal
// that distinguishes it from a sibling of the same type - so a generalization
// lifts them without redesign.
//
// WHAT IT CANNOT SEE, so its name is not read as more than it checks: a value
// laundered through an intermediate local is followed, but a value TRANSFORMED
// on the way is not - `n2 := passwordChangeN / 2` passes every check here. It
// proves the wiring was not deleted, zeroed or crossed. It proves nothing about
// fidelity.
func TestMain_PassesThePasswordChangeLimitItParsed(t *testing.T) {
	body := mainBodyOfPackage(t)

	// from[name] = identifiers AND unquoted string literals its RHS mentions,
	// collected only from assignments that are DIRECT children of main's body,
	// so a parse moved inside an if reaches nothing.
	//
	// ARITY-TOLERANT ON PURPOSE. The parse is `n, win, err := ParseRateLimit(...)`:
	// three names bound by one call. A walk that skips len(Lhs) != len(Rhs) - the
	// shape in TestServerCountersIsWiredByMain, correct for its own subject -
	// collects nothing here and fails on correct code.
	//
	// STRING LITERALS ARE COLLECTED ALONGSIDE IDENTIFIERS, and that is what makes
	// the env-var check below possible at all: every rate-limit local is an int
	// or a time.Duration parsed by the same function, so nothing about a value's
	// type or its derivation distinguishes it from a sibling. The only thing that
	// does is the env-var name its chain was parsed from.
	from := map[string][]string{}
	for _, st := range body.List {
		as, ok := st.(*ast.AssignStmt)
		if !ok {
			continue
		}
		var rhs []string
		for _, e := range as.Rhs {
			ast.Inspect(e, func(m ast.Node) bool {
				if id, ok := m.(*ast.Ident); ok {
					rhs = append(rhs, id.Name)
				}
				if bl, ok := m.(*ast.BasicLit); ok && bl.Kind == token.STRING {
					if s, err := strconv.Unquote(bl.Value); err == nil {
						rhs = append(rhs, s)
					}
				}
				return true
			})
		}
		for _, l := range as.Lhs {
			if id, ok := l.(*ast.Ident); ok {
				from[id.Name] = append(from[id.Name], rhs...)
			}
		}
	}

	// Every identifier assigned anywhere in main's subtree - ifs, loops,
	// switches and closures included. Derivation alone is defeated by a later
	// `name = 0` inside an if, which was a live green evasion in this package
	// and not a hypothesis.
	assignedAnywhere := map[string]int{}
	ast.Inspect(body, func(n ast.Node) bool {
		as, ok := n.(*ast.AssignStmt)
		if !ok {
			return true
		}
		for _, l := range as.Lhs {
			if id, ok := l.(*ast.Ident); ok {
				assignedAnywhere[id.Name]++
			}
		}
		return true
	})

	// The single buildHTTPServer(httpServerDeps{...}) literal.
	fields := map[string]ast.Expr{}
	calls := 0
	ast.Inspect(body, func(n ast.Node) bool {
		ce, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		id, ok := ce.Fun.(*ast.Ident)
		if !ok || id.Name != "buildHTTPServer" {
			return true
		}
		calls++
		require.Len(t, ce.Args, 1)
		cl, ok := ce.Args[0].(*ast.CompositeLit)
		require.True(t, ok,
			"buildHTTPServer must be called with an httpServerDeps composite literal at the call "+
				"site, so every dependency is readable there")
		for _, e := range cl.Elts {
			kv, ok := e.(*ast.KeyValueExpr)
			if !ok {
				continue
			}
			if k, ok := kv.Key.(*ast.Ident); ok {
				fields[k.Name] = kv.Value
			}
		}
		return true
	})
	require.Equal(t, 1, calls,
		"main must call buildHTTPServer exactly once: called twice the last one decides and this "+
			"guard cannot say which")

	const envVar = "RELAY_PASSWORD_CHANGE_RATE_LIMIT"
	const defaultValue = "5:1m"

	rows := []struct{ field, mustReach, envVar string }{
		{"passwordChangeLimitN", "ParseRateLimit", envVar},
		{"passwordChangeLimitWin", "ParseRateLimit", envVar},
	}

	for _, row := range rows {
		value, present := fields[row.field]
		require.True(t, present,
			"buildHTTPServer is called with no %s field, so the bucket is unarmed in production "+
				"while every test in this package stays green", row.field)

		ident, isIdent := value.(*ast.Ident)
		require.True(t, isIdent,
			"httpServerDeps.%s must be fed a plain identifier, not %T. A literal there is a "+
				"hard-coded bound that %s no longer controls.", row.field, value, row.envVar)

		seen := map[string]bool{}
		queue := []string{ident.Name}
		reachedFn, reachedEnv := false, false
		var otherEnv []string
		for len(queue) > 0 {
			name := queue[0]
			queue = queue[1:]
			if seen[name] {
				continue
			}
			seen[name] = true
			switch {
			case name == row.mustReach:
				reachedFn = true
			case name == row.envVar:
				// Checked BEFORE the RELAY_*_RATE_LIMIT arm below, which would
				// otherwise match this variable's own name.
				reachedEnv = true
			case strings.HasPrefix(name, "RELAY_") && strings.HasSuffix(name, "_RATE_LIMIT"):
				otherEnv = append(otherEnv, name)
			}
			queue = append(queue, from[name]...)
		}

		require.True(t, reachedFn,
			"httpServerDeps.%s is fed %q, which does not derive from %s through an unconditional "+
				"assignment in main's body", row.field, ident.Name, row.mustReach)
		require.True(t, reachedEnv,
			"httpServerDeps.%s is fed %q, whose chain never mentions %s. Both values on this route "+
				"are parsed by the same function from a same-typed sibling variable, so the env-var "+
				"name is the only thing that says WHICH bound arrived.", row.field, ident.Name, row.envVar)
		require.Empty(t, otherEnv,
			"httpServerDeps.%s is fed %q, whose chain reaches %v - another rate limit's variable. "+
				"The password route would then be bounded at some other control's budget.",
			row.field, ident.Name, otherEnv)
		require.Equal(t, 1, assignedAnywhere[ident.Name],
			"%q is assigned %d times inside main. Exactly one unconditional assignment is the whole "+
				"basis on which this test concludes anything: a second one, in an if or a loop, can "+
				"take the wiring back on some deployments while every check above still passes.",
			ident.Name, assignedAnywhere[ident.Name])

		if row.field == "passwordChangeLimitN" {
			// DOC-AND-CODE CONSISTENCY, not a behavioural check: its subject is
			// the README row, which states this default as a number an operator
			// plans against. It cannot tell which of the two strings on that
			// statement is the key and which is the default - both are collected
			// off the same RHS - so it says the pair is present, nothing more.
			require.True(t, seen[defaultValue],
				"main no longer defaults %s to %q, so the README row states a number the binary "+
					"does not use", row.envVar, defaultValue)
		}
	}
}
```

- [ ] **Step 4: Run it and confirm the RED**

```powershell
go test ./cmd/relay-server/ -run TestMain_PassesThePasswordChangeLimitItParsed -v -timeout 120s
```

Expected: FAIL with `buildHTTPServer is called with no passwordChangeLimitN field, so the bucket is unarmed in production while every test in this package stays green`.

- [ ] **Step 5: Do not commit yet.** Task 11 makes it green.

---

## Task 11: Parse `RELAY_PASSWORD_CHANGE_RATE_LIMIT` in `main`

**Files:**
- Modify: `cmd/relay-server/main.go`

- [ ] **Step 1: Add the parse, immediately after the `searchN, searchWin` parse**

```go
	// A THIRD INSTANCE of the user-keyed mechanism, and separate from both
	// existing buckets. The quantity bounded here is CPU spent in a key
	// derivation function, not task execution and not scan work; the ceilings
	// are three orders of magnitude apart and no single value works for two of
	// them. Folded into the submit bucket, this route would inherit a ceiling a
	// human can never reach.
	//
	// TIGHTER THAN RELAY_LOGIN_RATE_LIMIT ON PURPOSE: a refused login is a user
	// who cannot get in, while a refused password change is a user who already
	// holds a valid session, whose session is untouched, and who waits.
	//
	// THERE IS NO OFF VALUE, deliberately: ParseRateLimit rejects a zero count
	// and this is fatal, so an operator cannot disable the control from the
	// environment. The escape is a large number, 100000:1s, which leaves the
	// bound visible as a number in README and in the environment.
	passwordChangeN, passwordChangeWin, err := api.ParseRateLimit(
		envOrDefault("RELAY_PASSWORD_CHANGE_RATE_LIMIT", "5:1m"))
	if err != nil {
		log.Fatalf("parse RELAY_PASSWORD_CHANGE_RATE_LIMIT: %v", err)
	}
```

- [ ] **Step 2: Add the two named fields to the `httpServerDeps` literal**

Immediately after `searchLimitWin: searchWin,`:

```go
		passwordChangeLimitN:   passwordChangeN,
		passwordChangeLimitWin: passwordChangeWin,
```

- [ ] **Step 3: Run the guard and the package**

```powershell
go test ./cmd/relay-server/ -run TestMain_PassesThePasswordChangeLimitItParsed -v -timeout 120s
go test ./cmd/relay-server/ -count=1 -timeout 300s
go build ./...
```

Expected: PASS, ok, and a clean build.

- [ ] **Step 4: Commit**

```bash
git add cmd/relay-server/main.go cmd/relay-server/password_ratelimit_wiring_test.go
git commit -m "feat: RELAY_PASSWORD_CHANGE_RATE_LIMIT, default 5:1m, with a wiring guard

The guard is a parser guard because main is not callable from a test and
reaches the httpServerDeps literal only after opening the pool. Its rows are
written in the shape idea-2026-08-14 prescribes."
```

---

## Task 12: Prove the wiring guard against all four mutations

A parser guard that has not been evaded on purpose is not yet a guard: the `finishRegister` guard in this repo was written five times before it held. **Run every one of these. Restore from a copy each time, never `git checkout --`, and re-run the control after each restore.**

**Files:** none modified permanently.

- [ ] **Step 1: Copy `main.go` and confirm the green control**

```powershell
Copy-Item cmd/relay-server/main.go $env:TEMP/lane-a-plan-main.go.bak
go test ./cmd/relay-server/ -run TestMain_PassesThePasswordChangeLimitItParsed -v -timeout 120s
```

Expected: PASS. A battery with no green baseline reports uniform results and proves nothing.

- [ ] **Step 2: M1 - the literal set to `0`**

Change the literal line to `passwordChangeLimitN:   0,`. Run the guard.
Expected FAIL: `httpServerDeps.passwordChangeLimitN must be fed a plain identifier, not *ast.BasicLit`.
Restore from the copy; re-run to green.

- [ ] **Step 3: M2 - fed another same-typed local**

Change the literal line to `passwordChangeLimitN:   searchN,`. Run the guard.
Expected FAIL: the `reachedEnv` assertion (`whose chain never mentions RELAY_PASSWORD_CHANGE_RATE_LIMIT`) or the `otherEnv` assertion naming `[RELAY_JOB_SEARCH_RATE_LIMIT]`. **Read which one fired and record it** - a kill must name its guard, and a mutation that reddens a test for a different reason is not a kill.
Restore; re-run to green.

- [ ] **Step 4: M3 - the field omitted**

Delete the `passwordChangeLimitWin: passwordChangeWin,` line. Run the guard.
Expected FAIL: `buildHTTPServer is called with no passwordChangeLimitWin field`.
Restore; re-run to green.

- [ ] **Step 5: M4 - a later reassignment inside an `if`**

Insert, between the parse and the `buildHTTPServer` call:

```go
	if os.Getenv("RELAY_HTTP_ADDR") == "" {
		passwordChangeN = 0
	}
```

Run the guard.
Expected FAIL: `"passwordChangeN" is assigned 2 times inside main`.
**Also run the executed tests** (`go test ./cmd/relay-server/ -count=1`) and confirm they stay GREEN under this mutation - that is the demonstration that the parser guard is buying something the executed tests cannot.
Restore; re-run to green.

- [ ] **Step 6: Verify the tree is clean after the battery**

```powershell
git diff --stat cmd/relay-server/main.go
git ls-files --eol cmd/relay-server/main.go
```

Expected: the diffstat matches Task 11's change only; the eol reads `i/lf`.

- [ ] **Step 7: Record all four kills, each with the assertion that fired, in the PR draft.** No commit.

---

## Task 13: `buildHTTPServer`'s doc comment - add the pair, scope the closing claim

The closing paragraph of `buildHTTPServer`'s comment ("THAT LAST CLAIM STOPS AT THIS FUNCTION'S OWN ASSIGNMENTS ... The named fields removed the transposition hazard at the `api.New` boundary, not at the literal that feeds this one") becomes false as a general statement the moment Task 10's guard exists.

**Files:**
- Modify: `cmd/relay-server/http_server.go` (doc comment only)

- [ ] **Step 1: Extend the per-pair list**

In the second bullet of the "THAT CLAIM IS ABOUT Counters AND NOTHING ELSE" block, after the sentence about the SearchLimit pair, add:

```
//     The PasswordChangeLimit pair is guarded the same way, through
//     TestBuildHTTPServer_ThePasswordBucketIsWiredWithTheConfiguredLimit, which
//     drives three real requests through this function's output at a ceiling of
//     two and asserts 400, 400, 429.
```

- [ ] **Step 2: Scope the closing paragraph**

Replace the final sentence of that paragraph:

```
// Closing it needs a wiring guard general enough to cover an
// env-to-field pair, which is
// docs/backlog/idea-2026-08-14-generalize-the-env-to-field-wiring-guard.md.
```

with:

```
// It remains true of the jobSubmitLimit and searchLimit pairs. It is NOT true of
// the passwordChangeLimit pair, whose literal is covered by
// TestMain_PassesThePasswordChangeLimitItParsed until a guard general enough to
// cover any env-to-field pair subsumes it -
// docs/backlog/idea-2026-08-14-generalize-the-env-to-field-wiring-guard.md.
```

- [ ] **Step 3: Re-verify the surviving sentences**

Rewriting prose regenerates claims. Read the whole comment again against the function body and confirm each surviving claim still holds: `api.New` really does still take four same-typed arguments in a row; the `Metrics`, `StaticHandler` and `AllowSelfRegister` assignments really are still unguarded; the three `s.Counters` filters are unchanged.

- [ ] **Step 4: Run and commit**

```powershell
go test ./cmd/relay-server/ -count=1 -timeout 300s
```

```bash
git add cmd/relay-server/http_server.go
git commit -m "docs: scope buildHTTPServer's uncovered-literal claim to the two pairs it still describes"
```

---

## Task 14: `UserRateLimit`'s doc comment - delete the enumeration, keep the reason

The comment currently says the user is "also the unit the bounded cost belongs to" on `POST /v1/jobs` and that this is "NOT true of **the other two routes**". With a fourth mount that enumeration is a stale count of other code and its arithmetic is wrong.

**Files:**
- Modify: `internal/api/ratelimit.go` (doc comment only - **not the function**)

- [ ] **Step 1: Replace the enumerating sentences**

Replace:

```go
// On POST /v1/jobs it is also the unit the bounded cost belongs to: the task rows
// and subprocess spawns are charged to the submitter's own jobs. That is NOT true
// of the other two routes - an admin may retry another user's job or fire another
// user's schedule, and CreateJobFromSpec charges the execution to the owner while
// the bucket is charged to the admin - so the argument that carries all three is
// the operational one.
```

with:

```go
// On a SELF-SCOPED route - one that takes every identifier from the context
// principal - the principal charged is the principal the work is done to, so the
// bucket is also the unit the bounded cost belongs to. Where one principal may
// act on another's resource that does not hold: the bucket is charged to the
// caller while the work is charged to the owner. So the argument that carries
// every mount is the operational one below, not the cost-ownership one.
```

- [ ] **Step 2: Re-verify the surviving half against the router**

A correction writes a fresh claim, so re-read the rest of the comment against `Server.Handler`'s route list and confirm each surviving sentence is still true - in particular `IT MUST BE MOUNTED INSIDE THE AUTH CHAIN and outside any admin gate` (all four mounts satisfy it: three are `auth(userLimit(h))` with no admin gate, and the fourth is `auth(passwordLimit(h))`), and the 401-before-`allow` paragraph (unchanged code).

- [ ] **Step 3: Confirm the function body is untouched**

```powershell
git diff internal/api/ratelimit.go
```

Expected: comment lines only. No change below `func UserRateLimit(`.

- [ ] **Step 4: Run and commit**

```powershell
go test ./internal/api/ -count=1 -timeout 300s
```

```bash
git add internal/api/ratelimit.go
git commit -m "docs: UserRateLimit's cost-ownership note states the reason, not a route census"
```

---

## Task 15: The integration proof that a normal change still succeeds under the bucket

**Files:**
- Modify: `internal/api/password_ratelimit_integration_test.go`

**Lane:** integration only. Does NOT run in CI.

- [ ] **Step 1: Arm the bucket in the burst test from Task 2**

Replace its server construction with:

```go
	srv := api.New(pool, q, events.NewBroker(), worker.NewRegistry(), nil, 0, 0, 0, 0)
	srv.PasswordChangeLimitN = 3
	srv.PasswordChangeLimitWin = time.Minute
	h := srv.Handler()
```

(and add `"time"` to the imports). The three-attempt loop and the fourth-attempt 429 now match the configured ceiling.

- [ ] **Step 2: Add the success-path test**

```go
// TestChangePassword_ANormalChangeSucceedsUnderTheBucket is the acceptance
// criterion "a normal password change is unaffected", proven end to end: with
// the bucket armed, one correct change answers 204 and the caller's own token
// still authenticates afterwards.
//
// WHY IT CANNOT RUN IN THE DEFAULT LANE, per CLAUDE.md's rule that a guard
// behind a build tag must be able to run. It is the only assertion in this slice
// that reaches SetPasswordHash and the commit, which needs a pool and a
// transaction. cmd/relay-server's wiring tests reach handleChangePassword with
// no Postgres but stop at its length guard, because stubAdminDB cannot answer
// GetUser. What would have to exist for this to run in CI is a services:postgres
// lane covering internal/api, which
// docs/superpowers/specs/2026-09-04-integration-guards-ci-coverage.md section
// 4.1 excludes on cost and which
// docs/backlog/idea-2026-08-23-integration-only-guards-ci-never-runs.md tracks.
//
// IT CANNOT MEASURE THE COMPARE'S COST AT ALL. This lane calls
// api.SetBcryptCostForTest(), so every hash here is bcrypt.MinCost and the
// compare is cheap by construction. The shipped cost is measured separately and
// recorded in the PR.
func TestChangePassword_ANormalChangeSucceedsUnderTheBucket(t *testing.T) {
	pool := newTestPool(t)
	q := store.New(pool)
	srv := api.New(pool, q, events.NewBroker(), worker.NewRegistry(), nil, 0, 0, 0, 0)
	srv.PasswordChangeLimitN = 5
	srv.PasswordChangeLimitWin = time.Minute
	// BOUND ONCE. Handler allocates a fresh bucket per call, so re-deriving it
	// per request would give every request its own budget.
	h := srv.Handler()

	token := registerAndLogin(t, srv, q, "under-bucket@test.com", "oldpassword")

	rec := putPassword(t, h, token, "oldpassword", "newpassword1")
	require.Equal(t, http.StatusNoContent, rec.Code,
		"one correct change under an armed bucket must succeed. body: %s", rec.Body.String())

	probe := httptest.NewRequest("GET", "/v1/jobs", nil)
	probe.Header.Set("Authorization", "Bearer "+token)
	probeRec := httptest.NewRecorder()
	h.ServeHTTP(probeRec, probe)
	assert.Equal(t, http.StatusOK, probeRec.Code,
		"the caller's own token survives its own password change")

	login, err := json.Marshal(map[string]string{
		"email": "under-bucket@test.com", "password": "newpassword1",
	})
	require.NoError(t, err)
	loginReq := httptest.NewRequest("POST", "/v1/auth/login", strings.NewReader(string(login)))
	loginReq.Header.Set("Content-Type", "application/json")
	loginRec := httptest.NewRecorder()
	h.ServeHTTP(loginRec, loginReq)
	assert.Equal(t, http.StatusCreated, loginRec.Code,
		"the stored hash actually changed: the new password authenticates")
}
```

- [ ] **Step 2b: Note the shared login limiter**

`h` is one handler, so the login request above shares the server's login bucket - which is unarmed here (`api.New(..., 0, 0, 0, 0)`), so one login cannot be refused. Do not arm the login limits in this test.

- [ ] **Step 3: Run both integration tests**

```powershell
go test -tags integration -p 1 ./internal/api/ -run 'TestChangePassword_A(NormalChangeSucceedsUnderTheBucket|BurstIsRefusedAndAnotherUserIsNot)' -v -timeout 900s
```

Expected: PASS, both. Each starts its own Postgres container, so budget several minutes.

- [ ] **Step 4: Vet the tagged build**

```powershell
go vet -tags integration ./...
```

Expected: clean.

- [ ] **Step 5: Commit**

```bash
git add internal/api/password_ratelimit_integration_test.go
git commit -m "test: a normal password change succeeds under the armed bucket, end to end"
```

---

## Task 16: The README env-table row

**Files:**
- Modify: `README.md`

- [ ] **Step 1: Insert one row immediately after the `RELAY_JOB_SEARCH_RATE_LIMIT` row**

Do not reorder the table. Substitute `<PAIR>` with the measured median pair from Task 1, carried with its input (for example: `about 0.25 s on the reference machine - cost 12, a 28-byte password, median of 50 iterations`). Nothing else in this row is a placeholder.

```
| `RELAY_PASSWORD_CHANGE_RATE_LIMIT` | `5:1m` | Per-**user** rate limit (format `N:duration`) over `PUT /v1/users/me/password`, and that route only. **One bucket keyed on the authenticated user, not on the source address** - the same key and the same reasoning as the submit row above, which is not repeated here. **Why the bound is this small:** the handler runs a bcrypt compare at cost 12 on every request and a second bcrypt operation on success, so the work is CPU by construction and cannot be made cheaper without lowering the cost factor that protects stored passwords. Measured: <PAIR>. **What it costs a legitimate user:** somebody who mistypes their current password five times inside a minute waits up to a minute before the sixth attempt. Their session is untouched and they are not locked out. **Why it is tighter than `RELAY_LOGIN_RATE_LIMIT`:** a refused login is a user who cannot get in; a refused password change is a user who already holds a valid session and waits. A refusal is `429` and carries `Retry-After`, which is correct for a scripted client; no first-party client surfaces it, because `relayclient.ResponseError` and the SPA's `ApiError` both carry the status and not the headers. **It bounds repetition of the bcrypt work, not request volume** - the limiter runs inside the auth chain, so every attempt, refused or not, still costs one token lookup; the control for request volume would be an admission bound at the HTTP listener, which relay does not have. **Per replica and in memory:** two replicas give a caller twice the budget and a restart clears every bucket. **There is no off value** - `ParseRateLimit` refuses a zero count and the server refuses to boot on `0:1m`, exactly as the three rows above do; to widen it effectively, set a large number such as `100000:1s`, which leaves the bound visible in the environment. **The budget is per principal, so what an actor with M accounts buys is M x 5 attempts per minute, each costing one compare and, on success, a second bcrypt operation**; minting accounts through `POST /v1/auth/register` costs a cost-12 hash and is bounded by `RELAY_REGISTER_RATE_LIMIT` per source address, which is a real brake and a weak one because that limiter keys per IPv6 `/128`. For a farm whose users are not all trusted, `3:1m` is the recommended value; its whole cost is that a user who mistypes three times waits up to a minute, because no workflow on this route is scripted. `POST /v1/users/password-reset` and `POST /v1/users` also run a cost-12 hash and are deliberately **not** covered: both are admin-only, and rate-limiting an admin remedy refuses the operator's action during the incident they are responding to. |
```

- [ ] **Step 2: Apply the escalation rule from Task 1 Step 5**

- Pair over 1 s: keep the row as written; it already spells the aggregate.
- Pair under 50 ms: replace `so the work is CPU by construction and cannot be made cheaper without lowering the cost factor that protects stored passwords. Measured: <PAIR>.` with `Measured: <PAIR> - small per request, and the point is the aggregate: one authenticated principal issuing them in a loop saturates a core, and the bound is free for every real user.`

- [ ] **Step 3: Run the post-edit checks CLAUDE.md mandates**

```powershell
git diff --stat README.md
git ls-files --eol README.md
```

Expected: `1 file changed, 1 insertion(+)` and `i/lf`. **A diffstat larger than the change you intended means the edit reclassified line endings; stop and investigate rather than committing.**

- [ ] **Step 4: Assert the encoding**

```powershell
$bytes = [System.IO.File]::ReadAllBytes("README.md")
($bytes | Where-Object { $_ -gt 127 }).Count
[System.Text.Encoding]::UTF8.GetString($bytes).Contains([char]0xFFFD)
```

Expected: the non-ASCII count is unchanged from before the edit (record it first), and the replacement-character check prints `False`. **The row above is pure ASCII by design** - no em dashes, no en dashes, no accented characters - so any change in the non-ASCII count means the edit introduced a byte it should not have.

- [ ] **Step 5: Commit**

```bash
git add README.md
git commit -m "docs: document RELAY_PASSWORD_CHANGE_RATE_LIMIT with its measured cost"
```

---

## Task 17: The README Session-section sentence

**Files:**
- Modify: `README.md`

- [ ] **Step 1: Add one sentence to the Session section's bullet list**

After the existing bullet `- There is no per-session revoke endpoint. ...`, add:

```
- `PUT /v1/users/me/password` is rate-limited per authenticated user by
  `RELAY_PASSWORD_CHANGE_RATE_LIMIT` (default five per minute), because the handler runs a
  bcrypt compare on every request. Over the ceiling it answers `429` with `Retry-After`; the
  caller's session is untouched.
```

- [ ] **Step 2: Re-run the same CRLF and encoding checks as Task 16 Steps 3 and 4**

Expected: `1 file changed, 4 insertions(+)` and `i/lf`.

- [ ] **Step 3: Commit**

```bash
git add README.md
git commit -m "docs: name the password-change bound where the API reference lists the route"
```

---

## Task 18: The progress note on the wiring-guard item

The spec makes this required scope, not housekeeping: the item's own Notes ask for the priority to be reconsidered the next time somebody adds a post-construction field in `cmd/relay-server`, and this slice is that time.

**Files:**
- Modify: `docs/backlog/idea-2026-08-14-generalize-the-env-to-field-wiring-guard.md`

- [ ] **Step 1: Append a dated section at the end of the file**

```markdown
## 2026-09-04: another copy, and a row already written in this item's shape

The change-password rate-limit slice added `passwordChangeLimitN` /
`passwordChangeLimitWin` to `httpServerDeps` and guarded main's literal with
`TestMain_PassesThePasswordChangeLimitItParsed`
(`cmd/relay-server/password_ratelimit_wiring_test.go`).

Three things that slice owes this item:

- **The guard was written as this item's table**, not as a bespoke walk: one row per wired
  field, with columns for the field, the function its value must derive from, and the env-var
  literal that distinguishes it from a same-typed sibling. Lifting it should be a matter of
  moving rows, not redesigning the walk.
- **A correctness note for whoever generalizes.** `TestServerCountersIsWiredByMain`'s
  derivation walk skips any assignment where `len(Lhs) != len(Rhs)`. Every rate-limit parse in
  `main` is `n, win, err := api.ParseRateLimit(...)` - three names, one call - so that walk
  collects nothing for either name and a generalization built on it is RED on correct code.
  The arity-tolerant walk in `TestWatchdogIsStartedByMain` is the one to lift.
- **All four mutations this item cares about were run against the new guard and all four were
  killed**: the literal set to `0`, the field fed another same-typed local, the field omitted,
  and a later `= 0` inside an `if`. The executed tests in the same package stayed green under
  the fourth, which is the clearest available demonstration of what the parse buys over
  execution here.

**Proposed, for the human rather than applied: raise this item from `low` to `medium`.** The
argument is this item's own Notes - "reconsider the priority the next time somebody adds a
post-construction field in `cmd/relay-server`" - and the fact that the next slice under time
pressure now has one more nearby copy to paste.
```

- [ ] **Step 2: Do NOT edit the `priority:` frontmatter.** The bump is a proposal for the human, and the item stays `open`.

- [ ] **Step 3: Run the eol check**

```powershell
git diff --stat docs/backlog/idea-2026-08-14-generalize-the-env-to-field-wiring-guard.md
git ls-files --eol docs/backlog/idea-2026-08-14-generalize-the-env-to-field-wiring-guard.md
```

Expected: insertions only, `i/lf`.

- [ ] **Step 4: Commit**

```bash
git add docs/backlog/idea-2026-08-14-generalize-the-env-to-field-wiring-guard.md
git commit -m "backlog: record another wiring-guard copy and propose the priority bump its Notes ask for"
```

---

## Task 19: Final gates and the PR body

- [ ] **Step 1: Default lane, whole repo**

```powershell
go build ./...
go vet ./...
go vet -tags integration ./...
go test ./... -count=1 -timeout 600s
```

Expected: all green.

- [ ] **Step 2: Race lane**

The native Windows lane is unreliable; the container is the route that works and is also the only local way to run `//go:build !windows` files:

```bash
MSYS_NO_PATHCONV=1 docker run --rm -v "$(pwd -W):/src" -w /src -e CGO_ENABLED=1 \
  golang:1.26 go test -race ./... -count=1 -timeout 600s
```

If the lane is genuinely unavailable, **say so in the PR** rather than substituting `-count=N` repetition, which is not equivalent.

- [ ] **Step 3: Confirm the scope fence held**

```powershell
git diff --stat main...HEAD
```

Expected files, and nothing else: `internal/api/server.go`, `internal/api/ratelimit.go` (comment only), `internal/api/password_ratelimit_integration_test.go`, `cmd/relay-server/http_server.go`, `cmd/relay-server/main.go`, `cmd/relay-server/password_ratelimit_wiring_test.go`, `README.md`, `docs/backlog/idea-2026-08-14-generalize-the-env-to-field-wiring-guard.md`, plus the plan and spec docs.

**Nothing under `internal/store/`, nothing under `web/` or `web/dist/`, no change to `api.New`'s signature, `ParseRateLimit`, `RateLimit`, `UserRateLimit`'s signature or body, `bcryptCost`, or any handler body.**

- [ ] **Step 4: Write the PR body**

It must carry, each in its own section:

1. **The measurement, with its input** - the block from Task 1 Step 4, and which escalation branch Task 16 took.
2. **The behavioural RED at HEAD** - Task 2's exact `expected: 429, actual: 403` failure, or the plain statement that Docker was unavailable and only the compile RED was taken.
3. **The mutation battery** - one line per mutation with the assertion that fired: the ten rows below.
4. **The declared behaviour change on an unreachable path.** `handleChangePassword` reads `authUser, _ := UserFromCtx(...)` and discards the ok, so a request reaching it with no principal would today call `GetUser` with a zero UUID and answer 500. Behind the wrap it answers 401. The path is unreachable through the mux, where the route sits inside `auth`. Same shape as the search slice's declared change on `handleListJobs`.
5. **Why a parser guard was bought** - the R6 evaluation: `main` is not callable from a test and reaches the literal only after opening the pool, so no behavioural test in any lane this package has can observe it.
6. **Lane honesty** - the two `internal/api` integration tests do not run in CI. That gap is structural and pre-existing, tracked by `docs/backlog/idea-2026-08-23-integration-only-guards-ci-never-runs.md`, and is why tests 1 to 5 were pushed into the default lane wherever the property allowed.

- [ ] **Step 5: Tell the conductor to close the item**

`/backlog close change-password-runs-bcrypt`. Do not hand-edit the item's `status`. The resolution note should carry the measured pair and, if it contradicts the item's unverified "roughly a quarter second", say so there.

---

## Mutation battery: the full table

Every row must be run, each with a green control before and after, each restored from a copy rather than with `git checkout --`, and each verified to have actually applied before a survivor is recorded.

| Mutation | Killed by | Expected symptom |
|---|---|---|
| Route left as bare `auth(...)` | Task 3's test | third request answers 400 |
| Limiter wrapped outside auth: `passwordLimit(auth(h))` | Task 3's test, via Task 5 | **FIRST** request answers 401 |
| Limiter constructed per request instead of at `Handler()` time | Task 3's test | third request answers 400 |
| Hard-coded count instead of `s.PasswordChangeLimitN` | Task 3's test | third request answers 400 |
| `s.PasswordChangeLimitN = d.searchLimitN` (crossed assignment) | Task 3's test | third request answers 400, since the fixture leaves `searchLimitN` zero |
| Password route added to `userLimit` instead of its own bucket | Task 7 | direction A's final assertion answers 429 |
| Enable guard `&&` relaxed to `\|\|` | Task 8, zero-count row | panic on `hits[0]` in `rateLimiter.allow` |
| main's literal set to `0` | Task 10's guard | "must be fed a plain identifier, not \*ast.BasicLit" |
| main's literal fed `searchN` | Task 10's guard | "whose chain never mentions RELAY_PASSWORD_CHANGE_RATE_LIMIT" / `otherEnv` |
| main's field omitted | Task 10's guard | "called with no passwordChangeLimitWin field" |
| A later `passwordChangeN = 0` inside an `if` | Task 10's guard | "assigned 2 times inside main"; executed tests stay green |
| Key on `clientIP(r)` | existing `TestUserRateLimit_TheSameUserFromTwoAddressesSharesOneBucket` and its mirror | already green; not re-run by this slice |
| `Retry-After` computed as zero | existing `TestUserRateLimit_RetryAfterNamesWhenTheWindowActuallyClears` | already green; **do not write a second band test** |

---

## Self-review against the spec

- **Section 1** (the shape of the slice): Tasks 4, 11 - one env var, two `Server` fields, two `httpServerDeps` fields, two assignments, one route wrap, one guard, no handler change.
- **Section 2** (refutations): all five are design inputs already resolved in the plan; the two bcrypt operations and the successful-loop attack are quoted in Task 16's row.
- **Section 3** (the key): unchanged code, so nothing to do; the self-scoped clause lands in Task 14.
- **Section 4** (separate bucket): Task 7 makes it executable; Task 11's comment carries the reason.
- **Section 5** (window and default): Task 11's `5:1m`; Task 16's `3:1m` recommendation and the no-off-value sentence.
- **Section 6** (step 3 ruled out): no task, by design. The wrap satisfies the criterion structurally; recorded in the PR, not in a comment.
- **Section 7** (construction site): Tasks 4 and 9, with R4's correction about `Handler()` being called more than once in tests.
- **Section 8** (testing): Tasks 2, 3, 6, 7, 8, 10, 15. Test numbering maps 1->Task 3, 2->Task 6, 3->Task 7, 4->Task 8, 5->Task 10, 6->Task 15, 7->Task 2 plus Task 15 Step 1.
- **Section 9** (prose): Tasks 13, 14, 16, 17.
- **Section 10** (the measurement): Task 1, first, with R7's correction to the instrument.
- **Section 12** (out of scope): enforced by Task 19 Step 3's file-set check.
- **Section 13** (proposed items): Task 18 files the progress note and the priority proposal. The second proposed item - a `services: postgres` lane for `internal/api` - is one more data point for an existing item, not a new one; the PR names it in its lane-honesty section.
- **Section 14** (open questions): all three were resolved by the human before this plan was written. The plan implements `5:1m`, buys the guard, and leaves `POST /v1/users/password-reset` out. If any of those is reopened, Tasks 11, 10 and 16 respectively are where it lands.
