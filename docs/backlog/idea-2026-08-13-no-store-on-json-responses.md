---
title: JSON responses carry no Cache-Control, including the ones that list invitee emails and session inventory
type: idea
status: open
created: 2026-08-13
priority: low
source: Phase 6 triage of the 2026-08-13-web-enabler-list-endpoints slice; pre-existing global pattern
---

# JSON responses carry no Cache-Control

## Summary

`writeJSON` (`internal/api/server.go:186-190`) sets `Content-Type` and nothing else. Every JSON
response in the API - every list, every detail, every error, since `writeError` delegates to it
(`:192-194`) - goes out with no `Cache-Control`, no `Pragma`, no `Expires`, no `ETag` and no
`Last-Modified`. The only cache directive anywhere in the API is the SSE `no-cache` at
`internal/api/events.go:56`.

Adding `w.Header().Set("Cache-Control", "no-store")` at that one chokepoint would cover the whole
surface, because `writeJSON` is the single JSON exit point in the same way `readJSON` is the single
entry point.

## Context

**Pre-existing and global.** Not introduced by any recent slice. It is filed now because the
2026-08-13 slice added the two responses for which the argument is easiest to make concretely:
`GET /v1/invites` returns invitee email addresses to an admin, and `GET /v1/auth/tokens` returns a
user's session inventory. Neither returns a credential - the invite token exists in plaintext only
in the `POST` 201 body, and a session row id is not a credential
(`internal/store/query/tokens.sql:49-55` and the spec's threat model) - so this is defense in depth,
not a live disclosure.

**The honest weight of the argument, stated so triage is cheap:**

- Responses have no validator (`Last-Modified` / `ETag`), so under RFC 9111 heuristic freshness there
  is nothing for a shared cache to compute a lifetime from, and the practical risk from intermediary
  caches is small.
- The consumers are `fetch` calls from the SPA whose results are rendered from JavaScript state, so a
  back-button navigation re-renders from the app rather than from a cached body.
- The residual case is a shared-profile browser's on-disk cache retaining an authenticated response
  after sign-out, and any future intermediary (a corporate proxy, a CDN placed in front of the API)
  that decides to store an uncontrolled response.

The mismatch worth noting is that the API already fails **closed** on the adjacent question - `CORS`
rejects a wildcard origin (`internal/api/cors.go`) - so leaving the caching question entirely
unstated is the odd one out among the response-level policies.

## Proposal

One line in `writeJSON`:

```go
w.Header().Set("Cache-Control", "no-store")
```

`no-store` rather than `no-cache`: `no-cache` permits storage and requires revalidation, and there is
no validator to revalidate against. Decide explicitly whether the SSE handler keeps its own
`no-cache` (`internal/api/events.go:56`) or moves to the same value; it does not route through
`writeJSON`, so it is unaffected either way.

Two things to check before landing it, both cheap:

1. **The static SPA handler must not be affected.** `s.StaticHandler` at
   `internal/api/server.go:178` serves the built assets and wants ordinary caching; it does not call
   `writeJSON`, so it should be untouched, but assert that rather than assume it.
2. **Nothing downstream relies on JSON responses being cacheable.** The CLI and MCP clients issue
   plain requests with no conditional headers, so this is expected to be a no-op for them - confirm
   by grep rather than by reasoning.

## Acceptance / Done When

- Every JSON response, success and error, carries `Cache-Control: no-store`, asserted once at
  `writeJSON`'s level and once end to end through a real handler so the header is proven to reach the
  wire.
- The static asset path is asserted **not** to carry it.
- The SSE endpoint's own directive is either kept deliberately or aligned, with the choice stated.

## Related

- Source: `internal/api/server.go:186-190` (`writeJSON`, the single JSON exit point), `:192-194`
  (`writeError`), `:178` (the static handler), `internal/api/events.go:56` (the only existing cache
  directive)
- Triaged in `docs/retros/2026-08-13-web-enabler-list-endpoints.md`
- The responses that prompted it: `docs/superpowers/specs/2026-08-13-web-enabler-list-endpoints.md`
  ("Security and system design")

## Notes

**This item states its own rejection case, because the argued harm is thin.** Reject it if the
position is that a bearer-token JSON API consumed by `fetch` has no realistic cache exposure and the
header is cargo cult. Accept it if the position is that a one-line default at an existing chokepoint
is worth having before a proxy or CDN is ever placed in front of `relay-server`, at which point the
decision is somebody else's and is made by omission.
