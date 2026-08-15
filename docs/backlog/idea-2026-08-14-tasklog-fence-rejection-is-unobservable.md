---
title: A rejected task-log chunk is completely unobservable, and since the trailing window it can mean "legitimately late"
type: idea
status: open
created: 2026-08-14
updated: 2026-08-15
priority: medium
source: Phase 4 of the 2026-08-14-tasklog-terminal-append-bound slice; the diagnosability cost that slice accepted deliberately
---

# A rejected task-log chunk is completely unobservable, and since the trailing window it can mean "legitimately late"

## Summary

`handleTaskLog` (`internal/worker/handler.go`) drops a chunk whose `AppendTaskLog` fence returns
`pgx.ErrNoRows` **silently**: no log line, no counter, no metric, and nothing returned to the agent.
The chunk is stored nowhere and published nowhere, which is correct. What is missing is any way for
an operator to know it happened.

That was defensible while `ErrNoRows` meant one thing. As of the 2026-08-14 trailing-window bound it
means three:

1. **Stale generation** - the assignment ended (epoch mismatch). Forged, or a zombie agent.
2. **Wrong assignee** - the sender is not the task's `worker_id`. Forged.
3. **Closed window** - the task finished longer ago than `RELAY_TASKLOG_TRAILING_WINDOW`. **This one
   happens legitimately**, to a chunk buffered in the agent's `sendCh` across a long coordinator
   outage, and it is the case an operator who set the knob too small will hit constantly.

An operator who sets `RELAY_TASKLOG_TRAILING_WINDOW=15s` (units confusion is the likely mistake, and
the parser already warns about it at startup) gets task output silently truncated with **no signal of
any kind** at runtime. The startup warning fires once, months before anyone reads the log.

## Context

The 2026-08-14 spec took this trade explicitly and wrote down why the obvious fix is wrong: a log
line on this path would be **caller-driven volume on the gRPC recv goroutine**, handing back the
exact flood vector [[bug-2026-08-12-tasklog-err-limiter-attacker-keyed]] is about, and it would fire
on the legitimate late-flush case - the one an operator would most want quiet. Both the 2026-08-12
assignee-fence spec and this one argued for keeping the three rejection reasons indistinguishable
**to the caller**, which is still right: telling a prober why it was rejected is free information.

Indistinguishable to the caller and invisible to the operator are different properties, and only the
first was decided on purpose.

### Update 2026-08-15 - the arm is now a named branch with a pinning test, and this item is cited from source

The `2026-08-15-tasklog-err-limiter-keying` slice split `handleTaskLog`'s error handling into two
explicit arms and left the `ErrNoRows` arm shaped for exactly this item. **Scope, priority and
acceptance criteria are unchanged.** What changed is that the work got cheaper and the item got a
source citation.

- **The branch exists and is named.** The compound `if !errors.Is(err, pgx.ErrNoRows) && ...` is gone;
  the `ErrNoRows` case is now its own arm with its own `return` and its own comment block. The counter
  is a one-line insertion into a branch that already exists, with no re-litigation of the condition.
- **The arm's comment cites this item by name, in source**, and states that the answer here is a
  COUNTER and not a log line. Leaving this item unfiled or closing it without doing the work would
  strand that citation.
- **It is pinned by a test.** `TestHandleTaskLog_AFenceRejectionEmitsNoLogLineAtAll` asserts the
  **whole captured log is empty** across a fence rejection, so any future wording on that arm reddens
  it. That is a gift and a constraint: the counter must not log, and the test will say so.
- **The "no log line" argument survives, in a modified form.** Every other caller-driven log line on
  this goroutine is now budgeted by a per-connection token bucket
  (`internal/worker/ingest_log_limiter.go`), so a line here would be bounded rather than unbounded.
  It is still the wrong mechanism: it would fire on the legitimate late-flush case, it would spend a
  token that a genuine infra failure needs, and the arm's whole value is that it is silent. Keep the
  counter.
- **Half of the "where does an operator read it" question now has a second stakeholder.**
  [[idea-2026-08-15-ingest-log-suppression-is-uncounted]] was filed on the **complementary arm** of the
  same `if` - counting log lines the budget dropped, across five kinds in three handlers. It has the
  same read-surface dependency as this item. **Spec the two together and ship them as two slices**, so
  the endpoint decision is taken once. They are deliberately separate items because no input executes
  both arms, they count different nouns, and merging them would falsify this item's Done-When.

The read-surface finding below was re-checked at 2026-08-15 and is unchanged: `internal/api/server.go`
still routes no server-wide counters endpoint.

## Proposal

A counter, not a log line. Shape, to be argued at spec time rather than adopted as written:

- **One atomic add on the rejection path.** `taskLogFenceRejects` incremented where `ErrNoRows` is
  handled, before the existing early return. No allocation, no lock, no round trip - the standing
  constraint on this handler is one DB round trip and nothing else, and an `atomic.Uint64` respects
  it.
- **Surfaced through the existing `Handler.Metrics` seam**, not test-only. `Metrics` is already an
  exported field set by `cmd/relay-server` after construction and already nil-checked at every use
  (`h.Metrics != nil`), so the wiring precedent exists. Note the design question honestly:
  `metrics.Store` today is a **per-worker sample store** (`Activate`/`Append`/`Clear`/`Snapshot`/
  `LastSampleAt`), so a global counter is not a natural fit for its current shape. Decide whether to
  add a counter method there, key the counter per worker (which makes it a useful *diagnostic* -
  "worker X is sending chunks nobody wants" - and costs a map write), or introduce a small separate
  counters type.
- **Explicitly NOT a log line.** State this in the code comment where the counter is incremented, or
  the next person will "improve" the counter into a `log.Printf`. Cite the limiter item. **(2026-08-15:
  the arm's existing comment already says this and names this item; extend that comment rather than
  writing a second one, and note that a budgeted line is also not acceptable here.)**
- **Consider splitting the counter by reason.** The three reasons are indistinguishable to the
  caller, but the *server* knows which predicate failed only if it asks - the fence returns one row or
  none, not a reason. Per-reason counts would need a second query on the failure path, which the
  constraint forbids. **Do not add a round trip to the hot path for observability.** Either accept one
  number and say so in the comment, or find a way to get the reason out of the existing statement (an
  extra column on the fence row is not available, because a rejection returns no row at all - that
  asymmetry is worth stating in the spec so nobody spends an afternoon on it).
- **Where an operator reads it.** Verified at `ee88de0` and re-checked 2026-08-15: **there is no
  coordinator-level counters endpoint today.** `internal/api/server.go` routes `GET /v1/config`,
  `GET /v1/jobs/stats`, `GET /v1/workers/stats` and `GET /v1/workers/{id}/metrics`, and nothing else
  that would carry a server-wide number. So this item either extends `GET /v1/workers/stats` (natural
  if the counter ends up keyed per worker) or depends on
  [[feature-2026-08-09-server-info-allowlist-endpoint]] landing first. Settle that before speccing the
  counter, not after - it is the difference between a one-file change and a new endpoint with its own
  authorization question. **(2026-08-15: settle it for
  [[idea-2026-08-15-ingest-log-suppression-is-uncounted]] at the same time.)**

## Acceptance / Done When

- A rejected chunk increments a counter, proven by a handler-layer test that reads the counter across
  a rejection and a success.
- The counter is readable by an operator through an endpoint, not only from a test.
- No new log line on the rejection path - **including a budgeted one** (2026-08-15) - and
  `TestHandleTaskLog_PersistFailureIsLoggedOncePerTaskPerEpoch` still passes with no assertion changed.
- `TestHandleTaskLog_AFenceRejectionEmitsNoLogLineAtAll` still passes (2026-08-15).
- `handleTaskLog` still performs exactly one DB round trip and one statement, with no goroutine, no
  queue and no lock added to the recv goroutine.
- The counter cannot be used by an agent to learn why its chunk was rejected (it is server-side
  observability, never a response).

## Related

- Source: `internal/worker/handler.go` (`handleTaskLog`'s `pgx.ErrNoRows` branch - now its own named
  arm, whose comment cites this item - and the `Metrics` field beside
  `AllowAutoEnroll`/`TrailingLogWindow`), `internal/metrics/store.go` (the seam's current per-worker
  shape), `internal/api/worker_metrics.go` (how metrics reach the API today), `internal/api/server.go`
  (the route table, which has no server-wide counters endpoint)
- The slice that added the third meaning: `docs/superpowers/specs/2026-08-14-tasklog-terminal-append-bound.md`
  section 6.4 ("What the agent sees when the window has closed: nothing"),
  `docs/retros/2026-08-14-tasklog-terminal-append-bound.md`
- The slice that shaped the arm for this work and pinned it with a test:
  `docs/superpowers/specs/2026-08-15-tasklog-err-limiter-keying.md` section 5 ("what this spec owes the
  counter item"), `docs/retros/2026-08-15-tasklog-err-limiter-keying.md`
- **Sibling on the complementary arm, to be specced together and shipped separately** (2026-08-15):
  [[idea-2026-08-15-ingest-log-suppression-is-uncounted]]
- Must not regress: [[bug-2026-08-12-tasklog-err-limiter-attacker-keyed]] - the reason this is a
  counter and not a log line. **Closed 2026-08-15**; now in `docs/backlog/closed/`.
- Would add a fourth silent rejection reason: [[bug-2026-08-14-task-logs-have-no-per-task-volume-cap]].
  Landing that first without this makes the diagnosability problem worse; landing this first makes
  that one cheaper.
- Possible dependency for the read surface: [[feature-2026-08-09-server-info-allowlist-endpoint]]
- The knob whose misconfiguration this detects: `RELAY_TASKLOG_TRAILING_WINDOW`
  (`cmd/relay-server/main.go`, `parseTrailingLogWindow`)

## Notes

The generalizable rule this item exists to record: **a silent rejection path needs a counter the day
a second reason to reject is added to it.** One reason is diagnosable by reasoning about the code -
an operator seeing missing logs can conclude "the agent is stale". Three reasons, one of which is
legitimate and configuration-dependent, is not. The counter is cheap; the moment to add it was the
moment the meaning stopped being singular, which has already passed.

**2026-08-15 addendum to that rule, from the sibling item:** a rate limiter that drops silently has
the same shape and the opposite direction. This arm is silent because nothing was ever said; the
budget's arm is silent because something was said and then suppressed. Both convert a diagnosable
condition into an absence of evidence.
