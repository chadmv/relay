---
date: 2026-09-10
topic: sync-entry-count-bound
branch: claude/roadmap-now-dependencies-581b21
range: 1bdac00a..0922fd12
---

# Session Retro: 2026-09-10 - source.sync Entry Count Bound

**TL;DR:** A job could list unlimited paths to fetch from Perforce, and each one with a floating
revision costs a separate round trip to the customer's Perforce server before the task even starts.
This session capped the list at 512 entries. The interesting part was how much of the reasoning
turned out to be wrong: the cap does NOT meaningfully reduce what one request can cost, because the
limit applies per task and a job can carry thousands of tasks - and the first version of the code
comment claimed an 82x improvement that is actually about 1.5%.

## Handoff

Item 2 of 6 in the roadmap Now batch. Closes
[[idea-2026-09-04-source-sync-has-no-entry-count-bound]]. Merged as PR #207, four commits plus
spec/plan.

`maxSyncEntries = 512` in `internal/jobspec`, counting includes and exclusions together, checked
immediately after the existing empty-list refusal and before every per-entry rule. Message:
`at most 512 sync entries are allowed, got N`, prefixed `task <name>:` by `Validate`. Retroactive
over stored `scheduled_jobs.job_spec` with no grandfathering; the integration lens drove all five
stored-spec paths against a live database, twice each.

**The numbers, all measured:**

- Body ceiling: the smallest entry the validator accepts is **25 bytes** (a `@1` rev) giving
  `C_min = 41938`; the smallest that also costs a round trip is **28 bytes** (`#head`) giving
  `C_head = 37445`. These bound DIFFERENT axes and must not be collapsed.
- **The cap is a concentration control, not an aggregate one.** `validateSourceSpec` runs per TASK and
  `maxTasksPerJob` is 5000, so one 1 MiB body still carries ~72 tasks x 512 = 36,864 entries against
  a pre-change 37,444. Across 11 attempts: 411,884 to 405,504. A 1.5% reduction.
- **Argv, measured by actually starting the process** (Windows 11, Go 1.26.2; 16-char client name,
  40-char path tails): 441 entries gives 32,703 chars and Start OK; 442 gives 32,777 OK;
  **450 gives 33,369 and Start FAILED, "filename or extension is too long"**; 512 gives 37,957
  FAILED. So the cap sits ABOVE the documented 32,767-character `CreateProcess` limit, reached
  around 450 entries of ordinary depot paths - inside the cap. It is a refusal, not a truncation, and
  there is no injection surface.

`bug-2026-08-29-source-unshelves-is-one-subprocess-per-entry-and-unbounded` must be **re-read against
this slice, not closed by it**: this design corrects that item's premise that sync was the only
unbounded axis.

Next entry point: the two slice-2 items.

## What Was Built

- One package-level constant and a two-line comparison in `validateSourceSpec`. No new function, no
  new type, no wiring - every ingest and stored-spec path already reaches it through `jobspec.Validate`.
- `internal/jobspec/sync_bounds_test.go`, 10 subtests: both ends of the range, three placement cases
  proving the count check precedes the per-entry loop, the exclusion count and the coverage loop, two
  counting-semantics cases, and a two-task case.
- Two rows in `internal/api/job_spec_source_test.go`, reached through `ValidateJobSpec`'s value
  parameter.

## Key Decisions

- **Total entry count, not the `#head` subset.** Bounding only the round-trip-costing subset leaves
  the coordinator's per-worker rehash and the argv unbounded, and makes validity depend on a field's
  VALUE rather than its shape.
- **512 rather than 500**, deliberately, so it does not read as derived from `maxCommandsPerTask = 500`
  and get maintained as if it were. The two are independent: one counts commands the agent executes,
  the other counts p4 round trips before any command runs.
- **No job-wide aggregate cap.** The project's own 2.5x-5x band applied to a 10,000 high end puts one
  at or above what a 1 MiB body expresses - the `maxCommandsPerJob` failure where two caps whose
  product exceeds the body reduce nothing.
- **Non-configurable**, pointing at the `maxRetries` argument in three lines rather than restating it.

## What Went Wrong and What Changes

Ledger: the prior retro's entries were all promoted, so none are carried. Promoted lessons that
fired: [[reference_wrong_prose_is_the_dominant_defect]] - every finding in the round was prose or a
test gap rather than a code defect; [[reference_per_axis_bounds_can_reduce_nothing]], which is the
headline entry below; [[reference_relay_the_input_not_just_the_number]];
[[feedback_a_green_rerun_bounds_not_retires]]; [[reference_uniqueness_claim_is_about_the_complement]].

- **The comment claimed an 82x reduction that is actually 1.5%, and the spec's own framing produced
  it.** The paragraph framed the ratio as what one REQUEST buys, forgetting the check runs per task
  with `maxTasksPerJob` at 5000. Two review lenses disagreed - the security lens defended the figure
  as honest, the invariants lens refuted it - and the conductor broke the tie with arithmetic,
  finding the security lens had made the SAME error the comment made.
  -> **What changes:** before writing any "Nx reduction" for a new per-unit bound, enumerate what
  multiplies the unit and compute the aggregate both ways. A ratio comparing a per-unit cap against a
  whole-request ceiling is comparing unlike quantities.
- **Four precise derived numbers went into a code comment, pinned by nothing, computed from a
  constant the package cannot see.** `maxBodyBytes` is unexported in `internal/api` and
  `internal/jobspec` imports only the standard library.
  -> **What changes:** a measured number belongs in the commit message unless a test pins it. If a
  comment must carry one, the constant it derives from has to be reachable from that package.
- **The comment said "the cheapest entry the DECODER accepts is 25 bytes".** The decoder accepts an
  empty object. The cap bounds what is EXECUTED, not what is DECODED, and a 1 MiB body still
  materialises a multi-megabyte slice before the check runs.
  -> **What changes:** when naming the component a bound protects, name the one that performs the
  refusal. Decoder, validator and executor bound different populations, usually an order of magnitude
  apart.
- **Nothing pinned that the bound applies to every task.** Mutating the loop to check only the first
  task's source left all 23 packages green, because every source-bearing fixture in the tree is
  single-task.
  -> **What changes:** when a rule lives inside a per-element loop, one test must use at least two
  elements with the violation on a LATER one. A single-element fixture cannot distinguish "checks
  each" from "checks the first".
- **The spec named a control test in the wrong package.** `internal/jobspec` contains no source-spec
  test at all - found by the planner, not the spec.
  -> **What changes:** when a spec names an existing test as its control, confirm it lives in the
  package whose behaviour is changing before relying on it.

## Recommended Backlog Items

Backlog intake, not a priority order.

- [idea] **`BaselineHashFromAPISpec` is called inside `selectWorker`'s worker loop.** It is invariant
  in the worker being scored, and `warmKey` one branch above is already hoisted with a comment saying
  why. `internal/scheduler/dispatch.go`.
- [bug] **`SyncStream`'s argv length is bounded by nothing.** At 512 entries with a realistic 28-char
  client name and 35-char remainder the command line is 37,941 bytes against Windows' documented
  32,767-character `CreateProcess` limit, which that shape reaches at about 442 entries - inside the
  new cap. Measured by starting the process, not predicted. The remedy is probably a filespec file or
  chunked invocations, not a tighter count bound. `internal/agent/source/perforce/client.go`.
- [idea] **The Python SDK's `Sync` docstring enumerated the server-side sibling rules and is now one
  short.** The enumeration was deleted rather than extended this slice; a one-clause generalisation
  would be better. `python/src/relay/models.py`.
- [bug] **`_path_starts_with_slashes` raises "stream is required" for a bad PATH**, and the same
  prefix-check branch is duplicated with a second unreachable message.
  `python/src/relay/models.py`.

## Files Most Touched

- `internal/jobspec/jobspec.go` - the constant, the check, and a comment cut from 47 lines to 22.
- `internal/jobspec/sync_bounds_test.go` - new, 10 subtests.
- `internal/api/job_spec_source_test.go` - two rows plus a `manySyncIncludes` helper.
- `README.md` - the `sync` row, plus two enumerations dropped rather than extended.
- `python/src/relay/models.py` - one docstring enumeration deleted.
