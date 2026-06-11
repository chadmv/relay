# Request Body Size Limit Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Enforce a 1 MiB request body size limit on every HTTP endpoint by adding `http.MaxBytesReader` to the single JSON entry point `readJSON`, returning 413 for oversize bodies and 400 for malformed JSON.

**Architecture:** `readJSON` in `internal/api/server.go` is the only production request-body reader (the single JSON entry-point invariant). Change its signature to `readJSON(w, r, v) bool` so it can install `http.MaxBytesReader` (which needs the `ResponseWriter`) and write its own error response, centralizing the size limit and the 413-vs-400 status policy. Every call site is updated to the new `if !readJSON(...) { return }` shape.

**Tech Stack:** Go, `net/http` (`http.MaxBytesReader`, `*http.MaxBytesError`), `encoding/json`. Tests are stdlib-only (`net/http/httptest`, `testing`) matching the existing `internal/api/server_test.go`.

**Reference:** Design spec at `docs/superpowers/specs/2026-06-11-request-body-size-limit-design.md`.

---

### Task 1: Failing tests for `readJSON`

**Files:**
- Modify: `internal/api/server_test.go` (append tests; existing file is `package api`, no build tag, stdlib-only assertions)

- [ ] **Step 1: Add the `bytes` import and three tests**

Add `"bytes"` to the import block in `internal/api/server_test.go` (alphabetically first), so the block becomes:

```go
import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)
```

Append these three tests to the end of the file. They call `readJSON` with the
new `(w, r, v) bool` signature, which does not exist yet:

```go
func TestReadJSON_ValidBody_ReturnsTrue(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"name":"alice"}`))
	w := httptest.NewRecorder()
	var v struct {
		Name string `json:"name"`
	}
	if !readJSON(w, r, &v) {
		t.Fatalf("readJSON returned false for valid body; response: %d %s", w.Code, w.Body.String())
	}
	if v.Name != "alice" {
		t.Fatalf("expected name %q, got %q", "alice", v.Name)
	}
}

func TestReadJSON_MalformedBody_Returns400(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{not json`))
	w := httptest.NewRecorder()
	var v map[string]any
	if readJSON(w, r, &v) {
		t.Fatal("expected readJSON to return false for malformed body")
	}
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestReadJSON_OversizeBody_Returns413(t *testing.T) {
	// Body larger than maxBodyBytes (1 MiB).
	big := bytes.Repeat([]byte("a"), (1<<20)+1024)
	body := `{"name":"` + string(big) + `"}`
	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	w := httptest.NewRecorder()
	var v map[string]any
	if readJSON(w, r, &v) {
		t.Fatal("expected readJSON to return false for oversize body")
	}
	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected 413, got %d", w.Code)
	}
}
```

- [ ] **Step 2: Run the tests and verify they fail to compile**

Run: `go test ./internal/api/ -run TestReadJSON -v -timeout 30s`

Expected: build/compile FAILURE - `readJSON` currently has signature
`readJSON(r *http.Request, v any) error`, so calling `readJSON(w, r, &v)` as a
bool does not compile. This is the red state; the package will not compile until
Task 2 updates the signature and all call sites.

- [ ] **Step 3: Commit**

```bash
git add internal/api/server_test.go
git commit -m "test: add failing readJSON body-limit tests"
```

---

### Task 2: Implement the limit in `readJSON` and update all call sites

The signature change breaks every call site, so the package will not compile
until `readJSON` and all 13 call sites are updated together. Make all edits,
then verify.

**Files:**
- Modify: `internal/api/server.go` (add `errors` import; `maxBodyBytes` const; rewrite `readJSON`)
- Modify call sites: `internal/api/agent_enrollments.go:27`, `internal/api/auth.go:62,237,283,367`, `internal/api/jobs.go:173`, `internal/api/invites.go:27`, `internal/api/reservations.go:239`, `internal/api/scheduled_jobs.go:76,539`, `internal/api/users.go:58,575`, `internal/api/workers.go:387`

- [ ] **Step 1: Add the `errors` import to `server.go`**

In `internal/api/server.go`, add `"errors"` to the stdlib import group so it reads:

```go
import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"relay/internal/events"
	"relay/internal/metrics"
	"relay/internal/store"
	"relay/internal/worker"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)
```

- [ ] **Step 2: Rewrite `readJSON` with the limit and status policy**

Replace the current `readJSON` in `internal/api/server.go`:

```go
func readJSON(r *http.Request, v any) error {
	return json.NewDecoder(r.Body).Decode(v)
}
```

with:

```go
// maxBodyBytes caps the size of any JSON request body. It bounds server memory
// against arbitrarily large bodies, including on unauthenticated endpoints.
const maxBodyBytes = 1 << 20 // 1 MiB

// readJSON decodes the request body into v, enforcing maxBodyBytes. On failure
// it writes an error response (413 for an oversize body, 400 for malformed
// JSON) and returns false; callers should simply return.
func readJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			writeError(w, http.StatusRequestEntityTooLarge, "request body too large")
		} else {
			writeError(w, http.StatusBadRequest, "invalid request body")
		}
		return false
	}
	return true
}
```

- [ ] **Step 3: Update the 12 standard call sites**

Each of these currently reads (modulo variable name `req`/`body` and the error
message string):

```go
	if err := readJSON(r, &VAR); err != nil {
		writeError(w, http.StatusBadRequest, "MESSAGE")
		return
	}
```

Replace each with:

```go
	if !readJSON(w, r, &VAR) {
		return
	}
```

Apply to exactly these locations, preserving the existing variable name:

- `internal/api/agent_enrollments.go:27` (`&req`)
- `internal/api/auth.go:62` (`&req`)
- `internal/api/auth.go:237` (`&req`)
- `internal/api/auth.go:283` (`&req`)
- `internal/api/auth.go:367` (`&req`)
- `internal/api/jobs.go:173` (`&req`)
- `internal/api/invites.go:27` (`&req`)
- `internal/api/reservations.go:239` (`&body`)
- `internal/api/scheduled_jobs.go:76` (`&req`)
- `internal/api/scheduled_jobs.go:539` (`&req`)
- `internal/api/users.go:575` (`&req`)
- `internal/api/workers.go:387` (`&body`)

- [ ] **Step 4: Update the one non-standard call site (`users.go:58`)**

`internal/api/users.go:58` is inside a helper that returns `(string, bool)`, so
its early return differs. It currently reads:

```go
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return "", false
	}
```

Replace with:

```go
	if !readJSON(w, r, &req) {
		return "", false
	}
```

- [ ] **Step 5: Verify the tests pass**

Run: `go test ./internal/api/ -run TestReadJSON -v -timeout 30s`

Expected: PASS for `TestReadJSON_ValidBody_ReturnsTrue`,
`TestReadJSON_MalformedBody_Returns400`,
`TestReadJSON_OversizeBody_Returns413`.

- [ ] **Step 6: Verify nothing else broke**

Run: `go build ./...`
Expected: builds with no errors (confirms all call sites were updated).

Run: `go test ./internal/api/ -timeout 120s`
Expected: PASS (existing unit tests still green; integration-tagged tests are not
run without the `integration` tag).

Run: `go vet ./internal/api/`
Expected: no output.

- [ ] **Step 7: Commit**

```bash
git add internal/api/server.go internal/api/agent_enrollments.go internal/api/auth.go internal/api/jobs.go internal/api/invites.go internal/api/reservations.go internal/api/scheduled_jobs.go internal/api/users.go internal/api/workers.go
git commit -m "feat: enforce 1 MiB request body size limit in readJSON"
```

---

### Task 3: Close the backlog item

**Files:**
- Move: `docs/backlog/bug-2026-06-10-no-request-body-limit.md` -> `docs/backlog/closed/bug-2026-06-10-no-request-body-limit.md`

- [ ] **Step 1: Update status front-matter to `closed`**

In `docs/backlog/bug-2026-06-10-no-request-body-limit.md`, change the
front-matter line `status: open` to `status: closed`.

- [ ] **Step 2: Move the file to the closed directory**

```bash
git mv docs/backlog/bug-2026-06-10-no-request-body-limit.md docs/backlog/closed/bug-2026-06-10-no-request-body-limit.md
```

- [ ] **Step 3: Commit**

```bash
git add docs/backlog/closed/bug-2026-06-10-no-request-body-limit.md
git commit -m "backlog: close bug-2026-06-10-no-request-body-limit"
```

---

## Notes for the implementer

- **`*http.MaxBytesError`** (pointer) is the type returned by a `MaxBytesReader`
  over-read; match it with `errors.As`, not a string check. It was added in Go
  1.19.
- **Empty body** still yields 400 (`json.Decode` returns `io.EOF`), unchanged
  from before.
- Do not add a test-override var for `maxBodyBytes`; generating a >1 MiB body in
  the test is cheap and keeps the constant simple (matches CLAUDE.md "no
  speculative configurability").
