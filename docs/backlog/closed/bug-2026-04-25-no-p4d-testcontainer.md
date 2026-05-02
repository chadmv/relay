---
title: Integration test only runs against an existing P4 server
type: bug
status: closed
created: 2026-04-25
closed: 2026-05-01
resolution: fixed
source: 2026-04-25 perforce-workspace-management retro — Known Limitations
---

# Integration test only runs against an existing P4 server

## Summary
**Integration test only runs against an existing P4 server.** No testcontainer for `p4d` yet — would require a container image and CI integration.

## Resolution
Replaced env-var-driven skip with a containerized p4d (Dockerfile + entrypoint script under `internal/agent/source/perforce/testdata/p4d/`). Test fixture in `p4d_container_test.go` starts the container via testcontainers-go, waits for the `p4d ready` log line, and reads the deterministic shelved CL the entrypoint creates. `make test-integration` now exercises the full Perforce sync + unshelve lifecycle on any host with Docker and the `p4` CLI installed.
