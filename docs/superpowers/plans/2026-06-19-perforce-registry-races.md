# Perforce Workspace Registry Races Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Eliminate every unlocked access to `perforce.Registry.Workspaces` and every interior pointer that escapes `Registry.mu`, verified clean under `-race`.

**Architecture:** Convert the registry's getters to return value copies + bool, add a locked `Mutate`/`Snapshot`/`ShortIDInUse` API, and route every consumer (Prepare, EvictWorkspace, ListInventory, allocateShortID, sweeper) through that locked API. All work is backend Go in one package; tasks are sequential (later tasks depend on the API from Task 1).

**Tech Stack:** Go, `sync.Mutex`, `testify/require`, `go test -race`.

**Slice classification:** Backend only. Single package: `internal/agent/source/perforce`. Tasks are NOT independent - Task 1 introduces the API that Tasks 2-5 consume, and Task 6 is the cross-cutting race test. Execute in order.

---

## File Structure

- Modify: `internal/agent/source/perforce/registry.go` - getter signatures + new `Mutate`/`Snapshot`/`ShortIDInUse` methods.
- Modify: `internal/agent/source/perforce/perforce.go` - `Prepare`, `EvictWorkspace`, `ListInventory`, `allocateShortID`; delete free `shortIDInUse`.
- Modify: `internal/agent/source/perforce/sweeper.go` - `SweepOnce` candidate build + existence check.
- Modify: `internal/agent/source/perforce/registry_test.go` - new getter signatures, `Mutate`/`Snapshot`/`ShortIDInUse` coverage.
- Modify: `internal/agent/source/perforce/sweeper_test.go` - new `Get` signature in assertions.
- Modify: `internal/agent/source/perforce/perforce_test.go` - new `GetBySourceKey` signature.
- Modify: `internal/agent/source/perforce/perforce_integration_test.go` - new `Get` signature (`//go:build integration`).
- Create: `internal/agent/source/perforce/registry_race_test.go` - concurrent sweep+mutate race test.

---

### Task 1: Registry locked API (getters return copies; add Mutate/Snapshot/ShortIDInUse)

**Files:**
- Modify: `internal/agent/source/perforce/registry.go:72-94` (getters), append new methods after `Get`/`GetBySourceKey`.
- Test: `internal/agent/source/perforce/registry_test.go`

- [ ] **Step 1: Update existing registry tests to the new getter signatures and add new-method tests**

In `registry_test.go`, the existing `Get` calls currently expect a pointer. Replace the relevant assertions. Find the test that does `e := r2.Get("a")` / `e = r3.Get("a")` (around lines 44-54) and rewrite to the value+bool form, then append three new tests:

```go
func TestRegistry_GetReturnsCopyNotPointer(t *testing.T) {
	r := &Registry{}
	r.Upsert(WorkspaceEntry{ShortID: "a", SourceKey: "//s/x", BaselineHash: "h1", LastUsedAt: time.Now()})

	got, ok := r.Get("a")
	require.True(t, ok)
	require.Equal(t, "h1", got.BaselineHash)

	// Mutating the returned copy must not touch registry memory.
	got.BaselineHash = "MUTATED"
	again, ok := r.Get("a")
	require.True(t, ok)
	require.Equal(t, "h1", again.BaselineHash)

	_, ok = r.Get("missing")
	require.False(t, ok)
}

func TestRegistry_GetBySourceKeyReturnsCopy(t *testing.T) {
	r := &Registry{}
	r.Upsert(WorkspaceEntry{ShortID: "a", SourceKey: "//s/x", LastUsedAt: time.Now()})

	got, ok := r.GetBySourceKey("//s/x")
	require.True(t, ok)
	require.Equal(t, "a", got.ShortID)

	_, ok = r.GetBySourceKey("//s/none")
	require.False(t, ok)
}

func TestRegistry_Mutate(t *testing.T) {
	r := &Registry{}
	r.Upsert(WorkspaceEntry{ShortID: "a", SourceKey: "//s/x", BaselineHash: "old", LastUsedAt: time.Now()})

	err := r.Mutate("a", func(e *WorkspaceEntry) { e.BaselineHash = "new" })
	require.NoError(t, err)

	got, ok := r.Get("a")
	require.True(t, ok)
	require.Equal(t, "new", got.BaselineHash)

	err = r.Mutate("missing", func(e *WorkspaceEntry) {})
	require.Error(t, err)
}

func TestRegistry_SnapshotIsIndependentCopy(t *testing.T) {
	r := &Registry{}
	r.Upsert(WorkspaceEntry{ShortID: "a", SourceKey: "//s/x", LastUsedAt: time.Now()})

	snap := r.Snapshot()
	require.Len(t, snap, 1)

	// Appending to / mutating the snapshot must not affect the registry.
	snap[0].SourceKey = "MUTATED"
	snap = append(snap, WorkspaceEntry{ShortID: "b"})

	got, ok := r.Get("a")
	require.True(t, ok)
	require.Equal(t, "//s/x", got.SourceKey)
	require.Len(t, r.Snapshot(), 1)
}

func TestRegistry_ShortIDInUse(t *testing.T) {
	r := &Registry{}
	r.Upsert(WorkspaceEntry{ShortID: "a", SourceKey: "//s/x", LastUsedAt: time.Now()})

	// Same shortID, different sourceKey -> collision.
	require.True(t, r.ShortIDInUse("a", "//s/y"))
	// Same shortID, same sourceKey -> not a collision (it's the same workspace).
	require.False(t, r.ShortIDInUse("a", "//s/x"))
	// Unknown shortID -> free.
	require.False(t, r.ShortIDInUse("z", "//s/y"))
}
```

- [ ] **Step 2: Run the new/updated tests to verify they fail**

Run: `go test ./internal/agent/source/perforce/... -run 'TestRegistry_GetReturnsCopyNotPointer|TestRegistry_GetBySourceKeyReturnsCopy|TestRegistry_Mutate|TestRegistry_SnapshotIsIndependentCopy|TestRegistry_ShortIDInUse' -v`
Expected: compile failure (`Get`/`GetBySourceKey` return a single value; `Mutate`/`Snapshot`/`ShortIDInUse` undefined).

- [ ] **Step 3: Change getter signatures and add the new methods in `registry.go`**

Replace `Get` and `GetBySourceKey` (lines 72-94):

```go
// Get returns a copy of the entry with the given shortID and true, or a zero
// value and false. Callers receive a copy so they cannot mutate registry memory
// without going through Mutate.
func (r *Registry) Get(shortID string) (WorkspaceEntry, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := range r.Workspaces {
		if r.Workspaces[i].ShortID == shortID {
			return r.Workspaces[i], true
		}
	}
	return WorkspaceEntry{}, false
}

// GetBySourceKey returns a copy of the entry matching sourceKey and true, or a
// zero value and false.
func (r *Registry) GetBySourceKey(sourceKey string) (WorkspaceEntry, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := range r.Workspaces {
		if r.Workspaces[i].SourceKey == sourceKey {
			return r.Workspaces[i], true
		}
	}
	return WorkspaceEntry{}, false
}

// Mutate applies fn to the live entry with the given shortID under the lock.
// Returns an error if no entry matches. This is the only sanctioned way to edit
// an existing entry in place; it cannot clobber a concurrently-appended
// OpenTaskChangelist the way a read-copy-Upsert sequence could.
func (r *Registry) Mutate(shortID string, fn func(*WorkspaceEntry)) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := range r.Workspaces {
		if r.Workspaces[i].ShortID == shortID {
			fn(&r.Workspaces[i])
			return nil
		}
	}
	return fmt.Errorf("workspace %s not found", shortID)
}

// Snapshot returns an independent copy of the workspace slice for read-only
// iteration outside the lock.
func (r *Registry) Snapshot() []WorkspaceEntry {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]WorkspaceEntry, len(r.Workspaces))
	copy(out, r.Workspaces)
	return out
}

// ShortIDInUse reports whether shortID is already claimed by a workspace bound
// to a different sourceKey.
func (r *Registry) ShortIDInUse(shortID, sourceKey string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := range r.Workspaces {
		if r.Workspaces[i].ShortID == shortID && r.Workspaces[i].SourceKey != sourceKey {
			return true
		}
	}
	return false
}
```

Note: `OpenTaskChangelists` is a slice, so the returned `WorkspaceEntry` copy still shares that slice's backing array with the registry. `Get`/`Snapshot` callers in this codebase only read it, and the dangerous write path goes through `Mutate`/`AddPendingCL` under the lock, so a shallow copy is sufficient. Do not add a deep copy - it is not needed (YAGNI).

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/agent/source/perforce/... -run 'TestRegistry_GetReturnsCopyNotPointer|TestRegistry_GetBySourceKeyReturnsCopy|TestRegistry_Mutate|TestRegistry_SnapshotIsIndependentCopy|TestRegistry_ShortIDInUse' -v`
Expected: PASS. (Other tests + `perforce.go`/`sweeper.go` still reference the old signatures and will not compile yet - that is fixed in Tasks 2-4. Run the package build only after Task 4.)

- [ ] **Step 5: Commit**

```bash
git add internal/agent/source/perforce/registry.go internal/agent/source/perforce/registry_test.go
git commit -m "feat(perforce): registry getters return copies; add Mutate/Snapshot/ShortIDInUse"
```

---

### Task 2: Route Prepare and EvictWorkspace through the locked API

**Files:**
- Modify: `internal/agent/source/perforce/perforce.go:149` (`GetBySourceKey`), `:208-229` (`Get`+mutate), `:277-282` (`EvictWorkspace`).

- [ ] **Step 1: Update the `GetBySourceKey` call in Prepare (line 149)**

Replace:
```go
	existing := reg.GetBySourceKey(pf.Stream)
	var shortID string
	if existing != nil {
		shortID = existing.ShortID
	} else {
		shortID = allocateShortID(pf.Stream, reg)
	}
```
with:
```go
	existing, ok := reg.GetBySourceKey(pf.Stream)
	var shortID string
	if ok {
		shortID = existing.ShortID
	} else {
		shortID = allocateShortID(pf.Stream, reg)
	}
```

- [ ] **Step 2: Update the read-modify-write block in Prepare (lines 208-229)**

Replace:
```go
	cur := reg.Get(shortID)
	needsSync := handle.Mode() == ModeExclusive || (cur != nil && cur.BaselineHash != baseline)
```
with:
```go
	cur, curOK := reg.Get(shortID)
	needsSync := handle.Mode() == ModeExclusive || (curOK && cur.BaselineHash != baseline)
```

Then replace the post-sync write:
```go
		if cur != nil {
			cur.BaselineHash = baseline
			cur.LastUsedAt = time.Now()
			reg.Upsert(*cur)
		}
		_ = reg.Save()
```
with:
```go
		if curOK {
			_ = reg.Mutate(shortID, func(e *WorkspaceEntry) {
				e.BaselineHash = baseline
				e.LastUsedAt = time.Now()
			})
		}
		_ = reg.Save()
```

- [ ] **Step 3: Update `EvictWorkspace` (lines 277-282)**

Replace:
```go
	e := reg.Get(shortID)
	if e == nil {
		return fmt.Errorf("workspace %s not found in registry", shortID)
	}
	sw := &Sweeper{Root: p.cfg.Root, Reg: reg, Client: p.cfg.Client, ListLocked: p.lockedShortIDs}
	return sw.evict(ctx, reg, *e)
```
with:
```go
	e, ok := reg.Get(shortID)
	if !ok {
		return fmt.Errorf("workspace %s not found in registry", shortID)
	}
	sw := &Sweeper{Root: p.cfg.Root, Reg: reg, Client: p.cfg.Client, ListLocked: p.lockedShortIDs}
	return sw.evict(ctx, reg, e)
```

- [ ] **Step 4: Defer build/test to Task 4** (the package will not compile until `allocateShortID`/`ListInventory`/`sweeper.go` are updated). No commit yet - continue to Task 3.

---

### Task 3: Route allocateShortID and ListInventory through the locked API

**Files:**
- Modify: `internal/agent/source/perforce/perforce.go:77-88` (`ListInventory`), `:389` (`allocateShortID`), `:396-403` (delete free `shortIDInUse`).

- [ ] **Step 1: Update `allocateShortID` and delete the free `shortIDInUse` function**

In `allocateShortID`, replace the call (line 389):
```go
		if !shortIDInUse(reg, candidate, stream) {
```
with:
```go
		if !reg.ShortIDInUse(candidate, stream) {
```

Delete the now-unused free function (lines 396-403):
```go
func shortIDInUse(reg *Registry, shortID, sourceKey string) bool {
	for _, w := range reg.Workspaces {
		if w.ShortID == shortID && w.SourceKey != sourceKey {
			return true
		}
	}
	return false
}
```

- [ ] **Step 2: Update `ListInventory` (lines 77-88) to use Snapshot**

Replace:
```go
	reg.mu.Lock()
	defer reg.mu.Unlock()
	out := make([]source.InventoryEntry, 0, len(reg.Workspaces))
	for _, w := range reg.Workspaces {
```
with:
```go
	ws := reg.Snapshot()
	out := make([]source.InventoryEntry, 0, len(ws))
	for _, w := range ws {
```

(The `for` body and the `return out` below it are unchanged.)

- [ ] **Step 3: Defer build/test to Task 4.** No commit yet - continue to Task 4.

---

### Task 4: Route the sweeper through the locked API and compile the whole package

**Files:**
- Modify: `internal/agent/source/perforce/sweeper.go:62-67` (candidate build), `:91` (existence check).

- [ ] **Step 1: Build the candidate list from `Snapshot` (lines 62-67)**

Replace:
```go
	candidates := make([]WorkspaceEntry, 0, len(reg.Workspaces))
	for _, w := range reg.Workspaces {
		if !locked[w.ShortID] {
			candidates = append(candidates, w)
		}
	}
```
with:
```go
	snap := reg.Snapshot()
	candidates := make([]WorkspaceEntry, 0, len(snap))
	for _, w := range snap {
		if !locked[w.ShortID] {
			candidates = append(candidates, w)
		}
	}
```

- [ ] **Step 2: Update the pressure-pass existence check (line 91)**

Replace:
```go
			if reg.Get(w.ShortID) == nil {
				continue
			}
```
with:
```go
			if _, ok := reg.Get(w.ShortID); !ok {
				continue
			}
```

- [ ] **Step 3: Update the non-race test files for the new `Get`/`GetBySourceKey` signatures**

`sweeper_test.go` has exactly one affected assertion, at line 93 inside `TestSweeper_UsesInjectedRegistry`:
```go
	// The eviction must be visible directly on the injected registry pointer.
	require.Nil(t, reg.Get("old"))
```
Rewrite to the bool form:
```go
	// The eviction must be visible directly on the injected registry pointer.
	_, ok := reg.Get("old")
	require.False(t, ok)
```
(No other `Get`/`GetBySourceKey` call sites exist in `sweeper_test.go` - `TestSweeper_SkipsLockedWorkspaces` asserts on `evicted`/`fr.argHistory()`, not on a getter.)

`perforce_test.go` lines 121-123:
```go
	e := reg.GetBySourceKey("//s/x")
	require.NotNil(t, e)
	require.Empty(t, e.OpenTaskChangelists)
```
becomes:
```go
	e, ok := reg.GetBySourceKey("//s/x")
	require.True(t, ok)
	require.Empty(t, e.OpenTaskChangelists)
```

`perforce_integration_test.go` lines 86-88 (`//go:build integration`):
```go
	e := reg.Get(inv.ShortID)
	require.NotNil(t, e, "workspace entry should remain in registry after finalize")
	require.Empty(t, e.OpenTaskChangelists, "Finalize should clear pending changelists")
```
becomes:
```go
	e, ok := reg.Get(inv.ShortID)
	require.True(t, ok, "workspace entry should remain in registry after finalize")
	require.Empty(t, e.OpenTaskChangelists, "Finalize should clear pending changelists")
```

- [ ] **Step 4: Build and run the full package test suite**

Run: `go build ./... && go test ./internal/agent/source/perforce/... -v`
Expected: package compiles; all unit tests PASS.

- [ ] **Step 5: Commit Tasks 2-4 together**

```bash
git add internal/agent/source/perforce/perforce.go internal/agent/source/perforce/sweeper.go internal/agent/source/perforce/sweeper_test.go internal/agent/source/perforce/perforce_test.go internal/agent/source/perforce/perforce_integration_test.go
git commit -m "refactor(perforce): route all registry access through the locked API"
```

---

### Task 5: Concurrency race test (the primary gate)

**Files:**
- Create: `internal/agent/source/perforce/registry_race_test.go`

- [ ] **Step 1: Write the race test**

This test must hammer the registry from many goroutines while a sweeper runs, with all p4 I/O stubbed so it stays Docker-free. The established pattern in `sweeper_test.go` is `Client: &Client{r: newFakeP4Fixture(t)}` plus injected `ListLocked`/`FreeDiskGB` funcs. With `MaxAge: time.Hour` and freshly-touched entries, the age pass never evicts and `MinFreeGB` is left zero, so the sweeper never calls `client -d` - the fixture needs no canned responses. The test body:

```go
package perforce

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"
)

func TestRegistry_ConcurrentSweepAndMutate(t *testing.T) {
	root := t.TempDir()
	reg := &Registry{path: root + "/.relay-registry.json"}

	// Seed entries the workers will mutate and the sweeper will scan.
	const n = 24
	for i := 0; i < n; i++ {
		reg.Upsert(WorkspaceEntry{
			ShortID:    fmt.Sprintf("ws%d", i),
			SourceKey:  fmt.Sprintf("//s/%d", i),
			LastUsedAt: time.Now(),
		})
	}

	// Sweeper with stubbed I/O: never actually evicts (MaxAge huge), just scans.
	// Match the Client/stub construction used in sweeper_test.go.
	sw := &Sweeper{
		Root:       root,
		MaxAge:     time.Hour,
		Reg:        reg,
		Client:     &Client{r: newFakeP4Fixture(t)},
		ListLocked: func() map[string]bool { return map[string]bool{} },
	}

	ctx := context.Background()
	var wg sync.WaitGroup
	stop := make(chan struct{})

	// Sweeper goroutine: repeated Snapshot-based scans.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				_, _ = sw.SweepOnce(ctx)
			}
		}
	}()

	// Writer goroutines: Upsert / Mutate / AddPendingCL / Remove / Get / Snapshot.
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				id := fmt.Sprintf("ws%d", (g+i)%n)
				reg.Upsert(WorkspaceEntry{ShortID: id, SourceKey: "//s/" + id, LastUsedAt: time.Now()})
				_ = reg.Mutate(id, func(e *WorkspaceEntry) { e.LastUsedAt = time.Now() })
				_ = reg.AddPendingCL(id, fmt.Sprintf("t%d-%d", g, i), int64(i))
				_, _ = reg.Get(id)
				_ = reg.Snapshot()
				_ = reg.RemovePendingCL(id, fmt.Sprintf("t%d-%d", g, i))
			}
		}(g)
	}

	wg.Wait() // writers finish
	close(stop)
	// allow the sweeper goroutine to observe stop and exit
	wgDone := make(chan struct{})
	go func() { wg.Wait(); close(wgDone) }()
	<-wgDone
}
```

The sweeper must never call real `p4` - `MaxAge: time.Hour` keeps the age pass from evicting the freshly-touched entries, and `MinFreeGB` is left zero so the pressure pass (which would call `DeleteClient`) never runs. `newFakeP4Fixture` is the same helper the other tests in this package use.

- [ ] **Step 2: Run the race test under the race detector**

Run: `go test -race ./internal/agent/source/perforce/... -run TestRegistry_ConcurrentSweepAndMutate -v -timeout 60s`
Expected: PASS with no `WARNING: DATA RACE` output.

- [ ] **Step 3: Run the whole package under `-race`**

Run: `go test -race ./internal/agent/source/perforce/... -timeout 120s`
Expected: PASS, zero race reports.

- [ ] **Step 4: Commit**

```bash
git add internal/agent/source/perforce/registry_race_test.go
git commit -m "test(perforce): concurrent sweep+mutate race test under -race"
```

---

### Task 6: Close the backlog item

**Files:**
- Move: `docs/backlog/bug-2026-06-10-perforce-registry-races.md` -> `docs/backlog/closed/`

- [ ] **Step 1: git mv the backlog item to closed**

```bash
git mv docs/backlog/bug-2026-06-10-perforce-registry-races.md docs/backlog/closed/bug-2026-06-10-perforce-registry-races.md
```

- [ ] **Step 2: Append a closing note to the moved file**

Add to the end of `docs/backlog/closed/bug-2026-06-10-perforce-registry-races.md`:
```markdown

## Closed 2026-06-19
Fixed: getters return value copies + bool; added `Mutate`/`Snapshot`/`ShortIDInUse`;
all consumers (Prepare, EvictWorkspace, ListInventory, allocateShortID, sweeper)
route through the locked API. No unlocked `reg.Workspaces` iteration or escaping
interior pointers remain. Verified clean under `go test -race ./internal/agent/source/perforce/...`.
```

- [ ] **Step 3: Commit**

```bash
git add docs/backlog/
git commit -m "backlog: close bug-2026-06-10-perforce-registry-races"
```

---

## Final verification

- [ ] `go build ./...` succeeds.
- [ ] `go test -race ./internal/agent/source/perforce/... -timeout 120s` passes with zero race reports.
- [ ] `make test` passes (full suite, no `-race`).
- [ ] `grep -n "reg.Workspaces" internal/agent/source/perforce/*.go` returns only matches inside `registry.go` (no consumer iterates the slice directly).
- [ ] No getter returns `*WorkspaceEntry`.
