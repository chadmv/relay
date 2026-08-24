---
title: A panic on any gRPC handler goroutine terminates relay-server for the whole fleet
type: idea
status: open
created: 2026-08-24
priority: low
source: 2026-08-24 finishregister-strand review; no reachable panic is claimed
---

# A panic on any gRPC handler goroutine terminates relay-server for the whole fleet

## Summary

There is no `recover()` and no `grpc.UnaryInterceptor` or `StreamInterceptor` anywhere in production
code. A panic on any gRPC handler goroutine - or on a goroutine one of them spawns, such as
`go h.triggerDispatch()` - unwinds the whole process, disconnecting every agent and dropping every
in-flight stream.

The HTTP half is protected: `net/http` recovers per connection by default. The gRPC half is not. One
misbehaving connection is therefore a fleet-wide outage.

**No reachable panic is claimed**, which is why this is filed low. It is a blast-radius item, not a
bug report.

## Context

Noted while reasoning about what happens below the ownership handoff in `finishRegister`, where the
code comment now states that a panic there would leave the `workers` row online at a live epoch with
nothing to clear it - and after the crash-restart, nothing does.

## Proposal

Add a stream interceptor that recovers, logs with the worker id where known, and returns
`codes.Internal` to that one peer. Decide deliberately whether to also cover goroutines the handler
spawns - an interceptor cannot reach those, so they need their own `defer recover()` or need to not
exist.

Weigh the counter-argument honestly first: a recovered panic can leave the process in a state its
invariants no longer describe, and a fast crash plus a supervisor restart is sometimes safer. This
project's answer may legitimately be "crash, but reconcile properly on restart", in which case the
linked restart bug is the real fix and this item closes as wontfix with that reasoning recorded.

## Acceptance / Done When

- A decision is recorded either way, with its argument, in the code and not only in this item.
- If an interceptor is added: a test panics inside a handler and asserts the server survives and the
  peer receives `codes.Internal`.

## Related

- `cmd/relay-server/main.go` - gRPC server construction
- [[bug-2026-08-24-ungraceful-restart-leaves-workers-online-forever]] - what the crash leaves behind
