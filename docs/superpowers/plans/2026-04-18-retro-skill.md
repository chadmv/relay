# Retro Skill Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Create a personal `/retro` skill that writes a structured session retrospective to `docs/retros/YYYY-MM-DD-<topic>.md`, commits it, and adds a one-time CLAUDE.md directive for auto-loading at future session starts.

**Architecture:** A single personal skill file at `~/.claude/skills/retro/SKILL.md` guides Claude through a six-step process: non-trivial check, commit range detection, topic slug derivation, retro file creation, commit, and one-time CLAUDE.md update. No code — this is a documentation/process skill. Verification is done by invoking `/retro` in the current session and inspecting the output.

**Tech Stack:** Claude Code personal skills (`~/.claude/skills/`), git, Markdown

---

### Task 1: Create the retro skill file

**Files:**
- Create: `~/.claude/skills/retro/SKILL.md` (Windows path: `C:\Users\chadv\.claude\skills\retro\SKILL.md`)

- [ ] **Step 1: Create the skills directory**

```bash
mkdir -p /c/Users/chadv/.claude/skills/retro
```

Expected: directory created, no output.

- [ ] **Step 2: Write SKILL.md**

Create `C:\Users\chadv\.claude\skills\retro\SKILL.md` with this exact content:

````markdown
---
name: retro
description: Write a session retrospective after completing meaningful work. Saves to docs/retros/YYYY-MM-DD-<topic>.md, commits it, and adds a one-time CLAUDE.md directive to auto-load the latest retro at future session starts.
---

# Session Retrospective

Write a structured retrospective for this session and commit it to the project.

## Step 1: Non-Trivial Check

Run these commands:

```bash
git log --oneline -20
git diff --stat
git status
ls docs/retros/ 2>/dev/null | sort | tail -1
```

Find the most recent retro file in `docs/retros/` (if any exist). If there are **no commits since the most recent retro file's date** AND **no staged or unstaged changes**, output:

> "Nothing to retro — no meaningful changes this session."

Then stop.

## Step 2: Determine Commit Range

- If no prior retro exists: use the earliest commit SHA in `git log --oneline`
- If a prior retro exists: find the first commit after the retro file's date (use the date in the filename)

The range to record is `<first-session-commit>..HEAD`.

## Step 3: Derive Topic Slug

Infer a 2–4 word kebab-case slug from the git log messages and changed files.
Examples: `auth-commands`, `scheduler-fix`, `retro-skill`, `worker-registry-refactor`

## Step 4: Write the Retro File

Ensure `docs/retros/` exists:

```bash
mkdir -p docs/retros
```

Write to `docs/retros/YYYY-MM-DD-<topic>.md` using today's date. Populate each section from git history and session context. **Omit any section that has nothing meaningful to say** — never write "N/A" or leave a section empty.

Use this template:

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

## Step 5: Commit the Retro

```bash
git add docs/retros/YYYY-MM-DD-<topic>.md
git commit -m "docs: add session retro YYYY-MM-DD-<topic>"
```

## Step 6: Update CLAUDE.md (one-time, idempotent)

Check whether `CLAUDE.md` already contains the text `Session Continuity`:

```bash
grep -q "Session Continuity" CLAUDE.md && echo "already present" || echo "needs update"
```

If it is **not** present, append the following block to `CLAUDE.md` and commit:

```markdown

## Session Continuity

At the start of each session, read the most recent file in `docs/retros/` for context on prior work.
```

```bash
git add CLAUDE.md
git commit -m "docs: add session continuity directive to CLAUDE.md"
```

If it is already present, skip this step.
````

- [ ] **Step 3: Verify the file exists and is non-empty**

```bash
cat /c/Users/chadv/.claude/skills/retro/SKILL.md | head -5
```

Expected output includes the frontmatter `---` and `name: retro`.

- [ ] **Step 4: Commit the plan and spec files**

```bash
cd /path/to/worktree
git add docs/superpowers/plans/2026-04-18-retro-skill.md
git commit -m "docs: add retro skill implementation plan"
```

---

### Task 2: Verify the skill works

Invoke the skill in the current session to confirm it produces a correct retro for the relay project.

- [ ] **Step 1: Invoke `/retro` in the current session**

Use the Skill tool to invoke `retro`. It should:
1. Run git commands and find recent commits (auth commands, CLAUDE.md, retro spec)
2. Derive a topic slug from recent work
3. Write `docs/retros/<today>-<slug>.md`
4. Commit the retro file
5. Append the Session Continuity directive to `CLAUDE.md` and commit

- [ ] **Step 2: Verify the retro file was created**

```bash
ls docs/retros/
cat docs/retros/<today>-<slug>.md
```

Expected: a populated retro file with at least 5 of the 10 sections filled in.

- [ ] **Step 3: Verify CLAUDE.md was updated**

```bash
grep -A3 "Session Continuity" CLAUDE.md
```

Expected:
```
## Session Continuity

At the start of each session, read the most recent file in `docs/retros/` for context on prior work.
```

- [ ] **Step 4: Verify commits exist**

```bash
git log --oneline -5
```

Expected: two new commits — `docs: add session retro ...` and `docs: add session continuity directive to CLAUDE.md`.

- [ ] **Step 5: Invoke `/retro` a second time**

Invoke `retro` again. It should output "Nothing to retro — no meaningful changes this session." because no new commits have been made since the retro was written.

Expected: skill stops early with the nothing-to-retro message.
