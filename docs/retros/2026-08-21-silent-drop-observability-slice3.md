---
date: 2026-08-21
topic: silent-drop-observability-slice3
slice: 2026-08-21-silent-drop-observability (slice 3 of 4)
branch: claude/pr-merge-session-961184
range: origin/main..HEAD (backend only; Go plus README; zero SQL, zero migration, zero proto, zero generated file, zero files under web/; green, not yet merged, no PR opened at the time of writing)
closes: idea-2026-08-14-tasklog-fence-rejection-is-unobservable
enables-only: idea-2026-08-20-repeated-watchdog-sweeps-against-one-worker-are-unsurfaced
---

# Session Retro: 2026-08-21 - the word was wrong, not the premise; and a guard whose failure message invited the evasion it existed to prevent

**TL;DR:** one `atomic.Uint64` value field on `*worker.Handler`, incremented in `handleTaskLog`'s
existing named `pgx.ErrNoRows` arm before its return; a new `api.TaskLogFenceSource` returning a
scalar; a third `api.CounterSources` field fed by the `*worker.Handler` `cmd/relay-server` already
wires; and `task_log_fence.counts.rejected_total` on the admin-only `GET /v1/server/counters`. Unit
594 -> 603 top-level; all three touched integration packages green; `-race` clean module-wide in a
Linux container; `git diff origin/main...HEAD -- internal/store/` is **0 bytes**.

**This is the third slice of this batch and the fourth and final iteration of the autopilot run.** Slice
1's durable rule was "a guard that matches a SPELLING is evadable by respelling; a guard that counts a
PROPERTY is not". Slice 2 found "a hand-written copy between two types needs something comparing their
arity". This slice found the rung between them, and it is uncomfortable: **a guard can count a
property, and count the wrong one, and its own failure message can be the instruction manual for
evading it.**

---

## The headline: a prose refutation that changed the slice's HONESTY, not its code

The item said per-reason splitting is "structurally impossible within the one-round-trip constraint".
The joint spec said the same thing in section 3.2. Both were written by people who had read the SQL.

**The planner found the premise TRUE and the WORD false.** `AppendTaskLog`'s fence is a CTE
(`internal/store/query/tasks.sql`): a chunk failing any predicate matches no `fence` row, so `ins`
inserts nothing and the final `SELECT ins.id, ins.created_at, fence.job_id FROM ins, fence` returns
zero rows. There is genuinely nothing for a reason column to ride on. But a **one-round-trip variant
exists**: replace the `fence` CTE with a task CTE exposing the predicates as booleans and LEFT JOIN the
insert onto it, so the statement returns a row on the rejection path too.

It is declined **ON PRICE**, and the price is three specific things:

- it deletes the `pgx.ErrNoRows` signal that five statements' contracts, every caller, every comment
  and every test of this fence are written against;
- it makes `AppendTaskLogRow`'s three columns nullable on the success path, so the publish would have
  to re-derive "did the insert happen" from a NULL check - **a new way to publish an unstored chunk**,
  which is the one thing the arm's comment forbids absolutely;
- it puts a rewrite of the most security-sensitive statement in the repo inside an observability
  slice.

**The planner then required the comment to say "declined, and here is the price" rather than
"impossible".** That is the finding, and it is not pedantry:

> Shipping "impossible" would have put this project's **dominant recorded defect class** - wrong prose
> about correct code, fourteen consecutive iterations - inside the very comment that exists to stop the
> question being re-litigated. The comment's whole job is to be believed by someone who will not
> re-derive it. A false claim there is a claim with a permanent audience and no reader positioned to
> check it.

Both sites now read the corrected way, grep-verified:
`internal/worker/handler.go:1195-1213` ("IT IS ONE NUMBER AND NOT FOUR, AND THAT IS A PRICED DECISION
RATHER THAN AN IMPOSSIBILITY ... do not restate this as 'impossible' - it is not, it is declined") and
`internal/api/server_counters.go:284` ("DECLINED WITH THE PRICE WRITTEN DOWN - not impossible").

**Zero lines of shipped behaviour changed as a result.** The number is still one number, still
uncounted by reason, still an atomic add in the same arm. What changed is whether the next person who
asks the question is told a falsehood.

---

## A guard whose failure message INVITED the evasion it existed to prevent

The plan's R3 rewrote the `wiredDep` cardinality relation from "one row per deps field" to "N sections
over M deps fields", because this slice's third `CounterSources` field is fed by the **same**
`*worker.Handler` that already feeds `IngestLogBudget`. Each row gained a `sections []string` column
naming the `api.CounterSources` fields it feeds, and the relation counted distinct sections against
`NumField(api.CounterSources)`.

**Those strings were pure bookkeeping.** The AST walk consults only `d.field` and `d.mustReach`; it
never looks at `buildHTTPServer`'s body, so nothing ever compared a row's claimed sections against a
`s.Counters.<Section> = d.<field>` assignment anywhere.

**Both lenses found it independently, with different mutations, and both are measured rather than
argued:**

- **swapping which row claimed which section** - `grpcAdmission` claiming `TaskLogFence`,
  `agentHandler` not - left the package `ok`;
- **adding a whole FOURTH `CounterSources` field**, wired end to end *except* for the assignment in
  `buildHTTPServer` - its own response field, its own `handleServerCounters` branch, no new deps field,
  no call-site argument, **no `s.Counters.X = ...` anywhere** - was satisfied by **appending one string
  to an existing row** and left `go test ./...` green module-wide.

**It was a REGRESSION, and that is the part worth carrying.** Before the rewrite the relation was a
bijection between deps fields and sections, so a new section forced a new deps field, which then
inherited the plain-identifier check, the derives-from check and the assigned-exactly-once check. After
the rewrite a new section cost **one token in a string literal** and inherited **nothing**.

And the message said:

> `EVERY SECTION needs to be named by exactly one row`

**Following that sentence literally IS the evasion.** An author who adds a fourth section, sees the
count fail, and does exactly what the message asks, ships an unwired section with a green suite.

> **This is slice 2's own lesson inverted.** Slice 2 recorded "a guard's failure message must name what
> is WRONG, not the property being defended" - a message describing a satisfied condition sends the
> author to the wrong file. Here the message named a condition whose **literal satisfaction produces
> the defect**. The first wastes an hour. The second is an attack surface written in the second person.
> When you write a failure message, read it as an instruction and ask what the cheapest compliance is.

**The failure scenario was literally slice 4.** The fourth `CounterSources` field is the watchdog
section, and it is next.

---

## The fix went to the TOP rung and STRENGTHENED what it replaced

The remediation is not a better string check. `TestBuildHTTPServer_EverySourceFieldProducesAServedSection`
builds a **real** `buildHTTPServer` with every deps source wired - a real `netlimit.Wrap`'d socket and a
real `worker.NewHandler` - reads the payload back through the real admin-gated route, and counts the
**served top-level keys** against `1 + NumField(api.CounterSources)`.

Three things about how it landed:

- **One lens wrote and RAN it before proposing it.** Not "you should add a test": a test, executed,
  with its output.
- **It cannot be satisfied the way the string list could.** Passing the source in the fixture without
  assigning it in `buildHTTPServer` still leaves the section absent; assigning it without a
  `handleServerCounters` branch does too. Both halves of "wired" have to be real before the count comes
  out right. Its message says so: *"Fixing the fixture alone will NOT make this pass, which is the
  point."*
- **It catches the REVERSE mistake too** - a source field rendered but never assigned, which ships a
  permanently absent section reading as "not wired on this build".

**The engineer then added the second half, for the direction execution cannot cover.** A new *deps*
field feeding a section with no row would still get none of `main.go`'s identifier checks. So
`TestServerCountersIsWiredByMain` now reads `buildHTTPServer`'s OWN assignments
(`countersAssignmentSources`) and requires every deps field reaching `s.Counters` to have a row - a
property counted off the code, not a list maintained by hand. It **fails closed**: the assignment must
be spelled `d.<field>` exactly, so reaching a section through a local, a helper or a conversion is RED
rather than invisible.

That walk had a second effect nobody planned: it moved the **substituted-handler evasion** (slice 2's
forwarding gap, `s.Counters.X = worker.NewHandler(...)`) from integration-only into the **DEFAULT
lane** for its crude form. And the engineer then corrected the two now-stale prose claims that still
said the forwarding question was integration-only - catching what would otherwise have been a fresh
instance of the dominant defect class, in the same slice that opened with one.

> **When a guard is found to be decorative, the repair is not a stricter version of the same
> mechanism.** Execute the thing and count what comes out. Then ask which direction execution
> structurally cannot see, and guard only that by parsing.

---

## The epoch fence's own NAMED consequence was guarded only in a lane CI never runs

CLAUDE.md's epoch-fence bullet says `handleTaskLog` must drop a rejected chunk **before** publishing,
"or a zombie agent's output appears in a live view and then vanishes on refresh".

**Inserting an `h.broker.Publish` into the rejection arm left `go test ./internal/worker/...` GREEN.**
Measured. The only guard was `TestHandleTaskLog_StaleEpochIsNeitherStoredNorPublished`, whose file is
`//go:build integration`, and go-ci runs `go test -race ./... -timeout 180s` with **no tag**.

Pre-existing - slice 3 did not create it - but this slice built the default-lane harness (`stubFenceDB`,
a ~35-line `store.DBTX` returning a chosen error) that made the fix nearly free, so it was closed here
rather than filed.

**The fix is a subscription, and the subscription is load-bearing twice:**

- `Publish` on an unsubscribed broker is a map lookup that finds nothing, so **without a subscriber an
  added `Publish` on that arm is invisible** and `require.Empty(t, published())` would be vacuous;
- it **defeats `handleTaskLog`'s `HasLogSubscriber` short-circuit**, so the SUCCESS leg really reaches
  the publish rather than returning one line above it.

The test's own message says both, which is what keeps the next reader from "simplifying" the
subscription away as fixture noise.

---

## A negative asserted through a projection that could not see the claim

The success leg of the headline test asserted that an accepted chunk does **not** move the rejection
counter. It established nothing about the chunk being **accepted**.

A run in which the third call fell into the **persist-failure** arm produces the same counter (2) and
the same `db.calls` (3). The leg would have passed while exercising the wrong arm entirely. The one
check that would have caught it - the captured log, which the persist arm writes to - ran **before** the
leg, so it could not see a line that call emitted.

Its sibling test, `TestHandleTaskLog_ARealPersistFailureIsNotAFenceRejection`, carried exactly that
fixture check (`require.Contains(t, logged(), "handleTaskLog AppendTaskLog")`, commented "fixture: the
other arm still logs, so this test is exercising it rather than falling through"). **This one did not.**

Nothing today can make `stubFenceRow.Scan` fail with `err == nil`, so it was assertion strength rather
than a live defect. Closed by asserting the publish positively (`require.Len(t, accepted, 1)`) and by
re-checking the log **after** the leg, with a comment saying why the earlier check cannot see it.

> **This is the M14b shape this batch keeps re-finding**, now on its fourth appearance: a leg that
> asserts a negative through a projection shared by every other arm. The tell is always the same - the
> assertion would pass if the code under test did nothing at all. Ask what OTHER path produces this
> exact observation.

---

## Two prose defects worth recording

**"Three reasons" is a closed enumeration over a FOUR-conjunct fence.** The item, the joint spec, the
plan and the arm's own comment all said the fence rejects for three reasons: wrong assignee, stale
epoch, closed trailing window. The `WHERE` has **four** conjuncts - `t.id = sqlc.arg(task_id)` is one
of them, and it is easy to read past as a lookup rather than a predicate. A well-formed uuid naming no
task at all lands on this arm while being none of the other three, **and this commit's own integration
test drives exactly that path** (`TestGRPCAdmissionEndToEnd_TheServedTaskLogFenceCountsAreTheServing
Handlers` sends chunks for an unseeded task id, which is what lets it need no fixture task). The
comment, the payload doc and README now say four, and place the new one with "an unwelcome sender" so
an operator's reading is still correct.

**`rejected_total` is caller-inflatable, and the README's own remedy helps the forger.** Only
`worker_id` is authenticated on an incoming chunk; the task id comes from the wire, and **nothing rate
limits task-log CHUNKS** - the ingest budget bounds log *lines*, on a different arm. So any enrolled
agent can drive this number up smoothly and indefinitely by naming task ids it does not own,
manufacturing the exact "climbs steadily on a healthy fleet" signature README attributes to a too-small
trailing window. **And that signature's documented remedy is to RAISE the window - which widens the
hole the window exists to bound.** A forgeable signal whose remedy helps the forger. Disclosed in
README in-slice (`:1290`: *"That signature is forgeable, so confirm the window against its configured
value before raising it"*) and filed below rather than fixed.

---

## What did NOT recur

Recorded because the batch's value is partly in which lessons stopped costing anything.

- **The backwards concurrency measurement did not recur.** Slice 2 shipped its `-race` and exactness
  kill rates inverted and a lens had to re-measure. This slice measured both halves in the pinned
  container and **a lens independently re-measured and got the same numbers.** The comment ships
  measured figures, not plausible ones.
- **The cross-package arity defect did not recur - and the antecedent was VERIFIED rather than
  assumed.** The rule is "any section whose payload struct restates fields owned by another package
  needs a `NumField` assertion". The plan checked whether the antecedent holds here (it does not: the
  source returns a bare `uint64`, so there is no hand-written mapper) and then **guarded the
  antecedent** with `TestTaskLogFenceSourceReturnsAScalar`, whose failure message names the arity test
  that must ship in the same commit if the return type is ever widened. That is the difference between
  applying a rule and understanding it.

---

## What Was Built

- **`internal/worker/handler.go`** - `Handler.taskLogFenceRejects atomic.Uint64` as a **value** field
  (zero value ready, so a bare `&Handler{}` in a test has a working counter and there is no nil case),
  the exported `TaskLogFenceRejections() uint64`, and one `Add(1)` in the existing named
  `pgx.ErrNoRows` arm before its return. Read the arm's comment at `:1149-1213`: the four-reason
  enumeration with the third called out as the legitimate one, the absolute no-log-line/no-publish
  paragraph, and the declined-not-impossible paragraph with its three-part price.
- **`internal/worker/tasklog_fence_counter_test.go`** (new) - `stubFenceDB`, the ~35-line `store.DBTX`
  whose `Exec` and `Query` **panic** (`handleTaskLog` is one statement; a second must fail loudly) and
  whose `Scan` fills `AppendTaskLogRow` **by destination type** rather than by position. Four tests, all
  in the lane CI runs.
- **`internal/api/server_counters.go`** - `TaskLogFenceSource` (its own interface, never a widened
  `IngestLogBudgetSource`, with the reason in its comment), `CounterSources.TaskLogFence`,
  `taskLogFenceSection` / `taskLogFenceCounts`, the handler branch, and the corrected parenthetical in
  `ingestLogBudgetSection`'s "what they do NOT count" paragraph.
- **`cmd/relay-server/http_server.go`** - both `s.Counters.IngestLogBudget` and `s.Counters.TaskLogFence`
  under **one** `if d.agentHandler != nil`, with a comment saying why one nil filter is the honest shape
  (both controls live on this one object and neither exists without it) and why the two `api` fields
  stay independent anyway.
- **`cmd/relay-server/counters_wiring_test.go`** -
  `TestBuildHTTPServer_EverySourceFieldProducesAServedSection` (the executed completeness relation, and
  the two measured evasions of its predecessor written into its comment),
  `TestServerCountersIsWiredByMain` reduced to four questions execution cannot answer plus the
  `countersAssignmentSources` direction check, and one added `NotContains` on the shipped typed-nil test
  rather than a duplicated one.
- **`cmd/relay-server/grpc_admission_e2e_integration_test.go`** -
  `TestGRPCAdmissionEndToEnd_TheServedTaskLogFenceCountsAreTheServingHandlers`: a real registered
  stream, real rejections, read back through the real admin route, plus the assertion that **the count
  survives the disconnect that produced it** - the `metrics.Store` refutation made executable.
- **README** - the `task_log_fence` payload and reading bullet (four reasons, the forgeability warning,
  what it does not overlap), plus the two sentences this slice invalidated: the
  `RELAY_TASKLOG_TRAILING_WINDOW` row's "no signal of any kind" and the `ingest_log_budget` bullet's
  "contributes nothing to these numbers".
- **Zero SQL, zero migration, zero proto, zero generated file, zero files under `web/`.**

## Key Decisions

- **The comment says DECLINED, not impossible**, and carries the price in three specific clauses. The
  behaviour is identical; the honesty is not.
- **Reuse `agentHandler`; do NOT add a second deps field for the same object.** Only `agentHandler` is
  compared against the identifier passed to `RegisterAgentServiceServer`, so a sibling field could
  legitimately be fed a **different** `*worker.Handler` with every check green - the confident zero the
  guard exists to prevent, created in order to satisfy an arithmetic check.
- **The completeness relation is EXECUTED, not tabulated.** Count served top-level keys against
  `NumField(api.CounterSources)`; parse only the direction execution cannot see.
- **A value field on `Handler`, not a package-level `var`** - refuting the item's own "process-wide
  `atomic.Uint64` in `internal/worker`" wording on slice 2's grounds. Production has one Handler, so per
  Handler IS process-wide there, and it is a property the wiring guard can check.
- **A scalar source, and the scalar-ness is GUARDED.** `TestTaskLogFenceSourceReturnsAScalar` reddens
  if the return type is widened, and names the arity test that must then ship in the same commit.
- **One added assertion on the shipped typed-nil test, not a duplicated test.** One deps field means one
  nil filter and one `if`; a second test would copy a fixture to assert the same branch.
- **The counter is one number and the payload says what it does not cover**, in the arm's comment, in
  the section's doc and in README: it is neither a subset nor a superset of `ingest_log_budget`, because
  the rejection arm never consults the log budget at all.

## Findings Triage

- **1 prose refutation that changed the slice's honesty** ("impossible" -> "declined, and here is the
  price"), found by the planner against the item AND the joint spec, and applied at both sites.
- **1 decorative guard that was a REGRESSION on what it replaced**, found independently by both lenses
  with different mutations, one of which (a fourth section satisfied by appending one string) was proven
  green module-wide. Fixed at the top rung with an executed test plus a property-counting walk.
- **1 default-lane gap on the epoch fence's own named consequence** - a `Publish` on the rejection arm
  left `internal/worker` green because the only guard was integration-tagged. Pre-existing; closed here
  because this slice's harness made it cheap.
- **1 negative asserted through a projection that could not see the claim** (the success leg), closed by
  asserting the publish and re-checking the log after the leg.
- **2 prose defects** - "three reasons" over a four-conjunct fence (with this commit's own test driving
  the fourth), and the forgeable `rejected_total` whose documented remedy helps the forger.
- **10 item/spec/plan claims refuted by the plan** (R1-R10), including the two the item's own Done-When
  bullets are written against.
- **0 findings against the shipped behaviour after remediation.**

## What Remains Open

- **The forwarding proof is still INTEGRATION-ONLY for its strongest form.** CI compiles
  `TestGRPCAdmissionEndToEnd_TheServedTaskLogFenceCountsAreTheServingHandlers` (`make vet-integration`)
  and never runs it, because reaching the fence arm through `buildHTTPServer` needs `Connect`'s message
  loop and therefore Postgres. **What improved this slice:** the crude substitution
  (`s.Counters.X = worker.NewHandler(...)`) now dies in the DEFAULT lane on
  `countersAssignmentSources`, which demands the assignment be spelled `d.<field>`. What is still
  integration-only is a substitution that *reads* as a deps field, and the question of whether the
  numbers move at all. Stated in the tests' comments. Not filed: the alternative is an AST fallback on
  top of an executed check, which is the rung this batch deliberately climbed off.
- **`TestTaskLogFenceRejections_ConcurrentRejectionsAreExact`'s exactness half is INERT at one CPU:
  measured 0/20 at `-cpu=1`.** Its `-race` half is at full strength there (TSan's vector clocks do not
  need true parallelism to see two goroutines writing one word with no happens-before edge). CI is
  `ubuntu-latest` with 2-4 vCPUs and no `-cpu` flag, so both halves are live where it matters; a
  constrained cgroup would stop detecting the mutation rather than fail. The measured numbers and the
  green baseline are in the test's own comment. Not filed - there is no action short of pinning `-cpu`
  for one test.
- **`srv.Handler` remains reassignable after `buildHTTPServer` returns.**
  `if os.Getenv("...") != "" { srv.Handler = http.NewServeMux() }` between the `buildHTTPServer` call
  and `ListenAndServe` compiles, leaves `cmd/relay-server` green, and serves no
  `/v1/server/counters` at all. The assignments-per-identifier count deliberately ignores field
  assignments (a `SelectorExpr` on the left is a mutation, not a rebinding), so nothing sees it.
  Pre-existing from slices 1-2, and `main.go:208-214`'s own comment slightly overstates the win -
  "nothing to unwire after the fact" is true of the `api.Server` and false of the `http.Server`'s
  `Handler`. The blunt form removes the whole API and would be noticed in a minute; the realistic form
  is a middleware wrap that drops one route. **Carried by amendment into
  `idea-2026-08-14-generalize-the-env-to-field-wiring-guard`** (see below for why there rather than as a
  new item).
- **Slice 4 is the only one left and it is the hardest.** It is not blocked - the read surface, the
  section pattern, the cardinality relation and the typed-nil filter are all settled and it needs none
  of them designed. Its hard parts are untouched by any of the three shipped slices: the **import
  direction inverts** (`internal/scheduler` imports `internal/api`, so the snapshot type must be
  declared IN `internal/api` and the hand-written copy lives on the scheduler side); the watchdog is the
  only section that is **legitimately disable-able**, so the typed nil is the natural shape rather than a
  hypothetical; the **writer ambiguity** between a watchdog-written and an agent-written `timed_out` is
  side-stepped rather than resolved; and it is the only one of the four wanting a **per-worker map**,
  which is the only unbounded-in-principle key in the cluster. The amendment below carries the new
  forward guidance this slice's review produced.

## Improvement Goals

Carried forward:

- **Verify a backlog item's technical claims against the code** - honored, **nineteenth iteration**. The
  plan refuted ten claims (R1-R10), including the item's own "structurally impossible", its
  "package-level in `internal/worker`", its "its own concretely typed deps field", its "its own
  typed-nil test", and the inherited assumption that this proof needed Postgres.
- **A backlog proposal is not a contract** - nineteen for nineteen.
- **The verification chain is worth its length only if each stage treats the previous stage's output as
  untrusted** - honored in both directions: the plan refuted the spec and the item, and both lenses
  refuted the plan's own R3 rewrite.
- **A guard that counts a PROPERTY, not a spelling** - honored, and **sharpened**: this slice found that
  a guard can count a property and count the wrong one.
- **A mutation proof must leave a test behind** - honored; both lens mutations left permanent executed
  checks.
- **A mutation that reddens something is not yet evidence for the claim you are making** - honored; the
  integration forwarding mutation was required to fail on the exact-count assertion, not on an absent
  section and not on the sibling assertions.
- **A test can be robust and inert on the same machine** - honored, with measured numbers and a green
  baseline rather than copied ones.
- **State a coverage limit rather than implying it** - honored, and the engineer went further by
  correcting two prose claims that had become stale the moment the default-lane walk landed.
- **Wrong prose about correct code is the dominant defect class** - **fourteenth consecutive
  iteration**, and this time it was caught **before** shipping, in the comment whose whole purpose is to
  be believed.
- **Backlog housekeeping is required scope** - the close of the source item belongs to the conductor.

New from this iteration:

- **A guard's failure message must be read as an INSTRUCTION, and you must ask what the cheapest
  compliance is.** Slice 2 recorded that a message must name what is wrong. This is the inversion: a
  message naming a condition whose literal satisfaction produces the defect is worse than a vague one.
  **Candidate for durable memory.**
- **A relation between two hand-maintained lists is bookkeeping until something reads BOTH against the
  code.** The `sections []string` column named real fields, was checked for existence, was checked for
  duplication - and never once compared against an assignment. Existence and uniqueness checks on a list
  are not a check that the list is TRUE.
- **When you replace a bijection with a many-to-one relation, ask what the bijection was forcing.** Here
  it was forcing a new deps field, which inherited three unrelated checks. The relation was strictly
  more expressive and strictly weaker.
- **Say "declined, and here is the price" rather than "impossible".** A comment written to end a
  question has a permanent audience and no reader positioned to check it, so a false claim there is the
  most expensive kind.
- **A negative leg must be asserted through a projection only the claimed path produces.** Ask what
  OTHER arm yields the same observation; if the answer is "several", the leg proves nothing.

## Files Most Touched

- `internal/worker/handler.go` - the `pgx.ErrNoRows` arm at `:1148-1215`. Read the four-reason
  enumeration (and note *why* it is four), then the declined-not-impossible paragraph, which is the
  headline lesson written where the next person to ask the question will hit it.
- `internal/worker/tasklog_fence_counter_test.go` - `fenceSubscribe`'s comment ("SUBSCRIBING IS
  LOAD-BEARING TWICE") is the one to read: it is the only reason the no-publish assertion is
  non-vacuous, and the only reason the success leg reaches the publish at all.
- `cmd/relay-server/counters_wiring_test.go` -
  `TestBuildHTTPServer_EverySourceFieldProducesAServedSection`'s comment carries both measured evasions
  of the string-list relation, and `TestServerCountersIsWiredByMain`'s `countersAssignmentSources` block
  carries the direction execution cannot cover. Together they are this slice's whole guard story.
- `internal/api/server_counters.go` - `TaskLogFenceSource`'s comment (its own field, and why a scalar is
  load-bearing rather than minimal) and `taskLogFenceSection`'s doc (`:284`, the second "declined" site).
- `cmd/relay-server/http_server.go` - the "TWO SECTIONS, ONE OBJECT" block, so nobody "simplifies" the
  two independent `api` fields into one interface.
- `README.md` - `:1290`, the `task_log_fence` reading bullet, including the forgeability warning and the
  four-reason enumeration.
- `docs/superpowers/plans/2026-08-21-silent-drop-observability-slice3.md` - R1 (the headline refutation),
  R2/R3 (why `agentHandler` is reused and how the relation was re-expressed - note R3 is the claim both
  lenses then refuted), R4 (the default-lane discovery), and the M1-M19 battery.

## Verification

- **This pass had no shell.** Bash was unavailable to the TPM lane; nothing was executed. No `git log`,
  no `git diff`, no test run. Every claim below that could be checked by reading was checked against the
  worktree.
- **Verified by reading:** `internal/worker/handler.go:880-1030` (the two Go-side gates and the two
  sibling `pgx.ErrNoRows` arms) and `:1140-1240` (the fence arm in full);
  `internal/worker/tasklog_fence_counter_test.go:90-260`; `internal/api/server_counters_test.go:560-700`
  (both payload walks and the every-section fixture); `cmd/relay-server/counters_wiring_test.go:183-244`
  (the executed completeness relation) and `:380-593` (the table, `countersAssignmentSources`, the
  `RegisterAgentServiceServer` comparison and the assignments-per-identifier block);
  `cmd/relay-server/main.go:205-247`; `README.md:1286-1291`; the slice-3 plan in full; the closing item
  in full including all three dated amendments; the slice-1 and slice-2 retros in full; and the two
  sibling backlog items plus `bug-2026-08-21-handletaskstatus-db-error-lines-bypass-the-in-scope-budget`
  and `bug-2026-08-14-task-logs-have-no-per-task-volume-cap` for duplicate checking.
- **Confirmed against code, not inferred:** that both "impossible" sites now read "declined" (grep over
  `*.go` returns exactly the two corrected sites plus one unrelated hit in
  `ingest_log_counters.go:52`); that the fence `WHERE` has four conjuncts; that the assignments-per-
  identifier walk skips `SelectorExpr` left-hand sides by design and therefore cannot see
  `srv.Handler = ...`; that `handleTaskStatus`'s two sibling `pgx.ErrNoRows` arms
  (`handler.go:975` and `:1020`) count nothing; that both Go-side gates (`:914`, `:927`) return before
  the write; that `TestCounterPayloadCarriesNoIdentifiers` calls `t.Fatalf` on any non-struct,
  non-unsigned-integer kind, which includes every map, slice and string leaf.
- **Reported by the implementing and verifying lanes, not re-run here:** unit 594 -> 603 top-level, the
  three integration packages, the module-wide `-race` container run, the 0-byte `internal/store/` diff,
  and every mutation result including M1-M19, the two guard-evasion demonstrations, the
  `broker.Publish`-stays-green measurement, and the M6 `-cpu=1` / `-cpu=2` figures.
- **Not verified:** all test results, the commit set and diff stat, and the change set as `git` sees it.
  Each is attributed above.
- **No PR number appears anywhere in this retro or in the filed items**, by instruction. The work is
  referenced by date and slug.
- **Outstanding and belonging to the conductor:** the close of
  `idea-2026-08-14-tasklog-fence-rejection-is-unobservable` (`/backlog close`, never a hand-edited
  `status:`), the exact-file-set check, the final gates, all commits, and a ROADMAP refresh.
