---
title: jobs.go carries thirteen partial hand-written store.Job literals, each dropping any column jobs gains
type: idea
status: open
created: 2026-09-02
priority: low
source: 2026-09-01 worker-detail-tasks-panel slice (lane E), Decision 4 and the backend engineer's measurement
---

# jobs.go carries thirteen partial hand-written store.Job literals

## Summary
internal/api/jobs.go copies query rows into store.Job literals by hand at thirteen sites (the spec
said six; the engineer counted the shape). Every one omits scheduled_job_id, and any column jobs gains
next is silently dropped at all of them. The worker-tasks endpoint avoided adding a fourteenth by
using two statements instead of a JOIN, which sidestepped the class without closing it.

## Proposal
A reflect.NumField arity guard comparing each copy against store.Job (the sibling item for the Python
mappers describes the shape), or a switch to sqlc.embed so the row type carries the whole struct.

## Related
- [[idea-2026-08-27-hand-written-to-spec-dict-mappers-need-an-arity-check]]
- internal/api/jobs.go
