---
title: The MCP relay_get_task_logs tool advertises since_seq as inclusive while the SQL is exclusive, so a paging model loops on the boundary row
type: bug
status: open
created: 2026-08-26
priority: low
source: Phase 1 of the 2026-08-26-relay-logs-envelope-drift slice - found while verifying since_seq semantics, scoped out as a different client
---

# The MCP relay_get_task_logs tool advertises since_seq as inclusive while the SQL is exclusive, so a paging model loops on the boundary row

## Summary

`internal/mcp/task_logs.go` documents the argument to the model as:

```go
SinceSeq int `json:"since_seq" jsonschema:"Return only log entries with seq >= this value. Defaults to 0."`
```

The statement behind it is `GetTaskLogsPage`: `WHERE task_id = $1 AND id > $2`. **Exclusive, not
inclusive.** The description is the only contract a model has, and it is wrong in the direction that
produces a loop rather than a gap:

- A model paging with `since_seq = last_seq`, which is what the description prescribes, believes it
  will re-receive the boundary row and skip it. It will not receive it, and if it dedupes on that
  expectation it makes no progress reasoning about the boundary.
- A model paging with `since_seq = last_seq + 1`, which is what the description makes correct,
  **skips a row** whenever ids are contiguous - and `task_logs.id` is a global `BIGSERIAL`, so a
  task logging alone has contiguous ids, which is the common single-task case.

This is the project's dominant defect class: wrong prose about correct code. The SQL is right, the
generated `internal/store/tasks.sql.go` doc comment agrees with its own source, and only the
model-facing string disagrees.

## Repro / Symptoms

Call `relay_get_task_logs` with `since_seq` set to the previous response's last `seq`. The returned
rows begin **after** that row, not at it. Nothing errors; the model's page arithmetic is simply
built on a false premise.

## Context

Found while the 2026-08-26 envelope-drift slice was establishing `since_seq` semantics for the CLI's
new paging loop. That slice pinned exclusivity with
`TestWatchJobLogs_ExactPageMultiple_NoDropNoDuplicate`, using **contiguous** seq ids specifically
because a gapped fixture cannot tell `id > lastSeq` from `id > lastSeq+1`. It scoped the MCP tool
out as a different client in a different package.

The `next_seq` half is fine and should be described alongside: the handler assigns `next_seq` from
the last returned row and overwrites it with `0` when the page is short, so **the correct cursor is
the previous page's `next_seq` verbatim** - which is only correct because `since_seq` is exclusive.
Documenting the cursor is more useful to a model than documenting the comparison operator.

## Proposal

One-line prose fix, plus two checks so it does not drift again:

- Correct the jsonschema string to state exclusivity and to name `next_seq` as the value to pass:
  something like "Return log entries AFTER this sequence number (exclusive). Pass the previous
  response's `next_seq` verbatim; 0 starts from the beginning."
- Check the README MCP tool table for the same claim at the same time.
- Check the REST API documentation of `GET /v1/tasks/{id}/logs` in README for the same claim - the
  CLI half of this slice found README overselling the sibling command, so the endpoint's own prose
  is worth reading rather than assumed.

## Acceptance / Done When

- The jsonschema description states exclusivity and names `next_seq` as the cursor.
- README's MCP tool table and the `GET /v1/tasks/{id}/logs` section agree with the SQL, or a note
  records that they never made the claim.
- A test asserts the boundary: with rows at contiguous seqs, `since_seq = N` returns rows starting
  at `N+1`. `internal/mcp/task_logs_test.go` today only asserts the parameter reaches the query
  string.

## Related

- `internal/mcp/task_logs.go` (`getTaskLogsArgs.SinceSeq`), `internal/store/query/tasks.sql`
  (`GetTaskLogsPage`), `internal/api/tasks.go` (`handleGetTaskLogs`)
- The slice that established the semantics and scoped this out:
  `docs/superpowers/specs/2026-08-26-relay-logs-envelope-drift.md`,
  `docs/retros/2026-08-26-relay-logs-envelope-drift.md`
- The same endpoint's other client drift: [[bug-2026-08-25-python-sdk-task-logs-iterates-envelope-keys]]
