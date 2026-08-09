---
title: relay-code-reviewer is told to invoke skills but has no Skill tool
type: bug
status: open
created: 2026-08-09
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
