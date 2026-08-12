---
title: handleTaskLog narrows the wire-supplied int64 epoch to int32, so 2^32 + E matches epoch E
type: bug
status: open
created: 2026-08-12
priority: low
source: Phase 4 review of the task-log assignee-fence iteration (2026-08-12)
---

# handleTaskLog narrows the wire-supplied int64 epoch to int32, so 2^32 + E matches epoch E

## Summary
`TaskLogChunk.Epoch` is an `int64` on the wire. `handleTaskLog` narrows it with an unchecked
conversion when building the fence parameters (`internal/worker/handler.go:622`):

```go
AssignmentEpoch: int32(chunk.Epoch),
```

`tasks.assignment_epoch` is `INT` (int32), so the narrowing is needed - but it wraps rather than
rejecting. A chunk carrying `Epoch = 2^32 + E` (or any value congruent to `E` mod 2^32) is bound as
`E` and matches a task whose stored epoch is `E`.

**This is a latent sharp edge, not a live bug.** Record it as such. Since 2026-08-12 the fence also
requires `t.worker_id` to equal the connection's authenticated worker, so the only tasks a wrapped
epoch can reach are tasks the sender is already the assignee of - and for those it can simply send
the correct epoch. The wrap buys an attacker nothing today. What makes it worth recording is that
the guard it currently free-rides on is a *different* predicate, added for a different reason: if
anyone ever relaxes the assignee predicate, or reuses this narrowing pattern on a path without one,
the wrap becomes reachable again with no test standing in the way.

## Repro / Symptoms
No user-visible symptom. Demonstrable at the handler layer: a chunk from a task's genuine assignee
carrying `Epoch: 4294967296 + <current epoch>` is stored exactly as if it carried the current epoch.
A chunk from a non-assignee is still rejected, by the assignee predicate.

## Context
Found by the Phase 4 correctness lens on the assignee-fence change. Worth contrasting with the
status path, which does **not** have this shape: `handleTaskStatus` compares at int64 width first
(`internal/worker/handler.go:433`):

```go
if int64(task.AssignmentEpoch) != upd.Epoch {
    return
}
```

It widens the stored int32 to int64 rather than narrowing the wire value, so no wrap is possible
there. Its own later `AssignmentEpoch: int32(upd.Epoch)` at line 487 is safe precisely because that
equality already established the value fits. The two paths simply chose different orders, and the log
path chose the one that loses information. That asymmetry is the most useful thing in this item: it
shows the correct pattern already exists in the same file.

Note that `handleTaskStatus` has its own, unrelated and much more serious problem - see
[[bug-2026-08-12-taskstatus-update-unauthenticated-epoch-zero]]. It is cited here only as the
reference for the int64 comparison.

## Proposal
One line, if and when anyone is in this path for another reason. Reject out-of-range epochs before
narrowing rather than wrapping:

```go
if chunk.Epoch < 0 || chunk.Epoch > math.MaxInt32 {
    return
}
```

Dropping is the right failure mode and matches the surrounding code: an out-of-range epoch cannot
correspond to any real assignment, `handleTaskLog` already returns silently on an unparseable task
id, and a fence miss is already a silent drop. Do not add a log line - that would hand an attacker a
new flood vector on the recv goroutine, which is exactly the problem in
[[bug-2026-08-12-tasklog-err-limiter-attacker-keyed]].

Alternatively, and slightly better if the diff is being touched anyway: compare at int64 width the
way `handleTaskStatus` does, so there is one pattern in the file rather than two. That is a larger
change here, since the log path does its epoch check inside SQL rather than in Go.

## Acceptance / Done When
- A chunk whose `Epoch` is outside the int32 range is dropped rather than wrapped, proven by a test
  that is RED against today's code: sent by a task's genuine assignee with
  `Epoch = 2^32 + <current epoch>`, it must not be stored and must not be published.
- A positive control on the same path: the same assignee at the real epoch is still stored and
  published.
- No new log line on the rejection path.
- `handleTaskLog` still performs exactly one DB round trip and one statement.

## Related
- Source: `internal/worker/handler.go:622` (the narrowing), `:433` and `:487` (the int64-width
  comparison on the status path, which is the pattern to copy)
- Context: `docs/superpowers/specs/2026-08-12-tasklog-append-assignee-fence.md` - the assignee
  predicate is what makes this inert today
- Adjacent: [[bug-2026-08-12-tasklog-err-limiter-attacker-keyed]] (same call site),
  [[bug-2026-08-12-taskstatus-update-unauthenticated-epoch-zero]] (cited for its comparison order,
  not otherwise related)

## Notes
Filed at low priority deliberately. There is no exploit path today and the fix is a one-line guard;
the value of the item is that it names *why* the code is currently safe, so the next person to touch
the fence knows that the assignee predicate is carrying more weight than it appears to. An item whose
whole content is "this is fine, and here is the assumption it depends on" is still worth having.
