---
title: API clients interpolate ids into request paths without encodeURIComponent, so a crafted id can retarget the verb
type: bug
status: open
created: 2026-08-12
priority: low
source: the same defect was found and fixed in web/src/schedules/api.ts during the 2026-08-12-schedule-detail-page slice; these call sites remain
---

# API clients interpolate ids into request paths without encodeURIComponent, so a crafted id can retarget the verb

## Summary

Most of the SPA's API clients build request paths by template-interpolating an id straight into the
string:

```ts
return apiFetch<JobDetail>(`/jobs/${id}${force ? '?force=true' : ''}`, { method: 'DELETE' })
```

`fetch()` resolves and **normalizes dot segments before dispatch**, so a value containing `..` and
literal slashes is not sent literally - it walks up the path. An id of
`../tasks/<uuid>` turns the call above into `DELETE /v1/tasks/<uuid>`: the same verb, a different
resource. `encodeURIComponent` fixes it by turning the traversal segment's slashes into `%2F`,
which a URL parser does not treat as separators, so the request stays scoped under its intended
prefix.

This was found and fixed in `web/src/schedules/api.ts` during the 2026-08-12 schedule detail slice
(all five call sites now encode, with the reasoning at `:45-51` and a regression test at
`web/src/schedules/api.test.ts:213-221`). The other clients were out of that slice's scope.

## Repro / Symptoms

The proof shipped for the schedules client transfers directly: register an MSW handler for the
traversal target, call the client with a crafted id, and assert the handler was **not** hit and the
correctly-scoped path was. Without encoding, the traversal handler fires.

## Context - the complete remaining set, verified against the tree

**15 unencoded path-interpolating call sites across four files.** The brief that produced this item
named two files; reading the tree found two more, so the list below is the authority:

| file | line | call | notes |
|---|---|---|---|
| `web/src/jobs/api.ts` | 136 | `GET /jobs/${id}` | |
| | 167 | `GET /tasks/${taskId}/logs?...` | |
| | 220 | `DELETE /jobs/${id}...` | destructive |
| `web/src/workers/api.ts` | 92 | `GET /workers/${id}` | |
| | 98 | `GET /workers/${id}/metrics` | |
| | 103 | `GET /workers/${id}/workspaces` | |
| | 128 | `PATCH /workers/${id}` | |
| | 135 | `POST /workers/${id}/disable...` | |
| | 140 | `POST /workers/${id}/enable` | |
| | 147 | `DELETE /workers/${id}/token` | destructive |
| | 154 | `POST /workers/${id}/workspaces/${shortId}/evict` | **two** segments; see below |
| `web/src/admin/users/api.ts` | 68 | `PATCH /users/${id}` | |
| | 74 | `POST /users/${id}/archive` | destructive |
| | 78 | `POST /users/${id}/unarchive` | |
| `web/src/admin/reservations/api.ts` | 100 | `DELETE /reservations/${id}` | destructive |

Already correct and usable as the pattern: all five sites in `web/src/schedules/api.ts`, and
`web/src/jobs/api.ts:191`, which already encodes a **query** parameter
(`/events?task_id=${encodeURIComponent(taskId)}`) in the same file that leaves three path segments
unencoded.

**Severity, and the one site that argues for higher.** Filed at **low** because in almost every
case the id originates either from a server response (a UUID) or from the user's own route
params - so the "attacker" and the victim are the same principal, and the reachable targets are
endpoints that principal can already call. Retargeting a verb you were already allowed to issue is
a lateral move, not an escalation.

`workers/api.ts:154` is different and should be fixed first. Its `shortId` segment is
**agent-supplied**: `Workspace.short_id` (`web/src/workers/api.ts:82-88`) is served from
`worker_workspaces.short_id`, which the server stores verbatim from the gRPC wire with no
validation (`internal/worker/handler.go:919` and `:942`, both binding `u.ShortId` straight into
`UpsertWorkerWorkspaceParams`). So an enrolled agent controls a string that later lands in an
**admin's** browser and is interpolated into a `POST` path. That crosses a trust boundary the other
fourteen do not: the principal choosing the value is not the principal issuing the request. It is
still bounded by what the admin's own session can reach, which is why the item stays low overall -
but a human re-rating this should look at that row on its own.

Note this connects to the standing threat model for a compromised-but-enrolled agent recorded in
`docs/retros/2026-08-12-retry-resurrect-status-guard.md`: the three fence iterations closed what
such an agent can write to the *database*; this is a small thing it can still push into an
operator's *browser*.

## Proposal

Wrap every path segment above in `encodeURIComponent`, following
`web/src/schedules/api.ts:45-53` including its explanatory comment (write the comment once in each
file rather than fifteen times).

Also decide, once, whether to prevent the next occurrence structurally rather than by vigilance:

- **Option A: a helper.** A small `path` tagged-template or builder in `web/src/lib/api.ts` that
  encodes every interpolated value, so the safe form is the shortest form. Fits the project's
  **single JSON entry point** habit - policy lives at the boundary, not at call sites - and
  `apiFetch` is already that boundary.
- **Option B: encode inside `apiFetch`.** Rejected on sight: `apiFetch` receives an
  already-assembled string including query parameters, so it cannot tell a path separator it should
  keep from one it should escape. Recorded so it is not re-proposed.
- **Option C: leave it to review.** What is in place today, and it produced fifteen sites.

Also worth deciding: whether the **server** should reject a non-UUID `{id}` earlier. Several
handlers already call `parseUUID` and 400 on failure, which makes the traversal land on a 404 or
400 rather than a real resource - but only for handlers that do, and only after the request left
the browser. Client-side encoding is the fix; server-side strictness is defence in depth and a
separate question.

## Acceptance / Done When

- Every call site in the table encodes its interpolated segments.
- Each of the four files has at least one test proving the traversal is contained: a handler
  registered on the traversal target that must **not** be hit, plus a positive control asserting
  the correctly-scoped path **was** hit. A one-sided assertion passes against a client that sends
  nothing. Mirror `web/src/schedules/api.test.ts:213-221`.
- `workers/api.ts:154` has a test covering **both** interpolated segments, not just the first.
- A decision is recorded on Option A versus Option C, so the sixteenth call site is not a matter of
  whether the author remembered.

## Related

- The fix to copy: `web/src/schedules/api.ts:45-53,65-68,92-93,103-104` and the regression test at
  `web/src/schedules/api.test.ts:213-221`
- Design record: `docs/retros/2026-08-12-schedule-detail-page.md` (Problem 6, Deferred Findings 6)
- The trust-boundary row: `internal/worker/handler.go:915-926,931-946` (agent-supplied `ShortID`,
  stored unvalidated), `web/src/workers/api.ts:82-88,149-154`
- Same principal, other things a compromised agent can still do:
  [[bug-2026-08-12-tasklog-terminal-task-append-unbounded]],
  [[bug-2026-08-12-tasklog-err-limiter-attacker-keyed]],
  [[bug-2026-08-12-auto-enroll-hostname-takeover]]

## Notes

The transferable lesson is about **where the normalization happens**. Nothing in the client string
looks dangerous; the traversal is performed by `fetch()` itself, after the code has finished
running, which is why reading the call site tells you nothing and why this survived review in four
separate files. The same reasoning applies to any string handed to a URL parser, a filesystem API,
or a redirect - the question is never "does this look like a path", it is "who normalizes it, and
after which check".
