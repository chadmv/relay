---
title: Bump GitHub Actions off deprecated Node 20
type: idea
status: closed
created: 2026-06-03
closed: 2026-06-21
resolution: fixed
priority: low
source: surfaced as a deprecation warning during the python-v0.1.2 release run
---

# Bump GitHub Actions off deprecated Node 20

## Summary

The python-release run for python-v0.1.2 surfaced a GitHub deprecation warning: actions/checkout@v4 and actions/setup-python@v5 run on Node.js 20, which is being deprecated on GitHub Actions runners (forced to Node 24 by default ~June 16 2026, Node 20 removed ~Sept 16 2026). Bump these actions to versions that support Node 24 in .github/workflows/release.yml and .github/workflows/python.yml (and check any other workflow files) before the cutoff.

## Related

- Ref: https://github.blog/changelog/2025-09-19-deprecation-of-node-20-on-github-actions-runners/
- `.github/workflows/release.yml`, `.github/workflows/python.yml`

## Resolution
fixed (2026-06-21). Bumped all Node-20 GitHub Action majors to their Node-24 successors across every workflow: `actions/checkout@v4`->`@v5`, `actions/setup-python@v5`->`@v6`, `actions/setup-go@v5`->`@v6`. This covers `release.yml` and `python.yml` (named in the item) plus `go-ci.yml`, which was added after the item was filed and had the same `checkout@v4`/`setup-go@v5` pins. `pypa/gh-action-pypi-publish@release/v1` is a Docker-based action (not Node) and was correctly left unchanged. All three workflow YAML files still parse. Mechanical config bump verified by grep (no Node-20 pins remain) and a YAML parse check.
