---
date: 2026-08-27
topic: python-sdk-envelope-sweep
branch: claude/python-sdk-task-logs-envelope-0d53a7
range: a340ad7..10595bd
---

# Session Retro: 2026-08-27 - Python SDK Envelope Sweep

**TL;DR:** The Python client library had a method for fetching a task's log output that could never
work - it read the server's reply as if it were a plain list when the server actually sends an
object wrapping that list, so it crashed on every call, for every task, for three and a half
months. The same defect had been fixed in the same file for two neighbouring methods back in June
and this one was missed. This session fixed it, made it fetch long logs page by page instead of
stopping at the first fifty lines, and then checked every other method in the library against what
the server really sends. That check found more problems than the original bug, including a second
method that had never worked in any released version. The most important finding came not from
reading code but from starting a real server and talking to it: jobs submitted without labels send
an empty value the library refused to accept, so listing jobs failed - something four separate code
reviews had all read past.

## Handoff

Branch `claude/python-sdk-task-logs-envelope-0d53a7`, 35 commits, **unmerged and unpushed**. Gates:
`134 passed` on py3.13 AND on py3.9.5 (the CI floor, run not assumed), `ruff check src tests` clean,
`mypy src` clean, `pytest tests/integration` **3/3 against a live `relay-server` + real
`relay-agent`**. Tree clean. `python/` is +1662/-31 over 11 files; nothing outside `python/` except
docs. Closes [[bug-2026-08-25-python-sdk-task-logs-iterates-envelope-keys]] (now in
`docs/backlog/closed/`).

`task_logs()` decodes `LogPage{items,next_seq,total}` and auto-pages `?since_seq=` with the cursor
passed **VERBATIM, never +1** (`WHERE id > $2`), under three stops beyond the server's drained
signal plus `_MAX_LOG_PAGES = 10000`. `LogPage.next_seq`/`total` are **required with no default** -
a defaulted `0` would read a missing key as "drained" and rebuild the defect inside the fix.
`task_logs_page()` is the O(one-page) sibling. `ProtocolError` carries `.records` at all four raise
sites, so a walk that cannot complete still delivers what it collected - the Go original
(`printTaskLogs`) is safe only because it has already PRINTED the rows, and the first port kept the
wording while changing the delivery. Completeness is `len({r.seq for r in out}) >= page.total`, not
`len(out)`: a duplicate-serving server drove the record count to `total` while half the log was
never sent. Version is **0.2.0, not 0.1.3** - `LogRecord.seq` became required.

The sweep: **25 HTTP-performing methods over 18 route+verb pairs, 88 fields across 11 response
models**, 14 findings, 6 fixed here and 8 named. Also fixed because this diff shipped new docs for
them: `follow_job()` raised `ValueError` on its first frame in every released version
(`httpx.Timeout(connect=..., read=None)` needs all four or a default) with **zero tests**, now
observed working live for the first time; and five `rawJSON` wire fields that can arrive `null`
(`Job.labels` and `Reservation.selector` confirmed reachable on a live wire, the other three
defence-in-depth - the split is recorded at `_empty_on_null` in `models.py`).

Four verify rounds, each finding a defect in the previous round's own newest code, terminating on
round 4 (behaviourally clean; 9 of its 12 findings were prose). Nine new backlog items filed plus
three appends. **Next session starts at the merge decision** - this branch has never been pushed and
no PR exists. After that, ROADMAP `Now` needs a refresh; it is stale.

## What Was Built

- **`python/src/relay/client.py`** (+266) - `task_logs` rewritten as an auto-pager, `task_logs_page`,
  `_MAX_LOG_PAGES`, the three stops, `quote()` on the one path-interpolation site this slice
  rewrote, `follow_job`'s timeout fix, `_fetch_all`'s `limit >= 1` guard, and roughly 90 lines of
  comment carrying the arguments no test can: why the cursor is verbatim, why the drained arm must
  sit above the empty-page stop, why the page cap is a request bound and not a hang bound.
- **`python/src/relay/models.py`** (+139) - `LogPage`, required `LogRecord.seq`, `_empty_on_null`
  and its five annotations, `Worker.revoked_at`, six `Job` list-enrichment fields, three
  `EventType` members.
- **`python/src/relay/errors.py`** (+38) - `ProtocolError` with its `.records` snapshot.
- **Tests 93 -> 134.** `test_packaging.py` is new and holds the README-table guard.
- **`python/README.md`** (+134) - the Client API table repaired (it listed 15 of 25 methods), a
  paging section, and four corrected contracts.
- **Docs** - spec (756 lines) and plan (1712 lines) committed at their phase boundaries.

## Key Decisions

- **`LogPage` is a new type, not a generalised `Page`.** The backlog item said to follow the shape
  the list methods settled on. That is unachievable as literally written: different wire key
  (`next_seq` vs `next_cursor`), different type (pydantic v2 will not coerce `int` to `str`), and -
  the load-bearing reason - the int cursor is **ordered**, which is the only thing that makes the
  `next_seq <= since` stop expressible. The two share the PATTERN (`X()` / `X_page()`, dual `limit`
  semantics) and not the type.
- **`follow_job` and the null coercions came in; `follow_job`'s id canonicalisation stayed out.**
  The first two were in scope because this diff ships new documentation for exactly those paths.
  The third was declined and filed: `uuid.UUID()` accepts spellings (`urn:uuid:`, braces) that
  `pgtype.UUID.Scan` may not, so a naive SDK canonicaliser makes the SDK accept MORE than the
  server. The Go fix shares its parse half with the server precisely so it cannot drift; Python
  structurally cannot, so the acceptance surface needs its own RED.
- **The declined findings were filed, not left in the transcript.** The item's own acceptance
  criterion was a sweep that NAMES findings with a count, not one that repairs all of them.

## What Went Wrong and What Changes

**Ledger.** From `2026-08-26-relay-logs-envelope-drift`. *An artifact contradicts itself, in strings
and comments rather than documents* (already promoted to
[[reference_wrong_prose_is_the_dominant_defect]]) - **recurred hard**: round 4 was 9 prose findings
out of 12, including a comment stating a rule and the line four below doing the opposite. *A fixture
that simulates a producer must not import the consumer's types* (promoted to
[[reference_guard_never_sees_real_producer]]) - **applied**, and independently rediscovered as the
`EventType` guard comparing two Python literals in one file. *A registration in a guard is not a
guard if the instrument reads something else* (promoted to
[[reference_match_the_instrument_to_the_claim]]) - **recurred twice**, once as the README-table
guard and once as my own `ls a b` dangling-link check. *After a fix round, the verify lens's subject
is the fix's own diff* (promoted to `docs/agent-team/README.md`) - **applied and vindicated**: three
of four rounds found a defect in the previous round's newest code, and round 4 terminating is again
the only evidence the sequence ends. Promoted lessons used: [[reference_accurate_item_wrong_remedy]]
(three times - the backlog item's paging remedy, my own `return out`, and the README's httpx
remedy), [[reference_verify_the_mutation_applied]] (a `str.replace` hit the wrong function; a CRLF
anchor matched zero times), [[reference_mutation_battery_needs_green_baseline]] (an isolated copy
gave a RED baseline and a false "survived"), [[feedback_backlog_proposal_not_contract]] (seven of
the item's claims refuted), [[reference_added_a_property_forgot_its_guard]],
[[feedback_verify_tree_not_subagent_claims]] (the terminated agent left uncommitted work and a
failing test).

- **A sweep reported an exhaustive count on one axis and was silent about the axis it never
  enumerated.** The spec checked 25 methods and 88 fields against their handlers and reported
  "24 of 25 match on shape". It checked each response's CONTAINER shape (bare array vs envelope)
  and never once checked a FIELD's nullability. `Job.labels` arrives as `null`, the model required
  a dict, and `list_jobs()` therefore raised for any job submitted without labels. Four review
  lenses - including two adversarial ones told to attack the sweep - read past it. Only standing up
  a real server found it, on first contact.
  -> **What changes:** when a sweep reports a count, state the AXIS the count is over and name the
  axes it did not enumerate, in the artifact itself. "25 methods checked" invites the reader to
  believe the endpoint surface is closed; "25 methods checked for container shape; field
  nullability, field arity and error-status mapping NOT checked" is the same work and an honest
  claim. A count without its axis is the enumeration equivalent of a uniqueness claim
  ([[reference_uniqueness_claim_is_about_the_complement]]).
  (promoted to [[feedback_sweep_count_needs_its_axis]])

- **A prescribed remedy named a library capability that does not exist.** The README told an
  operator to bound the log walk's wall-clock and byte exposure with `Client(timeout=)` or an
  injected `http_client`. `httpx.Timeout` has exactly four axes - `connect`, `read`, `write`,
  `pool` - and httpx has no total-time and no response-size setting anywhere. The paragraph's own
  measurement (one request completing in 14.3 s under a 0.5 s read timeout) is the proof that a
  per-read bound is not a per-request one, so the text refuted itself two sentences apart. The
  diagnosis was right and the remedy was unreachable.
  -> **What changes:** extend the existing rule from commands to CAPABILITIES. Before writing "bound
  this with `<library setting>`", read that library's own API surface for the axis you are claiming
  - `dir()` the config object or open its `__init__` signature. A command is checked by grepping the
  CLI; a capability is checked by reading the type. Same property, different instrument.
  (promoted as an extension of [[reference_verify_a_prescribed_command_exists]])

- **A mutation harness crashed after writing the mutant and left the shared worktree silently
  broken.** I ran it against the live tree; `subprocess.run` raised `FileNotFoundError` on a
  relative interpreter path AFTER `write_text(mut)` and BEFORE the restore, so a guard I had just
  added was deleted with no error surfaced. I caught it only because a later grep found one
  occurrence where there should have been two. Then the isolated copy I built to fix it was itself
  invalid twice over: a RED baseline (I forgot to copy `README.md`, whose guard test then failed
  unconditionally) and, worse, `pip install -e .` meant `import relay` resolved to the REAL tree, so
  the mutation was never exercised and reported "survived".
  -> **What changes:** two triggers to add. (1) Any harness that writes a mutant must restore in a
  `finally`, and the run must end by asserting the tree is clean - a crashed harness is
  indistinguishable from a survived mutant. (2) When the package under test is installed editable, an
  isolated COPY is not isolation: set `PYTHONPATH=<copy>/src` and PRINT `module.__file__` to prove
  which tree is loaded, before trusting any result. Both are instances of "a green baseline and a
  control that dies come first" - I had the rule and applied it late.
  (promoted as an extension of [[feedback_mutation_testing_needs_isolated_tree]])

- **My own `/code-review` finding prescribed a fix that would have re-created the defect.** I found
  the page cap discarding a provably-complete log and prescribed `return out`. The correctness lens
  refuted it: `len(out)` counts records APPENDED, not distinct rows, so a server serving page 1
  twice with an advancing cursor drives the count to `total` while half the log was never sent -
  and `return out` would then hand the caller a silently incomplete log presented as complete.
  -> No process change - the pipeline caught it, which is what the pipeline is for. Worth recording
  only as the third instance in one session of an accurate diagnosis with a wrong remedy, all three
  by different authors ([[reference_accurate_item_wrong_remedy]] is already promoted and working).

- **A test asserted the kindness of an error message and never asserted what the caller received.**
  `test_task_logs_page_cap_message_does_not_blame_the_server_when_total_is_reached` checked that the
  message said "every one was collected" and did not say "may be longer". Its own docstring stated
  the client had "in fact collected every row". It never checked whether the caller got them - and
  the caller got nothing. The test was green BECAUSE of the bug, with a careful comment documenting
  the defect's output as the contract.
  -> **What changes:** when a test's subject is a message, add one assertion about the OUTCOME the
  message describes. A message test that does not pin the outcome converts a behavioural defect into
  a documented feature, and the docstring is what makes it survive review.
  (promoted as a fourth shape on [[reference_test_green_because_of_the_bug]])

- **Two verify lenses reported a survived mutation that had never applied.** Round 3's `.records`
  mutants at stops 1 and 2 genuinely survived, but the empty-page test could not have killed them
  under any implementation: its first page was the empty one, so `.records` was `[]` and a dropped
  payload was indistinguishable from a preserved one.
  -> **What changes:** already covered by [[reference_mutation_proof_position]] - a poisoned input
  placed where the assertion has nothing to lose cannot discriminate. Noting the new variant: it is
  not only about EARLY-EXIT mutations. Ask what the assertion's expected value would be under the
  mutant, and if it is the same as under correct code, the fixture is wrong regardless of ordering.

## Recommended Backlog Items

Filed during the session; this is intake, not a priority order. `ROADMAP.md` orders the work.

- See [`bug-2026-08-27-python-sdk-fetch-all-has-no-termination-stops`](../backlog/bug-2026-08-27-python-sdk-fetch-all-has-no-termination-stops.md) - `_fetch_all` walks a server cursor with no stops; a repeating cursor hangs six methods forever. Cross-language with Go `FetchAllPages`.
- See [`bug-2026-08-27-python-sdk-page-cursor-defaults-to-drained`](../backlog/bug-2026-08-27-python-sdk-page-cursor-defaults-to-drained.md) - `body.get("next_cursor", "")` reads a dropped key as drained, on six routes. This slice's own defect shape, one layer over.
- See [`bug-2026-08-27-python-sdk-follow-job-hangs-on-noncanonical-job-id`](../backlog/bug-2026-08-27-python-sdk-follow-job-hangs-on-noncanonical-job-id.md) - an uppercase or dashless job id subscribes to nothing forever. Newly reachable now that `follow_job` works.
- See [`bug-2026-08-27-api-rawjson-passes-null-where-rawobject-normalises`](../backlog/bug-2026-08-27-api-rawjson-passes-null-where-rawobject-normalises.md) - the server-side half of the null-field class, five sites.
- See [`bug-2026-08-27-python-sdk-exceptions-escape-the-relayerror-hierarchy`](../backlog/bug-2026-08-27-python-sdk-exceptions-escape-the-relayerror-hierarchy.md) - `json.JSONDecodeError`, `ValueError`, `KeyError` and `pydantic.ValidationError` all escape `RelayError`.
- See [`bug-2026-08-27-scheduled-jobs-null-job-spec-bypasses-required-guard`](../backlog/bug-2026-08-27-scheduled-jobs-null-job-spec-bypasses-required-guard.md) - found incidentally by the live lane; a misleading 400 blames the wrong field.
- See [`bug-2026-08-27-mypy-python-version-floor-is-silently-not-enforced`](../backlog/bug-2026-08-27-mypy-python-version-floor-is-silently-not-enforced.md) - mypy 2.x warns and exits 0 on `python_version = "3.9"`.
- See [`idea-2026-08-27-sdk-copies-of-server-vocabularies-are-unregistered`](../backlog/idea-2026-08-27-sdk-copies-of-server-vocabularies-are-unregistered.md) - three SDK copies of server vocabularies no lockstep guard knows about.
- See [`idea-2026-08-27-hand-written-to-spec-dict-mappers-need-an-arity-check`](../backlog/idea-2026-08-27-hand-written-to-spec-dict-mappers-need-an-arity-check.md) - the blind spot widened from six fields to twelve this session.
- See [`idea-2026-08-23-cli-tests-never-hit-real-server`](../backlog/idea-2026-08-23-cli-tests-never-hit-real-server.md) - appended: `python/tests/integration/` has never run in CI, and standing it up by hand immediately found what four reading lenses missed. **This is the highest-leverage item this session produced.**
- See [`bug-2026-08-26-cli-and-mcp-interpolate-ids-into-request-paths-unescaped`](../backlog/bug-2026-08-26-cli-and-mcp-interpolate-ids-into-request-paths-unescaped.md) - appended: the item's own asked-for measurement, including the SSRF and header-injection refutations that bound its severity.
- See [`bug-2026-08-26-relayclient-has-no-response-bound-and-no-client-timeout`](../backlog/bug-2026-08-26-relayclient-has-no-response-bound-and-no-client-timeout.md) - appended: the Python numbers (14.3 s under a 0.5 s read timeout, 343x gzip, 0.5-1 KB per record) and the warning that `Client(timeout=)` is not the remedy.

## Files Most Touched

| File | Context |
|---|---|
| `python/tests/unit/test_client.py` (+712) | 28 -> 39 tests. Every log fixture routes through one `_log_page` helper with hand-written keys, deliberately independent of `LogPage`. |
| `docs/superpowers/plans/2026-08-26-python-sdk-envelope-sweep.md` (+1712) | 11 TDD tasks and the stop-to-test kill table walked as a gate. |
| `docs/superpowers/specs/2026-08-26-python-sdk-envelope-sweep.md` (+756) | The sweep tables and the D1-D14 findings list. |
| `python/tests/unit/test_models.py` (+302) | `LogPage`, the five null coercions, the new `Job`/`Worker`/`EventType` fields. |
| `python/src/relay/client.py` (+266) | The whole fix. Most of the diff is comment. |
| `python/src/relay/models.py` (+139) | `LogPage`, `_empty_on_null`, required `seq`. |
| `python/README.md` (+134) | Table repaired 15 -> 25 methods; four contracts corrected. |
| `python/tests/unit/test_packaging.py` (+55) | New. The README-table guard, twice rewritten after it proved fail-open. |
| `python/src/relay/errors.py` (+38) | `ProtocolError` and its `.records` snapshot. |
| `docs/backlog/` (12 files) | Nine new items, three appends, one close. |
