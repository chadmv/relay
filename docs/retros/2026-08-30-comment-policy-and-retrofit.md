---
date: 2026-08-30
topic: comment-policy-and-retrofit
branch: claude/reduce-code-comments-9b8576
range: 10799ec8548705f3c77b304bc3f6f283c1a16fc7..86a76e28bcb6e0c8ae1fe12891b4e12178646e63
---

# Session Retro: 2026-08-30 - Comment Policy and Retrofit

**TL;DR:** The last two sessions each spent four to five review rounds correcting prose - comments
carrying dates, counts, measurements, and claims about other files - against only a few lines of
actual code change. This session attacked the cause instead of the symptoms: it added a written
policy saying what a code comment may contain (a hazard the code cannot show, briefly) and what it
must not (history, measurements, counts, claims about other files, which all drift), wired that
policy into the instructions of the agents who write and review code, and then trimmed the seven
worst-offending files' comments down to comply. Nothing about the software's behavior changed. The
session's own review needed just two rounds, and its only real findings were places where the
trimming itself accidentally created a new wrong claim - which is exactly the defect class the
policy exists to catch.

## Handoff

The comment policy is CLAUDE.md's new `## Comments` section (between Key Design Decisions and
Invariants). Enforcement points: the three implementer briefs' Conventions bullet (which now says
do NOT match surrounding comment density), `relay-code-reviewer.md`'s `## Prose findings` section,
and `docs/agent-team/README.md`'s correctness-lens cell plus fix-round paragraph - prose findings
are deletion-first, correction requires justifying why the claim must live in code. Spec at
`docs/superpowers/specs/2026-08-30-comment-policy-and-retrofit-design.md`, plan beside it.

Retrofit covered seven production files (the spec named four; comment-density measurement pulled in
`server_counters.go` at 72%, `netlimit/listener.go` at 66%, `relayclient/page.go` at 61%):
`internal/api/events.go` 93->57 comment lines, `internal/cli/logs.go` 474->411,
`internal/api/server_counters.go` 422->385, `internal/netlimit/listener.go` 299->283,
`internal/relayclient/page.go` 144->114, `python/src/relay/client.py` 347->228 plus docstrings,
`python/src/relay/models.py` 53->40 plus docstrings. Zero non-comment lines changed, proven by
grep-filtered diffs (Go) and docstring-stripped AST equality (Python). Test files were deliberately
left out; the policy governs them forward.

Verification: one combined review plus one re-verify scoped to the fix diff, both by the same
reviewer agent - 0 high, 3 medium, 8 low, all fixed or explicitly no-action, re-verify clean.
Gates: 22 Go packages `-count=1`, 165 Python unit tests, ruff, mypy. The integration lane and
`-race` were not run - a comments-only diff cannot reach them, and the AST/grep proofs are the
stronger evidence for this diff shape.

Five backlog changes filed at close: four new items (see Recommended Backlog Items) and an update
to `bug-2026-08-27-api-rawjson-passes-null-where-rawobject-normalises` now carrying the
`_empty_on_null` null-path provenance that the models.py docstring gave up.

Branch `claude/reduce-code-comments-9b8576` is complete and green but NOT yet merged or PR'd - the
finishing-a-development-branch options were presented and the session pivoted to this retro
instead. Next session: finish the branch (PR to main), then start at the Now section of
`ROADMAP.md`.

## What Was Built

- The comment policy: hazard stays (a few lines, citing the test or tests that pin it), argument
  moves (to the commit message, spec, or retro). Banned: dates/change history, session or review
  narrative, measurement provenance, counts of anything elsewhere, uniqueness/completeness claims
  about OTHER code, censuses, cross-language claims. A backlog-item filename cited as a pointer is
  an identifier, not history. Test comments state the property pinned and why the input
  discriminates; RED/GREEN provenance goes in the commit.
- The enforcement wiring in the three implementer briefs, the reviewer brief, and the agent-team
  playbook - deletion-first for prose findings, on the measured ground that correcting such claims
  regenerated the defect four times running on one docstring.
- The seven-file retrofit, delete/condense only, with each file's commit message carrying what was
  removed and what was kept.

## Key Decisions

- **Enforcement over guidance.** The prior retro measured that a promoted lesson recurred because
  "nothing consults a memory while drafting a comment"; this slice put the rule where drafting and
  review actually happen (briefs), not only in a memory.
- **Retrofit included, bounded.** Initially scoped out; reconsidered because the existing essays
  are the template implementers imitate and a recurring update tax (PR #163's +36/-14
  comments-only diff to logs.go was maintenance of old prose). Delete/condense only, no new
  guards - load-bearing unpinned claims became backlog items instead of tests.
- **Struck provenance was relocated, not destroyed.** The `_empty_on_null` observed-vs-defensive
  classification moved to the backlog item that tracks its producing mechanism; the retrofit
  commits' messages carry the rest.
- **One combined review, not the four-lens fan-out** - the project's standing convention for
  no-logic slices, and it was sufficient: the re-verify confirmed every fix against source.

## What Went Wrong and What Changes

Ledger: every entry from `2026-08-30-follow-job-canonical-id` was already promoted, so nothing
carries. Promoted lessons that earned their keep this session:
[[feedback_assert_encoding_after_a_programmatic_edit]] and CLAUDE.md's CRLF section were applied by
every editing agent (zero encoding defects across ~20 files of prose edits, and the one `cat >>`
append was caught mixed-eol by the mandated `git ls-files --eol` check);
[[reference_a_replacement_criterion_must_not_be_already_green]] was applied at spec time (both
phases' acceptance verified red at HEAD); [[feedback_relay_the_input_not_just_the_number]] was
applied to the reviewer's findings (both mediums re-verified against source before dispatching
fixes); [[feedback_verify_tree_not_subagent_claims]] was applied after every implementer batch. The
prior session's un-ruled entry - cross-language prose recurring because enforcement was missing -
is what this whole slice answers; its ledger line is the spec.

- **Condensing prose authored new false claims - twice in one file.** Shortening "five renderers
  plus one id-resolver" to "callers are renderers" created a false universal, and merging "queries
  fetch limit+1" with "buildPage emits the cursor" attributed the over-fetch to the wrong function.
  Both were written by an agent under explicit delete-only instructions; both were caught by the
  combined review checking each rewritten comment against source. A third instance (a citation that
  dropped which query parameter a test pins) was the same shape.
  -> **What changes:** when condensing prose, treat every rewritten sentence as newly authored:
  condense an enumeration to the REASON true of all its members, never to a classification of the
  members, and re-verify the result against source exactly as if it were a fresh claim. The fix
  that worked in both cases was restoring the original's own reason-sentence.
  (promoted: extended [[reference_correcting_a_uniqueness_claim]] with the condensation trigger)

- **The implementer's borderline KEEP was wrong, and the deletion-first review contract caught it
  on its first outing.** A date in client.py ("older than 2026-08-30") was judged a version
  identifier by the implementer; review ruled it change history, and the fix (naming the capability
  instead of the day) is strictly better. The policy's carve-out is backlog FILENAMES only.
  -> **What changes:** already encoded - this is the new review contract doing its job. The
  per-file acceptance grep is intake, not the property ([[reference_match_the_instrument_to_the_claim]]
  already states the general rule); borderline keeps go to review, and review defaults to deletion.

- **The conductor broke its own Edit-tool-only rule with a `cat >>` append to a backlog file**,
  leaving a mixed-eol working copy (index stayed `i/lf`, diff proportionate). Caught immediately by
  the post-edit eol check the rule mandates.
  -> No process change - the existing CLAUDE.md rule covers it and its check caught it; the miss
  was momentary non-compliance, not a gap.

## Recommended Backlog Items

All filed during the session:

- See [`idea-2026-08-30-cancel-path-task-frame-absence-is-unpinned`](../backlog/idea-2026-08-30-cancel-path-task-frame-absence-is-unpinned.md) - the CLI reconcile's assumption that cancel publishes no task frames is pinned by nothing.
- See [`idea-2026-08-30-list-overfetch-and-buildpage-routing-is-unpinned`](../backlog/idea-2026-08-30-list-overfetch-and-buildpage-routing-is-unpinned.md) - the limit+1/buildPage universal both SDK cap comments rest on has no structural guard.
- See [`idea-2026-08-30-scheduledjob-absent-means-healthy-has-no-pin`](../backlog/idea-2026-08-30-scheduledjob-absent-means-healthy-has-no-pin.md) - the failure-key omission contract needs a Go-side absence pin.
- See [`bug-2026-08-30-python-sdk-interpolates-ids-into-paths-unescaped`](../backlog/bug-2026-08-30-python-sdk-interpolates-ids-into-paths-unescaped.md) - the Python surface of the path-interpolation class, found by the review in passing.
- See [`bug-2026-08-27-api-rawjson-passes-null-where-rawobject-normalises`](../backlog/bug-2026-08-27-api-rawjson-passes-null-where-rawobject-normalises.md) - updated, now the durable home of the `_empty_on_null` null-path record.

## Files Most Touched

- `docs/superpowers/plans/2026-08-30-comment-policy-and-retrofit.md` (+574) - the plan, including
  the strike/keep rules the retrofit executed.
- `python/src/relay/client.py` (-127 net) - the largest single retrofit; measurement narratives and
  Go-source censuses out, httpx hazard statements kept.
- `internal/cli/logs.go` (-63 net) - dated history and the format-string census out; every hazard
  and lockstep-guard pointer kept, one struck clause restored by review.
- `internal/api/server_counters.go` (-37 net) - modest reduction because most of its prose states
  payload contracts the KEEP rules protect.
- `docs/superpowers/specs/2026-08-30-comment-policy-and-retrofit-design.md` (+146) - the spec.
- `internal/api/events.go` (-36 net) - the flagship canonicalJobIDFilter comment, 58 lines to 26
  with both pinning tests kept.
- `python/src/relay/models.py` (-77 net incl. docstrings) - the `_empty_on_null` classification
  condensed to one policy-compliant clause.
- `internal/relayclient/page.go`, `internal/netlimit/listener.go` - the grep-intake additions.
- `CLAUDE.md` (+25) - the policy itself.
- `.claude/agents/*` (4 files) and `docs/agent-team/README.md` - the enforcement wiring.
