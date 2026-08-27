---
title: The CLI and the MCP server interpolate caller-supplied ids into request paths unescaped, and internal/mcp is the worse half
type: bug
status: open
created: 2026-08-26
priority: medium
source: Phase 6 of the 2026-08-26-relay-logs-envelope-drift slice, which escaped logs.go's three sites and left the siblings
---

# The CLI and the MCP server interpolate caller-supplied ids into request paths unescaped, and internal/mcp is the worse half

## Summary

The 2026-08-26 envelope-drift slice added `jobPath`/`jobEventsPath` to `internal/cli/logs.go` and
escaped the three id-bearing request lines that command builds, with the reasoning stated there:

> In a path a `/` reroutes the request to another endpoint on the same host with the operator's
> bearer token attached. In a query string an `&` or `#` truncates the request or injects a
> parameter the handler will read.

The same shape remains at eleven other sites in two packages. `Client.Do` concatenates
`c.base + path` and hands it to `http.NewRequestWithContext`, which parses and normalises dot
segments, so `..` and literal slashes in an id are not sent literally - they walk up the path.

**Go CLI, id straight from argv:**

| symbol | file | note |
|---|---|---|
| `doGetJob` | `internal/cli/jobs.go` | `"/v1/jobs/" + fs.Arg(0)` |
| `doCancelJob` | `internal/cli/jobs.go` | `"/v1/jobs/" + fs.Arg(0)`, **destructive** |
| `doWorkersGet` | `internal/cli/workers.go` | `"/v1/workers/" + fs.Arg(0)` |
| `doReservationsDelete` | `internal/cli/reservations.go` | `"/v1/reservations/" + args[0]`, **destructive** |
| `doWorkersEvictWorkspace` | `internal/cli/workers_workspaces.go` | the **second** segment, `shortID := fs.Arg(1)` |

**Go MCP server, id straight from a model:**

| symbol | file | note |
|---|---|---|
| `callCancelJob` | `internal/mcp/cancel.go` | `DELETE /v1/jobs/%s`, **destructive** |
| `callGetJob` | `internal/mcp/jobs.go` | |
| `callListTasks` | `internal/mcp/tasks.go` | |
| `callGetTask` | `internal/mcp/tasks.go` | |
| `callGetTaskLogs` | `internal/mcp/task_logs.go` | |
| the wait poll | `internal/mcp/wait.go` | |

## Context - what is NOT affected, checked so nobody re-derives it

- **The `resolveWorkerID` family is safe and must not be "fixed".** `resolveWorkerIDIn`
  (`internal/cli/workers.go`) returns argv verbatim only when `looksLikeUUID(target)` holds, which
  requires exactly 36 characters of hex with hyphens at 8/13/18/23 - no path or query metacharacter
  survives it. Otherwise it returns a server-supplied `wk.ID`. So `doWorkersRevoke`,
  `doWorkersDelete`, `doWorkersEnable` and `doWorkersWorkspaces`'s **first** segment are already
  closed by construction. Only `doWorkersEvictWorkspace`'s second segment is open.
- `internal/mcp/resources.go` already escapes, with a comment at `readEntityByID` explaining why -
  which makes the six unescaped sites in the same package a within-file inconsistency, not an
  oversight class.
- `internal/cli/admin_users.go` already uses `url.QueryEscape` on the email parameter.
- The SPA half is a **separate open item**: [[bug-2026-08-12-unencoded-path-interpolation-api-clients]]
  enumerates 15 `web/` call sites and its table is explicitly scoped to `web/`. This item is its Go
  sibling; neither is a duplicate of the other and they can ship apart.

## Severity, and why internal/mcp argues for higher than the SPA item

The SPA item is `low` because the id originates from the user's own route params, so attacker and
victim are the same principal and retargeting a verb you could already issue is a lateral move.

**`internal/mcp` breaks that argument.** The id is chosen by a language model from whatever text
reached its context - a job name, a task log, a README, a prompt-injected document - and issued with
the **operator's** token. `cancel.go` is a `DELETE`. That is a different principal choosing the value
from the one issuing the request, which is exactly the property that made
`workers/api.ts:154` the row the SPA item singled out. Filed `medium` on that basis; a human
re-rating this should look at `internal/mcp/cancel.go` on its own.

The CLI half stays a hygiene fix: argv is typed by the operator, so it is self-inflicted - but it is
also pasted from wherever they got it, and `doCancelJob` and `doReservationsDelete` are destructive.

## Proposal

Escape every segment above. Follow `internal/cli/logs.go`'s `jobPath`/`jobEventsPath` shape - a
named builder per resource so the escaping decision is made once, in the one place that knows which
context the id lands in - rather than sprinkling `url.PathEscape` at call sites.

Decide once, and record the decision so the twelfth site is not a matter of whether the author
remembered:

- **Option A: a builder per resource** in each package. Matches what shipped in `logs.go` and reads
  well at the call site.
- **Option B: escape inside `relayclient.Client.Do`.** Rejected on sight, and recorded here so it is
  not re-proposed: `Do` receives an already-assembled string including the query, so it cannot tell
  a separator it must keep from one it must escape. This is the same rejection
  [[bug-2026-08-12-unencoded-path-interpolation-api-clients]] recorded for `apiFetch`.
- **Option C: server-side strictness.** Most of these handlers already `parseUUID` and 400, which
  makes a traversal land on a 400 rather than a real resource - but only for handlers that do, and
  only after the request left the client. Defence in depth, not the fix.

## Acceptance / Done When

- Every symbol in both tables escapes its interpolated segments.
- `doWorkersEvictWorkspace` has a test covering **both** segments, not just the first.
- Each package has at least one test proving containment: a fake server route registered on the
  traversal target that must **not** be hit, plus a positive control asserting the correctly-scoped
  path **was** hit. A one-sided assertion passes against a client that sends nothing.
  `internal/cli/logs_test.go`'s `TestWatchJobLogs_JobIDIsEscapedInEveryRequest` and
  `TestPrintTaskLogs_TaskIDIsPathEscaped` are the pattern.
- A test pins that `looksLikeUUID`'s passthrough cannot admit a metacharacter, so the "safe by
  construction" claim above is checked rather than asserted.
- The A/B/C decision is recorded in a comment where the builders live.

## Related

- The fix to copy: `internal/cli/logs.go` (`jobPath`, `jobEventsPath`, and the comment above them)
- Already correct in the same package: `internal/mcp/resources.go` (`readEntityByID`)
- The SPA sibling, separately scoped: [[bug-2026-08-12-unencoded-path-interpolation-api-clients]]
- `docs/retros/2026-08-26-relay-logs-envelope-drift.md`

## Update 2026-08-27 - the Python SDK is a third language here, and httpx's behaviour is now MEASURED

Filed against the CLI and MCP. `python/src/relay/client.py` has the same shape at nine sites:
`get_job`, `cancel_job`, `get_tasks`, `get_task`, `task_logs`, `get_schedule`, `update_schedule`,
`delete_schedule`, `run_schedule_now`. One of them, `task_logs_page`, was escaped in
[[bug-2026-08-25-python-sdk-task-logs-iterates-envelope-keys]] because that slice rewrote the line
and its Go upstream escapes; the other eight are untouched and are this item.

**The measurement this item's own acceptance criteria asked for has now been done**, against
httpx 0.28.1 with `base_url="http://relay.internal:8080"`. It bounds the severity in both
directions, so a future session need not re-derive it:

CONFIRMED - same-host path traversal, and query/fragment truncation:

```
'../../v1/users'       -> path='/v1/users/logs'    (bearer attached; the id chooses the ENDPOINT)
'abc/../../v1/users'   -> path='/v1/v1/users/logs'
'abc?limit=1&x='       -> path='/v1/tasks/abc'     (the /logs suffix AND the paging params gone)
'abc#frag'             -> path='/v1/tasks/abc'
base='http://h/relay', id='../../../admin' -> '/admin/logs'   (escapes a proxy sub-path prefix)
```

REFUTED - SSRF and header injection do NOT reach, which is the more important half:

```
'http://evil.example/steal'   -> host='relay.internal'   (httpx._merge_url makes it a PATH SEGMENT)
'//evil.example/steal'        -> host='relay.internal'
'%2f%2fevil.example%2fx'      -> host='relay.internal'
'abc
X-Injected: 1'        -> httpx.InvalidURL, before any socket is opened
'abc def'                  -> httpx.InvalidURL
'abc@evil.example'            -> host='relay.internal'   (userinfo is not parsed out of a path segment)
```

So the bearer token cannot be redirected to a foreign host by any spelling tried, and this is a
path-shape defect rather than a credential-exfiltration one. Severity should be read accordingly.

The Python remedy, measured to work: `urllib.parse.quote(task_id, safe="")`. Note it is NOT
identical to Go's `url.PathEscape` - Python additionally escapes `+ : @ = & $` - but it escapes a
strict superset, `%2F` survives into `raw_path` undecoded, and the two agree exactly on a UUID.

One test-design note that cost a round to find: assert on `request.url.raw_path`, not `.path`.
`.path` reads the DECODED form, so a `.path` assertion passes against no escape at all.

Add to Related: `python/src/relay/client.py` (eight remaining sites);
[[bug-2026-08-25-python-sdk-task-logs-iterates-envelope-keys]] (the ninth, escaped).
