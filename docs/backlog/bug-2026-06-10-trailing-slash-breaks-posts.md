---
title: Trailing slash in the server URL breaks every POST/PATCH/DELETE
type: bug
status: open
created: 2026-06-10
priority: medium
source: full-codebase review (2026-06-10)
---

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
