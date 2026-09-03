---
title: Perforce source builder in the New Job form builder, so a task with a source is authorable without JSON
type: feature
status: open
created: 2026-09-03
priority: medium
source: cut from lane FB (2026-09-02-web2-fb-design) as its own slice; the FB slice boundary is conditioned on this item existing
---

# Perforce source builder in the New Job form builder, so a task with a source is authorable without JSON

## Summary
Lane FB ships a structured form builder on `/jobs/new` covering the job fields and per-task name,
commands, env, requires, timeout, retries and dependencies. It does NOT cover `tasks[].source`. A
task needing a Perforce workspace is therefore authorable only in the page's JSON mode. This item
is the remaining third of `idea-2026-07-01-job-spec-form-builder`.

## Context
Nothing is silently lost in the interim, and that is deliberate rather than lucky. The FB design's
mode switch models the pasted JSON key by key and REFUSES to enter builder mode when it meets a key
it cannot represent, naming the path - so a spec carrying a `source` keeps the user in the JSON
editor with their text intact, rather than entering the builder and dropping the block. That
refusal is what makes deferring this safe; if it is ever relaxed, this item becomes urgent.

Why it was cut rather than folded in:

- `source` is a second nested repeater on top of the task repeater FB already builds, with its own
  add/remove/focus/announce work. `sync` is a list of two-field records; `unshelves` is a list of
  integers.
- `rev` has four accepted shapes (a head token, an at-sign followed by digits, an at-sign followed
  by label characters, a hash followed by digits) and `sync[i].path` must be the stream itself, the
  stream followed by an ellipsis segment, or a path under the stream. Both rules live in
  `validateSourceSpec` and MUST NOT be re-implemented client-side; a builder is strongly tempted to
  pre-check exactly these, which is where the single job-spec pipeline invariant gets bent.
- It is the only part of the builder whose output cannot be exercised end to end in this repo:
  `make test-e2e` runs no `relay-agent`, and the Perforce path needs a p4d and a live ticket. A
  source block authored in the SPA can be shown to serialize, never to work.

## Proposal
Extend `specBuilder.ts` (`BuilderState`, `toSpec`, `fromSpec`) and the task row with a source
section covering `type`, `stream`, `sync[]` of `{path, rev}`, `unshelves[]`, `workspace_exclusive`
and `client_template`. Then relax the import refusal so a `source` block is modelled instead of
rejected.

Constraints carried over from the FB design and not up for renegotiation here:

- No client-side implementation of any rule in `validateSourceSpec`: not the stream prefix, not the
  containment rule, not the four `rev` shapes, not the `client_template` character set, not the
  positivity of an unshelve. The server's message is rendered verbatim, as it is everywhere else on
  this page.
- `type` is the one closed set: `perforce` is the only accepted value, so a fixed control is
  correct there and nowhere else.
- Absent optionals emit no key; the round-trip property from the FB test plan extends to the README
  source example, which must go from refused to round-tripping identically.

## Acceptance / Done When
- A task with a Perforce source is authorable end to end in builder mode, with no JSON typed.
- `fromSpec` of the README Source-workspaces example round-trips through `toSpec` deep-equal,
  replacing the FB-era test that asserts it is refused.
- A source spec the server rejects still reaches the server: a test asserts the POST is issued for
  an out-of-stream sync path and that the server's message renders verbatim. No client-side
  pre-check.
- The nested repeaters meet the same accessibility rules as the FB task rows: stable non-index ids,
  per-row named remove controls, a defined focus target on remove, and announcements through the
  page's single polite live region.
- The narrow-viewport e2e surface covers the builder with a source section expanded at 320, 375 and
  1280.

## Related
- `docs/superpowers/specs/2026-09-02-web2-fb-design.md` - decision D5, which is conditioned on this
  item, and the D1 refusal rule that makes the deferral safe
- [[idea-2026-07-01-job-spec-form-builder]] - the parent item; this is its third bullet
- [internal/jobspec/jobspec.go](internal/jobspec/jobspec.go) - `SourceSpec`, `SyncEntry`,
  `validateSourceSpec` and the four rev shapes
- README.md - the Source workspaces section, whose field table is the authoring reference (and
  which omits `client_template`; see the FB design's escalation E4)
