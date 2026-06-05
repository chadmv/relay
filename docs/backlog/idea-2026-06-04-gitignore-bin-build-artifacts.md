---
title: Gitignore bin/ and root *.exe build artifacts
type: idea
status: open
created: 2026-06-04
source: noticed during auto-enroll verification (retro 2026-06-04-auto-enroll-mode)
---

# Gitignore bin/ and root *.exe build artifacts

## Summary
`bin/` is not gitignored. Build artifacts (`relay-server.exe`, `relay-agent.exe`, `relay.exe`) show up as untracked after a local build, which is easy to commit by accident. A `.gitignore` entry for `bin/` and root `*.exe` would prevent that.
