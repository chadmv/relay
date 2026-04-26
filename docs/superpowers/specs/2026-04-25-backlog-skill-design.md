# Design: Backlog Skill

**Date:** 2026-04-25
**Topic:** backlog-skill

## Overview

A personal Claude Code skill (`/backlog`) that captures forward-looking work — bugs, features, and ideas — into per-item files in the project's `docs/backlog/` directory. Future sessions discover and surface open items via a project-level `CLAUDE.md` directive, so deferred work is not lost between sessions.

The skill complements but does not subsume the existing `retro` skill. Retros are session-bounded historical snapshots; the backlog is the durable, forward-looking, cross-session worklist.

## Skill Location

Personal skill (cross-project): `~/.claude/skills/backlog/SKILL.md`

The skill is not project-specific. Any project benefits once a backlog item is filed in it (which also bootstraps the project's `CLAUDE.md` directive — see below).

## Categories

Three categories, distinguished by filename prefix:

- **`bug`** — broken or wrong behavior. Action: reproduce, fix, verify.
- **`feature`** — concrete net-new capability with imaginable acceptance criteria. Action: design → plan → implement.
- **`idea`** — anything not yet concrete enough to act on: half-formed thoughts, "we should explore X someday," product/roadmap musings, architectural questions. Action: think more before acting.

The discriminating test between `feature` and `idea` is *"could I write acceptance criteria for this today?"* If yes → `feature`; if no → `idea`. This naturally absorbs roadmap-flavored items into `idea` without inventing a separate `roadmap` category.

Improvement, polish, tech-debt, and refactor items are not separate categories — they are small `feature` or `bug` items, distinguished (if at all) via the optional `priority` field.

## File Layout

```
docs/backlog/
├── bug-2026-04-25-grpc-stream-concurrent-send.md   ← open
├── feature-2026-04-25-perforce-multi-workspace.md  ← open
├── idea-2026-04-26-rolling-retro-summary.md        ← open
└── closed/
    ├── bug-2026-04-20-old-issue.md                 ← resolved
    └── feature-2026-04-15-shipped.md               ← done
```

**Filename:** `<type>-YYYY-MM-DD-<slug>.md`

**Same-day collision rule:** append `-2`, `-3`, etc. until the path is free. If a same-day item would share an exact slug, prefer a more specific slug that reflects the new item.

**Closed items move to `closed/`** rather than being deleted. Reasoning:

- Future-you can grep historical decisions ("did we ever consider X?")
- `git mv` keeps history visible and traceable
- The live `docs/backlog/` directory stays a clean, scannable list of what is still open
- Cheap to delete `closed/` later if it grows unwieldy

Listing open items is `ls docs/backlog/*.md`. Listing by type is `ls docs/backlog/<type>-*.md`.

## File Schema

### Frontmatter

```yaml
---
title: <one-line summary>                           # required
type: bug | feature | idea                          # required
status: open | in-progress | closed                 # required, defaults to "open"
created: YYYY-MM-DD                                 # required
priority: low | medium | high                       # optional
source: <short note>                                # optional, e.g. "noticed while debugging worker registry"
---
```

When an item is closed, two additional frontmatter fields are added:

```yaml
closed: YYYY-MM-DD
resolution: fixed | wontfix | duplicate | obsolete
```

### Body

A section menu. Write only sections that have meaningful content. Never emit empty headers. Only `## Summary` is required.

```markdown
# <title>

## Summary
<1–3 sentences. The "what" and "why this matters". Required.>

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

When an item is closed, a `## Resolution` section is appended explaining why it closed (1–2 sentences, optionally a commit SHA).

## Operations

The skill supports three operations, dispatched on slash arguments.

### Capture

Three invocation forms:

- `/backlog` — interactive: skill asks for type, title, priority (optional), and summary.
- `/backlog <type> <title>` — fast path: skill drafts a file from conversation context (recent tool output, current discussion), shows the draft, user confirms or edits before write.
- `/backlog <type> <title> -- <one-line summary>` — terse path: writes the file with minimal prompting. For when the user just wants it captured and out of their head.

In all forms, the skill:

1. Validates the type is one of `bug`, `feature`, `idea`.
2. Derives a kebab-case slug from the title (2–5 words).
3. Resolves the filename, applying the same-day collision rule.
4. Writes the file with required frontmatter and `## Summary`.
5. Bootstraps the project `CLAUDE.md` directive on first use in the project (see Session-Start Behavior).
6. Commits with message `backlog: add <filename-without-extension>` (e.g., `backlog: add bug-2026-04-25-grpc-stream-concurrent-send`).
7. Reports the path to the user.

### List

`/backlog list [type]`

Reads `docs/backlog/*.md` (or `docs/backlog/<type>-*.md` if filtered), parses frontmatter, and prints a compact table:

| type | title | priority | age |
|------|-------|----------|-----|
| bug | gRPC stream concurrent-send race | high | 3d |
| feature | Perforce multi-workspace support | medium | 1d |
| idea | Rolling retro summary | — | 0d |

Closed items are not listed by default; `/backlog list closed` opts in.

### Close

`/backlog close <slug-or-fragment> [resolution]`

1. Resolves the fragment to a single open backlog file via case-insensitive substring match against open filenames in `docs/backlog/*.md`. If zero or multiple matches, prompts the user to disambiguate.
2. `git mv`s the file to `docs/backlog/closed/`.
3. Updates frontmatter: `status: closed`, adds `closed: YYYY-MM-DD`, adds `resolution:` (defaults to `fixed` if not specified).
4. Appends a `## Resolution` section with a 1–2 sentence note (prompted from the user if not provided inline).
5. Commits with message `backlog: close <filename-without-extension>`.

Reopening is intentionally not supported as a command in v1. To reopen, manually `git mv` the file out of `closed/` and edit frontmatter — rare enough not to deserve dedicated tooling yet.

## Proactive Offers

The skill's `description` frontmatter authorizes Claude to *offer* a backlog entry when deferred work is detected during normal conversation, mirroring the pattern used by `mcp__ccd_session__spawn_task`.

### Triggers

- User says "we should...", "TODO", "that's a bug, but not now", "let's circle back to..."
- Claude notices during code review or debugging that something is broken or suboptimal but out of scope for the current task.
- A retro draft includes `## Open Questions` or `## Known Limitations` items that are not yet backlog entries.

### Offer format

A single short message:

> Want me to file this as a backlog `<type>` item? — `<draft title>`

User accepts (filed via the fast-path capture flow) or dismisses (silent, no further nag).

### Hard rules

These rules are stated in the skill body as MUST / MUST-NOT directives:

- **Never offer for things the user is currently working on.** The current task is not deferred work.
- **Never offer twice for the same item in one session.**
- **High confidence only.** Vague observations like "this could be cleaner" do not qualify. The item must have a specific, actionable shape.
- **Never auto-file without confirmation.** All proactive captures require an explicit user accept.

## Session-Start Behavior

### Project `CLAUDE.md` directive

Appended to the project's `CLAUDE.md` the first time a backlog item is created in that project:

```markdown
## Backlog

At the start of each session, if `docs/backlog/` exists, list the open backlog files (`ls docs/backlog/*.md`) and surface a one-line summary: count by type, plus the titles of any `priority: high` items. Do not read full files unless asked.
```

If `CLAUDE.md` does not exist, the skill creates a minimal one. The skill never edits `CLAUDE.md` after the initial bootstrap — subsequent sessions update the file only when the user explicitly asks.

### What the user sees

When there are open items:

```
Backlog: 7 open (3 bug, 2 feature, 2 idea). High-priority: "gRPC stream concurrent-send race", "Perforce ticket renewal".
```

When `docs/backlog/` exists but is empty:

```
Backlog: 0 open.
```

When `docs/backlog/` does not exist (project has never used the skill), no summary is emitted — the directive's `if exists` guard short-circuits silently. This avoids cluttering session start in projects that do not opt in.

The summary is intentionally terse. Detail is one `/backlog list` away. Reading every backlog file at session start would burn tokens and bury signal in noise.

## Skill Body Structure

The `SKILL.md` file follows the shape of the existing `retro` skill — concrete numbered steps, not abstract principles.

1. **Frontmatter** — `name: backlog`, plus a `description` that lists slash invocations, capture triggers, and the proactive-offer authorization. The description is load-bearing; future Claude instances decide whether to invoke based on it.
2. **Step 1: Determine operation** — capture / list / close, dispatched on the slash args.
3. **Step 2 (capture): Gather fields** — type, title, priority, summary; draft from conversation context if possible; show draft; get confirmation.
4. **Step 3 (capture): Write file** — slug derivation, same-day collision rule, frontmatter, body section menu.
5. **Step 4 (capture): Bootstrap CLAUDE.md** — first-run check; append directive if absent.
6. **Step 5 (capture): Commit** — `backlog: add <type> <slug>`.
7. **Step 6 (list): Read directory** — parse frontmatter for title, priority, age; format as table.
8. **Step 7 (close): Resolve fragment → file** — fuzzy match, disambiguate if needed; `git mv`; update frontmatter; append `## Resolution`; commit.
9. **Proactive-offer rules** — the four hard rules from the Proactive Offers section, stated as MUST / MUST-NOT.

## Out of Scope

Deferred to follow-up backlog items, filed once the skill ships:

- Updating the `retro` skill to spin out `## Open Questions` and `## Known Limitations` items as backlog entries.
- Augmenting the session-start summary to flag stale items (e.g. older than N days).
- Cross-project / global backlog (`~/.claude/backlog/`).
- A `/backlog reopen <slug>` command. For now, manually `git mv` from `closed/` if needed.
- Tags, assignees, due dates. None of these earn their cost in a single-user, file-based system.

## Verification

Since this is a skill (markdown, not code), verification is dry-running each operation in a real session after the skill is written:

1. Capture a `bug-` item with the explicit fast-path form.
2. Capture an `idea-` item via the proactive-offer flow.
3. `/backlog list` and confirm formatting and ordering.
4. `/backlog close` one of the items and confirm the file moved to `closed/` with correct frontmatter and `## Resolution` section.
5. Verify the `CLAUDE.md` directive was appended exactly once.
6. Simulate a next-session start (read `CLAUDE.md`, run the directive's commands) and confirm the surfaced summary matches expectations.

A small inline "test" suite in the spec is sufficient — there is no automated test harness for skills.

## Open Questions

None at design time. All fork points were resolved during brainstorming:

- Storage scope → project-only (`docs/backlog/`).
- File granularity → one file per item.
- Categories → `bug`, `feature`, `idea`.
- Frontmatter → minimal required set + optional `priority` and `source`.
- Capture flow → explicit slash command + proactive offers, both with confirmation.
- Session-start surfacing → terse one-line summary via `CLAUDE.md` directive.
- Closed-item handling → move to `docs/backlog/closed/`, never delete.
- Retro skill update → out of scope; first item to be filed in the new backlog.
