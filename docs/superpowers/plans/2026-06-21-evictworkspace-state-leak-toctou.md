# EvictWorkspace State Leak and Lock TOCTOU Fix Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make `Provider.EvictWorkspace` clear per-task in-memory state on a manual eviction (matching the background sweeper) and close the lock TOCTOU window against a concurrent `Prepare`.

**Architecture:** Two defects, one file (`internal/agent/source/perforce/perforce.go`). (1) Wire `OnEvictedCB: p.InvalidateWorkspace` into the ad-hoc `Sweeper` so `syncedPaths`/workspace state for the evicted short ID is dropped. (2) Replace the check-then-evict gap with an atomic claim: under `p.mu`, verify the short ID is neither locked nor already reserved, mark it reserved in a new `evicting` set, release the lock, run the slow evict, then clear the reservation. `Prepare` consults the `evicting` set under `p.mu` and refuses a reserved short ID. The lock is never held across the slow p4/disk I/O in `evict`.

**Tech Stack:** Go, `sync.Mutex`, the existing fake-runner unit-test harness in package `perforce`.

**Slice independence:** Backend-only. No frontend slice. No `.sql`/`.proto` changes, so no `make generate` step.

---

## Background and design decisions (read before starting)

### Current code

`Provider.EvictWorkspace` (`internal/agent/source/perforce/perforce.go:267-283`):

```go
// EvictWorkspace deletes the workspace identified by shortID if it is not locked.
func (p *Provider) EvictWorkspace(ctx context.Context, shortID string) error {
	locked := p.lockedShortIDs()
	if locked[shortID] {
		return fmt.Errorf("workspace %s is currently in use", shortID)
	}
	reg, err := p.loadRegistry()
	if err != nil {
		return fmt.Errorf("load registry: %w", err)
	}
	e, ok := reg.Get(shortID)
	if !ok {
		return fmt.Errorf("workspace %s not found in registry", shortID)
	}
	sw := &Sweeper{Root: p.cfg.Root, Reg: reg, Client: p.cfg.Client, ListLocked: p.lockedShortIDs}
	return sw.evict(ctx, reg, e)
}
```

The background sweeper in `cmd/relay-agent/main.go:93-103` constructs its `Sweeper` with `OnEvictedCB: pp.InvalidateWorkspace`; the ad-hoc one above omits it.

Relevant collaborators:
- `Provider` struct (`perforce.go:36-42`): `mu sync.Mutex`, `workspaces map[string]*Workspace`, `reg *Registry`.
- `Provider.InvalidateWorkspace(shortID)` (`perforce.go:297-301`): takes `p.mu`, `delete(p.workspaces, shortID)`. This drops the in-memory `*Workspace`, which is where `syncedPaths` lives (`workspace.go:42`).
- `Provider.lockedShortIDs()` (`perforce.go:304-316`): takes `p.mu`, then for each workspace takes `ws.mu` and reports it locked when `len(ws.holders) > 0`.
- `Provider.Prepare` (`perforce.go:116-265`): the only place a holder is acquired. It takes `p.mu` briefly (lines 159-165) to get-or-create the in-memory `*Workspace`, releases `p.mu`, then calls `ws.Acquire` (`workspace.go:77`) which adds a holder under the separate `ws.mu`.
- `Sweeper.evict` (`sweeper.go:114-136`): calls `s.Client.DeleteClient` (p4 `client -d`, network I/O - see `client.go:103-106`) and `os.RemoveAll` (slow disk I/O), then `reg.Remove` + `reg.Save`, then `s.OnEvictedCB(w.ShortID)` if set.

### Decision 1 - OnEvictedCB wiring

`InvalidateWorkspace` is the correct callback. It is exactly what the background sweeper uses, and it clears the per-task cache (`p.workspaces[shortID]`, which owns `syncedPaths`) without touching the shared `*Registry` (the comment on `InvalidateWorkspace` at `perforce.go:292-296` confirms the registry is mutated in place via the shared pointer, so no `p.reg` nilling is needed). Fix: add `OnEvictedCB: p.InvalidateWorkspace` to the ad-hoc `Sweeper` literal. Exact field: `Sweeper.OnEvictedCB func(shortID string)` (`sweeper.go:26`); it is invoked at `sweeper.go:132-134`.

Note a self-deadlock check: `evict` calls `OnEvictedCB` AFTER it has finished all I/O and registry work, holding no `Provider` lock at that moment, so `InvalidateWorkspace` taking `p.mu` is safe. The TOCTOU claim (Decision 2) also releases `p.mu` before calling `evict`, so `InvalidateWorkspace` re-acquiring `p.mu` inside `evict` does not deadlock.

### Decision 2 - TOCTOU close (atomic claim, NOT lock-held-across-evict)

Rejected option (a) "hold `p.mu` across check+evict": `evict` performs p4 `client -d` (network) and `os.RemoveAll` (disk). Holding `p.mu` across that would block every concurrent `Prepare` and `lockedShortIDs` for the full duration of slow I/O. That violates relay lock hygiene (never hold a contended lock across slow I/O) and could stall the dispatcher. Rejected.

Chosen option (b) atomic claim via a reservation set:

1. Add a field `evicting map[string]bool` to `Provider`, guarded by `p.mu` (initialize it in `New` alongside `workspaces`).
2. In `EvictWorkspace`, under a single `p.mu` critical section, atomically: (a) check the short ID is not currently held (inline the holder check rather than calling `lockedShortIDs`, which re-locks `p.mu` and would self-deadlock) and (b) check it is not already in `evicting`; if either fails, return an error; otherwise set `evicting[shortID] = true`. Release `p.mu`.
3. Run `sw.evict(...)` with no `Provider` lock held.
4. In a deferred cleanup under `p.mu`, `delete(p.evicting, shortID)`.
5. In `Prepare`, inside the existing `p.mu` critical section that get-or-creates the `*Workspace` (lines 159-165), first check `if p.evicting[shortID]` and return an error if so. Because the reservation is set and cleared under `p.mu`, and `Prepare` reads it under `p.mu` at the exact point it would create/fetch the workspace, there is no window: either `Prepare` wins the lock first (reservation not yet set, eviction will then see the new holder and refuse) or `EvictWorkspace` wins first (reservation set, `Prepare` refuses).

Why this is race-free: the holder check and the reservation set/clear all happen under `p.mu`. `Prepare`'s holder creation also happens under `p.mu` and now also reads the reservation under `p.mu`. So the ordered states are total: either eviction observes "no holder, not reserved" and reserves before any `Prepare` proceeds, or `Prepare` has already created/will create the holder under `p.mu` first and eviction's holder check sees it. The slow `evict` runs outside the lock, but the reservation stays set for its whole duration, so `Prepare` keeps refusing until cleanup.

Invariant interactions:
- **No interior pointers across locks:** the inline holder check must not leak a `*Workspace` or `*holder` outside `p.mu`. It only reads `len(ws.holders)` under `ws.mu` (nested as `lockedShortIDs` already does) and returns a bool. No pointer escapes.
- **Lock ordering:** the existing `p.mu` -> `ws.mu` nesting order in `lockedShortIDs` is preserved; the inline check uses the same order. `evict` runs with no `Provider` lock held, so its `OnEvictedCB -> InvalidateWorkspace -> p.mu` acquisition cannot deadlock.

### Decision 3 - Share vs duplicate the Sweeper construction

Recommendation: do NOT extract a factory. There are exactly two call sites (background sweeper in `main.go`, ad-hoc in `EvictWorkspace`) and they differ materially: the background one sets `MaxAge`/`MinFreeGB`/`SweepInterval`/`FreeDiskGB` and runs a loop; the ad-hoc one evicts a single named entry. A shared factory would have to take all of those as parameters and would not meaningfully reduce drift for the one field that matters here (`OnEvictedCB`). Per the surgical-changes rule, just add the missing field to the existing literal. (If a third construction site ever appears, revisit.) This is a deviation-free choice consistent with the backlog proposal.

### Deviation from the proposal

The proposal says "close the lock gap so the locked-check and the evict are atomic." We implement that as an atomic-claim reservation rather than a literally-held lock, because the evict does slow I/O. This satisfies the proposal's intent (no concurrent `Prepare` can acquire the workspace between check and evict) without violating lock hygiene. No other deviations.

---

## File Structure

- Modify: `internal/agent/source/perforce/perforce.go`
  - `Provider` struct: add `evicting map[string]bool`.
  - `New`: initialize `evicting`.
  - `EvictWorkspace`: atomic claim + `OnEvictedCB` wiring + deferred reservation clear.
  - `Prepare`: refuse a reserved short ID inside the existing `p.mu` block.
- Test: `internal/agent/source/perforce/provider_evict_test.go` (new file; package `perforce`, no build tags - pure unit tests using the existing `fakeRunner`).

---

## Task 1: Manual eviction clears per-task state (OnEvictedCB)

**Files:**
- Test: `internal/agent/source/perforce/provider_evict_test.go` (create)
- Modify: `internal/agent/source/perforce/perforce.go:281` (the ad-hoc `Sweeper` literal)

- [ ] **Step 1: Write the failing test**

Create `internal/agent/source/perforce/provider_evict_test.go`:

```go
package perforce

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestEvictWorkspace_InvalidatesPerTaskState verifies that a manual eviction
// drops the in-memory *Workspace (and its syncedPaths) for the evicted short
// ID, matching the background sweeper's OnEvictedCB behavior.
func TestEvictWorkspace_InvalidatesPerTaskState(t *testing.T) {
	root := t.TempDir()
	fr := newFakeP4Fixture(t)
	fr.set("client -d relay_h_ws1", "Client deleted.\n")

	p := New(Config{Root: root, Hostname: "host", Client: &Client{r: fr}})

	// Seed the on-disk registry with a workspace to evict.
	reg, err := p.Registry()
	require.NoError(t, err)
	reg.Upsert(WorkspaceEntry{
		ShortID:    "ws1",
		SourceKey:  "//s/x",
		ClientName: "relay_h_ws1",
		LastUsedAt: time.Now(),
	})
	require.NoError(t, reg.Save())
	require.NoError(t, os.MkdirAll(filepath.Join(root, "ws1"), 0o755))

	// Seed in-memory per-task state for ws1 so we can prove it gets cleared.
	p.mu.Lock()
	w := NewWorkspace("ws1")
	w.syncedPaths = map[string]string{"//s/x/...": "baseline-abc"}
	p.workspaces["ws1"] = w
	p.mu.Unlock()

	require.NoError(t, p.EvictWorkspace(context.Background(), "ws1"))

	// The in-memory workspace entry (with syncedPaths) must be gone.
	p.mu.Lock()
	_, present := p.workspaces["ws1"]
	p.mu.Unlock()
	require.False(t, present, "EvictWorkspace must invalidate per-task state for the evicted short ID")
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/agent/source/perforce/... -run TestEvictWorkspace_InvalidatesPerTaskState -v -timeout 30s`
Expected: FAIL - `p.workspaces["ws1"]` is still present because the ad-hoc Sweeper has no `OnEvictedCB`, so `InvalidateWorkspace` never runs.

- [ ] **Step 3: Write minimal implementation**

In `internal/agent/source/perforce/perforce.go`, update the ad-hoc `Sweeper` literal in `EvictWorkspace` (currently line 281) to wire the callback:

```go
	sw := &Sweeper{
		Root:        p.cfg.Root,
		Reg:         reg,
		Client:      p.cfg.Client,
		ListLocked:  p.lockedShortIDs,
		OnEvictedCB: p.InvalidateWorkspace,
	}
	return sw.evict(ctx, reg, e)
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/agent/source/perforce/... -run TestEvictWorkspace_InvalidatesPerTaskState -v -timeout 30s`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/agent/source/perforce/provider_evict_test.go internal/agent/source/perforce/perforce.go
git commit -m "fix(perforce): invalidate per-task state on manual EvictWorkspace"
```

---

## Task 2: Add the eviction reservation set

**Files:**
- Modify: `internal/agent/source/perforce/perforce.go:36-42` (`Provider` struct), `perforce.go:45-51` (`New`)

This task adds the reservation field and initializes it. There is no standalone behavior to test yet; it is exercised by Task 3 and Task 4. Keep it tiny so Task 3/4 reference a real field.

- [ ] **Step 1: Add the field to the Provider struct**

In `internal/agent/source/perforce/perforce.go`, add `evicting` to `Provider`:

```go
// Provider implements source.Provider for Perforce.
type Provider struct {
	cfg        Config
	mu         sync.Mutex
	workspaces map[string]*Workspace // keyed by short_id
	evicting   map[string]bool       // short_ids reserved by an in-flight EvictWorkspace; guarded by mu
	reg        *Registry             // cached; loaded lazily
}
```

- [ ] **Step 2: Initialize it in New**

```go
func New(cfg Config) *Provider {
	if cfg.Client == nil {
		cfg.Client = NewClient()
	}
	cfg.Hostname = sanitizeHostname(cfg.Hostname)
	return &Provider{cfg: cfg, workspaces: map[string]*Workspace{}, evicting: map[string]bool{}}
}
```

- [ ] **Step 3: Verify it compiles**

Run: `go build ./internal/agent/source/perforce/...`
Expected: builds clean (the new field is unused for now; Go does not error on unused struct fields).

- [ ] **Step 4: Run the package tests to confirm no regression**

Run: `go test ./internal/agent/source/perforce/... -timeout 60s`
Expected: PASS (Task 1 test still green).

- [ ] **Step 5: Commit**

```bash
git add internal/agent/source/perforce/perforce.go
git commit -m "refactor(perforce): add eviction reservation set to Provider"
```

---

## Task 3: Atomic claim in EvictWorkspace; Prepare refuses a reserved short ID

**Files:**
- Test: `internal/agent/source/perforce/provider_evict_test.go` (add a test)
- Modify: `internal/agent/source/perforce/perforce.go` - `EvictWorkspace` (267-283) and `Prepare`'s get-or-create block (159-165)

- [ ] **Step 1: Write the failing test**

Append to `internal/agent/source/perforce/provider_evict_test.go`:

```go
// TestEvictWorkspace_PrepareRefusedWhileReserved verifies the atomic claim:
// once EvictWorkspace has reserved a short ID, a concurrent Prepare for that
// same stream is refused (cannot acquire the workspace mid-evict). We drive
// this deterministically by reserving the short ID directly under the lock
// (the same state EvictWorkspace sets) and asserting Prepare refuses.
func TestEvictWorkspace_PrepareRefusedWhileReserved(t *testing.T) {
	root := t.TempDir()
	fr := newFakeP4Fixture(t)
	p := New(Config{Root: root, Hostname: "host", Client: &Client{r: fr}})

	// Resolve the short ID Prepare would allocate for this stream.
	reg, err := p.Registry()
	require.NoError(t, err)
	shortID := allocateShortID("//depot/main", reg)

	// Simulate an in-flight eviction holding the reservation.
	p.mu.Lock()
	p.evicting[shortID] = true
	p.mu.Unlock()

	spec := &relayv1.SourceSpec{
		Source: &relayv1.SourceSpec_Perforce{
			Perforce: &relayv1.PerforceSpec{
				Stream: "//depot/main",
				Sync:   []*relayv1.PerforceSync{{Path: "//depot/main/...", Rev: "@1"}},
			},
		},
	}
	_, err = p.Prepare(context.Background(), "task-1", spec, func(string) {})
	require.Error(t, err, "Prepare must refuse a short ID reserved for eviction")
	require.Contains(t, err.Error(), "being evicted")
}
```

Add the import for `relayv1` at the top of the test file (the test file's import block becomes):

```go
import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	relayv1 "relay/internal/proto/relayv1"

	"github.com/stretchr/testify/require"
)
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/agent/source/perforce/... -run TestEvictWorkspace_PrepareRefusedWhileReserved -v -timeout 30s`
Expected: FAIL - `Prepare` does not yet consult `p.evicting`, so it proceeds (and likely fails later on a missing fixture or returns a handle) rather than returning the "being evicted" error.

- [ ] **Step 3: Write the implementation**

In `internal/agent/source/perforce/perforce.go`, replace `EvictWorkspace` (267-283) with the atomic-claim version:

```go
// EvictWorkspace deletes the workspace identified by shortID if it is not
// locked. The locked-check and eviction are atomic with respect to a concurrent
// Prepare: under p.mu we verify the workspace is neither held nor already being
// evicted, then reserve it in p.evicting so a concurrent Prepare refuses it.
// The reservation is held for the whole (slow) evict and cleared afterward; the
// lock is never held across the p4/disk I/O in evict.
func (p *Provider) EvictWorkspace(ctx context.Context, shortID string) error {
	p.mu.Lock()
	if ws, ok := p.workspaces[shortID]; ok {
		ws.mu.Lock()
		held := len(ws.holders) > 0
		ws.mu.Unlock()
		if held {
			p.mu.Unlock()
			return fmt.Errorf("workspace %s is currently in use", shortID)
		}
	}
	if p.evicting[shortID] {
		p.mu.Unlock()
		return fmt.Errorf("workspace %s is already being evicted", shortID)
	}
	p.evicting[shortID] = true
	p.mu.Unlock()

	defer func() {
		p.mu.Lock()
		delete(p.evicting, shortID)
		p.mu.Unlock()
	}()

	reg, err := p.loadRegistry()
	if err != nil {
		return fmt.Errorf("load registry: %w", err)
	}
	e, ok := reg.Get(shortID)
	if !ok {
		return fmt.Errorf("workspace %s not found in registry", shortID)
	}
	sw := &Sweeper{
		Root:        p.cfg.Root,
		Reg:         reg,
		Client:      p.cfg.Client,
		ListLocked:  p.lockedShortIDs,
		OnEvictedCB: p.InvalidateWorkspace,
	}
	return sw.evict(ctx, reg, e)
}
```

Then in `Prepare`, update the get-or-create block (currently 159-165) to refuse a reserved short ID under the same lock:

```go
	// Get or create the in-memory Workspace arbitrator.
	p.mu.Lock()
	if p.evicting[shortID] {
		p.mu.Unlock()
		return nil, fmt.Errorf("perforce: workspace %s is being evicted", shortID)
	}
	ws, ok := p.workspaces[shortID]
	if !ok {
		ws = NewWorkspace(shortID)
		p.workspaces[shortID] = ws
	}
	p.mu.Unlock()
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/agent/source/perforce/... -run TestEvictWorkspace_PrepareRefusedWhileReserved -v -timeout 30s`
Expected: PASS

- [ ] **Step 5: Run the whole package and the Task 1 test to confirm no regression**

Run: `go test ./internal/agent/source/perforce/... -timeout 120s`
Expected: PASS (Task 1's `TestEvictWorkspace_InvalidatesPerTaskState` still green - the inline holder check replaces the old `lockedShortIDs` call but preserves the not-found / not-locked behavior).

- [ ] **Step 6: Commit**

```bash
git add internal/agent/source/perforce/perforce.go internal/agent/source/perforce/provider_evict_test.go
git commit -m "fix(perforce): close EvictWorkspace/Prepare TOCTOU with atomic claim"
```

---

## Task 4: Regression test - a held workspace is not evicted (locked check preserved)

**Files:**
- Test: `internal/agent/source/perforce/provider_evict_test.go` (add a test)

This proves the inline holder check in the new `EvictWorkspace` still refuses an in-use workspace (the original guard) and that no `client -d` fixture is consulted (eviction never starts).

- [ ] **Step 1: Write the failing test**

Append to `internal/agent/source/perforce/provider_evict_test.go`:

```go
// TestEvictWorkspace_RefusesHeldWorkspace verifies the inline holder check:
// when a workspace currently has a holder, EvictWorkspace returns an error and
// does not attempt to delete the p4 client or the on-disk directory.
func TestEvictWorkspace_RefusesHeldWorkspace(t *testing.T) {
	root := t.TempDir()
	fr := newFakeP4Fixture(t) // no client -d fixture: any DeleteClient call fails the test
	p := New(Config{Root: root, Hostname: "host", Client: &Client{r: fr}})

	reg, err := p.Registry()
	require.NoError(t, err)
	reg.Upsert(WorkspaceEntry{
		ShortID:    "ws2",
		SourceKey:  "//s/y",
		ClientName: "relay_h_ws2",
		LastUsedAt: time.Now(),
	})
	require.NoError(t, reg.Save())
	require.NoError(t, os.MkdirAll(filepath.Join(root, "ws2"), 0o755))

	// Put a live holder on the workspace.
	p.mu.Lock()
	w := NewWorkspace("ws2")
	p.workspaces["ws2"] = w
	p.mu.Unlock()
	h, err := w.Acquire(context.Background(), Request{SyncPaths: []string{"//s/y/..."}})
	require.NoError(t, err)
	defer h.Release()

	err = p.EvictWorkspace(context.Background(), "ws2")
	require.Error(t, err, "EvictWorkspace must refuse a workspace that is currently held")
	require.Contains(t, err.Error(), "currently in use")

	// The on-disk directory must still exist (eviction never ran).
	_, statErr := os.Stat(filepath.Join(root, "ws2"))
	require.NoError(t, statErr, "held workspace directory must not be removed")
}
```

- [ ] **Step 2: Run test to verify it passes**

This test should already PASS against the Task 3 implementation (it is a regression guard for the inline holder check, not a new behavior).

Run: `go test ./internal/agent/source/perforce/... -run TestEvictWorkspace_RefusesHeldWorkspace -v -timeout 30s`
Expected: PASS. (If it fails because `DeleteClient` was called - i.e. the fakeRunner reports a missing `client -d relay_h_ws2` fixture - the holder check is broken; fix `EvictWorkspace` before proceeding.)

- [ ] **Step 3: Commit**

```bash
git add internal/agent/source/perforce/provider_evict_test.go
git commit -m "test(perforce): guard EvictWorkspace refuses a held workspace"
```

---

## Task 5: Full package run, race build, and vet

**Files:** none (verification only)

- [ ] **Step 1: Run the package unit tests**

Run: `go test ./internal/agent/source/perforce/... -timeout 120s`
Expected: PASS (all four `TestEvictWorkspace_*` tests plus the pre-existing suite).

- [ ] **Step 2: Vet the package**

Run: `go vet ./internal/agent/source/perforce/...`
Expected: no findings.

- [ ] **Step 3: Race build/run (deterministic-concurrency note)**

`internal/agent` was re-included in `make test-race`, and CI runs `-race` on Linux. On Windows `-race` needs the MSYS2 mingw64 gcc toolchain (see project memory: `CC=/c/msys64/mingw64/bin/gcc.exe`); the default Strawberry Perl gcc fails. If that toolchain is available locally, run:

Run (bash, Windows with MSYS2): `CC=/c/msys64/mingw64/bin/gcc.exe go test -race ./internal/agent/source/perforce/... -timeout 180s`
Expected: PASS, no race reports.

If the race toolchain is not available on this platform, do NOT claim the race dimension verified - note it for the integration tester / CI Linux run. The new tests are deterministic (they drive the reservation state directly under the lock and use synchronization primitives, not timing/sleeps), so they are race-clean by construction; the `-race` run confirms the production lock discipline in `EvictWorkspace`/`Prepare`.

- [ ] **Step 4: Final commit (if anything changed)**

No code changes expected in this task. If vet or race surfaced a fix, commit it with an appropriate message.

---

## Self-Review

**Spec coverage:**
- Defect 1 (per-task state leak / missing `OnEvictedCB`): Task 1 (wiring) + assertion that `syncedPaths`-bearing `*Workspace` is dropped.
- Defect 2 (lock TOCTOU): Task 2 (reservation field) + Task 3 (atomic claim in `EvictWorkspace`, refusal in `Prepare`) + Task 4 (held-workspace regression guard).
- Decision 1 (OnEvictedCB = `InvalidateWorkspace`): documented and implemented in Task 1.
- Decision 2 (atomic claim, not lock-held-across-evict): documented with rationale; implemented in Task 3; lock hygiene preserved (no slow I/O under `p.mu`).
- Decision 3 (no factory): documented; per surgical-changes rule.
- Testing: unit test for callback firing (Task 1), unit test for TOCTOU refusal (Task 3), regression guard (Task 4), race note (Task 5). All deterministic.

**Placeholder scan:** none - every code step shows real code; no TBD/TODO.

**Type consistency:** field `evicting map[string]bool` defined in Task 2, used identically in Task 3 (`EvictWorkspace`, `Prepare`) and seeded in tests (Tasks 3/4). `OnEvictedCB`/`InvalidateWorkspace`/`Sweeper`/`Workspace.holders`/`syncedPaths`/`Request`/`relayv1` types match the current source read during planning. The "being evicted" error string in `Prepare` matches the test's `require.Contains` assertion; the "currently in use" and "already being evicted" strings match likewise.

**Invariant interactions:** no-interior-pointers-across-locks honored (only a bool escapes the holder check); `p.mu` -> `ws.mu` ordering preserved; `evict` runs lock-free so `OnEvictedCB -> InvalidateWorkspace` re-acquiring `p.mu` cannot deadlock. No `.sql`/`.proto` edits, so no `make generate`. Epoch fence and job-spec pipeline are not touched.
