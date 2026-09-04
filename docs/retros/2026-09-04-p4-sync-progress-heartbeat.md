---
date: 2026-09-04
topic: p4-sync-progress-heartbeat
branch: claude/pr-merging-session-65b658
range: c2a7eae30fd6dcf07339016f2d9a1c4a541aa9c3..1b1f6f93ae04166084890cb0acb61af80cc1706e
---

# Session Retro: 2026-09-04 - p4 Sync Progress Heartbeat

**TL;DR:** Syncing a big Perforce workspace can take hours, and relay printed nothing at all while
it happened - so an operator could not tell a transfer that was working from one that had hung.
This session added a summary line on a timer: how long it has been running, how many files have
arrived, how much disk is left, and which file was last seen. The design the original request
proposed would have frozen the task permanently, and the spec caught that before any code was
written. The review then found four defects in the new code, the sharpest being that a filename
chosen by a user could make the log go silent for the rest of the sync.

## Handoff

Autopilot iteration 3 of 4. Closed [[feature-2026-09-03-p4-sync-progress-heartbeat]], the head of
the fork-upstreaming batch's remaining Perforce chain. Twenty-one commits: spec `a8dcc6e`, plan
`e7dac4f`, ten implementation commits `e091020..21393e6`, three review-fix commits
`c077d9d`/`1393a44`/`4e01ffa`, then the close and backlog work.

`p4 sync` loses `-q`. Its stdout lines are counted by `syncProgress.onLine` and NEVER forwarded;
`syncSummary` renders five fixed fields (elapsed, files, other lines, GB free, last path) and the
last field is `%q`-quoted. `RELAY_SYNC_HEARTBEAT_INTERVAL` defaults to 30s, disables at `0s`,
refuses anything under a 5s floor.

**The architecture is inverted from the item's.** The item wanted a heartbeat goroutine calling
`progress`; `progress` is a closure holding a mutex across a send that selects only on the send
channel and the AGENT context, so a second caller is a mutual-exclusion point and `Prepare` would
never return. What ships puts the SYNC on a goroutine and keeps the heartbeat loop on `Prepare`'s
own. `progress` has exactly one caller and the select has exactly two cases - no `ctx.Done()` arm,
because that would release the workspace while a live p4 child still writes into it, and
`exec.CommandContext` already covers cancellation through the error channel. Three lenses verified
this and called it the best-executed part of the diff; do not "simplify" either property.

Review changed four things. `syncLineDepotPath` is rune-aware now (drops Cc/Cf/Zl/Zp, applies
`ToValidUTF8`, cuts on a rune boundary) because a raw byte cut produced invalid UTF-8 that Postgres
rejects for a `TEXT` column. The tail is `%q` so a depot path cannot forge a `[sync] failed:` line.
The ticker seam captures the requested duration, because the interval never reached it and the knob
could be ignored end to end with everything green. And `execRunner.Stream` drains stdout on a scan
error before `cmd.Wait()`, because it wedged forever otherwise.

Verify: 22 packages default and under `-race` in the `golang:1.26` container; both p4d E2E tests
real PASS (35.8s, 34.1s), zero skips; `go vet` both tag sets.

Two items filed, three amended - see Recommended Backlog Items. Next session starts at ROADMAP.md's
Now, whose lead is now the `clobber` workspace option.

## What Was Built

- **`syncProgress`** counting file lines and unparsed lines separately, so the aggregate discloses
  its own loss rather than silently folding one into the other.
- **`syncLineDepotPath`**, splitting at the FIRST `#` (p4 escapes `#` inside a depot path as `%23`,
  so a `#` in the local half is always after the separator) and sanitizing rune-aware.
- **`runSyncWithHeartbeat`**, the inversion described above.
- **`probeFreeDiskGB`**, bounding a `statfs` that has no cancellation with a timeout and an
  abandoned goroutine, latched so it is one goroutine per `Prepare` rather than one per tick.
- **`resolveSyncHeartbeatInterval`** with a 5s floor, and an operand-side overflow guard in the
  shared duration parser.
- Guards for the properties the design leans on: the ticker's requested period, the buffered error
  channel, `onLine`'s wiring, the two-case select, the single `progress` caller, and the scan
  error outranking a non-zero exit.

## Key Decisions

- **Invert the goroutine.** The alternatives - a drop-on-full mailbox with a non-joined emitter,
  draining from `onLine`, landing the bounded-send fix first - are argued and rejected in the spec.
- **Per-file lines are counted, never persisted**, plus a second counter for lines that did not
  parse. An operator debugging a stalled sync needs "moving? fits? where?", and the summary carries
  all three.
- **`BaselineHash` untouched**, as in the previous slice: it is a cross-process contract.
- **The overflow guard went in the SHARED parser**, not at this knob's resolver. Every caller had
  the identical defect and each reads a wrapped value as its own silent disable, so guarding one
  would have left three siblings broken and the shared docstring false.
- **The free-disk figure stays**, though task logs are auth-only so any authenticated user reads it.
  The item asks for it; README now says so.

## What Went Wrong and What Changes

Ledger: the previous retro's entries were all promoted, so none are carried. Promoted lessons that
fired: [[reference_a_cadence_test_must_assert_the_wiring]] - fired exactly, and the guard it asks
for was missing, so the interval could be ignored end to end;
[[reference_repair_all_of_it_or_refuse]] and
[[reference_reordering_an_acquisition_invalidates_its_exclusion_argument]] were both written last
iteration and both informed this spec's refusal to add a third select case;
[[reference_wrong_prose_is_the_dominant_defect]] (again the largest finding class - eleven prose
findings across three rounds); [[reference_verify_the_mutation_applied]] (every lens ran a control
set); [[feedback_assert_encoding_after_a_programmatic_edit]] (fired for real - see below);
[[reference_document_the_mechanism_not_the_coverage]] (written last iteration, and it caught a
README sentence about p4's buffering that was both unverifiable and causally wrong).

- **The requested design would have deadlocked the feature it was adding, and only a fresh
  derivation found it.** The item asked for a heartbeat goroutine calling `progress`. Reading
  `progress`'s implementation rather than its signature showed it holds a mutex across a send
  bounded only by the agent context - so the second caller is not slow, it is exclusive, and
  `Prepare` never returns. The item was filed next to an open bug about that very callback parking,
  and still proposed adding a caller to it.
  -> **What changes:** before adding a caller to an existing callback, read what the callback DOES
  when its consumer is gone - not its signature, and not the item's summary of it. A callback that
  can block is a lock; a second caller of a lock is a design decision, not a wiring detail.
  (promoted to [[reference_read_what_a_callback_does_when_its_consumer_is_gone]])

- **A user-chosen filename could silence the task log for the rest of a sync, and the repo already
  had the correct sanitizer twenty lines long in another package.** The depot-path field was cut at
  a raw byte offset, so a multi-byte rune straddling the bound produced invalid UTF-8; `task_logs`
  is `TEXT`, the proto field is `bytes` so no wire-level UTF-8 gate exists, Postgres rejects the
  insert, the whole batched chunk is dropped, and the field is sticky so it re-poisons every later
  heartbeat. `sanitizeAgentErrorMessage` does exactly this transform correctly and its own comment
  names the defect.
  -> **What changes:** when you write a transform that clips, escapes or sanitizes text bound for
  storage, grep for the same transform before writing it. The one that exists has usually already
  paid for a bug you are about to re-buy, and its comment often names it. (promoted to
  [[reference_grep_for_the_sanitizer_before_writing_one]])

- **Three of my prescriptions to the engineer were wrong, and the engineer refuted all three with
  measurements.** `cmd.WaitDelay` would never have fired (it bounds a child that fails to exit
  after cancellation, not one blocked writing while the context is live). My framing of the
  duration bug was wrong about which claim was false. And my prescribed post-multiply overflow
  check was insufficient - `1000000000000d` wraps POSITIVE, past any floor, so the guard has to be
  on the operand.
  -> **What changes:** a conductor's fix brief is a set of claims and gets the same treatment as any
  other input. Brief the finding and the evidence; prescribe a remedy only where you have checked
  it, and say which parts are prescription rather than observation so the implementer knows what to
  re-derive. (promoted to [[reference_a_fix_brief_is_a_set_of_claims]])

- **A test's assertion was loosened for a good reason and lost coverage nobody noticed.** The clip
  test moved from exact equality to a plus-or-minus-two range, which the rune boundary genuinely
  requires - and that left the cap constant pinned only to within two, so changing 200 to 199
  survived. Found by the re-verify's mutation battery, fixed by adding a pure-ASCII row that
  asserts exact equality alongside the rune-boundary row.
  -> **What changes:** when a change forces an exact assertion to become a range, add a second case
  where the exactness still holds. A loosened assertion is a coverage loss even when the loosening
  is correct. (promoted to [[reference_a_loosened_assertion_needs_an_exact_sibling]])

- **A shell heredoc collapsed backslash escapes and wrote a literal CR into a Go source string.**
  Caught by `cat -A`, by no gate, and independently reproduced three times by a reviewer building
  probes. The fix was to build any file containing escapes with a script rather than a heredoc.
  -> **What changes:** already the rule; this session is the third instance and the first where the
  corrupted byte was inside a string literal rather than at a line ending. (already in
  [[feedback_assert_encoding_after_a_programmatic_edit]] and
  [[feedback_commit_heredoc_shell]] - stamping the escape-collapsing trigger)

## Recommended Backlog Items

Backlog intake, not a priority order.

- See [`bug-2026-09-04-sweeper-run-panics-on-a-negative-interval`](../backlog/bug-2026-09-04-sweeper-run-panics-on-a-negative-interval.md) - guards a zero interval but not a negative one, and runs on an unsupervised goroutine
- See [`idea-2026-09-04-abandoned-free-disk-probes-accumulate-on-a-wedged-volume`](../backlog/idea-2026-09-04-abandoned-free-disk-probes-accumulate-on-a-wedged-volume.md) - bounded per sync, unbounded per agent lifetime
- Three existing items amended with what this slice measured: the task-logs volume cap gained a
  producer whose rate is a constant; the wiring-guard idea gained `cmd/relay-agent` as a second
  unguarded package; the progress-parking bug gained both the raised park count and the fact that
  an uninterruptible `statfs` now sits inside the handle-held window.

## Files Most Touched

- `internal/agent/source/perforce/syncprogress.go` - the counter, the parser and the sanitizer.
- `internal/agent/source/perforce/perforce.go` - `runSyncWithHeartbeat` and `probeFreeDiskGB`.
- `internal/agent/source/perforce/sync_heartbeat_test.go` - the cadence, wiring and parking guards.
- `internal/agent/source/perforce/client.go` - `-q` dropped, the stdout drain, the scan-error
  ordering.
- `cmd/relay-agent/main.go` - the interval resolver and the shared parser's overflow guard.
- `internal/agent/source/perforce/client_error_test.go` - the two helper-process tests, one of
  which exists only to make the ordering claim observable.
- `internal/agent/source/perforce/syncprogress_test.go` - the parser table and the two clip rows.
- `README.md` - the env row, the summary's shape, and the disclosure that task logs are auth-only.
