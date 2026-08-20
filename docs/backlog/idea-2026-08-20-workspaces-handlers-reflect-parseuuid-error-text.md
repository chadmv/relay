---
title: Two workspace handlers reflect parseUUID's error text to the client where every other id handler writes a fixed message
type: idea
status: open
created: 2026-08-20
priority: low
source: Phase 4 of the 2026-08-20-reconcile-canonical-task-ids slice, correcting that slice's own first-pass sweep of internal/api
---

# Two workspace handlers reflect parseUUID's error text to the client where every other id handler writes a fixed message

## Summary

`handleListWorkerWorkspaces` and `handleEvictWorkerWorkspace` (`internal/api/workspaces.go`) both do:

```go
workerID, err := parseUUID(r.PathValue("id"))
if err != nil {
    writeError(w, http.StatusBadRequest, err.Error())
    return
}
```

They are the only two of **28 non-test `parseUUID` call sites** in `internal/api` that render the
parser's error into the response body. Twenty-three write a fixed caller-facing string
("invalid worker id", "invalid task id", ...), two discard the error entirely
(`decodeCursor` returns `errBadCursor`; `fillOwnerEmails` skips on `err != nil`), and one
(`handleCreateReservation`'s `worker_ids` loop) reflects the raw **input** but not the parser's text.

**This is a consistency cleanup, not a vulnerability.** Filed so the outlier is either brought into
line or documented as deliberate, rather than being rediscovered by the next sweep.

## Context

The 2026-08-20 `reconcile-canonical-task-ids` slice was asked by its backlog item to sweep
`internal/api` for any site keying, comparing or rendering a caller-supplied UUID string rather than
its re-encoding. **The sweep's first pass concluded "no caller renders `parseUUID`'s raw-string
error", and that was wrong.** Phase 4 caught it and the plan document carries a dated in-place
correction. This item is the residue of that correction; nothing about it was changed by that slice.

## Repro / Symptoms

`GET /v1/workers/<not-a-uuid>/workspaces` returns 400 with the parser's own text rather than a fixed
message. `parseUUID` wraps as `fmt.Errorf("invalid UUID %q: %w", s, err)` and pgx's inner error is
`fmt.Errorf("cannot parse UUID %v", src)`, so the response carries the path segment **twice**: once
`%q`-quoted by the wrapper, once verbatim from pgtype.

**Why this is not filed as security, stated explicitly so nobody re-escalates it:**

- `writeError` delegates to `writeJSON`, which uses `json.NewEncoder`, so the segment is JSON-escaped
  on the way out. There is no injection into the response.
- The input is a URL path segment, bounded by `http.Server`'s `MaxHeaderBytes`, so there is no
  amplification.
- Nothing is keyed, compared or logged on the raw string; it reaches the response and nothing else.
- The identical class at `handleCreateReservation`'s `worker_ids` loop (`"invalid worker_id: " + wid`)
  was examined during the same sweep and dismissed for the same reasons.

What it does leak is the fact that the id failed to parse *and how* - which the fixed message already
conveys - plus the shape of the internal error wrapping. That is a tidiness argument, not a threat
model.

## Proposal

Replace both with `writeError(w, http.StatusBadRequest, "invalid worker id")`, matching
`handleGetWorker`, `handleDisableWorker`, `handleWorkerMetrics` and every other worker-id handler in
the package.

The alternative worth considering rather than dismissing: decide that reflecting the parser text is
the *better* behaviour for a developer-facing API and change the other 23 to match. That is a bigger,
more debatable change and it collides with
[[bug-2026-08-09-create-reservation-500-on-client-error]], whose own acceptance criterion is "no raw
Postgres error text reaches the response body". **The two must agree on one rule.** If the fixed
message wins - which is this item's recommendation - say so once, somewhere a future handler author
will read it.

## Acceptance / Done When

- Neither `workspaces.go` handler passes `err.Error()` to `writeError`.
- A test asserts the 400 body for a malformed worker id does **not** contain the supplied path
  segment, for at least one of the two routes.
- The rule ("`parseUUID` failures produce a fixed caller-facing message; the parser's text is for
  logs, not for clients") is stated once, at `parseUUID` in `internal/api/server.go`, so the next
  handler inherits it without a sweep.

## Related

- Source: `internal/api/workspaces.go` (`handleListWorkerWorkspaces`, `handleEvictWorkerWorkspace`),
  `internal/api/server.go` (`parseUUID`, `writeError`, `writeJSON`), `internal/api/reservations.go`
  (`handleCreateReservation`, the input-reflecting variant of the same class)
- The sweep that found it, and the plan section carrying the dated correction:
  [[bug-2026-08-15-reconcile-compares-wire-task-ids-against-canonical-ones]] (closed),
  `docs/superpowers/plans/2026-08-20-reconcile-canonical-task-ids.md` ("The sweep", `internal/api`
  table), `docs/retros/2026-08-20-reconcile-canonical-task-ids.md`
- Must agree with this on one rule for error bodies:
  [[bug-2026-08-09-create-reservation-500-on-client-error]]
- Adjacent, about the same path segments on the client side:
  [[bug-2026-08-12-unencoded-path-interpolation-api-clients]]

## Notes

Also worth recording from the same sweep, because it is the same shape and is **deliberate**:
`internal/api/events.go`'s `?job_id=` is neither parsed nor canonicalized, so an uppercase `job_id`
yields an open, permanently empty SSE stream. The existing comment states that the asymmetry with
`?task_id=` is intentional and that changing it is a client-contract change. Not part of this item;
noted so a reader arriving here from the sweep does not open it a third time.
