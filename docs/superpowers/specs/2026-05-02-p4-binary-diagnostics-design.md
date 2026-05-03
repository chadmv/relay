# Design: p4 binary & ticket diagnostics

**Backlog item:** [`bug-2026-04-25-p4-binary-assumed-authenticated`](../../backlog/bug-2026-04-25-p4-binary-assumed-authenticated.md)

## Problem

The Perforce source provider assumes two things about the agent host without verifying either:

1. The `p4` CLI is installed and on `PATH`.
2. A valid Perforce ticket is active for the agent's user.

When either assumption is wrong, every source-bearing task that lands on the worker fails at runtime. The failure surfaces as a generic wrapped error like `p4 sync: exec: "p4": executable file not found in $PATH (stderr: )` or `p4 sync: exit status 1 (stderr: Perforce password (P4PASSWD) invalid or unset.)`. Operators have to read carefully, click into a task, and infer that the worker — not the job — is the problem.

There is no signal at agent startup that the host is misconfigured for Perforce work. A worker can run for hours, fail every dispatched source task, and stay registered as "online".

## Non-goals

- **Relay-managed Perforce credentials.** `CLAUDE.md` is explicit that operators provision P4 tickets out-of-band. This design does not call `p4 login`, store passwords, or manage `P4TICKETS`.
- **Active connectivity check at startup.** No `p4 login -s`, `p4 info`, or other call that reaches the Perforce server. Startup stays fast and offline; transient network problems on agent boot must not block the agent.
- **Reworking the `Client` layer.** The raw stderr propagation in `execRunner` is correct and useful for tests. Operator-facing classification happens at the provider boundary, not inside `Client`.

## Approach

Two complementary changes:

1. **Startup preflight** — a new `(*perforce.Provider).Preflight(ctx) error` method that runs `exec.LookPath("p4")` and returns a sentinel error if missing. `cmd/relay-agent/main.go` calls it after `perforce.New(...)`; on `ErrP4BinaryMissing` it logs loudly, sets `provider = nil`, and the agent continues running so non-source tasks still execute.

2. **Per-call error classification** — a new `classifyP4Error(err error) error` helper in `internal/agent/source/perforce/diagnostics.go` that recognizes a small set of known-bad stderr patterns and rewraps them with operator-facing guidance. Applied at every `Client.*` call site inside `perforce.go`. The original error is preserved via `%w` so `errors.Is`/`errors.Unwrap` still work.

This is diagnostics-only. The contract that operators own P4 credentials is unchanged.

## Architecture

### `internal/agent/source/perforce/perforce.go` — Preflight method

```go
// ErrP4BinaryMissing indicates the p4 CLI is not on PATH on this host.
// Returned by (*Provider).Preflight; main.go uses errors.Is to recognize it.
var ErrP4BinaryMissing = errors.New("p4 binary not found on PATH")

// lookPath is exec.LookPath; overridable in tests.
var lookPath = exec.LookPath

// Preflight verifies the agent host is configured for Perforce work.
// Currently checks only that the p4 binary exists on PATH. Does not contact
// the Perforce server, by design — startup must remain fast and offline.
func (p *Provider) Preflight(ctx context.Context) error {
    if _, err := lookPath("p4"); err != nil {
        return fmt.Errorf("%w: install Perforce CLI on this worker or unset RELAY_WORKSPACE_ROOT: %v",
            ErrP4BinaryMissing, err)
    }
    return nil
}
```

The `ctx` parameter is currently unused but is part of the signature for future extensibility (e.g., bounded `p4 -V` invocation). Keeping it now avoids a signature break later.

The package-level `lookPath` variable mirrors the existing testability-override pattern documented in `CLAUDE.md` ("Several package-level `var` functions can be swapped in tests without build tags").

### `cmd/relay-agent/main.go` — wiring

Replace the current block (around lines 63–94):

```go
var provider source.Provider
if root := os.Getenv("RELAY_WORKSPACE_ROOT"); root != "" {
    pp := perforce.New(perforce.Config{
        Root:     root,
        Hostname: caps.Hostname,
    })
    if err := pp.Preflight(ctx); err != nil {
        log.Printf("relay-agent: workspace provider disabled: %v", err)
        // Fall through with provider == nil. Source-bearing tasks will fail
        // at dispatch with the existing "no source provider" error path.
    } else {
        provider = pp
        // ... existing sweeper wiring unchanged ...
    }
}
```

Behavior:
- `p4` present → identical to today.
- `p4` missing, `RELAY_WORKSPACE_ROOT` set → one prominent log line at startup; agent continues; no sweeper goroutine; non-source tasks dispatch normally; source tasks fail with the existing "no provider" path.
- `RELAY_WORKSPACE_ROOT` unset → preflight not called (today's behavior).

### `internal/agent/source/perforce/diagnostics.go` — classifier

```go
package perforce

import (
    "fmt"
    "strings"
)

// classifyP4Error wraps known-bad p4 errors with operator-facing guidance.
// Unrecognized errors are returned unchanged. The wrapped error preserves the
// original via %w so callers can still errors.Is / errors.Unwrap.
func classifyP4Error(err error) error {
    if err == nil {
        return nil
    }
    msg := strings.ToLower(err.Error())
    switch {
    case strings.Contains(msg, "executable file not found"):
        return fmt.Errorf("p4 binary not found on PATH (install Perforce CLI on this worker or unset RELAY_WORKSPACE_ROOT): %w", err)
    case strings.Contains(msg, "perforce password (p4passwd) invalid or unset"):
        return fmt.Errorf("p4 ticket missing or invalid on this agent — operator must run 'p4 login' on the worker host: %w", err)
    case strings.Contains(msg, "your session has expired"):
        return fmt.Errorf("p4 ticket expired on this agent — operator must run 'p4 login' on the worker host: %w", err)
    case strings.Contains(msg, "connect to server failed"),
        strings.Contains(msg, "tcp connect to") && strings.Contains(msg, "failed"):
        return fmt.Errorf("cannot reach Perforce server from this agent — check P4PORT and network connectivity: %w", err)
    default:
        return err
    }
}
```

No additional sentinels are introduced for the ticket/connectivity cases. Callers don't currently need to distinguish "agent misconfigured" from "real task error" — they just bubble the classified error up to the task dispatch path. If a future caller needs programmatic discrimination, sentinels can be added and `errors.Is` integrated without touching the call sites.

### `internal/agent/source/perforce/perforce.go` — call-site wraps

Each call site that today wraps a `Client` error with `fmt.Errorf("...: %w", err)` is updated to wrap the result with `classifyP4Error`. Concretely:

| Line (approx) | Current call | New form |
|---|---|---|
| 109 | `return nil, fmt.Errorf("resolve head for %s: %w", e.Path, err)` | `return nil, classifyP4Error(fmt.Errorf("resolve head for %s: %w", e.Path, err))` |
| 163 | `return nil, fmt.Errorf("create client: %w", err)` | wrap with `classifyP4Error` |
| 194 | `return nil, fmt.Errorf("p4 sync: %w", err)` | wrap with `classifyP4Error` |
| 209 | `return nil, fmt.Errorf("create pending CL: %w", err)` | wrap with `classifyP4Error` |
| 221 (in loop) | `return nil, fmt.Errorf("unshelve %d: %w", src, err)` | wrap with `classifyP4Error` |
| 298 (in `recoverOrphanedCLs`) | `return fmt.Errorf("revert orphan CL %d: %w", cl, err)` | wrap with `classifyP4Error` |
| 301 (in `recoverOrphanedCLs`) | `return fmt.Errorf("delete orphan CL %d: %w", cl, err)` | wrap with `classifyP4Error` |
| 348 (`Finalize` revert) | `return fmt.Errorf("revert CL %d: %w", h.pendingCL, revertErr)` | wrap with `classifyP4Error` |
| 351 (`Finalize` delete) | `return fmt.Errorf("delete CL %d: %w", h.pendingCL, delErr)` | wrap with `classifyP4Error` |

The `handle.Release()` calls already in place are not affected — they happen before the wrap returns.

## Tests

### `internal/agent/source/perforce/perforce_preflight_test.go` (new, no build tag)

```go
func TestPreflight_BinaryPresent(t *testing.T) {
    orig := lookPath
    t.Cleanup(func() { lookPath = orig })
    lookPath = func(name string) (string, error) {
        if name != "p4" {
            t.Fatalf("unexpected lookup: %s", name)
        }
        return "/usr/bin/p4", nil
    }
    p := New(Config{Root: t.TempDir(), Hostname: "h", Client: &Client{r: &fakeRunner{}}})
    if err := p.Preflight(context.Background()); err != nil {
        t.Fatalf("Preflight: %v", err)
    }
}

func TestPreflight_BinaryMissing(t *testing.T) {
    orig := lookPath
    t.Cleanup(func() { lookPath = orig })
    lookPath = func(name string) (string, error) {
        return "", exec.ErrNotFound
    }
    p := New(Config{Root: t.TempDir(), Hostname: "h", Client: &Client{r: &fakeRunner{}}})
    err := p.Preflight(context.Background())
    if !errors.Is(err, ErrP4BinaryMissing) {
        t.Fatalf("expected ErrP4BinaryMissing, got %v", err)
    }
    if !strings.Contains(err.Error(), "RELAY_WORKSPACE_ROOT") {
        t.Errorf("error should mention RELAY_WORKSPACE_ROOT for operator guidance: %v", err)
    }
}
```

### `internal/agent/source/perforce/diagnostics_test.go` (new, no build tag)

One table-driven test:

```go
func TestClassifyP4Error(t *testing.T) {
    cases := []struct {
        name      string
        in        error
        wantSub   string // expected substring in wrapped message; "" means passthrough
    }{
        {"binary missing", fmt.Errorf("p4 sync: %w", errors.New(`exec: "p4": executable file not found in $PATH`)),
            "p4 binary not found on PATH"},
        {"password invalid", fmt.Errorf("p4 sync: %w", errors.New("exit status 1 (stderr: Perforce password (P4PASSWD) invalid or unset.)")),
            "operator must run 'p4 login'"},
        {"session expired", fmt.Errorf("p4 sync: %w", errors.New("exit status 1 (stderr: Your session has expired, please login again.)")),
            "p4 ticket expired"},
        {"connect failed", fmt.Errorf("p4 sync: %w", errors.New("exit status 1 (stderr: Perforce client error: Connect to server failed; check $P4PORT.)")),
            "cannot reach Perforce server"},
        {"passthrough", fmt.Errorf("p4 sync: %w", errors.New("exit status 1 (stderr: File(s) not in client view.)")),
            ""},
        {"nil", nil, ""},
    }
    for _, tc := range cases {
        t.Run(tc.name, func(t *testing.T) {
            got := classifyP4Error(tc.in)
            if tc.in == nil {
                if got != nil {
                    t.Fatalf("nil input should yield nil, got %v", got)
                }
                return
            }
            if tc.wantSub == "" {
                if got.Error() != tc.in.Error() {
                    t.Errorf("expected passthrough, got %v", got)
                }
                return
            }
            if !strings.Contains(got.Error(), tc.wantSub) {
                t.Errorf("missing %q in %v", tc.wantSub, got)
            }
            if !errors.Is(got, tc.in) {
                t.Errorf("classified error must wrap original (errors.Is failed)")
            }
        })
    }
}
```

### Integration test

No new integration test. Existing `perforce_integration_test.go` is unaffected — it always runs against a working `p4d` and never trips the classified paths.

### Manual verification (PR time)

1. On a host with `p4` installed: agent boots normally, source tasks run.
2. Temporarily rename `p4` (or run with `PATH=`): start the agent with `RELAY_WORKSPACE_ROOT` set, observe the warning log line, observe `provider = nil`, dispatch a non-source task and confirm it succeeds.
3. With `p4` present but no ticket: dispatch a source task, observe the wrapped error in the task failure reason on the server.

## Files Touched

**New:**
- `internal/agent/source/perforce/diagnostics.go`
- `internal/agent/source/perforce/diagnostics_test.go`
- `internal/agent/source/perforce/perforce_preflight_test.go`

**Modified:**
- `internal/agent/source/perforce/perforce.go` — add `ErrP4BinaryMissing`, `lookPath`, `Preflight`; wrap nine call sites with `classifyP4Error`.
- `cmd/relay-agent/main.go` — call `pp.Preflight(ctx)`; gate `provider = pp` and sweeper wiring on success; log on failure.

**Unchanged:**
- `internal/agent/source/perforce/client.go` — `Runner`, `execRunner`, `Client` methods all stay as-is.
- `internal/agent/source/perforce/perforce_integration_test.go` — no changes; existing `exec.LookPath("p4")` skip already covers the missing-binary case for tests.

## Out of scope / future work

- Active connectivity preflight (`p4 login -s` or `p4 info`) — could be added behind a `RELAY_PERFORCE_VERIFY=true` opt-in flag if operators want it. Not in this design.
- Per-task retry on transient ticket-expiry mid-job — out of scope. Operators handle ticket renewal; the classified error makes the failure obvious.
- Backlog item closure: this design closes [`bug-2026-04-25-p4-binary-assumed-authenticated`](../../backlog/bug-2026-04-25-p4-binary-assumed-authenticated.md). The implementation plan must include `git mv docs/backlog/bug-2026-04-25-p4-binary-assumed-authenticated.md docs/backlog/closed/` as in-scope work (per project convention that backlog housekeeping is part of the closing change, not optional cleanup).
