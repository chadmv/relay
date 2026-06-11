---
date: 2026-06-11
topic: request-body-size-limit
branch: claude/eager-bartik-f95c8f
range: 6ac89dfccb96cccf2ba335fb0c02e7d06ac95dda..dd96fe9e62e9f5a16c4686a3fb353488cd40349f
---

# Session Retro: 2026-06-11 - Request Body Size Limit

**TL;DR:** Added a 1 MiB `http.MaxBytesReader` cap at the single JSON entry point `readJSON` (now `readJSON(w, r, v) bool`), returning 413 for oversize bodies and 400 for malformed JSON, with all 13 call sites updated; closed `bug-2026-06-10-no-request-body-limit`.

## What Was Built

Fixed the high-priority backlog bug "No request body size limit on any endpoint,
including unauthenticated ones" end-to-end through the full brainstorm -> spec ->
plan -> subagent-driven-development flow.

The single behavioral change is in `internal/api/server.go`: `readJSON` now
installs `http.MaxBytesReader(w, r.Body, 1<<20)` before decoding. Because
`MaxBytesReader` needs the `ResponseWriter`, the signature changed from
`readJSON(r, v) error` to `readJSON(w, r, v) bool`, and the helper now writes its
own error response - **413** "request body too large" when it detects
`*http.MaxBytesError` via `errors.As`, **400** "invalid request body" for any
other decode error. All 13 production call sites collapsed to
`if !readJSON(w, r, &req) { return }` (the `parseUpdateUserRequest` helper uses
`return "", false`).

Three stdlib-style unit tests in `server_test.go` (no build tag, runs under
`make test`) cover the happy path, a real 400 on malformed JSON, and a real 413
on a body just over 1 MiB.

## Key Decisions

- **Limit lives in `readJSON`, not at call sites.** The bool-returning,
  self-writing shape means a call site *cannot* pick the wrong status code -
  this is what enforces the "single JSON entry point" invariant (size limit and
  decode policy live in one place). Returning an `error` would have forced all
  13 sites to re-implement the 413-vs-400 mapping.
- **1 MiB, universal.** One cap for every endpoint, no per-route config. At
  ~200-500 bytes per task spec that still leaves a several-thousand-task ceiling
  for job/scheduled-job creation. User confirmed both this and the 413/400 split
  via the design questions.
- **Unified the decode-error messages.** Centralizing in `readJSON` collapsed
  the per-site "invalid JSON" / "invalid request body" variants into one
  message. Accepted as a minor consistency win rather than threading a custom
  message through the helper.
- **No test-override var for the limit.** Generating a >1 MiB body in the test
  is cheap and deterministic, so `maxBodyBytes` stays a plain const (matches
  CLAUDE.md "no speculative configurability").

## Problems Encountered

- None of note. `make` was not on PATH (consistent with the prior session), so
  verification ran `go build ./...` / `go test ./internal/api/` / `go vet`
  directly. All green.

## Improvement Goals

- **Carried forward and applied:** the prior retro's goal of giving trivial,
  no-logic tasks a single combined review instead of the full two-stage
  subagent pass. This session, the test-only red-state task (Task 1) and the
  backlog-close task (Task 3) were verified inline rather than dispatched
  through spec + code-quality subagents; only the substantive `readJSON` change
  (Task 2) got the full two-stage review. Keep this as the norm.

## Files Most Touched

- `internal/api/server.go` - core fix: `maxBodyBytes` const, `readJSON` signature change + 413/400 policy.
- `internal/api/server_test.go` - three new `TestReadJSON_*` unit tests.
- `internal/api/auth.go` - 4 call sites updated (register, login, password, token).
- `internal/api/scheduled_jobs.go` / `users.go` - 2 call sites each (users includes the `"", false` helper case).
- `internal/api/{agent_enrollments,invites,jobs,reservations,workers}.go` - 1 call site each.
- `docs/superpowers/specs/2026-06-11-request-body-size-limit-design.md` - design spec.
- `docs/superpowers/plans/2026-06-11-request-body-size-limit.md` - implementation plan.
- `docs/backlog/closed/bug-2026-06-10-no-request-body-limit.md` - closed backlog item.
