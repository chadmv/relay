---
title: A cursor whose value is the wrong kind for the sort key silently returns a wrong page instead of a 400
type: bug
status: open
created: 2026-08-13
priority: medium
source: Phase 4 of the 2026-08-13-web-enabler-list-endpoints slice; found independently by two lanes, pre-existing and deliberately not fixed there
---

# A cursor whose value is the wrong kind for the sort key silently returns a wrong page instead of a 400

## Summary

`parsePage` validates two of the three things a cursor asserts and not the third. It checks the
cursor decodes (`internal/api/pagination.go:267-271`) and that the sort string the cursor was issued
under matches the sort the request resolved to (`:272-283`). It never checks that the cursor's
**value kind** matches the sort key's kind, even though it has just computed that kind and stored it
in `pageParams.SortKind` (`:265`).

`SortKind` is populated and then read by **no production code anywhere in the tree**. The only other
reference is an assertion in `internal/api/pagination_test.go:359`. The field exists precisely for
this check and the check was never written.

The result is that a cursor carrying a text value under a timestamp sort - or a timestamp value
under a text sort - passes every gate, leaves the corresponding cursor field at its **zero value**,
and the endpoint answers with a wrong page instead of the 400 that every other malformation earns.

## Repro / Symptoms

**Text value under a timestamp sort** (this is what the lanes probed, against `GET /v1/invites`):

1. Craft `base64url({"i":"<any valid uuid>","s":"-created_at","v":"x"})`.
2. `decodeCursor` accepts it: exactly one of `T`/`V`/`N` is set, so the `setCount != 1` guard at
   `internal/api/pagination.go:122-124` passes; the UUID parses; `cursor.StrVal` is `"x"` and
   `cursor.T` is left at the zero `time.Time`.
3. `parsePage` accepts it: `c.Sort` is `-created_at`, which equals the resolved sort, so the
   mismatch check at `:279-282` passes.
4. `pageParams.CursorTs()` (`:231-233`) returns `{Time: 0001-01-01T00:00:00Z, Valid: true}` - `Valid`
   tracks only whether a cursor was **sent**, never whether its value was populated.
5. The keyset predicate becomes `(created_at, id) < ('0001-01-01', <id>)`, which no row satisfies.

**Observable:** `200` with `items: []`, `next_cursor: ""`, and a **non-zero `total`**. A page that is
empty while the footer says twelve, and no error anywhere. Every other bad cursor on the same
endpoint - malformed base64, unparseable JSON, two value fields set, a bad UUID, a sort mismatch -
returns `400 invalid cursor`. This one does not.

**The inverse direction is worse and is read-derived rather than probed.** On a text sort key,
`cursor.StrVal` is threaded into the query as `CursorV` (for example
`internal/api/users.go:249,263,277,291`, `internal/api/jobs.go:526-591`,
`internal/api/workers.go:194-239`, `internal/api/scheduled_jobs.go:287-429`,
`internal/api/reservations.go:136-149`). A timestamp-valued cursor under a text sort leaves `StrVal`
as `""`. For a **descending** text sort the comparison excludes everything, giving the same empty
page. For an **ascending** text sort, every row compares greater than `''`, so the cursor is
effectively ignored and the endpoint returns **page 1 again** - a client walking pages would loop
rather than advance. This direction has not been probed and should be the first thing the fix's test
pins.

## Context

**Pre-existing and endpoint-independent.** It lives entirely in the shared pagination machinery, so
it affects `GET /v1/jobs`, `/v1/workers`, `/v1/users`, `/v1/scheduled-jobs`, `/v1/reservations`,
`/v1/agent-enrollments`, and the two endpoints added by the 2026-08-13 slice, identically. Found
independently by two Phase 4 lanes on that slice and deliberately left unfixed there: the slice's
diff was two read endpoints, and a change to `parsePage` would have put every paginated endpoint in
the repository inside its blast radius.

**Not a security issue.** A cursor names a position, never a scope. All the identity and
authorization predicates are applied by the query and the middleware regardless of the cursor's
contents, so no row crosses a user boundary and no data is disclosed. The failure is a wrong answer,
not a leaked one, which is why this is medium rather than high.

**Reaching it requires editing a cursor by hand**, since the server only ever emits well-formed ones
(`encodeCursorV2`, `:54-75`, dispatches on the value's runtime type and panics on anything else).
That makes it unlikely to be hit accidentally, and makes it near-certain to be hit by anyone
scripting against the API, debugging a paging bug, or fuzzing.

## Proposal

Add the kind check to `parsePage`, immediately after the existing sort-mismatch check at
`internal/api/pagination.go:279-282`, using the `kind` already in hand from `parseSort`:

- `SortKeyTimestamp` requires the cursor to carry `T` (or `N`, for the nullable-column case that
  `IsNull` exists to serve, `:35,119-121`) and to carry no `V`.
- `SortKeyText` requires the cursor to carry `V` (or `N`) and to carry no `T`.
- Anything else is `400 invalid cursor`, reusing the existing message so the wire behaviour of a
  bad cursor stays uniform and nothing about the crafted value is echoed back
  (`decodeCursor`'s doc comment at `:90-96` already forbids echoing decoded bytes).

The check belongs in `parsePage` and not in `decodeCursor`: `decodeCursor` has no `SortSpec` and
cannot know what kind is expected, which is exactly why the gap exists.

Consider also whether `IsNull` should be accepted for a sort key whose column is `NOT NULL`. Today
an `N` cursor on a non-nullable column produces a comparison against a NULL that no row satisfies,
which is the same class of silently-empty page. That may be worth the same treatment, and it needs
the `SortSpec` to record nullability, so it is a larger change - decide it explicitly rather than
folding it in.

## Acceptance / Done When

- A crafted cursor with `S` matching the requested sort but a value of the wrong kind returns
  `400 invalid cursor`, proven RED against today's code. A test that only asserts "a malformed
  cursor is a 400" is vacuous here - the discriminating input is a cursor that is **well-formed in
  every respect except its value kind**.
- Both directions are covered: a `V`-carrying cursor on a timestamp sort, and a `T`-carrying cursor
  on a text sort. The second is the one whose current behaviour is a repeated page rather than an
  empty one, and it must be shown to be wrong today before it is fixed.
- The positive controls survive: a legitimate cursor round-trips and pages correctly on both a
  timestamp-sorted and a text-sorted endpoint, and the existing `NULLS`-ordered `N` cursor on
  `workers.last_seen_at` still works.
- One endpoint's integration test asserts the fix end to end, so the check is proven wired into
  `parsePage` and not merely unit-tested in isolation.
- `pageParams.SortKind` has a production reader when this lands. If the chosen implementation does
  not use it, delete the field rather than leaving a second unread one.

## Related

- Source: `internal/api/pagination.go:97-138` (`decodeCursor`), `:220-233` (`pageParams`,
  `CursorTs`), `:239-286` (`parsePage`, where the check belongs), `:265` (`SortKind` populated),
  `:54-75` (`encodeCursorV2`, which can only emit well-formed cursors)
- Consumers of the un-validated value:
  `internal/api/jobs.go:526-591`, `internal/api/users.go:249-387`,
  `internal/api/workers.go:194-239`, `internal/api/scheduled_jobs.go:287-429`,
  `internal/api/reservations.go:136-149`
- Found during: `docs/superpowers/specs/2026-08-13-web-enabler-list-endpoints.md` and
  `docs/retros/2026-08-13-web-enabler-list-endpoints.md`
- Adjacent pagination correctness item on the same machinery:
  [[bug-2026-08-09-task-list-ordering-has-no-tiebreaker]]
- Would extend the same validation surface if filters are added to list endpoints:
  [[idea-2026-05-06-list-endpoint-filters]]

## Notes

The generalizable shape is one this project keeps rediscovering in new clothing: **a validated
envelope is not a validated payload.** `parsePage` checks that the cursor is authentic to the
request's sort - the same currency question the epoch fence asks - and never checks that its contents
mean what the consumer will assume they mean. The tell was sitting in the type the whole time:
`pageParams.SortKind` is computed, stored, and read by nothing. **A field with no reader is either
dead or a check that was never written**, and it is worth grepping for the others.
