---
title: reconcileRunningTasks compares wire task-id strings against canonical ones, so a non-canonical spelling cancels and requeues the agent's live tasks
type: bug
status: closed
closed: 2026-08-20
resolution: fixed
created: 2026-08-15
priority: medium
source: Phase 6 of the 2026-08-15-tasklog-err-limiter-keying slice; the lenses' pgtype.UUID.Scan finding carried one function outward
---

# reconcileRunningTasks compares wire task-id strings against canonical ones, so a non-canonical spelling cancels and requeues the agent's live tasks

## Summary

`reconcileRunningTasks` (`internal/worker/handler.go`) builds its server-side map from the **canonical**
rendering and looks it up with the **raw wire string**:

```go
serverSet := make(map[string]int64, len(serverTasks))
for _, t := range serverTasks {
    serverSet[uuidStr(t.ID)] = int64(t.AssignmentEpoch)   // canonical, lowercase, hyphenated
}

for _, rt := range reported {
    agentSet[rt.TaskId] = true                            // raw wire string
    srvEpoch, ok := serverSet[rt.TaskId]                  // raw wire string
    if !ok || srvEpoch != rt.Epoch {
        cancelIDs = append(cancelIDs, rt.TaskId)
    }
}
```

`uuidStr` renders `%08x-%04x-%04x-%04x-%012x`: **lowercase, hyphenated, 36 characters**. The wire string
is whatever the agent sent, and `pgtype.UUID.Scan` accepts several spellings that are not that. Read
from `pgx/v5@v5.9.1`, `pgtype/uuid.go`:

```go
switch len(src) {
case 36:
    src = src[0:8] + src[9:13] + src[14:18] + src[19:23] + src[24:]   // separators never checked
case 32:
    // dashes already stripped, assume valid
default:
    return dst, fmt.Errorf("cannot parse UUID %v", src)
}
buf, err := hex.DecodeString(src)   // accepts A-F as well as a-f
```

So all of these parse to the same 16 bytes and **none** of them equal `uuidStr`'s output:

- the same id in **uppercase** hex,
- the **32-character undashed** form,
- the 36-character form with **any four bytes** at indices 8, 13, 18 and 23 instead of hyphens (the
  finding two Phase 4 lenses probe-verified on 2026-08-15).

Both of the loop's consequences fire together:

1. **`ok` is false**, so the task is appended to `cancelIDs` and the coordinator instructs the agent to
   cancel it. The epoch comparison is **bypassed entirely** - `!ok` short-circuits it.
2. **The requeue loop then requeues it.** `agentSet` is keyed on the same wire string, so the canonical
   key in `serverSet` is "not reported by the agent", and `RequeueTaskByID` runs.

A live, correctly-running task is cancelled and requeued, silently, at every reconnect.

## Repro / Symptoms

Connect and register with `running_tasks` containing a task genuinely assigned to this worker at its
current epoch, spelled in uppercase (or undashed). The `RegisterResponse` carries that task in its
cancel list, and the task is requeued to `pending` with a bumped epoch behind it. No log line is emitted
on either path.

**Who hits this in practice.** Today's Go agent echoes back the ids the coordinator sent it, which are
canonical, so the shipped agent never triggers it. The exposure is (a) any third-party or reimplemented
agent whose UUID library emits uppercase - which several do - (b) any future change that round-trips a
task id through a component that normalizes differently, and (c) a deliberate attacker, for whom the
effect is bounded to its own tasks (`serverSet` is scoped to the authenticated worker) and is therefore
mostly self-harm.

That makes this primarily a **correctness and interop** bug with a silent, hard-to-diagnose failure mode:
"my tasks get cancelled and re-run every time the agent reconnects, and nothing logs why".

## Context

Found by carrying the 2026-08-15 slice's headline library finding one call outward. That slice fixed the
identical defect class at its own site: `handleTaskLog` now keys its dedupe map and renders its log line
on `canonicalID := uuidStr(taskID)`, never on `chunk.TaskId`, because keying on the wire string handed a
caller 2^32 distinct keys for one `(task, epoch)` pair. The comment there states the rule
(`logKey`'s doc comment, `internal/worker/ingest_log_limiter.go`).

`reconcileRunningTasks` is roughly 350 lines up in the same file and does the same thing for a different
reason, and it was out of scope for that slice - correctly, since it is not a logging path. It would go
invisible again the moment nobody is holding pgtype's parser in their head, which is what this file is
for.

The generalizable form, which is the project's own epoch-fence phrasing applied to identifiers: **a
string that parsed is not a string that is canonical.** `Scan` succeeding tells you the bytes decode; it
tells you nothing about what they look like. Any comparison, map key, set membership or log line
involving a caller-supplied identifier must use the re-encoding, not the input.

## Proposal

One line at the parse, then use the canonical string everywhere:

```go
for _, rt := range reported {
    var tID pgtype.UUID
    if err := tID.Scan(rt.TaskId); err != nil {
        // unparseable: cannot correspond to any assignment. Drop or cancel -
        // decide deliberately, and do not add an unbudgeted log line here.
        continue
    }
    canonical := uuidStr(tID)
    agentSet[canonical] = true
    srvEpoch, ok := serverSet[canonical]
    ...
}
```

Settle these:

- **What `cancelIDs` carries.** Today it echoes the wire string back to the agent, which is arguably
  friendlier (the agent recognizes its own spelling) and arguably wrong (the coordinator should speak
  canonically). Pick one and comment it. Note the agent looks these up in its own map, so changing the
  spelling it receives is a **wire-visible behaviour change** and needs checking against
  `internal/agent/`.
- **Unparseable ids.** Today they simply fail the lookup and land in `cancelIDs`, which is a reasonable
  outcome. Whatever is chosen, **do not add a log line without a budget key** - the ingest sites are all
  budgeted now and this one runs at registration, outside the budget
  ([[bug-2026-08-15-registration-log-sites-are-outside-the-connection-budget]]).
- **Sweep the rest of the package and `internal/api`.** The rule is general. Any other site that keys,
  compares, or renders a caller-supplied UUID string rather than its re-encoding has the same defect.
  `internal/api/server.go`'s `parseUUID` is the natural place to check first, since it returns the parsed
  value and its callers may still be holding the raw path segment.

## Acceptance / Done When

- A `RunningTask` reporting a genuinely-assigned task at the correct epoch, spelled in uppercase, is
  **not** cancelled and **not** requeued, proven by a handler-layer test that is RED against today's
  code.
- The same for the 32-character undashed spelling and for a 36-character spelling with non-hyphen
  separator bytes.
- A positive control on the same path: a task the agent genuinely does not report is still requeued, and
  a task at a stale epoch is still cancelled.
- The unparseable case has a stated, tested behaviour, and adds no unbudgeted log line.
- Whatever `cancelIDs` carries is checked against what `internal/agent/` does with it, with a test on
  the agent side if the spelling changes.
- A sweep note in the commit or the PR recording which other sites were checked for the same shape and
  what was found.

## Related

- Source: `internal/worker/handler.go` (`reconcileRunningTasks`, `uuidStr`, and `finishRegister`, which
  is its only caller), `internal/store/query/tasks.sql` (`GetActiveTasksForWorker`, `RequeueTaskByID`)
- The same defect class, fixed at its own site, with the library analysis written out:
  `internal/worker/ingest_log_limiter.go` (`logKey`'s doc comment),
  `internal/worker/handler.go` (`handleTaskLog`'s canonical-id block)
- The slice that produced the finding: `docs/superpowers/specs/2026-08-15-tasklog-err-limiter-keying.md`
  section 6.4, `docs/retros/2026-08-15-tasklog-err-limiter-keying.md` ("four lenses, two convergences")
- Library: `github.com/jackc/pgx/v5@v5.9.1`, `pgtype/uuid.go`, `parseUUID`
- Adjacent on the same handler, also about a wire value used more literally than it should be:
  [[bug-2026-08-12-tasklog-epoch-int32-truncation]]

## Notes

Filed at medium. There is no live exposure with the shipped agent, and the attacker version is bounded
to the attacker's own tasks - but the failure mode for an interop case is task loss and duplicate
execution with **no log line anywhere**, which is expensive to diagnose from the outside and cheap to
prevent. The fix is three lines.

Worth keeping the item even if the fix looks obvious, because the reasoning is the transferable part:
this is the second site in one file where a wire-supplied UUID string was used as though passing `Scan`
had normalized it. The first one cost 2^32 dedupe keys and five log lines per event; this one costs a
running task. **The lesson is not about UUIDs - it is that a successful parse is a statement about
decodability, never about form.**

## Resolution

Fixed 2026-08-20 by the `reconcile-canonical-task-ids` slice.

`reconcileRunningTasks` now canonicalizes the wire id (`pgtype.UUID.Scan` -> `uuidStr`) at the
top of the reported loop and uses that form for BOTH map operations, so all three non-canonical
spellings match and the epoch comparison is genuinely reached instead of being short-circuited
by `!ok`.

Two of the item's "settle these" questions were decided against its own leading reading:

- **`cancelIDs` still echoes the raw wire spelling.** The agent looks each id up in `a.runners[tid]`
  (`internal/agent/agent.go:246`), keyed at dispatch with the string it later reports back, so
  canonicalizing the echo would hand a non-canonical agent - the exact client this fix serves - a
  spelling it has never used, its lookup would miss, `Abandon()` would never run, and a task the
  coordinator decided to cancel would keep running. The result is a ZERO wire-visible change, and
  no agent-side test was needed (that criterion was conditional on the spelling changing).
- **Unparseable ids stay in `cancelIDs`, verbatim, silently.** Fail-safe, and no log line: reconcile
  runs inside `finishRegister`, before `Connect` allocates the connection's `ingestLogLimiter`.

Sweep result: exactly one defect. `internal/worker` is otherwise canonical throughout, and
`RegisterRequest.WorkerId` is never read server-side. In `internal/api`, `parseUUID` itself is clean;
the sweep's first pass wrongly concluded no caller renders its raw-string error, and Phase 4 caught
that - `workspaces.go:19-22` and `:43-46` both do `writeError(w, 400, err.Error())`. Reflected but
JSON-escaped by `writeJSON`, so not a vulnerability; filed separately as a consistency item.
`events.go:53`'s `?job_id=` is the same shape but deliberate and commented in place.

Guarded by `TestRegisterWorker_ReconcileMatchesNonCanonicalTaskIdSpellings` (three spellings x
{not cancelled, not requeued, epoch intact} plus both positive controls) and
`TestRegisterWorker_ReconcileEchoesAnUnparseableRunningTaskIdAndLogsNothing`. Both integration-tagged,
consistent with every other handler test in the package. Verified RED at `0fc1efc` independently by
the integration lane; three mutations killed, including a `continue`->`break` that Phase 4 proved was
invisible until the unparseable id was moved to the FRONT of the report.
