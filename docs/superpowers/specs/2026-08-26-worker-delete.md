# Worker delete: one transaction that ends every generation before it releases the row

- **Date:** 2026-08-26
- **Type:** backend slice (Go + four new SQL statements + `make generate`; **no migration**, no proto,
  no files under `web/`)
- **Closes:** `docs/backlog/bug-2026-08-25-no-worker-delete-at-any-layer.md`
- **Verified against:** worktree `reverent-solomon-87f44d`, branch `claude/pr-merging-session-868949`,
  even with `origin/main` at `02100da`
- **Phase:** 1 (design). Phase 2 writes the plan.
- **Gate mode:** autonomous. Every place the brainstorming flow would have asked a human, the call is
  made here with the reasoning written down; section 16 is the ledger. Where a fork was not resolvable
  by evidence, the more conservative and more reversible arm was taken and labelled as such.

Every claim about current code carries a `file:line` or a symbol. Where a claim could not be
established from the tree it is labelled as an assumption or as unverified.

**Read section 4 before section 5.** The item this spec closes was written by its own author on the
day of the slice that motivated it, with no independent review and no cooling-off, and its severity
argument does not survive. The feature does; the priority does not. Both conclusions are load-bearing.

---

## 1. Problem, restated after verification

Relay can create a `workers` row and can null its credential. It cannot remove one. Every row ever
created is permanent, and because `workers.hostname` is `UNIQUE` (`000001_initial.up.sql:25`) and every
enrollment path keys on that column, a permanent row means a **permanently claimed hostname**.

The three "does not exist" claims in the item are **all confirmed**, at the exact sites it names
(section 2.1). The four FK claims are **all confirmed**, verbatim, and a complement search finds no
fifth database relation (section 2.2). What does not survive is the severity argument, the claim about
what stale reservation ids do, and the framing of the `agent_enrollments` decision as a choice between
keeping and losing an audit trail (section 3).

The design below is one admin-only HTTP route, one CLI arm, four new SQL statements, and a
documentation sweep. The interesting part is not any of those. It is that the transaction's
**statement order is the whole correctness argument**, and that getting it wrong produces a row that
no statement in the tree can subsequently reach.

---

## 2. What the code actually does, verified at HEAD

### 2.1 The three "does not exist" claims: all three CONFIRMED

| Claim | Verification |
|---|---|
| No `delete` arm in the CLI | `internal/cli/workers.go:52-69`. The switch has arms for `list`, `get`, `disable`, `enable`, `revoke`, `workspaces`, `evict-workspace`, and a `default` returning `unknown workers subcommand: %s`. Usage strings at `:36` and `:45` match. **Confirmed.** |
| No `DELETE FROM workers` in `internal/store/query/` | A search for `DELETE FROM workers` across the tree returns **zero** matches in `internal/store/query/`. The hits are all prose (`README.md:353`, `ROADMAP.md`, three specs, one retro, `internal/agent/messages_test.go:129`, `internal/worker/handler.go:74`) plus `DeleteWorkerWorkspace`, which is a different table. **Confirmed.** |
| No delete route on the resource | `internal/api/server.go:151-191`. The `workers` routes are five `GET`, one `PATCH`, two `POST`, and exactly one `DELETE`: `DELETE /v1/workers/{id}/token` (`:175`), which is `handleDeleteWorkerToken` (`internal/api/agent_enrollments.go:227-243`) and is revoke. There is no `handleDeleteWorker`. **Confirmed.** |

### 2.2 Every relation that references `workers`, searched as a complement

The item lists four and asserts nothing about the complement. That shape is the project's own recorded
trap ("a uniqueness claim is a claim about the complement"), so the search was run over the *shape*
rather than over the item's list: `REFERENCES workers`, `workers(id)`, `worker_id` and `worker_ids`
across `internal/store/migrations/`. It returns seven lines, of which three are foreign keys, one is
the bare array, and three are an index and two comments.

| Relation | Declaration | `ON DELETE` | Item's claim |
|---|---|---|---|
| `agent_enrollments.consumed_by` | `000005_agent_auth.up.sql:9` | **none** | CONFIRMED |
| `tasks.worker_id` | `000001_initial.up.sql:62` | `SET NULL` | CONFIRMED |
| `reservations.worker_ids` | `000001_initial.up.sql:89` | n/a, bare `UUID[] NOT NULL DEFAULT '{}'`, no FK | CONFIRMED |
| `worker_workspaces.worker_id` | `000007_workspaces.up.sql:6` | `CASCADE` | CONFIRMED |

**No fifth database relation exists.** The remaining hits are `idx_tasks_worker_active`
(`000018_hot_path_indexes.up.sql:11`) and two comment lines in `000021_tasks_assigned_at.up.sql`.

**There is a fifth and a sixth relation that are not in the database, and the item does not name
them.** Both turn out to be benign, and both need saying because "delete the row" is not the whole of
"remove the worker":

- **`worker.Registry`** (`internal/worker/registry.go`) maps worker id to a live gRPC sender. Its only
  removal method is `UnregisterIf(workerID string, s Sender) bool` (`:38`) - identity-checked by
  design. An HTTP handler holds no `Sender`, so **it structurally cannot unregister a worker**, and
  section 8.3 explains why that is the right answer rather than a limitation to work around.
- **`worker.GraceRegistry`** (`internal/worker/grace.go`) holds requeue timers keyed on worker id and
  epoch. A timer armed for a deleted worker fires `RequeueWorkerTasksIfEpoch`
  (`internal/store/query/tasks.sql:717-730`), whose `EXISTS (SELECT 1 FROM workers w WHERE w.id = $1
  AND w.connection_epoch = $2)` finds no row and moves zero tasks. **Fails closed. No action needed**,
  and section 8.4 records why we do not reach for `GraceRegistry.Cancel`.

### 2.3 What `consumed_by` is for, and who reads it - the fact that decides Decision A

`consumed_by` has **exactly one writer and no production reader.**

- Writer: `ConsumeAgentEnrollment` (`internal/store/query/agent_enrollments.sql:9-12`), called from
  `internal/worker/handler.go:702` with `ConsumedBy: w.ID`.
- Readers: a search for `ConsumedBy` / `consumed_by` across `*.go`, `*.sql`, `*.ts` and `*.tsx`
  returns the write site, `models.go:19`, the generated `SELECT *` column lists, two test files, and
  the seeder script. **`ListActiveAgentEnrollments` and every paginated variant select
  `id, hostname_hint, created_by, created_at, expires_at` and deliberately omit it**
  (`agent_enrollments.sql:14-18`, `:23-29`). No API response carries it. No UI renders it.

So it is a stored forensic value reachable only by an operator with `psql`. That does not make nulling
it free, and it does mean the item's framing - "`SET NULL` (keeps the audit trail, loses the link)"
against "refusing to delete such workers" - offers a distinction that does not exist: **both arms of
Decision A produce the identical row state.** Section 5 re-poses the question as what it actually is.

### 2.4 What the watchdog does with a `running` task whose `worker_id` is NULL - and it already knew

The item says such a task is "orphaned in place ... not cleaned up". **Confirmed, and the tree already
says so in a comment written before the item existed**, which is the single most useful thing found
during verification. `ListOverdueAssignedTasks` (`internal/store/query/tasks.sql:581-608`):

> `worker_id IS NOT NULL` is not decoration. `UpdateTaskStatus`'s worker predicate is a plain `=`, so
> a row with a NULL `worker_id` can never be written by it; selecting such a row would buy a
> guaranteed zero-row round trip every sweep. It also documents the one state this watchdog cannot
> recover - a `dispatched` row whose `worker_id` was nulled by `workers`' `ON DELETE SET NULL` -
> **which is unreachable today, because nothing in this repo DELETEs a worker.**

That comment is a precondition this slice is about to invalidate. Three consequences, and the third is
the one that shapes the whole design:

1. The watchdog's scan skips the row (`worker_id IS NOT NULL`).
2. `UpdateTaskStatus`, `AppendTaskLog`, `IncrementTaskRetryCount`, `RequeueTask` and `RequeueTaskByID`
   all carry a plain `worker_id = $n`, which NULL never satisfies. Every write path fails closed.
3. **`RequeueWorkerTasks` and `RequeueWorkerTasksIfEpoch` both key on `worker_id = $1`
   (`tasks.sql:714`, `:728`).** So once the FK has nulled the column, *no statement in the tree can
   reach the row by worker*. The task is `running` forever, holds no slot (`CountActiveTasksByAllWorkers`
   filters `worker_id IS NOT NULL`, `:577`), and its job never leaves `running`. Recovering it needs a
   human with the task id.

**That is why statement order in section 6 is the correctness argument and not a style question.**

### 2.5 What `reservations.worker_ids` actually drives - the item's claim REFUTED

The item says stale ids "linger silently and the dispatcher keeps matching against them". The second
half is false, and the direction of the error matters.

`internal/scheduler/dispatch.go:185-191` builds `reservedIDs` from every active reservation's
`WorkerIds`, and `:221-223` is the only consumer:

```
if reservedIDs[uuidStr(w.ID)] {
    continue
}
```

It is an **exclusion** set, iterated over live `workers` rows. An id with no `workers` row is compared
against nothing and matches nothing. UUIDs are `gen_random_uuid()` and are not recycled. So a stale id
is inert on the dispatch path; it cannot cause a task to be sent to, or withheld from, any worker.

Two further facts:

- **`selector` is never read by the dispatcher.** `selectWorker` consults `WorkerIds` only. A
  reservation whose `worker_ids` becomes empty is inert, not selector-driven.
- **The state is already reachable today without any delete.** `handleCreateReservation`
  (`internal/api/reservations.go:265-274`) parses each `worker_id` for UUID *shape* and never checks
  existence. An admin can create a reservation naming a UUID that has never been a worker, right now.

So the cost of a stale id is administrative, not behavioural: `GET /v1/reservations` shows a phantom,
and a reservation silently reserves fewer machines than the admin who wrote it believes. Section 7
still scrubs, for a reason that is not the item's reason.

### 2.6 The verb vocabulary delete would join, stated precisely

The brief describes relay as already having "disable / drain / revoke". Two subcommands and a mode,
in fact - there is no `drain` verb:

| Verb | Surface | What it ends | Row? | Hostname? | Budget? |
|---|---|---|---|---|---|
| `disable` | `POST /v1/workers/{id}/disable` | dispatch eligibility (`dispatch.go:218`) | kept | kept | kept |
| `disable --requeue` | same, `?requeue=true` | eligibility **and** current assignments | kept | kept | kept |
| `enable` | `POST /v1/workers/{id}/enable` | (undoes disable) | - | - | - |
| `revoke` | `DELETE /v1/workers/{id}/token` | the credential (`ClearWorkerAgentToken`) | kept | **kept** | freed |
| **`delete`** | proposed | the identity | **removed** | **freed** | freed only if it was non-revoked |

The item's core claim - **only delete frees the hostname** - is confirmed by construction: `revoke`
runs `UPDATE workers SET agent_token_hash = NULL, status = 'revoked', revoked_at = NOW()`
(`workers.sql:116-119`) and touches no key column, while `InsertWorkerForAutoEnroll` conflicts on
`(hostname)` whatever the status (`workers.sql:93-96`). Nothing else can free it.

`handleDisableWorker` (`internal/api/workers.go:424-524`) is the closest structural precedent in the
tree and this design copies its shape deliberately: `parseUUID` -> `GetWorker` -> `s.pool.Begin` ->
`:execrows` check-and-set -> `RequeueWorkerTasks` -> `NotifyTaskSubmitted` -> commit ->
`sendCancelSignals` -> re-read -> respond with a count.

---

## 3. Discrepancies between the item and HEAD

Most important first.

**R1. REFUTED, and it is the reason section 4 downgrades the item: the severity argument is
self-refuting.** The item's terminal case is "a machine re-provisioned in place, under auto-enroll,
whose operator will not or cannot have an enrollment token issued, with **no remedy at all**". The
remedy it proposes for that operator is a worker delete. **Every mutating worker route in relay is
admin-only** (`server.go:155, :175-178, :190-191`), and this one must be too - it is the most
destructive of them. So the proposed fix requires *exactly the admin the premise says is
unavailable*. An operator who cannot get `relay agent enroll` run for them cannot get
`relay workers delete` run for them either. "No remedy at all" is really **"no remedy that avoids an
admin"**, and the fix does not avoid an admin. Those two statements justify very different
priorities.

**R2. REFUTED, narrowly but concretely: a remedy the item does not count exists today for its own
motivating case.** An operator with database access can run `DELETE FROM workers WHERE hostname = ...`.
For a *token-enrolled* worker that fails on `agent_enrollments.consumed_by` (which is the item's own
blocker, so the item is right about the general case). But the item's motivating case is a machine
**auto-enrolled under `RELAY_ALLOW_AUTO_ENROLL`**, and an auto-enrolled worker has consumed no
enrollment token, so `consumed_by` names it nowhere and the raw `DELETE` succeeds. It is unsupported,
out-of-band, orphans any live task (section 2.4), and is not an argument against building the feature.
It is an argument against "no remedy at all".

**R3. REFUTED: "the dispatcher keeps matching against" stale reservation ids.** Section 2.5. The
`reservedIDs` map is an exclusion set iterated over live rows; a dangling id matches nothing. The real
cost is that `GET /v1/reservations` shows a phantom and the reservation covers fewer machines than
intended. Also, the state is already creatable through the public API today, so delete introduces no
new state class.

**R4. RESHAPED: Decision A is not a choice between keeping and losing an audit trail.** Both arms the
item names produce `consumed_by IS NULL` on the same row. And `consumed_by` has no production reader
(section 2.3), so "the audit trail" is a `psql`-only value. The question that actually differs between
the arms is **what the schema does for the next caller who deletes a worker without reading this
spec**, and section 5 answers that one.

**R5. CONFIRMED and load-bearing, and the tree said it first: a delete that runs before the requeue
strands the task permanently.** `ListOverdueAssignedTasks`'s comment (`tasks.sql:603-608`) names this
exact state as "the one state this watchdog cannot recover" and justifies its unreachability with
"nothing in this repo DELETEs a worker". Section 2.4 walks the five statements that all fail closed on
a NULL `worker_id`. The item says "orphaned in place ... not cleaned up", which is true but
understates it: the row is unreachable by worker id from every recovery path in the tree.

**R6. CONFIRMED: `worker_workspaces` `ON DELETE CASCADE` "is fine".** The rows are a server-side
mirror of agent-side inventory, rebuilt by `applyInventoryUpdate` on the next connect, and a deleted
worker has no next connect. Nothing else joins the table. **Confirmed with no caveat.**

**R7. NEW: the item's acceptance criterion 1 is stronger than the design should promise, and one word
is why.** It asks that after a delete "the hostname is **immediately** re-usable by token-less
auto-enroll". True at the database (the unique index frees on commit), but the *observable* behaviour
depends on the deleted machine's own agent, which exits on `Unauthenticated`
(`internal/agent/agent.go:108`) and must be restarted. The criterion should be met at the level it can
be tested at: a subsequent `InsertWorkerForAutoEnroll` for that hostname returns a row instead of
`pgx.ErrNoRows`. Section 13's T-E1 is written that way.

**R8. NEW: `resolveWorkerID` cannot resolve the hostname of a revoked worker, which is the worker most
likely to be deleted.** `internal/cli/workers.go:165-179` resolves a non-UUID argument by listing
`GET /v1/workers`, and every paginated variant of that endpoint carries `WHERE status != 'revoked'`
(`workers.sql:125-131`, `:170-249`). So `relay workers delete render-07` fails with
`no worker found with hostname "render-07"` for exactly the rows an operator wants gone. Section 8.5
makes fixing this required scope. The same wart exists on `relay workers revoke <hostname>` today for
an already-revoked worker; that is pre-existing, harmless (revoke is idempotent in intent), and not
fixed here beyond what the shared helper change gives for free.

**R9. NEW: the delete transaction would take `agent_enrollments` before `workers`, which is the
opposite order from both enrollment transactions.** `enrollAndRegister` takes the worker row (via
`UpsertWorkerByHostname`, and since the 2026-08-25 slice also `GetWorkerByHostnameForUpdate`) and then
updates `agent_enrollments` via `ConsumeAgentEnrollment` (`handler.go:490-525`). A delete that nulls
`consumed_by` before deleting the worker inverts that. The cycle is not constructible in practice -
`ConsumeAgentEnrollment` predicates on `consumed_at IS NULL` and so can only lock an *unconsumed* row,
while the delete's statement locks only rows where `consumed_by = $1`, which are consumed - but
"not constructible" is a three-paragraph argument that a future statement can silently break. Section
6.3 makes it a one-sentence argument instead by taking the worker row first.

**R10. NEW: the item's Acceptance says the ghost-command guard lives in
`internal/agent/messages_test.go`. It moved.** The guard is
`TestOperatorMessages_OnlyPrescribeCommandsThatExist` in
`internal/agent/cli_commands_exist_test.go:146-180`. `messages_test.go:148-152` retains a three-string
deny-list (`"workers delete"`, `"relay workers rm"`, `"workers remove"`) that its own comment
describes as "NOT what holds the property". **That deny-list is the one thing in the tree that goes
RED when this slice succeeds**, and section 12.4 handles it explicitly rather than letting the
implementer discover it.

---

## 4. Severity, threat model, and the priority call

### 4.1 The priority call: the HIGH does not stand, and the reason is R1

**Recommendation: downgrade `bug-2026-08-25-no-worker-delete-at-any-layer` from `high` to `medium`,
correct its Summary, and keep it at the top of ROADMAP "Now".** Those are compatible: the roadmap
orders by readiness times value, not by severity alone, and this is a small, fully-specified slice
that unblocks another item. What is wrong is the *stated reason* for the HIGH, and a wrong reason in a
backlog item is a defect on this project by its own standard.

The elimination, run against the item's own claim:

| Is there ... | Answer |
|---|---|
| data loss? | No. |
| a new security exposure? | No. Delete removes a capability gap; it grants an admin nothing an admin lacks. |
| an operator state with no remedy? | **No.** With an admin: revoke-then-enrollment-token works (`SetWorkerAgentToken` at `workers.sql:98-114` revives, and `enrollAndRegister` admits a NULL hash - the 2026-08-25 slice's Decision 5.4). Renaming the host works (README:369). Without an admin: delete does not help either, because delete is admin-only (R1). |
| a blocked roadmap item? | **Yes.** `idea-2026-08-25-ttl-reaper-for-never-reconnected-workers` says delete "blocks the delete arm, not the revoke arm". |
| a documented treadmill? | **Yes.** README:414-421 already states that under an active attacker the revoke remedy trades a bounded count for an unbounded table, and that "**Nothing reclaims either the row or the hostname**". |

The last two are the honest case for building this, and they are a medium-priority case: a missing
cleanup primitive whose absence is documented, bounded, and has admin-available workarounds.

**Say this on the item's Resolution note rather than closing it silently.** The correction is the
useful artifact: an item written in the flush of discovery overstated its severity by proposing a fix
that requires the actor its premise removes.

### 4.2 Threat model of the new surface

**Principal.** An authenticated admin (`auth(admin(...))`). Everything below is about limiting the
blast radius of a *legitimate* admin action, not about an attacker.

**What delete grants that revoke does not:** the ability to free a hostname, and the ability to
destroy a row. Both are irreversible and neither is undoable from the product. There is no audit log
in relay (`feature-2026-06-26-audit-log-admin-console-actions` is open), so the response body and one
server log line are the entire record. Section 6.4 treats that as a requirement rather than as
observability garnish.

**What delete must not become:** a way to take a task away from a *live* agent. Deleting an online
worker's row leaves the agent connected, holding subprocesses, with its assignments requeued and
possibly re-dispatched to a second machine - the same double-execution shape the epoch fence exists to
prevent, arrived at from the coordinator side. Section 8.1 refuses it.

**What delete does to a still-valid agent credential:** the agent's stored token stops working at its
next connect, because `GetWorkerByAgentTokenHash` (`workers.sql:121-123`) finds no row and
`reconnectAndRegister` returns `Unauthenticated`, on which the agent exits (`agent.go:108`). Fails
closed, no new code, and it is the correct outcome: the credential dies with the identity.

---

## 5. Decision A - `agent_enrollments.consumed_by`: null it in the transaction, no migration

**Decision: a new statement, `ClearEnrollmentConsumerForWorker`, run inside the delete transaction.
The foreign key keeps its current no-action behaviour and no migration is written.**

Per R4, the question is not "keep or lose the audit link" - both arms lose it identically. The question
is which arm is safer for the code that does not exist yet. Four reasons, strongest first:

1. **A no-action FK fails CLOSED for every future deleter; `ON DELETE SET NULL` fails SILENT.**
   `idea-2026-08-25-ttl-reaper-for-never-reconnected-workers` explicitly plans a delete arm and is
   already in the backlog. If this slice alters the FK, that reaper - and anything else - silently
   shreds enrollment links with no statement naming the act and no reviewer prompted to think about
   it. If the FK stays as it is, a reaper written without reading this spec gets a loud SQLSTATE 23503
   and has to come find out why. **This is "added a property, forgot its guard" declined in advance:
   the slice deliberately does not give the schema a new job.**
2. **It is reversible at PR granularity.** A statement is a diff. A migration is a deployed schema
   change plus a down migration, and `000005`'s FK would then differ from what every reader of that
   file has assumed since it was written.
3. **It is explicit where it happens.** The delete transaction lists, in order, everything it
   destroys. A reader of the handler can see that the enrollment link is one of them. An `ON DELETE`
   clause is invisible at the call site, which on this project is the dominant defect shape.
4. **The cost the migration would buy is not measurable.** `consumed_by` has no production reader
   (section 2.3), so the conservative arm is also the cheap one. That is unusual and is worth naming:
   normally conservatism costs something.

**The counter-argument, stated rather than skipped.** An explicit statement is an obligation a second
delete path can forget, whereas a migration is automatic. True - and forgetting the statement fails
loudly with an FK violation, which is precisely the property reason 1 wants. The failure modes are not
symmetric.

**Rejected: refusing to delete a token-enrolled worker.** The item's other arm. It refuses the
majority of real workers (every machine that ever used `relay agent enroll`) and directly contradicts
the item's own acceptance criterion 2 ("must not make the delete fail for a token-enrolled worker").

**Rejected: preserving the link by denormalising the hostname into `agent_enrollments` at consume
time.** This is the only option that genuinely keeps the audit trail, and it is a migration plus a
change to `ConsumeAgentEnrollment` on the authenticated enrollment path - a security-relevant write
path - to preserve a value nothing reads. Out of scope; proposed as a follow-on item (section 17.2) so
the option is on the record rather than lost.

**The statement.** `:execrows`, so the count is reportable and testable:

```
-- name: ClearEnrollmentConsumerForWorker :execrows
UPDATE agent_enrollments SET consumed_by = NULL WHERE consumed_by = $1;
```

Its doc comment must carry: that the FK is deliberately left with no `ON DELETE` action; that this is
the only statement permitted to satisfy it; and that a future deleter hitting SQLSTATE 23503 has found
the guard working rather than a bug.

---

## 6. Decision B - live tasks: requeue unconditionally, and requeue FIRST

### 6.1 Requeue, not refuse, and not a flag

**Decision: the delete transaction always calls `RequeueWorkerTasks(id)`. There is no `?requeue=`
query parameter and no drain mode.**

`handleDisableWorker` offers the choice because disable has a genuine non-destructive alternative:
leave the tasks running on a worker that keeps existing. **Delete has no such alternative.** The row is
about to cease to exist; the FK will end every assignment regardless. The only question is whether
that ending is fenced and recoverable, or silent and unrecoverable (section 2.4). So "do not requeue"
is not a mode an operator could sensibly want, and a flag that can be omitted is a flag that will be.

Failing the tasks instead was considered and rejected in section 14.

Refusing while tasks are live was considered and rejected: it makes delete unusable for the exact case
that motivates it, since a worker with a stuck `running` task can never go quiet on its own. What
*does* get refused is a **connected** worker (section 8.1), which is the condition that actually
distinguishes "this machine is gone" from "this machine is working".

### 6.2 The order is the correctness argument

`RequeueWorkerTasks` (`tasks.sql:702-715`) sets `status = 'pending'`, nulls `worker_id`, `assigned_at`
and `started_at`, and **bumps `assignment_epoch`**, on `WHERE worker_id = $1 AND status IN
('dispatched','running')`.

**It must run before the `DELETE`.** If the delete runs first, the FK's `ON DELETE SET NULL` nulls
`worker_id` with no epoch bump and no status change, and the row becomes unreachable by every
worker-keyed statement in the tree (section 2.4, R5). Then `RequeueWorkerTasks` matches zero rows and
returns an empty slice, so the handler would report `"requeued_tasks": 0` and look successful.

This is **exactly** CLAUDE.md's first invariant, in its original wording: *end the generation before
releasing the resource*. The generation here is `assignment_epoch`; the resource is the `workers` row.
Requeue ends every generation while `worker_id` still identifies them; the delete then releases the
row. Reversing the two lets the released resource's own referential action perform a silent, unfenced,
irreversible teardown.

**Take the state and arm its release in the same breath.** Both statements are in one
`s.pool.Begin` transaction, so no early return added later can commit one without the other.

### 6.3 The transaction, in order, with the reason for each position

| # | Statement | Why here |
|---|---|---|
| 1 | `GetWorkerForUpdate(id)` (new) | Locks the worker row **first**, matching both enrollment transactions' lock order (R9), and supplies the 404/409 discrimination inside the transaction so there is no window between the precondition and the delete. |
| 2 | `RequeueWorkerTasks(id)` | Ends every assignment generation while `worker_id` still names them (6.2). Returns the ids, which the response reports. |
| 3 | `RemoveWorkerFromReservations(id)` (new) | Section 7. Before the delete because after it there is no id to scrub by. |
| 4 | `ClearEnrollmentConsumerForWorker(id)` (new) | Section 5. Must precede the delete or the FK fires. |
| 5 | `DeleteWorker(id)` (new), `:execrows` | Releases the resource. Cascades `worker_workspaces`. Its own status allow-list is the control (8.2). |
| 6 | `NotifyTaskSubmitted()` if step 2 requeued anything | Same as `handleDisableWorker:488`. Wakes the dispatcher so requeued tasks are placed promptly. Skipped when nothing moved, to avoid a spurious cycle. |
| 7 | `tx.Commit` | |

`GetWorkerForUpdate` needs a new statement because `GetWorkerByHostnameForUpdate`
(`workers.sql:12-13`) locks by hostname and this path has an id. It is one line:

```
-- name: GetWorkerForUpdate :one
SELECT * FROM workers WHERE id = $1 FOR UPDATE;
```

**After step 7 there are no cancel signals.** `handleDisableWorker` sends them (`workers.go:497-507`)
because a disabled worker is still connected and its agent must be told to kill orphaned subprocesses.
Delete refuses a connected worker (8.1), so by construction there is no agent to tell, and sending
anyway would imply a connection this path exists to forbid. `Registry.SendCancel` on a deleted id
returns an error that the best-effort path would discard, so it would also be untestable. **Stated
here so the asymmetry with the disable precedent reads as a decision, not an omission.**

### 6.4 What the delete records, and why its log site is unbudgeted

Relay has no audit log (`feature-2026-06-26-audit-log-admin-console-actions` is open), so this slice
must decide for itself what survives a delete. **Decision: a response body carrying three counts, and
exactly one unbudgeted server log line on success.**

**The response body.** `200 OK`, not `204`, because a `204` cannot carry the counts and the counts are
the only statement of what was destroyed. It embeds `workerResponse` the way `disableWorkerResponse`
does (`workers.go:37-40`), so the caller gets the deleted row's identity back one last time:

```
type deleteWorkerResponse struct {
    workerResponse                   // the row as it was, read under the FOR UPDATE at step 1
    RequeuedTasks       int `json:"requeued_tasks"`
    ReservationsUpdated int `json:"reservations_updated"`
    EnrollmentsUnlinked int `json:"enrollments_unlinked"`
}
```

All three counts come from `:execrows` or `:many` returns, so each is a real number rather than a
recomputation, and each has a test that goes red if the handler discards it (T-A1, T-B2, T-C1).

**One log line, and the budget question answered rather than skipped.** The 2026-08-25 slice added
**no** log site at all, because its refusal was attacker-reachable pre-authentication and
`bug-2026-08-15-registration-log-sites-are-outside-the-connection-budget` is open. **That reasoning
does not transfer here, and the difference is the same one as in section 11.** This site is reachable
only by an authenticated admin; it fires once per *successful* delete of a row that then ceases to
exist; and an unauthenticated peer cannot drive it at all. Its lifetime volume is bounded by the
number of workers that have ever existed. So: one unconditional `log.Printf` on success naming the
worker id, the hostname (read from the deleted row, never echoed from the request) and the three
counts. **No counter, no new `GET /v1/server/counters` section, no new `logKind`, and no change to the
`ingest_log_budget.counts` JSON contract or its eight-site checklist.**

**No log line on refusal.** A refusal writes nothing, changes nothing, and returns a `409` the caller
reads directly. Logging it would add an admin-driven line with no state change behind it.

---

## 7. Decision C - `reservations.worker_ids`: scrub, for a reason that is not the item's

**Decision: `RemoveWorkerFromReservations`, in the same transaction, using `array_remove`. Report the
row count. Never delete a reservation, even when its array empties.**

Per R3, the item's stated reason is wrong: the dispatcher cannot match a dangling id, and the state is
already creatable through `POST /v1/reservations` today. So this is not a correctness fix and the spec
must not sell it as one.

The reason to do it anyway:

- **Delete's contract is "this id ceases to exist".** The one place in the schema that stores worker
  ids with no referential integrity is the one place that contract can be quietly broken. Leaving it
  is the "a slice gives existing code a new job and pins nothing" shape.
- **It is one statement and it is directly testable**, so the cost of honouring the contract is lower
  than the cost of documenting why we did not.
- **The administrative cost is real even though the behavioural one is not.** A reservation that
  silently covers fewer machines than its admin wrote is a wrong answer to `GET /v1/reservations`, and
  on this project a wrong contract in a response is a defect.

```
-- name: RemoveWorkerFromReservations :execrows
UPDATE reservations
SET worker_ids = array_remove(worker_ids, $1)
WHERE $1 = ANY(worker_ids);
```

The `WHERE` clause is not redundant with `array_remove`: it makes the `:execrows` count mean "how many
reservations named this worker", which is the number the response reports and the number a test
asserts. Without it every reservation in the table is rewritten and the count is meaningless.

**A reservation whose array empties is left alone.** It becomes inert on the dispatch path
(`selector` is never read, section 2.5), and deleting it would be a second destructive act the admin
did not request. **This is a known incompleteness and it goes in README**: after deleting the last
worker a reservation names, that reservation reserves nothing and must be removed or re-pointed by
hand. Proposed as a follow-on (17.3) rather than absorbed.

**No FK is added.** Adding one would be a migration on a column whose whole current semantics is that
it has none, would retroactively invalidate any existing dangling row, and would need a decision about
`ON DELETE` behaviour for array *elements*, which Postgres does not offer.

---

## 8. Decision D - what guards the delete

### 8.1 Refuse while the worker is connected, as an allow-list

**Decision: delete is permitted only when `workers.status IN ('offline','revoked')`. Any other status
is refused with `409 Conflict`.**

The worker status vocabulary is exactly `('online','offline','stale','revoked')`, enforced by
`workers_status_check` (`000019_status_vocabulary_checks.up.sql:8-10`) and asserted by
`internal/store/status_vocabulary_constraints_test.go:45,84`. `online` and `stale` both mean
*connected* - `dispatch.go:210-215` documents that a stale worker "is still connected and able to run
tasks; the status only signals missing telemetry". So the permitted set is exactly the not-connected
set.

**It is written as an allow-list on purpose, and this is CLAUDE.md's rule applied to a second table.**
The equivalent deny-list (`status != 'online' AND status != 'stale'`, or the tempting
`status != 'online'` alone) is interchangeable against today's vocabulary and **fails open on the next
status added** - a future `quarantined` or `draining` worker would become deletable while connected,
silently. The allow-list fails closed: a new status is undeletable until somebody decides it should
be. This is the same reasoning that removed `if existing.Status == "revoked"` from
`autoEnrollAndRegister` on 2026-08-25 (`handler.go:59-66`), one table over.

`disabled` is deliberately absent from the discussion: it is an overlay column (`disabled_at`)
synthesised into the API's `status` by `toWorkerResponse` (`workers.go:48-57`), not a database status.
A disabled worker is still `online` or `offline` underneath, and delete keys on the underlying value.
**A disabled-and-connected worker is therefore refused**, which is correct - disable does not close
the stream (README:353).

**Not required: revoke-first.** It adds no safety over the connected check (a revoked worker is not
necessarily disconnected; `ClearWorkerAgentToken` does not close the stream, README:353), and it makes
delete a two-step whose first step's whole purpose is to *keep* the row. Deleting a non-revoked
offline worker is safe because the agent's credential dies with the row (section 4.2).

### 8.2 The status predicate lives in SQL; the Go check is a second question

**Decision: `DeleteWorker` carries the allow-list in its own `WHERE`, and the handler additionally
reads it off the locked row for the error code.**

```
-- name: DeleteWorker :execrows
DELETE FROM workers WHERE id = $1 AND status IN ('offline','revoked');
```

Both are wanted and neither is redundant, in the exact shape CLAUDE.md describes for
`handleTaskStatus`'s Go identity gate: **the SQL predicate is the control**, and the Go check is a
second question plus a better error. Because step 1 takes `FOR UPDATE`, the two cannot disagree within
one transaction - the SQL arm is defence for a future caller who writes a second delete path and skips
the lock. The Go arm is what turns a zero-row delete into `409` with a message an operator can act on
instead of a bare failure.

**`:execrows`, and the zero case is handled, not assumed.** `n == 0` after a `FOR UPDATE` read that
said the status was permitted means something is wrong (a concurrent delete, most plausibly). Roll
back and return `409`, never `204`. `markWorkerOffline`'s lesson applies: keep "the fence said no"
distinguishable from "the query failed".

### 8.3 What the delete does NOT do: touch the registry

**Decision: the handler never calls into `worker.Registry` or `worker.GraceRegistry`.**

`Registry.UnregisterIf(workerID, s Sender)` (`registry.go:38`) requires the caller to present the
sender it believes it owns. An HTTP handler owns none. **This is identity-checked teardown, and the
invariant's own escape clause applies verbatim: where there is no identity to check, say so and name
what replaces it.** What replaces it here is the connected-status refusal of 8.1 - the delete only
proceeds for a worker that has no live registration to tear down. Reaching for a registry call to
"clean up" would be the clobber the invariant forbids: it could unregister a *fresh* connection that
raced in, which is the finishRegister strand (PR #146) rebuilt on the HTTP side.

The residual race - a registration commits between step 1's `FOR UPDATE` and the response - is closed
by the lock: `RegisterWorkerConnection` is an `UPDATE ... WHERE id = $1` and blocks on our row lock
until we commit or roll back. If it wins the race and goes first, our step 1 reads `online` and we
refuse. Either way the outcome is coherent.

### 8.4 What the delete does NOT do: cancel the grace timer

A `GraceRegistry` timer armed for the deleted worker will fire and find nothing
(`RequeueWorkerTasksIfEpoch`'s `EXISTS` guard, section 2.2). Calling `Cancel` would be tidier and
would need `api.Server` to hold a `*worker.GraceRegistry` it does not hold today. The memory is one
timer for at most `RELAY_WORKER_GRACE_WINDOW` (2m default). **Declined, with the reason, rather than
left unexamined.**

### 8.5 The CLI arm

**Decision: `relay workers delete --yes <worker-id-or-hostname>`, with `--yes` required.**

- **`--yes` is a required flag, not an interactive prompt.** `internal/cli` has a `readPasswordFn`
  seam, but every destructive path in the CLI today is flag-driven and non-interactive, and a prompt
  breaks scripted use. Without `--yes` the command prints what it would delete and exits non-zero.
- **Hostname resolution must reach revoked workers (R8).** `resolveWorkerID` lists `GET /v1/workers`,
  which excludes revoked rows, so the workers most likely to be deleted cannot be named by hostname.
  Required scope: the delete path resolves against `GET /v1/workers` and, on a miss, `GET
  /v1/workers/revoked`. Prefer extending `resolveWorkerID` with a fallback so `revoke` gets the same
  fix for free; if that turns out to change `revoke`'s observable behaviour in a way a test pins, add a
  delete-local resolver and file the `revoke` half.
- Usage strings at `workers.go:32`, `:36` and `:45` all list the subcommands and **all three must be
  updated**. They are three separate string literals; changing one is the partial-fix shape.
- Output reports what was destroyed: the requeued task count, the reservation count, the enrollment
  count.

---

## 9. Decision E - the ceiling, and the one thing README must not gain

**Confirmed, exactly as the brief frames it:** `CountWorkers` is
`SELECT COUNT(*) FROM workers WHERE status != 'revoked'` (`workers.sql:150`), so **deleting a revoked
worker changes the count by zero and frees no auto-enroll ceiling budget.** Deleting a *non-revoked*
(offline) worker does decrement it.

No contradiction with README:430-433, which says revoke frees budget - it does, and still does. But
two things must be got right so the docs stay true:

1. **README must not gain a blanket "delete frees budget" claim.** The accurate sentence is that
   delete frees the **hostname** always, and frees **budget** only for a worker that was not already
   revoked. Since the natural operator sequence is revoke-then-later-delete, the common case frees no
   budget at all.
2. **Delete must NOT be added to the numbered ceiling remedy ladder (README:423-438).** That ladder is
   what an operator does in response to `fleet_at_ceiling`, a signal an attacker can drive. README
   already refuses to put "set the ceiling to `0`" in the ladder for exactly this reason, and CLAUDE.md
   records the general rule: an option that disables a control, or that is a treadmill under attack,
   belongs outside the ladder rather than inside as a peer. Deleting 1024 rows under an active attacker
   is the same treadmill as revoking them, and it is more destructive.

Where delete *does* belong in README is the sentence that currently says nothing reclaims the row or
the hostname (README:419-421), and the auto-enrollment recovery paragraph (README:365-369). Section 12
has the exact wording sites.

---

## 10. Invariants: how each is satisfied, or why it is not implicated

| Invariant | Status |
|---|---|
| **End the generation before releasing the resource** | **Directly implicated and it is this design's spine.** `RequeueWorkerTasks` (bump `assignment_epoch`) precedes `DeleteWorker` (release the row) in one transaction. Reversed, the FK's `ON DELETE SET NULL` ends every assignment with no epoch bump, and section 2.4 shows the row is then unreachable by every worker-keyed statement. Read in the ACQUIRE direction: step 1 takes the row lock and step 5 releases it in the same transaction, so no early return can hold one without the other. |
| **Epoch fence** | Satisfied through the *conditional bump* branch: `RequeueWorkerTasks` bumps `assignment_epoch` predicated on `worker_id = $1 AND status IN ('dispatched','running')`, so the bump is predicated on the generation actually being ended, not unconditional. **This slice adds no new writer of `tasks.status` and no new status partition on `tasks`.** The `ON DELETE SET NULL` on `tasks.worker_id` is a write to `tasks` that no fence covers - it does not write `status`, so it is outside the invariant's literal scope, and step 2 is what keeps it from mattering. Note the fail-closed direction: a NULL `worker_id` never satisfies the plain `=` in `UpdateTaskStatus`, `AppendTaskLog`, `IncrementTaskRetryCount`, `RequeueTask` or `RequeueTaskByID`, so a stranded row is inert rather than writable. |
| **Single job-spec pipeline** | Not implicated. No spec ingestion. |
| **One bounded sender per gRPC stream** | Not implicated: this path sends nothing (section 6.3 declines cancels, 8.3 declines registry access). |
| **Identity-checked teardown** | Satisfied by declining to tear anything down. Section 8.3: there is no sender to compare, so the connected-status refusal plus the row lock is what replaces the identity check, and adding a registry call to look symmetric would itself be the clobber. |
| **No interior pointers across locks** | Not implicated. |
| **Single JSON entry point** | Satisfied trivially and deliberately: `DELETE /v1/workers/{id}` has **no request body**, so `readJSON` is not called. There is also no query parameter (section 6.1 removed the need for one), so nothing is parsed off the URL beyond the path id via `parseUUID`. |
| **Status predicates as allow-lists** | Satisfied and extended to a new table: section 8.1. `('offline','revoked')` on `workers`, not the deny-list. `tasks`' allow-lists are untouched - `RequeueWorkerTasks` is reused byte-identical. |

**Two carve-outs that this slice must NOT touch, named so a reviewer can confirm they were
considered.** `AppendTaskLog`'s disjunction and `ListOverdueAssignedTasks`'s partition are the two
status predicates that must be read backwards. Neither changes here: no new task status is introduced
and no existing one moves. What *does* change is `ListOverdueAssignedTasks`'s **doc comment**, which
asserts the strand state is "unreachable today, because nothing in this repo DELETEs a worker"
(`tasks.sql:606-608`). That sentence becomes false the moment this slice merges. Section 12.5.

---

## 11. Does the `msgAuthFailed` rule transfer to this surface? No, and here is the boundary

The registration surface now funnels eleven `codes.Unauthenticated` sites through one `msgAuthFailed`
constant (`internal/worker/handler.go:57`), pinned by
`TestRegistrationRefusals_AllUseTheSharedConstant` in `internal/worker/refusal_string_guard_test.go`.
The question is whether `DELETE /v1/workers/{id}` inherits it.

**It does not, and the boundary is who is asking.**

- The gRPC registration surface is reachable **pre-authentication**, by any peer that can open a
  stream. Distinguishable refusals there are a hostname-state oracle for an anonymous caller, which is
  the entire content of the 2026-08-25 slice's Decision 5.3.
- `DELETE /v1/workers/{id}` sits behind `auth(admin(...))`. The caller has already proven admin, and
  an admin can `GET /v1/workers`, `GET /v1/workers/revoked` and `GET /v1/workers/{id}`. **A 404 versus
  a 409 discloses strictly nothing that two GETs do not.** Collapsing them would cost an operator a
  usable error and buy no confidentiality.
- The existing convention on this surface is already distinguishable and should be matched:
  `handleDeleteWorkerToken` returns `404 "worker not found"` (`agent_enrollments.go:238-240`),
  `handleDisableWorker` and `handleEnableWorker` both return `404 "worker not found"`
  (`workers.go:435-439`, `:534-540`), and `handleListRevokedWorkers` returns a specific `400`
  (`workers.go:308-311`).

**So: `400` invalid id, `404` not found, `409` connected, `200` with a body on success.** State the
transfer question and its answer in the handler's doc comment, because "the eleven-site rule applies
everywhere" is exactly the kind of over-generalisation a future reviewer will assume.

**One thing does transfer: the response must not echo a caller-controlled value into an error
string.** `registrationStoreFault` (`handler.go:88`) exists because store errors carried caller input
back to the peer. The delete handler's error strings are fixed literals; the hostname it logs
server-side comes from the database row, not from the request (section 6.4).

---

## 12. Prose sites, enumerated with their current wording

Wrong prose is this project's dominant defect class and "update the docs" gets partially done. Every
site below currently asserts that relay has no worker-delete. Each is listed with enough of its
present wording to grep for.

### 12.1 `README.md:353` - the revocation paragraph

Currently ends with a parenthetical correcting an earlier error:

> (This used to say "disable or delete the worker ... deleting the worker row destroys its assignments
> and reservations". Both halves were wrong and are corrected here: **relay has no worker-delete** - no
> CLI subcommand, no `DELETE FROM workers` query, and no DELETE route on the resource - and were one
> added, `tasks.worker_id` is `ON DELETE SET NULL` so running tasks would be orphaned rather than
> destroyed, `reservations.worker_ids` is a bare `UUID[]` with no foreign key so reservations would be
> untouched, and `agent_enrollments.consumed_by` has no `ON DELETE` action at all, so the delete would
> fail outright for any worker that was ever enrolled with a token.)

**All four of its factual claims are about to become false.** Rewrite as a statement of what delete now
does: requeues assignments (does not orphan), scrubs reservations (does not leave them untouched),
nulls the enrollment link (does not fail), and refuses a connected worker. Do not simply delete the
parenthetical - it is the only place README explains why the three relations matter.

### 12.2 `README.md:365-369` - the auto-enrollment recovery paragraph

> ... auto-enrollment under `RELAY_ALLOW_AUTO_ENROLL` does not revive a revoked worker - it stays
> revoked **until an admin clears or deletes it**. (Because identity is keyed by hostname, a renamed
> host can still rejoin as a new worker.)

"or deletes it" was aspirational and becomes true. Add the third route explicitly: delete the worker,
which frees the hostname for token-less auto-enroll. This is the item's acceptance criterion.

### 12.3 `README.md:371-378` and `README.md:414-421` - the two "no worker-delete" assertions

- `:377-378`: "relay has no worker-delete, so the revoked row would block its own recovery
  permanently." The *asymmetry* it justifies (enrollment tokens bind a NULL-hash row, auto-enroll does
  not) is still correct and must survive the edit; only its supporting clause changes.
- `:419-421`: "**Nothing reclaims either the row or the hostname** - relay has no worker-delete at any
  layer, so revoked junk rows are permanent. Bounding the table itself, and reaping those rows, is not
  something this ceiling does". Delete now reclaims both, **manually and per row**. Reaping is still
  not done. Say both halves; do not let the correction imply the reaper landed.
- `README.md:430-433` (ladder step 1): "`relay workers revoke <id>` ... **is the only cleanup relay
  has - there is no worker-delete**". Correct the clause. **Do not add delete as a ladder step**
  (section 9).
- **Also add `relay workers delete` to README's CLI reference**, wherever `relay workers revoke` is
  documented. This site is not in the item's list and is the one most likely to be missed.

### 12.4 `internal/agent/messages.go:30-44` - the terminal exit message, and the guard behind it

The token-less arm currently says:

> ... Failing that, rename the host: identity is keyed by hostname, so a renamed machine rejoins as a
> new worker. **Relay has no command that frees a claimed hostname for token-less enrollment, so if no
> admin will issue a token there is no remedy on this path;** ...

That sentence becomes false, and it is the sentence the whole backlog item grew out of. Replace it
with the real command. Two hard constraints:

1. **`TestOperatorMessages_OnlyPrescribeCommandsThatExist`
   (`internal/agent/cli_commands_exist_test.go:146-180`) must go green because the command exists.**
   It parses `internal/cli/*.go` for `Command{Name: ...}` literals and `case "..."` clauses and
   requires every `relay <cmd> <sub>` in all four operator messages to resolve. Adding
   `case "delete":` to `doWorkers`'s switch is what makes `relay workers delete` resolvable.
   **This is the acceptance criterion that closes the loop the item came from, and the plan must order
   the CLI arm before the message edit** so the guard is never transiently red for the wrong reason.
2. **`messages_test.go:148-152` will go RED and must be edited, not deleted.** It asserts
   `NotContains(msg, "workers delete")`. Its own comment says it is a deny-list that "is NOT what
   holds the property" and survives only for a sharper message on three known spellings. Now that the
   command exists, the correct edit is to **remove the `"workers delete"` entry** and keep
   `"relay workers rm"` and `"workers remove"`, with a comment recording that the first graduated from
   ghost to real command on this date. Deleting the whole test would remove the sharper message;
   keeping it unchanged would forbid the true remedy. The want-list in
   `TestAuthFailureMessage_TokenlessArmNamesAllThreeCausesAndBothRemedies` also needs a `"delete"`
   entry so remedy 3 is pinned positively rather than merely permitted.

Note the honest reading, per "an item naming a fix pre-commits the reader": what was wrong on
2026-08-25 was the *advertisement*, and the fix chosen then was to correct the advertisement. This
slice does the other repair - build the thing - and both were legitimate. The message must not now
swing to naming delete as the *first* remedy: revoke-then-enrollment-token stays remedy 1 (it is
non-destructive and keeps history), rename stays remedy 2, delete becomes remedy 3.

### 12.5 `internal/store/query/tasks.sql:603-608` - the watchdog comment

> It also documents the one state this watchdog cannot recover - a `dispatched` row whose `worker_id`
> was nulled by `workers`' `ON DELETE SET NULL` - **which is unreachable today, because nothing in
> this repo DELETEs a worker.**

Becomes false. Rewrite to name `DeleteWorker` and state that the strand is prevented by ordering
(`RequeueWorkerTasks` runs first in the same transaction) rather than by unreachability. **Because this
is a query-comment edit, CLAUDE.md's recorded lesson applies: after `make generate`, verify the
regenerated `tasks.sql.go` doc comment actually changed, since the CRLF revert can silently discard
it.**

### 12.6 `internal/worker/handler.go:71-78` - `errCredentialLive`'s comment

> ... refusing every existing row here would make the revoked row block its own recovery and leave NO
> ROUTE AT ALL: **relay has no worker-delete - no CLI subcommand, no `DELETE FROM workers`, no DELETE
> route on the resource** - so a revoked row that could not be re-enrolled by token would be
> permanently stuck.

The *decision* it documents is unchanged and correct; its supporting fact is not. Rewrite so the
argument stands on "the enrollment-token path is the non-destructive recovery" rather than on "delete
does not exist".

### 12.7 `internal/store/query/workers.sql:133-150` - `CountWorkers`'s comment

> THE `status != 'revoked'` EXCLUSION IS LOAD-BEARING FOR CALLER 2 ... **revoke is the ONLY cleanup
> relay has - there is no worker-delete at any layer, so a revoked row is permanent** - which is why
> it is the first remedy an operator at the ceiling is told to try.

Correct the clause and keep the conclusion: revoke stays the first ladder remedy, for the reason in
section 9. Same `make generate` verification note as 12.5.

### 12.8 Sites deliberately NOT edited

`docs/retros/2026-08-25-auto-enroll-guards.md`,
`docs/superpowers/specs/2026-08-25-auto-enroll-guards.md` and
`docs/superpowers/specs/2026-08-20-coordinator-stale-task-watchdog.md:304` all state the no-delete
fact. **They are dated records of what was true when written and must not be rewritten.** ROADMAP.md's
entry is refreshed by the roadmap skill, not by hand.

---

## 13. Test strategy and the mutation battery

A green test can be vacuous, so each test names its RED, and each carries an explicit vacuity
question - what would make it pass while the code is wrong.

### 13.1 The lane split, decided up front

The database work is the content of this slice and it cannot be exercised by a fake: `array_remove`,
an FK's no-action behaviour, `ON DELETE CASCADE`, and a status predicate are Postgres semantics.
**The load-bearing tests are integration-lane** (`//go:build integration`, real Postgres via
testcontainers). CI runs only the default lane, so this is a known coverage gap and it must be
disclosed rather than papered over with fakes that would assert nothing about SQL.

What the default lane can and should carry: the CLI arm (`internal/cli`) and the message edits
(`internal/agent`, where `TestOperatorMessages_OnlyPrescribeCommandsThatExist` already lives). **Whether
the HTTP status-code mapping (T-D3) can also reach the default lane is a plan-time question**: it
depends on whether `internal/api`'s existing handler tests have a seam that stubs the store without a
database. The plan must look and record the answer, not assume one; if there is no seam, T-D3 is
integration-lane and the CI gap widens by one test.

### 13.2 The tests

**T-A1. `TestDeleteWorker_SucceedsForATokenEnrolledWorker`** (integration). Enroll a worker with a real
enrollment token so `agent_enrollments.consumed_by` names it; delete it; assert `200`, the row is gone,
the enrollment row still exists with `consumed_at` intact and `consumed_by IS NULL`, and the response
reports `enrollments_unlinked: 1`.
**RED at HEAD:** no route. Once the route exists, dropping `ClearEnrollmentConsumerForWorker` reddens
it with SQLSTATE 23503.
**Vacuity:** passes trivially if the fixture never consumed a token. **Assert `consumed_by` is non-NULL
before the delete** - that pre-assertion is the whole discriminator, and without it this is the
generic delete test.

**T-B1. `TestDeleteWorker_RequeuesLiveTasksBeforeReleasingTheRow`** (integration). Give an **offline**
worker one `dispatched` and one `running` task, record their `assignment_epoch`s, delete. Assert for
each task: `status = 'pending'`, `worker_id IS NULL`, `assigned_at IS NULL`, `started_at IS NULL`, and
**`assignment_epoch` is exactly one greater than before**.
**RED at HEAD:** no route.
**Vacuity, and this is the important one:** `worker_id IS NULL` alone passes against a build with **no
requeue at all**, because the FK nulls it. The epoch increment and `status = 'pending'` are the only
assertions the FK cannot produce. This is precisely "a test can be green because of the bug".

**T-B2. `TestDeleteWorker_ReportsTheRequeuedCount`** (integration). Assert the response body's
`requeued_tasks` equals the number of live tasks. Distinguishes "requeued" from "requeued and
reported"; without it a handler that discards `RequeueWorkerTasks`'s return still passes T-B1.

**T-C1. `TestDeleteWorker_RemovesTheIdFromReservationsThatNameIt`** (integration). Three reservations:
one naming only this worker, one naming this worker plus another, one naming neither. Assert the first
is now empty, the second retains exactly the other id, the third is byte-identical, and the response
reports `reservations_updated: 2`.
**Vacuity:** a single-reservation fixture passes against `SET worker_ids = '{}'`. The mixed reservation
is what forces `array_remove` semantics, and the untouched third reservation is what makes the
`WHERE $1 = ANY(worker_ids)` clause load-bearing rather than cosmetic. Put the mixed reservation
**first** in the fixture, so an early-exit mutation cannot hide behind a benign row.

**T-C2. `TestDeleteWorker_CascadesWorkerWorkspaces`** (integration). Give the worker two
`worker_workspaces` rows; assert both are gone after the delete. This is the one relation where the
schema does the work, so the test's job is to prove the CASCADE is still there rather than that we
wrote code.

**T-D1. `TestDeleteWorker_RefusesAConnectedWorker`** (integration), table-driven over
`{online, stale}`. Assert `409`, the row still exists, **and its tasks are untouched** (same status,
same `assignment_epoch`). The task assertion is what proves the refusal happened before any write, and
it is the arm that catches a handler that requeues and then discovers it may not delete.
**RED at HEAD:** no route.

**T-D2. `TestDeleteWorker_PermitsExactlyTheDisconnectedStatuses`** (integration), table-driven over all
four values of the vocabulary, asserting `offline` and `revoked` succeed and `online` and `stale`
return `409`. **This is the allow-list test and it must enumerate the whole vocabulary**, so it goes
red if a fifth status is added without a decision - the same job `TestTasksStatusVocabularyIsExactly`
does for `tasks`.
**Vacuity:** testing only `online` passes against the deny-list `status != 'online'`, which is mutation
M5. The `stale` row is what kills it, and it must come first in the table.

**T-D3. `TestDeleteWorker_StatusCodes`** (lane per 13.1). `400` for a non-UUID path value, `404` for a
well-formed UUID with no row, `409` for connected, `200` for success. Asserts the **code**, not the
message.
**Vacuity:** asserting only "not 200" collapses all three failures. Compare the codes.

**T-D4. `TestDeleteWorker_ZeroRowDeleteDoesNotReportSuccess`.** Driving a concurrent delete
deterministically is not worth the flake; this property is asserted by mutation M8 against T-D3's
`409` arm instead. **Named here so the gap is on the record rather than absent.**

**T-E1. `TestDeleteWorker_FreesTheHostnameForTokenlessAutoEnroll`** (integration). **This is the item's
headline acceptance criterion.** Auto-enroll `render-07`; assert a second auto-enroll of `render-07` is
refused; delete the worker; assert a third auto-enroll of `render-07` now **succeeds and returns a
different worker id**.
**RED at HEAD:** no route, so the third step cannot run.
**Vacuity:** the middle refusal is not optional - without it the test passes against a build where
auto-enroll never refused anything, i.e. against a build with the 2026-08-25 guard removed. The
different-id assertion is what proves a new row rather than a revived one. Per R7 this asserts the
database-level property, not "the agent came back".

**T-E2. `TestDeleteWorker_OfARevokedWorkerDoesNotChangeCountWorkers`** (integration). Snapshot
`CountWorkers`, delete a revoked worker, assert unchanged; then delete an offline worker and assert it
decremented. Pins section 9 so README cannot drift into "delete frees budget".

**T-F1. `TestWorkersCommand_DeleteRequiresConfirmation`** (default lane, `internal/cli`). Without
`--yes`, no HTTP request is issued and the command exits non-zero.
**Vacuity:** assert **no request was made**, not just the exit code - an implementation that deletes
and then errors would pass an exit-code-only check.

**T-F2. `TestWorkersCommand_DeleteResolvesARevokedHostname`** (default lane). Serve a revoked worker
only from `/v1/workers/revoked` and assert the DELETE goes to the right id (R8).
**Vacuity:** a fixture serving the worker from `/v1/workers` too passes without the fallback. The
worker must be absent from the primary list.

**T-G1.** `TestOperatorMessages_OnlyPrescribeCommandsThatExist` (existing, default lane) must pass
**byte-identical and unedited** after the message names `relay workers delete`. Its passing is an
acceptance criterion, not a new test. Editing it to accommodate the new command would destroy the
property it exists to hold.

### 13.3 The mutation battery

Per the recorded lessons: run a control that should die first, confirm each mutation actually applied
(CRLF has silently defeated four in a row on this repo), and put any poisoned input **first** in a
table so an early-exit mutation cannot hide behind it.

| # | Mutation | Expected killer | Note |
|---|---|---|---|
| **M0** | Control: `DeleteWorker`'s `WHERE id = $1` -> `WHERE id = $1 AND FALSE` | Every T-* delete test | Must die. If it survives, the harness is broken, not the coverage good. |
| M1 | Remove the `RequeueWorkerTasks` call | T-B1 | Tasks stay `running` with `worker_id` NULL. |
| M2 | Move `RequeueWorkerTasks` **after** `DeleteWorker` | T-B1 | The FK has already nulled `worker_id`, so requeue matches nothing. **Survives an assertion on `worker_id IS NULL` alone.** This mutation is the reason T-B1 asserts the epoch. |
| M3 | Drop `ClearEnrollmentConsumerForWorker` | T-A1 | SQLSTATE 23503. Also proves the FK is genuinely no-action, i.e. that Decision A's premise holds. |
| M4 | `array_remove(worker_ids, $1)` -> `worker_ids` (no-op) | T-C1 | |
| M5 | Delete's status allow-list -> `status != 'online'` | T-D2's `stale` row | The deny-list mutation section 8.1 exists to prevent. |
| M6 | Remove the SQL status predicate, keep the Go check | T-D2 must still pass; a **second** mutation removing the Go check too must kill it | Proves the SQL arm is load-bearing rather than decorative. Per "a control that acquires a new job needs a test with that job as its subject", both arms need their own kill. |
| M7 | Remove the Go status check, keep the SQL predicate | T-D3's `409` arm | The delete refuses either way; only the status **code** changes. If nothing dies, T-D3 is asserting the wrong thing. |
| M8 | `n == 0` from `DeleteWorker` -> return `204` | T-D3 | A no-op reported as success. Stands in for T-D4. |
| M9 | `RemoveWorkerFromReservations`'s `WHERE $1 = ANY(worker_ids)` removed | T-C1's reported count | Every reservation is rewritten and the count becomes the table size. |
| M10 | Remove `case "delete":` from `doWorkers`'s switch | **`TestOperatorMessages_OnlyPrescribeCommandsThatExist`** | **The loop-closing mutation.** It proves the guard goes green *because the command exists*, which is the item's acceptance criterion, and it is the only mutation that spans two packages. |
| M11 | Remove the `--yes` requirement | T-F1 | |
| M12 | `GetWorkerForUpdate` -> `GetWorker` (drop `FOR UPDATE`) | **Nothing.** | **Declared unkillable and stated as such.** No deterministic test can catch it; the lock buys lock-order simplicity (R9) and closes a race that needs concurrent transactions to observe. What stands in: the statement's doc comment, and the plan recording that this mutation was run and survived. **Do not invent a flaky concurrency test to manufacture a kill.** |

**The battery must run in an isolated detached worktree**, never in the shared tree, per the recorded
lesson about sibling agents reading a mutated tree.

---

## 14. Alternatives considered and rejected

- **Migrating `agent_enrollments.consumed_by` to `ON DELETE SET NULL`.** Rejected: identical row
  outcome, but it fails silent for the next deleter where the current no-action FK fails closed, and it
  spends a migration on a column with no production reader (section 5).
- **Refusing to delete a token-enrolled worker.** Rejected: refuses most real workers and contradicts
  the item's own acceptance criterion.
- **Denormalising the consuming worker's hostname into `agent_enrollments`** so the audit link
  survives. The only option that actually preserves the trail; rejected for this slice as a migration
  plus a change to the authenticated enrollment write path to serve a value nothing reads. Proposed as
  an item (17.2).
- **A `?requeue=` flag mirroring disable.** Rejected: delete has no non-destructive alternative, so
  the flag has no sensible `false` value, and an omittable flag whose omission strands rows silently is
  the worst available default (6.1).
- **Failing live tasks instead of requeueing them.** Rejected: the tasks did nothing wrong, the job's
  other tasks would cascade through `FailDependentTasks`, and requeue is what every other
  worker-goes-away path in the tree already does (`RequeueWorkerTasks` has four callers).
- **Refusing while any task is live.** Rejected: unusable for the motivating case, since a task on a
  vanished machine never finishes.
- **Requiring revoke-first.** Rejected: no safety gain over the connected check, and it makes the first
  step of a delete a command whose purpose is to keep the row (8.1).
- **Unregistering from `worker.Registry` in the handler.** Rejected: no sender to identity-check, and
  it would rebuild the finishRegister strand on the HTTP side (8.3).
- **Cancelling the `GraceRegistry` timer.** Rejected: the timer already fails closed and
  `api.Server` does not hold the registry (8.4).
- **Adding a foreign key to `reservations.worker_ids`.** Rejected: Postgres has no per-element
  referential action for array columns, and it would retroactively invalidate rows the API can create
  today (section 7).
- **Deleting reservations that empty.** Rejected: a second destructive act the admin did not request.
  Documented as a limitation and proposed as an item (17.3).
- **Collapsing the delete's error codes behind one opaque message**, by analogy with `msgAuthFailed`.
  Rejected: the caller is an authenticated admin who can already read every fact the codes disclose
  (section 11).
- **`204 No Content`, matching `handleDeleteWorkerToken`.** Rejected: a 204 cannot carry the three
  counts, and with no audit log in the product those counts are the only record of what was destroyed
  (6.4).
- **A per-refusal log line, or a refusal counter with a `GET /v1/server/counters` section.** Rejected:
  the refusal changes no state and the caller reads the `409` directly; a counters section would be
  incidental scope larger than the feature (6.4).
- **An interactive confirmation prompt in the CLI.** Rejected: breaks scripted use, and every
  destructive CLI path in relay today is flag-driven.
- **A delete button in the web SPA.** Out of scope: this slice touches no files under `web/`.
  Proposed as an item (17.1).

---

## 15. Scope

**In scope.**

- Four new statements in `internal/store/query/workers.sql`, `agent_enrollments.sql` and
  `reservations.sql`: `GetWorkerForUpdate`, `DeleteWorker`, `ClearEnrollmentConsumerForWorker`,
  `RemoveWorkerFromReservations`. One `make generate`, with CLAUDE.md's CRLF procedure.
- `handleDeleteWorker` and `deleteWorkerResponse` in `internal/api/workers.go`, routed at
  `internal/api/server.go` as `mux.Handle("DELETE /v1/workers/{id}", auth(admin(...)))`.
- `doWorkersDelete` in `internal/cli/workers.go`, the switch arm, all three usage strings, and the
  `resolveWorkerID` revoked fallback (R8).
- The message edit in `internal/agent/messages.go` and the two test edits in
  `internal/agent/messages_test.go` (12.4).
- Six prose sites: README (four passages plus the CLI reference), `tasks.sql`, `workers.sql`,
  `handler.go` (section 12).
- The tests and mutation battery of section 13.
- The item's Resolution note recording the severity correction (4.1) and the three refuted claims,
  and the `git mv` to `docs/backlog/closed/` via `/backlog close`.

**Out of scope, each with a reason.**

- **No migration.** Decisions A and C both declined one; nothing else needs a schema change.
- **No `web/` changes.** The SPA has no worker-delete affordance and gains none here.
- **No TTL reaper.** Its own item; this slice unblocks its delete arm and nothing more.
- **No audit log.** `feature-2026-06-26-audit-log-admin-console-actions` is open; the response body and
  one log line stand in (6.4).
- **No hostname validation.** `bug-2026-08-25-hostname-is-unvalidated-and-reaches-a-unique-index` is
  open and separate.
- **No change to `revoke`'s idempotency wart** beyond what the shared `resolveWorkerID` fix gives.

**Recommended split: none.** This is one coherent transaction plus its two client surfaces and its
prose. The three sub-decisions (enrollment link, tasks, reservations) share one transaction and cannot
be shipped separately without shipping a delete that strands rows. The only candidate for splitting -
the CLI arm - is the half that closes the ghost-command loop, so splitting it out would leave the
item's acceptance criterion unmet.

---

## 16. Autonomous decision ledger

Gate mode is autonomous. Every fork a human would have been asked about, with the call and its basis:

| # | Question | Call | Basis |
|---|---|---|---|
| 1 | Migration or in-transaction NULL for `consumed_by`? | **In-transaction NULL** | Identical row outcome; no-action FK fails closed for the planned reaper; reversible; no reader to protect (5). |
| 2 | Refuse, requeue, or fail live tasks? | **Requeue, unconditionally, first** | The FK ends the assignment anyway; ordering is what makes it fenced and recoverable (6). |
| 3 | Scrub `reservations.worker_ids`? | **Yes, with `array_remove`** | Not for the item's reason, which is refuted; for the contract that a deleted id ceases to exist (7, R3). |
| 4 | What guards the delete? | **Admin-only, plus `status IN ('offline','revoked')` as an allow-list, plus `--yes`** | Connected means a live agent; an allow-list fails closed on a future status (8). |
| 5 | Require revoke-first? | **No** | No safety gain over the connected check (8.1). |
| 6 | Does the `msgAuthFailed` rule transfer? | **No** | Pre-auth gRPC versus authenticated admin HTTP; an admin can read every disclosed fact via GET (11). |
| 7 | `204` or `200` with a body? Log line or counter? | **`200` with three counts; one unbudgeted success log line; no counter** | No audit log exists, so the counts are the only record; the site is admin-only and fires once per row that then ceases to exist (6.4). |
| 8 | Does the item's HIGH stand? | **No - recommend medium** | Its severity premise removes the actor its own fix requires (4.1, R1). |
| 9 | Add delete to the README ceiling ladder? | **No** | Same treadmill-under-attack reasoning that keeps `ceiling=0` out of it (9). |
| 10 | Kill or accept the `FOR UPDATE` mutation? | **Accept as unkillable, on the record** | Manufacturing a flaky concurrency test to claim a kill is worse than the honest gap (M12). |

Where a fork was close, the more conservative and more reversible arm was taken: 1 (no schema change),
2 (requeue rather than fail), 5 (no extra required step), 9 (no new ladder entry).

---

## 17. Follow-on items proposed (not filed by this document)

Per the TPM boundary, these are proposals for a human to accept, not filed items.

1. **A worker-delete affordance in the SPA**, behind an admin check, with a typed-hostname
   confirmation. The API lands here; the UI does not.
2. **Preserve the enrollment audit link across a worker delete** by recording the consuming hostname on
   `agent_enrollments` at consume time. The option Decision A declined; worth a decision of its own now
   that a delete exists and the link can actually be broken.
3. **A reservation whose `worker_ids` empties is silently inert.** `selector` is never read by the
   dispatcher (2.5), so an emptied reservation reserves nothing and nothing says so. Surface it, or
   make `selector` meaningful, or refuse to empty the last id.
4. **`relay workers revoke <hostname>` cannot resolve an already-revoked worker** (R8). Pre-existing,
   partially fixed here as a side effect if the shared-helper route is taken; file the remainder.
