# Sort Error Endpoint Path Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Include the request path in the HTTP 400 response body when a client sends an unknown `?sort=` key, so developers debugging across multiple list endpoints can see which endpoint rejected the key.

**Architecture:** `parseSort` returns a typed `*unsupportedSortKeyError` (carrying the bad key and the allowed list) for the unknown-key branch only. `parsePage` does an `errors.As` and composes the path-enriched message at the HTTP boundary where `r.URL.Path` is in scope. `SortSpec` stays purely declarative.

**Tech Stack:** Go 1.x, stdlib `errors.As`, no new dependencies.

**Spec:** [docs/superpowers/specs/2026-05-27-sort-error-endpoint-path-design.md](../specs/2026-05-27-sort-error-endpoint-path-design.md)

---

## File Structure

- **Modify:** `internal/api/pagination.go` — add `unsupportedSortKeyError` type; change one branch of `parseSort` to return it; add `errors.As` reformat in `parsePage`. Single self-contained file edit.
- **Modify:** `internal/api/pagination_test.go` — tighten `TestParsePage_UnknownSortKey_400` with a `for /v1/jobs` assertion. `TestParseSort_UnknownKey` unchanged.
- **Move:** `docs/backlog/idea-2026-05-27-sort-error-message-endpoint-path.md` → `docs/backlog/closed/<same-filename>` (via `git mv`).

No new files. No changes to per-endpoint sort specs or to `internal/api/*_sort_integration_test.go`.

---

### Task 1: Tighten the failing test first

**Files:**
- Modify: `internal/api/pagination_test.go:362-369`

- [ ] **Step 1: Update `TestParsePage_UnknownSortKey_400` to assert the path appears**

Current test (pagination_test.go:362-369):

```go
func TestParsePage_UnknownSortKey_400(t *testing.T) {
	r := httptest.NewRequest("GET", "/v1/jobs?sort=labels", nil)
	w := httptest.NewRecorder()
	_, ok := parsePage(w, r, testDefaultSpec)
	assert.False(t, ok)
	assert.Equal(t, 400, w.Code)
	assert.Contains(t, w.Body.String(), "unsupported sort key 'labels'")
}
```

Replace the body of the test with:

```go
func TestParsePage_UnknownSortKey_400(t *testing.T) {
	r := httptest.NewRequest("GET", "/v1/jobs?sort=labels", nil)
	w := httptest.NewRecorder()
	_, ok := parsePage(w, r, testDefaultSpec)
	assert.False(t, ok)
	assert.Equal(t, 400, w.Code)
	body := w.Body.String()
	assert.Contains(t, body, "unsupported sort key 'labels'")
	assert.Contains(t, body, "for /v1/jobs")
	assert.Contains(t, body, "created_at")
	assert.Contains(t, body, "name")
}
```

The two new assertions (`"for /v1/jobs"` and the explicit `"name"` allowed-key check) are what drive the implementation. The `"created_at"` assertion is added for symmetry; the existing message already contains it, so it's just locking the contract in place.

- [ ] **Step 2: Run the test and confirm it fails on the new assertion**

Run:

```powershell
go test ./internal/api/... -run TestParsePage_UnknownSortKey_400 -v -timeout 30s
```

Expected: FAIL. Output should show the `assert.Contains` failure with the actual body lacking `"for /v1/jobs"`. The other assertions (`unsupported sort key 'labels'`, `created_at`, `name`) pass; only the path assertion fails.

If the test passes already, stop — something is wrong. Do not proceed.

- [ ] **Step 3: Do NOT commit yet**

The implementation in Task 2 makes this test pass. Tests and implementation land in a single commit at the end of Task 2.

---

### Task 2: Add typed error and rewire `parseSort` + `parsePage`

**Files:**
- Modify: `internal/api/pagination.go:179-189` (`parseSort` unknown-key branch)
- Modify: `internal/api/pagination.go:237-242` (`parsePage` sort-error handling)
- Modify: `internal/api/pagination.go:1-15` (imports — `errors` may need to be confirmed)

- [ ] **Step 1: Confirm `errors` is already imported**

The file already imports `errors` (pagination.go:6). No import change needed. If a future edit removed it, add `"errors"` back to the import block.

- [ ] **Step 2: Add the typed error definition**

Insert after the existing `errBadCursor` declaration (pagination.go:17), or grouped just above `parseSort` — choose the second location to keep the type next to its only producer/consumer:

Add immediately before `// parseSort validates ...` at pagination.go:159:

```go
// unsupportedSortKeyError is returned by parseSort when the requested sort
// key isn't in the spec's allowlist. parsePage reformats it with the
// request path; other callers (and parseSort's own unit tests) read the
// path-free Error() form.
type unsupportedSortKeyError struct {
	Key     string
	Allowed []string // sorted; same order as the wire message
}

func (e *unsupportedSortKeyError) Error() string {
	return fmt.Sprintf("unsupported sort key '%s'; supported: %s",
		e.Key, strings.Join(e.Allowed, ", "))
}
```

- [ ] **Step 3: Update `parseSort` to return the typed error**

Replace the existing unknown-key branch at pagination.go:179-187:

```go
	k, ok := spec.Keys[key]
	if !ok {
		allowed := make([]string, 0, len(spec.Keys))
		for k := range spec.Keys {
			allowed = append(allowed, k)
		}
		sort.Strings(allowed)
		return "", 0, fmt.Errorf("unsupported sort key '%s'; supported: %s", key, strings.Join(allowed, ", "))
	}
```

With:

```go
	k, ok := spec.Keys[key]
	if !ok {
		allowed := make([]string, 0, len(spec.Keys))
		for k := range spec.Keys {
			allowed = append(allowed, k)
		}
		sort.Strings(allowed)
		return "", 0, &unsupportedSortKeyError{Key: key, Allowed: allowed}
	}
```

The other error branches in `parseSort` (the empty-key and illegal-char paths, both using `fmt.Errorf("invalid sort %q", raw)`) are unchanged — the endpoint path doesn't add value there and rewording them is out of scope.

- [ ] **Step 4: Update `parsePage` to format the path-aware message**

Replace the sort-error handling at pagination.go:237-242:

```go
	sortRaw := r.URL.Query().Get("sort")
	canon, kind, err := parseSort(sortRaw, spec)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return pageParams{}, false
	}
```

With:

```go
	sortRaw := r.URL.Query().Get("sort")
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

- [ ] **Step 5: Run the originally-failing test and confirm it passes**

Run:

```powershell
go test ./internal/api/... -run TestParsePage_UnknownSortKey_400 -v -timeout 30s
```

Expected: PASS. The body now reads `unsupported sort key 'labels' for /v1/jobs; supported: created_at, name`, satisfying all four `Contains` assertions.

- [ ] **Step 6: Run `parseSort`'s own unit test to confirm path-free wording still holds**

Run:

```powershell
go test ./internal/api/... -run TestParseSort_UnknownKey -v -timeout 30s
```

Expected: PASS. `(*unsupportedSortKeyError).Error()` produces the same string `parseSort` used to format inline, so the existing `Contains(..., "unsupported sort key 'labels'")`, `Contains(..., "created_at")`, `Contains(..., "name")` assertions in pagination_test.go:340-342 still hold.

- [ ] **Step 7: Run the full `internal/api` unit test suite**

Run:

```powershell
go test ./internal/api/... -v -timeout 60s
```

Expected: PASS for every test. Any failure here is a regression — stop and investigate before proceeding.

- [ ] **Step 8: Commit the test + implementation together**

```powershell
git add internal/api/pagination.go internal/api/pagination_test.go
git commit -m "feat(api): include endpoint path in unsupported sort key 400

parseSort now returns a typed *unsupportedSortKeyError for the unknown-key
branch. parsePage uses errors.As to compose the path-aware message at the
HTTP boundary, where r.URL.Path is in scope. SortSpec stays declarative.

The path-free Error() form is preserved so parseSort remains testable in
isolation and existing Contains(..., \"unsupported sort key 'X'\")
assertions in unit and integration tests still hold.

Closes idea-2026-05-27-sort-error-message-endpoint-path.

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>"
```

---

### Task 3: Run integration tests that exercise the unsupported-key path

**Files:** none modified — verification only.

The per-endpoint `*_sort_integration_test.go` files assert `Contains(..., "unsupported sort key 'X'")`, which the new message still satisfies. This task confirms that empirically rather than by inspection.

- [ ] **Step 1: Identify which integration files assert on the message**

Run:

```powershell
go test -tags integration -list '.*' ./internal/api/... 2>&1 | Select-String -Pattern 'sort' -SimpleMatch
```

Expected: a list of `Test*Sort*` integration test names. No assertion needed — this is informational so the next step's `-run` filter is well-scoped.

- [ ] **Step 2: Run the sort-related integration tests**

Requires Docker Desktop running. If Docker is not available in the worktree environment, skip this task and note it in the handoff to the reviewer.

Run:

```powershell
go test -tags integration -p 1 ./internal/api/... -run Sort -v -timeout 180s
```

Expected: PASS for every test. The unsupported-key assertions inside these tests still match because the new HTTP body contains the old substring `"unsupported sort key 'X'"` as well as the new path fragment.

If a test fails because it asserts the exact full body (e.g., `assert.Equal(t, body, "unsupported sort key 'X'; supported: ...")` rather than `Contains`), it's a stricter assertion than the spec's acceptance criterion called out. Convert that single assertion from `Equal` to `Contains` in a follow-up step and amend the Task 2 commit (`git add <file> && git commit --amend --no-edit`). Do not loosen any other assertions in the same file.

- [ ] **Step 3: No commit**

This task is verification; nothing changed unless step 2 fell through to the amend path.

---

### Task 4: Close the backlog item

**Files:**
- Move: `docs/backlog/idea-2026-05-27-sort-error-message-endpoint-path.md` → `docs/backlog/closed/idea-2026-05-27-sort-error-message-endpoint-path.md`

- [ ] **Step 1: `git mv` the backlog file into `closed/`**

Run:

```powershell
git mv docs/backlog/idea-2026-05-27-sort-error-message-endpoint-path.md docs/backlog/closed/idea-2026-05-27-sort-error-message-endpoint-path.md
```

- [ ] **Step 2: Confirm the move registered as a rename**

Run:

```powershell
git status
```

Expected: a single `renamed:` line for the backlog file, no other changes.

- [ ] **Step 3: Commit**

```powershell
git commit -m "chore(backlog): close sort error endpoint path idea

Implemented in the preceding commit.

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>"
```

---

### Task 5: Final verification

**Files:** none modified.

- [ ] **Step 1: Re-run the api unit suite end to end**

Run:

```powershell
go test ./internal/api/... -timeout 60s
```

Expected: `ok` for the package, no failures.

- [ ] **Step 2: Confirm the branch contains exactly two new commits beyond the spec commit**

Run:

```powershell
git log --oneline master..HEAD
```

Expected output (top to bottom):

```
<sha> chore(backlog): close sort error endpoint path idea
<sha> feat(api): include endpoint path in unsupported sort key 400
<sha> spec: include endpoint path in SortSpec unsupported sort key error
```

If there are extra commits, surface them to the user before pushing.

- [ ] **Step 3: Hand off**

The branch is ready for review. No PR creation in this plan — that's a separate decision once the user reviews the diff.

---

## Self-review notes

- **Spec coverage:** Every spec section maps to a task. Goal → Tasks 1+2. Test plan → Tasks 1, 2, 3. Acceptance criteria → Tasks 1 (path assertion), 2 (test passes), 3 (integration still green), 4 (backlog moved), 5 (full suite). Non-goals respected: no edits to per-endpoint specs, CLI, SDK, or any other validation message.
- **Placeholder scan:** no TBD/TODO/"add appropriate". Every code block is the literal text to write or replace.
- **Type consistency:** `unsupportedSortKeyError` type, `Key`/`Allowed` fields, and `errors.As` target name `uke` are consistent across Task 2 steps and match the spec verbatim.
