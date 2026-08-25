# A test seam for `Handler.pool`, and the default lane's first successful worker registration

- **Date:** 2026-08-25
- **Type:** backend slice (Go only - no SQL, no migration, no proto, no wiring change)
- **Closes:** `docs/backlog/idea-2026-08-24-handler-pool-has-no-seam.md`
- **Blocked on:** nothing.
- **Phase:** 1 (design). Phase 2 writes the plan.

This spec was produced in an unattended run, so every place the brainstorming flow would ask a
human, the call is made here with the reasoning written down. Where a fork was not resolvable by
evidence, the more conservative and more reversible arm was taken and labelled as such. Every claim
about current code carries a `file:line`; where a claim could not be established from the tree it is
labelled as an assumption.

---

## 1. Verification of the backlog item's claims

The item is short and its diagnosis is mechanical, so all of it was checked. Everything load-bearing
is **confirmed**. Two things it does not say change the design and are recorded as findings.

### Confirmed

| Claim | Evidence |
|---|---|
| `applyInventory` opens a transaction on the concrete `*pgxpool.Pool` unconditionally | `internal/worker/handler.go:1743`: `return pgx.BeginTxFunc(ctx, h.pool, pgx.TxOptions{}, func(tx pgx.Tx) error {`. No `h.pool == nil` guard, no `len(inv) == 0` short circuit; the `for` loop over `inv` is *inside* the transaction (`:1748`), so an empty inventory still runs `ReplaceWorkerInventory` and commits. |
| `Handler.pool` is a concrete type with no seam | `internal/worker/handler.go:143`: `pool *pgxpool.Pool`. Set only by `NewHandler` (`:256`) and `NewHandlerWithGrace` (`:262`); there is no other assignment anywhere in the tree. |
| It sits between `reconcileRunningTasks` and the `RegisterResponse` send | `finishRegister`: reconcile at `:687`, `applyInventory` at `:693`, `stream.Send` at `:699`. |
| Everything below it is unreachable without Postgres | The observables the item names all sit below the `applyInventory` call: `h.registry.Register` (`:714`), `handedOff = true` (`:789`), `h.Metrics.Activate` (`:791-793`), the online broker publish (`:795-798`), `go h.triggerDispatch()` (`:800`), `return workerID, sender, nil` (`:802`). |
| The success path is covered only under `//go:build integration` | Every test file in `internal/worker` that drives a successful registration is tagged: `handler_test.go:1`, `handler_teardown_test.go:1`, `handler_auth_test.go:1`, `handler_atomic_test.go:1`, `handler_reconcile_canonical_test.go:1`, `handler_register_strand_integration_test.go:1`, and six more. CI runs `go test -race ./...` with no tag. |
| The default-lane fixture leaves `pool` nil deliberately | `internal/worker/handler_register_strand_test.go:202-209` builds `&Handler{q: store.New(db), registry: ..., broker: ..., grace: ..., triggerDispatch: ...}` - no `pool` field - and the file's own header (`:30-37`) says why. |
| The guard was bought instead of the seam, and is large | `internal/worker/handler_handoff_guard_test.go` is 669 lines for one flag, and its own header (`:16-22`) gives the pool as the reason a behavioural test was impossible. |

### Findings the item does not state

**F1. The pool seam alone does not reach a successful `finishRegister`. A `pgx.Rows` fake is also
required, and none exists in the tree.** `reconcileRunningTasks` calls `GetActiveTasksForWorker`
(`internal/worker/handler.go:810`), which sqlc emits as a `:many`:
`rows, err := q.db.Query(...)` then `defer rows.Close()`, `for rows.Next()`, `rows.Err()`
(`internal/store/tasks.sql.go:500-516`). The existing fake's `Query` returns `(nil, d.queryErr)`
(`internal/worker/handler_register_strand_test.go:65-70`), so with a nil `queryErr` it returns
`(nil, nil)` and the generated code calls `Close()` on a nil `pgx.Rows` interface - a panic, one
frame past where the pool used to panic. A repo-wide search for `Next() bool` returns **no matches**,
so no empty-rows fake exists to reuse. Section 4.2 scopes it. **This is the finding that makes
"narrow the pool and stop" an incomplete slice**, and the item's acceptance criterion would not be
met by it.

**F2. Three call sites share one interface, not one.** The item talks only about `applyInventory`,
but `h.pool` has three uses and all three are the same expression shape:
`pgx.BeginTxFunc(ctx, h.pool, pgx.TxOptions{}, ...)` at `:471` (`enrollAndRegister`), `:546`
(`autoEnrollAndRegister`) and `:1743` (`applyInventory`). One interface covers all three; see
section 3.1.

---

## 2. What is actually being fixed

Not a missing test. A **structural** property: `internal/worker` has no default-lane route to a
successful worker registration, so every control on that path can only be pinned by reading source
text. The measured cost is on the record - the 2026-08-24 finishregister-strand slice needed to pin
one line (`handedOff = true`, `handler.go:789`) and paid for a 669-line `go/parser` guard, which was
**evaded twice** before it held, each time by a construct that is nil in a default-lane fixture and
real under `main.go` (`h.Metrics != nil`, then `h.pool != nil`; see the guard's own `:236-244` and
`:437-443`).

The fix is a one-field type change plus the fixture work that field unlocks, and then behavioural
tests that CI actually runs.

**Non-goals** (section 8 states them in full): no behaviour change anywhere in `handler.go`, no
change to `applyInventory`'s semantics, no new production code path, no change to what
`cmd/relay-server` builds.

---

## 3. Decision 1 - the seam's shape

**Chosen: narrow the field to a single-method interface, following `internal/scheduler`.**

`terminalTailStore` (`internal/scheduler/dispatch.go:368-374`) and `failClaimedStore` (`:434-442`)
are the established precedent, and `failClaimedStore`'s own doc comment states this exact purpose:
"Dispatcher.q is a concrete `*store.Queries`, so this is what makes the fence-rejection branch
drivable by a fake - without Postgres, and therefore in the DEFAULT lane." Nothing about that
argument is specific to `store.Queries`. Its fake (`dispatch_fence_test.go:18-29`) is the shape to
copy, including the panic-by-default methods.

### 3.1 The exact interface

`pgx.BeginTxFunc`'s second parameter is an **anonymous** interface, declared inline
(`github.com/jackc/pgx/v5@v5.9.1/tx.go:410-417`):

```go
func BeginTxFunc(
	ctx context.Context,
	db interface {
		BeginTx(ctx context.Context, txOptions TxOptions) (Tx, error)
	},
	txOptions TxOptions,
	fn func(Tx) error,
) (err error)
```

`*pgxpool.Pool` satisfies it: `func (p *Pool) BeginTx(ctx context.Context, txOptions pgx.TxOptions) (pgx.Tx, error)`
(`pgxpool/pool.go:798`). So the minimal method set is **exactly one method**, and it is the same one
for all three call sites - they differ only in the closure they pass. Declare it once in
`internal/worker/handler.go`, near the `Handler` struct:

```go
// txBeginner is the subset of *pgxpool.Pool this package uses: the one method
// pgx.BeginTxFunc requires. Handler.pool is typed as this rather than as the
// concrete pool for the same reason internal/scheduler narrowed failClaimedStore
// - it is what makes finishRegister's SUCCESS path drivable by a fake, without
// Postgres, and therefore in the lane CI actually runs.
//
// *pgxpool.Pool satisfies it, so cmd/relay-server's wiring is unchanged.
type txBeginner interface {
	BeginTx(ctx context.Context, txOptions pgx.TxOptions) (pgx.Tx, error)
}
```

Then `handler.go:143` becomes `pool txBeginner`, and the two constructor parameters (`:255`, `:261`)
change type from `*pgxpool.Pool` to `txBeginner`. Nothing else in `handler.go` moves:
`pgx.BeginTxFunc(ctx, h.pool, ...)` at `:471`, `:546` and `:1743` is unchanged source text, because
an interface value satisfying the anonymous parameter interface is assignable to it.

**The field keeps the name `pool`.** In production it still is the pool, three call sites and a
dozen comments name it, and renaming it would churn the very guard file this slice is trying to
shrink. The doc comment above carries the type's real meaning. *Assumption, flagged:* this is a
judgement call, not evidence; if a reviewer prefers `db` or `tx`, it is a mechanical rename.

**The interface is unexported.** No external caller ever names it - `worker.NewHandler(q, pool, ...)`
compiles from any package because the caller supplies a value, not a type name - and unexported
matches both scheduler precedents. The one cost is godoc: `NewHandler`'s signature will render an
unexported type. Exporting it as `worker.TxBeginner` is a one-word change if that is judged to
matter; it is not judged to matter here.

### 3.2 Production wiring is byte-identical

`cmd/relay-server/main.go:142` is
`agentHandler := worker.NewHandlerWithGrace(q, pool, registry, broker, dispatcher.Trigger, grace)`
where `pool` is the `*pgxpool.Pool`. It compiles unchanged and behaves identically.

Every other call site in the tree passes either a real `*pgxpool.Pool` (the integration tests, e.g.
`internal/worker/handler_test.go:119`) or an untyped `nil`
(`cmd/relay-server/counters_wiring_test.go:235`, `:631`, `:668`, `:1033`). Untyped `nil` is
assignable to an interface, so those compile unchanged too. Their behaviour is unchanged in the way
that matters: previously `h.pool` held a typed-nil `*pgxpool.Pool` and a `BeginTxFunc` on it would
nil-dereference inside `Pool.BeginTx`; now it holds a nil interface and the same call panics on the
interface dispatch. Neither of those tests reaches a transaction, and both panic identically if a
future edit makes them.

### 3.3 The alternative, and why it lost

**Inject the inventory step as a function value** (`applyInventoryFn func(ctx, uuid, []*Update) error`,
defaulting to today's method). Rejected on three grounds:

1. It covers **one** of the three `h.pool` sites (F2). The two enrollment transactions stay
   untestable, so `enrollAndRegister` and `autoEnrollAndRegister` keep their integration-only
   coverage and a later slice pays this cost again.
2. A settable function field on `Handler` is a *production* seam - anything can replace the inventory
   step at runtime - where the interface is a *type* seam that only a test can exploit by
   constructing a different value. `Handler` already has four exported mutable fields set after
   construction (`Metrics`, `AllowAutoEnroll`, `RegistrationTimeout`, `TrailingLogWindow`;
   `handler.go:149-169`); adding a fifth that swaps out behaviour is a larger surface than adding an
   interface.
3. It does not match a precedent in this tree; the narrowed interface does, twice.

A third option - **keep the pool and add `if len(inv) == 0 { return nil }`** - is forbidden by the
item and is correct to forbid: an agent legitimately reporting zero devices would stop clearing stale
rows, and the dispatcher scores warm-workspace affinity off those rows
(`internal/scheduler/dispatch.go:110-120`). Section 8 restates it as a non-goal and section 6 gives
it a test.

---

## 4. Decision 2 - what the fake must do

The acceptance criterion is a default-lane test that drives `finishRegister` to a **successful
return**. Below is the complete list of what that path touches, in order, with what each needs.
`h.q` is `*store.Queries` (`handler.go:142`) built over the `store.DBTX` interface
(`internal/store/db.go:14-22`), which the package already fakes; `store.Queries.WithTx(tx pgx.Tx)`
returns a fresh `*Queries` whose `db` is the tx (`internal/store/db.go:28-32`), so the tx fake must
satisfy `pgx.Tx`, not `store.DBTX`.

| Step | Site | What the fixture must supply |
|---|---|---|
| `GetWorkerByAgentTokenHash` | `handler.go:525` | `DBTX.QueryRow` -> a `store.Worker` row. **Exists**: `strandWorkerRow` (`handler_register_strand_test.go:146-179`). |
| `RegisterWorkerConnection` | `handler.go:605` | `DBTX.QueryRow` -> the same `store.Worker` row (`internal/store/workers.sql.go:1137-1138`). **Exists**, same stub. |
| `grace.Cancel` | `handler.go:683` | A `GraceRegistry`. **Exists** (`handler_register_strand_test.go:198-201`). |
| `GetActiveTasksForWorker` | `handler.go:810` | `DBTX.Query` -> a non-nil, **empty** `pgx.Rows`. **DOES NOT EXIST** - see F1. |
| `applyInventory` -> `BeginTx` | `handler.go:1743` | The pool seam -> a `pgx.Tx` fake. **DOES NOT EXIST.** |
| `ReplaceWorkerInventory` | `handler.go:1745` | `Tx.Exec` returning `(pgconn.CommandTag{}, nil)` (`internal/store/worker_workspaces.sql.go:151-154`). |
| commit / rollback | `pgx/tx.go:427-442` | `Tx.Commit` -> nil; `Tx.Rollback` -> nil **or** `pgx.ErrTxClosed`. `beginFuncExec` defers `Rollback` after `Commit` and only propagates a rollback error that is not `ErrTxClosed` (`tx.go:428-433`), so returning nil from both is correct and simplest. |
| `stream.Send` | `handler.go:699` | A stream fake. **Exists**: `scriptedStream` (`handler_registration_deadline_test.go:26-58`), whose `Send` returns nil (`:52`). Needs to **record** what it was sent - see 4.3. |
| `NewWorkerSender` / `registry.Register` | `handler.go:712-714` | A `Registry` (`NewRegistry()`), already in the fixture. |
| `Metrics.Activate` | `handler.go:791-793` | A `*metrics.Store` (`metrics.NewStore(n)`), **not** in the fixture today - `newStrandHandler` leaves `Metrics` nil. |
| `broker.Publish` | `handler.go:795` | An `*events.Broker`. **Exists.** |
| `go h.triggerDispatch()` | `handler.go:800` | A non-nil func. **Exists**, but the new test needs it to be observable (a channel close), not `func() {}`. |

### 4.1 The `pgx.Tx` fake

`pgx.Tx` has eleven methods (`pgx/tx.go:122-151`). Implement it by **embedding the interface** and
overriding only the three that are reached:

```go
// fakeTx is a pgx.Tx that records the statements applyInventory issues. The
// embedded nil pgx.Tx supplies the eight methods this path never calls, and it
// supplies them as a PANIC rather than a plausible zero value - the same
// fail-loud choice fenceStore makes in internal/scheduler.
type fakeTx struct {
	pgx.Tx
	// ...recorder, error injection
}
func (tx *fakeTx) Exec(...) (pgconn.CommandTag, error)
func (tx *fakeTx) Commit(context.Context) error   // nil
func (tx *fakeTx) Rollback(context.Context) error // nil
```

The embedded-nil-interface idiom is what makes this eleven-method type cost four lines instead of
forty, and its failure mode is a panic naming the method - which is the right report if
`applyInventory` ever grows a `Query` or a `SendBatch`.

### 4.2 The empty `pgx.Rows` fake (F1)

`pgx.Rows` gets the same treatment: embed the nil interface, override `Close()` (no-op),
`Next() bool` (false), `Err() error` (nil). That is the whole of what `GetActiveTasksForWorker`'s
generated body calls when there are no rows (`internal/store/tasks.sql.go:500-516`).

**Return empty, not populated.** An empty result means `serverSet` is empty (`handler.go:815-818`),
the reported-task loop does nothing, and the requeue loop (`handler.go:903-911`) does nothing - so
`reconcileRunningTasks` issues exactly one statement and `cancelIDs` is nil. Reconcile's *content*
is already covered by `handler_reconcile_canonical_test.go` in the integration lane; this slice is
about reaching the code below it, and a populated fixture would add failure modes without adding
coverage of the thing under test.

### 4.3 Recording fakes, and the one existing type that must change

Three recorders are needed and they must be **separate**, because the assertions distinguish them:

- **`DBTX.Exec`** - already recorded by `strandDB` (`handler_register_strand_test.go:52-60`). This is
  where `MarkWorkerOfflineIfEpoch` lands, so "the deferred release did not fire" is
  `len(db.execsSeen()) == 0`.
- **`Tx.Exec`** - a *new*, separate list on `fakeTx`. `ReplaceWorkerInventory` must not pollute the
  list above, or the "no release fired" assertion has to be weakened from a count to a substring
  match. Keeping them separate also gives section 6's non-goal test its observable.
- **`stream.Send`** - `scriptedStream.Send` currently discards its argument
  (`handler_registration_deadline_test.go:52`). Add a mutex-guarded
  `sent []*relayv1.CoordinatorMessage` and a `sentMsgs()` accessor. The mutex is load-bearing: the
  new test runs `Connect` on its own goroutine and reads the slice from the test goroutine, and CI
  runs `-race`. Both existing users of `scriptedStream` are unaffected by an added field.

### 4.4 Where the fixture lives

A **new file**, `internal/worker/handler_register_success_test.go`, package `worker` (unexported
access is required for `finishRegister`, `handedOff`'s effects, and `sender.connEpoch`). It reuses
`strandDB`, `strandWorkerRow`, `strandWorkerID`, `strandEpoch` and `scriptedStream` from the existing
default-lane files rather than re-declaring them - they are the same package.

**`newStrandHandler` gains the fake pool** (`handler_register_strand_test.go:194-211`). This is a
two-line edit to an existing helper and it changes nothing about the three tests that use it - all
three fail inside `reconcileRunningTasks`, four lines above `applyInventory`, so the pool is never
touched. It is worth doing anyway, and section 7 explains why: it closes the evasion class the guard
was grown twice to catch, which is exactly "a field that is nil in every default-lane fixture and
real under `main.go`".

---

## 5. Decision 3 - what the new tests assert

Two tests. Both in the default lane, both driven through **`Connect`**, not by calling
`finishRegister` directly.

**Why through `Connect`.** The flag under test partitions a window between *two* releases -
`finishRegister`'s own deferred `releaseWorkerGeneration` (`handler.go:653-657`) and `Connect`'s
`defer h.teardownConnection` (`handler.go:283-299`, teardown at `:1582-1589`). A test that calls
`finishRegister` directly sees only one of them, and the property worth pinning is that **exactly
one release happens across the whole connection, and it is teardown's.** Driving `Connect` also
keeps the new test in the same fixture family as the three existing strand tests.

`scriptedStream.Recv` blocks on `release` once its scripted messages are exhausted
(`handler_registration_deadline_test.go:41-49`), so `Connect` parks in the message loop after a
successful registration and the observables are stable while the test asserts them. Closing
`release` then drives teardown.

### 5.1 `TestConnect_ASuccessfulRegistrationPublishesTheWorkerAndKeepsItsGeneration`

Run `Connect` on a goroutine. Wait for the online broker event (that is the natural barrier - it is
the last statement before `go h.triggerDispatch()`), then assert:

| Observable | Read from | Kills |
|---|---|---|
| **Sender registered** - `h.registry.Send(uuidStr(strandWorkerID), &relayv1.CoordinatorMessage{})` returns nil | `internal/worker/registry.go:50-58`; a missing entry returns `worker %q is not connected` | deleting or reordering `h.registry.Register` (`handler.go:714`) |
| **The registered sender carries this connection's epoch** - the message reaches `scriptedStream.Send`, and the returned `*workerSender`'s `connEpoch == strandEpoch` | `handler.go:713`; `sender.go:32-35`. `strandEpoch` is derived by counting `store.Worker`'s int32 fields and every int32 column scans distinct (`handler_register_strand_test.go:106-124`, pinned by `TestStrandFixture_EveryInt32ColumnScansDistinct`) | binding any other int32 column (e.g. `updated.MaxSlots`) at `:713` |
| **Worker online, exactly once** - exactly one `events.Event{Type:"worker"}` observed, and its data is `{"id":"...","status":"online"}` | `handler.go:795-798` | deleting the publish; and, jointly with the next row, deleting `handedOff = true` |
| **The generation was NOT released** - `db.execsSeen()` is **empty**; no statement containing `status = 'offline'` was issued | `strandDB.Exec` (`handler_register_strand_test.go:52-60`); `markWorkerOffline` -> `MarkWorkerOfflineIfEpoch` is the only `Exec` on this path | **deleting `handedOff = true` (`handler.go:789`)** - the primary target |
| **No grace timer armed** - nothing on the `fired` channel within a window comfortably past the registry's 20ms period | `handler_register_strand_test.go:196-201` | same mutation, second signal |
| **Metrics activated** - `h.Metrics.LastSampleAt(workerID)` returns `(t, true)` with `t` non-zero | `internal/metrics/store.go:104-114`; `Activate` seeds `activatedAt` and `LastSampleAt` returns it when there are no samples (`:55-59`, `:111-113`) | deleting the `Metrics.Activate` call (`handler.go:791-793`) |
| **Dispatch triggered** - the `triggerDispatch` func closes a channel; the test waits on it | `handler.go:800` | deleting the `go h.triggerDispatch()` |
| **The RegisterResponse was actually sent** - `scriptedStream.sentMsgs()` has one `CoordinatorMessage_RegisterResponse` whose `WorkerId` is `uuidStr(strandWorkerID)` and whose `CancelTaskIds` is empty | `handler.go:699-709` | deleting the send, or moving it below the registry publish (which the "no other goroutine can race us on stream.Send" comment at `:697-698` forbids for a different reason) |

Then close `release` and assert the **teardown half**:

- `Connect` returns.
- `db.execsSeen()` now has **exactly one** statement, it contains `status = 'offline'` and
  `connection_epoch = $4`, and its args contain `strandEpoch`. That is `teardownConnection` ->
  `releaseWorkerGeneration` (`handler.go:1588`), fenced on `sender.connEpoch`.
- One grace fire at `strandEpoch`.

**This second half is what makes the flag's assertion unfalsifiable by accident.** With
`handedOff = true` deleted, the count across the connection's whole life is **two**, not one, and the
first one lands *before* the mid-connection assertions - so both halves redden independently.

### 5.2 `TestConnect_ARegistrationWhoseRegisterResponseSendFailsReleasesTheGeneration`

Same fixture, with a stream whose `Send` returns an error. `finishRegister` returns at
`handler.go:708` with `handedOff` still false, so the deferred release fires.

Assert: `Connect` returns an error mentioning `send register response`; exactly one `Exec` containing
`status = 'offline'` fenced on `strandEpoch`; a grace fire at `strandEpoch`; and `h.registry.Send(...)`
**fails** - no sender was ever published, because `h.registry.Register` is below the send.

This arm exists today only as `TestRegisterWorker_SendFailureReleasesTheGeneration`
(`handler_register_strand_integration_test.go:65`), which CI compiles and never runs. **The
integration test stays** - it carries the durable half this one cannot (a real `workers` row, a real
task, a real `RequeueWorkerTasksIfEpoch`). What changes is its doc comment (section 9).

This test is also what lets section 7 retire the `flip-not-before-the-send` clause of the structural
guard.

---

## 6. The non-goal gets a test

`applyInventory`'s behaviour must not change (section 8). Pin it, in the same new file:

**`TestFinishRegister_AppliesInventoryEvenWhenTheAgentReportsNone`** - register with
`reg.Inventory == nil` and assert `fakeTx` recorded **one** statement and that it is the
`worker_workspaces` delete (`ReplaceWorkerInventory`,
`internal/store/worker_workspaces.sql.go:151-154`).

Without it, "return early on an empty inventory" is a green change that silently freezes stale
warm-workspace rows for any agent that legitimately reports zero. The item forbids that fix
explicitly and this is the check that makes the prohibition enforceable rather than advisory.

**`TestFinishRegister_SucceedsWhenTheInventoryTransactionFails`** - make `fakeTx.Exec` (or `BeginTx`
itself) return an error and assert `finishRegister` still returns successfully and the worker still
comes online. `applyInventory`'s error is logged and swallowed at `handler.go:693-695`, and the
"EVERYTHING BELOW THIS LINE MUST STAY INFALLIBLE" rule at `:755-759` names that log-and-continue
shape as the required pattern. Turning that `log.Printf` into a `return` is a plausible edit; this
test kills it.

---

## 7. Decision 4 - the fate of `handler_handoff_guard_test.go`

**Chosen: reduce, do not retire. Delete five clauses; keep the rest; rewrite the prose.**

Retiring it outright is wrong, and the reason is stated in the guard's own comment at
`handler_handoff_guard_test.go:32-45`: **what is pinned is a point; what the code needs is a range**,
and every position in that range behaves identically today. A behavioural test cannot see source
position, so the adjacency and infallibility clauses have no behavioural equivalent and never will.

### 7.1 Clause-by-clause

| # | Clause | Site | Covered behaviourally? | Disposition |
|---|---|---|---|---|
| G1 | exactly one deferred closure reaching `releaseWorkerGeneration` | `:472-478` | No. Zero closures is caught (5.2 reddens), but *two* closures is a source property. | **Keep** |
| G2 | that `defer` is a direct body statement | `:482-489` | Partly. `if h.grace != nil { defer ... }` is not caught - the fixture has grace non-nil, as does production. | **Keep** |
| G3 | the closure calls the release exactly once | `:490-497` | Yes, twice over - and this is the one deletion justified by **redundancy** rather than by behaviour. G4 already pins the closure body to exactly one or two statements with the release at a fixed place, so no second release can hide inside an accepted body; and both new tests assert exact release counts (5.1 expects zero mid-connection, 5.2 expects one). Its only remaining value is a better failure message than G4's. `countCallsNamed` stays - `handoffFlagIdent` uses it at `:467` to *select* the candidate closure, a different job. | **DELETE**, and it is the one clause safe to drop from the slice if a reviewer disagrees; nothing else depends on it |
| G4 | closure body is exactly the guard construct | `:502-579` | Partly. Its own worked example - `if h.pool != nil { return }` ahead of the release (`:437-443`, `:603-607`) - **becomes caught** once `newStrandHandler` gets the fake pool (4.4). Other conditions (`h.AllowAutoEnroll`, a future field) remain uncaught. | **Keep**, correct the example |
| G5 | `finishRegister` exists | `:77-81` | n/a - anchor | **Keep** |
| G6 | the stream parameter exists, found by type suffix | `:84-88` | n/a - anchor, serving **only** G15 | **DELETE** with G15 (and with it `paramNamedByType`, `:647-658`) |
| G7 | exactly one call on the stream receiver | `:92-95` | n/a - anchor, serving **only** G15 | **DELETE** with G15 |
| G8 | exactly one call on `h.registry` | `:96-103` | n/a - anchor, serving G16/G17 | **Keep** |
| G9 | exactly one return whose last result is nil | `:106-119` | n/a - anchor, serving G18/G19 | **Keep** |
| G10 | flag initialised false exactly once | `:200-204` | Partly - a shadowing re-declaration that leaves the success path unflipped reddens 5.1. Retained because it is what makes G13's count well-defined. | **Keep** |
| G11 | no write other than the literal `true` | `:205-209` | Partly, same reasoning as G10 | **Keep** |
| G12 | no address-of the flag | `:210-219` | **Yes, completely.** Its failure message spells the harm as "releases the generation on every SUCCESSFUL registration - the live agent published 'offline', its metrics entry wiped, a grace timer requeueing its running tasks". Those are literally 5.1's assertions. `p := &handedOff; *p = false` below the flip now reddens 5.1 four ways. | **DELETE** (mutation M14 is its replacement proof) |
| G13 | set to true exactly once | `:220-227` | Partly - zero flips reddens 5.1; two flips is a source property, and G17 needs `setTrue[0]` to be unique | **Keep** |
| G14 | the flip is a direct body statement, not nested | `:235-244` | No, and this is the sharp one. Its own example `if h.Metrics != nil { handedOff = true }` **evades** 5.1, because the new fixture sets `Metrics` (it must, to assert `Activate`). Any wrap on a condition true in the fixture and false in production evades. | **Keep** |
| G15 | the flip is not before the `RegisterResponse` send | `:247-253` | **Yes.** That is exactly the arm 5.2 drives: flip the flag above the send and 5.2's release assertions all fail. | **DELETE** |
| G16 | `h.registry.Register` is itself the indexed statement, not merely contained in one | `:288-302` | **No. Source position only.** | **Keep** |
| G17 | the flip is the statement immediately after `registry.Register` | `:303-313` | **No. Source position only** - the guard's own comment says every position in the range behaves identically today and mutation confirmed it. | **Keep** |
| G18 | the flip is not below the success return | `:314-318` | No - reachable only by a source arrangement the compiler would otherwise accept | **Keep** |
| G19 | no error return below the flip | `:322-336` | **No.** It is a claim about statements that do not exist yet. No runtime test can assert the absence of a future return. | **Keep** |

**Net: G3, G6, G7, G12 and G15 are deleted, plus the `paramNamedByType` helper.** Roughly 70 lines
including their comments. The guard stops being the *only* witness - which is what made it grow three
rounds of evasions - and becomes what it should have been: a drift guard over source positions no
runtime test can see.

**What the reduction is NOT.** It is not a claim that the guard was a mistake. Two of the deletions
(G6, G7) are bookkeeping for a third, one (G3) is redundancy with a sibling clause, and only G12 and
G15 are genuine structure-replaced-by-behaviour. The measurable win of this slice is not the deleted
lines; it is that the next slice touching `finishRegister` has a behavioural witness and does not
have to grow the guard again.

### 7.2 Ordering, and the prose

**The reduction is the last step of the slice.** The item is explicit: do not delete before the
behavioural test exists. Concretely: both tests in section 5 green in the default lane first, then
the clause deletions, then re-run, then the mutation battery in section 10.

The guard's header (`:12-69`) and several inline comments assert things this slice makes false, and
they are acceptance criteria, not cleanup (section 9).

---

## 8. Non-goals

- **`applyInventory`'s behaviour does not change.** No early return on an empty inventory, no nil-pool
  guard, no reordering relative to the send. Section 6 pins this with a test rather than a comment.
- **`bug-2026-08-23-applyinventory-null-timestamp-freezes-inventory` is not fixed here.** It stays
  open. This slice makes it *cheaply testable* for the first time (a `fakeTx.Exec` returning a NOT
  NULL violation reproduces it without Postgres), which is worth noting on the item but is not this
  slice's job. Note also that that item's line citations (`handler.go:1387-1411`) have drifted;
  `applyInventory` is at `:1742-1766` today.
- **No new production behaviour, anywhere.** The only non-test production edit is the type of one
  field and two parameters.
- **The integration lane is not thinned.** No `//go:build integration` test is deleted or moved.
  Duplication between `TestRegisterWorker_SendFailureReleasesTheGeneration` and section 5.2 is
  deliberate and the two are at different layers.
- **`Handler`'s other dependencies are out of scope.** `registry`, `broker`, `grace` and `Metrics`
  are all already constructible without Postgres; only `pool` was not.
- **`internal/api` and `cmd/relay-server` are untouched.**

### 8.1 Invariant compliance

- *End the generation before releasing the resource / arm the release in the same breath.* Untouched -
  and, for the first time, **observed**: section 5.1's teardown half asserts exactly one release
  across the connection's life, and 5.2 asserts the failing arm still releases exactly once.
- *Epoch fence.* No write to `tasks.status` or `task_logs` is added or moved. The tests assert the
  existing `connection_epoch = $4` fence is carried, on the real epoch.
- *Identity-checked teardown.* `teardownConnection`'s `UnregisterIf` gate (`handler.go:1583`) is
  unchanged and is now exercised in the default lane by 5.1's teardown half.
- *One bounded sender per gRPC stream.* Unchanged. The test drives one `workerSender` through
  `NewWorkerSender` (`handler.go:712`) exactly as production does, and asserts delivery through
  `Registry.Send` rather than touching the stream directly.
- *No interior pointers across locks / single JSON entry point / single job-spec pipeline.* Untouched.

---

## 9. Prose that must move

Prose has been the dominant defect class in this repo for nine consecutive iterations, and this slice
falsifies six specific written claims. Each is an acceptance criterion.

1. **`internal/worker/handler.go:753`** - "The guard lives in the default lane because no
   default-lane test can drive a successful registration at all." False after this slice. Replace
   with what remains true: the guard covers source positions no behavioural test can see, and name
   the new test as the behavioural half.
2. **`handler_handoff_guard_test.go:16-22`** ("WHY A GUARD AND NOT A BEHAVIOURAL TEST ... the
   default-lane fixture structurally cannot drive one"). Rewrite: there is now a behavioural witness;
   what survives here is the position half, per 7.1.
3. **`handler_handoff_guard_test.go:437-443` and `:603-607`** - both use
   `if h.pool != nil { return }` as the worked example of an evasion the default lane cannot notice,
   on the stated grounds that "newStrandHandler leaves pool nil deliberately". Once 4.4 gives that
   fixture a fake pool, the premise is false. Either pick an example that is still true
   (`h.AllowAutoEnroll` is one) or state that this specific evasion is now caught and the clause
   remains for the general shape.
4. **`handler_register_strand_test.go:30-37`** - "IT WORKS BECAUSE THE FAILING PATH NEVER TOUCHES
   `h.pool` ... That is also why the RegisterResponse-send arm cannot live in this lane." The second
   sentence becomes false. Correct it, and keep the first, which stays true and still explains why
   those three tests need no inventory fixture.
5. **`handler_register_strand_integration_test.go:57-64`** - "it needs a real pool, because
   applyInventory opens a transaction on `*pgxpool.Pool` unconditionally ... so a pool-less fixture
   cannot reach `stream.Send` at all." False after this slice. The test **stays**; its justification
   changes to the one that survives, already written in its next paragraph: "it carries what the
   default-lane proof cannot: the actual worker ROW and the actual TASK, through a real grace timer,
   to a real requeue."
6. **`docs/backlog/idea-2026-08-23-integration-only-guards-ci-never-runs.md`** - this slice removes
   one of that item's named instances. It is not closed by this slice; note the reduction on it if
   the plan has a cheap place to.

No README change: nothing operator-visible moves. No CLAUDE.md change: no invariant's wording is
affected.

---

## 10. Testing summary and the mutation battery

**RED-first is achievable for both new tests** and must be used: at HEAD, section 5.1 panics inside
`applyInventory` (nil pool) and section 5.2 does the same. That is a genuine RED, not a "capability
does not exist" RED - and per this project's own rule, the seam must not be what turns it green
vacuously, which is why every assertion in 5.1 is about handler state and none is about the pool.

**Does the fake pool make the test vacuous?** The honest answer, stated because the project's rule
demands it: the fake pool is a *pass-through* on the path under test, and it makes exactly one thing
unfalsifiable - that `applyInventory` writes correct SQL. That is not what this slice tests, it is
covered in the integration lane, and section 6 pins the two properties of `applyInventory` that
*are* load-bearing for this path (it runs unconditionally; its failure does not abort registration).
Everything else asserted in 5.1 is downstream state - the registry map, the broker channel, the
metrics store, the `DBTX` statement log - none of which the pool fake can produce.

Each mutation below must redden a **named** test, and each discriminating input must survive into a
permanent test rather than being reverted with the mutation. Per the recorded lesson, verify each
mutation actually applied (CRLF has silently defeated four in a row in this tree) and treat a uniform
result across the battery as a broken harness rather than as good coverage.

| # | Mutation | Killed by |
|---|---|---|
| M1 | Delete `handedOff = true` (`handler.go:789`) | 5.1, four ways: non-empty `execsSeen`, a second worker event, a grace fire, and a teardown count of 2 |
| M2 | `handedOff = true` -> `if h.Metrics != nil { handedOff = true }` | **Not** killed behaviourally - killed by guard clause G14. This is the mutation that proves G14 must survive; run it and record the survival against 5.1. |
| M3 | Delete `h.registry.Register` (`:714`) | 5.1's `registry.Send` assertion |
| M4 | `sender.connEpoch = updated.ConnectionEpoch` -> `updated.MaxSlots` (`:713`) | 5.1's `connEpoch` assertion and the teardown fence arg. Distinctness is guaranteed by `TestStrandFixture_EveryInt32ColumnScansDistinct`. |
| M5 | Delete `h.Metrics.Activate` (`:791-793`) | 5.1's `LastSampleAt` assertion |
| M6 | Delete the online broker publish (`:795-798`) | 5.1's event assertion - which is also its barrier, so the test must fail as a bounded timeout with a message that says what was missing, never hang |
| M7 | Delete `go h.triggerDispatch()` (`:800`) | 5.1's dispatch-signal wait, likewise bounded |
| M8 | Move `handedOff = true` above `stream.Send` | 5.2 - this is the replacement proof for deleting G15 |
| M9 | `if len(inv) == 0 { return nil }` at the top of `applyInventory` | 6, first test |
| M10 | `log.Printf(...)` -> `return "", nil, err` at `handler.go:693-695` | 6, second test |
| M11 | Revert `Handler.pool` to `*pgxpool.Pool` | Compile error in the new fixture. The seam cannot silently regress. |
| M12 | Remove the fake pool from `newStrandHandler` | Nothing - **by design**. Record it as a known survivor so nobody later "fixes" it; its purpose is closing G4's evasion class, which M13 measures. |
| M13 | Insert `if h.pool != nil { return }` ahead of the release in the deferred closure | Guard clause G4 (unchanged), **and** now 5.2 behaviourally, once 4.4 lands. Run both; if 5.2 does not redden, the fake pool did not reach `newStrandHandler`. |
| M14 | `p := &handedOff` plus `*p = false` below the flip | 5.1 - this is the replacement proof for deleting G12, and it must be run **after** the deletion, not before |

**Regression scope.** `go test ./internal/worker/... -race` must stay green with no edit to any
existing test *assertion*; the only permitted edits to existing test files are the additive
`scriptedStream` recorder (4.3), the `newStrandHandler` pool (4.4), the guard-clause deletions (7.1)
and the prose corrections (9). `go build ./...` must be clean with no change to `cmd/relay-server`.
The integration lane must still compile (`go vet -tags integration ./...`) and, where a Docker host
is available, still pass.

---

## 11. Known limitations

- **`applyInventory`'s SQL remains integration-only.** The seam moves the boundary, it does not erase
  it: a fake tx proves the statement was *issued*, never that it is correct against the schema.
- **The new tests do not cover `enrollAndRegister` or `autoEnrollAndRegister`.** The seam makes their
  transactions fakeable (F2) but this slice adds no test for them. That is a deliberate scope line,
  and it is the obvious next consumer of this seam - section 12.
- **The guard is reduced, not removed**, so the `go/parser` dependency and its brittleness (renaming
  `releaseWorkerGeneration` or `finishRegister` fails the guard with a message about a missing
  anchor) remain. That was already true and is not made worse.
- **`main()` wiring stays untested**, as it is for every other `Handler` field. Its protection is that
  the type change is compile-checked at `cmd/relay-server/main.go:142`.
- **A behavioural test still cannot see a wrap on a condition that is true in the fixture and false
  in production.** G14 is what covers that, and no amount of fixture work removes the need for it.

---

## 12. Backlog recommendations

Proposed, **not filed** - the human accepts or rejects each:

1. **`idea`: extend the default-lane registration fixture to the two enrollment transactions.**
   `enrollAndRegister` (`handler.go:448-516`) and `autoEnrollAndRegister` (`:539-600`) become
   fakeable the moment this slice lands (F2), and both contain branch logic no default-lane test
   reaches today: `errEnrollmentNotConsumable` (`:496-498`, `:509-511`), `errWorkerRevoked`
   (`:553-555`, `:580-582`), and the auto-enroll audit log line at `:598` whose own comment argues at
   length about its forgeability. Medium confidence that it deserves its own slice; high confidence
   that it is now cheap.
2. **Amendment, not a new item**, to `bug-2026-08-23-applyinventory-null-timestamp-freezes-inventory`:
   this slice supplies the seam that makes its regression test a default-lane test, and its line
   citations have drifted (section 8).
3. **Amendment, not a closure**, to `idea-2026-08-23-integration-only-guards-ci-never-runs`: this
   slice removes one named instance.

Nothing is split out of the slice itself, so there is no deferred-work item to file.
