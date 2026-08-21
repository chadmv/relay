---
title: The netlimit conn wrapper drops TCP_USER_TIMEOUT on Linux and empties channelz socket metrics
type: bug
status: open
created: 2026-08-21
priority: low
source: Phase 2 planning of the 2026-08-20-grpc-admission-bounds slice (R7); the plan mandated this item by name
---

# The netlimit conn wrapper drops TCP_USER_TIMEOUT on Linux and empties channelz socket metrics

## Summary

`netlimit.Accept` returns a **wrapping** `net.Conn`, because that wrapper's `Close` is the only hook
that can release a connection slot. Two things in grpc-go reach past a `net.Conn` interface with a
type assertion, and both are lost as a result.

**First, `TCP_USER_TIMEOUT` is not set on Linux.** `NewServerTransport` does `rawConn := conn` and,
because relay sets `Time: 30s` (not `infinity`), calls
`internal/syscall.SetTCPUserTimeout(rawConn, kp.Timeout)`
(`grpc@v1.80.0/internal/transport/http2_server.go:236-240`). That function is:

```go
func SetTCPUserTimeout(conn net.Conn, timeout time.Duration) error {
	tcpconn, ok := conn.(*net.TCPConn)
	if !ok {
		// not a TCP connection. exit early
		return nil
	}
```

(`internal/syscall/syscall_linux.go:71-76`). The assertion is on the **concrete type**, so no
interface trick on the wrapper can satisfy it, and it fails **silently** by returning `nil`. Relay got
this socket option before the 2026-08-20 admission slice and does not get it after.

**Second, channelz socket metrics go empty.** `channelz.GetSocketOption` asserts
`socket.(syscall.Conn)`, which the wrapper does not forward, so the per-socket options channelz would
report are absent.

Both are disclosed in `internal/netlimit/listener.go`'s package doc under "Known consequences of
wrapping the conn: there are TWO". **This item exists because the plan required it to be tracked
rather than left there**, in as many words: "it is a real behavioural regression introduced by this
slice and it must be tracked rather than lost in a doc comment."

## Repro / Symptoms

Neither is observable from relay's own surfaces today, which is why this is low.

- **`TCP_USER_TIMEOUT`:** on Linux, with either `RELAY_GRPC_MAX_CONNS` or
  `RELAY_GRPC_MAX_CONNS_PER_IP` non-zero, inspect the accepted socket's `TCP_USER_TIMEOUT`
  (`ss -ti`, or `getsockopt` in a probe). It is unset. With **both** caps at `0`, `Accept` returns the
  underlying conn unwrapped and the option is set again, which is the cleanest way to demonstrate the
  difference on one binary.
- **channelz:** not observable at all today. Relay registers no channelz service.

The user-visible consequence of losing `TCP_USER_TIMEOUT` is confined to a specific case: a peer whose
network path black-holes packets after data has been queued. Without the socket option, the kernel
retransmits on its own (long) schedule rather than failing the write at `kp.Timeout`.

## Context

Found while planning the admission slice, not while reviewing it, which is worth noting: it is a
regression the slice **introduces** rather than one it fails to fix, and it was found by reading what
grpc-go does with the value `Accept` returns rather than by reading the diff.

**The loss is bounded and the bound is real, which is the whole reason this is low rather than
medium.** grpc-go's application-layer liveness probe is unaffected: `http2Server.keepalive` decides
from `t.lastRead` rather than from whether a write succeeded, so relay's `Time = 30s` /
`Timeout = 10s` still tears a dead peer down at about 40s. `TCP_USER_TIMEOUT` would make one class of
failure fail faster; it is not the only thing detecting it.

## Proposal

To be argued at spec time. The two halves have very different costs and should be decided separately.

- **channelz is the cheap half and probably not worth doing alone.** Forwarding `SyscallConn` from the
  wrapper restores it in a few lines. But relay registers no channelz service, so it restores a
  diagnostic nobody reads. Do it only if it lands alongside something that consumes it. Note
  explicitly that **forwarding `SyscallConn` does NOT restore `TCP_USER_TIMEOUT`** - that needs the
  concrete `*net.TCPConn` and cannot be recovered through any interface.
- **`TCP_USER_TIMEOUT` needs a build-tagged file duplicating a grpc-go internal.** The shape is a
  `//go:build linux` file in `internal/netlimit` that, at `Accept` time, type-asserts the **underlying**
  conn to `*net.TCPConn` and sets the option itself before returning the wrapper. Settle:
  - **Where the timeout value comes from.** grpc-go uses `kp.Timeout`, which lives in
    `grpcKeepaliveParams` in `cmd/relay-server`. `netlimit` currently knows nothing about keepalive and
    should probably keep not knowing: pass a duration into `netlimit.Config`, or set the option in
    `main.go` through a hook, rather than importing keepalive config into a listener package.
  - **What happens when the assertion fails.** Same answer as grpc-go's: do nothing, silently. A Unix
    socket or a test fake is not a bug.
  - **Whether it is worth duplicating a value grpc-go may change.** If grpc-go's own default handling
    of this moves, the duplicate goes stale silently. A comment citing the exact grpc-go file and
    version is the minimum; a test that fails when the module version moves is probably
    disproportionate.
- **The alternative worth pricing first: do not wrap at all.** The wrapper exists only to release a
  slot on `Close`. If a slot could be released some other way - a `sync.Map` keyed on the conn, swept
  by something else - the whole class of assertion breakage goes away. This is almost certainly worse
  (a teardown you can get wrong, which is exactly what `conn.Close`'s `sync.Once` was designed to
  avoid), but it should be rejected on the record rather than assumed away.

## Acceptance / Done When

- On Linux, with the caps enabled, the accepted socket carries `TCP_USER_TIMEOUT`, proven by a
  build-tagged test that reads the option back off a real socket and is RED against today's code.
- The test is skipped, not failed, on non-Linux platforms, and the skip says why.
- The keepalive timeout the option is set from is the same value `grpcKeepaliveParams` gives grpc-go,
  and something makes a divergence between the two visible rather than silent.
- `internal/netlimit`'s package doc is amended: whichever half lands stops being a "known consequence"
  and the other half's paragraph says which one is still outstanding.
- Either channelz socket options are restored by forwarding `SyscallConn`, or the decision to leave
  them empty is stated with the reason (no channelz service registered).
- The both-caps-disabled branch still returns the conn **unwrapped**, and its comment still explains
  that this is what preserves the concrete type for an operator who caps connections at a proxy.

## Related

- Source: `internal/netlimit/listener.go` (the package doc's "Known consequences" section, `Accept`'s
  both-caps-disabled branch, the `conn` type)
- grpc-go, read at `v1.80.0`: `internal/transport/http2_server.go:151` (`rawConn := conn`),
  `:236-240` (the `SetTCPUserTimeout` call), `internal/syscall/syscall_linux.go:71-76` (the assertion)
- The slice that introduced it: `docs/superpowers/specs/2026-08-20-grpc-admission-bounds.md`,
  `docs/superpowers/plans/2026-08-20-grpc-admission-bounds.md` (R7),
  `docs/retros/2026-08-21-grpc-admission-bounds.md`
- The item that slice closed: [[bug-2026-08-15-grpc-connection-admission-is-unbounded]]

## Notes

Filed at **low** deliberately, and the honest severity argument is that the application-layer keepalive
already covers the failure class at 40s. The reason to keep the item open anyway is that this is a
**silent** regression: nothing in relay, nothing in grpc-go and no test observes that the option went
missing, and the only record is a paragraph in a package doc that a future reader has no reason to
open. An item is the cheapest thing that survives the doc comment being rewritten.

The generalizable note worth carrying: **wrapping an interface value silently disables every
type-assertion-based feature underneath it.** `TCP_USER_TIMEOUT` and channelz are the two instances
grpc-go has today; a future grpc-go version may add a third, and nothing will say so. If the wrapper
stays, that sentence belongs in its doc comment as a standing hazard rather than as an enumeration of
two known cases.
</content>
