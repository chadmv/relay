---
title: Trailing slash in the server URL breaks every POST/PATCH/DELETE
type: bug
status: closed
created: 2026-06-10
closed: 2026-06-20
priority: medium
source: full-codebase review (2026-06-10)
---

## Resolution
Resolved 2026-06-20. `relayclient.NewClient` now normalizes the base URL once with
`strings.TrimRight(serverURL, "/")` (`internal/relayclient/client.go:33`), so a
trailing slash in `RELAY_URL` no longer produces `//v1/...` (which the server
301-redirected, downgrading the POST to a body-less GET and 405ing). Normalization
stays at the single constructor chokepoint - all callers (`login`, `register`,
`mcp/server`, and `cfg.NewClient` for every CLI subcommand) benefit. Covered by a
table test (no/single/multiple trailing slashes) plus a behavioral test that POSTs
through a trailing-slash base and asserts the clean path is reached and the method
stays POST (not downgraded to GET via a redirect).

# Trailing slash in the server URL breaks every POST/PATCH/DELETE

## Summary
`relayclient.NewClient` stores the URL verbatim and `Do` concatenates `c.base+path`. With `ServerURL = "http://host:8080/"` (an extremely likely thing to type at the `relay login` prompt or set in `RELAY_URL`), requests go to `//v1/...`. The server's `http.NewServeMux` 301-redirects unclean paths, and Go's `http.Client` follows a 301 on POST by converting it to a body-less GET, which then 405s. Net effect: `relay login` against a trailing-slash URL fails with an opaque `request failed (405)`.

## Proposal
Normalize once in the constructor:

```go
func NewClient(serverURL, token string) *Client {
    return &Client{base: strings.TrimRight(serverURL, "/"), token: token, http: &http.Client{}}
}
```

## Related
- `internal/relayclient/client.go:33, 51`
- `internal/cli/login.go:49-54`
