---
title: List-filter remainder - multi-value status and label filters on jobs and workers, substring q on users
type: idea
status: open
created: 2026-09-03
priority: low
source: fan-in of the 2026-09-02 web-frontend batch; the unshipped part of idea-2026-05-06-list-endpoint-filters
---

# List-filter remainder - multi-value status and label filters on jobs and workers, substring q on users

## Summary
The 2026-05-06 list-filter idea shipped its jobs (q, mine, since, until), scheduled-jobs (enabled, q) and reservations (worker_id) halves in PRs #178 and #180. Three proposals from it have no implementation: a multi-value status filter on GET /v1/jobs and GET /v1/workers, a label filter (?label.<key>=<value>, JSONB containment) on jobs and workers, and a substring ?q= on GET /v1/users. This item carries exactly that remainder so the closed parent does not read as fully delivered.

## Context
GET /v1/jobs reads a single-value ?status= today and rejects ?sort= alongside any filter; GET /v1/workers reads no list filter at all; GET /v1/users reads ?email= (exact) and ?include_archived=. The shipped ?q= filters are unranked substring matches through the shared parseFilterQ, bounded in length, and deliberately keep the (created_at DESC, id DESC) cursor; the parent item noted that a relevance-ranked search would need a different cursor scheme, and that note still applies to anything here that changes sort order.

## Proposal
- Multi-value status: accept a comma-separated allow-listed set, validated against the status vocabulary the way TestTasksStatusVocabularyIsExactly pins it, and fold it into the existing filtered-list statement rather than adding a sibling statement per arity.
- Labels: ?label.<key>=<value> as JSONB containment; decide the index (GIN on labels) and the bound on the number of label predicates per request before exposing it, since each predicate multiplies the planner work on an unbounded table.
- Users ?q=: reuse parseFilterQ and the bound it enforces; an ILIKE over name and email is enough for the admin Users tab, which already debounces client-side.

## Acceptance / Done When
- Each new parameter rejects repeated occurrences and NUL bytes through the same guards parsePage applies to its parameters, and the 400 names the parameter.
- Every new filter has a cursor-walk integration test that crosses a real page boundary with the filter applied.
- README documents each parameter next to the filters PR #178 and PR #180 added.

## Related
- [[idea-2026-05-06-list-endpoint-filters]] (closed; the parent proposal)
- internal/api/job_filters.go, internal/api/list_filters.go, internal/api/pagination.go
