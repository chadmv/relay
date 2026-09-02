---
title: Task-log row virtualization so the retained-line cap can rise
type: idea
status: open
created: 2026-09-02
priority: low
source: carved out of idea-2026-08-09-task-log-tail-and-paging-improvements by the 2026-09-01 tail-paging spec (lane D)
---

# Task-log row virtualization

## Summary
The log view renders every retained row; MAX_LINES caps retention, and the tail-open plus Load earlier
now bound how much history a reader pulls in. Raising the cap substantially needs virtualization so
the DOM cost does not grow with it. Deferred because jsdom performs no layout, so the property cannot
be pinned in the unit lane, and because once the view opens at the right end the cap stops being the
binding constraint.

## Proposal
A windowed row renderer in LogView with the browser harness as its lane (the only one that can measure
scroll height), and a decision on what Load earlier does once retention is no longer the limit.

## Related
- [[idea-2026-08-09-task-log-tail-and-paging-improvements]] (closed)
- web/src/jobs/LogView.tsx, web/src/jobs/logBuffer.ts
