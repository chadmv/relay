# P4CLIENT explicit-flag fix — design

**Date:** 2026-05-01
**Backlog item:** `docs/backlog/bug-2026-05-01-p4client-env-var-dependency.md`
**Scope:** `internal/agent/source/perforce`

## Problem

`internal/agent/source/perforce/client.go:117` documents that "Caller is responsible for setting `P4CLIENT` in env before calling" `SyncStream`. No production caller fulfills that contract. `Provider.Prepare` invokes `cfg.Client.SyncStream` without setting `P4CLIENT`, so `p4 sync` falls back to whatever `P4CLIENT` happens to be in the agent process's environment (or, on Windows, in the `p4 set` registry). On a host with no `P4CLIENT` set the command errors with "no client specified"; on a host with a stale `P4CLIENT` it errors with `Client '<name>' unknown` or — worse — applies sync to the wrong workspace silently.

The bug surfaced concretely while building the p4d testcontainer (2026-05-01): the integration test had to inject `t.Setenv("P4CLIENT", …)` to work around the gap.

The same gap applies to the other client-scoped methods (`CreatePendingCL`, `Unshelve`, `RevertCL`, `DeleteCL`, `PendingChangesByDescPrefix`) — `p4` resolves the active client identically for all of them.

## Goal

Close the contract gap at the source. Each `p4` invocation that depends on a client must name that client explicitly in its argv, and must run from a deterministic cwd, so behavior is independent of any env var or registry state on the agent host.

## Non-goals

- Managing other p4 env vars (`P4PORT`, `P4USER`, `P4CHARSET`, `P4PASSWD`, `P4TICKETS`). Operators still own host configuration and `p4 login`.
- Adopting `P4CONFIG`. Considered and rejected — it would replace one env-var dependency with three (env, file, cwd) and adds an exception path for non-workspace-scoped operations like `ResolveHead`.
- Threading cwd through methods that don't need it (`CreateStreamClient`, `DeleteClient`, `ResolveHead`).
- Refactoring how `Client` is constructed or shared across providers.

## Approach

Two coordinated changes inside `internal/agent/source/perforce`:

1. The `Runner` interface gains a `cwd string` parameter on both methods. Empty string = inherit the agent process's cwd.
2. Six `Client` methods that target a specific p4 client take a `client string` parameter and emit p4's global `-c <client>` flag before the subcommand.
3. `Provider.Prepare`, `Provider.recoverOrphanedCLs`, and `perforceHandle.Finalize` thread workspace dir and client name through to those calls.

This uses p4's documented client-resolution order (command-line `-c` is highest priority, ahead of `P4CLIENT` env, ahead of `P4CONFIG`, ahead of `p4 set`) so the result is immune to operator host state.

### Why not P4CONFIG

We compared this approach against writing a `.p4config` file at each workspace root and setting `P4CONFIG` on the agent process. Trade-off:

| | `-c` flag (chosen) | `.p4config` |
|---|---|---|
| New env var on agent | none | `P4CONFIG` |
| New on-disk artifact per workspace | none | `.p4config` file |
| Cwd dependency | optional hygiene | required for correctness |
| Cross-workspace ops (`ResolveHead`) | uniform | needs separate path |
| Visible in argv / test asserts | yes | no |
| `Runner` interface change | no | yes (cwd) |
| Client-method signatures change | yes (six methods) | no |

P4CONFIG is the more idiomatic Perforce solution if we ever need *general* per-workspace env (different charsets, users, etc.); for the narrow goal of binding each p4 invocation to a known client it adds dependencies without adding correctness.

## Design

### `Runner` interface

```go
type Runner interface {
    Run(ctx context.Context, cwd string, args []string, stdin io.Reader) ([]byte, error)
    Stream(ctx context.Context, cwd string, args []string, onLine func(string)) error
}
```

`execRunner` sets `cmd.Dir = cwd` when non-empty. The fake test runner records cwd on each call but does not include it in the lookup key — test fixture tables shouldn't have to embed `t.TempDir()` paths.

Cwd hygiene is largely orthogonal to the P4CLIENT fix: setting cwd doesn't influence which client p4 selects (cwd is not part of p4's client-resolution chain unless P4CONFIG is set, which we explicitly don't use). It buys us reproducibility — an operator copy-pasting an argv from logs runs the same command in the same dir as the agent did — and removes a hidden global. We add it now because the call sites that need cwd are exactly the call sites we're already touching.

### `Client` method signatures

Methods that gain `(cwd, client)` and emit a global `-c` flag:

```go
SyncStream(ctx, cwd, client string, specs []string, onLine func(string)) error
CreatePendingCL(ctx, cwd, client, description string) (int64, error)
Unshelve(ctx, cwd, client string, sourceCL, targetCL int64) error
RevertCL(ctx, cwd, client string, cl int64) error
DeleteCL(ctx, cwd, client string, cl int64) error
PendingChangesByDescPrefix(ctx, cwd, client, prefix string) ([]int64, error)
```

Implementation pattern:

```go
args := append([]string{"-c", client, "sync", "-q", "--parallel=4"}, specs...)
return c.r.Stream(ctx, cwd, args, onLine)
```

`PendingChangesByDescPrefix` ends up with `-c` appearing twice in argv: once as the global flag (selects the active client for the invocation), once as the `changes` subcommand's "filter by client" flag. The two occurrences mean different things and both are required.

Methods that do not change shape:

- `ResolveHead(ctx, path)` — server-global; called *before* a workspace exists (it resolves `#head` to a CL number for the baseline hash). Stays as-is and runs with no cwd binding.
- `CreateStreamClient(ctx, name, root, stream, template)` — names the new client inside the spec form; `-c` does not apply.
- `DeleteClient(ctx, name)` — names the target client in argv; server-global.

### Caller wiring

| Call site | Cwd | Client |
|---|---|---|
| `Provider.Prepare` → `SyncStream` | `wsRoot` | `clientName` |
| `Provider.Prepare` → `CreatePendingCL` | `wsRoot` | `clientName` |
| `Provider.Prepare` → `Unshelve` | `wsRoot` | `clientName` |
| `Provider.recoverOrphanedCLs` → `PendingChangesByDescPrefix` | `wsRoot` | `clientName` |
| `Provider.recoverOrphanedCLs` → `RevertCL` / `DeleteCL` | `wsRoot` | `clientName` |
| `perforceHandle.Finalize` → `RevertCL` / `DeleteCL` | `h.workspaceDir` | `h.clientName` |

`recoverOrphanedCLs` already takes `clientName`; its signature becomes `recoverOrphanedCLs(ctx, wsRoot, clientName)`. `Sweeper.evict` is unaffected — it calls only `DeleteClient`, which doesn't change.

`Provider.Prepare` already constructs `wsRoot` and `clientName` near the top of the function, before any of the calls listed above; no extra plumbing is required.

### Source comment cleanup

Remove `// Caller is responsible for setting P4CLIENT in env before calling.` from `client.go:117`. The contract is now closed at the source.

## Tests

### Unit tests

The fake runner (`fixtures_test.go`) keys calls by `strings.Join(args, " ")`. Existing fixtures of the form

```go
fr.set("sync -q --parallel=4 //s/x/...@12345", …)
```

become

```go
fr.set("-c "+clientName+" sync -q --parallel=4 //s/x/...@12345", …)
```

The deterministic client name is computable in tests via the same logic the production code uses (`relay_<sanitized-host>_<shortid>`, where `shortid` is the first six chars of lowercase base32 of `sha256(stream)`). The `expectedClientName` helper currently lives in `p4d_container_test.go`, which has `//go:build integration` and is therefore invisible to unit tests. Move it to `fixtures_test.go` (no build tag) so both unit and integration tests can call it.

Files updated:
- `perforce_test.go` — `TestProvider_PrepareCreatesClientAndSyncs`, `TestProvider_UnshelveAndFinalizeRevert`, `TestProvider_CrashRecovery_DeletesOrphanedPendingCLs`. Each touched fixture key gets the `-c <client>` prefix.
- `client_test.go` — unchanged. `TestClient_CreateStreamClient_*` and `TestClient_ResolveHead` exercise methods whose signatures don't change. `TestClient_RunFailureBubbles` likewise unchanged.

One additional assertion: in at least one test (e.g. `TestProvider_PrepareCreatesClientAndSyncs`) explicitly assert that the sync invocation's argv begins with `["-c", expectedClient]`, so a future refactor cannot silently drop the flag without test failure.

### Integration test

`perforce_integration_test.go`:

- Remove `t.Setenv("P4CLIENT", expectedClientName("ci", "//test/main"))` and the comment block above it (currently at lines 40–46).
- Keep `t.Setenv("P4CHARSET", "none")`, `P4CONFIG`, `P4PASSWD`, `P4TICKETS`. Those are defense-in-depth against operator-host pollution, not workarounds for this bug.

The integration test passing with the `P4CLIENT` setenv removed is the empirical proof that the bug is fixed.

### Verification

```
make test
make test-integration
```

Both must pass.

## Acceptance criteria

- [ ] Production `p4 sync` and the other client-dependent p4 commands work on a host with no `P4CLIENT` set anywhere (no env, no `p4 set` registry entry).
- [ ] The `// Caller is responsible for setting P4CLIENT in env` comment in `client.go` is removed.
- [ ] `perforce_integration_test.go` no longer injects `P4CLIENT`.
- [ ] At least one unit test asserts that a representative invocation's argv begins with `["-c", <expected-client>]`.
- [ ] `make test` and `make test-integration` both pass.
- [ ] Backlog item `bug-2026-05-01-p4client-env-var-dependency.md` is moved to `docs/backlog/closed/` with `status: closed`, `resolution: fixed`, and a `## Resolution` note pointing at the merge commit.

## Out-of-scope follow-ups

None identified. The other env vars hardened in the integration test (`P4CHARSET`, `P4CONFIG`, `P4PASSWD`, `P4TICKETS`) remain operator concerns, consistent with the project's Perforce model documented in `CLAUDE.md`.
