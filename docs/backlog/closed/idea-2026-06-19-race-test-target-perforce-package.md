---
title: Add a -race test target for the perforce package
type: idea
status: closed
created: 2026-06-19
closed: 2026-06-20
priority: medium
source: noticed while closing bug-2026-06-10-perforce-registry-races
---

## Resolution
Resolved 2026-06-20. Added a `make test-race` target and a new
`.github/workflows/go-ci.yml` Go CI workflow (the project had no Go CI before -
only Python workflows). Scope was widened beyond perforce to the whole module's
unit tests: CI (Linux) runs `go test -race ./... -timeout 180s` directly, covering
the agent send goroutine, the worker/grace registries, the scheduler, and the
perforce registry race guard. The shared `make test-race` Makefile target excludes
`relay/internal/agent` so it stays green for local Windows devs (a Windows-only
proctree race surfaced on the first `-race` run, filed as
`bug-2026-06-20-agent-proctree-windows-race`); that file is `//go:build windows`
so Linux/CI compiles the clean build and the full set is green there. The Makefile
documents the Windows `CC=/c/msys64/mingw64/bin/gcc.exe` requirement.
Co-delivered with `idea-2026-06-20-vet-integration-tagged-build` (folded in - same
CI/Makefile surface). Spec: `docs/superpowers/specs/2026-06-20-race-test-target-perforce-design.md`.

# Add a -race test target for the perforce package

## Summary
`make test` runs `go test ./... -timeout 120s` with no `-race`, and CI does not
run the race detector either. The new `internal/agent/source/perforce/registry_race_test.go`
only catches a reintroduced data race when invoked explicitly with `-race`. Add a
dedicated `-race` target (at least for the perforce package, ideally the whole
module) so registry/concurrency regressions are caught by the default
verification flow.

## Proposal
Add a `make test-race` target (e.g. `go test -race ./internal/agent/source/perforce/...`,
or the whole module) and wire it into CI. Consider gating it on a label or
running it nightly if whole-module `-race` is too slow for every push.

## Notes
On this Windows dev box `-race` requires a compatible gcc: the default Strawberry
Perl gcc 8.3.0 fails with `exit status 0xc0000139` on every package, while MSYS2
mingw64 gcc 13.2.0 works via `CC=/c/msys64/mingw64/bin/gcc.exe` (with its bin on
PATH). CI on Linux is unaffected.

## Related
- `internal/agent/source/perforce/registry_race_test.go`
- [[bug-2026-06-10-perforce-registry-races]] (closed) - the fix this test guards.
