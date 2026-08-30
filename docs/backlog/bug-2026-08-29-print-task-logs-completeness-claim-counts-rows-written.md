---
title: printTaskLogs' "every one printed" claim counts rows written, not distinct seqs, so a re-served page inflates it
type: bug
status: open
created: 2026-08-29
priority: medium
source: Spec phase while fixing bug-2026-08-27-python-sdk-fetch-all-has-no-termination-stops
---

# `printTaskLogs`' completeness claim counts rows written

## Summary
At the page cap, `printTaskLogs` reports "the server reported N rows and every one printed" when
`progress.rows >= progress.total`. `progress.rows` counts rows WRITTEN, not distinct rows received.
A server re-serving a page behind an advancing cursor drives `rows` up to `total` while half the log
was never sent, and the operator is told the log is complete.

## Context
This is the exact weakness the Python port of the same loop was refuted for during
[[bug-2026-08-25-python-sdk-task-logs-iterates-envelope-keys]]: `task_logs` counts
`len({r.seq for r in out})` and its comment explains why `len(out)` will not do. The Go original it
was ported FROM still uses the non-distinct count. The port was corrected; the source was not.

Both the cursor and `total` are server-supplied, so the actor that can inflate the count is the same
actor whose honesty the message is asserting.

Found during the spec phase of
[[bug-2026-08-27-python-sdk-fetch-all-has-no-termination-stops]] and held out of that slice as
out-of-scope.

## Proposal
`printTaskLogs` streams rather than accumulating, so it cannot build a set the way Python does - but
it does not need one. Log seqs are strictly increasing along a correct walk, so an O(1) counter that
increments only when `l.Seq` exceeds the highest seq seen would count distinct rows exactly, with no
retained memory. If that holds up, it is also worth asking whether Python's `task_logs` can drop its
set: that set is up to 2,000,000 ints, roughly 35-70 MB, built inside the cap block.

Verify the monotonicity assumption against `GetTaskLogsPage` before relying on it - the query is
`WHERE task_id = $1 AND id > $2 ORDER BY id`, which makes it true within a correct walk, and the
whole point of this code path is a server that is not correct. The counter must be right, or fail
safe, when it is not.

## Acceptance / Done When
- The "every one printed" arm is backed by a count a re-serving server cannot inflate.
- The Python `task_logs` set is either retired with the same argument or its retention is justified
  in writing against the O(1) alternative.
- A test drives a re-served page behind an advancing cursor and shows the message does not claim
  completeness.

## Related
- `internal/cli/logs.go` `printTaskLogs`, `logProgress`
- `python/src/relay/client.py` `task_logs` - the corrected port
- [[bug-2026-08-25-python-sdk-task-logs-iterates-envelope-keys]] - closed; where the distinct-count
  argument was made
