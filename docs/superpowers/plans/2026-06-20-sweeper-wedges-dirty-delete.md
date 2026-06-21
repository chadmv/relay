# Sweeper Dirty-Delete Wedge Fix Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Stop a single dirty-delete entry (directory removal failed, p4 client already gone) from permanently wedging the Perforce workspace sweeper so disk pressure can always be relieved.

**Architecture:** Two surgical changes in `internal/agent/source/perforce/sweeper.go`. (1) In `evict`, skip `Client.DeleteClient` when the entry is already flagged `DirtyDelete` (the client was deleted on the first attempt; only the directory remains). (2) In both `SweepOnce` passes, log and `continue` past a per-entry `evict` failure instead of aborting the whole pass. No other files change.

**Tech Stack:** Go, testify, sqlc-unrelated. Unit tests use the in-package `fakeRunner` (`fixtures_test.go`); no Docker required for the core regression.

**Slice independence:** Backend-only. There is no frontend slice. There is no API, schema, proto, or migration change. `make generate` is NOT required.

**Invariant interaction:** None of the six relay Invariants are touched. This code is on the agent side (Perforce source provider), not the server task/epoch/gRPC/JSON paths. The change does not alter task status, epochs, stream senders, registry getter-copy semantics (it continues to use `reg.Get`/`reg.Mutate`/`reg.Remove`), or JSON ingestion.

---

## Background (read before starting)

Current `evict` (`internal/agent/source/perforce/sweeper.go:111-128`):

```go
func (s *Sweeper) evict(ctx context.Context, reg *Registry, w WorkspaceEntry) error {
	if err := s.Client.DeleteClient(ctx, w.ClientName); err != nil {
		return err
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

The wedge: first sweep deletes the client successfully, `RemoveAll` fails, the entry is flagged `DirtyDelete` and kept. Next sweep retries the same (oldest-first) entry, `DeleteClient` now fails because the client is gone, `evict` returns the error, and `SweepOnce` aborts (`sweeper.go:81` and `:103`). Every subsequent sweep dies on the same entry.

Two facts that drive the design:

1. **`DeleteClient` has no error typing.** `Client.DeleteClient` (`client.go:103-106`) returns the raw runner error verbatim. The real `execRunner` wraps p4's stderr (`client.go:40`), so a non-existent client surfaces as a free-form string like `p4 client -d ...: exit status 1 (stderr: Client 'name' doesn't exist.)`. There is no sentinel error and the fake runner (`fakerunner_test.go` / `fixtures_test.go`) models errors only as opaque `error` values keyed by command. String-matching this would be fragile and untestable against the fake.

2. **`WorkspaceEntry.DirtyDelete` already exists** (`registry.go:21`) and is currently written by `evict` but read nowhere else (confirmed by grep: only `MarkDirtyDelete` and the `evict` write site reference it). It already means exactly "the client was deleted but the directory removal failed."

**Detection-option decision: skip `DeleteClient` when `w.DirtyDelete` is true.** Rationale: it relies on state we already own and set ourselves on the first attempt, so it is deterministic and needs no p4 error parsing. It is robust across p4 versions and localized server messages. Treating "client doesn't exist" as success would require string-matching p4 stderr (brittle) or introducing a typed error path through `Runner`/`Client` (out of proportion for this fix and would touch the runner abstraction). The `DirtyDelete` skip is the minimal, surgical change.

The two changes are independent enough to land and test in separate TDD tasks; both are required to fully un-wedge the sweeper.

---

## File Structure

- Modify: `internal/agent/source/perforce/sweeper.go`
  - `evict` (lines 111-128): guard the `DeleteClient` call with `if !w.DirtyDelete`.
  - `SweepOnce` age pass (lines 77-86) and pressure pass (lines 88-107): replace `return evicted, err` from `evict` with log-and-`continue`. Add a `log` import.
- Test: `internal/agent/source/perforce/sweeper_test.go` (append two new tests; do not modify existing tests).

---

## Task 1: `evict` skips DeleteClient on a dirty-delete entry

**Files:**
- Modify: `internal/agent/source/perforce/sweeper.go:111-128`
- Test: `internal/agent/source/perforce/sweeper_test.go` (append)

This task proves that re-evicting a `DirtyDelete` entry succeeds without calling `p4 client -d` (whose client is gone), then removes the now-deletable directory and clears the entry.

- [ ] **Step 1: Write the failing test**

Append to `internal/agent/source/perforce/sweeper_test.go`. Note the fake runner registers NO fixture for `client -d relay_h_dirty`; if `evict` were to call it, `fakeRunner.Run` would call `t.Errorf` (failing the test) and return an error. So this single test asserts both "DeleteClient is skipped" and "the pass succeeds."

```go
func TestSweeper_DirtyDeleteSkipsDeleteClient(t *testing.T) {
	root := t.TempDir()
	fr := newFakeP4Fixture(t)
	// Deliberately register NO fixture for "client -d relay_h_dirty".
	// If evict calls DeleteClient, fakeRunner.Run will t.Errorf and fail.

	reg, _ := LoadRegistry(filepath.Join(root, ".relay-registry.json"))
	reg.Upsert(WorkspaceEntry{
		ShortID:     "dirty",
		SourceKey:   "//s/x",
		ClientName:  "relay_h_dirty",
		LastUsedAt:  time.Now().Add(-30 * 24 * time.Hour),
		DirtyDelete: true, // client already deleted on a prior sweep
	})
	require.NoError(t, reg.Save())
	// Directory now exists and is removable (the transient RemoveAll failure cleared).
	require.NoError(t, os.MkdirAll(filepath.Join(root, "dirty"), 0o755))

	s := &Sweeper{
		Root:       root,
		Reg:        reg,
		MaxAge:     14 * 24 * time.Hour,
		Client:     &Client{r: fr},
		ListLocked: func() map[string]bool { return nil },
	}
	evicted, err := s.SweepOnce(context.Background())
	require.NoError(t, err)
	require.Equal(t, []string{"dirty"}, evicted)

	// Directory gone, entry gone, and p4 client -d was never called.
	_, statErr := os.Stat(filepath.Join(root, "dirty"))
	require.ErrorIs(t, statErr, os.ErrNotExist)
	_, ok := reg.Get("dirty")
	require.False(t, ok)
	require.Empty(t, fr.argHistory(), "DeleteClient must be skipped for a DirtyDelete entry")
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/agent/source/perforce/... -run TestSweeper_DirtyDeleteSkipsDeleteClient -v -timeout 30s`

Expected: FAIL. `evict` calls `DeleteClient`, `fakeRunner.Run` reports `no fixture for args "client -d relay_h_dirty"` via `t.Errorf` and returns an error, so `SweepOnce` returns that error and `require.NoError(t, err)` fails.

- [ ] **Step 3: Write minimal implementation**

In `internal/agent/source/perforce/sweeper.go`, change the top of `evict` (lines 111-114) to guard the client deletion:

```go
func (s *Sweeper) evict(ctx context.Context, reg *Registry, w WorkspaceEntry) error {
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

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/agent/source/perforce/... -run TestSweeper_DirtyDeleteSkipsDeleteClient -v -timeout 30s`

Expected: PASS.

- [ ] **Step 5: Run the existing sweeper tests to confirm no regression**

Run: `go test ./internal/agent/source/perforce/... -run TestSweeper -v -timeout 60s`

Expected: PASS for `TestSweeper_AgeEviction`, `TestSweeper_PressureEviction`, `TestSweeper_UsesInjectedRegistry`, `TestSweeper_SkipsLockedWorkspaces`, and the new test. (These all use non-dirty entries, so `DeleteClient` is still called and their `client -d` fixtures still match.)

- [ ] **Step 6: Commit**

```bash
git add internal/agent/source/perforce/sweeper.go internal/agent/source/perforce/sweeper_test.go
git commit -m "fix(perforce): skip DeleteClient when re-evicting a DirtyDelete workspace"
```

---

## Task 2: `SweepOnce` continues past a per-entry evict failure

**Files:**
- Modify: `internal/agent/source/perforce/sweeper.go` (add `log` import; age pass lines 77-86; pressure pass lines 88-107)
- Test: `internal/agent/source/perforce/sweeper_test.go` (append)

This task proves that one entry whose `evict` fails does not abort the pass: a later, healthy entry is still evicted in the same `SweepOnce`.

- [ ] **Step 1: Write the failing test**

Append to `internal/agent/source/perforce/sweeper_test.go`. We force the first (oldest) entry's `evict` to fail by making its `DeleteClient` return an error via `fr.setErr`, while the second entry has a working `client -d` fixture. After the fix, `SweepOnce` must skip the bad entry, evict the good one, and return no error.

```go
func TestSweeper_ContinuesPastEvictFailure(t *testing.T) {
	root := t.TempDir()
	fr := newFakeP4Fixture(t)
	// Oldest entry: DeleteClient fails (simulates a still-present client that
	// cannot be deleted). Newer entry: DeleteClient succeeds.
	fr.setErr("client -d relay_h_bad", errors.New("p4 client -d relay_h_bad: boom"))
	fr.set("client -d relay_h_good", "Client deleted.\n")

	reg, _ := LoadRegistry(filepath.Join(root, ".relay-registry.json"))
	reg.Upsert(WorkspaceEntry{ShortID: "bad", SourceKey: "//s/bad",
		ClientName: "relay_h_bad", LastUsedAt: time.Now().Add(-40 * 24 * time.Hour)})
	reg.Upsert(WorkspaceEntry{ShortID: "good", SourceKey: "//s/good",
		ClientName: "relay_h_good", LastUsedAt: time.Now().Add(-30 * 24 * time.Hour)})
	require.NoError(t, reg.Save())
	require.NoError(t, os.MkdirAll(filepath.Join(root, "bad"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "good"), 0o755))

	s := &Sweeper{
		Root:       root,
		Reg:        reg,
		MaxAge:     14 * 24 * time.Hour,
		Client:     &Client{r: fr},
		ListLocked: func() map[string]bool { return nil },
	}
	evicted, err := s.SweepOnce(context.Background())
	require.NoError(t, err, "one bad entry must not abort the whole pass")
	require.Equal(t, []string{"good"}, evicted)

	// The good workspace is gone; the bad one remains for a future attempt.
	_, statErr := os.Stat(filepath.Join(root, "good"))
	require.ErrorIs(t, statErr, os.ErrNotExist)
	_, ok := reg.Get("good")
	require.False(t, ok)
	_, ok = reg.Get("bad")
	require.True(t, ok, "the failed entry stays in the registry")
}
```

Add `"errors"` to the test file's import block if it is not already present (the current `sweeper_test.go` imports do not include it).

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/agent/source/perforce/... -run TestSweeper_ContinuesPastEvictFailure -v -timeout 30s`

Expected: FAIL. The age pass hits `bad` first, `evict` returns the error, and the current `return evicted, err` aborts before `good` is evicted. `require.NoError(t, err)` fails (and `evicted` is empty, not `["good"]`).

- [ ] **Step 3: Write minimal implementation**

In `internal/agent/source/perforce/sweeper.go`:

(a) Add `log` to the import block (currently `context`, `errors`, `os`, `path/filepath`, `sort`, `time`):

```go
import (
	"context"
	"errors"
	"log"
	"os"
	"path/filepath"
	"sort"
	"time"
)
```

(b) Age pass - replace lines 78-84 (the loop body) so a failed `evict` logs and continues:

```go
	if s.MaxAge > 0 {
		for _, w := range candidates {
			if now.Sub(w.LastUsedAt) > s.MaxAge {
				if err := s.evict(ctx, reg, w); err != nil {
					log.Printf("sweeper: evict %s: %v", w.ShortID, err)
					continue
				}
				evicted = append(evicted, w.ShortID)
			}
		}
	}
```

(c) Pressure pass - replace the `evict` error handling (lines 102-105) the same way. Keep the existing `FreeDiskGB` error as a hard `return` (a disk-stat failure is not a per-entry problem):

```go
	if s.MinFreeGB > 0 && s.FreeDiskGB != nil {
		for _, w := range candidates {
			// Skip if already evicted above.
			if _, ok := reg.Get(w.ShortID); !ok {
				continue
			}
			free, err := s.FreeDiskGB(s.Root)
			if err != nil {
				return evicted, err
			}
			if free >= s.MinFreeGB {
				break
			}
			if err := s.evict(ctx, reg, w); err != nil {
				log.Printf("sweeper: evict %s: %v", w.ShortID, err)
				continue
			}
			evicted = append(evicted, w.ShortID)
		}
	}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/agent/source/perforce/... -run TestSweeper_ContinuesPastEvictFailure -v -timeout 30s`

Expected: PASS.

- [ ] **Step 5: Run the full perforce unit suite**

Run: `go test ./internal/agent/source/perforce/... -timeout 120s`

Expected: PASS (all non-integration tests; integration tests are gated behind `//go:build integration` and will not run without the `integration` tag).

- [ ] **Step 6: Commit**

```bash
git add internal/agent/source/perforce/sweeper.go internal/agent/source/perforce/sweeper_test.go
git commit -m "fix(perforce): continue sweep past a per-entry evict failure"
```

---

## Integration test (real p4d) - assessment

Not required for this fix, and the integration tester does NOT need to be dispatched.

Rationale: the bug is purely in the sweeper's Go control flow (skip-on-DirtyDelete and continue-past-failure). Both branches are deterministically reproducible with the `fakeRunner` - in fact more reliably than with real p4d, because forcing `RemoveAll` to fail and then succeed against a live p4d workspace is awkward and non-deterministic. The existing integration suite (`perforce_integration_test.go`, `p4d_container_test.go`) already exercises real client create/delete and eviction end-to-end; no new integration coverage adds signal for this specific control-flow fix. If a reviewer still wants belt-and-suspenders coverage, the existing `make test-integration` run is sufficient as a regression gate; no new integration test is in scope here.

---

## Related cleanup (EvictWorkspace OnEvictedCB / syncedPaths leak + TOCTOU) - SPLIT, not folded

**Recommendation: split into its own backlog item. Do NOT include it in this plan's tasks.**

Findings from current code:

- `cmd/relay-agent/main.go:93-103` already constructs the background sweeper WITH `OnEvictedCB: pp.InvalidateWorkspace`. So the leak only exists in the ad-hoc sweeper built inside `Provider.EvictWorkspace` (`perforce.go:281`), which omits `OnEvictedCB` and `FreeDiskGB`.
- `EvictWorkspace` (`perforce.go:268-283`) reads `lockedShortIDs()` and then later calls `evict`, with no lock held across the gap - a concurrent `Prepare` could acquire the workspace in that window (TOCTOU). Note `InvalidateWorkspace` clears `p.workspaces[shortID]` (per-task in-memory state, including any `syncedPaths`-equivalent), which is the leak the backlog item describes.

Why split:
- It is a genuinely separate concern (correctness of the manual single-workspace eviction API and its concurrency, not the background sweeper wedge). Folding it would violate the project's surgical-changes rule and "a backlog proposal is not a contract."
- Fixing the TOCTOU correctly likely needs a lock/acquire-then-evict design decision in `Provider`, which deserves its own scoping and review rather than riding along on a contained bug fix.
- Keeping this plan to the wedge keeps the change reviewable and the regression tests focused.

Draft backlog item for the conductor to file (do not implement here):

> **Title:** `Provider.EvictWorkspace leaks per-task state and has a lock TOCTOU`
> **Summary:** `Provider.EvictWorkspace` (perforce.go:268-283) builds an ad-hoc Sweeper without `OnEvictedCB`, so `InvalidateWorkspace` is never called and per-task in-memory workspace state survives a manual eviction; its `lockedShortIDs()` check also has a TOCTOU window against a concurrent `Prepare`. Wire `OnEvictedCB: p.InvalidateWorkspace` and close the lock gap (acquire/lock the workspace across the locked-check and evict).

---

## Self-Review

**Spec coverage:**
- "Treat client-doesn't-exist as success OR skip DeleteClient when DirtyDelete" -> Task 1 (chose the DirtyDelete skip, with rationale).
- "Continue past per-entry failures instead of aborting the pass" -> Task 2 (both age and pressure passes).
- "Deterministic unit test reproducing the wedge" -> Task 1 (no `client -d` fixture proves skip) and Task 2 (forced evict failure proves progress on other entries).
- "Note any integration test and whether the integration tester is needed" -> Integration test assessment section (not needed).
- "Assess the Related cleanup, fold vs split" -> Related cleanup section (split, with draft backlog title).
- "Flag any Invariant interaction" -> header (none).
- "Edits to .sql require make generate" -> N/A; no SQL/proto/migration files touched, explicitly stated in header.

**Placeholder scan:** No TBD/TODO/"handle edge cases"; every code step shows real code.

**Type consistency:** Uses existing symbols only - `Sweeper`, `WorkspaceEntry`, `WorkspaceEntry.DirtyDelete`, `Registry.Get`/`Upsert`/`Remove`/`MarkDirtyDelete`/`Save`, `Client{r: fr}`, `fakeRunner.set`/`setErr`/`argHistory`, `newFakeP4Fixture`. No new types or signatures introduced. The `log` import is the only new dependency and is stdlib.
