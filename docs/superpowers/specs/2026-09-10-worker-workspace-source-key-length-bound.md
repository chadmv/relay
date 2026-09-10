---
date: 2026-09-10
topic: worker-workspace-source-key-length-bound
item: docs/backlog/idea-2026-09-04-worker-workspaces-source-key-is-unbounded-in-a-primary-key.md
lane: W, slice 1 of 2
status: draft, pending human review
---

# One validating constructor between the inventory wire and `worker_workspaces`

## Status of this document

Written by the TPM agent from tree evidence. The brainstorming flow's one-question-at-a-time dialogue
was not available in a subagent session, so every design question below is resolved against the tree
and each answer carries the evidence that settled it.

**Nothing here was measured by running it.** The Bash tool was disabled in the authoring session.
Section 11 says, per figure, which are read from the tree, which are re-derived arithmetic, and which
are quoted from a document without verification. Section 10 makes one observation a precondition of
merge (plan Task 0) and says exactly what it can and cannot change.

Sibling slice, deliberately not specified here:
`docs/backlog/idea-2026-09-04-a-job-author-controls-how-many-p4-clients-each-agent-creates.md` - the
COUNT axis on the same table, landing in the same transaction. Section 12 says what this slice leaves
to it and what this design does to avoid foreclosing it.

## 1. The slice, in one paragraph

One new unexported constructor in `internal/worker` that takes a `*relayv1.WorkspaceInventoryUpdate`
and returns either a `store.UpsertWorkerWorkspaceParams` or an error. It is the only route by which
either of the package's two writers builds those params. It refuses a row whose agent-supplied
strings are over length, carry a NUL, or whose timestamp does not parse. A refused row is dropped
from its batch and counted; it is never logged with its value, and it never fails the surrounding
transaction. One migration adds a `NOT VALID` `CHECK` behind the Go check as the backstop for a
writer that does not exist yet. One README sentence. No new environment variable, no change to any
handler signature, no change to any statement in `internal/store/query/worker_workspaces.sql`.

## 2. What the item gets right, and the six things this spec refutes

The item was read once for self-contradiction, contradiction with the tree, and prescription of
things that do not exist, before any design was written.

### Confirmed against the tree

- `applyInventory` (`internal/worker/handler.go:2195`) passes `u.SourceKey` straight into
  `store.UpsertWorkerWorkspaceParams` with no check. True.
- `applyInventoryUpdate` (`:2222`) does the same on both arms. True.
- `source_key` is inside the table's primary key: `PRIMARY KEY (worker_id, source_type, source_key)`
  (`internal/store/migrations/000007_workspaces.up.sql:12`). True.
- Nothing bounds its length. `jobspec.validateSourceSpec` requires a `//` prefix and refuses control
  bytes and checks nothing about length, on either `s.Stream` or any `sync[i].path`
  (`internal/jobspec/jobspec.go:534-632`). True, and it is the fact section 4 turns on.
- These two functions are the **only** production writers to `worker_workspaces`. A search for
  `UpsertWorkerWorkspace|DeleteWorkerWorkspace|ReplaceWorkerInventory` across `*.go` returns, outside
  generated `internal/store/*.sql.go`, exactly these two functions plus test fixtures in
  `internal/worker`, `internal/api`, `internal/scheduler` and `internal/store`. Recorded as a hit
  count over a shape rather than as a uniqueness claim, per this project's rule; re-run it before
  relying on it.

### Refuted or corrected

1. **Failing the batch does not erase the inventory. It freezes it, which is worse in a specific
   way.** The framing this slice was handed says one bad row "erases a worker's entire inventory"
   because `applyInventory`'s `ReplaceWorkerInventory` DELETE has already run. That is wrong.
   `pgx.BeginTxFunc` rolls back when the closure returns an error, so the DELETE is undone with
   everything else and the worker keeps the rows it had before. `fakeTx`'s own comment in
   `handler_register_success_test.go` records this as measured mutation M15. The real harm is a
   **permanent, self-sustaining desync**: an agent that reports the same bad row on every
   registration never updates its inventory again, so the dispatcher's warm bias keeps scoring that
   worker on workspaces it may no longer hold, forever, with no error anywhere. This strengthens the
   drop-the-row choice rather than weakening it, and section 6 uses this corrected version.

2. **Bounding `source_key` alone does not close "an agent value fails the index".** The item's title
   scopes the fix narrower than its own diagnosis. There is a second index:
   `CREATE INDEX worker_workspaces_lookup_idx ON worker_workspaces (source_type, source_key,
   baseline_hash)` (`000007_workspaces.up.sql:14-15`). So `baseline_hash` reaches an index too, and
   `source_type` reaches both it and the primary key. All three are `TEXT NOT NULL` and all three
   come off the same wire message. Section 7 enumerates them by name.

3. **`sourcekey.go`'s "KEEP IT SHORT" comment is accurate and the inference from it is not.**
   `TestSourceKey_IsBoundedAtTwentyBytesOverTheStream` asserts
   `len(SourceKey(...)) == len("//s/x")+20` for sixteen 200-byte exclusions. What it pins is that the
   composite form's **overhead** is exactly 20 bytes (`"x1|"` = 3, sixteen hex = 16, `"|"` = 1); the
   key itself is `overhead + len(stream)` for the composite form and `len(stream)` for the bare form.
   Since nothing bounds the stream, the key is unbounded in both forms. The item's Context says the
   exclusion design "deliberately chose a short fixed-width key format partly to stay clear of this";
   that is true of the **digest** and false of the **key**. Not a defect in the comment - a defect in
   reading it, which is exactly why the item exists, and worth writing down so the implementer does
   not open that test expecting a bound.

4. **"The same shape as the unvalidated hostname" is half right, and the differing half is the half
   that decides the design.** Shared: an unvalidated wire string landing in a btree, where over-long
   fails the index rather than conflicting. Different, and decisive: `reg.Hostname` is reachable
   **pre-authentication** under `RELAY_ALLOW_AUTO_ENROLL` and its refusal must be indistinguishable
   from every other credential refusal (the `msgAuthFailed` constant and its AST guard); this value
   arrives from an **already-authenticated** worker, and every statement it feeds is scoped to
   `workerID`, which `finishRegister` resolved from the credential and never read off the wire. So
   the blast radius is the sender's own rows and there is no oracle to preserve. The two items
   therefore share a **rule** and not a **mechanism**. Section 9 states the rule the hostname item
   should adopt.

5. **The item names one writer in Related and the other only obliquely in Summary.** Related says
   `internal/worker/` (`applyInventoryUpdate`); Summary says "the registration-time bulk ingest",
   which is `applyInventory`. Both must be named or an implementer patches one and leaves the other,
   which is the larger of the two: `applyInventory` is the loop over `reg.Inventory`.

6. **This lane's brief asks whether an integration-tagged guard here can run. It can, and CLAUDE.md's
   "a guard behind a build tag must be able to run" is satisfied on the second branch, not the
   third.** `.github/workflows/go-ci.yml`'s `pg-integration` job runs `make test-pg-integration`
   (`go-ci.yml:128, 183-184`), and that target's package list includes `./internal/worker/...`
   (`Makefile:168`). So an `//go:build integration` test in `internal/worker` runs on every push.
   Section 15 still pushes most of the battery into the untagged lane, for a reason that is about
   what each lane can prove rather than about whether it runs.

## 3. The invariant this is an instance of, and the shape the fix must take

CLAUDE.md's **Single JSON entry point** says request bodies are read only via `readJSON`, so
size limits and decode policy live at one function rather than at call sites. This is the same
problem one layer down and on a different wire: two call sites each build a params struct out of an
agent-supplied message, and any policy about what is storable has to live at one of them or at both.
Both is how the second one drifts.

**The chokepoint is therefore a constructor, not a predicate.** It returns the params struct:

```
func inventoryUpsertParams(workerID pgtype.UUID, u *relayv1.WorkspaceInventoryUpdate) (store.UpsertWorkerWorkspaceParams, error)
```

A predicate (`ok := storable(u)`) leaves the struct literal at the call site, so a future writer can
build the params without calling it and nothing objects. A constructor makes the check and the
construction one statement, which is the same instinct as `finishRegister`'s "take the state and arm
its release in the same breath". Both existing writers become:

```
p, err := inventoryUpsertParams(workerID, u)
if err != nil { /* per-site handling, section 6 */ }
... q.UpsertWorkerWorkspace(ctx, p)
```

**It refuses; it never transforms.** `sanitizeAgentErrorMessage` sits in the same file and strips
NULs and truncates - and it must **not** be reused or imitated here, for a reason that is specific
rather than stylistic. That value is human-readable prose, where losing a byte loses a byte.
`source_key` is an **identity**: truncating it, or stripping a byte from it, makes two distinct
workspaces canonicalise onto one primary-key row. That is precisely the poisoning hazard
`SourceKey`'s own NUL set-terminator exists to prevent (`sourcekey.go:64-68`), re-created at the
coordinator by a sanitiser instead of at the agent by a missing separator. The constructor's doc
comment must carry this sentence, because "there is already a sanitiser in this file" is the obvious
and wrong thing to do next.

## 4. The number, and what it is a bound ON

### Bytes, not runes

Three reasons, in order of weight:

1. **The failure being closed is a byte failure.** A btree index entry has a byte limit; the number
   of runes in the value is not what Postgres compares against it.
2. **The Go bound and the SQL `CHECK` must be the same bound.** SQL's `octet_length()` and Go's
   `len()` on a string are the same measure. A rune bound in Go (`utf8.RuneCountInString`) against
   an `octet_length` `CHECK` would be two implementations of one policy that agree on ASCII and
   disagree on everything else, which is one disagreement away from a `CHECK` violation reaching a
   caller that thought it had already refused the row.
3. **The value's meaning to `p4` is bytes.** A depot path is a byte string on the filespec.

Invalid UTF-8 arriving whole is not reachable here and the comment must say so rather than guard it:
`proto.Unmarshal` rejects a proto3 string field carrying invalid UTF-8 and the stream dies before
`Connect`'s message loop sees it - the same argument `sanitizeAgentErrorMessage`'s comment already
makes for its own third transform. A **NUL** is a different case: it is legal in a proto3 string and
illegal in `TEXT` (SQLSTATE 22021), and `handleInventoryUpdate`'s comment already names it as live.
Section 5 puts it in the constructor.

### The bound is on the KEY, which makes it a bound on the STREAM

`SourceKey` returns `stream` when nothing is excluded and `"x1|" + 16 hex + "|" + stream` otherwise
(`sourcekey.go:52-70`). The composite overhead is exactly 20 bytes. So a bound of `N` on the key is a
bound of `N` on a bare stream and `N - 20` on a stream that carries any exclusion, and an author who
adds one exclusion to a spec that was inside the bound can push it outside. That asymmetry is real,
is 20 bytes wide, and belongs in the README sentence rather than being engineered away.

### The derivation is top-down from the index, and the bottom-up check is Task 0

**Proposed: `source_key` 512 bytes, `source_type` 64, `short_id` 128, `baseline_hash` 128.**

Top-down. The three columns of `worker_workspaces_lookup_idx` share one index entry, so what has to
fit is their **sum** plus tuple overhead, not each one separately - this is the per-axis-bounds trap,
and the arithmetic is written out for that reason. 512 + 64 + 128 = 704 bytes of data against a
quoted btree entry limit of about 2704, leaving roughly a factor of four in headroom for tuple
overhead and for a future column added to that index. The primary key's entry is smaller
(16 for the UUID + 64 + 512 = 592). Neither can be failed by any accepted row.

Bottom-up, stated as a sanity check and not as a derivation. README's own example stream is
`//depot/film-x/main`, 19 bytes; 512 is about 26 times that. `sanitizeHostname` caps at 32 and
`allocateShortID` returns a truncated lowercase base32 of a SHA-256, so at most 52 characters - 128
clears both with room. `BaselineHash` is documented as a 16-character hash and may be the empty
string on the cold branch; 128 clears it. `source_type` has exactly one producer today, the literal
`"perforce"`; 64 clears any plausible successor.

**512 is a power of two, and that is legibility, not derivation.** Any value between roughly 256 and
1024 satisfies both directions. Say so rather than implying the number fell out of an equation.

**What is deliberately NOT done: a vocabulary allow-list on `source_type`.** CLAUDE.md's status-
predicate rule argues for allow-lists because they fail closed on a value added later. That
reasoning does not transfer: a new provider is introduced by a coordinated change on both sides, and
an allow-list here would fail closed by **silently discarding a new provider's entire inventory**,
with the counter as the only signal. The hazard being closed is length, not vocabulary. A vocabulary
check is a defensible separate item and is named in section 13.

## 5. What the constructor refuses, and why it is three predicates and not one

Every predicate below answers one question - "can this row be stored at all" - and the batch-survival
property in section 6 is only true if all of them are covered. Documenting a partial fix as though it
were total is where partiality becomes a lie, so the set is closed here rather than in a follow-up.

| Predicate | Column(s) | Why it is here |
|---|---|---|
| `len(...) > max` | `source_key`, `source_type`, `short_id`, `baseline_hash` | The item. Two of these reach the primary key, three reach `worker_workspaces_lookup_idx`, one reaches neither (section 7). |
| NUL present | the same four | Legal in proto3, illegal in `TEXT` (SQLSTATE 22021). `handleInventoryUpdate`'s comment already names this as live and unbudgeted. |
| `time.Parse(time.RFC3339, u.LastUsedAt)` fails, or yields the zero time | `last_used_at` | `applyInventory` and `applyInventoryUpdate` both discard this error today, binding SQL NULL into a `NOT NULL` column. The same comment names it. |

**The timestamp arm is a behaviour change and must be called out in the PR.** Today a blank or
unparseable `LastUsedAt` fails the statement, which on the batch path rolls the whole transaction
back and on the single-message path spends one budgeted log line. After this slice it drops one row,
increments the counter and commits the rest. That is the intended direction, and it is the reason
the property "one malformed row cannot freeze a worker's inventory" is true rather than nearly true.
**If the human wants the slice narrower, this is the arm to cut** - and cutting it means the README
sentence and the constructor's comment must say the property holds for over-length rows only.

Note that an honest agent never sends a blank string here: `buildRegisterRequest` formats with
`e.LastUsedAt.Format("2006-01-02T15:04:05Z07:00")` (`internal/agent/agent.go:381`). A **zero**
`time.Time` formats as `0001-01-01T00:00:00Z`, which parses and then trips `ts.IsZero()`, so the
reachable honest case is a registry entry with an unset timestamp, not a malformed string.

## 6. What happens when the bound is exceeded

**Decision: the row is dropped from its batch, the batch continues and commits, the refusal is
counted, and nothing about the row is logged.** Four parts, each with its own argument.

### Drop the row, do not fail the batch

From refutation 1: failing the batch rolls back `ReplaceWorkerInventory` too, so the worker keeps its
previous rows and stops tracking reality for as long as the agent keeps reporting the bad row - which
is every registration, forever. Dropping the row keeps every other row accurate. The cost is that the
dropped workspace is invisible to the coordinator, which is section 10's degradation and is bounded
to that one workspace.

`applyInventory`'s call site in `finishRegister` already logs and continues on error rather than
failing registration (`handler.go:989-991`), and `handler_handoff_guard_test.go:331` pins that
log-and-continue shape by name. This slice does not change it, and must not: with per-row drops,
`applyInventory` now returns an error only for a genuine store fault, which is exactly what that
log line is for.

### Count it

A new `atomic.Uint64` on `Handler`, read through an exported method, in the same family as
`taskLogFenceRejects`. It answers one question an operator cannot otherwise ask: is this control
inert in production, or is it silently discarding rows? A control that has never fired and a control
that fires constantly are the same code with opposite meanings.

### Do not log it, and do not make it operator-tunable

CLAUDE.md's identity-is-not-honesty rule asks two things of a new signal a peer can move.

**What does a peer who can move this signal gain, and is its documented remedy in their favour?** The
peer is an authenticated agent, and it can drive the counter at one increment per inventory entry per
message, unbudgeted. The obvious remedy an operator would reach for on a climbing counter is "the
bound is too tight, raise it" - and **that remedy is the attack**. So:

- **There is deliberately no environment variable.** The four maxima are compile-time constants with
  no knob, following `ingestLogLimiter`'s constants ("THERE IS DELIBERATELY NO ENV KNOB: an operator
  raising the budget re-opens the vector this type exists to close"). An operator who genuinely needs
  a longer stream changes a constant and rebuilds, which is a visible diff.
- **The remedy ladder, wherever the counter is documented, must not contain "raise the bound" and
  must not contain a disable value.** A remedy list is part of the advertisement. The remedy is to
  find which agent is sending malformed inventory.
- **No log line carries the value.** `handleInventoryUpdate`'s comment already says "Never log `u`
  itself - `source_key` is a caller-supplied, unbounded depot path", and this slice makes that
  sentence stronger rather than weaker: after it, an over-long key is refused before any statement,
  so the store error that used to be the only way it surfaced no longer exists either.

**Pin the gate the new signal leans on.** The counter is attributable only to "some agent", and the
thing that keeps even that much true is that `workerID` is resolved at registration from the
credential and never taken from the wire. That is already true of every statement on this path and is
not weakened here; the constructor takes `workerID` as a parameter and never reads an identity out
of `u`. The guard is test 6 in section 15: an accepted batch must move the counter by **zero**, which
is what fails if the increment is put in the wrong branch.

### Publishing on `GET /v1/server/counters` is deferred, on precedent

The counter lives in `internal/worker` with its reader method and is **not** wired to
`GET /v1/server/counters` in this slice. `enrollmentRefusals` is already exactly this - "NOT YET ON
GET /v1/server/counters - the section is deliberately deferred to its own item" (`handler.go:429`) -
so the deferral is the established shape here rather than a dodge. It also keeps this lane out of
`cmd/relay-server` and `internal/api` entirely while three sibling lanes are running. Section 13
proposes the follow-up item, and it should be the same item that finally publishes
`enrollmentRefusals`.

### Blast radius, stated

Every statement on this path is scoped to one `workerID`. A worker that floods this control degrades
its own inventory and moves one process-wide number. It cannot drop, freeze or corrupt another
worker's rows. That is the sentence that makes "drop silently" acceptable here and would not make it
acceptable on a shared table.

## 7. Which other agent-supplied values reach a key or an index

Enumerated by name, per the brief. `worker_workspaces` has exactly six columns.

| Column | Agent-supplied? | In the primary key? | In `worker_workspaces_lookup_idx`? | Disposition |
|---|---|---|---|---|
| `worker_id` | **No** - `pgtype.UUID` resolved at registration from the credential | Yes | No | Out of scope. Not caller-controlled; this is the fact section 6's blast-radius argument rests on. |
| `source_type` | Yes (`u.SourceType`) | **Yes** | **Yes** | **In scope.** Bounded at 64 bytes. Length only; no vocabulary allow-list (section 4). |
| `source_key` | Yes (`u.SourceKey`) | **Yes** | **Yes** | **In scope.** The item. Bounded at 512 bytes. |
| `short_id` | Yes (`u.ShortId`) | No | No | **In scope anyway**, bounded at 128 bytes. It reaches no index, so it cannot produce the item's failure. It is stored per row and echoed to admins by `GET /v1/workers/{id}/workspaces` (`internal/api/workspaces.go:35`), and it is the value `handleEvictWorkerWorkspace` matches on, so an unbounded one is a row-size and response-size cost rather than an index failure. Bounding it costs one line at a chokepoint that already exists; say plainly in the comment that this one is not the item's hazard. |
| `baseline_hash` | Yes (`u.BaselineHash`) | No | **Yes** | **In scope.** Bounded at 128 bytes. Without it, the claim "no accepted row can fail an index" is false, since the lookup index's entry is the sum of three columns. |
| `last_used_at` | Yes (`u.LastUsedAt`, as an RFC3339 string) | No | No | **In scope**, as the timestamp arm of section 5. Not a length or index question; it is the other way this table's writers turn one bad row into a rolled-back batch. |

`u.Deleted` is a bool and reaches nothing. There is no seventh field on
`relayv1.WorkspaceInventoryUpdate` at time of writing; the constructor's arity is what would notice a
new one, not this table.

## 8. The migration, and what retroactivity costs

**Decision: `ALTER TABLE worker_workspaces ADD CONSTRAINT ... CHECK (...) NOT VALID`, in a new
migration pair. No data is deleted and no existing row is validated.**

Two alternatives were considered and rejected, and the reasons are the design:

- **A plain validated `CHECK`.** `ADD CONSTRAINT ... CHECK` scans every existing row and **fails the
  migration** if any violates. Migrations are embedded and run on startup, so this turns a
  pre-existing over-long row into a **server that will not boot after upgrade**. And an over-long
  row is exactly what an attacker can plant today. A control whose deployment can be denied in
  advance by the data it exists to reject is the wrong control.
- **Validated `CHECK` preceded by `DELETE FROM worker_workspaces WHERE octet_length(source_key) > N`.**
  Defensible - this table is derived state, rebuilt in full by `ReplaceWorkerInventory` on every
  agent registration - but it puts a destructive statement in a startup migration to solve a problem
  `NOT VALID` solves with no statement at all, and its down migration is not a true inverse.

`NOT VALID` gets the whole defence for future writes with none of that: Postgres enforces a
`NOT VALID` `CHECK` on every subsequent insert **and update**, and only skips the backfill scan. The
pre-existing violating set then drains on its own, because every agent reconnect runs
`ReplaceWorkerInventory` and re-reports through the now-bounded constructor. A later
`VALIDATE CONSTRAINT` is an optional operator action, is not part of this slice, and may fail while
rows from a never-reconnecting worker remain - say that in the migration's own comment so nobody runs
it expecting success.

**What the constraint buys over the Go check, stated honestly.** Nothing today: section 2 records
that the two functions this slice fixes are the only production writers. It buys the writer who does
not exist yet - a sweeper statement, an eviction path, a second provider - failing closed instead of
failing an index. That is also the reason this slice does **not** buy a `go/ast` guard forbidding a
direct `store.UpsertWorkerWorkspaceParams{...}` literal: the constructor is the seam, the constraint
is the backstop, and CLAUDE.md's own guidance is that the AST guard is the expensive fallback bought
after a guard has actually been evaded. Name the condition rather than pre-buying it: **if a third
production writer to this table appears, that is the argument for the AST guard.**

The `CHECK` covers `octet_length` on all four TEXT columns at the same four numbers. A NUL predicate
is **not** included - a NUL cannot be stored in `TEXT` at all, so the database already refuses it and
a redundant predicate would be a second statement of a rule Postgres owns.

The down migration drops the constraint. That is a true inverse.

## 9. The one answer this settles, for the hostname item to adopt

`ROADMAP.md:53` says this item and
`docs/backlog/bug-2026-08-25-hostname-is-unvalidated-and-reaches-a-unique-index.md` "should settle on
one answer". **That item is not in scope to fix here.** The answer this slice settles, stated so it
can be adopted verbatim rather than re-invented:

> **An agent-supplied string that reaches an index is bounded in BYTES, at a single Go constructor
> upstream of every statement that writes it, against a compile-time constant with no operator knob.
> The number is derived top-down from the index entry it must fit inside - summed across every column
> of that entry, not per column - and checked bottom-up against the honest producer's output, with at
> least an order of magnitude between them. The refusal is counted, never logged with its value, and
> no documented remedy for the counter widens the bound. A `NOT VALID` `CHECK` behind it is the
> backstop for future writers, never a validated one, because a validated constraint added by a
> startup migration lets planted data deny an upgrade.**

What the two items must **not** share is the over-bound behaviour, and the difference is principled
rather than incidental. Here the refusal drops one row from an authenticated worker's own batch,
because the blast radius is that worker's own rows and there is no observer to keep in the dark.
There the refusal is a **registration** refusal reachable pre-authentication, so it must join the
`msgAuthFailed` constant and its AST guard, and it must not become a new hostname-state oracle. Same
rule, different remedy, and the hostname item's own Proposal already asks the right questions about
its half.

## 10. Consequences of the bound being too tight, and the measurement this slice owes

### The degradation, which is the whole cost of getting the number wrong

`scheduler.SourceKeyFromAPISpec` computes the **same key** server-side from the job spec
(`internal/scheduler/source_proto.go:31`, and `TestSourceKeyFromAPISpec_DelegatesToThePerforceFunction`
pins that it delegates to `perforce.SourceKey`), and `ListWarmWorkspacesForKeys` joins it against
`worker_workspaces.source_key` for warm scoring (`internal/scheduler/dispatch.go:130`). So for a
stream long enough to exceed the bound:

- the agent still builds the workspace - **this slice deliberately does not mirror the bound into the
  agent** - so the task still runs and still syncs;
- the coordinator drops the inventory row, so the warm join never matches;
- every task on that stream dispatches **cold, forever, silently**, and the workspace never appears
  in `GET /v1/workers/{id}/workspaces`.

That is a performance regression with no error, which is why the number carries an order of
magnitude of margin and why mirroring the bound into the agent is explicitly out: an agent-side
refusal would convert this into a **task failure**, trading a silent slowdown for a loud outage on
input the operator chose legitimately.

### Plan Task 0 (required before the number is fixed)

Two observations, neither takeable in this session:

1. **The real btree index entry limit on the target Postgres.** The ~2704-byte figure in section 4 is
   quoted from the hostname backlog item and is unverified here. Cheapest instrument: insert a row
   with a deliberately enormous `source_key` against a scratch database and read the limit off the
   error message, which states it. Record the number and its Postgres version. If it is materially
   below 2704, re-run section 4's sum (704) against it before accepting 512.
2. **The longest `stream` value reachable in this environment.** Read from any real job spec, any
   `tasks.source` row, or the `p4d` test container's stream names - whichever exists. Record the
   number **with its input**, not as a bare figure. **Escalation rule: if any real stream exceeds 300
   bytes, stop and revisit the number before merging**, because 512 minus the 20-byte composite
   overhead leaves 492 and the margin argument stops holding. If no p4 environment is reachable, say
   so plainly in the PR and record the number as anchored top-down only.

**What Task 0 can and cannot change.** It cannot move the enforcement point, the drop-the-row
behaviour, the no-knob decision or the `NOT VALID` migration - none of those depend on a number. It
can move 512, and it is the only thing that may.

## 11. Provenance of every number

**Read directly from the tree in this session** (no execution):

- `PRIMARY KEY (worker_id, source_type, source_key)` and
  `worker_workspaces_lookup_idx (source_type, source_key, baseline_hash)` -
  `internal/store/migrations/000007_workspaces.up.sql:5-15`.
- All four TEXT columns `NOT NULL`, `last_used_at TIMESTAMPTZ NOT NULL` - same file.
- The two writers and the discarded `time.Parse` error - `internal/worker/handler.go:2195-2237`.
- `finishRegister` logs and continues on `applyInventory`'s error - `handler.go:989-991`.
- The composite key overhead of exactly 20 bytes - `sourcekey.go:70`, arithmetic on the literals;
  `sourcekey_test.go:90-99` asserts `len("//s/x")+20`.
- `validateSourceSpec` bounds no length on `Stream` or `sync[i].path` -
  `internal/jobspec/jobspec.go:534-632`.
- `maxSyncExclusions` bounds exclusions at 16 (README:589); that is a count, not a length.
- The agent formats `LastUsedAt` as RFC3339 without nanoseconds - `internal/agent/agent.go:381`.
- `allocateShortID` returns a truncated lowercase base32 of a SHA-256, so at most 52 characters -
  `internal/agent/source/perforce/perforce.go:866-877`.
- `sanitizeHostname` caps at 32 - same file, `:879-894`.
- `source_key` is echoed by `GET /v1/workers/{id}/workspaces` - `internal/api/workspaces.go:11-41`.
- Warm scoring joins on `source_key` - `internal/scheduler/dispatch.go:130`,
  `internal/store/query/worker_workspaces.sql:22-26`.
- `internal/worker` is in `test-pg-integration`'s package list - `Makefile:168`; that target is run by
  the `pg-integration` CI job - `.github/workflows/go-ci.yml:128, 183-184`.
- `fakePool` / `fakeTx` are untagged and record `Exec` statements with their args, and `commits` is
  the transaction-outcome discriminator - `internal/worker/handler_register_success_test.go:39-159`.
- The existing counter family and the deferred-publication precedent - `handler.go:385-431`.

**Re-derived arithmetic** (stated so it can be checked):

- Composite key length = `len(stream) + 20`. From `"x1|"` (3) + 16 hex + `"|"` (1).
- Maximum accepted `worker_workspaces_lookup_idx` entry payload = 512 + 64 + 128 = **704 bytes**.
- Maximum accepted primary-key entry payload = 16 (UUID) + 64 + 512 = **592 bytes**.
- Headroom factor against a 2704-byte limit: about **3.8x** on the lookup index.

**Quoted from a document, NOT verified in this session:**

- The ~2704-byte btree index entry limit -
  `docs/backlog/bug-2026-08-25-hostname-is-unvalidated-and-reaches-a-unique-index.md`. Plan Task 0
  measures it. **Do not carry this figure into a code comment** until it is measured.
- "`BaselineHash` returns a 16-char canonical hash" -
  `internal/agent/source/perforce/baseline.go:13`, read as prose, not computed.

**Judgements, with the reasoning in the section named:**

- 512 / 64 / 128 / 128 - section 4, top-down from the index sum, bottom-up against the producers.
- Drop the row rather than fail the batch - section 6, on refutation 1.
- `NOT VALID` rather than validated or delete-then-validate - section 8.

## 12. What this slice leaves to slice 2, deliberately

Slice 2 is
`docs/backlog/idea-2026-09-04-a-job-author-controls-how-many-p4-clients-each-agent-creates.md` - the
**count** axis on the same table, landing in the same transaction.

**What it owns and this slice does not touch:**

- **How many entries `applyInventory`'s loop may process.** This slice adds a per-**row** predicate
  and no count of any kind. The seam is left clean in both available places: a `len(inv)` guard ahead
  of `pgx.BeginTxFunc`, or a counter inside the `for`. Nothing here forecloses either, and the
  constructor returns per-row so a count bound composes with it rather than replacing it.
- **The per-agent ceiling on distinct source keys per stream, checked before `allocateShortID`**, in
  `internal/agent/source/perforce/perforce.go`. Untouched here.
- **What happens at that ceiling**, and its interaction with the sweeper - the item's own hardest
  question, and unaffected by anything in this design.
- **README line 599**, the "Exclusions change the workspace identity" disk paragraph. Slice 2's
  acceptance criterion names it explicitly ("The disk paragraph in README describes the
  author-controlled case"). Section 14 puts this slice's one sentence somewhere else for that reason.
- **Whether the p4 client spec must be created before the exclusion set is known to resolve.** Not a
  coordinator question at all.

**One thing slice 2 should know about this design.** After this slice, a row that slice 2's count
bound would refuse is a row this slice has already accepted or dropped individually. If slice 2
chooses to refuse a whole over-count batch rather than truncate it, it re-introduces exactly the
freeze described in refutation 1, and it should read that refutation before deciding. The counter
this slice adds counts **rows refused for content**; slice 2's ceiling is a different noun and needs
its own number, per the "distinct nouns" discipline the existing counters already follow.

## 13. Backlog items this spec proposes, for the human to accept

None are filed by this spec.

1. **Publish the deferred `internal/worker` counters on `GET /v1/server/counters`.** This slice adds
   the second unpublished counter (`enrollmentRefusals` is the first, deferred by name at
   `handler.go:429-430`). One item, one new section per noun, and the forgeability sentence stated
   where each number is READ. This is the natural moment to raise its priority, because "deferred to
   its own item" has now happened twice.
2. **Bound `source.stream` and `sync[i].path` lengths in `jobspec.validateSourceSpec`.** The natural
   complement: it refuses an over-long stream at submission with a 400 instead of silently losing the
   warm bias later. **It does not close this item and must not be filed as doing so** - the value
   this spec bounds arrives from the agent over gRPC, which can send any string regardless of what
   any spec said. `internal/jobspec/` is LANE S's file in this batch, so this must be a separate item
   for a lane that owns it.
3. **A vocabulary check on `source_type`.** Length is bounded here; the set is not. See section 4 for
   why an allow-list is the wrong reflex at this site and what it would have to solve instead.
4. **Mirror the key bound into the agent, or decide not to, once a second provider exists.** Section
   10 argues the mirror is wrong today because it converts a silent slowdown into a task failure.
   That balance changes if a provider ever produces keys near the bound legitimately.

## 14. Documentation

**One sentence, appended to the `stream` row of the source-field table in README's "Source
workspaces" section (README:588).** That row already says "Workspaces are keyed by stream, plus the
exclusion set when the spec has one", so the bound is a property of the thing that row describes.

It must carry, and nothing else:

- the key is bounded at 512 bytes;
- the composite form (any spec with an exclusion) adds a fixed 20 bytes, so a stream over 492 bytes
  is outside the bound as soon as one exclusion is added;
- the user-visible consequence: a workspace whose key exceeds it is not recorded in the coordinator's
  inventory, so tasks on that stream always dispatch cold and the workspace does not appear under
  `relay workers workspaces`. The task still runs.

**Nothing else in README changes.** No environment-table row (there is no variable). No counters
section (deferred, section 6). **Not line 599**, which slice 2 owns. Three sibling lanes are running
concurrently, so the collision surface is deliberately one line.

**Code comments this slice adds or edits**, and what each may and may not say:

- **The constructor's own doc comment.** The hazard and the constraint the code cannot show: that
  three of these strings reach a btree, that the value is an **identity** so it must be refused and
  never truncated or NUL-stripped, and that `sanitizeAgentErrorMessage` in the same file is
  deliberately not reused. It may cite the tests that pin those claims by name. It may **not** carry
  the derivation of 512 - that goes in the commit message and in this spec.
- **`handleInventoryUpdate`'s existing comment** becomes partly false: "every string in `u` is bound
  straight into `UpsertWorkerWorkspace` or `DeleteWorkerWorkspace`" and "no gate ahead of it" are
  both wrong after this slice. Rewrite the mechanism, and re-verify the surviving sentences while
  editing rather than assuming the untouched half is still true - correcting prose regenerates
  claims. The "Never log `u` itself" sentence stays and is strengthened, not deleted.
- **The three existing counter-field comments that carry ordinals** - "A DIFFERENT NOUN FROM
  `ingestDrops`" (`taskLogFenceRejects`), "A THIRD DISTINCT NOUN" (`statusFence`), "A FOURTH DISTINCT
  NOUN, and no input moves more than one of the four" (`enrollmentRefusals`). A fifth counter makes
  "the four" stale. **Delete the ordinals rather than bumping them to five**: a count of other fields
  is a count of code elsewhere, which CLAUDE.md forbids in a comment for exactly this reason. Keep
  the property - "no input moves more than one of these; do not sum them and do not merge the
  sections" - which is the part that is load-bearing.
- **The migration's own comment** carries why it is `NOT VALID` and that a later `VALIDATE
  CONSTRAINT` may fail while rows from a never-reconnecting worker remain.
- No new comment carries a date, a count of anything elsewhere, a uniqueness claim about other code,
  or the session's measurement provenance.

## 15. Testing

### Lane facts, verified in the tree

- `.github/workflows/go-ci.yml`'s `test` job runs `go test -race ./...` with no tags, so every
  untagged test in `internal/worker` runs on every push.
- The `pg-integration` job runs `make test-pg-integration`, whose package list includes
  `./internal/worker/...` (`Makefile:168`). **So an `//go:build integration` test in this package
  runs in CI**, and CLAUDE.md's "a guard behind a build tag must be able to run" is satisfied on its
  second branch. No test below needs the third branch's written excuse.
- `fakePool` and `fakeTx` (`handler_register_success_test.go:39-159`) are **untagged**, record every
  `Exec` with its args, and count commits and rollbacks. `applyInventory` is drivable with no
  Postgres.

### Default lane (untagged, runs in CI)

1. **`TestInventoryUpsertParams_TheBoundIsOnBytesNotRunes`.** Pure unit test over the constructor.
   Rows: a `source_key` of exactly 512 bytes built from multi-byte runes (accepted; under 512 in
   runes and at 512 in bytes), one of 513 bytes (refused), one of exactly 512 ASCII bytes (accepted),
   one of 513 ASCII (refused). The multi-byte rows are the discriminator: a `utf8.RuneCountInString`
   implementation accepts the 513-byte multi-byte input. The ASCII pair pins the off-by-one on the
   boundary itself.

2. **`TestApplyInventory_AnOverLongSourceKeyIsDroppedAndTheBatchCommits`.** Drives `applyInventory`
   through `fakePool`/`fakeTx` with three entries **in the order [bad, good1, good2]**. The bad entry
   goes **first** deliberately: placed last it cannot detect an implementation that returns on the
   first refusal, which is the mutation most likely to be written by accident. Assertions:
   - `ReplaceWorkerInventory` was issued;
   - exactly **two** `UpsertWorkerWorkspace` execs, and their bound `source_key` args are good1 and
     good2 **positionally**, using two distinguishable literals - not a count, and not a set;
   - `tx.outcome()` reports **commits == 1**. This is the load-bearing assertion and the one the
     existing `fakeTx` comment was written for: failing the batch produces zero commits, and
     asserting `rollbacks == 0` would fail against correct code because pgx defers a rollback after
     a successful commit;
   - `applyInventory` returned `nil`.

   **This is the slice's behavioural RED.** Written against HEAD with a literal 4096-byte key and no
   reference to any new symbol, it fails today with three upserts. Run it at HEAD first and record
   the output in the PR; the other tests' RED is non-compilation, which is a weak RED.

3. **`TestApplyInventoryUpdate_AnOverLongSourceKeyIssuesNoStatement`.** The single-message path.
   Assert **zero** statements reached the store and the returned error is the sentinel. Zero
   statements is the assertion that matters: "returned an error" is also what a fixture whose `Exec`
   errors produces, which is the same defect as a ceiling test that only checks the success path.

4. **`TestApplyInventory_EveryAgentSuppliedStringIsBounded`.** Table with one row per column -
   `source_type`, `short_id`, `baseline_hash` - each over its own bound with the other three legal,
   plus one row per column carrying a NUL, plus one row with an unparseable `LastUsedAt` and one with
   a zero-time `LastUsedAt`. Each: that entry is dropped, the batch commits, the surviving entries
   are stored. Without this, an implementation that bounds only `source_key` passes test 2.

5. **`TestHandleInventoryUpdate_ARefusedRowSpendsNoLogBudget`.** Drive `handleInventoryUpdate` with a
   refused row against a frozen-clock limiter (the `newFrozen()` helper in
   `ingest_log_limiter_test.go` is the existing precedent for reading `l.tokens`) and assert the
   token count is **unchanged** and neither `ingestDrops` arm moved. This pins section 6's
   "count, do not log" decision structurally rather than by convention.

6. **`TestInventoryRowRejections_CountsRefusalsAndNothingElse`.** Two halves, and the second is the
   control: a batch with two refused rows moves the counter by exactly 2; a batch with **no** refused
   rows moves it by **zero**. Without the second half the test is satisfied by an increment in the
   wrong branch.

### Integration lane, `internal/worker` (runs in CI via `pg-integration`)

7. **`TestApplyInventory_ARowAtTheBoundIsStoredAndOneOverIsNot`.** Against real Postgres: a batch of
   two rows, one whose `source_key` is exactly at the bound and one a single byte over.
   `ListWorkerWorkspaces` afterwards returns exactly the at-bound row. This is the **only** test that
   proves a value at the bound actually inserts through the real primary key and the real lookup
   index - which is the half a fake transaction structurally cannot show, and the half that would go
   red if section 4's sum were wrong.

8. **`TestUpsertWorkerWorkspace_TheCheckConstraintRefusesAnOverLongKey`.** Calls
   `q.UpsertWorkerWorkspace` **directly**, bypassing the constructor by construction, with an
   over-long key, and requires a real error. Calling it through the handler would prove nothing about
   the constraint. Its comment must say that bypassing the Go chokepoint is the point. Its key must
   be **multi-byte**, so a `CHECK` written on `length()` instead of `octet_length()` dies here.

### Mutation battery

| Mutation | Killed by |
|---|---|
| The length check deleted entirely | 2, three upserts instead of two |
| The refusal `return`s from `applyInventory` instead of `continue` | 2, commits == 0 |
| The refusal `break`s out of the loop | 2, one upsert instead of two, and the bad entry is first |
| `len()` replaced with `utf8.RuneCountInString` | 1, the multi-byte rows |
| Boundary flipped from `>` to `>=` (or the reverse) | 1, the exactly-at-bound rows |
| Only `source_key` bounded, siblings left open | 4 |
| The NUL predicate deleted | 4, the NUL rows |
| The timestamp predicate deleted | 4, the unparseable and zero-time rows |
| A `log.Printf` added at the refusal | 5 |
| The counter incremented on the accept branch | 6, second half |
| The constructor called but its error discarded | 2 and 3 |
| The `CHECK` omitted, or written on `length()` instead of `octet_length()` | 8 |
| The bound raised to a value the index cannot hold | 7, the at-bound row fails to insert |

Run each with a control that should die, verify the mutation actually applied before recording a
survivor, and never revert a mutation with `git checkout --` - restore from a copy.

## 16. Scope fence, and what this slice does NOT change

Three sibling lanes are running concurrently. **This design requires no edit in any of their files.**

- **`internal/jobspec/` (LANE S)** - not touched. Section 13 item 2 explains why a validator there
  would be a complement and not a fix.
- **`internal/schedrunner/`, `cmd/relay-server/main.go`, `internal/store/query/scheduled_jobs.sql`
  (LANE B)** - not touched. This is the reason counter publication is deferred (section 6).
- **`internal/testsupport/pgdsn/` (LANE E)** - not touched. **If the implementer sees a red guard in
  that package inside the `golang:1.26` container on a clean tree, it is LANE E's and not theirs.**

Also unchanged:

- **No statement in `internal/store/query/worker_workspaces.sql`.** The upsert, the delete, the
  replace and the two reads are byte-identical, so `make generate` produces no `.sql.go` diff and
  the CRLF hazard does not arise.
- **No agent-side bound.** Section 10 argues why mirroring it would trade a silent slowdown for a
  task failure.
- **The delete arm of `applyInventoryUpdate` does not call the constructor**, and this is a decision
  rather than an oversight. A `DELETE ... WHERE source_key = $3` binds the value as a **comparison
  key**, never as an index tuple, so it cannot fail the index - the hazard this slice closes is
  absent there. Refusing an over-long delete would additionally make any row stored before this slice
  agent-undeletable, since the admin evict path deletes the row only by way of the agent's confirming
  `WorkspaceInventoryUpdate{Deleted: true}` (`internal/api/workspaces.go:71-80`). Those rows are
  instead cleared by the next `ReplaceWorkerInventory`, which every reconnect runs. **Write this
  reason at the delete arm**, per CLAUDE.md's "where there is no identity to check, say so and name
  what replaces it" - the same discipline applied to an absent hazard.
- **No count bound of any kind.** Section 12.
- **No environment variable, no change to `Handler`'s constructors, no change to any exported
  signature** except the new counter reader.

## 17. Open questions for the human

1. **512, before Task 0 runs.** Confirm the top-down derivation is the right anchor, or fix a
   different number now. Task 0 can lower it; nothing else can.
2. **The timestamp arm (section 5).** It is a behaviour change - a currently loud failure becomes a
   silent counted drop - and it is what makes the batch-survival property true rather than nearly
   true. Include it, or cut it and narrow the README sentence and the constructor's comment to
   over-length rows only.
3. **The migration.** Confirm `NOT VALID` over both alternatives, and confirm that spending a
   migration on a backstop for a writer that does not exist yet is worth it, given that the Go
   constructor covers every production writer today.
4. **`short_id` in scope.** It reaches no index, so it is not the item's hazard. It is bounded here
   because it costs one line at a chokepoint that already exists. Confirm, or cut it and say so in
   the enumeration.
