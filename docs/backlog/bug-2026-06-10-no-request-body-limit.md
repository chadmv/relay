---
title: No request body size limit on any endpoint, including unauthenticated ones
type: bug
status: open
created: 2026-06-10
priority: high
source: full-codebase review (2026-06-10)
---

# No request body size limit on any endpoint, including unauthenticated ones

## Summary
`readJSON` is `json.NewDecoder(r.Body).Decode(v)` with no `http.MaxBytesReader`, and no `MaxBytesReader`/`LimitReader` exists anywhere in the repo. Every handler, including unauthenticated `POST /v1/auth/register` and `/login`, will buffer an arbitrarily large JSON string value into memory. The 10/min-per-IP rate limit does not bound per-request size, so a handful of multi-GB bodies can exhaust server memory.

## Proposal
Fix in one place:

```go
func readJSON(w http.ResponseWriter, r *http.Request, v any) error {
    r.Body = http.MaxBytesReader(w, r.Body, 1<<20) // 1 MiB
    return json.NewDecoder(r.Body).Decode(v)
}
```

Consider distinguishing `*http.MaxBytesError` to return 413 instead of 400.

## Related
- `internal/api/server.go:179-181` (`readJSON`)
- `internal/api/server.go:81-93` (unauthenticated routes)
