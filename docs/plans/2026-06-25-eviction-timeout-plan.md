# Per-Eviction Timeout for EvictWorkspace - Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Bound the `p4 client -d` subprocess inside `Sweeper.evict` with a per-eviction deadline so one wedged Perforce call can no longer permanently stall the serialized background sweep (and therefore disk reclamation). The deadline is read from `RELAY_EVICTION_TIMEOUT` (Go duration), defaulting to 30 minutes. `os.RemoveAll` stays unbounded by deliberate design (Option A in the spec).

**Architecture:** A package-level var `evictTimeout` in `internal/agent/source/perforce`, resolved once at package init from `RELAY_EVICTION_TIMEOUT` (falling back to a `defaultEvictTimeout = 30 * time.Minute` const). `Sweeper.evict` wraps its destructive body in `context.WithTimeout(ctx, evictTimeout)` so the bounded context flows through `Client.DeleteClient` -> `exec.CommandContext` -> `p4 client -d`. On deadline the `p4` call returns an error before `os.RemoveAll` is reached, so no DirtyDelete marker is set and the next sweep retries the full eviction. Existing callers already log-and-skip eviction errors, so eviction stays best-effort with no error-semantics change.

**Tech Stack:** Go, testify, `context`, `os/exec`. Backend Go only - no frontend, no `.sql`, no `.proto`, no `make generate`.

---

## Slice independence

Single backend slice. No frontend work, no API/schema changes, no `.sql`/`.proto`/`make generate`. Not applicable to Phase 3 parallelism.

## Worktree-path constraint (read before any command)

This is a git worktree. The working tree lives at
`D:/dev/relay/.claude/worktrees/happy-mendel-18687f` on branch
`claude/happy-mendel-18687f`. Run every command from that directory (the harness
resets cwd between bash calls, so use the absolute path or `cd` into it within the
same command). **NEVER `cd D:/dev/relay`** - that is a separate checkout on `main`;
committing there lands work on the wrong branch. All command blocks below assume cwd
is the worktree root.

## Grounding: where the timeout lives and the seam decision

### Where `RELAY_EVICTION_TIMEOUT` is parsed (the layer decision)

All other agent/workspace env vars are read in `cmd/relay-agent/main.go`
(`RELAY_WORKSPACE_ROOT`, `RELAY_WORKSPACE_MAX_AGE`, `RELAY_WORKSPACE_MIN_FREE_GB`,
`RELAY_WORKSPACE_SWEEP_INTERVAL` via the `parseDurationEnv` helper at
`cmd/relay-agent/main.go:85-87` and `:164`). The spec's preferred layering is "read
env where other agent env vars are read and thread the duration into the Sweeper,
UNLESS the package already reads env itself."

**However, there are TWO Sweeper construction sites, and one is inside the perforce
package, not main.go:**

1. `cmd/relay-agent/main.go:93-104` - the long-lived background sweeper. Has env access.
2. `internal/agent/source/perforce/perforce.go:352-358` - `Provider.EvictWorkspace`
   builds an internal one-shot `Sweeper` and calls `sw.evict(ctx, reg, e)` for the
   on-demand eviction path. This is deep inside the package; `Provider` carries no
   timeout config and there is no env-reading convention here.

A Sweeper-field-only approach would require threading the duration through BOTH sites,
including adding a timeout field to `Provider` (or `perforce.Config`) purely to feed
the internal Sweeper that `EvictWorkspace` constructs - extra plumbing across the
package boundary for a value that is identical everywhere.

**Decision: a package var `evictTimeout` in `internal/agent/source/perforce`,
resolved once from `RELAY_EVICTION_TIMEOUT` in that package.** Rationale:

- The perforce package builds its own Sweeper internally (`EvictWorkspace`), so the
  spec's "UNLESS the package already reads env itself" escape hatch applies in spirit:
  the value must be available *inside* the package regardless. Reading it once at the
  package layer that owns `evict` is the single source of truth that covers both blast
  radii with one change to `evict` (exactly as the spec's "place the timeout inside
  `evict`" guidance intends).
- It mirrors the established **untagged package-var override convention** already used
  for tunables in this codebase (`initialReconnectBackoff`, `reconnectSleep`,
  `dialContextFn` in `internal/agent/agent.go`), which is also the seam the spec's test
  strategy explicitly points at ("resolve it into a package var / Sweeper field that a
  test sets to a few milliseconds and restores ... mirroring the untagged-override
  pattern").
- It gives tests a one-line override seam without adding any production config beyond
  the `RELAY_EVICTION_TIMEOUT` env var.

The parsing itself mirrors the `RELAY_WORKER_GRACE_WINDOW` convention
(`cmd/relay-server/main.go:117-122`): `os.Getenv` + `time.ParseDuration`,
**ignore-on-error fallback** to the default. (Note: the agent's local
`parseDurationEnv` helper uses a custom `<N><unit>` regex grammar for
`14d`/`8h`-style values and lives in package `main`; it is not importable here. The
spec specifies `time.ParseDuration` semantics - `45m`, `2h`, `90s` - matching the
server-side `RELAY_WORKER_GRACE_WINDOW` pattern, so this plan uses `time.ParseDuration`
directly. Days like `14d` are not valid `time.ParseDuration` input by design; that is
acceptable for a minutes-to-hours timeout knob.)

### Test override seam (chosen, with rationale)

**Chosen: the `evictTimeout` package var, set directly in tests and restored via
`defer`.** This is the untagged-override pattern named above. Tests set
`evictTimeout = 50 * time.Millisecond` (or similar), defer restoring the prior value,
and assert `evict`/`SweepOnce` returns within a short bound. No build tags, no
production config beyond the env var. Rejected alternatives:

- *Pass a deadline-bounded parent ctx*: would test that `evict` honors an inherited
  deadline, but would NOT exercise the new `context.WithTimeout` wrapping (the inner
  child would just inherit the tighter parent deadline). It also leaves the env-parsing
  resolution untested at the call path. The package-var seam tests the real production
  code path with a shortened value.
- *A Sweeper field*: requires setting it at both construction sites and threading
  through `Provider`; the package var is strictly simpler and equally testable.

### Build tags / platform

The existing sweeper tests (`internal/agent/source/perforce/sweeper_test.go`) have
**no `//go:build` tag** and exercise `os.RemoveAll` on `t.TempDir()` directories;
they run under `make test` on Windows today. New tests in this plan follow suit: **no
`//go:build !windows` tag.** The hang is injected at the `fakeRunner` seam (the `p4`
call), not at the filesystem, so nothing platform-specific is introduced.

### `fakeRunner` today does not observe ctx

`fakeRunner.Run` (`fixtures_test.go:63-80`) returns its canned output immediately and
ignores `ctx`. To model `exec.CommandContext` killing a wedged subprocess on deadline,
Task 1 adds an opt-in per-key **block hook**: when set for a given args key, `Run`
blocks on `<-ctx.Done()` and returns `ctx.Err()`. This faithfully reproduces what the
real `execRunner.Run` returns when the context deadline fires mid-`p4`.

### Best-effort contract (unchanged)

A deadline error from `DeleteClient` is a normal `error` returned by `evict`. The
existing callers already log-and-skip it:

- `sweeper.go:97-103` (age pass) and `sweeper.go:123-129` (pressure pass): `log.Printf`
  then `continue` to the next candidate.
- `internal/agent/agent.go` on-demand path (~line 292): `log.Printf` and swallow.

This plan does NOT touch those call sites. No new panic, no `log.Fatal`, no abort of
the pass. Because the deadline fires inside `DeleteClient` (before `os.RemoveAll`),
`evict` returns at `sweeper.go:152-154` and never reaches the `os.RemoveAll` /
`MarkDirtyDelete` block at `sweeper.go:156-160`, so no DirtyDelete marker is set and
the next sweep retries the full eviction (client delete + dir delete).

---

## File structure

- **Create:** none.
- **Modify:** `internal/agent/source/perforce/sweeper.go`
  - Add `const defaultEvictTimeout = 30 * time.Minute` and `var evictTimeout = resolveEvictTimeout()` plus the `resolveEvictTimeout()` helper (reads `RELAY_EVICTION_TIMEOUT`).
  - Wrap the destructive body of `evict` in `context.WithTimeout(ctx, evictTimeout)`.
  - New import: `"os"` is already imported; add nothing for context (already imported). `time` already imported.
- **Modify (test):** `internal/agent/source/perforce/fixtures_test.go`
  - Add a `block map[string]bool` (or `blockKey string`) field + `setBlock` method to `fakeRunner`; honor it in `Run`.
- **Modify (test):** `internal/agent/source/perforce/sweeper_test.go`
  - Add the four new tests (Tasks 2, 4, 5) and the env-parsing test (Task 6).
- **Modify (docs):** `README.md` and `CLAUDE.md` env tables (Task 7).

Critical file: `internal/agent/source/perforce/sweeper.go` - the single production
change site. Everything else is tests and docs.

---

## Task 1: Add a ctx-aware block hook to `fakeRunner`

**Files:**
- Modify: `internal/agent/source/perforce/fixtures_test.go:18-25` (struct), `:33-41` (constructor), `:63-80` (`Run`)

- [ ] **Step 1: Write the failing test**

Add this test to `internal/agent/source/perforce/sweeper_test.go` (it drives the new
hook; it will not compile until the hook exists):

```go
func TestFakeRunner_BlockHookHonorsCtxCancel(t *testing.T) {
	fr := newFakeP4Fixture(t)
	fr.setBlock("client -d relay_h_x")

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err := fr.Run(ctx, "", []string{"client", "-d", "relay_h_x"}, nil)
	require.Error(t, err)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.Less(t, time.Since(start), 2*time.Second, "block hook must unblock on ctx deadline")
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd D:/dev/relay/.claude/worktrees/happy-mendel-18687f && go test ./internal/agent/source/perforce/... -run TestFakeRunner_BlockHookHonorsCtxCancel -v`
Expected: FAIL - compile error `fr.setBlock undefined (type *fakeRunner has no field or method setBlock)`.

- [ ] **Step 3: Write minimal implementation**

In `internal/agent/source/perforce/fixtures_test.go`, add a `block` map to the struct:

```go
type fakeRunner struct {
	t         tHelper
	calls     []runCall
	out       map[string]string
	err       map[string]error
	block     map[string]bool
	streamOut map[string]string
	streamErr map[string]error
}
```

Initialize it in the constructor:

```go
func newFakeP4Fixture(t tHelper) *fakeRunner {
	return &fakeRunner{
		t:         t,
		out:       map[string]string{},
		err:       map[string]error{},
		block:     map[string]bool{},
		streamOut: map[string]string{},
		streamErr: map[string]error{},
	}
}
```

Add the setter (place it next to `setErr`):

```go
// setBlock makes Run block on the given args key until ctx is cancelled, then
// return ctx.Err(). Models a wedged p4 subprocess that exec.CommandContext kills
// on deadline.
func (f *fakeRunner) setBlock(key string) {
	f.block[key] = true
}
```

Honor it at the top of `Run`, before the err/out lookups (so a blocked key never
falls through to the no-fixture `t.Errorf`):

```go
func (f *fakeRunner) Run(ctx context.Context, cwd string, args []string, stdin io.Reader) ([]byte, error) {
	key := strings.Join(args, " ")
	if f.block[key] {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	if e, ok := f.err[key]; ok && e != nil {
		return nil, e
	}
	if _, ok := f.out[key]; !ok {
		f.t.Helper()
		f.t.Errorf("fakeRunner.Run: no fixture for args %q (cwd=%q)", key, cwd)
		return nil, fmt.Errorf("fakeRunner: no fixture for %q", key)
	}
	var sb strings.Builder
	if stdin != nil {
		b, _ := io.ReadAll(stdin)
		sb.Write(b)
	}
	f.calls = append(f.calls, runCall{cwd: cwd, args: append([]string{}, args...), stdin: sb.String()})
	return []byte(f.out[key]), nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd D:/dev/relay/.claude/worktrees/happy-mendel-18687f && go test ./internal/agent/source/perforce/... -run TestFakeRunner_BlockHookHonorsCtxCancel -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
cd D:/dev/relay/.claude/worktrees/happy-mendel-18687f && \
git add internal/agent/source/perforce/fixtures_test.go internal/agent/source/perforce/sweeper_test.go && \
git commit -m "test(perforce): add ctx-aware block hook to fakeRunner"
```

---

## Task 2: Bound `p4 client -d` with a per-eviction timeout (the core change)

**Files:**
- Modify: `internal/agent/source/perforce/sweeper.go` (add const/var/helper; wrap `evict` body)
- Test: `internal/agent/source/perforce/sweeper_test.go`

- [ ] **Step 1: Write the failing test**

Add to `internal/agent/source/perforce/sweeper_test.go`. This test shortens the
package var `evictTimeout`, registers one stale candidate whose `client -d` hangs, and
asserts `SweepOnce` returns within a short bound, evicts nothing, leaves the directory
on disk, and does NOT mark DirtyDelete:

```go
func TestSweeper_EvictTimesOutOnHangingDeleteClient(t *testing.T) {
	prev := evictTimeout
	evictTimeout = 50 * time.Millisecond
	defer func() { evictTimeout = prev }()

	root := t.TempDir()
	fr := newFakeP4Fixture(t)
	fr.setBlock("client -d relay_h_stuck")

	reg, _ := LoadRegistry(filepath.Join(root, ".relay-registry.json"))
	reg.Upsert(WorkspaceEntry{ShortID: "stuck", SourceKey: "//s/x",
		ClientName: "relay_h_stuck", LastUsedAt: time.Now().Add(-30 * 24 * time.Hour)})
	require.NoError(t, reg.Save())
	require.NoError(t, os.MkdirAll(filepath.Join(root, "stuck"), 0o755))

	s := &Sweeper{
		Root:       root,
		Reg:        reg,
		MaxAge:     14 * 24 * time.Hour,
		Client:     &Client{r: fr},
		ListLocked: func() map[string]bool { return nil },
	}

	done := make(chan struct{})
	var evicted []string
	var err error
	go func() {
		evicted, err = s.SweepOnce(context.Background())
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("SweepOnce did not return; per-eviction timeout not enforced")
	}

	require.NoError(t, err, "a timed-out eviction is logged-and-skipped, not a SweepOnce error")
	require.Empty(t, evicted, "the stuck workspace must not be reported as evicted")

	// os.RemoveAll was never reached: the directory still exists.
	_, statErr := os.Stat(filepath.Join(root, "stuck"))
	require.NoError(t, statErr, "os.RemoveAll must not run when p4 client -d times out")

	// No DirtyDelete marker: the next sweep retries the FULL eviction.
	e, ok := reg.Get("stuck")
	require.True(t, ok, "the entry stays in the registry for retry")
	require.False(t, e.DirtyDelete, "a p4 timeout must NOT set DirtyDelete (dir untouched)")
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd D:/dev/relay/.claude/worktrees/happy-mendel-18687f && go test ./internal/agent/source/perforce/... -run TestSweeper_EvictTimesOutOnHangingDeleteClient -v`
Expected: FAIL - compile error `undefined: evictTimeout` (the package var does not exist yet).

- [ ] **Step 3: Write minimal implementation**

In `internal/agent/source/perforce/sweeper.go`, add the const, var, and helper near
the top (after the `ErrEvictClaimLost` declaration, before the `Sweeper` struct):

```go
// defaultEvictTimeout bounds a single workspace eviction's p4 client -d subprocess
// so a wedged Perforce call cannot stall the serialized sweep loop (and thus disk
// reclamation) for the agent's lifetime. os.RemoveAll is local I/O and is NOT
// bounded by this deadline (os.RemoveAll does not honor context).
const defaultEvictTimeout = 30 * time.Minute

// evictTimeout is the effective per-eviction p4 deadline. It is resolved once from
// RELAY_EVICTION_TIMEOUT (a Go duration, e.g. 45m, 2h), falling back to
// defaultEvictTimeout when the var is unset or unparseable, mirroring the
// RELAY_WORKER_GRACE_WINDOW convention. It is a package var (not a const) so tests
// can shorten it to a few milliseconds; this is the only override seam and adds no
// production config beyond the env var.
var evictTimeout = resolveEvictTimeout()

// resolveEvictTimeout reads RELAY_EVICTION_TIMEOUT and falls back to
// defaultEvictTimeout on unset/unparseable input (ignore-on-error).
func resolveEvictTimeout() time.Duration {
	if v := os.Getenv("RELAY_EVICTION_TIMEOUT"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			return d
		}
	}
	return defaultEvictTimeout
}
```

Then wrap the destructive body of `evict`. Insert the `WithTimeout` immediately after
the Claim/release block and before the `DeleteClient` call (`os` and `context` are
already imported):

```go
func (s *Sweeper) evict(ctx context.Context, reg *Registry, w WorkspaceEntry) error {
	if s.Claim != nil {
		release, ok := s.Claim(w.ShortID)
		if !ok {
			return ErrEvictClaimLost
		}
		// release() must stay the outermost deferred cleanup so the reservation
		// outlives OnEvictedCB (which clears p.workspaces). A Prepare arriving
		// between OnEvictedCB and release() still sees p.evicting set and backs
		// out; only after release() does it find no registry entry and rebuild.
		defer release()
	}
	// Bound the p4 client -d subprocess so a wedged Perforce call cannot stall the
	// serialized sweep. os.RemoveAll below is local I/O and intentionally NOT bounded
	// (it does not honor context). On deadline, DeleteClient returns before
	// os.RemoveAll, so no DirtyDelete marker is set and the next sweep retries fully.
	evictCtx, cancelEvict := context.WithTimeout(ctx, evictTimeout)
	defer cancelEvict()
	// When w.DirtyDelete is set, a prior sweep already deleted the p4 client
	// and only the on-disk directory remains. Calling DeleteClient again would
	// fail ("client doesn't exist") and previously wedged the whole sweep.
	if !w.DirtyDelete {
		if err := s.Client.DeleteClient(evictCtx, w.ClientName); err != nil {
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

Note the deferred cancel is named `cancelEvict` and the bounded context is
`evictCtx`; only the `DeleteClient` call uses `evictCtx`. `os.RemoveAll` and the
registry calls keep their non-ctx form (unchanged behavior).

- [ ] **Step 4: Run test to verify it passes**

Run: `cd D:/dev/relay/.claude/worktrees/happy-mendel-18687f && go test ./internal/agent/source/perforce/... -run TestSweeper_EvictTimesOutOnHangingDeleteClient -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
cd D:/dev/relay/.claude/worktrees/happy-mendel-18687f && \
git add internal/agent/source/perforce/sweeper.go internal/agent/source/perforce/sweeper_test.go && \
git commit -m "feat(perforce): bound p4 client -d in evict with RELAY_EVICTION_TIMEOUT"
```

---

## Task 3: Confirm happy-path regression (existing tests pass unchanged)

**Files:**
- Test: `internal/agent/source/perforce/sweeper_test.go` (existing `TestSweeper_AgeEviction`, `TestSweeper_PressureEviction`, `TestSweeper_DirtyDeleteSkipsDeleteClient`, `TestSweeper_ContinuesPastEvictFailure`)

This task adds no new code - it verifies the existing happy-path and best-effort tests
still pass with the timeout wrapping in place (they use the production `evictTimeout`,
which is 30m by default and never fires for the instant `fakeRunner` responses).

- [ ] **Step 1: Run the existing sweeper tests**

Run: `cd D:/dev/relay/.claude/worktrees/happy-mendel-18687f && go test ./internal/agent/source/perforce/... -run "TestSweeper_AgeEviction|TestSweeper_PressureEviction|TestSweeper_DirtyDeleteSkipsDeleteClient|TestSweeper_ContinuesPastEvictFailure|TestSweeper_UsesInjectedRegistry|TestSweeper_SkipsLockedWorkspaces" -v`
Expected: all PASS, no behavior change (the full eviction path including `os.RemoveAll`
completes for fast evictions).

- [ ] **Step 2: No commit needed** (no source change in this task). If the engineer
made any incidental fix to keep these green, that is a signal the wrapping was wrong -
stop and re-check Task 2. Otherwise proceed.

---

## Task 4: Pass continues after one timeout (second candidate still evicted)

**Files:**
- Test: `internal/agent/source/perforce/sweeper_test.go`

- [ ] **Step 1: Write the failing test**

This proves acceptance criterion 2: a stuck first eviction does not abort the pass.
Write it before confirming it passes (it depends only on Task 1 + Task 2 code, so it
should pass immediately once those land - but author it as a distinct regression so the
sweep-continuation property is asserted explicitly):

```go
func TestSweeper_ContinuesPastEvictTimeout(t *testing.T) {
	prev := evictTimeout
	evictTimeout = 50 * time.Millisecond
	defer func() { evictTimeout = prev }()

	root := t.TempDir()
	fr := newFakeP4Fixture(t)
	// Oldest candidate hangs; newer candidate's client -d succeeds.
	fr.setBlock("client -d relay_h_stuck")
	fr.set("client -d relay_h_good", "Client deleted.\n")

	reg, _ := LoadRegistry(filepath.Join(root, ".relay-registry.json"))
	reg.Upsert(WorkspaceEntry{ShortID: "stuck", SourceKey: "//s/stuck",
		ClientName: "relay_h_stuck", LastUsedAt: time.Now().Add(-40 * 24 * time.Hour)})
	reg.Upsert(WorkspaceEntry{ShortID: "good", SourceKey: "//s/good",
		ClientName: "relay_h_good", LastUsedAt: time.Now().Add(-30 * 24 * time.Hour)})
	require.NoError(t, reg.Save())
	require.NoError(t, os.MkdirAll(filepath.Join(root, "stuck"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "good"), 0o755))

	s := &Sweeper{
		Root:       root,
		Reg:        reg,
		MaxAge:     14 * 24 * time.Hour,
		Client:     &Client{r: fr},
		ListLocked: func() map[string]bool { return nil },
	}

	done := make(chan struct{})
	var evicted []string
	var err error
	go func() {
		evicted, err = s.SweepOnce(context.Background())
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("SweepOnce wedged on the stuck candidate; pass did not continue")
	}

	require.NoError(t, err)
	require.Equal(t, []string{"good"}, evicted, "the second candidate must still be evicted")

	// good is fully gone; stuck remains (un-dirty) for a future retry.
	_, statErr := os.Stat(filepath.Join(root, "good"))
	require.ErrorIs(t, statErr, os.ErrNotExist)
	_, ok := reg.Get("good")
	require.False(t, ok)
	stuckEntry, ok := reg.Get("stuck")
	require.True(t, ok)
	require.False(t, stuckEntry.DirtyDelete)
}
```

- [ ] **Step 2: Run test to verify behavior**

Run: `cd D:/dev/relay/.claude/worktrees/happy-mendel-18687f && go test ./internal/agent/source/perforce/... -run TestSweeper_ContinuesPastEvictTimeout -v`
Expected: PASS (the production code from Task 2 already satisfies this; the test locks
in the sweep-continuation property). If it FAILS, the timeout is aborting the pass -
re-check that `evict` returns a normal error and the `sweeper.go:97-103` log-and-continue
path is untouched.

- [ ] **Step 3: Commit**

```bash
cd D:/dev/relay/.claude/worktrees/happy-mendel-18687f && \
git add internal/agent/source/perforce/sweeper_test.go && \
git commit -m "test(perforce): sweep continues past a timed-out eviction"
```

---

## Task 5: Env parsing - `RELAY_EVICTION_TIMEOUT` honored; unset/garbage falls back

**Files:**
- Test: `internal/agent/source/perforce/sweeper_test.go`

`evictTimeout` is resolved at package-init time, so a `t.Setenv` after init does not
re-resolve it. Test `resolveEvictTimeout()` directly (it is the function that reads the
env), which is what package init calls.

- [ ] **Step 1: Write the failing test**

```go
func TestResolveEvictTimeout(t *testing.T) {
	t.Run("valid duration is honored", func(t *testing.T) {
		t.Setenv("RELAY_EVICTION_TIMEOUT", "45m")
		require.Equal(t, 45*time.Minute, resolveEvictTimeout())
	})
	t.Run("unset falls back to default", func(t *testing.T) {
		t.Setenv("RELAY_EVICTION_TIMEOUT", "")
		require.Equal(t, defaultEvictTimeout, resolveEvictTimeout())
	})
	t.Run("garbage falls back to default", func(t *testing.T) {
		t.Setenv("RELAY_EVICTION_TIMEOUT", "not-a-duration")
		require.Equal(t, defaultEvictTimeout, resolveEvictTimeout())
	})
	t.Run("non-positive falls back to default", func(t *testing.T) {
		t.Setenv("RELAY_EVICTION_TIMEOUT", "0s")
		require.Equal(t, defaultEvictTimeout, resolveEvictTimeout())
	})
}
```

- [ ] **Step 2: Run test to verify it passes**

Run: `cd D:/dev/relay/.claude/worktrees/happy-mendel-18687f && go test ./internal/agent/source/perforce/... -run TestResolveEvictTimeout -v`
Expected: PASS (`resolveEvictTimeout` exists from Task 2). If a subtest FAILS, fix the
helper's ignore-on-error / `d > 0` guard in `sweeper.go` to match.

- [ ] **Step 3: Commit**

```bash
cd D:/dev/relay/.claude/worktrees/happy-mendel-18687f && \
git add internal/agent/source/perforce/sweeper_test.go && \
git commit -m "test(perforce): RELAY_EVICTION_TIMEOUT parsing and fallback"
```

---

## Task 6: Document `RELAY_EVICTION_TIMEOUT`

**Files:**
- Modify: `README.md` (the agent env-var table near `RELAY_WORKSPACE_SWEEP_INTERVAL`, around `README.md:369-372`)

- [ ] **Step 1: Add the env-var row**

In `README.md`, in the agent workspace env-var table, add a row immediately after the
`RELAY_WORKSPACE_SWEEP_INTERVAL` row (currently `README.md:372`):

```markdown
| `RELAY_EVICTION_TIMEOUT` | Per-eviction deadline (Go duration, e.g. `45m`, `2h`) bounding the `p4 client -d` call during workspace eviction. Default `30m`. A wedged delete becomes a logged, retryable best-effort skip instead of stalling the sweeper. Does NOT bound the on-disk `os.RemoveAll`. |
```

- [ ] **Step 2: Verify the table renders (visual check)**

Run: `cd D:/dev/relay/.claude/worktrees/happy-mendel-18687f && git diff README.md`
Expected: a single added table row; column alignment matches neighbors.

- [ ] **Step 3: Commit**

```bash
cd D:/dev/relay/.claude/worktrees/happy-mendel-18687f && \
git add README.md && \
git commit -m "docs: document RELAY_EVICTION_TIMEOUT agent env var"
```

---

## Task 7: Final package verification

**Files:** none (verification only).

- [ ] **Step 1: Full perforce package test run**

Run: `cd D:/dev/relay/.claude/worktrees/happy-mendel-18687f && go test ./internal/agent/source/perforce/... -v -timeout 60s`
Expected: all PASS, including the new timeout, continuation, and env-parsing tests.

- [ ] **Step 2: Build the env-reading package (sanity)**

The env var is read inside the perforce package, but confirm the agent binary still
builds (no plumbing was needed in `cmd/relay-agent/main.go`, which is the established
env layer):

Run: `cd D:/dev/relay/.claude/worktrees/happy-mendel-18687f && go build ./cmd/relay-agent/...`
Expected: builds clean.

- [ ] **Step 3: Vet**

Run: `cd D:/dev/relay/.claude/worktrees/happy-mendel-18687f && go vet ./internal/agent/source/perforce/... ./cmd/relay-agent/...`
Expected: no findings.

- [ ] **Step 4: Full unit suite (no Docker)**

Run: `cd D:/dev/relay/.claude/worktrees/happy-mendel-18687f && make test`
Expected: all PASS. (No `//go:build !windows` tests were added, so Windows `make test`
exercises everything in this change; no Docker / `make test-integration` is required for
this change - it touches no DB, no p4d.)

---

## Verify commands (summary)

- `go test ./internal/agent/source/perforce/... -v -timeout 60s` - the changed package.
- `go build ./cmd/relay-agent/...` - the established env layer still builds.
- `go vet ./internal/agent/source/perforce/... ./cmd/relay-agent/...`
- `make test` - full unit suite (Windows-runnable; no Docker needed for this change).
- No `make test-integration` / Docker required: no DB, no p4d, no `//go:build !windows`
  tests added.

---

## Self-review against the spec

- **Acceptance 1** (hang -> bounded return): Task 2 `TestSweeper_EvictTimesOutOnHangingDeleteClient`.
- **Acceptance 2** (pass continues): Task 4 `TestSweeper_ContinuesPastEvictTimeout`.
- **Acceptance 3** (logged, no panic/exit): callers at `sweeper.go:101/:127` and
  `agent.go:~292` unchanged; verified by Task 4 (`SweepOnce` returns `nil` err and the
  next candidate is processed) - no new panic/`log.Fatal` introduced.
- **Acceptance 4** (no DirtyDelete on p4 timeout): asserted in both Task 2 and Task 4
  (`require.False(t, e.DirtyDelete)`).
- **Acceptance 5** (happy-path unchanged): Task 3 re-runs the existing suite.
- **Env-configurable, 30m default** (spec "Default timeout value" section): Task 2
  adds `defaultEvictTimeout = 30 * time.Minute` and `RELAY_EVICTION_TIMEOUT` parsing via
  `time.ParseDuration` ignore-on-error; Task 5 tests it; Task 6 documents it.
- **Option A** (bound p4 only, leave `os.RemoveAll` unbounded): Task 2 wraps only the
  `DeleteClient` call in `evictCtx`; `os.RemoveAll` keeps its non-ctx form.
- **Test seam** (untagged package-var override): `evictTimeout` set+restored in tests;
  no production config beyond the env var.
- **No CLAUDE.md Invariant touched**: no `tasks.status`/`task_logs` writes (no epoch
  fence), no gRPC sender, locking discipline (`p.mu`->`ws.mu`, the Claim reservation)
  untouched - the timeout wraps only the I/O between reservation and release.
