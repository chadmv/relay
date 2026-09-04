---
title: The Python SDK's _CLIENT_TEMPLATE_RE has drifted from Go's clientTmplRe and accepts a leading hyphen
type: bug
status: open
created: 2026-09-04
priority: low
source: Found while checking the Python SDK during the sync-spec exclusion paths design (2026-09-04)
---

# The Python SDK's _CLIENT_TEMPLATE_RE has drifted from Go's clientTmplRe and accepts a leading hyphen

## Summary
The two validators for the same field no longer agree:

- Go, `internal/jobspec/jobspec.go`: `clientTmplRe = ^[A-Za-z0-9_.][A-Za-z0-9_.-]*$`
- Python, `python/src/relay/models.py`: `_CLIENT_TEMPLATE_RE = ^[A-Za-z0-9_.-]+$`

The Go pattern was tightened to refuse a leading hyphen because `CreateStreamClient` places the
value immediately after `-t`, where a leading hyphen would be read as a flag. The Python pattern
still allows one.

## Context
The server is the validator of record, so this is not a hole: a leading-hyphen template is
refused by `jobspec.Validate` whichever client sends it. What it is instead is the SDK accepting
a spec the server refuses, so the failure surfaces as a server rejection at submit time rather
than as a local validation error, which is the whole point of having the pattern in the SDK.

This is the "unguarded copy is in another language" shape. A lockstep guard proves only the
copies it knows about, and nothing enumerates this pair.

## Acceptance / Done When
- The two patterns agree, or a guard fails when they disagree.
- If a guard is added, it lives in a lane that runs on the commits that can break it. Note that
  `.github/workflows/python.yml` filters `paths: python/**`, so a guard placed there cannot fire
  on the Go commit that changes `clientTmplRe`.

## Related
- `internal/jobspec/jobspec.go` (`clientTmplRe`), `python/src/relay/models.py`
  (`_CLIENT_TEMPLATE_RE`)
- [[idea-2026-08-23-integration-only-guards-ci-never-runs]] - the lane-placement rule this needs
