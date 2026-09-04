---
date: 2026-09-04
topic: workspace-clobber-option
branch: claude/pr-merging-session-65b658
range: 1448ed274f6b4c45bc535566d33aef2fda3b4adc..7c6590aeb505b7f5781f6c972d546c98dd72971a
---

# Session Retro: 2026-09-04 - Workspace Clobber Option

**TL;DR:** If a writable file is left lying in a Perforce workspace, `p4 sync` refuses to overwrite
it and every later task sharing that workspace fails the same way until an operator clears it. This
session added an opt-in setting that lets p4 overwrite such files, off by default because it means
silently destroying local changes. The interesting part was small and specific: nothing in the
repository had ever recorded what a real Perforce options line looks like, so the work observed one
against a live server first - and that observation found the actual hazard, which was a comment
line in the spec that a careless reader would have edited instead of the real field.

## Handoff

Autopilot iteration 4 of 4, and the last of the batch. Closed
[[feature-2026-09-03-workspace-clobber-option]]. Twelve commits: spec `11910a0`, plan `12ee343`,
eight implementation commits `632ad81..2c8004c`, a review-fix round `a2859b7`, then the close and
the filing.

`RELAY_WORKSPACE_CLOBBER` (bool, default off) is parsed by a new `parseBoolEnv` - `strconv.ParseBool`'s
vocabulary, warn-and-fall-back on anything else, silent on empty - onto `perforce.Config.Clobber`,
and `CreateStreamClient` rewrites the `noclobber` token in the fetched spec's `Options:` line.
**OFF is a byte-identical no-op**, including for an operator template that already carries
`Options: clobber`; the item asked for `noclobber` to be forced when off, which contradicts its own
first acceptance criterion.

**What p4 r25.2 actually emits, recorded not predicted:** `Options:\tnoallwrite noclobber nocompress
unlocked nomodtime normdir noaltsync` - seven tokens, not the six every document assumed, and
`noaltsync` is named nowhere in the repo. Nothing had to change, because the transform edits
whatever is present and no test hard-codes a default set. The same run found the load-bearing
hazard: a real spec carries `#  Options:     Client options:` BEFORE the field, so an unanchored
reader takes the comment, fails the token check, and silently no-ops the knob fleet-wide. That
header is in a fixture now and the anchor is pinned against it.

Deliberately not done: the `classifyP4Error` case for "can't clobber writable file". Its message is
a live channel in the open misclassification bug, and a remedy string naming this option would
advertise turning fleet-wide silent overwriting ON to whoever can forge the message.

Verify: 22 packages default and under `-race` in the container; both p4d E2E tests real PASS.
One item filed. Next session starts at ROADMAP.md's Now, whose lead is now the sync-spec exclusion
paths design - the last of the fork-upstreaming batch.

## What Was Built

- **`withClobberOption`**, an anchored reader plus the existing line writer, editing one token of a
  space-separated field. Token equality, not prefix stripping - `noallwrite`/`allwrite` would be
  flipped by a prefix rule.
- **`parseBoolEnv`**, matching the agent's existing warn-and-fall-back idiom rather than the
  server's `log.Fatalf` one.
- Fail-closed handling for a missing, empty or malformed `Options:` line: leave it untouched and
  warn naming both the client and the env var. Never synthesize a one-token option set, whose
  effect on the five unnamed options nobody has observed.

## Key Decisions

- **OFF is a no-op**, against the item's own instruction. An operator template carrying `clobber`
  is a deliberate choice and this slice does not get to override it.
- **The classify case is deferred**, with its precondition named.
- **No wiring guard was pasted in.** It does not exist for this binary, and the slice states its one
  residual unpinned assignment rather than pretending otherwise.
- **Agent-level, never per-task.** The client spec is shared by every task on a stream admitted
  `ModeShared`, so a per-task field would let whichever task arrived first decide for all of them.

## What Went Wrong and What Changes

Ledger: the previous retro's entries were all promoted, so none are carried. Promoted lessons that
fired: [[reference_document_the_mechanism_not_the_coverage]] - the one blocker in this slice was a
README sentence asserting coverage ("no job spec can request it") that the branch's own fixture
refuted; [[reference_wrong_prose_is_the_dominant_defect]] again, with every finding in the round
being prose or a test gap rather than a code defect;
[[reference_a_fix_brief_is_a_set_of_claims]] - written last iteration and fired immediately, since
the engineer refuted three of the brief's own points; [[reference_backlog_proposal_not_contract]];
[[reference_uniqueness_claim_is_about_the_complement]] (three complement claims deleted).
[[feedback_combined_review_trivial_tasks]] was exercised for the first time in this batch - a
low-priority slice got one combined lens instead of a four-way fan-out, and it still produced a
26-mutant battery and a HIGH.

- **An observation nobody had made in the repository's history found the real hazard, and the
  predicted hazard was the weaker one.** The plan expected the anchor to matter because
  `SubmitOptions:` is a field containing `Options:`. It does - but the fixture that pinned it placed
  `SubmitOptions:` after the target line, so the unanchored mutant read the right line anyway and
  survived. The hazard that actually bites is p4's own comment header, present in every real spec,
  and it was found only because the slice was told to observe a live server rather than assert a
  predicted default set.
  -> **What changes:** when a transform parses a format nothing in the repo has ever captured, the
  first task is to capture one - not to write the parser from documentation. The captured artifact
  usually contains a shape the documentation does not mention, and it is usually the shape that
  matters. (promoted to [[reference_capture_the_format_before_parsing_it]])

- **A test that pins a hazard must place the poison BEFORE the subject, and doing so once did not
  generalise.** The item itself named this for the `Description:` case - put the poisoned
  description above the `Options:` line, or a first-occurrence byte replacement survives. The same
  reasoning was needed for `SubmitOptions:` and was not applied, so that fixture pinned nothing
  until the engineer moved it.
  -> **What changes:** when a test exists to kill a "took the wrong match" mutant, the decoy goes
  BEFORE the target, every time - and if one such test in a slice needed that ordering, audit the
  others in the same slice for it rather than treating it as a one-off. (promoted to
  [[reference_a_decoy_goes_before_its_target]])

- **The false claim was the one sentence in the slice asserting a security property.** README said
  there was "no per-job override and no job spec can request it". `client_template` is job-spec
  supplied, `p4 -t` copies the template's `Options:` field, and with the knob off the fetched line
  is written back verbatim - which is precisely the byte-identical no-op the slice guarantees. The
  row even contradicted itself two sentences later.
  -> **What changes:** a documentation sentence asserting that something CANNOT happen is a security
  claim, and it gets a security claim's verification: construct the route, or delete the sentence.
  The nearby prose usually already knows - here the contradiction was two sentences away. (promoted:
  extends [[reference_document_the_mechanism_not_the_coverage]])

## Recommended Backlog Items

- See [`bug-2026-09-04-setspecfield-uses-regex-replacement-semantics`](../backlog/bug-2026-09-04-setspecfield-uses-regex-replacement-semantics.md) - a `$` in the value expands rather than being written literally, and every duplicate field line is rewritten; both halves measured, neither reachable today

## Files Most Touched

- `internal/agent/source/perforce/client.go` - the anchored reader, the token edit, the two warnings.
- `internal/agent/source/perforce/client_test.go` - the token-list helpers that make the vacuity
  trap unreachable, and the fixture carrying p4's real comment header.
- `cmd/relay-agent/main.go` - `parseBoolEnv` and the knob.
- `README.md` - the env row, including the shared-workspace consequence stated as one task's output
  destroyed by another task's sync, and the `client_template` route.
