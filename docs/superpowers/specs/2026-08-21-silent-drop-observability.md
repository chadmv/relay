# Silent-drop observability: one mechanism for four controls that drop, refuse or kill without saying so

- Date: 2026-08-21
- Backlog items (all four, specced together, shipped separately):
  - `docs/backlog/idea-2026-08-20-repeated-watchdog-sweeps-against-one-worker-are-unsurfaced.md` (roadmap anchor)
  - `docs/backlog/idea-2026-08-14-tasklog-fence-rejection-is-unobservable.md`
  - `docs/backlog/idea-2026-08-15-ingest-log-suppression-is-uncounted.md`
  - `docs/backlog/idea-2026-08-21-netlimit-occupancy-is-unobservable.md`
- Adjacent, folded into slice 4: `docs/backlog/bug-2026-08-20-watchdog-error-branch-log-repeats-every-tick.md`
- Verified against: worktree `pr-merge-session-961184`, branch `claude/pr-merge-session-961184`, `main` @ `4b97895`
- Gate mode: autonomous. Every call that would have been a question is recorded in section 15.

---

## 1. Problem, restated after verification

Over four slices in nine days relay shipped five controls that stop something bad, correctly and
silently:

| Control | Shipped | What it stops | What it says |
| --- | --- | --- | --- |
| `AppendTaskLog`'s three-predicate fence | 2026-08-12, 2026-08-14 | a forged, stale or too-late log chunk | nothing |
| `ingestLogLimiter` dedupe arm | 2026-08-15 | a repeating diagnostic | nothing |
| `ingestLogLimiter` budget arm | 2026-08-15 | a log flood | nothing |
| `Watchdog.SweepOnce` | 2026-08-20 | an unbounded assignment | one line per swept task, aggregated nowhere |
| `netlimit.Listener` | 2026-08-20 | connection exhaustion | cumulative refusal counts, no occupancy |

Each one converted a loud failure into a quiet one. That is a real improvement in cost and a real
regression in detectability, and only the first half was decided on purpose. The operator-visible
signature of an attack is now **fewer log lines than normal**, which is indistinguishable from a
healthy fleet.

**There is no mechanism.** Verified in section 2: no `/metrics` route, no `/debug` route, no
Prometheus, no expvar, no direct OpenTelemetry dependency, and exactly two counters in the whole
tree. So this is not four small slices waiting on a shared decision; it is one design decision with
four consumers, and it has been deferred three roadmap refreshes running because each item
individually asked "where does an operator read it?" and none of them was allowed to answer.

This spec answers it once, decides the four forks that answer implies, and sequences four PRs.

---

## 2. What the code actually does, verified at HEAD

Every claim in this section was read in the tree.

### 2.1 There is no counter facility, and `internal/metrics` is not one

`internal/metrics` is **short-term per-worker utilization telemetry with liveness derivation**, and
its package doc says exactly that (`store.go:1-3`). It is:

- `Store` - a map of worker id to a bounded ring of `Sample`s, with `Activate` / `Append` / `Clear` /
  `Snapshot` / `LastSampleAt` (`store.go:38-115`);
- `Sweeper` - flips workers between `online` and `stale` from `LastSampleAt` (`sweep.go:25-40`).

Two properties of that type are decisive against putting a rejection counter in it, and both are
mechanical rather than stylistic:

- **`Append` is a no-op for an untracked worker** (`store.go:63-69`). Any counter routed through the
  same map inherits a silent-drop path, which is the exact defect class this spec exists to close.
- **`Clear` deletes the whole entry on teardown** (`store.go:81-85`, called from
  `handler.go:1246-1247` when a worker goes offline). A cumulative counter stored there is
  **destroyed at the moment the operator would go looking for it** - a worker that floods and then
  disconnects leaves zero.

`Store` is also lifecycle-coupled to registration (`Activate` at `handler.go:548`), which is the
wrong lifecycle for a process-lifetime count.

### 2.2 The only two counters in the tree

- `netlimit.refusedTotal` / `refusedPerIP`, two `atomic.Uint64` (`listener.go:87-88`), exposed as a
  value struct by `Stats()` (`listener.go:142-145`) and consumed by `refusalReporter`
  (`cmd/relay-server/grpc_config.go:432-460`).
- `worker.taskLogPublishes`, an `atomic.Int64` (`handler.go:1011`) whose own comment says
  **"Test-only observability ... Production code never reads it."**

So `netlimit` is the only precedent, it is one day old, and it was reviewed by a four-lens fan-out.

### 2.3 The route table has no server-wide counter surface

`internal/api/server.go:79-179`, complete for the relevant routes: `GET /v1/health`,
`GET /v1/config`, `GET /v1/jobs/stats`, `GET /v1/workers`, `GET /v1/workers/stats`,
`GET /v1/workers/{id}/metrics`. Nothing else carries a number about the server itself.

- `handleWorkerStats` (`workers.go:92-105`) is a **database status census**: `WorkerStatusCounts`
  aggregated into `{online, stale, offline, disabled, total}`. It is `auth`-only, not admin.
- `handleGetWorkerMetrics` (`worker_metrics.go:56-74`) serves the utilization ring for one worker,
  `auth`-only.

### 2.4 No metrics library is available

`go.mod:74-78` carries five `go.opentelemetry.io/...` lines, **every one marked `// indirect`**
(pulled in by the pgx/otelhttp chain). No Prometheus, no expvar, no direct OTel. Adding any of them
is a new direct dependency and a new deployment assumption.

### 2.5 The seam precedent for optional server-side state

`api.Server.Metrics` and `worker.Handler.Metrics` are exported fields set by `cmd/relay-server` after
construction (`main.go:143`, `main.go:174`) and nil-checked at every use (`handler.go:548, 1185,
1246`; `worker_metrics.go:34`). This is the established shape for "state that main wires and the
package must tolerate being absent".

### 2.6 The reporter's trigger, and the trap

`grpc_config.go:437-444`:

```go
func (r *refusalReporter) tick(s netlimit.Stats) {
	if s == r.last {
		return
	}
	r.logf("gRPC admission: %d connection(s) refused ...", s.RefusedTotal, s.RefusedPerIP)
	r.last = s
}
```

**Confirmed: whole-struct `==`.** Adding a level field to `Stats` makes the reporter speak every
minute forever.

And a second, sharper constraint nobody has written down yet.
`TestRefusalSummaryLogsOnlyWhenCountersMove` (`grpc_config_test.go:324-355`) ends with:

```go
		for _, a := range l.args {
			assert.IsType(t, uint64(0), a, "the summary must carry COUNTS ONLY. ...")
		}
```

`l.total` is an `int` and `len(l.perIP)` is an `int` (`listener.go:84-85`). **Any occupancy figure
put into that log line as an `int` turns a shipped test RED.** The occupancy fields must be typed
`uint64` at the snapshot boundary. That is a load-bearing implementation detail that reads like a
nit and is not one.

---

## 3. Item-by-item verification: what is confirmed and what is refuted

The project's most repeated lesson is that an item can be accurate and still prescribe a wrong
remedy. Every stage here refutes the one before it.

### 3.1 Item 1 - repeated watchdog sweeps

**CONFIRMED. `CountActiveTasksByAllWorkers` counts only `('dispatched','running')`.**
`internal/store/query/tasks.sql:565-572`:

```sql
SELECT worker_id, count(*)::bigint AS active
FROM tasks
WHERE worker_id IS NOT NULL
  AND status IN ('dispatched', 'running')
GROUP BY worker_id;
```

It is the dispatcher's slot computation (`internal/scheduler/dispatch.go:104`). So a `timed_out` row
does free the slot immediately, exactly as the item says.

**CONFIRMED. `sendCancel` discards the return value**, deliberately, with the reason in the comment
(`internal/scheduler/watchdog.go:247-249`): `_ = w.canceller.SendCancel(workerID, taskID, false)`.

**CONFIRMED. `SweepOnce` logs one line per swept task and it names the worker**
(`watchdog.go:211-213`), and the comment above it argues the line is safe because
`WatchdogMaxRowsPerSweep` bounds the count and each task is swept at most once.

**CONFIRMED, and this is the item's most important claim: a watchdog-written `timed_out` is
indistinguishable in the table from an agent-written one.** `handler.go:876-877` maps
`TASK_STATUS_TIMED_OUT` straight to `statusStr = "timed_out"`, so an agent honouring its own timeout
writes the same value the watchdog writes, and `handler.go:888` treats it as terminal. The two mean
opposite things about the worker's health. **This is what kills the cheap DB-query route** (section
7.2).

**CONFIRMED. Nothing aggregates by worker and nothing survives the process.** No counter exists on
`Watchdog` (whole file read), and section 2.3 covers the endpoints.

**NOT INDEPENDENTLY VERIFIED, and flagged rather than repeated as fact:** the item's Repro claim that
"the worker's `last_seen_at` stays fresh because its stream is healthy". Plausible and not load
bearing for any decision here; it is not relied on.

**REFUTED in emphasis, not in fact: "This one has a genuinely easier answer than the other two."**
The item argues that because the underlying event is already durable in `tasks`, a windowed
`COUNT(*)` needs no process state. The premise is true and the conclusion does not follow, because
of the writer-ambiguity the item itself flags two bullets later. Making the query correct requires a
schema change on a write path that sits under the epoch fence. **Item 1 is the HARDEST of the four,
not the easiest** - it is the only one whose correct-by-construction route is blocked and whose
fallback needs the only unbounded-in-principle key in the cluster. Section 10 sequences it last, and
that is a direct disagreement with the roadmap's ordering.

### 3.2 Item 2 - task-log fence rejection

**CONFIRMED. The arm exists, is named, is side-effect-free, and cites this item in source.**
`handler.go:1084-1112`. The comment block at `:1103-1110` says in as many words: "THIS ARM IS
DELIBERATELY SIDE-EFFECT-FREE AND MUST STAY SILENT ... Observability for this arm is
idea-2026-08-14-tasklog-fence-rejection-is-unobservable, whose answer is a COUNTER, not a log line.
... Pinned by TestHandleTaskLog_AFenceRejectionEmitsNoLogLineAtAll, which asserts the whole captured
log is empty."

**CONFIRMED. Three meanings of `ErrNoRows`, and all three are live at HEAD.** The fence has three
predicates (`tasks.sql:207-260`): identity (`worker_id` as a NULL-rejecting `=`), currency
(`assignment_epoch`), recency (the `status IN (...) OR finished_at > cutoff` disjunction). The
handler binds all three (`handler.go:1075-1082`), including `MinFinishedAt` from
`h.TrailingLogWindow`. The query comment states the three cases are deliberately indistinguishable
and that the third is the legitimate one.

**CONFIRMED, and it closes an open question the item left open: per-reason splitting is impossible
within the one-round-trip constraint.** `tasks.sql:220-224` - a chunk failing any predicate "matches
no fence row, inserts nothing, and returns zero rows". There is no row to carry a reason column on,
so the reason can only be recovered by a second query, which `handler.go:1026-1030` forbids ("Do not
add a query, a goroutine, or a queue here"). **One number. Say so in the comment. Nobody should
spend an afternoon on this again.**

**CONFIRMED. The "no log line" argument still holds, including for a budgeted line.** The path is
caller-driven volume on the recv goroutine; it fires on the legitimate late-flush case; and a
budgeted line spends a token from a bucket a genuine infra failure needs (`ingest_log_limiter.go:
136-142` - six tokens per minute for the whole connection across all five kinds).

**REFUTED, and this is the single most consequential error in the cluster: "Surfaced through the
existing `Handler.Metrics` seam".** The item hedges ("a global counter is not a natural fit for its
current shape") and then proposes three sub-options, two of which route through `metrics.Store`.
Section 2.1 is the refutation and it is mechanical, not aesthetic: `Store.Append` silently no-ops for
an untracked worker, and `Store.Clear` **deletes the entry on worker teardown**. A rejection counter
in `metrics.Store` is zeroed by the disconnect of the worker that caused the rejections. The `Metrics`
**wiring pattern** (exported field, set by main, nil-checked) is the right precedent and this spec
adopts it; `metrics.Store` itself is the wrong home and must not gain a counter method.

### 3.3 Item 3 - ingest log suppression

**CONFIRMED. Five kinds, three handlers, five call sites.** `ingest_log_limiter.go:99-105` defines
`kindTaskLogPersist`, `kindBadTaskIDLog`, `kindBadTaskIDStatus`, `kindStatusGetTask`,
`kindInventory`. Call sites, all read at HEAD after the admission slice's prose amendment:

| Site | Kind | Handler |
| --- | --- | --- |
| `handler.go:743` | `kindBadTaskIDStatus` | `handleTaskStatus` (`:719`) |
| `handler.go:774` | `kindStatusGetTask` | `handleTaskStatus` |
| `handler.go:1055` | `kindBadTaskIDLog` | `handleTaskLog` (`:1036`) |
| `handler.go:1147` | `kindTaskLogPersist` | `handleTaskLog` |
| `handler.go:1352` | `kindInventory` | `handleInventoryUpdate` (`:1347`) |

Plus two test-only constructions in `export_test.go`.

**REFUTED on a detail that matters for the implementation: `allow` has THREE `return false` paths,
not two.** `ingest_log_limiter.go:223` is a `l == nil` fail-closed guard, deliberately unreachable in
production (one allocation site, `handler.go:228`). **It must not be counted**, because no event was
suppressed on that path - there was no limiter. Counting it would be counting a phantom, and it is
the kind of thing an implementer adds while "covering all the return-false arms".

**CONFIRMED. The steady-state numbers in the item's Repro.** `ingestLogBurst = 16`,
`ingestLogRefill = 10 * time.Second` -> 6 lines/min (`:133-143`). The Repro's "16 lines immediately
and then 6 per minute" is exact.

**CONFIRMED. The limiter is a stack local in `Connect` with no mutex, by design** (`:72-77`,
`handler.go:228`).

**REFUTED, and it is the item's own preferred option: "option (b) is probably right" - accumulate in
the limiter and flush once at teardown.** Read it against the item's own Repro, which is a single
open stream sending 100,000 chunks. Under option (b) the operator sees **nothing at all for as long
as the attack continues**, and the numbers appear only after the attacker chooses to disconnect. An
observability control that is blind exactly during the attack it exists to reveal is not a control.
Section 7.3 takes option (a) in a form that does not add a mutex.

### 3.4 Item 4 - netlimit occupancy

**CONFIRMED. `Stats` is refusal counts only** (`listener.go:71-74`), with a doc comment already
stating the counts-only rule.

**CONFIRMED. `admit` checks the total first, so `RefusedPerIP` under-reports when the fleet cap is
also saturated** (`listener.go:234-248`, and the function's own comment says so).

**CONFIRMED. The reporter compares whole `Stats` structs with `==`** (section 2.6). The item's trap
is real, and section 6.2 settles the rule it forces.

**CONFIRMED. The three proposed fields are all available with no new state.** `l.total`
(`listener.go:84`), `len(l.perIP)` and a max over `l.perIP` (`:85`), all under `l.mu`.

**CONFIRMED. The `perIP` map is already bounded**: entries are deleted at zero
(`listener.go:296-305`) and pinned by `TestLimitListener_ReleasedIPIsRemovedFromTheMap`. So
`DistinctSources` is bounded above by `MaxTotal`, and `MaxPerSource`'s scan is bounded with it.

**CONFIRMED, and the item is right that this one is the odd one out:** `netlimit` runs before
authentication and never learns a worker identity. It has no per-worker shape and must never grow
one.

**NEW, not in the item, and it would have gone RED in CI:** the `uint64` typing constraint of
section 2.6. `l.total` and `len(l.perIP)` are `int`s; the shipped test asserts every log argument is
a `uint64`.

---

## 4. What "silent" actually costs, per item

Not a threat model in the admission-slice sense - none of these four changes what an attacker can
do. They change what an operator can see, and the exposure is the gap between the two.

| Item | The attack or fault | What the operator sees today | What the number buys |
| --- | --- | --- | --- |
| 1 | a wedged or hostile worker drains the queue and fails everything it is given | N log lines per sweep with a repeating UUID | "worker X: 37 sweeps" is a disable decision with one number behind it |
| 2 | a zombie or forged sender; **or** `RELAY_TASKLOG_TRAILING_WINDOW` set to `15s` by units confusion | task output silently truncated, no runtime signal of any kind | a non-zero rejection count is the only symptom of a misconfigured knob |
| 3 | 100,000 chunks on one stream | 16 lines then 6/min, forever | "39,984 suppressed on kind=task_log_persist" is the difference between an attack and a quiet fleet |
| 4 | 1024 slots held from 16 `/64`s | one line/min of cumulative refusals | `MaxPerSource == 1` with `DistinctSources` near the cap is the distributed-source signature; `MaxPerSource == 64` across 16 sources is the IPv6 delegation escape the admission slice disclosed and could not fix |

**The one rule every item states independently and that this spec carries unchanged: none of these
numbers may ever reach an agent.** They are server-side observability, never a response on the
worker stream. That is an acceptance criterion in all four items and it is satisfied structurally
here, because the only read path is an admin-authenticated HTTP route on `:8080` and the gRPC
service has exactly one RPC (`AgentService.Connect`), which this spec does not touch.

---

## 5. The mechanism (fork a)

**Decision: typed per-subsystem counters exposed as value structs, assembled by one admin-only HTTP
endpoint. No new counter package, no registry, no metrics library, no scrape port.**

Concretely, the mechanism is three things and only one of them is new code shared across the four
slices:

1. **A convention (no code).** A subsystem that drops, refuses or kills something owns
   package-private counters and exposes exactly one exported `Stats()` method returning a flat
   value struct. `netlimit` already does this; it is the pattern, not a coincidence.
2. **A response contract** (section 9), fixed now for all four sections so that no later slice
   reshapes a shipped payload.
3. **One route**, `GET /v1/server/counters`, `auth(admin(...))`, assembling whichever sections are
   wired.

The wiring seam is `api.Server.Counters`, a single struct of nil-able interface values set by
`cmd/relay-server` after construction, exactly mirroring `httpServer.Metrics = metricsStore`
(`main.go:174`). The interfaces are declared **in `internal/api`**, so `internal/api` gains no import
of `internal/scheduler` or `internal/netlimit`, and no import cycle is possible.

### 5.1 What was rejected, and what it would have cost

**A generic in-process counter registry** (`registry.Inc("name", labels...)`). Rejected. A
string-keyed map with caller-supplied labels is the precise shape of the defect this repo already
shipped once (`bug-2026-08-12-tasklog-err-limiter-attacker-keyed`, closed): a counter keyed on
anything a peer can vary is an unbounded-memory bug wearing a metrics hat. A registry also needs its
own mutex on hot paths, its own lifecycle, and it discards compile-time typing - which is what makes
the cardinality rules of section 7 checkable at all. Typed structs cannot be enlarged at runtime by
anything.

**Prometheus or a direct OpenTelemetry metrics SDK.** Rejected, and priced. It buys real things -
histograms, a scrape ecosystem, rate derivation for free - and costs: a new direct dependency tree
(section 2.4 shows all five OTel lines are indirect today); a `/metrics` endpoint whose auth model
does not exist, because Prometheus scrapers do not carry relay bearer tokens naturally, so the
realistic outcome is an unauthenticated port or a bespoke scrape credential; and a deployment
assumption relay does not currently make - it ships as three self-contained binaries with no assumed
sidecar. **The decisive argument is reversibility.** Typed `Stats()` value structs can be fed to a
Prometheus exporter later with zero changes at any call site; adopting Prometheus now cannot be
undone cheaply. Where a fork is genuinely unresolvable this spec prefers the reversible option, and
this one is not even close.

**expvar.** Rejected. `expvar.Publish` registers on `http.DefaultServeMux` as an init side effect;
relay builds its own mux (`server.go:79`). `/debug/vars` is unauthenticated by convention. Values are
untyped `Var`s, which defeats the reflection guard of section 11. It would be about twenty lines and
it is the wrong twenty lines.

**Extending `GET /v1/workers/stats`.** Rejected, and this is the option all four items name as the
likely answer. Three reasons: it is a **database** census, so mixing process-lifetime in-memory
counters into it makes one payload with two incompatible truth models (one survives a restart, one
does not); it is `auth`-only, not admin, and these numbers describe adversary activity; and three of
the four subjects are not per-worker at all, so joining them to a worker census forces a shape onto
`netlimit`, which has no identity **by design**. Item 4 warns against exactly that.

**Depending on `feature-2026-08-09-server-info-allowlist-endpoint`.** Rejected as a dependency, and
this corrects a line that appears in all four items. `GET /v1/server/info` is build and config facts
- a different noun, a different volatility profile, and it carries its own unrelated work (`-ldflags`
stamping, a `db_version` round trip). Making four observability items wait on it is why they have
waited three refreshes. **`/v1/server/counters` is a sibling of `/v1/server/info` under a shared
prefix, not a consumer of it**, and neither blocks the other. All four items should have that
"possible dependency" line removed.

---

## 6. Surfacing (fork b)

**Decision: an endpoint is the primary surface; a periodic log line is an optional per-subsystem
extra; and no new log line ever appears on the gRPC recv goroutine.**

### 6.1 Why both, and where each one is allowed

The endpoint is what all four items' Done-When actually require ("readable by an operator through an
endpoint, not only from a test"). It answers on demand, it can carry levels as well as counts, and it
is the only surface that can report a *current* value.

A log line is a poor primary surface for the same reason it is a good secondary one: it is a push,
so it is bounded by an interval rather than by demand, and a bounded push cannot answer "what is it
right now". Where the netlimit precedent already exists, keep it. Where an aggregate line is
genuinely more useful than the number (item 1's once-per-sweep summary), add it. Nowhere else.

**Absolutely excluded, verified in section 3.2 and carried unchanged: a log line - including a
budgeted one - on the `AppendTaskLog` fence-rejection arm or on any `ingestLogLimiter` suppression
path.** It would be caller-driven volume on the recv goroutine, it would fire on the legitimate
late-flush case, it would spend a token a real infra failure needs, and
`TestHandleTaskLog_AFenceRejectionEmitsNoLogLineAtAll` asserts the whole captured log is empty across
a rejection, so it would go RED. The reasoning holds at HEAD; the test makes it enforceable.

### 6.2 The trigger rule, settled once for every reporter

The item-4 trap generalizes: **any design that puts a level into a struct compared by equality makes
its reporter speak every interval forever.**

> **RULE. A reporter decides whether to speak from MONOTONIC COUNTS ONLY. Levels are carried in the
> line when it speaks, and are never consulted to decide whether it speaks.**

Made structural rather than remembered, by splitting every stats struct into two halves:

```go
type Stats struct {
	Counts RefusalCounts // monotonic, ==-comparable, decides whether to speak
	Levels Occupancy     // current, carried in the line, never compared
}
```

`refusalReporter.tick` then reads `if s.Counts == r.last { return }`, and the compiler makes the
wrong version awkward to write rather than easy. Both the JSON payload (section 9) and the Go types
carry the same split, so a future section author inherits the rule from the shape.

**One clarification, because the rule as stated would otherwise contradict section 9.**
`==`-comparability is required of a `Counts` half **only where a reporter compares it**, which today
means `netlimit` alone. The watchdog's `Counts` carries a per-worker map (section 7.2) and so is not
comparable at all. That is not an exception smuggled in: the watchdog's aggregate line (slice 4) is
emitted **once per sweep**, driven by the sweep itself rather than by a counter-move test, so it has
no comparison to get wrong and is already bounded by `WatchdogSweepInterval`. The
monotonic-versus-current classification still applies to every section, because it is part of the
payload contract. Any future section that *does* want a counter-move reporter must keep its `Counts`
half comparable, and the compiler will say so at the comparison.

**The honest residual, stated because it is the price of this rule.** A fleet sitting at exactly the
ceiling with no further connection attempts produces no line at all. That is the "settled at the
ceiling versus pressure ended" ambiguity item 4 complains about, and the trigger rule does not close
it in the log. It is closed by the endpoint, which reports the level on demand at any time - and it
is the main reason the endpoint is the primary surface rather than the nice-to-have. Note also that
the ambiguity only persists while nobody is being refused: the instant a legitimate agent is turned
away, refusals move, the reporter speaks, and the line carries the occupancy that explains why.

### 6.3 The counts-only rule, carried from item 4 without softening

Nothing surfaced from a pre-authentication path may carry an address, a prefix, a hostname or any
other caller-supplied byte. `netlimit.Stats`'s doc comment already states this and must state it as a
**rule** rather than as a description of the current fields. Item 4 is non-negotiable on this and
this spec carries it verbatim.

Two consequences the implementer must respect:

- **Occupancy fields are `uint64`, not `int`** (section 2.6). `TestRefusalSummaryLogsOnlyWhenCountersMove`
  asserts `IsType(uint64(0))` on every log argument. Converting at the snapshot boundary is the fix;
  changing that assertion is not.
- **The rule extends to the endpoint by a different argument, not by the same one.** The endpoint is
  admin-authenticated, so an identifier there is not an attacker-writable log site. Worker UUIDs are
  therefore admissible **in the endpoint** (item 1 needs them) and remain inadmissible in any log
  line reachable from the recv path. Section 11 pins the distinction with an allow-list guard rather
  than leaving it to judgement.

### 6.4 The under-reporting `RefusedPerIP` is closed by explanation, not by re-attribution

Item 4's third question - "which cap is the binding one" - is caused by `admit` checking the total
first (`listener.go:237-244`), so a connection over both caps is counted against `RefusedTotal` only.

**Decision: do not change the attribution.** Re-attributing a shipped counter changes what
`RefusedTotal` has meant since yesterday and invalidates the log line's own wording; adding a third
counter for "over both" is a third number for a question two numbers now answer. With occupancy
present, `LiveTotal == MaxTotal` tells the operator the total cap is binding and therefore that
`RefusedPerIP` is a floor rather than a measurement. That sentence goes in `Stats`'s doc comment and
in README. Stated plainly so the conductor can judge it: **this half of item 4 closes by
documentation plus a derived signal, not by code.**

---

## 7. Cardinality (fork c)

This is the fork most likely to produce a new bug, because this repo has already shipped exactly this
defect once. `taskLogErrLimiter` was keyed on wire-supplied task ids
(`docs/backlog/closed/bug-2026-08-12-tasklog-err-limiter-attacker-keyed.md`), and the current
limiter's own doc comment (`ingest_log_limiter.go:59-70`) is a monument to what that cost.

> **RULE. A counter key must come from a set the SERVER enumerates.**
>
> - Enumerated at **compile time** -> use a fixed-size array indexed by the enum. Free, and
>   unbounded growth is structurally impossible.
> - Enumerated at **runtime but bounded by an operator knob or a database row** -> a map with an
>   explicit capacity and an explicit **overflow counter**, so exceeding the cap is visible rather
>   than silent.
> - Enumerated by a **peer** -> it is not a key. Use a plain total.

Applied:

| Item | Key | Class | Bound |
| --- | --- | --- | --- |
| 4 netlimit | none - levels only | n/a | `perIP` already bounded by `MaxTotal`; no key is ever exported |
| 3 ingest suppression | `logKind` x arm | compile-time | `[5][2]atomic.Uint64`, no map |
| 2 fence rejections | none | n/a | one total; the reason is unavailable without a second round trip |
| 1 watchdog sweeps | `worker_id` | runtime-bounded | capped map + overflow counter (7.2) |

### 7.1 Item 4 - no keys at all, and it must stay that way

`DistinctSources` and `MaxPerSource` are integers derived from the map; **no key leaves the
package**. `MaxPerSource` costs an O(len(perIP)) scan under `l.mu`, at most `MaxTotal` = 1024 entries
at the defaults.

**Priced, because the endpoint is a new trigger for it.** The reporter scans once a minute; the
endpoint scans once per request, and an admin can request at any rate. A 1024-entry integer map scan
is on the order of a microsecond, and the mutex's other holders are `admit` and `release`, which run
**once per TCP connection** rather than per message. Even absurd polling holds a negligible fraction
of a lock that is not hot. The incremental-maximum alternative item 4 suggests is rejected for the
reason item 4 itself gives: a decremented maximum is not exactly recoverable without a scan, so the
incremental form needs the scan anyway on the decrement path - which moves the cost onto `release`,
a path that is closer to hot than the reporter is.

### 7.2 Item 1 - the only genuinely hard key, and why the durable route is closed

The signal *is* the per-worker split; a plain total says "37 sweeps happened" and answers nothing.

**The DB-query route is rejected for now, with the reason recorded so it can be revisited.** A
windowed `COUNT(*) ... WHERE status = 'timed_out' AND worker_id = $1 AND finished_at > $2` needs no
process state, survives restarts, and is correct across replicas - it is strictly better on every
axis except one, and that one is fatal: section 3.1 confirms an **agent** can write `timed_out`
itself, and the two writers mean opposite things about the worker's health. Distinguishing them
needs either a new terminal status (which must then be threaded through every status allow-list,
including the two that must be read backwards - `AppendTaskLog`'s first arm and
`ListOverdueAssignedTasks` - plus `TestTasksStatusVocabularyIsExactly`) or a nullable
`timed_out_by`-style column plus a migration and a regenerate on a write path that sits under the
epoch fence. Either is a larger and riskier slice than the observability it buys.

**If such a column is ever added for another reason, this counter should be revisited**, because the
query route is genuinely better. Write that in the code comment where the counter lives.

**So: an in-process per-worker counter on the `Watchdog`, bounded.** The keys come from rows the
coordinator's own scan returned, so they are not caller-chosen - but they are unbounded in
principle, because `bug-2026-08-21-auto-enroll-worker-row-creation-is-unbounded` is open and in
`Now`: under `RELAY_ALLOW_AUTO_ENROLL=true` a reachable host creates one persistent `workers` row per
hostname it claims. A map keyed on `worker_id` with no cap is therefore an unbounded map behind a
reachable-if-misconfigured path, which is the defect class one layer removed. Design:

- `sweptByWorker map[string]uint64`, capacity `watchdogSweptWorkerMax = 256`.
- At capacity, a **new** key is not inserted: `sweptOverflow` (a plain total of sweeps attributable
  to untracked workers) increments instead. Already-tracked keys keep counting.
- `sweptTotal` always counts every sweep, so the tracked map plus overflow always reconciles.
- Cumulative since process start. No rolling window, no clear - a sawtooth reset would make "37
  sweeps" mean different things at different times of day. The window is process uptime and the
  payload states it (`started_at`).
- The map is read under the `Watchdog`'s own mutex and **copied out**; a pointer to it never escapes
  (CLAUDE.md: no interior pointers across locks).

This is deliberately a lossy design and the loss is disclosed rather than hidden. A fleet with more
than 256 workers that have each been swept at least once loses per-worker attribution for the tail;
`sweptOverflow` being non-zero is the operator's signal that this happened. First-come rather than
top-K is chosen because top-K needs eviction, eviction needs a comparison on every increment, and the
signal item 1 wants ("worker X has 37") survives first-come in every realistic fleet.

### 7.3 Items 2 and 3 - process-wide atomics, and why not per-worker

Both increment on the gRPC recv goroutine, whose standing constraint is one DB round trip and
nothing else: no new lock, queue, goroutine or round trip.

**Decision: package-level `atomic.Uint64`s in `internal/worker`, keyed by nothing at the increment
site, split only by a compile-time enum.**

- An **atomic add is not a lock.** It is one locked exchange-add, no allocation, no map, no
  scheduling. On these paths it sits next to a Postgres round trip (item 2) or a map lookup plus a
  log-line decision (item 3). The constraint is respected in substance, not just in letter, and the
  limiter's documented no-mutex property (`ingest_log_limiter.go:72-77`) is preserved verbatim.
- **Cross-connection cache-line contention is the real cost and it is bounded**: at most
  `RELAY_GRPC_MAX_CONNS` writers, and every one of them is doing far more expensive work on the same
  call.
- **Per-worker keying is rejected at the increment site.** It needs a map write behind a shared lock
  on the recv goroutine, which is the thing the constraint forbids, and it buys attribution that the
  aggregate plus the existing per-connection log lines already approximate.
- **Item 3's option (b) - accumulate in the limiter, flush at teardown - is rejected** for the reason
  in section 3.3: it is blind for the entire duration of an ongoing flood. It also would have added
  the one thing the limiter's comment is proud of not having: a teardown to get wrong.
- For item 3, `[5][2]atomic.Uint64` - five kinds, two arms (deduped, budget-suppressed) - indexed by
  `logKind`. Item 3's "a `[5]uint64` on the limiter is free. Do not add a map" is right about the
  array and wrong about the location.
- **Do not count the `l == nil` arm** (section 3.3).
- For item 2, one number, with the comment saying why there will never be three (section 3.2).

**One consequence to record before slice 2 ships it.** `logKind`'s comment currently says "Values are
never persisted or sent anywhere, so they may be renumbered freely"
(`ingest_log_limiter.go:88-89`). Publishing per-kind counts makes the **names** part of a response
contract. Values may still be renumbered; renaming a kind changes a JSON key. Slice 2 amends that
comment and pins the name mapping with a test, or the comment becomes wrong prose about correct code
- the defect class this project has led with for nine consecutive iterations.

---

## 8. Lifetime and process boundaries (fork d)

**Decision: per process, best-effort, zeroed by a restart. No persistence, no cross-replica
aggregation.**

Why, per item rather than as a blanket assertion:

- **Items 2 and 3 cannot be durable.** Persisting would mean a write per event on paths whose entire
  constraint is one round trip. Disqualifying, not merely expensive.
- **Item 4 is inherently per-process.** The caps are per listener; the occupancy of *this* listener
  is the only occupancy that exists. A fleet-wide number would be a sum over replicas and is
  meaningless as a level (`MaxPerSource` does not add).
- **Item 1 is the only one whose event is already durable, and its durable form cannot distinguish
  the writer** (section 7.2). So it is per-process too, for now, and for a reason rather than by
  default.

**What per-process does NOT buy, stated so nobody discovers it in an incident:**

- **It is not fleet-wide.** A two-replica deployment splits its numbers arbitrarily. The watchdog is
  explicitly multi-replica-safe by first-write-wins (`watchdog.go:88-90`), so a sweep of worker X may
  be counted on either replica. An operator must read both endpoints and add them, and for
  `MaxPerSource` may not add them at all. This must be in the payload's documentation and in README,
  and the field names must not imply otherwise.
- **It is not a history.** No rate, no window, no ring buffer. A monitoring system that polls the
  endpoint derives rates itself; that is the standard division of labour and it is why counts are
  monotonic. Anyone wanting a graph wants the Prometheus exporter of section 5.1, which this design
  makes cheap later.
- **It is not an alert.** Nobody is paged. Every item's Done-When is "an operator can see the
  number", and that is what ships.
- **A restart zeroes everything**, so "the counter stopped moving" and "the process restarted" look
  identical. `started_at` is in the payload for exactly this reason and is the one field that makes
  the rest interpretable.

---

## 9. The response contract, fixed now for all four sections

Fixed in this spec even though slice 1 populates one section, because the alternative is a breaking
response-shape change in slice 4. This is the whole reason the four are specced together.

```
GET /v1/server/counters        auth(admin(...))
```

```json
{
  "started_at": "2026-08-21T09:00:00Z",
  "grpc_admission": {
    "counts": { "refused_total": 12, "refused_per_source": 3 },
    "levels": { "live_total": 812, "distinct_sources": 16, "max_per_source": 64 }
  },
  "ingest_log_budget": {
    "counts": {
      "deduped":    { "task_log_persist": 0, "bad_task_id_log": 0, "bad_task_id_status": 0,
                      "status_get_task": 0, "inventory": 0 },
      "suppressed": { "task_log_persist": 0, "bad_task_id_log": 0, "bad_task_id_status": 0,
                      "status_get_task": 0, "inventory": 0 }
    }
  },
  "task_log_fence": {
    "counts": { "rejected_total": 0 }
  },
  "watchdog": {
    "counts": { "swept_total": 0, "swept_overflow": 0, "swept_by_worker": { "<uuid>": 37 } },
    "levels": { "swept_workers_tracked": 0, "swept_workers_max": 256 }
  }
}
```

Rules that are part of the contract, not of the implementation:

- **`counts` are monotonic since `started_at`. `levels` are current.** A reporter may consult
  `counts` and may never consult `levels` (section 6.2).
- **An unwired section is ABSENT, never zero-valued.** This distinction is the spec's own subject one
  layer up: a section of zeros means "this control ran and stopped nothing"; an absent section means
  "this build or this replica does not have that control wired". Collapsing them reintroduces
  "fewer numbers than normal is indistinguishable from healthy" inside the payload that exists to
  fix it. Pinned by a test in every slice.
- **`started_at` is always present**, including when every section is absent.
- **No field anywhere carries a caller-supplied byte.** The only non-integer values in the entire
  payload are `started_at` and the keys of `watchdog.counts.swept_by_worker`, which are
  server-resolved worker UUIDs from a row the coordinator's own scan returned. That two-item
  allow-list is enforced by a reflection guard (section 11), so any third one goes RED and forces an
  argument.

**Route naming.** `/v1/server/counters` rather than `/v1/server/stats`, because `stats` is already
taken twice for database aggregates (`/v1/jobs/stats`, `/v1/workers/stats`) and these are neither.
It sits under the same `/v1/server/` prefix that `feature-2026-08-09-server-info-allowlist-endpoint`
proposes for `/v1/server/info`, deliberately, so the two compose without either blocking the other.

**Admin-only**, matching the `server-info` item's reasoning: `/v1/jobs/stats` and
`/v1/workers/stats` are `auth`-only and `/v1/config` and `/v1/health` are public, so this must not be
modelled on them. These numbers describe adversary activity and internal control state.

---

## 10. Slice plan (fork e)

One PR per slice. The mechanism ships in slice 1 with a single consumer.

### 10.1 Slice 1 - the mechanism plus `netlimit` occupancy (item 4)

**Recommended first, and this disagrees with the roadmap's anchor and with the brief's expectation
that the first consumer should exercise cardinality. The argument, on merit:**

1. **Its increment sites already exist and were reviewed yesterday.** Only the snapshot shape and
   the reporter's trigger change. Slice 1's risk therefore concentrates entirely on the genuinely
   new thing - the endpoint, the contract and the trigger rule - instead of being split between a new
   endpoint and a new write on the most security-sensitive goroutine in the repo.
2. **It is the only one of the four with a pre-existing discriminating test.**
   `TestRefusalSummaryLogsOnlyWhenCountersMove` goes RED if the trigger rule is got wrong, and its
   `IsType(uint64(0))` arm goes RED if the counts-only rule is got wrong. That is the strongest
   verification asset available in this cluster, and it exists only here.
3. **It exercises three of the four properties that are hard to get right**: the trigger rule,
   snapshot atomicity (all occupancy read in one critical section), and counts-not-identifiers.
4. **It touches no SQL, no migration, no epoch-fenced write and no recv goroutine**, so the review
   can be about the mechanism.
5. **Cardinality is the one hard property it does not exercise - and that is deliberate.** Shipping
   the hardest cardinality case (item 1's capped map with overflow) in the same PR as a brand-new
   endpoint is what scope discipline forbids. Cardinality ships in slice 1 as a **written rule plus
   the payload's allow-list guard** (section 11), which is what constrains slices 2 and 4; the two
   key classes are then instantiated by the slices that need them.

**Why not item 1 first**, despite the roadmap: it is the most valuable and the least ready. It
carries an unresolved schema question (section 7.2), the only unbounded-in-principle key, an
interaction with a second open backlog item, and a per-worker payload section. It is the right *last*
slice, when the contract is settled and the rules are enforced by a shipped guard.

**In slice 1:**

- `internal/netlimit/listener.go` - `Stats` splits into `Counts`/`Levels`; `Stats()` reads
  `total`, `len(perIP)` and the per-source maximum **in one critical section**, converting to
  `uint64` at the boundary; the doc comment states counts-only as a rule and states the
  `RefusedPerIP` under-report and how occupancy makes it interpretable (section 6.4).
- `cmd/relay-server/grpc_config.go` - `refusalReporter.tick` compares `s.Counts` only, and the line
  carries occupancy when it speaks.
- `internal/api/server_counters.go` (new) - the response types, the `CounterSources` struct of
  nil-able interfaces, section omission, the handler.
- `internal/api/server.go` - one route, `auth(admin(...))`.
- `cmd/relay-server/main.go` - `httpServer.Counters = api.CounterSources{GRPCAdmission: grpcLis}`.
- `README.md` - the endpoint, the per-replica semantics, the counts-only rule, the distributed-source
  versus NAT reading of `MaxPerSource`/`DistinctSources`.
- Tests and mutations of section 11.

**Out of slice 1, explicitly:** any counter in `internal/worker` or `internal/scheduler`. The
mechanism serves them; absorbing them is what this spec exists to prevent.

### 10.2 Slice 2 - `ingestLogLimiter` suppression counts (item 3)

Second because it establishes the **array** cardinality class and the hot-path atomic decision, it
creates the `internal/worker` counters home that slice 3 then reuses, and its numbers matter most
under active attack. Contents: `[5][2]atomic.Uint64` in `internal/worker`, a pointer threaded into
`ingestLogLimiter`, the two arms counted (never the nil arm), the `logKind` comment amendment plus
the name-mapping test (section 7.3), the `ingest_log_budget` section, README.

### 10.3 Slice 3 - task-log fence rejections (item 2)

Third, not second, purely because of the home: it is one atomic add plus one payload field, and
doing it first would create the `internal/worker` counters struct for a single number and then
reshape it in slice 2. **Slices 2 and 3 are deliberately not merged**, and both items say why: they
count different nouns in different branches of the same `if`, no input executes both, and merging
falsifies item 2's own Done-When. Confirmed at HEAD - the two arms are `handler.go:1084` and
`handler.go:1114`. Contents: one `atomic.Uint64`, incremented in the existing named arm before its
return; the arm's comment extended (not duplicated) to say the counter landed and that per-reason
splitting is structurally impossible; the `task_log_fence` section.

### 10.4 Slice 4 - watchdog sweeps per worker (item 1), plus the error-branch log bug

Last, for the readiness reasons in 10.1. **Recommend folding
`bug-2026-08-20-watchdog-error-branch-log-repeats-every-tick` into this slice's scope.** That bug
wants a once-per-sweep aggregate line and says "If both land, they should be one line, not two"; item
1 wants a once-per-sweep aggregate line. Two slices each adding a summary line to `SweepOnce` will
produce two lines and a third item to reconcile them. Contents: the capped map plus overflow
(section 7.2), one aggregated sweep line covering both the swept set and the failed-write set, the
comment fix naming which line the "swept at most once" argument covers, the `watchdog` section, and
the code comment recording that the DB-query route should be revisited if a writer-distinguishing
column is ever added.

### 10.5 What each slice actually CLOSES

| Slice | Item | Closes or enables |
| --- | --- | --- |
| 1 | item 4 netlimit-occupancy | **CLOSES.** All six Done-When bullets are in scope, with the `RefusedPerIP` under-report closed by documentation plus a derived signal (section 6.4) rather than by code - which is within the item's Done-When, since that point appears under "questions it cannot answer" and not under acceptance. |
| 1 | items 1, 2, 3 | **ENABLES ONLY. All three stay open.** Slice 1 settles the read surface, which is the shared expensive part all three name, and nothing else. None of their acceptance criteria - each of which requires a counter proven by a handler-layer or scheduler-layer test - is met. |
| 2 | item 3 ingest-log-suppression | CLOSES. |
| 3 | item 2 tasklog-fence-rejection | CLOSES. |
| 4 | item 1 watchdog-sweeps | CLOSES, and closes `bug-2026-08-20-watchdog-error-branch-log-repeats-every-tick` with it. |

**So this iteration can legitimately close exactly one backlog item: item 4.** Anyone reporting that
slice 1 "closed the observability cluster" is wrong, and the three remaining items must be amended
rather than closed (section 17).

---

## 11. Test strategy for slice 1

A green test can be vacuous. Each criterion names what proves it.

### 11.1 `netlimit`

1. **`TestStats_ReportsOccupancy`.** Admit 3 conns from 2 sources against generous caps; assert
   `LiveTotal == 3`, `DistinctSources == 2`, `MaxPerSource == 2`. Close one; assert the numbers
   follow. **RED at HEAD:** the fields do not exist (new-field-style vacuous RED, so it carries
   little on its own - the mutations below are what make it load-bearing).
2. **`TestStats_DistinguishesDistributedFromNAT`.** This is item 4's Notes requirement and the
   detection story for the IPv6 delegation residual the admission slice disclosed and could not fix.
   Two arrangements with **identical `LiveTotal`**: 64 conns from one source, and 64 conns from 64
   sources. Assert `MaxPerSource`/`DistinctSources` differ as `64/1` versus `1/64`. Then the real
   case: 16 sources x 64 conns, and assert the pair reads `64/16` while `RefusedTotal` is still 0.
3. **`TestStats_IsOneCriticalSection`.** Drive `admit`/`release` from N goroutines while a reader
   calls `Stats()` in a loop, under `-race`, asserting the internal consistency invariants
   `MaxPerSource <= LiveTotal`, `DistinctSources <= LiveTotal`, and `LiveTotal <= MaxTotal` on every
   snapshot. **This is the test item 4 asks for**, and the discriminating property is real: with
   three separate lock acquisitions, connections closing between reads make `DistinctSources >
   LiveTotal` observable. Enough iterations to make the window reliably hit; if it proves flaky under
   load, the fix is more iterations, never a weaker invariant.
4. **`TestStats_CarriesNoIdentifiers`.** Reflection over `netlimit.Stats`: every field of `Counts`
   and `Levels` must be an unsigned integer type. Nothing else is permitted. This is the guard that
   answers a future "which IP is it?" request on the record.

### 11.2 The reporter

5. **`TestRefusalSummaryLogsOnlyWhenCountersMove`** - existing (`grpc_config_test.go:324-355`). Its
   **assertions must not weaken**; only its `netlimit.Stats` literals may be re-nested into
   `Counts{...}`. **If the implementer finds an assertion must change, that is a finding to report,
   not to fix.** Note in particular that its `IsType(uint64(0))` arm is what forces occupancy to be
   `uint64` (section 2.6).
6. **`TestRefusalSummaryIsSilentWhenOnlyOccupancyMoves`** (new). Tick with static counts and moving
   occupancy, several times; assert zero lines. **This is the discriminating test for the item-4
   trap**, and it is RED under the naive `s == r.last`.
7. **`TestRefusalSummaryLineCarriesOccupancyWhenItSpeaks`** (new). One tick with moved counts;
   assert the line's arguments include the three occupancy figures and that all arguments are
   `uint64`.

### 11.3 The endpoint

8. **`TestServerCounters_RequiresAdmin`.** 401 unauthenticated, 403 for a non-admin token, 200 for
   admin. Mirrors the existing admin-route tests.
9. **`TestServerCounters_OmitsUnwiredSections`.** Nil `GRPCAdmission` -> the `grpc_admission` key is
   **absent**, not present-and-zero, and `started_at` is still present. The discriminating assertion
   is on key absence in the raw JSON, not on a decoded zero value.
10. **`TestServerCounters_ReportsTheNetlimitSnapshot`.** A fake source; assert the payload's
    `counts`/`levels` match it exactly.
11. **`TestCounterPayloadCarriesNoIdentifiers`.** Reflection over the whole response type tree:
    every field must be an unsigned integer **except** an explicit allow-list of exactly
    `{started_at, watchdog.counts.swept_by_worker}`. Written as an allow-list, never as a deny-list,
    per the project's stated rule - a deny-list fails open on the next field somebody adds. Slice 1
    ships the allow-list already naming the slice-4 field, so slice 4 adds no exception and any
    *other* new string or map field goes RED.
12. **`TestServerCountersIsWiredByMain`.** A structural guard in the shape of
    `TestGRPCAdmissionIsWiredByMain` (`grpc_config_test.go:357`): deleting
    `httpServer.Counters = ...` from `main.go` compiles and leaves every package green while the
    endpoint permanently reports an empty payload. Read the assignment out of `main.go` with
    `go/ast`, not a regex. **And heed the recorded lesson that a guard built from a mutation kill
    inherits the mutation's shape, not the defect's**: the admission slice's three structural guards
    were each evaded on the first attempt (by an `append`, by an import alias, and by moving a field
    assignment to the next line). Write this one to resolve the value flowing into the field, and
    attempt at least those three evasions before calling it done.

### 11.4 Mutation matrix

A test is load-bearing only if a mutation kills it. Run in an isolated worktree, never the shared
tree.

| Mutation | Must go RED |
| --- | --- |
| `tick` compares the whole `Stats` (levels included) | **6** |
| `tick` compares nothing (always speak) | 5 |
| `Stats()` takes the lock three times, once per field | **3** |
| `MaxPerSource` computed as `len(perIP)` | **2** |
| `MaxPerSource` computed as `LiveTotal` | 1, 2 |
| Occupancy fields typed `int` and logged as such | **5** (its `IsType` arm) |
| Add a `SourcePrefix string` field to `Levels` | **4**, 11 |
| Unwired sections emitted as zero-valued objects | **9** |
| Route registered with `auth` but not `admin` | 8 |
| `httpServer.Counters` assignment deleted from `main.go` | **12** |
| `httpServer.Counters` fields assigned on a following line instead of in the literal | **12** (this is the exact evasion that beat the `netlimit.Config` guard) |

### 11.5 Existing tests

Only `TestRefusalSummaryLogsOnlyWhenCountersMove` may change, and only its literals. Any other
existing test whose result changes is a finding to report, not to fix.

---

## 12. Alternatives considered and rejected

Consolidated; the reasoning is in the sections named.

- **A generic string-keyed counter registry.** Section 5.1. It is the shape of a defect this repo has
  already shipped.
- **Prometheus / direct OTel / a `/metrics` scrape port.** Section 5.1. New dependency, new auth
  problem, new deployment assumption, and irreversible where the chosen design is not.
- **expvar.** Section 5.1. Wrong mux, unauthenticated by convention, untyped.
- **Extending `GET /v1/workers/stats`.** Section 5.1. Two truth models in one payload, wrong
  authorization level, forces a per-worker shape on a subsystem that has no identity by design.
- **Blocking on `feature-2026-08-09-server-info-allowlist-endpoint`.** Section 5.1. It is a sibling,
  not a dependency; treating it as one is why four items have waited three refreshes.
- **A counter method on `metrics.Store`.** Section 3.2. `Append` no-ops for untracked workers and
  `Clear` deletes the entry on teardown; the numbers vanish exactly when they are wanted.
- **Per-connection accumulation flushed at teardown (item 3's own preference).** Section 3.3. Blind
  for the whole duration of an ongoing flood.
- **Per-worker keying at the recv-goroutine increment sites.** Section 7.3. Needs a shared-map write
  on the one path where that is forbidden.
- **Splitting item 2's counter by rejection reason.** Section 3.2. The fence returns no row, so the
  reason is only recoverable by a second round trip. Structurally impossible within the constraint.
- **The `tasks`-table query for item 1.** Section 7.2. Better on every axis except that an agent
  writes the same status; unblocking it costs a migration on an epoch-fenced write path.
- **Re-attributing `RefusedPerIP` when both caps are saturated.** Section 6.4. Changes the meaning of
  a shipped counter to answer a question two new numbers now answer.
- **Folding `taskLogPublishes` into the endpoint.** Rejected as a scope extension. It counts a
  **success** path and its own comment (`handler.go:1006-1011`) declares it a test seam that
  production never reads. This spec's subject is drops, refusals and kills; promoting a test seam to
  a product surface is a different decision with a different argument.
- **Auto-disabling a worker on a sweep count** (item 1 raises and rejects it). Agreed and carried:
  the threshold is a product decision nobody has made, the failure mode of a wrong threshold is
  removing a healthy machine from a fleet, and `handleDisableWorker` already gives an operator a
  one-call remedy once the number is visible. Surface first; automate later if asked.

---

## 13. Constraint checks

- **Epoch fence.** Slice 1 adds no SQL, no migration, no generated file, and no write to
  `tasks.status` or `task_logs`. Slices 2-4 add none either; slice 4 explicitly rejects the schema
  change that would have touched a fenced write path. If a plan step in any slice proposes
  `make generate`, that step is wrong.
- **Single job-spec pipeline.** Not applicable.
- **One bounded sender per gRPC stream.** Untouched. No send is added anywhere, and no counter is
  ever returned to an agent - the only read path is an admin HTTP route.
- **Identity-checked teardown.** Untouched, and strengthened by omission: rejecting item 3's
  flush-at-teardown option (section 3.3) means no new teardown exists to get wrong.
- **No interior pointers across locks.** `Stats()` returns a value struct. The slice-4 watchdog map
  is copied out under the lock and its pointer never escapes.
- **Single JSON entry point.** The endpoint is a `GET` with no body, so `readJSON` is not involved;
  the response goes through `writeJSON`, matching `handleGetWorkerMetrics`.
- **End the generation before releasing the resource.** No generation, no async continuation, no
  teardown ordering in scope.
- **No new attacker-driven log site.** Sections 6.1 and 6.3. This is the constraint most likely to be
  violated by a well-meaning implementation, and tests 5, 6 and 7 are its guards.
- **Status vocabulary.** No status is added, so the two inverted allow-lists (`AppendTaskLog`'s first
  arm, `ListOverdueAssignedTasks`) are untouched and `TestTasksStatusVocabularyIsExactly` is
  unaffected. Slice 4's rejection of the new-status route is what keeps this true.

---

## 14. Scope

**In (slice 1).** `internal/netlimit/listener.go`; `cmd/relay-server/grpc_config.go`;
`internal/api/server_counters.go` (new); one line in `internal/api/server.go`; one line in
`cmd/relay-server/main.go`; README; the tests of section 11.

**Out, sequenced rather than dropped.** The three remaining consumers (10.2-10.4).

**Out, permanently, with reasons.**

- Any metrics library, scrape endpoint or exporter. Section 5.1. A future exporter is cheap over this
  design and is nobody's task today.
- Cross-replica aggregation. Section 8.
- Histograms, rates, windows or any history. Section 8.
- Alerting or auto-remediation of any kind, including auto-disabling a worker. Section 12.
- `taskLogPublishes`. Section 12.
- `GET /v1/server/info` and `-ldflags` stamping. A sibling item, unblocked and unblocking.
- Any change to refusal attribution in `netlimit.admit`. Section 6.4.
- A frontend surface. The admin console's Server tab is the obvious eventual consumer and it is not
  in any of these four slices; it should be its own item once numbers exist to render.

---

## 15. Decisions taken autonomously

Gate mode is autonomous. Each of these would otherwise have been a question.

- **D1. The mechanism is typed per-subsystem `Stats()` structs plus one admin endpoint - not a
  registry, not Prometheus, not expvar.** Section 5. Would have escalated: it commits the project to
  a shape for every future counter. Called on reversibility - typed structs feed an exporter later at
  zero call-site cost, and the reverse is not true - and on the recorded string-key defect.
- **D2. `GET /v1/server/counters`, admin-only, and NOT a dependency on
  `feature-2026-08-09-server-info-allowlist-endpoint`.** Section 5.1. Would have escalated: it
  contradicts a line in all four items. Called: a sibling under a shared prefix. Deferring four
  observability items behind an unrelated build-facts endpoint is why they have not shipped.
- **D3. `metrics.Store` is the wrong home, and item 2's proposal is refuted on mechanism.**
  Sections 2.1 and 3.2. Would have escalated: it refutes the item's own leading proposal. Called:
  `Append` no-ops for untracked workers and `Clear` deletes the entry on teardown, so a counter there
  is destroyed by the disconnect that caused it. The `Metrics` **wiring pattern** is adopted; the type
  is not.
- **D4. Levels never participate in a reporter's "did anything move" test, enforced by splitting
  every stats struct into `Counts`/`Levels`.** Section 6.2. Would have escalated: it reshapes a
  struct shipped yesterday and edits a shipped test's literals. Called: the rule must be structural,
  because three more slices will each add a section and a remembered discipline will not survive
  them. The residual - a fleet silently parked at the ceiling - is disclosed and is closed by the
  endpoint.
- **D5. Item 3's preferred option (b) is refuted.** Section 3.3. Would have escalated: the item calls
  it "probably right". Called: it is blind for the entire duration of the flood in the item's own
  Repro. Process-wide atomics instead - an atomic is not a lock, and the limiter keeps its no-mutex
  property verbatim.
- **D6. Item 2's counter is ONE number and per-reason splitting is closed as structurally
  impossible.** Section 3.2. Called rather than left open, because the item asks the next reader to
  "find a way to get the reason out of the existing statement" and the statement returns no row at
  all. Closing it here is worth an explicit sentence in the code comment.
- **D7. Item 1's DB-query route is rejected, with a revisit condition recorded in source.** Section
  7.2. Would have escalated: the item says to weigh it first, and on the merits it is the better
  design. Called: it needs a writer-distinguishing schema change on an epoch-fenced write path, which
  is a bigger slice than the observability it buys.
- **D8. Item 1's per-worker map is capped at 256 with a first-come policy and an explicit overflow
  counter.** Section 7.2. Would have escalated: it is a lossy design and the loss is the item's own
  headline signal for a large fleet. Called: unbounded is not an option while auto-enroll row growth
  is open, top-K costs a comparison per increment, and `sweptOverflow != 0` makes the loss visible
  rather than silent - which is this spec's own subject applied to itself.
- **D9. Slice 1 is item 4, not the roadmap's anchor item 1.** Section 10.1. Would have escalated: it
  reorders three roadmap refreshes' stated priority and disagrees with the task brief's expectation
  that the first slice exercise cardinality. Called on readiness: item 4's increment sites already
  exist, it owns the only pre-existing discriminating test in the cluster, it touches no fenced path,
  and it needs no deferred decision. Cardinality ships in slice 1 as a rule plus a payload guard.
- **D10. Per-process, best-effort, no persistence, no cross-replica aggregation.** Section 8. Not
  really a fork - two of the four cannot be durable - but the *disclosure* is a decision, and
  `started_at` is in the payload specifically because a restart and a stalled counter are otherwise
  identical.
- **D11. An unwired section is absent, not zero.** Section 9. Would have escalated as a contract
  detail. Called: collapsing "not wired" into "nothing happened" reintroduces this spec's own defect
  inside the payload meant to fix it.
- **D12. `bug-2026-08-20-watchdog-error-branch-log-repeats-every-tick` is folded into slice 4.**
  Section 10.4. Would have escalated as a scope extension. Called: both items want a once-per-sweep
  aggregate line, and shipping them separately produces two lines and a third item.
- **D13. `RefusedPerIP`'s under-report is closed by documentation plus a derived signal, not by
  re-attribution.** Section 6.4. Called: changing a one-day-old counter's meaning is worse than
  explaining it, and occupancy makes the explanation checkable.

---

## 16. Acceptance criteria for slice 1

1. `netlimit.Stats` carries occupancy - `LiveTotal`, `DistinctSources`, `MaxPerSource` - split from
   the refusal counts into a `Levels` half, with every field an unsigned integer type (test 4).
2. Every occupancy figure in one snapshot comes from a **single critical section**, proven by test 3
   and killed by the three-acquisition mutation.
3. The distributed-source case is distinguishable from the NAT case by the reported numbers alone,
   including the 16-prefixes-x-64 IPv6 delegation shape, proven by test 2.
4. `refusalReporter` still emits **at most one line per interval and only when a monotonic count
   moved**, proven by tests 5 and 6, with 6 RED under the naive whole-struct comparison. Test 5's
   assertions are unchanged; only its literals are re-nested.
5. When the reporter speaks, its line carries occupancy, and every argument is a `uint64` (test 7).
6. Nothing added carries an address, a prefix, a hostname or any other caller-supplied byte, and
   `netlimit.Stats`'s doc comment states this as a **rule** rather than as a description of today's
   fields (test 4, and test 11 for the payload).
7. `GET /v1/server/counters` exists, is `auth(admin(...))`, and returns the section-1 payload of
   section 9 (tests 8, 10).
8. An unwired section is **absent** from the payload rather than zero-valued, and `started_at` is
   always present (test 9).
9. The payload's non-integer fields are exactly `{started_at, watchdog.counts.swept_by_worker}`,
   enforced as an allow-list that already names the slice-4 field (test 11).
10. The wiring in `main.go` is guarded structurally, and the guard survives at least the three
    evasions that beat the admission slice's guards (test 12).
11. The per-replica semantics are documented in README and in the payload's own documentation, and
    no field name implies a fleet-wide figure.
12. `README.md` documents the endpoint, the counts-only rule, and the reading of
    `MaxPerSource`/`DistinctSources` including that `LiveTotal == MaxTotal` means `RefusedPerIP` is a
    floor rather than a measurement.
13. No new lock, goroutine or allocation on `Accept`'s hot path; no SQL, migration, proto or
    generated file touched; `make test` green and the tagged integration suites unchanged.
14. **`docs/backlog/idea-2026-08-21-netlimit-occupancy-is-unobservable.md` is closed via
    `/backlog close`**, and the other three items are amended, not closed (section 17).

---

## 17. Backlog effects - proposed, not filed

Per the standing rule, these are proposals for the human or the conductor to accept.

**Close on slice 1** (via `/backlog close`, which does the `git mv` to `docs/backlog/closed/`):

- `idea-2026-08-21-netlimit-occupancy-is-unobservable` - resolution should name the two halves closed
  by decision rather than code: the `RefusedPerIP` attribution (section 6.4) and the ceiling-with-no-
  attempts silence (section 6.2).

**Amend on slice 1, do not close** - each of the three remaining items should gain a short update
recording that the read surface is settled:

- `idea-2026-08-20-repeated-watchdog-sweeps-against-one-worker-are-unsurfaced` - the endpoint exists;
  the DB-query route is rejected with a revisit condition (section 7.2); the per-worker key is capped
  with an overflow counter; `bug-2026-08-20-watchdog-error-branch-log-repeats-every-tick` is folded
  into its slice; and its "easier than the other two" framing is refuted (section 3.1).
- `idea-2026-08-15-ingest-log-suppression-is-uncounted` - option (b) is refuted (section 3.3); the
  shape is `[5][2]atomic.Uint64` in `internal/worker`; the `l == nil` arm must not be counted; the
  `logKind` comment needs amending because kind names become a response contract.
- `idea-2026-08-14-tasklog-fence-rejection-is-unobservable` - the `metrics.Store` home is refuted on
  mechanism (section 3.2); per-reason splitting is closed as structurally impossible; the shape is
  one process-wide `atomic.Uint64`.

**Remove from all four items:** the "Possible dependency for the read surface:
`feature-2026-08-09-server-info-allowlist-endpoint`" line. It is not a dependency (section 5.1), and
leaving it there is what has deferred this cluster three times.

**No new items are proposed by this spec.** If slice 1's review surfaces something, it files its own.
