---
date: 2026-06-21
topic: mcp-overlap-policy-description-says-queue
branch: claude/distracted-allen-9c27c1
pr: "2026-06-21 / mcp-overlap-policy-description-says-queue"
merge: "2026-06-21 / mcp-overlap-policy-description-says-queue"
---

# Session Retro: 2026-06-21 - MCP overlap_policy description fix

**TL;DR:** Closed `bug-2026-06-20-mcp-overlap-policy-description-says-queue`. The
`relay_create_schedule` MCP tool's `overlap_policy` jsonschema description advertised "skip or
queue", but there is no `queue` policy - the API handler accepts only `skip`/`allow` and migration
000019's `scheduled_jobs_overlap_policy_check` rejects `queue`. Corrected the string to "skip or
allow". Autopilot batch, item 2 of 7.

## What Was Built

- `internal/mcp/schedules_write.go:17` - one-word jsonschema description correction
  ("skip or queue" -> "skip or allow").

## Key Decisions

- **No test added.** The change is a literal jsonschema description string with no behavior. The
  project has no test that asserts tool-description text (and adding one would be a brittle
  change-detector), so a TDD cycle would have been theater. Correctness was established by triple
  cross-reference instead: the API validation message (`scheduled_jobs.go:94`), the CHECK
  constraint (`000019`), and `schedrunner/runner.go` special-casing only `skip`.
- **Scope limited to the one wrong string.** A grep for `skip or queue` / `overlap_policy` confirmed
  the only misleading description was the create arg; `relay_update_schedule`'s description names no
  specific value, so it was left untouched.

## Process Note

- Genuinely trivial doc fix - made directly by the conductor rather than dispatching the backend
  engineer + full verify fan-out, per the project's combined-review-for-trivial-tasks guidance.
  Verification was `go build` + `go vet` on `internal/mcp` plus the cross-reference check.

## Backlog Triage

- No new items.
