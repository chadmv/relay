# p4 Binary & Ticket Diagnostics Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add diagnostics-only signals when the agent host lacks the `p4` CLI or a valid Perforce ticket, so operators see a loud startup warning and clearer per-task failure messages instead of generic wrapped errors.

**Architecture:** Two complementary changes. (1) A `(*perforce.Provider).Preflight(ctx) error` method that runs `exec.LookPath("p4")` at startup; the agent main logs and continues with `provider = nil` on failure. (2) A `classifyP4Error(err) error` helper that rewraps four known stderr patterns ("executable not found", "P4PASSWD invalid", "session expired", "connect to server failed") with operator-facing messages, applied at every `Client.*` call site in `perforce.go`.

**Tech Stack:** Go 1.22, `os/exec`, `errors`/`fmt`/`strings`. No new dependencies. Existing `fakeRunner` test fixture is sufficient.

**Spec:** `docs/superpowers/specs/2026-05-02-p4-binary-diagnostics-design.md`. Read it before starting Task 1.

---

## File Structure

**New files:**
- `internal/agent/source/perforce/diagnostics.go` — `classifyP4Error` helper
- `internal/agent/source/perforce/diagnostics_test.go` — table-driven test
- `internal/agent/source/perforce/perforce_preflight_test.go` — `Preflight` tests

**Modified files:**
- `internal/agent/source/perforce/perforce.go` — add `ErrP4BinaryMissing`, `lookPath` var, `Preflight` method, wrap nine call sites with `classifyP4Error`
- `internal/agent/source/perforce/perforce_test.go` — one new wiring test
- `cmd/relay-agent/main.go` — call `pp.Preflight(ctx)` and gate `provider = pp` on success

**Backlog housekeeping:**
- `git mv docs/backlog/bug-2026-04-25-p4-binary-assumed-authenticated.md docs/backlog/closed/` after the implementation lands and tests pass.

---

## Task 1: Implement `classifyP4Error` (table-driven, isolated)

**Files:**
- Create: `internal/agent/source/perforce/diagnostics.go`
- Test: `internal/agent/source/perforce/diagnostics_test.go`

This task implements the per-call error classifier in isolation. No production code in `perforce.go` calls it yet; that's Task 3.

- [ ] **Step 1: Write the failing test**

Create `internal/agent/source/perforce/diagnostics_test.go`:

```go
package perforce

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestClassifyP4Error(t *testing.T) {
	cases := []struct {
		name    string
		in      error
		wantSub string // substring expected in classified message; "" => passthrough
	}{
		{
			name:    "binary missing",
			in:      fmt.Errorf("p4 sync: %w", errors.New(`exec: "p4": executable file not found in $PATH`)),
			wantSub: "p4 binary not found on PATH",
		},
		{
			name:    "password invalid",
			in:      fmt.Errorf("p4 sync: %w", errors.New("exit status 1 (stderr: Perforce password (P4PASSWD) invalid or unset.)")),
			wantSub: "operator must run 'p4 login'",
		},
		{
			name:    "session expired",
			in:      fmt.Errorf("p4 sync: %w", errors.New("exit status 1 (stderr: Your session has expired, please login again.)")),
			wantSub: "p4 ticket expired",
		},
		{
			name:    "connect failed",
			in:      fmt.Errorf("p4 sync: %w", errors.New("exit status 1 (stderr: Perforce client error: Connect to server failed; check $P4PORT.)")),
			wantSub: "cannot reach Perforce server",
		},
		{
			name:    "tcp connect failed",
			in:      fmt.Errorf("p4 sync: %w", errors.New("exit status 1 (stderr: TCP connect to perforce.example.com:1666 failed.)")),
			wantSub: "cannot reach Perforce server",
		},
		{
			name:    "passthrough",
			in:      fmt.Errorf("p4 sync: %w", errors.New("exit status 1 (stderr: File(s) not in client view.)")),
			wantSub: "",
		},
		{
			name:    "nil",
			in:      nil,
			wantSub: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := classifyP4Error(tc.in)
			if tc.in == nil {
				if got != nil {
					t.Fatalf("nil input must yield nil, got %v", got)
				}
				return
			}
			if tc.wantSub == "" {
				// Passthrough: same string, same identity (errors.Is to itself).
				if got.Error() != tc.in.Error() {
					t.Errorf("expected passthrough; got=%q in=%q", got, tc.in)
				}
				return
			}
			if !strings.Contains(got.Error(), tc.wantSub) {
				t.Errorf("missing %q in classified message: %v", tc.wantSub, got)
			}
			if !errors.Is(got, tc.in) {
				t.Error("classified error must wrap original via %w (errors.Is failed)")
			}
		})
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/agent/source/perforce/... -run TestClassifyP4Error -v -timeout 30s`

Expected: FAIL with `undefined: classifyP4Error`.

- [ ] **Step 3: Implement `classifyP4Error`**

Create `internal/agent/source/perforce/diagnostics.go`:

```go
package perforce

import (
	"fmt"
	"strings"
)

// classifyP4Error wraps known-bad p4 errors with operator-facing guidance.
// Unrecognized errors are returned unchanged. The classified error preserves
// the original via %w so callers can still errors.Is / errors.Unwrap.
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

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/agent/source/perforce/... -run TestClassifyP4Error -v -timeout 30s`

Expected: PASS, all 7 subtests green.

- [ ] **Step 5: Run the full perforce package tests to confirm no regressions**

Run: `go test ./internal/agent/source/perforce/... -timeout 60s`

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/agent/source/perforce/diagnostics.go internal/agent/source/perforce/diagnostics_test.go
git commit -m "feat(perforce): add classifyP4Error for operator-facing diagnostics"
```

---

## Task 2: Add `Preflight`, `ErrP4BinaryMissing`, and `lookPath` testability hook

**Files:**
- Modify: `internal/agent/source/perforce/perforce.go`
- Create: `internal/agent/source/perforce/perforce_preflight_test.go`

This task adds the startup preflight in isolation. The wiring into `cmd/relay-agent/main.go` is Task 4.

- [ ] **Step 1: Write the failing tests**

Create `internal/agent/source/perforce/perforce_preflight_test.go`:

```go
package perforce

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"testing"
)

func TestPreflight_BinaryPresent(t *testing.T) {
	orig := lookPath
	t.Cleanup(func() { lookPath = orig })
	lookPath = func(name string) (string, error) {
		if name != "p4" {
			t.Fatalf("unexpected lookup: %s", name)
		}
		return "/usr/bin/p4", nil
	}
	p := New(Config{Root: t.TempDir(), Hostname: "h", Client: &Client{r: newFakeP4Fixture()}})
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
	p := New(Config{Root: t.TempDir(), Hostname: "h", Client: &Client{r: newFakeP4Fixture()}})
	err := p.Preflight(context.Background())
	if !errors.Is(err, ErrP4BinaryMissing) {
		t.Fatalf("expected errors.Is(err, ErrP4BinaryMissing) to be true, got %v", err)
	}
	if !strings.Contains(err.Error(), "RELAY_WORKSPACE_ROOT") {
		t.Errorf("error must mention RELAY_WORKSPACE_ROOT for operator guidance: %v", err)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/agent/source/perforce/... -run TestPreflight -v -timeout 30s`

Expected: FAIL with compile errors — `undefined: lookPath`, `undefined: ErrP4BinaryMissing`, `p.Preflight undefined`.

- [ ] **Step 3: Add `lookPath`, `ErrP4BinaryMissing`, and `Preflight` to `perforce.go`**

Edit `internal/agent/source/perforce/perforce.go`:

First, update the import block to add `errors` and `os/exec`. The current imports are:

```go
import (
	"context"
	"crypto/sha256"
	"encoding/base32"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"relay/internal/agent/source"
	relayv1 "relay/internal/proto/relayv1"
)
```

Change to:

```go
import (
	"context"
	"crypto/sha256"
	"encoding/base32"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"relay/internal/agent/source"
	relayv1 "relay/internal/proto/relayv1"
)
```

Then, immediately after the `import` block and before `// Config holds constructor parameters for the Perforce provider.` (currently around line 18), insert:

```go
// ErrP4BinaryMissing indicates the p4 CLI is not on PATH on this host.
// Returned by (*Provider).Preflight; cmd/relay-agent uses errors.Is to
// recognize it and degrade gracefully.
var ErrP4BinaryMissing = errors.New("p4 binary not found on PATH")

// lookPath is exec.LookPath; overridable in tests via the same package-level
// var pattern used elsewhere in the codebase (see CLAUDE.md "Testability
// overrides").
var lookPath = exec.LookPath
```

Then, add the `Preflight` method. Insert it immediately after the `func (p *Provider) Type() string` line (currently line 42), so it sits with the other small `Provider` methods:

```go
// Preflight verifies the agent host is configured for Perforce work.
// Currently checks only that the p4 binary exists on PATH. Does not contact
// the Perforce server, by design — startup must remain fast and offline.
//
// On failure returns an error wrapping ErrP4BinaryMissing. Callers should
// use errors.Is(err, ErrP4BinaryMissing) to detect the recoverable case.
//
// The ctx parameter is currently unused but is kept in the signature so
// future preflight checks can be cancellable without a breaking change.
func (p *Provider) Preflight(ctx context.Context) error {
	_ = ctx
	if _, err := lookPath("p4"); err != nil {
		return fmt.Errorf("%w: install Perforce CLI on this worker or unset RELAY_WORKSPACE_ROOT: %v",
			ErrP4BinaryMissing, err)
	}
	return nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/agent/source/perforce/... -run TestPreflight -v -timeout 30s`

Expected: PASS, both subtests green.

- [ ] **Step 5: Run the full perforce package tests to confirm no regressions**

Run: `go test ./internal/agent/source/perforce/... -timeout 60s`

Expected: PASS.

- [ ] **Step 6: Run go vet to confirm imports are clean**

Run: `go vet ./internal/agent/source/perforce/...`

Expected: no output (clean).

- [ ] **Step 7: Commit**

```bash
git add internal/agent/source/perforce/perforce.go internal/agent/source/perforce/perforce_preflight_test.go
git commit -m "feat(perforce): add Provider.Preflight for startup binary check"
```

---

## Task 3: Wire `classifyP4Error` into `perforce.go` call sites + wiring test

**Files:**
- Modify: `internal/agent/source/perforce/perforce.go`
- Modify: `internal/agent/source/perforce/perforce_test.go`

Wraps every `Client.*` call site that returns an error with `classifyP4Error`. Adds one integration test that exercises the wiring end-to-end via `Provider.Prepare`.

- [ ] **Step 1: Write the failing wiring test**

Append to `internal/agent/source/perforce/perforce_test.go`:

```go
func TestProvider_Prepare_ClassifiesAuthError(t *testing.T) {
	root := t.TempDir()
	fr := newFakeP4Fixture()
	// ResolveHead is the first p4 call inside Prepare. Inject the canonical
	// "ticket invalid" stderr that execRunner would surface in production.
	fr.setErr("changes -m1 //s/x/...#head",
		fmt.Errorf("p4 changes -m1 //s/x/...#head: exit status 1 (stderr: Perforce password (P4PASSWD) invalid or unset.)"))

	p := New(Config{Root: root, Hostname: "h", Client: &Client{r: fr}})
	spec := &relayv1.SourceSpec{Provider: &relayv1.SourceSpec_Perforce{
		Perforce: &relayv1.PerforceSource{
			Stream: "//s/x",
			Sync:   []*relayv1.SyncEntry{{Path: "//s/x/...", Rev: "#head"}},
		},
	}}
	_, err := p.Prepare(context.Background(), "task-1", spec, func(string) {})
	require.Error(t, err)
	require.Contains(t, err.Error(), "operator must run 'p4 login'",
		"Prepare must surface the classified message so it appears in task failure logs")
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/agent/source/perforce/... -run TestProvider_Prepare_ClassifiesAuthError -v -timeout 30s`

Expected: FAIL — the existing `Prepare` returns `fmt.Errorf("resolve head for %s: %w", e.Path, err)` which contains the raw stderr but not the classified guidance.

- [ ] **Step 3: Wrap the nine `Client.*` error returns with `classifyP4Error`**

Edit `internal/agent/source/perforce/perforce.go`. Each change replaces an existing `fmt.Errorf("...: %w", ..., err)` return with the same expression wrapped by `classifyP4Error`. The `handle.Release()` calls are unchanged — they happen on the line before the return.

**Site 1 — `ResolveHead` failure inside `Prepare` (currently line 110-111):**

Old:

```go
				cl, err := p.cfg.Client.ResolveHead(ctx, e.Path)
				if err != nil {
					return nil, fmt.Errorf("resolve head for %s: %w", e.Path, err)
				}
```

New:

```go
				cl, err := p.cfg.Client.ResolveHead(ctx, e.Path)
				if err != nil {
					return nil, classifyP4Error(fmt.Errorf("resolve head for %s: %w", e.Path, err))
				}
```

**Site 2 — `CreateStreamClient` failure inside `Prepare` (currently line 163-166):**

Old:

```go
		if err := p.cfg.Client.CreateStreamClient(ctx, clientName, wsRoot, pf.Stream, tmpl); err != nil {
			handle.Release()
			return nil, fmt.Errorf("create client: %w", err)
		}
```

New:

```go
		if err := p.cfg.Client.CreateStreamClient(ctx, clientName, wsRoot, pf.Stream, tmpl); err != nil {
			handle.Release()
			return nil, classifyP4Error(fmt.Errorf("create client: %w", err))
		}
```

**Site 3 — `SyncStream` failure inside `Prepare` (currently line 194-197):**

Old:

```go
		if err := p.cfg.Client.SyncStream(ctx, wsRoot, clientName, syncSpecs, progress); err != nil {
			handle.Release()
			return nil, fmt.Errorf("p4 sync: %w", err)
		}
```

New:

```go
		if err := p.cfg.Client.SyncStream(ctx, wsRoot, clientName, syncSpecs, progress); err != nil {
			handle.Release()
			return nil, classifyP4Error(fmt.Errorf("p4 sync: %w", err))
		}
```

**Site 4 — `CreatePendingCL` failure inside `Prepare` (currently line 209-213):**

Old:

```go
		cl, err := p.cfg.Client.CreatePendingCL(ctx, wsRoot, clientName, "relay-task-"+taskID)
		if err != nil {
			handle.Release()
			return nil, fmt.Errorf("create pending CL: %w", err)
		}
```

New:

```go
		cl, err := p.cfg.Client.CreatePendingCL(ctx, wsRoot, clientName, "relay-task-"+taskID)
		if err != nil {
			handle.Release()
			return nil, classifyP4Error(fmt.Errorf("create pending CL: %w", err))
		}
```

**Site 5 — `Unshelve` failure inside `Prepare` (currently line 220-223):**

Old:

```go
			if err := p.cfg.Client.Unshelve(ctx, wsRoot, clientName, src, cl); err != nil {
				handle.Release()
				return nil, fmt.Errorf("unshelve %d: %w", src, err)
			}
```

New:

```go
			if err := p.cfg.Client.Unshelve(ctx, wsRoot, clientName, src, cl); err != nil {
				handle.Release()
				return nil, classifyP4Error(fmt.Errorf("unshelve %d: %w", src, err))
			}
```

**Sites 6 and 7 — Inside `recoverOrphanedCLs` (currently lines 297-303):**

Old:

```go
	for _, cl := range cls {
		if err := p.cfg.Client.RevertCL(ctx, wsRoot, clientName, cl); err != nil {
			return fmt.Errorf("revert orphan CL %d: %w", cl, err)
		}
		if err := p.cfg.Client.DeleteCL(ctx, wsRoot, clientName, cl); err != nil {
			return fmt.Errorf("delete orphan CL %d: %w", cl, err)
		}
	}
```

New:

```go
	for _, cl := range cls {
		if err := p.cfg.Client.RevertCL(ctx, wsRoot, clientName, cl); err != nil {
			return classifyP4Error(fmt.Errorf("revert orphan CL %d: %w", cl, err))
		}
		if err := p.cfg.Client.DeleteCL(ctx, wsRoot, clientName, cl); err != nil {
			return classifyP4Error(fmt.Errorf("delete orphan CL %d: %w", cl, err))
		}
	}
```

**Sites 8 and 9 — Inside `perforceHandle.Finalize` (currently lines 347-353):**

Old:

```go
	if revertErr != nil {
		return fmt.Errorf("revert CL %d: %w", h.pendingCL, revertErr)
	}
	if delErr != nil {
		return fmt.Errorf("delete CL %d: %w", h.pendingCL, delErr)
	}
	return nil
```

New:

```go
	if revertErr != nil {
		return classifyP4Error(fmt.Errorf("revert CL %d: %w", h.pendingCL, revertErr))
	}
	if delErr != nil {
		return classifyP4Error(fmt.Errorf("delete CL %d: %w", h.pendingCL, delErr))
	}
	return nil
```

Note: `PendingChangesByDescPrefix` (called from `recoverOrphanedCLs` at line 293) returns its error to the caller, which is `Prepare` at line 188-190. That call already handles the error by logging via `progress(fmt.Sprintf("[recover] %v", err))`, not by returning. The classified wrap on the inner CL revert/delete sites (sites 6 and 7 above) is sufficient — adding one for `PendingChangesByDescPrefix` would only flow into a `progress()` log line and isn't worth the indirection. Skip it.

- [ ] **Step 4: Run the wiring test to verify it passes**

Run: `go test ./internal/agent/source/perforce/... -run TestProvider_Prepare_ClassifiesAuthError -v -timeout 30s`

Expected: PASS.

- [ ] **Step 5: Run the full perforce package tests to confirm no regressions**

Run: `go test ./internal/agent/source/perforce/... -timeout 60s`

Expected: PASS. The existing tests still pass because every classified error wraps the original via `%w`, so `errors.Is` chains and substring assertions on the original message still hold.

- [ ] **Step 6: Run integration tests if Docker is available (optional but recommended)**

Run: `make test-integration` (or skip if Docker is not running locally — CI will run them).

Expected: PASS, including `TestPerforceIntegration_Lifecycle`.

- [ ] **Step 7: Commit**

```bash
git add internal/agent/source/perforce/perforce.go internal/agent/source/perforce/perforce_test.go
git commit -m "feat(perforce): classify p4 errors at provider call sites"
```

---

## Task 4: Wire `Preflight` into `cmd/relay-agent/main.go`

**Files:**
- Modify: `cmd/relay-agent/main.go`

This task is glue. No new unit test — main wiring is verified manually at PR time per the spec. The `Preflight` method itself is already covered by Task 2's tests.

- [ ] **Step 1: Edit the workspace-provider construction block**

Open `cmd/relay-agent/main.go` and locate the block currently at lines 63-94 that begins with `// Build workspace provider if RELAY_WORKSPACE_ROOT is set.`.

Old (this is the entire current block):

```go
	// Build workspace provider if RELAY_WORKSPACE_ROOT is set.
	var provider source.Provider
	if root := os.Getenv("RELAY_WORKSPACE_ROOT"); root != "" {
		pp := perforce.New(perforce.Config{
			Root:     root,
			Hostname: caps.Hostname,
		})
		provider = pp

		// Start sweeper if age or disk-pressure threshold is configured.
		maxAge := parseDurationEnv("RELAY_WORKSPACE_MAX_AGE", os.Getenv("RELAY_WORKSPACE_MAX_AGE"), 0)
		minFreeGB, _ := strconv.ParseInt(os.Getenv("RELAY_WORKSPACE_MIN_FREE_GB"), 10, 64)
		sweepInterval := parseDurationEnv("RELAY_WORKSPACE_SWEEP_INTERVAL", os.Getenv("RELAY_WORKSPACE_SWEEP_INTERVAL"), 15*time.Minute)
		if maxAge > 0 || minFreeGB > 0 {
			reg, err := pp.Registry()
			if err != nil {
				log.Fatalf("workspace registry: %v", err)
			}
			sw := &perforce.Sweeper{
				Root:          root,
				Reg:           reg,
				MaxAge:        maxAge,
				MinFreeGB:     minFreeGB,
				SweepInterval: sweepInterval,
				Client:        pp.Client(),
				ListLocked:    pp.LockedShortIDs,
				FreeDiskGB:    freeDiskGB,
				OnEvictedCB:   pp.InvalidateWorkspace,
			}
			go sw.Run(ctx)
		}
	}
```

New (preflight gates the entire `provider = pp` + sweeper setup):

```go
	// Build workspace provider if RELAY_WORKSPACE_ROOT is set.
	var provider source.Provider
	if root := os.Getenv("RELAY_WORKSPACE_ROOT"); root != "" {
		pp := perforce.New(perforce.Config{
			Root:     root,
			Hostname: caps.Hostname,
		})
		if err := pp.Preflight(ctx); err != nil {
			// Non-fatal: log loudly and run without the workspace provider.
			// Source-bearing tasks will fail at dispatch with the existing
			// "no source provider" path; non-source tasks still run.
			log.Printf("relay-agent: workspace provider disabled: %v", err)
		} else {
			provider = pp

			// Start sweeper if age or disk-pressure threshold is configured.
			maxAge := parseDurationEnv("RELAY_WORKSPACE_MAX_AGE", os.Getenv("RELAY_WORKSPACE_MAX_AGE"), 0)
			minFreeGB, _ := strconv.ParseInt(os.Getenv("RELAY_WORKSPACE_MIN_FREE_GB"), 10, 64)
			sweepInterval := parseDurationEnv("RELAY_WORKSPACE_SWEEP_INTERVAL", os.Getenv("RELAY_WORKSPACE_SWEEP_INTERVAL"), 15*time.Minute)
			if maxAge > 0 || minFreeGB > 0 {
				reg, err := pp.Registry()
				if err != nil {
					log.Fatalf("workspace registry: %v", err)
				}
				sw := &perforce.Sweeper{
					Root:          root,
					Reg:           reg,
					MaxAge:        maxAge,
					MinFreeGB:     minFreeGB,
					SweepInterval: sweepInterval,
					Client:        pp.Client(),
					ListLocked:    pp.LockedShortIDs,
					FreeDiskGB:    freeDiskGB,
					OnEvictedCB:   pp.InvalidateWorkspace,
				}
				go sw.Run(ctx)
			}
		}
	}
```

- [ ] **Step 2: Build the agent binary**

Run: `go build -o bin/relay-agent.exe ./cmd/relay-agent`

Expected: success, no compile errors.

- [ ] **Step 3: Run unit tests across the whole repo**

Run: `make test`

Expected: PASS.

- [ ] **Step 4: Manual verification — `p4` present**

This step is informational; do it on a host with `p4` on PATH if available, otherwise skip and note in the PR description that manual verification was deferred.

```bash
RELAY_WORKSPACE_ROOT=/tmp/relay-ws ./bin/relay-agent.exe --coordinator <addr>
```

Expected: agent starts normally; no "workspace provider disabled" log line.

- [ ] **Step 5: Manual verification — `p4` missing**

On a host without `p4`, or by temporarily renaming `p4` so `LookPath` fails:

```bash
PATH=/usr/bin:/bin RELAY_WORKSPACE_ROOT=/tmp/relay-ws ./bin/relay-agent.exe --coordinator <addr>
```

(Adjust `PATH` to exclude wherever `p4` lives, or move the binary.)

Expected: a single log line containing `relay-agent: workspace provider disabled: p4 binary not found on PATH: install Perforce CLI on this worker or unset RELAY_WORKSPACE_ROOT: ...`. Agent then continues to run normally (registers with coordinator, accepts non-source tasks).

- [ ] **Step 6: Commit**

```bash
git add cmd/relay-agent/main.go
git commit -m "feat(relay-agent): preflight p4 binary at startup"
```

---

## Task 5: Close the backlog item

**Files:**
- Move: `docs/backlog/bug-2026-04-25-p4-binary-assumed-authenticated.md` → `docs/backlog/closed/`

Per project convention (`feedback_backlog_housekeeping`), the backlog `git mv` is in-scope for the change that closes the bug, not optional cleanup.

- [ ] **Step 1: Add a resolution note to the file**

Open `docs/backlog/bug-2026-04-25-p4-binary-assumed-authenticated.md` and append a Resolution section so the closed file remains self-explanatory:

Append at end of file:

```markdown

## Resolution (2026-05-02)

Closed by the diagnostics pass:

- `(*perforce.Provider).Preflight` runs `exec.LookPath("p4")` at agent startup. On failure, `cmd/relay-agent/main.go` logs `workspace provider disabled: ...` once and continues running with `provider = nil` so non-source tasks still execute.
- `classifyP4Error` (`internal/agent/source/perforce/diagnostics.go`) rewraps four common stderr patterns ("executable not found", "P4PASSWD invalid", "session expired", "connect to server failed") with operator-facing guidance. Applied at every `Client.*` call site in `perforce.go`.

Per the original design contract (CLAUDE.md), Relay still does not manage P4 credentials — operators provision tickets via `p4 login` out-of-band. This closes the diagnostics gap, not the credential-management question (which remains a non-goal).

Spec: `docs/superpowers/specs/2026-05-02-p4-binary-diagnostics-design.md`.
```

- [ ] **Step 2: Move the file to the closed directory**

Run:

```bash
git mv docs/backlog/bug-2026-04-25-p4-binary-assumed-authenticated.md docs/backlog/closed/
```

Expected: success. Verify with `git status` that the rename is staged correctly.

- [ ] **Step 3: Commit**

```bash
git commit -m "backlog: close bug-2026-04-25-p4-binary-assumed-authenticated"
```

---

## Final verification

- [ ] **Step 1: Full test suite**

Run: `make test`

Expected: PASS.

- [ ] **Step 2: Vet & build**

Run: `go vet ./... && go build ./...`

Expected: clean output, success.

- [ ] **Step 3: Confirm commit log**

Run: `git log --oneline master..HEAD`

Expected: five commits in this order:

1. `feat(perforce): add classifyP4Error for operator-facing diagnostics`
2. `feat(perforce): add Provider.Preflight for startup binary check`
3. `feat(perforce): classify p4 errors at provider call sites`
4. `feat(relay-agent): preflight p4 binary at startup`
5. `backlog: close bug-2026-04-25-p4-binary-assumed-authenticated`

Each commit is a working state — tests pass after every step.
