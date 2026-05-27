# Sort examples for non-jobs list CLI help — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add two `--sort` example lines to each of four CLI list sections in README.md (`relay workers list`, `relay reservations list`, `relay schedules list`, `relay admin users list`), matching the existing `relay list` block.

**Architecture:** Documentation-only change. One file modified (`README.md`), eight lines added total (two per section). No code, no tests, no migrations. Comment columns are aligned per-block so comments line up vertically within each ` ```sh ` example.

**Tech Stack:** None — README markdown only. Verification uses `git diff` inspection plus a defensive `make build`.

**Spec:** [docs/superpowers/specs/2026-05-27-sort-examples-cli-help-design.md](../specs/2026-05-27-sort-examples-cli-help-design.md)

---

## Task 1: Add --sort examples to four list-section help blocks

**Files:**
- Modify: `README.md` (four sections: lines ~657-661, ~742-744, ~793-795, ~888-892)
- Modify: `docs/backlog/bug-2026-05-27-sort-flag-cli-help-examples.md` (close + add resolution)
- Move: `docs/backlog/bug-2026-05-27-sort-flag-cli-help-examples.md` → `docs/backlog/closed/bug-2026-05-27-sort-flag-cli-help-examples.md`

### Step 1: Confirm current state of the four blocks

Before editing, verify the README still matches what the spec assumes. If any block has drifted from the snippets below, stop and surface the diff before continuing.

Run: `git grep -n "^relay workers list$\|^relay reservations list$\|^relay schedules list$\|^relay admin users list$" -- README.md`

Expected output includes lines at roughly README.md:658, README.md:743, README.md:794, README.md:889 (line numbers may drift by a few).

- [ ] **Confirmed all four blocks are present.**

### Step 2: Edit the `relay workers list` block

Find this block in `README.md` (around line 657-661):

```sh
relay workers list
relay workers list --limit 10
relay workers list --json
```

Replace it with:

```sh
relay workers list
relay workers list --limit 10
relay workers list --json
relay workers list --sort name             # alphabetical
relay workers list --sort -last_seen_at    # most-recently-seen first
```

Comment column: `#` at column 44. `relay workers list --sort name` (30 chars) + 13 spaces; `relay workers list --sort -last_seen_at` (39 chars) + 4 spaces.

Use the Edit tool with `old_string` matching the three-line current block (including the surrounding fence is not necessary — match just the three command lines for a unique match).

- [ ] **`relay workers list` block updated.**

### Step 3: Edit the `relay reservations list` block

Find this block in `README.md` (around line 742-744):

```sh
relay reservations list
```

Replace it with:

```sh
relay reservations list
relay reservations list --sort name         # alphabetical
relay reservations list --sort starts_at    # chronological by start
```

Comment column: `#` at column 45. `relay reservations list --sort name` (35 chars) + 9 spaces; `relay reservations list --sort starts_at` (40 chars) + 4 spaces.

Because this `relay reservations list` line appears in multiple places in the README (the section heading, the flag table, etc.), match a larger block in your Edit `old_string`: include the fenced ` ```sh ` opening and ` ``` ` closing so the match is unique. Same applies to Steps 4 and 5 if the bare command line appears elsewhere.

- [ ] **`relay reservations list` block updated.**

### Step 4: Edit the `relay schedules list` block

Find this block in `README.md` (around line 793-795):

```sh
relay schedules list
```

Replace it with:

```sh
relay schedules list
relay schedules list --sort name           # alphabetical
relay schedules list --sort next_run_at    # next-to-fire first
```

Comment column: `#` at column 44. `relay schedules list --sort name` (32 chars) + 11 spaces; `relay schedules list --sort next_run_at` (39 chars) + 4 spaces.

- [ ] **`relay schedules list` block updated.**

### Step 5: Edit the `relay admin users list` block

Find this block in `README.md` (around line 888-892):

```sh
relay admin users list
relay admin users list --include-archived
relay admin users list --limit 25
```

Replace it with:

```sh
relay admin users list
relay admin users list --include-archived
relay admin users list --limit 25
relay admin users list --sort email    # alphabetical by email
relay admin users list --sort name     # alphabetical by name
```

Comment column: `#` at column 40. `relay admin users list --sort email` (35 chars) + 4 spaces; `relay admin users list --sort name` (34 chars) + 5 spaces.

- [ ] **`relay admin users list` block updated.**

### Step 6: Inspect the diff

Run: `git diff README.md`

Expected:
- Exactly 8 added lines (`+` prefix), zero removed lines (no `-` prefix lines).
- All 8 added lines start with `relay ` and contain `--sort`.
- Four distinct hunks, one per section.

Run: `git diff --stat`

Expected: `README.md | 8 ++++++++` (or similar; only README.md should appear).

If anything else changed, undo it before continuing.

- [ ] **Diff matches expectations.**

### Step 7: Defensive build check

Run: `make build`

Expected: build succeeds. No README change should affect compilation, but a green build confirms nothing in the worktree drifted.

- [ ] **`make build` passed.**

### Step 8: Update the backlog item frontmatter and add a Resolution section

Edit `docs/backlog/bug-2026-05-27-sort-flag-cli-help-examples.md`.

Change the frontmatter from:

```yaml
---
title: Add --sort example usage to non-jobs list CLI help
type: bug
status: open
created: 2026-05-27
priority: low
source: list-endpoint-sort retro (docs/retros/2026-05-27-list-endpoint-sort.md)
---
```

To:

```yaml
---
title: Add --sort example usage to non-jobs list CLI help
type: bug
status: closed
created: 2026-05-27
closed: 2026-05-27
priority: low
source: list-endpoint-sort retro (docs/retros/2026-05-27-list-endpoint-sort.md)
---
```

(Added `closed: 2026-05-27`; changed `status: open` to `status: closed`.)

Append a `## Resolution` section at the end of the file:

```markdown

## Resolution

README.md now has two `--sort` example lines in each of the four affected CLI sections (`relay workers list`, `relay reservations list`, `relay schedules list`, `relay admin users list`). Sort keys chosen per the per-endpoint allowlist: workers `name`/`-last_seen_at`, reservations `name`/`starts_at`, schedules `name`/`next_run_at`, admin users `email`/`name`. See [docs/superpowers/specs/2026-05-27-sort-examples-cli-help-design.md](../superpowers/specs/2026-05-27-sort-examples-cli-help-design.md).
```

- [ ] **Backlog item frontmatter and Resolution updated.**

### Step 9: Move the backlog item to closed/

Run: `git mv docs/backlog/bug-2026-05-27-sort-flag-cli-help-examples.md docs/backlog/closed/bug-2026-05-27-sort-flag-cli-help-examples.md`

Confirm with: `git status`

Expected: `renamed: docs/backlog/bug-2026-05-27-sort-flag-cli-help-examples.md -> docs/backlog/closed/bug-2026-05-27-sort-flag-cli-help-examples.md` plus the README and backlog file modifications.

- [ ] **Backlog item moved to closed/.**

### Step 10: Commit

Run:

```bash
git add README.md docs/backlog/closed/bug-2026-05-27-sort-flag-cli-help-examples.md docs/backlog/bug-2026-05-27-sort-flag-cli-help-examples.md
git commit -m "docs: add --sort examples to non-jobs list CLI help blocks

Four CLI sections in README.md (workers/reservations/schedules/
admin-users list) now have two --sort example lines each, mirroring
the existing relay list block. Sort keys are members of the
per-endpoint allowlist already documented under #### Configurable
sort order.

Closes bug-2026-05-27-sort-flag-cli-help-examples.

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>"
```

Expected: one commit, `README.md` modified, backlog file renamed and modified.

- [ ] **Committed.**

---

## Self-Review (already performed)

- **Spec coverage:** All four sections from the spec have a dedicated step (Steps 2-5). Backlog closing — required by user's `feedback_backlog_housekeeping` memory — is covered in Steps 8-9.
- **Placeholders:** None. Every step contains the actual lines and commands.
- **Type consistency:** All sort keys used are verified members of the per-endpoint allowlist at README.md:1067-1072 (workers: `name`, `last_seen_at`; reservations: `name`, `starts_at`; schedules: `name`, `next_run_at`; users: `email`, `name`).
