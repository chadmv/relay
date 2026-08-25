---
title: CI gates on -race but no working local invocation is documented
type: idea
status: open
created: 2026-08-25
priority: low
source: 2026-08-25 handler-pool-seam slice - second consecutive slice to lose time to the race lane
---

# CI gates on -race but no working local invocation is documented

## Summary

`.github/workflows/go-ci.yml` runs `go test -race ./...`, so `-race` is a merge gate. No working
local invocation is written down anywhere. Two consecutive slices have lost time rediscovering that,
and the 2026-08-25 slice shipped new mutex-guarded fixture state that was never exercised under
`-race` locally at all.

## Context

There are two distinct failure modes and they are easy to confuse, which is part of the cost:

- **The compiler.** The default Strawberry Perl gcc fails with exit `0xc0000139` on every package.
  The known fix is MSYS2 mingw64 (`CC=/c/msys64/mingw64/bin/gcc.exe`). This is recorded in a session
  memory but not in the repo.
- **The runtime.** Even with the right `CC`, ThreadSanitizer can fail to allocate its shadow arena:
  `ThreadSanitizer failed to allocate 0x000004670000 bytes (error code: 87)`. On 2026-08-25 this
  reproduced on `internal/tokenhash` - a trivial, untouched package - at `origin/main`, so it is
  environmental and not attributable to any change. It appears to be memory-pressure related, which
  makes it intermittent and therefore easy to misdiagnose as a regression.

The second is the dangerous one: it presents identically to a real failure, and the correct response
(measure it at `origin/main` on an untouched package before concluding anything) is exactly the
project's "diagnose a red gate, measure pre-existing both ways" rule - which nothing here reminds
anyone of.

## Proposal

Document the working local `-race` path in CLAUDE.md next to the other test commands, covering both
failure modes and their distinct remedies. If a container fallback is the only reliable route on
Windows, write down the actual command - it was used successfully for platform-gated tests before and
is recorded nowhere.

Not proposed: changing what CI does. The gate is correct; only its local reproducibility is missing.

## Acceptance / Done When

- CLAUDE.md carries a `-race` invocation that works on this machine, or names the container fallback
  if that is the only one that does.
- Both failure modes are named with their distinguishing symptom, so the runtime one is not
  misdiagnosed as a code regression.
- A substitute is stated for when the lane is genuinely unavailable (`-count=N` repetition is what
  the 2026-08-25 slice used, and it does not cover data races - say so plainly rather than implying
  equivalence).

## Related

- `.github/workflows/go-ci.yml` - the gate
- `CLAUDE.md` - where the other test commands live
- `docs/retros/2026-08-25-handler-pool-seam.md` - section 7, what the outage left unverified
- [[idea-2026-08-23-integration-only-guards-ci-never-runs]] - the sibling "CI runs a lane developers do not" problem, from the other direction
