---
date: 2026-09-04
topic: pg-integration-ci-lane
branch: claude/lane-ci-integration-guards
range: b548f16..5a81fb1
---

# Session Retro: 2026-09-04 - The pg-integration CI Lane

**TL;DR:** A lot of this project's safety checks only run when a human remembers to run them, because
they need a real database and CI never started one for them. This session gave two of those packages
a CI job, by pulling the database-setup helper out of the one package that already had it. The fix
worked. What makes the session worth reading is that the fix, twice, committed the exact mistake it
was written to prevent: it moved one guard OUT of CI, and it shipped a lane that failed about a third
of the time. Both were caught by review, and the flaky one only because a reviewer ran the lane seven
times instead of once.

## Handoff

Lane C of a six-item batch over ROADMAP.md's Now section. Four commits on
`docs/backlog/idea-2026-08-23-integration-only-guards-ci-never-runs.md`, which **stays open** - this
slice retires some instances and a progress note records which.

`internal/testsupport/pgdsn` is new, untagged and importable: the DSN harness formerly inside
`internal/cli/pgharness_integration_test.go`. `internal/store` and `internal/schedrunner` are rewired
onto it, and `.github/workflows/go-ci.yml` gains a `pg-integration` job on a `services: postgres`
block, running `make test-pg-integration`.

**Measured, not quoted.** Container mode: `internal/schedrunner` 24.9s, `internal/store` 244.7s. The
shared-service mode CI actually uses: store ~75s, schedrunner ~9.5s, pgdsn ~1.5s, about 1m10s wall.
Both container figures back ROADMAP.md's numbers over the item's lower ones.

**`internal/api` is deliberately out**, and the argument is the one to keep: the tree gives two costs
for it that differ by nearly 2x (CLAUDE.md says about 9.5 minutes, the Makefile comment says about
320-340s). Adding a lane whose runtime is unknown to that degree risks a timeout kill, which is the
exact ambiguity `cli-integration`'s timeout comment exists to prevent.

**`python/tests/integration/` is formally accepted as manual**, with a binding consequence recorded in
`conftest.py` and in the new CLAUDE.md rule: an assertion whose only home is that lane is not
evidence. It needs a live relay-agent running real subprocesses, so it is a topology problem, not a
service-dependency one.

Six guards moved from an integration-tagged file into an untagged one and now run in `make test` and
in CI's race job. Five are the DSN guards that stop a supplied `RELAY_TEST_DATABASE_URL` redirecting
`CREATE`/`DROP DATABASE` to another server; the sixth is the `BoundedCleanup` panic test.

## What Was Built

- **`internal/testsupport/pgdsn`**, exporting `NewIntegrationDSN` (migrated) and `NewEmptyDSN`
  (unmigrated, for the two tests that migrate from scratch). The package is untagged **because it has
  to be**: a tagged source file plus an untagged `_test.go` beside it does not compile in the default
  lane, so getting the five pure guards untagged forces the package source untagged too.
- **A `pg-integration` job**, modelled on `cli-integration` down to its reasoning about why it is a
  separate job and why its `timeout-minutes` is deliberately not 15.
- **A CLAUDE.md rule**: ask in order whether a guard needs the tag at all, whether it can run in a
  lane CI runs, and otherwise write the reason in the test's own comment.

## Key Decisions

- **`CREATE DATABASE ... TEMPLATE template0`**, not a retry and not `-p 1`. See below.
- **Argue the lane on signal, not enforcement.** Nothing in this repo blocks a merge; `main` has no
  branch protection. A CI lane here buys a signal a human would otherwise have to remember to
  generate.
- **The cheapest instance is retired by deleting code, not adding a job.** Five of the six moved
  guards test pure functions and never needed the tag.

## What Went Wrong and What Changes

**The slice moved a guard OUT of a lane that runs, which is the defect it exists to fix.**
`TestIntegration_HarnessDSNIsMigratedAndEmpty` ran in CI via `cli-integration` before the move. After
it, the package appeared in no lane's package list, so only the human-only `make test-integration`
reached it - and its whole stated purpose is making a downstream RED attributable. Worse, the CLAUDE.md
rule written in the same commit told readers that a package "joins `pg-integration` by taking its
database from `internal/testsupport/pgdsn`", which is false: the Makefile hardcodes the list, so
wiring to `pgdsn` joins nothing. **The rule was refuted by its own commit**, and the proof was the
slice's own new package.

The lesson is about how the claim was checked. Reading the Makefile says the target names two
packages; it does not say which tests execute. The instrument that settles it is
`go test -tags integration -list` **against the lane's own package pattern**, which returned 0 before
and 1 after. Match the instrument to the claim: a lane-membership claim is a claim about what runs.

**One green run was accepted as evidence for a lane that fails about a third of the time.** The
implementer measured the combined target once, got 68.4s/9.5s, and reported it. A reviewer ran it
seven times: 5 green, 2 red, both `ERROR: source database "template1" is being accessed by other
users (SQLSTATE 55006)`. The single measurement was accurate and was a measurement of a broken lane.

**And the predicted mechanism was wrong, which mattered because the prescribed remedy followed from
it.** The conductor seeded the review with a hypothesis: dropping `-p 1` raised concurrent
`CREATE DATABASE` pressure. The reviewer falsified it directly - 200 create/drop cycles at
concurrency 8 produced zero errors, and one of the two failures was in the 26-acquisition package.
The real trigger is any session attached to `template1` at the instant of the CREATE, and the
producer was never identified (2M polls of `pg_stat_activity` saw nothing, so it is very short-lived).
So `-p 1`, which the spec itself named as the fallback, would have been the wrong fix: it lowers the
RATE of CREATE statements, not their count, and rate is not the variable. `TEMPLATE template0` has
`datallowconn = f`, so nothing can ever be connected to it and the class is structurally eliminated.
**A remedy inherits the diagnosis; falsifying the diagnosis retires the remedy.**

**A deleted hazard note was exactly the note the fix needed.** `origin/main` warned that golang-migrate
takes `pg_advisory_lock` keyed on the database name and that "a future TEMPLATE-database optimisation
with a fixed name would put them all back on one lock - do not add one without re-checking this". The
note did not travel when the migration call moved, and the fix is precisely the thing it warns about.
It was re-derived rather than restored or ignored: advisory locks are database-scoped, confirmed
empirically (a lock held in one database did not block the same key in another), and the target
database is still uniquely named - `TEMPLATE` only changes the byte-copy source. The conclusion is
recorded where the migration call now lives.

**A guard lost its environment pinning by moving into the default lane.** A DSN test with no userinfo
lets `PGUSER` win in pgconn's settings merge, so an ambient `PGUSER=root` reddens it. Its sibling
carries `t.Setenv` on `PGUSER`/`PGSERVICE`/`PGSERVICEFILE` and a comment naming the hazard; the
unprotected one did not. Moving a test to a lane with a different ambient environment changes what it
depends on, even when the assertions are character-identical.

**Two prose censuses were carried forward unread**, and one had never been right at any commit: a
"three existing copies" claim about `WithOccurrence(2)` where `origin/main` had 14 sites and HEAD has
10. The same move stripped the identical defect from the neighbouring doc comment and copied this one
into a new shared package with a wider readership. And a "five times" evasion count contradicted the
primary record in four places, one of which carries the identical sentence with the count swapped.
Both were deleted rather than corrected.

**A conductor error worth recording, because it is the same shape as the flake.** The conductor ran
its own 7-run verification against a container the fix agent owned; the agent removed it on
completion, and all 7 runs came back red. Had that been reported it would have said the fix failed.
The tells were all present: zero occurrences of the actual failure signature, failures at 0.00s, and
uniform results across all seven - which is this project's own broken-harness signal. The redo used
its own container behind a green control first. **A battery without a control that should die cannot
distinguish a result from a broken instrument.**

## Recommended Backlog Items

None new. The parent item stays open with a progress note recording which instances this closes and
which it does not: the three originally-named guards, `internal/api`'s whole handler surface, the
`errorMessageLogStream` stub-widening case, and the p4d-dependent packages.

## Files Most Touched

- `internal/testsupport/pgdsn/pgdsn.go` - the extracted harness
- `internal/testsupport/pgdsn/pgdsn_guards_test.go` - six guards, now untagged
- `.github/workflows/go-ci.yml`, `Makefile` - the lane
- `CLAUDE.md` - the generalizing rule
