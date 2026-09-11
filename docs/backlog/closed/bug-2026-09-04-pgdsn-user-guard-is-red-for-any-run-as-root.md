---
title: pgdsn's DSN user-arm guard is RED for any run as root, which is the documented local container route
type: bug
status: closed
created: 2026-09-04
priority: medium
source: Combined review of the windows cross-compile CI slice (2026-09-04), found outside that diff
closed: 2026-09-10
resolution: fixed
---

# pgdsn's DSN user-arm guard is RED for any run as root, which is the documented local container route

## Summary

`TestAssertDSNTargetsDatabase_UserArmCatchesQueryOverrideOnNoUserinfoDSN`
(`internal/testsupport/pgdsn/pgdsn_guards_test.go`) asserts that the derived default user is not
`"root"`. `wantDefaultUser` (`internal/testsupport/pgdsn/pgdsn.go`) returns the URL's userinfo
username when present, and otherwise strips `RawQuery` and hands the DSN to `pgx.ParseConfig`.
With `PGUSER` cleared - which the test does deliberately - pgx has no environment override left and
falls through to its default settings, which derive the user from the **OS account**. So on any host
whose OS user is `root`, `wantDefaultUser` returns `"root"` and the assertion fails.

## Repro / Symptoms

Measured 2026-09-04 with a throwaway probe in that package: for the test's own DSN
`postgres://example.invalid:5432/wanted?user=root`, the probe logged the OS user as `CHAD-PC\chadv`
and `wantDefaultUser` as `chadv` - the OS account name, nothing from the DSN.

Both assertions in the test fail together rather than one compensating for the other. Once
`wantUser == "root"`, `assertDSNTargetsDatabase` is called with that value, the full parse's
`cfg.User` is also `"root"` via the `?user=` override, the user arm matches, `runAssertDSN` returns
`false`, and the `require.True` below it fails too.

Observed as part of a full-suite run inside `docker run --rm ... golang:1.26`, which runs as root by
default:

```
--- FAIL: TestAssertDSNTargetsDatabase_UserArmCatchesQueryOverrideOnNoUserinfoDSN (0.00s)
        Error:      Should not be: "root"
        Messages:   wantDefaultUser must not itself adopt the query override it exists to let
                    assertDSNTargetsDatabase detect
```

## Context

**This is the CLAUDE.md-recommended local gate going red on a clean tree.** CLAUDE.md names the
Linux container as the reliable route for `-race` on this machine, and as the only local way to run
the `//go:build !windows` files that `go test` on Windows silently skips. A developer following that
guidance sees a red that has nothing to do with their change. It is green on GitHub's
`ubuntu-latest` only because that runner's user is `runner`, so no CI lane reports it either -
which is why it has survived.

**The mechanical cause is that one literal is doing two jobs.** `"root"` is both the value injected
through `?user=root` and the value asserted against. The test's own comment anticipates the shape -
it pins `PGUSER`, `PGSERVICE` and `PGSERVICEFILE` against an ambient `root` in the ENVIRONMENT - but
the OS-user axis produces the identical collision and is not pinned. Nothing about the test's actual
subject (that `wantDefaultUser` must not adopt a query override) requires the two values to be
equal.

## Proposal

Sketch only.

Choose a discriminator that no OS account can be named, so the injected value and the ambient state
cannot collide. The property under test is unchanged by the choice: it is that `wantDefaultUser`
ignores `?user=`, not that it ignores `root` specifically.

Check the sibling guards in the same file for the same coupling before fixing only this one - the
shape is "injected value equals asserted-against value", and a fix to one instance says nothing
about the others.

## Acceptance / Done When

- The test passes when the process owner is `root`, and still fails if `wantDefaultUser` starts
  honouring `?user=`.
- The full suite is green inside `docker run --rm ... golang:1.26`, so the route CLAUDE.md
  prescribes reports only real failures.
- Any sibling guard sharing the injected-equals-asserted coupling is either fixed or recorded as
  checked.

## Related

- `internal/testsupport/pgdsn/pgdsn_guards_test.go`, `internal/testsupport/pgdsn/pgdsn.go`
  (`wantDefaultUser`, `assertDSNTargetsDatabase`)
- `CLAUDE.md` - "Running `-race` locally", which names the container as the reliable route
- [[idea-2026-08-23-integration-only-guards-ci-never-runs]] - the item that put this package's
  lane in CI; that lane is green because its runner is not root, which is why this went unnoticed

## Resolution

Fixed in PR #206, squashed as `1bdac00a`. The injected user is now a single const carrying a colon -
illegal in a Unix account name, where it is `/etc/passwd`'s field separator, and in a Windows SAM
account name - so no host can produce it as an OS user, and the only way `wantDefaultUser` can
return it is by adopting the query override the test exists to detect. Verified in-container that
even `useradd --badname` cannot write a colon-bearing name into `/etc/passwd`.

**This item's Repro section is wrong on one point, and the correction is worth keeping.** It says
"Both assertions in the test fail together rather than one compensating for the other."
`require.NotEqual` calls `FailNow` via `runtime.Goexit`, so the `require.True` below it never
executes and the observed RED reports exactly one failure. The reasoning about what `runAssertDSN`
would return is correct in the abstract; it simply never gets there.

**Review caught the first fix weakening the guard it was fixing.** Percent-encoding the colon made
the test survive a mutation the pre-fix version killed: `wantDefaultUser` returns `cfg.User` from
`pgx.ParseConfig`, which DECODES the query, so a mutant adopting the query without decoding returns
the `%3A` form while `cfg.User` carries the colon form. The two differ, so `NotEqual` passes; they
differ from each other, so the arm still fires and `require.True` passes too - both assertions pass
while the function does exactly what the test forbids. Measured on a non-root host so the result was
not confounded by the bug being fixed. The escape was dropped and the colon now sits in the query
literally.

All eight sibling guards in the file were enumerated by shape and each recorded as fixed or checked.
One, `DerivesOSUserWhenDSNCarriesNone/explicit_userinfo_wins`, has the injected-equals-asserted shape
but was left alone after testing the premise: deleting `wantDefaultUser`'s userinfo branch leaves it
PASSING, because `pgx.ParseConfig` reads the same userinfo back out of the URL, so the OS account is
on no code path for that input.

Acceptance criterion 2 met: `go test -race ./... -count=1 -timeout 900s` inside `golang:1.26` as
root, 23 packages ok, zero data races, exit 0.

One finding was deliberately NOT fixed and is a separate item: any ambient `PG*` variable that makes
`pgconn` produce an invalid setting causes `ParseConfig` to ERROR, reddening sibling tests.
`PGSERVICE=nosuchservice` reddens three; `PGSSLMODE=bogus` reddens four, including this test. That is
a different coupling - ambient state making the parse fail rather than return a colliding value - it
is pre-existing, and the prescribed container sets no `PG*` vars. The existing three-variable
`t.Setenv` pin is a deny-list against an open set, so the item should be written on the general axis
rather than on `PGSERVICE`.
