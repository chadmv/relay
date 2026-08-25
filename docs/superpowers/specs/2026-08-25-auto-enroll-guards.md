# Two guards inside `autoEnrollAndRegister`: enrollment may create a worker, never claim one, and never without a ceiling

- **Date:** 2026-08-25
- **Type:** backend slice (Go + one new SQL statement + `make generate`; no migration, no proto, no files under `web/`)
- **Closes:** `docs/backlog/bug-2026-08-12-auto-enroll-hostname-takeover.md`,
  `docs/backlog/bug-2026-08-21-auto-enroll-worker-row-creation-is-unbounded.md`,
  `docs/backlog/idea-2026-08-25-default-lane-fixture-for-the-enrollment-paths.md` (with one criterion
  reshaped rather than met - section 8.2)
- **Verified against:** worktree `reverent-solomon-87f44d`, branch `claude/pr-merging-session-868949`,
  even with `origin/main` at `5b7b20b` (`1097211` in `git log`)
- **Phase:** 1 (design). Phase 2 writes the plan.
- **Gate mode:** autonomous. Every place the brainstorming flow would have asked a human, the call is
  made here with the reasoning written down; section 14 is the ledger. Where a fork was not resolvable
  by evidence, the more conservative and more reversible arm was taken and labelled as such.

Every claim about current code carries a `file:line` or a symbol. Where a claim could not be
established from the tree it is labelled as an assumption or as unverified.

---

## 1. Problem, restated after verification

Two open bugs, both guards inside `autoEnrollAndRegister` (`internal/worker/handler.go:558-619`),
which the roadmap says ship in one sitting. Both items are detailed and both survive verification in
their headline. Three load-bearing claims do not survive, and two of the three change where the code
goes.

**The headline of item 1 (takeover) is confirmed and is the more serious of the two.** With
`RELAY_ALLOW_AUTO_ENROLL=true`, naming an in-use hostname returns the existing worker's id and
overwrites its `agent_token_hash`. The legitimate agent is locked out at its next reconnect, and the
claimant inherits its registry slot, its assignments and its reservations. There is an integration
test that pins this behaviour as *desirable* today: `TestConnect_AutoEnrollRotatesTokenForExistingHost`
(`internal/worker/handler_auth_test.go:517-562`), whose assertion message reads
"re-enrollment should rotate the agent token". That test is the strongest evidence that the exposure
is real, and it is the first thing this slice has to rewrite.

**The headline of item 2 (unbounded rows) is confirmed**, including its central nuance about where a
fix belongs. Section 3 records the confirmation with the line it was read on.

**What does not survive.** Item 1's proposed predicate is the wrong predicate for the path it puts it
on, and its acceptance criterion 2 asks for a regression. Item 2's four candidate mechanisms are not
four alternatives - three of them are not bounds at all. Sections 3, 5 and 6.

---

## 2. What the code actually does, verified at HEAD

Line numbers in both items have drifted; everything below was resolved by symbol and re-read.

### 2.1 The three registration paths

`authenticateAndRegister` (`internal/worker/handler.go:449-461`) dispatches on the credential oneof:

| Credential | Handler | Creates a `workers` row? |
|---|---|---|
| `EnrollmentToken` | `enrollAndRegister` (`:467-535`) | Yes, via `UpsertWorkerByHostname` (`:493`) |
| `AgentToken` | `reconnectAndRegister` (`:538-553`) | **No.** `GetWorkerByAgentTokenHash` then `finishRegister`; no upsert anywhere |
| none, with `AllowAutoEnroll` | `autoEnrollAndRegister` (`:558-619`) | Yes, via the same `UpsertWorkerByHostname` (`:576`) |
| none, without it | `codes.Unauthenticated, "auto-enroll disabled"` (`:459`) | No |

`autoEnrollAndRegister`'s transaction body, in order (`:565-597`):
`GetWorkerByHostnameForUpdate` -> `if existing.Status == "revoked" { return errWorkerRevoked }` ->
`UpsertWorkerByHostname` -> `SetWorkerAgentToken`. Then, outside the transaction, one audit
`log.Printf` (`:617`) and `finishRegister`.

`enrollAndRegister`'s transaction body (`:490-525`): `UpsertWorkerByHostname` ->
`ConsumeAgentEnrollment` -> `if rows == 0 { return errEnrollmentNotConsumable }` ->
`SetWorkerAgentToken`.

### 2.2 The statements

`internal/store/query/workers.sql`:

- `UpsertWorkerByHostname` (`:56-68`) is `INSERT ... ON CONFLICT (hostname) DO UPDATE ... RETURNING id, ...`.
  It preserves admin-managed fields on conflict and refreshes hardware specs. **Confirmed: naming an
  existing hostname returns the existing row's id.**
- `SetWorkerAgentToken` (`:70-81`) is `UPDATE workers SET agent_token_hash = $2, revoked_at = NULL,
  status = CASE WHEN status = 'revoked' THEN 'offline' ELSE status END WHERE id = $1`. **Confirmed:
  it overwrites any existing hash and revives a revoked worker.**
- `ClearWorkerAgentToken` (`:83-86`) is `UPDATE workers SET agent_token_hash = NULL,
  status = 'revoked', revoked_at = NOW() WHERE id = $1`.
- `GetWorkerByAgentTokenHash` (`:88-90`) is `SELECT * FROM workers WHERE agent_token_hash = $1 AND
  status != 'revoked'`. **Confirmed: overwriting the hash locks the legitimate agent out.**
- `GetWorkerByHostnameForUpdate` (`:12-13`) is `SELECT * FROM workers WHERE hostname = $1 FOR UPDATE`.
  **It has exactly one caller in the tree**, `autoEnrollAndRegister` (`handler.go:568`); a search for
  the symbol across `*.go` returns that call site plus its own generated definition
  (`internal/store/workers.sql.go:253-260`) and nothing else. Recorded as a hit count rather than as a
  uniqueness assertion, per the project's rule that a uniqueness claim is a claim about the complement.
- `CountWorkers` (`:100-103`) is `SELECT COUNT(*) FROM workers WHERE status != 'revoked'`, already
  used by `internal/api/workers.go:155`.

### 2.3 The state space of `agent_token_hash`, which decides Decision A

`agent_token_hash` has exactly two writers in the tree: `SetWorkerAgentToken` sets it non-NULL and
clears `revoked`; `ClearWorkerAgentToken` sets it NULL and sets `status = 'revoked'`. `CreateWorker`
(`workers.sql:1-4`) inserts without the column, so a row it creates has a NULL hash - **but
`CreateWorker` has no production caller.** A search across `*.go` returns twenty call sites and every
one of them is in a `_test.go` file (`internal/worker/handler_test.go`,
`internal/api/jobs_cancel_test.go`, `cmd/relay-server/startup_reconcile_test.go`, and so on).
`UpsertWorkerByHostname`'s `INSERT` arm also leaves the column NULL, but both of its callers run
`SetWorkerAgentToken` in the same transaction, so a committed row from either path always has a hash.

**Therefore, in production, `agent_token_hash IS NULL` is equivalent to `status = 'revoked'`.** That
single fact is what refutes item 1's proposal (section 3, R1) and is what makes item 1's acceptance
criterion 2 a request for a regression (R2).

### 2.4 What each refusal currently tells the caller

| Site | Status | Message |
|---|---|---|
| `handler.go:459` | `Unauthenticated` | `auto-enroll disabled` |
| `handler.go:469, :475, :478, :481, :529` | `Unauthenticated` | `authentication failed` |
| `handler.go:540, :547` | `Unauthenticated` | `authentication failed` |
| `handler.go:549` | `Internal` | `token lookup failed` |
| `handler.go:600` | `Unauthenticated` | **`worker revoked`** |

**The existing auto-enroll revoked refusal already discloses hostname state**, which neither item
notices. `worker revoked` tells an unauthenticated caller that a row for that hostname exists and is
revoked - the exact disclosure both items forbid for the *new* refusal. Section 5.3 closes it.

The only test that asserts on that arm is `TestConnect_AutoEnrollRefusesRevokedWorker`
(`handler_auth_test.go:465-515`), and it asserts `codes.Unauthenticated` and that the worker stays
revoked - **never the message**. So the message is free to change.

### 2.5 What the agent does with a refusal

`internal/agent/agent.go:108` exits the process on `codes.Unauthenticated` rather than retrying. The
exit log comes from `authFailureMessage` (`internal/agent/messages.go:16-25`), whose token-less arm is:

> `agent: authentication failed - token-less auto-enroll was rejected; the server must have RELAY_ALLOW_AUTO_ENROLL enabled; exiting`

Exiting is the right behaviour for a permanent refusal, and that message becomes actively misleading
under either new guard: the flag *is* enabled, and the operator is told to enable it. This is required
scope, and it is where item 1's "surfaced in the agent's error message" criterion lands.

### 2.6 The default-lane fixture family, and exactly what it cannot do yet

PR #149 narrowed `Handler.pool` to `txBeginner` (`handler.go:153-155`), which made all three
`pgx.BeginTxFunc(ctx, h.pool, ...)` sites fakeable. `internal/worker/handler_register_success_test.go`
carries `fakePool` (`:42-52`), `fakeTx` (`:89-140`), `newSuccessFixture` (`:156-226`) and
`startConnect` (`:243-277`); `handler_register_strand_test.go` carries `strandDB` (`:44-127`),
`emptyRows` (`:103-107`), `strandWorkerRow` (`:183-216`) and `strandEpoch` (`:161`);
`handler_registration_deadline_test.go` carries `scriptedStream` (`:28-152`) with its
`agentTokensSent` counter (`:45`), `tokensSent()` (`:130-134`) and the scrub-at-retention in `Send`
(`:107-118`).

**Four concrete gaps stand between that family and the enrollment paths. Each is load-bearing and
none is stated in the fixture item.**

1. **`strandDB.QueryRow` does not discriminate by statement.** It returns `strandWorkerRow{}`
   unconditionally (`handler_register_strand_test.go:109-111`), so *every* `:one` finds a row. A test
   for "a brand new hostname still enrolls" is impossible: nothing can return `pgx.ErrNoRows`.
2. **`fakeTx` has no `QueryRow` at all.** It overrides `Exec`, `Commit` and `Rollback` only
   (`handler_register_success_test.go:98-120`); `QueryRow` falls through to the embedded nil `pgx.Tx`
   and panics with a bare nil dereference. Every statement inside both enrollment transactions -
   `UpsertWorkerByHostname`, `GetWorkerByHostnameForUpdate`, `CountWorkers`, and the new statement of
   section 5.2 - is a `QueryRow` on the tx.
3. **`fakeTx.Exec` returns `pgconn.NewCommandTag("DELETE 0")`** (`:105`), i.e. zero rows affected. So
   `ConsumeAgentEnrollment` returns `rows == 0` and `errEnrollmentNotConsumable` fires *by default*.
   The fixture can drive that rejection branch today and structurally cannot drive a successful
   token enrollment. It needs a configurable tag, exactly as `strandDB` has `execTag`.
4. **`strandWorkerRow.Scan` would successfully scan a `store.AgentEnrollment`** - its type switch
   covers `*pgtype.UUID`, `*string`, `**string` and `*pgtype.Timestamptz`, which is the whole of that
   struct (`internal/store/models.go:11-20`) - but it fills every `pgtype.Timestamptz` with
   `time.Unix(0, 0)` (`:202-203`). So `ConsumedAt.Valid` is true and `ExpiresAt` is 1970: an
   enrollment token read through this stub is *both* already-consumed and expired, and
   `enrollAndRegister` rejects it at `:477` before reaching its transaction.

`captureLog` and `countLines`, which the audit-line assertions need, live in
`handler_tasklog_integration_test.go:250-262` - the **integration** lane. A default-lane assertion on
the audit line needs them moved to an untagged file or re-declared.

### 2.7 The environment the guards land in

`RELAY_ALLOW_AUTO_ENROLL` is off by default (`handler.go:173`, README:290). `RELAY_GRPC_MAX_CONNS`
(1024) and `RELAY_GRPC_MAX_CONNS_PER_IP` (64) bound concurrent connections;
`RELAY_GRPC_REGISTRATION_TIMEOUT` (30s, `handler.go:136`) prices a parked stream. `netlimit.hostKey`
(`internal/netlimit/listener.go:355-377`) aggregates IPv6 to a `/64` and keys IPv4 exactly, and it is
**unexported**.

---

## 3. Discrepancies between the items and HEAD

Most important first.

**R1. REFUTED, and it moves the code: item 1's predicate is the wrong predicate for the auto-enroll
path.** The proposal is "in `autoEnrollAndRegister` ... reject when `existing.AgentTokenHash` is
non-NULL". By section 2.3, on that path a non-NULL hash is equivalent to "the row is not revoked", and
a revoked row is *already* refused three lines earlier (`handler.go:572-574`). So the proposed
predicate is production-equivalent to **"refuse whenever a row exists"** while being written as
something narrower, and it inherits a status vocabulary it does not need. Section 5.1 takes the
equivalent-and-honest form instead. The predicate is not wrong everywhere - it is exactly right on the
*enrollment-token* path, where the NULL and non-NULL cases are genuinely different products
(section 5.4). The item put a correct predicate on the wrong path.

**R2. REFUTED, and it is a request for a regression: item 1's acceptance criterion 2.** "A revoked
worker's hostname still re-enrolls (`agent_token_hash` is NULL after revocation), so the legitimate
recovery path is not broken." Auto-enroll does **not** re-enroll a revoked worker today and must not
start: `handler.go:572-574` refuses it, `TestConnect_AutoEnrollRefusesRevokedWorker`
(`handler_auth_test.go:465-515`) pins it, and README:364-368 documents it as deliberate. Implementing
that criterion literally would revive revoked workers on the credential-less path - the opposite of the
item's own purpose. **The criterion is struck.** What replaces it: the revoked *hostname* stays
refused on auto-enroll, and the revoked *worker* stays revivable by an admin-issued enrollment token,
which is the recovery route this slice documents.

**R3. CONFIRMED, and it is the fact the whole of item 2 rests on: the enrollment-token path is
already bounded.** `enrollAndRegister`'s upsert (`handler.go:493`) and `ConsumeAgentEnrollment`
(`:508`) are in one `pgx.BeginTxFunc` closure; `rows == 0` returns `errEnrollmentNotConsumable`
(`:515-517`), and `pgx.BeginTxFunc` rolls the transaction back on any non-nil closure error, so the
upsert is undone. Already-consumed and expired tokens are refused before the transaction opens
(`:477-482`). **One admin-issued token buys exactly one row.** `reconnectAndRegister` (`:538-553`)
creates no rows at all. So a bound belongs on the auto-enroll path specifically and never on
`UpsertWorkerByHostname`. **Item 2 is right, and this slice keeps that statement byte-identical.**

**R4. CONFIRMED: item 1's claim that `enrollAndRegister` has no revoked check at all.**
`enrollAndRegister` (`handler.go:467-535`) contains no `GetWorkerByHostnameForUpdate`, no reference to
`existing`, and no comparison against `"revoked"`. It reads the *enrollment* record's `ConsumedAt` and
`ExpiresAt` and nothing about the worker. **Confirmed.**

**R5. REFUTED, and it decides Decision B: item 2's four candidate mechanisms are not four
alternatives, because three of them do not bound anything.** Item 2's own acceptance criterion 1 asks
that "a caller that repeatedly registers with fresh hostnames **stops creating rows at a stated
bound**". A rate limit does not stop a slow drip and the item says so. A TTL reaper does not stop a
fast drip: its steady state is creation-rate x TTL, and creation rate is the unbounded quantity. A
CIDR allowlist changes the trust boundary rather than bounding a resource, and item 2 forbids
absorbing it. **Only a ceiling satisfies the criterion as written.** Section 6.

**R6. NEW, not in either item, and it changes what "the refusal discloses nothing" costs.** The
existing refusal on this path already discloses: `codes.Unauthenticated, "worker revoked"`
(`handler.go:600`). Adding a second, generic refusal beside it would leave a caller able to
distinguish "revoked" from "taken" by message. The new guard must therefore *replace* the revoked
refusal rather than sit beside it (section 5.3). No test asserts that message (section 2.4), so the
change is free.

**R7. NEW: an existing integration test asserts the takeover behaviour as correct.**
`TestConnect_AutoEnrollRotatesTokenForExistingHost` (`handler_auth_test.go:517-562`) enrolls
`rotate-host` token-lessly twice, requires the two minted tokens to differ, and then requires a
reconnect with the second token to succeed. Every one of those assertions is falsified by Decision A.
This is not "an existing test whose result changes is a finding to report" - it **is** the behaviour
change, and rewriting it is required scope, named in the plan's step list rather than discovered
during verification.

**R8. NEW: item 2's suggested rate limit would need `netlimit.hostKey` exported, and would need an
eviction story the listener gets for free.** `hostKey` is unexported
(`internal/netlimit/listener.go:355`). More importantly, `netlimit`'s per-source map is evicted at
`net.Conn.Close` - there is a natural end-of-life event. A rate limiter keyed on peer address has no
such event, so its map is unbounded memory keyed on attacker-chosen source addresses unless it grows a
sweeper. That is the same defect one layer down that
`TestLimitListener_ReleasedIPIsRemovedFromTheMap` exists to prevent. This is part of why section 6
declines the rate limit, and it means "cheapest" in item 2's Proposal understates the cost.

**R9. NEW: the existing revoked check is a deny-list of exactly one status value.**
`if existing.Status == "revoked"` (`handler.go:572`) fails **open** on every status added to the
vocabulary - a future `quarantined` or `retired` worker would be silently claimable by auto-enroll.
CLAUDE.md's allow-list rule is written for `tasks.status`, but the reasoning is not table-specific.
Decision A's shape removes the status vocabulary from this decision entirely, which is a second,
independent argument for it.

---

## 4. Threat model and honest exposure

**Principal.** Anything that can reach `:9090`. Both defects require `RELAY_ALLOW_AUTO_ENROLL=true`
(off by default; documented trust model is that any host able to reach gRPC is trusted), except the
enrollment-token half of the takeover, which requires an admin-issued one-shot credential.

**What each defect grants beyond the stated trust model.** The trust model says "a new host may join
the pool". It does not say:

- **"a joining host may invalidate an existing host's credential"** (item 1). The overwrite is a
  silent, persistent lockout that survives disabling auto-enroll, and is indistinguishable from an
  ordinary revocation from the agent's side. It also grants the claimant a *legitimate* assignee
  identity, so it passes `AppendTaskLog`'s and `UpdateTaskStatus`'s `worker_id` predicates as the
  task's genuine assignee. **Identity takeover is upstream of every per-task fence in the tree**,
  which is what makes this the load-bearing half of the pair rather than a hardening item.
- **"a joining host may create unbounded rows in the pool"** (item 2). The rows outlive their
  connections, survive a restart, appear in every `GET /v1/workers` page and every dispatcher scan,
  and nothing removes them.

**Two follow-on effects of takeover, both confirmed by reading `finishRegister`.** A claimant that
reports the victim's live task at its current epoch keeps that task assigned to an identity it now
controls (`reconcileRunningTasks`, `handler.go:842-997`). A claimant that reports nothing gets every
one of the victim's active tasks requeued in a single connect, which is a cheap availability attack.

**What this slice does NOT change, stated up front.** An attacker with reach to `:9090` under
auto-enroll can still register as *some* worker and receive dispatched tasks. That is the trust
model, it is documented, and closing it is the CIDR allowlist item's job, not this slice's.

**The tradeoff Decision B introduces, stated before the design.** A ceiling converts unbounded growth
into a bounded refusal, and a bounded refusal is a denial ceiling: an attacker who fills it stops
*token-less* fleet growth. Two things keep that acceptable and both are load-bearing rather than
decorative - the ceiling gates **only** the auto-enroll path, so enrollment tokens (the route README
already prescribes on an untrusted network) are never refused by it; and `CountWorkers` excludes
revoked workers, so `relay workers revoke` on the junk rows frees the budget without the assignment
and reservation destruction that deleting a worker causes (README:352). Section 6.4 is the operator
story item 2 demands.

---

## 5. Decision A - the takeover guard

### 5.1 Auto-enroll may CREATE a worker and may never touch one

**Decision: `autoEnrollAndRegister` refuses whenever a `workers` row for the claimed hostname already
exists, whatever its status and whatever its token.**

This is the honest spelling of item 1's predicate rather than a widening of it: by section 2.3 the two
are production-equivalent, and R1 gives the derivation. Three further reasons to write it this way:

- **It fails closed on states that do not exist yet.** `agent_token_hash IS NOT NULL` is a claim about
  today's writers. "A row exists" is a claim about the table. If `CreateWorker` ever gains a
  production caller, or an admin pre-provisioning endpoint lands, the narrow predicate silently
  starts admitting takeovers of rows it was never reasoned about.
- **It removes the status vocabulary from the decision** (R9). The current deny-list on `"revoked"`
  fails open on the next status added; "a row exists" cannot.
- **It states the rule an operator can hold in their head**: enrollment creates workers; it never
  claims them. One sentence, both paths (section 5.4 gives the enrollment-token half the same rule
  with the one exception the recovery route requires).

**What it costs, and who pays.** An agent that loses its state directory but keeps its hostname is
refused. **The call: yes, that is the intended behaviour, and revoke-then-re-enroll is the supported
route.** The reasoning, since item 1 requires this be decided and not discovered:

- The refused case and the attack are *the same request*. There is no field in a credential-less
  `RegisterRequest` that distinguishes "I am the same machine that lost its token" from "I am
  claiming your machine". Admitting one admits the other.
- The recovery is cheap and already exists: `relay workers revoke <id>` nulls the token and sets
  `status = 'revoked'`; the operator then issues an enrollment token, or - if they prefer the
  token-less route - the revoked row must be deleted first, which is the honest cost. Auto-enroll
  does **not** revive a revoked worker and this slice does not change that (R2).
- It is not a new class of operator action. Revoke is an existing, documented, non-destructive admin
  action (README:356-362), unlike delete.

**What it does not cost, checked rather than assumed.** Hardware specs are refreshed only by
`UpsertWorkerByHostname`, whose two callers are the enrollment paths; `reconnectAndRegister` does not
call it (`handler.go:538-553`). So an agent that keeps its token already has frozen specs today, and
this guard does not freeze anything that was not already frozen. Any hardware refresh for an existing
worker is a separate concern and is out of scope.

### 5.2 The mechanism: a new statement, not a Go predicate

**Decision: a new query, `InsertWorkerForAutoEnroll`, that is
`INSERT ... ON CONFLICT (hostname) DO NOTHING RETURNING id`, replacing the
lookup-plus-revoked-check-plus-upsert triple on the auto-enroll path.**

sqlc emits it as a `:one`, so `pgx.ErrNoRows` from the scan is the refusal signal and any other error
is a fault. The Go becomes:

```
id, err := txq.InsertWorkerForAutoEnroll(ctx, params)
if errors.Is(err, pgx.ErrNoRows) { return errHostnameClaimed }
if err != nil { return fmt.Errorf("insert worker: %w", err) }
```

Why a statement rather than the item's `GetWorkerByHostnameForUpdate` + Go predicate:

- **It closes a TOCTOU the Go predicate leaves open, and the window is real.**
  `SELECT ... FOR UPDATE` on a hostname that does **not** exist locks nothing, so two concurrent
  auto-enrolls of the same fresh hostname both see `pgx.ErrNoRows`, both proceed, and
  `ON CONFLICT DO UPDATE` lets the loser overwrite the winner's freshly minted token. That is
  takeover of a first-boot agent by a racing attacker - narrower than the shipped defect, and the
  same shape. `DO NOTHING` makes the check and the write one statement, so there is no window at all.
  *Assumption, flagged and worth the plan re-reading Postgres' docs on:* with `ON CONFLICT DO
  NOTHING`, a conflict against an **uncommitted** concurrent insert returns zero rows rather than
  blocking, which is the outcome this design wants; `DO UPDATE` would block instead.
- **It leaves `UpsertWorkerByHostname` byte-identical**, which is item 2's explicit constraint and R3's
  finding. After the change that statement has exactly one caller, `enrollAndRegister`. The plan must
  record the hit count from a search for the symbol rather than asserting uniqueness.
- **It deletes the deny-list** (R9) rather than editing it.
- It reduces the transaction from three statements to two.

`GetWorkerByHostnameForUpdate` does not become dead: section 5.4 gives it its new and only caller.

**Cost: one `make generate`.** No migration, no proto. CLAUDE.md's CRLF procedure applies
(`git diff --ignore-all-space`, revert LF-only hunks), and the recorded lesson "verify the generated
file after a query-comment edit" applies to the new statement's doc comment and to
`UpsertWorkerByHostname`'s corrected one (section 12).

**The conservative fallback, named so the plan may take it if `make generate` proves troublesome:**
keep `GetWorkerByHostnameForUpdate` and refuse on `err == nil`. It is behaviourally identical except
for the concurrent-first-boot race, which would then have to be disclosed in the handler comment
rather than closed. Prefer the statement.

### 5.3 The refusal discloses nothing, and it replaces the one that did

**Decision: `codes.Unauthenticated, "authentication failed"` - the exact status and the exact string
that `enrollAndRegister` and `reconnectAndRegister` already return for every credential failure
(`handler.go:469, :475, :478, :481, :529, :540, :547`). The `worker revoked` message at `:600` is
deleted and its arm folded into this one.**

- Checked against the two existing refusal paths as the brief requires: after this change every
  credential-related refusal on the gRPC registration surface returns the identical status and the
  identical string. The only other refusal is `auto-enroll disabled` (`:459`), which is reachable with
  no hostname at all and therefore cannot be a hostname oracle.
- **R6 is the reason this must be a replacement.** Leaving `worker revoked` beside a generic refusal
  would let a caller separate "revoked" from "taken", which is more disclosure than exists today would
  suggest anybody intended.
- **The oracle that survives, stated rather than papered over.** Refusing at all is itself a signal: a
  caller learns that a hostname is claimed because claiming it fails, while an unclaimed hostname
  succeeds. That is inherent to a create-only rule and cannot be closed without refusing everything.
  Item 1's wording accounts for it - "no information about whether the hostname exists **beyond the
  refusal itself**" - and this design meets that and no more. Say so in README rather than claiming
  the refusal is opaque.
- `errWorkerRevoked` (`handler.go:43-45`) is replaced by a sentinel named for the new rule
  (`errHostnameClaimed` or similar); `errEnrollmentNotConsumable` is untouched.

### 5.4 The enrollment-token path gets a *different* guard, and the difference is forced

**Decision: `enrollAndRegister` refuses when the existing row's `agent_token_hash` is non-NULL, and
still binds to a row whose hash is NULL. That is item 1's original predicate, applied to the path
where it is not a tautology.**

The asymmetry is not a judgement call; it is forced by the recovery route:

- **Revoke-then-re-enroll is the recovery this whole slice points operators at**, and revoking does
  not delete the row (`ClearWorkerAgentToken`, `workers.sql:83-86`). If the enrollment path refused
  every existing row, the revoked row would block its own recovery and the only remaining route would
  be deleting the worker - which destroys its assignments and reservations (README:352). Section 5.1's
  cost would become unpayable.
- README:364-368 already documents the revive as the deliberate difference between the two paths, and
  `TestConnect_EnrollmentTokenRevivesRevokedWorker` (integration, referenced at ROADMAP:360) pins it.
  Both stay true and green.
- On this path the predicate is genuinely discriminating: NULL means revoked (recovery, allowed),
  non-NULL means a live credential (takeover, refused).

So the one-sentence rule holds for both paths: **enrollment never overwrites a live credential.**
Auto-enroll additionally never touches an existing row at all, because it has no admin in the loop to
authorize the exception.

**What it costs.** An operator who wants to re-enroll a *live* agent with a fresh enrollment token -
credential rotation, or a machine being re-provisioned in place - must revoke first. Same rule, same
remedy, one sentence to document. Deliberate.

**It sits inside the existing transaction, which is item 1's third "settle these".** Add
`txq.GetWorkerByHostnameForUpdate(ctx, reg.Hostname)` as the **first** statement of
`enrollAndRegister`'s existing `pgx.BeginTxFunc` closure, ahead of the upsert. `pgx.ErrNoRows` means a
fresh hostname and is not an error. Two notes for the plan:

- **The `FOR UPDATE` lock is what makes this non-racy** for the case that matters, an existing row.
  For a *fresh* hostname it locks nothing, and the residual race there is two enrollment tokens
  claiming the same new hostname concurrently - both admin-issued, so out of the threat model, and
  `ON CONFLICT DO UPDATE` resolves it to one row.
- **Lock ordering stays consistent.** This transaction will hold a worker-row lock and then update an
  `agent_enrollments` row. `ConsumeAgentEnrollment` has no other caller, so no transaction anywhere
  takes those two in the opposite order and there is no deadlock cycle to construct. The plan should
  re-check that claim by searching for the symbol rather than trusting this sentence.

---

## 6. Decision B - the row-creation bound

### 6.1 Decision A does not shrink this attack, and saying so is the first job

The brief's lean was that Decision A materially shrinks the attack Decision B must cover. **Checked,
and it does not.** Decision A confines the attacker to hostnames with no row - and creating a row for
a hostname with no row *is* the attack item 2 describes. Each distinct hostname still yields exactly
one persistent row; what Decision A removes is the attacker's ability to *re-claim* or *rotate*, which
was never the growth vector. The two defects are genuinely independent, exactly as item 2 says when it
insists "one does not close the other".

So a cheaper bound is not made sufficient by Decision A, and the reaper is not made the only thing
that matters. Decision B stands on its own.

### 6.2 The mechanism: a ceiling on the auto-enroll path only

**Decision: `RELAY_AUTO_ENROLL_WORKER_CEILING`, default `1024`, `0` disables. When
`CountWorkers` (non-revoked workers) is at or above the ceiling, `autoEnrollAndRegister` refuses. No
other path consults it.**

The elimination, per R5 and item 2's own acceptance criterion 1 ("stops creating rows at a **stated
bound**"):

| Mechanism | Bounds the total? | Verdict |
|---|---|---|
| Rate limit on the auto-enroll path | **No** - a slow drip defeats it, as the item says. Plus R8's unbounded map | Reject |
| TTL reaper on never-reconnected rows | **No** - steady state is rate x TTL, and rate is the unbounded quantity. Also a destructive background job whose deletions take assignments and reservations with them | Reject **for this slice**; propose as an item (section 16) |
| CIDR allowlist | Changes the trust boundary; item 2 forbids absorbing it; ROADMAP:366 has it under Deferred | Non-goal |
| Total ceiling | **Yes**, and it is the only one that does | **Ship** |

**The ceiling and the reaper are complements, not alternatives, and that is worth stating plainly
because it is the strongest argument against shipping only one.** A ceiling without a reaper converts
"unbounded junk rows" into "bounded junk rows that permanently occupy the fleet's growth budget"; a
reaper without a ceiling bounds nothing. This slice ships the half that satisfies the acceptance
criterion, and section 6.4 supplies the recovery that stands in for the reaper - `revoke` frees budget
today, manually, without a background process. The reaper is what makes that automatic and is what
helps a deployment that has already been hit; it is proposed as its own item with that framing.

**Where the check goes.** Inside `autoEnrollAndRegister`'s transaction, **before** the insert:
`n, err := txq.CountWorkers(ctx)`; refuse when `ceiling > 0 && n >= int64(ceiling)`. Refusing before
the write is what makes the refusal free of side effects.

**The bound is approximate, and the arithmetic is stated rather than implied.** Two concurrent
auto-enrolls at `n == ceiling-1` both pass the check under read-committed isolation and both insert,
so the true bound is `ceiling + C` where `C` is the number of auto-enroll transactions in flight -
itself bounded by `RELAY_GRPC_MAX_CONNS` (1024 by default). Making it exact would need serializable
isolation or an advisory lock on a hot path, for an overshoot that is a fraction of a percent of the
ceiling. **Do not claim an exact cap in README or in the doc comment; claim `ceiling + RELAY_GRPC_MAX_CONNS`.**

**The knob's home.** `Handler.AutoEnrollWorkerCeiling int`, set by `cmd/relay-server` after
construction, non-positive meaning the default - the same shape as `RegistrationTimeout`
(`handler.go:175-179`) and `TrailingLogWindow` (`:181-187`), so every existing `NewHandler` /
`NewHandlerWithGrace` call site stays correct with no edit. Note the one asymmetry that must be
written into the resolver's doc comment: unlike those two, this knob has a meaningful **zero**
(disabled), so its resolver cannot use the "non-positive means default" rule for `0`. Resolve
`0` as disabled, negative as the default with a startup line saying so, following
`parseWatchdogDuration`'s three-outcome shape and never `log.Fatalf`.

### 6.3 Why 1024, derived rather than picked - and where the derivation is weak

The nearest anchor in the tree is `RELAY_GRPC_MAX_CONNS = 1024`
(`docs/superpowers/specs/2026-08-20-grpc-admission-bounds.md` section 6.3), chosen as "far above any
plausible relay fleet and far below where FDs or goroutines hurt". Matching it means the two admission
ceilings agree, and an operator who has already raised one has an obvious pointer to the other.

**The derivation is not airtight and the weakness must be in the doc comment.** The two knobs count
different things: `RELAY_GRPC_MAX_CONNS` bounds *concurrent connections*, this bounds *total
non-revoked rows*. A farm of 2000 intermittently-connected machines with 800 online at any moment
stays under the connection cap and exceeds a 1024-row ceiling. That deployment hits the ceiling
legitimately, and section 6.4 is what it does about it. `0` disables, and an operator who knows their
fleet exceeds 1024 machines should set it explicitly rather than relying on a default derived from a
different quantity.

### 6.4 The operator story, which item 2 forbids shipping the ceiling without

**What the operator sees.** An unconditional startup line naming the effective ceiling and saying
explicitly when it is disabled, in the shape of `watchdogBoundsLine`
(`cmd/relay-server/watchdog_config.go`) and the gRPC bounds line. Plus a cumulative refusal counter
per reason, published under a new `auto_enroll` section of `GET /v1/server/counters` (section 7).

**What the agent sees.** `codes.Unauthenticated, "authentication failed"`, so the agent exits rather
than reconnect-looping (section 2.5), and `authFailureMessage`'s token-less arm - corrected by this
slice - names both new causes and both remedies.

**How an operator raises it, honestly.** Three routes, in the order they should be tried:

1. **Revoke the junk.** `CountWorkers` excludes `status = 'revoked'`, so
   `relay workers revoke <id>` on rows that do not correspond to real machines frees budget
   immediately, with no restart, and without the assignment and reservation destruction that deleting
   a worker causes (README:352). This is the recovery that exists *today* and it is why the ceiling is
   shippable without the reaper.
2. **Use enrollment tokens.** The ceiling gates the auto-enroll path only. An operator whose
   token-less budget is exhausted can still add machines with `relay agent enroll`, which is what
   README already tells them to do on a network where the auto-enroll trust model does not hold. This
   is the "without downtime" answer.
3. **Raise `RELAY_AUTO_ENROLL_WORKER_CEILING`, or set it to `0`.** This requires a server restart, and
   that is not free: agents reconnect on backoff and the `RELAY_WORKER_GRACE_WINDOW` (2m default)
   covers their running tasks, so it is a blip rather than data loss. **Say "requires a restart" in
   README rather than implying the knob is hot-reloadable.**

---

## 7. Observability, and the log-budget question

**Decision: refusals are COUNTED, never logged. The successful auto-enroll audit line
(`handler.go:617`) stays exactly as it is.**

`bug-2026-08-15-registration-log-sites-are-outside-the-connection-budget` is open and says the
registration path's log sites sit outside the per-connection budget - the limiter is allocated at
`handler.go:350`, after `authenticateAndRegister` has returned. A new `log.Printf` on an
attacker-reachable refusal would be a fresh instance of exactly the flood class the 2026-08-15 slice
closed. **This slice adds no log site at all**, which is a stronger outcome than budgeting one and
needs no new `logKind` (and so does not touch the `ingest_log_budget.counts` JSON contract or its
eight-site checklist).

**The asymmetry with the existing audit line is the argument, and it is precise.** That line survives
unbudgeted by decision because a *successful* token-less enrollment is a one-time state change per
hostname - and, after Decision A, permanently so: the hostname can never be enrolled again. A
*refusal* is unboundedly repeatable by the same caller with the same hostname. The volume defence
`clipID` + `%q` gives the success line is not a substitute for a count defence the refusal would not
have. The audit line's comment (`handler.go:605-616`) should gain that sentence; it currently reasons
about per-stream bounding and does not have the "one per hostname, forever" argument available to it.

**Where the counts live.** A `autoEnrollRefusals` value field on `Handler`, alongside `ingestDrops`,
`taskLogFenceRejects` and `statusFence` (`handler.go:199-237`), read through an exported accessor and
wired to `GET /v1/server/counters` by `cmd/relay-server`'s `buildHTTPServer` under its **own** section
with its **own** `CounterSources` field. Split by reason, the way `statusFence` splits by what the row
said:

| Key | Cause |
|---|---|
| `hostname_claimed` | auto-enroll, a row for that hostname already exists (5.1) |
| `fleet_at_ceiling` | auto-enroll, `CountWorkers >= ceiling` (6.2) |
| `credential_live` | enrollment token, existing row has a non-NULL hash (5.4) |

**A deliberate departure from `netlimit`'s precedent, with the reason.** The admission slice ships a
periodic refusal *log summary* because `netlimit` is a listener in a package with no HTTP surface.
`Handler` has one. Shipping both would give an operator two numbers for one event, and the log summary
is the weaker of the two (a rate, not a cumulative total). One mechanism.

**The diagnosability cost, named rather than hidden.** A legitimately refused agent - the lost-state-
directory case - produces no server-side line naming it. The operator's signals are the counter and
the agent's own exit message, which names its own hostname. The server deliberately cannot name the
hostname, because on this path the hostname is attacker-chosen and a refusal is unboundedly
repeatable; that is the whole content of the previous two paragraphs. Write this into README so an
operator debugging a refused agent looks at the agent's log first.

**The counters payload checklist is the largest piece of incidental scope in this slice**, and the
plan should size it before committing: the const, the accessor, the `api.CounterSources` field, the
response struct and json tags, the `counterPayloadLeaves` entries, and the section list in the server
counters test. If it proves larger than the guards themselves, the reduced form - counters on
`Handler` with the accessor and no HTTP section - still satisfies every acceptance criterion in both
items, and the section can follow. Flagged as a plan-time decision with a stated fallback, not left
for verification to discover.

---

## 8. Testability, and the fixture item

### 8.1 Fold it in: yes

`docs/backlog/idea-2026-08-25-default-lane-fixture-for-the-enrollment-paths.md` is folded into this
slice. Both guards need tests; CI runs the default lane (`go test -race ./...`, no tag) and does not
run the integration lane; and section 2.6 shows the fixture family is four small edits away from
reaching both enrollment paths. Doing the fixture work in a slice that has an independent reason to
reach those paths is strictly cheaper than doing it as its own slice and then doing it again here.

The four edits, from section 2.6, are this slice's fixture scope:

1. `strandDB.QueryRow` gains statement discrimination - a per-test map from a statement substring to
   either a `pgx.Row` or an error - so `pgx.ErrNoRows` is expressible. **Without this the fresh-hostname
   test cannot exist and the refusal test is vacuous.** This is the load-bearing one.
2. `fakeTx` gains `QueryRow` with the same discrimination, because every statement in both enrollment
   transactions is a `QueryRow` on the tx.
3. `fakeTx.Exec` gains a configurable command tag, so `ConsumeAgentEnrollment` can return one row and
   a successful token enrollment becomes reachable.
4. A `store.AgentEnrollment` row stub with a **future** `ExpiresAt` and an invalid `ConsumedAt`, since
   the shared `strandWorkerRow` makes every enrollment look consumed and expired.

Plus: `captureLog` / `countLines` move from `handler_tasklog_integration_test.go:250-262` to an
untagged file, or are re-declared, if the audit-line assertions land in the default lane. Moving them
is preferable and is a zero-assertion edit to the integration lane.

### 8.2 Which of the item's criteria this slice meets, and which it reshapes

| Criterion | Met? |
|---|---|
| "A default-lane test drives `enrollAndRegister` to a successful return, and one drives `autoEnrollAndRegister`" | **Met.** Both are the GREEN controls the guards need (T2, T8) |
| "`errEnrollmentNotConsumable` ... asserted somewhere CI executes" | **Met.** And note it is currently the fixture's *default* behaviour (2.6.3), so the test must set the tag to prove it is asserting the branch rather than the fixture |
| "`errWorkerRevoked` - a revoked worker attempting auto-enroll" | **RESHAPED, not met as written.** This slice deletes that sentinel: a revoked worker's hostname is refused because a row exists, not because it is revoked (5.1, 5.2). The *behaviour* is asserted in the default lane and the integration test that pins it stays green; the named branch ceases to exist. The plan must say so on the item's Resolution note rather than claiming the criterion was met |
| "The auto-enroll audit log line ... asserted somewhere CI executes" | **Met**, contingent on the `captureLog` move |
| "`TestScriptedStream_DoesNotRetainARawAgentToken` stops being speculative ... Confirm the scrub actually fires here (`agentTokensSent`)" | **Met, and this slice is the first consumer** - see 8.3 |

Recommendation: close the item, with the third row's reshaping written into the `## Resolution` note.
Do not close it silently as a side effect.

### 8.3 The token scrub: confirm it fires, do not assume it

`scriptedStream.Send` (`handler_registration_deadline_test.go:107-118`) increments `agentTokensSent`
and clones-and-redacts **only** when `rr.AgentToken != ""`. `finishRegister`'s reconnect caller passes
`""` (`handler.go:552`); both enrollment callers pass a real minted token (`:534`, `:618`), produced by
the package-level `agentTokenGenerator` (`:30-37`) which is not stubbed by default. So the successful
auto-enroll and successful token-enrollment tests are the first inputs that take the redaction branch.

**Assert the fire, not the absence.** `require.Equal(t, 1, f.stream.tokensSent())` is the positive
signal; asserting only "the secret is not in the slice" passes just as well against a build where the
token was never minted, which is the vacuity this project's own fixture comment
(`handler_registration_deadline_test.go:83-89`) predicted. A second assertion that the retained
message carries the redaction placeholder is what distinguishes "scrubbed" from "never sent".

---

## 9. Alternatives considered and rejected

- **Item 1's literal predicate on the auto-enroll path** (`existing.AgentTokenHash != nil`). Rejected:
  production-equivalent to "a row exists" (2.3), narrower in what it says than in what it does, and it
  keeps a status deny-list that fails open (R9). Adopted verbatim on the *enrollment* path, where it
  discriminates (5.4).
- **Item 1's acceptance criterion 2** (auto-enroll re-enrolls a revoked worker). Rejected as a
  regression against code, test and README (R2).
- **Guarding `UpsertWorkerByHostname` itself.** Rejected, and item 2 predicted exactly this mistake:
  it would penalise the one path that was already correct (R3). The new statement leaves it untouched.
- **The same guard on both paths.** Rejected: it would block revoke-then-re-enroll, leaving delete as
  the only recovery, which destroys assignments and reservations (5.4).
- **A rate limit on the auto-enroll path.** Rejected: does not satisfy item 2's stated-bound criterion,
  and needs an eviction story `netlimit` gets free from `Conn.Close` (R8). Would also require exporting
  `netlimit.hostKey` or duplicating its `/64` logic.
- **A TTL reaper.** Rejected **for this slice**, not on merit: it bounds nothing on its own (R5), it is
  a destructive background job whose deletions take assignments and reservations with them, and it
  needs a column the schema does not have (nothing records which path created a row;
  `connection_epoch <= 1` is the nearest available proxy and it does not distinguish an auto-enrolled
  row from a token-enrolled one). It is the right complement to the ceiling and is proposed as its own
  item (16.1).
- **A CIDR allowlist.** Non-goal. `idea-2026-06-04-cidr-allowlist-auto-enroll` stays open and separate;
  item 2 forbids absorbing it and ROADMAP:366 has it under Deferred.
- **A periodic refusal log summary in addition to the counters.** Rejected: two numbers for one event,
  and the log summary is the weaker one, because `Handler` has an HTTP counter surface that `netlimit`
  did not (section 7).
- **A per-refusal `log.Printf`.** Rejected: a new unbounded attacker-driven log site on the exact path
  the connection caps exist to bound, and a fresh instance of the class
  `bug-2026-08-15-registration-log-sites-are-outside-the-connection-budget` describes.
- **Keeping `worker revoked` as a distinct message.** Rejected: it is a live hostname-state oracle
  (R6), no test asserts it (2.4), and leaving it beside a generic refusal makes the new refusal
  distinguishable by message - which both items forbid.
- **Making the ceiling exact** (serializable isolation, or an advisory lock). Rejected: a hot-path cost
  for an overshoot bounded by `RELAY_GRPC_MAX_CONNS`. The approximation is disclosed instead (6.2).
- **Validating or bounding `reg.Hostname` here.** Out of scope; proposed as an item (16.2).

---

## 10. Test strategy and the mutation battery

A green test can be vacuous, so each item below names its RED, the error that proves the RED, and what
would make it pass while the guard is wrong.

### 10.1 The guards

**T1. `TestConnect_AutoEnrollRefusesAHostnameThatAlreadyHasAWorkerRow`** (default lane).
Fixture: `InsertWorkerForAutoEnroll`'s `QueryRow` answers `pgx.ErrNoRows`. Assert `Connect` returns
`codes.Unauthenticated`, no worker event is published, and `h.registry.Send(...)` fails.
**RED at HEAD:** the fixture reaches a successful registration, so `Connect` blocks in the message
loop and the assertion fails as `Error(...)` on a nil error after the harness's bounded wait, with
`startConnect`'s explanatory timeout message. *(At HEAD the statement does not exist, so the RED is
structural for T1 specifically - which is why T1 is not the criterion-carrying test; T3 is.)*
**Vacuity:** passes if `AllowAutoEnroll` were false, or if the fixture never reached the guard. T2 is
the control in the same fixture family.

**T2. `TestConnect_AutoEnrollStillCreatesAWorkerForAFreshHostname`** (default lane). The control for
T1 and the fixture item's first criterion. `InsertWorkerForAutoEnroll` answers with a row. Assert a
successful registration (online event, registry entry, `RegisterResponse`), and
`f.stream.tokensSent() == 1` plus the redaction placeholder in the retained message (8.3).
**Vacuity:** the `tokensSent` assertion is what stops this passing against a build that never minted
a token.

**T3. `TestConnect_AutoEnrollRefusalLeavesTheExistingWorkerUntouched`** - item 1's acceptance
criterion 1, and the test that carries it. Two arms:
- Default lane: assert `f.tx.execsSeen()` contains **no** `SetWorkerAgentToken`, and
  `f.tx.outcome()` reports `commits == 0` with `rollbacks >= 1`. **Asserting that no statement was
  issued is not asserting the transaction did not commit** - the inverse of the M15 lesson from the
  2026-08-25 retro, and the reason both halves are here.
- Integration lane: enroll `takeover-host`, capture its `agent_token_hash`, attempt a token-less
  enroll of the same hostname, assert the refusal **and that the hash is byte-identical afterwards**,
  and that the original agent's token still authenticates. **RED at HEAD**, failing on the hash
  comparison with the old and new values printed.

**T4. `TestConnect_AutoEnrollRefusalIsIndistinguishableFromACredentialFailure`** (default lane).
Assert the refusal's `status.Code` and `status.Message` are equal to what `reconnectAndRegister`
returns for an unknown agent token, driven in the same test so the two are compared rather than each
compared to a literal. Additionally assert the message contains neither the hostname, nor `revoked`,
nor `exists`, nor `ceiling`. **RED at HEAD** for the revoked arm: today it is `worker revoked`.
**Vacuity:** comparing two literals in the test would pass if both sites were changed to something
disclosing; comparing the two *produced* messages will not.

**T5. `TestConnect_AutoEnrollRefusesWhenTheFleetIsAtTheCeiling`** (default lane). `CountWorkers`
answers `ceiling`. Assert the refusal, and assert **no insert statement was issued** - the check must
precede the write. **RED at HEAD:** no ceiling exists, so the registration succeeds.
**Vacuity:** passes if the guard refused everything; T6 and T2 are the controls.

**T6. `TestConnect_AutoEnrollAdmitsOneBelowTheCeiling`** (default lane). `CountWorkers` answers
`ceiling - 1`. Assert success. This is the boundary test that distinguishes `>=` from `>` - a
one-character mutation that T5 alone cannot see.

**T7. `TestConnect_EnrollmentTokenRefusesAHostnameWithALiveCredential`** (default lane).
`GetWorkerByHostnameForUpdate` answers a row with a non-NULL hash. Assert the refusal, the identical
message, and that no `SetWorkerAgentToken` was issued and the transaction rolled back.
**RED at HEAD:** today the enrollment path never looks the worker up (R4) and the takeover succeeds.

**T8. `TestConnect_EnrollmentTokenStillEnrollsARevokedHostname`** (default lane). Same fixture with a
NULL hash. Assert success and `tokensSent() == 1`. The control for T7 and the guard on the recovery
route. Its integration sibling, `TestConnect_EnrollmentTokenRevivesRevokedWorker`, **must stay green
and unedited** - that is the strongest single guarantee in this slice that the recovery route survives.

**T9. `TestConnect_ReconnectIsRefusedByNeitherGuard`** (default lane) - item 2's acceptance criterion
3. Drive `reconnectAndRegister` with `CountWorkers` far above the ceiling and a live existing row;
assert a successful registration and that no insert or upsert statement was issued.
**Vacuity:** the "no insert" half is what proves the path really is row-free rather than merely
succeeding.

**T10. `TestConnect_EnrollmentTokenIsNotSubjectToTheCeiling`** - item 2's acceptance criterion 2. One
valid enrollment token creates exactly one row with `CountWorkers` above the ceiling.

**T11. `TestConnect_AutoEnrollRefusalWritesNoLogLine`** (default lane), using the moved `captureLog`.
Assert the whole captured log is empty across N refusals, mirroring
`TestRegisterWorker_ReconcileEchoesAnUnparseableRunningTaskIdAndLogsNothing`'s shape so any wording
reddens it. This is the check behind section 7's decision.

**T12. `TestConnect_AutoEnrollSuccessStillWritesExactlyOneAuditLine`** - the audit line survives, and
`TestConnect_AutoEnrollLogLineCannotBeForgedOrFloodedByTheHostname`
(`handler_auth_test.go:401-442`) **must stay green**. Note that its helper enrolls twice under
different hostnames, so Decision A does not break it - the plan should confirm that by reading rather
than by running.

**T13.** The counters section, per the established checklist:
`TestServerCounters_ReportsAutoEnrollRefusals` reading the three reasons back through
`GET /v1/server/counters`, plus whatever payload-arity guards the existing sections carry.

### 10.2 Existing tests whose result changes

**`TestConnect_AutoEnrollRotatesTokenForExistingHost` (`handler_auth_test.go:517-562`) is rewritten,
not deleted.** Its three assertions invert: the second token-less enroll must be refused, the first
worker's token must still authenticate, and the row's hash must be unchanged. Rename to
`TestConnect_AutoEnrollRefusesAnExistingHostnameAndLeavesItsTokenIntact`. **This is required scope in
the plan's step list**, per R7.

`TestConnect_AutoEnrollRefusesRevokedWorker` (`:465-515`) stays green unedited - it asserts the code
and the row's status, never the message (2.4).

Every other test in `internal/worker` should be unaffected. **Any other test whose result changes is a
finding to report, not to fix.**

### 10.3 Mutation battery

Run in an isolated worktree with an isolated scratchpad path (both recorded lessons). Verify each
mutation actually applied - CRLF has silently defeated four in a row in this tree - and treat a uniform
result across the battery as a broken harness rather than as good coverage.

| # | Mutation | Must redden |
|---|---|---|
| M1 | `ON CONFLICT (hostname) DO NOTHING` -> `DO UPDATE SET ...` in the new statement | T1, T3 (both arms) |
| M2 | Drop the `errors.Is(err, pgx.ErrNoRows)` arm, treat it as a generic error | T1 (wrong status code) and T4 |
| M3 | Move the ceiling check **after** the insert | T5's "no insert issued" assertion |
| M4 | `n >= ceiling` -> `n > ceiling` | **T6** - and NOT T5, which is the point of having both |
| M5 | `ceiling > 0` disable-check inverted (`ceiling == 0` means unlimited -> means zero) | T2 (a fresh enrol with the knob unset is refused) |
| M6 | Apply the ceiling to `enrollAndRegister` too | T10 |
| M7 | Apply the ceiling to `reconnectAndRegister` too | T9 |
| M8 | Delete `GetWorkerByHostnameForUpdate` from `enrollAndRegister` | T7 |
| M9 | `existing.AgentTokenHash != nil` -> `existing.Status == "revoked"` on the enrollment path | **T7 and T8 together** - T8 alone survives it, which is why the pair is needed |
| M10 | Move `enrollAndRegister`'s new lookup **outside** the transaction | Nothing behavioural. **A known survivor, recorded** - it is a source property (item 1's third "settle these"), and the plan should decide whether it is worth a structural check or a comment. Recommend a comment; this tree has just measured what a structural guard costs |
| M11 | Restore `codes.Unauthenticated, "worker revoked"` for the existing-row arm | T4 |
| M12 | Add `log.Printf("auto-enroll refused for %q", reg.Hostname)` to the refusal | **T11** |
| M13 | Delete the audit line at `handler.go:617` | T12 |
| M14 | Reuse `UpsertWorkerByHostname` on the auto-enroll path | T1, T3 |
| M15 | Increment the wrong reason counter (`hostname_claimed` for a ceiling refusal) | T13 - and this is the mutation that proves the split is real rather than decorative |
| M16 | Have `finishRegister` pass `""` as `rawAgentToken` from the auto-enroll caller | T2's `tokensSent()` assertion - the discriminating check that the scrub fires (8.3) |

Control: at least one mutation known to die trivially, run first, to establish the harness is sound
before any survivor is recorded.

---

## 11. Invariant compliance

- **Epoch fence.** No write to `tasks.status` or `task_logs` is added, moved or re-predicated. No
  existing fence argument changes. **The slice's relationship to this invariant is upstream of it:**
  the epoch establishes currency and the `worker_id` predicate establishes identity, and both are
  worthless if an attacker can *become* the assignee. Closing takeover is what makes those fences
  mean what their comments say. Say this in the handler comment; it is the sentence
  `bug-2026-08-12`'s Notes section was written to preserve.
- **Identity-checked teardown, read in the acquire direction.** Both new refusals return from inside
  the enrollment transaction, **before** `finishRegister` and therefore before
  `RegisterWorkerConnection` acquires the worker's generation. So no generation is taken, no grace
  timer is cancelled, no sender is registered, and there is nothing to release. This is CLAUDE.md's
  "where there is no identity to check, say so and name what replaces it": what replaces it is that
  the refusal happens strictly above the acquisition, and adding any release call on these paths
  would itself be the clobber the rule forbids. **A test that a refusal issues zero `Exec`s
  (`f.db.execsSeen()` empty - `MarkWorkerOfflineIfEpoch` is the only `Exec` on that seam) is the
  cheap check that this stays true**, and it should be an assertion in T1 and T7 rather than a
  sentence here.
- **One bounded sender per gRPC stream.** Untouched. No send is added anywhere; a refused
  registration never reaches `NewWorkerSender`.
- **Single job-spec pipeline.** Not implicated; no spec ingestion.
- **No interior pointers across locks.** The refusal counters follow `statusFence`'s shape - a value
  field on `Handler` containing atomics, read through an accessor that returns a snapshot by value.
  No getter returns a pointer.
- **Single JSON entry point.** Not implicated. The counters section is a response, not a request body;
  `readJSON` is untouched.
- **Allow-list, not deny-list.** The auto-enroll guard stops consulting `workers.status` entirely
  (R9), which removes a deny-list rather than adding one. The enrollment-path predicate is on
  nullability, not on a status vocabulary. Neither can fail open on a new status.
- **Generated code.** One new statement in `internal/store/query/workers.sql` plus `make generate`.
  `*.sql.go` and `models.go` are never hand-edited. The CRLF procedure and the
  "verify the generated file after a query-comment edit" lesson both apply.

---

## 12. Prose that must move

Wrong prose has been this repository's dominant defect class for ten consecutive iterations, and a
"correct the docs" instruction without a site list gets partially done. Each site below is an
acceptance criterion, with its exact current wording.

**README.md**

1. **`:200`** - "On a trusted private network you can instead run the server with
   `RELAY_ALLOW_AUTO_ENROLL=true` and start the agent with no token at all - skip the
   `relay agent enroll` step entirely." Add that this works for a hostname with **no existing worker
   row**, and that a machine re-provisioned in place must be revoked first.
2. **`:290`** (the `RELAY_ALLOW_AUTO_ENROLL` env-table row) - "...A long-lived agent token is still
   issued on join and used for all later reconnects. **Revoked workers are not revived.**" The last
   sentence becomes a special case of a larger rule: an existing worker row is never touched at all.
   Plus a **new row** for `RELAY_AUTO_ENROLL_WORKER_CEILING` naming its default, its `0`, that it
   counts **all** non-revoked workers rather than only auto-enrolled ones, and why (nothing records
   which path created a row).
3. **`:354`** - "When the server runs with `RELAY_ALLOW_AUTO_ENROLL=true`, an agent with no `token`
   file and no `RELAY_AGENT_ENROLLMENT_TOKEN` attempts token-less auto-enrollment instead of exiting.
   If the server does not allow it, the agent exits with an authentication error." Add the two new
   refusal causes and the fact that the agent exits on both.
4. **`:364-368`** - "Token-less auto-enrollment is the exception to that rule: whereas a deliberate
   token re-enrollment (with a fresh admin-issued enrollment token) clears the revoked state,
   auto-enrollment under `RELAY_ALLOW_AUTO_ENROLL` does not revive a revoked worker..." This paragraph
   stays **true** and gains the new enrollment-token rule: an enrollment token still revives a revoked
   worker and no longer binds to one whose credential is live.
5. **`:370-388`**, the whole "What auto-enrollment costs, stated plainly" paragraph. Six specific
   falsifications:
   - "any host able to reach the gRPC port may **take over an existing worker by claiming its
     hostname**" - **false after this slice.**
   - "Takeover is the larger of the two costs and is not a special case: the upsert is
     `ON CONFLICT (hostname) DO UPDATE ... RETURNING id`, so claiming an *in-use* hostname returns the
     existing worker's id and auto-enrollment then overwrites that worker's agent token hash. The
     legitimate agent is locked out at its next reconnect and the claimant inherits its registry slot,
     its assignments and its reservations." - **false**, and it describes a statement the auto-enroll
     path no longer calls.
   - "(See `docs/backlog/bug-2026-08-12-auto-enroll-hostname-takeover.md`, which is open.)" - the item
     is closed by this slice; the reference must go or point at this spec.
   - "Revoked workers are the one exception auto-enrollment refuses" - **false**: every existing row is
     refused, and revoked is no longer distinguished. The clause that follows - "note that the
     *enrollment-token* path has no revoked check at all, so an admin-issued token can revive a
     revoked worker that auto-enrollment could not" - stays **true** and is promoted from an anomaly to
     the documented recovery route.
   - **"Nothing bounds the total."** - the exact sentence item 2 names, and **false** after the
     ceiling. Replace with the ceiling, its default, its `0`, and the honest
     `ceiling + RELAY_GRPC_MAX_CONNS` arithmetic (6.2).
   - "Row growth is a deliberate, recorded decision rather than an oversight" - **false**; it is now
     a bounded, recorded decision.
   The two sentences that stay true and should be kept verbatim: "`RELAY_GRPC_MAX_CONNS_PER_IP` bounds
   how many such registrations one source address can have *in flight at once*..." and "The
   enrollment-token path does **not** have this property - the worker upsert and the single-use token
   consume share one transaction, so one admin-issued token buys exactly one row" (R3 confirms both).
6. **New README content**, not a correction: the operator story of 6.4 (revoke frees budget, enrollment
   tokens bypass the ceiling, raising the knob needs a restart), the counters section of 7, and the
   diagnosability note that a refused agent is diagnosed from the **agent's** log because the server
   deliberately does not name an attacker-chosen hostname on a repeatable refusal.

**Go**

7. **`internal/agent/messages.go:22-24`** - "agent: authentication failed - token-less auto-enroll was
   rejected; the server must have RELAY_ALLOW_AUTO_ENROLL enabled; exiting". Actively misleading under
   either new guard. Must name all three causes (flag off; hostname already has a worker; fleet at the
   ceiling) and the remedy (revoke the existing worker, or use an enrollment token). This is where item
   1's "surfaced in the agent's error message" lands.
8. **`internal/worker/handler.go:555-557`** - `autoEnrollAndRegister`'s doc comment: "It **upserts** the
   worker by hostname and issues a fresh agent token without consuming any enrollment record." False
   after 5.2.
9. **`internal/worker/handler.go:43-45`** - `errWorkerRevoked`'s comment. Sentinel replaced.
10. **`internal/worker/handler.go:605-616`** - the audit-line comment block. Gains the section 7
    asymmetry: a success is one line per hostname *forever* now that the hostname can never be
    re-enrolled, while a refusal is unboundedly repeatable and is therefore counted, not logged.
11. **`internal/worker/handler.go:463-466`** - `enrollAndRegister`'s doc comment ("All DB writes (worker
    upsert, enrollment consume, agent-token set) execute inside a single transaction") must name the new
    lookup and say the guard is inside the same transaction and holds `FOR UPDATE`.
12. **`internal/store/query/workers.sql:56-58`** - `UpsertWorkerByHostname`'s comment: "Insert a new
    worker or **update hardware specs on reconnect**." Misleading today (reconnect does not call it) and
    more so afterwards, when its only caller is `enrollAndRegister`. Correct it and name the caller.
    **Then verify the regenerated `workers.sql.go` doc comment actually changed** - the CRLF revert has
    silently discarded a regenerated file before.
13. **`internal/store/query/workers.sql:70-81`** - `SetWorkerAgentToken`'s comment ("This is the one
    place a revoked worker is revived") stays true and should say which caller may reach it with an
    existing row and which may not.
14. **`internal/worker/handler_registration_deadline_test.go:83-89`** - "the next test written against
    this fixture is the one that would have leaked". This slice **is** that test. Rewrite to say the
    scrub now fires, and name the test that proves it (8.3).
15. **`internal/worker/handler_register_strand_test.go:23-43`** and
    **`handler_register_success_test.go:142-155`** - both headers describe what the fixture family can
    and cannot reach. Both become stale the moment `QueryRow` discriminates. Prefer descriptions that
    survive the next fixture edit over enumerations.

**Docs**

16. **`docs/superpowers/specs/2026-08-12-tasklog-append-assignee-fence.md` section 5** carries a dated
    correction saying an auto-enroll attacker can seize an existing worker. Add a dated note that the
    correction is now closed by this spec, rather than leaving a live-sounding threat statement.
17. **`docs/superpowers/specs/2026-06-04-auto-enroll-mode-design.md`** Non-Goals - a dated amendment
    that hostname takeover is now refused, and that per-host allowlisting remains the non-goal it was.
18. **`docs/backlog/bug-2026-08-15-registration-log-sites-are-outside-the-connection-budget.md`** - an
    amendment, not a closure: this slice adds a refusal path with **no** log line, deliberately, and
    the item's census should record it as a third category (counted rather than budgeted or unbudgeted).
19. **Check, do not assume:** README's list of what the ingest log budget covers and does not cover.
    This slice adds no log site, so it probably does not move - but the list was wrong in two ways at
    once once before, so the plan should read it rather than reason about it.

---

## 13. Scope

**In.**

- `internal/store/query/workers.sql`: one new statement (`InsertWorkerForAutoEnroll`), one corrected
  comment on `UpsertWorkerByHostname`, one amended comment on `SetWorkerAgentToken`. `make generate`.
- `internal/worker/handler.go`: `autoEnrollAndRegister` (the create-only guard, the ceiling, the
  unified refusal), `enrollAndRegister` (the live-credential guard inside its transaction), the
  sentinel rename, the refusal counters field and accessor, the `AutoEnrollWorkerCeiling` field and its
  resolver, and the prose of section 12.
- `cmd/relay-server`: the env parse, the startup bounds line, the counters wiring.
- `internal/api`: the `auto_enroll` counters section and its `CounterSources` field (or the reduced
  form of section 7, decided at plan time).
- `internal/agent/messages.go`: the corrected token-less failure message.
- The fixture work of 8.1 and the tests of section 10, including the rewrite of
  `TestConnect_AutoEnrollRotatesTokenForExistingHost`.
- `README.md`: section 12 sites 1-6.

**Out, explicitly.**

- **The CIDR allowlist.** `idea-2026-06-04-cidr-allowlist-auto-enroll` stays open, unabsorbed,
  unamended except for a cross-reference if the plan has a cheap place for one.
- **The TTL reaper.** Its own item (16.1). This slice bounds creation; it removes nothing already
  created. A deployment that has already been hit is helped by `revoke`, manually, and by nothing else.
- **Hostname validation, normalization or length limits.** Item (16.2).
- **Hardware-spec refresh for an existing worker.** Already absent on the reconnect path; not this
  slice's subject (5.1).
- **Moving the ingest log limiter's allocation site.** `bug-2026-08-15-registration-log-sites...`
  stays open; this slice avoids the problem rather than solving it.
- **`MaxConnectionAge` / revoked-credential lifetime.** Untouched.
- **Any change to `UpsertWorkerByHostname`'s SQL**, `ConsumeAgentEnrollment`, `ClearWorkerAgentToken`,
  `GetWorkerByAgentTokenHash`, or any migration.
- **Any file under `web/`.**

---

## 14. Decisions taken autonomously

Gate mode is autonomous; each of these would otherwise have been a question.

- **D1. Auto-enroll refuses on "a row exists", not on "the hash is non-NULL".** Section 5.1, R1. Would
  have escalated: it contradicts item 1's stated proposal. Called on the evidence that the two are
  production-equivalent (2.3), that the wider spelling fails closed on future states, and that it
  removes a status deny-list.
- **D2. Item 1's acceptance criterion 2 is struck as a request for a regression.** R2. Would have
  escalated: it changes the item's own Done-When. Called: auto-enroll does not and must not revive a
  revoked worker; the enrollment-token path is the recovery route.
- **D3. A revoke-then-re-enroll requirement for a lost state directory is the intended behaviour.**
  Section 5.1. The item asked for this to be decided; called yes, because the refused case and the
  attack are literally the same request, and because the remedy is an existing non-destructive admin
  action.
- **D4. The enrollment-token path gets a DIFFERENT guard from auto-enroll.** Section 5.4. Would have
  escalated: item 1 leaves it open. Called on a mechanical argument rather than a judgement - the
  symmetric guard would block its own recovery route and leave only `delete`, which destroys
  assignments and reservations.
- **D5. A new SQL statement rather than a Go predicate**, costing one `make generate`. Section 5.2.
  Would have escalated as a scope extension. Called: it closes a concurrent-first-boot TOCTOU the Go
  predicate leaves open, and it leaves `UpsertWorkerByHostname` untouched, which is item 2's explicit
  constraint. The conservative fallback is named in 5.2 if generate proves troublesome.
- **D6. `worker revoked` is deleted and folded into the generic refusal.** Section 5.3, R6. Would have
  escalated: it changes an existing user-visible message. Called: it is a live hostname-state oracle,
  no test asserts it, and both items require the new refusal to be indistinguishable.
- **D7. A total ceiling, not a rate limit, not a reaper, not an allowlist.** Section 6.2, R5. The item
  says explicitly that picking one is a product decision. Called: only the ceiling satisfies the item's
  own "stated bound" criterion; the other three do not bound the total at all.
- **D8. `RELAY_AUTO_ENROLL_WORKER_CEILING = 1024`, `0` disables.** Section 6.3. Derived from
  `RELAY_GRPC_MAX_CONNS` as the nearest anchor, **with the derivation's weakness written into the doc
  comment** rather than smoothed over: the two knobs count different quantities.
- **D9. The ceiling gates the auto-enroll path only, and that is the operator story.** Section 6.4.
  Would have escalated as a design decision. Called: it is what makes the denial-primitive objection
  answerable without a reaper, and it matches what README already tells operators to do on an
  untrusted network.
- **D10. The bound is approximate and README says so.** Section 6.2. Called: exactness would cost
  serializable isolation on a hot path for an overshoot bounded by `RELAY_GRPC_MAX_CONNS`.
- **D11. Refusals are counted, never logged; the success audit line survives unchanged.** Section 7.
  Would have escalated: it is the item's explicit "settle this". Called: no new log site at all is a
  stronger outcome than a budgeted one, it needs no new `logKind`, and the asymmetry with the audit
  line has a precise argument.
- **D12. Counters go to `GET /v1/server/counters`, not to a periodic log summary.** Section 7. A
  deliberate departure from `netlimit`'s precedent, with the reason stated. The reduced form is named
  as a plan-time fallback if the payload checklist outweighs the guards.
- **D13. The fixture item is folded in and recommended for closure, with its `errWorkerRevoked`
  criterion reshaped rather than met.** Section 8.2. Would have escalated: closing an item on a
  criterion that ceased to exist is exactly the silent-side-effect closure the brief forbids. Called:
  fold in, close, and write the reshaping into the Resolution note.
- **D14. `TestConnect_AutoEnrollRotatesTokenForExistingHost` is rewritten as required scope**, named in
  the plan's step list. R7, section 10.2. Would have escalated: it inverts an existing test's
  assertions. Called: the test asserts the defect, so inverting it is the deliverable.

---

## 15. Acceptance criteria

Mapped to the two items' own criteria, with the amendments called out.

1. Auto-enroll naming a hostname that already has a `workers` row is refused, the existing row's
   `agent_token_hash` is byte-identical afterwards, and the original agent's token still
   authenticates - proven by T3, RED at HEAD in both lanes. *(Item 1, criterion 1.)*
2. **Item 1's criterion 2 is STRUCK** (R2) and replaced: a revoked worker's hostname stays refused on
   auto-enroll (`TestConnect_AutoEnrollRefusesRevokedWorker` green, unedited) and stays revivable by an
   admin-issued enrollment token (`TestConnect_EnrollmentTokenRevivesRevokedWorker` green, unedited).
3. A brand-new hostname still auto-enrolls, mints a real agent token, and the fixture's scrub fires -
   `tokensSent() == 1` (T2). *(Item 1, criterion 3, plus the fixture item's fifth.)*
4. The enrollment-token path refuses a hostname whose worker holds a live credential and still binds to
   one whose credential is NULL, with a test pinning each (T7, T8). The new predicate sits inside the
   existing transaction, after a `FOR UPDATE` lookup. *(Item 1, criterion 4 and its third
   "settle these".)*
5. Both refusals return `codes.Unauthenticated, "authentication failed"`, equal to what
   `reconnectAndRegister` returns for an unknown token, and disclose nothing about the hostname beyond
   the refusal itself; `worker revoked` no longer exists (T4, RED at HEAD). *(Item 1, criterion 5,
   amended: the item asked for the refusal to be **logged** with hostname and remote address, and
   criterion 6 below is why it is not.)*
6. **Item 1's criterion 5 is AMENDED: the refusal is COUNTED, not logged.** No `log.Printf` is added
   anywhere on either refusal path, proven by T11 asserting the captured log is empty. The counters are
   published under `auto_enroll` split by three reasons (T13, M15). *(Item 2's third "settle this",
   answered in the direction that adds no site at all.)*
7. Under `RELAY_ALLOW_AUTO_ENROLL=true`, a caller registering with fresh hostnames stops creating rows
   at `RELAY_AUTO_ENROLL_WORKER_CEILING` (default 1024, `0` disables), proven by T5, RED at HEAD, with
   T6 pinning the boundary. The stated bound is `ceiling + RELAY_GRPC_MAX_CONNS`, not `ceiling`.
   *(Item 2, criterion 1, with the arithmetic made honest.)*
8. The enrollment-token path is unaffected by the ceiling - one valid token still creates exactly one
   row above the ceiling (T10) - and `UpsertWorkerByHostname` is unchanged, with the plan recording its
   caller count from a search rather than asserting uniqueness. *(Item 2, criterion 2.)*
9. `reconnectAndRegister` still creates no rows and is refused by neither guard (T9). *(Item 2,
   criterion 3.)*
10. An unconditional startup line names the effective ceiling and says explicitly when it is disabled;
    `0`, negative and unparseable values follow `parseWatchdogDuration`'s three-outcome shape and never
    `log.Fatalf`.
11. README's auto-enrollment section states the create-only rule, the revoke-then-re-enroll recovery,
    the ceiling with its operator story, and no longer contains any of the six falsified sentences of
    section 12.5 - including **"Nothing bounds the total."** *(Item 1 criterion 6, item 2 criteria 5
    and 6.)*
12. `authFailureMessage`'s token-less arm names all three causes and both remedies.
13. Every prose site of section 12 is corrected, each verified by reading the file rather than by
    reasoning about where the text lives.
14. The default lane drives both enrollment paths to a successful return and to each refusal;
    `docs/backlog/idea-2026-08-25-default-lane-fixture-for-the-enrollment-paths.md` closes via
    `/backlog close`, with the `errWorkerRevoked` reshaping in its Resolution note.
15. `make test` green; `go vet` and `go vet -tags integration` clean; the integration lane compiles and,
    where a Docker host is available, passes with exactly one intentionally rewritten test
    (10.2). Any other existing test whose result changes is reported, not fixed.
16. Both bug items close via `/backlog close`, and their files `git mv` to `docs/backlog/closed/`.

---

## 16. Known limitations and backlog recommendations

**Known limitations, stated rather than discovered later.**

- **The ceiling bounds row COUNT, not row SIZE.** `reg.Hostname` is caller-supplied and validated
  nowhere, bounded only by gRPC's 4 MiB receive limit (the function's own comment says so at
  `handler.go:605-607`). *Unverified:* Postgres' btree index entry limit (~2704 bytes) may already
  reject a very long hostname on the `hostname TEXT NOT NULL UNIQUE` index
  (`internal/store/migrations/000001_initial.up.sql:25`), in which case `autoEnrollAndRegister` returns
  the raw `txErr` to the caller (`handler.go:602`) - a possible Postgres-error disclosure. Neither the
  limit nor the disclosure was verified in this pass. Item 16.2.
- **The ceiling helps no deployment that has already been hit.** `revoke` frees budget manually and is
  the only remedy this slice ships. Item 16.1.

**Added 2026-08-25, Phase 4.** Three limitations this section did not have, all found in review.

- **The ceiling bounds NON-REVOKED rows, and remedy 1 is what makes the gap reachable.** Revoking keeps
  the row, so under an active attacker the operator revokes the junk, the attacker creates more under
  NEW hostnames (the old ones stay claimed), and the table grows without bound in the revoked bucket
  while `CountWorkers` sits flat. The stated bound is honest about `CountWorkers`; it was not honest
  about the table. README now says both, and `0` has been demoted out of the remedy ladder - a climbing
  `fleet_at_ceiling` is exactly the signal an attacker produces, and disabling the bound is exactly what
  that attacker wants.
- **A FIRST BOOT THAT NEVER COMPLETES CLAIMS THE HOSTNAME PERMANENTLY, and this is a cost the slice
  CREATES rather than one it inherits.** `autoEnrollAndRegister` commits the row and the minted
  `agent_token_hash` before the `RegisterResponse` is sent and before the agent persists it. If the
  stream dies in that window, or `creds.Persist` fails on a read-only or full state directory, the
  hostname is claimed with a live credential the agent never received - refused thereafter by
  auto-enroll (a row exists) AND by the enrollment-token path (the credential is live). Section 5.1's
  cost list names only "an agent that loses its state directory but keeps its hostname"; this is a
  machine that never registered at all. **Before this slice the retry self-healed**, because the upsert
  rotated the token, so closing the takeover is what makes it permanent. DISCLOSED, NOT CLOSED, in this
  slice: closing it properly needs a server-side notion of an unconfirmed first registration - the row
  is not really the agent's until the agent has the token - which is a design question of its own and
  is out of scope here.
- **`fleet_at_ceiling` aliases the other two reasons at capacity.** The ceiling check precedes the
  insert, deliberately, so that a refused auto-enroll writes nothing; the consequence is that at
  capacity every token-less refusal is attributed to the ceiling and `hostname_claimed_total` goes flat
  exactly when an operator starts triaging. The ordering is correct and stays; the aliasing is
  disclosed where the signal is read.
- **The refusal is a hostname-existence oracle by construction** (5.3). Closing it would mean refusing
  everything.
- **A legitimately refused agent produces no server-side line naming it** (section 7). The agent's own
  exit message is the naming signal.
- **`FOR UPDATE` on a fresh hostname locks nothing**, so the enrollment path retains a
  two-admin-tokens-race on a brand-new hostname. Out of the threat model; disclosed.
- **M10 is a known survivor** (moving the enrollment lookup outside its transaction is not
  behaviourally detectable in the default lane). Recorded so nobody later "fixes" it, and so that a
  future reader knows a comment - not a check - is what holds that position.

**Proposed, NOT filed** - the human accepts or rejects each:

1. **`idea`: reap auto-enrolled worker rows that never reconnected**, priority `medium`. The complement
   the ceiling needs, and the one option that helps deployments already hit. Its design questions are
   real and are why it is not in this slice: nothing in the schema records which path created a row
   (`connection_epoch <= 1 AND status != 'online'` is the nearest available proxy and it also catches
   token-enrolled machines that never returned), and deleting a worker destroys its assignments and
   reservations, so a reaper is a destructive background job that needs its own blast-radius argument.
   Should also decide `revoke` versus `delete` as the reaper's action - `revoke` frees ceiling budget,
   is non-destructive, and is what an operator does by hand today.
2. **`bug` or `idea`: `reg.Hostname` has no length or charset bound, and the unique-index behaviour on
   an oversized one is unverified**, priority `low`. Named in this spec's known limitations with the
   two specific things to check: whether the btree limit rejects it, and whether
   `autoEnrollAndRegister:602` returns a raw Postgres error to an unauthenticated caller.
3. **Amendment, not a new item**, to
   `bug-2026-08-15-registration-log-sites-are-outside-the-connection-budget`: this slice adds a
   refusal path with no log line by decision, which is a third category its two-class framing has no
   slot for (section 12.18).
4. **Amendment, not a closure**, to `idea-2026-06-04-cidr-allowlist-auto-enroll`: it remains the answer
   for an operator who can enumerate their networks, and it now sits on top of a create-only,
   ceilinged auto-enroll rather than an unbounded one.
