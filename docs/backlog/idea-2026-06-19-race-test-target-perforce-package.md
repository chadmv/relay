---
title: Add a -race test target for the perforce package
type: idea
status: open
created: 2026-06-19
priority: medium
source: noticed while closing bug-2026-06-10-perforce-registry-races
---

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
