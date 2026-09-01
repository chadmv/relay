# Per-task identity environment variables - Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Every task subprocess relay runs learns which job and task it is (`RELAY_JOB_ID`, `RELAY_TASK_ID`) and, when the operator has configured `RELAY_PUBLIC_URL`, where a human can watch it (`RELAY_JOB_URL`, `RELAY_TASK_URL`) - with values a job spec author cannot forge.

**Architecture:** Two new `string` fields on `DispatchTask` (9 and 10), rendered server-side in `Dispatcher.sendTask` from the ids of the row `ClaimTaskForWorker` returned, using a base URL parsed once at boot by a new fail-closed `parsePublicURL` in `cmd/relay-server`. The agent appends four entries to the subprocess environment **after** both existing merge loops, so `os/exec`'s documented last-occurrence-wins dedup makes the coordinator's values beat the job spec's and the workspace provider's. No SQL, no migration, no frontend, no new agent configuration.

**Tech Stack:** Go 1.26, protobuf via `buf generate`, testify, testcontainers-go (integration lane), Postgres 16. **One `make generate` step, in Task 1** - see the CRLF hazard section, which applies here even though no `.sql` file is touched.

**Spec:** `docs/superpowers/specs/2026-09-01-per-task-identity-env-vars.md` (signed off by the user including the fail-closed `RELAY_PUBLIC_URL` policy in its section 5.3)
**Backlog item this closes:** `docs/backlog/feature-2026-08-31-per-task-identity-env-vars.md`
**In-tree exemplar this slice copies for its hardest test:** `internal/agent/runner_crlf_test.go`'s `TestCRLFHelperProcess` / `crlfHelperCmd`

---

## Slice independence declaration

**This is a single-slice, single-PR, single-session plan. It has no stages and must NOT be handed to `/backlog phases`.**

**There is no frontend work in this slice at all.** The two routes the URLs point at, `/jobs/:id` and `/jobs/:id/tasks/:taskId`, already exist in `web/src/app/router.tsx` and are not touched. Nothing under `web/` renders, fetches or asserts anything about these variables, and `web/dist` must not be rebuilt or committed. **Do not dispatch `relay-frontend-engineer` for any part of this plan.** Phase 3 is a single backend lane.

**The tasks are sequential and the ordering is load-bearing in two places:**

- **Task 1 (proto + `make generate`) gates Tasks 4 and 5.** `task.JobUrl` and `task.TaskUrl` do not exist as Go symbols until the generated `relay.pb.go` is regenerated, so neither the agent-side test nor the dispatcher-side test can even compile before it.
- **Task 2 (`parsePublicURL`) gates Task 4.** Task 4 wires `main.go` to pass the *parsed* value into `NewDispatcher`. It must never pass a literal `""` "for now": a placeholder there would recreate exactly the silent-absence failure spec 7.1 chose a constructor parameter to eliminate, and nothing would catch it if the follow-up were forgotten.

The only genuine fork is that **Task 5 (agent) depends on nothing but Task 1**, so it could in principle run beside Tasks 2-4. Do not do that here: the two lanes share one git index in this worktree (see `feedback_concurrent_agents_share_one_git_index`), the whole plan is one PR, and running it serially costs a few minutes.

---

## What I refuted in the spec

The spec is sound in its design and its threat model, and I re-derived every claim it makes about the tree. **Five findings**, three of which change what the engineer must do.

### F1. `make generate` DOES rewrite the sqlc-generated store on this slice, and the spec says it does not

Spec 7.4 says: *"No `.sql` file is touched by this slice, so the generated-store half of that hazard does not apply."* **False.** The `generate` target in `Makefile` is:

```make
generate:
	sqlc generate
	buf generate
```

`sqlc generate` runs unconditionally and rewrites every file under `internal/store/` with LF endings on this CRLF working copy, whether or not any `.sql` input changed. That is precisely the 2026-08-28 incident CLAUDE.md records ("identical file lists at two stages, which reads as 'nothing to revert' - and 13 generated files were modified"). The post-generate check sequence in Task 1 therefore covers `internal/store/` as well as `internal/proto/`, and `git status --porcelain` - not `git diff` - is the authority on which files were touched.

### F2. The spec's "13 call sites in test files" is a mis-attribution, and one of the 13 is production code

Spec 7.1 says: *"13 call sites in `internal/scheduler/*_test.go` and `internal/worker/handler_test.go` gain a `""` argument."* The total 13 is right; the attribution is not. Enumerated with `rg 'NewDispatcher\('` over the whole tree (documents under `docs/superpowers/plans/` excluded - they are records of a moment and must not be edited):

**Production, 1 site:**

| File | Enclosing symbol |
|---|---|
| `cmd/relay-server/main.go` | `main`, the `dispatcher :=` assignment above `NewNotifyListener` |

**Tests, 12 sites, and every one of them is behind `//go:build integration`:**

| File | Enclosing test |
|---|---|
| `internal/scheduler/dispatch_test.go` | `TestDispatcher_DispatchesEligibleTask` |
| `internal/scheduler/dispatch_test.go` | `TestDispatcher_UsesAggregateCountQuery` |
| `internal/scheduler/dispatch_test.go` | `TestDispatcher_PrefersWarmWorker` |
| `internal/scheduler/dispatch_test.go` | `TestDispatcher_ColdFallback_NoWarmWorker` |
| `internal/scheduler/dispatch_test.go` | `TestDispatcher_PassesSourceToAgent` |
| `internal/scheduler/dispatch_test.go` | `TestDispatcher_BadCommandsJSON_FailsTaskNoRequeue` |
| `internal/scheduler/dispatch_test.go` | `TestDispatcher_FailClaimedTask_PublishesJobEventOnTerminal` |
| `internal/scheduler/dispatch_test.go` | `TestDispatcher_BadSourceJSON_FailsTaskNoLeak` |
| `internal/scheduler/dispatch_test.go` | `TestDispatcher_SendFailureRequeuesWithRealFenceValues` |
| `internal/scheduler/watchdog_integration_test.go` | `TestDispatcher_ClaimStampsAssignedAt` |
| `internal/worker/handler_test.go` | `TestRegisterAndDispatch_SourceTaskHeldOnProviderlessWorker` |
| `internal/worker/handler_test.go` | `TestRegisterAndDispatch_CapableWorkerReDispatchesHeldSourceTask` |

**The consequence the spec missed is a lane, not a number.** All three test files carry `//go:build integration`, so **`make test` never compiles any of them**. After Task 4 changes the signature, a tree with twelve un-updated call sites is fully green under `make test` and does not build at all under the integration tag. `make vet-integration` (`go vet -tags integration ./...`, which needs no Docker) is the gate that sees it, and Task 4 makes it a required step.

### F3. The "render from `task` instead of `claimed`" mutation is not coverable, and it is inert

Spec 9.5 lists it as conditional: *"only if the test seeds a row whose pre-claim and post-claim identity can differ; if it cannot, say so rather than claiming the mutation is covered"*. Resolved by reading the statement:

```sql
-- name: ClaimTaskForWorker :one
UPDATE tasks
SET status = 'dispatched', worker_id = ..., assigned_at = ..., assignment_epoch = assignment_epoch + 1
WHERE id = sqlc.arg(id) AND status = 'pending'
RETURNING *;
```

It selects on `id`, writes four columns, and returns the same row. `tasks.id` and `tasks.job_id` are not among the four and are never rewritten by anything. `GetEligibleTasks` returns `store.Task` and `ClaimTaskForWorker` returns `store.Task`, so `task.ID == claimed.ID` and `task.JobID == claimed.JobID` hold for every input that can exist.

**So this mutation is behaviourally inert: no test can kill it, and the battery must list it as SURVIVES BY CONSTRUCTION rather than as covered.** Sourcing both ids from `claimed` is still the right code - it matches the discipline the `RequeueTask` call site in the same function states for its two fences - but it is a consistency convention here, not a behaviour, and calling it "pinned by test 11" would be a false claim about a complement. The comment written in Task 4 says what is true (one row, so they cannot drift) and does not cite a test that cannot fail.

### F4. Two of the spec's mutation rows collapse two different mutations, one of which nothing kills

Spec 9.5's *"Make `parsePublicURL` return a warning instead of an error - killed by acceptance criterion 8"* conflates:

- **Changing `parsePublicURL` to return `("", nil)` for an invalid value.** Killed, by the `require.Error` legs of the Task 2 table test.
- **Changing `main.go`'s `log.Fatalf` to `log.Printf`.** Killed by **nothing**, and spec limitation 5 already concedes this ("the `log.Fatalf` call itself is untested, as with every other `main`-resident Fatalf in this binary"). The battery lists it as an accepted, disclosed gap rather than hiding it inside the row above.

### F5. The spec's helper-process idiom is workable, and the tree already contains a working instance of it that the spec does not cite

Verified against `internal/agent/`. `Runner.Run` calls `exec.CommandContext(ctx, argv[0], argv[1:]...)`, `setupProcTree` only sets `SysProcAttr`/`cmd.Cancel` (Unix) or a Job Object (Windows) and never touches `cmd.Env`, and `cmd.Env = env` is assigned after `setupProcTree` returns, so nothing interferes. `internal/agent` has no `TestMain`. Stdout already flows through `chunkWriter` into `sendCh`, which `collectStdoutLogs` and `drainByStream` already drain.

**And `runner_crlf_test.go` already does exactly this**, which the spec's section 9.1 does not mention: `crlfHelperCmd` returns `[]string{os.Args[0], "-test.run=^TestCRLFHelperProcess$"}` plus a sentinel carried in `DispatchTask.Env`, and `TestCRLFHelperProcess` ends in `os.Exit(0)`. Task 5 copies that shape rather than inventing one. **Three refinements to the spec's sketch, all learned from the exemplar or from reading `Runner.Run`:**

1. **`os.Exit(0)` is mandatory, and the spec omits it.** Without it the child's `testing` framework appends `PASS` to the very stdout the parent parses.
2. **The sentinel is `RELAY_ENV_HELPER`, not `GO_WANT_HELPER_PROCESS`.** The spec avoided a `RELAY_*` name to dodge a collision with the subject; that collision cannot occur here because the helper reports a **fixed list of four names** through `os.LookupEnv` rather than dumping the whole environment, and matching `RELAY_CRLF_HELPER` keeps one convention in the package.
3. **Drain after `Run` returns, do not race a timeout.** `Runner.Run` blocks until the subprocess is done and every message is already in the buffered `sendCh`, so a non-blocking drain (the `default: break drain` shape `drainByStream` uses) is deterministic where `collectMessages(sendCh, 500*time.Millisecond)` would be a race against process spawn under `-race`.

### Checked, not refuted - recorded so nobody re-derives it

- **Field numbers 9 and 10 are free** on `DispatchTask` (`task_id=1, job_id=2, reserved 3, env=4, timeout_seconds=5, epoch=6, source=7, commands=8`).
- **`buf.gen.yaml` emits Go only** (`protoc-gen-go` and `protoc-gen-go-grpc` into `internal/proto`). There is no Python or TypeScript proto artifact to keep in step; the Python SDK is REST-only.
- **No plumbing is needed in `internal/agent/agent.go`.** `handleDispatch` passes the whole `*relayv1.DispatchTask` to `runner.Run`, so two new fields are visible inside `Run` for free.
- **`RELAY_URL` is genuinely taken**, and by more than the spec says: README documents it as the CLI's `server_url` override **and** as an override the MCP server honours. Rejecting it for the base URL is right.
- **`internal/scheduler` already has untagged `package scheduler` test files** (`watchdog_test.go`, `select_worker_test.go`, `dispatch_fence_test.go`, `backoff_test.go`), so a new internal-package test for the unexported `jobURL`/`taskURL` is the house pattern, not a new one.
- **No symbol collisions.** `jobURL`, `taskURL`, `parsePublicURL`, `publicURLLine`, `publicBaseURL` and the literal `RELAY_PUBLIC_URL` do not appear anywhere in the tree's Go source.
- **`api.ParseCORSOrigins` is the right precedent** and has the shape the spec claims: `url.Parse`, an `http`/`https` allow-list, a non-empty-host check, an `error` return, `log.Fatalf` at the call site.
- **`fakeHandle` in `runner_test.go` really has no seam** - `Env()` returns a hard-coded `map[string]string{"P4CLIENT": "fake"}` with no field behind it - so Task 5 declares its own handle type instead of editing a fixture five other tests depend on.

---

## Critical files

**Modified (production):**

| File | What changes |
|---|---|
| `proto/relayv1/relay.proto` | Two fields on `message DispatchTask`: `string job_url = 9;`, `string task_url = 10;` |
| `internal/proto/relayv1/relay.pb.go` | **Generated. Never hand-edited.** Produced by Task 1's `make generate`. |
| `internal/scheduler/dispatch.go` | `Dispatcher` gains a `publicBaseURL string` field; `NewDispatcher` gains a fourth parameter; `sendTask` binds `jobIDStr`/`taskIDStr` and sets `JobUrl`/`TaskUrl` |
| `internal/scheduler/publicurl.go` | **New.** `jobURL`, `taskURL` |
| `cmd/relay-server/publicurl_config.go` | **New.** `parsePublicURL`, `publicURLLine` |
| `cmd/relay-server/main.go` | Parse `RELAY_PUBLIC_URL`, `log.Fatalf` on error, `log.Print(publicURLLine(...))`, pass the value to `NewDispatcher` - all immediately above the `dispatcher :=` assignment |
| `internal/agent/runner.go` | Four guarded appends after the `extraEnv` loop in `Runner.Run`, plus the merge comment |
| `README.md` | One row in the relay-server configuration table; one new `### Task subprocess environment` subsection under `## relay-agent` |

**Modified (tests):**

| File | What changes |
|---|---|
| `internal/proto/relayv1/proto_test.go` | One new round-trip test |
| `internal/scheduler/dispatch_test.go` | Two new tests; nine existing `NewDispatcher` calls gain `""` |
| `internal/scheduler/watchdog_integration_test.go` | One `NewDispatcher` call gains `""` |
| `internal/worker/handler_test.go` | Two `NewDispatcher` calls gain `""` |

**Created (tests):**

| File | Contents |
|---|---|
| `cmd/relay-server/publicurl_config_test.go` | Table-driven `parsePublicURL` cases, the redaction case, `publicURLLine` |
| `internal/scheduler/publicurl_test.go` | `jobURL`/`taskURL` table (untagged, `package scheduler`) |
| `internal/agent/runner_identity_env_test.go` | The helper process and six child-observed tests (untagged, `package agent`) |

**Not touched, deliberately:** anything under `web/` (**no frontend slice, no `web/dist` rebuild**), anything under `python/`, anything under `internal/store/` (**revert every LF-only change `sqlc generate` makes** - see Task 1), `internal/agent/runner_test.go` (`fakeHandle` stays as it is), and every historical plan under `docs/superpowers/plans/` that contains the old three-argument `NewDispatcher` call.

**Read before starting:** `CLAUDE.md`'s Invariants, its Comments policy (a comment states a hazard the code cannot show and may cite the test that pins it; **no counts, no dates, no measurement provenance** - those go in the commit message), and its CRLF/encoding section. Then read `internal/agent/runner_crlf_test.go`'s `TestCRLFHelperProcess` and `crlfHelperCmd`, which Task 5 mirrors.

---

## The `make generate` CRLF hazard, and the exact check sequence

Run this in Task 1, after editing the `.proto` and before staging anything. It is not optional and `git diff` alone cannot answer the question - `core.autocrlf=true` normalizes LF churn out of `git diff` while `git status` still lists the file as modified.

```bash
make generate

# 1. WHICH files were touched. This, not `git diff`, is the authority.
git status --porcelain

# 2. Which of them have a real CONTENT change.
git diff --ignore-all-space --stat

# 3. Revert every file that appears in (1) with no content change in (2).
#    sqlc generate runs unconditionally in `make generate` and rewrites
#    internal/store/*.sql.go and models.go with LF even though this slice
#    touches no .sql file.
git checkout -- internal/store/

# 4. Line endings on everything that legitimately changed.
git ls-files --eol proto/relayv1/relay.proto internal/proto/relayv1/relay.pb.go
#    Every line must read `i/lf`.

# 5. Proportionality. Only relay.pb.go should carry content, and its diff is
#    LARGER than two fields because the serialized file descriptor is a byte
#    array that shifts wholesale. relay_grpc.pb.go should have no content change.
git diff --ignore-all-space --stat -- internal/proto/
```

`buf generate` alone produces the same proto output and skips step 3 entirely; `make generate` is the documented command, so use it and do the revert.

---

## The four variable names, fixed for the whole plan

Spelled as **string literals** in every test, never as a shared constant, so no test can agree with a moved implementation by construction.

| Name | Source in `Runner.Run` | Appended when |
|---|---|---|
| `RELAY_TASK_ID` | `r.taskID` | `r.taskID != ""` |
| `RELAY_JOB_ID` | `task.JobId` | `task.JobId != ""` |
| `RELAY_JOB_URL` | `task.JobUrl` | `task.JobUrl != ""` |
| `RELAY_TASK_URL` | `task.TaskUrl` | `task.TaskUrl != ""` |

> **Every code block below is a SKETCH.** It was written against the tree but has not been compiled. Verify each test compiles and actually goes RED for the stated reason before writing the implementation; if a helper name collides with something already in the package, rename yours rather than editing the existing file.

---

## Task 1: Two rendered-URL fields on `DispatchTask`

**Files:**
- Modify: `proto/relayv1/relay.proto` (`message DispatchTask`)
- Modify: `internal/proto/relayv1/relay.pb.go` (**generated only, via `make generate`**)
- Test: `internal/proto/relayv1/proto_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/proto/relayv1/proto_test.go`:

```go
// TestDispatchTaskCarriesRenderedURLs pins that job_url and task_url are two
// DISTINCT fields on the wire, and that a coordinator with no public URL
// configured pays nothing for them. Setting one must not populate the other -
// which is what a copy-pasted field number would do, and protoc is the only
// thing that would catch that.
func TestDispatchTaskCarriesRenderedURLs(t *testing.T) {
	task := &relayv1.DispatchTask{
		TaskId:  "t1",
		JobId:   "j1",
		JobUrl:  "https://relay.example.com/jobs/j1",
		TaskUrl: "https://relay.example.com/jobs/j1/tasks/t1",
	}
	b, err := proto.Marshal(task)
	require.NoError(t, err)
	var got relayv1.DispatchTask
	require.NoError(t, proto.Unmarshal(b, &got))
	require.Equal(t, "https://relay.example.com/jobs/j1", got.JobUrl)
	require.Equal(t, "https://relay.example.com/jobs/j1/tasks/t1", got.TaskUrl)

	// Proto3 does not serialize an empty scalar, so "not configured" costs
	// nothing on the wire and needs no version negotiation in either direction:
	// an old agent ignores the fields, and a new agent reads them as empty.
	bare, err := proto.Marshal(&relayv1.DispatchTask{TaskId: "t1", JobId: "j1"})
	require.NoError(t, err)
	var gotBare relayv1.DispatchTask
	require.NoError(t, proto.Unmarshal(bare, &gotBare))
	require.Empty(t, gotBare.JobUrl)
	require.Empty(t, gotBare.TaskUrl)
	require.Less(t, len(bare), len(b))
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run:

```bash
go test ./internal/proto/relayv1/ -run TestDispatchTaskCarriesRenderedURLs -v
```

Expected: **a compile failure, not an assertion failure.**

```
./proto_test.go:NN:3: unknown field JobUrl in struct literal of type relayv1.DispatchTask
./proto_test.go:NN:3: unknown field TaskUrl in struct literal of type relayv1.DispatchTask
```

This is the honest RED for a generated-symbol task and the spec's section 9 says so. The behavioural half of the test (distinctness, and the shorter bare encoding) only becomes meaningful once the fields exist, which is why it is written now rather than after.

- [ ] **Step 3: Edit the proto**

In `proto/relayv1/relay.proto`, `message DispatchTask`:

```proto
message DispatchTask {
  string              task_id         = 1;
  string              job_id          = 2;
  reserved 3;
  reserved "command";
  map<string, string> env             = 4;
  int32               timeout_seconds = 5;
  int64               epoch           = 6;
  SourceSpec          source          = 7;
  repeated CommandLine commands       = 8;
  // Rendered by the coordinator, never by the agent: the frontend's route shape
  // is not an independently-deployed, long-lived agent's to know, and a fleet
  // the server cannot force to upgrade would keep emitting dead links after a
  // route change. Empty when the server has no RELAY_PUBLIC_URL.
  string              job_url         = 9;
  string              task_url        = 10;
}
```

- [ ] **Step 4: Regenerate, then run the CRLF check sequence**

Run `make generate` and then every step of the "`make generate` CRLF hazard" section above. Do not proceed until `git status --porcelain` lists only `proto/relayv1/relay.proto`, `internal/proto/relayv1/relay.pb.go` and the test file, and `git ls-files --eol` reads `i/lf` for both non-test paths.

- [ ] **Step 5: Run the test to verify it passes**

Run:

```bash
go test ./internal/proto/relayv1/ -run TestDispatchTaskCarriesRenderedURLs -v
go test ./... -timeout 120s
```

Expected: PASS, and the full default lane stays green.

- [ ] **Step 6: Commit**

```bash
git add proto/relayv1/relay.proto internal/proto/relayv1/relay.pb.go internal/proto/relayv1/proto_test.go
git commit -m "feat(proto): DispatchTask carries a coordinator-rendered job_url and task_url

Field numbers 9 and 10 on DispatchTask. The server renders both; the agent
never builds a URL. Agents are independently deployed and long-lived, so
putting /jobs/%s/tasks/%s in the agent binary would version the SPA's routing
table against a fleet the server cannot force to upgrade - the day the route
moves, every un-upgraded agent emits dead links silently.

Proto3 omits empty scalars, so a server with no public URL configured sends
nothing extra and both skew directions degrade to 'the URL variables are
absent', which is already the not-configured state the design handles."
```

---

## Task 2: `parsePublicURL` and `publicURLLine`

**Files:**
- Create: `cmd/relay-server/publicurl_config.go`
- Create: `cmd/relay-server/publicurl_config_test.go`

Lane: **default** (`make test`). Pure functions, no Docker, no network.

- [ ] **Step 1: Write the failing test**

Create `cmd/relay-server/publicurl_config_test.go`:

```go
package main

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestParsePublicURL_AcceptsAndNormalizes covers every accepted shape and the
// normalization contract jobURL and taskURL depend on: the returned base NEVER
// ends in a slash, which is why those two joiners can concatenate with no
// separator logic at all.
func TestParsePublicURL_AcceptsAndNormalizes(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want string
	}{
		{"unset disables the feature", "", ""},
		{"whitespace-only is the same as unset", "   ", ""},
		{"bare origin", "https://relay.example.com", "https://relay.example.com"},
		{"one trailing slash", "https://relay.example.com/", "https://relay.example.com"},
		{"several trailing slashes", "https://relay.example.com///", "https://relay.example.com"},
		{"scheme is lower-cased, host case is left alone", "HTTPS://Relay.Example.com", "https://Relay.Example.com"},
		{"http and an explicit port", "http://10.0.0.5:8080", "http://10.0.0.5:8080"},
		{"path prefix, trailing slash trimmed", "https://ops.example.com/relay/", "https://ops.example.com/relay"},
		{"surrounding whitespace is trimmed", "  https://relay.example.com  ", "https://relay.example.com"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parsePublicURL("RELAY_PUBLIC_URL", tc.raw)
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
			require.False(t, strings.HasSuffix(got, "/"),
				"jobURL and taskURL concatenate with no separator logic; a trailing slash here "+
					"produces a double slash in every link relay publishes")
		})
	}
}

// TestParsePublicURL_Rejects is the fail-closed half. Each row is a value an
// operator could plausibly type, and each is refused at boot rather than
// warned about and disabled - a warn-and-disable typo is indistinguishable
// from never having set the variable at all.
func TestParsePublicURL_Rejects(t *testing.T) {
	cases := []struct {
		name string
		raw  string
	}{
		{"userinfo is a phishing shape in a value relay publishes", "https://relay.example.com@evil.example/"},
		{"non-http scheme", "ftp://relay.example.com"},
		{"no scheme at all leaves the host empty", "relay.example.com"},
		{"query string cannot have a path appended", "https://relay.example.com/?x=1"},
		{"fragment cannot have a path appended", "https://relay.example.com/#frag"},
		{"embedded space", "https://relay example.com"},
		{"embedded tab", "https://relay.example.com\tx"},
		{"embedded newline", "https://relay.example.com\nX"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parsePublicURL("RELAY_PUBLIC_URL", tc.raw)
			require.Error(t, err)
			require.Empty(t, got, "a rejected value must not also return a usable base")
			require.Contains(t, err.Error(), "RELAY_PUBLIC_URL",
				"the message must name the variable, or an operator cannot tell which setting "+
					"stopped the boot")
		})
	}
}

// TestParsePublicURL_RejectionDoesNotLeakAPassword is the only test that pins
// the redaction rule. The message goes to a server log an operator reads and
// ships; the value it is refusing may carry a credential.
func TestParsePublicURL_RejectionDoesNotLeakAPassword(t *testing.T) {
	_, err := parsePublicURL("RELAY_PUBLIC_URL", "https://user:hunter2@relay.example.com")
	require.Error(t, err)
	require.NotContains(t, err.Error(), "hunter2")
}

// TestPublicURLLine_SaysWhichVariablesAreInjected asserts on the variable NAMES,
// not on the sentence: a rewording must stay green, a variable silently dropping
// out of the feature must not.
func TestPublicURLLine_SaysWhichVariablesAreInjected(t *testing.T) {
	off := publicURLLine("")
	require.Contains(t, off, "RELAY_PUBLIC_URL")
	require.Contains(t, off, "RELAY_JOB_URL")
	require.Contains(t, off, "RELAY_TASK_URL")
	require.Contains(t, off, "RELAY_JOB_ID")
	require.Contains(t, off, "RELAY_TASK_ID")

	on := publicURLLine("https://ops.example.com/relay")
	require.Contains(t, on, "https://ops.example.com/relay")
	require.Contains(t, on, "https://ops.example.com/relay/jobs/<job-id>")
	require.Contains(t, on, "https://ops.example.com/relay/jobs/<job-id>/tasks/<task-id>")
}
```

Note on the tab and newline rows: they are written as Go escapes (`\t`, `\n`) deliberately. A raw control byte in a source literal is unverifiable by eye and survives every check this repo runs.

- [ ] **Step 2: Run the test to verify it fails**

Run:

```bash
go test ./cmd/relay-server/ -run 'TestParsePublicURL|TestPublicURLLine' -v
```

Expected: **a compile failure** - `undefined: parsePublicURL`, `undefined: publicURLLine`. The subject does not exist at HEAD; spec section 9 states this openly, and the table above is what makes the task more than a naming test once it compiles.

- [ ] **Step 3: Write the implementation**

Create `cmd/relay-server/publicurl_config.go`:

```go
package main

import (
	"fmt"
	"net/url"
	"strings"
)

// parsePublicURL resolves RELAY_PUBLIC_URL into the browser-facing base the
// coordinator renders task links from, or "" when the feature is off.
//
// FAIL-CLOSED, unlike parseConnLimit / parseAutoEnrollCeiling / parseWatchdogDuration,
// which warn and fall back. Three reasons, and the first is the one that decides
// it: a warn-and-disable typo produces exactly "the URL variables are absent",
// which is also what an operator who never set the variable sees, so the
// degraded mode is indistinguishable from the unconfigured mode. There is also
// no defensible default origin to fall back to, and two of the rejections below
// are security rejections. The error-returning shape is api.ParseCORSOrigins's.
//
// The cost is real: a deployment whose value is edited badly will not come back
// up on its next restart. publicURLLine's unconditional startup line is the
// mitigation, and it is also the only defence against the failure no validator
// can catch - a value that parses perfectly and names the wrong host.
//
// Every rejection that has a structured URL in hand renders it through
// (*url.URL).Redacted(), so a base carrying a password cannot reach the log.
func parsePublicURL(name, raw string) (string, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", nil
	}
	// Ahead of url.Parse on purpose: url.Parse rejects some control bytes and
	// accepts a space, so leaving this to it would make the refusal depend on
	// which byte was typed. A shell step interpolating this value unquoted is
	// the realistic footgun.
	for i := 0; i < len(s); i++ {
		if s[i] < 0x21 || s[i] == 0x7f {
			return "", fmt.Errorf("%s must not contain whitespace or control characters", name)
		}
	}
	u, err := url.Parse(s)
	if err != nil {
		// The one branch that echoes the raw value: there is no structured URL
		// to redact yet.
		return "", fmt.Errorf("%s=%q is not a URL: %w", name, s, err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", fmt.Errorf("%s=%q must use the http or https scheme", name, u.Redacted())
	}
	if u.Host == "" {
		return "", fmt.Errorf("%s=%q is missing a host", name, u.Redacted())
	}
	if u.User != nil {
		// A base URL carrying userinfo is both a credential in an environment
		// variable and a phishing shape (https://relay.example.com@evil.example/)
		// that relay would render into every link it publishes.
		return "", fmt.Errorf("%s=%q must not carry userinfo", name, u.Redacted())
	}
	if u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return "", fmt.Errorf("%s=%q must not carry a query or fragment; relay appends a path to it",
			name, u.Redacted())
	}
	// EscapedPath rather than Path so an operator's percent-encoding survives.
	// Assembled explicitly rather than through u.String(): the two agree only
	// because every other component has been rejected above, and explicit
	// assembly cannot resurrect a component if a later edit stops rejecting one.
	return u.Scheme + "://" + u.Host + strings.TrimRight(u.EscapedPath(), "/"), nil
}

// publicURLLine renders the unconditional startup line, in the shape of
// grpcBoundsLine, autoEnrollCeilingLine and watchdogBoundsLine. A fail-closed
// parser plus a silent success is half a control: nothing else tells an operator
// that the value relay believes is the one they meant.
func publicURLLine(base string) string {
	if base == "" {
		return "public URL: not configured (RELAY_PUBLIC_URL is unset), so RELAY_JOB_URL and " +
			"RELAY_TASK_URL are not injected into task subprocesses. RELAY_JOB_ID and RELAY_TASK_ID " +
			"still are - they need no configuration."
	}
	return fmt.Sprintf(
		"public URL: %s - task subprocesses receive RELAY_JOB_URL=%s/jobs/<job-id>, "+
			"RELAY_TASK_URL=%s/jobs/<job-id>/tasks/<task-id>, RELAY_JOB_ID and RELAY_TASK_ID.",
		base, base, base)
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run:

```bash
go test ./cmd/relay-server/ -run 'TestParsePublicURL|TestPublicURLLine' -v
```

Expected: PASS on every subtest.

- [ ] **Step 5: Commit**

```bash
git add cmd/relay-server/publicurl_config.go cmd/relay-server/publicurl_config_test.go
git commit -m "feat(server): parse RELAY_PUBLIC_URL fail-closed, and print it at startup

parsePublicURL joins the ParseCORSOrigins family rather than the numeric-bounds
family. A warn-and-disable typo would produce exactly 'the URL variables are
absent', which is also what an operator who never set the variable sees - so the
degraded mode would be indistinguishable from the unconfigured one, with no
signal anywhere. There is no defensible default origin to fall back to either,
and userinfo and non-http(s) schemes are security rejections.

Not yet called from main; the dispatcher signature it feeds lands next."
```

---

## Task 3: `jobURL` and `taskURL`

**Files:**
- Create: `internal/scheduler/publicurl.go`
- Create: `internal/scheduler/publicurl_test.go`

Lane: **default** (`make test`). `package scheduler`, untagged - the symbols are unexported and the package already has four untagged internal test files.

- [ ] **Step 1: Write the failing test**

Create `internal/scheduler/publicurl_test.go`:

```go
package scheduler

import "testing"

// TestJobAndTaskURL covers the joining rule and its single gate: ANY empty
// argument yields "", so the decision "this field goes on the wire empty" lives
// in one place rather than at the call site.
func TestJobAndTaskURL(t *testing.T) {
	const jobID = "11111111-2222-3333-4444-555555555555"
	const taskID = "66666666-7777-8888-9999-aaaaaaaaaaaa"

	t.Run("job URL from a bare base", func(t *testing.T) {
		got := jobURL("https://relay.example.com", jobID)
		want := "https://relay.example.com/jobs/" + jobID
		if got != want {
			t.Fatalf("jobURL = %q, want %q", got, want)
		}
	})

	t.Run("task URL from a bare base", func(t *testing.T) {
		got := taskURL("https://relay.example.com", jobID, taskID)
		want := "https://relay.example.com/jobs/" + jobID + "/tasks/" + taskID
		if got != want {
			t.Fatalf("taskURL = %q, want %q", got, want)
		}
	})

	t.Run("a path prefix is preserved with exactly one slash", func(t *testing.T) {
		// parsePublicURL guarantees no trailing slash, so no separator logic
		// belongs here. This is the leg that reddens if somebody adds some.
		got := jobURL("https://ops.example.com/relay", jobID)
		want := "https://ops.example.com/relay/jobs/" + jobID
		if got != want {
			t.Fatalf("jobURL = %q, want %q", got, want)
		}
	})

	t.Run("an empty base yields no URL at all", func(t *testing.T) {
		if got := jobURL("", jobID); got != "" {
			t.Fatalf("jobURL with no base = %q, want %q", got, "")
		}
		if got := taskURL("", jobID, taskID); got != "" {
			t.Fatalf("taskURL with no base = %q, want %q", got, "")
		}
	})

	t.Run("an empty id yields no URL at all", func(t *testing.T) {
		// Never render https://relay.example.com/jobs/ - a link to a page that
		// does not exist is worse than no link, and the absent-or-non-empty rule
		// is what lets a consumer write exactly one check.
		if got := jobURL("https://relay.example.com", ""); got != "" {
			t.Fatalf("jobURL with no job id = %q, want %q", got, "")
		}
		if got := taskURL("https://relay.example.com", "", taskID); got != "" {
			t.Fatalf("taskURL with no job id = %q, want %q", got, "")
		}
		if got := taskURL("https://relay.example.com", jobID, ""); got != "" {
			t.Fatalf("taskURL with no task id = %q, want %q", got, "")
		}
	})
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run:

```bash
go test ./internal/scheduler/ -run TestJobAndTaskURL -v
```

Expected: **a compile failure** - `undefined: jobURL`, `undefined: taskURL`.

- [ ] **Step 3: Write the implementation**

Create `internal/scheduler/publicurl.go`:

```go
package scheduler

// jobURL and taskURL render the browser-facing links the coordinator puts on
// DispatchTask.
//
// AN EMPTY ARGUMENT YIELDS "". That is the single gate for "this field goes on
// the wire empty", so the emptiness decision lives here instead of at the call
// site, and a consumer of the resulting environment variable needs one check
// rather than a second one for "set but blank".
//
// Plain concatenation with no separator logic, because parsePublicURL
// guarantees base carries no trailing slash - that guarantee is why
// normalization happens at parse time and not here.
//
// THE IDS ARE NOT ESCAPED, on a stated premise: both are uuidStr output over
// pgtype.UUID values read off the claimed row, so they can contain only
// [0-9a-f-]. If task or job ids ever stop being UUIDs, the escaping question
// reopens here.
func jobURL(base, jobID string) string {
	if base == "" || jobID == "" {
		return ""
	}
	return base + "/jobs/" + jobID
}

func taskURL(base, jobID, taskID string) string {
	if base == "" || jobID == "" || taskID == "" {
		return ""
	}
	return base + "/jobs/" + jobID + "/tasks/" + taskID
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run:

```bash
go test ./internal/scheduler/ -run TestJobAndTaskURL -v
```

Expected: PASS on all five subtests.

- [ ] **Step 5: Commit**

```bash
git add internal/scheduler/publicurl.go internal/scheduler/publicurl_test.go
git commit -m "feat(scheduler): jobURL and taskURL, with one empty-argument gate

Any empty argument yields the empty string, so 'the URL field goes on the wire
empty' is decided in one place rather than at the call site. Plain
concatenation: parsePublicURL already guarantees the base has no trailing
slash, which is why normalization belongs at parse time and separator logic
does not belong here."
```

---

## Task 4: Render both URLs in `sendTask`, and wire the base URL through `main`

**Files:**
- Modify: `internal/scheduler/dispatch.go` (`Dispatcher` struct, `NewDispatcher`, `sendTask`)
- Modify: `cmd/relay-server/main.go` (immediately above the `dispatcher :=` assignment)
- Test: `internal/scheduler/dispatch_test.go` (two new tests; nine existing calls updated)
- Test: `internal/scheduler/watchdog_integration_test.go` (one call updated)
- Test: `internal/worker/handler_test.go` (two calls updated)

Lane: **integration** for the new assertions (`dispatch_test.go` is `//go:build integration` and already has `fakeSender`), and **`make vet-integration`** as the compile gate for all twelve updated call sites. Per CLAUDE.md's "where a CLI test goes" reasoning: the assertion's truth depends on what a real `ClaimTaskForWorker` returns from a real Postgres and on what the dispatcher actually puts on the wire, so it belongs in the lane that has both.

- [ ] **Step 1: Write the failing tests**

Append to `internal/scheduler/dispatch_test.go`:

```go
// TestDispatcher_RendersJobAndTaskURLsFromTheClaimedRow is the wire-level half
// of the feature. It asserts the URLs against THE IDS THIS TEST SEEDED, never
// against dt.JobId / dt.TaskId: sourcing both sides of the comparison from the
// same message makes the test agree with itself by construction and blind both
// to the two ids being swapped and to the URLs being built from the wrong row.
//
// The base carries a path prefix because that is the shape a reverse-proxied
// deployment uses and the one where an accidental separator shows up.
func TestDispatcher_RendersJobAndTaskURLsFromTheClaimedRow(t *testing.T) {
	ctx := context.Background()
	q := newTestStore(t)

	user, err := q.CreateUserWithPassword(ctx, store.CreateUserWithPasswordParams{
		Name: "u", Email: "urls@example.com", IsAdmin: false, PasswordHash: "x",
	})
	require.NoError(t, err)
	job, err := q.CreateJob(ctx, store.CreateJobParams{
		Name: "url-job", Priority: "normal", SubmittedBy: user.ID, Labels: []byte(`{}`),
		ScheduledJobID: pgtype.UUID{},
	})
	require.NoError(t, err)
	task, err := q.CreateTask(ctx, store.CreateTaskParams{
		JobID: job.ID, Name: "url-task", Commands: []byte(`[["echo","hello"]]`),
		Env: []byte(`{}`), Requires: []byte(`{}`), Retries: 0,
	})
	require.NoError(t, err)

	wRow, err := q.UpsertWorkerByHostname(ctx, store.UpsertWorkerByHostnameParams{
		Name: "url-worker", Hostname: "url-worker", CpuCores: 1, RamGb: 1, Os: "linux",
	})
	require.NoError(t, err)
	w, err := q.UpdateWorkerStatus(ctx, store.UpdateWorkerStatusParams{
		ID: wRow.ID, Status: "online", LastSeenAt: pgtype.Timestamptz{Time: time.Now(), Valid: true},
	})
	require.NoError(t, err)

	registry := worker.NewRegistry()
	sender := &fakeSender{}
	registry.Register(uuidStr(w.ID), sender)

	d := scheduler.NewDispatcher(q, registry, events.NewBroker(), "https://ops.example.com/relay")
	d.RunOnce(ctx)

	require.Len(t, sender.sent, 1)
	dt := sender.sent[0].GetDispatchTask()
	require.NotNil(t, dt)
	assert.Equal(t, "https://ops.example.com/relay/jobs/"+uuidStr(job.ID), dt.JobUrl)
	assert.Equal(t,
		"https://ops.example.com/relay/jobs/"+uuidStr(job.ID)+"/tasks/"+uuidStr(task.ID),
		dt.TaskUrl,
		"the task URL must nest the TASK id under the JOB id; the two are independently "+
			"generated UUIDs, so a transposed argument pair cannot produce this string")
}

// TestDispatcher_NoPublicURLSendsEmptyURLFieldsButStillSendsTheIds is the
// conjunction. The empty-URL half alone is green against a dispatcher that
// never learned to render anything at all.
func TestDispatcher_NoPublicURLSendsEmptyURLFieldsButStillSendsTheIds(t *testing.T) {
	ctx := context.Background()
	q := newTestStore(t)

	user, err := q.CreateUserWithPassword(ctx, store.CreateUserWithPasswordParams{
		Name: "u", Email: "nourls@example.com", IsAdmin: false, PasswordHash: "x",
	})
	require.NoError(t, err)
	job, err := q.CreateJob(ctx, store.CreateJobParams{
		Name: "no-url-job", Priority: "normal", SubmittedBy: user.ID, Labels: []byte(`{}`),
		ScheduledJobID: pgtype.UUID{},
	})
	require.NoError(t, err)
	task, err := q.CreateTask(ctx, store.CreateTaskParams{
		JobID: job.ID, Name: "no-url-task", Commands: []byte(`[["echo","hello"]]`),
		Env: []byte(`{}`), Requires: []byte(`{}`), Retries: 0,
	})
	require.NoError(t, err)

	wRow, err := q.UpsertWorkerByHostname(ctx, store.UpsertWorkerByHostnameParams{
		Name: "no-url-worker", Hostname: "no-url-worker", CpuCores: 1, RamGb: 1, Os: "linux",
	})
	require.NoError(t, err)
	w, err := q.UpdateWorkerStatus(ctx, store.UpdateWorkerStatusParams{
		ID: wRow.ID, Status: "online", LastSeenAt: pgtype.Timestamptz{Time: time.Now(), Valid: true},
	})
	require.NoError(t, err)

	registry := worker.NewRegistry()
	sender := &fakeSender{}
	registry.Register(uuidStr(w.ID), sender)

	d := scheduler.NewDispatcher(q, registry, events.NewBroker(), "")
	d.RunOnce(ctx)

	require.Len(t, sender.sent, 1)
	dt := sender.sent[0].GetDispatchTask()
	require.NotNil(t, dt)
	assert.Empty(t, dt.JobUrl)
	assert.Empty(t, dt.TaskUrl)
	assert.Equal(t, uuidStr(job.ID), dt.JobId,
		"only the URLs depend on RELAY_PUBLIC_URL; the ids must still reach the agent")
	assert.Equal(t, uuidStr(task.ID), dt.TaskId)
}
```

- [ ] **Step 2: Run the tests to verify they fail (compile stage)**

Run:

```bash
go vet -tags integration ./internal/scheduler/
```

Expected: **a compile failure** naming the arity.

```
too many arguments in call to scheduler.NewDispatcher
	have (*store.Queries, *worker.Registry, *events.Broker, string)
	want (*store.Queries, *worker.Registry, *events.Broker)
```

- [ ] **Step 3: Add the parameter and the field, but NOT the rendering**

This intermediate step exists so the RED is behavioural, not just a compile error. In `internal/scheduler/dispatch.go`:

```go
type Dispatcher struct {
	q             *store.Queries
	registry      *worker.Registry
	broker        *events.Broker
	publicBaseURL string        // "" disables the rendered URLs; see jobURL/taskURL
	trigger       chan struct{} // buffered 1, coalesced
}

// NewDispatcher returns a ready-to-use Dispatcher. publicBaseURL is the
// normalized RELAY_PUBLIC_URL, or "" when the operator has not set one.
//
// A CONSTRUCTOR PARAMETER, NOT A SETTABLE FIELD like agentHandler.TrailingLogWindow:
// a field that main forgets to set produces silently absent URLs,
// indistinguishable from an unconfigured server, with every test green. A
// parameter makes that a compile error instead of something a structural
// main.go-parsing test has to notice.
func NewDispatcher(q *store.Queries, r *worker.Registry, b *events.Broker, publicBaseURL string) *Dispatcher {
	return &Dispatcher{
		q:             q,
		registry:      r,
		broker:        b,
		publicBaseURL: publicBaseURL,
		trigger:       make(chan struct{}, 1),
	}
}
```

Then update every one of the twelve test call sites to pass `""`, and `cmd/relay-server/main.go` to pass the parsed value (Step 5 below writes that block; do it now, in this step, so `main` never passes a placeholder). Use the enumeration in finding F2 as the checklist.

- [ ] **Step 4: Run the tests to verify they fail (behavioural stage)**

Requires Docker. Run:

```bash
go test -tags integration -p 1 ./internal/scheduler/ -run 'TestDispatcher_RendersJobAndTaskURLs|TestDispatcher_NoPublicURL' -v -timeout 600s
```

Expected: `TestDispatcher_RendersJobAndTaskURLsFromTheClaimedRow` FAILS with an empty actual value; `TestDispatcher_NoPublicURLSendsEmptyURLFieldsButStillSendsTheIds` PASSES (it is the not-configured state, which is what an unrendered dispatch already looks like - that is why it is not the headline test).

```
--- FAIL: TestDispatcher_RendersJobAndTaskURLsFromTheClaimedRow
    Error: Not equal:
           expected: "https://ops.example.com/relay/jobs/<uuid>"
           actual  : ""
```

- [ ] **Step 5: Render the URLs, and wire `main`**

In `Dispatcher.sendTask`, replace the `dt := &relayv1.DispatchTask{...}` literal:

```go
	// Both ids come off `claimed` - the RETURNING row of the fenced
	// ClaimTaskForWorker - and the URLs are rendered from those same two locals,
	// so a link can never name a different row than the dispatch it travels on.
	jobIDStr := uuidStr(claimed.JobID)
	taskIDStr := uuidStr(claimed.ID)
	dt := &relayv1.DispatchTask{
		TaskId:         taskIDStr,
		JobId:          jobIDStr,
		JobUrl:         jobURL(d.publicBaseURL, jobIDStr),
		TaskUrl:        taskURL(d.publicBaseURL, jobIDStr, taskIDStr),
		Commands:       dtCommands,
		Env:            env,
		TimeoutSeconds: timeoutSecs,
		Epoch:          int64(claimed.AssignmentEpoch),
	}
```

In `cmd/relay-server/main.go`, immediately above the `dispatcher :=` assignment:

```go
	// Parsed HERE rather than beside ParseCORSOrigins further down, because
	// NewDispatcher consumes it and the value must not be shadowed between its
	// construction and its use (the reason resolveGRPCBounds keeps its parsing
	// out of main too). Fatal on error: see parsePublicURL.
	publicBaseURL, err := parsePublicURL("RELAY_PUBLIC_URL", os.Getenv("RELAY_PUBLIC_URL"))
	if err != nil {
		log.Fatalf("%v", err)
	}
	log.Print(publicURLLine(publicBaseURL))

	dispatcher := scheduler.NewDispatcher(q, registry, broker, publicBaseURL)
```

Note on `err`: `main` already declares `err` above (from `pgxpool.ParseConfig`), so this is an assignment to the existing `err` plus a new `publicBaseURL`, and `:=` is correct. If the compiler disagrees, declare `publicBaseURL` separately rather than shadowing.

- [ ] **Step 6: Run the tests to verify they pass**

Run, in order:

```bash
make vet-integration
go test ./... -timeout 120s
go test -tags integration -p 1 ./internal/scheduler/ -run TestDispatcher -v -timeout 900s
go test -tags integration -p 1 ./internal/worker/ -run TestRegisterAndDispatch -v -timeout 900s
```

Expected: `vet-integration` clean; the default lane green; every `TestDispatcher_*` green including the two new ones; both `TestRegisterAndDispatch_*` green.

- [ ] **Step 7: Commit**

```bash
git add internal/scheduler/dispatch.go cmd/relay-server/main.go internal/scheduler/dispatch_test.go internal/scheduler/watchdog_integration_test.go internal/worker/handler_test.go
git commit -m "feat(scheduler): dispatch carries the coordinator-rendered job and task URLs

NewDispatcher takes the normalized base URL as a fourth parameter rather than
exposing a settable field. A field main forgets to set produces silently absent
URLs, indistinguishable from an unconfigured server, with every test green; a
parameter makes that a compile error and needs no structural guard test.

Both URLs are rendered from the ids of the row ClaimTaskForWorker RETURNED, the
same discipline the requeue fences in this function already state.

Every one of the twelve updated call sites is behind //go:build integration, so
make test cannot see this signature change at all - make vet-integration is the
gate that can."
```

---

## Task 5: Inject the identity block into the task subprocess

**Files:**
- Modify: `internal/agent/runner.go` (`Runner.Run`, after the `extraEnv` loop; and the merge comment above `env := os.Environ()`)
- Create: `internal/agent/runner_identity_env_test.go`

Lane: **default** (`make test`). No Docker. The tests exec the test binary as a helper subprocess, which works on Windows and Linux alike.

**The constraint that shapes every test here:** each assertion reads the **child process's own environment**, never `cmd.Env`. `os/exec` deduplicates at `Start` time, so `cmd.Env` legitimately contains both the spec's entry and the coordinator's, and an assertion on it can be written to pass in either direction while proving nothing.

- [ ] **Step 1: Write the failing tests**

Create `internal/agent/runner_identity_env_test.go`:

```go
package agent

import (
	"context"
	"os"
	"strings"
	"testing"

	"relay/internal/agent/source"
	relayv1 "relay/internal/proto/relayv1"

	"github.com/stretchr/testify/require"
)

// identityVars is the exact set the coordinator injects. The helper reports each
// through os.LookupEnv, so ABSENT and PRESENT-BUT-EMPTY stay distinguishable -
// the distinction the absent-or-non-empty rule turns on, and one that
// `echo $VAR` cannot make on either platform.
var identityVars = []string{"RELAY_TASK_ID", "RELAY_JOB_ID", "RELAY_JOB_URL", "RELAY_TASK_URL"}

const identityLinePrefix = "relayenv "

// TestRunnerEnvHelperProcess IS NOT A TEST. It is the subprocess the tests in
// this file exec, in the shape of TestCRLFHelperProcess: the test binary
// re-execs itself so that what is observed is the CHILD's environment.
//
// os.Exit(0) IS NOT OPTIONAL: without it the testing framework appends "PASS" to
// the very stdout the parent parses.
func TestRunnerEnvHelperProcess(t *testing.T) {
	if os.Getenv("RELAY_ENV_HELPER") == "" {
		return // an ordinary test run; this process is not the helper
	}
	for _, k := range identityVars {
		if v, ok := os.LookupEnv(k); ok {
			_, _ = os.Stdout.WriteString(identityLinePrefix + k + "=" + v + "\n")
		}
	}
	os.Exit(0)
}

// identityHelperCmd returns the argv and the env that re-exec this test binary
// as the helper above. os.Args[0] under `go test` is the built test binary, an
// absolute path. The sentinel travels through DispatchTask.Env, so the parent's
// own environment is never mutated by it, and it is not one of identityVars so
// it can never be mistaken for the subject.
func identityHelperCmd() ([]string, map[string]string) {
	return []string{os.Args[0], "-test.run=^TestRunnerEnvHelperProcess$"},
		map[string]string{"RELAY_ENV_HELPER": "1"}
}

// runIdentityHelper dispatches task through a real Runner and returns the
// child's view: present names mapped to their values, absent names missing from
// the map entirely. ambient entries are exported into the AGENT's own process
// environment first, which is how the inheritance test drives its input.
func runIdentityHelper(
	t *testing.T,
	task *relayv1.DispatchTask,
	provider source.Provider,
	ambient map[string]string,
) map[string]string {
	t.Helper()
	// Hermetic: whatever the developer's shell exports must not decide the
	// result. t.Setenv registers the restore whether or not the name was
	// originally set, so the Unsetenv is safe to pair with it.
	for _, k := range identityVars {
		t.Setenv(k, "")
		os.Unsetenv(k)
	}
	for k, v := range ambient {
		t.Setenv(k, v)
	}

	sendCh := make(chan *relayv1.AgentMessage, 256)
	r, runCtx := newRunner(task.TaskId, task.Epoch, sendCh, context.Background(), 0)
	if provider != nil {
		r.SetProviderForTest(provider)
	}
	r.Run(runCtx, task) // blocks until the subprocess has exited

	// Run has returned, so every message is already in the buffer; a
	// non-blocking drain is deterministic where a timeout would race the spawn.
	var out strings.Builder
drain:
	for {
		select {
		case m := <-sendCh:
			if l := m.GetTaskLog(); l != nil && l.Stream == relayv1.LogStream_LOG_STREAM_STDOUT {
				out.Write(l.Content)
			}
		default:
			break drain
		}
	}

	got := map[string]string{}
	for _, line := range strings.Split(out.String(), "\n") {
		if !strings.HasPrefix(line, identityLinePrefix) {
			continue
		}
		k, v, ok := strings.Cut(strings.TrimPrefix(line, identityLinePrefix), "=")
		require.True(t, ok, "malformed helper line %q", line)
		got[k] = v
	}
	return got
}

// spoofingHandle is a source.Handle whose workspace environment tries to set the
// coordinator's own names. runner_test.go's fakeHandle returns a fixed one-key
// map with no seam behind it, and other tests depend on that.
type spoofingHandle struct{}

func (spoofingHandle) WorkingDir() string { return "" }
func (spoofingHandle) Env() map[string]string {
	return map[string]string{
		"RELAY_JOB_URL": "https://evil.example/",
		"RELAY_TASK_ID": "SPOOFED-BY-WORKSPACE",
	}
}
func (spoofingHandle) Finalize(ctx context.Context) error { return nil }
func (spoofingHandle) Inventory() source.InventoryEntry {
	return source.InventoryEntry{SourceType: "perforce", SourceKey: "//s/x"}
}

func TestRunner_InjectsTheDispatchedIdentityIntoTheChildEnvironment(t *testing.T) {
	argv, env := identityHelperCmd()
	got := runIdentityHelper(t, &relayv1.DispatchTask{
		TaskId:   "task-abc",
		JobId:    "job-xyz",
		JobUrl:   "https://relay.example.com/jobs/job-xyz",
		TaskUrl:  "https://relay.example.com/jobs/job-xyz/tasks/task-abc",
		Commands: []*relayv1.CommandLine{{Argv: argv}},
		Env:      env,
	}, nil, nil)

	// The whole map, not four Contains: the two URLs differ from each other, so
	// this also refuses a transposed JobUrl/TaskUrl pair - two same-typed
	// adjacent arguments that no compiler can tell apart.
	require.Equal(t, map[string]string{
		"RELAY_TASK_ID":  "task-abc",
		"RELAY_JOB_ID":   "job-xyz",
		"RELAY_JOB_URL":  "https://relay.example.com/jobs/job-xyz",
		"RELAY_TASK_URL": "https://relay.example.com/jobs/job-xyz/tasks/task-abc",
	}, got)
}

// TestRunner_CoordinatorIdentityBeatsSpecEnv is the headline security test. A
// job spec is authored by any authenticated user; a downstream notifier reading
// RELAY_JOB_URL posts a link other humans click.
func TestRunner_CoordinatorIdentityBeatsSpecEnv(t *testing.T) {
	argv, env := identityHelperCmd()
	for _, k := range identityVars {
		env[k] = "https://evil.example/SPOOFED"
	}
	got := runIdentityHelper(t, &relayv1.DispatchTask{
		TaskId:   "task-abc",
		JobId:    "job-xyz",
		JobUrl:   "https://relay.example.com/jobs/job-xyz",
		TaskUrl:  "https://relay.example.com/jobs/job-xyz/tasks/task-abc",
		Commands: []*relayv1.CommandLine{{Argv: argv}},
		Env:      env,
	}, nil, nil)

	require.Equal(t, map[string]string{
		"RELAY_TASK_ID":  "task-abc",
		"RELAY_JOB_ID":   "job-xyz",
		"RELAY_JOB_URL":  "https://relay.example.com/jobs/job-xyz",
		"RELAY_TASK_URL": "https://relay.example.com/jobs/job-xyz/tasks/task-abc",
	}, got, "a job spec's env must not decide what a notifier posts as a link")
}

// TestRunner_CoordinatorIdentityBeatsWorkspaceEnv is the second precedence leg
// and it is the one that discriminates. Without it, moving the identity block
// to sit BETWEEN the two merge loops passes everything else in this file.
func TestRunner_CoordinatorIdentityBeatsWorkspaceEnv(t *testing.T) {
	argv, env := identityHelperCmd()
	got := runIdentityHelper(t, &relayv1.DispatchTask{
		TaskId:   "task-abc",
		JobId:    "job-xyz",
		JobUrl:   "https://relay.example.com/jobs/job-xyz",
		TaskUrl:  "https://relay.example.com/jobs/job-xyz/tasks/task-abc",
		Commands: []*relayv1.CommandLine{{Argv: argv}},
		Env:      env,
		Source: &relayv1.SourceSpec{Provider: &relayv1.SourceSpec_Perforce{
			Perforce: &relayv1.PerforceSource{Stream: "//s/x"},
		}},
	}, &fakeProvider{handle: spoofingHandle{}}, nil)

	require.Equal(t, map[string]string{
		"RELAY_TASK_ID":  "task-abc",
		"RELAY_JOB_ID":   "job-xyz",
		"RELAY_JOB_URL":  "https://relay.example.com/jobs/job-xyz",
		"RELAY_TASK_URL": "https://relay.example.com/jobs/job-xyz/tasks/task-abc",
	}, got, "the workspace provider's env is merged after the spec's and must lose too")
}

// TestRunner_UnconfiguredPublicURLLeavesTheURLNamesAbsentAndTheIdsPresent is a
// CONJUNCTION on purpose. At HEAD all four names are absent, so the absence half
// alone is green against an unmodified tree and is not a criterion.
func TestRunner_UnconfiguredPublicURLLeavesTheURLNamesAbsentAndTheIdsPresent(t *testing.T) {
	argv, env := identityHelperCmd()
	got := runIdentityHelper(t, &relayv1.DispatchTask{
		TaskId:   "task-abc",
		JobId:    "job-xyz",
		Commands: []*relayv1.CommandLine{{Argv: argv}}, // JobUrl and TaskUrl deliberately unset
		Env:      env,
	}, nil, nil)

	require.Equal(t, map[string]string{
		"RELAY_TASK_ID": "task-abc",
		"RELAY_JOB_ID":  "job-xyz",
	}, got, "the URL names must be ABSENT, not present and empty, while both ids are still there")
}

// TestRunner_AnEmptyDispatchedIdProducesNoVariableAtAll pins the guard on the id
// half. relay's own dispatcher always populates both, so the discriminating
// input is a dispatch that does not - which is what a future peer on this wire,
// or a hand-built message, can produce.
func TestRunner_AnEmptyDispatchedIdProducesNoVariableAtAll(t *testing.T) {
	argv, env := identityHelperCmd()
	got := runIdentityHelper(t, &relayv1.DispatchTask{
		TaskId:   "task-abc",
		JobId:    "", // the subject
		Commands: []*relayv1.CommandLine{{Argv: argv}},
		Env:      env,
	}, nil, nil)

	require.Equal(t, map[string]string{"RELAY_TASK_ID": "task-abc"}, got,
		"relay must never set one of these names to the empty string: a consumer gets one "+
			"check, not a second one for 'set but blank'")
}

// TestRunner_AnAgentProcessEnvValueSurvivesWhenTheCoordinatorHasNone documents a
// known limitation AS A BEHAVIOUR. relay only appends; it does not strip. The
// trust boundary this feature defends is the job spec author, not the agent
// operator, who already chooses the binary and owns the machine. A later slice
// that decides to start stripping must redden HERE rather than changing this
// silently.
func TestRunner_AnAgentProcessEnvValueSurvivesWhenTheCoordinatorHasNone(t *testing.T) {
	argv, env := identityHelperCmd()
	got := runIdentityHelper(t, &relayv1.DispatchTask{
		TaskId:   "task-abc",
		JobId:    "job-xyz",
		Commands: []*relayv1.CommandLine{{Argv: argv}}, // no JobUrl
		Env:      env,
	}, nil, map[string]string{"RELAY_JOB_URL": "https://inherited.example/"})

	require.Equal(t, map[string]string{
		"RELAY_TASK_ID": "task-abc",
		"RELAY_JOB_ID":  "job-xyz",
		"RELAY_JOB_URL": "https://inherited.example/",
	}, got)
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run:

```bash
go test ./internal/agent/ -run 'TestRunner_(Injects|Coordinator|Unconfigured|AnEmpty|AnAgent)' -v -timeout 120s
```

Expected: **six FAIL, zero PASS.** At HEAD `Runner.Run` never writes any of these four names, so:

```
--- FAIL: TestRunner_InjectsTheDispatchedIdentityIntoTheChildEnvironment
    Error: Not equal:
           expected: map[string]string{"RELAY_JOB_ID":"job-xyz", ...}
           actual  : map[string]string{}
```

Each test fails for its own reason, and two are worth checking individually rather than trusting the count:

- `TestRunner_CoordinatorIdentityBeatsSpecEnv` fails with the **spoofed** values present, not with an empty map, because at HEAD the spec's `env` reaches the child unopposed. If it fails with an empty map instead, the helper is not being exec'd and the whole file is measuring nothing.
- `TestRunner_AnAgentProcessEnvValueSurvivesWhenTheCoordinatorHasNone` fails on the **ids**, not on the inherited URL: inheritance already works at HEAD. Its assertion is a conjunction on purpose, so that it keeps a job after the change instead of being green against every tree.

If instead you get `unknown field JobUrl`, Task 1 has not landed; stop and do it first.

- [ ] **Step 3: Write the implementation**

In `internal/agent/runner.go`, replace the merge block in `Runner.Run`:

```go
	// Merge env: current process env first, task env overrides, then workspace
	// env. THE COORDINATOR'S IDENTITY BLOCK IS APPENDED LAST AND MUST STAY LAST.
	// os/exec keeps the LAST occurrence of a duplicate key - a documented Cmd.Env
	// contract, case-insensitive on Windows and case-sensitive elsewhere - so
	// appending after both loops is the whole reason a job spec, authored by any
	// authenticated user, cannot choose the link a downstream notifier posts.
	// Moving this block above either loop is what
	// TestRunner_CoordinatorIdentityBeatsSpecEnv and
	// TestRunner_CoordinatorIdentityBeatsWorkspaceEnv exist to redden.
	//
	// Each name is appended only when its value is non-empty, so relay never sets
	// one of them to the empty string and a consumer needs one check rather than
	// a second for "set but blank".
	env := os.Environ()
	for k, v := range task.Env {
		env = append(env, k+"="+v)
	}
	for k, v := range extraEnv {
		env = append(env, k+"="+v)
	}
	if r.taskID != "" {
		env = append(env, "RELAY_TASK_ID="+r.taskID)
	}
	if task.JobId != "" {
		env = append(env, "RELAY_JOB_ID="+task.JobId)
	}
	if task.JobUrl != "" {
		env = append(env, "RELAY_JOB_URL="+task.JobUrl)
	}
	if task.TaskUrl != "" {
		env = append(env, "RELAY_TASK_URL="+task.TaskUrl)
	}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run:

```bash
go test ./internal/agent/ -run TestRunner -v -timeout 120s
go test ./... -timeout 120s
```

Expected: all six new tests PASS and every existing `internal/agent` test stays green - in particular `TestRunner_done`, `TestRunner_MultiStepAllSucceed` and the CRLF wiring tests, which run their own subprocesses through the same merge block.

- [ ] **Step 5: Commit**

```bash
git add internal/agent/runner.go internal/agent/runner_identity_env_test.go
git commit -m "feat(agent): inject RELAY_JOB_ID, RELAY_TASK_ID, RELAY_JOB_URL, RELAY_TASK_URL

Appended after both existing merge loops, so os/exec's documented
last-occurrence-wins dedup makes the coordinator's values beat a job spec's env
and the workspace provider's. Verified against the Cmd.Env field documentation
and dedupEnv in the toolchain go.mod pins: dedupEnv is called unconditionally
from (*Cmd).environ, after the nil-Env branch, so it applies to a
caller-supplied slice exactly as it does to the inherited default.

Every assertion reads the CHILD process's environment through a re-exec of the
test binary, never cmd.Env: exec dedups at Start time, so cmd.Env legitimately
holds both entries and a test on it can be written to pass in either direction.

Each name is appended only when non-empty, so 'the server has no public URL'
and 'this dispatch predates the feature' are the same observable state, which is
the right answer for both."
```

---

## Task 6: README

**Files:**
- Modify: `README.md` (one row in the `## relay-server` -> `### Configuration` table; one new subsection under `## relay-agent`)

No test. This is documentation of behaviour three earlier tasks already pinned.

- [ ] **Step 1: Add the server configuration row**

In the `## relay-server` -> `### Configuration` table, immediately after the `RELAY_GRPC_ADDR` row:

```markdown
| `RELAY_PUBLIC_URL` | _(empty)_ | Browser-facing base URL of the relay web UI, e.g. `https://relay.example.com`, or `https://ops.example.com/relay` behind a reverse proxy that strips a path prefix. The coordinator renders `RELAY_JOB_URL` and `RELAY_TASK_URL` from it into every task subprocess (see **relay-agent -> Task subprocess environment**). Unset means those two variables are simply absent - relay never guesses an origin, and `RELAY_JOB_ID` / `RELAY_TASK_ID` are injected either way. **An invalid value refuses to boot** rather than warning and disabling: a warn-and-disable typo would be indistinguishable from never having set the variable, and there is no defensible default origin to fall back to. Rejected: any scheme other than `http`/`https`, a missing host, userinfo (`https://user:pass@host`), a query string, a fragment, and any whitespace or control character. Trailing slashes are trimmed. The effective value is printed at startup on every boot, which is the only check on a value that parses perfectly and names the wrong host. A path prefix here is a string, not a route rewrite: relay serves its SPA from its own routes, so `https://ops.example.com/relay` produces working links only if your proxy actually strips `/relay`. |
```

- [ ] **Step 2: Add the agent subsection**

Insert a new subsection immediately after the `## relay-agent` -> `### Environment variables` table and before `### Hardware detection`:

```markdown
### Task subprocess environment

The table above describes the **agent's own** process environment. This section is a different thing: it is what relay adds to the environment of every **task subprocess** the agent spawns.

| Variable | Value | Present when |
|----------|-------|--------------|
| `RELAY_JOB_ID` | The job's UUID | Every dispatch from a relay coordinator. No server configuration needed. |
| `RELAY_TASK_ID` | The task's UUID | Every dispatch from a relay coordinator. No server configuration needed. |
| `RELAY_JOB_URL` | `<RELAY_PUBLIC_URL>/jobs/<job-id>` | `RELAY_PUBLIC_URL` is set on the **server** |
| `RELAY_TASK_URL` | `<RELAY_PUBLIC_URL>/jobs/<job-id>/tasks/<task-id>` | `RELAY_PUBLIC_URL` is set on the **server** |

**A job spec cannot override these four names.** The coordinator's values are appended after the spec's `env` map and after the workspace provider's environment, and `os/exec` keeps the last occurrence of a duplicate key, so a spec that sets `RELAY_JOB_URL` loses. That is the point: a step that posts `$RELAY_JOB_URL` into chat is posting a link other people will click, and the guarantee is what makes the value worth trusting. It covers **exactly these four names** and nothing that merely resembles them.

**Never set-and-empty.** Each name is either absent or carries a non-empty value, so one check is enough and there is no second case for "set but blank":

```sh
if [ -n "$RELAY_JOB_URL" ]; then notify "build running at $RELAY_JOB_URL"; fi
```

**Two limitations.**

- **These four names are reserved, and relay only appends - it does not strip.** If `relay-agent` is itself started from a shell that exported `RELAY_JOB_URL`, every task inherits that value wherever relay has no value of its own. The trust boundary this feature defends is the job spec author, not the agent operator, who already chooses the agent binary and owns the machine the subprocess runs on.
- **Windows folds environment variable names to upper case when resolving duplicates; other platforms do not.** A job spec key `relay_job_id` therefore loses to relay's `RELAY_JOB_ID` on a Windows agent and survives as a genuinely distinct variable elsewhere. Neither is a defect - a distinct variable is not an override - and the guarantee above is stated over these exact names for that reason.

A task that itself submits a relay job runs with its parent's `RELAY_JOB_ID` in scope, so a script that reads the variable after submitting gets the parent's id, not the new one.
```

- [ ] **Step 3: Verify the edit did not corrupt the file**

Programmatic edits to tracked text files on this CRLF repo have three independent failure modes. Run all of these:

```bash
git diff --stat -- README.md          # must be proportionate: ~2 lines + ~25 added
git ls-files --eol README.md          # must read i/lf
python -c "open('README.md','rb').read().decode('utf-8')"   # must not raise
```

Every character added above is ASCII; if the encoding check fails, something in the edit path introduced a non-ASCII byte and it must be found before committing.

- [ ] **Step 4: Commit**

```bash
git add README.md
git commit -m "docs: RELAY_PUBLIC_URL and the four task subprocess variables

The agent's existing 'Environment variables' table documents the agent's own
process environment; the new section documents the task subprocess environment,
which is a different thing, and the heading says so.

The precedence sentence is scoped to these four exact names. It cannot be
written as a claim about variables resembling them: os/exec folds case on
Windows only, so a spec's relay_job_id loses there and survives elsewhere as a
distinct variable."
```

---

## Task 7: Mutation battery and full lane sweep

**Files:** none committed by this task except the battery's own findings, which go in the PR description.

**Isolation is mandatory.** Copy the worktree to a scratch directory and mutate there. Never mutate this worktree while a sibling agent may be reading it, and **never revert a mutation with `git checkout --`** - that discards the uncommitted guard under test. Restore each mutated file from a copy you made first, and re-run a control that should die so you know the harness is live.

- [ ] **Step 1: Establish a green baseline**

```bash
go test ./... -timeout 120s
```

Uniform results across the battery mean a broken harness, not a strong suite. Record this baseline before mutating anything.

- [ ] **Step 2: Run the battery**

| # | Mutation | Killed by | The discriminating input |
|---|---|---|---|
| M1 | Move the identity block above the `task.Env` loop | `TestRunner_CoordinatorIdentityBeatsSpecEnv` | `task.Env` sets all four names to `https://evil.example/SPOOFED` |
| M2 | Move the identity block between the `task.Env` and `extraEnv` loops | `TestRunner_CoordinatorIdentityBeatsWorkspaceEnv` | `spoofingHandle.Env()` names `RELAY_JOB_URL` and `RELAY_TASK_ID` |
| M3 | Drop the `if task.JobUrl != ""` guard (append unconditionally) | `TestRunner_UnconfiguredPublicURLLeavesTheURLNamesAbsentAndTheIdsPresent` | a dispatch with `JobUrl` unset and both ids set - **see the platform note below** |
| M4 | Drop the `if task.JobId != ""` guard | `TestRunner_AnEmptyDispatchedIdProducesNoVariableAtAll` | `JobId: ""` with `TaskId: "task-abc"` - **same platform note** |
| M5 | Swap `JobUrl` and `TaskUrl` at the two `append` sites | `TestRunner_InjectsTheDispatchedIdentityIntoTheChildEnvironment` | the two URLs differ, and the assertion is on the whole map |
| M6 | Filter the four names out of `os.Environ()` before merging | `TestRunner_AnAgentProcessEnvValueSurvivesWhenTheCoordinatorHasNone` | the agent's own env carries `RELAY_JOB_URL` while the dispatch does not |
| M7 | Swap the `jobID`/`taskID` arguments at the `taskURL` call site in `sendTask` | `TestDispatcher_RendersJobAndTaskURLsFromTheClaimedRow` | job and task ids are independently generated UUIDs and the expected string is built from the seeded values, not from `dt` |
| M8 | `jobURL`/`taskURL` return a URL for an empty base | `TestJobAndTaskURL/an_empty_base_yields_no_URL_at_all` and `TestDispatcher_NoPublicURLSendsEmptyURLFieldsButStillSendsTheIds` | `base == ""` |
| M9 | Drop the `u.User != nil` check | `TestParsePublicURL_Rejects/userinfo...` and `TestParsePublicURL_RejectionDoesNotLeakAPassword` | `https://relay.example.com@evil.example/` |
| M10 | `strings.TrimRight(path, "/")` becomes `strings.TrimSuffix(path, "/")` | `TestParsePublicURL_AcceptsAndNormalizes/several_trailing_slashes` | `https://relay.example.com///` |
| M11 | `parsePublicURL` returns `("", nil)` instead of an error for an invalid value | every `require.Error` leg of `TestParsePublicURL_Rejects` | any rejected row |
| M12 | Hoist the control-character check below `url.Parse` | `TestParsePublicURL_Rejects/embedded_space` | `https://relay example.com` - `url.Parse` accepts a space, so only the pre-parse position refuses it |
| M13 | `publicURLLine` returns `""` for a configured base | `TestPublicURLLine_SaysWhichVariablesAreInjected` | any non-empty base |

**Two mutations are NOT covered, and the battery must say so rather than pad the table:**

| # | Mutation | Status |
|---|---|---|
| X1 | Render the URLs from `task` instead of `claimed` in `sendTask` | **SURVIVES BY CONSTRUCTION.** `ClaimTaskForWorker` selects on `id`, writes `status`/`worker_id`/`assigned_at`/`assignment_epoch`, and `RETURNING *` returns the same row, so `task.ID == claimed.ID` and `task.JobID == claimed.JobID` for every input that can exist. No test can distinguish the two. Sourcing from `claimed` is still correct as a consistency discipline; it is not a pinned behaviour and must not be described as one. |
| X2 | `main.go`'s `log.Fatalf` becomes `log.Printf` | **SURVIVES, DISCLOSED.** No test covers any `main`-resident `Fatalf` in this binary. The parser's error return is fully tested; the decision to make it fatal is one line protected only by sitting adjacent to its identical siblings (`ParseCORSOrigins`, `ParseRateLimit`, `RELAY_ALLOW_AUTO_ENROLL`). |

**Platform note on M3 and M4.** Both mutants produce a `NAME=` entry with an empty value in the child's environment block. On Linux that is an observable present-but-empty variable and `os.LookupEnv` returns `("", true)`, so the test reddens. On Windows the empty entry may not survive process creation, in which case the mutant is genuinely indistinguishable from correct behaviour **on that platform** and the mutation will report as survived. If you are on Windows and M3/M4 survive, re-run those two in the Linux container before concluding anything:

```bash
MSYS_NO_PATHCONV=1 docker run --rm -v "$(pwd -W):/src" -w /src -e CGO_ENABLED=1 \
  golang:1.26 go test ./internal/agent/ -run TestRunner -count=1 -timeout 300s
```

Report the result per platform. Do not record "killed" from a run that did not observe the kill.

- [ ] **Step 3: Run every lane**

```bash
make test
make vet-integration
go test -tags integration -p 1 ./internal/scheduler/ -timeout 900s
go test -tags integration -p 1 ./internal/worker/ -timeout 900s
make test-race
```

`make test-race` matters here: the identity block is appended inside `Runner.Run`, which runs one goroutine per task. On Windows the native lane is unreliable in two distinct ways (see CLAUDE.md) and the Linux container is the route that works:

```bash
MSYS_NO_PATHCONV=1 docker run --rm -v "$(pwd -W):/src" -w /src -e CGO_ENABLED=1 \
  golang:1.26 go test -race ./... -count=1 -timeout 600s
```

The container also runs `//go:build !windows` files that `go test` on Windows silently skips. **If the race lane is genuinely unavailable, say so in the PR description rather than substituting `-count=N`** - repetition raises confidence in flakiness, not in race-freedom.

- [ ] **Step 4: Record the battery in the PR description**

Thirteen killed, two survivors with their reasons, and the platform on which each M3/M4 result was observed. Do not commit a battery file.

---

## Acceptance criteria to task map

Every criterion in spec section 10, and where it lands. Run this check before opening the PR.

| Criterion | Task | Test |
|---|---|---|
| 1. Child sees `RELAY_TASK_ID` / `RELAY_JOB_ID` | 5 | `TestRunner_InjectsTheDispatchedIdentityIntoTheChildEnvironment` |
| 2. Spec `env` cannot spoof all four | 5 | `TestRunner_CoordinatorIdentityBeatsSpecEnv` |
| 3. Workspace handle cannot spoof | 5 | `TestRunner_CoordinatorIdentityBeatsWorkspaceEnv` |
| 4. Configured base renders both URLs | 4, 5 | `TestDispatcher_RendersJobAndTaskURLsFromTheClaimedRow` + `TestRunner_Injects...` |
| 5. Path prefix, one slash | 2, 3, 4 | `TestParsePublicURL_AcceptsAndNormalizes`, `TestJobAndTaskURL`, `TestDispatcher_Renders...` |
| 6. Unset means URL names absent AND ids present | 5 | `TestRunner_UnconfiguredPublicURLLeavesTheURLNamesAbsentAndTheIdsPresent` |
| 7. No relay-injected name is ever empty | 5 | `TestRunner_AnEmptyDispatchedIdProducesNoVariableAtAll` |
| 8. Server refuses to start on an invalid value | 2 | `TestParsePublicURL_Rejects` (the `Fatalf` line itself is X2, disclosed) |
| 9. Rejection does not leak the password | 2 | `TestParsePublicURL_RejectionDoesNotLeakAPassword` |
| 10. Every boot logs the effective value or its absence | 2 | `TestPublicURLLine_SaysWhichVariablesAreInjected` |
| 11. `DispatchTask` carries both, from the claimed row | 1, 4 | `TestDispatchTaskCarriesRenderedURLs`, `TestDispatcher_Renders...` (row provenance is X1) |
| 12. New agent + URL-less server injects ids only | 5 | `TestRunner_UnconfiguredPublicURLLeavesTheURLNamesAbsentAndTheIdsPresent` |
| 13. README documents all of it | 6 | prose |

---

## For the conductor

- **Backlog:** this plan has no stages and must not be handed to `/backlog phases`. It closes `docs/backlog/feature-2026-08-31-per-task-identity-env-vars.md` via `/backlog close per-task-identity`, and the closing note must record that the item was implemented **as amended**: the item names three variables and leaves the task-URL question open, and this slice ships four variables and two server-rendered URLs (spec R5).
- **One backlog item to file, offered not filed** (spec section 13): *strip inherited relay identity variables when relay has no value of its own*. It would make "absent" absolute rather than "absent unless the agent operator exported it". Deliberately out of this slice because it defends against an already-fully-trusted principal, and because `TestRunner_AnAgentProcessEnvValueSurvivesWhenTheCoordinatorHasNone` (mutation M6) makes the current behaviour a pinned, findable fact rather than an unexamined one.
- **Phase 4 verification lenses:** the security lens should read the precedence argument end to end - the guarantee rests entirely on `os/exec` dedup ordering and on the block staying last, and M1/M2 are the only things standing between it and a chat-phishing primitive. The invariants lens should confirm what this slice does *not* touch: no `tasks.status` write, no `task_logs` write, no epoch, no new job-spec struct, no new stream send.
