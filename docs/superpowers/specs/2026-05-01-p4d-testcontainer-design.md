# p4d Testcontainer for Perforce Integration Test — Design

**Date:** 2026-05-01
**Backlog item:** [`bug-2026-04-25-no-p4d-testcontainer`](../../backlog/bug-2026-04-25-no-p4d-testcontainer.md)

## Goal

Replace the env-var-driven skip in `internal/agent/source/perforce/perforce_integration_test.go` with a containerized `p4d` so the integration test runs deterministically without an external Perforce server. After this change, `make test-integration` exercises the full `Provider.Prepare` → `Finalize` lifecycle (including the unshelve path) on any host with Docker and the `p4` CLI installed.

## Non-goals

- **`p4 login` in the agent.** A separate backlog item ([`bug-2026-04-25-p4-binary-assumed-authenticated`](../../backlog/bug-2026-04-25-p4-binary-assumed-authenticated.md)) covers diagnostics for the production case. The container runs `security=0` so the test never needs to authenticate; the agent's existing assumption ("a valid ticket exists") is satisfied trivially.
- **CI workflow changes.** This repo has no `.github/workflows/` or `.gitlab-ci.yml` today; this design adds no CI infrastructure. When CI lands, it will need Docker and the `p4` CLI on the runner — flagged here for whoever sets it up.
- **Multiarch image.** v1 ships `linux/amd64` only. Apple Silicon devs run via Rosetta emulation under Docker Desktop's `desktop-linux` context. Adding `linux/arm64` is a one-line addition (download the `bin.linux26aarch64` build); deferred until someone hits a real performance issue.
- **Replacing the host `p4` CLI requirement.** The agent under test shells out to `p4` via `os/exec`. The host running the test still needs `p4` on PATH. The test pre-flights with `exec.LookPath("p4")` and skips cleanly if missing.
- **The other two p4-related backlog items.** [`bug-2026-04-25-p4-binary-assumed-authenticated`](../../backlog/bug-2026-04-25-p4-binary-assumed-authenticated.md) and [`bug-2026-04-25-no-client-uuid-validation-workspaces`](../../backlog/bug-2026-04-25-no-client-uuid-validation-workspaces.md) are out of scope.

## Architecture

Three units with clear boundaries:

1. **The image** (`internal/agent/source/perforce/testdata/p4d/`) — `Dockerfile` + `entrypoint.sh`. Self-contained; can be `docker run`'d manually for debugging without involving Go.
2. **The test fixture** (`p4d_container_test.go`, build tag `integration`) — owns the testcontainers-go lifecycle: pre-flight checks, build, start, wait-for-ready, expose connection params, terminate on cleanup.
3. **The test body** (existing `perforce_integration_test.go`) — keeps its current shape; loses the `P4_TEST_HOST` skip block and gains an `exec.LookPath("p4")` pre-flight + a call to the fixture.

## The image

**Files:**
- `internal/agent/source/perforce/testdata/p4d/Dockerfile`
- `internal/agent/source/perforce/testdata/p4d/entrypoint.sh`

**Dockerfile:**
- Base: `debian:bookworm-slim`.
- Build ARG: `P4D_VERSION=r25.2`. Perforce reorganized `bin.linux26x86_64/` in r26.1 — the standalone `p4d`/`p4` binaries are no longer published there. r25.2 is the latest release that exposes the bare executables at a stable URL. Re-evaluate when r26.x's layout stabilizes.
- Install `curl`, download `p4d` and `p4` binaries from `https://ftp.perforce.com/perforce/${P4D_VERSION}/bin.linux26x86_64/`, `chmod +x`, place under `/usr/local/bin/`.
- Create unprivileged `perforce` user; set `P4ROOT=/var/p4root`, owned by `perforce`.
- `EXPOSE 1666`.
- `ENTRYPOINT ["/usr/local/bin/entrypoint.sh"]`.

**Entrypoint script (`entrypoint.sh`):**

The script runs as the `perforce` user and is responsible for the entire setup. Steps:

1. Start `p4d -d /var/p4root -p 0.0.0.0:1666 &` in the background; capture its pid.
2. Poll `p4 -p localhost:1666 info` (with a ~30s timeout) until it returns successfully.
3. Configure `p4 configure set security=0` so no authentication is required.
4. Create stream depot `test`:
   ```
   echo -e "Depot: test\nType: stream\nMap: test/..." | p4 depot -i
   ```
5. Create mainline stream `//test/main`:
   ```
   echo -e "Stream: //test/main\nName: main\nParent: none\nType: mainline\nPaths: share ..." | p4 stream -i
   ```
6. Create a temp client mapped to `//test/main`, populate with a single `readme.txt` (content: `"baseline"`), `p4 add`, `p4 submit -d "init"`.
7. Edit `readme.txt` to `"shelved-content"`, `p4 change` to create a numbered CL, `p4 shelve -c <N>`. Capture the CL number.
8. Write the shelved CL number to `/var/p4root/shelved-cl.txt` (so the test can read it back).
9. Echo `p4d ready` to stdout — this is the wait-for-ready signal the test fixture probes for.
10. `wait` on the p4d background pid (keeps the container alive).

Any failure in steps 1–9 causes the script to exit non-zero, which testcontainers-go treats as a startup failure and surfaces in the test error.

## The test fixture

**File:** `internal/agent/source/perforce/p4d_container_test.go` (build tag `integration`).

**Public surface:**

```go
type p4dHandle struct {
    P4Port    string  // e.g. "localhost:54321"
    P4User    string  // "perforce" — the only user that exists
    ShelvedCL int64   // the CL number captured by the entrypoint
}

func startP4dContainer(t *testing.T) p4dHandle
```

**Lifecycle:**

1. **Pre-flight `p4` CLI check.** `exec.LookPath("p4")`; if not found, `t.Skip("p4 client binary required on PATH")`. Done *before* the container build to avoid paying the build cost only to skip.
2. **Build & start the container** via `testcontainers.GenericContainer` with `FromDockerfile{Context: "testdata/p4d"}`, `ExposedPorts: []string{"1666/tcp"}`, and `WaitingFor: wait.ForLog("p4d ready").WithStartupTimeout(2*time.Minute)`. If the call returns an error indicating Docker is unreachable, `t.Skipf("Docker required: %v", err)`. Other errors fail the test.
3. **Resolve the random host port** via `container.MappedPort(ctx, "1666/tcp")`. Build `P4PORT = "localhost:<port>"`.
4. **Read the shelved CL number** by `container.CopyFileFromContainer(ctx, "/var/p4root/shelved-cl.txt")` (or `container.Exec(ctx, []string{"cat", "/var/p4root/shelved-cl.txt"})` if simpler), parse to `int64`.
5. **Register cleanup**: `t.Cleanup(func() { _ = container.Terminate(context.Background()) })`.
6. **Return** the populated `p4dHandle`.

**Skip-vs-fail policy:** the only legitimate skip reasons are "Docker unavailable" and "p4 CLI missing on PATH." Everything else (image build failure, container start failure, depot setup failure) is a hard test failure.

## The test body

`perforce_integration_test.go` is rewritten to use the fixture. Diff in shape:

**Removed:**
- `P4_TEST_HOST` skip block (currently lines 30–33).
- `P4_TEST_USER` env override (currently lines 36–38).
- The standalone `p4 info` reachability probe (currently lines 41–43) — moved into the fixture's wait strategy.
- `P4_TEST_SHELVED_CL` optional wiring (currently lines 56–60).

**Added at top of test:**
```go
p4d := startP4dContainer(t)
t.Setenv("P4PORT", p4d.P4Port)
t.Setenv("P4USER", p4d.P4User)
```

**Spec construction:** `Unshelves` is now always populated with `p4d.ShelvedCL` instead of being gated on the env var:
```go
spec.GetPerforce().Unshelves = []int64{p4d.ShelvedCL}
```

**Assertions:** the existing assertions remain. The unshelve path is now exercised on every run (was previously conditional), so any assertions touching post-unshelve workspace state become unconditionally meaningful. Optional follow-up: assert that `Finalize` reverts the unshelved file (depends on whether the existing test already covers this — verified during implementation).

**Timeout:** the existing 5-minute test timeout stays. First-run image build can take ~2–3 minutes; subsequent runs reuse the cached image.

## Documentation

**`CLAUDE.md` — `Source providers` paragraph:**

The current paragraph reads:

> **Source providers:** Relay assumes `p4` is installed and a valid P4 ticket is active on the agent host. Provision P4 tickets out-of-band (e.g. via `p4 login` in your system startup). Relay does not manage P4 credentials.

Add a sentence noting the integration test now uses a containerized `p4d` and only requires the host `p4` CLI:

> The Perforce integration test (`perforce_integration_test.go`) spins up a `p4d` container via testcontainers-go; it requires Docker and the `p4` CLI on PATH but no external Perforce server.

**`CLAUDE.md` — Commands block:**

Update the `make test-integration` comment to mention p4d alongside Postgres:

> ```bash
> # Integration tests (requires Docker Desktop running and the `p4` CLI on PATH;
> # spins up Postgres and p4d containers; -p 1 prevents parallel container conflicts)
> make test-integration
> ```

**`Makefile`:** no change.

**`README.md`:** if the README documents how to run integration tests, add the `p4` CLI requirement note there too. Verified during implementation.

## Testing

**Unit tests:** none added. The fixture is integration-only; the entrypoint script is verified by the integration test it enables.

**Integration test:** the existing `TestPerforce_E2E_SyncAndUnshelve` is the regression target. Success criteria:
- Test passes on a clean dev machine with Docker running and `p4` CLI installed, with no `P4_TEST_*` env vars set.
- Test skips with a clear message when Docker is unavailable.
- Test skips with a clear message when `p4` CLI is missing.
- The unshelve assertions all pass (previously skipped without `P4_TEST_SHELVED_CL`).

**Manual verification:**
- `docker run -p 1666:1666 <image>` — manual smoke test that the image is debuggable in isolation.
- `make test-integration` end-to-end on Windows with Docker Desktop.

## Risks & open questions

- **First-run build cost.** ~2–3 minutes downloading the p4d binary and configuring the depot. Acceptable on a one-time basis; subsequent runs reuse the cached image. No mitigation needed for v1.
- **Apple Silicon emulation.** Running `linux/amd64` under Rosetta on M-series Macs is slower (~30–50%) but functional. If a maintainer hits real friction here, add a multiarch build later.
- **`ftp.perforce.com` availability.** The image build downloads p4d at build time. If the FTP is down, image build fails and the test fails to start. Acceptable risk — Perforce's FTP is reliable. Could be mitigated later by hosting a copy in our own registry, but YAGNI for now.
- **p4d release cadence.** Perforce ships quarterly (rNN.1 / rNN.2). The pinned `r25.2` will go stale; bumping is one line. Re-evaluating to r26.x once Perforce's directory layout stabilizes is a good follow-up — track via a backlog item if it becomes load-bearing.
