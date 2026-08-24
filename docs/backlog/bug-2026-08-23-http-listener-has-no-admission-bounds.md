---
title: The HTTP listener has none of the admission bounds the gRPC listener got
type: bug
status: open
created: 2026-08-23
priority: medium
source: 2026-08-23 deep roadmap refresh - gaps agent finding
---

# The HTTP listener has none of the admission bounds the gRPC listener got

## Summary
`buildHTTPServer` returns a bare `&http.Server{Addr: d.addr, Handler: s.Handler()}` with no
`ReadHeaderTimeout`, `ReadTimeout`, `WriteTimeout`, `IdleTimeout`, or connection cap
(`cmd/relay-server/http_server.go:158`), and `main` serves it directly via `srv.ListenAndServe()`
(`cmd/relay-server/main.go:277`). The gRPC port on the same binary got five admission controls in
the 2026-08-21 slice (#139: netlimit total/per-IP caps, `MaxConcurrentStreams(1)`, keepalive
policy, idle reaper, registration deadline). An unauthenticated peer can hold a goroutine and a
file descriptor per connection indefinitely by trickling header bytes (slowloris), and nothing
bounds concurrent SSE subscriptions on `/v1/events` either.

## Context
This is the same unbounded-per-connection exposure the netlimit work closed on `:9090`, still open
on `:8080`. The asymmetry is the finding: the hardening argument that justified #139 applies to
both listeners, and only one got the controls. Note `WriteTimeout` interacts with SSE - a naive
`WriteTimeout` would kill every long-lived `/v1/events` stream, so the SSE route needs either a
per-route override (`http.ResponseController.SetWriteDeadline`) or the timeout set to a value SSE
refreshes; this is the design question the slice must answer rather than copying values blindly.

## Acceptance / Done When
- `ReadHeaderTimeout` (the slowloris bound) and `IdleTimeout` are set on the server, with values
  documented in README alongside the gRPC bounds.
- A decision is recorded (in code comment + README) for `ReadTimeout`/`WriteTimeout` vs the SSE
  streaming route - not silently omitted.
- Whether a connection cap (netlimit-style) is wanted on :8080 is decided and recorded; the
  refusal mechanics differ from gRPC (`http.Server` tolerates `Accept` errors), so a written
  argument either way is acceptable.
- The env-to-field wiring for any new knobs is guarded (see
  [[idea-2026-08-14-generalize-the-env-to-field-wiring-guard]] - this slice would add more
  post-construction fields, the exact trigger that item tracks).

## Related
- `cmd/relay-server/http_server.go:158`, `cmd/relay-server/main.go:277`; contrast `main.go:204-207` (gRPC bounds)
- `docs/backlog/closed/bug-2026-08-15-grpc-connection-admission-is-unbounded.md` - the sibling slice this mirrors
- [[idea-2026-08-09-sse-revoked-token-keeps-streaming]] - the other unbounded-lifetime property of `/v1/events`
