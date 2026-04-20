# Retro Skill Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Create a personal `/retro` skill that writes a structured session retrospective to `docs/retros/YYYY-MM-DD-<topic>.md`, shows a draft for review, commits after user confirmation, and adds a one-time session-continuity directive to `~/.claude/CLAUDE.md`.

**Architecture:** A single personal skill file at `~/.claude/skills/retro/SKILL.md` guides Claude through a seven-step process: non-trivial check, commit range detection via prior retro SHA, topic slug derivation, retro file creation (draft + pause), branch-aware commit, and user-level CLAUDE.md update. No code — this is a documentation/process skill.

**Tech Stack:** Claude Code personal skills (`~/.claude/skills/`), git, Markdown

---

### Task 1: Create the retro skill file

**Files:**
- Create: `~/.claude/skills/retro/SKILL.md`
  - macOS/Linux: `~/.claude/skills/retro/SKILL.md`
  - Windows (Git Bash): `~/.claude/skills/retro/SKILL.md` (resolves to `%USERPROFILE%\.claude\skills\retro\SKILL.md`)

- [ ] **Step 1: Create the skills directory**

```bash
mkdir -p ~/.claude/skills/retro
```

Expected: directory created, no output.

- [ ] **Step 2: Write SKILL.md**

Create `~/.claude/skills/retro/SKILL.md` with this exact content:

````markdown
---
name: retro
description: Write a session retrospective after completing meaningful work. Saves to docs/retros/YYYY-MM-DD-<topic>.md, shows a draft for review, commits after user confirmation, and adds a one-time session-continuity directive to ~/.claude/CLAUDE.md.
---

# Session Retrospective

Write a structured retrospective for this session and commit it to the project.

## Step 1: Non-Trivial Check

Run these commands:

```bash
git log --oneline -20
git diff --stat
git status --porcelain
ls docs/retros/ 2>/dev/null | sort | tail -1
```

Find the most recent retro file in `docs/retros/` (if any exist). If one exists, extract its ending SHA from the `Commit Range` section (format `abc1234..def5678` — take the part after `..`).

If `git rev-list <ending-SHA>..HEAD` returns nothing **and** `git status --porcelain` is empty, output:

> "Nothing to retro — no meaningful changes this session."

Then stop.

## Step 2: Determine Commit Range

- If a prior retro exists: read its `Commit Range` section and extract the ending SHA. The new range is `<ending-SHA>..HEAD`.
- If no prior retro exists: use `$(git rev-list --max-parents=0 HEAD)..HEAD` (root to HEAD).

Record this range — it will be written into the new retro's `Commit Range` section so the *next* retro can find its start point.

## Step 3: Derive Topic Slug

Infer a 2–4 word kebab-case slug from the git log messages and changed files.
Examples: `auth-commands`, `scheduler-fix`, `retro-skill`, `worker-registry-refactor`

## Step 4: Write the Retro File (Draft)

Ensure `docs/retros/` exists:

```bash
mkdir -p docs/retros
```

Determine the filename: `docs/retros/YYYY-MM-DD-<slug>.md` using today's date. If that path already exists (same-day collision), append `-2`, `-3`, etc. until the path is free. If a same-day retro would share the same slug, choose a more specific slug that reflects the *new* work.

Populate each section from git history and session context. **Only write sections that have meaningful content — omit any section with nothing to say.** The sections are a *menu*, not a required skeleton. Never write "N/A" or emit empty headers.

Always include `Commit Range` — the next retro depends on its ending SHA.

Available sections (include in this order when present):

```markdown
# Session Retro: YYYY-MM-DD — <Topic>

## What Was Built

## Key Decisions

## Problems Encountered

## Known Limitations

## Open Questions

## What We Did Well

## What We Did Not Do Well

## Improvement Goals

## Files Most Touched

## Commit Range
```

Do **not** commit yet.

Output to the user:

> "Draft written to `<path>`. Review and edit the file if needed, then tell me to commit (or discard)."

**Wait for user confirmation before proceeding.**

## Step 5: Branch Check

On user confirmation to commit, run:

```bash
git rev-parse --abbrev-ref HEAD
```

If the current branch is **not** `main` or `master`, warn the user:

> "Current branch is `<branch>`. The retro commit will live on this branch and travel with any PR/merge/squash. Proceed, switch branches first, or skip the commit?"

**Wait for user direction before proceeding.**

## Step 6: Commit the Retro

```bash
git add docs/retros/YYYY-MM-DD-<topic>.md
git commit -m "docs: add session retro YYYY-MM-DD-<topic>"
```

## Step 7: Ensure Session-Continuity Directive (user-level, one-time)

Check whether `~/.claude/CLAUDE.md` already contains the text `Session Continuity`:

```bash
grep -q "Session Continuity" ~/.claude/CLAUDE.md 2>/dev/null && echo "already present" || echo "needs update"
```

If it is **not** present, append the following block to `~/.claude/CLAUDE.md`:

```markdown

## Session Continuity

At the start of each session, if the current project has a `docs/retros/` directory, read the most recent file in it for context on prior work.
```

This is a **file edit only** — do not commit `~/.claude/CLAUDE.md`. It is user configuration, not project source.

If it is already present, skip this step.
````

- [ ] **Step 3: Verify the file exists and is non-empty**

```bash
head -5 ~/.claude/skills/retro/SKILL.md
```

Expected output includes the frontmatter `---` and `name: retro`.

- [ ] **Step 4: Commit the plan and spec files**

```bash
git add docs/superpowers/plans/2026-04-18-retro-skill.md docs/superpowers/specs/2026-04-18-retro-skill-design.md
git commit -m "docs: add retro skill implementation plan"
```

---

### Task 2: Verify the skill works

Invoke the skill in the current session to confirm it produces a correct retro for the relay project.

- [ ] **Step 1: Invoke `/retro` in the current session**

Use the Skill tool to invoke `retro`. It should:
1. Run git commands and detect recent commits
2. Extract the ending SHA from the prior retro's `Commit Range` section (or use root if none)
3. Derive a topic slug from the recent work
4. Write `docs/retros/<today>-<slug>.md` as a draft
5. Output the path and pause for user review
6. On confirmation, check branch and commit
7. Append the session-continuity directive to `~/.claude/CLAUDE.md` if absent

- [ ] **Step 2: Verify the retro file was created**

```bash
ls docs/retros/
cat docs/retros/<today>-<slug>.md
```

Expected: a populated retro file with at least 5 sections filled in, including `Commit Range`.

- [ ] **Step 3: Verify `~/.claude/CLAUDE.md` was updated**

```bash
grep -A3 "Session Continuity" ~/.claude/CLAUDE.md
```

Expected:
```
## Session Continuity

At the start of each session, if the current project has a `docs/retros/` directory, read the most recent file in it for context on prior work.
```

- [ ] **Step 4: Verify the retro commit exists**

```bash
git log --oneline -5
```

Expected: one new commit — `docs: add session retro ...`

- [ ] **Step 5: Invoke `/retro` a second time**

Invoke `retro` again. It should output "Nothing to retro — no meaningful changes this session." because no new commits have been made since the retro was written.

Expected: skill stops early with the nothing-to-retro message.
