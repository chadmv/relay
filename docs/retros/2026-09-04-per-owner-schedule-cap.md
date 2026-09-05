---
date: 2026-09-04
topic: per-owner-schedule-cap
branch: claude/roadmap-now-dependencies-159be0
range: 294a23a..229591d
---

# Session Retro: 2026-09-04 - Per-Owner Schedule Cap

**TL;DR:** Any logged-in user could create unlimited recurring jobs, and the server reads all of them
before it starts accepting requests - so one account could make startup arbitrarily slow. This session
capped it at 100 per user. The interesting part was the counting: the fix the original bug report
prescribed does not actually work, which was proven by running two database sessions side by side, and
the version that does work quietly stops working if the database is configured with a different (and
perfectly legal) default setting - found in review, also by measurement.

## Handoff

Lane D of the six-item Now batch. Closed `feature-2026-09-04-per-owner-schedule-cap`, merged as PR #205.
`RELAY_MAX_SCHEDULES_PER_OWNER` default 100, warn-and-default on a bad value (matching
`parseAutoEnrollCeiling`, not the rate limits' fatal). Enforcement is one transaction:
`BeginTx(IsoLevel: pgx.ReadCommitted)` -> `LockOwnerForScheduleCap` (`FOR NO KEY UPDATE` on the
owner's `users` row) -> `CountScheduledJobsForOwnerUpTo` (inner `LIMIT`, so a grandfathered over-cap
owner cannot turn each refused POST into an O(N) scan) -> the unchanged insert. Over-cap owners are
grandfathered: read, PATCH, delete and run-now all still work. **The sweep's DURATION is explicitly
not bounded by this** - filed as `feature-2026-09-04-wall-clock-deadline-on-the-boot-sweep` and cited
from the sweep header; `bug-2026-09-04-reconcileonstartup-lists-every-overdue-schedule-unbounded` is
the other unbounded pre-listener read.

## What Was Built

- `internal/store/query/scheduled_jobs.sql` - the two new statements and their headers.
- `internal/api/scheduled_jobs.go` - the transaction and the 409.
- `cmd/relay-server/schedulecap_config.go` - `parseScheduleCap` and the startup line.
- `cmd/relay-server/schedulecap_race_integration_test.go` - the concurrent guard (a CI lane).
- `internal/store/scheduled_jobs_cap_integration_test.go` - the lock's mechanism, with the control
  that shows the race without it.

## Key Decisions

- **Lock in an earlier statement, not a clever single statement.** Measured: two sessions at cap-1
  running the item's prescribed conditional `INSERT` both commit.
- **`FOR NO KEY UPDATE`, not `FOR UPDATE`.** Measured both ways against the real `jobs.submitted_by`
  foreign key: NO KEY lets a concurrent `INSERT INTO jobs` through in ~9 ms, `FOR UPDATE` blocks it.
  The blast radius includes `schedrunner.TickOnce`, which fires jobs for schedule owners.
- **Admins are not exempt.** The route is reachable by every principal, so an exemption shrinks the
  driver set by zero callers while holing the control for the population most likely to be scripting.
- **Grandfathered rather than retroactively enforced**, because the cap has no re-running reader -
  nothing else counts an owner's schedules - so retroactivity is confined to one route.
- **The duration bound is a separate item**, and the acceptance criteria deliberately do not claim it.

## What Went Wrong and What Changes

- **The control failed OPEN on a legal server setting, and its own comment named the premise it did
  not establish.** The query header said "Under READ COMMITTED"; `pool.Begin` inherited
  `default_transaction_isolation`. Under `repeatable read` the lock is still taken and still blocks,
  but the counting session's snapshot is fixed at its first statement - the lock itself - so the count
  never sees the competitor's row. Measured: 3 rows against a cap of 2, no error and no log line.
  -> **What changes:** when a comment states an environmental premise a control depends on ("under
  READ COMMITTED", "assuming UTC", "with foreign keys enforced"), make the code establish it at the
  point of use rather than inheriting it. A premise stated in prose and supplied by ambient
  configuration is a control that fails silently when an operator changes something unrelated.
  (promoted to [[reference_pin_the_premise_where_the_control_relies_on_it]])

- **A guard at the store layer proved the mechanism and said nothing about its caller.** The
  store-level test pins that lock-then-count serialises two creates - but it builds its own sequence.
  Deleting the lock call from the handler, and separately moving it BELOW the count, both left every
  test green. The handler was correct; nothing held it there.
  -> **What changes:** when a control is a SEQUENCE of statements, the guard has to exercise the
  caller that issues them in that order, not a test-local reconstruction of the sequence. Ask which
  file the mutation would live in, and put the guard where it can see that file.
  (promoted to [[reference_guard_the_caller_not_your_reconstruction_of_it]])

- **A "kill" in the battery was an equivalent mutant, and its assertion message said otherwise.**
  Moving the cap check below the insert but above the commit changes nothing observable - the deferred
  rollback discards the row, so the client still gets 409 and the count is still right. The test's
  message claimed it would answer 201. Removed during implementation.
  -> **What changes:** already covered by "a kill must name its guard", extended here: before
  recording a mutant as killed, check whether it is *observable at all*. A mutation inside a
  transaction that is rolled back on the same path is equivalent by construction, and a battery that
  counts it inflates its own coverage claim.

- **Two test comments justified a kill with an ordering rule that does not apply.** "The admin case
  runs first, so an early-exit exemption cannot pass by never being reached." Refuted by experiment:
  reordering the table still kills the mutant, because `require` inside `t.Run` calls `FailNow` on the
  subtest goroutine only, so both arms always run.
  -> **What changes:** the decoy-before-target rule applies to a shared-fate loop or an early-returning
  code path, not to independent subtests. Before invoking a known rule in a comment, check that its
  precondition holds in this construct - restating it where it does not teaches the next reader to
  apply it wrongly.

- **A comment named an index dropped two migrations ago**, and it shipped twice because the second copy
  is generated. `idx_scheduled_jobs_owner` was created in `000006` and dropped in `000011`; `EXPLAIN`
  shows `idx_sched_jobs_owner_created`.
  -> **What changes:** when a comment names a database object, confirm it against `\d` on a freshly
  migrated database rather than against the migration that created it. Migrations that DROP are easy
  to miss because the create is the one that turns up in a search.

## Recommended Backlog Items

Backlog intake, not a priority order.

- See [`feature-2026-09-04-wall-clock-deadline-on-the-boot-sweep`](../backlog/feature-2026-09-04-wall-clock-deadline-on-the-boot-sweep.md) - the duration bound this slice deliberately does not provide
- See [`bug-2026-09-04-reconcileonstartup-lists-every-overdue-schedule-unbounded`](../backlog/bug-2026-09-04-reconcileonstartup-lists-every-overdue-schedule-unbounded.md) - the other unpaged pre-listener read
- [idea] **`ListEligibleScheduledJobs` has no per-owner fairness term.** One owner at the cap can fill a tick's `BatchLimit`, so the fleet-wide firing rate is bounded but its distribution is not. Surfaced while establishing that no HTTP rate limit bounds a schedule's firing.
- [bug] **MCP's `MapError` renders every 409 as "another change conflicts with this request".** That now includes a quota refusal, which reads wrong - the caller is over a limit, not in a conflict.

## Files Most Touched

- `docs/superpowers/plans/2026-09-04-per-owner-schedule-cap-plan.md` (+1892) and the spec (+694).
- `cmd/relay-server/schedulecap_config_test.go` (+235) and `schedulecap_race_integration_test.go`
  (+230) - the parser table and the concurrent guard that kills both lock mutants.
- `internal/store/scheduled_jobs_cap_integration_test.go` (+232) - the lock's mechanism plus the
  control that demonstrates the race without it.
- `internal/store/{query/scheduled_jobs.sql,scheduled_jobs.sql.go}` (+179) - the two statements.
- `internal/api/scheduled_jobs.go` (+63/-8) - the transaction, the count, the 409.
- `internal/schedrunner/startup_validation.go` (+18/-6) - the sweep header, now citing the deadline
  item and saying "per owner, not per fleet".
