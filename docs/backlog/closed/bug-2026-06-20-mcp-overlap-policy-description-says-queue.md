---
title: MCP create_schedule overlap_policy description says "queue" but value is rejected
type: bug
status: closed
created: 2026-06-20
closed: 2026-06-21
resolution: fixed
priority: low
source: status-vocabulary-drift retro (2026-06-20)
---

# MCP create_schedule overlap_policy description says "queue" but value is rejected

## Summary
The `relay_create_schedule` MCP tool's `overlap_policy` jsonschema description says
the choice is "skip or queue", but the accepted values are `skip` and `allow`.
There is no `queue` policy: `schedrunner/runner.go:111` special-cases only `skip`
(everything else behaves as `allow`), and migration `000019` adds
`scheduled_jobs_overlap_policy_check CHECK (overlap_policy IN ('skip','allow'))`,
which rejects `queue` outright. An MCP client that follows the description and
sends `queue` gets a constraint/handler rejection.

## Proposal
Fix the description string to read "skip or allow" (matching the real accepted
values and the CHECK constraint).

## Related
- `internal/mcp/schedules_write.go:18` (jsonschema description)
- `internal/schedrunner/runner.go:111` (only `skip` is special-cased)
- `internal/store/migrations/000019_status_vocabulary_checks.up.sql` (`scheduled_jobs_overlap_policy_check`)

## Resolution
fixed (2026-06-21). Corrected the `relay_create_schedule` `overlap_policy` jsonschema description in `internal/mcp/schedules_write.go:17` from "skip or queue" to "skip or allow", matching the only two accepted values (the API handler's `overlap_policy must be 'skip' or 'allow'` validation and the `scheduled_jobs_overlap_policy_check` CHECK from migration 000019). Pure doc-string fix, no behavior change; the `relay_update_schedule` arg description named no specific value and was left untouched. Build + vet clean.
