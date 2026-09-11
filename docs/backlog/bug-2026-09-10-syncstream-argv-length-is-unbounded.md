---
title: SyncStream builds one un-batched argv element per include, with no byte bound
type: bug
status: open
created: 2026-09-10
priority: medium
source: Measured during the source.sync entry-count bound slice (PR #207), which bounds the count and explicitly not the bytes
---

# SyncStream builds one un-batched argv element per include, with no byte bound

## Summary

`SyncStream` passes one argv element per include to a single `exec` call, and path length is bounded
only by `maxBodyBytes`. The new `maxSyncEntries = 512` bounds the COUNT and does not narrow the bytes
at all: before it, one entry carrying a megabyte-long path was legal; after it, 512 long paths still
are.

## Repro / Symptoms

Measured by actually starting the process on Windows 11 with Go 1.26.2, building the same argv
`SyncStream` builds (16-char client name, 40-char path tails):

| entries | command line | result |
|---|---|---|
| 441 | 32,703 | Start OK |
| 442 | 32,777 | Start OK |
| 450 | 33,369 | **Start FAILED** - the filename or extension is too long |
| 512 | 37,957 | Start FAILED |
| 512, 1900-char tails | 990,277 | Start FAILED |

So the cap sits ABOVE the documented 32,767-character `CreateProcess` limit, which ordinary depot
paths reach around 450 entries - inside the cap.

## Context

**It is a refusal, not a truncation, and there is no injection surface.** `CreateProcess` fails, Go
returns the error from `Start`, and nothing runs a partial sync. The binary is the hardcoded `p4`,
args are a `[]string` with no shell, and every path is already prefix- and control-byte-checked.

**What is genuinely bad is the ORDERING.** The failure lands after the per-entry `ResolveHead` loop
and after the preempt, so an over-long spec pays up to 512 round trips to the operator's Perforce
server, fails, and repeats on every retry.

Linux is far from its limits with this shape: `MAX_ARG_STRLEN` is 131,072 per single argument and the
longest element here is 73 bytes, well under. The 1-entry megabyte-path case is the one that blows
the Linux per-argument limit, and it was legal before this bound and still is.

## Proposal

Sketch only. **A tighter count bound is the wrong instrument** - path length is unbounded, so no
count reliably prevents it, and a single long path blows the limit at one entry. Candidates are a
filespec file (`p4 -x`) or chunked invocations. Either changes the argv shape, so decide what a
partial-chunk failure means before picking.

## Acceptance / Done When

- The p4 sync invocation cannot be made to exceed a platform command-line limit by a legal job spec.
- Whatever the remedy, a partial failure mid-chunk has a decided meaning and it is documented.
- The `maxSyncEntries` comment's "does not bound argv bytes" paragraph is updated or deleted once it
  is no longer true.

## Related

- `internal/agent/source/perforce/client.go` (`SyncStream`),
  `internal/agent/source/perforce/perforce.go` (`Prepare`, where the specs are assembled)
- [[idea-2026-09-04-source-sync-has-no-entry-count-bound]] - closed; bounds the count, not the bytes
