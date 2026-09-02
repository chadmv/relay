---
title: JobsPage and WorkersPage carry near-verbatim persisted view switches that already diverge
type: idea
status: open
created: 2026-09-02
priority: low
source: Phase 4 invariants lens on the 2026-09-01 jobs-lanes slice (lane F)
---

# Extract usePersistedView before its third consumer

## Summary
JobsPage copied WorkersPage's localStorage-persisted view switch (the key shape, the lazy read, the
setter, the aria-pressed pill markup) and the two copies already disagree: Jobs guards the storage read
and write with try/catch, Workers does not. A reader of either file draws the wrong conclusion about
whether the guard is needed, and a fix to one will not reach the other. The house rule is extract
before the third consumer; this is the second.

## Proposal
A usePersistedView hook in web/src/lib/ taking the key, the allow-list and the fallback, returning the
view and its setter, validating the stored value in one place, adopted by both pages behind a
byte-identical-test refactor gate. Add the group role and name both switches should carry.

## Related
- web/src/jobs/JobsPage.tsx, web/src/workers/WorkersPage.tsx
