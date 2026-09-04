---
title: classifyP4Error does not recognise an out-of-disk failure, so a full workspace volume reads as a raw p4 error
type: feature
status: closed
created: 2026-09-03
closed: 2026-09-03
resolution: fixed
priority: low
source: SDNM fork divergence analysis (relay_updates.md, PR-4), evaluated 2026-09-03
---

# classifyP4Error does not recognise an out-of-disk failure, so a full workspace volume reads as a raw p4 error

## Summary

`internal/agent/source/perforce/diagnostics.go` rewraps four known p4 failures with operator
guidance (binary missing, ticket missing, ticket expired, server unreachable). A sync that fills
the workspace volume is the most common failure on a render farm and is not one of them, so the
operator sees p4's own text with no pointer to the remedy. A fork of relay added the case; this
item ports it with the studio-specific text removed.

## Proposal

1. Add a case to `classifyP4Error` matching, after the existing lower-casing: `no space left on
   device` (Linux), `there is not enough space on the disk` and `not enough space` (Windows), `disk
   full`, and `insufficient disk space`. **Do not match `insufficient` and `space` as two
   independent substrings** as the fork does: `workspace` contains `space`, so an error such as
   "insufficient permissions on workspace" would be misreported as a full disk. Match the phrase.
2. Message, with a hyphen and not an em dash (the existing lines carry em dashes; do not copy them,
   and leave them alone): `out of disk space on this agent's workspace volume - free space, let the
   sweeper evict idle workspaces (RELAY_WORKSPACE_MIN_FREE_GB), or reduce the sync paths: %w`. If
   [[idea-2026-09-03-sync-spec-exclusion-paths-design]] ships, extend the remedy to name
   exclusions; until then it must not mention a feature that does not exist.
3. Table-driven cases in `diagnostics_test.go`: one per substring, plus negative cases for
   `workspace not found` and `insufficient permissions on workspace`, which must fall through to
   the default arm unchanged. The negative cases are the reason the test exists; the positive ones
   alone would pass the fork's two-substring version.

## Acceptance / Done When

- Each of the listed p4 disk-full phrasings is rewrapped with the remedy line and preserves the
  original via `%w`.
- The two negative cases return the error unchanged.
- No studio-specific path or product name appears in the message.

## Related

- `internal/agent/source/perforce/diagnostics.go`, `diagnostics_test.go`
- [[idea-2026-09-03-sync-spec-exclusion-paths-design]] - the remedy clause this message may gain later
- [[feature-2026-09-03-p4-sync-progress-heartbeat]] - reports free space during the sync, before
  this error fires

## Resolution

Fixed on `claude/top-3-roadmap-items-65856f`. `classifyP4Error` gained the out-of-disk case,
matching the phrase and not the words, with `workspace not found` and `insufficient permissions on
workspace` as negative cases. The fork's two-substring form was run as a mutation and the
permissions negative kills it.

**The slice shipped two things this item did not ask for, and both are load-bearing.**

First, the remedy text. The item prescribed a message naming `RELAY_WORKSPACE_MIN_FREE_GB`; that
was dropped, because raising it evicts other tenants' warm workspaces and, on a deployment where
it is unset, turns the sweeper on for the first time - a remedy in the forger's favour for a
signal a job author can drive.

Second, classification no longer sees caller-supplied command args at all. `execRunner` returns a
`p4CommandError` keeping args, underlying error and stderr in separate fields, and
`classifiableText` returns `""` when no such error is in the chain, so an error that did not come
from a p4 invocation is never classified. This was necessary rather than opportunistic: without
it, a job spec naming a stream `//depot/disk full` had its own task diagnosed as a full volume.

**A residual remains and is filed separately.** p4 echoes the failing path into its own stderr in
some error classes, and stderr is classified by design - see
[[bug-2026-09-03-classify-p4-error-matches-p4-echoed-path-in-stderr]]. Note also that every
pre-existing fixture in `TestClassifyP4Error` modelled a producer shape production never emits;
`fakeRunner` now returns the production type, so all cases exercise the args exclusion.
