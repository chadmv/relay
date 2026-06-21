---
date: 2026-06-21
topic: bump-github-actions-deprecated-node
branch: claude/distracted-allen-9c27c1
pr: "2026-06-21 / bump-github-actions-deprecated-node"
merge: "2026-06-21 / bump-github-actions-deprecated-node"
---

# Session Retro: 2026-06-21 - bump GitHub Actions off Node 20

**TL;DR:** Closed `idea-2026-06-03-bump-github-actions-deprecated-node`. GitHub is forcing
`actions/checkout@v4` / `setup-python@v5` / `setup-go@v5` off Node 20 (~June 2026, removed ~Sept 2026).
Bumped all of them to their Node-24 majors across every workflow. Autopilot batch, item 7 of 7 (final).

## What Was Built

- `.github/workflows/go-ci.yml` - `checkout@v4`->`@v5`, `setup-go@v5`->`@v6`.
- `.github/workflows/python.yml` - `checkout@v4`->`@v5` (x2), `setup-python@v5`->`@v6` (x2).
- `.github/workflows/release.yml` - `checkout@v4`->`@v5`, `setup-python@v5`->`@v6`.

## Key Decisions

- **Include go-ci.yml even though the item only named release/python.** The item was filed 2026-06-03;
  `go-ci.yml` was added in a later cycle with the same `checkout@v4`/`setup-go@v5` pins. The item said
  "check any other workflow files", so a grep across `.github/workflows/` caught it - a partial bump
  would have left CI still warning.
- **Leave the Docker action alone.** `pypa/gh-action-pypi-publish@release/v1` is a container action, not
  a Node action, so the Node-20 deprecation does not apply; bumping it was out of scope.

## Process Note

- Mechanical config bump, applied directly by the conductor (no engineer/verify fan-out per the
  trivial-task guidance). Verification: a grep confirming no Node-20 pins remain plus a YAML parse of
  all three files. The actual runner behavior can only be confirmed on the next CI run, but the major
  versions chosen (checkout v5, setup-python v6, setup-go v6) are the established Node-24 successors.

## Backlog Triage

- No new items.
