---
title: An MSW /logs fixture that omits prev_seq fails open, and nothing enforces the key
type: idea
status: open
created: 2026-09-02
priority: low
source: Phase 4 correctness lens on the 2026-09-01 tail-paging slice (lane D)
---

# An MSW /logs fixture that omits prev_seq fails open

## Summary
Every /logs response literal under web/src now carries prev_seq, by a hand sweep across seven test
files. MSW bodies are untyped, so a future fixture that omits it decodes as undefined, earlierComplete
stays false, and the view renders both the tail notice and a Load earlier button with no assertion
going red. The repo rule that fixtures never encode through the production type is right; the remedy
is a shared hand-written helper that always emits all four keys, not a type.

## Related
- web/src/jobs/useTaskLogStream.test.tsx, web/src/jobs/logSecrecy.test.tsx, web/src/jobs/LogTab.test.tsx
- CLAUDE.md "Where a CLI test goes" (the fixture rule)
