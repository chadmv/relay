# p4d Testcontainer Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the env-var-driven skip in `internal/agent/source/perforce/perforce_integration_test.go` with a containerized `p4d` so `make test-integration` runs the full Perforce lifecycle test deterministically.

**Architecture:** A custom Dockerfile in `internal/agent/source/perforce/testdata/p4d/` builds an image that runs `p4d r25.2` and an entrypoint script that creates depot `//test`, stream `//test/main`, an initial submitted CL, and a deterministic shelved CL before signaling readiness via the log line `p4d ready`. A new test fixture (`p4d_container_test.go`, build tag `integration`) starts the container via testcontainers-go, reads the shelved CL number from the container, and returns connection params. The existing integration test loses its env-var-gated skip and uses the fixture directly.

**Tech Stack:** Go 1.26, testcontainers-go v0.42 (already a dep), Docker, Debian Bookworm Slim, Perforce r25.2 standalone Linux x86_64 binaries from `https://ftp.perforce.com/perforce/r25.2/bin.linux26x86_64/`.

**Spec:** [`docs/superpowers/specs/2026-05-01-p4d-testcontainer-design.md`](../specs/2026-05-01-p4d-testcontainer-design.md)

**Backlog item to close:** [`docs/backlog/bug-2026-04-25-no-p4d-testcontainer.md`](../../backlog/bug-2026-04-25-no-p4d-testcontainer.md)

---

## File map

**Created:**
- `internal/agent/source/perforce/testdata/p4d/Dockerfile` — builds the p4d image
- `internal/agent/source/perforce/testdata/p4d/entrypoint.sh` — depot/stream/shelf setup; emits the readiness log line
- `internal/agent/source/perforce/p4d_container_test.go` — Go test fixture that starts the container

**Modified:**
- `internal/agent/source/perforce/perforce_integration_test.go` — uses the fixture; drops env-var skips
- `CLAUDE.md` — updates `make test-integration` comment and the source-providers paragraph
- `README.md` — updates the integration-tests note to mention p4d and the `p4` CLI prerequisite

**Moved:**
- `docs/backlog/bug-2026-04-25-no-p4d-testcontainer.md` → `docs/backlog/closed/bug-2026-04-25-no-p4d-testcontainer.md` with a `Resolution` section appended.

No changes to `Makefile` (`make test-integration` already runs all integration tests via `-tags integration`).

---

### Task 1: Create the Dockerfile

**Files:**
- Create: `internal/agent/source/perforce/testdata/p4d/Dockerfile`

- [ ] **Step 1: Verify the testdata directory does not yet exist**

Run: `ls internal/agent/source/perforce/testdata 2>/dev/null || echo "not present"`
Expected: `not present` (or an empty listing if the directory has been touched in a prior step). The Dockerfile lives at `internal/agent/source/perforce/testdata/p4d/Dockerfile`.

- [ ] **Step 2: Create the Dockerfile**

Write the following to `internal/agent/source/perforce/testdata/p4d/Dockerfile`:

```dockerfile
# syntax=docker/dockerfile:1
FROM debian:bookworm-slim

ARG P4D_VERSION=r25.2
ARG P4_BIN_BASE=https://ftp.perforce.com/perforce/${P4D_VERSION}/bin.linux26x86_64

RUN apt-get update \
 && apt-get install -y --no-install-recommends curl ca-certificates bash \
 && rm -rf /var/lib/apt/lists/*

# Standalone p4d / p4 binaries are published as bare executables in this directory in r25.2.
RUN curl -fsSL -o /usr/local/bin/p4d "${P4_BIN_BASE}/p4d" \
 && curl -fsSL -o /usr/local/bin/p4  "${P4_BIN_BASE}/p4" \
 && chmod +x /usr/local/bin/p4d /usr/local/bin/p4

RUN useradd -m -d /home/perforce -s /bin/bash perforce \
 && mkdir -p /var/p4root \
 && chown perforce:perforce /var/p4root

COPY --chown=perforce:perforce entrypoint.sh /usr/local/bin/entrypoint.sh
RUN chmod +x /usr/local/bin/entrypoint.sh

USER perforce
WORKDIR /home/perforce
EXPOSE 1666

ENTRYPOINT ["/usr/local/bin/entrypoint.sh"]
```

- [ ] **Step 3: Commit**

```bash
git add internal/agent/source/perforce/testdata/p4d/Dockerfile
git commit -m "test(perforce): add p4d testcontainer Dockerfile"
```

The image won't build successfully yet — `entrypoint.sh` doesn't exist. That's intentional; Task 2 adds it.

---

### Task 2: Create the entrypoint script and smoke-test the image

**Files:**
- Create: `internal/agent/source/perforce/testdata/p4d/entrypoint.sh`

- [ ] **Step 1: Create the entrypoint script**

Write the following to `internal/agent/source/perforce/testdata/p4d/entrypoint.sh`. The script starts p4d in the background, waits for it to respond, sets `security=0`, creates a stream depot and mainline stream, populates an initial baseline file, creates a shelved CL holding a modified version of that file, writes the shelved CL number to `/var/p4root/shelved-cl.txt`, emits the readiness signal `p4d ready`, and finally `wait`s on the p4d background process so the container stays alive.

```bash
#!/usr/bin/env bash
# Entrypoint for the p4d test container.
# Runs as the unprivileged 'perforce' user (set in the Dockerfile via USER).
set -euo pipefail

P4ROOT=/var/p4root

echo "[entrypoint] starting p4d..."
p4d -d "$P4ROOT" -p 0.0.0.0:1666 &
P4D_PID=$!

# Talk to local p4d on loopback during setup.
export P4PORT=localhost:1666
export P4USER=perforce

echo "[entrypoint] waiting for p4d to respond..."
for _ in $(seq 1 30); do
  if p4 info >/dev/null 2>&1; then
    echo "[entrypoint] p4d responsive"
    break
  fi
  sleep 1
done
if ! p4 info >/dev/null 2>&1; then
  echo "[entrypoint] FATAL: p4d did not respond within 30s" >&2
  exit 1
fi

echo "[entrypoint] disabling auth (security=0)..."
p4 configure set security=0 >/dev/null

echo "[entrypoint] creating depot //test ..."
p4 depot -i <<'EOF'
Depot:    test
Owner:    perforce
Type:     stream
Map:      test/...
EOF

echo "[entrypoint] creating stream //test/main ..."
p4 stream -i <<'EOF'
Stream:    //test/main
Owner:     perforce
Name:      main
Parent:    none
Type:      mainline
Paths:     share ...
EOF

WORKDIR=$(mktemp -d)
echo "[entrypoint] creating setup client rooted at ${WORKDIR} ..."
p4 client -i <<EOF
Client:   setup-client
Owner:    perforce
Root:     ${WORKDIR}
Stream:   //test/main
EOF
export P4CLIENT=setup-client

echo "[entrypoint] populating //test/main with baseline file ..."
echo "baseline" > "${WORKDIR}/readme.txt"
p4 add "${WORKDIR}/readme.txt"
p4 submit -d "init"

echo "[entrypoint] creating shelved CL ..."
SHELVED_CL=$(p4 --field "Description=relay-test-shelf" change -o | p4 change -i | awk '{print $2}')
p4 edit -c "$SHELVED_CL" "${WORKDIR}/readme.txt"
echo "shelved-content" > "${WORKDIR}/readme.txt"
p4 shelve -c "$SHELVED_CL"
# Revert the local-side opens; the shelf remains on the server.
p4 revert -k "${WORKDIR}/readme.txt"

echo "$SHELVED_CL" > /var/p4root/shelved-cl.txt
echo "[entrypoint] shelved CL = $SHELVED_CL"

echo "p4d ready"

wait "$P4D_PID"
```

- [ ] **Step 2: Confirm the script is executable and has Unix line endings**

The Dockerfile's `RUN chmod +x` handles the executable bit at image-build time, so file-mode in git doesn't matter. Line endings, however, do — Bash will fail on CRLF. On Windows ensure the file is committed with LF endings:

Run: `file internal/agent/source/perforce/testdata/p4d/entrypoint.sh 2>/dev/null || true`
Expected on Linux/macOS: `... ASCII text` (or similar — *not* `... with CRLF line terminators`).
On Windows the `file` command is unavailable; instead verify with:
Run: `git check-attr text internal/agent/source/perforce/testdata/p4d/entrypoint.sh`
Expected: the path is reported. If a `.gitattributes` rule forces LF for `*.sh` already, no further action is needed. Otherwise add an entry to `.gitattributes` (or commit the file with `git add --renormalize`) so the script does not arrive in the image with `\r\n` line endings.

If `.gitattributes` does not exist or does not cover `*.sh`, append:

```
*.sh text eol=lf
```

to `.gitattributes` and re-add the script:

```bash
git add .gitattributes
git rm --cached internal/agent/source/perforce/testdata/p4d/entrypoint.sh
git add internal/agent/source/perforce/testdata/p4d/entrypoint.sh
```

- [ ] **Step 3: Build the image manually as a smoke test**

Run:

```bash
docker build -t relay-p4d-test:smoke internal/agent/source/perforce/testdata/p4d
```

Expected: the build completes successfully. Failure here is most likely a `curl` 404 (Perforce moved or removed the binary), a missing dep in the script, or a typo. Investigate and fix in this step before moving on; don't proceed until the image builds.

- [ ] **Step 4: Run the image manually as a smoke test**

Run:

```bash
docker run --rm --name relay-p4d-test-smoke -p 16666:1666 -d relay-p4d-test:smoke
```

Then poll its logs:

```bash
docker logs -f relay-p4d-test-smoke
```

Expected: within ~30 seconds you see:

```
[entrypoint] starting p4d...
[entrypoint] waiting for p4d to respond...
[entrypoint] p4d responsive
[entrypoint] disabling auth (security=0)...
[entrypoint] creating depot //test ...
[entrypoint] creating stream //test/main ...
[entrypoint] creating setup client rooted at /tmp/...
[entrypoint] populating //test/main with baseline file ...
... (p4 submit output) ...
[entrypoint] creating shelved CL ...
... (p4 shelve output) ...
[entrypoint] shelved CL = N
p4d ready
```

(Press Ctrl-C to stop following logs; the container keeps running.)

If `p4` is installed on your host, verify connectivity:

```bash
P4PORT=localhost:16666 P4USER=perforce p4 info
```

Expected: a populated `p4 info` response naming the server. If `p4` is not installed locally, skip this verification — the Go fixture will exercise it.

Read the captured shelved CL:

```bash
docker exec relay-p4d-test-smoke cat /var/p4root/shelved-cl.txt
```

Expected: an integer (e.g. `2`), terminated by a newline.

- [ ] **Step 5: Tear down the smoke test container**

Run:

```bash
docker stop relay-p4d-test-smoke
```

(`--rm` on `docker run` removes the container after it stops.)

- [ ] **Step 6: Commit**

```bash
git add internal/agent/source/perforce/testdata/p4d/entrypoint.sh
# also stage .gitattributes if it was created/edited in Step 2
git status --short
git commit -m "test(perforce): add p4d testcontainer entrypoint script"
```

---

### Task 3: Add the Go test fixture

**Files:**
- Create: `internal/agent/source/perforce/p4d_container_test.go`

This is the helper that the integration test calls. It pre-flights `p4` CLI availability (skip if missing), starts the container (skip if Docker is unavailable, fail otherwise), waits for the `p4d ready` log line, reads the shelved CL number out of the container, and returns the connection params. Cleanup is registered with `t.Cleanup`.

- [ ] **Step 1: Write the fixture**

Write the following to `internal/agent/source/perforce/p4d_container_test.go`:

```go
//go:build integration

package perforce

import (
	"context"
	"io"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

// p4dHandle holds the connection parameters for a running p4d test
// container. P4User is always "perforce" (the only account the entrypoint
// creates); P4Port is host:port reachable from the test process;
// ShelvedCL is the changelist number the entrypoint shelved during setup.
type p4dHandle struct {
	P4Port    string
	P4User    string
	ShelvedCL int64
}

// startP4dContainer builds and starts the p4d test image, waits for it to
// be ready, and returns connection parameters. The container is terminated
// via t.Cleanup.
//
// Skips (does not fail) the test when:
//   - the `p4` client binary is not on PATH (the test process needs it because
//     the agent code under test shells out to `p4` via os/exec)
//   - Docker is not reachable on the host
//
// All other errors fail the test.
func startP4dContainer(t *testing.T) p4dHandle {
	t.Helper()

	if _, err := exec.LookPath("p4"); err != nil {
		t.Skip("p4 client binary required on PATH")
	}

	ctx := context.Background()

	req := testcontainers.ContainerRequest{
		FromDockerfile: testcontainers.FromDockerfile{
			Context: "testdata/p4d",
		},
		ExposedPorts: []string{"1666/tcp"},
		WaitingFor:   wait.ForLog("p4d ready").WithStartupTimeout(2 * time.Minute),
	}
	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		if isDockerUnavailable(err) {
			t.Skipf("Docker required: %v", err)
		}
		t.Fatalf("p4d container start failed: %v", err)
	}
	t.Cleanup(func() {
		_ = container.Terminate(context.Background())
	})

	host, err := container.Host(ctx)
	require.NoError(t, err, "container.Host")
	port, err := container.MappedPort(ctx, "1666/tcp")
	require.NoError(t, err, "container.MappedPort")

	return p4dHandle{
		P4Port:    host + ":" + port.Port(),
		P4User:    "perforce",
		ShelvedCL: readShelvedCL(t, ctx, container),
	}
}

// readShelvedCL reads /var/p4root/shelved-cl.txt out of the container and
// parses it as an int64.
func readShelvedCL(t *testing.T, ctx context.Context, container testcontainers.Container) int64 {
	t.Helper()
	rc, err := container.CopyFileFromContainer(ctx, "/var/p4root/shelved-cl.txt")
	require.NoError(t, err, "CopyFileFromContainer shelved-cl.txt")
	defer rc.Close()
	data, err := io.ReadAll(rc)
	require.NoError(t, err, "read shelved-cl.txt")
	cl, err := strconv.ParseInt(strings.TrimSpace(string(data)), 10, 64)
	require.NoError(t, err, "parse shelved CL")
	return cl
}

// isDockerUnavailable inspects an error from testcontainers-go to decide
// whether it indicates that Docker is unreachable on this host (legitimate
// skip) versus a hard test failure. testcontainers-go does not expose a
// typed sentinel for this, so we string-match the most common cases.
func isDockerUnavailable(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	for _, needle := range []string{
		"cannot connect to the docker daemon",
		"docker daemon",
		"docker socket",
		"connection refused",
		"docker: not found",
		"executable file not found",
	} {
		if strings.Contains(msg, needle) {
			return true
		}
	}
	return false
}
```

- [ ] **Step 2: Build the integration package to confirm the fixture compiles**

Run:

```bash
go build -tags integration ./internal/agent/source/perforce/...
```

Expected: success (no output).

If the build fails, the most likely causes are import-path or testcontainers-go API mismatches. Fix and re-run.

- [ ] **Step 3: Commit**

```bash
git add internal/agent/source/perforce/p4d_container_test.go
git commit -m "test(perforce): add p4d testcontainer fixture"
```

---

### Task 4: Rewrite the integration test to use the fixture

**Files:**
- Modify: `internal/agent/source/perforce/perforce_integration_test.go` (rewrite the top portion; assertions below the `ctx` block stay the same)

- [ ] **Step 1: Replace the file with the fixture-based version**

Overwrite `internal/agent/source/perforce/perforce_integration_test.go` with the following. The lifecycle and assertions below the `ctx` block are byte-for-byte the same as the original; only the setup at the top changes.

```go
//go:build integration

package perforce

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	relayv1 "relay/internal/proto/relayv1"
)

// TestPerforce_E2E_SyncAndUnshelve exercises the full Provider.Prepare → Finalize
// lifecycle against a containerized p4d. The container is provisioned by
// startP4dContainer (see p4d_container_test.go); it pre-creates depot //test,
// stream //test/main, an initial baseline file, and a shelved CL.
//
// The test skips cleanly when Docker is unavailable or when the `p4` client
// binary is not on PATH; both are pre-flighted by the fixture.
func TestPerforce_E2E_SyncAndUnshelve(t *testing.T) {
	p4d := startP4dContainer(t)
	t.Setenv("P4PORT", p4d.P4Port)
	t.Setenv("P4USER", p4d.P4User)

	root := t.TempDir()
	prov := New(Config{Root: root, Hostname: "ci"})

	spec := &relayv1.SourceSpec{Provider: &relayv1.SourceSpec_Perforce{
		Perforce: &relayv1.PerforceSource{
			Stream:    "//test/main",
			Sync:      []*relayv1.SyncEntry{{Path: "//test/main/...", Rev: "#head"}},
			Unshelves: []int64{p4d.ShelvedCL},
		},
	}}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	// --- First prepare: creates workspace, syncs to head, unshelves the CL ---
	var progressLines []string
	h, err := prov.Prepare(ctx, "task-1", spec, func(s string) {
		progressLines = append(progressLines, s)
	})
	require.NoError(t, err, "Prepare should succeed")
	t.Cleanup(func() { _ = h.Finalize(context.Background()) })
	require.NotEmpty(t, progressLines, "sync should produce progress lines")

	inv := h.Inventory()
	require.Equal(t, "perforce", inv.SourceType)
	require.Equal(t, "//test/main", inv.SourceKey)
	require.NotEmpty(t, inv.ShortID, "ShortID must be set")
	require.NotEmpty(t, inv.BaselineHash, "BaselineHash must be set after sync")

	// Workspace directory must exist on disk.
	wsDir := filepath.Join(root, inv.ShortID)
	_, err = os.Stat(wsDir)
	require.NoError(t, err, "workspace directory should exist")

	// Registry should show no open task changelists after Finalize.
	reg, err := LoadRegistry(filepath.Join(root, ".relay-registry.json"))
	require.NoError(t, err)
	e := reg.Get(inv.ShortID)
	require.NotNil(t, e, "workspace entry should remain in registry after finalize")
	require.Empty(t, e.OpenTaskChangelists, "Finalize should clear pending changelists")

	// --- Second prepare: same spec → should not re-sync (baseline matches) ---
	var progress2 []string
	h2, err := prov.Prepare(ctx, "task-2", spec, func(s string) {
		progress2 = append(progress2, s)
	})
	require.NoError(t, err, "second Prepare on same baseline should succeed")
	t.Cleanup(func() { _ = h2.Finalize(context.Background()) })
	require.Empty(t, progress2, "second Prepare with same baseline should not trigger re-sync")

	// Workspace dir must still exist after second finalize.
	_, err = os.Stat(wsDir)
	require.NoError(t, err, "workspace directory should persist after second finalize")
}
```

Notes for the implementer:
- `os/exec`, `strconv`, and `strings` are no longer used by this file — the fixture handles those concerns. They MUST NOT be re-added; `goimports` or `go vet` would flag them.
- `Unshelves` is now unconditionally populated from `p4d.ShelvedCL`; the previous `P4_TEST_SHELVED_CL` opt-in is gone.
- The doc comment is rewritten to describe the new behavior — no env vars listed.

- [ ] **Step 2: Build to confirm the test compiles**

Run:

```bash
go build -tags integration ./internal/agent/source/perforce/...
```

Expected: success.

- [ ] **Step 3: Run the integration test end-to-end**

Run:

```bash
go test -tags integration -p 1 ./internal/agent/source/perforce/... -run TestPerforce_E2E_SyncAndUnshelve -v -timeout 600s
```

Expected: `--- PASS: TestPerforce_E2E_SyncAndUnshelve` after roughly 30s–4min depending on whether the image is already cached. The first run will take longest (downloads p4d binaries during image build).

If the test fails, the most likely causes are:
- `p4d ready` log line not appearing within the 2-minute startup timeout — inspect `docker ps -a` and `docker logs <id>` for the failed container.
- `BaselineHash` empty after sync — the `//test/main/...` content from the entrypoint may not have been submitted; re-verify Task 2 Step 4 manually.
- Unshelve fails — the `Unshelves` value disagrees with what the container shelved. Re-read `/var/p4root/shelved-cl.txt` from the container.

- [ ] **Step 4: Verify the test skips cleanly when `p4` is missing on PATH**

This is a behavioral check, not a regression. On a host without `p4` on PATH (or with PATH temporarily scrubbed), the test should report `--- SKIP: TestPerforce_E2E_SyncAndUnshelve` with the message `p4 client binary required on PATH`. To simulate without uninstalling `p4`:

```bash
PATH=/usr/bin:/bin go test -tags integration -p 1 ./internal/agent/source/perforce/... -run TestPerforce_E2E_SyncAndUnshelve -v
```

(Adjust `PATH` as needed to exclude `p4`.)

Expected: `--- SKIP: TestPerforce_E2E_SyncAndUnshelve (...)` with the skip reason printed.

If `p4` is installed system-wide and cannot be excluded by PATH manipulation, this verification is informational only — the skip path is exercised by code review.

- [ ] **Step 5: Commit**

```bash
git add internal/agent/source/perforce/perforce_integration_test.go
git commit -m "test(perforce): use p4d testcontainer instead of external server"
```

---

### Task 5: Update CLAUDE.md and README.md

**Files:**
- Modify: `CLAUDE.md` (two locations)
- Modify: `README.md` (one location)

- [ ] **Step 1: Update the `make test-integration` comment in `CLAUDE.md`**

Locate the existing line in the `## Commands` block (around line 14):

```
# Integration tests (requires Docker Desktop running; -p 1 prevents parallel container conflicts)
make test-integration
```

Replace the comment with:

```
# Integration tests (requires Docker Desktop running and the `p4` CLI on PATH;
# spins up Postgres and p4d containers; -p 1 prevents parallel container conflicts)
make test-integration
```

- [ ] **Step 2: Update the source-providers paragraph in `CLAUDE.md`**

Locate the existing line (around line 63):

```
**Source providers:** Relay assumes `p4` is installed and a valid P4 ticket is active on the agent host. Provision P4 tickets out-of-band (e.g. via `p4 login` in your system startup). Relay does not manage P4 credentials.
```

Append a sentence to the same paragraph (no new heading; same line break behavior as the rest of the doc):

```
**Source providers:** Relay assumes `p4` is installed and a valid P4 ticket is active on the agent host. Provision P4 tickets out-of-band (e.g. via `p4 login` in your system startup). Relay does not manage P4 credentials. The Perforce integration test (`perforce_integration_test.go`) spins up a `p4d` container via testcontainers-go; it requires Docker and the `p4` CLI on PATH but no external Perforce server.
```

- [ ] **Step 3: Update the integration-tests paragraph in `README.md`**

Locate the blockquote near the bottom of the `### Run tests` section (around line 992):

```
> Integration tests use [testcontainers-go](https://golang.testcontainers.org/) to spin up a real PostgreSQL container per test. Docker Desktop must be running. The `-p 1` flag is required on Windows to prevent container provider conflicts when multiple packages run in parallel.
```

Replace it with:

```
> Integration tests use [testcontainers-go](https://golang.testcontainers.org/) to spin up real PostgreSQL and p4d containers per test. Docker Desktop must be running, and the `p4` CLI must be on PATH (the Perforce test fixture shells out to it). The `-p 1` flag is required on Windows to prevent container provider conflicts when multiple packages run in parallel.
```

- [ ] **Step 4: Spot-check the diffs**

Run:

```bash
git diff CLAUDE.md README.md
```

Expected: three localized changes — two in `CLAUDE.md`, one in `README.md`. No collateral edits, no whitespace-only churn.

- [ ] **Step 5: Commit**

```bash
git add CLAUDE.md README.md
git commit -m "docs: note p4d testcontainer in test instructions"
```

---

### Task 6: Close the backlog item

**Files:**
- Move: `docs/backlog/bug-2026-04-25-no-p4d-testcontainer.md` → `docs/backlog/closed/bug-2026-04-25-no-p4d-testcontainer.md` (with frontmatter and body updates)

- [ ] **Step 1: Move the file**

Run:

```bash
mkdir -p docs/backlog/closed
git mv docs/backlog/bug-2026-04-25-no-p4d-testcontainer.md docs/backlog/closed/bug-2026-04-25-no-p4d-testcontainer.md
```

- [ ] **Step 2: Update the frontmatter and append a Resolution section**

Edit `docs/backlog/closed/bug-2026-04-25-no-p4d-testcontainer.md`. Change:

```markdown
---
title: Integration test only runs against an existing P4 server
type: bug
status: open
created: 2026-04-25
source: 2026-04-25 perforce-workspace-management retro — Known Limitations
---
```

to:

```markdown
---
title: Integration test only runs against an existing P4 server
type: bug
status: closed
created: 2026-04-25
closed: 2026-05-01
resolution: fixed
source: 2026-04-25 perforce-workspace-management retro — Known Limitations
---
```

Then append the following section at the end of the file (after the existing `## Summary`):

```markdown

## Resolution
Replaced env-var-driven skip with a containerized p4d (Dockerfile + entrypoint script under `internal/agent/source/perforce/testdata/p4d/`). Test fixture in `p4d_container_test.go` starts the container via testcontainers-go, waits for the `p4d ready` log line, and reads the deterministic shelved CL the entrypoint creates. `make test-integration` now exercises the full Perforce sync + unshelve lifecycle on any host with Docker and the `p4` CLI installed.
```

- [ ] **Step 3: Verify the test still passes after backlog housekeeping**

Run:

```bash
go test -tags integration -p 1 ./internal/agent/source/perforce/... -run TestPerforce_E2E_SyncAndUnshelve -v -timeout 300s
```

Expected: PASS. (The backlog move shouldn't affect the test, but a quick sanity check before committing closes the loop.)

- [ ] **Step 4: Commit**

```bash
git add docs/backlog/closed/bug-2026-04-25-no-p4d-testcontainer.md
git commit -m "backlog: close bug-2026-04-25-no-p4d-testcontainer"
```

---

## Verification checklist

After all tasks:

- [ ] `go build -tags integration ./...` succeeds.
- [ ] `go test -tags integration -p 1 ./internal/agent/source/perforce/... -run TestPerforce_E2E_SyncAndUnshelve -v -timeout 600s` passes on a clean dev box with Docker running and `p4` CLI installed, with no `P4_TEST_*` env vars set.
- [ ] The test skips with `p4 client binary required on PATH` when `p4` is missing.
- [ ] The test skips with `Docker required: ...` when the Docker daemon is not reachable. (Optional manual verification by stopping Docker Desktop briefly.)
- [ ] The closed backlog file lives under `docs/backlog/closed/` with `status: closed`, `closed: 2026-05-01`, and `resolution: fixed`.
- [ ] No env-var references (`P4_TEST_HOST`, `P4_TEST_USER`, `P4_TEST_SHELVED_CL`) remain in the codebase. Run: `grep -r "P4_TEST" . --include="*.go" --include="*.md"` — expected: no hits other than this plan and the spec.

---

## Out of scope

These remain as separate backlog items and are NOT addressed by this plan:

- [`bug-2026-04-25-p4-binary-assumed-authenticated`](../../backlog/bug-2026-04-25-p4-binary-assumed-authenticated.md) — agent-side `p4 login` diagnostics.
- [`bug-2026-04-25-no-client-uuid-validation-workspaces`](../../backlog/bug-2026-04-25-no-client-uuid-validation-workspaces.md) — CLI UUID validation UX fix.
- Multiarch (linux/arm64) image. Defer until someone hits real friction running under Rosetta on Apple Silicon.
- Hosting our own copy of the p4d binary in a private registry. Defer unless `ftp.perforce.com` reliability becomes a real problem.
- Bumping to r26.x once Perforce republishes standalone binaries in the historical layout — re-evaluate as a one-line ARG bump in a future iteration.
