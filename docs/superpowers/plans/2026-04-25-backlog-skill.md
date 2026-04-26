# Backlog Skill Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Create a personal `/backlog` skill that captures forward-looking work (bugs, features, ideas) into per-item files under `docs/backlog/` in the active project, with proactive offers, terse session-start summaries, and a project-level `CLAUDE.md` directive for cross-session continuity.

**Architecture:** A single personal skill file at `~/.claude/skills/backlog/SKILL.md` guides Claude through three operations (capture / list / close) plus proactive-offer rules. No code — this is a documentation/process skill, just like the existing `retro` skill. The skill bootstraps a per-project `CLAUDE.md` directive on first use so future sessions auto-surface a one-line summary of open items.

**Tech Stack:** Claude Code personal skills (`~/.claude/skills/`), git, Markdown.

**Spec:** `docs/superpowers/specs/2026-04-25-backlog-skill-design.md` (committed as `f4e0924`).

---

### Task 1: Create the backlog skill file

**Files:**
- Create: `~/.claude/skills/backlog/SKILL.md`
  - On Windows (Git Bash): resolves to `%USERPROFILE%\.claude\skills\backlog\SKILL.md`
  - On macOS/Linux: `~/.claude/skills/backlog/SKILL.md`

- [ ] **Step 1: Create the skills directory**

```bash
mkdir -p ~/.claude/skills/backlog
```

Expected: directory created, no output.

- [ ] **Step 2: Write SKILL.md**

Create `~/.claude/skills/backlog/SKILL.md` with this exact content:

````markdown
---
name: backlog
description: Capture forward-looking work — bugs, features, ideas — into per-item files in the project's docs/backlog/ directory so future sessions can find and act on them. Invoke when the user types /backlog, /backlog list, or /backlog close. ALSO use proactively to offer a backlog entry when the user defers work mid-conversation ("we should...", "TODO", "that's a bug, but not now", "let's circle back to..."), when you notice during code review or debugging that something is broken or suboptimal but out of scope for the current task, or when a retro draft includes Open Questions / Known Limitations items not yet captured. Never auto-file without confirmation; never offer for the current task; never offer twice for the same item in one session; high-confidence specific items only.
---

# Backlog

Capture, list, and close forward-looking items in the active project.

## Step 1: Determine the operation

Parse the slash arguments after `/backlog`:

- No arguments, or first token is `bug`, `feature`, or `idea` → **capture** (Step 2).
- First token is `list` → **list** (Step 6).
- First token is `close` → **close** (Step 7).
- Anything else → respond `Unknown subcommand. Use /backlog [type] [title], /backlog list [type], or /backlog close <fragment>.` and stop.

## Step 2: Capture — gather fields

Three invocation forms; choose based on the slash arguments:

- **Interactive** (`/backlog` with no args): ask the user for type, title, optional priority, and a 1–3 sentence summary, one prompt at a time.
- **Fast path** (`/backlog <type> <title>`): infer summary from recent conversation context (last few user/assistant turns or recent tool output). Show a draft and ask for confirmation before writing.
- **Terse path** (`/backlog <type> <title> -- <summary>`): use the inline summary verbatim. No confirmation step unless the slug collides or the type is invalid.

Validate that `<type>` is one of `bug`, `feature`, `idea`. If not, ask the user to pick one.

Optional fields to gather (skip if not offered):
- `priority`: `low`, `medium`, or `high`.
- `source`: a short note on where the idea came from (e.g. "noticed while debugging worker registry"). Inferred from context if obvious, otherwise omitted.

## Step 3: Capture — derive slug and resolve filename

1. Lowercase the title.
2. Replace any non-alphanumeric character with `-`.
3. Collapse runs of `-` into a single `-`; trim leading/trailing `-`.
4. Take the first 2–5 meaningful words (drop stop-words like `the`, `a`, `for`, `in`, `to`, `of`, `and` if there are more than 5 words).

Filename: `docs/backlog/<type>-YYYY-MM-DD-<slug>.md` using today's date.

Same-day collision: if the path already exists, append `-2`, `-3`, etc. until the path is free. If a same-day item shares an exact slug, prefer choosing a more specific slug that reflects the new item.

```bash
mkdir -p docs/backlog
```

## Step 4: Capture — write the file

Write the file with this structure:

```markdown
---
title: <one-line summary>
type: bug | feature | idea
status: open
created: YYYY-MM-DD
priority: <low | medium | high>      # omit field entirely if not specified
source: <short note>                  # omit field entirely if not specified
---

# <title>

## Summary
<1–3 sentences. The "what" and "why this matters". Always required.>
```

Add additional sections from this menu **only if they have content**. Never emit empty headers.

```markdown
## Context
<Where this came from, what surrounding work prompted it, links to retros or commits.>

## Repro / Symptoms          ← bugs only
<Steps, observed vs expected behavior, error messages.>

## Proposal                   ← features mostly; sometimes ideas
<Sketch of the approach. Not a full design — that is brainstorming's job.>

## Acceptance / Done When
<How we will know it is resolved. Bullet list.>

## Related
<Files, other backlog slugs, retro references.>

## Notes
<Anything else worth keeping.>
```

## Step 5: Capture — bootstrap CLAUDE.md (first run per project)

Check whether the project's `CLAUDE.md` already contains the backlog directive:

```bash
grep -q "^## Backlog$" CLAUDE.md 2>/dev/null
```

- If `grep` exits 0, the directive is already present — skip this step.
- If `grep` exits non-zero (absent or file missing):
  - If `CLAUDE.md` does not exist, create it with a minimal header (`# CLAUDE.md\n\nThis file provides guidance to Claude Code...\n`).
  - Append this section to `CLAUDE.md`:

```markdown

## Backlog

At the start of each session, if `docs/backlog/` exists, list the open backlog files (`ls docs/backlog/*.md`) and surface a one-line summary: count by type, plus the titles of any `priority: high` items. Do not read full files unless asked.
```

## Step 6: Capture — commit

```bash
git add docs/backlog/<filename> CLAUDE.md
git commit -m "backlog: add <filename-without-extension>"
```

(Stage `CLAUDE.md` only if it was created or modified in Step 5.) Then report the path to the user:

> Filed: `docs/backlog/<filename>`

## Step 7: List

`/backlog list [type]` — list open items in the project.

```bash
ls docs/backlog/*.md 2>/dev/null
```

If `[type]` is `bug`, `feature`, or `idea`, narrow the glob to `docs/backlog/<type>-*.md`.
If `[type]` is `closed`, list `docs/backlog/closed/*.md` instead.
Otherwise list everything in `docs/backlog/` (top level only).

For each file, read its frontmatter and extract `title`, `type`, `priority`, `created`. Compute age in days from `created` to today.

Render as a compact table:

```
| type    | title                                  | priority | age |
|---------|----------------------------------------|----------|-----|
| bug     | gRPC stream concurrent-send race       | high     | 3d  |
| feature | Perforce multi-workspace support       | medium   | 1d  |
| idea    | Rolling retro summary                  | —        | 0d  |
```

Sort: by priority descending (`high`, `medium`, `low`, none), then by age descending.

If no files are present, say `Backlog: 0 open.` and stop.

## Step 8: Close

`/backlog close <fragment> [resolution]` — mark an item resolved.

1. Find a single matching open file via case-insensitive substring match against filenames in `docs/backlog/*.md`:

```bash
ls docs/backlog/*.md 2>/dev/null | grep -i "<fragment>"
```

  - Zero matches: respond `No open backlog items match "<fragment>".` and stop.
  - Multiple matches: list them and ask the user to pick one.
  - Exactly one match: proceed.

2. Resolve `[resolution]` (default `fixed`). Must be one of `fixed`, `wontfix`, `duplicate`, `obsolete`. If invalid, ask the user.

3. If the user did not provide a one-line resolution note inline, ask: `Resolution note? (1–2 sentences, optional commit SHA)`. Use the response as the note.

4. Move the file:

```bash
mkdir -p docs/backlog/closed
git mv docs/backlog/<filename> docs/backlog/closed/<filename>
```

5. Update the moved file's frontmatter:
   - Set `status: closed`.
   - Add `closed: YYYY-MM-DD` (today's date).
   - Add `resolution: <fixed|wontfix|duplicate|obsolete>`.

6. Append to the body:

```markdown

## Resolution
<note>
```

7. Commit:

```bash
git add docs/backlog/closed/<filename>
git commit -m "backlog: close <filename-without-extension>"
```

8. Report:

> Closed: `docs/backlog/closed/<filename>` (<resolution>)

## Proactive-offer rules

You are AUTHORIZED and ENCOURAGED to offer a backlog entry when you detect deferred work during normal conversation, but you MUST follow these rules:

1. **Never offer for things the user is currently working on.** The current task is not deferred work. If the user is actively asking you to fix or implement something, just do it — do not interrupt to file a backlog entry.
2. **Never offer twice for the same item in one session.** Track offers mentally for the duration of the session. If the user dismisses an offer, do not nag.
3. **High confidence only.** Vague observations like "this could be cleaner" do not qualify. The item must have a specific, actionable shape — a recognizable bug, a concrete feature outline, or a defined question.
4. **Never auto-file without confirmation.** All proactive captures require an explicit user accept. The user dismisses by saying no, ignoring, or moving on.

**Triggers** (use judgment, these are signals not rules):
- User says "we should...", "TODO", "that's a bug, but not now", "let's circle back to...", "remind me to...", "for later".
- During code review or debugging, you notice something broken or suboptimal that is clearly out of the current task's scope.
- You are drafting a retro and an item belongs in `## Open Questions` or `## Known Limitations` but is not yet a backlog entry.

**Offer format** — a single short message, nothing else:

> Want me to file this as a backlog `<type>` item? — `<draft title>`

If the user accepts, capture via the fast-path flow (Step 2 fast path), inferring summary from the surrounding context. If the user dismisses, drop it silently.

## Notes for Claude

- The `description` field in frontmatter is what triggers this skill. Read it carefully — it lists every signal that should make you invoke `/backlog` proactively.
- Always commit each capture and each close as its own commit. The git log of `docs/backlog/` is itself a useful artifact.
- Never read backlog files at session start unless the user asks. The session-start `CLAUDE.md` directive only requires `ls` + frontmatter parse.
- This skill is project-scoped. If `git rev-parse --is-inside-work-tree` fails, respond `Not in a git repository — backlog requires a project.` and stop.
````

Expected: file created with the exact content above.

- [ ] **Step 3: Verify the file**

```bash
ls -la ~/.claude/skills/backlog/SKILL.md
head -3 ~/.claude/skills/backlog/SKILL.md
```

Expected:
- File exists with non-zero size.
- First three lines show `---`, `name: backlog`, and the `description:` line.

- [ ] **Step 4: Confirm the skill is registered**

In a fresh Claude Code session (or by reloading skills), confirm that `backlog` appears in the user-invocable skills list with its full description. The description should include the proactive-offer triggers.

If the skill does not appear, troubleshoot:
- Path: must be exactly `~/.claude/skills/backlog/SKILL.md`.
- Frontmatter: must start with `---` on line 1, end with `---`, and contain `name: backlog`.
- Reload: some Claude Code installs cache the skill list; restart the session if needed.

---

### Task 2: Verification — dry-run each operation

This task is a manual end-to-end test. No new files are written; existing operations are exercised against a real project (the relay repo is fine). Each step has a clear pass/fail check.

**Files:**
- Touch: `D:/dev/relay/docs/backlog/*` (test files, intentionally created and then closed)
- Modify: `D:/dev/relay/CLAUDE.md` (one-time bootstrap, will be committed)

- [ ] **Step 1: Capture a `bug` via the terse path**

Invoke:

```
/backlog bug Test capture for skill verification -- This is a verification capture; close it after listing.
```

Expected:
- File `docs/backlog/bug-2026-04-25-test-capture-skill-verification.md` (or similar slug) exists.
- Frontmatter has `title`, `type: bug`, `status: open`, `created: 2026-04-25`.
- Body has `## Summary` with the supplied sentence.
- `CLAUDE.md` has a new `## Backlog` section (first-run bootstrap).
- A commit `backlog: add bug-2026-04-25-...` exists.

- [ ] **Step 2: Capture an `idea` via the proactive-offer flow**

In conversation, casually say something like: *"We should probably add stale-item flagging to the backlog summary at some point."* Confirm Claude offers a backlog entry. Accept the offer.

Expected:
- An `idea-` file is created with a title roughly matching the surfaced phrase.
- Confirmation request was the offer format from the proactive-offer rules.
- Commit landed.

- [ ] **Step 3: List**

Invoke:

```
/backlog list
```

Expected:
- Table with both items from Steps 1 and 2.
- Sort order: high-priority first if any, otherwise newest first.
- No closed items shown.

- [ ] **Step 4: List by type**

Invoke:

```
/backlog list bug
```

Expected: only the bug from Step 1 is listed.

- [ ] **Step 5: Close**

Invoke:

```
/backlog close test-capture fixed
```

Expected:
- The bug file moved to `docs/backlog/closed/`.
- Frontmatter updated: `status: closed`, `closed: 2026-04-25`, `resolution: fixed`.
- A `## Resolution` section appended to the body (Claude prompts for the note).
- A commit `backlog: close bug-2026-04-25-...` exists.

- [ ] **Step 6: Confirm session-start simulation**

Read `D:/dev/relay/CLAUDE.md` and confirm the `## Backlog` section exists exactly once. Then simulate the directive manually:

```bash
ls D:/dev/relay/docs/backlog/*.md 2>/dev/null
```

Expected: exactly one open file (the `idea-` one from Step 2). Closing the session and starting a fresh one should produce a one-line summary like `Backlog: 1 open (1 idea).`

- [ ] **Step 7: Cleanup the verification items**

The verification items are intentionally test-only. Close the remaining one and remove the `closed/` versions if you do not want the test items polluting history:

```
/backlog close <fragment> obsolete
```

Then optionally:

```bash
git rm docs/backlog/closed/*verification* docs/backlog/closed/*test*
git rm docs/backlog/closed/*-stale-item-flagging*  # whatever slug the idea got
git commit -m "backlog: remove verification test items"
```

(Skip this step if you want to keep the test items as the first real backlog history.)

- [ ] **Step 8: File the first real follow-up**

Invoke:

```
/backlog idea Update retro skill to spin out Open Questions and Known Limitations as backlog entries
```

Expected:
- An `idea-2026-04-25-update-retro-skill-...` file is created.
- This is the meta-test: the skill captures the work item that the brainstorming process explicitly deferred.

---

## Self-Review Notes

Spec coverage:
- Skill location → Task 1 Step 1.
- Categories (bug/feature/idea) → SKILL.md Step 1 (validation) and Step 2 (gather).
- File layout (per-item files, `closed/` subdir) → SKILL.md Steps 3 and 8.
- File schema (frontmatter, body section menu, resolution fields) → SKILL.md Steps 4 and 8.
- Capture operation (3 forms) → SKILL.md Step 2.
- List operation → SKILL.md Step 7.
- Close operation → SKILL.md Step 8.
- Proactive offers (triggers, format, hard rules) → SKILL.md "Proactive-offer rules" section.
- Session-start `CLAUDE.md` directive → SKILL.md Step 5.
- Verification dry-runs → Task 2.
- Out-of-scope items (retro update, stale flagging, etc.) → not implemented; first one is filed as the meta-test in Task 2 Step 8.

No placeholders. All bash commands are exact. All file paths are absolute or rooted in `~`. The slug derivation, frontmatter shape, and commit-message format are all concrete.
