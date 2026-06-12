# Design: Request Body Size Limit

- Date: 2026-06-11
- Backlog item: `docs/backlog/bug-2026-06-10-no-request-body-limit.md`
- Status: approved

## Problem

`readJSON` in `internal/api/server.go` is `json.NewDecoder(r.Body).Decode(v)`
with no `http.MaxBytesReader`. No `MaxBytesReader`/`LimitReader` exists anywhere
in the server. Every handler - including the unauthenticated `POST
/v1/auth/register` and `POST /v1/auth/login` - will buffer an arbitrarily large
JSON value into memory. The per-IP rate limit (10/min) does not bound
per-request size, so a handful of multi-GB bodies can exhaust server memory.

`readJSON` is the single JSON entry point: in `internal/api`, the only
production request-body read goes through it (verified - every other
`r.Body`/`json.NewDecoder` reference is test code or client-side response
decoding in `relayclient`/`mcp`). Fixing `readJSON` therefore covers all 15
handlers, including the MCP write tools, which reach the API over HTTP through
this same path.

## Decisions

- **Limit: 1 MiB, universal.** A single cap for every endpoint keeps the policy
  in one place and honors the single-entry-point invariant with no per-route
  config. At ~200-500 bytes per task spec, 1 MiB leaves a several-thousand-task
  ceiling for job/scheduled-job creation - ample for real workloads.
- **Status: 413 for an oversize body, 400 otherwise.** Detect
  `*http.MaxBytesError` and return `413 Request Entity Too Large`; all other
  decode errors stay `400`. More correct and more diagnosable than collapsing
  everything to 400.

## Change

`readJSON` cannot enforce a limit today because `http.MaxBytesReader` needs the
`ResponseWriter`. Change the signature to `readJSON(w, r, v) bool` and have it
write the error response itself:

```go
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

### Why a `bool` that writes its own response (not an `error` return)

The invariant requires the size limit *and* decode policy to live in
`server.go`, "not at call sites." If `readJSON` returned an `error`, all 15 call
sites would have to re-implement the 413-vs-400 mapping, re-fragmenting the
policy. With this shape a call site cannot pick the wrong status.

### Call sites (all 15)

Each site already changes because of the new `w` parameter. The pattern goes
from:

```go
if err := readJSON(r, &req); err != nil {
	writeError(w, http.StatusBadRequest, "invalid JSON")
	return
}
```

to:

```go
if !readJSON(w, r, &req) {
	return
}
```

This unifies the per-site decode-error messages ("invalid JSON", "invalid
request body", etc.) into a single "invalid request body". Approved as a minor
consistency win.

Call sites (from grep `readJSON(`):

- `internal/api/jobs.go:173`
- `internal/api/invites.go:27`
- `internal/api/auth.go:62, 237, 283, 367`
- `internal/api/agent_enrollments.go:27`
- `internal/api/reservations.go:239`
- `internal/api/scheduled_jobs.go:76, 539`
- `internal/api/users.go:58, 575`
- `internal/api/workers.go:387`

### Behavior preserved

Empty-body requests already get 400 today (`Decode` returns `io.EOF`) and still
will after the change.

## Testing (TDD, non-integration)

New `internal/api/server_test.go` (no build tag, runs under `make test`)
calling `readJSON` directly with `httptest` - no DB needed, since decode failure
precedes any handler logic:

- Oversize body (> 1 MiB) -> returns `false`, response code `413`.
- Malformed JSON -> returns `false`, response code `400`.
- Valid JSON -> returns `true`, value decoded correctly.

## Out of scope

- Per-endpoint limits (single universal cap chosen).
- Streaming/chunked upload handling (no such endpoints exist).
- gRPC message size limits (separate transport, not HTTP bodies).
