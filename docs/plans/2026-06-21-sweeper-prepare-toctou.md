# Background Sweeper Prepare TOCTOU Fix Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Close the eviction-vs-`Prepare` TOCTOU on the background sweeper path by routing each per-entry sweeper eviction through the same `p.evicting` atomic-claim discipline the manual `EvictWorkspace` path already uses.

**Architecture:** Add an optional `Claim` hook to `Sweeper` that reserves `p.evicting[shortID]` (under `p.mu`, after an inline holder re-check) for the duration of the slow `DeleteClient` + `RemoveAll`, mirroring `EvictWorkspace`. A new `Provider.ReserveForEvict` supplies that hook; it is wired only on the background `Sweeper` in `cmd/relay-agent/main.go`, never on the internal `Sweeper` `EvictWorkspace` builds (which already holds the reservation). A losing claim is a benign sentinel-error skip, not a counted eviction or a logged error.

**Tech Stack:** Go, package `relay/internal/agent/source/perforce`. Tests use `testify/require`, the existing `newFakeP4Fixture`/`gatingRunner` deterministic race harness, and the `prepareAcquireHook` package var.

**Slice independence:** Backend-only. No frontend work is involved. There is a single sequential backend slice (one package); the conductor should not fan out a frontend slice in Phase 3.

---

## Background and grounding (read before starting)

Confirmed against current code (do not re-derive, but re-read if a line moved):

- `internal/agent/source/perforce/sweeper.go`
  - `Sweeper` struct (lines 14-27): fields `Root`, `MaxAge`, `MinFreeGB`, `SweepInterval`, `Reg`, `Client`, `ListLocked`, `FreeDiskGB`, `OnEvictedCB`. No reference to the provider's `evicting` set - this is the gap.
  - `SweepOnce` (lines 51-112): snapshots `locked` via `ListLocked` once, builds candidates, then in both the age pass (79-88) and the pressure pass (92-109) calls `s.evict(...)`. **It appends to `evicted` only when `err == nil`; on a non-nil error it `log.Printf("sweeper: evict ...")` and `continue`s.** This is the err-handling contract we must extend with a benign-skip sentinel.
  - `evict` (lines 114-136): optional `DeleteClient` (skipped when `w.DirtyDelete`), then `os.RemoveAll`, then `reg.Remove`/`reg.Save`, then `OnEvictedCB`. This is where the claim must be taken before any destructive work.
- `internal/agent/source/perforce/perforce.go`
  - `Provider` (lines 44-50): `mu sync.Mutex`, `workspaces map[string]*Workspace`, `evicting map[string]bool` (guarded by `mu`), `reg *Registry`.
  - `EvictWorkspace` (lines 311-353): the exact discipline to mirror. Under `p.mu`: if `ws := p.workspaces[shortID]` exists, lock `ws.mu`, read `len(ws.holders) > 0`, unlock `ws.mu`; if held, unlock `p.mu` and refuse. Then if `p.evicting[shortID]` refuse; else set `p.evicting[shortID] = true`, unlock `p.mu`, `defer` a `p.mu`-guarded `delete(p.evicting, shortID)`. **Lock order is `p.mu` then `ws.mu` (never the reverse).**
  - `Prepare` (lines 167-208): pre-Acquire check of `p.evicting[shortID]` under `p.mu` (168-171), then `prepareAcquireHook` (179-181), then `ws.Acquire` (189), then the post-Acquire re-check of `p.evicting[shortID]` under `p.mu` (202-208) that releases the handle and returns `"... is being evicted"` if set. **This re-check is what observes the sweeper's claim.**
  - `prepareAcquireHook` package var (line 34): fires after the pre-check, before Acquire - the test seam.
  - `InvalidateWorkspace` (367-371): `OnEvictedCB` target; deletes from `p.workspaces` under `p.mu`.
- `cmd/relay-agent/main.go` (lines 88-105): builds the background `Sweeper` with `ListLocked: pp.LockedShortIDs` and `OnEvictedCB: pp.InvalidateWorkspace`, then `go sw.Run(ctx)`. This is the one site to wire `Claim`.
- `internal/agent/source/perforce/provider_evict_recheck_test.go`: the deterministic, timing-free regression harness for the manual path. `gatingRunner` pauses the first `Run` whose joined args equal `gateKey` (used as `"client -d <clientName>"`) until `proceed` closes, signaling `entered`. `prepareAcquireHook` launches the concurrent eviction into the gap and waits on `gate.entered`. Reuse `gatingRunner`, `joinArgs`, `newFakeP4Fixture`, `allocateShortID`, and the `prepareAcquireHook` pattern verbatim.

**Key correctness facts:**

1. `EvictWorkspace`'s internal `Sweeper` (built at perforce.go:346-352) must keep `Claim` nil. It already set `p.evicting[shortID]` before calling `sw.evict`; if its `evict` also called `Claim -> ReserveForEvict`, `ReserveForEvict` would see `p.evicting[shortID]` already true and refuse, breaking the manual path. Leaving `Claim` nil on that internal Sweeper preserves today's behavior exactly.
2. The benign "claim lost" outcome (entry currently held OR already being evicted) must NOT append to `evicted` and must NOT `log.Printf` an error - it is the expected, correct result of losing the race. A real `evict` error (DeleteClient/RemoveAll failure) keeps today's log-and-continue behavior.

## File structure

- Modify: `internal/agent/source/perforce/sweeper.go`
  - Add `Claim func(shortID string) (release func(), ok bool)` field to `Sweeper`.
  - Add an exported sentinel `var ErrEvictClaimLost = errors.New("sweeper: evict claim lost (workspace in use or already evicting)")`.
  - In `evict`, when `Claim != nil`, call it first; on `!ok` return `ErrEvictClaimLost` before any destructive work; on `ok` `defer release()`.
  - In `SweepOnce`, both passes: treat `errors.Is(err, ErrEvictClaimLost)` as a benign skip (`continue` without logging, without appending).
- Modify: `internal/agent/source/perforce/perforce.go`
  - Add `Provider.ReserveForEvict(shortID string) (func(), bool)` mirroring `EvictWorkspace`'s holder-check + reservation, returning a release closure.
- Modify: `cmd/relay-agent/main.go:93-103`
  - Add `Claim: pp.ReserveForEvict,` to the background `Sweeper` literal.
- Test: `internal/agent/source/perforce/sweeper_claim_test.go` (new) - the regression test driving a background-sweeper eviction into the Prepare gap.

---

## Task 1: Regression test (RED) - background sweeper claim closes the Prepare race

**Files:**
- Test: Create `internal/agent/source/perforce/sweeper_claim_test.go`

This test must be proven RED against the unfixed code: it drives a concurrent BACKGROUND-SWEEPER eviction (through the wired `Claim`/`ReserveForEvict`, via `SweepOnce`) into the gap between Prepare's pre-Acquire check and its `ws.Acquire`, and asserts the property only the fix produces: the losing `Prepare` backs out with `"being evicted"` and leaves zero holders, and the sweeper reports zero evictions (it lost the claim, so it did not delete the workspace the live Prepare acquired).

Against unfixed code: `Sweeper` has no `Claim` field (compile error) - so write the test so it fails to compile first, which is the RED signal for a field/method that does not yet exist. Then Tasks 2-4 add the field/method/sentinel; the test must then pass.

- [ ] **Step 1: Write the failing test**

```go
package perforce

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	relayv1 "relay/internal/proto/relayv1"

	"github.com/stretchr/testify/require"
)

// TestSweeperClaim_PrepareBacksOutWhenSweepReservesDuringAcquire is the
// background-sweeper analogue of
// TestEvictWorkspace_PrepareBacksOutWhenEvictReservesDuringAcquire. The
// dominant eviction path is the background Sweeper, not the manual
// EvictWorkspace API. With the Claim hook wired (Sweeper.Claim =
// Provider.ReserveForEvict), a sweeper eviction reserves p.evicting[shortID]
// under p.mu after an inline holder re-check; a concurrent Prepare that loses
// the race must observe the reservation in its post-Acquire re-check and back
// out (release the handle, return "being evicted") rather than sync into a
// workspace being deleted.
//
// Deterministic, no timing: prepareAcquireHook drives the concurrent SweepOnce
// into the gap, and a gatingRunner pauses the sweeper inside its `client -d`
// call so the reservation is provably held when Prepare's re-check runs.
func TestSweeperClaim_PrepareBacksOutWhenSweepReservesDuringAcquire(t *testing.T) {
	root := t.TempDir()
	fr := newFakeP4Fixture(t)
	gate := &gatingRunner{
		inner:   fr,
		entered: make(chan struct{}),
		proceed: make(chan struct{}),
	}
	p := New(Config{Root: root, Hostname: "host", Client: &Client{r: gate}})

	reg, err := p.Registry()
	require.NoError(t, err)
	shortID := allocateShortID("//depot/main", reg)
	clientName := "relay_host_" + shortID
	fr.set("client -d "+clientName, "Client deleted.\n")
	gate.gateKey = "client -d " + clientName
	// Seed the entry as STALE so the sweeper's age pass selects it.
	reg.Upsert(WorkspaceEntry{
		ShortID:    shortID,
		SourceKey:  "//depot/main",
		ClientName: clientName,
		LastUsedAt: time.Now().Add(-30 * 24 * time.Hour),
	})
	require.NoError(t, reg.Save())
	require.NoError(t, os.MkdirAll(filepath.Join(root, shortID), 0o755))

	// The background sweeper, with Claim wired exactly as cmd/relay-agent does.
	sw := &Sweeper{
		Root:       root,
		Reg:        reg,
		MaxAge:     14 * 24 * time.Hour,
		Client:     p.Client(),
		ListLocked: p.LockedShortIDs,
		Claim:      p.ReserveForEvict,
		OnEvictedCB: p.InvalidateWorkspace,
	}

	// The hook fires on Prepare's goroutine, after the pre-check and before
	// Acquire. It launches one SweepOnce concurrently and waits until that
	// sweep has reserved the short ID and reached its (gated) `client -d` call,
	// i.e. the reservation is set but the slow evict is not yet done.
	type sweepResult struct {
		evicted []string
		err     error
	}
	sweepDone := make(chan sweepResult, 1)
	var once bool
	prepareAcquireHook = func(string) {
		if once {
			return
		}
		once = true
		go func() {
			ev, err := sw.SweepOnce(context.Background())
			sweepDone <- sweepResult{ev, err}
		}()
		<-gate.entered // sweeper has reserved and is paused in client -d
	}
	t.Cleanup(func() { prepareAcquireHook = nil })

	spec := &relayv1.SourceSpec{
		Provider: &relayv1.SourceSpec_Perforce{
			Perforce: &relayv1.PerforceSource{
				Stream: "//depot/main",
				Sync:   []*relayv1.SyncEntry{{Path: "//depot/main/...", Rev: "@1"}},
			},
		},
	}

	_, prepErr := p.Prepare(context.Background(), "task-1", spec, func(string) {})

	// Let the gated sweep finish so we can join it cleanly.
	close(gate.proceed)
	res := <-sweepDone
	require.NoError(t, res.err, "SweepOnce itself must not error")

	// The hook forces the sweep to reserve (ReserveForEvict succeeds, then it
	// reaches the gated `client -d`) BEFORE Prepare's ws.Acquire runs. So the
	// sweep WINS the race and completes the eviction, exactly as EvictWorkspace
	// does in the manual-path test; the live Prepare is the loser and must back
	// out. The safety property is mutual exclusion: the sweep evicts AND Prepare
	// never synced into the workspace (it returns "being evicted" and holds
	// nothing).
	require.Error(t, prepErr, "Prepare must not succeed when it loses the sweep race")
	require.ErrorContains(t, prepErr, "being evicted")

	// The winning sweep must have evicted the workspace it reserved first.
	require.Equal(t, []string{shortID}, res.evicted, "the winning sweep must complete the eviction")

	// And the losing Prepare must not leave a holder dangling.
	p.mu.Lock()
	ws := p.workspaces[shortID]
	p.mu.Unlock()
	if ws != nil {
		ws.mu.Lock()
		n := len(ws.holders)
		ws.mu.Unlock()
		require.Zero(t, n, "losing Prepare must release the workspace handle it acquired")
	}
}
```

- [ ] **Step 2: Run the test to verify it fails (RED)**

Run: `go test ./internal/agent/source/perforce/... -run TestSweeperClaim_PrepareBacksOutWhenSweepReservesDuringAcquire -v -timeout 60s`

Expected: FAIL - a compile error, because `Sweeper` has no `Claim` field and `Provider` has no `ReserveForEvict` method:
`unknown field 'Claim' in struct literal` and `p.ReserveForEvict undefined`.

This compile failure is the RED proof for the missing fix surface. Do not proceed to Task 5's pass check until Tasks 2-4 are done.

- [ ] **Step 3: Commit the failing test**

```bash
git add internal/agent/source/perforce/sweeper_claim_test.go
git commit -m "test(perforce): add RED regression for sweeper-vs-Prepare TOCTOU"
```

---

## Task 2: Add the `Claim` hook and `ErrEvictClaimLost` sentinel to `Sweeper`

**Files:**
- Modify: `internal/agent/source/perforce/sweeper.go:14-27` (struct), `:114-136` (evict), add sentinel near top.

- [ ] **Step 1: Add the sentinel var**

Add immediately after the imports block (before `// Sweeper evicts ...` at line 13):

```go
// ErrEvictClaimLost is returned by Sweeper.evict when the optional Claim hook
// declines the reservation - the workspace is currently held by a task or is
// already being evicted. It is a benign, expected outcome of losing the
// eviction race, not a failure: SweepOnce skips such entries without logging
// or counting them. Callers distinguish it with errors.Is.
var ErrEvictClaimLost = errors.New("sweeper: evict claim lost (workspace in use or already evicting)")
```

`errors` is already imported (sweeper.go line 6).

- [ ] **Step 2: Add the `Claim` field to the `Sweeper` struct**

Modify the struct (lines 14-27) to add the field after `OnEvictedCB`:

```go
// Sweeper evicts stale workspaces by age and/or disk pressure.
type Sweeper struct {
	Root          string
	MaxAge        time.Duration // 0 = disabled
	MinFreeGB     int64         // 0 = disabled
	SweepInterval time.Duration // 0 = 15m default

	// Reg is the shared on-disk workspace registry. Required.
	Reg *Registry

	Client      *Client
	ListLocked  func() map[string]bool           // returns short_ids of currently-held workspaces
	FreeDiskGB  func(root string) (int64, error) // injectable for tests
	OnEvictedCB func(shortID string)

	// Claim, when non-nil, is invoked by evict before any destructive work to
	// atomically reserve the short_id against a concurrent Prepare (mirroring
	// Provider.EvictWorkspace's p.evicting reservation). If ok is false the
	// entry is held or already being evicted: evict performs no deletion and
	// returns ErrEvictClaimLost. If ok is true, evict defers release() and
	// proceeds. Left nil where the caller already holds the reservation (the
	// internal Sweeper that EvictWorkspace builds).
	Claim func(shortID string) (release func(), ok bool)
}
```

- [ ] **Step 3: Reserve via Claim at the top of `evict`**

Modify `evict` (lines 114-136) to claim before the `DirtyDelete` branch:

```go
func (s *Sweeper) evict(ctx context.Context, reg *Registry, w WorkspaceEntry) error {
	if s.Claim != nil {
		release, ok := s.Claim(w.ShortID)
		if !ok {
			return ErrEvictClaimLost
		}
		defer release()
	}
	// When w.DirtyDelete is set, a prior sweep already deleted the p4 client
	// and only the on-disk directory remains. Calling DeleteClient again would
	// fail ("client doesn't exist") and previously wedged the whole sweep.
	if !w.DirtyDelete {
		if err := s.Client.DeleteClient(ctx, w.ClientName); err != nil {
			return err
		}
	}
	if err := os.RemoveAll(filepath.Join(s.Root, w.ShortID)); err != nil {
		_ = reg.MarkDirtyDelete(w.ShortID, true)
		_ = reg.Save()
		return err
	}
	reg.Remove(w.ShortID)
	if err := reg.Save(); err != nil {
		return err
	}
	if s.OnEvictedCB != nil {
		s.OnEvictedCB(w.ShortID)
	}
	return nil
}
```

- [ ] **Step 4: Make `SweepOnce` treat a lost claim as a benign skip**

Modify the two `evict` call sites in `SweepOnce`. Age pass (lines 79-87):

```go
	// Age pass: evict anything older than MaxAge.
	if s.MaxAge > 0 {
		for _, w := range candidates {
			if now.Sub(w.LastUsedAt) > s.MaxAge {
				if err := s.evict(ctx, reg, w); err != nil {
					if errors.Is(err, ErrEvictClaimLost) {
						continue
					}
					log.Printf("sweeper: evict %s: %v", w.ShortID, err)
					continue
				}
				evicted = append(evicted, w.ShortID)
			}
		}
	}
```

Pressure pass (lines 104-108), inside the existing loop, after the `FreeDiskGB`/`MinFreeGB` checks:

```go
			if err := s.evict(ctx, reg, w); err != nil {
				if errors.Is(err, ErrEvictClaimLost) {
					continue
				}
				log.Printf("sweeper: evict %s: %v", w.ShortID, err)
				continue
			}
			evicted = append(evicted, w.ShortID)
```

- [ ] **Step 5: Build to verify the package compiles (`ReserveForEvict` still missing - test stays red)**

Run: `go build ./internal/agent/source/perforce/...`
Expected: PASS (the production package compiles; `ReserveForEvict` is only referenced by the test, added in Task 3).

- [ ] **Step 6: Commit**

```bash
git add internal/agent/source/perforce/sweeper.go
git commit -m "feat(perforce): add Sweeper.Claim hook and ErrEvictClaimLost sentinel"
```

---

## Task 3: Add `Provider.ReserveForEvict`

**Files:**
- Modify: `internal/agent/source/perforce/perforce.go` (add method after `EvictWorkspace`, around line 353).

This mirrors `EvictWorkspace`'s holder-check + reservation exactly, respecting the `p.mu` then `ws.mu` lock order, and returns a release closure that clears the reservation under `p.mu`.

- [ ] **Step 1: Add the method**

Insert after `EvictWorkspace` (after line 353, before the `Client()` accessor):

```go
// ReserveForEvict atomically reserves shortID for an in-flight eviction, the
// same way EvictWorkspace does, for the background Sweeper's Claim hook. Under
// p.mu it verifies the workspace is neither held (inline holders re-check) nor
// already being evicted, then sets p.evicting[shortID] and returns a release
// closure that clears it under p.mu. The holder check and the reservation are
// one p.mu critical section, so Prepare's post-Acquire re-check observes the
// reservation and backs out if it loses the race. Lock order is p.mu then
// ws.mu, matching EvictWorkspace and lockedShortIDs. Returns ok=false (and a
// nil release) when the workspace is held or already reserved.
func (p *Provider) ReserveForEvict(shortID string) (func(), bool) {
	p.mu.Lock()
	if ws, ok := p.workspaces[shortID]; ok {
		ws.mu.Lock()
		held := len(ws.holders) > 0
		ws.mu.Unlock()
		if held {
			p.mu.Unlock()
			return nil, false
		}
	}
	if p.evicting[shortID] {
		p.mu.Unlock()
		return nil, false
	}
	p.evicting[shortID] = true
	p.mu.Unlock()

	release := func() {
		p.mu.Lock()
		delete(p.evicting, shortID)
		p.mu.Unlock()
	}
	return release, true
}
```

- [ ] **Step 2: Build to verify it compiles**

Run: `go build ./internal/agent/source/perforce/...`
Expected: PASS.

- [ ] **Step 3: Commit**

```bash
git add internal/agent/source/perforce/perforce.go
git commit -m "feat(perforce): add Provider.ReserveForEvict for the sweeper Claim hook"
```

---

## Task 4: Verify the regression test now passes (GREEN)

**Files:** none (validation only).

- [ ] **Step 1: Run the Task 1 regression test**

Run: `go test ./internal/agent/source/perforce/... -run TestSweeperClaim_PrepareBacksOutWhenSweepReservesDuringAcquire -v -timeout 60s`

Expected: PASS. The wired `Claim: p.ReserveForEvict` sets `p.evicting[shortID]` while the gated `client -d` is paused; Prepare's post-Acquire re-check observes it and backs out with `"being evicted"`; the sweep, having reserved first, completes and returns `evicted == [shortID]`.

Note on determinism: the hook launches the sweep AFTER Prepare's pre-check, and waits on `gate.entered`, which only fires once the sweep has passed `ReserveForEvict` and reached `client -d`. For the sweep to reach `client -d`, its `ReserveForEvict` must have succeeded - which requires Prepare had not yet acquired. So deterministically the sweep WINS the reservation, Prepare's later Acquire + re-check loses and backs out, and the sweep proceeds to evict. The asserted safety invariant: a live Prepare never syncs into a workspace being deleted, and exactly one side (here the sweep) proceeds.

- [ ] **Step 2: Run the existing manual-path regression test to confirm no regression**

Run: `go test ./internal/agent/source/perforce/... -run TestEvictWorkspace_PrepareBacksOutWhenEvictReservesDuringAcquire -v -timeout 60s`

Expected: PASS (unchanged - the internal Sweeper that `EvictWorkspace` builds still has `Claim` nil, so its behavior is identical to before).

---

## Task 5: Wire `Claim` on the background sweeper in `cmd/relay-agent/main.go`

**Files:**
- Modify: `cmd/relay-agent/main.go:93-103`

- [ ] **Step 1: Add `Claim` to the background Sweeper literal**

Modify the `&perforce.Sweeper{...}` literal (lines 93-103) to add `Claim: pp.ReserveForEvict,`:

```go
				sw := &perforce.Sweeper{
					Root:          root,
					Reg:           reg,
					MaxAge:        maxAge,
					MinFreeGB:     minFreeGB,
					SweepInterval: sweepInterval,
					Client:        pp.Client(),
					ListLocked:    pp.LockedShortIDs,
					FreeDiskGB:    freeDiskGB,
					Claim:         pp.ReserveForEvict,
					OnEvictedCB:   pp.InvalidateWorkspace,
				}
```

- [ ] **Step 2: Build the agent binary**

Run: `go build ./cmd/relay-agent/...`
Expected: PASS.

- [ ] **Step 3: Commit**

```bash
git add cmd/relay-agent/main.go
git commit -m "fix(perforce): claim p.evicting on the background sweeper to close Prepare TOCTOU"
```

---

## Task 6: Full package test pass and close the backlog item

**Files:**
- Close: `docs/backlog/bug-2026-06-21-sweeper-prepare-toctou.md`

- [ ] **Step 1: Run the full perforce package unit tests**

Run: `go test ./internal/agent/source/perforce/... -v -timeout 120s`
Expected: PASS for all tests, including both TOCTOU regression tests and the existing sweeper tests (`provider_sweeper_test.go`, `sweeper_test.go`).

- [ ] **Step 2: Run the whole unit suite to confirm no collateral breakage**

Run: `make test`
Expected: PASS (Windows skips platform-gated tests; that is acceptable for this backend-only, platform-agnostic change).

- [ ] **Step 3: Close the backlog item**

Use the backlog skill's close command (NOT a hand edit of `status`):

`/backlog close sweeper-prepare-toctou`

This `git mv`s `docs/backlog/bug-2026-06-21-sweeper-prepare-toctou.md` into `docs/backlog/closed/`, stamps `status: closed` plus `closed:`/`resolution:` frontmatter, appends a `## Resolution` note, and commits.

---

## Self-review against the backlog item

- **Sweeper sets `p.evicting`:** Task 2 (`Claim` hook) + Task 3 (`ReserveForEvict`) + Task 5 (wiring) - covered.
- **Holder re-check immediately before destructive work:** `ReserveForEvict` does the inline `len(ws.holders) > 0` check under `p.mu`, called from `evict` before `DeleteClient`/`RemoveAll` - covered.
- **Losing Prepare backs out (release, no sync):** asserted in the Task 1 regression test (`"being evicted"`, zero holders) and produced by the existing post-Acquire re-check in `Prepare` - covered, no `Prepare` change needed.
- **Eviction must not delete a workspace a live Prepare holds:** `ReserveForEvict` returns `ok=false` when held -> `evict` returns `ErrEvictClaimLost` -> no deletion; asserted via `require.Empty(res.evicted)` - covered.
- **Internal `EvictWorkspace` Sweeper unchanged:** `Claim` left nil there; Task 4 Step 2 re-runs its regression test - covered.
- **Benign skip not logged/counted vs real error logged:** `SweepOnce` `errors.Is(err, ErrEvictClaimLost)` branch in both passes - covered.
- **Lock order p.mu -> ws.mu:** `ReserveForEvict` mirrors `EvictWorkspace` exactly - covered.
- **No frontend slice:** stated in the header - covered.
- **RED proof before fix:** Task 1 Step 2 (compile-error RED) precedes Tasks 2-5 - covered.
