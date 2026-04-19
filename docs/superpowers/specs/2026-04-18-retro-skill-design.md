# Design: Session Retrospective Skill

**Date:** 2026-04-18
**Topic:** retro-skill

## Overview

A personal Claude Code skill (`/retro`) that writes a structured session retrospective to `docs/retros/YYYY-MM-DD-<topic>.md` at the end of any non-trivial session. Future sessions auto-load the latest retro via a CLAUDE.md directive for continuity.

## Skill Location

Personal skill (cross-project): `~/.claude/skills/retro/SKILL.md`

The skill is not project-specific. Any project that has a `docs/retros/` directory (created on first run) benefits from it.

## Mechanics

When `/retro` is invoked, Claude:

1. **Non-trivial check** — Inspect `git log` and `git diff --stat`. If there are no commits newer than the most recent retro file (or no commits at all if no retro exists yet) and no staged/unstaged changes, output "Nothing to retro — no meaningful changes this session." and stop.
2. **Derive topic slug** — Infer a 2–4 word kebab-case topic from the work (e.g., `auth-commands`, `scheduler-fix`, `retro-skill`).
3. **Ensure directory** — Create `docs/retros/` if it does not exist.
4. **Write retro file** — Save to `docs/retros/YYYY-MM-DD-<topic>.md` using today's date.
5. **Commit** — Commit the file with message `docs: add session retro YYYY-MM-DD-<topic>`.
6. **One-time CLAUDE.md update** — If the project's `CLAUDE.md` does not yet contain the session continuity directive, append it (idempotent).

## Retro Document Format

```markdown
# Session Retro: YYYY-MM-DD — <Topic>

## What Was Built
<!-- Narrative summary of the work completed this session -->

## Key Decisions
<!-- Architecture and implementation choices made, with reasoning -->

## Problems Encountered
<!-- Issues hit during the session and how they were resolved -->

## Known Limitations
<!-- Intentional shortcuts, deferred scope, or v1 constraints accepted -->

## Open Questions
<!-- Unresolved decisions, design questions, or things to revisit next session -->

## What We Did Well
<!-- Practices or approaches that worked particularly well -->

## What We Did Not Do Well
<!-- Mistakes, inefficiencies, or missed opportunities -->

## Improvement Goals
<!-- Concrete changes to apply in future sessions -->

## Files Most Touched
<!-- Short list of the most-changed files with one-line context each -->

## Commit Range
<!-- First..Last SHA covered by this session, e.g. abc1234..def5678 -->
```

Sections with nothing to say are **omitted** — never written as "N/A" or left empty.

## Auto-Load Mechanism

On the first `/retro` run in a project, the skill appends this block to `CLAUDE.md`:

```markdown
## Session Continuity

At the start of each session, read the most recent file in `docs/retros/` for context on prior work.
```

The skill checks for the directive's presence before writing, so re-running `/retro` in a project that already has the directive does not duplicate it.

At future session starts, Claude reads `CLAUDE.md`, sees the directive, and loads the latest retro file automatically.

## Out of Scope

- Integration with `verification-before-completion` (manual invocation only)
- Cross-project retro aggregation
- Retro indexing or search
