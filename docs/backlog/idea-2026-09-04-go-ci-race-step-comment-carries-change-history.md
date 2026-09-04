---
title: go-ci.yml's Race unit tests comment carries the change history current policy forbids
type: idea
status: open
created: 2026-09-04
priority: low
source: Combined review of the windows cross-compile CI slice (2026-09-04), noted as outside that diff
---

# go-ci.yml's Race unit tests comment carries the change history current policy forbids

## Summary

The `Race unit tests` step in `.github/workflows/go-ci.yml` carries a comment recording what a
previous version of that same comment said, why it was wrong, and the date it was fixed. CLAUDE.md's
Comments section puts dates and change history in git rather than in comments, so the comment is
outside current policy.

## Context

Noticed while the windows cross-compile step was added two steps below it. Left untouched
deliberately: it was outside that diff, and rewriting an unrelated comment inside a slice is how
unreviewed edits reach a file.

The content is not wrong - it accurately records that the comment once claimed the Makefile target
excluded `internal/agent` for a Windows-only proctree race, that the exclusion was removed when
the race was fixed, and that the comment was not updated at the time. The objection is only that a
comment is the wrong home for it. The surviving useful claim is the present-tense one: the step is
byte-identical to `make test-race`, and the two must be kept in step.

## Proposal

Delete the historical paragraph and keep the present-tense contract. Per CLAUDE.md the remedy for
this class is deletion, not correction - a correction writes a fresh claim that can drift in turn.
The history is already in git, and the closed item it cites
(`docs/backlog/closed/bug-2026-06-20-agent-proctree-windows-race.md`) is the durable record.

Worth doing as part of a sweep rather than alone: check the rest of the file for the same shape
while there. The three job-level timeout comments reason carefully about a real hazard and should
be left; the target is dated narrative about what a comment used to say.

## Acceptance / Done When

- The `Race unit tests` comment states only what is true now: that the step mirrors `make
  test-race` and that the two must stay in step.
- No comment in the file carries a date, a "corrected" note, or an account of what an earlier
  version said.
- The closed proctree-race item is not cited from a comment merely to justify history; if the
  citation earns its place it states a live constraint.

## Related

- `.github/workflows/go-ci.yml` (the `Race unit tests` step in the `test` job)
- `CLAUDE.md` - the Comments section, which forbids dates and change history in comments
- `docs/backlog/closed/bug-2026-06-20-agent-proctree-windows-race.md` - the history's real home
