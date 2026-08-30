# A server-supplied cursor must not be able to drive a client loop forever - implementation plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Give `relay.Client._fetch_all` (Python SDK, six public `list_*` methods) and `relayclient.FetchAllPages` (Go, six CLI call sites) the three termination stops their sibling log walks already have - an empty-page stop, a repeated-cursor stop, and a page cap - so a server answering `next_cursor: "SAME"` forever raises instead of hanging.

**Architecture:** One loop, six checks, in a fixed order, implemented twice against the same server contract. Python raises `ProtocolError` with the collected rows on `.records`; Go returns `nil, 0, err` because its five renderers and one id-resolver have nowhere to put a partial list. The non-advancing predicate is a *set* of every cursor the walk has accepted, because a two-cycle (`A,B,A,B`) is what a load balancer in front of two replicas produces and a previous-cursor comparison never sees it. No hashing, no cursor decoding, no cycle-detection re-traversal - all three declined with reasons in the spec's section 5.1.

**Tech Stack:** Python 3.9-3.13, pydantic v2, httpx (`httpx.MockTransport`), pytest, ruff, mypy strict. Go 1.26, `net/http/httptest`, testify.

Spec: `docs/superpowers/specs/2026-08-28-fetch-all-termination-stops.md`
Backlog item: `docs/backlog/bug-2026-08-27-python-sdk-fetch-all-has-no-termination-stops.md`

---

## Slice independence declaration

**Two sub-slices on ONE branch, SEQUENTIAL, one PR, one session. Not a multi-stage plan.**

- **This is NOT a `/backlog phases` plan.** It is one backlog item, one spec, one plan, one PR. There are no `## Stage N` units here and the conductor should not file any. The two sub-slices below are commit boundaries inside a single PR, not schedulable units.
- **Sub-slice A (Python), Tasks 1-9.** Files under `python/` only.
- **Sub-slice B (Go), Tasks 10-13.** `internal/relayclient/page.go`, `internal/relayclient/page_test.go`, plus a comment in `internal/cli/workers.go`.
- **They MUST NOT run in parallel, and the reason is not file overlap** - the file sets are disjoint. Two reasons:
  1. **One git index.** Both sub-slices commit to the same worktree. `git add` followed by a bare `git commit` is not atomic on a shared worktree; two engineers committing concurrently cross-contaminate. If the conductor runs them anyway, every commit must use an explicit pathspec.
  2. **Sub-slice B's page-cap comment is written *against* the Python message.** Go cannot compute a distinct-id count (`T` is a bare type parameter), so its cap message must assert neither completeness nor incompleteness, and the comment must say *why it differs from Python's*. That comment cannot be written before the Python message exists. Sequence B after A.
- **Sub-slice B is droppable.** If the Go half is abandoned mid-flight, drop Tasks 10-13's commits. Nothing in Tasks 1-9 unwinds, and no Python file references the Go work.
- **No frontend.** Zero files under `web/`. Do not dispatch `relay-frontend-engineer`. Both sub-slices are `relay-backend-engineer` work (Python SDK client code and a Go leaf package, both under TDD).
- **Within a sub-slice the tasks are sequential.** Task N's implementation is what Task N+1's test is written against. Do not reorder.

### Tasks verifiable with NO Docker and NO Postgres

**All of them.** Confirmed rather than assumed:

- **Tasks 1-9 (Python):** `httpx.MockTransport` only. No network, no database. The one exception is **Task 9 (T11)**, which needs a live `relay-server` - it is explicitly non-blocking (see Task 9).
- **Tasks 10-12 (Go, `internal/relayclient`):** a search for `go:build` under `internal/relayclient` returns **zero matches**. The package has **no integration-tagged files at all**; `page_test.go`, `client_test.go`, `client_settoken_test.go`, `client_transient_test.go` and `sanitize_test.go` are all default-lane and all use `httptest`. `go test ./internal/relayclient/...` needs nothing but a Go toolchain.
- **Task 13 (`internal/cli/workers.go`):** a comment-only edit. It changes no behaviour, so it needs `go build ./...` and `go vet ./...` and nothing else. `internal/cli` *does* have `*_integration_test.go` files (Docker) but they are untouched and a comment cannot redden them.

The only gate in this whole plan that needs Docker is the optional `-race` container run (see "Verification gates"), and CLAUDE.md already says to state plainly if it did not run.

---

## What this plan refutes, corrects, or adds to the spec

I read the spec once asking only whether it contradicts itself, contradicts the tree, or prescribes something that does not exist, and resolved every symbol it cites. **Seven things did not survive. Four spec claims I specifically tried to break and could not.** The two the conductor flagged as load-bearing are in the "could not break" list, with the evidence.

### Refuted: three rows of the section 9 mutation kill table claim a red set of one and have a red set of two or three

Memory: *plan-supplied tests are untrusted*. I hand-executed each mutation against the fixtures the spec sketches and against the tests already in `python/tests/unit/test_client.py`.

1. **M3b (`pages >= cap` -> `pages > cap`) is claimed "T5 only". Its real red set is {T5, T6, T7}, and it is identical to M3's.** T6 and T7 both shrink the cap to 2 and return HTTP 500 past request 2. Under `pages > cap`, `2 > 2` is false, so both make a third request, get the 500, and raise `ServerError` instead of `ProtocolError`. Worse, **no fixture can make M3b kill a test M3 does not**: deleting the cap and shifting it by one both produce "one more request than intended", so M3 always subsumes M3b. Keep the row - a boundary mutation is still worth running - but the honest claim is that **T5's `assert len(calls) == 3`**, not a distinct red set, is what separates "the cap fired at the right page" from "the cap fired at all".

2. **M4 (move the empty-page stop above the drained return) is claimed "T4 only". Its real red set is {T4, `test_list_jobs_sort_passed_through`}.** That existing test (`python/tests/unit/test_client.py:976-985`) returns `_page_response([])`, i.e. `{"items": [], "next_cursor": "", "total": 0}`, and under M4 the empty-page stop fires before the drained return and `client.list_jobs(sort="-name")` raises. This is not a problem - it is a second, independently-written witness that inverting the order accuses a correct server - but an engineer who expects one RED and sees two will start looking for a bug that is not there.

3. **M6 (move the `limit` short-circuit below the stops) is claimed "T8 only". Its real red set is {T8, `test_list_jobs_limit_caps_total`}.** There is nothing between step 3 and step 5 of the spec's own order except "read the cursor", so *any* placement of the limit check below the stops is also below the **drained return**. `test_list_jobs_limit_caps_total` (line 946) asks for `limit=250` across two 200-row pages whose second page drains, so under M6 the drained arm returns the untrimmed 400 rows and the test's `assert len(jobs) == 250` fails. T8 remains the discriminating test because it is the only one where a **stop** (not the drained return) is what fires. **Do not "fix" this by editing `test_list_jobs_limit_caps_total`'s fixture** - editing an existing test to make a mutation table prettier destroys the independent witness.

### Refuted: section 5.5 says "four raise sites" and there are five

4. `_fetch_all` gains **five** `raise ProtocolError` statements, not four: the empty-page stop, the cursor-repeat stop, and **three** page-cap message arms (section 5.4 specifies three arms explicitly). The "four" is a copy of `task_logs`, which genuinely has four (empty, non-advancing, and *two* cap arms) - `python/tests/unit/test_errors.py:82` records that count for `task_logs` and is still correct there. **Consequence for M7:** the plan needs a `.records` assertion at **five** sites, not four. T3 covers empty, T1 covers repeat, T6 covers cap arm 2, T7 covers cap arm 3, and **T12 (added by this plan) covers cap arm 1**, which the spec's test list has no test for at all.

### Refuted: section 5.4 calls the no-id arm "unreachable ... defence in depth", and it is reachable for exactly one of the six

5. **The arm is unreachable for five of the six types even against a hostile server, and reachable for `Job` alone.** The spec is right that `Job.id` is `Optional[str] = None` (`python/src/relay/models.py:331`) and the other five declare `id: str` (`ScheduledJob:494`, `Worker:558`, `Reservation:578`, `AgentEnrollment:592`, `User:602`) - I resolved all six. But the spec draws the wrong conclusion from it. For the five required-id models, a row missing `id` fails **inside `model.model_validate(item)`** with a `pydantic.ValidationError`, several statements before the cap arm can run: the arm is not merely unreachable-in-practice there, it is unreachable by construction. For `Job` it is genuinely reachable, because `Job(name="nightly")` must keep working and so `id` is defaulted. **So the arm is a `list_jobs`-specific behaviour, it must be tested through `list_jobs`, and the comment must say that** rather than filing it under generic defence in depth. Task 4 does both.

### Refuted: the spec never says where `pages` is incremented, and the request-count assertions depend on it

6. Section 4's numbered order has no `pages += 1`. Placed wrongly the cap is off by one and T5's `len(calls) == 3` breaks. **The increment is the first statement of the loop body, before the request** - matching `task_logs` (`python/src/relay/client.py:417-418`) - so with the cap at 3, requests 1/2/3 carry `pages` 1/2/3 and request 3 trips `pages >= 3`. Written literally into every task below.

### Refuted: T3 as sketched can go RED for the wrong reason

7. Section 9's T3 says "page 1 has rows and a cursor; page 2 has `items: []` with a non-empty cursor" and does not say the two cursors differ. If they are the same string, a second stop becomes a possible explanation for the raise and the test's diagnosis is unpinned. T3's page-2 cursor must be a **different** string from page 1's, so the only stop that can produce the raise is the empty-page one, and the assertion carries `match="empty page"`.

### What I tried to break in the spec and COULD NOT

"The spec was correct" is indistinguishable from having checked nothing, so here is what I actually measured.

- **Section 5.1's central claim - a repeated cursor is unreachable on a correct server - HOLDS, and the argument is sound in both the directions the conductor asked about.** `encodeCursorV2` (`internal/api/pagination.go:54-75`) is `base64.RawURLEncoding(json.Marshal(cursorWire{T,I,S,V,N}))`, and `buildPage:326-328` builds it from `rows[len(rows)-1]` - the **last kept** row, never the trimmed extra. The next page's SQL predicate is strictly past that key with `id` as tiebreaker: I read it in all six walked routes' queries and it is the same shape in every one, `(created_at, id) < (sqlc.arg(cursor_ts)::timestamptz, sqlc.arg(cursor_id)::uuid)` (`internal/store/query/` - `workers.sql:182`, `users.sql:28` and `:39`, `reservations.sql:12`, `agent_enrollments.sql:29`, `scheduled_jobs.sql:14` and `:25`). Cursor keys therefore strictly decrease along a walk, and a strictly monotone sequence cannot repeat a value.
  - **Concurrent mutation does not break it.** Take the worst case - a *text* sort key, where the value is mutable. Suppose page N's cursor is `(v1, X)`. For row X to be re-emitted as page N+1's cursor, X must both satisfy the strictly-past predicate and be encoded with value `v1` again. Postgres evaluates the `WHERE` clause and the output projection in **one snapshot**, so if X reads as `v1` it fails `> (v1, X)` and is filtered out; if it reads as some `v2 != v1` it passes but encodes a different string. There is no snapshot in which both hold.
  - **The legacy-cursor argument holds and is narrower than it needs to be.** `encodeCursor` (`:80-88`) emits no `S` field where `encodeCursorV2` always emits one, so the two spellings for the same row differ. In practice the case cannot arise inside one walk at all: `parsePage:272-283` rejects a cursor whose effective sort disagrees with the request's resolved sort, and every walked route reaches `buildPage`, which calls `encodeCursorV2` unconditionally.
  - **Therefore the seen-set stop is not a false-positive generator.** If this had failed, the whole design would have had to change; it does not.
- **Section 5.2's empty-page claim HOLDS, including the complement count.** `buildPage:312-318` returns `([]Out{}, "")` for zero rows and emits a cursor only on `len(rows) > limit`, in which case `items` holds exactly `limit >= 1` rows - so `next_cursor != "" && len(items) == 0` is unproducible. The spec's "16 `page[T]` write sites across 8 files" is right: a repo-wide count of `page[` in `internal/api` returns 18 across 10 files, of which one is the type declaration in `pagination.go` and one is `testhelper_test.go`, leaving **16 across 8 production files** (`agent_enrollments` 1, `invites` 1, `jobs` 3, `reservations` 1, `scheduled_jobs` 2, `tokens` 1, `users` 5, `workers` 2). Exact.
- **Section 8.1's blast radius is exact.** A repo-wide search for `FetchAllPages` returns **six production call sites, all in `internal/cli`** (`jobs.go:158`, `workers.go:94`, `workers.go:251`, `schedules.go:171`, `reservations.go:72`, `admin_users.go:72`), **three test call sites** in `internal/relayclient/page_test.go`, and **zero in `internal/mcp`**. And `resolveWorkerIDIn` (`internal/cli/workers.go:246-271`) really is called with `userLimit=0` from **six commands**: `workers.go:154` (get), `:201` (delete, via `resolveWorkerIDIncludingRevoked`), `:307` and `:335` (enable/disable), and `workers_workspaces.go:32` and `:67`.
- **No existing CLI fixture is reddened by the Go change.** Every `next_cursor` / `NextCursor` literal in `internal/cli`'s tests is the empty string (`admin_users_test.go:410,432,467,482`, `schedules_test.go:312,368,522`, `workers_revoked_list_test.go:20`), so every one takes the drained return before any new stop is evaluated.

---

## Answers to the spec's section 13 open questions

Four were resolved by the conductor and are restated here so an engineer reading only this file has them. Two were delegated and are settled here with a number and a choice.

| # | Question | Answer |
|---|---|---|
| Q1 | Cursor truncation threshold | **200 characters.** Not a range. A real relay cursor is base64url of a `{t,i,s}` JSON of ~96 bytes, so ~128 characters; 200 shows every legitimate cursor in full with headroom for a text-sort cursor carrying a row's name, and still bounds a message built from a value the client does not control. Python: module constant `_CURSOR_MESSAGE_CHARS = 200` in `client.py`. Go: `const maxCursorInMessage = 200` in `page.go`. |
| Q2 | `errors.py`'s `TYPE_CHECKING` import of `LogRecord` | **Dropped, deliberately, with the whole `if TYPE_CHECKING:` block and the `TYPE_CHECKING` name.** Once the annotation is `list[Any]` the module does not name `LogRecord` in any position that needs a symbol - the remaining mentions are prose in the docstring, which is never resolved. Keeping a `TYPE_CHECKING` import alive only to satisfy a docstring would be an import that exists to defeat a linter. `Any` is already imported in that module (`_extract_message` uses it), so the import line becomes `from typing import Any, Optional`. |
| Q3 | Sequencing vs `bug-2026-08-27-python-sdk-page-cursor-defaults-to-drained` | **That item stays UNTOUCHED. `body.get("next_cursor", "")` is left exactly as it is.** Nothing else is in flight. **What this slice therefore does about a MISSING `next_cursor` key: nothing, and the behaviour is unchanged - a dropped key still reads as drained and `_fetch_all` still returns early with a silently short list.** That is the other item's RED and its subject (it also covers `_get_page`, which this slice does not touch). This slice does not open that hole and does not close it. Do not add a `body["next_cursor"]` here "while you are in the file": it would steal the other item's RED and leave it unmeasurable. Note the *other* default this slice introduces, `body.get("total", 0)`, is a different class: a missing `total` falls into the **blaming** arm, which is the safe direction, whereas a missing `next_cursor` falls into "drained", which silently truncates. Task 4's comment says so, so a later sweep does not conflate them. |
| Q4 | `resolveWorkerIDIn`'s soft-error branch | **Left as is, behaviourally. A comment is added at the branch and a backlog item is proposed.** The branch (`internal/cli/workers.go:252-263`) treats a *fallback* path's error as soft, keyed on the loop index, so a non-admin's 403 on `/v1/workers/revoked` does not mask the primary list's miss. After this slice a cursor-repeat or page-cap error on that fallback path is reported to the operator as `no worker found with hostname "myhost"`, which is a **wrong diagnosis**, not merely a terse one. Fixing it properly means deciding which error classes are soft (an `errors.As(&relayclient.ResponseError{})` status test, plausibly 401/403 soft and everything else fatal), which is a separate judgement with its own tests in `internal/cli` and would double this slice's Go verification surface. **The smallest honest change is to record the consequence where the code is, not to discover it in review.** Task 13 adds the comment; the plan proposes the item in "Phase 6 proposals" for the conductor to file. |
| Q5 | Same PR or follow-up | **Same branch, separate commits, Go sequenced after Python.** The Go half is dropped by dropping Tasks 10-13's commits. |
| Q6 | T11 (live-server assertion) | **Does not block the slice.** Attempt it in Phase 4 if a server can be stood up. Every fixture-based assertion stands alone. **If it does not run, the final report must say so explicitly** - "T11 did not run, no live server was available" - never silently omit it. |

---

## Critical files

| File | Role |
|---|---|
| `python/src/relay/client.py` | The defect: `_fetch_all` (line 194), whose loop body ends at lines 240-242 with the server's drained signal and nothing else. `task_logs` (line 379) is the sibling that already has the three stops - **read its loop and the 40-line `_MAX_LOG_PAGES` comment (lines 75-111) before writing anything.** `_PAGE_REQUEST_LIMIT` (line 73) is the class-attribute precedent. |
| `python/src/relay/errors.py` | `ProtocolError` (line 63). The `TYPE_CHECKING` block is lines 7-8; the annotations to widen are lines 90 and 93. |
| `python/tests/unit/test_client.py` | `_make_client` (line 24) and `_page_response` (line 52) are the helpers to reuse. `_page_response` **hand-writes** `{"items", "next_cursor", "total"}` - that is the point; the comment at lines 60-72 explains why and forbids de-duplicating it against the models. Existing pagination tests start at line 917. |
| `python/tests/unit/test_errors.py` | `test_protocol_error_records_is_a_snapshot` (line 78). Its docstring's "all four raise sites inside task_logs" stays correct - it is about `task_logs`, not `_fetch_all`. Do not edit it. |
| `python/tests/unit/test_packaging.py` | `test_version_files_are_in_lockstep` (line 48) is Task 7's RED. `test_readme_client_api_table_documents_every_public_method` (line 11) must be re-run after Task 6. |
| `python/README.md` | The auto-paginating-methods note (lines 95-102) is where the new stops are documented. The error table's `ProtocolError` row (line 201) currently describes a *log* walk only. |
| `python/pyproject.toml` + `python/src/relay/_version.py` | `0.2.0` -> `0.2.1`, in lockstep. |
| `internal/relayclient/page.go` | `FetchAllPages` (lines 25-70). The loop's only exits today are `userLimit` (line 62) and an empty `NextCursor` (line 65). |
| `internal/relayclient/page_test.go` | Three existing tests. **`TestFetchAllPages_ForwardsParams` (line 73) encodes `PageEnvelope[item]{Total: 0}` through the production type and is the vacuous-fixture shape - do not copy it.** New fixtures write JSON bytes. |
| `internal/cli/workers.go` | **Comment-only edit at the soft-error branch (lines 252-263).** Do not change its behaviour. |
| `internal/cli/logs.go` | **READ-ONLY REFERENCE.** `maxLogPages` (line 658) is the `var`-not-`const` precedent and says why; `printTaskLogs` (line 710) is the Go loop with the three stops. Do not edit. |
| `internal/api/pagination.go` | **READ-ONLY REFERENCE.** `encodeCursorV2`, `decodeCursor`, `buildPage` - the contract every claim above rests on. Do not edit. |

---

## The design: one loop, six checks, in this order

Both languages. This is the spec's section 4, with the `pages` increment made explicit (see refutation 6).

```
loop:
  0. pages += 1
  1. request the page
  2. append its items to `out`
  3. if the caller's `limit` is now satisfied      -> RETURN out[:limit]     (not an error)
  4. read `next_cursor`
  5. if it is empty                                -> RETURN out            (drained; not an error)
  6. if this page carried no items                 -> RAISE  empty page
  7. if this cursor was already requested          -> RAISE  cursor repeat
  8. if `pages` has reached the cap                -> RAISE  page cap
  9. record the cursor as seen; continue
```

Membership (7) is tested **before** the cursor is recorded (9), so a self-loop fires on request 2 and an `A,B,A` cycle fires on request 3. Four orderings are load-bearing and each has its own test: **3 above everything** (T8), **5 above 6** (T4), **6 above 7** (T3), **8 last**.

---

## Mutation kill table

Every row must be run in Task 8 (Python) or Task 13 (Go). **Each mutation must be verified as APPLIED before its result is believed** - on this repo CRLF silently broke four mutations in a row and every one reported "survived". The procedure is in Task 8, step 1.

Test-name shorthand: T1 `..._repeats_a_cursor`, T2 `..._two_cycle...`, T3 `..._empty_page_that_still_advertises_more`, T4 `..._zero_matching_rows_is_not_an_error`, T5 `..._raises_at_the_page_cap`, T6 `..._does_not_blame_the_server_when_total_is_reached`, T7 `..._completeness_is_distinct_ids_not_a_row_count`, T8 `..._limit_satisfied_on_page_two...`, T9 `..._truncates_an_over_long_cursor...`, T12 `..._says_so_when_no_row_carried_an_id`. (There is no T10 or T11 in this table: T10's "wire a public method" property is carried by every test above, all of which call `client.list_jobs()` rather than `_fetch_all` directly, and T11 is the manual live-server gate in Task 9.)

### Python

| # | Mutation (exactly this, one at a time) | Expected RED | Note |
|---|---|---|---|
| M1 | Delete the `if cursor in seen:` block | T1, T2, **T9** | All three terminate on their fixture's HTTP 500, so `ServerError != ProtocolError` is the RED. None hangs. **T9 is in this set because its fixture is also a self-loop** - it repeats one over-long cursor - so deleting the stop removes T9's only route to a raise. Expect three, not two. |
| M1b | Replace `seen` membership with `cursor == previous` (keep a `previous` local) | **T2 only** | This is the row that justifies the set over a comparison. T1 and T9 still pass - both are self-loops, which a previous-cursor comparison catches. T2's `A,B,A` never repeats consecutively and runs into the 500. |
| M2 | Delete the `if not items:` block | **T3 only** | T3's page-2 cursor differs from page 1's, so no other stop can fire; request 3 is a 500. |
| M3 | Delete the `if pages >= self._MAX_LIST_PAGES:` block | T5, T6, T7, T12 | |
| M3b | `pages >= self._MAX_LIST_PAGES` -> `pages > self._MAX_LIST_PAGES` | T5, T6, T7, T12 | **Identical red set to M3 and that is unavoidable** (refutation 1). What discriminates is T5's `assert len(calls) == 3`. |
| M4 | Move the `if not items:` block above `if not cursor: return out` | T4, **and `test_list_jobs_sort_passed_through`** | Two witnesses that inverting the order accuses a correct server (refutation 2). |
| M5 | In cap arm 2 only, `distinct >= total` -> `len(out) >= total` | **T7 only** | T6 has `distinct == len(out) == 4` so it cannot see this; T12 takes arm 1 before arm 2 is reached. |
| M6 | Move the `limit` short-circuit below the stops | T8, **and `test_list_jobs_limit_caps_total`** | Any such move is also below the drained return (refutation 3). T8 is the discriminating one. |
| M7a | Drop `records=out` from the empty-page raise | **T3 only** | T3's page 1 is non-empty, so `.records` differs under the mutant. |
| M7b | Drop `records=out` from the cursor-repeat raise | **T1 only** | |
| M7c | Drop `records=out` from cap arm 1 | **T12 only** | |
| M7d | Drop `records=out` from cap arm 2 | **T6 only** | |
| M7e | Drop `records=out` from cap arm 3 | **T7 only** | |
| M8 | `_quote_cursor` returns `repr(cursor)` unconditionally | T9, **and `test_quote_cursor_bounds_a_long_cursor`** | Two are expected. The helper test proves the function truncates; **T9 proves it is WIRED into the message** - asserting the helper alone would be the cadence-test vacuity. |
| M9 | Delete cap arm 1 (the no-id arm) | **T12 only** | Without it T12 falls into arm 3 and the message prints `0 distinct row ids` - a computed-looking number standing in for a measurement that did not happen. |

### Go

| # | Mutation | Expected RED | Note |
|---|---|---|---|
| GM1 | Delete the `seen` membership check | G-T1, G-T2 | Go has no walk-level truncation test to add a third (see GM6). |
| GM1b | Replace `seen` with a previous-cursor comparison | **G-T2 only** | |
| GM2 | Delete the `len(resp.Items) == 0` check | **G-T3 only** | |
| GM3 | Delete the `pages >= maxListPages` check | **G-T5 only** | |
| GM4 | Move the `len(resp.Items) == 0` check above the `NextCursor == ""` return | G-T4, **and `TestFetchAllPages_ForwardsParams`** | That existing fixture marshals `PageEnvelope[item]{Total: 0}`, so `Items` is nil and `NextCursor` is `""`. |
| GM5 | Move the `userLimit` short-circuit below the stops | **G-T6 only** | Note the asymmetry with Python's M6: `TestFetchAllPages_RespectsUserLimit`'s fixture never drains and is satisfied on page 1, so it stays green - which is exactly why the spec says it must not be counted as a guard for this ordering. |
| GM6 | `quoteCursor` returns `strconv.Quote(cursor)` unconditionally | **`TestQuoteCursor_BoundsAnOverLongCursor` only** | **A stated asymmetry with Python, not an oversight.** Go has no walk-level equivalent of T9. What replaces it: G-T1 and G-T3 assert the **quoted** form (`"CUR-SAME"`, `"CUR-TWO"`, with the double quotes) rather than the bare cursor, which is what proves `quoteCursor` is on the message path at all - a `Contains(err, "CUR-TWO")` would pass whether the helper were called or not. A Go T9 would need a 5000-character fixture cursor to prove one `if` that the direct test already covers. If a reviewer wants it, it is four lines; say so rather than assuming it was forgotten. |

---

# SUB-SLICE A - Python

## Task 1: Bound how much of a server-supplied cursor an error message may quote

**Files:**
- Modify: `python/src/relay/client.py` (add a module-level constant and helper, after the `M = TypeVar(...)` line at line 43, before `class Client:`)
- Test: `python/tests/unit/test_client.py`

- [ ] **Step 1: Write the failing tests**

Append to `python/tests/unit/test_client.py`:

```python
# ─── cursor quoting ──────────────────────────────────────────────────────────


def test_quote_cursor_returns_a_short_cursor_verbatim() -> None:
    """A real relay cursor is base64url of a ~96-byte {t,i,s} JSON, so about 128
    characters. The threshold is 200, so every legitimate cursor is quoted in
    full and an operator can paste it back.
    """
    from relay.client import _quote_cursor

    assert _quote_cursor("eyJ0IjoiMjAyNi0wOC0yOCJ9") == "'eyJ0IjoiMjAyNi0wOC0yOCJ9'"


def test_quote_cursor_bounds_a_long_cursor() -> None:
    """The cursor is SERVER-SUPPLIED and its length is unbounded, so a message
    built from it is unbounded too. The bound must still leave the message
    diagnosable, so the true length is reported alongside the prefix.
    """
    from relay.client import _quote_cursor

    quoted = _quote_cursor("a" * 5000)

    assert len(quoted) < 300
    assert "truncated from 5000 characters" in quoted
    assert "a" * 5000 not in quoted
    assert "a" * 200 in quoted
```

- [ ] **Step 2: Run the tests to verify they fail**

Run:

```
python/.venv/Scripts/python.exe -m pytest python/tests/unit/test_client.py -k quote_cursor -v
```

Expected: both FAIL with `ImportError: cannot import name '_quote_cursor' from 'relay.client'`.

- [ ] **Step 3: Write the implementation**

In `python/src/relay/client.py`, insert immediately after `M = TypeVar("M", bound=BaseModel)` (line 43) and before `class Client:`:

```python
# How much of a server-supplied cursor a ProtocolError message may quote.
#
# The cursor is chosen by the SERVER and its length is unbounded, so a message
# that interpolates it whole is unbounded too - the same "provenance says
# nothing about content" argument that makes the stops below necessary, applied
# to the diagnostic rather than the loop.
#
# 200 characters. A real relay cursor is base64url of a ~96-byte {t,i,s} JSON
# (encodeCursorV2, internal/api/pagination.go), so ~128 characters: every
# legitimate cursor is quoted in full, including a text-sort cursor that carries
# a row's name, and only a cursor no correct server emits is ever cut.
_CURSOR_MESSAGE_CHARS = 200


def _quote_cursor(cursor: str) -> str:
    """Render a server-supplied cursor for an error message, bounded.

    Longer than the bound, the prefix is quoted and the TRUE length reported -
    a truncated string with no length would let a 5 MB cursor and a 201-byte one
    produce the same message.
    """
    if len(cursor) <= _CURSOR_MESSAGE_CHARS:
        return repr(cursor)
    head = cursor[:_CURSOR_MESSAGE_CHARS]
    return f"{head!r} (truncated from {len(cursor)} characters)"
```

- [ ] **Step 4: Run the tests to verify they pass**

Run:

```
python/.venv/Scripts/python.exe -m pytest python/tests/unit/test_client.py -k quote_cursor -v
```

Expected: 2 passed.

- [ ] **Step 5: Run the full gate and commit**

```
python/.venv/Scripts/python.exe -m pytest python/tests/unit -v
python/.venv/Scripts/python.exe -m ruff check python/src python/tests
python/.venv/Scripts/python.exe -m mypy python/src
```

Expected: 139 passed (137 baseline + 2), ruff clean, mypy clean.

```bash
git add python/src/relay/client.py python/tests/unit/test_client.py
git commit -m "feat(python): bound how much of a server-supplied cursor an error may quote"
```

**Before committing**, per CLAUDE.md's line-endings section: run `git diff --cached --stat` and check the insert counts against the size of the change you intended, and run `git ls-files --eol python/src/relay/client.py python/tests/unit/test_client.py` - both must read `i/lf`.

---

## Task 2: The empty-page stop, and the drained return above it

**Files:**
- Modify: `python/src/relay/errors.py` (lines 3, 7-8, 63-93)
- Modify: `python/src/relay/client.py` (`_fetch_all` loop body, lines 229-242)
- Test: `python/tests/unit/test_client.py`

- [ ] **Step 1: Write the failing tests**

Append to `python/tests/unit/test_client.py`:

```python
# ─── _fetch_all termination stops ────────────────────────────────────────────
#
# Every fixture body below is a hand-written dict literal, built by
# _page_response and _job_response. NEVER build one by dumping Page[Job] or Job:
# a fixture encoded through the type under test agrees with the decoder by
# construction, on the envelope keys AND on the item fields, and can detect
# drift in neither direction. Same rule, same reason, as the task-log fixtures
# above.
#
# Every fixture also has a TERMINATOR - an HTTP 500 past the request count the
# correct implementation makes. This project has no pytest-timeout. Without the
# terminator, deleting the stop under test leaves the handler answering forever
# and the test HANGS instead of failing. With it, the mutant raises ServerError,
# which is not ProtocolError, so the test is RED.


def test_fetch_all_raises_on_an_empty_page_that_still_advertises_more() -> None:
    """Stop 1. On a correct server this is unreachable - buildPage
    (internal/api/pagination.go) returns ([], "") for zero rows and emits a
    cursor only when it kept at least one row - which is the point: the loop is
    driven by a value the client does not control, and "no correct server does
    this" is a statement about correct servers.

    Page 1 must be NON-EMPTY. With an empty page 1, `.records` is [] under both
    the correct code and a mutant that drops records=, and the payload assertion
    is vacuous.

    Page 2's cursor must DIFFER from page 1's, or the repeated-cursor stop
    becomes a second possible explanation for the raise and the diagnosis is
    unpinned. The match= is the other half of that.
    """
    calls: list[dict[str, str]] = []

    def handler(request: httpx.Request) -> httpx.Response:
        calls.append(dict(request.url.params))
        if len(calls) == 1:
            return httpx.Response(
                200,
                json=_page_response(
                    [_job_response(id="j1"), _job_response(id="j2")],
                    next_cursor="CUR-ONE",
                    total=99,
                ),
            )
        if len(calls) == 2:
            return httpx.Response(
                200, json=_page_response([], next_cursor="CUR-TWO", total=99)
            )
        return httpx.Response(500, json={"error": "past the stop"})

    client = _make_client(handler)
    with pytest.raises(ProtocolError, match="empty page") as excinfo:
        client.list_jobs()

    assert len(calls) == 2
    assert "CUR-TWO" in str(excinfo.value)
    assert [j.id for j in excinfo.value.records] == ["j1", "j2"]


def test_fetch_all_zero_matching_rows_is_not_an_error() -> None:
    """The drained return MUST stay above the empty-page stop.

    A list with no matching rows answers `items: []` with `next_cursor: ""` -
    that IS the legitimate empty page here, and it reports itself drained.
    Testing emptiness first turns list_jobs() against an empty jobs table into a
    ProtocolError. That inversion is a one-line mutation (M4).
    """
    calls: list[dict[str, str]] = []

    def handler(request: httpx.Request) -> httpx.Response:
        calls.append(dict(request.url.params))
        return httpx.Response(200, json=_page_response([], total=0))

    client = _make_client(handler)
    assert client.list_jobs() == []
    assert len(calls) == 1
```

- [ ] **Step 2: Run the tests to verify they fail**

Run:

```
python/.venv/Scripts/python.exe -m pytest python/tests/unit/test_client.py -k "empty_page_that_still_advertises_more or zero_matching_rows" -v
```

Expected: `test_fetch_all_raises_on_an_empty_page_that_still_advertises_more` FAILS with `relay.errors.ServerError` (the walk sails past the empty page and hits the terminator). `test_fetch_all_zero_matching_rows_is_not_an_error` PASSES - it is a characterization test that locks in behaviour the drained return already has, and its job is to go RED under M4 in Task 8. Do not treat its green as a problem.

- [ ] **Step 3: Widen `ProtocolError` to carry any walk's rows**

In `python/src/relay/errors.py`, replace lines 3-8:

```python
from typing import TYPE_CHECKING, Any, Optional

import httpx

if TYPE_CHECKING:
    from .models import LogRecord
```

with:

```python
from typing import Any, Optional

import httpx
```

Then replace the whole `ProtocolError` class (lines 63-93) with:

```python
class ProtocolError(RelayError):
    """The server answered with well-formed HTTP that is not a usable relay
    response: a page that advertises more rows but carries none, a cursor that
    does not advance, or a walk that never reports itself drained.

    Raised by EVERY cursor walk in the SDK, not only the log one:
    :meth:`relay.Client.task_logs` and all six ``list_*`` methods. The paging
    loop is driven by a value the server chooses, and the provenance of a value
    says nothing about who controls its content.

    Carries no ``.response``, unlike the status-derived errors above: it is
    raised from a walk across several responses, so there is no single
    ``httpx.Response`` that explains it.

    ``records`` is what the abandoned walk had already collected, and it is
    the point of the raise rather than a debugging extra. It holds whatever that
    walk collects - ``LogRecord`` objects from ``task_logs``, resource models
    (``Job``, ``Worker``, ``User``, ...) from the six ``list_*`` methods - which
    is why it is annotated ``list[Any]`` rather than any one of those types. A
    Python method that returns a list cannot deliver rows and raise, so it
    delivers them HERE::

        try:
            jobs = client.list_jobs()
        except relay.ProtocolError as e:
            jobs = e.records   # incomplete, and e says why

    printTaskLogs (internal/cli/logs.go), which :meth:`relay.Client.task_logs`
    ports, has already written every row to its output by the time it returns
    the equivalent error: there, the error is a completeness caveat on rows the
    operator can already see. The list walks have no output at all, so ``records``
    is the ONLY route by which up to 2,000,000 collected rows reach the caller -
    ``Client._MAX_LIST_PAGES`` is private, so nobody can raise the bound and retry.

    It is ``[]`` when nothing was collected, never ``None``, so a caller need
    not test it before iterating.
    """

    def __init__(
        self, message: str, *, records: Optional[list[Any]] = None
    ) -> None:
        super().__init__(message)
        self.records: list[Any] = list(records) if records else []
```

**The kwarg stays `records`.** It is documented in `python/README.md` in three places and a synonym would be two names for one thing.

- [ ] **Step 4: Add the empty-page stop to `_fetch_all`**

In `python/src/relay/client.py`, replace the initialisation and `while True:` body of `_fetch_all` (lines 229-242) with:

```python
        out: list[M] = []
        cursor: Optional[str] = None
        pages = 0
        while True:
            pages += 1
            if cursor:
                p["cursor"] = cursor
            response = self._http.get(path, params=p)
            raise_for_response(response)
            body = response.json()
            # Bound to a local rather than re-read below: the emptiness test
            # must ask about THIS page, and `out` is cumulative. (These six
            # routes never send JSON null here - buildPage returns a non-nil
            # empty slice - so no null-coercion is needed.)
            items = body["items"]
            out.extend(model.model_validate(item) for item in items)
            if limit is not None and len(out) >= limit:
                return out[:limit]
            # NOT changed by this slice: a MISSING next_cursor key still reads
            # as drained here, and that is the subject of the open item
            # bug-2026-08-27-python-sdk-page-cursor-defaults-to-drained, which
            # also covers _get_page and is therefore wider than this loop.
            cursor = body.get("next_cursor", "")
            if not cursor:
                return out
            # This arm MUST stay above the empty-page stop below. A list with no
            # matching rows legitimately answers items: [] - and it reports
            # itself drained, so it never reaches the stop. Inverted, list_jobs()
            # against an empty jobs table raises.
            if not items:
                raise ProtocolError(
                    "server returned an empty page while still advertising more "
                    f"rows (next_cursor {_quote_cursor(cursor)})",
                    records=out,
                )
```

`pages` is incremented as the FIRST statement of the loop body, before the request, matching `task_logs`. Tasks 3 and 4 read it; the counter is introduced here so the loop is not reshaped twice.

- [ ] **Step 5: Run the tests to verify they pass**

Run:

```
python/.venv/Scripts/python.exe -m pytest python/tests/unit/test_client.py -k "empty_page_that_still_advertises_more or zero_matching_rows" -v
```

Expected: 2 passed.

- [ ] **Step 6: Run the full gate and commit**

```
python/.venv/Scripts/python.exe -m pytest python/tests/unit -v
python/.venv/Scripts/python.exe -m ruff check python/src python/tests
python/.venv/Scripts/python.exe -m mypy python/src
```

Expected: 141 passed, ruff clean, mypy clean. **If ruff reports `F401` on `TYPE_CHECKING`, the block was not fully removed** - remove the name from the `from typing import` line too.

```bash
git add python/src/relay/client.py python/src/relay/errors.py python/tests/unit/test_client.py
git commit -m "fix(python): _fetch_all raises on an empty page that still advertises more rows"
```

Run the CRLF checks from Task 1 step 5 before committing.

---

## Task 3: The repeated-cursor stop

**Files:**
- Modify: `python/src/relay/client.py` (`_fetch_all`)
- Test: `python/tests/unit/test_client.py`

- [ ] **Step 1: Write the failing tests**

Append to `python/tests/unit/test_client.py`:

```python
def test_fetch_all_raises_when_the_server_repeats_a_cursor() -> None:
    """Stop 2, self-loop. The repro from the backlog item: a server answering
    the same cursor forever drove 2000 requests and counting.

    The cursor here is an opaque base64 string with no order, so "did not
    advance" cannot be a comparison the way task_logs' `next_seq <= since` is.
    The stop is membership: this walk already requested this cursor.

    Membership is tested BEFORE the cursor is recorded, so a self-loop fires on
    request 2 - hence `len(calls) == 2`, not 3.
    """
    calls: list[dict[str, str]] = []

    def handler(request: httpx.Request) -> httpx.Response:
        calls.append(dict(request.url.params))
        if len(calls) > 3:
            return httpx.Response(500, json={"error": "past the stop"})
        return httpx.Response(
            200,
            json=_page_response(
                [_job_response(id=f"j{len(calls)}")], next_cursor="CUR-SAME", total=99
            ),
        )

    client = _make_client(handler)
    with pytest.raises(ProtocolError, match="already requested") as excinfo:
        client.list_jobs()

    assert len(calls) == 2
    assert "CUR-SAME" in str(excinfo.value)
    assert [j.id for j in excinfo.value.records] == ["j1", "j2"]


def test_fetch_all_raises_on_a_two_cycle_of_cursors() -> None:
    """THIS is the test that discriminates a seen-SET from a comparison against
    the immediately previous cursor.

    Under previous-cursor-only, A,B,A,B never fires: it runs to the page cap,
    10000 requests and up to 2,000,000 retained rows later. That is not an
    exotic adversarial construction - two replicas behind a load balancer with
    different data, or a caching proxy alternating two cached bodies, produce
    exactly this.

    The set fires on request 3, when A comes round again.
    """
    calls: list[dict[str, str]] = []
    cursors = ["CUR-A", "CUR-B", "CUR-A"]

    def handler(request: httpx.Request) -> httpx.Response:
        calls.append(dict(request.url.params))
        if len(calls) > len(cursors):
            return httpx.Response(500, json={"error": "past the stop"})
        return httpx.Response(
            200,
            json=_page_response(
                [_job_response(id=f"j{len(calls)}")],
                next_cursor=cursors[len(calls) - 1],
                total=99,
            ),
        )

    client = _make_client(handler)
    with pytest.raises(ProtocolError, match="already requested") as excinfo:
        client.list_jobs()

    assert len(calls) == 3
    assert "CUR-A" in str(excinfo.value)


def test_fetch_all_truncates_an_over_long_cursor_in_its_message() -> None:
    """The message quotes a value the SERVER chose, so the message's length is
    the server's to choose unless the client bounds it.

    This is the WIRING half of the _quote_cursor tests: asserting the helper
    truncates proves nothing about the code that builds the message.

    Note this fixture is also a self-loop, so deleting the repeated-cursor stop
    (M1) reddens this test too. That is expected and recorded in the mutation
    table; it is not a sign the truncation is unpinned - M8 kills this test
    while leaving the stop intact.
    """
    huge = "z" * 5000
    calls: list[dict[str, str]] = []

    def handler(request: httpx.Request) -> httpx.Response:
        calls.append(dict(request.url.params))
        if len(calls) > 3:
            return httpx.Response(500, json={"error": "past the stop"})
        return httpx.Response(
            200,
            json=_page_response([_job_response(id="j1")], next_cursor=huge, total=99),
        )

    client = _make_client(handler)
    with pytest.raises(ProtocolError) as excinfo:
        client.list_jobs()

    message = str(excinfo.value)
    assert len(message) < 1000
    assert "truncated from 5000 characters" in message
    assert huge not in message
```

- [ ] **Step 2: Run the tests to verify they fail**

Run:

```
python/.venv/Scripts/python.exe -m pytest python/tests/unit/test_client.py -k "repeats_a_cursor or two_cycle or over_long_cursor" -v
```

Expected: all 3 FAIL with `relay.errors.ServerError` - the walk has no repeat stop, so it runs into each fixture's terminator.

- [ ] **Step 3: Write the implementation**

In `python/src/relay/client.py`, add to the initialisation above the loop, beside `pages = 0`:

```python
        seen: set[str] = set()
```

and append to the loop body, immediately after the empty-page stop added in Task 2:

```python
            # The stop is: this walk already requested this cursor. A SET, not a
            # comparison against the previous cursor - the two catch different
            # things and a two-cycle (A,B,A,B, which two replicas behind a load
            # balancer produce) is invisible to the comparison and runs to the
            # page cap. This is not a second stop; it is the one stop, with the
            # container that implements it. Previous-cursor-only is this set
            # restricted to its last element.
            #
            # A repeated cursor is UNREACHABLE on a correct server: the server's
            # cursor (encodeCursorV2, internal/api/pagination.go) encodes the
            # LAST KEPT row's key and the next page's predicate is strictly past
            # it with id as tiebreaker, so cursor keys strictly decrease along a
            # walk. Comparison is byte-exact on the base64 string; the SDK never
            # decodes it, and deliberately so - decoding would make a
            # server-internal encoding a cross-language contract to keep in step.
            #
            # Memory, stated rather than hidden: at most one entry per page, so
            # the entry COUNT is bounded by _MAX_LIST_PAGES. The BYTE cost is
            # entries x cursor length, and cursor length is server-supplied and
            # unbounded - roughly 0.1% of a real walk (~128 bytes against ~100 KB
            # of models per page), and dominant only against a server sending
            # one-item pages with multi-megabyte cursors. A digest per entry
            # would close that term and is DECLINED: the same attacker already
            # has an equal retention channel through `items`, and the
            # unbounded-response-bytes axis belongs to
            # bug-2026-08-26-relayclient-has-no-response-bound-and-no-client-timeout
            # at the right layer.
            if cursor in seen:
                raise ProtocolError(
                    "server cursor did not advance - it repeated a cursor this "
                    f"walk had already requested ({_quote_cursor(cursor)}) after "
                    f"{pages} pages",
                    records=out,
                )
```

and, as the LAST statement of the loop body:

```python
            seen.add(cursor)
```

- [ ] **Step 4: Run the tests to verify they pass**

Run:

```
python/.venv/Scripts/python.exe -m pytest python/tests/unit/test_client.py -k "repeats_a_cursor or two_cycle or over_long_cursor" -v
```

Expected: 3 passed.

- [ ] **Step 5: Run the full gate and commit**

```
python/.venv/Scripts/python.exe -m pytest python/tests/unit -v
python/.venv/Scripts/python.exe -m ruff check python/src python/tests
python/.venv/Scripts/python.exe -m mypy python/src
```

Expected: 144 passed, ruff clean, mypy clean.

```bash
git add python/src/relay/client.py python/tests/unit/test_client.py
git commit -m "fix(python): _fetch_all raises when the server repeats a cursor it already served"
```

Run the CRLF checks before committing.

---

## Task 4: The page cap and its three message arms

**Files:**
- Modify: `python/src/relay/client.py` (add `_MAX_LIST_PAGES` class attribute; extend `_fetch_all`)
- Test: `python/tests/unit/test_client.py`

- [ ] **Step 1: Write the failing tests**

Append to `python/tests/unit/test_client.py`:

```python
def test_fetch_all_raises_at_the_page_cap(monkeypatch: pytest.MonkeyPatch) -> None:
    """Stop 3, which catches an ever-advancing, never-repeating cursor that
    never drains - something neither of the other two stops can see.

    _MAX_LIST_PAGES is a CLASS attribute so this monkeypatch works, which means
    the loop must read it off `self` and never off a module global.

    The request-count assertion is not decoration: a test that only checks the
    exception class cannot tell the cap from a different stop firing, and it
    cannot see an off-by-one in the cap's own predicate.
    """
    monkeypatch.setattr(Client, "_MAX_LIST_PAGES", 3)
    calls: list[dict[str, str]] = []

    def handler(request: httpx.Request) -> httpx.Response:
        calls.append(dict(request.url.params))
        if len(calls) > 3:
            return httpx.Response(500, json={"error": "past the cap"})
        return httpx.Response(
            200,
            json=_page_response(
                [_job_response(id=f"j{len(calls)}")],
                next_cursor=f"CUR-{len(calls)}",
                total=9999,
            ),
        )

    client = _make_client(handler)
    with pytest.raises(ProtocolError, match="page cap"):
        client.list_jobs()
    assert len(calls) == 3


def test_fetch_all_page_cap_does_not_blame_the_server_when_total_is_reached(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    """Stop 3's message arms. A list of exactly _MAX_LIST_PAGES * 200 rows
    drains correctly, but its last page is FULL and so carries a cursor: the
    walk stopped one request short of learning it was done, having collected
    every row. The envelope's own total settles that, so the message must not
    tell the caller their list may be longer.

    The outcome assertion is not optional. The sibling's version of this test
    was green BECAUSE OF the bug until `.records` was asserted.
    """
    monkeypatch.setattr(Client, "_MAX_LIST_PAGES", 2)
    calls: list[dict[str, str]] = []

    def handler(request: httpx.Request) -> httpx.Response:
        calls.append(dict(request.url.params))
        if len(calls) > 2:
            return httpx.Response(500, json={"error": "past the cap"})
        n = len(calls) * 2
        return httpx.Response(
            200,
            json=_page_response(
                [_job_response(id=f"j{n - 1}"), _job_response(id=f"j{n}")],
                next_cursor=f"CUR-{len(calls)}",
                total=4,
            ),
        )

    client = _make_client(handler)
    with pytest.raises(ProtocolError) as excinfo:
        client.list_jobs()

    message = str(excinfo.value)
    assert "may be longer" not in message
    assert "every one was collected" in message
    assert "4 distinct row ids" in message
    assert [j.id for j in excinfo.value.records] == ["j1", "j2", "j3", "j4"]


def test_fetch_all_page_cap_completeness_is_distinct_ids_not_a_row_count(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    """The completeness claim above is only sound if it counts DISTINCT ids.

    `len(out)` counts rows APPENDED and `total` is server-supplied, so a server
    that re-serves a page behind an ADVANCING cursor drives them equal while
    half the list was never sent - and the new repeated-cursor stop cannot see
    it, because the cursor genuinely advances. This handler does exactly that:
    ids j1 and j2 twice, cursors CUR-1 then CUR-2, total 4. Rows 3 and 4 do not
    exist on the wire at any point.
    """
    monkeypatch.setattr(Client, "_MAX_LIST_PAGES", 2)
    calls: list[dict[str, str]] = []

    def handler(request: httpx.Request) -> httpx.Response:
        calls.append(dict(request.url.params))
        if len(calls) > 2:
            return httpx.Response(500, json={"error": "past the cap"})
        return httpx.Response(
            200,
            json=_page_response(
                [_job_response(id="j1"), _job_response(id="j2")],
                next_cursor=f"CUR-{len(calls)}",
                total=4,
            ),
        )

    client = _make_client(handler)
    with pytest.raises(ProtocolError) as excinfo:
        client.list_jobs()

    message = str(excinfo.value)
    assert "every one was collected" not in message
    assert "may be longer" in message
    assert "2 distinct row ids" in message
    # Duplicates and all: the client does not know which of them the server
    # meant, so it hands back exactly what it received.
    assert [j.id for j in excinfo.value.records] == ["j1", "j2", "j1", "j2"]


def test_fetch_all_page_cap_says_so_when_no_row_carried_an_id(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    """The third message arm, and it exists for list_jobs SPECIFICALLY.

    Of the six models _fetch_all walks, five declare `id: str` - required and
    undefaulted - so a row missing `id` fails inside model_validate long before
    the cap arm runs, and this arm is unreachable for them BY CONSTRUCTION.
    `Job` declares `id: Optional[str] = None`, because Job is the authoring
    model too and Job(name="nightly") must keep working. So list_jobs is the one
    method that can reach here, which is why this fixture is a jobs walk.

    The message must NOT print "0 distinct row ids": that is a computed-looking
    number standing in for a measurement that did not happen. It says
    completeness could not be checked, and why.
    """
    monkeypatch.setattr(Client, "_MAX_LIST_PAGES", 2)
    calls: list[dict[str, str]] = []

    def handler(request: httpx.Request) -> httpx.Response:
        calls.append(dict(request.url.params))
        if len(calls) > 2:
            return httpx.Response(500, json={"error": "past the cap"})
        n = len(calls) * 2
        # Hand-written rows that deliberately carry NO "id" key. Job's only
        # required field is `name`.
        return httpx.Response(
            200,
            json=_page_response(
                [{"name": f"n{n - 1}"}, {"name": f"n{n}"}],
                next_cursor=f"CUR-{len(calls)}",
                total=4,
            ),
        )

    client = _make_client(handler)
    with pytest.raises(ProtocolError) as excinfo:
        client.list_jobs()

    message = str(excinfo.value)
    assert "carry no id" in message
    assert "0 distinct" not in message
    assert "every one was collected" not in message
    assert [j.name for j in excinfo.value.records] == ["n1", "n2", "n3", "n4"]
```

- [ ] **Step 2: Run the tests to verify they fail**

Run:

```
python/.venv/Scripts/python.exe -m pytest python/tests/unit/test_client.py -k "page_cap" -v
```

Expected: all 4 FAIL with `AttributeError: <class 'relay.client.Client'> has no attribute '_MAX_LIST_PAGES'`, raised by `monkeypatch.setattr`. That is the right RED - it names the missing symbol.

- [ ] **Step 3: Add the class attribute**

In `python/src/relay/client.py`, immediately after the `_MAX_LOG_PAGES = 10000` line (line 111), add:

```python
    # Bounds the NUMBER OF REQUESTS the LIST paging loop (_fetch_all) makes
    # against a server whose next_cursor keeps advancing but which never reports
    # the list as drained. 10000 pages at _PAGE_REQUEST_LIMIT rows is 2,000,000
    # rows - a jobs table on a long-lived farm can plausibly reach that, and a
    # cap that truncates a legitimate list is worse than the hang it prevents is
    # frequent, so this is the wrong place to be clever with a smaller number.
    # The public, caller-chosen bound on ROWS is `limit=`.
    #
    # Requests is all it bounds. Wall clock, response bytes and the memory of a
    # single response are all open; those three axes are MEASURED in the
    # _MAX_LOG_PAGES comment above - read them there. They are not restated
    # here, because a second copy is a second thing that can go stale. Closing
    # them belongs to
    # bug-2026-08-26-relayclient-has-no-response-bound-and-no-client-timeout.
    #
    # SEPARATE from _MAX_LOG_PAGES rather than a shared _MAX_PAGES: the two
    # loops bound different populations, and that comment's measurements are
    # log-specific and would become wrong if the constant were shared.
    #
    # A CLASS attribute, like _MAX_LOG_PAGES, so a test can shrink it - which
    # means the loop must read it off `self`, not off a module global.
    _MAX_LIST_PAGES = 10000
```

- [ ] **Step 4: Add the cap check and its three arms**

In `_fetch_all`, insert immediately after the repeated-cursor stop and BEFORE `seen.add(cursor)`:

```python
            if pages >= self._MAX_LIST_PAGES:
                # Count DISTINCT ids, never len(out). `total` is server-supplied
                # and so is the cursor, so a server that re-serves a page behind
                # an ADVANCING cursor drives len(out) up to total while half the
                # list was never sent - and the repeated-cursor stop above cannot
                # see that, because the cursor genuinely advances.
                #
                # M is bound only to BaseModel, so nothing structural guarantees
                # an id: the accessor is getattr with a default. Five of the six
                # walked models declare `id: str` (required, undefaulted), so
                # against them a row without an id fails in model_validate long
                # before this line. `Job` declares `id: Optional[str] = None`
                # because it is the authoring model too, so list_jobs is the one
                # method that can reach the "no id" arm below.
                #
                # The failure direction is UNDER-count, which is safe: it can
                # only push the code into the blaming arm. It can never
                # over-count - the set holds at most one entry per row received.
                #
                # Built ONCE, inside a block every path of which raises. The
                # common path - a walk that finishes - pays nothing. Do not
                # accumulate this per page: that would put a string per row on
                # 100% of walks to serve a message that fires at 2,000,000 rows.
                ids: set[str] = set()
                for row in out:
                    row_id = getattr(row, "id", None)
                    if isinstance(row_id, str) and row_id:
                        ids.add(row_id)
                distinct = len(ids)
                # From the CURRENT page, matching task_logs' use of page.total.
                # A MISSING total defaults to 0 and so falls into the blaming arm
                # below - the safe direction, and deliberately NOT the same class
                # of default as the next_cursor one above, where a missing key
                # reads as "drained" and silently truncates.
                total = body.get("total", 0)
                if distinct == 0:
                    # Reaching here means every collected row lacked a usable id;
                    # `out` itself is non-empty, because the empty-page stop above
                    # rejects any page that contributed no rows. Do NOT print
                    # "0 distinct rows collected" - that is a computed-looking
                    # number standing in for a measurement that did not happen.
                    raise ProtocolError(
                        f"truncated after {self._MAX_LIST_PAGES} pages - hit the "
                        f"client's page cap; {len(out)} rows were collected and "
                        f"the server reported {total}, but completeness could not "
                        "be checked because the rows carry no id",
                        records=out,
                    )
                if total > 0 and distinct >= total:
                    # Do not blame the server here. A list of exactly
                    # _MAX_LIST_PAGES * _PAGE_REQUEST_LIMIT rows drains correctly,
                    # but its last page is full and so carries a cursor: we
                    # stopped one request short of learning we were done, having
                    # collected every row. The envelope's own total settles it.
                    raise ProtocolError(
                        f"truncated after {self._MAX_LIST_PAGES} pages - hit the "
                        f"client's page cap; the server reported {total} rows and "
                        f"every one was collected ({distinct} distinct row ids), "
                        "but it had not yet reported the list as drained",
                        records=out,
                    )
                raise ProtocolError(
                    f"truncated after {self._MAX_LIST_PAGES} pages - hit the "
                    "client's page cap; the list may be longer than "
                    f"{self._MAX_LIST_PAGES * self._PAGE_REQUEST_LIMIT} rows, or "
                    "the server may never report it as drained "
                    f"({distinct} distinct row ids collected, server reported "
                    f"{total})",
                    records=out,
                )
```

- [ ] **Step 5: Run the tests to verify they pass**

Run:

```
python/.venv/Scripts/python.exe -m pytest python/tests/unit/test_client.py -k "page_cap" -v
```

Expected: 4 passed.

- [ ] **Step 6: Run the full gate and commit**

```
python/.venv/Scripts/python.exe -m pytest python/tests/unit -v
python/.venv/Scripts/python.exe -m ruff check python/src python/tests
python/.venv/Scripts/python.exe -m mypy python/src
```

Expected: 148 passed, ruff clean, mypy clean.

```bash
git add python/src/relay/client.py python/tests/unit/test_client.py
git commit -m "fix(python): bound _fetch_all's request count and report completeness honestly"
```

Run the CRLF checks before committing.

---

## Task 5: Pin the `limit` short-circuit ABOVE every stop

**This task is a characterization test plus a mutation proof, not a TDD cycle.** The production code already has the right order - Task 2 deliberately left the `limit` check where it was - so the test is GREEN on arrival. Its value is that it goes RED under M6, and the mutation proof in step 3 is what establishes that. Do not skip step 3: without it this is an unmeasured test.

**Files:**
- Test: `python/tests/unit/test_client.py`

- [ ] **Step 1: Write the test**

Append to `python/tests/unit/test_client.py`:

```python
def test_fetch_all_limit_satisfied_on_page_two_by_a_page_that_repeats_a_cursor() -> None:
    """The `limit` short-circuit stays ABOVE every stop.

    A caller who asked for 3 rows and has 3 rows has been served. Turning that
    into an error because the page that completed the order also repeated a
    cursor would make a correct result depend on a defect the caller never
    observes.

    The discriminating case is narrower than it looks and no existing test
    covers it. Neither the cursor-repeat stop nor the page cap can fire on
    request 1 - there is no previous cursor, and pages == 1 < cap - so a walk
    satisfied on page 1 proves nothing about the ordering. `limit` must be
    satisfied on page 2 OR LATER, by a page that also trips a stop. That is why
    both pages return cursor CUR-A.
    """
    calls: list[dict[str, str]] = []

    def handler(request: httpx.Request) -> httpx.Response:
        calls.append(dict(request.url.params))
        if len(calls) > 2:
            return httpx.Response(500, json={"error": "past the stop"})
        n = len(calls) * 2
        return httpx.Response(
            200,
            json=_page_response(
                [_job_response(id=f"j{n - 1}"), _job_response(id=f"j{n}")],
                next_cursor="CUR-A",
                total=99,
            ),
        )

    client = _make_client(handler)
    jobs = client.list_jobs(limit=3)

    assert [j.id for j in jobs] == ["j1", "j2", "j3"]
    assert len(calls) == 2
```

- [ ] **Step 2: Run it and confirm it PASSES**

Run:

```
python/.venv/Scripts/python.exe -m pytest python/tests/unit/test_client.py -k "limit_satisfied_on_page_two" -v
```

Expected: 1 passed.

- [ ] **Step 3: Prove it is load-bearing (mutation M6)**

Apply M6 by hand: in `_fetch_all`, cut the two lines

```python
            if limit is not None and len(out) >= limit:
                return out[:limit]
```

and paste them immediately after the page-cap block, just above `seen.add(cursor)`.

Run:

```
python/.venv/Scripts/python.exe -m pytest python/tests/unit -v
```

Expected RED set: **exactly two tests** -
`test_fetch_all_limit_satisfied_on_page_two_by_a_page_that_repeats_a_cursor` (raises `ProtocolError` instead of returning 3 rows) and `test_list_jobs_limit_caps_total` (returns 400 rows because the drained arm now runs first and returns `out` untrimmed).

**Confirm the mutation actually applied before believing this** - `git diff --stat python/src/relay/client.py` must show a non-zero change. A mutation that silently fails to write reports "survived".

Then revert:

```bash
git checkout -- python/src/relay/client.py
```

and re-run the full unit suite to confirm green again.

- [ ] **Step 4: Commit**

```
python/.venv/Scripts/python.exe -m pytest python/tests/unit -v
python/.venv/Scripts/python.exe -m ruff check python/src python/tests
python/.venv/Scripts/python.exe -m mypy python/src
```

Expected: 149 passed, ruff clean, mypy clean.

```bash
git add python/tests/unit/test_client.py
git commit -m "test(python): pin the limit short-circuit above _fetch_all's termination stops"
```

---

## Task 6: Document the new contract - six docstrings, the README note, the README error table

**Files:**
- Modify: `python/src/relay/client.py` (`list_jobs` 262-279, `list_schedules` 607-618, `list_workers` 691-695, `list_users` 706-710, `list_reservations` 721-725, `list_agent_enrollments` 738-742 - line numbers as at HEAD; they will have shifted)
- Modify: `python/README.md` (the note at lines 95-102; the error table row at line 201)

Wrong prose about correct code is this project's dominant defect class, and a wrong contract in docs IS a defect - consumers implement against prose and no test covers it. This task is not cleanup.

- [ ] **Step 1: Add one sentence to each of the six `list_*` docstrings**

Append this sentence to each of the six docstrings, adjusting nothing else:

```
        A walk that cannot be completed raises :class:`ProtocolError` with the
        rows collected so far on ``.records``.
```

`list_workers`, `list_users`, `list_reservations` and `list_agent_enrollments` currently have one-line docstrings; expand each to a triple-quoted block with the existing text as the summary line, a blank line, and the sentence above. For example, `list_workers` becomes:

```python
    def list_workers(
        self, *, sort: Optional[str] = None, limit: Optional[int] = None
    ) -> list[Worker]:
        """List workers, auto-paginating across all pages. ``limit`` caps total rows.

        A walk that cannot be completed raises :class:`ProtocolError` with the
        rows collected so far on ``.records``.
        """
        return self._fetch_all("/v1/workers", Worker, sort=sort, limit=limit)
```

- [ ] **Step 2: Extend the README's auto-paginating note**

In `python/README.md`, immediately after the paragraph that ends `...answers 400 for anything outside 1-200.` (line 102), insert:

````markdown
Every one of those seven walks is driven by a cursor the **server** chooses, and
the provenance of a value says nothing about who controls its content. So each
has three stops beyond the server's own drained signal, and a server that trips
one raises `ProtocolError`: a page that carries no rows while still advertising
more, a cursor the walk has already requested (a repeat, or a two-cycle such as
`A,B,A,B`), and a client-side cap of 10000 requests. The cap bounds **requests**
and nothing else - not wall clock, not response bytes, not the memory of one
response, for the reasons measured under "Reading a task's log" below. The rows
collected before a walk was abandoned are on the exception:

```python
try:
    jobs = client.list_jobs()
except relay.ProtocolError as e:
    jobs = e.records          # never None; [] if nothing was collected
    print(f"partial list ({len(jobs)} jobs): {e}")
```

A list with **no matching rows is not an error** - it answers `items: []` with an
empty cursor, which is the drained signal, and `list_jobs()` returns `[]`.
````

(The outer fence above is four backticks only so this plan can nest the Python
block; insert the content with the ordinary three-backtick fence.)

- [ ] **Step 3: Widen the README error table row**

Replace line 201 of `python/README.md`:

```markdown
| `ProtocolError` | A 200 that is not a usable relay response: an empty page advertising more rows, a cursor that does not advance, or a log that never reports itself drained. Carries `.records` (what the abandoned walk collected) instead of `.response` |
```

with:

```markdown
| `ProtocolError` | A 200 that is not a usable relay response, raised by **any** cursor walk - `task_logs` and all six `list_*` methods: an empty page advertising more rows, a cursor the walk already requested, or a walk that never reports itself drained within the client's page cap. Carries `.records` (whatever that walk collected - log records from `task_logs`, resource models from `list_*`) instead of `.response` |
```

- [ ] **Step 4: Run the gate**

```
python/.venv/Scripts/python.exe -m pytest python/tests/unit -v
python/.venv/Scripts/python.exe -m ruff check python/src python/tests
python/.venv/Scripts/python.exe -m mypy python/src
```

Expected: 149 passed. `test_readme_client_api_table_documents_every_public_method` is in that run and must stay green - no public method was added or removed. Ruff clean, mypy clean.

- [ ] **Step 5: Commit**

```bash
git add python/src/relay/client.py python/README.md
git commit -m "docs(python): the six list_* walks raise ProtocolError, and .records is not log-specific"
```

**Run `git diff --cached --stat` and check the line counts against the edit you intended, and `git ls-files --eol python/README.md python/src/relay/client.py` (both `i/lf`).** A programmatic edit to a README on this repo once committed a two-line change as 1845 insertions.

---

## Task 7: Version bump, in lockstep

**Files:**
- Modify: `python/pyproject.toml:7`
- Modify: `python/src/relay/_version.py:1`

`0.2.1`, not `0.3.0`: no signature changes, and the only new behaviour replaces a hang, which nobody can have depended on.

- [ ] **Step 1: Bump `pyproject.toml` only, and watch the existing guard go RED**

In `python/pyproject.toml`, change line 7 from `version = "0.2.0"` to `version = "0.2.1"`.

Run:

```
python/.venv/Scripts/python.exe -m pytest python/tests/unit/test_packaging.py -v
```

Expected: `test_version_files_are_in_lockstep` FAILS with `assert '0.2.1' == '0.2.0'`. That guard exists exactly so bumping one copy is red.

- [ ] **Step 2: Bump `_version.py`**

In `python/src/relay/_version.py`, change line 1 to:

```python
__version__ = "0.2.1"
```

- [ ] **Step 3: Run the gate**

```
python/.venv/Scripts/python.exe -m pytest python/tests/unit -v
python/.venv/Scripts/python.exe -m ruff check python/src python/tests
python/.venv/Scripts/python.exe -m mypy python/src
```

Expected: 149 passed, ruff clean, mypy clean.

- [ ] **Step 4: Commit**

```bash
git add python/pyproject.toml python/src/relay/_version.py
git commit -m "chore(python): 0.2.1"
```

---

## Task 8: The Python mutation battery

Not optional. The backlog item's acceptance criterion is "each stop is pinned by a mutation that kills exactly one test", and an unrun table is an assertion, not a measurement.

**Files:** none committed. This task edits `python/src/relay/client.py` and `python/src/relay/errors.py` temporarily and reverts each time.

- [ ] **Step 1: Establish the baseline and the verification procedure**

A uniform result across a battery means the harness is broken, not that coverage is good. Before the first mutation, and after every revert:

```
python/.venv/Scripts/python.exe -m pytest python/tests/unit -q
```

Expected: 149 passed. **If the baseline is not green, stop** - every subsequent "survived" is unreadable.

For **each** mutation, in this order:
1. Apply the edit.
2. **Verify it applied:** `git diff --stat python/src/relay/client.py` (or `errors.py`) must show a non-zero change, and `git diff python/src/relay/client.py` must show the intended hunk. CRLF has silently swallowed four mutations in a row on this repo and every one reported "survived".
3. Run `python/.venv/Scripts/python.exe -m pytest python/tests/unit -q -rf` and record the failing test names.
4. Revert: `git checkout -- python/src/relay/client.py python/src/relay/errors.py`.
5. Re-run the baseline and confirm 149 passed before the next mutation.

- [ ] **Step 2: Run every row of the Python mutation kill table**

Work through M1, M1b, M2, M3, M3b, M4, M5, M6, M7a, M7b, M7c, M7d, M7e, M8, M9 exactly as the table above specifies them, recording the observed RED set for each.

**M1b needs a real edit, not a deletion.** Replace the `seen` set with a previous-cursor local:

```python
        previous: Optional[str] = None
```
```python
            if cursor == previous:
                raise ProtocolError(...)   # message unchanged
```
```python
            previous = cursor
```

**M7a-M7e** are one raise site each: delete `records=out,` from that site only, leaving the other four.

- [ ] **Step 3: Compare observed against expected**

Every observed RED set must match the table. **A row whose observed set is EMPTY is a hole**: the stop is unpinned and a test must be added before this task closes. **A row whose observed set is larger than the table says** is either the documented multi-witness case (M1, M3b, M4, M6, M8) or a finding - write it down either way; do not adjust the table to match the observation without saying which you did.

- [ ] **Step 4: Confirm the tree is clean and record the result**

```bash
git status --porcelain
```

Expected: empty. Then run the full gate once more:

```
python/.venv/Scripts/python.exe -m pytest python/tests/unit -v
python/.venv/Scripts/python.exe -m ruff check python/src python/tests
python/.venv/Scripts/python.exe -m mypy python/src
```

Nothing is committed by this task. The observed table goes in the Phase 3 report.

---

## Task 9: T11 - a real empty list does not raise (MANUAL, NON-BLOCKING)

**This task does not block the slice** (conductor decision on Q6). Attempt it in Phase 4 if a `relay-server` can be stood up. **If it does not run, the final report must say "T11 did not run: no live server was available" explicitly.** Never omit it silently.

The Python integration lane is **not in CI** - `.github/workflows/python.yml` has a `test` job that runs `pytest tests/unit` and a `lint` job, and no integration job - so this is a manual gate the conductor runs and reports, not a check that will catch a regression later.

**Files:**
- Modify: `python/tests/integration/test_smoke.py`

- [ ] **Step 1: Add the test**

Append to `python/tests/integration/test_smoke.py`:

```python
def test_a_list_with_no_matching_rows_returns_empty_and_does_not_raise(
    client: relay.Client,
) -> None:
    """The ONE assertion in this slice whose truth depends on what the SERVER
    puts on the wire, rather than on a fixture.

    Inverting the drained return and the empty-page stop is a one-line mutation
    that turns every list call against an empty result set into a ProtocolError.
    A fixture proves the client handles `{"items": [], "next_cursor": ""}`; only
    a live handler proves that is what buildPage actually sends for zero rows.

    The filter is a random scheduled_job_id, so the result set is empty on any
    server regardless of what else is in the database.
    """
    import uuid

    jobs = client.list_jobs(scheduled_job_id=str(uuid.uuid4()))
    assert jobs == []
```

- [ ] **Step 2: Run it against a live server**

With a `relay-server` reachable and a valid token, in PowerShell:

```
$env:RELAY_INTEGRATION = "1"
python/.venv/Scripts/python.exe -m pytest python/tests/integration -v
```

Expected: passed. Without `RELAY_INTEGRATION=1` the `conftest.py` hook skips it, so the default lane is unaffected and the `pytest python/tests/unit` count stays 149.

- [ ] **Step 3: Commit**

```bash
git add python/tests/integration/test_smoke.py
git commit -m "test(python): a live list with zero matching rows returns [] and does not raise"
```

Commit the test **whether or not it ran** - a skipped integration test is still the recorded intent - but say in the report which happened.

---

# SUB-SLICE B - Go

Sequenced after sub-slice A. Droppable by dropping these commits.

## Task 10: The Go empty-page stop, the drained return above it, and bounded cursor quoting

**Files:**
- Modify: `internal/relayclient/page.go`
- Test: `internal/relayclient/page_test.go`

- [ ] **Step 1: Write the failing tests**

Add `"io"` and `"strings"` to the import block of `internal/relayclient/page_test.go`, then append:

```go
// The fixtures below write JSON BYTES. They deliberately do NOT marshal a
// PageEnvelope[T] the way TestFetchAllPages_ForwardsParams above does: a
// fixture encoded through the production envelope type agrees with the decoder
// by construction, on the envelope keys AND on the item fields, and can detect
// drift in neither direction.
//
// Each fixture also has a TERMINATOR - a 500 past the request count the correct
// implementation makes - so a mutant that drops the stop under test fails with
// a transport error instead of looping forever.
//
// The cursor assertions check the QUOTED form (with the double quotes), not the
// bare cursor. `Contains(err, "CUR-TWO")` would pass whether quoteCursor were on
// the message path or not; `Contains(err, "\"CUR-TWO\"")` is what proves it is.

func TestFetchAllPages_EmptyPageAdvertisingMoreIsAnError(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		switch calls {
		case 1:
			// Page 1 is NON-empty and its cursor DIFFERS from page 2's, so the
			// repeated-cursor stop is not a second possible explanation.
			_, _ = io.WriteString(w, `{"items":[{"id":"a"},{"id":"b"}],"next_cursor":"CUR-ONE","total":99}`)
		case 2:
			_, _ = io.WriteString(w, `{"items":[],"next_cursor":"CUR-TWO","total":99}`)
		default:
			http.Error(w, `{"error":"past the stop"}`, http.StatusInternalServerError)
		}
	}))
	defer srv.Close()

	type item struct {
		ID string `json:"id"`
	}
	c := NewClient(srv.URL, "tok")
	got, _, err := FetchAllPages[item](context.Background(), c, "/v1/things", url.Values{}, 0)
	require.Error(t, err)
	require.Contains(t, err.Error(), "empty page")
	require.Contains(t, err.Error(), `"CUR-TWO"`)
	require.Contains(t, err.Error(), "paginate /v1/things")
	assert.Nil(t, got, "the partial slice is deliberately NOT returned; five renderers have nowhere to put it")
	assert.Equal(t, 2, calls)
}

func TestFetchAllPages_ZeroRowsIsNotAnError(t *testing.T) {
	// The drained return must stay ABOVE the empty-page stop. buildPage returns
	// ([]Out{}, "") for zero rows, so this is what a real list with no matching
	// rows sends, and inverting the order makes `relay list` fail on an empty
	// jobs table.
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		_, _ = io.WriteString(w, `{"items":[],"next_cursor":"","total":0}`)
	}))
	defer srv.Close()

	type item struct {
		ID string `json:"id"`
	}
	c := NewClient(srv.URL, "tok")
	got, total, err := FetchAllPages[item](context.Background(), c, "/v1/things", url.Values{}, 0)
	require.NoError(t, err)
	assert.Empty(t, got)
	assert.EqualValues(t, 0, total)
	assert.Equal(t, 1, calls)
}

func TestQuoteCursor_BoundsAnOverLongCursor(t *testing.T) {
	short := "eyJ0IjoiMjAyNi0wOC0yOCJ9"
	assert.Equal(t, `"`+short+`"`, quoteCursor(short))

	huge := strings.Repeat("z", 5000)
	quoted := quoteCursor(huge)
	assert.Less(t, len(quoted), 300)
	assert.Contains(t, quoted, "truncated from 5000 bytes")
	assert.NotContains(t, quoted, huge)
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run:

```
go test ./internal/relayclient/ -run "TestFetchAllPages_EmptyPageAdvertisingMoreIsAnError|TestFetchAllPages_ZeroRowsIsNotAnError|TestQuoteCursor" -v -count=1
```

Expected: a **compile failure** - `undefined: quoteCursor`. That is not yet a behavioural RED, so step 3 is split: 3a adds only the helper and re-measures, 3b adds the stop.

- [ ] **Step 3a: Add `quoteCursor` only, and re-measure the RED**

In `internal/relayclient/page.go`, add after the `PageRequestLimit` const (line 19):

```go
// maxCursorInMessage bounds how much of a server-supplied cursor an error
// message may quote. The cursor is chosen by the SERVER and its length is
// unbounded, so a message that interpolates it whole is unbounded too.
//
// 200 bytes. A real relay cursor is base64url of a ~96-byte {t,i,s} JSON
// (encodeCursorV2, internal/api/pagination.go), so ~128 bytes: every legitimate
// cursor is quoted in full and only a cursor no correct server emits is cut.
const maxCursorInMessage = 200

// quoteCursor renders a server-supplied cursor for an error message, bounded.
// The prefix is cut at a BYTE boundary and may split a UTF-8 rune;
// strconv.Quote escapes the resulting invalid bytes rather than emitting them,
// which is the safe direction for a value the client does not control. The true
// length is reported, so a 5 MB cursor and a 201-byte one do not produce the
// same message.
func quoteCursor(cursor string) string {
	if len(cursor) <= maxCursorInMessage {
		return strconv.Quote(cursor)
	}
	return fmt.Sprintf("%s (truncated from %d bytes)",
		strconv.Quote(cursor[:maxCursorInMessage]), len(cursor))
}
```

`strconv` and `fmt` are already imported by this file.

Run:

```
go test ./internal/relayclient/ -run "TestFetchAllPages_EmptyPageAdvertisingMoreIsAnError|TestFetchAllPages_ZeroRowsIsNotAnError|TestQuoteCursor" -v -count=1
```

Expected: `TestQuoteCursor_BoundsAnOverLongCursor` PASSES, `TestFetchAllPages_ZeroRowsIsNotAnError` PASSES (characterization), and `TestFetchAllPages_EmptyPageAdvertisingMoreIsAnError` FAILS - the walk sails past the empty page, makes a third request, and the terminator's 500 does produce an error, so the precise failures are `require.Contains(err.Error(), "empty page")` and `assert.Equal(t, 2, calls)` (observed 3). Record both.

- [ ] **Step 3b: Add the empty-page stop**

In `FetchAllPages`, replace the `var` block and the whole `for` loop (lines 37-69) with:

```go
	var (
		out    []T
		total  int64
		cursor string
		first  = true
		pages  int
	)
	for {
		pages++
		if cursor != "" {
			params.Set("cursor", cursor)
		} else {
			params.Del("cursor")
		}
		path := basePath
		if encoded := params.Encode(); encoded != "" {
			path += "?" + encoded
		}
		var resp PageEnvelope[T]
		if err := c.Do(ctx, "GET", path, nil, &resp); err != nil {
			return nil, 0, fmt.Errorf("paginate %s: %w", basePath, err)
		}
		if first {
			total = resp.Total
			first = false
		}
		out = append(out, resp.Items...)
		if userLimit > 0 && len(out) >= userLimit {
			return out[:userLimit], total, nil
		}
		if resp.NextCursor == "" {
			return out, total, nil
		}
		// This arm MUST stay above the empty-page stop below. buildPage
		// (internal/api/pagination.go) returns ([]Out{}, "") for zero rows, so a
		// list with no matching rows is an empty page that reports itself
		// drained. Inverted, `relay list` fails against an empty jobs table.
		if len(resp.Items) == 0 {
			return nil, 0, fmt.Errorf(
				"paginate %s: server returned an empty page while still advertising more rows (next_cursor %s)",
				basePath, quoteCursor(resp.NextCursor))
		}
		cursor = resp.NextCursor
	}
```

Note the error return is `nil, 0, err` - **not the partial slice**. That asymmetry with the Python SDK is documented on the function in Task 12.

- [ ] **Step 4: Run the tests to verify they pass**

```
go test ./internal/relayclient/ -run "TestFetchAllPages|TestQuoteCursor" -v -count=1
```

Expected: all pass, including the three pre-existing tests.

- [ ] **Step 5: Full gate and commit**

```
go build ./...
go vet ./...
go test ./... -count=1
```

Expected: all packages ok.

```bash
git add internal/relayclient/page.go internal/relayclient/page_test.go
git commit -m "fix(relayclient): FetchAllPages errors on an empty page that still advertises more rows"
```

Run `git diff --cached --stat` and `git ls-files --eol internal/relayclient/page.go internal/relayclient/page_test.go` before committing.

---

## Task 11: The Go repeated-cursor stop

**Files:**
- Modify: `internal/relayclient/page.go`
- Test: `internal/relayclient/page_test.go`

- [ ] **Step 1: Write the failing tests**

Append to `internal/relayclient/page_test.go`:

```go
func TestFetchAllPages_RepeatedCursorIsAnError(t *testing.T) {
	// The repro shape: a server answering the same cursor forever. Membership is
	// tested BEFORE the cursor is recorded, so a self-loop fires on request 2.
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls > 3 {
			http.Error(w, `{"error":"past the stop"}`, http.StatusInternalServerError)
			return
		}
		fmt.Fprintf(w, `{"items":[{"id":"a%d"}],"next_cursor":"CUR-SAME","total":99}`, calls)
	}))
	defer srv.Close()

	type item struct {
		ID string `json:"id"`
	}
	c := NewClient(srv.URL, "tok")
	got, _, err := FetchAllPages[item](context.Background(), c, "/v1/things", url.Values{}, 0)
	require.Error(t, err)
	require.Contains(t, err.Error(), "already requested")
	require.Contains(t, err.Error(), `"CUR-SAME"`)
	assert.Nil(t, got)
	assert.Equal(t, 2, calls)
}

func TestFetchAllPages_TwoCycleOfCursorsIsAnError(t *testing.T) {
	// THIS is the test that discriminates the seen-SET from a comparison against
	// the immediately previous cursor. Under previous-cursor-only, A,B,A,B never
	// fires and runs to the page cap - 10000 requests and up to 2,000,000
	// retained rows later. Two replicas behind a load balancer with different
	// data, or a caching proxy alternating two cached bodies, produce exactly
	// this.
	cursors := []string{"CUR-A", "CUR-B", "CUR-A"}
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls > len(cursors) {
			http.Error(w, `{"error":"past the stop"}`, http.StatusInternalServerError)
			return
		}
		fmt.Fprintf(w, `{"items":[{"id":"a%d"}],"next_cursor":%q,"total":99}`, calls, cursors[calls-1])
	}))
	defer srv.Close()

	type item struct {
		ID string `json:"id"`
	}
	c := NewClient(srv.URL, "tok")
	_, _, err := FetchAllPages[item](context.Background(), c, "/v1/things", url.Values{}, 0)
	require.Error(t, err)
	require.Contains(t, err.Error(), "already requested")
	require.Contains(t, err.Error(), `"CUR-A"`)
	assert.Equal(t, 3, calls)
}
```

- [ ] **Step 2: Run the tests to verify they fail**

```
go test ./internal/relayclient/ -run "RepeatedCursor|TwoCycle" -v -count=1
```

Expected: both FAIL - `err.Error()` carries the terminator's 500 text, not `already requested`, and `calls` is 4 in both.

- [ ] **Step 3: Write the implementation**

Add `seen` to the `var` block:

```go
		seen   = map[string]struct{}{}
```

and insert into the loop body, immediately after the empty-page stop and immediately before `cursor = resp.NextCursor`:

```go
		// The stop is: this walk already requested this cursor. A SET, not a
		// comparison against the previous cursor - the two catch different
		// things, and a two-cycle (A,B,A,B, which two replicas behind a load
		// balancer produce) is invisible to the comparison and runs to the page
		// cap. This is not a second stop; it is the container that implements
		// the one stop. Previous-cursor-only is this set restricted to its last
		// element.
		//
		// A repeated cursor is UNREACHABLE on a correct server: encodeCursorV2
		// (internal/api/pagination.go) encodes the LAST KEPT row's key, and the
		// next page's predicate is strictly past it with id as tiebreaker, so
		// cursor keys strictly decrease along a walk. Comparison is byte-exact
		// on the base64 string; this package never decodes it.
		//
		// No digest per entry. Beyond costing an exception to CLAUDE.md's rule
		// that all hashing goes through internal/tokenhash.Hash, the residual it
		// would close - a server sending one-item pages with multi-megabyte
		// cursors - is the unbounded-response-bytes axis owned by
		// bug-2026-08-26-relayclient-has-no-response-bound-and-no-client-timeout,
		// and the same attacker already has an equal retention channel through
		// Items.
		if _, ok := seen[resp.NextCursor]; ok {
			return nil, 0, fmt.Errorf(
				"paginate %s: server cursor did not advance - it repeated a cursor this walk had already requested (%s) after %d pages",
				basePath, quoteCursor(resp.NextCursor), pages)
		}
		seen[resp.NextCursor] = struct{}{}
```

- [ ] **Step 4: Run the tests to verify they pass**

```
go test ./internal/relayclient/ -run "TestFetchAllPages|TestQuoteCursor" -v -count=1
```

Expected: all pass.

- [ ] **Step 5: Full gate and commit**

```
go build ./...
go vet ./...
go test ./... -count=1
```

```bash
git add internal/relayclient/page.go internal/relayclient/page_test.go
git commit -m "fix(relayclient): FetchAllPages errors when the server repeats a cursor it already served"
```

---

## Task 12: The Go page cap, and the message it must NOT copy from Python

**Files:**
- Modify: `internal/relayclient/page.go`
- Test: `internal/relayclient/page_test.go`

- [ ] **Step 1: Write the failing tests**

Append to `internal/relayclient/page_test.go`:

```go
// maxListPages is package-global state. A test that shrinks it must NOT call
// t.Parallel(). Neither of the two below does; do not add it.

func TestFetchAllPages_PageCapBoundsTheRequestCount(t *testing.T) {
	original := maxListPages
	defer func() { maxListPages = original }()
	maxListPages = 3

	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls > 3 {
			http.Error(w, `{"error":"past the cap"}`, http.StatusInternalServerError)
			return
		}
		fmt.Fprintf(w, `{"items":[{"id":"a%d"}],"next_cursor":"CUR-%d","total":9999}`, calls, calls)
	}))
	defer srv.Close()

	type item struct {
		ID string `json:"id"`
	}
	c := NewClient(srv.URL, "tok")
	got, _, err := FetchAllPages[item](context.Background(), c, "/v1/things", url.Values{}, 0)
	require.Error(t, err)
	require.Contains(t, err.Error(), "page cap")
	// The message reports what it has and asserts NEITHER completeness nor
	// incompleteness. T is a bare type parameter with no id, so this package
	// cannot compute the distinct-row count the Python SDK's equivalent message
	// uses, and must not claim one.
	require.Contains(t, err.Error(), "3 rows collected")
	require.Contains(t, err.Error(), "9999")
	assert.NotContains(t, err.Error(), "every one")
	assert.Nil(t, got)
	assert.Equal(t, 3, calls)
}

func TestFetchAllPages_UserLimitSatisfiedOnPageTwoByAPageThatRepeatsACursor(t *testing.T) {
	// The userLimit short-circuit stays ABOVE every stop. A caller who asked for
	// 3 rows and has 3 rows has been served.
	//
	// TestFetchAllPages_RespectsUserLimit above LOOKS like it covers this and
	// does not: its userLimit=3 is satisfied on page 1, and neither the
	// repeated-cursor stop nor the cap can fire on request 1. The discriminating
	// case needs the limit satisfied on page 2 or later, by a page that also
	// trips a stop - hence CUR-A on both pages.
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls > 2 {
			http.Error(w, `{"error":"past the stop"}`, http.StatusInternalServerError)
			return
		}
		fmt.Fprintf(w, `{"items":[{"id":"a%d"},{"id":"b%d"}],"next_cursor":"CUR-A","total":99}`, calls, calls)
	}))
	defer srv.Close()

	type item struct {
		ID string `json:"id"`
	}
	c := NewClient(srv.URL, "tok")
	got, _, err := FetchAllPages[item](context.Background(), c, "/v1/things", url.Values{}, 3)
	require.NoError(t, err)
	assert.Equal(t, []item{{ID: "a1"}, {ID: "b1"}, {ID: "a2"}}, got)
	assert.Equal(t, 2, calls)
}
```

- [ ] **Step 2: Run the tests to verify they fail**

```
go test ./internal/relayclient/ -run "PageCapBounds|UserLimitSatisfiedOnPageTwo" -v -count=1
```

Expected: a compile failure - `undefined: maxListPages`. `TestFetchAllPages_UserLimitSatisfiedOnPageTwoByAPageThatRepeatsACursor` cannot run until the file compiles; after step 3 it must PASS on arrival (the ordering is already right) and its proof is the GM5 mutation in Task 13.

- [ ] **Step 3: Write the implementation**

Add above `FetchAllPages` in `internal/relayclient/page.go`:

```go
// maxListPages bounds the NUMBER OF REQUESTS FetchAllPages makes against a
// server whose next_cursor keeps advancing but which never reports the list as
// drained - the case neither the empty-page stop nor the repeated-cursor stop
// can see. 10000 pages at PageRequestLimit rows is 2,000,000 rows.
//
// Requests is all it bounds. NewClient returns &http.Client{} with no Timeout
// and cmd/relay/main.go builds its context with signal.NotifyContext and no
// deadline, so wall clock, response bytes and the memory of one response are
// all open; they belong to
// bug-2026-08-26-relayclient-has-no-response-bound-and-no-client-timeout.
//
// A var rather than a const so a test can shrink it, matching internal/cli's
// maxLogPages, which is a var for exactly this reason and says so. It is
// package-global state: a test that shrinks it must NOT call t.Parallel().
var maxListPages = 10000
```

Insert into the loop body, immediately after the repeated-cursor stop's `if` block and immediately before `seen[resp.NextCursor] = struct{}{}`:

```go
		if pages >= maxListPages {
			// This message reports what it has and asserts NEITHER possibility:
			// it does not blame the server, and it does not claim every row was
			// collected.
			//
			// That is a DELIBERATE difference from the Python SDK's equivalent
			// message, which does split on completeness. Python can count
			// DISTINCT row ids and compare them against the envelope's total;
			// this package cannot. T is a bare type parameter with no
			// constraint and no id, so reading one out would take reflection or
			// a decode change, either of which couples this leaf package to its
			// callers' row shapes. A count of rows APPENDED is not a count of
			// distinct rows received - a server re-serving a page behind an
			// advancing cursor drives them apart - so claiming completeness
			// from len(out) would be a claim the number cannot support. Do NOT
			// "fix" this asymmetry by copying Python's wording onto this count.
			//
			// `total` is the FIRST page's total (see `if first` above) - the
			// existing contract of this function's return value - so the message
			// says which page it came from rather than implying it is current.
			return nil, 0, fmt.Errorf(
				"paginate %s: truncated after %d pages - hit the client's page cap; %d rows collected, the server's first page reported %d, and it had not yet reported the list as drained",
				basePath, maxListPages, len(out), total)
		}
```

Finally, replace the doc comment on `FetchAllPages` (lines 21-24) with:

```go
// FetchAllPages walks ?cursor= until next_cursor is empty, or until userLimit
// rows have been collected (when userLimit > 0). Returns the merged slice and
// the total reported by the first page response. Caller-supplied params are
// forwarded on every page request alongside ?limit=200&cursor=<...>.
//
// Beyond the server's own drained signal the loop has THREE stops, and all
// three are needed. The cursor is server-supplied and drives a client loop, and
// the provenance of a value says nothing about who controls its content. An
// empty page that still advertises more catches a server the client cannot page
// at all; a repeated cursor catches a self-loop on request 2 and an A,B,A cycle
// on request 3; maxListPages catches an ever-advancing cursor that never drains,
// which neither of the other two can see.
//
// On any of those the return is `nil, 0, err` - NOT the partial slice. That is
// a deliberate asymmetry with the Python SDK, whose ProtocolError carries the
// collected rows on .records: its caller is a program that can use partial rows
// and has no other route to them, while this function's callers are five
// renderers that have printed nothing yet plus one id-resolver, none of which
// has anywhere to put a partial list - and the existing transport-error path
// already returns nil, 0.
```

- [ ] **Step 4: Run the tests to verify they pass**

```
go test ./internal/relayclient/ -run "TestFetchAllPages|TestQuoteCursor" -v -count=1
```

Expected: all pass.

- [ ] **Step 5: Full gate and commit**

```
go build ./...
go vet ./...
go test ./... -count=1
```

```bash
git add internal/relayclient/page.go internal/relayclient/page_test.go
git commit -m "fix(relayclient): bound FetchAllPages' request count with a page cap"
```

---

## Task 13: The `resolveWorkerIDIn` comment (Q4), and the Go mutation battery

**Files:**
- Modify: `internal/cli/workers.go` (comment only, at the soft-error branch, lines 252-263)

- [ ] **Step 1: Add the comment**

In `internal/cli/workers.go`, inside `resolveWorkerIDIn`, append these lines to the existing comment above `if i == 0 {` (keep the existing text unchanged):

```go
			// KNOWN CONSEQUENCE, recorded rather than discovered in review.
			// Since FetchAllPages grew termination stops, this branch can now
			// swallow a PAGINATION error on a fallback path - a repeated cursor
			// or a page cap on /v1/workers/revoked - and report it to the
			// operator as `no worker found with hostname "..."`, which is a
			// wrong diagnosis rather than merely a terse one. The soft rule is
			// right for the 403 it was written for and wrong for this; narrowing
			// it means deciding which error classes are soft (plausibly: a
			// relayclient.ResponseError with 401/403 stays soft, everything else
			// becomes fatal), which is a separate judgement with its own tests
			// and is deliberately NOT made here.
```

- [ ] **Step 2: Verify nothing changed behaviourally**

```
go build ./...
go vet ./...
go test ./internal/cli/... -count=1
```

Expected: ok. A comment cannot redden a test; if anything is red, the edit went wrong.

- [ ] **Step 3: Run the Go mutation battery**

Same procedure as Task 8 step 1: baseline first, verify each mutation applied with `git diff --stat internal/relayclient/page.go`, revert with `git checkout -- internal/relayclient/page.go`, re-baseline between rows.

Baseline: `go test ./internal/relayclient/ -count=1` -> ok.

Work through GM1, GM1b, GM2, GM3, GM4, GM5, GM6 as the Go mutation table specifies, recording the observed RED set for each. **GM1b needs a real edit**: replace `seen` with a `previous string` local and the membership test with `if resp.NextCursor == previous`.

Every observed RED set must match the table. An empty red set is a hole and must be closed before this task does. A larger one is either the documented case (GM4's second witness) or a finding - write it down.

- [ ] **Step 4: Confirm the tree is clean and commit the comment**

```bash
git status --porcelain
```

Expected: only `internal/cli/workers.go` modified.

```bash
git add internal/cli/workers.go
git commit -m "docs(cli): record that resolveWorkerIDIn's soft-error branch now swallows pagination errors"
```

---

## Verification gates

Every target written as its literal command. **`make` is NOT installed on this machine** - do not write or run a `make` invocation anywhere in this slice.

### Python (no Docker, no Postgres)

```
python/.venv/Scripts/python.exe -m pytest python/tests/unit -v
python/.venv/Scripts/python.exe -m ruff check python/src python/tests
python/.venv/Scripts/python.exe -m mypy python/src
```

Baseline at HEAD, measured by the conductor: **137 unit tests pass, ruff clean, mypy --strict clean.** Expected at the end of sub-slice A: **149 pass** - 137 baseline, +2 (Task 1), +2 (Task 2), +3 (Task 3), +4 (Task 4), +1 (Task 5). Task 9's integration test is not collected by `pytest python/tests/unit` at all, so it does not move this number. If the count differs, reconcile it before moving on rather than assuming.

**The 3.9 floor is a CI-only cell.** `.github/workflows/python.yml`'s `test` job runs 3 OS x 5 Python versions; there is one local venv and it is not 3.9. Everything in this plan is 3.9-valid: `set[str]` and `list[Any]` appear only in annotations, `client.py` and `errors.py` both open with `from __future__ import annotations`, and local variable annotations are never evaluated at runtime in any version. **Note there is an open item `bug-2026-08-27-mypy-python-version-floor-is-silently-not-enforced` - do not fix it here, and do not write anything that depends on it being broken.** In particular do not use `str.removeprefix`, `match`, PEP 604 `X | Y` at runtime, or a runtime `dict[str, str]()` call.

### Go (no Docker, no Postgres)

```
go build ./...
go vet ./...
go test ./internal/relayclient/ -count=1 -v
go test ./... -count=1
```

`internal/relayclient` has zero `//go:build` lines and every test in it uses `httptest`.

### Race (Docker; state plainly if it did not run)

Per CLAUDE.md, the native Windows `-race` lane is unreliable and the Linux container is the route that works. From the **Bash** tool (Git Bash), not PowerShell:

```bash
MSYS_NO_PATHCONV=1 docker run --rm -v "$(pwd -W):/src" -w /src -e CGO_ENABLED=1 \
  golang:1.26 go test -race ./... -count=1 -timeout 600s
```

This slice adds no goroutines, no channels and no shared mutable state except the `maxListPages` package var, which only tests write. **If the container is unavailable, say so - do not substitute `-count=N` repetition, which re-runs under the ordinary scheduler and cannot observe an unsynchronised access.**

### Line endings, before EVERY commit

```bash
git diff --cached --stat
git ls-files --eol <the paths you are committing>
```

`git diff` and `git status` disagree by design on this repo, so **never conclude "nothing to revert" from `git diff` alone.** Every touched path must read `i/lf`. Check the diffstat against the size of the change you intended - a two-line change that committed as 1845 insertions has happened here.

### Not run by this slice, and why

- **`make generate`** - no `.sql` or `.proto` file is touched. No `*.sql.go` or `models.go` is edited.
- **The CLI real-server integration lane** - `internal/cli` gets a comment and nothing else. Per `idea-2026-08-27-cli-lane-never-crosses-a-list-page-boundary` the lane never crosses a list page boundary anyway, so it would not exercise this change even if run.
- **The browser e2e suite** - zero files under `web/`.

---

## Phase 6 proposals (not filed; the conductor accepts)

1. **`resolveWorkerIDIn`'s soft-error branch reports a pagination failure as "no worker found".** Q4 above, and Task 13's comment. The branch treats every fallback-path error as soft, keyed on the loop index. That is right for the 403 it was written for and wrong for a cursor-repeat or page-cap error, which is now reachable there and reaches the operator as a wrong diagnosis. Proposed remedy: keep soft only for a `relayclient.ResponseError` with status 401 or 403, make everything else fatal, with a test in `internal/cli`. *(Type: bug. Priority: low - it needs a misbehaving server, and the message is wrong rather than the outcome.)*

2. **`printTaskLogs`'s "every one printed" rests on a non-distinct row count.** Carried forward from the spec's section 11.1. `logProgress.rows` counts rows *written*, so under a server re-serving pages behind an advancing cursor the Go cap message asserts completeness it has not verified - the same defect the Python `task_logs` port was refuted for and fixed with a distinct-`seq` set. The Go loop cannot afford that set (its O(one page) memory is a stated design property), but a cheap exact alternative exists for an *ordered* cursor: count a row only when its `seq` exceeds the highest yet written. O(1), exact on a correct server, under-counting on a misbehaving one. The same counter would let Python's `task_logs` drop its 35-70 MB transient set. *(Type: bug. Priority: low.)*

3. **`internal/relayclient`'s existing test fixtures encode through the production envelope type.** Carried forward from the spec's section 11.2. `TestFetchAllPages_ForwardsParams` marshals `PageEnvelope[item]{Total: 0}` and so agrees with the decoder by construction on both the envelope keys and the item fields - the vacuous-fixture shape CLAUDE.md enumerates for `internal/cli`, in a package that paragraph does not name. Worth a sweep of `internal/relayclient` and `internal/mcp` fixtures on the same axis. If it is taken up, **this slice's own new fixtures are the counter-exemplar**: `page_test.go`'s new tests write JSON bytes with `io.WriteString` / `fmt.Fprintf` and a locally declared `item` struct whose tags are independent of `PageEnvelope`. *(Type: idea. Priority: low.)*

---

## Backlog item to close

`docs/backlog/bug-2026-08-27-python-sdk-fetch-all-has-no-termination-stops.md`, via `/backlog close fetch-all-has-no-termination-stops` - which `git mv`s the file into `docs/backlog/closed/`, stamps the frontmatter, appends a `## Resolution` note and commits. **The `git mv` is required scope, not optional cleanup.** Never flip `status:` by hand.

`bug-2026-08-27-python-sdk-page-cursor-defaults-to-drained` stays **open and untouched** (Q3).
