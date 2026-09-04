---
title: Generalize the env-to-field wiring guard - Metrics, AllowAutoEnroll and two duration knobs have the identical untested seam
type: idea
status: open
created: 2026-08-14
updated: 2026-08-21
priority: low
source: Phase 4 of the 2026-08-14-tasklog-terminal-append-bound slice; the new guard's own comment names this item and says "do not generalize here"
---

# Generalize the env-to-field wiring guard

## Summary

`cmd/relay-server/main.go` configures `worker.Handler` by assigning exported fields after
construction:

```go
agentHandler := worker.NewHandlerWithGrace(...)
agentHandler.Metrics = metricsStore
// ... parse RELAY_TASKLOG_TRAILING_WINDOW ...
agentHandler.TrailingLogWindow = trailingLogWindow
// ... parse RELAY_ALLOW_AUTO_ENROLL ...
agentHandler.AllowAutoEnroll = allow
```

**Deleting any one of those assignment lines compiles, and leaves the entire test suite green.**
Nothing constructs `main()`. The field falls back to its zero value (or its documented default), the
feature quietly stops working, and every gate in the project stays green - including
`go build ./...`, `go vet -tags integration ./...`, `go test ./...` and all four integration suites.

The 2026-08-14 slice measured this rather than assuming it, then closed the gap for exactly one
field. `cmd/relay-server/trailing_log_window_test.go` now carries
`TestTrailingLogWindowIsWiredIntoTheHandler`, a `go/ast` structural guard that parses `main.go` and
asserts something derived from `parseTrailingLogWindow` reaches a `.TrailingLogWindow` field. Its own
comment says:

> `agentHandler.Metrics` and `agentHandler.AllowAutoEnroll` have the identical gap ... so this guard
> is worth generalizing rather than pasting a third time. The conductor is filing that as its own
> item - do not generalize here.

This is that item. Not filing it would leave a source comment pointing at nothing.

## Context

Three points that shape the design, all verified:

- **The seam is real and measured.** The correctness lens deleted the six-line wiring block and ran
  every package: 20 green. That is what upgraded "no test covers this" from a plan footnote to a
  remediation.
- **The pattern already existed.** `internal/store/incrementtaskretrycount_guard_test.go` and
  `internal/store/updatetaskstatusepoch_guard_test.go` are structural guards over source. The new one
  is the third in the repo, and this project's own **extract-before-the-third-consumer rule just fired
  on the cursor-pager item** (`idea-2026-08-13-cursor-pager-hook`, closed 2026-08-14).
- **It must not be a regex.** A source-scanning regex guard in `web/` was proven breakable by a single
  JSX comment (`docs/retros/2026-08-13-narrow-viewport-overflow.md`), and the fix there was to delete
  the guard in favour of something the compiler enforces. The new guard uses `go/ast` for that reason
  and any generalization must keep that property.

### Update 2026-08-21 - PARSE THE PACKAGE, NOT THE FILE. This is no longer a `main.go` problem.

The `2026-08-21-silent-drop-observability` slice 1 moved the **`api.Server`'s own after-construction
wiring - `Metrics`, `StaticHandler`, `AllowSelfRegister` - out of `main` and into `buildHTTPServer` in
the new `cmd/relay-server/http_server.go`.** Both structural guards in this package call
`parser.ParseFile(fset, "main.go", nil, 0)`:

- `TestTrailingLogWindowIsWiredIntoTheHandler` (`trailing_log_window_test.go`)
- `TestServerCountersIsWiredByMain` (`counters_wiring_test.go`)

**A generalization written against `main.go` alone would report clean while covering none of those
three assignments.** Use `parser.ParseDir` (or `packages.Load`) over `cmd/relay-server` and walk every
non-test file, or the guard's own name becomes the wrong prose about correct code that this project
keeps finding. The constraint is already recorded in `trailing_log_window_test.go`'s comment; it is
repeated here so the item carries it.

**Measured, not assumed, in the same slice: deleting ANY of `s.Metrics = d.metrics`,
`s.StaticHandler = d.static` or `s.AllowSelfRegister = d.allowSelfRegister` from `buildHTTPServer`
leaves all three packages green today.** That is the same seam as the three `agentHandler` fields, now
in a second file, which raises the copy count from three to six and makes the "generalize rather than
paste" argument stronger rather than weaker.

**A second, harder gap in the same function, discovered by the same slice and NOT covered by any guard
this item proposes.** `api.New` is positional and takes **four same-typed arguments in a row**:

```go
s := api.New(d.pool, d.q, d.broker, d.registry, d.corsOrigins,
    d.loginLimitN, d.loginLimitWin, d.registerLimitN, d.registerLimitWin)
```

Swapping the login pair with the register pair **compiles, and every package stays green**; login would
then be rate-limited at the registration budget. The named fields on `httpServerDeps` make the CALL SITE
in `main` readable and do nothing for the four positions inside `buildHTTPServer`. A derivation guard
cannot see this at all - both values ARE derived from something plausible. **This is the strongest
argument yet for the constructor-argument or functional-options route below**, and it should be weighed
as part of this item rather than filed separately, because the two remedies are alternatives to each
other.

**And a shape lesson from that slice worth applying to whatever this item ships.** Seven separate
evasions of two structural guards were run to green across the 2026-08-20 and 2026-08-21 slices. The
form that finally held is generic rather than shape-matching: **count `AssignStmt`s per identifier
across the whole function subtree and require exactly one for every name on the reachability chain.**
A guard that matches a spelling is evadable by respelling; a guard that counts a property is not. In
particular, a derivation walk that only asks "was this name EVER assigned something mentioning X" is
defeated by a later `name = nil` inside an `if`. Prefer executing the code to parsing it; where you
must parse, count properties. See `TestServerCountersIsWiredByMain`'s "EVERY NAME ON THAT CHAIN" block
for the working implementation, and `docs/retros/2026-08-21-silent-drop-observability-slice1.md` for the
seven evasions.

### Update 2026-08-21 (later) - the conditional pair is a SEVENTH and EIGHTH copy, and slice 3 adds a ninth

Slice 2 (`docs/retros/2026-08-21-silent-drop-observability-slice2.md`) added a second section to the
counters endpoint, and with it a second conditional assignment in the same function:

```go
if d.grpcAdmission != nil {
    s.Counters.GRPCAdmission = d.grpcAdmission
}
if d.agentHandler != nil {
    s.Counters.IngestLogBudget = d.agentHandler
}
```

These are the **same unguarded-copy shape** as the three unconditional assignments above, with one
wrinkle in each direction. Better: deleting either one is caught today, by an executed test rather than
a parse (`TestBuildHTTPServer_ServesTheRealListenersCounters`,
`TestBuildHTTPServer_ServesTheWiredHandlersIngestSection`) - which is the ladder's top rung and exactly
what this item should prefer where it is available. Worse: the `if` is load-bearing (it filters the
typed nil, and removing it panics per admin request), so the shape a generalized *derivation* guard
would look for is not the shape that is written, and a third section arriving with no assignment at all
is caught only by the hand-maintained `wiredDep` cardinality check in `counters_wiring_test.go`.

**The copy count in `cmd/relay-server` is now eight - six unconditional and two conditional - and
`idea-2026-08-14-tasklog-fence-rejection-is-unobservable` (slice 3) adds a ninth**, on the same
`*worker.Handler`. That item's amendment already warns that the natural way to satisfy the cardinality
check is a duplicated row, which is the evasion the check was rewritten to stop.

**What this changes for this item's design, concretely:**

- The guard must handle an assignment nested inside an `if` **without** treating the nesting as the
  defect: here it is required. Counting assignments per identifier across the subtree still works;
  matching "an `AssignStmt` at statement level of the function body" does not.
- **Prefer extending the executed checks over widening the parse.** Two of the eight copies are already
  covered by executing `buildHTTPServer` and reading the result; a generalization that replaces those
  with an AST rule is a step DOWN the ladder. The right target is the six that nothing executes.
- Whatever ships should decide, once, whether `s.Counters.X = d.x` inside a filter belongs in the same
  table as `s.Metrics = d.metrics`, or whether the counters wiring is a separate mechanism with its own
  guard. Both answers are defensible; leaving it implicit is what produces the ninth copy.

### Update 2026-08-21 (slice 3) - the OPPOSITE POLARITY belongs here too: `srv.Handler` is reassignable and nothing sees it

Slice 3 (`docs/retros/2026-08-21-silent-drop-observability-slice3.md`) landed the ninth copy as
predicted, and did it **without** a new deps field: `s.Counters.TaskLogFence = d.agentHandler` sits under
the same `if d.agentHandler != nil` as `IngestLogBudget`, so the count in `buildHTTPServer` is nine
assignments over two conditional filters. **Better news than that: part of slice 1's parse-based
checking was replaced by executed checking**, which is this item's own stated preference confirmed by
events rather than asserted. `TestBuildHTTPServer_EverySourceFieldProducesAServedSection` builds a real
server with every source wired and counts served top-level keys against
`NumField(api.CounterSources)`; the hand-maintained string relation it replaced was **proved
decorative** - a fourth section wired end to end except for the assignment went green module-wide once
one token was appended to an existing row. **Whatever this item ships must not trade those executed
checks back for a uniform parse.**

**The new gap, and it is the reason this update lands here: `cmd/relay-server/main.go` can UNWIRE the
entire HTTP API after `buildHTTPServer` returns, and every guard in the package is blind to it.**

```go
srv := buildHTTPServer(httpServerDeps{...})   // main.go:215
if os.Getenv("RELAY_SOMETHING") != "" {
    srv.Handler = http.NewServeMux()          // compiles; cmd/relay-server stays green
}
// ...
if err := srv.ListenAndServe(); ...           // main.go:277
```

- **`TestServerCountersIsWiredByMain`'s assignments-per-identifier count does not see it, by design.**
  Its own comment says so: *"Field assignments (`agentHandler.Metrics = ...`) have a SelectorExpr on the
  left rather than an Ident, so they are deliberately not counted: they mutate the object, they do not
  rebind the name."* That exclusion is right for its question and wrong for this one.
- **`main.go:208-214`'s comment overstates the win.** It says building the server there "removes the
  whole class of ordering and mutation mistakes the old wiring had: main never holds the api.Server, so
  there is nothing to unwire after the fact and no 'must come before serving' constraint left to get
  wrong." True of the `*api.Server`. **False of the `*http.Server`'s `Handler`**, which is the entire
  API surface and an ordinary settable field of a stdlib struct - the return-type change moved the
  surface, it did not delete it. That sentence is a live instance of the project's dominant defect class
  and should be corrected by whatever ships here.
- **The blunt form above would be noticed in a minute** - it removes every route. **The realistic form
  is a middleware wrap that drops or shadows ONE route**, which is green everywhere and produces a
  deployment where `/v1/server/counters` (or any other single endpoint) silently 404s while every
  section it would have served is correctly wired.

**Why it belongs to this item rather than to a new one.** The polarity is inverted - every other row
here asserts an assignment must EXIST, this one asserts an assignment must NOT - but the subject, the
file, the walk and the claim family are identical: *post-construction mutation in `cmd/relay-server`
that nothing checks*. The remedy is one more rule in the same `ast.Inspect` over the same package
(collect `AssignStmt`s whose LHS is a `SelectorExpr` rooted at a name on the reachability chain, and
require none for `srv`). A standalone item whose whole content is "add one rule to a guard that this
item is about to rewrite" produces two open items that conflict on one file. **This item's Acceptance
list already asks which assignments are in scope; this makes that question concrete rather than adding
a tenth copy to count.**

**It is shippable on its own in roughly fifteen lines even if the rest of this item stays deferred**, and
that is deliberate: it must not be held hostage to the constructor-versus-guard decision, which is the
expensive open question here. It has its own Done-When bullet below.

## Proposal

One table-driven guard in `cmd/relay-server` covering every post-construction wiring the binary
depends on, replacing the single-purpose one rather than sitting beside it:

```go
cases := []struct{ file, field, derivedFrom string }{
    {"main.go",        "Metrics",           "metricsStore"},       // or NewStore, whichever names the source
    {"main.go",        "AllowAutoEnroll",   "RELAY_ALLOW_AUTO_ENROLL"},
    {"main.go",        "TrailingLogWindow", "parseTrailingLogWindow"},
    {"http_server.go", "Metrics",           "d.metrics"},          // 2026-08-21: api.Server, not worker.Handler
    {"http_server.go", "StaticHandler",     "d.static"},
    {"http_server.go", "AllowSelfRegister", "d.allowSelfRegister"},
}
```

Points to settle at spec time:

- **Parse the package, not the file** (2026-08-21). The `file` column above is illustrative; the guard
  should discover non-test files rather than hardcode two names, or the next extraction repeats this.
- **What "derived from" should mean per row.** The shipped guard walks assignments transitively: it
  collects `name -> identifiers its RHS mentions`, then follows `x := f(...)` into `h.Field = x`. That
  is the right shape and it should be lifted verbatim rather than reinvented. **(2026-08-21: lift the
  assignment-count check with it. Derivation alone is defeated by a later conditional reassignment, and
  that was a live green evasion, not a hypothetical.)**
- **Whether the two `s.Counters.X = d.x` assignments belong in this table at all** (2026-08-21, later).
  They are the same shape, they are already covered by executed tests, and their `if` wrapper is
  required rather than suspicious. Decide it explicitly; the ninth copy arrives with slice 3.
- **Whether the FORBIDDEN-assignment rule belongs in the same mechanism as the REQUIRED-assignment
  rows** (2026-08-21, slice 3). They share a walk and a package and nothing else; one table with a
  polarity column is one option, two adjacent tests is another. Decide it rather than letting the
  `srv.Handler` rule attach itself to whichever row is nearest.
- **The two limitations the shipped guard has, which a generalization should decide about rather than
  inherit silently.** (1) It proves *derivation*, not *fidelity* - `TrailingLogWindow =
  trailingLogWindow / 2` passes. (2) It keys on the field **name** only, so an assignment to any
  object carrying that field name satisfies it. Both are acceptable for "the wiring was not deleted";
  neither is stated in the test's own name, which claims `...IsWiredIntoTheHandler`. Either tighten
  (match the receiver identifier too) or say so in the comment.
- **Whether the guard should also cover `internal/api`'s equivalent seams.** `Server` has several
  exported knobs (`RegisterLimitN`, `LoginLimitWin`, and friends) set the same way. **(2026-08-21:
  answered by events - three of them now live in `buildHTTPServer`, so the guard is about the
  cmd/relay-server PACKAGE rather than about `worker.Handler`, which is the better framing and changes
  the file it lives in.)**
- **Whether a constructor-argument refactor would be better than any guard.** Passing these as
  arguments to `NewHandlerWithGrace` would make the compiler enforce them and delete the guard
  entirely. It was rejected in the trailing-window slice as pure churn (every test in
  `internal/worker` constructs a handler), and that reasoning is still sound - but it should be
  re-checked once, deliberately, rather than assumed forever. A functional-options constructor is the
  middle path and has its own cost. **(2026-08-21: re-weigh this HARDER. `buildHTTPServer` closed four
  guard evasions by changing its return type so the evasions became unwritable, which is the same move
  one level up. And `api.New`'s four same-typed positional arguments are a defect class NO derivation
  guard can see, so a guard cannot be the whole answer here anyway.)** **(2026-08-21, slice 3: note the
  limit of that move. Returning `*http.Server` removed the `api.Server` from main's reach and left the
  `http.Server`'s own `Handler` field writable, so "make it unwritable" bought a smaller surface rather
  than none. Weigh the constructor route knowing that.)**

## Acceptance / Done When

- One guard test covers every post-construction assignment in the `cmd/relay-server` package - both
  `main.go`'s three `agentHandler` fields and `http_server.go`'s three `api.Server` fields - and the
  single-purpose `TestTrailingLogWindowIsWiredIntoTheHandler` is removed rather than left as a fourth
  copy.
- **The guard parses the PACKAGE, not one named file**, and adding a seventh wiring in a new file of the
  same package is covered without editing the guard's file list.
- Each row is proven by deleting its wiring line and observing that guard row - and only that row -
  go RED. A guard that passes with the wiring deleted is worse than no guard.
- **Each row is also proven against a later conditional reassignment** (`name = nil` inside an `if`),
  not only against deletion. Derivation without an assignment count is defeated by exactly that shape.
- The guard is structural (`go/ast`), never a regex or a string scan.
- Its stated claim matches what it checks: whatever it does not prove (fidelity of the value, identity
  of the receiver) is written in its comment.
- It stays untagged, so it runs under `make test`.
- **`api.New`'s four same-typed positional arguments are either covered or explicitly declared out of
  scope with the reason**, since no derivation guard can see a swap between them.
- **(2026-08-21) The two `s.Counters.X = d.x` conditional assignments are explicitly in scope or
  explicitly out**, with the reason - they are the same shape, they are already covered by executed
  tests, and their count is growing.
- **(2026-08-21, slice 3) `srv.Handler` cannot be reassigned after `buildHTTPServer` returns without a
  test going RED**, proven by inserting `srv.Handler = http.NewServeMux()` inside an `if` in `main` and
  observing the failure. The assignments-per-identifier count deliberately ignores `SelectorExpr`
  left-hand sides, so this needs its own rule. **This bullet is shippable independently of every other
  bullet here** and should not wait on the constructor-versus-guard decision.
- **(2026-08-21, slice 3) `main.go:208-214`'s "nothing to unwire after the fact" comment is corrected**
  to say what the return-type change actually bought: main holds no `*api.Server`, and the
  `*http.Server`'s `Handler` field remains writable and is checked by the rule above.
- **(2026-08-21, slice 3) The executed checks are preserved, not replaced.**
  `TestBuildHTTPServer_EverySourceFieldProducesAServedSection` and the per-section presence tests answer
  their questions by running the code; a generalization that swaps them for AST rules for the sake of one
  table is a step down the ladder and must be rejected explicitly.

## Related

- Source: `cmd/relay-server/main.go` (the three `agentHandler` assignments; the `srv :=
  buildHTTPServer(...)` binding at `:215`, the `ListenAndServe` at `:277`, and the `:208-214` comment
  that overstates what the return-type change bought),
  `cmd/relay-server/http_server.go` (`buildHTTPServer`'s three `api.Server` assignments, the two
  conditional `s.Counters.X` filters now carrying three assignments, and its comment naming both
  uncaught gaps), `cmd/relay-server/trailing_log_window_test.go`
  (`TestTrailingLogWindowIsWiredIntoTheHandler`, the one to generalize, and the "Parse the package, not
  the file" constraint), `cmd/relay-server/counters_wiring_test.go` (`TestServerCountersIsWiredByMain`,
  the working assignment-count check and its deliberate `SelectorExpr` exclusion;
  `countersAssignmentSources`, the walk over `buildHTTPServer`'s own assignments;
  `TestBuildHTTPServer_EverySourceFieldProducesAServedSection`, the executed completeness relation that
  replaced a decorative one)
- Existing structural guards to follow: `internal/store/incrementtaskretrycount_guard_test.go`,
  `internal/store/updatetaskstatusepoch_guard_test.go`
- Why not a regex: `docs/retros/2026-08-13-narrow-viewport-overflow.md` (a compliant consumer reddened
  by one JSX comment; the guard was deleted and replaced with a required prop)
- Why a guard must count a property rather than match a shape, with worked evasions:
  `docs/retros/2026-08-21-grpc-admission-bounds.md`,
  `docs/retros/2026-08-21-silent-drop-observability-slice1.md`,
  `docs/retros/2026-08-21-silent-drop-observability-slice2.md` (three more, twelve total),
  `docs/retros/2026-08-21-silent-drop-observability-slice3.md` (a guard that counted a property and
  counted the WRONG one, with a failure message that invited the evasion)
- The item that added the ninth copy: [[idea-2026-08-14-tasklog-fence-rejection-is-unobservable]]
  (**closed 2026-08-21**)
- The item that adds the tenth: [[idea-2026-08-20-repeated-watchdog-sweeps-against-one-worker-are-unsurfaced]]
- The rule that says three copies is the trigger: `docs/retros/2026-08-14-cursor-pager-hook.md`
- Origin: `docs/retros/2026-08-14-tasklog-terminal-append-bound.md` ("The conductor override")

## Notes

Filed at **low** priority on purpose. Nothing is broken today; all three assignments are present and
correct. The value is that the next knob added to `Handler` gets its wiring covered for free instead
of arriving with a fourth copy of the same test or - much likelier - with no test at all, which is
the state all three of these were in until 2026-08-14.

**2026-08-21: still low, but the copy count went from three to six in one slice and the file set went
from one to two.** That is the shape of a guard that will be pasted a seventh time under time pressure.
Reconsider the priority the next time somebody adds a post-construction field in `cmd/relay-server`.

**2026-08-21, later: eight copies, and a ninth is scheduled.** Two of the eight are covered by executed
tests rather than by parsing, which is the better answer where it is available and is the thing this
item should be careful not to trade away for uniformity.

**2026-08-21, slice 3: nine copies, and the item now carries a second polarity.** Still low overall -
nothing is broken and the `srv.Handler` shape is latent rather than present - but the `srv.Handler`
bullet is the cheapest thing on this item's list and the only one that guards against a whole endpoint
disappearing rather than one knob. If this item keeps deferring, ship that bullet alone.

## 2026-08-24: a tenth copy, and slice 4 found a fail-open in the pattern itself

Slice 4 added the `watchdog` wiring and with it another copy of this guard family, plus two
structural changes worth folding into whatever generalization this item produces:

- **Construction and start split.** `main` used to write `go scheduler.NewWatchdog(...).Run(ctx)`,
  one statement carrying both. The counters endpoint needs the watchdog as a bound local above
  `buildHTTPServer`, so `TestWatchdogIsStartedByMain` now checks three things instead of two:
  exactly one unconditional construction binding a plain identifier, a `go <sameIdent>.Run(...)`
  that is a direct child of a function body, and both naming the same watchdog. "Exactly one"
  rather than "at least one" was forced by measurement - a last-wins walk passed a second
  construction started in place of the one `buildHTTPServer` reports on.

- **A positional check, because the guard was FAIL-OPEN on argument order.** `NewWatchdog`'s last
  two parameters are both `time.Duration`, so transposing them compiles and - measured - left
  `go test ./cmd/relay-server/...` **green**. In production that is margin 24h and maxAssignment
  30m: every assignment older than half an hour stamped `timed_out` within one tick, dependents
  cascaded to `failed`. The fix collects `*ast.BasicLit` string values alongside idents, so
  `Args[3]` must reach `parseWatchdogDuration` **and** `"RELAY_TASK_WATCHDOG_MARGIN"`, `Args[4]` the
  same plus `"RELAY_TASK_MAX_ASSIGNMENT"`, and neither may reach the other's name.

**The generalization must handle same-typed adjacent parameters**, or it regresses this. Any
env-to-field guard that proves "both bounds are wired" without proving *which is which* is fail-open
wherever two parameters share a type - and that is the common case for durations, sizes and counts.

Two evasions remain disclosed rather than closed in the slice-4 copy, and a generalization should
decide whether to close them: a `go func() { wd.Run(ctx) }()` wrapper is a false positive, and
reassignment of the bound variables between parse and call is unreachable by any scanner.

**`cmd/relay-agent` is a second unguarded package, and it now has a knob whose whole value
is observability.** Every guard this item describes lives in `cmd/relay-server`; the
agent's only tests are for its duration parser. Measured on the sync-heartbeat slice:
deleting the `SyncHeartbeatInterval:` or `FreeDiskGB:` assignment from the `perforce.Config`
literal in `main()` compiles and leaves every package green.

That slice closed the half it could reach - the provider's ticker seam now captures the
requested duration, so a hard-coded interval at the call site goes red - but the
env-var-to-`Config`-field hop is still unpinned, and a monitoring feature that is silently
absent is worse than one that is loudly broken.

## 2026-09-04: another copy, and a row already written in this item's shape

The change-password rate-limit slice added `passwordChangeLimitN` /
`passwordChangeLimitWin` to `httpServerDeps` and guarded main's literal with
`TestMain_PassesThePasswordChangeLimitItParsed`
(`cmd/relay-server/password_ratelimit_wiring_test.go`).

Three things that slice owes this item:

- **The guard was written as this item's table**, not as a bespoke walk: one row per wired
  field, with columns for the field, the function its value must derive from, and the env-var
  literal that distinguishes it from a same-typed sibling. Lifting it should be a matter of
  moving rows, not redesigning the walk. It also asserts that the statement immediately after
  the `ParseRateLimit` call is an `err != nil` branch that ends the process, because deleting
  that branch compiles and leaves every other check green.
- **A correctness note for whoever generalizes.** `TestServerCountersIsWiredByMain`'s
  derivation walk skips any assignment where `len(Lhs) != len(Rhs)`. Every rate-limit parse in
  `main` binds three names from one `api.ParseRateLimit` call, so that walk collects nothing
  for either name and a generalization built on it is RED on correct code. This was measured,
  not reasoned: substituting that filter into the new guard, against correct `main.go`, fails
  with `httpServerDeps.passwordChangeLimitN is fed "passwordChangeN", which does not derive
  from ParseRateLimit`. The arity-tolerant walk in `TestWatchdogIsStartedByMain` is the one to
  lift.
- **All four mutations this item cares about were run against the new guard, each with a
  discard added for the local it orphans where one was needed, and all four were killed**:
  the literal set to `0` (the plain-identifier assertion), the field fed another
  same-typed local `searchN` (the env-var-reachability assertion, not the other-env one - the
  chain does reach `ParseRateLimit`, so only the env-var name discriminates), the field
  omitted (the field-presence assertion), and a later `= 0` inside an `if` (the
  assigned-exactly-once assertion). Every executed `TestBuildHTTPServer*` test in the same
  package stayed green under the fourth, which is the clearest available demonstration of what
  the parse buys over execution here.

Note for whoever generalizes: THREE of those four do not compile in their naive form - the
zeroed literal, the crossed literal and the omitted field all orphan `passwordChangeN`; only
the later `= 0` inside an `if` builds. Two more rows of the wider battery for this slice are
non-compiling for the same reason on the other side: leaving the route bare `auth(...)`, and
substituting `userLimit` for `passwordLimit`, each orphan `passwordLimit` in
`internal/api/server.go`. A battery that records any of the five as a kill without adding a
discard for the orphaned local is recording build failures, not guard failures.

**Proposed, for the human rather than applied: raise this item from `low` to `medium`.** The
argument is this item's own Notes - reconsider the priority the next time somebody adds a
post-construction field in `cmd/relay-server` - and the fact that the next slice under time
pressure now has one more nearby copy to paste. The `priority:` frontmatter is deliberately
left untouched.
