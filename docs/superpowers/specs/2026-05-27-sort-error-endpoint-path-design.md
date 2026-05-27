# Include endpoint path in SortSpec "unsupported sort key" error

**Date:** 2026-05-27
**Status:** Design
**Backlog item:** [idea-2026-05-27-sort-error-message-endpoint-path](../../backlog/idea-2026-05-27-sort-error-message-endpoint-path.md)
**Related retro:** [2026-05-27-list-endpoint-sort](../../retros/2026-05-27-list-endpoint-sort.md)

## Problem

The list-endpoint-sort feature shipped a 400 error of the form:

```
unsupported sort key 'labels'; supported: created_at, name
```

The spec for that feature described it as:

```
unsupported sort key 'labels' for /v1/jobs; supported: created_at, name
```

The endpoint path was dropped because `parseSort` (in `internal/api/pagination.go`) is a shared utility that doesn't receive the request — it only sees the raw sort value and the `SortSpec`. A developer hitting multiple list endpoints in one session (jobs, tasks, workers, ...) reading a terminal full of 400s cannot tell at a glance which endpoint rejected the key.

## Goal

Restore the endpoint path in the HTTP 400 response body for unknown `?sort=` values, without coupling `SortSpec` to the request URL.

## Non-goals

- Changing the error wording or shape for any other validation failure (bad cursor, invalid limit, illegal sort syntax, cursor/sort mismatch).
- Touching per-endpoint `SortSpec` declarations.
- Changing CLI or Python SDK error rendering.
- Adding the path to logs or metrics that already have the request context elsewhere.

## Design

### Approach: format the error at the HTTP boundary

`parsePage` has the `*http.Request` and can read `r.URL.Path`. `parseSort` does not, and shouldn't — it's a pure validator over `(raw string, SortSpec)`. The path is a property of the call site, not the spec.

To let `parsePage` distinguish "unknown sort key" (where the path is useful) from other validation errors (where it isn't), introduce a small typed error in `internal/api/pagination.go`:

```go
type unsupportedSortKeyError struct {
    Key     string
    Allowed []string // sorted, same order as today's message
}

func (e *unsupportedSortKeyError) Error() string {
    return fmt.Sprintf("unsupported sort key '%s'; supported: %s",
        e.Key, strings.Join(e.Allowed, ", "))
}
```

`parseSort` returns `*unsupportedSortKeyError` for the unknown-key branch (the `!ok` case at pagination.go:179-187). Its other error branches (`invalid sort %q` for empty or syntactically illegal input) remain plain `fmt.Errorf` — those paths don't benefit from the endpoint path, and rewording them is out of scope.

`parsePage` does an `errors.As` after the `parseSort` call and reformats only the typed-error case:

```go
canon, kind, err := parseSort(sortRaw, spec)
if err != nil {
    var uke *unsupportedSortKeyError
    if errors.As(err, &uke) {
        writeError(w, http.StatusBadRequest, fmt.Sprintf(
            "unsupported sort key '%s' for %s; supported: %s",
            uke.Key, r.URL.Path, strings.Join(uke.Allowed, ", ")))
    } else {
        writeError(w, http.StatusBadRequest, err.Error())
    }
    return pageParams{}, false
}
```

### Why a typed error rather than string-matching

String-prefix matching ("does this error start with `unsupported sort key`?") would be a hidden contract — the next person to reword the message in `parseSort` would silently break the path-enrichment in `parsePage`. `errors.As` is an explicit, compiler-enforced contract.

### Why parseSort's `.Error()` keeps the original wording

The existing unit test `TestParseSort_UnknownKey` (pagination_test.go:333) asserts on `err.Error()` directly, with no HTTP request in scope. Keeping `(*unsupportedSortKeyError).Error()` path-free means:

- `parseSort` remains a pure validator, testable without an `*http.Request`.
- The path-aware message is composed at the HTTP boundary where the path actually exists.
- The acceptance criterion - "existing tests that match the error message via `Contains(..., "unsupported sort key 'X'")` still pass" - holds for both `parseSort` unit tests and `parsePage` HTTP tests.

## Files changed

- `internal/api/pagination.go` — add `unsupportedSortKeyError`; rewire `parseSort` and `parsePage`.
- `internal/api/pagination_test.go` — tighten `TestParsePage_UnknownSortKey_400` to also assert the path appears.
- `docs/backlog/idea-2026-05-27-sort-error-message-endpoint-path.md` — `git mv` to `docs/backlog/closed/`.

Per-endpoint integration tests (`internal/api/*_sort_integration_test.go`) are unchanged: they assert with `Contains(..., "unsupported sort key 'X'")`, which the new message still satisfies.

## Test plan

1. **Existing tests pass unchanged:**
   - `TestParseSort_UnknownKey` — `err.Error()` still contains `"unsupported sort key 'labels'"`, `"created_at"`, `"name"`.
   - All `*_sort_integration_test.go` files that touch the unsupported-key path.

2. **New assertion in `TestParsePage_UnknownSortKey_400`:**
   ```go
   assert.Contains(t, w.Body.String(), "unsupported sort key 'labels'")
   assert.Contains(t, w.Body.String(), "for /v1/jobs")
   assert.Contains(t, w.Body.String(), "created_at")
   ```

3. **Manual sanity:** none required — fully covered by unit tests.

## Acceptance criteria

- The 400 response body for an invalid `?sort=` key contains the request path between the key name and the `; supported:` clause.
- `TestParseSort_UnknownKey` and all integration tests asserting `Contains(..., "unsupported sort key 'X'")` pass unchanged.
- `go test ./internal/api/...` is green.
- Backlog item file moved to `docs/backlog/closed/` in the same commit/branch.

## Rollout

Single PR. No migration. The change is an error-message refinement on a feature that's been in `master` for one day; no clients depend on the exact wording.
