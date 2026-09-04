---
title: An abandoned free-disk probe goroutine is left per Prepare, so a permanently wedged volume accumulates them over the agent's lifetime
type: idea
status: open
created: 2026-09-04
priority: low
source: Phase 4 re-verify lens on the p4 sync heartbeat slice
---

# An abandoned free-disk probe goroutine is left per Prepare, so a permanently wedged volume accumulates them over the agent's lifetime

## Summary

The sync heartbeat bounds its free-disk probe with a timeout and renders `-` when the
probe does not answer in time, latching the failure so it is not retried for that sync.
A `statfs` has no cancellation, so the probe goroutine cannot be stopped - it is
abandoned. The latch keeps that to **one goroutine per `Prepare`** rather than one per
tick, which is the right bound and was the point of the design. But on a volume that is
permanently wedged, every task that prepares against it leaves one more, and nothing ever
reaps them.

## Context

Strictly better than what it replaced: before the bound, a wedged `statfs` blocked
`Prepare` itself with the workspace handle held. The residue is a slow leak on a host
that is already in a bad state, and each goroutine holds only a buffered channel.

The reason to write it down rather than shrug: the bound is stated in the code as "one
per Prepare", which is true and reads as closed. It is closed per sync and open per
agent lifetime, and those are different claims.

## Proposal

Options, cheapest first:

- Accept it and say so where the bound is stated, so the per-agent axis is not mistaken
  for closed. This is probably the right answer.
- Latch at the provider rather than per-sync, so a wedged volume costs one goroutine for
  the agent's life instead of one per task. Needs a way to un-latch when the volume
  recovers, which is the part that is not obvious.
- Probe free disk on a dedicated long-lived goroutine that the heartbeat reads from,
  which trades the leak for a staleness question.

## Acceptance / Done When

- Either the per-agent behaviour is bounded, or it is stated where the per-sync bound is
  stated, so the two axes are not conflated.

## Related

- `internal/agent/source/perforce/perforce.go` (`probeFreeDiskGB`)
- `cmd/relay-agent/free_disk_unix.go`, `free_disk_windows.go`
