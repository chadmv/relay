---
date: 2026-08-29
topic: python-sdk-strict-page-envelope
branch: claude/pr-merging-session-b8cda7
range: e7b850a..9c47ad7
---

# Session Retro: 2026-08-29 - Python SDK Strict Page Envelope

**TL;DR:** When the Python SDK asked the server for a long list, it fetched one page at a time and
stopped when the server said there were no more pages. If the server's reply left that "no more"
field out entirely, the SDK treated the missing field as "the list is finished" - so it quietly
returned the first 200 rows and reported success, and nothing in the result told the caller a longer
list had been cut short. The fix makes the field mandatory, so a reply missing it is an error instead
of a silent short list. The larger finding was that the safety argument behind the fix - "the server
always sends that field" - was checked by no test in the project. Deliberately breaking the server
proved 21 of the 22 Go test packages stayed green, and the only thing that noticed was a Python test
suite CI never runs. There is now a Go test that catches it.

## Handoff

Closes `bug-2026-08-27-python-sdk-page-cursor-defaults-to-drained` (moved to `docs/backlog/closed/`).
`Page.next_cursor` and `Page.total` in `python/src/relay/models.py` lose their defaults and become
required, matching `LogPage`. Two field declarations; `client.py` behaviour is unchanged because #161
had already routed both `_get_page` and `_fetch_all` through `Page[model]`. Version `0.2.1 -> 0.3.0`
(MINOR, not the spec's recommended patch): `Page` is in `__all__`, so removing defaults breaks
downstream *constructors* - test doubles and recorded fixtures, which routinely omit keys. Precedent
is `e536f3e`, which added a required `seq` to the already-exported `LogRecord` and took a minor bump.
Note `e536f3e` also introduced `LogPage`; that half is not precedent, since fields required from
birth break nothing.

Two items filed, one edited. `bug-2026-08-29-go-pageenvelope-reads-a-dropped-next-cursor-as-drained`
is the Go peer (`relayclient.PageEnvelope`, same latent defect; `encoding/json` has no required-field
mechanism, so the remedy is a `*string` plus an MCP output-contract decision across five
`PageEnvelope[map[string]any]` decode sites, and six `FetchAllPages` call sites of which one is the
implicit `resolveWorkerIDIn` walk with `userLimit=0`).
`bug-2026-08-27-python-sdk-exceptions-escape-the-relayerror-hierarchy` gained two acceptance criteria:
the `_read_json` wrapper must STRIP `input`, and it must DECIDE the partial-walk question.

`internal/api/pagination_test.go` gained two guards. `TestPageEnvelope_AllThreeKeysArePresentOnAZeroValuePage`
marshals a zero-value `page[T]` and asserts all three keys - the zero-value subject is load-bearing
and kills all three single-tag `omitempty` mutations, because every field sits at its Go zero, which
is exactly what `omitempty` elides. `TestPythonProseCitesThisPackagesEnvelopeGuard` pins the
cross-language symbol reference in both directions; it asserts `func <name>(`, not a bare substring,
because a "renamed from ..." breadcrumb was the one mutation of seven that survived the first version.

Verified against a live `relay-server` + `relay-agent`: 4/4 smoke tests, and the `,omitempty`
mutation reproduced end to end. Gates green - 22 Go packages, 164 Python unit tests, ruff, mypy.

Next session starts at the Now section of `ROADMAP.md`, whose lead after this closes is
`bug-2026-08-27-python-sdk-follow-job-hangs-on-noncanonical-job-id`.

## What Was Built

- `Page.next_cursor: str` and `Page.total: int` - defaults removed. The entire production change.
- Four new tests pinning each field's absence separately (a fixture omitting BOTH would make the two
  declarations indistinguishable), plus the replacement of `test_page_defaults_empty_cursor_and_zero_total`
  by its inverse.
- Two Go guards in `internal/api`, described in the Handoff.
- A spec and a plan, each of which refuted its predecessor: the spec found three wrong claims in the
  backlog item (including an acceptance criterion already satisfied by #161), and the plan found four
  in the spec (including that the spec's own "Go decision" rested on a backlog item that did not
  exist).

## Key Decisions

- **`pydantic.ValidationError` escapes unwrapped**, matching `LogPage`. Wrapping one field of one
  model would make `_get_page` and `task_logs_page` raise different types for the identical defect
  shape, which the tracked `_read_json` chokepoint would then have to unwind.
- **`total` made required too**, not just `next_cursor`. The existing comment called a defaulted
  `total` "the safe direction" because it "only makes a reported number smaller" - true of the
  page-cap message, false of the six `*_page` methods whose callers render it. The argument had been
  scoped to one reader when there are seven.
- **The Go peer filed, not fixed.** Different remedy shape, and it carries an MCP output-contract
  decision that does not belong in a Python slice.
- **`0.3.0` over the spec's `0.2.2`**, on precedent rather than semver theory.
- **The Go guard's lane over its symmetry.** The cross-language guard was first written in
  `python/tests/unit/test_packaging.py` and moved to `internal/api`. The move is the lesson, below.

## What Went Wrong and What Changes

Ledger: every unpromoted lesson from `2026-08-29-fetch-all-termination-stops` is already promoted, so
nothing carries. Promoted lessons that earned their keep this session:
[[feedback_verify_tree_not_subagent_claims]] caught a false HIGH reported by two lenses at once;
[[reference_verify_the_mutation_applied]] and [[reference_mutation_battery_needs_green_baseline]] each
caught a false result; [[reference_wrong_prose_is_the_dominant_defect]] was the single best predictor
of where this session's defects would be; [[feedback_reproduction_outranks_argument]] settled every
disagreement between lenses; CLAUDE.md's CRLF section fired three times.
[[feedback_mutation_testing_needs_isolated_tree]] **recurred, and the conductor caused it** - below.

- **A guard was placed inside the CI path filter of the lane meant to run it, so it could not fire on
  the commit it existed to catch.** The cross-language guard went into
  `python/tests/unit/test_packaging.py`; `.github/workflows/python.yml` filters `pull_request` on
  `paths: python/**`. A PR renaming the Go symbol and touching nothing under `python/` never triggers
  that lane. The guard's own docstring cited that filter as the gap it closed. Caught by a re-verify
  lens that read the workflow file rather than the guard.
  -> **What changes:** when a guard's subject lives outside the directory the guard lives in, read the
  workflow that runs it and confirm no `paths:` filter excludes the subject. Put the guard on the side
  that owns the thing guarded, in a lane with no filter.
  (promoted to [[feedback_guard_must_live_in_a_lane_that_runs_on_the_breaking_commit]])

- **`git checkout -- <file>` reverted an uncommitted change that was itself under test, twice, and the
  second time turned a kill into a false "SURVIVED".** Mid mutation-battery, reverting the mutation
  also reverted the uncommitted guard being measured, so the next mutation ran against a tree with no
  guard in it and "survived". Caught only because a control was re-run afterwards.
  -> **What changes:** when mutating a file that also holds uncommitted work, revert from a file copy
  taken before the battery, never `git checkout --`. Commit the artifact under test first where
  possible, and always re-run the control after the last mutation.
  (promoted to [[feedback_never_git_checkout_to_revert_a_mutation]])

- **A prose claim about another language's source was corrected four times and was wrong every time
  until it was replaced by a test.** `Page`'s docstring said "buildPage is the only thing that builds
  it" (false), then "Sixteen non-test handlers" (16 sites, 9 handlers), then "Every list handler
  builds its own literal" (false - `handleListTasks` and `handleListWorkerWorkspaces` return bare
  arrays), then a fourth pass that finally held. Each correction was written by someone who had just
  been told the previous one was wrong. What ended it was deleting the checkable claim and citing a
  Go test by name instead.
  -> **What changes:** when prose in one language makes a checkable claim about another language's
  source, do not correct the claim - replace it with a named executable guard and cite that. A count
  or quantifier across a language boundary is pinned by nothing and drifts on the next endpoint.
  (promoted to [[reference_replace_a_cross_language_prose_claim_with_a_guard]])

- **A security caveat was wrong in one direction, then over-claimed in the other, and both versions
  read as measured.** The README first said `logger.exception` leaks "only when the entire page repr
  fits inside the ~50-character window" - backwards, because pydantic truncates head AND tail, so the
  tail is emitted at every size. The correction then said a Go field reorder "would silently move a
  credential into the window" - also wrong: through `Page[T]` the tail is spent mostly on the envelope
  trailer, and the measured worst case exposes 4 of 23 characters. Both versions cited a real
  measurement of a neighbouring case.
  -> **What changes:** when writing a redaction or truncation claim, measure the exact case being
  claimed, not an adjacent one, and state the bound rather than the warning. A measurement of a bare
  dict establishes nothing about the same value inside an envelope.

- **The conductor told an agent to mutate the shared worktree, and two parallel lenses reported the
  in-flight mutation as a HIGH finding.** [[feedback_mutation_testing_needs_isolated_tree]] says
  exactly not to do this. Both lenses reported `internal/api/pagination.go` as dirty with an
  `,omitempty` mutation about to ship; both were reading the integration lens's live experiment.
  -> **What changes:** no new rule - the memory already states it. The gap was the conductor applying
  it to the review-lens briefs but not to the integration brief, which is the one that mutates by
  design.

- **The session hit its usage limit mid fix-round**, terminating a subagent that had made no edits.
  -> No process change - one-off. The tree was clean and the round was re-run inline.

## Recommended Backlog Items

Backlog intake, not a priority order.

- See [`bug-2026-08-29-go-pageenvelope-reads-a-dropped-next-cursor-as-drained`](../backlog/bug-2026-08-29-go-pageenvelope-reads-a-dropped-next-cursor-as-drained.md) - the Go peer of the closed defect.
- See [`idea-2026-08-29-scheduledjobresponse-field-order-is-an-unpinned-redaction-input`](../backlog/idea-2026-08-29-scheduledjobresponse-field-order-is-an-unpinned-redaction-input.md) - a redaction bound in the README depends on an unpinned Go field order.
- See [`idea-2026-08-23-integration-only-guards-ci-never-runs`](../backlog/idea-2026-08-23-integration-only-guards-ci-never-runs.md) - appended as the tenth instance, and the first partly retired rather than only recorded. The sharpened question: is `python/tests/integration/` ever meant to run automatically? The answer decides what future slices may prove there.

## Files Most Touched

- `python/src/relay/models.py` - the two field declarations, and four generations of the `Page`
  docstring.
- `python/README.md` (+83) - the fail-closed advertisement, the `LogPage` scope correction, and two
  rounds on the `errors()` retention caveat.
- `internal/api/pagination_test.go` (+129) - both new guards. The only Go file the slice touches.
- `python/tests/unit/test_client.py` (+113) - the envelope-ABSENCE section; fixtures omit exactly one
  key apiece.
- `python/tests/unit/test_models.py` (+61) - `test_page_requires_next_cursor_and_total` replacing the
  test that asserted the removed behaviour.
- `python/src/relay/client.py` (+48) - comments only; no behaviour change.
- `python/tests/integration/test_smoke.py` (+25) - the live-server property, twice corrected for
  mutation provenance.
- `docs/superpowers/specs/2026-08-29-python-sdk-strict-page-envelope.md` (+602) and the plan beside it
  (+947) - each carrying its own supersede notes.
- `python/pyproject.toml`, `python/src/relay/_version.py` - `0.3.0`, in lockstep.
