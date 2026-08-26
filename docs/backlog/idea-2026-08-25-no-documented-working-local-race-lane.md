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

## Notes

**2026-08-25, windows-crlf-log-lines slice - a working invocation exists and was run green twice.**
The container lane is not a fallback to be described in the abstract; this command works on this
machine today:

```
MSYS_NO_PATHCONV=1 docker run --rm -v <abs-worktree-path>:/src -w /src \
  -e CGO_ENABLED=1 golang:1.26 go test -race ./internal/agent/... -count=1
```

Run green at two different HEADs during that slice (before and after a refactor that changed the
code under test), which is what makes it a verified recipe rather than a plausible one. That
satisfies the first acceptance criterion with a command instead of a promise; what remains open is
the CLAUDE.md edit itself and naming both failure modes.

Two details that cost time and belong with the command:

- **`MSYS_NO_PATHCONV=1` is not optional under Git Bash.** Without it, MSYS rewrites `-w /src` into
  `C:/Program Files/Git/src` and Docker refuses with "the working directory ... is invalid". The
  first attempt here failed exactly that way.
- **Do not judge the run by its exit code through a pipe.** That same failed attempt reported
  `exit 0`, because the `| tail` at the end of the pipeline supplied the status. The failure was
  only visible in the output text.

The same container run also executes the `//go:build !windows` files that `go test` skips wholesale
on Windows (`internal/agent/runner_cancel_test.go`), so it closes two local gaps at once, not one.
That is worth stating in CLAUDE.md alongside the command - see
[[feedback_platform_gated_test_verification]] for the standing rule it satisfies.
