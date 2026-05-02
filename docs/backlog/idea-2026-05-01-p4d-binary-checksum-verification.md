---
title: Pin SHA-256 of p4d/p4 binaries downloaded by test container
type: idea
status: open
created: 2026-05-01
source: 2026-05-01 p4d-testcontainer code review (Task 1)
---

# Pin SHA-256 of p4d/p4 binaries downloaded by test container

## Summary
The test container Dockerfile (`internal/agent/source/perforce/testdata/p4d/Dockerfile`) `curl`s `p4d` and `p4` from `ftp.perforce.com` over HTTPS without any checksum verification. Acceptable for a test-only image — supply-chain risk is bounded (image never ships to production, attacker would need to compromise Perforce's CDN to land a malicious binary in our test runs) — but pinning a SHA-256 alongside the version ARG would close the gap.

## Proposal
Add `ARG P4D_SHA256=...` and `ARG P4_SHA256=...` and verify each download with `sha256sum -c` before `chmod +x`. Update the SHA pins when bumping `P4D_VERSION`.

## Acceptance / Done When
- The Dockerfile fails the build if either downloaded binary's SHA-256 does not match the pinned value.
- A version bump documents the procedure for capturing new SHAs.

## Related
- `internal/agent/source/perforce/testdata/p4d/Dockerfile`
- Closes the "no checksum verification" follow-up flagged by the Task 1 code review.
