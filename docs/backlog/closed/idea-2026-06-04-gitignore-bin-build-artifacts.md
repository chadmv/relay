---
title: Gitignore bin/ and root *.exe build artifacts
type: idea
status: closed
created: 2026-06-04
closed: 2026-06-05
resolution: fixed
source: noticed during auto-enroll verification (retro 2026-06-04-auto-enroll-mode)
---

# Gitignore bin/ and root *.exe build artifacts

## Summary
`bin/` is not gitignored. Build artifacts (`relay-server.exe`, `relay-agent.exe`, `relay.exe`) show up as untracked after a local build, which is easy to commit by accident. A `.gitignore` entry for `bin/` and root `*.exe` would prevent that.

## Resolution
Closed 2026-06-05 by `b8ba2e8`. `bin/` and root `*.exe` build artifacts are gitignored.
