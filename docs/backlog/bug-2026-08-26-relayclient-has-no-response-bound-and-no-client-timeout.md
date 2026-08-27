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

## Update 2026-08-27 - the Python SDK has the identical hole, now with numbers

`python/src/relay/client.py` buffers every response through `response.json()` with no size bound,
and its `_MAX_LOG_PAGES = 10000` bounds the request COUNT only. Measured against httpx 0.28.1, so
the appending session does not have to re-derive them:

- **No wall-clock bound.** httpx's `timeout` is per socket read, not per request. Against a real
  trickling socket server, one request completed in **14.3 s under a 0.5 s read timeout** (29x).
  Then multiply by 10,000 sequential pages.
- **No byte bound.** httpx sends `accept-encoding: gzip, deflate` by default and decodes without a
  bound: **89 KiB on the wire materialised as 31 MB**, a 343x ratio. Per page.
- **Memory.** Roughly **0.5-1 KB retained per LogRecord**, so a benign 2,000,000-row log walk
  retains well over a gigabyte before the call returns.

**And the remedy has to be chosen carefully, because the obvious one does not exist.** The Python
README initially told an operator to bound the first two axes with `Client(timeout=)` or an
injected `http_client`. That is false and has been corrected: `httpx.Timeout` has exactly four
axes (`connect`, `read`, `write`, `pool`), there is no total-time setting and no response-size
setting anywhere in httpx, and a per-read bound is exactly what the 14.3 s measurement defeats.
Closing either axis needs a caller-supplied `httpx.BaseTransport` wrapper or an out-of-band
deadline. Whatever this item settles for Go, the Python half must not repeat that prescription.

The right Python shape is one `_read_json(response)` chokepoint - the analogue of CLAUDE.md's
single JSON entry point invariant, which the SDK currently drifts from at 13 sites. That is the
same chokepoint [[bug-2026-08-27-python-sdk-exceptions-escape-the-relayerror-hierarchy]] needs, so
the two should probably be done together.

Add to Related: `python/src/relay/client.py`;
[[bug-2026-08-27-python-sdk-exceptions-escape-the-relayerror-hierarchy]];
[[bug-2026-08-27-python-sdk-fetch-all-has-no-termination-stops]].
