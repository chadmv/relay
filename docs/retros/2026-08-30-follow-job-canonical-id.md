---
date: 2026-08-30
topic: follow-job-canonical-id
branch: claude/pr-merging-session-b8cda7
range: 9c47ad7..a6261b3
---

# Session Retro: 2026-08-30 - Server-Side Job-ID Canonicalisation

**TL;DR:** A job's id can be written several ways - uppercase, without dashes - and the server
accepted all of them when you asked for the job directly. But the live event stream compared the id
you sent against the one spelling the server itself uses, letter for letter, so any other spelling
subscribed to nothing and sat there silently forever. The bug was filed against the Python client,
but measuring both parsers showed a client-side fix could not work: Python and the server disagree
about which spellings are valid in *both* directions, so a client fixing it would reject ids the
server accepts and accept ids the server rejects - three of which silently resolve to a *different*
job. The fix went into the server instead, in four lines, and the client needed no code change at all.

## Handoff

Closes `bug-2026-08-27-python-sdk-follow-job-hangs-on-noncanonical-job-id` (moved to
`docs/backlog/closed/`). `internal/api/handleEvents` routes `?job_id=` through a new
`canonicalJobIDFilter`, which returns `uuidStr(u)` when `parseUUID(raw)` succeeds and `u.Valid`, and
returns `raw` byte-identical otherwise. Python SDK: zero production diff (docstring, README, one
test), no version bump.

**The `err != nil` guard is the whole correctness argument.** `parseUUID` returns the zero UUID on
failure, `uuidStr` renders that as `""`, and `internal/events/broker.go` treats `Filter.JobID == ""`
as broadcast-to-all - so an unguarded render promotes every typo from "one job, silently empty" to
"every job on the cluster". Two tests die when it is deleted:
`TestEvents_JobIDRejectedSpellingsAreNotCanonicalised` (integration, asserts SCOPE via `assert.Never`
against a concurrent canonical control) and `TestCanonicalJobIDFilter`'s passthrough rows (default
lane, no container). It is a scope surprise rather than a privilege escalation - `/v1/events` is
bearer-auth only with no owner gate, and omitting `job_id` is already a full-cluster subscription.

Measured acceptance surfaces, by execution, both directions. `pgtype.UUID.Scan`'s 36-byte branch is
`src[0:8]+src[9:13]+src[14:18]+src[19:23]+src[24:]` - indexes 8, 13, 18, 23 are sliced out
**unexamined**, so `_`, `:`, space and `*` separators all parse, and so does a non-UTF-8 byte. Python
`uuid.UUID` takes brace-wrapped, `urn:uuid:` and stray-dash forms the server rejects; and `+<31 hex>`,
`0x<30 hex>` and a PEP 515 `_` inside 32 hex digits each yield a **different** uuid.

Three items filed/amended: `bug-2026-08-30-empty-job-id-opens-the-broadcast-subscription` (both
`follow_job("")` and `relay logs ""` open the broadcast filter - pre-existing),
`idea-2026-08-30-parseuuid-formats-the-whole-rejected-input-into-a-discarded-error` (measured ~29x
`Scan`'s own cost on a 1 MiB input), and `idea-2026-08-26-six-copies-of-the-uuid-render-format`
amended and narrowed.

Gates: 22 Go packages, full `-tags integration` across all 24 packages, the CLI real-server lane
(238 tests), a `-race` pass over the events tests, 165 Python unit tests, ruff, mypy.

Next session starts at the Now section of `ROADMAP.md`.

## What Was Built

- `canonicalJobIDFilter` - four lines of body, ~35 lines of doc comment. The comment is the argument
  for why the guard cannot be simplified away, and it earns its length.
- `TestCanonicalJobIDFilter` - an exhaustive acceptance table on the default lane: 9 accepted rows
  (including three separator variants and one non-UTF-8 byte) and 10 passthrough rows.
- `TestEvents_JobIDSpellingIsCanonicalisedNotRejected` and
  `TestEvents_JobIDRejectedSpellingsAreNotCanonicalised` in the integration lane.
- A repair to `TestEvents_TaskIDValidation`, which was vacuous (below).

## Key Decisions

- **The fix moved from the SDK to the server**, against the item's own framing. The item said an SDK
  canonicaliser would over-accept; measuring showed it would also under-accept, and the item's own
  "Done When" list was therefore unsatisfiable client-side.
- **Canonicalise, never reject.** `handleEvents` documents "an unknown job yields an open,
  permanently empty stream" as an existing contract. The fix preserves it; `TestEvents_TaskIDValidation`
  pins that an unparseable id still opens a stream.
- **The `!u.Valid` arm is dead at pgx v5.9.1 and kept anyway.** It fails in the closed direction and
  its comment is honest about being defensive rather than live.
- **Python gets no canonicaliser and no version bump** - it deliberately sends the caller's spelling
  verbatim, and a new test pins that.

## What Went Wrong and What Changes

Ledger: the three lessons promoted in the previous retro all fired this session.
[[feedback_never_git_checkout_to_revert_a_mutation]] was applied by every agent that ran a battery.
[[reference_replace_a_cross_language_prose_claim_with_a_guard]] is the lesson this whole slice is an
instance of, and it recurred rather than being avoided - see below.
[[feedback_guard_must_live_in_a_lane_that_runs_on_the_breaking_commit]] was not exercised. Also used:
[[feedback_verify_tree_not_subagent_claims]], [[reference_verify_the_mutation_applied]] (twice, in a
new variant), [[feedback_reproduction_outranks_argument]], [[feedback_relay_the_input_not_just_the_number]]
(against the conductor, below), and CLAUDE.md's CRLF section.

- **A programmatic edit wrote a raw Latin-1 byte into README.md, and every line-ending check passed.**
  `git ls-files --eol` read `i/lf`, the diffstat was proportionate, and the file was nonetheless not
  valid UTF-8 from that commit on. Worse, the damaged literal was 36 bytes and **accepted**, while the
  sentence containing it said "36 characters but 37 bytes, so it is rejected" - the one sentence in
  the round whose own example falsified it, labelled "both measured". Then I reproduced the identical
  defect one file over while adding a test row, writing U+00FF as a character instead of the
  four-character Go escape.
  -> **What changes:** CLAUDE.md's programmatic-edit checklist covers line endings only. Add the
  encoding axis: after any programmatic edit to a tracked text file, assert the file still decodes as
  UTF-8, and assert any non-ASCII you intended is the byte sequence you intended. When an example
  needs a non-ASCII byte, prefer describing it in words or writing it as an escape - a literal in a
  document is unverifiable by eye and survives every check the repo runs.
  (promoted to [[feedback_assert_encoding_after_a_programmatic_edit]] and to CLAUDE.md)

- **A mutation applied, compiled, and was behaviourally inert - and reported as "survived".** The plan
  specified `%011x` as a wrong-width render. `%` width is a MINIMUM and the slice already renders 12
  hex chars, so the mutant was byte-identical. This is one variant past "verify the mutation applied":
  it applied.
  -> **What changes:** for a mutation intended to change an output, assert the mutated code produces a
  DIFFERENT output before running the tests. A diff to the source is not evidence the behaviour moved.
  (extends [[reference_verify_the_mutation_applied]])

- **A test's probe never reached the handler it claimed to test.** `TestEvents_TaskIDValidation`'s
  `?job_id=not-a-uuid` case served an already-cancelled context, so `BearerAuth`'s token lookup
  returned 401 before `handleEvents` ran. Both the spec and the plan asserted this test would go RED
  under a rejection mutation; both were wrong, and the mutation survived it. A lens then swept the
  complement - 95 context sites repo-wide - and found zero remaining instances.
  -> **What changes:** when a test asserts a handler's behaviour, assert first that the handler was
  REACHED - a positive status, a flush, a body - before asserting what it did. `rec.Code == 200` does
  not qualify: `httptest.NewRecorder()` initialises it to 200, so it also holds for a handler that
  wrote nothing.
  (promoted to [[reference_assert_the_handler_was_reached]])

- **I relayed a benchmark I had not measured, and its axes were transposed.** I passed a lens's "~4.2x
  CPU and ~32x allocations" into a fix brief. It did not reproduce; 4.2x is the allocation-BYTES ratio,
  and the framing implied a comparable predecessor when what the fix replaced was a zero-cost
  passthrough. The engineer measured it and corrected me. Later I wrote a claim about
  `internal/scheduler` drift into a commit having measured only the `internal/worker` half, then
  measured it before letting it stand.
  -> **What changes:** [[feedback_relay_the_input_not_just_the_number]] already covers relaying a
  number without its input. The new trigger is narrower: before a measurement enters a commit message
  or a durable doc, run it yourself, even when a lens reported it - a lens's number is an input to
  verify, not a result to forward.
  (extends [[feedback_relay_the_input_not_just_the_number]])

- **A correction to an accidental-coverage claim wrote a fresh one in the same edit.** The fix round
  struck through a backlog Acceptance bullet as "done incidentally" and, three lines below, added a
  replacement bullet already satisfied at HEAD in both packages - so the item would have closed
  vacuously for the same reason.
  -> **What changes:** when striking a criterion as accidentally satisfied, run the replacement
  against HEAD before writing it down. A criterion that is already green is not a criterion.
  (promoted to [[reference_a_replacement_criterion_must_not_be_already_green]]; it is the
  acceptance-criterion instance of [[reference_correcting_a_uniqueness_claim]].)

- **The cross-language prose lesson recurred rather than being avoided.** The previous retro promoted
  "replace a cross-language prose claim with a guard". This slice then wrote a fresh set of Go claims
  into Python docstrings and a README, and four of them were wrong. The lesson was promoted one
  session too early to help, because nothing consults a memory while drafting a comment.
  -> **What changes:** no new rule. The existing memory is correct and the gap is enforcement, not
  knowledge - which is an argument for the guard half of it, not the prose half. Where a claim about
  another language's source is worth making, pin it with a test row in the same commit, as
  `TestCanonicalJobIDFilter`'s non-UTF-8 row now does for the one README claim that had no pin.

## Recommended Backlog Items

Backlog intake, not a priority order.

- See [`bug-2026-08-30-empty-job-id-opens-the-broadcast-subscription`](../backlog/bug-2026-08-30-empty-job-id-opens-the-broadcast-subscription.md) - `follow_job("")` and `relay logs ""` both open the broadcast filter.
- See [`idea-2026-08-30-parseuuid-formats-the-whole-rejected-input-into-a-discarded-error`](../backlog/idea-2026-08-30-parseuuid-formats-the-whole-rejected-input-into-a-discarded-error.md) - ~29x `Scan`'s cost on a rejected 1 MiB input, discarded unread.
- See [`idea-2026-08-26-six-copies-of-the-uuid-render-format`](../backlog/idea-2026-08-26-six-copies-of-the-uuid-render-format.md) - amended, not closed; the render format is now load-bearing on both sides of the client/server boundary.
- [bug] **A tracked backlog file is classified binary by git.**
  `docs/backlog/bug-2026-08-26-cli-and-mcp-interpolate-ids-into-request-paths-unescaped.md` reads
  `i/-text w/-text`, the `\r\r\n` reclassification CLAUDE.md documents. Pre-existing and untouched by
  this slice. While it is binary, `autocrlf` stops normalising it and any edit commits as a whole-file
  rewrite - the exact 1845-insertion failure CLAUDE.md records.

## Files Most Touched

- `internal/api/events.go` (+51/-13) - four lines of production Go and the comment arguing for the guard.
- `internal/api/events_task_log_integration_test.go` (+198) - the headline and scope tests, the
  `TestEvents_TaskIDValidation` repair, and two rounds of provenance corrections.
- `internal/api/events_test.go` (+83) - the default-lane acceptance table.
- `README.md` (+35/-9) - the Normalisation section, twice corrected, once for encoding.
- `internal/cli/logs.go` (+36/-14) - comments only; `canonicalJobID` keeps two jobs no server change reaches.
- `python/tests/unit/test_client.py` (+49) - the verbatim-spelling pin and its corrected mutation.
- `docs/superpowers/{specs,plans}/2026-08-30-follow-job-canonical-id` - the measured acceptance table
  is the spec's core contribution.
- `docs/backlog/idea-2026-08-26-six-copies-of-the-uuid-render-format.md` (+18/-7) - amended, narrowed.
