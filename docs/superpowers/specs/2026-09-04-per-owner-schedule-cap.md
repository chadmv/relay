# A per-owner cap on scheduled_jobs

- Date: 2026-09-04
- Backlog item: `docs/backlog/feature-2026-09-04-per-owner-schedule-cap.md`
- Subject: `handleCreateScheduledJob`, `scheduled_jobs`, `RELAY_MAX_SCHEDULES_PER_OWNER`
- Status: design, awaiting review
- Predecessor slices: `docs/superpowers/specs/2026-09-04-boot-sweep-keyset-paging.md` (shipped;
  bounded the sweep's memory), `docs/superpowers/specs/2026-09-04-authenticated-route-rate-limiting.md`
  (decided the rate-limit half of this item OUT)

## TL;DR

`POST /v1/scheduled-jobs` is bare `auth(...)` (`internal/api/server.go:306`), so any authenticated
principal may grow `scheduled_jobs` without limit. This slice bounds the number of `scheduled_jobs`
rows one owner may hold, enforced in `handleCreateScheduledJob` inside one transaction: a per-owner
row lock, a bounded count, then the insert. Over the cap the route answers `409`. Admins are not
exempt. Owners already over the cap are grandfathered: nothing is deleted, disabled or flagged, and
every route except creation keeps working for them.

The cap bounds the boot sweep's STARTING work set to `(rows existing when it lands) + owners x cap`.
It does not bound the sweep's DURATION, and the acceptance criteria below do not claim it does. The
control that would bound duration is a wall-clock deadline on the sweep, proposed here as its own
item.

## What I checked in the item, and what I refuted

Every claim in `feature-2026-09-04-per-owner-schedule-cap.md` was read against this tree before any
design was written. Five held, three did not.

**Held.**

1. "`POST /v1/scheduled-jobs` is bare `auth(...)`." Confirmed:
   `mux.Handle("POST /v1/scheduled-jobs", auth(http.HandlerFunc(s.handleCreateScheduledJob)))`,
   `internal/api/server.go:306`. Its two siblings on the same surface, `POST /v1/jobs/{id}/retry`
   and `POST /v1/scheduled-jobs/{id}/run-now`, ARE wrapped (`auth(userLimit(...))`, lines 253 and
   316), so the bare wrap is a deliberate gap rather than an oversight.
2. "The rate limit is OUT, decided not deferred." Confirmed against the rate-limiting spec's
   "Out: `POST /v1/scheduled-jobs`" section and against the shipped README row for
   `RELAY_JOB_SUBMIT_RATE_LIMIT`, which states it in prose an operator reads.
3. "`schedrunner.fireOne` runs on the runner goroutine and never touches an HTTP route." Confirmed:
   `Runner.Run` ticks at `TickInterval` and calls `TickOnce`, which is the only caller of `fireOne`.
4. "An owner sitting at the cap can DELETE one schedule and POST another indefinitely, and a count
   cap bounds N0 alone." Confirmed, and the mechanism is exactly as stated: every page of the sweep
   is a separate statement and therefore a separate snapshot, and `id` is `gen_random_uuid()`, so an
   inserted row joins the remaining work set with probability equal to the unswept fraction of the
   key space.
5. "`handleRunScheduledJobNow` ... is bucketed by the submit rate limit, but that bounds job
   creation, not schedule count." Confirmed on both halves.

**Refuted.**

6. **"A per-owner `COUNT(*)` at create time is the obvious shape; note it is a read-then-write across
   two statements unless it is done in one."** Doing it in one statement does not fix the race, so
   the item's parenthetical points at the wrong remedy. The single-statement form is

   ```sql
   INSERT INTO scheduled_jobs (...)
   SELECT ... WHERE (SELECT COUNT(*) FROM scheduled_jobs WHERE owner_id = $2) < $9
   ```

   and under READ COMMITTED its count subquery is evaluated against the snapshot taken when the
   statement began, which cannot see a concurrent uncommitted insert. Two requests at `cap - 1`
   therefore both pass and both commit. That is not a hypothetical: it is the same overshoot
   `internal/worker/handler.go`'s auto-enroll ceiling documents in its own comment ("two concurrent
   auto-enrolls at `n == ceiling-1` both pass the check under read-committed isolation and both
   insert"). Exactness needs a lock taken in an EARLIER STATEMENT of the same transaction, so that
   the counting statement's snapshot is taken after the competitor has committed. One statement is
   neither necessary nor sufficient. See "Decision 1".

7. **"Note the same hazard applies to `handleRunScheduledJobNow`."** It does not apply in any degree.
   That handler calls `CreateJobFromSpec` on a stored spec (`internal/api/scheduled_jobs.go:1078`);
   it inserts into `jobs` and `tasks` and cannot create a `scheduled_jobs` row at all, so the
   table-size hazard this item is about has no surface there. The item's own conclusion (the submit
   bucket bounds job creation, not schedule count) is right; the premise above it is not. See
   "Decision 5".

8. **Acceptance criterion "The boot sweep's STARTING work set is bounded as a consequence"**, stated
   without qualification. The starting work set after this slice is
   `(rows that exist when the cap lands) + owners x cap`. Grandfathering means the first term is not
   reduced at all, and the owner population is itself unbounded when `RELAY_ALLOW_SELF_REGISTER` is
   on: `handleRegister` is bounded only by `RELAY_REGISTER_RATE_LIMIT` per source address, keyed per
   IPv6 `/128`, which the README's submit row already records as a weak brake. The criterion is
   rewritten below to state the bound as the function it is.

**One staleness note, not a contradiction.** The item is written as though the paging slice is
prospective ("`docs/superpowers/specs/2026-09-04-boot-sweep-keyset-paging.md` keyset-pages
`ValidateStoredSpecsOnStartup`"). It has landed: `ListEnabledScheduledJobsPage`, `sweepPageSize`, the
page loop and the `ctx.Err()` return are all in the tree, and the sweep's header cites this item by
filename. Nothing in the item's reasoning depends on the tense.

**One thing neither the item nor the paging spec says, and it changes what may be claimed at boot.**
`ReconcileOnStartup` also runs before `srv.ListenAndServe()`, and its statement
`ListOverdueScheduledJobsForCatchup` (`internal/store/query/scheduled_jobs.sql:130`) carries **no
LIMIT of any kind**. So the boot path still holds one unbounded result set in memory, for the overdue
subset, and the paging slice's "peak memory is one page" is a true statement about the SWEEP and a
false one about the BOOT. This slice does not fix that either; it is named in "Backlog items this
spec proposes".

## Decision 1: the cap, and the statement that enforces it

### Where

In `handleCreateScheduledJob`, which is the only production caller of `CreateScheduledJob`
(the other twenty-odd call sites are test fixtures). The cap is an ADMISSION policy, exactly like
`minScheduleInterval` and `ValidateJobSpec` beside it: the store does not enforce it, no constraint
or trigger enforces it, and `ValidateStoredSchedule` never learns about it. Two consequences that
must both survive review:

- Test fixtures calling `q.CreateScheduledJob` directly are unaffected. The schedrunner paging test
  plants 250 rows for one owner and must keep passing.
- A CHECK constraint or a trigger would be retroactively hostile in the way a validator tightening
  usually is: it would make an over-cap owner's rows unwritable by any statement, including the
  sweep's own `RecordScheduledJobFailure`. Do not implement it there.

### The sequence

One transaction, opened from `s.pool` the way `handleRunScheduledJobNow` and `handleAdminArchiveUser`
already do, containing three statements in this order:

```
tx := s.pool.Begin(ctx); defer tx.Rollback(ctx); txq := s.q.WithTx(tx)

1. LockOwnerForScheduleCap(ctx, u.ID)            -- SELECT id FROM users WHERE id = $1 FOR NO KEY UPDATE
2. CountScheduledJobsForOwnerUpTo(ctx, {u.ID, cap})
   if n >= cap -> 409, return (tx rolls back, nothing written)
3. CreateScheduledJob(ctx, ...)                  -- unchanged statement
tx.Commit(ctx)
```

**The transaction opens AFTER all body validation.** `readJSON`, the required-field checks, the
`overlap_policy` check, `json.Unmarshal`, `ValidateJobSpec`, `ValidateMinInterval` and `ParseSchedule`
all run first and all are CPU-only. Putting the cap check ahead of them would make a malformed-body
flood into a lock-acquisition flood on the owner's `users` row, buying nothing: an invalid request
cannot create a row whether the owner is at the cap or not.

### Statement 1: the lock, and why it is `FOR NO KEY UPDATE` on `users`

```sql
-- name: LockOwnerForScheduleCap :one
SELECT id FROM users WHERE id = sqlc.arg(owner_id)::uuid FOR NO KEY UPDATE;
```

- **It must be its own statement, before the count.** Under READ COMMITTED a statement's snapshot is
  taken when the statement starts. A lock acquired part-way through the counting statement is granted
  after the competitor commits, but the count has already been evaluated against the older snapshot,
  so merging the two re-opens the exact race the lock exists to close. This is the load-bearing
  sentence of the whole design and it goes in the query's comment.
- **`FOR NO KEY UPDATE`, not `FOR UPDATE`.** `FOR UPDATE` conflicts with `FOR KEY SHARE`, which is
  what any insert of a row referencing `users(id)` takes. Under `FOR UPDATE`, one caller's schedule
  creation would block that same caller's concurrent `POST /v1/jobs`. `FOR NO KEY UPDATE` conflicts
  with itself, which is all this needs, and does not conflict with `FOR KEY SHARE`.
- **Not an advisory lock.** `pg_advisory_xact_lock` would need a new global key space (golang-migrate
  already uses the bigint advisory space during migration, per
  `internal/testsupport/pgdsn/pgdsn.go:108`), a hash of the owner id, and a written collision
  argument. The `users` row is already the natural home of a per-owner budget, needs no hash, and the
  FK on `scheduled_jobs.owner_id` guarantees the row exists.
- **Lock ordering, so this cannot deadlock with the archive path.** `handleAdminArchiveUser` takes
  `users` (through `ArchiveUser`'s UPDATE) and then `scheduled_jobs` (through
  `DisableScheduledJobsByOwner`). This transaction takes `users` and then inserts into
  `scheduled_jobs`. Same order, so there is no cycle. Any future transaction touching both tables
  must take `users` first; say so in the query comment, since it is a constraint the code cannot show.
- **`pgx.ErrNoRows` is unreachable and still handled.** There is no `DELETE FROM users` statement in
  `internal/store/query/` (searched; the only match is `invites.sql`'s comment asserting the same
  thing), and users are archived rather than deleted. Handle it as `401` and log it, so a future hard
  delete fails closed instead of skipping the count.

### Statement 2: the count is BOUNDED, and that is not an optimization

```sql
-- name: CountScheduledJobsForOwnerUpTo :one
SELECT COUNT(*) FROM (
  SELECT 1 FROM scheduled_jobs
   WHERE owner_id = sqlc.arg(owner_id)::uuid
   LIMIT sqlc.arg(ceiling)::int
) t;
```

A plain `COUNT(*)` would make every refused create cost an index scan proportional to how many rows
the owner already holds. Since owners over the cap are grandfathered (Decision 3) and this route is
in no rate-limit bucket, that hands the actor who is already over the cap an amplification primitive
that grows with the damage they have already done. The inner `LIMIT` makes the check cost O(cap)
whatever the owner holds, and it answers exactly the question asked - "is the count at least
`ceiling`" - with no loss on that predicate.

**The returned value is a bounded count and must never be read as a census.** It saturates at
`ceiling`. Nothing may serve it, log it as a total, or feed it into
`handleScheduledJobStats`, which has its own real count in `ScheduledJobCounts`. State that in the
statement's comment, beside the `LIMIT`, where a future reader wiring it into a payload will see it.
The refusal message below therefore says "at the limit" and never "you own N".

**No new index.** `idx_scheduled_jobs_owner ON scheduled_jobs(owner_id)`
(`internal/store/migrations/000006_scheduled_jobs.up.sql:18`) already serves this predicate.

**A new statement rather than reuse of `CountScheduledJobsByOwner`.** That one carries the list's two
optional filters and its `LEFT JOIN users`, and calling it with both filters NULL happens to return
the same number today. Reusing it would tie the cap's meaning to the list's filter vocabulary, and
`parseScheduleFilters`' own comment records the failure shape: a call site that omits a filter field
disables that filter silently, with no error. A cap that changed meaning because someone added a
filter arm to a list query is not a cap.

### What a concurrent pair does

Two requests from the same owner, who holds `cap - 1` schedules:

- A takes the lock. B's lock statement blocks.
- A counts `cap - 1`, inserts, commits. The lock is released at commit.
- B's lock statement returns. B's COUNTING STATEMENT NOW BEGINS, and takes a fresh snapshot that
  includes A's committed row. B counts `cap` and answers 409.

Exactly one insert. The property holds across replicas, because the lock is in the database rather
than in the process.

Two requests from DIFFERENT owners never contend: they lock different `users` rows.

### What is deliberately NOT bounded by this

The cap counts rows, not identities. An actor with M accounts holds `M x cap` schedules, exactly as
the submit bucket is a per-principal budget and not a fleet ceiling. That limitation is inherited
rather than introduced, it is the same one the README's submit row already states, and the README row
for this variable must state it too rather than let an operator read "capped" as a fleet number.

### Counting ALL rows, not just enabled ones

The cap counts every `scheduled_jobs` row the owner holds, whatever `enabled` says. The boot sweep
reads only enabled rows, so a cap on enabled rows alone would look like the tighter fit. It is the
wrong choice for two reasons:

- **It would create a second enforcement point.** `handlePatchScheduledJob` can flip `enabled` to
  true. Under an enabled-only cap, "create a million disabled schedules, then PATCH them enabled" is
  the evasion, so PATCH would have to enforce too - and a PATCH that refuses is a PATCH that can
  refuse an owner's attempt to REPAIR a broken schedule. Counting all rows means PATCH cannot
  increase the count and therefore needs no check at all, leaving exactly one enforcement point.
- **Disabled rows are not free.** They occupy every list page, every `CountScheduledJobs`, the admin
  fleet-wide census, and the table itself.

## Decision 2: admins are NOT exempt

The two prior decisions in this batch cut in different directions and neither transfers unchanged, so
the argument is made here rather than cited.

- The submit bucket refused an admin exemption on the grounds that "an exempt class of caller is a
  control that does not exist for the people most able to trip it".
- `RELAY_PASSWORD_CHANGE_RATE_LIMIT` left `POST /v1/users/password-reset` and `POST /v1/users`
  unbounded on a different argument: those routes are admin-only, so the set that can drive them is
  the admin table rather than anyone who can create an account, and rate-limiting them refuses an
  operator's remedy during the incident they are responding to.

That second argument is about routes only admins can reach. `POST /v1/scheduled-jobs` is reachable by
every authenticated principal, so exempting admins would not shrink the set that can drive the route
by one caller. It would only carve a hole in a control everyone else is subject to, for the
population most likely to be running the automation that fills a table. The boot sweep does not care
whose rows they are.

The remedy question that made the password-change carve-out right does not arise either: creating a
scheduled job is not an incident remedy. An admin who needs one more schedule during an incident
deletes one, or raises the variable and restarts - the same two moves anyone else has.

**Owner and caller are the same principal at the only enforcement point.** `handleCreateScheduledJob`
sets `OwnerID: u.ID` unconditionally; there is no route on which one principal creates a schedule
owned by another. So the divergence `UserRateLimit`'s doc comment warns about - the bucket charged to
the caller while the work is charged to the owner - cannot occur here, and "per owner" and "per
caller" name the same quantity. If a create-on-behalf-of route is ever added, the count must stay
keyed on the OWNER, because the owner is who the sweep's cost is charged to.

**What an admin sees that an owner does not.** Nothing new. The admin arm of
`handleListScheduledJobs` and `handleScheduledJobStats` already serve fleet-wide numbers, so an admin
can already see who holds what. This slice adds no per-owner quota column to any payload; see
"What this slice does NOT cover".

## Decision 3: owners already over the cap are GRANDFATHERED

Tightening a validator is retroactive over stored data and the readers are hard to find, so they are
enumerated rather than asserted. Every production reader and writer of `scheduled_jobs`:

| Site | What it does | Effect of the cap |
| --- | --- | --- |
| `handleCreateScheduledJob` | the only row-creating path | THE enforcement point: 409 at or above the cap |
| `handleListScheduledJobs` | owner-scoped and admin arms, paged | unchanged; an over-cap owner lists and pages normally |
| `handleGetScheduledJob` | one row, owner-or-admin | unchanged |
| `handlePatchScheduledJob` | rewrites mutable columns in place | unchanged, and deliberately NOT an enforcement point: it cannot increase the count, and a refusal here would block an over-cap owner from repairing or disabling a schedule |
| `handleDeleteScheduledJob` | removes one row | unchanged, and it is the self-service remedy; it must never be refused |
| `handleRunScheduledJobNow` | creates a JOB from a stored spec | unchanged; creates no `scheduled_jobs` row (Decision 5) |
| `handleScheduledJobStats` -> `ScheduledJobCounts` | owner-scoped or fleet-wide census | unchanged; this is where an owner sees their own `total` |
| `schedrunner.TickOnce` -> `ListEligibleScheduledJobs` | fires due schedules, `LIMIT BatchLimit` | unchanged; an over-cap owner's schedules keep firing |
| `ReconcileOnStartup` -> `ListOverdueScheduledJobsForCatchup` | unpaged read at boot | unchanged, and still unpaged |
| `ValidateStoredSpecsOnStartup` -> `ListEnabledScheduledJobsPage` | paged diagnostic sweep | unchanged; its work set stops GROWING without limit, and does not shrink |
| `handleAdminArchiveUser` -> `DisableScheduledJobsByOwner` | disables an archived user's schedules | unchanged; disabled rows still count, and the archived user has no tokens left anyway |
| `CountActiveJobsForSchedule` | overlap policy, reads `jobs` | unaffected |
| CLI `relay schedules ...`, MCP `relay_*_schedule` | HTTP clients of the routes above | inherit the create refusal for free |
| `web/src/schedules/` | list, get, patch, delete, run-now | **has no create call at all**; no frontend work in this slice |

**The decisive fact this enumeration produces:** unlike `jobspec.Validate`, whose rules are
retroactive precisely because `ValidateStoredSchedule`, `fireOne` and the boot sweep RE-RUN them over
stored rows, the cap has no re-running reader. Nothing else in the tree counts an owner's schedules.
So the retroactivity is confined to one route, and the whole grandfathering question is "what does an
over-cap owner lose". Answer: the ability to create another schedule, and nothing else.

**Rejected alternatives, so the choice is a decision:**

- *Delete the excess.* Destroys an operator's configuration on a deploy, on a rule they have never
  seen, with no undo.
- *Disable the excess.* Silently stops production work. Disabling is currently an explicit act with a
  visible cause (`relay schedules update --enabled=false`, or an admin archiving the owner); making a
  version upgrade a third cause teaches operators that `enabled=false` means nothing.
- *Record it in `last_error`.* Puts an owner-scoped, configuration-derived fact into a per-row field
  whose contract is "this row cannot produce a job", on rows that are individually fine, and it would
  be written by the boot sweep - a pass whose whole audience is long-cadence schedules nobody is
  watching. The row's owner could not clear it except by deleting rows.

**What grandfathering costs, stated where the criterion is written:** the cap does not shrink an
existing table by one row, so on the deploy that lands it the boot sweep's work set is exactly what it
was the day before. The bound is on growth from that moment.

## Decision 4: the duration question, and where it goes

**The cap does not bound the sweep's duration, and this slice's acceptance criteria do not claim it
does.** The item is right about the mechanism and right that a decision is owed before acceptance can
say otherwise.

What a count cap actually buys: a bound on `N0`, the work set the sweep starts with. What it cannot
buy: a bound on the pass, because every page is a separate snapshot and an owner at the cap can
delete one row and post another for as long as the sweep runs. The pass converges - the unswept
fraction of the key space only shrinks - so this is amplification and not non-termination.

**Decision: a wall-clock deadline on the sweep, as its own item, not here.** The reasoning:

- **A page ceiling bounds round trips, not time.** `maxSweepPages` gives deterministic coverage,
  which is attractive for a diagnostic pass, but the quantity that delays the boot is seconds. With
  `RELAY_DB_STATEMENT_TIMEOUT` at 30s each, a page ceiling of P bounds duration at `P x 30s`, which
  is not a useful number. The property asked for is time, so bound time.
- **The mechanism already exists.** `ValidateStoredSpecsOnStartup` checks `ctx.Err()` at the top of
  the row loop and returns it, and `cmd/relay-server` already logs that return as a warning. A
  deadline is `context.WithTimeout` around the existing call plus one env variable.
- **The hazard that item must solve, named now so it is not discovered late:** a truncated sweep
  under-reports, and an empty failure surface reads as "nothing is broken" - the exact invisibility
  the sweep exists to fix. The deadline's log line must say the pass was cut short and how far it
  got, and that is a design constraint on that item rather than a caveat on this one.
- **It touches no file this slice touches**, and this slice is not a prerequisite for it.

Separately, and worth writing into that item: a deadline on the sweep does not bound
`ReconcileOnStartup`, which also runs pre-listen and is unpaged.

## Decision 5: `handleRunScheduledJobNow` has no surface here

It creates a job from a stored spec inside its own transaction. It inserts into `jobs` and `tasks`
and never into `scheduled_jobs`, so it cannot move the count in either direction and is neither an
enforcement point nor an evasion. It is already in the submit bucket
(`auth(userLimit(...))`, `internal/api/server.go:316`), which bounds how much execution it buys per
window, and that is unchanged.

The one true interaction is worth recording because it is the closest thing this cap has to an
execution bound: `fireOne` never touches an HTTP route, so no HTTP limiter bounds a schedule's own
firing, and until now nothing bounded how many firing schedules one owner could own. **That is not
the same as "the cap is the only bound on schedule-driven job creation", and the tree says
otherwise:** `ListEligibleScheduledJobs` runs with `LIMIT BatchLimit` (100) once per `TickInterval`
(10s), so schedule-driven job creation is already bounded fleet-wide at 100 per tick regardless of how
many schedules exist. What the cap changes is how much of that fleet-wide batch one owner can demand.

It does not make the batch fair, and that is a separate gap: `ListEligibleScheduledJobs` orders by
`next_run_at` with no per-owner term, so one owner with many due schedules can occupy the whole batch
and delay everyone else's fires. The cap mitigates that in proportion to its value and does not fix
it. Proposed as an item below.

## Decision 6: the configuration

**Variable: `RELAY_MAX_SCHEDULES_PER_OWNER`. Default `100`. Parsed by a new
`parseScheduleCap(name, raw string) (int, string)` in `cmd/relay-server`, in the three-outcome shape
of `parseAutoEnrollCeiling`, with one arm deliberately removed.**

```
- unset:                       DefaultMaxSchedulesPerOwner, no message
- a valid integer >= 1:        used as-is, no message
- 0, negative, or unparseable: DefaultMaxSchedulesPerOwner, plus a warning naming the ignored value
```

**What the parser refuses, checked against the parser rather than asserted.** This is the claim the
password-change slice got wrong and had to delete from three places, so it is stated as behaviour and
pinned by the table test in the acceptance criteria:

- There is no off token and no environment value that disables the check. `0` does not disable it;
  `0` is folded to the default and warned about, which is the one arm where this parser diverges from
  `parseAutoEnrollCeiling`. That divergence has a reason: the auto-enroll ceiling gates a path with a
  non-refused alternative (enrollment tokens are never refused by it), so an operator on a trusted
  network can legitimately turn it off. This gates the only route that creates a scheduled job, and
  the sweep's own header cites this cap as the thing that bounds its starting work set.
- **It is NOT fatal on a bad value.** A typo must not stop a farm booting when a safe default exists,
  which is the reason `parseAutoEnrollCeiling`, `parseWatchdogDuration` and `parseConnLimit` all warn
  and continue. The rate-limit family fatals; this is not in that family.
- **The honest statement of "effectively off", which is what must go in the README.** An arbitrarily
  large value is accepted and is the spelling for effectively-unbounded. That is deliberate, and it
  differs from an off token in the way that matters: it stays visible as a number in the environment
  and in the startup line, so nobody reads a bound that is not there. Do not write "an operator
  cannot disable this from the environment" anywhere; write what the parser does.

**A startup line, unconditional**, in the shape of `autoEnrollCeilingLine` and `watchdogBoundsLine`:
name the effective cap, and say that existing owners over it keep their schedules and are refused new
ones. An operator upgrading into this needs to see the number without reading the release notes.

**`internal/api` gets `const DefaultMaxSchedulesPerOwner = 100` and a field
`Server.MaxSchedulesPerOwner int`, resolved through a method that folds a non-positive value to the
default**, exactly as `Handler.autoEnrollWorkerCeiling()` does and for the reason its comment gives:
"so a direct-construction caller fails bounded rather than refusing everything". Two consequences,
both wanted:

- Every `&Server{}` and `api.New(...)` in the test lanes gets the default with no edit.
- A deleted or crossed wiring assignment in `buildHTTPServer` degrades to "the operator's number was
  ignored", not to "the control is off". The acceptance criteria still guard the assignment; this is
  about which way it fails when a guard is missed.

It threads as a NAMED FIELD, never as another positional argument to `api.New`, whose tail is already
four same-typed arguments in a row with a measured green transpose across them:
`httpServerDeps.maxSchedulesPerOwner` -> `s.MaxSchedulesPerOwner`.

### Why 100, and what it must not refuse

**The number is a judgement, not a measurement, and nothing in this tree can measure it.** There is
no rate to time here, unlike the submit bucket's CLI for-loop. What decides it is the largest
legitimate per-owner population anybody can name against what the number permits.

- **Must not refuse:** a solo operator or a studio maintaining one schedule per project per cadence -
  a nightly build, a weekly cleanup, a per-show render - which is tens at the outside. Nor a user
  paging their own list, which the SPA does 50 at a time.
- **Deliberately refuses:** a pipeline service account that mints one schedule per shot or per asset.
  That is the pattern that fills the table, it is the realistic support ticket, and the README must
  name it with its two remedies: one schedule whose `job_spec` fans out into tasks (the model relay is
  built around, and the same advice the submit row gives), or raise the number.
- **The other side of the number, re-derived rather than assumed:** `minScheduleInterval` is 30
  seconds, so an owner at 100 schedules can demand up to 200 fires per minute, forever, and no HTTP
  limiter touches a fire. That demand is served against a fleet-wide `BatchLimit` of 100 per 10s
  tick, so it does not translate into 200 jobs a minute on a busy farm - it translates into occupying
  the batch. Doubling the cap doubles both.
- **The shared-account failure mode, which the README row must carry:** in a studio where one service
  account owns every schedule, that one account hits the cap while every artist owns zero. The cap is
  per owner and owners are not evenly loaded. The remedy is to raise the number, which is visible.

### The refusal

`409` with `writeError`, message: `scheduled job limit reached: this account is at the per-owner
limit of N. Delete a scheduled job before creating another.`

- **409, not 429 and not 400.** It is not a rate and the input is not invalid.
  `relayclient.ErrorIsTransient` classifies 409 as NOT transient (`internal/relayclient/client.go:54`),
  which is right: no poller should retry it, and the caller must act.
- **The message names the cap and never the count.** The bounded count saturates, so any "you own N"
  phrasing would be a claim the statement cannot support.
- **The message does not name the environment variable and does not say "ask an operator to raise
  it".** The self-service remedy (delete one) is in the message; the operator remedy belongs in
  README. A refusal an actor can drive should not advertise, to that actor, a remedy that loosens the
  control - the same reading of the remedy-ladder rule that keeps "set it to 0" out of the
  auto-enroll ladder.
- **Clients:** the CLI prints `ResponseError.Message` verbatim (`Error()` returns `Message`). MCP maps
  409 to `{Code: "conflict", Message: <as sent>}` with the generic hint "another change conflicts with
  this request", which reads oddly for a quota refusal. Accepted for this slice - the message carries
  the truth and changing `MapError`'s 409 hint would change it for every 409 - and noted as a
  candidate item. The SPA has no create call, so nothing there renders it.

## Advertisement surfaces

- **README env table**, a new row after `RELAY_AUTO_ENROLL_WORKER_CEILING` so the two count ceilings
  are adjacent. It must carry: the default; that it is **per owner and not a fleet ceiling**, so M
  accounts hold `M x cap`; that it counts ALL of an owner's rows, enabled or not, and why (a PATCH
  cannot increase the count, so creation is the only enforcement point); that owners already over it
  keep every schedule and are refused only new ones; the 409 and the delete remedy; the shared
  service-account failure mode and the fan-out-into-tasks alternative; that there is no off value,
  what the parser does with `0` and with a typo, and that a large number is the spelling for
  effectively-unbounded; that it needs a restart; and, plainly, **that it bounds the boot sweep's
  starting work set and not the sweep's duration**.
- **README `RELAY_JOB_SUBMIT_RATE_LIMIT` row** currently reads "**`POST /v1/scheduled-jobs` is
  deliberately not in this bucket** - a creation-rate limit bounds how fast `scheduled_jobs` fills,
  not how full it gets". That stays true and becomes incomplete: add the clause naming the variable
  that does bound how full it gets. Do not remove the existing sentence; it is the record of a
  decision.
- **The scheduled-jobs section of the API reference** gains the 409 on `POST /v1/scheduled-jobs`.
- **`ValidateStoredSpecsOnStartup`'s header** currently says "A per-owner schedule cap WOULD bound the
  STARTING work set and not the pass" and cites this backlog item by filename. Once this lands the
  conditional is false. Rewrite it to name the variable, keep the unchanged half (the pass is not
  bounded, and every page is a fresh snapshot), and re-point the citation at whatever item carries
  the sweep deadline. Follow the comment rules: the constraint stays, the argument for it goes in the
  commit message and here.
- **Doc comments this slice owes**, and no more than these:
  `LockOwnerForScheduleCap` (why the lock is a separate statement, why `FOR NO KEY UPDATE`, and the
  `users`-then-`scheduled_jobs` ordering rule); `CountScheduledJobsForOwnerUpTo` (the inner LIMIT, and
  that the result saturates and is never a census); `DefaultMaxSchedulesPerOwner` (what the number
  must not refuse and what it deliberately does); `Server.MaxSchedulesPerOwner` (non-positive folds to
  the default, and why that is the right failure direction). None of them may carry a census of other
  files, a count of anything elsewhere, a uniqueness claim about other code, or any history.

## Testing

### Lane facts, checked rather than assumed

- `.github/workflows/go-ci.yml`'s `test` job runs `go test -race ./...` with **no tags**, so every
  `//go:build integration` file is compiled by `make vet-integration` and not run there.
- `internal/api`'s handler harness (`api_test.go`, `testhelper_test.go`) is integration-tagged, and
  `make test-integration` is run by no CI job. A sibling lane is in flight to give `internal/api` its
  own CI job; nothing below depends on that landing.
- **`cmd/relay-server` IS in a CI lane.** `make test-pg-integration` runs
  `./internal/store/... ./internal/schedrunner/... ./internal/testsupport/... ./cmd/relay-server/...`
  with `-tags integration`, and the `pg-integration` job runs that target against a
  `services: postgres`. `cmd/relay-server` already has integration tests that take a DSN from
  `internal/testsupport/pgdsn` and drive requests through `buildHTTPServer`'s handler
  (`grpc_admission_e2e_integration_test.go`). **That is where the headline guards go**, and it is why
  this slice does not have to wait for the `internal/api` lane.

### Acceptance criteria, each naming the mutation it kills

**AC1. At a configured cap of 2, the first two creates succeed and the THIRD is refused with 409.**
Lane: `cmd/relay-server`, integration, real Postgres via `pgdsn`, server built by
`buildHTTPServer(httpServerDeps{maxSchedulesPerOwner: 2, ...})`.
*The third request is not optional.* Two successes under a cap of 2 are also exactly what an absent
control produces, which is the defect this batch shipped once already and fixed in review.
Kills: the check never runs; `>` instead of `>=`; the check placed after the insert.

**AC2. The refusal uses the value `buildHTTPServer` was GIVEN.** Same test, same fixture: under a
hard-coded `DefaultMaxSchedulesPerOwner`, or a deleted or crossed
`s.MaxSchedulesPerOwner = d.maxSchedulesPerOwner`, the third create succeeds and AC1 goes red for the
right reason. Leave every other limit field at zero in the fixture, so a crossed assignment produces a
zero and therefore the default rather than a plausible number.
Kills: hard-coded cap; deleted assignment; crossed assignment.

**AC3. Two concurrent creates by an owner at `cap - 1` produce exactly one row.** Lane:
`internal/store`, integration (CI runs it). Two connections, sequenced deterministically rather than
by timing: A locks, A counts, B attempts the lock and blocks, A inserts and commits, B's lock returns,
B counts, B sees the cap.
*With the control that proves the test can see the race:* the same sequence with statement 1 omitted
must end with TWO rows. Without that control, a green result is indistinguishable from a test whose
sessions never overlapped.
*Bound the failure:* B's blocked acquisition runs under a context timeout, so a mutant that never
blocks or never releases fails by name instead of hanging - a hang is indistinguishable from
infrastructure trouble.
Kills: count-then-insert with no lock; the lock merged into the counting statement; the
single-statement conditional INSERT; `FOR SHARE` or any lock mode that does not conflict with itself.

**AC4. An admin is refused at the cap exactly as a non-admin is.** Lane: `cmd/relay-server`,
integration (real users, real tokens). Two owners, one admin and one not, same server, cap 2: both get
201, 201, 409. Order the ADMIN case first, so an early-exit exemption cannot pass by never being
reached.
Kills: `if u.IsAdmin { skip the check }`.

**AC5. An owner over the cap keeps everything except creation.** Lane: `internal/api`, integration
(does NOT run in CI today). Seed `cap + 3` rows for one owner through the store, then assert: list
returns them, get returns one, PATCH succeeds (including `enabled` true), run-now succeeds, DELETE
succeeds, and only POST is 409.
Kills: a cap check added to PATCH, to run-now, or to the store.
*If the `internal/api` CI lane lands, this test moves there unchanged and starts running in CI.* The
PATCH and run-now arms are the ones that most want it, because they are the arms where a later
"consistency" edit would add a check.

**AC6. The store does not enforce the cap.** Lane: `internal/store`, integration (CI runs it). Insert
`cap + 5` rows for one owner through `CreateScheduledJob` directly; all succeed.
Kills: the cap implemented as a CHECK constraint or a trigger, which would break every fixture and
would make an over-cap owner's rows unwritable by the sweep.

**AC7. The parser's outcomes.** Lane: `cmd/relay-server`, default lane (CI runs it). Table, with the
`0` row FIRST: `0` -> default plus a warning naming the ignored value; `-1` -> same; `"abc"` -> same;
`""` -> default, no message; `"7"` -> 7, no message.
Kills: `0` treated as disabled; a silently ignored typo; `<= 0` treated as "use as-is".

**AC8. main passes the parsed value into the deps literal.** Lane: `cmd/relay-server`, default lane,
in the shape of `TestAutoEnrollCeilingIsWiredIntoTheHandler`: parse `main.go`, find the
`httpServerDeps` composite literal, require a `maxSchedulesPerOwner` key whose value derives
transitively from `parseScheduleCap`.
Kills: the zeroed or deleted literal field, which is the mutation that left the whole
`cmd/relay-server` lane green in a sibling slice.
*State its weakness in the test's own comment.* A syntactic guard is the expensive fallback and this
project has watched one get evaded four ways; it is here because nothing executable inside the process
can see main's literal, and AC1 and AC2 cover every hop after it.

**AC9. README documents the cap.** Not executable. The claim about what the parser refuses must be
phrased as what AC7's table pins, not as an assertion written from memory.

### RED first

Before any of the above, run the AC1 fixture against HEAD: three creates, three 201s. That is the
behavioural reproduction. AC3's control (two rows without the lock) is the second one, and it is the
one that establishes the concurrency defect is real rather than argued.

### Mutations to run, each with the test that kills it

| Mutation | Killed by |
| --- | --- |
| Delete the count check | AC1 |
| `n > cap` instead of `n >= cap` | AC1 (the third create succeeds) |
| Check after the insert | AC1 |
| Hard-code the cap to the default | AC2 |
| Delete `s.MaxSchedulesPerOwner = d.maxSchedulesPerOwner` | AC2 |
| Drop the lock statement | AC3 |
| Fold the lock into the counting statement | AC3 |
| Replace the sequence with one conditional INSERT | AC3 |
| Exempt admins | AC4 |
| Add the check to PATCH or run-now | AC5 |
| Enforce in SQL with a constraint or trigger | AC6, AC5 |
| `0` disables the cap | AC7 |
| Zero the deps literal field | AC8 |

Verify each mutation actually applied before recording a survivor, run a control that should die, and
never revert a mutation with `git checkout --` - restore from a copy.

## What this slice does NOT cover

- **It does not bound the sweep's DURATION.** Decision 4. Do not let the backlog item record
  "bounded" for duration on the strength of this.
- **It does not shrink an existing table.** Grandfathered owners keep every row, so the sweep's work
  set on the landing deploy is unchanged.
- **It is not a fleet ceiling.** M accounts hold `M x cap`, and with `RELAY_ALLOW_SELF_REGISTER=true`
  the account population is bounded only by a per-source-address rate limit.
- **It does not bound `ReconcileOnStartup`**, which is unpaged and also runs before
  `ListenAndServe`.
- **It does not make the eligible batch fair.** One owner with many due schedules can still occupy
  `BatchLimit`.
- **It does not bound a schedule's firing.** `fireOne` never touches an HTTP route; that is unchanged.
- **No rate limit on `POST /v1/scheduled-jobs`.** Decided out by the rate-limiting spec, on the axis
  argument. This slice does not revisit it.
- **No quota field in any payload**, and no `/v1/config` change. An owner reads their own `total` from
  `GET /v1/scheduled-jobs/stats` today. Publishing the cap would be a new advertisement surface with
  its own contract and its own disclosure question, and the SPA has no create form to use it.
- **No frontend work.** `web/src/schedules/api.ts` has no create call.
- **No change to `CreateScheduledJob`, `ValidateStoredSchedule`, the sweep's page loop, `BatchLimit`
  or `TickOnce`.**

## Backlog items this spec proposes, for the human to accept

None are filed by this spec.

1. **A wall-clock deadline on `ValidateStoredSpecsOnStartup`.** Decision 4, with the truncated-sweep
   under-reporting hazard as its opening constraint. This is the item the sweep's header comment must
   cite once this cap lands.
2. **`ListOverdueScheduledJobsForCatchup` is unpaged and runs before `ListenAndServe`.** The paging
   slice bounded the sweep's peak memory; the boot still materializes every overdue enabled row in
   the other statement, so "the boot's peak memory is one page" is not true of the boot.
3. **`ListEligibleScheduledJobs` has no per-owner fairness term.** One owner's due schedules can
   occupy the whole `BatchLimit` batch and delay every other owner's fires.
4. **MCP's 409 hint reads wrong for a quota refusal.** `MapError` returns "another change conflicts
   with this request" for every 409, which will now include "you are at the schedule limit".

## Open questions for the human

1. **The default value.** `100` is a judgement. The two things it is weighed against are in the spec:
   the largest legitimate hand-maintained population (tens), and what it permits at the 30-second
   minimum interval. Confirm, or fix a different number now.
2. **Grandfathering versus a one-time report.** This spec grandfathers silently. An alternative is to
   grandfather AND log one startup line naming how many owners are over the cap - which needs a
   fleet-wide grouped count at boot, on the same boot path this item exists to shorten. Declined for
   that reason; say if the visibility is worth the query.
3. **Confirmation that `parseScheduleCap` should warn-and-default rather than fatal**, which is the
   one place it diverges from the three rate-limit variables and follows the ceiling family instead.

## Provenance of every number

**No figure in this spec was measured by running anything. The Bash tool was unavailable in the
session that wrote it**, so there is no timing, no `psql` session pair and no test run behind any
claim here. Each figure is marked for what it actually is, and the two that a reader would most want
measured are called out as implementation obligations.

| Figure | Provenance |
| --- | --- |
| `POST /v1/scheduled-jobs` is bare `auth` | read from the tree, `internal/api/server.go:306` |
| run-now and retry are wrapped in `userLimit` | read from the tree, `internal/api/server.go:253,316` |
| `sweepPageSize = 100` | read from the tree, `internal/schedrunner/startup_validation.go:32` |
| `BatchLimit = 100`, `TickInterval = 10s` | read from the tree, `internal/schedrunner/runner.go:19,22` |
| `minScheduleInterval = 30s` | read from the tree, `internal/api/scheduled_jobs.go:20` |
| `idx_scheduled_jobs_owner` exists | read from the tree, `internal/store/migrations/000006_scheduled_jobs.up.sql:18` |
| `jobs.scheduled_job_id` is `ON DELETE SET NULL` | read from the tree, same migration, line 20 |
| No `DELETE FROM users` statement exists | searched `internal/store/query/`; the only match is `invites.sql`'s comment asserting the same. A claim about the complement, so it is a search result and not a proof |
| 409 is classified non-transient | read from the tree, `internal/relayclient/client.go:54` |
| MCP maps 409 to `conflict` | read from the tree, `internal/mcp/errors.go:40` |
| `ResponseError.Error()` returns `Message` | read from the tree, `internal/relayclient/client.go:23` |
| The SPA has no create-schedule call | searched `web/src/schedules/api.ts`; it exports list, stats, run-now, get, update, delete, setEnabled |
| `cmd/relay-server` is in the `pg-integration` CI lane | read from `Makefile:151` and `.github/workflows/go-ci.yml:181` |
| `ParseRateLimit` refuses a zero count and a zero window | read from the tree, `internal/api/ratelimit.go:21,25` |
| `parseAutoEnrollCeiling`'s three outcomes | read from the tree, `cmd/relay-server/autoenroll_config.go` |
| The READ COMMITTED snapshot argument (refutation 6, Decision 1) | **re-derived** from Postgres isolation semantics and from `internal/worker/handler.go:300-305`, which documents the identical overshoot for the same shape. **The implementation must reproduce it with two `psql` sessions and record the result in the PR** before relying on the lock; AC3's control is the permanent form of that measurement |
| `FOR NO KEY UPDATE` does not conflict with `FOR KEY SHARE` | **re-derived** from Postgres lock-mode semantics; the implementation must confirm it, since it is the whole reason a schedule create does not block that caller's job submission |
| `100` as the default cap | **judgement**, argued in Decision 6, measured by nothing |
| "100 schedules x 30s minimum = up to 200 fires per minute demanded" | **re-derived** arithmetic from the two tree constants above; it is DEMAND, and actual fires are served against `BatchLimit` per tick |
| `owners x cap` as the starting-work-set bound | **re-derived** from the cap's definition plus grandfathering |

## Files touched

| File | Change |
| --- | --- |
| `internal/store/query/scheduled_jobs.sql` | `LockOwnerForScheduleCap` and `CountScheduledJobsForOwnerUpTo`, with the comments named above |
| `internal/store/scheduled_jobs.sql.go` | regenerated by `make generate` (watch the CRLF revert, and verify the regenerated `.sql.go` survived it) |
| `internal/api/scheduled_jobs.go` | `handleCreateScheduledJob` opens a transaction after validation: lock, bounded count, insert; 409 on refusal |
| `internal/api/server.go` | `DefaultMaxSchedulesPerOwner`, `Server.MaxSchedulesPerOwner`, the resolver that folds a non-positive value to the default |
| `cmd/relay-server/schedulecap_config.go` | new: `parseScheduleCap` and the startup line |
| `cmd/relay-server/main.go` | parse, warn, pass `maxSchedulesPerOwner` in the `httpServerDeps` literal, print the line |
| `cmd/relay-server/http_server.go` | `maxSchedulesPerOwner` field; `s.MaxSchedulesPerOwner = d.maxSchedulesPerOwner` |
| `cmd/relay-server/schedulecap_config_test.go` | new, default lane: AC7, AC8 |
| `cmd/relay-server/schedulecap_wiring_integration_test.go` | new, integration: AC1, AC2, AC4 |
| `internal/store/scheduled_jobs_cap_integration_test.go` | new, integration: AC3 with its control, AC6 |
| `internal/api/scheduled_jobs_cap_integration_test.go` | new, integration: AC5 |
| `README.md` | the new env row; the submit row's clause; the API reference's 409 |
| `internal/schedrunner/startup_validation.go` | the header's conditional sentence about the cap becomes a statement, and the citation re-points |
