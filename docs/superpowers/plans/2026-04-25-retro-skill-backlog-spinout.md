# Retro Skill: Spin Out Open Questions & Known Limitations as Backlog Items — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Extend the `retro` skill so that, when a draft retro contains `## Open Questions` or `## Known Limitations`, it offers to spin each bulleted item out as a `docs/backlog/` entry — turning deferred work into a first-class artifact that future sessions can find.

**Architecture:** Insert a new `Step 5` between the existing draft-and-wait (`Step 4`) and the existing branch check (which becomes `Step 6`). The new `Step 5` parses bullet items from the two target sections, offers them in a single message, and on accept queues `(type, title, summary, original-bullet-locator)` tuples in memory. The new `Step 7` (formerly `Step 6` — commit) consumes the queue: for each queued item it invokes the `/backlog` skill via its terse path (`/backlog <type> <title> -- <summary>`), takes the resulting filename from `/backlog`'s output, rewrites the corresponding retro bullet to link to it, and only then commits the retro. Delegating to `/backlog` reuses the slug derivation, frontmatter format, file path convention, and per-item commit message convention already centralized there. Holding the queue across the branch check (Step 6) means a user who aborts after the warning ends with no orphaned backlog commits. No code change — this is a pure markdown edit to `~/.claude/skills/retro/SKILL.md`. Verification is dry-runs against a recent retro that has populated Open Questions / Known Limitations sections.

**Tech Stack:** Markdown (skill instruction text). The new flow uses the `Skill` tool to invoke `/backlog` from within `/retro`. No build, no runtime dependencies. Validation is by dry-run in a real session.

---

## File Structure

**Modified:**
- `~/.claude/skills/retro/SKILL.md` — insert a new `## Step 5: Offer to spin out deferred work as backlog items`; renumber the three existing steps after it (`Step 5 → 6`, `Step 6 → 7`, `Step 7 → 8`); modify the body of the renumbered `Step 7` (commit) to invoke `/backlog` for each queued item and rewrite retro bullets with the resulting filenames before committing the retro.

**Touched (verification only):**
- One project's `docs/retros/` and `docs/backlog/` — used as a dry-run target. Dry-run produces no committed artifacts.

**Closed (in this repo):**
- `docs/backlog/idea-2026-04-25-update-retro-skill-open-questions.md` → `docs/backlog/closed/...` after verification passes.

The retro skill itself lives outside any single project's repo (`~/.claude/skills/retro/SKILL.md` is user-level configuration), so no commits there.

---

## Step Numbering — Before vs After

| Old | New | Body |
|---|---|---|
| Step 1 | Step 1 | Non-Trivial Check (unchanged) |
| Step 2 | Step 2 | Determine Commit Range (unchanged) |
| Step 3 | Step 3 | Derive Topic Slug (unchanged) |
| Step 4 | Step 4 | Write the Retro File (Draft) — unchanged. Still ends with "Wait for user confirmation before proceeding." |
| — | **Step 5 (NEW)** | Offer to spin out deferred work as backlog items |
| Step 5 | Step 6 | Branch Check (body unchanged) |
| Step 6 | Step 7 | Commit the Retro — body modified to consume the queue |
| Step 7 | Step 8 | Ensure Session-Continuity Directive (body unchanged) |

The user-facing wait at the end of `Step 4` stays where it is. New `Step 5` runs **after** the user has confirmed "commit" — so the spin-out offer is presented as a follow-up question, not a precondition for committing the retro.

---

## Design Notes (read before Task 1)

**Why both sections, with different default types?**
`## Known Limitations` describes *something currently broken or constrained* — maps to `bug`. `## Open Questions` describes *unresolved decisions* — maps to `idea`. The user can override per-item if needed.

**Why queue, not invoke `/backlog` immediately?**
The `/backlog` skill commits each item the moment it's invoked. If the retro skill called `/backlog` at the moment the user accepted the offer (inside Step 5), and the user then aborted at the Step 6 branch-check warning, the backlog items would be committed but the retro would not — an orphan state on a possibly-wrong branch. Queueing in memory means everything happens after the branch check has been navigated.

**Why use `/backlog`'s terse path rather than its interactive or fast paths?**
- Interactive (`/backlog` no args) prompts for each field — wrong UX when retro already has the title/summary.
- Fast (`/backlog <type> <title>`) infers summary from conversation context and asks for confirmation — adds N extra confirmations.
- Terse (`/backlog <type> <title> -- <summary>`) uses the supplied summary verbatim with no further prompts. Exactly what we want: the retro skill has already gotten user confirmation in its own offer message.

**What gets parsed as an "item"?**
A top-level bullet — a line beginning with `- ` or `* ` at the start of the line, inside a `## Open Questions` or `## Known Limitations` section. Sub-bullets (indented bullets) fold into the parent bullet's summary, not split into separate items. Prose paragraphs inside these sections are ignored.

**Section ordering in the offer:**
The existing skill's section menu lists `## Known Limitations` before `## Open Questions`. The offer preserves that document order (Limitations items first, then Questions items).

**Empty sections / missing backlog dir:**
If both sections together yield zero items, skip Step 5 silently — no offer message. If `docs/backlog/` does not exist in the project, skip Step 5 silently — `/backlog` will bootstrap it on first use, but the retro skill should not bootstrap from inside its flow.

**Bullet rewrite — when?**
At the moment `/backlog` returns in Step 7, with the actual filename from `/backlog`'s output (`Filed: docs/backlog/<filename>`). Rewriting earlier (in Step 5) would require duplicating slug derivation and same-day collision resolution — the very logic we're trying not to duplicate.

**Title default heuristic:**
First sentence of the bullet text, with leading `**bold markup**` stripped, trimmed to ~70 chars. If the bullet starts with `**Title.**` followed by detail text, use the bolded title verbatim. Acceptable v1 limitation: bullets starting with prose like "The reason we should..." may produce awkward titles; the user can edit the title in the offer or rename the resulting backlog file later.

---

## Task 1: Insert new Step 5 (detect-offer-queue) and renumber subsequent steps

**Files:**
- Modify: `~/.claude/skills/retro/SKILL.md`

- [ ] **Step 1: Read the current retro skill end-to-end**

```bash
cat ~/.claude/skills/retro/SKILL.md
```

Note the exact text of:
- The closing of current Step 4 (lines around `Wait for user confirmation before proceeding.`)
- The current `## Step 5: Branch Check` header

The Edit tool insertion will land between these two anchors.

- [ ] **Step 2: Insert the new Step 5 into `~/.claude/skills/retro/SKILL.md`**

Use the Edit tool. Insert immediately AFTER the line `**Wait for user confirmation before proceeding.**` (end of current Step 4) and BEFORE `## Step 5: Branch Check`.

The exact text to insert:

````markdown

## Step 5: Offer to spin out deferred work as backlog items

After the user confirms the commit (and before the branch check), look at the draft for items in `## Open Questions` and `## Known Limitations`. If both sections are absent, both empty, or `docs/backlog/` does not exist in the project, skip this step silently and continue to the next step.

**Detection:**

Parse top-level bullet lines (lines beginning with `- ` or `* ` at column 0) inside `## Known Limitations` and `## Open Questions`. Indented sub-bullets fold into the parent item's summary. Prose paragraphs are ignored.

For each detected item:
- **Default type:** `bug` for items in `## Known Limitations`; `idea` for items in `## Open Questions`.
- **Default title:** the first sentence of the bullet text, with leading `**bold markup**` stripped, trimmed to ~70 characters. If the bullet starts with `**Title.**` followed by detail text, use the bolded title verbatim.
- **Summary:** the full bullet text (including any folded sub-bullets), with the leading `- ` or `* ` stripped.

**Offer:**

Present a single message listing the items in document order (Known Limitations first, then Open Questions):

> Found N items that could become backlog entries:
> 1. [bug] Sweeper still uses an independent Registry instance
> 2. [bug] `parseDurationEnv` silently falls back on garbage input
> 3. [idea] Should the warm-preference scoring be configurable?
> 4. [idea] Is `last_used_at` accurate enough for the sweeper's age policy?
>
> File all, a subset (e.g. `1,3`), or none?

**Parse the response:**

- `all` / `yes` / `y` → all items.
- A comma- or space-separated list of indices (`1,3` or `1 3`) → that subset.
- `none` / `skip` / `no` / `n` / empty → skip the spin-out, no queue, continue to the next step.
- Anything else (including questions like "what's #2?") → answer the question, then re-prompt with the same offer.

**For each accepted item, queue (do NOT invoke `/backlog` yet, do NOT touch the retro file yet):**

Add to an in-memory queue a tuple of:
- `type` (`bug` or `idea`)
- `title` (string)
- `summary` (string — full bullet text with sub-bullets)
- `original_bullet_line` (the exact line in the retro draft, used later to locate-and-replace)

The queue is consumed in Step 7 (after the branch check). Holding it across the branch check ensures that a user who aborts at the branch warning ends with no orphaned backlog commits.

**Output:**

> Queued N backlog items. They will be filed via the `/backlog` skill when the retro is committed.

If zero items were accepted, output nothing and continue silently.
````

- [ ] **Step 3: Verify the insertion landed correctly**

```bash
grep -n "^## Step" ~/.claude/skills/retro/SKILL.md
```

Expected output, in order: `Step 1`, `Step 2`, `Step 3`, `Step 4`, `Step 5: Offer to spin out`, `Step 5: Branch Check`, `Step 6: Commit`, `Step 7: Ensure Session-Continuity`.

(Two `## Step 5` headers will exist after this Edit — the new one and the not-yet-renumbered old one. Step 4 of this task fixes that.)

- [ ] **Step 4: Renumber the three existing steps below the insertion**

Using the Edit tool, change three header lines:

- `## Step 5: Branch Check` → `## Step 6: Branch Check`
- `## Step 6: Commit the Retro` → `## Step 7: Commit the Retro`
- `## Step 7: Ensure Session-Continuity Directive (user-level, one-time)` → `## Step 8: Ensure Session-Continuity Directive (user-level, one-time)`

Each is a unique line so a single `Edit` call per header works.

- [ ] **Step 5: Verify the final header order**

```bash
grep -n "^## Step" ~/.claude/skills/retro/SKILL.md
```

Expected output, in order: `Step 1`, `Step 2`, `Step 3`, `Step 4`, `Step 5: Offer to spin out`, `Step 6: Branch Check`, `Step 7: Commit the Retro`, `Step 8: Ensure Session-Continuity`.

If the order is wrong or any step is missing/duplicated, fix and re-verify before moving on.

- [ ] **Step 6: No commit**

The retro skill lives at `~/.claude/skills/retro/SKILL.md`, outside any project repo. There is nothing to commit. Proceed directly to Task 2.

---

## Task 2: Modify the renumbered Step 7 (commit) to consume the queue via `/backlog`

**Files:**
- Modify: `~/.claude/skills/retro/SKILL.md` (replace the body of `## Step 7: Commit the Retro`)

- [ ] **Step 1: Read the current Step 7 body**

After Task 1, Step 7's body (carried over from old Step 6) is:

```markdown
## Step 7: Commit the Retro

```bash
git add docs/retros/YYYY-MM-DD-<topic>.md
git commit -m "docs: add session retro YYYY-MM-DD-<topic>"
```
```

- [ ] **Step 2: Replace Step 7's body**

Use the Edit tool to replace Step 7's body with the following. The header line `## Step 7: Commit the Retro` stays; only the body underneath changes.

````markdown
## Step 7: Commit the Retro

If Step 5 queued any backlog items, drain the queue first — invoking the `/backlog` skill once per item, in queue order. This delegates slug derivation, file structure, frontmatter, and the per-item commit to the centralized `/backlog` skill rather than duplicating those concerns here.

For each queued tuple `(type, title, summary, original_bullet_line)`:

1. Invoke the `/backlog` skill via its terse path:

```
/backlog <type> <title> -- <summary>
```

Use the `Skill` tool with `skill: "backlog"` and `args: "<type> <title> -- <summary>"`. The terse path uses the supplied summary verbatim with no further confirmation prompts (see Step 2 of `~/.claude/skills/backlog/SKILL.md`).

2. The skill writes the file, commits it (`backlog: add <filename-without-extension>`), and reports `Filed: docs/backlog/<filename>`. Capture that filename.

3. Replace `original_bullet_line` in the retro draft (use the Edit tool) with:

```markdown
- See [`<filename-without-extension>`](../backlog/<filename>) — <title>
```

(Path is relative to `docs/retros/`. The link target is the file `/backlog` just committed, so the link resolves immediately.)

After all queued items are drained (or immediately, if the queue was empty), commit the retro:

```bash
git add docs/retros/YYYY-MM-DD-<topic>.md
git commit -m "docs: add session retro YYYY-MM-DD-<topic>"
```

The git log on the branch will read:
```
backlog: add <slug-1>
backlog: add <slug-2>
...
docs: add session retro YYYY-MM-DD-<topic>
```

— matching the per-item commit convention used everywhere else `/backlog` is invoked.
````

- [ ] **Step 3: Verify the file edit**

```bash
grep -A2 "## Step 7: Commit the Retro" ~/.claude/skills/retro/SKILL.md
```

Expected: the new "If Step 5 queued any backlog items..." prose is the first line under the Step 7 header.

```bash
grep -c "/backlog <type> <title> -- <summary>" ~/.claude/skills/retro/SKILL.md
```

Expected: `1` (the terse-path invocation pattern is in Step 7).

```bash
grep -n "^## Step" ~/.claude/skills/retro/SKILL.md
```

Expected, in order: `Step 1`, `Step 2`, `Step 3`, `Step 4`, `Step 5: Offer to spin out`, `Step 6: Branch Check`, `Step 7: Commit the Retro`, `Step 8: Ensure Session-Continuity`.

- [ ] **Step 4: No commit**

(Same as Task 1 — file lives outside any project repo.)

---

## Task 3: Dry-run verification against a recent retro

**Files:**
- Read-only: `docs/retros/2026-04-25-perforce-workspace-management.md` (this retro has populated Open Questions and Known Limitations sections — it's an ideal test fixture).

- [ ] **Step 1: Sanity-check the test fixture**

```bash
grep -E "^## (Open Questions|Known Limitations)" docs/retros/2026-04-25-perforce-workspace-management.md
```

Expected: both headers present.

```bash
grep -cE "^- " docs/retros/2026-04-25-perforce-workspace-management.md
```

Expected: at least 5 lines (the perforce retro has 5 Known Limitations + 3 Open Questions among other bulleted lists).

This file will not be modified by the dry run — we use its content only as a model for what the spin-out should detect.

- [ ] **Step 2: Confirm the early-exit path still works**

In a clean child session, simulate `/retro` with HEAD at the current commit and no new work since the most recent retro. Verify Step 1 (Non-Trivial Check) still returns "Nothing to retro — no meaningful changes this session." This confirms the renumbering did not break the early-exit branch.

- [ ] **Step 3: Walk Step 5 against the perforce-retro fixture**

Simulate Step 5 against `docs/retros/2026-04-25-perforce-workspace-management.md`. List:

a) The N items it would detect — expect 8 total: 5 from `## Known Limitations` (`bug`) + 3 from `## Open Questions` (`idea`).
b) The proposed offer message — verify item ordering (Limitations first, then Questions) and that the type tags `[bug]` / `[idea]` are correct.
c) For one chosen item (e.g. item 1, "Sweeper still uses an independent `Registry` instance."), draft what would land in the queue:
   - type: `bug`
   - title: `Sweeper still uses an independent Registry instance`
   - summary: the full bullet text, including any sub-bullets, with the leading `- ` stripped
   - original_bullet_line: the exact verbatim line from the retro

Confirm by inspection that:
- The proposed title is sensible and human-readable.
- The summary is non-empty and includes the full content of the bullet.

- [ ] **Step 4: Walk Step 7 against a non-empty queue**

For the same chosen item, simulate Step 7's drain logic:

1. The retro skill invokes `/backlog bug Sweeper still uses an independent Registry instance -- <full-summary>` via the `Skill` tool.
2. `/backlog` derives slug per its Step 3 rules — predict it (e.g. `sweeper-uses-independent-registry`).
3. `/backlog` writes `docs/backlog/bug-2026-04-25-<slug>.md`, commits with `backlog: add bug-2026-04-25-<slug>`, outputs `Filed: docs/backlog/bug-2026-04-25-<slug>.md`.
4. The retro skill captures the filename and rewrites the bullet to `- See [\`bug-2026-04-25-<slug>\`](../backlog/bug-2026-04-25-<slug>.md) — Sweeper still uses an independent Registry instance`.
5. After the queue is drained, the retro file is committed.

Confirm by inspection that the resulting git log reads:
```
backlog: add bug-2026-04-25-<slug>
docs: add session retro 2026-04-25-<topic>
```
— with the backlog commit landing first (so the retro's link target exists in git history when the retro commit lands).

- [ ] **Step 5: Walk the empty-queue path**

Simulate: user reaches the Step 5 offer, says `none`. Queue is empty. Step 6 branch check runs. Step 7 skips the drain (queue empty) and goes directly to the retro commit. Verify only one commit lands (`docs: add session retro ...`), no `backlog: add` commits.

- [ ] **Step 6: Walk the abort-at-branch-check path**

Simulate: user accepts items at Step 5 (queue has 2 items). At Step 6 branch check, user is warned about a non-main branch and says "switch branches first." Verify:
- Step 7 does NOT run.
- No `/backlog` invocations happen.
- No backlog files appear in `docs/backlog/`.
- No commits land.

This is the orphan-prevention property the queue exists to guarantee.

- [ ] **Step 7: Walk the empty-section / no-bullets path**

Simulate: a retro with `## Open Questions` present but no bullets (only a paragraph), and `## Known Limitations` absent. Confirm Step 5 detects 0 items and skips silently — no offer message, no queue, no output.

- [ ] **Step 8: Walk the no-backlog-dir path**

Simulate: a project with NO `docs/backlog/` directory. Confirm Step 5 skips silently with no offer message.

- [ ] **Step 9: Document any defects found**

If any walk surfaces a problem (offer message unclear, link path wrong, queue handling broken, slug-collision interaction with `/backlog` misfires), edit the skill text and re-run the relevant walks. Do NOT proceed to Task 4 until all walks pass.

- [ ] **Step 10: No commit**

Dry-run verification produces no committed artifacts.

---

## Task 4: Close the originating backlog item

**Files:**
- Move: `docs/backlog/idea-2026-04-25-update-retro-skill-open-questions.md` → `docs/backlog/closed/idea-2026-04-25-update-retro-skill-open-questions.md`

- [ ] **Step 1: Confirm the file is still open**

```bash
ls docs/backlog/idea-2026-04-25-update-retro-skill-open-questions.md
```

Expected: file exists at top level of `docs/backlog/`, not in `closed/`.

- [ ] **Step 2: Close via the `/backlog` skill**

Invoke `/backlog close idea-2026-04-25-update-retro-skill-open-questions fixed`. The skill will:
- `git mv` the file to `docs/backlog/closed/`.
- Update its frontmatter (`status: closed`, `closed: 2026-04-25`, `resolution: fixed`).
- Append a `## Resolution` section with a 1–2 sentence note: "Implemented via Step 5 (offer to spin out Open Questions / Known Limitations as backlog entries) and Step 7 (drain the queue by invoking `/backlog` per item, then commit the retro). The retro skill now delegates backlog-item creation to the `/backlog` skill rather than duplicating its conventions."
- Commit with `backlog: close idea-2026-04-25-update-retro-skill-open-questions`.

- [ ] **Step 3: Verify**

```bash
ls docs/backlog/closed/idea-2026-04-25-update-retro-skill-open-questions.md
git log --oneline -3
```

Expected: file is in `closed/`; the most recent commit is `backlog: close idea-2026-04-25-update-retro-skill-open-questions`.

---

## Self-Review

**Spec coverage** (against the originating backlog item's Proposal):
- "After drafting `## Open Questions` or `## Known Limitations` sections, the retro skill prompts: 'Want me to file these as backlog items?'" → Task 1 Step 2 (the offer message in new Step 5).
- "Each accepted item gets an `idea-` or `bug-` backlog entry" → Task 2 Step 2 (Step 7 invokes `/backlog` per queued item with the right type defaults set in new Step 5).
- "The retro section becomes a brief mention with a link to the backlog file rather than the system-of-record" → Task 2 Step 2 (post-`/backlog` bullet rewrite).

**Placeholder scan:** No "TBD", "TODO in plan", "fill in details", or unimplemented references. Every step shows the exact text to insert or the exact command to run.

**Type / name consistency:**
- Step numbers: every reference to "Step 5" inside the inserted bodies refers to the new spin-out step; every reference to "Step 7" refers to the renumbered commit step. The summary table at the top of this plan locks the mapping.
- "queue" / "queued items" — same concept used consistently in new Step 5 (where it's populated) and new Step 7 (where it's drained).
- Terse-path invocation `/backlog <type> <title> -- <summary>` — matches `/backlog`'s Step 2 verbatim.
- Commit message pattern `backlog: add <filename-without-extension>` — produced by `/backlog` itself, not by the retro skill, so no risk of drift.

**Edge cases addressed:** abort-at-branch-check (Task 3 Step 6 — the queue's reason for existing), empty sections (Task 3 Step 7), missing backlog directory (Task 3 Step 8), empty queue → just commit retro (Task 3 Step 5).

**One known soft spot:** the title heuristic ("first sentence trimmed to ~70 chars, with leading bold stripped") may produce awkward titles for bullets that start with prose like "The reason we should...". Documented as an acceptable v1 limitation; the user can edit titles in the offer or rename backlog files later.

---

## Execution Handoff

Plan complete and saved to `docs/superpowers/plans/2026-04-25-retro-skill-backlog-spinout.md`. Two execution options:

**1. Subagent-Driven** — Fresh subagent per task with two-stage review between tasks. Overkill for a 4-task plan that mostly edits one markdown file.

**2. Inline Execution (recommended for this plan)** — Execute tasks in this session using `superpowers:executing-plans`. The plan is small (one file edit + one verification + one backlog close), so checkpoints between tasks are sufficient.

Which approach?
