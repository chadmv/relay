---
title: An agent-level option to create workspace clients with `clobber`, so a stray writable file cannot wedge every later sync
type: feature
status: closed
closed: 2026-09-04
resolution: fixed
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

## Resolution

`RELAY_WORKSPACE_CLOBBER` (bool, default off) rewrites the `noclobber` token in a p4
client spec's `Options:` line. Off is a byte-identical no-op, including for an operator
template that already carries `Options: clobber` - the item asked for `noclobber` to be
forced when the knob is off, which contradicts its own first acceptance criterion and
would override a deliberate template.

**What p4 r25.2 actually emits, recorded rather than predicted**, since no fixture in the
repo showed a real `Options:` line: seven tokens, not the six every document assumed -
`noallwrite noclobber nocompress unlocked nomodtime normdir noaltsync`. Nothing had to
change, because the transform edits whatever is present and no test hard-codes a default
set. The same observation found the load-bearing hazard: a real spec carries a comment
header `#  Options:     Client options:` BEFORE the field, so an unanchored reader takes
the comment, fails the token check, and silently no-ops the knob on every workspace in
the fleet. That header is now in a fixture and the anchor is pinned against it.

The prescribed wiring guard does not exist for `cmd/relay-agent` - every guard the cited
idea describes is in `cmd/relay-server` - so the slice pushed coverage to executed checks
(env to value, `Config` to spec bytes) and states the one residual unpinned assignment
rather than papering over it.

The `classifyP4Error` case is **deferred**, not forgotten. Its message is a live channel
in the open misclassification bug, and a remedy string naming this option would advertise
turning fleet-wide silent overwriting ON to whoever can forge the message - the
"an option that disables the control belongs outside the remedy ladder" invariant.

Review found one false claim: README said no job spec could request clobber, and
`client_template` is job-spec-supplied while `p4 -t` copies the template's `Options:`
field, so a cold workspace named by a job spec can obtain one. The branch's own fixture
demonstrated it. The sentence now names that route.
