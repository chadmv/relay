# A count bound on `source.sync`

Date: 2026-09-10
Item: `docs/backlog/idea-2026-09-04-source-sync-has-no-entry-count-bound.md`
Status: design, ready for a plan

## 1. Problem

`jobspec.validateSourceSpec` bounds nothing about `len(s.Sync)`. The same field's excluded entries
are capped at `maxSyncExclusions = 16`; its included entries are not capped at all. So this is an
asymmetry inside ONE spec field - closed on one axis, open on the other - and not a general gap.

The open axis costs, per source spec, per attempt:

- One `Client.ResolveHead` subprocess (`p4 -c <client> changes -m1 <path>#head`) for every include
  whose `rev` is `#head`, issued serially in `Provider.Prepare` before `ws.Acquire`.
- One argv element on the single `p4 sync` invocation for every include, whatever its `rev`.
- The second factor of the `O(exclusions x len(Sync))` coverage and swallow loops, once in
  `validateSourceSpec` and again in `perforce.preemptSpecs`. `maxSyncExclusions` closed the first
  factor and deliberately left the second one open, because at the time there was nothing to close
  it with.
- One `BaselineHash` over every entry, on the agent per prepare AND on the coordinator inside
  `selectWorker`'s worker loop (`BaselineHashFromAPISpec`, section 2), on every dispatch attempt.

## 2. What the tree actually says

The item was read twice: once for what it asks, once asking only whether it contradicts itself,
contradicts the tree, or prescribes something that does not exist. Three of its claims needed
correcting, and the corrections change the design rather than decorating it.

| The item's claim | What the tree says |
| --- | --- |
| "This is the third per-entry subprocess axis on the same spec, beside `unshelves` and the exclusions the 2026-09-04 design adds, **and it is the only one of the three with no bound at all**." | **False, and the error is in the direction that inflates this item's priority.** `source.unshelves` has no count bound either: `validateSourceSpec` checks only `cl <= 0` per entry, and `bug-2026-08-29-source-unshelves-is-one-subprocess-per-entry-and-unbounded` is still OPEN in `docs/backlog/`. Two of the three axes are unbounded, not one, and by that item's own measurement the OTHER one has the better byte-per-spawn ratio (an unshelve entry is `1,`; the cheapest sync entry is an order of magnitude larger, section 4). Nothing here closes the unshelves axis and nothing here should be read as having closed it. |
| "the exclusions ... beside" it, as a peer axis. | **The exclusions are not a peer axis; they are a bounded sub-population of the very field this item is about.** An excluded entry lives in `s.Sync`, so a total bound covers it, and a bound on includes alone would leave the two halves of one field counted by two different rules. It is also two subprocesses per exclusion, not one: `PathHasFiles` then `SyncPreempt`. |
| "`Prepare` runs one `ResolveHead` subprocess per `#head` entry inside the task's own prepare phase" - offered as the whole cost. | **True and incomplete, and the omission decides which axis to bound.** The `ResolveHead` loop runs BEFORE `ws.Acquire`, so unlike the unshelves loop (which forces `ModeExclusive` in `workspace.go`) it holds no workspace and serialises no peer task on that stream - which LOWERS this item's severity relative to its sibling. Meanwhile `selectWorker` calls `BaselineHashFromAPISpec(taskSrc)` INSIDE its worker loop, guarded by a warm-key match and broken out of after the first hit, so a spec's entries are re-sorted and re-hashed once per candidate worker holding a matching warm workspace, on every dispatch attempt, on the coordinator. That cost is driven by EVERY entry, not by the `#head` subset, and `GetEligibleTasks` has no LIMIT. Bounding only `#head` entries would leave it untouched. |

The item prescribes no remedy, so there is nothing prescribed to refute. Its diagnosis of the
`ResolveHead` loop is correct as far as it goes.

## 3. Decision 1: the axis is TOTAL `len(s.Sync)`

Three candidates were considered.

**Chosen: a bound on `len(s.Sync)`, counting includes and exclusions together.**

1. **It is the only candidate that covers every cost in section 1.** The `#head` subset drives the
   subprocess axis alone; the argv axis, both coverage loops, the proto conversion and both
   `BaselineHash` sites are driven by the entry count regardless of what any `rev` says.
2. **It reads as the other end of an existing range.** `validateSourceSpec` already refuses an empty
   `sync` with "source.sync must have at least one sync entry". A total bound is that sentence's
   upper end, the way `maxTasksPerJob` is the upper end of "at least one task is required". A bound
   on a subset of the entries would be a second concept for the operator to hold.
3. **It subsumes the axis the item asked about.** `#head` includes are a subset of entries, so a
   total bound of N caps `ResolveHead` round trips at N.

**Rejected: a bound on `#head` entries only.** It is the tightest fit to the item's headline and the
worst fit to the costs. It leaves the coordinator-side hash and the argv length unbounded; it makes
whether a spec validates depend on the VALUE of a field rather than on the shape of the list, so
editing `@1200` to `#head` on the 513th entry turns an accepted spec into a refused one with no
change in size; and its refusal message has to explain a subset ("at most N of your entries may use
#head"), which is advice about revision pinning smuggled into a count bound.

**Rejected: a bound on includes only, leaving exclusions counted separately by
`maxSyncExclusions`.** It is within 16 of the chosen bound for every input, so it buys no
precision, and it costs a second counting pass plus a message that has to say which entries it
counted.

**What the other axes cost at the bound**, stated so the bound is not read as bounding more than it
does. With `maxSyncEntries` = 512 (section 4) and `maxSyncExclusions` = 16 unchanged, one source
spec is worth at most:

- 512 `ResolveHead` round trips per attempt, and 5632 across a full `maxRetries` budget, since
  prepare runs again on every attempt.
- 512 argv elements on one `p4 sync`. In BYTES that is unbounded, because path length is not
  bounded - see section 10.
- 32 subprocesses on the exclusion axis (16 x `PathHasFiles` + `SyncPreempt`), unchanged.
- 8192 `DepotPathCovers` calls in `validateSourceSpec` and 8192 more in `preemptSpecs`.
- One `BaselineHash` over at most 512 entries per prepare, and one per candidate worker holding a
  matching warm workspace per dispatch attempt.

**The two source-spec bounds are not redundant and neither implies the other.** The total bound says
how much work one task's prepare may buy; the exclusion bound says how much of that work may be the
expensive per-exclusion kind, and it is 32x tighter. This is the same division of labour as
`maxCommandsPerJob` against `maxCommandsPerTask`.

**Accepted consequence:** an operator with 512 includes cannot add an exclusion, because the
exclusion counts toward the total. At 512 entries they are already far outside the envelope section
4 argues from, so this refuses nothing anybody plausibly wants.

**Rejected: deduplicating identical entries instead of counting them.** Nothing today refuses two
identical include entries, so the cheapest maximal spec is one path repeated (section 4). Dedupe
would remove that particular shape and nothing else - an adversary varies one character per path and
pays a few bytes - and it would add a silent normalisation to a validator that currently normalises
only the command form. Not taken.

## 4. Decision 2: the number is 512, and here is what produced it

**The legitimate high end.** A relay `sync` list names subtrees to sync, and it is coarser than a p4
client view. The common shape is one entry: the stream root at `#head`. The next shape up is a
handful of named top-level directories - Engine, Content, Config, Binaries, plus a per-department
directory or two - which is tens. The outer edge is a generated list with one entry per asset or per
shot directory, which is low hundreds. A spec wanting more than a few hundred entries is better
served by naming a parent path, which is exactly what the sibling includes already reach.

**The multiplier this project applies to a plausible high end.** `maxSyncExclusions` is "several
times" a handful. `maxTasksPerJob` is 2.5x to 5x its 1000-to-2000-frame high end.
`maxCommandsPerTask` is roughly 20x its "tens". Taking 100 to 200 as the outer edge and the
2.5x-to-5x band gives 250 to 1000.

**The cross-check that fixes it inside that band.** `maxCommandsPerTask = 500` is the other
concentration control on this exact quantity - subprocess spawns one task pins to one worker slot
per attempt - so the two belong in the same order of magnitude or one of them is wrong. 512 sits in
the middle of the band and next to 500.

**512 rather than 500, deliberately.** A cap spelled 500 beside `maxCommandsPerTask = 500` would
read as derived from it and be maintained as if it were, which is the failure `maxTimeoutSeconds`
avoids by sitting deliberately ABOVE `RELAY_TASK_MAX_ASSIGNMENT`'s default so the independence is
visible in the numbers. The two bounds are independent: one counts commands the agent executes, the
other counts p4 round trips before any command runs.

**What would falsify it:** a submission somebody actually wanted, refused. Not "the number looks
small". The comment must say so, in the form the other bounds already use.

**The reduction factor is deliberately NOT asserted here.** The obvious sentence to write is "the 1
MiB body permits about N sync entries, so this is an Nx reduction". The input to that sentence is a
hand count of the smallest legal entry: with the degenerate stream `//`, the entry
`{"path":"//","rev":"#head"},` is 28 bytes by hand count, every path equals the stream so
containment passes, nothing dedupes them, and every one of them resolves `#head`. 1 MiB over 28
bytes is on the order of 37,000. **Both the count and the division are unverified arithmetic in a
document.** Task 0 of the plan establishes the ceiling by CONSTRUCTING a maximal body and counting
what the decoder accepts, and no factor derived from it goes into a code comment, a commit message
or README until that number exists. See section 13.

**DO NOT MAKE THIS ENV-CONFIGURABLE.** `jobspec.go`'s `maxRetries` comment carries the argument in
full and it applies to this bound without modification. The new constant's comment points at it in
the three-line form the other bounds use and does not restate it.

## 5. Decision 3: where the check goes

**Immediately after the `len(s.Sync) == 0` check in `validateSourceSpec`, before the first per-entry
loop.** The count is known without traversing anything, so there is no reason to walk the list
first, and there are three reasons not to. The check must precede:

1. **The per-entry loop.** Per entry it runs three `strings.HasPrefix` calls, a `hasControlByte`
   scan whose cost is the path's own length, and up to four regexp matches. This is the pass
   `maxTasksPerJob`'s placement argument is about: refuse before allocating and walking.
2. **The `excluded > maxSyncExclusions` check**, which cannot move earlier because it needs the
   count that loop produces. So a spec over BOTH bounds reports the entry count. That precedence is
   deliberate: the entry count is knowable without work, and it is the more actionable message for a
   spec that is over both.
3. **The coverage and swallow loops**, which are `O(exclusions x len(Sync))`. `maxSyncExclusions`'s
   own comment records that it is checked before that loop so an over-count spec is refused after
   one linear pass. The new bound closes the loop's other factor, and it does so before the same
   loop, for the same reason.

**Precedence consequence, taken deliberately.** A spec that is over the entry bound AND carries a
path that is not a depot path, a path outside the stream, a control byte, or an invalid rev now
reports the entry count where the older code reported whichever of those came first. No existing
test can depend on the old order, because every source-spec fixture in the tree is small - the
largest, `manySyncExclusions(17)`, is 18 entries against a bound of 512 - so the exclusion-count case
keeps its message. Task 0 confirms that by search rather than by assumption (section 13).

**What does not move.** `validateSourceSpec` stays where `Validate` calls it, in the final loop after
cycle detection. This bound does not reorder source validation relative to the rest of `Validate`,
and the earlier loops still run first for a many-task spec. That is existing structure and changing
it is not in this slice.

## 6. Decision 4: the message

```
at most 512 sync entries are allowed, got 900
```

and, as the caller prefixes it in `Validate`:

```
task render-001: at most 512 sync entries are allowed, got 900
```

- **It follows the shape all three count messages already use**, "at most %d X are allowed, got %d",
  so a reader who has seen one has seen them all.
- **It reports what arrived, not only the limit.** A caller who generated the spec has to know by how
  much to cut it. This is the same reasoning `TestValidate_TheTaskCountIsBoundedAtBothEnds` records,
  and unlike `maxCommandsPerJob` this check knows the exact number, so there is no honesty problem
  with printing it.
- **It says "sync entries", pairing with the lower end's "at least one sync entry"**, so the two ends
  of the range read as one range even though they do not share a prefix.
- **It has to be legible with no context around it**, because section 7 puts it in stored
  `last_error`. "at most 512 sync entries are allowed, got 900" is legible there; the task prefix
  names which task.
- **Rejected: `source.sync: at most 512 entries are allowed, got 900`.** It pairs better with the
  lower-end message and worse with the other three count messages, and consistency across the count
  vocabulary is the more valuable of the two.
- **No remedy clause and no knob named.** The other count messages carry none, the guidance belongs
  in README (section 9), and a remedy in a validator message is a surface that has to be checked for
  whether it favours whoever provoked it.

## 7. Decision 5: retroactivity over stored schedules

**This bound is retroactive, no grandfathering, same as the count bounds.**

`jobspec.Validate` runs on stored `scheduled_jobs.job_spec` rows on the paths `jobspec.go`'s
`maxRetries` comment enumerates - that comment is the authority and this spec does not copy the
enumeration, because a second copy is a second thing that can go stale. Two of those paths produce a
consequence an operator sees:

- **The boot sweep.** `schedrunner.ValidateStoredSpecsOnStartup` validates every ENABLED schedule at
  startup and WRITES the returned message into `scheduled_jobs.last_error`, stamping
  `last_error_at`. A stored schedule whose spec carries more than 512 sync entries stops firing on
  upgrade and carries "task <name>: at most 512 sync entries are allowed, got N" from the first boot
  after the release.
- **Run-now.** `api.handleRunScheduledJobNow` answers 400 with the validator's own message rather
  than collapsing into a 500.

`fireOne` records the same message on each tick it refuses, and `handlePatchScheduledJob`'s
clear-decision will not clear the record until the stored spec is fixed - which is the correct
behaviour, since the spec is still unfireable.

**What the operator sees.** `last_error` is a first-class field on the scheduled-job row: it is in
the API response, it is what the `failing` bucket of the schedule stats counts, and README already
documents it as "the row-level health signal". So the failure is not silent - it is named,
attributed to a task, and carries the number that was too big. That is the whole reason the message
shape in section 6 matters.

**Why grandfathering is refused.** The only way to grandfather is a second, more permissive
validation vocabulary for stored specs, which contradicts the Single job-spec pipeline invariant and
the "a validation vocabulary is a property of the binary" argument that `maxRetries`'s comment rests
on. The count bounds took exactly this trade in 2026-08-29 and recorded it: "a schedule created
before this release with more than 5000 tasks stops firing on upgrade."

**The exposure is different in kind from the count bounds', and smaller.** A 5000-task schedule is a
shape somebody plausibly stored. A 512-entry sync list is not a shape a human authors; the population
at risk is machine-generated specs. **That is an argument, not a measurement.** How many stored
schedules in any real deployment exceed 512 sync entries cannot be established from this repository
and is not a Task 0 item - it is a question for the operator of a deployment, and the answer belongs
in the release note as an instruction to check, not in this spec as a claim.

**Release note obligation.** The PR states the retroactivity consequence, in the form the
count-bounds PR used. This is required scope, not a nicety: a schedule that stops firing without the
reader having been told a bound landed is the failure mode this whole section exists to avoid.

## 8. Decision 6: the Python SDK, and the SPA

**No Python change is required, and the number must not be copied there.**

- `python/src/relay/models.py` mirrors the per-entry source rules (`Sync._path_starts_with_slashes`,
  `Sync._rev_matches_exclude`, `Source._sync_paths_under_stream`) and the LOWER end of this exact
  range: `Source._at_least_one_sync` raises "sync must have at least one entry" where Go says
  "source.sync must have at least one sync entry". The lower end is therefore already duplicated
  with independent wording; the upper end will not be.
- `Sync`'s own docstring already assigns the sibling-dependent rules to the server, naming "how many
  exclusions one spec may carry" among them, and `Source`'s validators stop short of any count
  ceiling. So the SDK's posture is exactly the one this bound needs: it accepts a large `sync` list
  and the server refuses the same spec on submission. Nothing drifts, because nothing is copied.
- **Copying the number is refused for the reason `maxRetries`'s comment already gives**: it would
  put a number from `jobspec.go` into a separately released package, where a version skew produces
  two different answers for one spec.
- **What a Python caller experiences** is an error from the server carrying the exact message in
  section 6, rather than a local pydantic `ValidationError`. That is the SDK's documented division of
  labour.

**One editorial residue, out of this lane's scope.** `Sync`'s docstring lists four server-side rules
by name. A total-entry ceiling is a fifth rule of the same class, so the list becomes less complete
than it reads. The general sentence around it stays true. A one-clause generalisation of that list is
worth doing and belongs to whoever owns `python/`; it is proposed in section 15 rather than done
here. Do not read any of this as a census of what `python/` copies - the open items
`idea-2026-09-04-nothing-guards-the-go-python-job-spec-type-pair` and
`idea-2026-08-27-sdk-copies-of-server-vocabularies-are-unregistered` own that question, and this
slice must not be described as having answered it.

**No SPA change.** `web/src/jobs/specTemplate.ts`'s `validateSpecText` is deliberately shallow and
names `source` only as a rule it does not implement, so it has nothing to keep in step. It duplicates
the lower end of the TASK-count range and its own comment records why upper bounds are not copied
into it; the same reasoning covers this bound.

## 9. Decision 7: documentation

**One row changes: the `sync` row of the source-spec field table in README's "Source workspaces"
section.** That is the section that documents the field, and it already carries the exclusion cap, so
the entry cap belongs beside it rather than in a new place.

Current first sentence of that row:

> One or more paths to sync.

Replacement (the rest of the row is unchanged, including "At most 16 exclusions per spec." and the
version-skew sentence):

> One or more paths to sync, at most `512` entries; a longer list is rejected at submission, and
> re-checked every time a stored schedule fires - see [Scheduled jobs](#scheduled-jobs), because that
> makes the bound retroactive over schedules created by earlier releases. Every `#head` entry costs
> one round trip to the Perforce server inside the task's own prepare phase, and every attempt
> repeats them, so a broad path costs less than an enumerated list of its children.

The retroactivity clause is lifted verbatim from the `tasks` and `timeout_seconds` rows so the four
bounds read identically. The cost sentence is there because a cap with no stated mechanism gets read
as arbitrary and raised on the first complaint.

**Considered and declined: the `tasks[].retries` row.** It lists the counts retries multiply -
`tasks`, `commands` per task, `commands` per job - and sync entries are also re-paid on every
attempt, so a fourth number could go there. Declined: that row is an argument about the command cost
model, it was already silent about `unshelves` (which is multiplied by attempts and still unbounded),
adding a number would not make its list complete, and two sibling lanes may be editing README in this
batch. Keeping the edit to one row keeps the conflict surface at one row.

## 10. What this bound does NOT do

Stated in full, because a bound documented by what it is called rather than by what it does is how
partial coverage becomes a false claim.

- **It does not reduce the AGGREGATE cost of one request, and a job-wide bound could not do so by
  much either.** This is the `maxCommandsPerJob` question, asked again and answered the other way.
  There, two per-axis caps whose product exceeded what the body could express reduced nothing, and a
  third job-wide cap took the worst case down 6x to 7x. Here a job-wide cap cannot buy that, because
  the legitimate many-tasks shape sets its floor: a perfectly ordinary job at `maxTasksPerJob` with
  one `//stream/...` entry per task is 5000 sync entries, and a 2000-task job with five subtrees each
  is 10,000, so any job-wide cap that does not refuse ordinary renders has to sit around 20,000 to
  25,000 - within a small factor of what a 1 MiB body expresses anyway (section 4). The window
  between "what a real job needs" and "what the body permits" is roughly 8x wide on the command axis
  and only around 1.5x wide here. **So this is a CONCENTRATION control, in the `maxCommandsPerTask`
  sense, and nothing else.** It bounds what one task pins to one worker slot and one prepare phase,
  and what one task costs the coordinator per dispatch attempt. The per-request aggregate stays
  bounded by `maxBodyBytes` alone, exactly as it is today, and repetition stays bounded by
  `RELAY_JOB_SUBMIT_RATE_LIMIT` alone.
- **It does not bound argv BYTES.** `SyncStream` builds `p4 -c <client> sync --parallel=4` plus one
  element per include, and path length is bounded by nothing but the body. 512 long client paths is a
  large command line, and platform command-line limits are real (Windows in particular). Relay has
  never captured what happens at that boundary and this spec does not claim to have tested it. It is
  a pre-existing hazard that a count bound narrows and does not close; section 15 proposes it as its
  own item.
- **It does not close `source.unshelves`,** which is the other unbounded per-entry subprocess axis
  and, by its own item's measurement, the denser one.
  `bug-2026-08-29-source-unshelves-is-one-subprocess-per-entry-and-unbounded` stays open and must not
  be marked as addressed by this slice.
- **It does not batch `ResolveHead`, and batching would not substitute for it.** The unshelves item's
  doctrine says to check for a batching fix first, because batching carries no retroactivity cost.
  Applied here: whether one `p4 changes` invocation can report PER-PATH head for many paths is
  unverified in this repository, and the alternative reading - one head changelist across all paths -
  is a different sync semantics (a consistent snapshot instead of per-path head), changes
  `BaselineHash`'s inputs and would re-sync every warm workspace in the fleet once. It also lives in
  `internal/agent/source/perforce/`, which this lane does not own. **And even a perfect batching fix
  would leave the argv axis, both coverage loops and both `BaselineHash` sites driven by the entry
  count.** So the bound is not a stand-in for a cheaper fix; it is the control for the costs batching
  cannot reach.
- **It does not reduce the bytes the dispatcher re-parses per tick.** `dispatch` unmarshals each
  eligible task's `source` more than once per tick; that cost is linear in stored spec BYTES and a
  count bound does not move it. `bug-2026-08-23-dispatch-cycle-unbounded-per-tick` owns it.
- **It does not hoist `BaselineHashFromAPISpec` out of `selectWorker`'s worker loop.** That call is
  invariant in the worker being scored and could be computed once beside `warmKey`. It is an
  `internal/scheduler` change, outside this lane, and proposed in section 15.

## 11. Threat model

The actor is anyone who can submit a job spec, i.e. any authenticated user, plus anyone who can write
a schedule.

- **Prepare-phase amplification.** One task, one attempt, up to `C` (section 4) serial p4 round trips
  against the shared Perforce server, repeated on every retry. After the bound: at most 512 per
  attempt, 5632 across a full retry budget. **The worker slot is held for all of it, but the
  workspace is not** - the `ResolveHead` loop precedes `ws.Acquire` - so unlike the unshelves axis it
  does not serialise a peer task on the same stream. The load lands on the worker slot and on the
  Perforce server, which is shared infrastructure relay does not own.
- **Coordinator amplification.** Entry count drives an `O(n log n)` hash inside the dispatcher's
  worker loop for every pending source-bearing task with a warm match, on every tick, with no LIMIT
  on the eligible-task scan. Bounded per task by this change; the aggregate over a backlog is not
  (section 10).
- **Repetition is not bounded here and is not meant to be.** `RELAY_JOB_SUBMIT_RATE_LIMIT` bounds how
  often one authenticated principal may repeat a submission; nothing bounds the cumulative total over
  time. Tightening a count cap to serve as a DoS control costs a refused real render, which has no
  workaround inside the product. This is the position `maxCommandsPerJob`'s comment already states,
  and this bound adopts it rather than re-arguing it.
- **The refusal is not a signal anyone can drive against an operator.** It increments no counter,
  feeds no remedy ladder, and names no knob. It writes one message into one schedule's `last_error`,
  which the schedule's own owner caused. There is no forgeability question here of the kind
  `task_status_fence.counts.conflicting_total` raised.

## 12. Test plan

All of it is untagged and lives in `internal/jobspec` (plus the existing `internal/api` table), which
is where the sibling bounds' tests live. `Validate` is a pure function, so no lane, tag or database
is needed - and by the "does it need the tag at all" rule, none may be added.

**New file `internal/jobspec/sync_bounds_test.go`**, following `count_bounds_test.go`'s conventions,
including its rule that the numbers are LITERALS and never the constant, so a test cannot agree with
the implementation by construction when the constant moves.

A helper builds a source spec with `n` include entries under one stream, each with a unique path so
no coverage or swallow rule is in play, and each at `#head` so the fixture matches the axis the bound
is argued from.

1. `one over the cap is rejected and the message reports the count` - 513 entries, expect exactly
   `task t: at most 512 sync entries are allowed, got 513`.
2. `exactly at the cap is accepted` - 512 entries, expect no error. This is the leg an off-by-one
   written as `>=` breaks, and nothing else in the tree catches it.
3. `the count is refused before the per-entry loop runs` - 513 entries where the FIRST entry carries
   an invalid rev. Expect the count message, not the rev message. The precedence is the instrument;
   the property is that the spec is refused before 513 iterations of prefix checks, control-byte
   scans and regexp matches, and the message is the only observable trace of "before".
4. `the count is refused before the coverage loop runs` - 513 entries plus one exclusion covered by
   two of them. Expect the count message, not "covered by exactly one included path". This is the
   quadratic's second factor and the reason the placement argument in section 5 is not decoration.
5. `the entry count outranks the exclusion count` - 513 entries of which 17 are exclusions. Expect
   the count message, not "at most 16 excluded sync paths are allowed, got 17". Pins the deliberate
   precedence in section 5.
6. `an exclusion counts toward the total` - 512 includes plus 1 exclusion covered by exactly one of
   them, so the spec is legal on every other rule and 513 on this one. Expect the count message.
   **This is the discriminating case for the axis decision in section 3**: an implementation that
   counts includes only accepts it, and every other case above stays green.
7. `a spec of pinned revisions is bounded too` - 513 entries all at `@1200`, no `#head` anywhere.
   Expect the count message. **This is the discriminating case against bounding `#head` entries
   only**, and without it the item's own framing is what gets implemented.

**Extend `internal/api/job_spec_source_test.go`'s table** with one row at 513 entries, so the bound
is proven through the type aliases the API layer actually uses. One row, not a copy of the file
above: that table's job is to prove the rule reaches the API's spec types.

**Mutations that must kill something.** Each is run against the suite and named in the commit message
with which test died:

| Mutation | Test that must go RED |
| --- | --- |
| `len(s.Sync) > maxSyncEntries` -> `>=` | case 2 |
| `maxSyncEntries` 512 -> 513 | case 1 |
| the check moved below the per-entry loop | case 3 |
| the check moved below the coverage loop | case 4 |
| the check moved below the `excluded > maxSyncExclusions` check | case 5 |
| counting only `!e.Exclude` entries | case 6 |
| counting only entries whose rev is `#head` | case 7 |
| the "got %d" argument dropped from the message | case 1 |

**A control is required**, per `count_bounds_test.go`'s shape: a small, ordinary source spec must
still validate after the change, so a `validateSourceSpec` that had started refusing everything
cannot pass the refusal cases. The existing `TestValidateJobSpec_Source_Perforce` happy-path row is
that control and must stay green untouched.

**No new integration lane, no new tag, no `//go:build` anything.** If a plan proposes one, that is
the signal that the test was written against the wrong subject.

## 13. Measurements deferred to plan Task 0

None of these can be taken by a doc-only agent, and none of the numbers they produce may be written
into a comment, a commit message or README before they exist.

1. **The body ceiling `C`.** Construct a maximal JSON body under `maxBodyBytes` consisting of one
   task with a degenerate stream and the smallest legal sync entry, submit it through the decoder
   `readJSON` uses, and count the entries that arrive. Section 4 offers a hand count of 28 bytes per
   entry and an order-of-37,000 division as the INPUT to be checked, not as the answer. Every
   "reduction factor" sentence anywhere in the slice is blocked on this number.
2. **The argv byte length at the bound.** For a realistic client path, compute the command line
   `SyncStream` builds at 512 entries and compare it against the documented platform command-line
   limits. Report the number. Do NOT report a claim about whether exec fails at that boundary unless
   it was actually run; section 10 states the hazard as open on purpose.
3. **Whether any fixture in the tree builds a source spec with more than 512 sync entries.** A new
   bound is retroactive over test fixtures as well as over stored rows, and a fixture that goes red is
   cheap to find before the change and expensive to diagnose after. Search the whole tree including
   `python/`, `web/` and `docs/`, not just `internal/`. Section 5's claim that the largest existing
   fixture is 18 entries is from a targeted read and is exactly the kind of complement claim that
   needs a search behind it.
4. **A green baseline before any mutation.** The mutation table in section 12 is meaningless without
   one, and a compile error is not a kill.

## 14. Scope fence and cross-lane notes

This lane owns `internal/jobspec/` and its tests. The design as written stays inside that, plus the
one README row in section 9 and the one table row in `internal/api/job_spec_source_test.go`.

**One cross-lane item the conductor must sequence, flagged rather than designed around.**
`internal/schedrunner/stored_spec_count_bounds_test.go` is a table over the count bounds proving that
the STORED-spec paths refuse with the bound's own message, and a fourth row for this bound is the
obvious place a reader would look for it. `internal/schedrunner/` belongs to LANE B in this batch.
**This design does not add that row**, on the argument that the file's own comment already makes: the
three existing rows plus the control prove the delegation carries a bound's OWN message, the new
bound reaches `ValidateStoredSchedule` through the same single `jobspec.Validate` call with no new
wiring, and the callers' wiring is pinned message-agnostically elsewhere. So the marginal value of a
fourth row is one more vocabulary item on a delegation already proven. If the conductor disagrees, the
row belongs to LANE B and must be sequenced after it, not written here.

No other lane's files are touched: `internal/worker/`, `internal/agent/source/perforce/` (read only,
for the cost model in sections 1 and 3), `internal/store/query/worker_workspaces.sql`,
`cmd/relay-server/main.go`, `internal/store/query/scheduled_jobs.sql` and
`internal/testsupport/pgdsn/` are all outside this design.

## 15. Recommended backlog items

Proposals for the conductor. Not filed here, and each is specific because it was found by reading the
symbol rather than by inference.

1. **`BaselineHashFromAPISpec` is called inside `selectWorker`'s worker loop.** `warmKey` is hoisted
   above the loop with a comment explaining why; the baseline estimate one branch below is invariant
   in the worker being scored and is recomputed per candidate holding a matching warm workspace, on
   every dispatch attempt, over every sync entry. Hoisting it beside `warmKey` is a small change with
   the same argument the existing hoist already carries. `internal/scheduler/dispatch.go`.
2. **`SyncStream`'s argv length is bounded by nothing.** One element per include, path length capped
   only by the request body. Platform command-line limits are real and relay has never captured what
   p4 does at that boundary. The remedy is probably a filespec file or chunked invocations, not a
   tighter count bound. `internal/agent/source/perforce/client.go`.
3. **`Sync`'s docstring in `python/src/relay/models.py` enumerates the server-side sibling rules and
   will be one short** once this bound lands. A one-clause generalisation, no number copied. Small,
   and it belongs to whoever owns `python/`.
4. **`bug-2026-08-29-source-unshelves-is-one-subprocess-per-entry-and-unbounded` should be re-read
   against this slice, not closed by it.** This design corrects the sibling item's premise that the
   sync axis was the only unbounded one, which leaves the unshelves axis as the remaining open one on
   the same spec, with the better byte-per-spawn ratio.
