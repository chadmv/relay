# Design: Session Retrospective Skill

**Date:** 2026-04-18
**Topic:** retro-skill

## Overview

A personal Claude Code skill (`/retro`) that writes a structured session retrospective to `docs/retros/YYYY-MM-DD-<topic>.md` at the end of any non-trivial session. Future sessions auto-load the latest retro via a user-level `~/.claude/CLAUDE.md` directive for continuity.

## Skill Location

Personal skill (cross-project): `~/.claude/skills/retro/SKILL.md`

The skill is not project-specific. Any project that has a `docs/retros/` directory (created on first run) benefits from it.

## Mechanics

When `/retro` is invoked, Claude follows seven steps:

1. **Non-trivial check** — Find the most recent retro file in `docs/retros/`. If one exists, extract its ending SHA from the `Commit Range` section. If `git rev-list <SHA>..HEAD` is empty **and** `git status --porcelain` is empty, output "Nothing to retro — no meaningful changes this session." and stop.

2. **Determine commit range** — If a prior retro exists, extract its ending SHA from the `Commit Range` section. The new range is `<that-SHA>..HEAD`. If no prior retro exists, use `$(git rev-list --max-parents=0 HEAD)..HEAD` (root to HEAD).

3. **Derive topic slug** — Infer a 2–4 word kebab-case slug from the git log messages and changed files (e.g., `auth-commands`, `scheduler-fix`, `retro-skill`).

4. **Ensure directory and resolve filename** — Create `docs/retros/` if needed. Use `docs/retros/YYYY-MM-DD-<slug>.md`. If that path already exists (same-day collision), append `-2`, `-3`, etc. until the path is free. If a same-day retro would share the same slug, prefer a more specific slug that reflects the *new* work.

5. **Write retro file (draft)** — Populate from git history and session context. Do **not** commit yet. Output the path to the user and say: "Draft written. Review and edit if needed, then tell me to commit (or discard)." Wait for user confirmation.

6. **Branch check + commit** — On user confirmation, run `git rev-parse --abbrev-ref HEAD`. If not `main`/`master`, warn the user: "Current branch is `<branch>`. The retro will be committed there and travel with any PR/merge/squash. Proceed, switch branches, or skip the commit?" Wait for direction, then commit with `docs: add session retro YYYY-MM-DD-<topic>`.

7. **Ensure session-continuity directive (user-level, one-time)** — Check `~/.claude/CLAUDE.md` for the text `Session Continuity`. If absent, append the directive (see below). This is a file edit, not a git operation. Because it is user-level, it applies across all projects without mutating any project's CLAUDE.md.

## Retro Document Format

The sections below are a **menu**, not a required skeleton. Write only sections that have meaningful content. **Always include `Commit Range`** — the next retro depends on its ending SHA to find its own start point. Never write "N/A" or emit empty headers.

Available sections (include in this order when present):

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
<!-- First..Last SHA covered by this session, e.g. abc1234..def5678 — MANDATORY -->
```

## Auto-Load Mechanism

On the first `/retro` run ever, the skill appends this block to `~/.claude/CLAUDE.md` (user-level, not project-level):

```markdown
## Session Continuity

At the start of each session, if the current project has a `docs/retros/` directory, read the most recent file in it for context on prior work.
```

Because this is user-level, it applies to all projects automatically. The skill checks for the directive's presence before writing, so re-running `/retro` will never duplicate it.

**Migration note:** Any existing project-level `Session Continuity` directive added to a project's `CLAUDE.md` before this design update is redundant and can be removed in a follow-up commit.

## Out of Scope

- Integration with `verification-before-completion` (manual invocation only)
- Cross-project retro aggregation
- Retro indexing or search
