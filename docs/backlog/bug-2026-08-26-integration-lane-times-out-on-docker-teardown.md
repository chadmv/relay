---
title: Long -count=N integration runs trip the 15m global timeout inside Docker container teardown
type: bug
status: open
created: 2026-08-26
priority: low
source: 2026-08-26 worker-delete slice - reproduced with a full stack after ~3 suite runs and hundreds of containers
---

# Long -count=N integration runs trip the 15m global timeout inside Docker container teardown

## Summary

A flake-hunting run of the form `go test -tags integration -p 1 ./internal/api/... -count=20 -run ...`
died with `panic: test timed out after 15m0s`. The entire stack is inside Docker's HTTP client talking
to the Windows engine over the named pipe (`go-winio`) during testcontainers' `ContainerRemove` - not
in relay code, and not an assertion failure.

## Repro / Symptoms

Observed 2026-08-26 on Windows with Docker Desktop, after roughly three full suite runs and several
hundred ephemeral Postgres containers inside an hour:

```
panic: test timed out after 15m0s
    running tests:
        TestListReservations_Sort_OrderingAcrossKeys (2s)
...
github.com/testcontainers/testcontainers-go.(*DockerContainer).Terminate(...)
relay/internal/api_test.newTestPool.func1()
    internal/api/testhelper_test.go:85
FAIL  relay/internal/api  900.853s
```

The named test's own subtests had all finished; the hang is in cleanup. Docker was responsive again
immediately afterwards.

**Why this is worth an item rather than a shrug:** the failure renders as a bare `panic:`/`FAIL`
banner with **no test name attached**. Piped through `tail`, it is indistinguishable from a real code
failure - which is exactly what happened earlier in the same slice and cost a round of investigation
before the baseline and four full-capture runs established it was infrastructure.

## Proposal

Cheapest first:

- **Document it.** A line in CLAUDE.md's integration-test section describing the shape and saying to
  capture full output rather than piping to `tail`, so the next person recognises it in seconds.
- Consider raising the per-package `-timeout` for high-`-count` runs, or reusing one container across
  a package rather than per-test, if the fixture allows.
- Consider whether `newTestPool`'s cleanup should bound its own `Terminate` call so a hung teardown
  fails one test rather than the package.

## Acceptance / Done When

- The failure shape is documented where someone running the integration lane will find it.
- Either the teardown is bounded, or the doc explicitly says an unbounded Docker hang is a known
  environmental failure and how to tell it from a real one.

## Related

- `internal/api/testhelper_test.go:85` - the cleanup that hangs
- [[idea-2026-08-25-no-documented-working-local-race-lane]] - the same class: an environmental
  failure that presents identically to a code regression
- `docs/retros/2026-08-26-worker-delete.md`
