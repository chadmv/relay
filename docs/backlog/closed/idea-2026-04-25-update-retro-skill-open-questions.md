---
title: Update retro skill to spin out Open Questions and Known Limitations as backlog entries
type: idea
status: closed
created: 2026-04-25
closed: 2026-04-25
resolution: fixed
source: brainstorming session for backlog skill design
---

# Update retro skill to spin out Open Questions and Known Limitations as backlog entries

## Summary
The retro skill only reads the most recent retro at session start, so open questions and known limitations captured in older retros are silently dropped. When writing a retro that includes these sections, the skill should offer to spin each item out as a backlog entry so deferred work persists across sessions.

## Proposal
After drafting `## Open Questions` or `## Known Limitations` sections, the retro skill prompts: "Want me to file these as backlog items?" Each accepted item gets an `idea-` or `bug-` backlog entry. The retro section becomes a brief mention with a link to the backlog file rather than the system-of-record.

## Related
- `docs/backlog/bug-2026-04-25-test-capture-skill-verification.md`
- `docs/superpowers/specs/2026-04-25-backlog-skill-design.md` (Out of Scope section)
- `~/.claude/skills/retro/SKILL.md`

## Resolution
Implemented via new Step 5 (offer to spin out Open Questions / Known Limitations as backlog entries) and modified Step 7 (drain the queue by invoking `/backlog` per item, then commit the retro). The retro skill now delegates backlog-item creation to the `/backlog` skill via its terse path rather than duplicating slug, frontmatter, and commit conventions. Plan: `docs/superpowers/plans/2026-04-25-retro-skill-backlog-spinout.md`.
