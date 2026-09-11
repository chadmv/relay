---
title: The boot admits and dispatches agent work for the whole pre-listener window while HTTP is down
type: idea
status: open
created: 2026-09-10
priority: medium
source: Found while specifying the ReconcileOnStartup paging slice (PR #208); the premise every pre-listener item rests on is half false
---

# The boot admits and dispatches agent work for the whole pre-listener window while HTTP is down

## Summary

`grpcSrv.Serve`'s goroutine and `go dispatcher.Run(ctx)` both start ABOVE the two blocking
pre-listener passes, and `srv.ListenAndServe()` starts below them. So agents connect, register and
receive dispatched tasks for the entire duration of those passes, while a readiness probe on `:8080`
fails.

## Context

**Every pre-listener item says "before the server accepts a request", and that is true of HTTP and
false of gRPC.** The phrase appears in several items and reads as if nothing is running. Recording the
real ordering once, in its own item, is cheaper than correcting each.

Three consequences follow that no existing item states:

- A readiness probe on `:8080` fails while `:9090` accepts, so an orchestrator may restart a server
  that is working.
- The passes' UPDATEs contend with in-flight registrations for the same 25-connection pool.
- A cross-replica `handlePatchScheduledJob` is reachable in that window, which is what makes the
  reconcile clobber hazard real rather than theoretical.

Pre-existing and unchanged by any slice in the 2026-09-10 batch. The deadline slice bounds how long
the window lasts; it does not change what is admitted during it.

## Proposal

Sketch only, and the options differ in kind:

- **Accept and document.** Name the window in README's startup sequence so an operator configuring a
  probe knows `:8080` is not ready while `:9090` is.
- **Move the gRPC listener below the passes.** Symmetric, but it delays agent reconnect after every
  deploy, and the grace-timer seeding exists precisely to tolerate that gap - so this trades one
  window for another.
- **Admit gRPC connections but not dispatch.** The dispatcher is the part that assigns work; holding
  only `dispatcher.Run` until after the passes is the narrower change.

## Acceptance / Done When

- The window is either closed or documented where an operator configures health checks.
- Whichever is chosen, the items that say "before the server accepts a request" are corrected to name
  HTTP specifically.

## Related

- `cmd/relay-server/main.go` (the ordering of `grpcSrv.Serve`, `dispatcher.Run`, the two passes, and
  `srv.ListenAndServe`)
- [[bug-2026-08-28-boot-sweep-lists-every-schedule-ahead-of-the-listener]]
- [[feature-2026-09-04-wall-clock-deadline-on-the-boot-sweep]] - closed; bounds the window's duration
