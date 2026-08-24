---
title: The agent channel is hardcoded plaintext and mDNS discovery trusts the first advertiser, so agent tokens are harvestable on the LAN
type: bug
status: open
created: 2026-08-23
priority: medium
source: 2026-08-23 deep roadmap refresh - gaps agent finding
---

# The agent channel is hardcoded plaintext and mDNS discovery trusts the first advertiser, so agent tokens are harvestable on the LAN

## Summary
The agent dials with `insecure.NewCredentials()` unconditionally - no flag or env enables TLS
(`internal/agent/agent.go:196`) - and the server's `grpcServerOptions` has no credentials option
(`cmd/relay-server/grpc_config.go:68-73`). `discovery.Browse` returns the first `_relay._tcp`
responder (`internal/discovery/mdns.go:13-38`), which `cmd/relay-agent` then dials and hands its
long-lived agent token (or one-shot enrollment token) in `RegisterRequest`
(`cmd/relay-agent/main.go:156`). Any LAN host can advertise the service and collect tokens, and a
passive observer reads them off the wire. README's "Transport Security" section covers only the
HTTP server behind a reverse proxy and is silent on `:9090`.

## Context
The entire epoch-fence and assignee-fence edifice assumes the agent token is secret - a harvested
token confers the worker's full write surface (task status, task logs, inventory). This is
distinct from the deferred [[idea-2026-06-04-cidr-allowlist-auto-enroll]]: that item records
"network reachability" as the accepted trust boundary for who may *become* a worker under
auto-enroll; nothing recorded accepts that any LAN host may *impersonate the server* and collect
existing workers' credentials. The two halves (transport encryption; discovery authentication) are
separable - TLS with server verification closes both, since a spoofed advertiser then fails the
handshake before the token is sent.

## Proposal
Optional TLS on the gRPC listener (server cert + `RELAY_GRPC_TLS_CERT/KEY`), with the agent
gaining a corresponding root-of-trust setting and refusing to send credentials over plaintext
unless explicitly opted in (`RELAY_AGENT_INSECURE=1` for dev). Discovery then either carries no
trust at all (just a hint the TLS handshake validates) or is documented as untrusted input.

## Acceptance / Done When
- The agent can dial with TLS and verify the server before sending any token; plaintext requires
  an explicit opt-in flag rather than being the silent default.
- README's transport-security section covers `:9090` and states the discovery trust model
  explicitly.
- The mDNS path is either validated by the TLS handshake or documented as a hint only.

## Related
- `internal/agent/agent.go:196,357-362`, `internal/discovery/mdns.go:13-38`, `cmd/relay-agent/main.go:156`, `cmd/relay-server/grpc_config.go:68-73`
- [[idea-2026-06-04-cidr-allowlist-auto-enroll]] (Deferred) - adjacent but distinct trust decision
- [[bug-2026-08-12-auto-enroll-hostname-takeover]] - what a harvested or forged registration can seize
