---
title: relay-code-reviewer is told to invoke skills but has no Skill tool
type: bug
status: closed
created: 2026-08-09
closed: 2026-08-09
resolution: fixed
priority: medium
source: Phase 4 of the admin-console shell + Users tab iteration (2026-08-09)
---

# relay-code-reviewer is told to invoke skills but has no Skill tool

## Summary
`.claude/agents/relay-code-reviewer.md` instructs the agent to "invoke the /code-review skill via
the Skill tool" and "the /security-review skill via the Skill tool" (lines 15 and 17), but its
frontmatter grants `tools: Read, Grep, Glob, Bash` - `Skill` is not among them. Every review
dispatched to this agent therefore silently skips both skills.

## Repro / Symptoms
Dispatch `relay-code-reviewer` on any diff. It reports that no `Skill` tool is exposed to it and
that it could find no `/code-review` or `/security-review` skill on disk, then falls back to an
ad-hoc manual review. Observed on the 2026-08-09 admin-console review, where the agent flagged the
gap itself. The skills do exist and are available to the main session - this is purely the
subagent's missing tool grant.

## Proposal
Add `Skill` to the `tools:` list in `.claude/agents/relay-code-reviewer.md`. Then re-run a review
and confirm the agent actually invokes both skills rather than improvising.

While there, audit the other agent definitions in `.claude/agents/` for the same class of
mismatch - any agent whose body instructs it to use a tool its frontmatter does not grant. The
frontmatter and the body drifted apart here and nothing detects that.

## Acceptance / Done When
- `relay-code-reviewer` can invoke `/code-review` and `/security-review`, verified by a real
  dispatch that reports having run them (not merely by the tool appearing in the list).
- Every other agent definition in `.claude/agents/` is checked for instructions referencing tools
  it is not granted, and any found are fixed.

## Related
- Source: `.claude/agents/relay-code-reviewer.md` (frontmatter line 4 vs body lines 15-17)
- Playbook: [[docs/agent-team/README.md]] Phase 4, which routes verification through this agent

## Notes
Impact is degraded coverage, not zero coverage - the fallback manual review on 2026-08-09 was
thorough and did find four medium findings including a vacuous test. But the pipeline was silently
running something other than what the playbook documents, and reviews are exactly where an
unnoticed gap is most expensive.

## Update 2026-08-09 - root cause is deeper than the tool grant

Partially addressed, **still open**. The `tools:` line now grants `Skill` on both
`relay-code-reviewer` and `relay-tpm` (the audit found the same drift in the TPM, whose body says to
"invoke the roadmap and backlog skills via the Skill tool" - which is why it could not run `/retro`
or `/roadmap` itself during the 2026-08-09 autopilot batch). But a verification dispatch proved the
grant insufficient, and found the real problem:

1. **`/code-review` is a slash command, not a skill.** The plugin at
   `~/.claude/plugins/marketplaces/claude-plugins-official/plugins/code-review/` ships **only**
   `commands/code-review.md` - there is no `skills/` directory. So no subagent can invoke it via the
   Skill tool no matter what its `tools:` line says. The agent's prose was not merely
   under-permissioned, it was describing something that does not exist. That prose is now corrected
   to say so explicitly, so the next reader does not re-derive this.
2. **`security-review` is not on disk either** under `~/.claude/plugins` or `~/.claude/skills`; it
   appears in the parent session's available-skills list, so it is harness-provided rather than a
   file the project can point an agent at.
3. **Agent definitions appear to be cached from session start.** The verification dispatch, made
   after editing the frontmatter, still reported exactly the old four tools. So even a correct grant
   may need a fresh session to take effect - which is itself worth knowing before anyone concludes a
   future frontmatter fix "did not work".

## What is left to do
- Determine whether `Skill` is even a valid entry in this project's agent `tools:` allowlist, in a
  **fresh session** so the caching in (3) does not confound the result.
- Decide what the reviewer should actually run. Options: leave it doing its own passes (which has
  been producing strong results - 2 high and 13 medium findings across the 2026-08-09 batch, several
  empirically reproduced with probes); or have the **conductor** run `/code-review` itself and feed
  the output to the agent; or find genuine skill-form equivalents.
- Do not close this item on the strength of a frontmatter edit alone. The acceptance criterion above
  deliberately requires a real dispatch that reports having run the skills, precisely because the
  original defect was a config that looked right and silently did nothing.

## Resolution
Fixed 2026-08-09, by a different route than this item's original acceptance criteria assumed - and
the criteria were wrong rather than unmet, so they are worth reading against what follows.

The premise was that the agent needed the `Skill` tool. It was granted (to `relay-code-reviewer` and,
from the audit this item asked for, to `relay-tpm`, which had the identical drift and could therefore
never run `/retro` or `/roadmap` itself). A verification dispatch then proved the grant insufficient
and surfaced the real cause: **`/code-review` is a slash command, not a skill.** It ships as
`commands/code-review.md` with no `skills/` directory, so no subagent can invoke it via the Skill
tool no matter what its `tools:` line says, and `security-review` is harness-provided rather than a
file on disk. The agent's prose was not under-permissioned - it described something that does not
exist.

**The adopted resolution (user decision): the conductor runs `/code-review` itself and feeds the
output into the `relay-code-reviewer` dispatch as prior findings.** Verified working - the conductor
can invoke the skill. The agent's role becomes triage-and-extend: confirm or refute each fed-in
finding with its own `file:line` evidence and a concrete failure scenario, then run its own
adversarial passes over the dimensions it owns (the seven Invariants, security, test non-vacuity).

Feeding the output in rather than substituting it is deliberate. The two find different things: across
the 2026-08-09 five-item autopilot batch, the agent's own passes produced 2 high and 13 medium
findings, several empirically reproduced with probes rather than argued - including a stream teardown
that let a dying connection resurrect its own status, and an unbounded re-subscribe whose "exactly one
retry" guarantee was per-event while the trigger was caused by the retry. A generic pass would not
have found those, and they are the reason the agent is not merely a formatter for someone else's
output.

Both `docs/agent-team/README.md` Phase 4 and the agent definition are updated to describe this, each
stating plainly that the agent cannot run the command and that earlier versions claimed otherwise -
so the next reader does not re-derive the dead end.

Also recorded from the investigation, because it will otherwise cost someone an afternoon: **agent
definitions appear to be cached from session start.** The post-edit verification dispatch still
reported exactly the pre-edit tool list, so a correct frontmatter change can look like a failed one
until a fresh session.
