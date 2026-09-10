---
date: 2026-09-10
topic: worker-workspace-inventory-bounds
branch: claude/roadmap-now-dependencies-581b21
range: 3dc21367..ef86c877
---

# Session Retro: 2026-09-10 - Worker Workspace Inventory Bounds

**TL;DR:** Agents report which Perforce workspaces they hold, and those reports land in a database
table whose index has a hard size limit. Nothing checked the length of the agent-supplied strings, so
an over-long one failed the index rather than being rejected cleanly - and worse, one bad row made the
server throw away the agent's entire workspace list instead of just that row. This session bounded the
four columns and made a bad row get dropped while the rest commits. That second half closed a separate
bug that had been open since August.

## Handoff

Item 4 of 6 in the roadmap Now batch. Closes TWO items:
[[idea-2026-09-04-worker-workspaces-source-key-is-unbounded-in-a-primary-key]] and
[[bug-2026-08-23-applyinventory-null-timestamp-freezes-inventory]]. Merged as PR #209, 13 commits plus
spec/plan.

`inventoryUpsertParams(workerID, u) (store.UpsertWorkerWorkspaceParams, error)` in
`internal/worker/inventory_params.go` is the **sole production route** to that params struct, verified
by a shape search across the tree with exactly one deliberate test bypass. Bounds in BYTES, not runes,
so `octet_length()` in the CHECK and `len()` in Go are the same measure:
`source_type` 64, `source_key` 512, `short_id` 128, `baseline_hash` 128. Plus a NUL refusal and a
`last_used_at` storability check. **It refuses; it never transforms** - `source_key` is an IDENTITY, so
truncating it collapses two distinct workspaces onto one primary-key row, re-creating at the
coordinator the hazard `perforce.SourceKey`'s NUL set-terminator prevents at the agent.

Migration `000024`, four `CHECK ... NOT VALID` constraints. `NOT VALID` deliberately: a validated
CHECK scans existing rows and FAILS the startup migration, so one planted over-long row would be a
server that will not boot after upgrade - a control whose deployment the attacker can deny in advance.
The legacy violating set drains itself because every reconnect runs `ReplaceWorkerInventory`.

**Measured, and it refuted the plan's own instrument:** the btree entry limit is **2704 bytes on
PostgreSQL 16.13**, confirming a figure the spec had quoted from another item and never verified. The
plan prescribed reading the limit off the error from a 12800-byte value - but that fails at
`INDEX_SIZE_MASK` (8191) during index-tuple formation, BEFORE the btree page check, overstating
headroom threefold. 3008 bytes is what reaches the btree message. The sum that must fit is
64+512+128 = 704 against the lookup index, leaving ~3.8x.

No p4 environment was reachable, so the escalation rule (revisit if a real stream exceeds 300 bytes)
did not fire, and 512 is recorded as **anchored top-down only**.

Counter `InventoryRowRejections` is NOT published; see the item filed for it,
`idea-2026-09-10-publish-inventory-row-rejection-counter`. Slice 2 (the distinct-key COUNT axis) has
two clean seams left for it: a `len(inv)` guard before `BeginTxFunc`, or a counter inside the loop.
**Warning handed forward: if slice 2 refuses a whole over-count batch rather than truncating, it
re-creates the freeze this slice removed.**

Next entry point: the two slice-2 items.

## What Was Built

- The constructor, four byte bounds, a value-free sentinel error, and the drop-the-row-commit-the-batch
  branch in `applyInventory`.
- A `last_used_at` two-arm check: unparseable, and parses-but-zero. The second arm exists because a
  zero `time.Time` formats as the year-one RFC3339 instant, which PARSES - so a value past the parse
  can still bind NULL.
- Migration `000024` up and down.
- An at-bound integration test that maxes all four columns in one row and reads the lengths back.

## Key Decisions

- **Drop the row, keep the batch, count it, log nothing.** The refusal is fully agent-chosen and
  unboundedly repeatable, so a log token spent there is one the connection's real diagnostics lose.
- **No env knob.** The remedy an operator reaches for on a climbing counter is "raise the bound", which
  IS the attack. Changing one costs a rebuild and leaves a visible diff.
- **Every error is value-free.** `time.Parse`'s own message echoes its input, so it is deliberately not
  wrapped; every wrapper formats a column name and a compile-time bound.
- **The DELETE arm deliberately has no bound.** It binds these values as comparison keys rather than
  index tuples, and bounding it would put a row stored before the bound existed out of reach of the
  agent's own per-row delete, since the admin evict path deletes only via the agent's confirming
  update. Attacked by two lenses and held.
- **Blast radius is the sender's own rows** - `workerID` is resolved at registration from the credential
  and never read off the wire. That is what makes a silent drop acceptable here and would not on a
  shared table.

## What Went Wrong and What Changes

Ledger: the prior retro's entries were all promoted, so none are carried. Promoted lessons that
fired: [[reference_a_ceiling_test_must_assert_the_refusal]];
[[reference_lossy_aggregate_discloses_where_read]] - the counter entry below;
[[reference_same_typed_args_transpose_silently]] (four adjacent TEXT fields);
[[reference_identity_is_not_honesty]]; [[feedback_file_the_item_a_decision_is_conditioned_on]] - the
deferral entry below; [[reference_a_kill_must_name_its_guard]].

- **The item's harm model was backwards, and correcting it decided the design.** Its shape implies a
  bad row should fail the batch. It already did - and that IS the defect, because the rollback undoes
  `ReplaceWorkerInventory`'s DELETE too, so an agent reporting the same bad row every registration
  never updates its inventory again while the dispatcher keeps warm-scoring it. The tree asserted this
  twice already, in a `fakeTx` comment recording a prior mutation and in an existing test.
  -> **What changes:** when a backlog item implies a remedy, read the failure path it describes to
  the end before adopting it. "Fails loudly" and "fails usefully" are different properties, and a
  transactional rollback turns the first into neither.
- **The suite did not prove the case it exists to prove.** The at-bound test maxed only `source_key`,
  passing an 8-byte `source_type` and a 6-byte `baseline_hash`, while the index the bound protects is
  the sum of three columns. It proved 526 bytes fit, not 704. A review lens closed the gap against a
  real database - but that evidence lived in a transcript, not the suite.
  -> **What changes:** when a bound protects a COMPOSITE limit, the test must max every component in
  one row simultaneously. Maxing one at a time proves a smaller case and reads as if it proved the
  real one.
- **`strings.Repeat` of one character is TOAST-compressible and gives a false negative.** Measured, not
  assumed: with the bounds mutated to 1000 each, a three-column row built with `strings.Repeat` INSERTS
  FINE at 3000 bytes, while the same row from incompressible text fails with `index row size 3024
  exceeds btree version 4 maximum 2704`. Two lenses hit this independently.
  -> **What changes:** when a test probes a storage-size limit, generate incompressible values and
  assert the distinct-byte count, so the fixture cannot silently regress to a run of one character.
  (promoted to project CLAUDE.md)
- **A comment deferred to an item that did not exist.** "NOT YET ON GET /v1/server/counters - the
  section is deliberately deferred to its own item" named no item, and none had been filed. Its sibling
  `enrollmentRefusals` has the same deferral WITH a filed item, which is what made the gap visible.
  -> **What changes:** a comment that defers to future work must name the findable artifact in the same
  commit that writes the deferral. Until the item exists the deferral cannot be checked, which is the
  whole point of writing it down.
- **The accessor's docstring prescribed an operator remedy for a number nothing reads.** It said "this
  is the sentence to read before acting on it" for a counter reachable only from a test. Its neighbour
  is honest about the same deferral and claims no actionability.
  -> **What changes:** when a counter is not wired to a read path, the accessor says so. Remedy guidance
  is for whoever wires it up, and must be scoped to that reader.
- **A completeness claim about other code.** "Both of this package's writers go through it" is true
  today, pinned by nothing, and a third writer would falsify it silently.
  -> **What changes:** state the function's own contract instead. A count of the callers is a claim
  about the complement. [[reference_uniqueness_claim_is_about_the_complement]]
- **Two lanes both edited README's source-workspaces field table**, in adjacent rows, producing a
  rebase conflict at merge time.
  -> **What changes:** when partitioning concurrent lanes by file, partition shared reference tables by
  ROW and say so in each brief - naming the section is not enough when two lanes own different rows of
  one table.

## Recommended Backlog Items

Backlog intake, not a priority order.

- See [`idea-2026-09-10-publish-inventory-row-rejection-counter`](../backlog/idea-2026-09-10-publish-inventory-row-rejection-counter.md) - filed during the session; the counter exists but no operator can read it.
- [idea] **The `bug-2026-08-23` Amendment asserts in present tense that the item is not fixed.** This
  slice makes that false. The close must delete the Amendment's claim, not just stamp the frontmatter.

## Files Most Touched

- `internal/worker/inventory_params.go` - new; the constructor, the four bounds, the value-free sentinel.
- `internal/worker/handler.go` - `applyInventory`'s drop branch, `applyInventoryUpdate`, the counter, and
  the budget branch that no longer spends a token on a refusal.
- `internal/store/migrations/000024_worker_workspace_text_bounds.{up,down}.sql` - new.
- `internal/worker/inventory_bounds_test.go`, `inventory_params_test.go` - new unit coverage.
- `internal/worker/inventory_bounds_integration_test.go` - new; the all-columns-maxed at-bound row and
  the CHECK-constraint bypass test.
- `internal/worker/handler_ingest_budget_integration_test.go` - the flood re-pointed at the DELETE arm,
  which is the path that still produces a genuine store fault.
- `README.md` - the `stream` row, stating the bound and what exceeding it costs.
