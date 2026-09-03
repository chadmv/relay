---
title: "Structured/visual job-spec form-builder for New Job"
type: idea
status: closed
closed: 2026-09-03
resolution: fixed
created: 2026-07-01
priority: medium
source: New Job form slice (2026-07-01 job-submit-form)
---

# Structured/visual job-spec form-builder for New Job

## Summary
The New Job form shipped in the 2026-07-01 job-submit-form slice uses a raw JSON textarea editor: the
user hand-authors the job-spec JSON. A structured, visual form-builder would make job creation
accessible without hand-authoring JSON.

## Context
Surfaced while building the 2026-07-01 job-submit-form slice. That slice deliberately shipped the
minimal editor - a JSON textarea prefilled with a starter spec, with only the lightest client-side
checks (valid JSON, non-empty name, non-empty tasks) before deferring everything to the server's
`jobspec.Validate`. A structured form-builder is a materially larger surface than the JSON editor, so
it was deferred out of the first slice rather than expanding it.

## Proposal
Replace or augment the raw JSON textarea with a structured form-builder:

- **Per-task rows** capturing name, command / commands, env, requires, timeout, and retries.
- **A dependency picker** for wiring task dependencies without hand-editing a `requires` list.
- **A Perforce source-spec builder** for authoring the source spec through fields rather than raw
  JSON.

## Acceptance / Done When
- A user can author a valid job spec through structured form controls without writing JSON by hand.
- Per-task fields (name, command/commands, env, requires, timeout, retries), a dependency picker, and
  a Perforce source-spec builder are all supported.
- The builder still submits via `POST /v1/jobs` and surfaces backend validation errors inline, as the
  JSON editor does today.

## Related
- Source slice: 2026-07-01 job-submit-form
  (`docs/superpowers/specs/2026-07-01-job-submit-form-design.md`).
- Builds on [[feature-2026-07-01-job-submit-new-job-form]] (the raw-JSON New Job form this replaces
  or augments).
- Source: `internal/api/jobs.go` (`POST /v1/jobs`), `internal/api/job_spec.go` (`jobspec.Validate`),
  `web/src/jobs/`.

## Notes
Design tension to respect: any client-side structural validation the builder adds must still defer
semantic validation to the server (`jobspec.Validate`), to avoid drifting from the single job-spec
pipeline invariant. The builder should shape and constrain input for usability, not become a second,
divergent copy of the server's validation rules. Larger surface than the JSON editor, which is why
it is deferred to its own item rather than folded into the first slice.

## Resolution
Shipped in lane FB of the 2026-09-02 web-frontend batch: a structured builder is the default on /jobs/new, rendering into a read-only JSON preview, with the JSON editor kept for everything the builder cannot type and an explicit Form import that refuses (naming the path) rather than drops. One mapping module owns emission; task rows carry name, argv tokens, env and requires repeaters, timeout, retries, priority and an inline dependency picker; every semantic rule stays on the server and the server's message renders verbatim. Rows have positional accessible names, a named remove control and defined focus targets; the dispatchers use the functional updater form and the per-batch side effects (focus target, announced ordinal) are tracked across a batching window. Carve-out: the Perforce source builder is not in this slice and is filed as [[feature-2026-09-03-perforce-source-builder-in-the-new-job-builder]]; the item's dialog framing was refuted (the page is a route).
