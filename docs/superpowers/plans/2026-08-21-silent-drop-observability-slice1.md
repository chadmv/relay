# Silent-drop observability, slice 1: the counters mechanism plus `netlimit` occupancy - Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Give `netlimit` a snapshot that carries current occupancy as well as cumulative refusals, put that snapshot behind one admin-only HTTP endpoint (`GET /v1/server/counters`), and make "a reporter decides whether to speak from monotonic counts only" a property the compiler enforces rather than one somebody remembers.

**Architecture:** `netlimit.Stats` splits into two halves - `Counts` (monotonic, `==`-comparable, drives the reporter) and `Levels` (current, carried in the line, never compared). `Stats()` reads all five numbers in a single critical section and converts to `uint64` at the boundary. `internal/api` gains one new file with the response types, a `CounterSources` struct of nil-able per-subsystem interfaces, and a handler that omits any section that is not wired. `cmd/relay-server` assigns `httpServer.Counters` after the listener exists. No SQL, no migration, no proto, no generated file, no frontend, no write to `tasks.status` or `task_logs`.

**Tech Stack:** Go 1.26, testify, `net/http.ServeMux` pattern routing, `go/ast` for two structural guards. No new module dependency.

---

## Slice independence declaration

**THERE IS ZERO FRONTEND WORK IN THIS SLICE. Do not dispatch `relay-frontend-engineer`.**

The slice is one backend lane, executed by one engineer, strictly sequenced (see the task index). The admin console's Server tab is the obvious eventual consumer of `GET /v1/server/counters` and is explicitly out of scope (spec section 14); it should become its own backlog item once numbers exist to render. Nothing under `web/` is touched, so `web/dist` must stay untouched too - if it becomes dirty, `git checkout -- web/dist/` before assembling the PR.

Phase 3 is therefore **not** parallel. Phase 4 is the usual four-lens fan-out plus the integration lane (Task 8 gives that lane a real target).

---

## Scope: this plan implements SLICE 1 ONLY

Slice 1 closes exactly one backlog item, `idea-2026-08-21-netlimit-occupancy-is-unobservable`. The three siblings (`idea-2026-08-20-repeated-watchdog-sweeps-...`, `idea-2026-08-14-tasklog-fence-rejection-...`, `idea-2026-08-15-ingest-log-suppression-...`) are **enabled only and stay open**. Anything that adds a counter to `internal/worker` or `internal/scheduler` is out of this slice; see "Not foreclosing slices 2-4" (refutation R2) for the constraint each of them inherits.

---

## Verification of the spec against HEAD (`4b97895`)

Every claim below was read in the tree at the worktree `pr-merge-session-961184`. The spec is not a contract; six of its claims moved the plan.

### Confirmed

- **C1. The `uint64` landmine is real, exactly as stated.** `cmd/relay-server/grpc_config_test.go:345-354` loops over every argument of every captured line and asserts `assert.IsType(t, uint64(0), a, "the summary must carry COUNTS ONLY. ...")`. `internal/netlimit/listener.go:84-85` declares `total int` and `perIP map[string]int`, so `len(l.perIP)` is an `int` too. Putting either into the log line unconverted turns a shipped test RED. **Resolution: convert at the snapshot boundary (Task 2), never weaken the assertion.** That assertion is the only thing standing between this log line and a caller-supplied byte; Task 4 keeps it and extends its reach from 2 arguments to 5.
- **C2. `refusalReporter.tick` compares the whole struct** (`grpc_config.go:437-444`, `if s == r.last`), and `r.last` is typed `netlimit.Stats` (`:433`). Adding a level to a flat `Stats` makes it speak every minute forever.
- **C3. The three occupancy figures need no new state.** `l.total` (`listener.go:84`), `len(l.perIP)` and a max over `l.perIP` (`:85`), all reachable under `l.mu`.
- **C4. The refusal counters are incremented under `l.mu`.** `admit` holds the lock across both `refusedTotal.Add(1)` and `refusedPerIP.Add(1)` (`listener.go:234-248`). This is what makes a five-field snapshot under one lock genuinely consistent rather than merely tidy.
- **C5. `perIP` entries are deleted at zero** (`listener.go:296-305`), pinned by `TestLimitListener_ReleasedIPIsRemovedFromTheMap`. `DistinctSources` therefore falls as sources drain.
- **C6. `internal/api` does not import `internal/netlimit` today** (`server.go:10-17`: events, metrics, store, worker), and `internal/netlimit` imports only `net`, `net/netip`, `sync`, `sync/atomic` (`listener.go:52-57`). It is a leaf.
- **C7. The `Metrics` seam is the shape claimed.** `api.Server.Metrics` is an exported field (`server.go:36-38`), set at `main.go:174`, nil-checked at `worker_metrics.go:34`. `handleGetWorkerMetrics` responds through `writeJSON` (`worker_metrics.go:73`) and reads no body, so the Single JSON entry point invariant is untouched.
- **C8. `AdminOnly` chains after `BearerAuth` and reads the user out of the request context** (`middleware.go:50-59`). The established spelling is `auth(admin(http.HandlerFunc(...)))` (`server.go:141-168`).

### REFUTED - each of these changed the plan

**R1. "The interfaces are declared in `internal/api`, so `internal/api` gains no import of `internal/scheduler` or `internal/netlimit`, and no import cycle is possible" (spec section 5) is false in its first half and right in its second half for the wrong reason.**

An interface declared in `internal/api` whose method returns `netlimit.Stats` **requires** `internal/api` to import `internal/netlimit`; there is no way to name the return type otherwise. The only shapes that avoid the import are (a) an adapter function built in `cmd/relay-server` and stored as a func field, which moves the field mapping out of the package that owns the payload contract and into `main()` where it cannot be unit-tested, or (b) `netlimit` importing `api`, which inverts the layering.

**The import is safe and is what this plan does:** `netlimit` is a stdlib-only leaf (C6), so `api -> netlimit` cannot cycle, and a cycle would be a compile error rather than a silent defect, so it needs no guard. The plan keeps the spec's wiring line verbatim (`httpServer.Counters = api.CounterSources{GRPCAdmission: grpcLis}`) and puts the mapping inside `internal/api` where a fake source can drive it.

The second half of the claim - "no import cycle is possible" - is true for `netlimit` and **inverts for `scheduler`**: see R2.

**R2. NEW, not in the spec, and it is the constraint that decides whether this mechanism survives to slice 4: `internal/scheduler` already imports `internal/api`** (`internal/scheduler/dispatch.go:11`, `internal/scheduler/source_proto.go:5`). So `internal/api` can **never** import `internal/scheduler`, and slice 4's watchdog source therefore cannot follow slice 1's pattern of "the interface returns the subsystem's own type".

This does not foreclose slice 4, but only because `CounterSources` is a struct of independent fields whose types are decided per section. Slice 4's shape must be: declare `WatchdogCounters` **in `internal/api`** alongside the rest of the payload types, declare `type WatchdogSource interface { CounterSnapshot() WatchdogCounters }` in `internal/api`, and have `scheduler.Watchdog` return that type - legal, because scheduler already imports api. Slices 2 and 3 have no such problem (`internal/api` already imports `internal/worker`, `server.go:13`). **Write this down in `server_counters.go`'s doc comment (Task 5) or slice 4 will rediscover it as a cycle error and reshape the mechanism under time pressure.**

**R3. "Only `TestRefusalSummaryLogsOnlyWhenCountersMove` may change, and only its literals" (spec section 11.5) is false.** The `Counts`/`Levels` split also breaks **ten** assertion expressions in `internal/netlimit/listener_test.go` - lines 99, 100, 249, 250, 305, 429, 474, 475, 527, 548 - each of the form `l.Stats().RefusedPerIP` or `l.Stats().RefusedTotal`, which must become `l.Stats().Counts.RefusedPerIP` / `.Counts.RefusedTotal`. These are compile errors, so they are loud, but the spec priced this change at four literals and it is fourteen sites. **Task 1 isolates them into one mechanical commit with an explicit diff gate and a mutation that proves the re-nested assertions are still load-bearing** (the recorded lesson that a zero-diff refactor gate can be decorative for every re-wired site).

`listener_test.go:275` (`assert.Equal(t, Stats{}, l.Stats(), ...)`) is the one site that needs **no** edit and gains teeth for free: see R6.

**R4. "`MaxPerSource`'s scan is bounded with `perIP` ... at most `MaxTotal` = 1024 entries at the defaults" (spec section 7.1) is true at the defaults and false at a documented, supported configuration.** `admit` is reached whenever *either* cap is enabled (`listener.go:131-137`), so with `RELAY_GRPC_MAX_CONNS=0` and `RELAY_GRPC_MAX_CONNS_PER_IP=64` - a configuration README explicitly discusses - `l.perIP` is bounded only by the process file-descriptor limit, and so is the scan. The map is already unbounded in that configuration at HEAD; this slice does not introduce it, but it does introduce an O(len(perIP)) walk over it under the lock.

**Priced and accepted, not fixed.** The endpoint's own `BearerAuth` costs a Postgres round trip per request (`middleware.go:27`, `GetTokenWithUser`), which is three orders of magnitude more than a 1024-entry integer map walk, so an admin cannot poll fast enough for the scan to matter; and `l.mu`'s other holders are `admit` and `release`, which run once per TCP connection, not per message. The operator who disables the total cap has already accepted unbounded connections. **This must be stated in `Stats()`'s doc comment** (Task 2) rather than left as a bound that is true only at the defaults.

**R5. The spec's payload leaves a hole that is the spec's own subject one layer down, and the plan discloses it rather than closing it.** When **both** caps are disabled, `Accept` returns the conn unwrapped and never calls `admit` (`listener.go:131-133`), so every level reads `0` no matter how many connections are live. `live_total: 0` then means "not measured", not "nothing there" - exactly the "absent versus zero" distinction the spec makes for whole sections (D11) and does not make for this case. Closing it in the payload would need either a boolean (banned by the counts-only rule) or the configured caps as extra fields (a contract expansion, and `max_per_source` as the observed maximum next to `max_per_source` as the configured cap is a naming trap nobody should ship). **Decision: document it in `Stats`'s doc comment and in README, and hand the conductor a candidate backlog item.** It is already pinned by an existing test (R6).

**R6. The spec's proposed `TestStats_LevelsAreZeroWhenBothCapsAreDisabled` is redundant and must not be written.** `TestLimitListener_ZeroDisables` (`listener_test.go:262-276`) already asserts `Stats{} == l.Stats()` after admitting 200 connections with both caps off. Once `Stats` carries levels, that existing assertion becomes the guard for R5's disclosure at zero cost, and it goes RED if anybody "fixes" R5 by accounting on the disabled path. An existing test gaining teeth from a shape change is worth noticing rather than duplicating.

### Settled, not refuted: the six conductor leads

**Lead 1 - the `uint64` landmine.** Confirmed (C1). The fields are `uint64` at the snapshot boundary; the assertion is untouched and its reach grows from 2 arguments to 5. `l.total` cannot go negative (`release` runs once per admitted conn, enforced by `conn.once`, `listener.go:290-294`), so the conversion is safe; and if an accounting bug ever made it negative, an absurd number in the payload is a better signal than a clamped zero. No clamp. Comment says so.

**Lead 2 - the Counts/Levels split.** It does solve the recorded trap, and the mechanism is not the split by itself: it is that `refusalReporter.last` is typed `netlimit.RefusalCounts`, which makes `s == r.last` a **compile error**. A split with `last` still typed `Stats` would buy nothing. Cost: R3's fourteen sites. **Breaking a one-day-old type does not matter** - `netlimit` is `internal/`, its only non-test consumer is `cmd/relay-server`, and every break is a compile error.

Embedding (`type Stats struct { RefusalCounts; Occupancy }`) was considered and **rejected**, even though it would leave ten of the fourteen sites byte-identical: promoted fields make `s.LiveTotal` read exactly like `s.RefusedTotal` at every call site, and making that distinction visible is the entire point of the slice. Named halves also mirror the JSON payload's `counts`/`levels` shape, which is what carries the rule to the author of slice 2.

**Lead 3 - snapshot atomicity.** All five numbers are read under `l.mu` in one critical section. The refusal counters stay `atomic.Uint64` (no reason to change a working type) and are `Load()`ed inside the lock; because `admit` increments them under that same lock (C4), the snapshot is internally consistent rather than merely each-field-atomic. `MaxPerSource` is an O(len(perIP)) scan - see R4 for the real bound and the price. The endpoint is a second, faster-cadence caller than the reporter, and it is still bounded by its own auth round trip.

**Lead 4 - the endpoint.** Routes are registered on a `http.ServeMux` inside `Server.Handler()` with method-and-path patterns (`server.go:71-186`); admin routes are spelled `auth(admin(http.HandlerFunc(s.handleX)))` where `auth := BearerAuth(s.q)` and `admin := AdminOnly` (`:75-76`). The `api.Server.Counters` seam exists in the claimed shape (C7) with one correction: **`httpServer` is constructed at `main.go:173` but `grpcLis` does not exist until `main.go:207`**, so the assignment cannot sit next to `httpServer.Metrics = metricsStore`; it goes after the `netlimit.Wrap` block and before `httpServer.Handler()` is called at `main.go:253`. Response shape is fixed by spec section 9 and reproduced verbatim in Task 5. It **does** need README: this project treats a missing or wrong docs contract as a defect (Task 9).

**Lead 5 - the cardinality guard.** The guard is a **reflection walk over the response type tree** (`TestCounterPayloadCarriesNoIdentifiers`), plus a sibling walk over `netlimit.Stats` (`TestStats_CarriesNoIdentifiers`). It is not decorative, and the reason is that it is written from the property ("every leaf is an unsigned integer, except an explicit path allow-list") and searched against the shape: pointers are dereferenced, structs are recursed into, and maps, slices, strings, signed integers, floats, bools and interfaces all fail. Five mutations in the battery (M15-M19) each add a *differently shaped* offending field, and one of them (M15b) adds it with the expected-path list already updated, so the type rule has to kill it on its own. This is the discipline the 2026-08-21 retro demands: write the guard from the property, then adversarially search for other spellings and record the hit count.

The walk also asserts the **exact set of leaf paths**, which is what makes it non-vacuous - a walk that visited nothing would pass a type check trivially, and `require.NotEmpty` with a scary message is the recorded anti-pattern ("a principle stated in an assertion message is not a check").

**Lead 6 - what slice 1 closes.** Item 4's seven acceptance bullets, checked one at a time against this plan, are all met - see "Item 4 closure check" below. It is genuinely closable.

---

## File structure

**Create**

- `internal/api/server_counters.go` - the response types, `CounterSources`, `GRPCAdmissionSource`, `handleServerCounters`. Owns the payload contract for all four slices.
- `internal/api/server_counters_test.go` (package `api`) - endpoint unit tests, the payload identifier guard, and the route-shape AST guard.
- `internal/api/server_counters_integration_test.go` (package `api_test`, `//go:build integration`) - real 401/403/200 through `BearerAuth` and the database.
- `cmd/relay-server/counters_wiring_test.go` (package `main`) - the `main.go` structural wiring guard.

**Modify**

- `internal/netlimit/listener.go:67-74` (the `Stats` type) and `:142-145` (`Stats()`).
- `internal/netlimit/listener_test.go` - ten mechanical re-nestings, plus three new tests.
- `cmd/relay-server/grpc_config.go:432-444` (`refusalReporter`, `tick`).
- `cmd/relay-server/grpc_config_test.go:332-342` (four literals) plus two new tests.
- `internal/api/server.go:20-43` (two new `Server` fields), `:46-68` (`New` records `startedAt`), `:133-138` area (one route).
- `cmd/relay-server/main.go` - one line after the `netlimit.Wrap` block.
- `README.md` - a new `### Server` REST subsection and one sentence in the `RELAY_GRPC_MAX_CONNS_PER_IP` row.

**Critical files - read these before writing anything:** `internal/netlimit/listener.go` (whole file, especially the package doc and `admit`/`release`), `cmd/relay-server/grpc_config.go:420-460`, `cmd/relay-server/grpc_config_test.go:313-762` (the test you must not weaken and the AST guard you are copying), `internal/api/server.go`, `internal/api/worker_metrics.go` (the optional-state precedent), `docs/retros/2026-08-21-grpc-admission-bounds.md` (why every structural guard in this repo gets evaded on the first attempt), and CLAUDE.md's Invariants.

**Never touched:** anything under `web/`, `internal/store/`, `proto/`, `internal/proto/`, `internal/agent/`, `internal/worker/`, `internal/scheduler/`, `internal/metrics/`. **If a step ever suggests `make generate`, that step is wrong** - this slice has zero SQL and zero proto.

---

## Task index and sequencing

| Task | What | Depends on |
| --- | --- | --- |
| 1 | Split `netlimit.Stats` into `Counts`/`Levels` (no new fields) | - |
| 2 | Occupancy, read in one critical section, plus the type guard | 1 |
| 3 | The reporter's trigger reads counts only | 2 |
| 4 | The reporter's line carries occupancy | 3 |
| 5 | `GET /v1/server/counters`: types, handler, route, unit tests | 2 |
| 6 | The payload identifier allow-list guard | 5 |
| 7 | `main.go` wiring plus its structural guard | 5 |
| 8 | Integration gating test (real auth, real database) | 5 |
| 9 | README | 5 |
| 10 | Full gates plus the mutation battery | all |

Tasks 3-4 and 5-9 are independent of each other after Task 2, but one engineer executes them in the order above. **Task 7 is the one the slice depends on: everything before it compiles, passes, and reports an empty payload in production.**

---

## Task 1: Split `netlimit.Stats` into `Counts` and `Levels`

This task adds **no new field and changes no behaviour**. It is a shape change whose only purpose is to make the later trigger rule a compile-time property. There is therefore no new RED test; the gate is the one stated in Step 5, plus the mutation in Step 6.

**Files:**
- Modify: `internal/netlimit/listener.go:67-74`, `:142-145`
- Modify: `internal/netlimit/listener_test.go` (10 assertion expressions)
- Modify: `cmd/relay-server/grpc_config.go:432-444`
- Modify: `cmd/relay-server/grpc_config_test.go:332-342` (4 literals)

- [ ] **Step 1: Replace the `Stats` type.** In `internal/netlimit/listener.go`, replace lines 67-74 (the `Stats` type and its comment) with:

```go
// RefusalCounts are MONOTONIC totals since process start. They only ever
// increase, which is what makes them safe to compare: a consumer that wants to
// know whether anything happened can compare two snapshots of this half.
//
// Comparable by == deliberately. cmd/relay-server's refusalReporter stores one
// of these and compares, and that must keep compiling.
type RefusalCounts struct {
	RefusedTotal uint64

	// RefusedPerIP UNDER-REPORTS whenever the fleet cap is also saturated:
	// admit checks the total first, so a connection over BOTH caps is counted
	// here as zero and against RefusedTotal only. That is deliberate and is not
	// being changed. What makes it interpretable is Occupancy: when LiveTotal
	// has reached the configured MaxTotal, read this number as a FLOOR rather
	// than as a measurement.
	RefusedPerIP uint64
}

// Occupancy is the CURRENT state of the two caps. Every field is a level, not a
// count: it goes down as well as up.
//
// LEVELS ARE NEVER CONSULTED TO DECIDE WHETHER A REPORTER SPEAKS. Occupancy
// changes on essentially every connection, so a periodic summary that included
// it in its "did anything move" test would emit a line every single interval
// forever - which is the property TestRefusalSummaryLogsOnlyWhenCountersMove
// exists to protect. Levels are carried IN the line when it speaks. Splitting
// them from RefusalCounts is what makes that structural: refusalReporter.last
// is typed RefusalCounts, so comparing a whole Stats does not compile.
type Occupancy struct {
	LiveTotal       uint64
	DistinctSources uint64
	MaxPerSource    uint64
}

// Stats is a snapshot of this listener's counters and levels.
//
// RULE, NOT DESCRIPTION: nothing in this type may ever carry an address, a
// prefix, a hostname, or any other caller-supplied byte. The refusal path is
// reachable by any unauthenticated peer, and the consumer reports these as a
// periodic log summary, so a field carrying caller-supplied bytes would be a new
// attacker-driven log site inside the very control that bounds attacker-driven
// log volume. Counts and levels only, forever - "which IP is it?" is answered
// NO on the record, and TestStats_CarriesNoIdentifiers enforces it by walking
// this type with reflection rather than by trusting this paragraph.
//
// PER REPLICA. These are in-process numbers about ONE listener. A two-server
// deployment splits its connections arbitrarily; an operator must read both
// endpoints and add the counts, and must NOT add the levels - MaxPerSource in
// particular does not sum into anything meaningful.
type Stats struct {
	Counts RefusalCounts
	Levels Occupancy
}
```

- [ ] **Step 2: Re-nest the constructor.** Replace `Stats()` (lines 142-145) with:

```go
// Stats returns a snapshot of the refusal counters.
func (l *Listener) Stats() Stats {
	return Stats{Counts: RefusalCounts{
		RefusedTotal: l.refusedTotal.Load(),
		RefusedPerIP: l.refusedPerIP.Load(),
	}}
}
```

- [ ] **Step 3: Re-nest the ten call sites in `internal/netlimit/listener_test.go`.** Insert `.Counts` after `l.Stats()` at lines 99, 100, 249, 250, 305, 429, 474, 475, 527, 548 - and nowhere else. For example line 99 becomes:

```go
	assert.Equal(t, uint64(1), l.Stats().Counts.RefusedPerIP)
```

Line 275 (`assert.Equal(t, Stats{}, l.Stats(), "nothing may be counted as refused when both caps are off")`) is **left exactly as it is**. It still compiles and still passes, and in Task 2 it silently becomes the guard for the both-caps-disabled disclosure.

- [ ] **Step 4: Re-nest the reporter.** In `cmd/relay-server/grpc_config.go`, `refusalReporter.tick` keeps comparing the whole struct for now - this task changes no behaviour - but the log line must read through the new half:

```go
func (r *refusalReporter) tick(s netlimit.Stats) {
	if s == r.last {
		return
	}
	r.logf("gRPC admission: %d connection(s) refused over the total cap and %d over the per-source-IP cap since startup",
		s.Counts.RefusedTotal, s.Counts.RefusedPerIP)
	r.last = s
}
```

And in `cmd/relay-server/grpc_config_test.go`, re-nest the four literals at lines 332-342 **without touching a single assertion**:

```go
	r.tick(netlimit.Stats{})
	assert.Empty(t, lines, "a quiet interval must produce no line at all")

	r.tick(netlimit.Stats{Counts: netlimit.RefusalCounts{RefusedPerIP: 3}})
	require.Len(t, lines, 1, "the first movement must be reported")

	r.tick(netlimit.Stats{Counts: netlimit.RefusalCounts{RefusedPerIP: 3}})
	assert.Len(t, lines, 1,
		"an unchanged counter must not re-log: a sustained attack must cost ONE line per interval, not one per tick")

	r.tick(netlimit.Stats{Counts: netlimit.RefusalCounts{RefusedTotal: 2, RefusedPerIP: 3}})
	require.Len(t, lines, 2, "a movement on the OTHER counter must also be reported")
```

- [ ] **Step 5: Run the gate.**

Run: `go test ./internal/netlimit/... ./cmd/relay-server/... -timeout 120s`
Expected: PASS, all packages.

Then run: `git diff internal/netlimit/listener_test.go cmd/relay-server/grpc_config_test.go`
Expected: **every hunk is either the insertion of `.Counts` or the re-nesting of a literal.** No expected value changes, no assertion message changes, no test added or removed in this task. If any assertion had to change to stay green, STOP: that is a finding to report, not to fix.

- [ ] **Step 6: Prove the re-nested assertions are still load-bearing.** A refactor gated only on "the tests still pass" is decorative until you show the re-wired assertions can still fail. Temporarily edit `Stats()` to return the wrong counter:

```go
		RefusedPerIP: l.refusedTotal.Load(),
```

Run: `go test ./internal/netlimit/ -timeout 60s`
Expected: **FAIL** in `TestLimitListener_RefusesBeyondPerIPCap` (expects 1, gets 0) and in at least three other tests. Revert the edit with `git checkout -- internal/netlimit/listener.go` and re-run to confirm green.

- [ ] **Step 7: Commit.**

```bash
git add internal/netlimit/listener.go internal/netlimit/listener_test.go cmd/relay-server/grpc_config.go cmd/relay-server/grpc_config_test.go
git commit -m "refactor(netlimit): split Stats into monotonic Counts and current Levels halves"
```

---

## Task 2: `Stats()` reports occupancy, read in one critical section

**Files:**
- Modify: `internal/netlimit/listener.go` (the `Occupancy` fields are already declared; `Stats()` gains the read)
- Modify: `internal/netlimit/listener_test.go` (three new tests, appended)

**On the RED.** A test that fails only because a field does not exist yet is a vacuous RED. Step 1 therefore installs the **HEAD behaviour as an explicit stub** - the listener reports no occupancy today, so the stub reports zeros - and the tests in Step 2 then fail on a *value*, which is a real behavioural RED.

- [ ] **Step 1: Install the zero stub.** Replace `Stats()` (from Task 1) with:

```go
func (l *Listener) Stats() Stats {
	return Stats{Counts: RefusalCounts{
		RefusedTotal: l.refusedTotal.Load(),
		RefusedPerIP: l.refusedPerIP.Load(),
	}, Levels: Occupancy{}}
}
```

Run: `go test ./internal/netlimit/ -timeout 60s`
Expected: PASS (this is HEAD's behaviour, spelled out).

- [ ] **Step 2: Write the two failing behavioural tests.** Append to `internal/netlimit/listener_test.go`:

```go
// TestStats_ReportsOccupancy is the "how full is it right now" half of
// idea-2026-08-21-netlimit-occupancy-is-unobservable. Cumulative refusals cannot
// answer it: a RefusedTotal that stopped moving means either the pressure ended
// or the fleet settled at exactly the ceiling, and those need opposite
// responses.
//
// admit/release are driven directly rather than through Accept: they are the two
// critical sections Stats reads, and driving them straight makes the arithmetic
// the subject instead of the fake listener's plumbing.
func TestStats_ReportsOccupancy(t *testing.T) {
	l := Wrap(&fakeListener{}, Config{MaxTotal: 100, MaxPerIP: 100})
	require.True(t, l.admit("10.0.0.1"))
	require.True(t, l.admit("10.0.0.1"))
	require.True(t, l.admit("10.0.0.2"))

	s := l.Stats()
	assert.Equal(t, uint64(3), s.Levels.LiveTotal)
	assert.Equal(t, uint64(2), s.Levels.DistinctSources)
	assert.Equal(t, uint64(2), s.Levels.MaxPerSource, "10.0.0.1 holds two of the three")

	l.release("10.0.0.1")
	s = l.Stats()
	assert.Equal(t, uint64(2), s.Levels.LiveTotal, "a released slot must lower the level")
	assert.Equal(t, uint64(2), s.Levels.DistinctSources, "10.0.0.1 still holds one, so it is still a source")
	assert.Equal(t, uint64(1), s.Levels.MaxPerSource, "both sources now hold one each")

	l.release("10.0.0.2")
	s = l.Stats()
	assert.Equal(t, uint64(1), s.Levels.LiveTotal)
	assert.Equal(t, uint64(1), s.Levels.DistinctSources, "an emptied source leaves the map entirely")
	assert.Equal(t, uint64(0), s.Counts.RefusedTotal, "nothing was refused; occupancy must not be confused with refusal")
}

// TestStats_DistinguishesDistributedFromNAT is the item's second acceptance
// bullet AND the detection story for the IPv6 delegation residual the admission
// slice disclosed and could not fix. A healthy fleet behind NAT is a few sources
// holding many connections each; a distributed source pattern is many sources
// holding one each. RefusedTotal cannot tell them apart, and neither can
// LiveTotal - arrangements (a) and (b) below have IDENTICAL LiveTotal.
func TestStats_DistinguishesDistributedFromNAT(t *testing.T) {
	admitN := func(t *testing.T, l *Listener, key string, n int) {
		t.Helper()
		for i := 0; i < n; i++ {
			require.True(t, l.admit(key), "admit %s #%d must not be refused by these caps", key, i)
		}
	}

	// (a) The NAT shape: one source holding 64.
	nat := Wrap(&fakeListener{}, Config{MaxTotal: 4096, MaxPerIP: 4096})
	admitN(t, nat, "10.0.0.1", 64)
	n := nat.Stats()
	assert.Equal(t, uint64(64), n.Levels.LiveTotal)
	assert.Equal(t, uint64(1), n.Levels.DistinctSources)
	assert.Equal(t, uint64(64), n.Levels.MaxPerSource)

	// (b) The distributed shape: 64 sources holding one each.
	dist := Wrap(&fakeListener{}, Config{MaxTotal: 4096, MaxPerIP: 4096})
	for i := 0; i < 64; i++ {
		admitN(t, dist, fmt.Sprintf("10.1.0.%d", i), 1)
	}
	d := dist.Stats()
	require.Equal(t, n.Levels.LiveTotal, d.Levels.LiveTotal,
		"the two shapes must be indistinguishable by total occupancy alone - that is the premise of this test")
	assert.Equal(t, uint64(64), d.Levels.DistinctSources)
	assert.Equal(t, uint64(1), d.Levels.MaxPerSource)

	// (c) The IPv6 delegation shape at relay's real defaults: 16 /64s x 64
	//     connections fills the 1024 fleet cap with NOTHING refused. This is the
	//     case the item's Notes section asks to be in the matrix.
	deleg := Wrap(&fakeListener{}, Config{MaxTotal: 1024, MaxPerIP: 64})
	for p := 0; p < 16; p++ {
		admitN(t, deleg, fmt.Sprintf("2001:db8:0:%x::/64", p), 64)
	}
	g := deleg.Stats()
	assert.Equal(t, uint64(1024), g.Levels.LiveTotal, "the fleet cap is exactly full")
	assert.Equal(t, uint64(16), g.Levels.DistinctSources)
	assert.Equal(t, uint64(64), g.Levels.MaxPerSource, "every source sits exactly on the per-source cap")
	assert.Equal(t, uint64(0), g.Counts.RefusedTotal,
		"nothing has been refused YET, which is exactly why the refusal counters cannot see this shape")
	assert.Equal(t, uint64(0), g.Counts.RefusedPerIP)

	// The seventeenth source - a legitimate agent - is now refused by the TOTAL cap.
	require.False(t, deleg.admit("2001:db8:0:ff::/64"))
	g = deleg.Stats()
	assert.Equal(t, uint64(1), g.Counts.RefusedTotal)
	assert.Equal(t, uint64(64), g.Levels.MaxPerSource, "a refusal must move no level at all")

	// (d) The UNEQUAL arrangement, and the busiest source is deliberately in the
	//     MIDDLE: a MaxPerSource implemented as "the first entry" or "the last
	//     entry" must not be able to pass by position. 1 + 7 + 2 = 10 live, 3
	//     sources, max 7 - four numbers, all different, so len(perIP) and
	//     LiveTotal are both visibly wrong answers.
	uneq := Wrap(&fakeListener{}, Config{MaxTotal: 100, MaxPerIP: 100})
	admitN(t, uneq, "10.2.0.1", 1)
	admitN(t, uneq, "10.2.0.2", 7)
	admitN(t, uneq, "10.2.0.3", 2)
	u := uneq.Stats()
	assert.Equal(t, uint64(10), u.Levels.LiveTotal)
	assert.Equal(t, uint64(3), u.Levels.DistinctSources)
	assert.Equal(t, uint64(7), u.Levels.MaxPerSource,
		"MaxPerSource is the LARGEST per-source count, not the number of sources and not the total")
}
```

- [ ] **Step 3: Run them and watch them fail on values.**

Run: `go test ./internal/netlimit/ -run 'TestStats_ReportsOccupancy|TestStats_DistinguishesDistributedFromNAT' -v -timeout 60s`
Expected: **FAIL**, both, with value mismatches (`expected: 0x3, actual: 0x0`) - not with "undefined" compile errors. If you see a compile error, the stub in Step 1 was not installed.

- [ ] **Step 4: Implement the single-critical-section read.** Replace `Stats()` with:

```go
// Stats returns ONE snapshot of every counter and every level, taken in a
// SINGLE critical section.
//
// THE SINGLE CRITICAL SECTION IS THE CONTRACT, not an implementation detail.
// Three separate lock acquisitions let a caller observe a combination that never
// existed - DistinctSources greater than LiveTotal is directly reachable while
// connections are being admitted - and an operator would then draw a conclusion
// from an arrangement the fleet was never in. Pinned by
// TestStats_IsOneCriticalSection, which is RED under exactly that mutation.
//
// The refusal counters are atomics, but they are INCREMENTED under this same
// mutex (see admit), so reading them here makes the whole five-field snapshot
// consistent rather than merely each field individually atomic.
//
// COST: MaxPerSource is an O(len(perIP)) walk under l.mu. len(perIP) is bounded
// by MaxTotal (1024 at the defaults) only while the TOTAL cap is enabled; with
// RELAY_GRPC_MAX_CONNS=0 and a live per-source cap, admit still runs and perIP
// is bounded only by the process file-descriptor limit, so this walk is
// proportional to live connections. That is accepted rather than fixed: the
// mutex's other holders are admit and release, which run once per TCP
// connection rather than per message, and the only other caller is an
// admin-authenticated HTTP handler whose own bearer-token check costs a Postgres
// round trip. Maintaining the maximum incrementally is NOT cheaper - a
// decremented maximum is not exactly recoverable without a scan, which would
// move this walk onto release, a path much closer to hot than this one.
//
// WHEN BOTH CAPS ARE DISABLED, EVERY LEVEL READS ZERO NO MATTER HOW MANY
// CONNECTIONS ARE LIVE. Accept returns the conn unwrapped in that configuration
// and never calls admit, so nothing is counted. A zero here therefore means "not
// measured", not "nothing there". Pinned by TestLimitListener_ZeroDisables.
func (l *Listener) Stats() Stats {
	l.mu.Lock()
	defer l.mu.Unlock()
	maxPer := 0
	for _, n := range l.perIP {
		if n > maxPer {
			maxPer = n
		}
	}
	return Stats{
		Counts: RefusalCounts{
			RefusedTotal: l.refusedTotal.Load(),
			RefusedPerIP: l.refusedPerIP.Load(),
		},
		// uint64 AT THE BOUNDARY, and this is load-bearing rather than tidy:
		// the consumer's summary line asserts that every argument it carries is
		// a uint64 (TestRefusalSummaryLogsOnlyWhenCountersMove), which is what
		// keeps caller-supplied bytes out of an attacker-reachable log site. An
		// int occupancy figure turns that shipped test RED.
		//
		// No clamp on the conversion. l.total cannot go negative - release runs
		// exactly once per admitted conn, enforced by conn.once - and if an
		// accounting bug ever made it negative, an absurd number here is a
		// better signal than a zero that hides it.
		Levels: Occupancy{
			LiveTotal:       uint64(l.total),
			DistinctSources: uint64(len(l.perIP)),
			MaxPerSource:    uint64(maxPer),
		},
	}
}
```

- [ ] **Step 5: Run the whole package.**

Run: `go test ./internal/netlimit/ -timeout 60s`
Expected: PASS, including the untouched `TestLimitListener_ZeroDisables`.

- [ ] **Step 6: Add the concurrency test.** Append to `internal/netlimit/listener_test.go`:

```go
// TestStats_IsOneCriticalSection is the test the backlog item asks for by name,
// and its discriminating property is real rather than aspirational: with three
// separate lock acquisitions, connections being admitted between the reads make
// DistinctSources > LiveTotal directly observable.
//
// -race IS NOT THE INSTRUMENT HERE. The mutation this test exists to kill takes
// the lock three times instead of once, so every read is still properly
// synchronised and -race stays perfectly quiet under it. The INVARIANTS are the
// instrument.
//
// The two "saw" counters are not decoration: a reader that only ever sampled an
// empty listener would satisfy every invariant vacuously, which is the recorded
// "measure the populated state" failure. They make the test fail when it proves
// nothing.
func TestStats_IsOneCriticalSection(t *testing.T) {
	const (
		sources = 128
		rounds  = 40
	)
	l := Wrap(&fakeListener{}, Config{MaxTotal: 100000, MaxPerIP: 100000})

	keys := make([]string, sources)
	for i := range keys {
		keys[i] = fmt.Sprintf("10.9.%d.%d", i/256, i%256)
	}

	stop := make(chan struct{})
	var churn sync.WaitGroup
	churn.Add(1)
	go func() {
		defer churn.Done()
		defer close(stop)
		for r := 0; r < rounds; r++ {
			for _, k := range keys {
				l.admit(k)
			}
			for _, k := range keys {
				l.release(k)
			}
		}
	}()

	var bad []Occupancy
	reads, sawLive, sawManySources := 0, 0, 0
	for done := false; !done; {
		select {
		case <-stop:
			done = true
		default:
		}
		s := l.Stats()
		reads++
		if s.Levels.LiveTotal > 0 {
			sawLive++
		}
		if s.Levels.DistinctSources > 1 {
			sawManySources++
		}
		if s.Levels.DistinctSources > s.Levels.LiveTotal || s.Levels.MaxPerSource > s.Levels.LiveTotal {
			bad = append(bad, s.Levels)
			done = true // one counter-example is the whole finding
		}
	}
	churn.Wait()

	t.Logf("%d snapshots; %d saw live connections; %d saw more than one source", reads, sawLive, sawManySources)
	require.Positive(t, sawLive,
		"the reader never observed a single live connection, so it proved nothing about a populated listener")
	require.Positive(t, sawManySources,
		"the reader never observed more than one source, so the DistinctSources invariant was never exercised")
	require.Empty(t, bad,
		"a snapshot reported more distinct sources (or a bigger per-source maximum) than it reported live "+
			"connections. That arrangement never existed: the numbers were read in separate critical sections "+
			"with connections opening and closing in between, and an operator would read it as a distributed "+
			"source pattern that is not there.")
}
```

- [ ] **Step 7: Run it, including under the race detector.**

Run: `go test ./internal/netlimit/ -run TestStats_IsOneCriticalSection -v -timeout 60s`
Expected: PASS, with a log line showing a large `reads` count and non-zero `sawLive`/`sawManySources`.

Run: `go test -race ./internal/netlimit/ -timeout 120s`
Expected: PASS. (On Windows `-race` needs MSYS2 mingw64 gcc: `CC=/c/msys64/mingw64/bin/gcc.exe`. If that toolchain is unavailable, say so in the PR body and leave the `-race` run to Phase 4.)

- [ ] **Step 8: Add the identifier guard.** Append to `internal/netlimit/listener_test.go` (and add `"reflect"` to the file's imports):

```go
// TestStats_CarriesNoIdentifiers answers "which IP is it?" NO, on the record and
// in code rather than in a comment. The refusal path is reachable by any
// unauthenticated peer and this type is rendered into a periodic log line, so a
// string field here would be an attacker-writable log site inside the control
// that exists to bound attacker-driven log volume.
//
// The leaf-path assertion is what stops this being vacuous: a walk that visited
// nothing would satisfy the type check trivially, and a NotEmpty check with a
// stern message is not a check.
func TestStats_CarriesNoIdentifiers(t *testing.T) {
	st := reflect.TypeOf(Stats{})
	require.Equal(t, 2, st.NumField(),
		"Stats has exactly two halves, Counts and Levels. A field added directly to Stats is neither "+
			"monotonic nor current, so no reporter can classify it and the trigger rule has no answer for it.")

	var leaves []string
	for i := 0; i < st.NumField(); i++ {
		half := st.Field(i)
		require.Equal(t, reflect.Struct, half.Type.Kind(), "Stats.%s must be a struct half", half.Name)
		for j := 0; j < half.Type.NumField(); j++ {
			f := half.Type.Field(j)
			path := half.Name + "." + f.Name
			leaves = append(leaves, path)
			switch f.Type.Kind() {
			case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
			default:
				t.Fatalf("netlimit.Stats.%s is a %s. Every field of this type must be an UNSIGNED INTEGER: "+
					"an address, a prefix, a hostname or any other caller-supplied byte reaches an "+
					"attacker-driven log site through the refusal summary. More numbers, never identifiers.",
					path, f.Type.Kind())
			}
		}
	}
	assert.ElementsMatch(t, []string{
		"Counts.RefusedTotal", "Counts.RefusedPerIP",
		"Levels.LiveTotal", "Levels.DistinctSources", "Levels.MaxPerSource",
	}, leaves,
		"the field set of netlimit.Stats changed. Adding a number is fine - update this list deliberately - "+
			"but the list is here so the addition is a decision rather than a diff nobody read.")
}
```

- [ ] **Step 9: Run the package and commit.**

Run: `go test ./internal/netlimit/ ./cmd/relay-server/... -timeout 120s`
Expected: PASS.

```bash
git add internal/netlimit/listener.go internal/netlimit/listener_test.go
git commit -m "feat(netlimit): report live occupancy in one critical section alongside refusal counts"
```

---

## Task 3: the reporter decides from counts only

**Files:**
- Modify: `cmd/relay-server/grpc_config.go:432-444`
- Modify: `cmd/relay-server/grpc_config_test.go` (one new test, appended)

- [ ] **Step 1: Write the failing test.** Append to `cmd/relay-server/grpc_config_test.go`:

```go
// TestRefusalSummaryIsSilentWhenOnlyOccupancyMoves is the discriminating test for
// the trap idea-2026-08-21-netlimit-occupancy-is-unobservable identified before
// any code was written: occupancy changes on essentially every connection, so a
// reporter that consulted it to decide whether to speak would emit a line every
// single interval forever - permanently destroying the "one line per interval,
// and only when something moved" property that
// TestRefusalSummaryLogsOnlyWhenCountersMove exists to protect.
//
// The counts are held STATIC across every tick here. The only thing moving is
// the half that must never be consulted.
func TestRefusalSummaryIsSilentWhenOnlyOccupancyMoves(t *testing.T) {
	lines := 0
	r := &refusalReporter{logf: func(string, ...any) { lines++ }}

	counts := netlimit.RefusalCounts{RefusedTotal: 7, RefusedPerIP: 2}
	r.tick(netlimit.Stats{
		Counts: counts,
		Levels: netlimit.Occupancy{LiveTotal: 10, DistinctSources: 3, MaxPerSource: 5},
	})
	require.Equal(t, 1, lines, "the first tick after a counter moved must speak")

	for i, lv := range []netlimit.Occupancy{
		{LiveTotal: 900, DistinctSources: 16, MaxPerSource: 64},
		{LiveTotal: 1, DistinctSources: 1, MaxPerSource: 1},
		{LiveTotal: 1024, DistinctSources: 1024, MaxPerSource: 1},
		{},
	} {
		r.tick(netlimit.Stats{Counts: counts, Levels: lv})
		require.Equal(t, 1, lines,
			"occupancy move %d produced a line. A level must never take part in the 'did anything move' "+
				"test: on a live fleet it moves constantly, so this reporter would speak every interval "+
				"forever and the bound would be a bound in name only.", i)
	}
}
```

- [ ] **Step 2: Run it and watch it fail behaviourally.**

Run: `go test ./cmd/relay-server/ -run TestRefusalSummaryIsSilentWhenOnlyOccupancyMoves -v -timeout 60s`
Expected: **FAIL** at occupancy move 0 with `expected: 1, actual: 2`. This is a genuine behavioural RED: `tick` compiles fine and does the wrong thing.

- [ ] **Step 3: Make the trigger structural.** In `cmd/relay-server/grpc_config.go`, replace the `refusalReporter` type and `tick`:

```go
// refusalReporter turns netlimit's counters into at most one log line per
// interval. A line per refusal is deliberately NOT an option: it would be a new
// unbounded attacker-driven log site inside the control that bounds
// attacker-driven log volume. The line names counts and levels, never addresses,
// so no caller-supplied byte can reach the log through it.
//
// THE TRIGGER READS MONOTONIC COUNTS ONLY, AND THE TYPE IS WHAT ENFORCES IT.
// last is a netlimit.RefusalCounts rather than a netlimit.Stats, so `s == r.last`
// no longer compiles and a level cannot be dragged into the "did anything move"
// test by somebody adding a field. Levels are CARRIED in the line when it speaks
// and never consulted to decide whether it speaks.
//
// THE HONEST RESIDUAL, because it is the price of that rule: a fleet parked at
// exactly the ceiling with no further connection attempts produces no line at
// all, so "the pressure ended" and "we settled at the ceiling" still look alike
// IN THE LOG. That is closed by GET /v1/server/counters, which reports the level
// on demand at any time, and it is the main reason the endpoint is the primary
// surface. Note the ambiguity only persists while nobody is being refused: the
// instant a legitimate agent is turned away, the counts move, this speaks, and
// the line carries the occupancy that explains why.
//
// tick is separate from runRefusalReporter so the "only when counters move"
// property can be driven directly, with no timer and no sleeping.
type refusalReporter struct {
	last netlimit.RefusalCounts
	logf func(format string, args ...any)
}

func (r *refusalReporter) tick(s netlimit.Stats) {
	if s.Counts == r.last {
		return
	}
	r.logf("gRPC admission: %d connection(s) refused over the total cap and %d over the per-source-IP cap since startup",
		s.Counts.RefusedTotal, s.Counts.RefusedPerIP)
	r.last = s.Counts
}
```

- [ ] **Step 4: Run both reporter tests.**

Run: `go test ./cmd/relay-server/ -run TestRefusalSummary -v -timeout 60s`
Expected: PASS, both `TestRefusalSummaryLogsOnlyWhenCountersMove` (unchanged since Task 1) and the new one.

- [ ] **Step 5: Commit.**

```bash
git add cmd/relay-server/grpc_config.go cmd/relay-server/grpc_config_test.go
git commit -m "fix(relay-server): refusal reporter speaks on monotonic counts only, never on levels"
```

---

## Task 4: the reporter's line carries occupancy when it speaks

**Files:**
- Modify: `cmd/relay-server/grpc_config.go` (`tick`'s log call)
- Modify: `cmd/relay-server/grpc_config_test.go` (one new test, appended)

- [ ] **Step 1: Write the failing test.** Append to `cmd/relay-server/grpc_config_test.go`:

```go
// TestRefusalSummaryLineCarriesOccupancyWhenItSpeaks. The trigger reads counts
// only; the LINE must still answer "how full is it, and is this one source or
// many", or the operator gets a refusal count with no context and has to go
// looking for the endpoint to interpret it.
//
// FIVE DISTINCT VALUES, IN A FIXED ORDER. Equal values would make a crossed
// argument invisible, which is half of what this test is for.
func TestRefusalSummaryLineCarriesOccupancyWhenItSpeaks(t *testing.T) {
	var format string
	var args []any
	r := &refusalReporter{logf: func(f string, a ...any) { format, args = f, a }}

	r.tick(netlimit.Stats{
		Counts: netlimit.RefusalCounts{RefusedTotal: 7, RefusedPerIP: 2},
		Levels: netlimit.Occupancy{LiveTotal: 1024, DistinctSources: 16, MaxPerSource: 64},
	})

	require.Len(t, args, 5,
		"the line must carry both refusal counts AND all three occupancy figures: MaxPerSource with "+
			"DistinctSources is what separates a distributed source pattern from a NAT gateway, and "+
			"RefusedTotal alone cannot")
	assert.Equal(t, []any{uint64(7), uint64(2), uint64(1024), uint64(16), uint64(64)}, args,
		"the arguments must be counts-then-levels in that order; five distinct values make a crossed "+
			"argument visible")
	for i, a := range args {
		assert.IsType(t, uint64(0), a,
			"argument %d is not a uint64. Every argument of this line must be a count or a level - a "+
				"caller-supplied byte here would make an attacker-reachable log site out of the control "+
				"that bounds attacker-driven log volume.", i)
	}
	assert.Equal(t, 5, strings.Count(format, "%d"), "the template must consume all five numbers")

	rendered := fmt.Sprintf(format, args...)
	assert.Contains(t, rendered, "1024")
	assert.Contains(t, rendered, "16")
	assert.Contains(t, rendered, "64")
}
```

- [ ] **Step 2: Run it and watch it fail.**

Run: `go test ./cmd/relay-server/ -run TestRefusalSummaryLineCarriesOccupancy -v -timeout 60s`
Expected: **FAIL** on `require.Len(args, 5)` with actual 2. Behavioural, not a compile error.

- [ ] **Step 3: Extend the line.** In `tick`, replace the `r.logf(...)` call with:

```go
	r.logf("gRPC admission: %d connection(s) refused over the total cap and %d over the per-source-IP cap "+
		"since startup; %d live connection(s) held from %d source(s), busiest source holds %d",
		s.Counts.RefusedTotal, s.Counts.RefusedPerIP,
		s.Levels.LiveTotal, s.Levels.DistinctSources, s.Levels.MaxPerSource)
```

- [ ] **Step 4: Run every test in the package.**

Run: `go test ./cmd/relay-server/... -timeout 120s`
Expected: PASS. In particular `TestRefusalSummaryLogsOnlyWhenCountersMove` must still pass **with its assertions unchanged** - its `IsType(uint64(0))` loop now covers five arguments instead of two, which is the point. If it goes RED, an occupancy field is not a `uint64`: fix the type, never the assertion.

- [ ] **Step 5: Commit.**

```bash
git add cmd/relay-server/grpc_config.go cmd/relay-server/grpc_config_test.go
git commit -m "feat(relay-server): refusal summary carries live occupancy when it speaks"
```

---

## Task 5: `GET /v1/server/counters`

**Files:**
- Create: `internal/api/server_counters.go`
- Create: `internal/api/server_counters_test.go`
- Modify: `internal/api/server.go` (two `Server` fields, `New`, one route)

**On the RED.** Same recipe as Task 2: Step 1 lands the endpoint with **no section wired**, which is a coherent, committable state and is what production would report if `main.go` never assigned `Counters`. The payload test in Step 3 then fails on a missing section rather than on a missing symbol.

- [ ] **Step 1: Create `internal/api/server_counters.go`.**

```go
package api

import (
	"net/http"
	"time"

	"relay/internal/netlimit"
)

// GET /v1/server/counters is relay's ONE process-lifetime counter surface. It
// exists because relay ships several controls that stop something bad quietly -
// a connection cap that refuses, a fence that drops a forged log chunk, a
// limiter that suppresses a repeating line, a watchdog that ends an assignment -
// and the operator-visible signature of an attack against a silent control is
// FEWER log lines than normal, which is indistinguishable from a healthy fleet.
// See docs/superpowers/specs/2026-08-21-silent-drop-observability.md.
//
// THE CONTRACT, fixed for all four sections before the first one shipped so that
// no later slice reshapes a payload that is already in the wild:
//
//   - "counts" are MONOTONIC since started_at. "levels" are CURRENT. A reporter
//     may consult counts to decide whether to speak and may NEVER consult
//     levels: a level moves constantly, so a reporter that compared one would
//     speak every interval forever.
//   - An unwired section is ABSENT, never zero-valued. A section of zeros means
//     "this control ran and stopped nothing"; an absent section means "this
//     build or this replica does not have that control wired". Collapsing the
//     two reintroduces the very defect this payload exists to fix, inside the
//     payload.
//   - started_at is ALWAYS present, including when every section is absent. A
//     restart zeroes everything, so "the counter stopped moving" and "the
//     process restarted" are otherwise identical.
//   - PER REPLICA, per process, best effort, zeroed by a restart. Counts from
//     two replicas may be added; levels may NOT (max_per_source in particular
//     does not sum into anything meaningful). No persistence, no history, no
//     rates, no alerting - a poller derives rates itself.
//   - NO FIELD ANYWHERE CARRIES A CALLER-SUPPLIED BYTE. The only non-integer
//     values in the whole payload are started_at and, when slice 4 lands, the
//     server-resolved worker UUIDs keying watchdog.counts.swept_by_worker.
//     TestCounterPayloadCarriesNoIdentifiers enforces that as an ALLOW-LIST, so
//     any third one goes RED and forces an argument. Worker UUIDs are admissible
//     HERE and remain inadmissible in any log line reachable from the gRPC recv
//     path, and those are two different arguments: this route is
//     admin-authenticated, so it is not an attacker-writable site.
//
// HOW A FUTURE SECTION ATTACHES ITSELF, because the answer is NOT the same for
// every package and getting it wrong shows up as an import cycle:
//
//   - internal/netlimit is a stdlib-only leaf, so this package imports it and
//     the source interface can return netlimit.Stats directly.
//   - internal/worker is already imported by this package (server.go), so a
//     worker-side counters type works the same way.
//   - internal/scheduler IMPORTS THIS PACKAGE (scheduler/dispatch.go), so this
//     package can never import it. The watchdog section must therefore declare
//     its snapshot type HERE, next to the response types, and scheduler.Watchdog
//     returns that type. CounterSources is a struct of independent fields
//     precisely so each section can make that choice separately.

// GRPCAdmissionSource is whatever can report the agent-port admission
// counters - in production, *netlimit.Listener.
type GRPCAdmissionSource interface {
	Stats() netlimit.Stats
}

// CounterSources is the set of subsystem counter sources the endpoint
// assembles. Every field is nil-able and nil means the section is ABSENT from
// the payload, not zero. cmd/relay-server sets this after construction, in the
// established shape of Server.Metrics.
type CounterSources struct {
	GRPCAdmission GRPCAdmissionSource
}

type serverCountersResponse struct {
	StartedAt     time.Time             `json:"started_at"`
	GRPCAdmission *grpcAdmissionSection `json:"grpc_admission,omitempty"`
}

type grpcAdmissionSection struct {
	Counts grpcAdmissionCounts `json:"counts"`
	Levels grpcAdmissionLevels `json:"levels"`
}

type grpcAdmissionCounts struct {
	RefusedTotal uint64 `json:"refused_total"`
	// refused_per_source, not refused_per_ip: the cap is keyed on a SOURCE,
	// which is an exact IPv4 address but an aggregated /64 for IPv6. It also
	// under-reports whenever the fleet cap is saturated, because the total is
	// checked first - read it as a floor when live_total has reached the
	// configured maximum.
	RefusedPerSource uint64 `json:"refused_per_source"`
}

type grpcAdmissionLevels struct {
	LiveTotal       uint64 `json:"live_total"`
	DistinctSources uint64 `json:"distinct_sources"`
	MaxPerSource    uint64 `json:"max_per_source"`
}

// handleServerCounters assembles whichever sections are wired. It reads no
// request body, so readJSON is not involved; the response goes through
// writeJSON, matching handleGetWorkerMetrics.
func (s *Server) handleServerCounters(w http.ResponseWriter, r *http.Request) {
	resp := serverCountersResponse{StartedAt: s.startedAt}
	if src := s.Counters.GRPCAdmission; src != nil {
		st := src.Stats()
		resp.GRPCAdmission = &grpcAdmissionSection{
			Counts: grpcAdmissionCounts{
				RefusedTotal:     st.Counts.RefusedTotal,
				RefusedPerSource: st.Counts.RefusedPerIP,
			},
			Levels: grpcAdmissionLevels{
				LiveTotal:       st.Levels.LiveTotal,
				DistinctSources: st.Levels.DistinctSources,
				MaxPerSource:    st.Levels.MaxPerSource,
			},
		}
	}
	writeJSON(w, http.StatusOK, resp)
}
```

- [ ] **Step 2: Wire the fields and the route in `internal/api/server.go`.**

Add to the `Server` struct, immediately after the `Metrics` field (line 38):

```go
	// Counters, when its fields are non-nil, supplies process-lifetime counters
	// for GET /v1/server/counters. Set by cmd/relay-server after construction.
	// A nil field means the section is ABSENT from the payload, not zero.
	Counters CounterSources

	// startedAt is when this server object was constructed, i.e. process start.
	// It is in the payload because a restart zeroes every counter, so a stalled
	// counter and a restart are otherwise identical.
	startedAt time.Time
```

In `New`, add `startedAt: time.Now().UTC(),` to the returned literal.

Add the route after the Workers block (after line 138):

```go
	// Server-wide counters (admin-only). NOT auth-only like /v1/workers/stats:
	// that is a database census of the fleet, while these are process-lifetime
	// in-memory numbers describing adversary activity and internal control
	// state.
	mux.Handle("GET /v1/server/counters", auth(admin(http.HandlerFunc(s.handleServerCounters))))
```

- [ ] **Step 3: Write the endpoint unit tests.** Create `internal/api/server_counters_test.go`:

```go
package api

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"relay/internal/netlimit"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeAdmissionSource returns a fixed snapshot. FIVE DISTINCT VALUES: equal
// values would hide a crossed field assignment in the mapping, which is the
// mutation TestServerCounters_ReportsTheNetlimitSnapshot exists to kill.
type fakeAdmissionSource struct{ s netlimit.Stats }

func (f fakeAdmissionSource) Stats() netlimit.Stats { return f.s }

func testStartedAt() time.Time { return time.Date(2026, 8, 21, 9, 0, 0, 0, time.UTC) }

// counterKeys returns the key set of a decoded JSON object.
func counterKeys(m map[string]json.RawMessage) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func mapKeys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func TestServerCounters_RequiresAuth(t *testing.T) {
	s := &Server{}
	req := httptest.NewRequest("GET", "/v1/server/counters", nil) // no Authorization header
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	require.Equal(t, http.StatusUnauthorized, rec.Code,
		"the counters route must sit behind BearerAuth. These numbers describe adversary activity and "+
			"internal control state; /v1/config and /v1/health are the public routes and this is not one.")
}

func TestServerCounters_OmitsUnwiredSections(t *testing.T) {
	s := &Server{startedAt: testStartedAt()}
	rec := httptest.NewRecorder()
	s.handleServerCounters(rec, httptest.NewRequest("GET", "/v1/server/counters", nil))
	require.Equal(t, http.StatusOK, rec.Code)

	// The discriminating assertion is on KEY ABSENCE in the raw JSON. A decoded
	// zero value cannot tell "this control ran and stopped nothing" from "this
	// control is not wired on this build", and those need opposite responses -
	// which is this endpoint's own subject, one layer up.
	var top map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &top))
	assert.ElementsMatch(t, []string{"started_at"}, counterKeys(top),
		"an unwired section must be ABSENT from the payload, never present-and-zero")
	assert.Contains(t, rec.Body.String(), `"started_at":"2026-08-21T09:00:00Z"`,
		"started_at is present even when every section is absent: a restart zeroes every counter, so a "+
			"stalled counter and a restart are otherwise the same payload")
}

func TestServerCounters_ReportsTheNetlimitSnapshot(t *testing.T) {
	s := &Server{
		startedAt: testStartedAt(),
		Counters: CounterSources{GRPCAdmission: fakeAdmissionSource{s: netlimit.Stats{
			Counts: netlimit.RefusalCounts{RefusedTotal: 11, RefusedPerIP: 22},
			Levels: netlimit.Occupancy{LiveTotal: 33, DistinctSources: 44, MaxPerSource: 55},
		}}},
	}
	rec := httptest.NewRecorder()
	s.handleServerCounters(rec, httptest.NewRequest("GET", "/v1/server/counters", nil))
	require.Equal(t, http.StatusOK, rec.Code)

	var body struct {
		StartedAt     string `json:"started_at"`
		GRPCAdmission *struct {
			Counts map[string]any `json:"counts"`
			Levels map[string]any `json:"levels"`
		} `json:"grpc_admission"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.NotNil(t, body.GRPCAdmission, "a wired section must be present")

	// Key-set equality, not per-key assertions alone: a RENAMED key would
	// otherwise decode as a missing value and a per-key check would report zero.
	assert.ElementsMatch(t, []string{"refused_total", "refused_per_source"}, mapKeys(body.GRPCAdmission.Counts))
	assert.ElementsMatch(t, []string{"live_total", "distinct_sources", "max_per_source"}, mapKeys(body.GRPCAdmission.Levels))

	assert.Equal(t, float64(11), body.GRPCAdmission.Counts["refused_total"])
	assert.Equal(t, float64(22), body.GRPCAdmission.Counts["refused_per_source"])
	assert.Equal(t, float64(33), body.GRPCAdmission.Levels["live_total"])
	assert.Equal(t, float64(44), body.GRPCAdmission.Levels["distinct_sources"])
	assert.Equal(t, float64(55), body.GRPCAdmission.Levels["max_per_source"])
	assert.Equal(t, "2026-08-21T09:00:00Z", body.StartedAt)
}

// TestServerCountersRouteIsAdminGated.
//
// THE BEHAVIOURAL PROOF OF THE ADMIN HALF NEEDS A DATABASE - BearerAuth resolves
// a token against Postgres - and CI runs `go test -race ./...` with no
// integration tag and no container (.github/workflows/go-ci.yml). So the
// integration test next door (server_counters_integration_test.go) proves 403 vs
// 200 for real, and THIS guard is what keeps the default gate able to see
// `auth(...)` silently losing its `admin(...)`.
//
// go/ast, not a regex: a source-scanning regex guard in this repo was proven
// breakable by a single stray comment. Written from the PROPERTY - "the counters
// pattern is registered exactly once, with Handle, wrapped in bearer-auth
// outermost and AdminOnly inside" - and then searched for other spellings:
// HandleFunc (no auth at all), a non-literal pattern, a locally-defined `admin`
// that is not AdminOnly, and admin(auth(...)) in the wrong order, which 403s
// every caller including admins because AdminOnly reads the user that BearerAuth
// puts in the context.
func TestServerCountersRouteIsAdminGated(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "server.go", nil, 0)
	require.NoError(t, err)

	// Resolve the two middleware locals from their RIGHT-HAND SIDES. A name-only
	// check would be satisfied by `admin := func(h http.Handler) http.Handler { return h }`.
	authName, adminName := "", ""
	ast.Inspect(file, func(n ast.Node) bool {
		as, ok := n.(*ast.AssignStmt)
		if !ok || len(as.Lhs) != 1 || len(as.Rhs) != 1 {
			return true
		}
		id, ok := as.Lhs[0].(*ast.Ident)
		if !ok {
			return true
		}
		switch rhs := as.Rhs[0].(type) {
		case *ast.CallExpr:
			if fn, ok := rhs.Fun.(*ast.Ident); ok && fn.Name == "BearerAuth" {
				authName = id.Name
			}
		case *ast.Ident:
			if rhs.Name == "AdminOnly" {
				adminName = id.Name
			}
		}
		return true
	})
	require.NotEmpty(t, authName, "server.go no longer binds BearerAuth(...) to a local in Handler()")
	require.NotEmpty(t, adminName, "server.go no longer binds AdminOnly to a local in Handler()")

	const route = "GET /v1/server/counters"
	var regs []*ast.CallExpr
	ast.Inspect(file, func(n ast.Node) bool {
		ce, ok := n.(*ast.CallExpr)
		if !ok || len(ce.Args) == 0 {
			return true
		}
		sel, ok := ce.Fun.(*ast.SelectorExpr)
		if !ok || (sel.Sel.Name != "Handle" && sel.Sel.Name != "HandleFunc") {
			return true
		}
		lit, ok := ce.Args[0].(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return true
		}
		if v, err := strconv.Unquote(lit.Value); err == nil && v == route {
			regs = append(regs, ce)
		}
		return true
	})
	require.Len(t, regs, 1,
		"%q must be registered exactly once, with the pattern spelled as a string literal at the "+
			"registration site. A pattern built from a constant or a variable leaves this guard unable to "+
			"see the route at all.", route)

	sel := regs[0].Fun.(*ast.SelectorExpr)
	require.Equal(t, "Handle", sel.Sel.Name,
		"the counters route must be registered with mux.Handle(pattern, auth(admin(...))). mux.HandleFunc "+
			"takes a bare handler, which is this route with no authentication whatsoever.")
	require.Len(t, regs[0].Args, 2)

	outer, ok := regs[0].Args[1].(*ast.CallExpr)
	require.True(t, ok, "the counters handler is registered unwrapped: no auth, no admin")
	outerName := routeIdentName(outer.Fun)
	require.Contains(t, []string{authName, "BearerAuth"}, outerName,
		"the OUTERMOST wrapper on the counters route must be the bearer-auth middleware, got %q. AdminOnly "+
			"reads the user out of the request context and BearerAuth is what puts it there, so "+
			"admin(auth(...)) returns 403 to every caller including admins.", outerName)
	require.Len(t, outer.Args, 1)

	inner, ok := outer.Args[0].(*ast.CallExpr)
	require.True(t, ok,
		"the counters route is wrapped in %s(...) alone. Every authenticated user would then be able to "+
			"read numbers that describe adversary activity and internal control state.", outerName)
	innerName := routeIdentName(inner.Fun)
	require.Contains(t, []string{adminName, "AdminOnly"}, innerName,
		"the counters route must be wrapped in AdminOnly inside the bearer-auth middleware, got %q", innerName)
}

func routeIdentName(e ast.Expr) string {
	switch v := e.(type) {
	case *ast.Ident:
		return v.Name
	case *ast.SelectorExpr:
		return v.Sel.Name
	}
	return ""
}
```

- [ ] **Step 4: Run them.**

Run: `go test ./internal/api/ -run TestServerCounters -v -timeout 60s`
Expected: PASS for all four. (`TestServerCounters_ReportsTheNetlimitSnapshot` was RED before Step 1's mapping existed; if you are re-running the plan out of order and want to see that RED, delete the body of the `if src := ...` block and re-run - it fails with "a wired section must be present".)

Run: `go test ./internal/api/ -timeout 120s`
Expected: PASS, whole package.

- [ ] **Step 5: Commit.**

```bash
git add internal/api/server_counters.go internal/api/server_counters_test.go internal/api/server.go
git commit -m "feat(api): add admin-only GET /v1/server/counters with the gRPC admission section"
```

---

## Task 6: the payload identifier allow-list guard

**Files:**
- Modify: `internal/api/server_counters_test.go` (one new test)

- [ ] **Step 1: Write the guard.** Append to `internal/api/server_counters_test.go` (add `"reflect"` and `"strings"` to the imports):

```go
// TestCounterPayloadCarriesNoIdentifiers is the cardinality rule of the
// silent-drop spec, shipped in slice 1 as a payload guard so that slices 2, 3
// and 4 inherit it rather than re-argue it.
//
// WRITTEN AS AN ALLOW-LIST, NEVER AS A DENY-LIST. The two are interchangeable
// against today's payload, but a deny-list fails OPEN on the next field somebody
// adds and an allow-list fails closed. The allow-list already names slice 4's
// swept_by_worker map, so slice 4 adds a field rather than an exception, and any
// OTHER new non-integer field goes RED and forces the argument.
//
// The walk is written from the PROPERTY - every leaf of the response tree is an
// unsigned integer - and searched against the shape: pointers are dereferenced,
// structs are recursed into, and maps, slices, strings, signed integers, floats,
// bools and interfaces all fail. The exact leaf-path assertion is what makes it
// non-vacuous: a walk that visited nothing would satisfy the type rule
// trivially.
func TestCounterPayloadCarriesNoIdentifiers(t *testing.T) {
	// path -> why it is allowed. Entries need not exist yet: the slice-4 field
	// is named here deliberately so that slice ADDS a field rather than an
	// exception. That means an entry can go stale, which is a cost accepted in
	// exchange for the contract being decided once.
	allowed := map[string]string{
		"started_at": "a timestamp, server-generated, and the one field that makes a zeroed counter " +
			"distinguishable from a restart",
		"watchdog.counts.swept_by_worker": "server-resolved worker UUIDs from a row the coordinator's own " +
			"scan returned, never a caller-supplied byte. Admissible HERE because this route is " +
			"admin-authenticated; still inadmissible in any log line reachable from the gRPC recv path.",
	}

	var leaves []string
	var walk func(typ reflect.Type, path string)
	walk = func(typ reflect.Type, path string) {
		for typ.Kind() == reflect.Pointer {
			typ = typ.Elem()
		}
		require.Equal(t, reflect.Struct, typ.Kind(), "%s is not a struct", path)
		for i := 0; i < typ.NumField(); i++ {
			f := typ.Field(i)
			name := f.Tag.Get("json")
			if c := strings.Index(name, ","); c >= 0 {
				name = name[:c]
			}
			if name == "" {
				name = f.Name
			}
			p := name
			if path != "" {
				p = path + "." + name
			}
			// The allow-list is consulted BEFORE the kind switch, and that
			// ordering is load-bearing: time.Time is a struct whose own fields
			// would otherwise be walked into.
			if _, ok := allowed[p]; ok {
				leaves = append(leaves, p)
				continue
			}
			ft := f.Type
			for ft.Kind() == reflect.Pointer {
				ft = ft.Elem()
			}
			switch ft.Kind() {
			case reflect.Struct:
				walk(ft, p)
			case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
				leaves = append(leaves, p)
			default:
				t.Fatalf("counter payload field %q is a %s. Every field in this payload must be an "+
					"UNSIGNED INTEGER unless it is on the allow-list at the top of this test. A string, a "+
					"map key, a slice element or a signed value is where a caller-supplied byte or an "+
					"unbounded cardinality gets in - this repo has already shipped an attacker-keyed "+
					"counter once. If the field is genuinely necessary, add it to the allow-list WITH the "+
					"argument, in the same commit.", p, ft.Kind())
			}
		}
	}
	walk(reflect.TypeOf(serverCountersResponse{}), "")

	assert.ElementsMatch(t, []string{
		"started_at",
		"grpc_admission.counts.refused_total",
		"grpc_admission.counts.refused_per_source",
		"grpc_admission.levels.live_total",
		"grpc_admission.levels.distinct_sources",
		"grpc_admission.levels.max_per_source",
	}, leaves,
		"the counter payload's field set changed. This list is the response CONTRACT: update it in the "+
			"same commit as the field, so that adding a section is a decision rather than a diff nobody read.")
}
```

- [ ] **Step 2: Run it.**

Run: `go test ./internal/api/ -run TestCounterPayloadCarriesNoIdentifiers -v -timeout 60s`
Expected: PASS.

- [ ] **Step 3: Prove it is not decorative - run five shaped mutations now, not at the end.** For each row, apply the edit, run the test, confirm RED, then `git checkout -- internal/api/server_counters.go`:

1. `SourcePrefix string` with tag `json:"source_prefix"` added to `grpcAdmissionLevels` (a string at depth 3).
2. `PerSource map[string]uint64` with tag `json:"per_source"` added to `grpcAdmissionCounts` (a map).
3. `Recent []uint64` with tag `json:"recent"` added to `grpcAdmissionLevels` (a slice).
4. `Drift int64` with tag `json:"drift"` added to `grpcAdmissionLevels` (a SIGNED integer).
5. `Extra *grpcAdmissionExtra` with tag `json:"extra,omitempty"` added to `serverCountersResponse`, plus `type grpcAdmissionExtra struct { Note string }` (a pointer to a struct containing a string - proves the pointer deref and the recursion).

Then run row 1 **again with its path added to the `ElementsMatch` list**. Expected: still RED, on the type rule alone. That is what proves the two assertions are independently load-bearing rather than one masking the other.

Record the five results (and the sixth) in the PR body.

- [ ] **Step 4: Commit.**

```bash
git add internal/api/server_counters_test.go
git commit -m "test(api): allow-list guard keeping identifiers and unbounded keys out of the counters payload"
```

---

## Task 7: `main.go` wires the listener, and a structural guard says so

**Files:**
- Create: `cmd/relay-server/counters_wiring_test.go`
- Modify: `cmd/relay-server/main.go` (one line, after line 210)

- [ ] **Step 1: Write the failing guard.** Create `cmd/relay-server/counters_wiring_test.go`:

```go
package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestServerCountersIsWiredByMain is a structural guard in the same spirit as
// TestGRPCAdmissionIsWiredByMain. Deleting `httpServer.Counters = ...` from
// main.go compiles and leaves `go test ./...` entirely green: internal/api keeps
// its own passing tests with a fake source, internal/netlimit keeps its own, and
// the endpoint permanently answers 200 with started_at and nothing else - which
// reads exactly like a server whose admission control has never refused
// anything. That is the whole bug.
//
// HEED THE RECORDED LESSON THAT A GUARD BUILT FROM A MUTATION KILL INHERITS THE
// MUTATION'S SHAPE, NOT THE DEFECT'S. The admission slice's three structural
// guards were each evaded on the first attempt - by an append, by an import
// alias, and by moving a field assignment to the next line. This one is written
// from the property "the value that reaches the SERVED api.Server's Counters
// field is a CounterSources literal whose GRPCAdmission derives from
// netlimit.Wrap", and it was attacked with the five evasions listed in the
// plan's Task 7 Step 5 before it was called done.
func TestServerCountersIsWiredByMain(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "main.go", nil, 0)
	require.NoError(t, err)

	// name -> identifiers its RHS mentions, so the walk can follow
	// `grpcLis := netlimit.Wrap(...)`.
	from := map[string][]string{}
	type selAssign struct {
		lhs string
		rhs ast.Expr
	}
	var sels []selAssign
	ast.Inspect(file, func(n ast.Node) bool {
		as, ok := n.(*ast.AssignStmt)
		if !ok {
			return true
		}
		for i, l := range as.Lhs {
			var rhs ast.Expr
			if len(as.Lhs) == len(as.Rhs) {
				rhs = as.Rhs[i]
			}
			if id, ok := l.(*ast.Ident); ok {
				if rhs != nil {
					ast.Inspect(rhs, func(m ast.Node) bool {
						if x, ok := m.(*ast.Ident); ok {
							from[id.Name] = append(from[id.Name], x.Name)
						}
						return true
					})
				}
				continue
			}
			// A SELECTOR on the left. Keyed by the rendered path, so that
			// `httpServer.Counters.GRPCAdmission = nil` on the next line is
			// visible as a mutation rather than invisible as "not an *ast.Ident".
			sels = append(sels, selAssign{lhs: exprString(l), rhs: rhs})
		}
		return true
	})

	reaches := func(seed, target string) bool {
		seen := map[string]bool{}
		queue := []string{seed}
		for len(queue) > 0 {
			name := queue[0]
			queue = queue[1:]
			if seen[name] {
				continue
			}
			seen[name] = true
			if name == target {
				return true
			}
			queue = append(queue, from[name]...)
		}
		return false
	}

	var target *selAssign
	var mutations []string
	for i := range sels {
		switch {
		case strings.HasSuffix(sels[i].lhs, ".Counters"):
			require.Nil(t, target,
				"main.go assigns to .Counters more than once. The last write decides and this guard cannot "+
					"say which one it is.")
			target = &sels[i]
		case strings.Contains(sels[i].lhs, ".Counters."):
			mutations = append(mutations, sels[i].lhs)
		}
	}
	require.NotNil(t, target,
		"main.go never assigns <httpServer>.Counters = api.CounterSources{...}. Deleting that one line "+
			"compiles, leaves every package green, and makes GET /v1/server/counters report an empty "+
			"payload forever - indistinguishable from a control that has stopped nothing.")
	require.Empty(t, mutations,
		"main.go mutates the counter sources after building them (%v). `httpServer.Counters.GRPCAdmission "+
			"= nil` on the following line compiles and silently unwires the section: this is the exact "+
			"evasion that beat the netlimit.Config guard one slice ago.", mutations)

	base := strings.TrimSuffix(target.lhs, ".Counters")
	cl, ok := target.rhs.(*ast.CompositeLit)
	require.True(t, ok,
		"the value assigned to %s must be an api.CounterSources composite literal AT THE ASSIGNMENT SITE. "+
			"Building it into a named variable first re-opens the hole: `cs := api.CounterSources{...}; "+
			"cs.GRPCAdmission = nil; %s = cs` compiles and every other check here still passes.",
		target.lhs, target.lhs)
	require.Equal(t, "api.CounterSources", exprString(cl.Type))

	var src ast.Expr
	for _, e := range cl.Elts {
		kv, ok := e.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		if k, ok := kv.Key.(*ast.Ident); ok && k.Name == "GRPCAdmission" {
			src = kv.Value
		}
	}
	require.NotNil(t, src,
		"api.CounterSources is built with no GRPCAdmission field, so the only wired section is absent and "+
			"the endpoint reports an empty payload")
	srcIdent, ok := src.(*ast.Ident)
	require.True(t, ok,
		"GRPCAdmission must be fed a plain identifier - the netlimit listener - not %s. A nil or a stub "+
			"defined in this file compiles and reports zeros forever.", exprString(src))
	require.True(t, reaches(srcIdent.Name, "Wrap"),
		"GRPCAdmission is fed %q, which does not derive from netlimit.Wrap: the endpoint would report a "+
			"listener that is not the one serving the agent port.", srcIdent.Name)

	// The api.Server that received the sources must be the one whose Handler()
	// is served. Wiring a DIFFERENT api.Server value compiles and satisfies
	// every check above.
	handlerBase := ""
	ast.Inspect(file, func(n ast.Node) bool {
		kv, ok := n.(*ast.KeyValueExpr)
		if !ok {
			return true
		}
		k, ok := kv.Key.(*ast.Ident)
		if !ok || k.Name != "Handler" {
			return true
		}
		if ce, ok := kv.Value.(*ast.CallExpr); ok {
			if s, ok := ce.Fun.(*ast.SelectorExpr); ok && s.Sel.Name == "Handler" {
				handlerBase = exprString(s.X)
			}
		}
		return true
	})
	require.NotEmpty(t, handlerBase, "main.go no longer builds the http.Server from <server>.Handler()")
	require.Equal(t, handlerBase, base,
		"main.go wires the counter sources into %q but serves %q.Handler(). The endpoint would report an "+
			"empty payload with every check above satisfied.", base, handlerBase)
}
```

`exprString` already exists in this package (`cmd/relay-server/grpc_config_test.go:767`); do not redeclare it.

- [ ] **Step 2: Run it and watch it fail.**

Run: `go test ./cmd/relay-server/ -run TestServerCountersIsWiredByMain -v -timeout 60s`
Expected: **FAIL** with "main.go never assigns <httpServer>.Counters". Genuine: nothing is missing at compile time, the wiring is simply absent.

- [ ] **Step 3: Add the wiring line.** In `cmd/relay-server/main.go`, immediately after the `grpcLis := netlimit.Wrap(...)` block (which ends at line 210) and before `go runRefusalReporter(...)`:

```go
	// The counters endpoint reads the listener's snapshot on demand. This
	// assignment cannot sit next to httpServer.Metrics above - grpcLis does not
	// exist yet at that point - and must come before httpServer.Handler() is
	// built below. A nil field here means the section is ABSENT from the
	// payload, never zero.
	httpServer.Counters = api.CounterSources{GRPCAdmission: grpcLis}
```

- [ ] **Step 4: Run the package.**

Run: `go test ./cmd/relay-server/... -timeout 120s`
Expected: PASS, including `TestGRPCAdmissionIsWiredByMain`, which is unaffected (its "no field assignment on the bounds identifier" walk compares against `grpcBnds`, and `httpServer` is not it).

- [ ] **Step 5: Attempt the evasions and record the hit count.** Apply each, run `go test ./cmd/relay-server/ -run TestServerCountersIsWiredByMain`, confirm RED, then `git checkout -- cmd/relay-server/main.go`:

1. Delete the assignment entirely.
2. `httpServer.Counters = api.CounterSources{}`.
3. `httpServer.Counters = api.CounterSources{GRPCAdmission: nil}`.
4. Keep the assignment and add `httpServer.Counters.GRPCAdmission = nil` on the next line.
5. `cs := api.CounterSources{GRPCAdmission: grpcLis}` / `cs.GRPCAdmission = nil` / `httpServer.Counters = cs`.

Also note in the PR body the one evasion the **type system** blocks, so it is not on the list: `GRPCAdmission: grpcRawLis` does not compile, because the raw `net.Listener` has no `Stats()`.

- [ ] **Step 6: Commit.**

```bash
git add cmd/relay-server/main.go cmd/relay-server/counters_wiring_test.go
git commit -m "feat(relay-server): wire the netlimit listener into GET /v1/server/counters"
```

---

## Task 8: the real admin gate, through real auth

**Files:**
- Create: `internal/api/server_counters_integration_test.go`

This is the only place the 403 is proved behaviourally, because `BearerAuth` resolves a token against Postgres. Follow `internal/api/invites_list_integration_test.go:59-80` exactly; the helpers (`newTestServer`, `createTestUser`, `createTestToken`) already exist in that package.

- [ ] **Step 1: Write the test.** Create `internal/api/server_counters_integration_test.go`:

```go
//go:build integration

package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestServerCounters_Gating is the behavioural half of the admin gate. The AST
// guard in server_counters_test.go can see that the route is spelled
// auth(admin(...)); only this can see that spelling produce a 403.
func TestServerCounters_Gating(t *testing.T) {
	srv, q := newTestServer(t)
	admin := createTestUser(t, q, "Counters Admin", "counters-admin@example.com", true)
	adminToken := createTestToken(t, q, admin.ID)
	user := createTestUser(t, q, "Counters User", "counters-user@example.com", false)
	userToken := createTestToken(t, q, user.ID)

	get := func(token string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("GET", "/v1/server/counters", nil)
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)
		return rec
	}

	assert.Equal(t, http.StatusUnauthorized, get("").Code, "no bearer token must be 401")
	assert.Equal(t, http.StatusForbidden, get(userToken).Code,
		"these numbers describe adversary activity and internal control state; a non-admin must not read them")

	rec := get(adminToken)
	require.Equal(t, http.StatusOK, rec.Code, "an admin must be able to read the counters: %s", rec.Body.String())

	// newTestServer wires no counter sources, so this also proves the
	// absent-not-zero rule through the whole real stack.
	var top map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &top))
	require.Contains(t, top, "started_at")
	assert.NotContains(t, top, "grpc_admission",
		"an unwired section must be ABSENT: a section of zeros would say the control ran and stopped "+
			"nothing, which is a different fact")

	var startedAt time.Time
	require.NoError(t, json.Unmarshal(top["started_at"], &startedAt))
	assert.False(t, startedAt.IsZero(), "started_at must be a real timestamp: api.New records it")
}
```

- [ ] **Step 2: Compile it.**

Run: `make vet-integration`
Expected: no output (success). This is the check CI runs; it catches a shared-signature break in a tagged file that `make test` never compiles.

- [ ] **Step 3: Run it if Docker is available.**

Run: `go test -tags integration -p 1 ./internal/api/ -run TestServerCounters_Gating -v -timeout 300s`
Expected: PASS. If Docker Desktop is not running, say so explicitly in the PR body and hand the run to the Phase 4 integration lane - do **not** claim it green unrun.

- [ ] **Step 4: Commit.**

```bash
git add internal/api/server_counters_integration_test.go
git commit -m "test(api): integration gate proving GET /v1/server/counters is 401/403/200"
```

---

## Task 9: README

A wrong or missing docs contract is a defect in this project: consumers implement against the prose and no test covers it.

**Files:**
- Modify: `README.md` (a new `### Server` subsection after the `### Workers` table, which ends at line 1252; and one sentence in the `RELAY_GRPC_MAX_CONNS_PER_IP` row, line 281)

- [ ] **Step 1: Add the REST subsection.** Insert immediately before `### Reservations` (line 1254). The outer fence below is four backticks purely so this plan can show an inner JSON block; copy the CONTENT, not the outer fence.

````markdown
### Server

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/v1/server/counters` | Process-lifetime counters for the server's own silent controls (admin only). Unlike `/v1/jobs/stats` and `/v1/workers/stats`, which are database censuses readable by any authenticated user, these are in-memory numbers describing adversary activity and internal control state. |

```json
{
  "started_at": "2026-08-21T09:00:00Z",
  "grpc_admission": {
    "counts": { "refused_total": 12, "refused_per_source": 3 },
    "levels": { "live_total": 812, "distinct_sources": 16, "max_per_source": 64 }
  }
}
```

- **`counts` are monotonic since `started_at`; `levels` are current.** Counts only ever increase, so a poller derives rates itself; levels go up and down and are meaningless to sum.
- **A section that is not wired is ABSENT, not zero.** A section of zeros means the control ran and stopped nothing; an absent section means this build or this replica does not have that control wired. `started_at` is always present, because a restart zeroes every counter and a stalled counter would otherwise look identical to a restart.
- **Per replica.** These are in-process numbers about one server. A two-replica deployment splits its connections arbitrarily: read both endpoints and add the `counts`; do **not** add the `levels`, and `max_per_source` in particular does not sum into anything meaningful.
- **Nothing here carries an address, a prefix or a hostname**, and nothing ever will - the refusal path is reachable by any unauthenticated peer, so the counters are numbers by rule, not by omission.
- **Reading `grpc_admission`.** `distinct_sources` with `max_per_source` is what separates the two shapes `refused_total` cannot: a NAT gateway or a busy site is a few sources holding many connections each (`max_per_source` high, `distinct_sources` low), while a distributed source pattern is many sources holding one each. The IPv6 delegation case the per-source cap cannot close - sixteen `/64`s each holding the full 64 - reads as `live_total: 1024, distinct_sources: 16, max_per_source: 64` with **`refused_total` still at zero**, which is precisely the shape the refusal counters are blind to.
- **When `live_total` has reached your `RELAY_GRPC_MAX_CONNS`, read `refused_per_source` as a floor rather than a measurement.** The total cap is checked first, so a connection over both caps is counted against `refused_total` only.
- **When both `RELAY_GRPC_MAX_CONNS` and `RELAY_GRPC_MAX_CONNS_PER_IP` are `0`, every `levels` figure reads `0` however many connections are live.** With both caps off the listener does no accounting at all (that is what lets it hand the raw connection to gRPC), so a zero there means "not measured", not "nothing there". The startup line says `total DISABLED, per-source-IP DISABLED` when that is the case.
````

- [ ] **Step 2: Point the NAT hazard at the endpoint.** In the `RELAY_GRPC_MAX_CONNS_PER_IP` row (line 281), immediately after "the symptom is agents that never come online while the server logs a once-a-minute refusal summary, and the fix is to raise or disable this value.", append this sentence inside the same table cell:

> `GET /v1/server/counters` is what tells that case apart from an attack: a NAT gateway shows few `distinct_sources` each holding many connections, a distributed source pattern shows many sources holding one each, and sixteen `/64`s each at exactly 64 shows the delegation escape described above with nothing refused yet.

- [ ] **Step 3: Check the diff.** Confirm the new subsection's JSON block is outside the table, that the appended sentence stayed on the single table-row line (a newline inside a Markdown table cell breaks the row), and that no em dash or en dash was introduced.

Run: `git diff README.md`

- [ ] **Step 4: Commit.**

```bash
git add README.md
git commit -m "docs: document GET /v1/server/counters and its per-replica, counts-only semantics"
```

---

## Task 10: gates and the mutation battery

- [ ] **Step 1: Green baseline, before any mutation.** A battery run against a broken fixture reports uniform results and is worthless.

Run: `go test ./... -timeout 120s`
Expected: PASS, every package. Record the top-level test count.

Run: `make vet-integration`
Expected: success.

Run: `go test -race ./internal/netlimit/ ./internal/api/ ./cmd/relay-server/ -timeout 180s`
Expected: PASS. (Windows: `CC=/c/msys64/mingw64/bin/gcc.exe`.)

- [ ] **Step 2: Confirm the working tree is exactly the intended file set.**

Run: `git status --short` and `git diff --stat origin/main`
Expected: exactly `README.md`, `cmd/relay-server/counters_wiring_test.go`, `cmd/relay-server/grpc_config.go`, `cmd/relay-server/grpc_config_test.go`, `cmd/relay-server/main.go`, `internal/api/server.go`, `internal/api/server_counters.go`, `internal/api/server_counters_integration_test.go`, `internal/api/server_counters_test.go`, `internal/netlimit/listener.go`, `internal/netlimit/listener_test.go`. **Nothing under `web/`, `internal/store/`, `proto/` or `internal/proto/`. No `*.sql.go`, no `models.go`, no migration.**

- [ ] **Step 3: Run the mutation battery in an ISOLATED worktree.** Never mutate the shared tree - sibling agents read it.

```bash
git worktree add ../relay-mutations HEAD
```

Every row below must **compile**, and must be killed by the named test. Apply, run, confirm RED, revert, and move on. Record the outcome of every row in the PR body, including any row that fails to kill - a surviving mutation is a finding.

| # | Mutation | File | Must go RED |
| --- | --- | --- | --- |
| M1 | `last netlimit.Stats` with `if s == r.last` / `r.last = s` | grpc_config.go | **TestRefusalSummaryIsSilentWhenOnlyOccupancyMoves** |
| M2 | delete the early return from `tick` | grpc_config.go | TestRefusalSummaryLogsOnlyWhenCountersMove |
| M3 | `Stats()` takes and releases `l.mu` once per field (three acquisitions) | listener.go | **TestStats_IsOneCriticalSection** |
| M4 | `MaxPerSource: uint64(len(l.perIP))` | listener.go | TestStats_DistinguishesDistributedFromNAT (arrangement d: 3 != 7) |
| M5 | `MaxPerSource: uint64(l.total)` | listener.go | TestStats_ReportsOccupancy, TestStats_DistinguishesDistributedFromNAT |
| M6 | `DistinctSources: uint64(l.total)` | listener.go | TestStats_DistinguishesDistributedFromNAT (arrangement b) |
| M7 | `Occupancy` fields typed `int`, logged as such | listener.go + grpc_config.go | **TestRefusalSummaryLogsOnlyWhenCountersMove** (its `IsType` arm) |
| M8 | add `SourcePrefix string` to `Occupancy` | listener.go | TestStats_CarriesNoIdentifiers |
| M9 | delete the `if l.cfg.MaxTotal <= 0 && l.cfg.MaxPerIP <= 0` early return in `Accept` | listener.go | TestLimitListener_ZeroDisables (unchanged since before this slice) |
| M10 | `Stats()` returns `RefusedPerIP: l.refusedTotal.Load()` | listener.go | TestLimitListener_RefusesBeyondPerIPCap (proves Task 1's re-nested assertions still bite) |
| M11 | `GRPCAdmission` becomes a value field with no `omitempty` | server_counters.go | **TestServerCounters_OmitsUnwiredSections** |
| M12 | swap `LiveTotal` and `DistinctSources` in the mapping | server_counters.go | TestServerCounters_ReportsTheNetlimitSnapshot |
| M13 | route registered as `auth(http.HandlerFunc(...))` | server.go | TestServerCountersRouteIsAdminGated; and, with Docker, TestServerCounters_Gating |
| M14 | route registered with `mux.HandleFunc("GET /v1/server/counters", s.handleServerCounters)` | server.go | TestServerCounters_RequiresAuth **and** TestServerCountersRouteIsAdminGated |
| M15 | add `SourcePrefix string` to `grpcAdmissionLevels` | server_counters.go | TestCounterPayloadCarriesNoIdentifiers |
| M15b | M15 **with the path added to the expected leaf list** | server_counters.go | TestCounterPayloadCarriesNoIdentifiers (type rule alone) |
| M16 | add `PerSource map[string]uint64` to `grpcAdmissionCounts` | server_counters.go | TestCounterPayloadCarriesNoIdentifiers |
| M17 | add `Recent []uint64` to `grpcAdmissionLevels` | server_counters.go | TestCounterPayloadCarriesNoIdentifiers |
| M18 | add `Drift int64` to `grpcAdmissionLevels` | server_counters.go | TestCounterPayloadCarriesNoIdentifiers |
| M19 | add a pointer-to-struct field carrying a string to `serverCountersResponse` | server_counters.go | TestCounterPayloadCarriesNoIdentifiers |
| M20 | delete `httpServer.Counters = ...` | main.go | **TestServerCountersIsWiredByMain** |
| M21 | keep it and add `httpServer.Counters.GRPCAdmission = nil` on the next line | main.go | **TestServerCountersIsWiredByMain** (the exact evasion that beat the `netlimit.Config` guard) |
| M22 | `cs := api.CounterSources{GRPCAdmission: grpcLis}; cs.GRPCAdmission = nil; httpServer.Counters = cs` | main.go | TestServerCountersIsWiredByMain |
| M23 | wire a second `api.New(...)` value's `.Counters` and serve the original | main.go | TestServerCountersIsWiredByMain (the served-server check) |
| M24 | drop the three occupancy arguments from the log line | grpc_config.go | TestRefusalSummaryLineCarriesOccupancyWhenItSpeaks |

**M3 is the row most likely to be flaky.** If it does not go RED within a couple of seconds, raise `rounds` in the test; never weaken an invariant to make it deterministic. If M3 cannot be made to kill reliably, that is a finding to report, not to paper over.

Clean up: `git worktree remove ../relay-mutations`.

- [ ] **Step 4: Re-run the full gate in the shared tree and confirm nothing was left mutated.**

Run: `go test ./... -timeout 120s` and `git status --short`
Expected: PASS; the file set from Step 2, unchanged.

---

## Item 4 closure check

`docs/backlog/idea-2026-08-21-netlimit-occupancy-is-unobservable.md`, "Acceptance / Done When", bullet by bullet:

| Bullet | Met by | Verdict |
| --- | --- | --- |
| Occupancy of both caps visible through the reporter line **and** through the shared read surface | Task 4 (line) and Tasks 5-7 (endpoint) | MET |
| Distributed-source case distinguishable from NAT by the numbers alone | `TestStats_DistinguishesDistributedFromNAT`, all four arrangements including the 16 x 64 delegation shape the item's Notes section demands | MET |
| Every figure from a single critical section, proven by a test that goes RED when the read is split into separate acquisitions | `TestStats_IsOneCriticalSection` plus mutation M3, which must be **run**, not asserted | MET, conditional on M3 actually killing |
| Nothing carries an address, prefix or hostname, and the constraint is stated in `Stats`'s doc comment as a **rule** | Task 1's doc comment plus `TestStats_CarriesNoIdentifiers` (netlimit) and `TestCounterPayloadCarriesNoIdentifiers` (payload) | MET |
| Reporter still at most one line per interval, only when something moved, with the trigger rule settled explicitly | Task 3, `TestRefusalSummaryIsSilentWhenOnlyOccupancyMoves`, and the type-level enforcement | MET |
| Per-replica semantics documented | Task 1 doc comment, Task 5 file comment, Task 9 README | MET |
| No new lock, goroutine or allocation on `Accept`'s hot path | `Accept`, `admit`, `release` are untouched; the only new work is inside `Stats()` | MET, with one honest note: `Stats()` now **contends** for `l.mu` where before it did not. No new lock exists, and the contending callers run at once per minute and once per admin request against a mutex whose other holders run once per TCP connection. |

The item's three "questions it cannot answer" are addressed as follows, and none of them is an acceptance bullet: "how full is the cap" -> `live_total`; "one adversary or many hosts" -> `distinct_sources` with `max_per_source`; "which cap is binding" -> **closed by documentation plus a derived signal, not by code** (`live_total == RELAY_GRPC_MAX_CONNS` means `refused_per_source` is a floor). Re-attributing a one-day-old counter is rejected.

**Conclusion: item 4 is genuinely closable by this slice.** The conductor closes it with `/backlog close netlimit-occupancy`, which does the `git mv` into `docs/backlog/closed/`.

---

## Backlog effects (conductor, not the engineer)

Proposals only - the engineer files nothing.

**Close:** `idea-2026-08-21-netlimit-occupancy-is-unobservable`. The resolution note should name the two halves closed by decision rather than code: the `RefusedPerIP` attribution and the ceiling-with-no-attempts silence.

**Amend, do not close** (the read surface is now settled, and each item's own leading proposal has a refutation waiting for it): `idea-2026-08-20-repeated-watchdog-sweeps-...`, `idea-2026-08-15-ingest-log-suppression-is-uncounted`, `idea-2026-08-14-tasklog-fence-rejection-is-unobservable`. The watchdog item additionally needs the R2 constraint recorded: `internal/scheduler` imports `internal/api`, so its counters type must be declared in `internal/api`.

**Remove from all four items:** the "Possible dependency for the read surface: `feature-2026-08-09-server-info-allowlist-endpoint`" line. It is a sibling under a shared prefix, not a dependency.

**Candidate new item (R5), for the conductor to judge:** when both gRPC connection caps are disabled the listener does no accounting, so `GET /v1/server/counters` reports `live_total: 0` on a server holding thousands of live connections. "Not measured" and "nothing there" are the same payload, which is this spec's own subject one layer down. Closing it needs either the configured caps in the payload (and a naming decision, since `max_per_source` is already the observed maximum) or an omitted `levels` object. Documented in three places in this slice; not fixed.

---

## Constraint check (CLAUDE.md Invariants)

- **Epoch fence:** no SQL, no migration, no generated file, and **no write to `tasks.status` or `task_logs`** anywhere in this diff. If any step suggests `make generate`, that step is wrong.
- **No interior pointers across locks:** `Stats()` returns a value struct; `l.perIP` is walked under `l.mu` and its pointer never escapes. `CounterSources` holds interfaces, and the handler copies the snapshot by value.
- **Single JSON entry point:** the endpoint is a `GET` with no body, so `readJSON` is not involved; the response goes through `writeJSON`, matching `handleGetWorkerMetrics`.
- **One bounded sender per gRPC stream:** untouched. No send is added, and **no counter is ever returned to an agent** - `AgentService` has exactly one RPC and this slice does not touch it. The only read path is an admin-authenticated HTTP route on `:8080`.
- **Identity-checked teardown / end the generation before releasing the resource:** no generation, no teardown, no async continuation in scope.
- **Status vocabulary:** no status added, so both inverted allow-lists (`AppendTaskLog`'s first arm and `ListOverdueAssignedTasks`) and `TestTasksStatusVocabularyIsExactly` are untouched.
- **No new attacker-driven log site:** the reporter's line remains one per interval, gated on monotonic counts, carrying five `uint64` arguments and nothing else. This is the constraint most likely to be violated by a well-meaning addition, and the `IsType` arm of `TestRefusalSummaryLogsOnlyWhenCountersMove` is its guard - preserve it exactly.

---

## Self-review

**Spec coverage.** Section 10.1's five bullets map to Tasks 1-2 (netlimit), 3-4 (reporter), 5-7 (endpoint, route, wiring), 9 (README) and 10 (tests and mutations). Section 11's twelve tests all appear, with test 1 folded into `TestStats_ReportsOccupancy`, the spec's `TestStats_LevelsAreZero...` deleted as redundant (R6), and two guards added that the spec did not name (`TestServerCountersRouteIsAdminGated`, because CI runs no integration tests; and `TestServerCounters_Gating`, because the AST guard cannot see a 403). Section 16's fourteen acceptance criteria are covered; criterion 14 is the conductor's `/backlog close`.

**Type consistency.** `netlimit.RefusalCounts` / `netlimit.Occupancy` / `netlimit.Stats{Counts, Levels}`; `api.CounterSources{GRPCAdmission}`; `api.GRPCAdmissionSource` with the single method `Stats() netlimit.Stats`; `serverCountersResponse` / `grpcAdmissionSection` / `grpcAdmissionCounts` / `grpcAdmissionLevels`; `Server.Counters`, `Server.startedAt`, `handleServerCounters`. The Go field `RefusedPerIP` maps to the JSON key `refused_per_source` in exactly one place (`handleServerCounters`), deliberately, and the reason is in the struct field's comment.

**Residual risks consciously accepted.** (1) R4's unbounded scan when the total cap is disabled - disclosed, not fixed. (2) R5's "levels read zero when both caps are off" - disclosed in three places, filed as a candidate item. (3) M3's timing dependence - the plan says raise the iteration count, never weaken the invariant, and report if it will not kill. (4) A typed-nil `*netlimit.Listener` stored in `CounterSources.GRPCAdmission` would pass the handler's nil check and panic inside `Stats()`; unreachable from `main.go`, which assigns the real listener, and blocked from the tests by the structural guard.
