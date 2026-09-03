---
title: Retrofit the file:line citations under web/src now that web/CLAUDE.md forbids them
type: idea
status: open
created: 2026-09-02
priority: low
source: Phase 4 lenses on the 2026-09-01 pager chain (lane B of the web SPA batch)
---

# Retrofit the file:line citations under web/src

## Summary
The pager chain added the rule to web/CLAUDE.md (cite by symbol or phrase, never by line) and fixed
the three citations its own item named. A shape search for a file name followed by a colon and digits
under web/src matches roughly 342 lines across 107 files (axis: matching lines, not distinct citations;
includes cross-language pointers into Go, SQL and the hi-fi, and is not hand-verified per hit). One
survivor sits six lines from a citation the chain fixed: ReservationsTab.tsx still cites
internal/scheduler/dispatch.go by line range, which is both a line citation and a cross-language claim
the root CLAUDE.md separately forbids.

## Proposal
- Sweep by shape, converting each to a symbol, a phrase or a named test; delete pointers whose target
  no longer earns a comment.
- Promote the rule to the root CLAUDE.md Comments list so it governs Go too.
- Consider extending responsive.guard.test.ts's walk (it already strips comments) into a guard that
  flags a file-and-line pattern inside comments, converting the rule from prose into a check.

## Related
- [[bug-2026-08-14-stale-citations-in-gate-frozen-test-files]] (closed)
- web/CLAUDE.md, web/src/components/holo/responsive.guard.test.ts

## Notes
Lane JB's second re-verify (PR #178) counted 43 cross-file line-range citations from web/src and internal/store into internal/api/pagination.go, users.go and jobs.go, several already stale after that PR moved the query parsing into parsePage; those three files are the first targets for the retrofit.
