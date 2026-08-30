# A missing pagination key must not read as "the list ended" - implementation plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make `relay.Page.next_cursor` and `relay.Page.total` REQUIRED and undefaulted in the Python SDK, so a paged envelope that omits either raises instead of decoding into a page that reports the list drained - and correct every sentence in `python/` that states the removed defaults as current fact.

**Architecture:** Two field declarations in `python/src/relay/models.py`. `_get_page` already routes the whole response body through `Page[model].model_validate(...)` and `_fetch_all` already goes through `_get_page`, so one model change covers all twelve public methods over six routes with **zero logic change in `client.py`**. Everything else in this plan is tests, prose, a version bump, and a mutation battery. The failure surfaces as an unwrapped `pydantic.ValidationError`, deliberately, because routing decode failures into `RelayError` is a twelve-site chokepoint owned by a different item.

**Tech Stack:** Python 3.9-3.13, pydantic v2, httpx (`httpx.MockTransport`), pytest, ruff, mypy strict. No Go. No TypeScript.

Spec: `docs/superpowers/specs/2026-08-29-python-sdk-strict-page-envelope.md`
Backlog item: `docs/backlog/bug-2026-08-27-python-sdk-page-cursor-defaults-to-drained.md`

---

## Slice independence declaration

**Python SDK only. One backlog item, one spec, one plan, one PR, one session.**

- **There is no frontend slice and no backend slice.** Every file this plan touches is under `python/`. Zero files under `web/`, zero `.go` files, zero `.sql` files, zero `.proto` files. No `make generate`. No Docker, no Postgres, no `p4`. Do not dispatch `relay-frontend-engineer`. This is `relay-backend-engineer` work in the sense that it is server-adjacent client code under TDD, not in the sense that it touches Go.
- **Nothing here can run in parallel with anything else**, because there is only one lane. The six tasks below are strictly sequential: Task N's committed state is what Task N+1's edit is written against.
- **This is NOT a `/backlog phases` plan.** There are no `## Stage N` units and the conductor should not file any. The six tasks are commit boundaries inside a single PR.
- **Closes:** `docs/backlog/bug-2026-08-27-python-sdk-page-cursor-defaults-to-drained.md`. Close it with `/backlog close python-sdk-page-cursor`, never by hand-editing `status:` - the `git mv` into `docs/backlog/closed/` is required scope.
- **One backlog item must be FILED by the conductor** before this PR merges. See "The Go peer" below. The planner does not file items.

### Tasks verifiable with NO Docker and NO Postgres

**Tasks 1, 2, 4, 5, 6: all of them.** `httpx.MockTransport` only - no network, no database, no container. Verified rather than assumed: `python/tests/unit/` contains no `testcontainers` import, no socket bind, and `python/tests/unit/test_client.py` builds every client through `_make_client`, which wraps `httpx.MockTransport`.

**Task 3 is the one exception and it is NON-BLOCKING.** It edits a docstring in `python/tests/integration/test_smoke.py`, which needs a live `relay-server` to run. The lane is manual (`make python-test-integration`, gated on `RELAY_INTEGRATION=1`) and is **not in CI** - `.github/workflows/python.yml` runs `pytest tests/unit` only. Task 3 changes no code and no assertion, so it cannot redden that lane. Do not block the PR on running it.

---

## What this plan refutes, corrects, or adds to the spec

I read the spec once asking only whether it contradicts itself, contradicts the tree, or prescribes something that does not exist, and resolved every symbol and count it cites against the worktree. **Four things did not survive. Five spec claims I specifically tried to break and could not**, including all three the conductor flagged as load-bearing.

### Refuted: section 7.4's heading over-claims. Nine sites is the right COUNT; "state the old behaviour as current fact" is true of five of them

I counted the nine and they are exactly the nine the spec names - the count is correct. What is not correct is the category. Sorted by what actually happens to each:

| Site (by symbol) | File | What is true of it |
|---|---|---|
| `_get_page`'s "keep their DEFAULTS" paragraph | `client.py` | **Becomes false.** Names the defaults, names the item, asserts they are preserved. |
| `_fetch_all`'s "NOT changed by this slice" comment | `client.py` | **Becomes false.** Says a missing `next_cursor` still reads as drained. |
| `_fetch_all`'s page-cap `total = page.total` comment | `client.py` | **Becomes false.** "A MISSING total still defaults to 0, via Page's own default." |
| `test_fetch_all_rejects_a_missing_items_key` docstring | `test_client.py` | **Becomes false** after the comma: "whose defaults are deliberate and preserved". |
| `test_log_page_requires_next_seq_and_total` docstring | `test_models.py` | **Already false at HEAD**, independent of this slice. It calls `_get_page`'s `body.get("next_cursor", "")` a live departure; #161 deleted that statement. Doubly false after. |
| `LogPage` docstring | `models.py` | **Becomes false.** "unlike :class:`Page`, which declares `next_cursor: str = ""`". |
| `Page` docstring | `models.py` | **Stays true and becomes incomplete.** It says nothing about defaults at all. It needs the rationale added; it has no false clause to correct. |
| `Job`'s list-only enrichment comment | `models.py` | **Stays true and becomes misleading.** "the strict no-default rule LogPage follows does not apply here" is still an accurate sentence; it is just no longer the whole picture, since `Page` is now the closer analogue. |
| `test_job_authoring_does_not_require_enrichment_fields` docstring | `test_models.py` | Same as above. Still accurate, now incomplete. |

**Why this matters for the plan and not just for pedantry.** The spec's acceptance criterion "after the change, none of these patterns hits a site that states the old behaviour as current fact" is checkable for the first five and vacuous for the last four, because they never stated it. If an engineer treats all nine as corrections-of-falsehoods they will rewrite `Page`'s and `Job`'s prose to *deny* something it never claimed, which is how a third generation of wrong prose gets written about this exact trio. Task 2 handles the last four as **relationship** edits with the axes named, not as corrections.

### Refuted: section 10 requires all nine prose sites "in the same commit as the field change", and that is not achievable alongside bite-sized TDD

Taken literally it forces one enormous commit. Taken as intent - *not in a later PR, and not deferred to an item* - it is right and this plan honours it. The rule this plan adopts is stronger in the place that matters: **no commit in this plan leaves a false statement in the tree.**

So the nine sites split by *who falsifies them*, not by file:

- **Task 1 owns the five sites its own diff touches or falsifies** (the three `client.py` comments, `test_fetch_all_rejects_a_missing_items_key`, and the `Page` docstring that is the natural home of the new rationale). They land in the same commit as the two field declarations, exactly as section 10 asks.
- **Task 2 owns the remaining four**, all of which are cross-model *relationship* claims that Task 1 does not falsify - plus the README. Task 2 is the section 7.4 sweep and carries the full enumerated site list and the grep battery over all nine.

### Refuted: section 8's Go decision is an INTENTION, not a decision, because the item it is conditioned on does not exist

The spec's own test: "the plan must confirm that item exists before this spec's Go paragraph counts as a decision rather than an intention." I searched `docs/backlog/` and `docs/backlog/closed/` for `PageEnvelope` and for `relayclient`. **There is no item for this defect.** The four hits are unrelated (`bug-2026-08-26-relayclient-has-no-response-bound-and-no-client-timeout` is about response size and timeouts; the other three are a closed pagination item, a closed logs item, and the CLI fixture idea).

The underlying claim is nonetheless **true, and I verified it directly** rather than inheriting it. `internal/relayclient/page.go:11-15` declares `NextCursor string` tagged `next_cursor` with no pointer and no presence check, and `FetchAllPages` at `:156` does `if resp.NextCursor == "" { return out, total, nil }`. Go's `encoding/json` leaves an absent key at the zero value, so a dropped key returns the first page and reports success - the identical defect in the identical position.

**Action for the conductor: file it.** Named in "Backlog items this plan needs filed" below. Until it is filed, the Go paragraph in the spec is an intention, and no comment in this PR may cite it as a tracked item.

### Refuted: `test_packaging.py`'s "README guard" is named as a gate for this slice and is not one

`test_readme_client_api_table_documents_every_public_method` guards that the `## Client API` table documents every public `Client` method. This slice adds no public method and edits the README's **Errors** section, which is a different `##` section and outside the guard's scope by construction (the guard splits on `"## Client API"` then on `"\n## "`). It will run and it will pass, and it would have passed if Task 2 were skipped entirely. Keep it in the gate - it is part of `pytest tests/unit` - but **do not report it as evidence that the README edit landed.** The evidence for that is a human reading it, per section 10's own note that a grep is the instrument, not the claim.

### Confirmed: the five claims I tried hardest to break

"The spec was correct" is indistinguishable from having checked nothing, so here is what I measured.

1. **Section 7.1's "exactly ONE fixture in the SDK breaks" HOLDS**, and I checked the complement rather than the subject. I enumerated every construction of a page-shaped body anywhere under `python/tests/` - the search was for the SHAPE (`"items"` as a JSON key, plus every `_page_response` call site) across both `tests/unit/` and `tests/integration/`, not for the helper. Results: `_page_response` (`test_client.py:53-58`) is typed `next_cursor: str = "", total: Optional[int] = None` and unconditionally emits all three keys, `total` falling back to `len(items)` - it **cannot** express a missing key, which is why the type-varying tests were already hand-written. Its call sites are all complete by construction. The hand-written envelope literals are `test_client.py:1149` (`{"items", "next_cursor", "total"}`), `:1823`, `:1844`, `:1876-1880`, `:1900` - all three keys in every one. `test_models.py:265-271` carries all three. `tests/integration/test_smoke.py` constructs no envelope at all; its only mention of one is inside a docstring. **The single breaker is `test_models.py:278-282`, `test_page_defaults_empty_cursor_and_zero_total`**, which validates `{"items": []}` and asserts both defaults.
2. **No existing fixture goes VACUOUS**, which is the failure mode the conductor asked about and is not the same question as "does it go red". Three candidates, each checked: `test_fetch_all_rejects_a_missing_items_key` (`:1908`) omits ONLY `items` and carries the other two, so it still isolates `items` and still discriminates; `test_fetch_all_rejects_a_null_items` (`:1888`) sends `items: None` with both other keys present, and an explicit null fails a required field regardless of this change; `test_fetch_all_rejects_a_non_integer_total` (`:1852`) parametrizes `["many", None, {"n": 1}]` with all three keys present, and every one is a type failure, not an absence failure. None of the three has its RED moved to an earlier line by this change.
3. **Section 9.3's mutation kill table is CORRECT, including the M1/M2 asymmetry, and the spec's warning about `total` is load-bearing.** I hand-executed each row. M1 (`next_cursor: str = ""` restored): T1's body `{"items": [job], "total": 99}` decodes, `next_cursor` is `""`, `_fetch_all` takes the drained return and gives back one `Job` - RED. T3 same body through `list_jobs_page` - decodes, no raise - RED. T4[next_cursor] - decodes - RED. T2's body `{"items": [job], "next_cursor": ""}` still lacks the required `total`, so it still raises - **GREEN, as claimed.** M2 (`total: int = 0` restored): T2 decodes, cursor is `""`, drained return, one `Job` - RED. T4[total] - RED. T1 and T3 still lack the required `next_cursor`, so they still raise - **GREEN, as claimed.** The spec is right that omitting both keys from one fixture would collapse M1 and M2 into one indistinguishable mutation; T1 carries `total: 99` and T2 carries `next_cursor: ""` precisely to prevent that. M4's control behaviour also holds: with `items: list[T] = []`, `test_fetch_all_rejects_a_missing_items_key`'s body decodes to a drained empty page and the test dies, while `test_fetch_all_rejects_a_null_items` survives because an explicit `null` is not an absent key.
4. **Both named guards exist.** `test_version_files_are_in_lockstep` is at `python/tests/unit/test_packaging.py:48-55` and compares `^version = "..."` in `pyproject.toml` against `relay.__version__`; both currently read `0.2.1`. The README guard exists at `:11-45` (see the refutation above for what it actually guards).
5. **Section 2's safety argument HOLDS.** `internal/api/pagination.go`'s `page[T]` tags `items`, `next_cursor` and `total` with no `omitempty` on any of them, so `encoding/json` emits all three keys on every response including the zero-row one, and `buildPage` is the single envelope constructor.

### Added by this plan, not in the spec

- **A trap in the Q1 rewrite the spec does not warn about.** The obvious sentence for the `LogPage` docstring is "`Page`, `LogPage` and `LogRecord` are response-only and nothing constructs one". **That is false for `LogRecord`**: `python/tests/unit/test_errors.py:95-99` constructs three of them by keyword. It is true for `Page` and `LogPage` - I searched for `Page(`, `LogPage(` and `LogRecord(` across all of `python/` and the only constructor hits are those three `LogRecord` lines. Task 2's wording is scoped to `Page` and `LogPage` for that reason. This is the exact shape of the wrong-prose defect the spec is trying to avoid, one layer in.
- **A second exemption axis the spec's Q1 does not name.** Q1 asks the plan to say which axis `Job`'s exemption sits on and correctly identifies it as authoring. But a rewrite that says "response-only models declare every field required" is **false in this same file**: `Worker.labels`, `Reservation.selector`, `Reservation.worker_ids` and `Task.env`/`requires`/`commands` are response-model fields with `Field(default_factory=...)`. Task 2's answer names three axes, not two.
- **The `make` targets.** The spec names raw `pytest` / `ruff` / `mypy` invocations. The repo has `make python-test`, `make python-lint` and `make python-test-integration` (`Makefile:165-176`), which drive a venv at `python/.venv`. Named in "Verification gates".

---

## Answers to the spec's section 14 open questions

### Q1 - the replacement wording for `LogPage`'s cross-reference. DECIDED: rewrite as agreement, keep the reason, and move the contrast onto three named axes

The current docstring's *reason* is the best statement of this defect class in the codebase and must survive verbatim in substance: **an absent key with a benign-looking default is not a missing value, it is a fabricated one, and for a cursor the fabricated value is "there is nothing more".** That sentence is axis-free and stays.

What must change is the contrast, because after this slice `Page` agrees rather than departs. The trap the spec warns about is real and there are **two** ways to fall into it, not one. Both are closed by naming the axis explicitly:

| Axis | Who is on it | Why the default is or is not allowed |
|---|---|---|
| **Cursor / termination** | `LogPage.next_seq`, `Page.next_cursor` | The default value **is** the loop's stop signal (`0`, `""`). An absent key becomes a control-flow lie. Required, non-negotiable. This is the axis the whole rule is about. |
| **Reported count** | `LogPage.total`, `Page.total` | Not control flow. It is a public number a `*_page` caller renders and an error message quotes, so a silent `0` is a wrong number rather than a missing one. Required, for the weaker reason. |
| **Authoring** | `Job.total_tasks`, `done_tasks`, `started_at`, `finished_at`, `scheduled_job_id`, `scheduled_job_name`, and `Task`'s authoring fields | Exempt because a CALLER constructs the object - `Job(name="nightly")` is the README's first example. The question is who builds it, not what the value means. |
| **Payload container** | `Worker.labels`, `Reservation.selector`, `Reservation.worker_ids`, `Task.env` / `requires` / `commands` | Exempt because an empty dict or list is the honest reading of an absent map, and no control flow or reported count is derived from it. |

**So the rule stated in `LogPage`'s docstring is scoped to PAGE-ENVELOPE fields, and the docstring says so.** It does not generalize to every default in `models.py`, and the two exemptions are on different axes from each other. Exact wording in Task 2, Step 1.

### Q2 - does the page-cap comment keep a `total`-versus-`next_cursor` distinction? DECIDED: no. Delete it and replace it with a PRESENCE statement

The distinction the comment draws is between two *classes of default*, and after this slice neither default exists, so the sentence has no referent. Keeping any version of it re-creates the wrong prose one generation later.

What is newly true at that line and worth recording instead: **`page.total` is present because the model requires it - a page that omitted it raised in `_get_page` and never reached the cap.** That is a claim about provenance and it is exactly the right claim there, because the paragraph immediately below it is about not settling completeness with a server-supplied number. Presence and truth are different properties and the comment now says both. Section 5.2's argument (the safe-direction claim was scoped to one reader and there are seven) belongs in the spec and in `Page`'s docstring, where the requirement is declared, not at a page-cap message that no missing `total` can reach.

### Q3 - `0.2.2` or `0.3.0`? DECIDED: `0.2.2`

Four reasons, in order of weight. No public signature changes - the twelve method names, parameters and return types are byte-identical. The behaviour change is only observable against a response **no correct server produces**, and section 2 measured that (`page[T]` carries no `omitempty`). The project has no CHANGELOG and no release notes, so a minor bump advertises to nobody; the advertisement this slice actually owes is the README Errors paragraph, and Task 2 writes it. And there is a larger, genuinely breaking change queued behind this one - the `_read_json` chokepoint that moves decode failures into `RelayError` and *will* change what an existing `except` clause catches - which is a better place to spend a minor bump.

Reversible: if the human prefers `0.3.0`, it is a two-file edit in Task 4 and nothing else in this plan moves.

---

## File structure

| File | Change |
|---|---|
| `python/src/relay/models.py` | Two field declarations on `Page` (drop both defaults) + `Page` docstring rewrite (Task 1); `LogPage` docstring + `Job` enrichment comment (Task 2). **The only production behaviour change in this plan is two lines.** |
| `python/src/relay/client.py` | **Comment edits only, three sites. No logic change.** (Task 1) |
| `python/tests/unit/test_client.py` | Three new tests + one docstring correction (Task 1). |
| `python/tests/unit/test_models.py` | One test replaced (Task 1); two docstring corrections (Task 2). |
| `python/README.md` | Errors section gains the fail-closed advertisement (Task 2). |
| `python/tests/integration/test_smoke.py` | One docstring note - the test acquires a new job (Task 3). |
| `python/pyproject.toml`, `python/src/relay/_version.py` | `0.2.1` -> `0.2.2` (Task 4). |

---

## Task 1: The strict envelope - RED first, then two field declarations

**Files:**
- Test: `python/tests/unit/test_client.py` (append after `test_fetch_all_rejects_a_missing_items_key`, currently the last test in the file; and edit that test's docstring)
- Test: `python/tests/unit/test_models.py` (replace `test_page_defaults_empty_cursor_and_zero_total`)
- Modify: `python/src/relay/models.py` (`Page` - docstring and two field declarations)
- Modify: `python/src/relay/client.py` (`_get_page`, `_fetch_all` x2 - comments only)

- [ ] **Step 0: Record the line-ending baseline**

This repo is CRLF and `git diff` normalizes what `git status` does not. Capture the baseline so Task 6 can compare rather than guess.

```bash
cd D:/dev/relay/.claude/worktrees/stoic-cannon-15b269
git ls-files --eol python/src/relay/models.py python/src/relay/client.py \
  python/tests/unit/test_client.py python/tests/unit/test_models.py \
  python/README.md python/pyproject.toml python/src/relay/_version.py \
  python/tests/integration/test_smoke.py
```

Write the output down. Every touched file must report the same `i/` value at the end of Task 6 as it does now.

- [ ] **Step 1: Write the headline failing test**

Append to `python/tests/unit/test_client.py`:

```python
# ─── envelope ABSENCE ────────────────────────────────────────────────────────
#
# The section above varies the TYPE of an envelope field. These vary its
# PRESENCE, which is the sharper case: a wrong type crashes somewhere loud,
# while an absent key used to decode into a value that looked legitimate.
# `next_cursor`'s default was the empty string and the empty string is
# _fetch_all's drained signal, so a dropped key reported the list finished -
# `list_jobs()` returned page 1 and raised nothing, and no caller could tell a
# 200-row prefix from a complete 200-row list.
#
# _page_response cannot express these bodies: it is typed `next_cursor: str`
# with `total` defaulting to len(items), so it always emits all three keys.
# That is why it survived this change untouched, and why these are hand-written.
#
# Each fixture omits EXACTLY ONE of the two keys and carries the other. A single
# fixture omitting both would make the two field declarations indistinguishable
# - restoring either default alone would leave it green - and the pair would
# look covered while pinning nothing.


def test_fetch_all_rejects_a_missing_next_cursor() -> None:
    """A body with no `next_cursor` key must RAISE, not read as drained.

    `total` is PRESENT and non-zero here on purpose. It is what separates this
    test from test_fetch_all_rejects_a_missing_total: restoring
    `total: int = 0` alone must leave this one GREEN.

    A request-count assertion would not be evidence here and is deliberately
    absent: the correct code raises on request 1 and the old code stopped after
    request 1, so `len(calls) == 1` holds under both. The 500 terminator stays
    per this file's convention - it costs nothing, and it turns a mutant that
    keeps walking into a failure instead of a hang, since the project has no
    pytest-timeout.
    """
    calls: list[dict[str, str]] = []

    def handler(request: httpx.Request) -> httpx.Response:
        calls.append(dict(request.url.params))
        if len(calls) > 1:
            return httpx.Response(500, json={"error": "past the decode"})
        return httpx.Response(
            200, json={"items": [_job_response(id="j1")], "total": 99}
        )

    client = _make_client(handler)
    with pytest.raises(PydanticValidationError):
        client.list_jobs()
```

- [ ] **Step 2: Run it and verify it fails**

```bash
cd D:/dev/relay/.claude/worktrees/stoic-cannon-15b269/python
python -m pytest tests/unit/test_client.py::test_fetch_all_rejects_a_missing_next_cursor -v
```

Expected: `FAILED ... Failed: DID NOT RAISE <class 'pydantic_core._pydantic_core.ValidationError'>`

- [ ] **Step 3: Prove the RED is the silent truncation itself, then revert the proof**

`DID NOT RAISE` alone does not say what happened instead, and "a green test can be vacuous" cuts both ways - a red one can be red for a fixture typo. Temporarily replace the last two lines of the test body with:

```python
    jobs = client.list_jobs()
    assert [j.id for j in jobs] == ["j1"]
    assert len(calls) == 1
```

Run the same command. Expected: **PASS.** That is the defect stated as a passing assertion: one request, one row, no error, and the caller has been told the list is complete.

**Now revert those three lines back to the `pytest.raises` form from Step 1.** Do not commit the diagnostic version. Record the observation in the task notes; the permanent test is the `raises` one.

- [ ] **Step 4: Write the other two client-level tests**

Append after the headline test in `python/tests/unit/test_client.py`:

```python
def test_fetch_all_rejects_a_missing_total() -> None:
    """The other half. `next_cursor` is PRESENT and DRAINED here, so a walk that
    ignored the missing `total` would terminate normally with one row and
    report success - which is exactly what it did before this slice.

    Restoring `next_cursor: str = ""` alone must leave this one GREEN. That is
    what makes the two field declarations separately pinned.
    """
    calls: list[dict[str, str]] = []

    def handler(request: httpx.Request) -> httpx.Response:
        calls.append(dict(request.url.params))
        if len(calls) > 1:
            return httpx.Response(500, json={"error": "past the decode"})
        return httpx.Response(
            200, json={"items": [_job_response(id="j1")], "next_cursor": ""}
        )

    client = _make_client(handler)
    with pytest.raises(PydanticValidationError):
        client.list_jobs()


def test_get_page_rejects_a_missing_next_cursor() -> None:
    """The same body through `list_jobs_page` - one request, no walk at all.

    `_get_page` and `_fetch_all` share the model today, so this looks
    redundant, and it is the six one-page methods' only pin. Nothing structural
    forbids a lenient path being added back to `_get_page` - a `.get()` at the
    call site is exactly how this defect was originally written - and a
    `list_jobs_page` caller reads `page.next_cursor` to decide whether to ask
    for more. Wired, not just the helper.

    `len(calls) == 1` documents that the refusal happens after the request, not
    instead of it. It does not discriminate the fix: the un-fixed code also made
    exactly one request and returned a Page.
    """
    calls: list[dict[str, str]] = []

    def handler(request: httpx.Request) -> httpx.Response:
        calls.append(dict(request.url.params))
        return httpx.Response(
            200, json={"items": [_job_response(id="j1")], "total": 99}
        )

    client = _make_client(handler)
    with pytest.raises(PydanticValidationError):
        client.list_jobs_page()

    assert len(calls) == 1
```

- [ ] **Step 5: Replace the model test that asserts the removed behaviour**

In `python/tests/unit/test_models.py`, **delete** `test_page_defaults_empty_cursor_and_zero_total` (currently lines 278-282) and put this in its place. Deleting without replacing would leave `Page`'s field-optionality unasserted at the model level.

```python
@pytest.mark.parametrize("missing", ["next_cursor", "total"])
def test_page_requires_next_cursor_and_total(missing: str) -> None:
    """Both REQUIRED and undefaulted, matching LogPage's next_seq and total.

    Replaces test_page_defaults_empty_cursor_and_zero_total, which asserted the
    opposite. `next_cursor: str = ""` made an ABSENT key decode to the empty
    string, and the empty string is _fetch_all's drained signal - so a dropped
    key reported the list finished, and twelve methods over six routes returned
    a 200-row prefix indistinguishable from a complete list. `total: int = 0`
    is the milder half: not a control-flow signal, but a public number the six
    *_page methods hand back for a caller to render.

    Requiring them costs nothing against a correct server. internal/api's
    page[T] envelope carries no omitempty on any of its three json tags, so
    encoding/json emits all three keys on every response including the zero-row
    one - the same argument test_log_page_requires_next_seq_and_total makes for
    LogPage.

    One key is deleted per case, never both: a body missing both would go red
    under either default alone and could not tell the two apart.
    """
    body = {"items": [], "next_cursor": "c", "total": 5}
    del body[missing]
    with pytest.raises(PydanticValidationError):
        Page[Job].model_validate(body)
```

- [ ] **Step 6: Run all four and verify all four fail**

```bash
cd D:/dev/relay/.claude/worktrees/stoic-cannon-15b269/python
python -m pytest \
  tests/unit/test_client.py::test_fetch_all_rejects_a_missing_next_cursor \
  tests/unit/test_client.py::test_fetch_all_rejects_a_missing_total \
  tests/unit/test_client.py::test_get_page_rejects_a_missing_next_cursor \
  tests/unit/test_models.py::test_page_requires_next_cursor_and_total -v
```

Expected: **5 failed** (four tests, one of them parametrized twice), every one `Failed: DID NOT RAISE <class 'pydantic_core._pydantic_core.ValidationError'>`.

- [ ] **Step 7: Make the change - two field declarations and the `Page` docstring**

In `python/src/relay/models.py`, replace the whole `Page` class (currently lines 545-557):

```python
class Page(BaseModel, Generic[T]):
    """One page of a paginated list response.

    ``next_cursor`` is the empty string on the last page; pass it back as
    ``cursor=`` to fetch the next page. ``total`` is the server's count of
    all matching rows, not just this page.

    All three fields are REQUIRED and undefaulted, and ``next_cursor`` is the
    one that matters. The empty string is this SDK's drained signal, so a
    defaulted ``next_cursor: str = ""`` read an ABSENT key as "the list ended":
    :meth:`relay.Client.list_jobs` returned page 1, raised nothing, and no
    caller could tell a 200-row prefix from a complete 200-row list. ``total``
    is the milder half - not a control-flow signal, but a number the six
    ``*_page`` methods hand back for a caller to render, where a silent 0 is a
    wrong number rather than a missing one.

    Requiring them costs nothing against a correct server: internal/api's
    ``page[T]`` envelope tags ``items``, ``next_cursor`` and ``total`` with no
    ``omitempty``, so all three keys are emitted on every response including
    the zero-row one, and ``buildPage`` is the only thing that builds it.

    ``extra="ignore"`` stays. Strictness here is about the ABSENCE of a
    contract field, not the presence of an unknown one - opposite directions.
    A model that rejected new envelope fields could not talk to a newer server.
    """

    model_config = ConfigDict(extra="ignore")

    items: list[T]
    next_cursor: str
    total: int
```

- [ ] **Step 8: Correct the three `client.py` comments this change falsifies**

**8a.** In `_get_page`, replace the paragraph currently at lines 263-267 (it begins ``# `next_cursor: str = ""` and `total: int = 0` keep their DEFAULTS``) with:

```python
        # `next_cursor` and `total` are REQUIRED and undefaulted, so a MISSING
        # key is a decoding error here rather than a value. They carried
        # defaults until the strict-envelope slice, and `next_cursor: str = ""`
        # meant an absent key decoded to the drained signal: _fetch_all below
        # stopped, and list_jobs() returned page 1 and reported success.
        # Requiring them costs nothing - internal/api's `page[T]` tags all three
        # fields without `omitempty`, so a correct server always sends all three.
        #
        # The failure is `pydantic.ValidationError`, which does NOT descend from
        # RelayError. Deliberate, and not a new class of escape: python/README.md
        # already documents it for every response body. Routing it belongs to the
        # single `_read_json` chokepoint over all twelve `response.json()` sites
        # in bug-2026-08-27-python-sdk-exceptions-escape-the-relayerror-hierarchy.
        # A local try/except here would make _get_page and task_logs_page raise
        # DIFFERENT types for the identical defect shape, which the chokepoint
        # would then have to unwind.
```

**8b.** In `_fetch_all`, replace the comment currently at lines 331-334 (it begins `# NOT changed by this slice`) with:

```python
            # `page.next_cursor` is PRESENT by construction: the field is
            # required on the model, so a body that omitted the key raised in
            # _get_page above and never reached this line. An empty string here
            # is therefore the server SAYING drained, not the SDK inferring it
            # from an absent key - which is what it used to be, and what made a
            # dropped key silently truncate every walk to its first page.
```

**8c.** In `_fetch_all`'s page-cap block, replace the comment currently at lines 440-444 (it begins `# From the CURRENT page`), keeping the `total = page.total` line beneath it:

```python
                # From the CURRENT page, and the message says so rather than
                # implying the number is authoritative. It is PRESENT because
                # the model requires it: a page that omitted `total` raised in
                # _get_page and never reached the cap. That is a claim about
                # PROVENANCE, not about truth - `total` is still server-supplied
                # and still unverifiable, which is why the message reports it
                # beside a distinct-id count instead of settling completeness
                # with it.
                total = page.total
```

- [ ] **Step 9: Correct the test docstring this change falsifies**

In `python/tests/unit/test_client.py`, replace the docstring of `test_fetch_all_rejects_a_missing_items_key` (currently lines 1909-1913):

```python
    """The absent-key sibling of the null case, and it still isolates `items`:
    the fixture carries `next_cursor` and `total`, so `items` is the only key
    missing and the only thing that can produce the raise.

    All THREE of Page's fields are required and undefaulted. `next_cursor` and
    `total` carried defaults until the strict-envelope slice; their absent-key
    cases are test_fetch_all_rejects_a_missing_next_cursor and
    test_fetch_all_rejects_a_missing_total.
    """
```

- [ ] **Step 10: Run the whole unit suite and verify green**

```bash
cd D:/dev/relay/.claude/worktrees/stoic-cannon-15b269/python
python -m pytest tests/unit -v
```

Expected: **all pass, zero failures.** If `test_page_defaults_empty_cursor_and_zero_total` still appears in the output, Step 5's deletion did not land.

- [ ] **Step 11: Lint and type-check**

```bash
cd D:/dev/relay/.claude/worktrees/stoic-cannon-15b269/python
python -m ruff check src tests
python -m mypy src
```

Expected: `All checks passed!` and mypy reporting success.

- [ ] **Step 12: Check the diffstat against the intended change, then commit**

```bash
cd D:/dev/relay/.claude/worktrees/stoic-cannon-15b269
git diff --stat python/
```

Expected: exactly 4 files. `models.py` and `client.py` are tens of lines (docstring and comments); `test_client.py` gains roughly 90 lines; `test_models.py` is roughly net zero. **If any file reports hundreds of changed lines, stop** - that is the CRLF-reclassification failure mode, not your edit.

```bash
git add python/src/relay/models.py python/src/relay/client.py \
        python/tests/unit/test_client.py python/tests/unit/test_models.py
git commit -m "fix(python): a paged envelope missing next_cursor or total must raise, not read as drained"
```

Use an explicit pathspec, never a bare `git commit -a`.

---

## Task 2: The section 7.4 prose sweep - the four relationship claims and the README

**Files:**
- Modify: `python/src/relay/models.py` (`LogPage` docstring; `Job`'s list-only enrichment comment)
- Modify: `python/tests/unit/test_models.py` (`test_log_page_requires_next_seq_and_total` docstring; `test_job_authoring_does_not_require_enrichment_fields` docstring)
- Modify: `python/README.md` (Errors section)

**The enumerated site list, by SYMBOL.** Line numbers rot; symbols do not. Five were done in Task 1 and are listed so the sweep is auditable as a whole.

| # | Symbol | File | Owner |
|---|---|---|---|
| 1 | `Page` (class docstring) | `python/src/relay/models.py` | Task 1, Step 7 |
| 2 | `Client._get_page` (the DEFAULTS paragraph) | `python/src/relay/client.py` | Task 1, Step 8a |
| 3 | `Client._fetch_all` (the comment above `cursor = page.next_cursor`) | `python/src/relay/client.py` | Task 1, Step 8b |
| 4 | `Client._fetch_all` (the comment above `total = page.total` in the page-cap block) | `python/src/relay/client.py` | Task 1, Step 8c |
| 5 | `test_fetch_all_rejects_a_missing_items_key` (docstring) | `python/tests/unit/test_client.py` | Task 1, Step 9 |
| 6 | `LogPage` (class docstring) | `python/src/relay/models.py` | **Task 2, Step 1** |
| 7 | `Job` (the `# List-only enrichment` comment) | `python/src/relay/models.py` | **Task 2, Step 2** |
| 8 | `test_log_page_requires_next_seq_and_total` (docstring) | `python/tests/unit/test_models.py` | **Task 2, Step 3** |
| 9 | `test_job_authoring_does_not_require_enrichment_fields` (docstring) | `python/tests/unit/test_models.py` | **Task 2, Step 4** |
| 10 | `python/README.md` "## Errors" section | `python/README.md` | **Task 2, Step 5** (new text, required scope per spec section 6) |
| 11 | `python/README.md` pagination paragraph | `python/README.md` | **Task 2, Step 6** (re-check, expected: no edit) |

- [ ] **Step 1: Rewrite the `LogPage` docstring as agreement, with the axes named**

In `python/src/relay/models.py`, replace the paragraph in `LogPage`'s docstring beginning ``` ``next_seq`` and ``total`` are REQUIRED, unlike :class:`Page` ``` (currently lines 466-470) with:

```python
    ``next_seq`` and ``total`` are REQUIRED and undefaulted, and so are
    :class:`Page`'s ``next_cursor`` and ``total``. The two envelopes agree; what
    matters is the reason, which is the same for both. A defaulted
    ``next_seq: int = 0`` would read a MISSING key as "drained" and silently
    return page 1, because 0 is this walk's end-of-log signal - and ``Page``
    had exactly that hole with ``next_cursor: str = ""`` until it was closed.
    An absent key with a benign default is not a missing value. It is a
    FABRICATED one, and for a cursor the fabricated value is "there is nothing
    more".

    Read that as a rule about PAGE-ENVELOPE fields, not about this file. It does
    not generalize to every default here, and the exemptions sit on two axes
    that are not the same as each other:

    - :class:`Job`'s list-only enrichment fields are exempt on the AUTHORING
      axis. ``Job`` is the model a caller CONSTRUCTS - ``Job(name="nightly")``
      is the README's first example - so a required ``total_tasks`` would break
      every authoring call site. The question there is who BUILDS the object,
      not what the value means. ``Page`` and ``LogPage`` are response-only and
      nothing in ``relay/`` or its tests constructs one, which is why the strict
      rule costs nothing here.
    - Container fields such as ``Worker.labels`` and ``Reservation.selector``
      are exempt on the PAYLOAD axis: an empty dict is the honest reading of an
      absent map, and no control flow and no reported count is derived from it.
      A cursor is neither a container nor a payload - it is the loop's stop
      condition, which is why it gets no default at all.
```

**Do not widen "nothing constructs one" to include `LogRecord`.** `python/tests/unit/test_errors.py:95-99` constructs three `LogRecord`s by keyword. The claim is true for `Page` and `LogPage` and is scoped to them deliberately.

- [ ] **Step 2: Rewrite `Job`'s list-only enrichment comment**

In `python/src/relay/models.py`, replace the comment block above `total_tasks: int = 0` (currently lines 338-342):

```python
    # List-only enrichment (GET /v1/jobs rows). The server computes these from
    # the job's tasks and its scheduled-job source, and does not populate them
    # on single-job routes. They are DEFAULTED because Job is the authoring
    # model too - Job(name="nightly") must keep working.
    #
    # That is an exemption on the AUTHORING axis and it is the only axis it is
    # on. Page and LogPage require every envelope field they declare, and Page
    # is now the closer analogue of the two: like Job it is a response model
    # over a generic item list, and unlike Job nothing constructs one, so
    # requiring its fields costs nothing. Do not "make these consistent" - what
    # makes these six defaulted is who BUILDS a Job, not what the values mean.
```

- [ ] **Step 3: Rewrite `test_log_page_requires_next_seq_and_total`'s docstring**

This one has been stale since #161, independently of this slice: it describes `_get_page`'s `body.get('next_cursor', '')` as a live departure and that statement no longer exists anywhere in `python/src/relay/`. In `python/tests/unit/test_models.py`, replace its docstring (currently lines 403-409):

```python
    """A defaulted `next_seq: int = 0` would read a MISSING key as "drained" and
    silently return page 1 - the defect shape this family of models exists to
    refuse. The handler writes both keys unconditionally from a map literal, so
    requiring them costs nothing.

    This used to call itself "a deliberate departure from _get_page's
    body.get('next_cursor', '')". Both halves of that are gone: the body.get()
    was deleted when the envelope was routed through Page[model], and Page's own
    next_cursor/total defaults were removed after it. The sibling assertion is
    test_page_requires_next_cursor_and_total above.
    """
```

- [ ] **Step 4: Rewrite `test_job_authoring_does_not_require_enrichment_fields`'s docstring**

In `python/tests/unit/test_models.py`, replace its docstring (currently lines 476-484):

```python
    """The six D3 fields are DEFAULTED, and that is deliberate rather than a
    lapse from the strict rule Page and LogPage both follow.

    The exemption is on the AUTHORING axis and nothing else. Job is the
    authoring model as well as the response model - Job(name=...) is the
    README's first example - so a required total_tasks would break every
    authoring call site. Page and LogPage are response-only and nothing
    constructs one, which is why the strict rule costs nothing there and
    everything here. Do not "make these consistent".
    """
```

- [ ] **Step 5: Add the fail-closed advertisement to the README's Errors section**

Spec section 6 argues this is required scope, not documentation polish: this slice makes an already-documented escape reachable on a new input, and a signal discloses its properties where it is READ. In `python/README.md`, insert immediately after the line `That gap is known and tracked separately.` (currently line 245) and before the error table:

```markdown
The first of those two escapes now has one more occasion, and it is a
**fail-closed change** worth naming. Every paged envelope - the six `list_*`
walks and their six `*_page` siblings - requires `items`, `next_cursor` and
`total`. A 200 whose envelope OMITS `next_cursor` or `total` raises
`pydantic.ValidationError` rather than decoding into a page that reports the
list drained. Before, a dropped key returned page 1 and reported success, and
nothing in the return value distinguished a 200-row prefix from a complete
200-row list. A correct server never produces this: the envelope is written by
one `page[T]` struct whose three json tags carry no `omitempty`.

It is still not a `RelayError`, so `except relay.ValidationError` does **not**
catch it - the two classes share a name, and that trap is why
`bug-2026-08-27-python-sdk-exceptions-escape-the-relayerror-hierarchy` is
tracked. Catch `pydantic.ValidationError` explicitly until that lands. Nothing
counted above changes: this widens when the first escape fires, not what the
escapes are.
```

- [ ] **Step 6: Re-check the README's pagination paragraph rather than inheriting the spec's read**

Read `python/README.md` from the `## Client API` table through the end of `### Reading a task's log`. Confirm by eye that **no sentence describes `next_cursor` or `total` as optional, defaulted, or safe to omit.** The sentence at "A list with **no matching rows is not an error** - it answers `items: []` with an empty cursor, which is the drained signal" stays: an empty cursor that is PRESENT is exactly what the server sends, and it is what the SDK now requires.

Expected outcome: **no edit.** If you find one, correct it and say so in the task notes - the spec flagged this as a claim to re-check rather than inherit.

- [ ] **Step 7: Run the grep battery over all nine sites**

```bash
cd D:/dev/relay/.claude/worktrees/stoic-cannon-15b269
grep -rn -E "body\.get|keep their DEFAULTS|Page's own default|unlike :class:|still reads as drained|no-default rule|bug-2026-08-27-python-sdk-page-cursor-defaults-to-drained" python/
```

Expected: **zero hits.** At HEAD this pattern set hits nine lines across four files (`client.py` x4 lines, `models.py` x2, `test_models.py` x2, plus the item name); after Tasks 1 and 2 every one is gone.

**The grep is the instrument, not the claim.** A pattern list can only establish that these known-wrong sentences are absent. It cannot establish that the replacements are correct - read all nine replacements once, in order, before moving on.

- [ ] **Step 8: Run the gate**

```bash
cd D:/dev/relay/.claude/worktrees/stoic-cannon-15b269/python
python -m pytest tests/unit -v
python -m ruff check src tests
python -m mypy src
```

Expected: all pass. Note that `test_readme_client_api_table_documents_every_public_method` passing is **not** evidence the README edit landed - it guards the `## Client API` method table, a different section, and would pass with Step 5 skipped.

- [ ] **Step 9: Commit**

```bash
cd D:/dev/relay/.claude/worktrees/stoic-cannon-15b269
git diff --stat python/
git add python/src/relay/models.py python/tests/unit/test_models.py python/README.md
git commit -m "docs(python): correct the Page/LogPage/Job prose the strict envelope moved, and advertise the fail-closed decode"
```

---

## Task 3: Record the new job the integration lane acquired

**Files:**
- Modify: `python/tests/integration/test_smoke.py` (`test_a_list_with_no_matching_rows_returns_empty_and_does_not_raise` docstring)

The project's recurring miss is a slice that gives existing code a new job and pins nothing. Two integration tests acquire one here for free, and neither says so.

- [ ] **Step 1: Append to the zero-row test's docstring**

In `python/tests/integration/test_smoke.py`, add as the final paragraph of `test_a_list_with_no_matching_rows_returns_empty_and_does_not_raise`'s docstring, before the closing `"""`:

```
    Since the strict-envelope slice this test carries a SECOND job, and it is
    the only live-server proof of it: Page requires next_cursor and total as
    KEYS, so an `omitempty` creeping onto internal/api's page[T] would make this
    call raise pydantic.ValidationError instead of returning []. No fixture can
    see that - the server's serializer is what is under test, and the zero-row
    page is the shape where an omitempty would most plausibly bite.
    test_list_jobs_includes_recent_submission is the same proof for the
    non-empty shape. Under the old model these two proved only that the cursor
    was empty-or-absent; they now prove present-and-empty.
```

- [ ] **Step 2: Verify nothing behavioural changed**

```bash
cd D:/dev/relay/.claude/worktrees/stoic-cannon-15b269
git diff python/tests/integration/test_smoke.py
```

Expected: additions inside a docstring only. Zero changed statements, zero changed assertions.

- [ ] **Step 3: Lint (the integration lane itself is NOT a gate here)**

```bash
cd D:/dev/relay/.claude/worktrees/stoic-cannon-15b269/python
python -m ruff check src tests
```

Expected: `All checks passed!`. `ruff check src tests` covers `tests/integration/` too, so the docstring is syntax-checked without a server. Running the lane itself needs a live `relay-server` plus `RELAY_INTEGRATION=1` and is optional - it is not in CI, and a docstring cannot redden it.

- [ ] **Step 4: Commit**

```bash
cd D:/dev/relay/.claude/worktrees/stoic-cannon-15b269
git add python/tests/integration/test_smoke.py
git commit -m "test(python): record the live-server property the strict envelope gave the smoke lane"
```

---

## Task 4: Version bump to 0.2.2

**Files:**
- Modify: `python/pyproject.toml:7`
- Modify: `python/src/relay/_version.py:1`

Two hand-maintained copies of one number. `test_version_files_are_in_lockstep` (`python/tests/unit/test_packaging.py:48-55`) exists to make moving one of them RED, so this task moves one first on purpose - a guard that is never observed failing is a guard nobody has checked.

- [ ] **Step 1: Bump `pyproject.toml` only, and watch the guard go RED**

`python/pyproject.toml` line 7: `version = "0.2.1"` -> `version = "0.2.2"`.

```bash
cd D:/dev/relay/.claude/worktrees/stoic-cannon-15b269/python
python -m pytest tests/unit/test_packaging.py::test_version_files_are_in_lockstep -v
```

Expected: `FAILED ... assert '0.2.2' == '0.2.1'`

- [ ] **Step 2: Bump `_version.py`**

`python/src/relay/_version.py` line 1: `__version__ = "0.2.1"` -> `__version__ = "0.2.2"`.

- [ ] **Step 3: Run the gate**

```bash
cd D:/dev/relay/.claude/worktrees/stoic-cannon-15b269/python
python -m pytest tests/unit -v
python -m ruff check src tests
python -m mypy src
```

Expected: all pass.

- [ ] **Step 4: Commit**

```bash
cd D:/dev/relay/.claude/worktrees/stoic-cannon-15b269
git add python/pyproject.toml python/src/relay/_version.py
git commit -m "chore(python): bump relay-jobs to 0.2.2"
```

---

## Task 5: The mutation battery

**Files:** none committed. This task edits `python/src/relay/models.py` four times and reverts every one.

**Isolation.** If any sibling agent is reading this worktree, run this task in a detached worktree (`git worktree add --detach <scratch> HEAD`) and do the mutations there - never mutate a tree a sibling is reading. If this session is the only one, the shared tree is acceptable **provided** Step 5's clean check is run and passes.

- [ ] **Step 1: Establish the green baseline and the verification procedure**

```bash
cd D:/dev/relay/.claude/worktrees/stoic-cannon-15b269/python
python -m pytest tests/unit -q
```

Expected: zero failures. A battery run from a red baseline reports nothing.

**After every mutation edit, before running pytest**, confirm the mutation actually applied:

```bash
cd D:/dev/relay/.claude/worktrees/stoic-cannon-15b269
git diff --numstat python/src/relay/models.py
```

Expected: `1<TAB>1<TAB>python/src/relay/models.py`. Anything else - especially `0 0` - means the edit did not land, and a mutation that silently fails to apply reports "survived". Four mutations in a row have failed this way on this repo under CRLF.

- [ ] **Step 2: M4 - the control, and it runs FIRST**

Edit `python/src/relay/models.py`: `items: list[T]` -> `items: list[T] = []` in `Page`.

```bash
cd D:/dev/relay/.claude/worktrees/stoic-cannon-15b269/python
python -m pytest tests/unit/test_client.py::test_fetch_all_rejects_a_missing_items_key \
                tests/unit/test_client.py::test_fetch_all_rejects_a_null_items -v
```

Expected: `test_fetch_all_rejects_a_missing_items_key` **FAILS** (its body decodes to a drained empty page and `list_jobs()` returns `[]`). `test_fetch_all_rejects_a_null_items` **PASSES** - an explicit `null` fails a field regardless of its default, so it is not a witness for absence.

**If the missing-items test survives, the harness is broken. Stop and fix that before reading any other row.** This row is a control, not a coverage claim.

Revert: `git checkout -- python/src/relay/models.py`.

- [ ] **Step 3: M1, M2 and M3**

For each row: apply the edit, run the numstat check from Step 1, run the command below, then `git checkout -- python/src/relay/models.py`.

```bash
cd D:/dev/relay/.claude/worktrees/stoic-cannon-15b269/python
python -m pytest \
  tests/unit/test_client.py::test_fetch_all_rejects_a_missing_next_cursor \
  tests/unit/test_client.py::test_fetch_all_rejects_a_missing_total \
  tests/unit/test_client.py::test_get_page_rejects_a_missing_next_cursor \
  tests/unit/test_models.py::test_page_requires_next_cursor_and_total -v
```

| # | Mutation in `Page` | Expected RED | Expected GREEN |
|---|---|---|---|
| M1 | `next_cursor: str` -> `next_cursor: str = ""` | T1 `..._missing_next_cursor`, T3 `test_get_page_rejects_a_missing_next_cursor`, T4`[next_cursor]` | T2 `..._missing_total`, T4`[total]` |
| M2 | `total: int` -> `total: int = 0` | T2 `..._missing_total`, T4`[total]` | T1, T3, T4`[next_cursor]` |
| M3 | **both** defaults restored (the HEAD state) | all four, T4 both cases - **5 failed** | none |

The M1/M2 asymmetry is the whole point of the table: each field owns at least one test the other does not redden, so the two declarations are separately pinned. A run where M1 and M2 produce the same red set means one of the fixtures acquired the other's missing key.

- [ ] **Step 4: Also run the FULL unit suite under M1 and M2, once each**

```bash
cd D:/dev/relay/.claude/worktrees/stoic-cannon-15b269/python
python -m pytest tests/unit -q
```

Expected under M1: exactly 3 failures (T1, T3, T4`[next_cursor]`) and nothing else. Under M2: exactly 2 (T2, T4`[total]`).

**An unexpected extra RED is a signal, not noise** - it would mean an existing fixture depends on a default this slice removed, which the pre-plan survey says does not exist. Report it rather than absorbing it.

- [ ] **Step 5: Confirm the tree is clean and record the result**

```bash
cd D:/dev/relay/.claude/worktrees/stoic-cannon-15b269
git status --porcelain python/
```

Expected: **empty.** Nothing from this task is committed. Record the observed table in the task notes so the reviewer can compare against the expected one above.

---

## Task 6: Final gate and tree verification

- [ ] **Step 1: Full Python gate**

```bash
cd D:/dev/relay/.claude/worktrees/stoic-cannon-15b269/python
python -m pytest tests/unit -v
python -m ruff check src tests
python -m mypy src
```

Or, from the repo root, the two make targets that wrap exactly these (`Makefile:165-176`, driving `python/.venv`):

```bash
cd D:/dev/relay/.claude/worktrees/stoic-cannon-15b269
make python-test
make python-lint
```

Expected: zero pytest failures, `All checks passed!` from ruff, and mypy reporting success over the seven modules in `python/src/relay/` (`__init__`, `_version`, `client`, `config`, `errors`, `events`, `models`).

- [ ] **Step 2: Verify the line endings did not move**

```bash
cd D:/dev/relay/.claude/worktrees/stoic-cannon-15b269
git ls-files --eol python/src/relay/models.py python/src/relay/client.py \
  python/tests/unit/test_client.py python/tests/unit/test_models.py \
  python/README.md python/pyproject.toml python/src/relay/_version.py \
  python/tests/integration/test_smoke.py
```

Every file must report the same `i/` value it did in Task 1 Step 0. A file that changed classification - especially to `i/-text` - means a programmatic edit produced `\r\r\n` and git has reclassified it as binary; that is the failure mode that turned a two-line change into 1845 insertions on this repo.

- [ ] **Step 3: Verify the exact file set**

```bash
cd D:/dev/relay/.claude/worktrees/stoic-cannon-15b269
git status --porcelain
git diff --stat origin/main...HEAD
```

Expected: a clean working tree, and **exactly eight** changed files, all under `python/`:
`src/relay/models.py`, `src/relay/client.py`, `src/relay/_version.py`, `pyproject.toml`, `README.md`, `tests/unit/test_client.py`, `tests/unit/test_models.py`, `tests/integration/test_smoke.py`.

**Zero `.go` files. Zero files under `web/`. Zero files under `docs/`** at this point - the plan doc and the spec were committed by the conductor at their phase boundaries, and the backlog close happens after the PR via `/backlog close`.

- [ ] **Step 4: Confirm what CI will run**

`.github/workflows/python.yml` triggers on `paths: python/**`, so this PR fires the `python-sdk` workflow: **15 `test` jobs** (`ubuntu-latest`, `macos-latest`, `windows-latest` x Python `3.9`, `3.10`, `3.11`, `3.12`, `3.13`), each running `pip install -e ".[dev]"` then `pytest tests/unit -v`; plus one `lint` job on Python 3.13 running `ruff check src tests` and `mypy src`.

Local verification runs ONE interpreter. **State which one in the PR body** (`python --version`), because a local green is a sample of the matrix, not the matrix. There is nothing version-sensitive in this change - pydantic v2 required-field semantics are identical across 3.9-3.13, and no new syntax is introduced - but say what was measured rather than implying more.

The Go workflow (`.github/workflows/go-ci.yml`) is not triggered by this PR's paths and needs no local Go gate. `make test`, `make test-race` and `make test-integration` are all out of scope: no Go file changes.

---

## Verification gates, in one place

| Gate | Command | Where it runs | Blocking |
|---|---|---|---|
| Unit tests | `make python-test` / `python -m pytest tests/unit -v` | local (1 interpreter) + CI (15 jobs, 3 OS x py3.9-3.13) | **Yes** |
| Lint | `python -m ruff check src tests` | local + CI `lint` job | **Yes** |
| Types | `python -m mypy src` (strict, `python_version = "3.9"`) | local + CI `lint` job | **Yes** |
| Mutation battery | Task 5 | local only | **Yes** - M4 must die; M1/M2 must have the asymmetric red sets |
| Prose grep battery | Task 2 Step 7 | local only | **Yes** - zero hits, plus a human read of all nine replacements |
| Line endings | `git ls-files --eol` on the 8 touched paths | local only | **Yes** |
| Integration smoke lane | `make python-test-integration` (needs `RELAY_INTEGRATION=1` + a live server) | manual, **not in CI** | No - Task 3 changes a docstring only |
| Go lanes (`make test`, `test-race`, `test-integration`) | - | - | **Not applicable.** Zero Go files change. |

---

## What this plan does NOT do

Each of these was considered and declined with a reason and an owner. Do not let a sweep pull any of them in.

- **No `_read_json` chokepoint, and no wrapping of decode failures in `RelayError`.** `pydantic.ValidationError` escapes unwrapped, exactly as `LogPage`'s already does. Owned by `docs/backlog/bug-2026-08-27-python-sdk-exceptions-escape-the-relayerror-hierarchy.md`. A local `try/except` in `_get_page` would make it and `task_logs_page` raise different types for the identical defect shape, which is how the single-entry-point invariant gets bypassed. The cost is real and this plan pays it in advertisement (Task 2 Step 5), not in code.
- **No change to `get_tasks`**, which iterates a raw `response.json()` and is the SDK's one remaining `TypeError` escape. It is a different, unpaged route, already documented in the README, and now owned by `docs/backlog/bug-2026-08-29-python-sdk-get-tasks-reads-a-raw-response-body.md`. **This is the site a sweep will find** if it reads the item's stale acceptance criterion 3 as still open; that criterion was satisfied for the paged path by #161 and is restated in spec section 10.
- **No Go change.** `internal/relayclient/page.go` has the identical latent defect and cannot be fixed the same way - Go has no required-field mechanism, so the remedy is a `*string` plus a nil check, a decode-site audit across six `internal/cli` call sites, and a decision about what `internal/mcp`'s `PageEnvelope[map[string]any]` tools do with an absent key. Different shape, different slice. **The item does not exist yet - see below.**
- **No change to `Job`'s defaulted enrichment fields.** Deliberate, pinned by `test_job_authoring_does_not_require_enrichment_fields`, and exempt on the authoring axis. Task 2 makes the axis explicit precisely so nobody "makes these consistent".
- **No model-level empty-page-with-cursor rule.** `_fetch_all` already raises `ProtocolError` for that shape and carries `.records`. Moving it into `Page` would convert a walk stop into a decode error and lose the collected rows, and would break `list_jobs_page`, which makes no walk-level claim.
- **No response-size, byte or wall-clock bound.** Owned by `docs/backlog/bug-2026-08-26-relayclient-has-no-response-bound-and-no-client-timeout.md`.
- **No new `Page` construction API.** Nothing constructs a `Page` in production or test code; if a future caller wants to, that is a new decision.
- **No `_page_response` change.** It already emits all three keys and is why exactly one fixture broke. Leave it.

---

## Backlog items this plan needs FILED (the conductor files these; the planner does not)

1. **`relayclient.PageEnvelope` reads a dropped `next_cursor` as drained, and Go cannot fix it the way Python did.** *Type: bug. Priority: medium.* The exact defect this slice closes on the Python side, verified in the tree: `internal/relayclient/page.go:11-15` declares `NextCursor string` with json tag `next_cursor`, and `FetchAllPages` at `:156` returns on `resp.NextCursor == ""`, so `encoding/json`'s zero value for an absent key reads as drained. Reached by six `internal/cli` call sites including the implicit `resolveWorkerIDIn` walks that `relay workers get|enable|disable|delete <hostname>` make with `userLimit=0`. Go has no required-field mechanism, so the remedy is a `*string` plus an explicit nil check (or a custom `UnmarshalJSON`), and it must decide what `internal/mcp`'s tools - which decode `PageEnvelope[map[string]any]` directly and republish `next_cursor` - do with an absent key. Exposure is lower than Python's: a CLI operator can see a short list and Ctrl-C, where a service loop cannot.

   **This is required scope for the PR, not a nice-to-have.** Spec section 8 declines the Go work *conditioned* on this item existing, and I searched `docs/backlog/` and `docs/backlog/closed/`: it does not. Until it is filed, the spec's Go paragraph is an intention, not a decision, and this PR is shipping a decision that rests on nothing findable.

2. *(No second item.)* Spec section 11's proposal 2 - the stale `test_log_page_requires_next_seq_and_total` docstring - is **closed by Task 2 Step 3** and should not be filed. It was proposed only in case the sweep were descoped, and it is not.

---

## Self-review

**Spec coverage.** Section 4's design -> Task 1 Step 7. Section 5.1 (no model-level empty-page rule) and 5.2 (`total` too) -> "What this plan does NOT do" and Task 1 Step 5/7 respectively. Section 6 (G1, unwrapped `ValidationError` + the advertisement it owes) -> Task 1 Step 8a and Task 2 Step 5. Section 7.1 -> Task 1 Step 5. Section 7.2 -> Task 1 Step 9. Section 7.3 -> verified in the refutations, no action needed. Section 7.4's nine sites -> the Task 2 table, all eleven rows with owners. Section 7.5 -> Task 3. Section 8 (G2) -> "What this plan does NOT do" plus the filing requirement, downgraded per the spec's own test. Section 9.1-9.2 (T1-T4) -> Task 1 Steps 1, 4, 5. Section 9.3 (mutation table) -> Task 5. Section 10's criteria -> Tasks 1-4 and the gates table; the "same commit" clause is restated in the refutations. Section 11 -> the filing list. Section 13's non-goals -> "What this plan does NOT do", all seven carried. Section 14's three questions -> answered above with decisions.

**Placeholder scan.** No TBD, no "add error handling", no "similar to Task N". Every code step carries the literal text to write. Every command carries its expected output.

**Type consistency.** `Page[T]` fields are `items: list[T]`, `next_cursor: str`, `total: int` in Task 1, in Task 5's mutation table, and in every docstring - no drift. Test names are identical everywhere they appear: `test_fetch_all_rejects_a_missing_next_cursor`, `test_fetch_all_rejects_a_missing_total`, `test_get_page_rejects_a_missing_next_cursor`, `test_page_requires_next_cursor_and_total`. The deleted test is named `test_page_defaults_empty_cursor_and_zero_total` at both its mentions. Every import the new tests need (`httpx`, `pytest`, `PydanticValidationError`, `_make_client`, `_job_response`, `Page`, `Job`) already exists at the top of the file it lands in - verified, no new import statement is required in either test file.
