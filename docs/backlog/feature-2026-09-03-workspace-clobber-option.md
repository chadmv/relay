---
title: An agent-level option to create workspace clients with `clobber`, so a stray writable file cannot wedge every later sync
type: feature
status: open
created: 2026-09-03
priority: low
source: SDNM fork divergence analysis (relay_updates.md, PR-5), evaluated 2026-09-03
---

# An agent-level option to create workspace clients with `clobber`, so a stray writable file cannot wedge every later sync

## Summary

`CreateStreamClient` leaves p4's default `noclobber` in place. If any writable, unopened file is
present in a relay-managed workspace - left by an interrupted sync, a tool that touched a synced
file, or a hand edit - `p4 sync` aborts the whole sync with "Can't clobber writable file", and every
later task on that shared workspace fails the same way until an operator evicts it. A fork of relay
forces `clobber` unconditionally. Upstream should offer it as an opt-in, because it silently
overwrites local files in every workspace and that is a deployment's decision to make.

## Proposal

1. **Agent-level, not per-task.** A client is created once per stream and shared by every task on
   that stream, so a per-task spec field would mean whichever task arrived first decides for all of
   them. Add `RELAY_WORKSPACE_CLOBBER` (bool, default `false`) to the agent's env, a matching field
   on the provider `Config`, and wire it through the same env-to-field path as the other workspace
   knobs, covered by the wiring guard ([[idea-2026-08-14-generalize-the-env-to-field-wiring-guard]]).
2. **Edit the `Options:` field properly.** The fork replaces the first `noclobber` byte sequence in
   the spec text. A client template's `Description:` can contain the word, and the spec is fetched
   from a template the operator controls. Parse the `Options:` line and replace the `noclobber`
   token within it, or extend `setSpecField` to edit one token of a space-separated field.
3. README: env table row stating what it does, that render and build farms whose workspaces are
   never edited by hand are the intended case, and that p4 never clobbers a file that is opened in
   the client, so unshelved files are unaffected either way.
4. Optional, small: add a `classifyP4Error` case for "can't clobber writable file" whose remedy
   names the option ([[feature-2026-09-03-classify-out-of-disk-p4-errors]] is the pattern).

Tests: the spec written back contains `clobber` in `Options:` when enabled and `noclobber` when
not; a template whose `Description:` contains `noclobber` is untouched outside `Options:` in both
cases. The second is the test that carries the field-edit requirement and is written first.

## Acceptance / Done When

- With the option off, behaviour is byte-identical to today.
- With it on, the client spec's `Options:` carries `clobber` and no other field changes.
- README documents the option, its default and the opened-file caveat.

## Related

- `internal/agent/source/perforce/client.go` - `CreateStreamClient`, `setSpecField`
- `cmd/relay-agent/main.go` - the workspace knobs
