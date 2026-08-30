# Comment Policy and Retrofit - Design

- **Date:** 2026-08-30
- **Status:** approved-pending-user-review
- **Scope:** docs and agent briefs only in Phase 1; comments-only source edits in Phase 2. No behavior change in either phase.

## Problem

The last two sessions (retros `2026-08-29-python-sdk-strict-page-envelope` and
`2026-08-30-follow-job-canonical-id`) spent 4 and 5 fix rounds over 2 and 4 lines of production
code, with almost every finding in prose: comments, docstrings, test justifications, README claims.
The prose defects share a shape - checkable claims about somewhere else in the tree (counts,
uniqueness claims, censuses, dates, cross-language claims) that are pinned by nothing and drift
when the somewhere-else changes. PR #163's +36/-14 comments-only diff to `internal/cli/logs.go`
was pure maintenance of such claims.

Two measured facts constrain the fix:

1. Guidance-only fails. The 2026-08-30 retro: the cross-language-prose lesson "recurred rather
   than being avoided... because nothing consults a memory while drafting a comment. The gap is
   enforcement, not knowledge."
2. Correction regenerates the defect. The `Page` docstring was corrected four times, wrong every
   time, until the claim was deleted and replaced by a named test. A fix round striking one
   already-green acceptance bullet wrote another in the same edit.

## Design

Two phases. Phase 1 changes the rules; Phase 2 removes the existing prose the rules now forbid,
because the existing essays are both the template new code imitates and a recurring update tax on
every adjacent slice.

### Phase 1a: the policy (CLAUDE.md)

Add a `## Comments` section to CLAUDE.md, placed after `## Key Design Decisions` and before
`## Invariants`. Text (final wording may be lightly edited for fit, content is fixed):

> ## Comments
>
> A comment exists to state a hazard or constraint the code cannot show, in a few lines. It may
> cite the one test that pins the claim ("deleting this guard turns every typo into a broadcast
> subscription; TestCanonicalJobIDFilter's passthrough rows go red"). Everything else - the
> argument that the change is correct, its history, its measurements - goes in the commit
> message, spec, or retro: records of a moment, which cannot drift. If content feels worth
> keeping, it is - in the commit message.
>
> Never put in a comment or docstring:
>
> - Dates or change history ("since 2026-08-30", "was previously two readers"). Git owns history.
> - Session or review narrative, and measurement provenance ("measured by rendering it uppercase
>   and watching that test fail").
> - Counts of anything elsewhere ("16 sites", "four other copies").
> - Uniqueness or completeness claims ("the only", "every", "all N"). These are claims about the
>   complement, pinned by nothing; replace with a named guard or delete.
> - Censuses of other files or packages, and claims about another language's source.
>
> Test comments state the property pinned and why the input discriminates. RED/GREEN history and
> mutation provenance go in the commit that adds the test.

### Phase 1b: implementer briefs

In `.claude/agents/relay-backend-engineer.md`, replace the Conventions bullet "Match the
surrounding code's style, naming, and comment density." with:

> - Match the surrounding code's style and naming, but NOT its comment density - much of the
>   existing density is history the comment policy now forbids. A comment states a hazard or
>   constraint the code cannot show, in a few lines, optionally citing the one test that pins it.
>   No dates, change history, measurement narratives, counts, uniqueness/completeness claims, or
>   censuses of other files - that content goes in the commit message or spec. Test comments
>   state the property pinned and why the input discriminates; provenance goes in the commit.

Add the same bullet to `.claude/agents/relay-frontend-engineer.md` and
`.claude/agents/relay-integration-tester.md` (their Conventions sections currently say nothing
about comments).

### Phase 1c: review-side enforcement

`.claude/agents/relay-code-reviewer.md` gains a short `## Prose findings` section:

> A checkable-but-unpinned claim in an added comment or docstring is itself a finding - counts,
> uniqueness claims, dates, censuses of other files, cross-language claims, measurement
> narratives. The default remedy to suggest is delete, or relocate to the commit message. Suggest
> a corrected wording only with a stated reason the claim must live in code at all: corrections
> to such claims regenerated the defect four times running on one docstring.

`docs/agent-team/README.md`, two edits:

- The correctness lens's brief cell gains: "A checkable-but-unpinned prose claim is a finding;
  the default remedy is deletion or relocation to the commit message."
- The fix-round paragraph (the one opening "After a fix round, the verify lens's primary subject
  is the FIX'S OWN DIFF") gains one sentence: "For prose findings the fix is deletion-first; a
  correction is the remedy that regenerated the defect four times running on one docstring."

### Phase 2: bounded retrofit

A dedicated slice, sequenced after Phase 1 lands so the reviewer has the policy to review
against. Comments-only edits; zero non-comment production diff.

- **Targets:** the known offenders from recent retros - `internal/cli/logs.go`,
  `internal/api/events.go`, `python/src/relay/client.py`, `python/src/relay/models.py` - plus
  whatever a grep over comment lines for banned shapes (4-digit years, "the only", "every",
  "all N", "measured", cross-package censuses) surfaces above roughly ten lines of comment per
  site. The grep list is an intake filter, not a completeness claim; the sweep covers the files
  it names and does not assert no others exist.
- **Delete/condense only, never author.** Apply the keep-rule: the hazard sentence and its one
  pinning-test citation stay; history, censuses, counts, measurements, dates go. Everything
  deleted already survives in git history, the specs, and the retros.
- **No new guards in this slice.** Where a deleted claim looks load-bearing and unpinned, file a
  backlog item rather than writing a test, keeping the sweep reviewable as pure prose removal.
- **Mechanics:** Edit-tool edits only, no scripted rewrites - a mass comment rewrite is the CRLF
  and encoding hazard CLAUDE.md documents. After the sweep: diffstat proportionate to intent,
  `git ls-files --eol` reads `i/lf` on touched paths, files still decode as UTF-8.
- **Review:** one combined review (no-logic change), not the full four-lens fan-out.

## Non-goals

- No mechanical lint or CI guard for comment shapes. Pattern guards of this kind were evaded
  five times running on one parser guard, and would false-positive on legitimate numerals.
- No repo-wide sweep beyond the Phase 2 target list.
- CLAUDE.md's own prose, the retro format, spec/plan format, and commit-message style are
  untouched. Commit messages are the designated destination for displaced content and may stay
  long.
- README API-contract prose is untouched: consumers implement against it (a wrong contract in
  docs is a live defect), so it is governed by the existing replace-claim-with-guard practice,
  not by this policy's deletion default.

## Acceptance

Phase 1:

- CLAUDE.md contains the `## Comments` section above.
- All three implementer briefs carry the condensed policy bullet; the string "comment density"
  appears in them only inside the new bullet's "NOT its comment density" phrasing.
- `relay-code-reviewer.md` contains the `## Prose findings` section; both `docs/agent-team/README.md`
  edits are in place.

Phase 2:

- In the target files, comment lines contain no 4-digit dates, no "the only"/"every"/"all N"
  completeness claims about other code, no measurement narratives, and no cross-file censuses;
  each guard that had a hazard sentence still has one.
- `git diff` for the slice touches comment lines only (verify by stripping comments and
  diffing, or by reading every hunk); all gates green; line-ending and encoding checks pass.

Expected effect (falsifiable in future retros, not an acceptance gate): fix rounds driven by
behavior findings rather than prose corrections, and no further comments-only maintenance diffs
of the logs.go kind.
