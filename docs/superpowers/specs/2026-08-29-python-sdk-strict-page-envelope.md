# A missing pagination key must not read as "the list ended"

- **Date:** 2026-08-29
- **Type:** Python SDK slice. One production model field change (`python/src/relay/models.py`), plus prose corrections in `client.py`, `models.py`, `README.md`, and two test files.
- **Closes:** `docs/backlog/bug-2026-08-27-python-sdk-page-cursor-defaults-to-drained.md`
- **Blocked on:** nothing. Its sequencing dependency (#161) has merged.
- **Phase:** 1 (design). Phase 2 writes the plan.

This spec was produced by a subagent in autonomous gate mode, so every place the brainstorming flow
would ask a human, one of two things happened. Where the evidence in the tree decides the question,
the call is made here with the reasoning written down. Where a genuine fork exists it is a **GATE
QUESTION** in section 1, with a recommendation the conductor may put to the human.

---

## 1. GATE QUESTIONS

Four. Each has a recommendation and each recommendation is argued in the section named.

**G1. Does the strict envelope surface as a `RelayError` subclass, or does `pydantic.ValidationError`
escape?** **Recommendation: it escapes, unwrapped, exactly as `LogPage`'s does today.** Argued in
section 6. The one-line version: `python/README.md` already documents `pydantic.ValidationError` as
an escape, and `bug-2026-08-27-python-sdk-exceptions-escape-the-relayerror-hierarchy` already owns
the remedy as **one `_read_json` chokepoint over all twelve `response.json()` sites**. Wrapping one
field of one model here would be a partial, divergent implementation of that fix which the chokepoint
then has to unwind. This slice ships no new *class* of escape, only more occasions for an existing
documented one - and section 6 states the cost of that honestly, because it is real.

**G2. Is the Go peer `relayclient.PageEnvelope` fixed in this slice?** **Recommendation: no, and
filed instead.** Argued in section 8. `PageEnvelope.NextCursor` has the identical latent defect (a
missing key leaves Go's zero value, the empty string, which `FetchAllPages` reads as drained), but
Go's `encoding/json` has no required-field mechanism, so the fix is a `*string` field plus an explicit
nil check plus a decision about every one of the six `FetchAllPages` call sites and the MCP tools that
decode the envelope directly. That is a different slice with a different shape, not a mirror of this
one-line change.

**G3. Is `total` made required as well as `next_cursor`?** **Recommendation: yes, both.** Argued in
section 5.2. The existing comment in `client.py` calls a defaulted `total` the safe direction because
"it only makes a reported number smaller" - true of the page-cap message, and **false of the six
`*_page` methods**, whose callers read `page.total` to render a count. Cost of requiring it is zero;
the server emits it unconditionally.

**G4. Version bump.** **Recommendation: `0.2.2`.** No signature change. The only behaviour change is
against a response no correct server produces. Reversible; the plan may prefer `0.3.0` if it wants
the strictness advertised in the version.

---

## 2. What is actually broken, in the tree as it is today

`python/src/relay/models.py`, symbol `Page`:

```python
class Page(BaseModel, Generic[T]):
    items: list[T]
    next_cursor: str = ""
    total: int = 0
```

`items` is required. `next_cursor` and `total` are not. Because the empty string is the SDK's drained
signal - `_fetch_all` stops on `if not cursor` - a body that **omits** `next_cursor` entirely decodes
into a `Page` that reports the list finished. `list_jobs()` against a multi-page list returns page 1,
raises nothing, and the caller has no way to tell a 200-row prefix from a complete 200-row list.

The sibling in the same file, symbol `LogPage`, declares:

```python
    items: list[LogRecord]
    next_seq: int
    total: int
```

Both cursor and total required, no defaults, for exactly this reason - its docstring says so. This
slice makes `Page` match.

**Why the field default, and not a `.get()` call.** Section 3 refutes the item on this point, but it
belongs here too because it is what the implementer will go looking for: there is no
`body.get("next_cursor", "")` anywhere in `python/` at HEAD. #161 deleted both of them and routed the
whole envelope through `Page[model]` in `_get_page`, which `_fetch_all` now calls. The defect survived
that refactor one layer down, as the pydantic field default. Same behaviour, different statement.

**Blast radius, counted.** Twelve public methods over six routes, measured by reading every call site
in `python/src/relay/client.py`:

| Route | `_fetch_all` (walks) | `_get_page` (one page) |
|---|---|---|
| `/v1/jobs` | `list_jobs` | `list_jobs_page` |
| `/v1/scheduled-jobs` | `list_schedules` | `list_schedules_page` |
| `/v1/workers` | `list_workers` | `list_workers_page` |
| `/v1/users` | `list_users` | `list_users_page` |
| `/v1/reservations` | `list_reservations` | `list_reservations_page` |
| `/v1/agent-enrollments` | `list_agent_enrollments` | `list_agent_enrollments_page` |

Plus one internal call: `_fetch_all` reaches `_get_page`, which is why a single model change covers
both entry points. The item's "six routes / twelve methods" is exact.

**Requiring the fields is safe against a correct server, and this is measurable rather than assumed.**
`internal/api/pagination.go` declares the wire envelope as a generic `page[T]` struct whose three
fields carry the json tags `items`, `next_cursor` and `total` with **no `omitempty` on any of them**,
so `encoding/json` emits all three keys on every response including the zero-row one. Every list
handler in `internal/api` writes that same struct: **sixteen** non-test
`writeJSON(w, ..., page[...]{...})` sites - `users.go` 5, `jobs.go` 3, `workers.go` 2,
`scheduled_jobs.go` 2, and one each in `reservations.go`, `agent_enrollments.go`, `invites.go`,
`tokens.go`. There is no second envelope type. This is the same argument
`test_log_page_requires_next_seq_and_total` already makes for `LogPage` ("the handler writes both keys
unconditionally from a map literal, so requiring them costs nothing"), and it holds identically here.

Note in passing: the server has **eight** paged routes, two of which (`invites`, `tokens`) the Python
SDK has no method for. "Six" is a claim about the SDK, and it is exact for the SDK.

---

## 3. What in the backlog item this spec REFUTES or CORRECTS

The item's **diagnosis** is correct and its **remedy** is correct. Two of its three sections describe
a tree that #161 changed underneath it.

1. **REFUTED - the Summary's location.** "`_get_page` and `_fetch_all` both do
   `body.get("next_cursor", "")`." False at HEAD. Neither does; the string `body.get` does not occur
   anywhere in `python/src/relay/`. The one surviving mention is a *stale docstring* in
   `python/tests/unit/test_models.py::test_log_page_requires_next_seq_and_total`, which describes it
   as a live departure. An implementer who greps for the item's own words finds a test comment and
   no code.

2. **REFUTED - acceptance criterion 3, "the `TypeError`/`KeyError` escapes are resolved or explicitly
   deferred to the RelayError item."** Both were **already resolved by #161**, so the criterion is
   stale rather than open. Evidence, by test name, all currently green:
   - Repro case D, `KeyError 'items'`, came from a raw `body["items"]` subscript. That subscript is
     gone; `Page.items` is required. Pinned by `test_fetch_all_rejects_a_missing_items_key` and
     `test_fetch_all_rejects_a_null_items`.
   - Repro case C, `TypeError`, came from an untyped `next_cursor` / `total` reaching `len()`,
     `in seen`, and `>`. Both are typed on the model now. Pinned by
     `test_fetch_all_rejects_a_non_string_next_cursor`, `test_fetch_all_rejects_a_list_next_cursor`,
     and `test_fetch_all_rejects_a_non_integer_total`.

   What *remains* true is the general statement behind those two lines: `pydantic.ValidationError`
   does not descend from `RelayError`. That is
   `bug-2026-08-27-python-sdk-exceptions-escape-the-relayerror-hierarchy`, it is already documented in
   `python/README.md`, and section 6 decides this slice's relationship to it. The criterion is
   restated in section 10, not carried verbatim.

   Note why this refutation matters practically: the paged path's remaining `TypeError` exposure is
   **zero**, but the SDK's is not - `get_tasks(job_id)` still iterates a raw `response.json()` and
   raises `TypeError` on a non-array body, and README already says so. A sweep that reads criterion 3
   as still-open will find that site and pull an unrelated route into this slice.

3. **CORRECTED - "Related" points the fix at `client.py`.** At HEAD the behaviour change is entirely
   in `models.py`, two field declarations. `client.py` needs **comment edits only**, no logic change.
   That collapse is what #161 bought and it should be stated in the plan so nobody re-opens the loop.

4. **CORRECTED - "changes the failure behaviour of twelve methods across six endpoints and needs its
   own RED" (the decline note).** The count is right; the implied size is not, any more. It needed its
   own RED because the fix was a rewrite of hand-picked reads across twelve methods. It still needs
   its own RED, for a different reason: **one existing test asserts the behaviour being removed**
   (section 7), and the removal is retroactive over every fixture in the SDK.

5. **CONFIRMED, checked by symbol, not by line number.** `Page` still carries `next_cursor: str = ""`
   and `total: int = 0`. `LogPage` still declares `next_seq: int` and `total: int` undefaulted. The
   claim "the fix already exists in the same file" is accurate. The claim "it is not a live drift
   TODAY" is accurate - `next_cursor` is the key the server sends.

6. **NOT IN THE ITEM, found while checking it.** Six production comments and three test docstrings
   state the preserved default as current fact and become false the moment the field changes.
   Enumerated with their symbols in section 7.4. This is the repo's dominant defect class and the item
   gives no warning of it, because at filing time the prose it invalidates did not exist - #161 wrote
   most of it.

7. **NOT IN THE ITEM.** The Go peer `relayclient.PageEnvelope` has the same latent defect and cannot
   be fixed the same way. G2 / section 8.

---

## 4. The design

Two lines in `python/src/relay/models.py`:

```python
class Page(BaseModel, Generic[T]):
    model_config = ConfigDict(extra="ignore")

    items: list[T]
    next_cursor: str
    total: int
```

`extra="ignore"` stays: the server may add envelope fields and an SDK that rejects them cannot talk
to a newer server. Strictness here is about **absence of a contract field**, not about presence of an
unknown one, and those are opposite directions.

Nothing in `client.py` changes behaviourally. `_get_page` already calls
`Page.__class_getitem__(model).model_validate(response.json())` on the whole body, so the new
requirement is enforced on every page of every walk, from one place.

**`Page` has no authoring caller.** `Job` keeps its defaulted enrichment fields because
`Job(name="nightly")` is the README's first example and a required `total_tasks` would break every
authoring call site - `test_job_authoring_does_not_require_enrichment_fields` pins that and says "do
not make these consistent". `Page` is in the opposite position: it is response-only, it is never
constructed in `python/src/relay/`, and no test constructs one either. The strict rule "costs nothing
there and everything here" argument applies to `Page` on the *costs nothing* side, which is the same
place `LogPage` sits.

### 5.1 What this does NOT put in the model

An empty `items` alongside a non-empty `next_cursor` is a protocol violation, and `_fetch_all` already
raises `ProtocolError` for it. It must **not** move into `Page`. `list_jobs_page` hands a single page
to a caller who may legitimately hold an empty page with a cursor (the server does not produce one,
but the *page* method makes no walk-level claim), and a model-level rule would convert a walk stop
into a decode error and lose `.records`. Different layer, different owner. Leave it.

### 5.2 Why `total` too (G3)

The existing page-cap comment in `_fetch_all` argues that a defaulted `total` is "deliberately NOT the
same class of default as the next_cursor one ... Here it only makes a reported number smaller." That
is true **of that message** and it is the whole justification on record. It does not cover the six
`*_page` methods, where `page.total` is a public return value a caller renders: a UI showing "200 of
0" or a script computing `total // page_size` gets a wrong number silently, with no walk and no
message involved. The safe-direction argument was scoped to one reader and there are seven.

Requiring it costs nothing (section 2's `omitempty` measurement covers both fields), keeps `Page`
symmetric with `LogPage`, and removes the asymmetry that has now produced wrong prose twice.

---

## 6. G1: `RelayError` versus `pydantic.ValidationError`, decided

**Decision: `pydantic.ValidationError` escapes, unwrapped.**

The question is sharp because the SDK has its own `relay.ValidationError`, which *is* a `RelayError`,
and whose docstring reads "Either local Pydantic validation or a 400 from the server." A reader could
reasonably conclude the SDK already intends to route this.

It does not, today, and the reasons to keep it that way are stronger than the reasons to change it
here:

1. **The contract is already documented, and documented as a gap.** `python/README.md` "Errors" says
   errors from the SDK's own request handling descend from `RelayError`, that response DECODING is the
   exception, and names `pydantic.ValidationError` as one of exactly two escapes. The six `list_*`
   docstrings in `client.py` repeat it per-method - "a page the SDK cannot decode raises
   `pydantic.ValidationError`, and both discard what was collected" - and `task_logs` a seventh time.
   So this slice ships **no new class of failure**. It makes an already-documented one reachable on a
   new input.
2. **The remedy is already specified elsewhere, as a chokepoint.**
   `bug-2026-08-27-python-sdk-exceptions-escape-the-relayerror-hierarchy` proposes one `_read_json`
   covering all twelve `response.json()` sites, explicitly modelled on CLAUDE.md's single-JSON-entry-
   point invariant, and it is the natural home for the response-byte bound too. A local `try/except
   pydantic.ValidationError: raise ProtocolError(...)` inside `_get_page` would make `_get_page` and
   `task_logs_page` raise *different types for the identical defect shape*, and the chokepoint would
   have to unwind it. Fixing one site of a twelve-site cross-cutting problem is how the
   single-entry-point invariant gets bypassed.
3. **`ProtocolError` is the tempting wrong answer.** Its docstring fits the words ("a 200 that is not a
   usable relay response") but not the shape: it is documented as raised by *cursor walks*, it carries
   `.records`, and it has no `.response`. A `list_jobs_page` decode failure is not a walk and has no
   records. Widening `ProtocolError` to mean "decode failure" makes the README's `ProtocolError` row
   false in a second way.

**The cost, stated rather than buried.** A caller writing `except relay.ValidationError` will not
catch this, and the two classes share a name. That trap is real, it is the reason the RelayError item
exists, and this slice makes it more likely to be hit. The mitigation this slice owes is
**advertisement, at the point the failure is READ**: section 7.4 requires the README's Errors section
and the `list_*` docstrings to say that a paged envelope missing `next_cursor` or `total` now raises
`pydantic.ValidationError` rather than reading as drained, and to name the tracked item. Per the
project's rule that a signal discloses its properties where it is read, that sentence is required
scope, not documentation polish.

---

## 7. Retroactive blast radius: every fixture and every claim that moves

A required-field change is retroactive over everything that ever constructed the shape. Enumerated by
reading, and split by what happens to each.

### 7.1 The one test that goes RED and must be REPLACED, not deleted

`python/tests/unit/test_models.py::test_page_defaults_empty_cursor_and_zero_total` calls
`Page[Job].model_validate({"items": []})` and asserts `next_cursor == ""` and `total == 0`. It is a
direct assertion of the behaviour being removed. Replace it with
`test_page_requires_next_cursor_and_total`, parametrized over `["next_cursor", "total"]`, mirroring
`test_log_page_requires_next_seq_and_total` in the same file. Deleting without replacing leaves
`Page`'s field-optionality unasserted at the model level.

### 7.2 The test that stays GREEN while its docstring becomes FALSE

`python/tests/unit/test_client.py::test_fetch_all_rejects_a_missing_items_key`. Its fixture is
`{"next_cursor": "", "total": 0}` with `items` absent, so it still isolates `items` and still
discriminates. Its docstring says: "`items` has NO default, unlike `next_cursor` and `total`, whose
defaults are deliberate and preserved - so an absent `items` is an error while an absent `next_cursor`
still reads as drained." Every clause after the comma is false after this slice.

### 7.3 Fixtures that survive, and why that is worth writing down

`python/tests/unit/test_client.py` has one page-envelope helper, `_page_response(items, *,
next_cursor="", total=None)`, which **always emits all three keys**. Every hand-written envelope
literal in the file (measured: the bodies at the `next_cursor: 12345`, `next_cursor: ["x"]`,
`total: bad_total`, and `items: None` tests, plus the worker-page body) also carries all three. So
exactly **one** fixture in the SDK breaks. This is the good outcome and it is not luck - the file's
own comment block forbids building fixtures by dumping `Page[Job]`, which is why they are complete
literals rather than model dumps.

`python/tests/integration/test_smoke.py` constructs no envelopes; it drives a live server.

### 7.4 Nine prose sites that state the old behaviour as current fact

The repo's dominant defect is wrong prose about correct code. These are named by symbol so the plan
does not have to rediscover them, and the greps that find them are given in section 10.

Production, `python/src/relay/models.py`:
- **`Page` docstring** - add the required-and-undefaulted rationale, mirroring `LogPage`'s.
- **`LogPage` docstring** - currently reads "`next_seq` and `total` are REQUIRED, unlike
  :class:`Page`, which declares `next_cursor: str = ""` and so reads a missing key as the empty
  string." The comparison inverts. Rewrite it as agreement, and keep the *reason*, which is still the
  best statement of the defect class in the codebase.
- **`Job`'s list-only enrichment comment** - "the strict no-default rule LogPage follows does not
  apply here". Should name both models, since `Page` is now the closer analogue (response-only) and
  `Job`'s exemption is on the authoring axis.

Production, `python/src/relay/client.py`:
- **`_get_page`** - the paragraph beginning "`next_cursor: str = \"\"` and `total: int = 0` keep their
  DEFAULTS", which also names the item this spec closes.
- **`_fetch_all`, above `cursor = page.next_cursor`** - "NOT changed by this slice: a MISSING
  next_cursor key still reads as drained here", which also names the item.
- **`_fetch_all`, the page-cap `total = page.total` comment** - "A MISSING total still defaults to 0,
  via Page's own default - deliberately NOT the same class of default as the next_cursor one above."
  After the change a missing `total` cannot reach this line at all; it raised on request 1. The
  *distinction* the comment draws is still worth keeping in some form (see section 5.2), but not as a
  statement about defaults that no longer exist.

Tests:
- `test_client.py::test_fetch_all_rejects_a_missing_items_key` - section 7.2.
- `test_models.py::test_log_page_requires_next_seq_and_total` - "A deliberate departure from
  `_get_page`'s `body.get(\"next_cursor\", \"\")`." Stale since #161; after this slice it is not a
  departure at all.
- `test_models.py::test_job_authoring_does_not_require_enrichment_fields` - "the strict no-default
  rule LogPage follows"; same edit as the `Job` comment above.

`python/README.md`:
- The **Errors** section gains the advertisement required by section 6. The existing two-escape
  enumeration stays accurate and must not be re-counted.
- The **pagination** paragraph ends "A list with no matching rows is not an error - it answers
  `items: []` with an empty cursor, which is the drained signal". Still true. The plan should confirm
  no README sentence describes the keys as optional; a read of the file found none, and that is a
  claim about the file the plan should re-check rather than inherit.

### 7.5 One existing test acquires a new job

`python/tests/integration/test_smoke.py` gets a new property for free and should say so, because the
project's recurring miss is a slice that gives existing code a new job and pins nothing.
`test_list_jobs_includes_recent_submission` (a non-empty single page) and
`test_a_list_with_no_matching_rows_returns_empty_and_does_not_raise` (a zero-row page) are, after this
slice, **the only live-server proof that `buildPage` actually emits both keys** on each of those two
shapes. Under the old model they proved only that the cursor was empty-or-absent; now they prove
present-and-empty. No new integration test is needed. Add the note to the zero-row test, which is the
shape where an `omitempty` would most plausibly have bitten. The lane is manual and not in CI, so this
is a note, not a gate.

---

## 8. G2: the Go peer, declined in writing and filed

`internal/relayclient/page.go` declares `PageEnvelope[T]` with a `NextCursor string` field tagged
`next_cursor`. Go's `encoding/json` leaves a missing key at the zero value, so `FetchAllPages`'s
`if resp.NextCursor == "" { return out, total, nil }` reads a dropped key as drained - **the identical
defect, in the identical position**, reached by six `internal/cli` call sites (including the implicit
walks through `resolveWorkerIDIn`) and by the CLI's list commands.

It is not fixed here, and the reason is not scope hygiene:

- **Go has no required-field mechanism.** The fix is `NextCursor *string` plus an explicit nil check,
  or a custom `UnmarshalJSON`, or a post-decode presence assertion. Each changes the *type* the six
  call sites read, and `internal/mcp` decodes `PageEnvelope[map[string]any]` directly and hands
  `next_cursor` to its caller, so the field's optionality is part of an MCP tool's own output shape.
- **So the diff is not a mirror of this one.** This slice is two field declarations and nine prose
  corrections. The Go one is a type change with a decode-site audit and an MCP output-contract
  decision. Folding them makes the diff two features, which is the same reason #161 declined this one.
- **The exposure differs.** A CLI operator can Ctrl-C and can see a short list; a Python service loop
  consuming `list_workers()` cannot.

Filed as a proposal in section 11. Per the project rule that a decision conditioned on future work
needs a findable item, **the plan must confirm that item exists before this spec's Go paragraph counts
as a decision rather than an intention.**

---

## 9. RED-first test design

The house rules this obeys: a test must be RED *for the reason the fix addresses*; a green test can be
vacuous; and every fixture in `test_client.py` carries an HTTP 500 terminator past the expected
request count, because the project has no `pytest-timeout` and a mutant that keeps walking would hang
instead of failing.

### 9.1 The headline test

**`test_fetch_all_rejects_a_missing_next_cursor`**, in `python/tests/unit/test_client.py`.

Handler: request 1 returns a hand-written literal `{"items": [_job_response(id="j1")], "total": 99}` -
**`next_cursor` absent, `total` present**. Request 2 and beyond returns HTTP 500.

Assert: `pytest.raises(PydanticValidationError)` from `client.list_jobs()`.

- **At HEAD it is RED**, and red for the right reason: `list_jobs()` returns `[Job(id="j1")]` after one
  request and pytest reports `DID NOT RAISE`. The failure is the silent-truncation behaviour itself,
  not a side effect of it.
- **Reverting `next_cursor: str` to `next_cursor: str = ""` restores exactly that failure.** Nothing
  else in the fix can make it pass.
- **`total` must be present in this fixture.** If both keys were omitted, reverting only the `total`
  default would leave this test green, and the two mutations would not be separable. Each required
  field needs a fixture that omits **only** it.
- **A request-count assertion here is not evidence and should be labelled as such if written at all.**
  The correct code raises on request 1 and the unfixed code stops after request 1, so `len(calls) == 1`
  holds under both. It documents; it does not discriminate. The terminator is still required by the
  file's convention - it costs nothing and guards a mutant that continues walking.

### 9.2 The rest

- **T2 `test_fetch_all_rejects_a_missing_total`.** Literal `{"items": [...], "next_cursor": ""}` -
  `total` absent, cursor present and **drained**, so a walk that ignores the missing `total` terminates
  normally with one row. At HEAD: returns `[Job]`, no raise, RED. Reverting `total: int` to
  `total: int = 0`: RED again. Isolates `total` from `next_cursor` in both directions.
- **T3 `test_get_page_rejects_a_missing_next_cursor`.** Same body, but through `list_jobs_page`. Pins
  the six one-page methods as wired rather than assumed. Today they share `_get_page` and so share the
  model, but nothing structural forbids a future lenient path in `_get_page`, and "wired, not just the
  helper" is the pattern the `_fetch_all` slice used for the same reason.
- **T4 `test_page_requires_next_cursor_and_total`** in `test_models.py`, parametrized over the two
  field names, deleting one key from `{"items": [], "next_cursor": "c", "total": 5}` per case.
  Replaces `test_page_defaults_empty_cursor_and_zero_total`. This is the model-level statement; T1-T3
  are the client-level ones, and both layers are wanted because the model is the pin and the client is
  the consumer.
- **No new integration test.** Section 7.5.

### 9.3 Mutation kill table

Each row must have a non-empty RED set, and each field must own at least one test the other does not
redden.

| # | Mutation | Expected RED |
|---|---|---|
| M1 | `next_cursor: str` -> `next_cursor: str = ""` | T1, T3, T4[next_cursor] - **and not T2** |
| M2 | `total: int` -> `total: int = 0` | T2, T4[total] - **and not T1, not T3** |
| M3 | both restored (the HEAD state) | T1, T2, T3, T4 both cases |
| M4 (control) | `items: list[T]` -> `items: list[T] = []` | `test_fetch_all_rejects_a_missing_items_key` must die. If it survives, the harness did not apply the mutation |

M4 is a control, not a coverage claim. The project has had four mutations in a row silently fail to
apply under CRLF and report "survived"; a mutation battery with uniform results means a broken
harness, so one mutation that **must** die is run first.

The M1/M2 asymmetry is the point of the table. A single fixture omitting both keys would make M1 and
M2 indistinguishable and the table would be vacuous while looking complete.

---

## 10. Acceptance criteria

Carried from the backlog item, corrected where the item's wording is false against this design. The
project rule is that a criterion false against its own design produces a test that fails on correct
code, so the changes are marked.

Carried unchanged:

- A `Page` whose body omits `next_cursor`, or omits `total`, **raises** rather than defaulting.

**Restated** (the item's version names a statement that no longer exists):

- ~~"Reverting to `body.get(...)` while keeping the new fixtures turns a test RED."~~ ->
  **Reverting `next_cursor: str` to `next_cursor: str = ""`, or `total: int` to `total: int = 0`,
  each turns a DIFFERENT named test RED, per section 9.3.**

**Dropped as already satisfied** (#161, not this slice - see section 3.2):

- ~~"`Page` validates the whole envelope through the model."~~ True at HEAD. It is this slice's
  precondition, not its criterion.
- ~~"The `TypeError`/`KeyError` escapes are resolved or explicitly deferred."~~ Resolved for the paged
  path by #161 and pinned by five named tests. The residual `get_tasks` `TypeError` is a different
  route and is out of scope.

Added by this spec:

- The one test asserting the removed behaviour is **replaced**, not deleted, by its inverse.
- Every one of the nine prose sites in section 7.4 is corrected in the same commit as the field change.
  After the change, none of these patterns hits a site in `python/` that states the old behaviour as
  current fact: `body.get`, `keep their DEFAULTS`, `Page's own default`, `unlike :class:`,
  `still reads as drained`, `no-default rule`, and the literal string
  `bug-2026-08-27-python-sdk-page-cursor-defaults-to-drained`. (Measured at HEAD, those patterns hit
  `client.py` at the `_get_page`, `_fetch_all` cursor, and page-cap comments; `models.py` at `LogPage`
  and `Job`; `test_models.py` twice; `test_client.py` once. Note the grep is the *instrument*, not the
  claim - a pattern list cannot establish that the prose is now correct, only that these known-wrong
  sentences are gone. A reader must confirm the replacements.)
- `python/README.md`'s Errors section states that a paged envelope missing `next_cursor` or `total`
  raises `pydantic.ValidationError`, which is not a `RelayError`, and names
  `bug-2026-08-27-python-sdk-exceptions-escape-the-relayerror-hierarchy`. Section 6 argues this is
  required scope.
- The Go decision is explicit: a backlog item for `relayclient.PageEnvelope` exists and is linked, or
  the Go paragraph in section 8 is downgraded to an intention.
- `_version.py` and `pyproject.toml` move together to `0.2.2` (`test_version_files_are_in_lockstep`
  makes moving one of them RED).
- Gates: `pytest tests/unit` on **3.9 through 3.13** (the CI matrix in `.github/workflows/python.yml`),
  `ruff check src tests`, `mypy src`, and `test_packaging.py`'s README guard. No Go gate is needed -
  this slice touches no Go file.

---

## 11. Proposed backlog items

Proposals only. Not filed. The human accepts.

1. **`relayclient.PageEnvelope` reads a dropped `next_cursor` as drained, and Go cannot fix it the way
   Python did.** The exact defect this spec closes on the Python side, in
   `internal/relayclient/page.go`, reached by six `internal/cli` call sites including the implicit
   `resolveWorkerIDIn` walks that `relay workers get|enable|disable|delete <hostname>` make with
   `userLimit=0`. Go's `encoding/json` has no required-field mechanism, so the remedy is a `*string`
   field plus an explicit nil check (or a custom `UnmarshalJSON`), and it has to decide what
   `internal/mcp`'s tools - which decode `PageEnvelope[map[string]any]` directly and republish
   `next_cursor` - do with an absent key. *(Type: bug. Priority: medium. The exposure is real but an
   operator at a terminal sees a short list, where a Python service loop does not.)*

2. **`test_log_page_requires_next_seq_and_total`'s docstring has been stale since #161 and this is the
   second slice to walk past it.** It describes `_get_page`'s `body.get("next_cursor", "")` as live.
   This spec's section 7.4 corrects it as part of the sweep, so filing is only warranted if the sweep
   is descoped. Recorded here so the decision is visible either way. *(Type: chore. Priority: low.
   Likely closed by this slice.)*

---

## 12. Load, failure modes, threat model

**Load.** Zero cost. Two fewer field defaults to apply; no new allocation, no new request, no new
round trip. The validation already runs on every page of every walk.

**Failure mode before.** Silent truncation to one page, reported as success. The dangerous consumers
are reconciliation loops, which is most of what an SDK list call is for: a script that calls
`list_workers()` to find agents to disable, or `list_jobs()` to find stragglers, sees at most 200 rows
and treats the absent remainder as *not existing*. Nothing in the return value distinguishes a
200-row prefix from a complete 200-row list.

**Failure mode after.** `pydantic.ValidationError` on the first page. No rows, no `.records`, no
partial result. **Fail-closed, and the availability cost is real**: a server-side regression that
drops the key turns a degraded-but-running SDK into a hard-down one, and this spec should not pretend
otherwise. The judgement is that the degraded state was actively wrong rather than merely reduced -
every consumer of a paginated list treats it as complete, because there is no other thing to treat it
as - and a caller who wants the old behaviour can catch the exception and decide, which is not
available in the other direction.

**Threat model.** The envelope is entirely server-supplied, and the SDK points at whatever `RELAY_URL`
says, including a proxy, a mirror, or a development server. Today an actor who can shape one field of
the envelope has a **silent truncation primitive**: drop `next_cursor` and every SDK list read in the
process is capped at 200 rows with no signal at any layer. Making the field required removes that
primitive. This is the strongest argument for the slice and the item does not make it.

**What it does not close, stated so nobody thinks it does.** The same actor can still send
`next_cursor: ""` with a truncated `items`, which is byte-identical to a genuinely drained list. No
client-side check can distinguish those, and this slice does not try. What changes is that truncation
now requires the server to *lie* rather than to *omit*, and an omission is the failure mode a schema
drift or a proxy actually produces.

**Invariants.** No backend invariant is touched - this is client-side decode policy. The relevant one
by analogy is **single JSON entry point**: this slice moves the envelope's decode policy further into
the one model that owns it, rather than spreading it across call sites. That is the direction the
invariant wants. The one invariant-adjacent risk is section 6's: a local `try/except` wrap would have
put decode policy back at a call site, and it was declined for that reason.

---

## 13. Explicit non-goals

What this slice does NOT do, and who owns each instead.

- **No `_read_json` chokepoint, and no wrapping of decode failures in `RelayError`.** Owned by
  `bug-2026-08-27-python-sdk-exceptions-escape-the-relayerror-hierarchy`. Section 6.
- **No change to `get_tasks`'s raw-list iteration**, which is the SDK's one remaining `TypeError`
  escape. Different route, not paged, already documented in README, owned by the item above.
- **No Go change.** Section 8, proposed as item 1 in section 11.
- **No change to `Job`'s defaulted enrichment fields.** Deliberate and pinned; `Job` is the authoring
  model. Section 4.
- **No model-level empty-page-with-cursor rule.** Section 5.1.
- **No response-size or wall-clock bound.** Owned by
  `bug-2026-08-26-relayclient-has-no-response-bound-and-no-client-timeout`.
- **No new `Page` construction API.** `Page` is never constructed in production or test code today; if
  a future caller wants to build one, that is a new decision, not a consequence of this one.

---

## 14. Open questions the plan inherits

1. **The exact replacement wording for `LogPage`'s cross-reference.** The current docstring is the
   best statement of this defect class in the codebase and it is phrased as a contrast with `Page`.
   Rewriting it as agreement risks losing the *reason*. The plan should preserve the reasoning and
   move the contrast to a model that still has a default - `Job`'s enrichment fields are the live
   example, and they are exempt on a different axis (authoring), so the plan must say *which* axis or
   it will write a third generation of wrong prose about this exact pair.
2. **Whether the page-cap comment keeps a `total`-versus-`next_cursor` distinction at all.** After the
   change a missing `total` cannot reach that line. Section 5.2's argument (the distinction was scoped
   to one reader and there are seven) is the reason the fields are treated alike; the plan decides
   whether that belongs in the comment or only in this spec.
3. **`0.2.2` versus `0.3.0`.** G4 recommends `0.2.2`; the plan may prefer to advertise the strictness.
