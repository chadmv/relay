---
title: CLAUDE.md's canonical commands are all make, and make is not runnable on the dev machine
type: idea
status: open
created: 2026-09-10
priority: medium
source: Hit independently by three agents during the 2026-09-10 Now batch
---

# CLAUDE.md's canonical commands are all make, and make is not runnable on the dev machine

## Summary

Every command CLAUDE.md presents as canonical is a `make` target, and no `make` is on PATH under the
shell agents use. Only `C:\msys64\usr\bin\make.exe` and Strawberry Perl's `gmake` exist.

## Repro / Symptoms

`make test-pg-integration` fails with a not-found error. Three separate agents in one batch hit this
and each worked it out again from scratch.

**The obvious workaround is worse than the problem.** Prepending `C:\msys64\usr\bin` to PATH brings
an msys `git` along with `make`, and that git reports **every tracked file in the repo as modified**
under `core.autocrlf`. One agent also saw its console handling turn a lane's output into roughly 1.2 MB
of padded text with no `ok` or `FAIL` lines in it.

That is the never-conclude-nothing-to-revert trap arriving from a direction CLAUDE.md does not cover:
the file list is not wrong because of an edit, it is wrong because of which `git` ran.

## Context

What every agent ended up doing, successfully, is reading the recipe out of the Makefile and running
its `go test` line directly. That works and is what the guidance should say, since the Makefile
hardcodes each lane's package list and the recipe is one line.

The cost is not just the rediscovery. A lane list copied by hand can drift from the Makefile's, which
is the same class of defect as a census: the single source of truth stops being read.

## Proposal

Sketch only. Either document the absolute-path invocation with a warning never to put msys64 on PATH,
or state that agents should run the recipe's `go test` line directly and say how to find it. If the
second, consider whether anything should verify that a hand-run invocation still matches the
Makefile's package list.

## Acceptance / Done When

- A fresh agent can run any lane CLAUDE.md names without rediscovering this.
- The msys-git hazard is stated wherever the workaround is, not separately from it.
- Nothing in the guidance implies `make <target>` works as written on this machine.

## Related

- `CLAUDE.md` - the Commands block, every target in it
- `Makefile` - the recipes and their hardcoded package lists
- `CLAUDE.md`'s "Line endings" section - the never-conclude-nothing-to-revert rule this arrives at
  from an unexpected direction
