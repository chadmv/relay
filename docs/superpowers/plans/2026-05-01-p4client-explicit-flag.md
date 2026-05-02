# P4CLIENT explicit-flag fix — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Close the "caller is responsible for setting `P4CLIENT`" contract gap in `internal/agent/source/perforce` by making every workspace-scoped `p4` invocation name its client explicitly via the global `-c` flag and run from the workspace dir as cwd.

**Architecture:** Two coordinated changes inside `internal/agent/source/perforce`:
1. The `Runner` interface gains a `cwd string` parameter; `execRunner` honors it via `cmd.Dir`; the test `fakeRunner` records but does not key on cwd.
2. Six `Client` methods (`SyncStream`, `CreatePendingCL`, `Unshelve`, `RevertCL`, `DeleteCL`, `PendingChangesByDescPrefix`) take a `client string` parameter and prepend `["-c", client]` to argv. Production callers (`Provider.Prepare`, `Provider.recoverOrphanedCLs`, `perforceHandle.Finalize`) pass the workspace's `wsRoot` and `clientName`.

`ResolveHead`, `CreateStreamClient`, `DeleteClient` do not change shape — they are server-global or already name their target client in argv.

**Tech Stack:** Go 1.22+, `os/exec`, testify, `relay` internal packages. No new dependencies.

**Spec:** [docs/superpowers/specs/2026-05-01-p4client-explicit-flag-design.md](../specs/2026-05-01-p4client-explicit-flag-design.md)

---

## File Map

**Modified:**
- `internal/agent/source/perforce/client.go` — `Runner` interface, `execRunner`, six `Client` methods, comment on `SyncStream`.
- `internal/agent/source/perforce/perforce.go` — `Provider.Prepare`, `Provider.recoverOrphanedCLs` (signature gains `wsRoot`), `perforceHandle.Finalize`.
- `internal/agent/source/perforce/fixtures_test.go` — fake runner gains `cwd` param on `Run`/`Stream`; `expectedClientName` helper moved here from `p4d_container_test.go`.
- `internal/agent/source/perforce/perforce_test.go` — three tests updated: fixture keys gain `-c <client>` prefix; one test gains an explicit argv-prefix assertion.
- `internal/agent/source/perforce/perforce_integration_test.go` — remove the `t.Setenv("P4CLIENT", …)` workaround.
- `internal/agent/source/perforce/p4d_container_test.go` — remove `expectedClientName` (moved to `fixtures_test.go`).
- `docs/backlog/bug-2026-05-01-p4client-env-var-dependency.md` — moved to `docs/backlog/closed/`, frontmatter updated, `## Resolution` appended.

**Unchanged:** `client_test.go`, `provider_sweeper_test.go`, `sweeper_test.go`, `sweeper.go` — they exercise only `CreateStreamClient`, `DeleteClient`, and `ResolveHead`, none of which change shape.

---

## Task 1: Promote `expectedClientName` helper

The helper currently lives in `p4d_container_test.go`, which has `//go:build integration`. Unit tests can't see it. Move it to `fixtures_test.go` (no build tag) so both unit and integration tests can use it.

**Files:**
- Modify: `internal/agent/source/perforce/fixtures_test.go`
- Modify: `internal/agent/source/perforce/p4d_container_test.go`

- [ ] **Step 1: Move the helper to `fixtures_test.go`**

Append to the bottom of `internal/agent/source/perforce/fixtures_test.go`:

```go
import "fmt" // add to existing imports if not already present

// expectedClientName predicts the stream-bound client name that
// Provider.Prepare creates. Calls allocateShortID directly with an empty
// registry so the helper tracks any future change to the production shortID
// derivation (including the collision-resolution loop, if it ever fires).
func expectedClientName(hostname, sourceKey string) string {
	return fmt.Sprintf("relay_%s_%s", hostname, allocateShortID(sourceKey, &Registry{}))
}
```

(The current `fixtures_test.go` imports `"context"`, `"io"`, `"strings"`. Add `"fmt"` to the import block.)

- [ ] **Step 2: Remove the helper from `p4d_container_test.go`**

Delete lines 96-103 (the `expectedClientName` function and its preceding doc comment) from `internal/agent/source/perforce/p4d_container_test.go`. If `fmt` is no longer used in that file, remove it from the import block.

- [ ] **Step 3: Verify both build tags compile**

Run:
```bash
go build ./internal/agent/source/perforce/...
go test -tags integration -run TestNeverMatches ./internal/agent/source/perforce/... -v
```

Both must succeed (the second compiles the integration build with no test selected).

- [ ] **Step 4: Commit**

```bash
git add internal/agent/source/perforce/fixtures_test.go internal/agent/source/perforce/p4d_container_test.go
git commit -m "refactor(perforce): make expectedClientName visible to unit tests"
```

---

## Task 2: Add `cwd` to the `Runner` interface

This is a pure interface widening: every call site passes `""` after this task. No production behavior changes.

**Files:**
- Modify: `internal/agent/source/perforce/client.go` (lines 15-60 area)
- Modify: `internal/agent/source/perforce/fixtures_test.go` (lines 51, 65)

- [ ] **Step 1: Update the `Runner` interface and `execRunner` in `client.go`**

Replace the existing `Runner` interface and `execRunner` methods (current lines 15-60) with:

```go
// Runner is the interface used to invoke p4 commands. Swappable in tests.
// cwd, when non-empty, sets the child process's working directory; pass ""
// for server-global operations that aren't tied to a specific workspace.
type Runner interface {
	Run(ctx context.Context, cwd string, args []string, stdin io.Reader) ([]byte, error)
	Stream(ctx context.Context, cwd string, args []string, onLine func(string)) error
}

// execRunner uses os/exec to invoke the p4 binary on PATH.
type execRunner struct{ binary string }

func newExecRunner() *execRunner { return &execRunner{binary: "p4"} }

func (e *execRunner) Run(ctx context.Context, cwd string, args []string, stdin io.Reader) ([]byte, error) {
	cmd := exec.CommandContext(ctx, e.binary, args...)
	if cwd != "" {
		cmd.Dir = cwd
	}
	if stdin != nil {
		cmd.Stdin = stdin
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("p4 %s: %w (stderr: %s)", strings.Join(args, " "), err, stderr.String())
	}
	return out, nil
}

func (e *execRunner) Stream(ctx context.Context, cwd string, args []string, onLine func(string)) error {
	cmd := exec.CommandContext(ctx, e.binary, args...)
	if cwd != "" {
		cmd.Dir = cwd
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return err
	}
	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		onLine(sc.Text())
	}
	if err := cmd.Wait(); err != nil {
		return fmt.Errorf("p4 %s: %w (stderr: %s)", strings.Join(args, " "), err, stderr.String())
	}
	return nil
}
```

- [ ] **Step 2: Update the fake runner in `fixtures_test.go`**

Replace the `runCall` struct and the `Run`/`Stream` methods with versions that accept and record `cwd`:

```go
type runCall struct {
	cwd   string
	args  []string
	stdin string
}

func (f *fakeRunner) Run(ctx context.Context, cwd string, args []string, stdin io.Reader) ([]byte, error) {
	key := strings.Join(args, " ")
	if e, ok := f.err[key]; ok && e != nil {
		return nil, e
	}
	var sb strings.Builder
	if stdin != nil {
		b, _ := io.ReadAll(stdin)
		sb.Write(b)
	}
	f.calls = append(f.calls, runCall{cwd: cwd, args: append([]string{}, args...), stdin: sb.String()})
	return []byte(f.out[key]), nil
}

func (f *fakeRunner) Stream(ctx context.Context, cwd string, args []string, onLine func(string)) error {
	key := strings.Join(args, " ")
	if e, ok := f.streamErr[key]; ok && e != nil {
		return e
	}
	for _, line := range strings.Split(f.streamOut[key], "\n") {
		if line != "" {
			onLine(line)
		}
	}
	f.calls = append(f.calls, runCall{cwd: cwd, args: append([]string{}, args...)})
	return nil
}
```

The lookup key remains `strings.Join(args, " ")` — cwd is recorded but not part of the match key, so test fixture tables don't need to embed `t.TempDir()` paths.

- [ ] **Step 3: Update every call to `c.r.Run` / `c.r.Stream` in `client.go` to pass `""` for cwd**

In `client.go`, every `c.r.Run(ctx, …)` and `c.r.Stream(ctx, …)` becomes `c.r.Run(ctx, "", …)` / `c.r.Stream(ctx, "", …)`. There are eight call sites:

- Line 78: `c.r.Run(ctx, args, nil)` → `c.r.Run(ctx, "", args, nil)` (in `CreateStreamClient`)
- Line 88: `c.r.Run(ctx, []string{"client", "-i"}, bytes.NewReader(spec))` → `c.r.Run(ctx, "", []string{"client", "-i"}, bytes.NewReader(spec))`
- Line 96: `c.r.Run(ctx, []string{"client", "-d", name}, nil)` → `c.r.Run(ctx, "", []string{"client", "-d", name}, nil)` (in `DeleteClient`)
- Line 104: `c.r.Run(ctx, []string{"changes", "-m1", path + "#head"}, nil)` → add `""` before args (in `ResolveHead`)
- Line 120: `c.r.Stream(ctx, args, onLine)` → `c.r.Stream(ctx, "", args, onLine)` (in `SyncStream`)
- Line 126: `c.r.Run(ctx, []string{"change", "-o"}, nil)` → add `""` (in `CreatePendingCL`)
- Line 132: `c.r.Run(ctx, []string{"change", "-i"}, bytes.NewReader(spec))` → add `""` (in `CreatePendingCL`)
- Line 146: `c.r.Run(ctx, []string{"unshelve", …}, nil)` → add `""` (in `Unshelve`)
- Line 155: `c.r.Run(ctx, []string{"revert", …}, nil)` → add `""` (in `RevertCL`)
- Line 161: `c.r.Run(ctx, []string{"change", "-d", …}, nil)` → add `""` (in `DeleteCL`)
- Line 168: `c.r.Run(ctx, []string{"changes", "-c", client, …}, nil)` → add `""` (in `PendingChangesByDescPrefix`)

(Tasks 3-8 will replace several of these `""` placeholders with real workspace dirs.)

- [ ] **Step 4: Run the full unit suite to confirm no regressions**

Run:
```bash
go test ./internal/agent/source/perforce/... -v -timeout 30s
```

All tests must pass. Behavior is unchanged because every call passes `""` and `execRunner` only sets `cmd.Dir` when cwd is non-empty.

- [ ] **Step 5: Commit**

```bash
git add internal/agent/source/perforce/client.go internal/agent/source/perforce/fixtures_test.go
git commit -m "refactor(perforce): add cwd to Runner interface (no-op)"
```

---

## Task 3: Wire `(cwd, client)` into `SyncStream`

The first method to thread the new contract end-to-end. Test-first: update the fixture key to expect the new argv, watch the test fail, then update production code.

**Files:**
- Modify: `internal/agent/source/perforce/perforce_test.go` (`TestProvider_PrepareCreatesClientAndSyncs`)
- Modify: `internal/agent/source/perforce/client.go` (`SyncStream`)
- Modify: `internal/agent/source/perforce/perforce.go` (`Provider.Prepare` SyncStream call)

- [ ] **Step 1: Update `TestProvider_PrepareCreatesClientAndSyncs` to expect `-c <client>` in argv**

In `perforce_test.go`, replace the body of `TestProvider_PrepareCreatesClientAndSyncs` so the sync fixture key includes the global `-c` flag and an explicit argv-prefix assertion is added:

```go
func TestProvider_PrepareCreatesClientAndSyncs(t *testing.T) {
	root := t.TempDir()
	fr := newFakeP4Fixture()
	expectedClient := expectedClientName("h", "//s/x")
	// ResolveHead: "changes -m1 //s/x/...#head" → CL 12345
	fr.set("changes -m1 //s/x/...#head", "Change 12345 on 2026-04-24 by relay@h '...'\n")
	// CreateStreamClient: "client -i" succeeds; "client -o -S //s/x <name>" returns empty (ok)
	fr.set("client -i", "Client saved.\n")
	// SyncStream: now invoked with global -c <client>.
	fr.setStream("-c "+expectedClient+" sync -q --parallel=4 //s/x/...@12345", "1 of 1 files\n")

	p := New(Config{Root: root, Hostname: "h", Client: &Client{r: fr}})
	spec := &relayv1.SourceSpec{Provider: &relayv1.SourceSpec_Perforce{
		Perforce: &relayv1.PerforceSource{
			Stream: "//s/x",
			Sync:   []*relayv1.SyncEntry{{Path: "//s/x/...", Rev: "#head"}},
		},
	}}
	var lines []string
	h, err := p.Prepare(context.Background(), "task-1", spec, func(s string) { lines = append(lines, s) })
	require.NoError(t, err)
	defer h.Finalize(context.Background())

	inv := h.Inventory()
	require.Equal(t, "perforce", inv.SourceType)
	require.Equal(t, "//s/x", inv.SourceKey)
	require.NotEmpty(t, inv.ShortID)
	require.NotEmpty(t, inv.BaselineHash)

	require.True(t, filepath.IsAbs(h.WorkingDir()))
	require.Contains(t, h.WorkingDir(), inv.ShortID)
	require.Contains(t, h.Env()["P4CLIENT"], inv.ShortID)
	require.NotEmpty(t, lines, "sync stream should have produced progress lines")

	// Pin the contract: the sync invocation MUST start with `-c <client>`.
	// This guards against a future refactor silently dropping the global flag.
	var syncCall []string
	for _, c := range fr.argHistory() {
		if len(c) >= 3 && c[2] == "sync" {
			syncCall = c
			break
		}
	}
	require.NotNil(t, syncCall, "expected a sync invocation in argHistory")
	require.Equal(t, []string{"-c", expectedClient}, syncCall[:2],
		"sync invocation must begin with -c <client>")
}
```

- [ ] **Step 2: Run the test, expect failure**

Run:
```bash
go test ./internal/agent/source/perforce/ -run TestProvider_PrepareCreatesClientAndSyncs -v -timeout 30s
```

Expected: FAIL. `lines` is empty (no fixture matches the old `sync -q --parallel=4 …` key) and the argv-prefix assertion fails.

- [ ] **Step 3: Update `Client.SyncStream` signature and body**

In `client.go`, replace `SyncStream` (current lines 116-121) with:

```go
// SyncStream runs `p4 -c <client> sync -q --parallel=4 <specs...>` from cwd,
// streaming lines to onLine.
func (c *Client) SyncStream(ctx context.Context, cwd, client string, specs []string, onLine func(string)) error {
	args := append([]string{"-c", client, "sync", "-q", "--parallel=4"}, specs...)
	return c.r.Stream(ctx, cwd, args, onLine)
}
```

- [ ] **Step 4: Update the production caller in `Provider.Prepare`**

In `perforce.go`, replace the SyncStream call (current line 194):

```go
if err := p.cfg.Client.SyncStream(ctx, syncSpecs, progress); err != nil {
```

with:

```go
if err := p.cfg.Client.SyncStream(ctx, wsRoot, clientName, syncSpecs, progress); err != nil {
```

- [ ] **Step 5: Run the test, expect pass**

Run:
```bash
go test ./internal/agent/source/perforce/ -run TestProvider_PrepareCreatesClientAndSyncs -v -timeout 30s
```

Expected: PASS.

- [ ] **Step 6: Run the rest of the perforce unit suite**

Run:
```bash
go test ./internal/agent/source/perforce/... -v -timeout 30s
```

Expected: `TestProvider_UnshelveAndFinalizeRevert` and `TestProvider_CrashRecovery_DeletesOrphanedPendingCLs` will FAIL — they exercise SyncStream too, with the old fixture keys. That is expected; Tasks 4-8 will fix them as their methods get updated. Other tests must still pass.

- [ ] **Step 7: Commit**

```bash
git add internal/agent/source/perforce/client.go internal/agent/source/perforce/perforce.go internal/agent/source/perforce/perforce_test.go
git commit -m "fix(perforce): pass -c <client> on SyncStream"
```

---

## Task 4: Wire `(cwd, client)` into `CreatePendingCL`

`CreatePendingCL` calls `p4 change -o` (read template) and `p4 change -i` (commit). Both need `-c <client>` so the resulting CL is owned by the right client.

**Files:**
- Modify: `internal/agent/source/perforce/perforce_test.go` (`TestProvider_UnshelveAndFinalizeRevert`)
- Modify: `internal/agent/source/perforce/client.go` (`CreatePendingCL`)
- Modify: `internal/agent/source/perforce/perforce.go` (`Provider.Prepare` CreatePendingCL call)

- [ ] **Step 1: Update `TestProvider_UnshelveAndFinalizeRevert` fixture keys**

In `perforce_test.go`, near the top of `TestProvider_UnshelveAndFinalizeRevert`, compute the expected client and update the `change -o` / `change -i` / sync fixture keys. Replace the fixture-setup block (current lines 51-59):

```go
fr := newFakeP4Fixture()
fr.set("changes -m1 //s/x/...#head", "Change 12345 on 2026-04-24 by relay@h '...'\n")
fr.set("client -i", "Client saved.\n")
fr.setStream("sync -q --parallel=4 //s/x/...@12345", "1 of 1 files\n")
fr.set("change -o", "Change: new\nDescription:\t<enter description here>\n")
fr.set("change -i", "Change 91244 created.\n")
fr.set("unshelve -s 12346 -c 91244", "//s/x/foo - unshelved\n")
fr.set("revert -c 91244 //...", "//s/x/foo - reverted\n")
fr.set("change -d 91244", "Change 91244 deleted.\n")
```

with:

```go
fr := newFakeP4Fixture()
expectedClient := expectedClientName("h", "//s/x")
fr.set("changes -m1 //s/x/...#head", "Change 12345 on 2026-04-24 by relay@h '...'\n")
fr.set("client -i", "Client saved.\n")
fr.setStream("-c "+expectedClient+" sync -q --parallel=4 //s/x/...@12345", "1 of 1 files\n")
fr.set("-c "+expectedClient+" change -o", "Change: new\nDescription:\t<enter description here>\n")
fr.set("-c "+expectedClient+" change -i", "Change 91244 created.\n")
fr.set("unshelve -s 12346 -c 91244", "//s/x/foo - unshelved\n")
fr.set("revert -c 91244 //...", "//s/x/foo - reverted\n")
fr.set("change -d 91244", "Change 91244 deleted.\n")
```

(Tasks 5-7 will further prefix `unshelve`, `revert`, and `change -d` with `-c <client>`. Update the `found(...)` checks at the end accordingly when those tasks land.)

- [ ] **Step 2: Run the test, expect failure on `change -o`**

Run:
```bash
go test ./internal/agent/source/perforce/ -run TestProvider_UnshelveAndFinalizeRevert -v -timeout 30s
```

Expected: FAIL — the production `CreatePendingCL` still emits `change -o` without `-c`, so the fakeRunner returns empty for the prefixed key and the spec form is empty.

- [ ] **Step 3: Update `Client.CreatePendingCL`**

In `client.go`, replace `CreatePendingCL` (current lines 123-142):

```go
// CreatePendingCL creates an empty pending changelist on the named client
// with the given description. Returns the new CL number.
func (c *Client) CreatePendingCL(ctx context.Context, cwd, client, description string) (int64, error) {
	spec, err := c.r.Run(ctx, cwd, []string{"-c", client, "change", "-o"}, nil)
	if err != nil {
		return 0, err
	}
	spec = setSpecField(spec, "Description", description)
	spec = removeSpecBlock(spec, "Files")
	out, err := c.r.Run(ctx, cwd, []string{"-c", client, "change", "-i"}, bytes.NewReader(spec))
	if err != nil {
		return 0, err
	}
	re := regexp.MustCompile(`Change (\d+) created`)
	m := re.FindSubmatch(out)
	if m == nil {
		return 0, fmt.Errorf("unexpected change -i output: %s", out)
	}
	return strconv.ParseInt(string(m[1]), 10, 64)
}
```

- [ ] **Step 4: Update `Provider.Prepare` caller**

In `perforce.go`, replace the CreatePendingCL call (current line 209):

```go
cl, err := p.cfg.Client.CreatePendingCL(ctx, "relay-task-"+taskID)
```

with:

```go
cl, err := p.cfg.Client.CreatePendingCL(ctx, wsRoot, clientName, "relay-task-"+taskID)
```

- [ ] **Step 5: Run the test**

Run:
```bash
go test ./internal/agent/source/perforce/ -run TestProvider_UnshelveAndFinalizeRevert -v -timeout 30s
```

Expected: it advances past `change -o` / `change -i`. It may still fail on `unshelve` / `revert` / `change -d` (those are the next tasks) — that's fine. As long as the failure point has moved past CreatePendingCL, this task is done.

- [ ] **Step 6: Commit**

```bash
git add internal/agent/source/perforce/client.go internal/agent/source/perforce/perforce.go internal/agent/source/perforce/perforce_test.go
git commit -m "fix(perforce): pass -c <client> on CreatePendingCL"
```

---

## Task 5: Wire `(cwd, client)` into `Unshelve`

**Files:**
- Modify: `internal/agent/source/perforce/perforce_test.go` (`TestProvider_UnshelveAndFinalizeRevert`)
- Modify: `internal/agent/source/perforce/client.go` (`Unshelve`)
- Modify: `internal/agent/source/perforce/perforce.go` (`Provider.Prepare` Unshelve call)

- [ ] **Step 1: Update the unshelve fixture key**

In `TestProvider_UnshelveAndFinalizeRevert`, change:

```go
fr.set("unshelve -s 12346 -c 91244", "//s/x/foo - unshelved\n")
```

to:

```go
fr.set("-c "+expectedClient+" unshelve -s 12346 -c 91244", "//s/x/foo - unshelved\n")
```

Also update the corresponding `found(...)` assertion at the end of the test — change:

```go
require.True(t, found([]string{"unshelve", "-s", "12346", "-c", "91244"}))
```

to:

```go
require.True(t, found([]string{"-c", expectedClient, "unshelve", "-s", "12346", "-c", "91244"}))
```

- [ ] **Step 2: Run the test, expect failure on the unshelve step**

Run:
```bash
go test ./internal/agent/source/perforce/ -run TestProvider_UnshelveAndFinalizeRevert -v -timeout 30s
```

Expected: FAIL at unshelve.

- [ ] **Step 3: Update `Client.Unshelve`**

In `client.go`, replace `Unshelve` (current lines 144-151):

```go
// Unshelve unshelves files from sourceCL into targetCL on the named client.
func (c *Client) Unshelve(ctx context.Context, cwd, client string, sourceCL, targetCL int64) error {
	_, err := c.r.Run(ctx, cwd, []string{
		"-c", client,
		"unshelve",
		"-s", strconv.FormatInt(sourceCL, 10),
		"-c", strconv.FormatInt(targetCL, 10),
	}, nil)
	return err
}
```

- [ ] **Step 4: Update `Provider.Prepare` caller**

In `perforce.go`, replace the Unshelve call (current line 221):

```go
if err := p.cfg.Client.Unshelve(ctx, src, cl); err != nil {
```

with:

```go
if err := p.cfg.Client.Unshelve(ctx, wsRoot, clientName, src, cl); err != nil {
```

- [ ] **Step 5: Run the test**

Run:
```bash
go test ./internal/agent/source/perforce/ -run TestProvider_UnshelveAndFinalizeRevert -v -timeout 30s
```

Expected: failure point moves past unshelve to the next un-prefixed call (revert). That's the cue Task 6 is next.

- [ ] **Step 6: Commit**

```bash
git add internal/agent/source/perforce/client.go internal/agent/source/perforce/perforce.go internal/agent/source/perforce/perforce_test.go
git commit -m "fix(perforce): pass -c <client> on Unshelve"
```

---

## Task 6: Wire `(cwd, client)` into `RevertCL` (and update `recoverOrphanedCLs` signature)

`RevertCL` is called from two production sites: `perforceHandle.Finalize` and `Provider.recoverOrphanedCLs`. This task also widens `recoverOrphanedCLs` to take `wsRoot` (used here and in Tasks 7-8).

**Files:**
- Modify: `internal/agent/source/perforce/perforce_test.go` (`TestProvider_UnshelveAndFinalizeRevert`, `TestProvider_CrashRecovery_DeletesOrphanedPendingCLs`)
- Modify: `internal/agent/source/perforce/client.go` (`RevertCL`)
- Modify: `internal/agent/source/perforce/perforce.go` (`recoverOrphanedCLs`, `Provider.Prepare` callsite, `perforceHandle.Finalize`)

- [ ] **Step 1: Update revert fixture keys in both tests**

In `TestProvider_UnshelveAndFinalizeRevert`, change:

```go
fr.set("revert -c 91244 //...", "//s/x/foo - reverted\n")
```

to:

```go
fr.set("-c "+expectedClient+" revert -c 91244 //...", "//s/x/foo - reverted\n")
```

And update its assertion:

```go
require.True(t, found([]string{"revert", "-c", "91244", "//..."}))
```

becomes:

```go
require.True(t, found([]string{"-c", expectedClient, "revert", "-c", "91244", "//..."}))
```

In `TestProvider_CrashRecovery_DeletesOrphanedPendingCLs`, the existing client name is already computed as `clientName := fmt.Sprintf("relay_h_%s", shortID)` (line 112). Change:

```go
fr.set("revert -c 91244 //...", "//... - reverted\n")
```

to:

```go
fr.set("-c "+clientName+" revert -c 91244 //...", "//... - reverted\n")
```

And update the assertion:

```go
require.True(t, found([]string{"revert", "-c", "91244", "//..."}))
```

becomes:

```go
require.True(t, found([]string{"-c", clientName, "revert", "-c", "91244", "//..."}))
```

- [ ] **Step 2: Run both tests, expect failures on revert**

Run:
```bash
go test ./internal/agent/source/perforce/ -run "TestProvider_UnshelveAndFinalizeRevert|TestProvider_CrashRecovery_DeletesOrphanedPendingCLs" -v -timeout 30s
```

Expected: FAIL at revert.

- [ ] **Step 3: Update `Client.RevertCL`**

In `client.go`, replace `RevertCL` (current lines 153-157):

```go
// RevertCL reverts all files in the given pending CL on the named client.
func (c *Client) RevertCL(ctx context.Context, cwd, client string, cl int64) error {
	_, err := c.r.Run(ctx, cwd, []string{
		"-c", client, "revert", "-c", strconv.FormatInt(cl, 10), "//...",
	}, nil)
	return err
}
```

- [ ] **Step 4: Update `recoverOrphanedCLs` signature and body**

In `perforce.go`, replace `recoverOrphanedCLs` (current lines 292-306):

```go
func (p *Provider) recoverOrphanedCLs(ctx context.Context, wsRoot, clientName string) error {
	cls, err := p.cfg.Client.PendingChangesByDescPrefix(ctx, clientName, "relay-task-")
	if err != nil {
		return err
	}
	for _, cl := range cls {
		if err := p.cfg.Client.RevertCL(ctx, wsRoot, clientName, cl); err != nil {
			return fmt.Errorf("revert orphan CL %d: %w", cl, err)
		}
		if err := p.cfg.Client.DeleteCL(ctx, cl); err != nil {
			return fmt.Errorf("delete orphan CL %d: %w", cl, err)
		}
	}
	return nil
}
```

(`PendingChangesByDescPrefix` and `DeleteCL` retain their old signatures here; Tasks 7 and 8 will update them.)

- [ ] **Step 5: Update the `recoverOrphanedCLs` callsite in `Provider.Prepare`**

In `perforce.go`, replace the call (current line 188):

```go
if err := p.recoverOrphanedCLs(ctx, clientName); err != nil {
```

with:

```go
if err := p.recoverOrphanedCLs(ctx, wsRoot, clientName); err != nil {
```

- [ ] **Step 6: Update `perforceHandle.Finalize`**

In `perforce.go`, replace the RevertCL line in Finalize (current line 340):

```go
revertErr := h.provider.cfg.Client.RevertCL(ctx, h.pendingCL)
```

with:

```go
revertErr := h.provider.cfg.Client.RevertCL(ctx, h.workspaceDir, h.clientName, h.pendingCL)
```

- [ ] **Step 7: Run both tests**

Run:
```bash
go test ./internal/agent/source/perforce/ -run "TestProvider_UnshelveAndFinalizeRevert|TestProvider_CrashRecovery_DeletesOrphanedPendingCLs" -v -timeout 30s
```

Expected: failures move past revert to the `change -d` step.

- [ ] **Step 8: Commit**

```bash
git add internal/agent/source/perforce/client.go internal/agent/source/perforce/perforce.go internal/agent/source/perforce/perforce_test.go
git commit -m "fix(perforce): pass -c <client> on RevertCL; widen recoverOrphanedCLs"
```

---

## Task 7: Wire `(cwd, client)` into `DeleteCL`

**Files:**
- Modify: `internal/agent/source/perforce/perforce_test.go` (`TestProvider_UnshelveAndFinalizeRevert`, `TestProvider_CrashRecovery_DeletesOrphanedPendingCLs`)
- Modify: `internal/agent/source/perforce/client.go` (`DeleteCL`)
- Modify: `internal/agent/source/perforce/perforce.go` (`recoverOrphanedCLs`, `perforceHandle.Finalize`)

- [ ] **Step 1: Update `change -d` fixture keys in both tests**

In `TestProvider_UnshelveAndFinalizeRevert`, change:

```go
fr.set("change -d 91244", "Change 91244 deleted.\n")
```

to:

```go
fr.set("-c "+expectedClient+" change -d 91244", "Change 91244 deleted.\n")
```

And update the assertion:

```go
require.True(t, found([]string{"change", "-d", "91244"}))
```

becomes:

```go
require.True(t, found([]string{"-c", expectedClient, "change", "-d", "91244"}))
```

In `TestProvider_CrashRecovery_DeletesOrphanedPendingCLs`, change:

```go
fr.set("change -d 91244", "Change 91244 deleted.\n")
```

to:

```go
fr.set("-c "+clientName+" change -d 91244", "Change 91244 deleted.\n")
```

And update both assertions:

```go
require.True(t, found([]string{"change", "-d", "91244"}))
require.False(t, found([]string{"change", "-d", "99999"}))
```

become:

```go
require.True(t, found([]string{"-c", clientName, "change", "-d", "91244"}))
require.False(t, found([]string{"-c", clientName, "change", "-d", "99999"}))
```

(The negative assertion ensures the orphan-recovery path didn't accidentally touch a non-relay-owned CL; with the global `-c` flag included, it stays an accurate negative.)

- [ ] **Step 2: Run both tests, expect failures on `change -d`**

Run:
```bash
go test ./internal/agent/source/perforce/ -run "TestProvider_UnshelveAndFinalizeRevert|TestProvider_CrashRecovery_DeletesOrphanedPendingCLs" -v -timeout 30s
```

Expected: FAIL at `change -d`.

- [ ] **Step 3: Update `Client.DeleteCL`**

In `client.go`, replace `DeleteCL` (current lines 159-163):

```go
// DeleteCL deletes an empty pending CL on the named client.
func (c *Client) DeleteCL(ctx context.Context, cwd, client string, cl int64) error {
	_, err := c.r.Run(ctx, cwd, []string{
		"-c", client, "change", "-d", strconv.FormatInt(cl, 10),
	}, nil)
	return err
}
```

- [ ] **Step 4: Update `recoverOrphanedCLs` body**

In `perforce.go`, inside `recoverOrphanedCLs` change:

```go
if err := p.cfg.Client.DeleteCL(ctx, cl); err != nil {
```

to:

```go
if err := p.cfg.Client.DeleteCL(ctx, wsRoot, clientName, cl); err != nil {
```

- [ ] **Step 5: Update `perforceHandle.Finalize`**

In `perforce.go`, replace the DeleteCL line in Finalize (current line 341):

```go
delErr := h.provider.cfg.Client.DeleteCL(ctx, h.pendingCL)
```

with:

```go
delErr := h.provider.cfg.Client.DeleteCL(ctx, h.workspaceDir, h.clientName, h.pendingCL)
```

- [ ] **Step 6: Run both tests**

Run:
```bash
go test ./internal/agent/source/perforce/ -run "TestProvider_UnshelveAndFinalizeRevert|TestProvider_CrashRecovery_DeletesOrphanedPendingCLs" -v -timeout 30s
```

Expected: `TestProvider_UnshelveAndFinalizeRevert` PASSES (it doesn't exercise `PendingChangesByDescPrefix`). `TestProvider_CrashRecovery_DeletesOrphanedPendingCLs` may still fail at the `changes -c <client> -s pending -l` step — that's Task 8.

- [ ] **Step 7: Commit**

```bash
git add internal/agent/source/perforce/client.go internal/agent/source/perforce/perforce.go internal/agent/source/perforce/perforce_test.go
git commit -m "fix(perforce): pass -c <client> on DeleteCL"
```

---

## Task 8: Wire `(cwd, client)` global into `PendingChangesByDescPrefix`

The method already takes a `client` argument used as the `changes` *subcommand* `-c` filter. Now also emit the *global* `-c` flag so the invocation runs in that client's context. Both occurrences of `-c` are required and mean different things.

**Files:**
- Modify: `internal/agent/source/perforce/perforce_test.go` (`TestProvider_CrashRecovery_DeletesOrphanedPendingCLs`)
- Modify: `internal/agent/source/perforce/client.go` (`PendingChangesByDescPrefix`)
- Modify: `internal/agent/source/perforce/perforce.go` (`recoverOrphanedCLs`)

- [ ] **Step 1: Update the `changes -c …` fixture key**

In `TestProvider_CrashRecovery_DeletesOrphanedPendingCLs`, change:

```go
fr.set("changes -c "+clientName+" -s pending -l",
	"Change 91244 on 2026-04-24 by relay@h *pending*\n\trelay-task-old\n\nChange 99999 on 2026-04-24 by other@h *pending*\n\thuman work\n")
```

to:

```go
fr.set("-c "+clientName+" changes -c "+clientName+" -s pending -l",
	"Change 91244 on 2026-04-24 by relay@h *pending*\n\trelay-task-old\n\nChange 99999 on 2026-04-24 by other@h *pending*\n\thuman work\n")
```

- [ ] **Step 2: Run the test, expect failure at the changes-listing step**

Run:
```bash
go test ./internal/agent/source/perforce/ -run TestProvider_CrashRecovery_DeletesOrphanedPendingCLs -v -timeout 30s
```

Expected: FAIL — production still emits `changes -c <client> -s pending -l` without the leading global `-c`, so the fakeRunner returns empty for the prefixed key. With no orphans found, `revert`/`change -d` never run and the assertions about them fail.

- [ ] **Step 3: Update `Client.PendingChangesByDescPrefix`**

In `client.go`, replace the function (current lines 165-190). Note: the parameter currently named `client` shadows the package-level concept; rename to keep clarity. Both `cwd` (new) and the global `-c` flag get added:

```go
// PendingChangesByDescPrefix returns relay-owned pending CLs on the named
// client whose description starts with the given prefix. The global -c flag
// scopes the invocation; the inner `changes -c <client>` is the subcommand's
// "filter by client" option — both are required.
func (c *Client) PendingChangesByDescPrefix(ctx context.Context, cwd, client, prefix string) ([]int64, error) {
	out, err := c.r.Run(ctx, cwd, []string{
		"-c", client,
		"changes", "-c", client, "-s", "pending", "-l",
	}, nil)
	if err != nil {
		return nil, err
	}
	var cls []int64
	var current int64
	var inDesc bool
	for _, line := range strings.Split(string(out), "\n") {
		if m := changeFirstLine.FindStringSubmatch(line); m != nil {
			current, _ = strconv.ParseInt(m[1], 10, 64)
			inDesc = true
			continue
		}
		if inDesc && strings.TrimSpace(line) != "" && current != 0 {
			if strings.HasPrefix(strings.TrimSpace(line), prefix) {
				cls = append(cls, current)
			}
			inDesc = false
			current = 0
		}
	}
	return cls, nil
}
```

- [ ] **Step 4: Update `recoverOrphanedCLs` body**

In `perforce.go`, inside `recoverOrphanedCLs`, change:

```go
cls, err := p.cfg.Client.PendingChangesByDescPrefix(ctx, clientName, "relay-task-")
```

to:

```go
cls, err := p.cfg.Client.PendingChangesByDescPrefix(ctx, wsRoot, clientName, "relay-task-")
```

- [ ] **Step 5: Run the full perforce unit suite**

Run:
```bash
go test ./internal/agent/source/perforce/... -v -timeout 30s
```

Expected: ALL tests PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/agent/source/perforce/client.go internal/agent/source/perforce/perforce.go internal/agent/source/perforce/perforce_test.go
git commit -m "fix(perforce): pass -c <client> on PendingChangesByDescPrefix"
```

---

## Task 9: Remove the misleading comment and the integration-test workaround

Cosmetic + integration cleanup. With the production contract now closed, both the `client.go` warning comment and the integration-test setenv workaround are obsolete.

**Files:**
- Modify: `internal/agent/source/perforce/client.go`
- Modify: `internal/agent/source/perforce/perforce_integration_test.go`

- [ ] **Step 1: Remove the misleading comment from `SyncStream`'s docstring**

In `client.go`, the SyncStream docstring (rewritten in Task 3) should already not include "Caller is responsible for setting P4CLIENT". Verify, and if any stale variant of that line survives anywhere in the file, remove it. The expected doc is:

```go
// SyncStream runs `p4 -c <client> sync -q --parallel=4 <specs...>` from cwd,
// streaming lines to onLine.
```

- [ ] **Step 2: Remove the `P4CLIENT` setenv workaround from the integration test**

In `perforce_integration_test.go`, delete lines 40-46:

```go
	// The agent creates a stream-bound client named relay_<hostname>_<shortid>
	// where shortid = first 6 chars of lowercase base32(sha256(stream)). Compute
	// the same value here and inject it as P4CLIENT so the agent's `p4 sync`
	// (which the production code currently relies on env to provide; see
	// client.go's "Caller is responsible for setting P4CLIENT" comment) finds
	// the right client.
	t.Setenv("P4CLIENT", expectedClientName("ci", "//test/main"))
```

The `t.Setenv` calls for `P4CHARSET`, `P4CONFIG`, `P4PASSWD`, `P4TICKETS` (lines 32-39) **stay** — they are defense-in-depth against operator-host pollution, not workarounds for this bug.

- [ ] **Step 3: Run the integration test**

Run:
```bash
go test -tags integration -p 1 ./internal/agent/source/perforce/ -run TestPerforce_E2E_SyncAndUnshelve -v -timeout 5m
```

Expected: PASS. With the production code now passing `-c <client>` on every workspace-scoped invocation, the test no longer needs the `P4CLIENT` env var to bridge the gap.

If the test fails, the failure mode is informative: the agent's `p4 sync` will report `Client '<name>' unknown` or "no client specified", which means a call site was missed in Tasks 3-8. Find it via the failing argv in test output and ensure it's threading `-c <client>`.

- [ ] **Step 4: Commit**

```bash
git add internal/agent/source/perforce/client.go internal/agent/source/perforce/perforce_integration_test.go
git commit -m "refactor(perforce): drop P4CLIENT env workaround from integration test"
```

---

## Task 10: Final verification and backlog hygiene

**Files:**
- Move: `docs/backlog/bug-2026-05-01-p4client-env-var-dependency.md` → `docs/backlog/closed/`
- Modify: the moved file's frontmatter and body

- [ ] **Step 1: Run the full unit suite**

Run:
```bash
make test
```

Expected: PASS.

- [ ] **Step 2: Run the full integration suite**

Run:
```bash
make test-integration
```

Expected: PASS. (Requires Docker Desktop running and `p4` on PATH — same as the rest of the integration suite.)

- [ ] **Step 3: Move the backlog file**

Run:
```bash
mkdir -p docs/backlog/closed
git mv docs/backlog/bug-2026-05-01-p4client-env-var-dependency.md docs/backlog/closed/bug-2026-05-01-p4client-env-var-dependency.md
```

- [ ] **Step 4: Update frontmatter and append a Resolution section**

Edit `docs/backlog/closed/bug-2026-05-01-p4client-env-var-dependency.md`:

Change the frontmatter from:

```yaml
---
title: Production agent relies on env-var P4CLIENT but no caller sets it
type: bug
status: open
created: 2026-05-01
source: 2026-05-01 p4d-testcontainer Task 4 fix and final review
---
```

to:

```yaml
---
title: Production agent relies on env-var P4CLIENT but no caller sets it
type: bug
status: closed
created: 2026-05-01
closed: 2026-05-01
resolution: fixed
source: 2026-05-01 p4d-testcontainer Task 4 fix and final review
---
```

Append at the bottom of the file (after the `## Related` section):

```markdown

## Resolution
Closed by passing the global `p4 -c <client>` flag explicitly on every workspace-scoped invocation (`SyncStream`, `CreatePendingCL`, `Unshelve`, `RevertCL`, `DeleteCL`, `PendingChangesByDescPrefix`) and threading the workspace dir as cwd through the `Runner` interface. The `// Caller is responsible for setting P4CLIENT` comment in `client.go` was removed; the integration test's `t.Setenv("P4CLIENT", …)` workaround was removed and the test still passes. See `docs/superpowers/specs/2026-05-01-p4client-explicit-flag-design.md` and `docs/superpowers/plans/2026-05-01-p4client-explicit-flag.md`.
```

- [ ] **Step 5: Commit the backlog close**

```bash
git add docs/backlog/closed/bug-2026-05-01-p4client-env-var-dependency.md
git commit -m "backlog: close bug-2026-05-01-p4client-env-var-dependency"
```

- [ ] **Step 6: Final sanity check**

Run:
```bash
git log --oneline -15
make test
```

The recent log should show roughly: `backlog: close …`, `refactor(perforce): drop P4CLIENT env workaround …`, the six `fix(perforce): pass -c <client> on …` commits, the `refactor(perforce): add cwd to Runner interface (no-op)` commit, and the `refactor(perforce): make expectedClientName visible to unit tests` commit. The unit suite must pass.

---

## Self-Review Checklist (executed during plan authoring)

**Spec coverage:**
- Runner interface change → Task 2 ✓
- Six methods get `(cwd, client)` and emit `-c` → Tasks 3-8 ✓
- `recoverOrphanedCLs(ctx, wsRoot, clientName)` widening → Task 6 ✓
- `Provider.Prepare` callsite wiring → Tasks 3, 4, 5, 6 ✓
- `perforceHandle.Finalize` wiring → Tasks 6, 7 ✓
- `expectedClientName` promoted → Task 1 ✓
- Explicit argv-prefix assertion → Task 3 Step 1 ✓
- Integration test cleanup → Task 9 Step 2 ✓
- Comment removal at `client.go:117` → Task 3 Step 3 (rewrites the docstring) + Task 9 Step 1 (verification) ✓
- `make test` and `make test-integration` pass → Task 10 Steps 1-2 ✓
- Backlog item closed → Task 10 Steps 3-5 ✓

**Placeholder scan:** None. Every step contains exact code or exact commands.

**Type consistency:** `Runner` signatures used in Task 2 (`Run(ctx, cwd, args, stdin)`, `Stream(ctx, cwd, args, onLine)`) match what every later task passes. `Client` method signatures introduced in Tasks 3-8 match their callers in `Provider.Prepare`, `recoverOrphanedCLs`, and `Finalize`. `expectedClientName(hostname, sourceKey)` signature is identical between Task 1 (definition) and Tasks 3-7 (usage).
