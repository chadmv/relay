---
title: relayclient bounds no response body and sets no http.Client timeout, so one server can hang or exhaust every CLI and MCP command
type: bug
status: open
created: 2026-08-26
priority: low
source: Phase 4 security lens of the 2026-08-26-relay-logs-envelope-drift slice; scoped out because the slice touched no client-layer code
---

# relayclient bounds no response body and sets no http.Client timeout, so one server can hang or exhaust every CLI and MCP command

## Summary

`internal/relayclient/client.go` has two missing bounds at the layer every non-web Go client shares:

```go
func NewClient(serverURL, token string) *Client {
	return &Client{base: ..., token: token, http: &http.Client{}}
}
```

- **No `Timeout`.** `http.Client{}`'s zero value is no timeout at all. A server that accepts the
  connection and then writes nothing holds the request until the caller's context expires - and
  `cmd/relay` gives its commands **no deadline**, so for the CLI that is "forever". A slowloris
  server, or a wedged one, wedges the operator's terminal with no diagnostic.
- **No response-size bound.** `Do` ends in `json.NewDecoder(resp.Body).Decode(out)` with no
  `io.LimitReader` or `http.MaxBytesReader`, so a single response is unbounded and is fully buffered
  by the decoder into the target value. The 4xx path is the same: it decodes an unbounded error body.

Neither is reachable by an ordinary caller, because the server relay talks to is relay - which is
exactly why this is `low`. It is reachable by anything that can point a client at a URL: a
misconfigured `~/.relay/config.json`, an mDNS discovery result
([[bug-2026-08-23-agent-grpc-plaintext-mdns-first-advertiser]] is the standing item on trusting the
first advertiser), or a compromised or partitioned server.

## Context

**Fixing it at this layer bounds every command at once**, which is the whole argument for the item.
`relayclient` is the single HTTP entry point for `internal/cli`, `internal/mcp`, and
`internal/discovery`'s follow-up calls - the client-side analogue of the project's single-JSON-entry-point
invariant, where request-size limits and decode policy live at the boundary rather than at call
sites. There are dozens of `c.Do` call sites and none of them can sensibly own this.

Two adjacent facts, so the scope is not over-drawn:

- `StreamEvents` **is** partly bounded already: `scanner.Buffer(..., 1<<20)` caps a single SSE frame
  at 1 MiB, with a comment explaining the ~192 KiB worst case. That is a per-line bound on one
  method, not a per-response bound, and it does not help `Do`.
- The 2026-08-26 envelope-drift slice bounded the **paging loop** (`maxLogPages`, a non-advancing
  cursor guard) precisely because a server-supplied cursor drives a client loop. That closes
  unbounded *iteration*; it does nothing about an unbounded *single response*, and the two were
  explicitly separated in that slice's threat-model section.

## Proposal

1. `http.Client{Timeout: ...}` in `NewClient`. The value needs a decision rather than a default:
   relay's operational timeouts are env-configurable and generous elsewhere because P4 workspaces
   can be 1 TB+, but **no `relayclient` call is a bulk transfer** - the largest is one 200-row log
   page. A per-request timeout in the tens of seconds is defensible; check `StreamEvents` first,
   because `http.Client.Timeout` covers the whole body read and would kill a healthy long-lived SSE
   subscription. That almost certainly means a timeout on the `Do` path only, or a
   `*http.Transport` with `ResponseHeaderTimeout` so headers are bounded and the body is not.
   **Say which, and why, in the code.**
2. Wrap `resp.Body` in an `io.LimitReader` before decoding, on both the success and the error path.
   Size it from the largest legitimate response - a full log page, or a jobs list at
   `PageRequestLimit` - with headroom, and return a distinguishable error when the limit is hit so
   the operator is told "response too large" rather than a decode error.

## Acceptance / Done When

- A test server that accepts and then writes nothing causes a `Do` call to return an error in
  bounded time, with no context deadline supplied by the test.
- A test server that streams an oversized body causes `Do` to return a named error rather than
  growing memory, proven by asserting the error and the bytes read.
- `StreamEvents` against a healthy long-lived subscription is **unaffected** - a test holds a
  subscription open past whatever timeout is chosen. This is the assertion that stops the fix
  breaking `relay logs`.
- The 4xx error-body decode is bounded too, not just the success path.
- The chosen numbers and the `Do`-versus-`StreamEvents` split are justified in a comment.

## Related

- `internal/relayclient/client.go` (`NewClient`, `Do`, `StreamEvents`)
- The iteration bound that is NOT this: `internal/cli/logs.go` (`maxLogPages`), and
  `docs/superpowers/specs/2026-08-26-relay-logs-envelope-drift.md` "Load, failure modes, threat model"
- The server-side analogue this mirrors: `readJSON` in `internal/api/server.go`
- Why pointing at an untrusted server is not purely hypothetical:
  [[bug-2026-08-23-agent-grpc-plaintext-mdns-first-advertiser]]
