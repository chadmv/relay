---
date: 2026-08-21
topic: silent-drop-observability-slice1
slice: 2026-08-21-silent-drop-observability (slice 1 of 4)
branch: claude/pr-merge-session-961184
range: origin/main..HEAD (backend only; Go plus README; zero SQL, zero migration, zero proto, zero generated file, zero files under web/; green, not yet merged, no PR opened at the time of writing)
closes: idea-2026-08-21-netlimit-occupancy-is-unobservable
enables-only: idea-2026-08-20-repeated-watchdog-sweeps-against-one-worker-are-unsurfaced, idea-2026-08-14-tasklog-fence-rejection-is-unobservable, idea-2026-08-15-ingest-log-suppression-is-uncounted
---

# Session Retro: 2026-08-21 - a guard that matches a SPELLING is evadable by respelling; a guard that counts a PROPERTY is not

**TL;DR:** `netlimit.Stats` split into `Counts` (monotonic) and `Levels` (current), with occupancy -
`LiveTotal`, `DistinctSources`, `MaxPerSource` - computed in ONE critical section; `refusalReporter`
now compares `RefusalCounts` because its `last` field is typed as one, so comparing a whole `Stats` is
a compile error; a new admin-only `GET /v1/server/counters` assembles whichever sections are wired and
omits the rest; and `cmd/relay-server/http_server.go` is new, holding `buildHTTPServer`, which returns
`*http.Server` so that `main` never touches the `*api.Server` at all. Unit 554 -> 574 top-level; all
five integration packages green (`internal/api` 295 tests, ~530s); `-race` green module-wide in a
Linux container.

**This is the direct sequel to yesterday's slice, and it is the iteration where the guard lesson
finally closed - not by patching a seventh evasion, but by changing what the guard counts.** Yesterday
recorded "a guard built from a mutation kill inherits the MUTATION's shape, not the DEFECT's" after
three rounds. This slice added four more evasions on top of those three, and then found the durable
form.

---

## The headline: seven evasions, then the fix, and the fix is generic

Count them in order, because the order is the finding.

**Two found by the engineer, before review, unprompted.** This is the first time in this batch that a
recorded lesson changed behaviour UPSTREAM of Phase 4 rather than being re-learned inside it.

- A handler assembling its own `map[string]any` with an IP in it. That defeats a type-level reflection
  walk **entirely**, and the reason is worth stating in general terms: a type walk certifies a TYPE,
  and a `map[string]any` is a type that certifies nothing about the BYTES written into it. The walk
  said "this is fine" about a container whose contents it had no opinion on.
- An `if os.Getenv(...)` wrapper around the wiring assignment, which made the whole control
  configuration-dependent while every check still passed.

**Two HIGHs found by Phase 4 anyway.** Proactive self-checking did not make the fan-out redundant.

- **The route-gating guard compared middleware NAMES rather than BINDINGS.** A block-scoped
  `admin := identity` inside `Handler()` left `GET /v1/server/counters` readable by **every
  authenticated non-admin**, with the whole repo green. The endpoint's entire authorization story was
  held by an AST check that could not tell a name from the thing it names.
- **The wiring guard was defeated four separate ways**: a pointer alias (`cs := &httpServer.Counters`),
  a sibling-file helper (a `CallExpr` is not an `AssignStmt`), a conditional `Wrap` producing a typed
  nil, and simply reordering the assignment past `ListenAndServe`.

**The remediation stopped patching and removed the surface.** `buildHTTPServer` returns `*http.Server`
rather than `*api.Server`, so `main` never holds the `api.Server`. There is nothing to alias, nothing
to mutate on a following line, nothing to reorder. Two of the four evasions became **compile errors**;
the other two became execution-checked by `TestBuildHTTPServer_ServesTheRealListenersCounters`, which
calls the function with a real `netlimit.Wrap`'d socket and reads the numbers back through the real
route. The function's own doc comment now separates the two categories explicitly - "IMPOSSIBLE TO
WRITE" versus "STILL WRITABLE, AND CHECKED BELOW" - because a reader who believes a shape is unwritable
has no reason to preserve the check that is in fact the only thing stopping it.

**Then re-verify found a FIFTH.** `grpcLis` was still an ordinary mutable local, so:

```go
grpcLis := netlimit.Wrap(grpcRawLis, cfg)
if bothCapsZero {
    grpcLis = nil
}
```

was green. The top-level seed still reached `Wrap`, so every check passed, while any deployment taking
that branch served an endpoint with no `grpc_admission` section at all - **an admission control that
reads as having never refused anything**. That is not an exotic mutation: "only wrap when a cap is
configured" is the exact optimisation the engineer had already considered and declined on merits,
which makes it the change a maintainer is most likely to attempt.

**The final fix is generic and it is the durable form.** Count `AssignStmt`s per identifier across
`main`'s **entire subtree** - ifs, loops, switches, closures - and require exactly one for every name
on the reachability chain. Not "look for this shape". Count a property. The engineer then found a
**SIXTH** evasion one level up, on the `srv` binding itself (the `ListenAndServe` check matches on
name, so a conditional `srv = &http.Server{...}` would serve an unwired server through an identifier
the test had already blessed) - and the same generic check kills it with no new code.

> **The durable rule: a guard that matches a SPELLING is evadable by respelling; a guard that counts a
> PROPERTY is not. Prefer EXECUTING the code to parsing it, and where you must parse, count
> properties.** Seven evasions across two slices is not a sample worth arguing with. The escalation
> ladder that actually worked, in order of preference: make it unwritable (change the return type),
> then execute it (a real socket through the real route), then count a property (assignments per
> identifier), and only then match a shape.

---

## Two acceptance criteria were met by REMOVING the thing being guarded

`buildHTTPServer` is the instance and it deserves its own heading, because it is a move rather than an
incident.

Criteria 10 (the wiring is guarded structurally, surviving the three evasions that beat yesterday's
guards) and the implicit "the endpoint is actually reachable in production" were both closed not by a
better guard but by deleting the surface the evasions were expressible on. Four shapes stopped being
*checked* and started being *impossible*.

> **When a guard keeps being evaded, the question is not "how do I check this better" but "what surface
> makes the evasion expressible at all".** A guard is a tax paid forever on a shape you chose to allow.
> Sometimes the shape is the mistake.

---

## The behavioural 403 was reachable in the DEFAULT lane and nobody had checked

The plan recorded, correctly, that go-ci runs `go test -race ./...` with **no integration tag**. It
then placed the admin-gating proof behind `//go:build integration`, so CI could not see the one test
that proves an authenticated non-admin gets a 403 from an endpoint serving adversary-activity numbers.
The justification was the usual one: the behavioural test needs a database, so it needs Docker.

**It did not.** A fake `DBTX` serving only `GetTokenWithUser` is enough - `stubTokenDB` plus
`serverWithStubAuth(bool)`, about forty lines - and `TestServerCounters_NonAdminIsForbidden` /
`TestServerCounters_AdminReadsThePayload` now run through `srv.Handler()` in the default lane. The stub
even self-checks its own assumption: it counts the `bool` destinations in `GetTokenWithUserRow` and
fails loudly if the row shape changes, rather than silently deciding the admin gate on the wrong column.

That pair then killed a mutation a name-check **structurally cannot see**: `if false && (!ok || !u.IsAdmin)`
inside `AdminOnly` itself. The AST guard is looking at the registration site; the defect is in the
middleware. Execution does not have that blind spot.

> **A test placed behind a build tag on the strength of "it needs Docker" should be checked against
> whether it actually does.** The cost of getting this wrong is not a missing test - it is a test that
> exists, passes locally, and is invisible to the gate that decides whether the branch merges.

---

## A CI-flake blocker that three parties reproduced and the engineer's own 10/10 had missed

`TestStats_IsOneCriticalSection` failed **30/30 at `GOMAXPROCS=1`** and **6/6 at `-cpu=1`**. The
engineer's own verification was 10/10 green, on a multi-core box.

The failure was on the **vacuity guard**, not on the invariant, and that distinction is the whole
lesson. The reader is an unsynchronised busy-spin; on one CPU the churn goroutine runs its entire
admit/release loop inside a single scheduling slice, so every snapshot the reader takes is of an empty
listener. Logged output: `412628 snapshots; 0 saw live connections`. The test spun roughly a million
times, observed nothing, and **correctly reported that it had proved nothing**. That is the guard
working exactly as designed - the recorded "measure the populated state" lesson, paying off - and it
was still a hard blocker, because a 1-CPU cgroup would fail the build every time.

Fixed structurally rather than by adding iterations: admit two sources BEFORE the churn starts and
release them after it finishes, putting a floor of `LiveTotal >= 2, DistinctSources >= 2` under every
snapshot regardless of scheduling.

**The honest asymmetry is now in the test's own comment, and it is uncomfortable.** The fix is 30/30
at `-cpu=1`. The three-lock KILL - the mutation the test exists for - is **0/10 at `-cpu=1` and 10/10
at `-cpu=2`**. CI is `ubuntu-latest` with no `-cpu` flag on a 2-4 vCPU runner, so the kill is live
where it matters. But:

> **A test can be robust and inert on the same machine.** Passing is not evidence of discriminating.
> The comment says which property needs more than one CPU and which no longer does, so the next person
> who runs it on a constrained box knows that green means "did not detect" rather than "verified".

---

## Prose that misidentifies what is LOAD-BEARING is worse than prose that is merely wrong

Nine consecutive iterations of "wrong prose about correct code is the dominant defect class" now has a
sharper variant, and both instances this slice are of the sharper kind.

**Instance 1: "the compiler forbids a read outside the lock" is false.** The two refusal counters were
converted from `atomic.Uint64` to plain `uint64` guarded by the listener's mutex, which is right - it
turns an unsynchronised access into a **data race** rather than a legal-but-inconsistent read. But Go
has no mutex-guard analysis. Adding
`func (l *Listener) unlockedRead() uint64 { return l.refusedTotal + l.refusedPerIP }` to that file
builds clean **and vets clean**.

Why it matters more than an ordinary wrong sentence: the actual enforcement is `-race` PLUS
`TestStats_ConcurrentRefusalsAndReadsShareTheMutex`, and a lens proved that test is the **sole** test
in the package that gives `-race` anything to see - with the increments moved back outside the lock,
every other test in `internal/netlimit` still reports `ok` under `-race`. So a maintainer who believes
the false sentence has **no reason to preserve that test**, and deleting it silently restores
comment-enforced coupling with `-race` still green. The field block's comment now says exactly this,
in capitals, and names the test as LOAD-BEARING.

**Instance 2: the endpoint's own doc still pre-blessed `swept_by_worker`** after the allow-list entry
had been deliberately deleted - in `server_counters.go`, the file the slice-4 implementer opens first.
Corrected to state the de-authorization and what re-authorizing costs.

> **Wrong prose about correct code is a defect. Prose that misidentifies WHICH artifact is load-bearing
> is a defect with a delayed detonator**, because it licenses the deletion of the thing actually holding
> the property, and the deletion is green.

---

## An allow-list that exempts a PATH exempts it from everything

Both payload guards - the reflection walk over the Go type tree and the walk over the marshalled JSON -
consulted the allow-list **before** the marshaller check and before the kind switch, then continued
without descending. So an allow-listed path was never inspected at all: not its kind, not its key
type, not its cardinality, and on the wire not its keys and not its values.

**Three lenses demonstrated it independently, and the demonstration is the point.** A
`map[string]string` placed at an allow-listed path, carrying a hostname-shaped key with an embedded
newline and an RTL override, and an IP address for a value, passed **BOTH** guards with zero failures.
Not theorised. Run.

The fix is a shape change to the exemption itself: `counterPayloadExemption{why, typeOK, jsonOK}`, so
`started_at` is exempt **as a `time.Time` serialising to an RFC 3339 instant**, not as a path. And the
pre-authorized `watchdog.counts.swept_by_worker` entry was deleted, because an entry written in slice 1
against code nobody had written had reduced its only forcing function to a one-line edit with the
justification already supplied.

**The residual is recorded rather than glossed:** an exemption is now shape-checked but still
**non-descending** - both walks stop at an exempted path once the predicate passes. That is exactly
right for `started_at`, whose shape is a scalar and whose predicate therefore examines the whole value.
It is **not** right for a container: a `jsonOK` that accepted `map[string]any` would leave every key
and value uninspected, which is the total exemption this mechanism just replaced, re-entered through
the predicate. A future container exemption must do the descending itself inside `typeOK`/`jsonOK`.

> **An exemption granted to a NAME is an exemption from every question.** Grant exemptions to shapes,
> check the shape, and state whether the check descends.

---

## Two engineer refutations were correct, and both were verified rather than accepted

Recorded because the standing lesson is "treat the previous stage's output as untrusted" and this is
that lesson pointed the other way: a refutation from a downstream stage is also an untrusted artifact.

**(a) No single-snapshot count-to-level assertion is possible.** Counts are monotonic and levels move
freely, so no arrangement of the five numbers in one snapshot is impossible enough to assert on. The
engineer wrote the relation test anyway to see what it bought, then measured it: it stayed **green**
under a "count the refusal before the cap check" mutation, while the existing suite killed that
mutation four other ways. The right call was made - it kept the test, **renamed** to
`TestStats_ConcurrentRefusalsAndReadsShareTheMutex` for the `-race` exposure it genuinely provides, and
corrected the comment rather than leaving it claiming a test pins a relation no test can pin. Which is
how instance 1 above got found.

**(b) It DECLINED to leave the section absent when both caps are 0.** The reasoning is the better half
of this slice's design thinking: making `absent` mean "not wired OR wired-but-disabled" would degrade
the vocabulary **permanently, for every future section**, in order to disambiguate one configuration.

**The honest residual, and it is a real one:** in that configuration the payload affirmatively asserts
"this control ran and stopped nothing" where the truth is "this control measured nothing". A **false**
statement rather than an ambiguous one. That is this slice's own subject one layer down, and it is
disclosed in three places (`netlimit.Stats`'s doc comment, `server_counters.go`'s doc comment, README)
rather than fixed. A backlog item is proposed below.

---

## The spec's sequencing disagreed with the roadmap anchor and was right

Worth recording that this was decided on merit, not on queue position.

The roadmap's top item was `idea-2026-08-20-repeated-watchdog-sweeps-...`. The spec argued slice 1
should be the mechanism plus netlimit occupancy instead, and gave four reasons that all held up: the
increment sites already existed and had been reviewed by a four-lens fan-out the day before; it was the
only one of the four with a **pre-existing discriminating test** (`TestRefusalSummaryLogsOnlyWhenCountersMove`,
whose `IsType(uint64(0))` arm is what forced occupancy to be `uint64` at the snapshot boundary); it
touches no SQL, no migration, no epoch-fenced write and no recv goroutine; and putting the hardest
cardinality class (item 1's capped per-worker map with an overflow counter) in the same PR as a
brand-new endpoint is what scope discipline forbids.

The spec also refuted, correctly, the "possible dependency on
`feature-2026-08-09-server-info-allowlist-endpoint`" line that appears in all four items. It is a
sibling under a shared `/v1/server/` prefix, not a consumer. **That false dependency is why this cluster
sat through three roadmap refreshes.**

---

## Process: the integration lane returned three non-answers before reporting

Recorded honestly because it is a gate-integrity issue, not a personality note.

The integration lane replied "Standing by for the background suite notifications" three times before
finally reporting in full. It was waiting on a genuinely long suite (`internal/api`, 295 tests, ~530s),
and its eventual result was correct and matched everything else. But:

> **A lane that reports nothing is indistinguishable from a lane that reports green if nobody reads
> carefully.** A verification lane must report OBSERVED OUTPUT, and a conductor must never accept
> "still waiting" as a gate result.

The conductor ran the suites itself in parallel and separately wrote its own real-socket probe rather
than waiting. That is the right response and it should not have been necessary.

---

## The unit tests all used a FAKE listener, so nobody had confirmed the numbers against real sockets

Every `netlimit` occupancy test drives `admit`/`release` directly against a `fakeListener`, which is
the right choice for making the arithmetic the subject. The consequence nobody stated until Phase 4:
**no test anywhere had confirmed the five numbers against a real TCP listener.**

Both the conductor's probe and the lane's committed test confirmed `live_total`, `distinct_sources`,
`max_per_source`, `refused_total` and `refused_per_source` are correct against real sockets, and
confirmed the both-caps-disabled disclosure **empirically** rather than by reading `Accept`.

`max_per_source` recomputing correctly on release is the one nobody would have caught by inspection -
it is an O(len(perIP)) scan whose result after a decrement is not derivable from its result before one,
which is precisely why the incremental-maximum alternative was rejected at spec time.

---

## What Was Built

- **`internal/netlimit`.** `Stats` splits into `Counts RefusalCounts` (monotonic, `==`-comparable) and
  `Levels Occupancy` (current, never compared). `Stats()` takes `l.mu` once and reads all five numbers
  in that one critical section, converting to `uint64` at the boundary with **no clamp** (`l.total`
  cannot go negative - `release` runs exactly once per admitted conn, enforced by `conn.once` - and an
  absurd number is a better signal than a zero that hides an accounting bug).
- **The two refusal counters are now plain `uint64` guarded by `l.mu`**, not `atomic.Uint64`. Read the
  field block's comment for what that buys (an unsynchronised access is a data race `-race` can see)
  and what it does NOT (the compiler enforces nothing; one named test is the sole `-race` exposure).
- **`Occupancy`'s doc comment states the trigger rule in capitals** and says the split is what makes it
  structural.
- **`refusalReporter.last` is typed `netlimit.RefusalCounts`**, so `s == r.last` no longer compiles.
  That, not the split by itself, is the mechanism. The line carries all five numbers when it speaks.
- **`internal/api/server_counters.go`** (new) - the response contract for all four sections, fixed
  before the first one shipped. Read its doc comment first: the counts/levels rule, absent-not-zero,
  `started_at` always present, per-replica semantics, the no-caller-supplied-bytes rule with the
  de-authorization of `swept_by_worker` spelled out, the two things the endpoint does NOT buy, and the
  import-direction note (`internal/scheduler` imports `internal/api`, so slice 4's snapshot type must
  be declared IN `internal/api`).
- **`CounterSources`'s comment covers the typed-nil trap explicitly**, because it is not hypothetical
  for slice 4: the watchdog is legitimately disable-able, so
  `var wd *scheduler.Watchdog; if enabled { wd = ... }; CounterSources{Watchdog: wd}` is the natural
  shape and it panics on a nil receiver per admin request - inside the feature whose subject is bounding
  log volume. Filter the typed nil at the wiring boundary where the CONCRETE type is still visible; do
  NOT make the snapshot method nil-tolerant, which would turn an unwired control into a section of
  zeros.
- **`cmd/relay-server/http_server.go`** (new) - `httpServerDeps` and `buildHTTPServer`, the only place
  either the `api.Server` or the `http.Server` is constructed. Returns `*http.Server`. Its comment names
  the two wiring mistakes still writable INSIDE it and caught by nothing: `api.New`'s four same-typed
  positional arguments in a row, and deletion of any of the three `Metrics`/`StaticHandler`/
  `AllowSelfRegister` assignments.
- **`GET /v1/server/counters`**, `auth(admin(...))`, registered as a direct statement of `Handler()`'s
  own body because the AST guard requires it to be one.
- **README** - a new Server subsection with the payload, the counts/levels rule, absent-not-zero,
  per-replica semantics, the counts-only rule stated as "argued by SHAPE in the commit that adds it",
  the NAT-versus-distributed reading, the `refused_per_source`-is-a-floor rule, and the
  both-caps-off disclosure.
- **Zero SQL, zero migration, zero proto, zero generated file, zero files under `web/`.**

## Key Decisions

- **Typed per-subsystem `Stats()` structs plus one admin endpoint** - not a registry, not Prometheus,
  not expvar. Called on reversibility: typed structs feed an exporter later at zero call-site cost and
  the reverse is not true. A string-keyed registry is the exact shape of a defect this repo already
  shipped once (`bug-2026-08-12-tasklog-err-limiter-attacker-keyed`).
- **`buildHTTPServer` returns `*http.Server`.** Two evasions become compile errors.
- **`refused_per_source`, not `refused_per_ip`**, in the JSON. The cap is keyed on a SOURCE, which is an
  exact IPv4 address but an aggregated `/64` for IPv6.
- **The both-caps-disabled case is documented in three places, not fixed.** Closing it in the payload
  needs either a boolean (banned by the counts-only rule) or the configured caps as fields - and
  `max_per_source` as an observed maximum sitting next to `max_per_source` as a configured cap is a
  naming trap. Filed as an item instead.
- **`RefusedPerIP`'s under-report is closed by documentation plus a derived signal, not by
  re-attribution.** When `live_total` has reached the configured `MaxTotal`, read `refused_per_source`
  as a floor. Changing a one-day-old counter's meaning is worse than explaining it.
- **`MaxPerSource` is sampled by a scan, not maintained incrementally.** A decremented maximum is not
  exactly recoverable without a scan, which would move the walk onto `release` - a path much closer to
  hot than `Stats()` is.
- **The scan's real bound is stated rather than the convenient one.** `len(perIP)` is bounded by
  `MaxTotal` only while the TOTAL cap is enabled; with `RELAY_GRPC_MAX_CONNS=0` and a live per-source
  cap - a configuration README discusses - it is bounded by the process file-descriptor limit. Priced
  and accepted, in `Stats()`'s comment.

## Findings Triage

- **2 guard evasions found by the ENGINEER before review** (the `map[string]any` type-walk defeat, the
  `os.Getenv` wrapper). First upstream payoff of a recorded lesson in this batch.
- **2 HIGHs from Phase 4** - the by-name route gate (authenticated non-admins could read the payload)
  and the four-way wiring-guard defeat.
- **1 more evasion found on re-verify** (`grpcLis = nil` inside an if), and **1 more found by the
  engineer** on the `srv` binding after the generic fix landed. Seven total across two slices.
- **1 CI-flake blocker** reproduced by three parties, 30/30 at `GOMAXPROCS=1`, missed by the engineer's
  10/10 on a multi-core box.
- **1 total-exemption defect in both payload guards**, demonstrated by three lenses independently with
  a runnable `map[string]string` carrying a newline-injected RTL-override key and an IP value.
- **2 prose defects, both of the load-bearing-misidentification kind.**
- **2 engineer refutations, both correct, both verified rather than accepted.**
- **1 build-tag placement error** (the admin gate proof invisible to CI's default lane).
- **0 findings against the shipped behaviour after remediation.**

## What Remains Open

Stated here rather than buried, because three of the four are deliberate residuals somebody will meet
later.

- **The both-caps-off payload makes a FALSE assertion, not an ambiguous one.** With
  `RELAY_GRPC_MAX_CONNS=0` and `RELAY_GRPC_MAX_CONNS_PER_IP=0`, `Accept` returns the connection
  unwrapped and does no accounting, so `grpc_admission.levels` reads all zeros with thousands of live
  connections. A wired section of zeros means "this control ran and stopped nothing" by the payload's
  own contract, and that sentence is untrue there. Disclosed in `netlimit.Stats`, in
  `server_counters.go` and in README. **Filed as an item** (see below).
- **The payload exemption mechanism is shape-checked but NON-DESCENDING.** Both walks stop at an
  exempted path once the predicate passes. Correct for `started_at`; wrong for any future container,
  whose `jsonOK`/`typeOK` must descend itself. Recorded in `counterPayloadExemption`'s comment. Not
  filed - there is no container exemption today, and the constraint is carried to slice 4 through the
  sibling item amendments instead.
- **`TestStats_IsOneCriticalSection` is robust and inert on the same machine.** Fixed 30/30 at
  `-cpu=1`; the three-lock kill is 0/10 at `-cpu=1` and 10/10 at `-cpu=2`. CI's 2-4 vCPU
  `ubuntu-latest` keeps the kill live. A constrained cgroup would stop detecting the mutation rather
  than fail. Recorded in the test's comment. Not filed: there is no action that does not amount to
  pinning `-cpu` for one test.
- **Slice 1 ENABLES but does not close items 1, 2 and 3 of the cluster.** All three stay open. It
  settles the read surface - the shared expensive part all three name - and nothing else. **None** of
  their acceptance criteria is met: each requires a counter proven by a handler-layer or scheduler-layer
  test, and this slice adds no counter to `internal/worker` or `internal/scheduler` at all. Anyone
  reporting that this slice "closed the observability cluster" is wrong.
- **Two wiring mistakes remain writable inside `buildHTTPServer` and nothing catches either.**
  `api.New`'s four same-typed positional arguments (swapping the login and register rate-limit pairs
  compiles and stays green everywhere), and deletion of any of the `Metrics`/`StaticHandler`/
  `AllowSelfRegister` assignments. Both are named in the function's own comment; the second is carried
  into `idea-2026-08-14-generalize-the-env-to-field-wiring-guard`.

## Improvement Goals

Carried forward:

- **Verify a backlog item's technical claims against the code** - honored, **seventeenth iteration**.
  The spec refuted the `metrics.Store` home on mechanism, item 3's own preferred option (b), item 1's
  "easier than the other two" framing, and the false `server-info` dependency in all four items.
- **A backlog proposal is not a contract** - seventeen for seventeen.
- **The verification chain is worth its length only if each stage treats the previous stage's output as
  untrusted** - honored in both directions this time: the plan refuted six spec claims, and the
  conductor verified two engineer refutations rather than accepting them.
- **A guard built from a mutation kill inherits the MUTATION's shape** - honored the hard way, four more
  times, and then **closed** by the generic form below.
- **A mutation proof must leave a test behind** - honored; every evasion left a permanent check, and the
  three-lock mutation left `TestStats_IsOneCriticalSection` with its asymmetry documented.
- **Measure the populated state** - honored, and it is what made the CI-flake blocker *visible* rather
  than silent. The vacuity guard did its job by failing.
- **Wrong prose about correct code is the dominant defect class** - **twelfth consecutive iteration**.
- **Say what a fix does not buy in the same sentence that says what it does** - honored; the endpoint's
  doc has a "WHAT THIS ENDPOINT DOES NOT BUY" block and README has the both-caps-off bullet.
- **Backlog housekeeping is required scope** - the close of the source item belongs to the conductor.

New from this iteration:

- **A guard that matches a SPELLING is evadable by respelling; a guard that counts a PROPERTY is not.**
  The escalation ladder: make it unwritable, then execute it, then count a property, then match a shape.
  **Candidate for durable memory** - it is the terminal form of the mutation-shape lesson recorded
  yesterday, arrived at after seven evasions across two slices.
- **When a guard keeps being evaded, ask what surface makes the evasion expressible at all.** Two
  acceptance criteria here were met by removing the surface rather than by guarding it better.
- **A test placed behind a build tag "because it needs Docker" should be checked against whether it
  actually does.** A fake `DBTX` serving one query moved the admin-gate proof into the lane CI runs.
- **Prose that misidentifies WHICH artifact is load-bearing is worse than prose that is merely wrong.**
  It licenses deleting the thing holding the property, and the deletion is green.
- **An exemption granted to a NAME is an exemption from every question.** Grant to shapes, check the
  shape, and state whether the check descends.
- **A test can be robust and inert on the same machine.** Passing is not evidence of discriminating;
  say which property needs which environment.
- **A verification lane must report observed output, and "still waiting" is not a gate result.**

## Files Most Touched

- `cmd/relay-server/http_server.go` - read `buildHTTPServer`'s doc comment in full. The
  IMPOSSIBLE-TO-WRITE versus STILL-WRITABLE-AND-CHECKED split is the headline lesson written where the
  next person to refactor this will hit it, and the two uncaught wiring mistakes are named in the same
  place.
- `cmd/relay-server/counters_wiring_test.go` - `TestServerCountersIsWiredByMain`'s "EVERY NAME ON THAT
  CHAIN, PLUS THE SERVER BINDING, MUST BE ASSIGNED EXACTLY ONCE" block is the generic property check
  and the `grpcLis = nil` shape it kills.
- `internal/netlimit/listener.go` - the `Listener` field block's comment (what plain fields buy over
  atomics, and which single test is the sole `-race` exposure) and `Stats()`'s comment (the single
  critical section as a contract, the real scan bound, and the both-caps-off disclosure).
- `internal/netlimit/listener_test.go` - `TestStats_IsOneCriticalSection`'s comment for the `-cpu`
  asymmetry, and `TestStats_ConcurrentRefusalsAndReadsShareTheMutex` for what it does and does not cover.
- `internal/api/server_counters.go` - the payload contract for all four slices, the `swept_by_worker`
  de-authorization, and the import-direction note slice 4 needs.
- `internal/api/server_counters_test.go` - `TestServerCountersRouteIsAdminGated`'s comment ("Names are
  not bindings") and `counterPayloadExemption`'s comment (the total-exemption defect and the
  non-descending residual).
- `README.md` - the Server counters subsection, lines around the payload example.
- `docs/superpowers/specs/2026-08-21-silent-drop-observability.md` - sections 5.1, 6.2, 7 and 10 are the
  reusable parts, and section 17 is the backlog effects this pass acted on.
- `docs/superpowers/plans/2026-08-21-silent-drop-observability-slice1.md` - R1-R6, the spec refutations,
  of which R2 (the `scheduler -> api` import direction) is the one slice 4 must not rediscover.

## Verification

- **This pass had no shell.** Bash was unavailable to the TPM lane; nothing was executed. No `git log`,
  no `git diff`, no test run. Every claim below that could be checked by reading was checked against the
  worktree.
- **Verified by reading:** `internal/netlimit/listener.go` (the `Listener` field block lines 120-152 and
  `Stats()` lines 240-292, confirming plain `uint64` counters read under `l.mu` and the single critical
  section); `internal/netlimit/listener_test.go`'s `TestStats_IsOneCriticalSection` comment (the 30/30
  `GOMAXPROCS=1` and 0/10 versus 10/10 `-cpu` figures) and
  `TestStats_ConcurrentRefusalsAndReadsShareTheMutex`; `internal/api/server_counters.go` in full;
  `internal/api/server_counters_test.go`'s test-name list, `serverWithStubAuth`/`stubTokenDB`,
  `TestServerCounters_NonAdminIsForbidden`, `TestServerCounters_AdminReadsThePayload`,
  `TestServerCountersRouteIsAdminGated` in full, and `counterPayloadExemption`'s comment;
  `internal/api/server.go`'s `Counters` field and the route registration at line 161;
  `cmd/relay-server/http_server.go` in full; `cmd/relay-server/counters_wiring_test.go`'s three test
  names and `TestServerCountersIsWiredByMain` in full; `cmd/relay-server/trailing_log_window_test.go`'s
  "Parse the package, not the file" constraint; README lines 1255-1278; the spec in full; the plan's
  verification section and Tasks 1-5; the closing item in full; the three sibling items; and the
  2026-08-21 grpc-admission retro for structure.
- **Confirmed against code, not inferred:** the default-lane placement of the behavioural admin gate
  (`server_counters_test.go` is `package api` with no build tag); the route is registered
  `auth(admin(...))` as a direct statement of `Handler()`'s body; `buildHTTPServer` returns
  `*http.Server` and `main` therefore holds no `*api.Server`; the allow-list contains `started_at` and
  no `swept_by_worker` entry.
- **Reported by the implementing and verifying lanes, not re-run here:** all suite counts (unit
  554 -> 574 top-level, five integration packages green, `internal/api` 295 tests / ~530s), the
  module-wide `-race` result from the Linux container, every mutation result including the seven guard
  evasions and the `if false &&` kill, the 30/30 and 6/6 flake reproductions, the three-lens
  `map[string]string` demonstration, and the real-socket probe results.
- **Not verified:** all test results, the commit set and diff stat, and the change set as `git` sees it.
  Each is attributed above.
- **No PR number appears anywhere in this retro or in the filed items**, by instruction: the PR does not
  exist at the time of writing and a predicted number is wrong the moment a concurrent session opens one
  first. The work is referenced by date and slug.
- **Outstanding and belonging to the conductor:** the close of
  `idea-2026-08-21-netlimit-occupancy-is-unobservable` (`/backlog close`, never a hand-edited
  `status:`), the exact-file-set check, the final gates, all commits, and a ROADMAP refresh.
