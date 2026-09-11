---
title: _path_starts_with_slashes raises "stream is required" for a bad path, and duplicates its own branch
type: bug
status: open
created: 2026-09-10
priority: low
source: Noticed by the security lens on the source.sync entry-count bound (PR #207), outside that diff
---

# _path_starts_with_slashes raises "stream is required" for a bad path, and duplicates its own branch

## Summary

In `python/src/relay/models.py`, `_path_starts_with_slashes` raises a message naming the STREAM for a
malformed `path`, and the same `startswith("//")` branch appears twice - the second carrying a
different message that is unreachable.

## Repro / Symptoms

A `Sync` entry whose `path` does not begin with `//` is rejected locally with text about `stream`
rather than about the field that was wrong. The caller is told to fix the wrong field.

## Context

Local-only: the server validates independently and its message is correct, so the consequence is a
misleading client-side error rather than a wrong acceptance. Found while checking the cross-language
claims of a neighbouring docstring, not by a failing test.

The duplicated branch is the tell that this was an edit that did not finish: whichever message was
meant to survive, only the first is reachable.

## Acceptance / Done When

- A bad `path` is refused with a message naming `path`.
- The duplicated branch is gone, so there is one reachable message per condition.
- A test covers the message text, since the defect is invisible to any test that only asserts that
  validation failed.

## Related

- `python/src/relay/models.py` (`_path_starts_with_slashes`)
