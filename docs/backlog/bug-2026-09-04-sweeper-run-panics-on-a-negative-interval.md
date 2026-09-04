---
title: Sweeper.Run guards a zero interval but not a negative one, so a bad env value panics an unsupervised goroutine and kills the agent
type: bug
status: open
created: 2026-09-04
priority: low
source: Phase 4 re-verify lens on the p4 sync heartbeat slice; measured with the new overflow guard disabled
---

# Sweeper.Run guards a zero interval but not a negative one, so a bad env value panics an unsupervised goroutine and kills the agent

## Summary

`Sweeper.Run` returns early when its interval is exactly zero and does nothing about a
negative one, so the value reaches `time.NewTicker`, which panics with "non-positive
interval for NewTicker". `cmd/relay-agent/main.go` launches it as a bare `go sw.Run(ctx)`,
so the panic is unrecovered on an unsupervised goroutine and the agent process dies at
startup.

## Context

Found while reviewing the sync-heartbeat slice, which closed the route that reached it:
`parseDurationEnv` discarded `strconv.Atoi`'s error and multiplied without an overflow
check, so a large-but-well-formed value like `10000000000s` wrapped negative. That guard
now rejects the operand before the multiply, so **no env value can currently produce a
negative interval** - which is exactly why this is low and why it is worth writing down.
The precondition is unguarded, and the next caller to compute an interval arithmetically
has nothing telling it so.

Note the asymmetry that makes the omission easy to miss: a zero interval IS handled, so
the function looks like it validates its input.

## Proposal

Guard `interval <= 0` rather than `interval == 0` in `Sweeper.Run`, and say in one line
that the ticker panics rather than misbehaves on a non-positive value. A test that calls
`Run` with a negative interval and asserts it returns rather than panics is two lines.

Worth deciding at the same time whether an unrecovered panic on `go sw.Run(ctx)` should
kill the agent at all, or whether the sweeper's goroutine deserves the same treatment as
other long-lived agent goroutines.

## Acceptance / Done When

- A negative interval makes `Sweeper.Run` return, proven by a test.
- The panic-not-misbehave property is stated where a future caller will read it.

## Related

- `internal/agent/source/perforce/sweeper.go` (`Run`), `cmd/relay-agent/main.go`
- [[bug-2026-09-04-a-warm-workspace-in-active-use-ages-out]] - same sweeper
