---
title: Three cross-file line citations in test files went stale during the cursor-pager extraction and were deliberately left
type: bug
status: open
priority: low
created: 2026-08-14
source: deliberate deferral recorded at Phase 4 of the 2026-08-14-cursor-pager-hook slice, filed at Phase 6 so the deferral does not become an omission
---

# Three cross-file line citations in test files went stale during the cursor-pager extraction and were deliberately left

## Summary

The 2026-08-14-cursor-pager-hook slice deleted four `useState` calls and two or three local functions
from each of seven surfaces, which moved every line below them. It invalidated 13 cross-file line
citations that were accurate on `origin/main`. Ten were fixed in the slice by converting them to
symbol names. **Three were deliberately left**, because they live inside test files that the slice's
zero-diff gate required to be byte-identical to the merge base.

All three were re-verified against the tree at Phase 6:

| Citation | Says | Actually |
|---|---|---|
| `web/src/admin/reservations/ReservationsTab.test.tsx:137` | the phrase "tasks already running on them are unaffected" exists only in the dialog body at `ReservationsTab.tsx:45` | that phrase is now at `ReservationsTab.tsx:46`; `:45` is a bare `}` |
| `web/src/admin/reservations/ReservationsTab.test.tsx:139` | the tab's own footnote is at `ReservationsTab.tsx:253` | the file is **243 lines**, so the citation runs past EOF; the footnote is at `:223` |
| `web/src/admin/enrollments/EnrollmentsTab.test.tsx:263` | the `reset()`-before-reopen convention is at `UsersTab.tsx:238-245` | that convention is now at `UsersTab.tsx:205-207` and `:221` |

The first two are load-bearing prose, not decoration: they document **why** the positive control in
that secrecy test is the phrase it is. The comment records that a previous control
(`/general dispatch pool/i`) also matched the tab's own footnote and therefore "stayed green under
exactly the scope error it existed to catch". A reader following `:45` today lands on a closing brace
and cannot check that reasoning.

## Context

**Why they were left.** The refactor's entire licence was `reference_refactor_gate_byte_identical_tests`:
a zero-line diff to every pre-existing test file, with `git diff --numstat` output as the acceptance
evidence. Editing a comment inside one of those files - even a comment - would have cost the gate its
evidence and replaced a fact with a fact plus a footnote. That was the right call, and it creates
this item.

**Why the fix is not renumbering.** `:45` -> `:46` is a fix with the same expiry date as the defect.
This project has now produced the same class of defect in three consecutive slices:

- 2026-08-13 cross-generation-401: a comment the diff added was invalidated by the same diff before
  it was committed, and a comment in a file the diff never opened became false.
- 2026-08-14 cursor-pager: `InvitesTab.tsx:14` cited `EnrollmentsTab.tsx:16-21` while the same diff
  rewrote the comment block **two lines below** that citation.
- and these three, which are the residue.

Nothing reddens when a line citation goes stale. No test covers it, no type checks it, and the
mechanism that invalidates it is somebody else's diff. **A symbol name cannot drift.**

## Proposal

Convert all three to symbol- or phrase-based references, in a change that touches nothing else:

- `ReservationsTab.test.tsx:137` - cite `deleteWarning()` (or "the delete-warning builder in
  `ReservationsTab.tsx`") rather than a line.
- `ReservationsTab.test.tsx:139` - cite "the tab's own explanatory footnote" rather than a line.
- `EnrollmentsTab.test.tsx:263` - cite "the `reset()`-before-reopen convention in `UsersTab`" rather
  than a line range.

Optional and cheap while in there: a one-paragraph note in the frontend conventions doc stating the
rule, since it is now the third instance.

**Not proposed:** a lint rule or a source scan that validates line citations. That is a scan, and
`reference` from the 2026-08-13 narrow-viewport slice applies - a scan fails open on the pattern it
does not match and fails closed on a compliant file, and the pressure under the second is to weaken
the rule. The durable fix is to stop writing the citations that can drift.

## Acceptance / Done When

- None of the three citations names a line number.
- Each still identifies its target unambiguously enough that the reasoning above it can be checked.
- `web/src/admin/reservations/ReservationsTab.test.tsx` and
  `web/src/admin/enrollments/EnrollmentsTab.test.tsx` are otherwise unchanged - comment-only diffs.
- Full web suite green (this changes no code and no assertion).

## Related

- The slice that created them: `docs/superpowers/specs/2026-08-14-cursor-pager-hook.md`,
  `docs/retros/2026-08-14-cursor-pager-hook.md` ("This diff invalidated 13 line-number citations")
- Prior instance of the same class: `docs/retros/2026-08-13-cross-generation-401.md`
- The gate that forced the deferral: `reference_refactor_gate_byte_identical_tests`
