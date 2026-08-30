# A server-supplied cursor must not be able to drive a client loop forever

- **Date:** 2026-08-28
- **Type:** Python SDK slice (`python/src/relay/client.py`, `errors.py`) plus one Go leaf-package sub-slice (`internal/relayclient/page.go`)
- **Closes:** `docs/backlog/bug-2026-08-27-python-sdk-fetch-all-has-no-termination-stops.md`
- **Blocked on:** nothing.
- **Phase:** 1 (design). Phase 2 writes the plan.

This spec was produced by a subagent in autonomous gate mode, so every place the brainstorming flow
would ask a human, one of two things happened. Where the evidence in the tree decides the question,
the call is made here with the reasoning written down. Where a genuine fork exists, it is a **GATE
QUESTION** in section 1 with a recommendation, and the conductor may put it to the human.

The Go question (G1) was explicitly delegated to this spec by the user, so its argument is the
spec's most load-bearing output and is given a section of its own (section 8).

---

## 1. GATE QUESTIONS

Four. Each has a recommendation, and each recommendation is argued in the section named.

**G1. Is the Go peer `FetchAllPages` fixed in this slice, or declined in writing?**
**Recommendation: FIXED, as a second sub-slice sequenced after the Python one so it can be dropped
without unwinding anything.** Argued in full in section 8. The one-line version: the Go exposure is
*wider* than the item states - six commands walk a list *implicitly* through `resolveWorkerIDIn` with
`userLimit=0`, so `relay workers get <hostname>` hangs, not just `relay workers list` - and the fix
lands in one leaf package with six callers whose error plumbing already exists and a default-lane
`httptest` test file that needs no Docker. Declining costs more than fixing.

**G2. Non-advancing predicate: a set of seen cursors, or a comparison against the previous one?**
**Recommendation: a set.** The stop is "the walk revisited a cursor"; previous-cursor comparison is
a strictly weaker *implementation* of that same stop, not a cheaper stop. Argued in section 5.1.

**G3. Does the Python page-cap message compute a distinct-row count, the way `task_logs` counts
distinct `seq`?** **Recommendation: yes, computed inside the raising block from
`getattr(m, "id", None)`, with a third message arm for the case where no row carried an id.** But
the honest half matters more than the yes: `Job.id` is `Optional[str] = None` (because `Job` doubles
as the authoring model), so unlike `LogRecord.seq` the count is **not** backed by a required field
for the most-used of the six types. The failure direction is under-count, which is safe. Argued in
section 5.4. **The Go half cannot compute this count at all and must not claim it** - see 8.4.

**G4. Version bump.** **Recommendation: `0.2.1`.** No signature changes; the only new behaviour
replaces a hang, which nobody can have depended on. Reversible; the plan may prefer `0.3.0`.

---

## 2. What is actually broken

`Client._fetch_all` (`python/src/relay/client.py`) ends its loop body with:

```python
            cursor = body.get("next_cursor", "")
            if not cursor:
                return out
```

That is the server's own drained signal and nothing else. There is no empty-page stop, no
non-advancing-cursor stop, and no page cap. A server answering `next_cursor: "SAME"` forever drives
an unbounded number of requests, and `out` grows without bound for as long as that server also
returns items.

**Six public methods reach it.** Verified by a repository-wide search for the literal `_fetch_all`
under `python/`: one definition, six call sites - `list_jobs`, `list_schedules`, `list_workers`,
`list_users`, `list_reservations`, `list_agent_enrollments` - and one mention in a test docstring.
The item's count of six is exact.

**Nothing outside the SDK bounds it.** `httpx`'s `timeout` is per socket read, not per request
(measured last session at 14.3 s under a 0.5 s read timeout), and there is no total-time setting in
httpx at all. The comment above `_MAX_LOG_PAGES` in this same file already records that and records
why `Client(timeout=)` is not the remedy. A Python program calling `list_jobs()` inside a service
loop has no operator at a keyboard to interrupt it.

The Go peer `relayclient.FetchAllPages` has the same three-stop gap in the same shape. Section 8
measures its blast radius; it is not identical to Python's.

---

## 3. What in the backlog item this spec REFUTES or CORRECTS

The item's diagnosis is correct in every particular I could check. Its **prescriptions** are where it
needs correcting, which is the shape [[reference_accurate_item_wrong_remedy]] describes.

1. **"The reasoning is already written down ... It applies verbatim next door." - half right, and the
   half that is wrong is the design work.** The *justification* ports verbatim: the provenance of a
   value says nothing about who controls its content. The *predicates* do not port at all.
   `task_logs` compares `next_seq <= since`, which works only because the log cursor is a monotone
   integer; `_fetch_all`'s cursor is an opaque base64 string with no order. Reading the item as
   "copy the three stops" under-specifies exactly the two places real decisions are needed
   (sections 5.1 and 5.4).

2. **"`task_logs`'s `_MAX_LOG_PAGES` is the shape to copy" - true for the cap, false for the
   completeness arm.** That arm's key is `LogRecord.seq`, and the file's own comment says the
   required-and-undefaulted declaration is what makes the count honest. There is no equivalent for
   `_fetch_all`: `M` is bound only to `BaseModel`, and of the six concrete models, five declare
   `id: str` (required) but **`Job` declares `id: Optional[str] = None`**, because `Job` is the
   authoring model too and `Job(name="nightly")` must keep working. The one type where the count is
   weakest is the one behind the most-used method. Section 5.4 says what to do about it.

3. **"The Go peer ... has the identical gap, so this is a cross-language hole ... Fixing one
   language and not the other leaves the CLI exposed." - the gap is identical, the EXPOSURE is
   not, in three ways the item does not state.**
   - **Wider than "the CLI".** Six commands walk a list the operator never asked to see:
     `relay workers get|enable|disable|delete <hostname>` and `relay workers workspaces` (two call
     sites) all go through `resolveWorkerID` -> `resolveWorkerIDIn`, which calls
     `FetchAllPages[workerResp](..., nil, 0)` - `userLimit=0`, so the caller's own `--limit` cannot
     short-circuit it, and on a hostname miss it walks a **second** path (`/v1/workers/revoked`)
     too.
   - **`internal/mcp` is NOT exposed.** Every MCP list tool decodes one
     `relayclient.PageEnvelope[map[string]any]` from a single `c.Do` and hands `next_cursor` to its
     caller; none of them calls `FetchAllPages`. Verified by repository-wide search for the symbol:
     six production call sites, all in `internal/cli`, plus three in `page_test.go`.
   - **Severity is lower than Python's for one reason the item does not weigh.** `cmd/relay/main.go`
     builds its context with `signal.NotifyContext(context.Background(), SIGINT, SIGTERM)` and no
     deadline, and `relayclient.NewClient` returns `&http.Client{}` with no `Timeout`, so "forever"
     is literal - but an operator at a terminal can Ctrl-C, and a Python daemon cannot. This is the
     strongest argument *against* G1 and it is stated here rather than buried.

4. **The repro is one of two variants and they terminate at different stops.** A mock returning
   `next_cursor: "SAME"` *with items* is caught by the cursor stop on request 2. A mock returning
   `"SAME"` with `items: []` is caught by the **empty-page** stop on request 1. Not a refutation -
   a note for whoever writes the RED, because the two are one character apart in the fixture and
   pin different code.

5. **Not in the item, found while checking it: `printTaskLogs`'s own completeness arm rests on a
   non-distinct count.** `logProgress.rows` is documented as "how many rows were written", and the
   Go cap message says "the server reported %d rows and every one printed" when
   `progress.rows >= progress.total`. That is precisely the `len(out)` weakness the Python port was
   refuted for last session. It is **out of scope here** (different function, different package,
   and a distinct-seq set would break that loop's deliberate O(one page) memory) but it is real, and
   there is a cheap O(1) remedy. Proposed as a backlog item in section 11.

---

## 4. The design: one loop, six checks, in this order

Both languages get the same order. Reading it top to bottom is the whole design.

```
loop:
  1. request the page
  2. append its items to `out`
  3. if the caller's `limit` is now satisfied      -> RETURN out[:limit]     (not an error)
  4. read `next_cursor`
  5. if it is empty                                -> RETURN out            (drained; not an error)
  6. if this page carried no items                 -> RAISE  empty page
  7. if this cursor was already requested          -> RAISE  cursor repeat
  8. if the page count has reached the cap         -> RAISE  page cap
  9. record the cursor as seen; continue
```

Membership (7) is tested **before** the cursor is recorded (9), so the self-loop case fires on
request 2 and an `A,B,A` cycle fires on request 3.

Four orderings in that list are load-bearing and each is pinned by its own test (section 9):

- **3 above everything.** A caller who asked for 50 rows and got 50 rows must receive them, even if
  the page that completed the order was also the page that repeated a cursor. This is question 6
  from the brief and it is answered in section 5.6.
- **5 above 6.** Zero matching rows is a *legitimate* response: `items: []` with `next_cursor: ""`.
  Testing emptiness first turns `list_jobs()` on an empty table into a `ProtocolError`. This is the
  same ordering `task_logs` needs and for a related but not identical reason (section 5.2).
- **6 above 7.** When both are true, the empty-page message is the more specific diagnosis, and it
  matches `task_logs`'s order.
- **8 last.** The cap is the backstop for the case neither of the other two can see: an
  ever-advancing, never-repeating cursor that never drains.

---

## 5. The design questions, settled

### 5.1 The non-advancing predicate for an opaque string cursor (G2)

**The stop is: the walk requested a cursor it has already requested.** Implementation: a
`set[str]` in Python, `map[string]struct{}` in Go, holding every non-empty `next_cursor` the loop has
accepted. The empty cursor never enters the set because step 5 returns first.

**First, establish there is no legitimate repeat**, because a false positive here breaks real users.
The server's cursor (`encodeCursorV2` in `internal/api/pagination.go`) is
`base64url(JSON{sort, value, id})` of the **last kept row** of the page, and the next page's
predicate is strictly past that key with `id` as a unique tiebreaker. So page N+1 cannot contain the
row page N's cursor names, and therefore cannot re-emit that cursor. This survives concurrent
mutation: a row whose sort value moves can reappear in a later page, but its key - and hence its
cursor - has changed, so the string differs. A legacy (no-`s`-field) cursor and a v2 cursor for the
same row are different strings, so they cannot collide either. **A repeated cursor is unreachable on
a correct server.** Comparison is byte-exact on the base64 string; the SDK never decodes it.

**Why a set rather than a comparison against the immediately previous cursor.** These catch different
things and the difference is not academic:

| | self-loop (`A,A,A...`) | cycle of length 2 (`A,B,A,B...`) |
|---|---|---|
| previous-cursor comparison | fires on request 2 | never fires; runs to the page cap |
| seen-set | fires on request 2 | fires on request 3 |

A length-2 cycle is not an exotic adversarial construction. Two replicas behind a load balancer with
different data, or a caching proxy alternating two cached bodies, produce exactly `A,B,A,B`. Under
previous-cursor-only, that walk makes 10,000 requests and retains up to 2,000,000 rows before
reporting "hit the client's page cap" - and the comment above `_MAX_LOG_PAGES` in this very file
already says, with measurements, that a request cap bounds requests and **not** wall clock, bytes, or
memory. Choosing the weaker predicate means accepting exactly the exposure that comment warns about,
for a failure mode that has an ordinary cause.

The decisive framing: **this is not a second stop, it is a choice of implementation for one stop.**
Previous-cursor-only is the set restricted to its last element. Adopting the set adds no branch, no
message, and no test that the weaker version would not also need - it adds one container.

**Memory, stated honestly.** The set holds at most one entry per page, so its entry count is bounded
by the page cap. Its *byte* cost is (entries) x (cursor length), and cursor length is
server-supplied and unbounded. Against a real server that term is ~128 bytes per entry against
~100 KB of models per page - roughly 0.1% of the walk. The pathological case is a server sending
one-item pages with multi-megabyte cursors, where the set becomes the dominant retention and `out`
does not.

**A 32-byte digest per entry would close that term, and it is DECLINED.** Three reasons, ranked:
(i) in Go it collides with CLAUDE.md's stated invariant that all hashing goes through
`internal/tokenhash.Hash` and `sha256.Sum256` is never inlined at a new site - buying a memory bound
by carving an exception out of an invariant whose whole point is a single entry point is the wrong
trade in this codebase, and doing it in Python only would leave two implementations of one design;
(ii) the residual is a strictly-malicious server pointed at deliberately, which is the precondition
of the **open** item `bug-2026-08-26-relayclient-has-no-response-bound-and-no-client-timeout`, and
that item owns the unbounded-response-bytes axis at the right layer - closing a sliver of it inside
one loop in one language is the fix-at-the-call-site mistake it argues against; (iii) the same
attacker already has an equal retention channel through `items`.
**So: name the residual in the comment, with the arithmetic, and cross-reference that item.** That is
the house pattern `_MAX_LOG_PAGES` already uses - bound what you can, name what you do not.

**Also declined: decoding the cursor to recover an order.** The server treats the cursor as opaque
and versioned (`decodeCursor` handles legacy cursors with no `s` field, and its comment forbids
echoing decoded bytes). An SDK that decodes it takes on a server-internal encoding as a cross-language
contract to keep in step, to buy a stop the set already provides.

**Also declined: Floyd/Brent cycle detection.** O(1) memory, but it requires re-traversal, and here
traversal costs a network round trip and appends rows. Cycle detection that re-walks is more
expensive than the thing it saves.

### 5.2 What the empty-page stop means here (Q2)

**On a correct server, an empty page with a non-empty cursor is unreachable.** `buildPage`
(`internal/api/pagination.go`) returns `([]Out{}, "")` for zero rows, and emits a non-empty cursor
only when `len(rows) > limit`, in which case `items` holds exactly `limit >= 1` rows. So
`next_cursor != "" && len(items) == 0` cannot be produced by it.

Instrument and count, since that is a claim about a complement: 16 `writeJSON(..., page[T]{...})`
sites across 8 files in `internal/api` (`agent_enrollments` 1, `jobs` 3, `invites` 1,
`reservations` 1, `scheduled_jobs` 2, `tokens` 1, `users` 5, `workers` 2). Every one takes
`(items, next)` from `buildPage` except the three literals in `handleListUsers`'s `?email=` branch,
all of which set `NextCursor: ""` explicitly. The six routes this SDK walks are covered by that set.

**The ordering rationale therefore differs from `task_logs`'s, and the spec should say so rather
than copy the comment.** In `task_logs`, an empty page is *legitimate* when the log length is an
exact multiple of the page size, and it reports itself drained - the drained check must run first or
a correct server is accused. In `_fetch_all`, the legitimate empty page is the *zero-matching-rows*
response, and it too reports itself drained (`next_cursor: ""`). Different situation, same
conclusion, and the same catastrophic failure if inverted: with the empty-page stop first,
`list_jobs()` against an empty jobs table raises. That inversion is a one-line mutation and it gets
its own test (T4, and T11 against a real server).

The stop stays even though the server cannot reach it, for the reason the whole item exists: the
loop is driven by a value the client does not control, and "no correct server does this" is a
statement about correct servers.

One implementation note: the emptiness test must read the raw `body["items"]` list bound to a local,
not `len(out)`, because `out` is cumulative. (`body["items"]` is never JSON `null` on these six
routes - `buildPage` returns a non-nil empty slice - so no null-coercion is needed here.)

### 5.3 The page cap (Q3)

**`_MAX_LIST_PAGES = 10000`, a class attribute on `Client`, read off `self` inside the loop.**

- **Name** parallels `_MAX_LOG_PAGES` and says what it bounds. It stays a *separate* constant rather
  than a shared `_MAX_PAGES`: the two loops bound different populations, and the 40-line comment
  above `_MAX_LOG_PAGES` carries log-specific measurements that would become wrong if the constant
  were shared.
- **Value.** `_fetch_all` already requests `_PAGE_REQUEST_LIMIT = 200` per page, so the same
  arithmetic applies: 10,000 x 200 = 2,000,000 rows. A jobs table on a long-lived farm can plausibly
  reach that, and a cap that truncates a legitimate list is worse than the hang it prevents is
  frequent, so this is the wrong place to be clever with a smaller number. The public, caller-chosen
  bound on rows is `limit=`, exactly as the `_MAX_LOG_PAGES` comment argues when it declines a
  private row budget.
- **Class attribute**, per the item, so `monkeypatch.setattr(Client, "_MAX_LIST_PAGES", 2)` works -
  which means the loop must read `self._MAX_LIST_PAGES`, never a module global. This is the same
  technique the existing cap tests use.
- **What it bounds is requests.** The comment must say so in one line and point at the
  `_MAX_LOG_PAGES` block for the wall-clock/bytes/memory measurements rather than restating them.
  Do **not** re-derive or paraphrase those numbers; a second copy is a second thing that can go
  stale, and wrong prose about correct code is this project's dominant defect class.

### 5.4 The "do not blame the server" arm (G3, Q4)

`task_logs` counts `len({r.seq for r in out})` because a server that re-serves a page behind an
*advancing* cursor drives `len(out)` to `total` while half the log was never sent - and the new
cursor-repeat stop does **not** catch that, because the cursor genuinely advances. So the threat is
live here too and the arm is needed.

**What is computable, precisely.**

- Of the six models, five declare `id: str`, required and undefaulted: `ScheduledJob`, `Worker`,
  `Reservation`, `AgentEnrollment`, `User`. **`Job` declares `id: Optional[str] = None`**, and the
  comment on its list-enrichment fields records why: `Job` is the authoring model, so
  `Job(name="nightly")` must construct without an id.
- All six server responses carry a non-`omitempty` `"id"` on every item: `jobResponse.ID`,
  `scheduledJobResponse.ID`, `workerResponse.ID`, `userResponse.ID`, `reservationResponse.ID`, and
  the `"id"` key that every `agent_enrollments.go` row-to-map converter writes. So against a correct
  server the count is exact for all six.
- `M` is bound only to `BaseModel`, so nothing structural guarantees an `id` at all. The accessor
  must be `getattr(m, "id", None)` with `None` dropped.

**The failure direction is under-count, and under-count is safe.** A `Job` page with ids omitted
yields `None` for every row, which is dropped, so `distinct` falls below `total`, so the code takes
the *blaming* arm. It can never over-count: the set holds at most one entry per row received.

**Cost.** Built once, inside a block every path of which raises - exactly as `task_logs` does, and
for the same reason. The common path (a walk that finishes) pays nothing. Do **not** accumulate the
id set per page; that would put a string per row on 100% of walks to serve a message that fires at
2,000,000 rows.

**Three message arms, not two.** This is the part most likely to be silently weakened, so the arms
are given explicitly.

Let `distinct = len({i for i in (getattr(m, "id", None) for m in out) if i is not None})` and
`total = body.get("total", 0)` from the **current** page (matching `task_logs`, which uses
`page.total`; a missing `total` defaults to 0, which fails into the blaming arm - safe, and
deliberately not the same class of default as the `next_cursor` one, see section 10).

1. **`out` is non-empty and `distinct == 0`** - the rows carried no usable id. Say that: report the
   number of rows collected and the server's reported total, and state that completeness could not
   be verified because the rows carry no `id`. **Do not print "0 distinct rows collected"** - that is
   a computed-looking number standing in for a measurement that did not happen. This arm is
   unreachable against the six routes as they stand today and is defence in depth; mark it as such in
   the comment, the way `_empty_on_null` marks its observed-versus-defensive split in `models.py`.
2. **`total > 0 and distinct >= total`** - do not blame the server. A list of exactly
   `_MAX_LIST_PAGES * _PAGE_REQUEST_LIMIT` rows drains correctly, but its last page is full and so
   carries a cursor: the walk stopped one request short of learning it was done, having collected
   every row. Say that, and say `distinct`.
3. **Otherwise** - the list may be longer than the cap, or the server may never report it exhausted.
   Report `distinct` and the server's `total` and assert neither.

### 5.5 Error type and payload (Q5)

**`ProtocolError`, with the collected rows attached, at all four raise sites.**

`ProtocolError`'s docstring already generalises correctly ("a page that advertises more rows but
carries none, a cursor that does not advance") and already explains why `.records` exists: a Python
method that returns a list cannot both deliver rows and raise, so it delivers them on the exception.
That argument is not log-specific. A `list_jobs()` walk abandoned on page 9,999 has collected
~2,000,000 jobs, `_MAX_LIST_PAGES` is private so the caller cannot raise the bound and retry, and
discarding them leaves the caller with a message and nothing else. Dropping the payload here would
re-create, one function over, the exact defect the sibling slice fixed - which is how this item came
to exist in the first place.

**The kwarg stays `records`.** It is documented in `python/README.md` in three places (the paging
example, the error table, and the "Absent entirely" bullet) and renaming it buys nothing. A second
synonym attribute (`.items`) is rejected: two names for one thing is a trap.

**The annotation widens from `list[LogRecord]` to `list[Any]`.** This is the compatible direction:
nothing that typechecks today stops typechecking (a `task_logs` caller doing `e.records[0].content`
still passes under `Any`), and the alternative `list[BaseModel]` would *break* that caller. The cost
is real and small - mypy stops catching a wrong attribute on `.records` - and the docstring carries
the contract. Two consequential edits the plan must not miss:

- `errors.py`'s `if TYPE_CHECKING: from .models import LogRecord` becomes unused if the annotation
  no longer names `LogRecord`; ruff will flag it. Either drop it or keep `LogRecord` referenced from
  the docstring only - decide deliberately, do not let the linter decide.
- The `ProtocolError` docstring and the README error table both currently describe a *log* walk.
  Both must say the exception is raised by any cursor walk and that `.records` holds whatever that
  walk collected - log records from `task_logs`, resource models from the six `list_*` methods.
  `python/tests/unit/test_packaging.py` holds a README-table guard; re-run it.

Each of the six public `list_*` docstrings gains one sentence naming `ProtocolError` and `.records`.

### 5.6 Interaction with the existing `limit` short-circuit (Q6)

**The `limit` short-circuit stays exactly where it is, above every stop.** A caller who asked for
`limit=50` and has 50 rows has been served; turning that into an error because the page that
completed the order also repeated a cursor would make a correct result depend on a defect the caller
never observes. The current code already has this order; the requirement is that the new stops go
*below* it and that a test pins the ordering so a later tidy-up cannot invert it.

**The discriminating case is narrower than it looks, and the existing tests do not cover it.**
The cursor-repeat and page-cap stops cannot fire on request 1 (there is no previous cursor, and
`pages == 1 < cap`), so a walk satisfied on page 1 proves nothing about the ordering. The test must
have `limit` satisfied on **page 2 or later**, by a page that also trips a stop.

This is worth stating because `TestFetchAllPages_RespectsUserLimit` in `internal/relayclient/page_test.go`
*looks* like it already covers this - its fixture returns `NextCursor: "more"` on **every** request,
forever - but `userLimit=3` is satisfied on page 1, so the walk returns before any stop could be
evaluated. It is not a guard for this ordering and must not be counted as one.

---

## 6. Load, failure modes, threat model

- **Who supplies the cursor.** The server. The client has no way to validate it and (by design)
  does not decode it. Every bound in this design is therefore a client-side bound on a value the
  client does not control - which is the whole argument, and the same one already written above
  `_MAX_LOG_PAGES`.
- **What the fix bounds.** Requests, in every case. Retained rows, only via the cursor-repeat stop
  (which now terminates cycles at the first repeat instead of at the cap) and via the caller's
  `limit=`.
- **What it does NOT bound**, and must say so where it is read: wall clock, response bytes, and the
  memory of a single response. Those axes belong to
  `bug-2026-08-26-relayclient-has-no-response-bound-and-no-client-timeout`, which is open and covers
  both languages. Do not write a remedy sentence pointing at `Client(timeout=)`; it does not exist
  as a total-time bound and the README was corrected for saying so once already.
- **Threat model.** The precondition for every stop firing is a server that is malicious, wedged, or
  behind an inconsistent proxy. For relay the server is relay, which is why the parent item is
  `high` rather than critical - but "a client points at a URL from `~/.relay/config.json` or mDNS
  discovery" is the reachable path, and it is the same path that open item names.
- **No relay Invariant is touched.** This slice adds no writes, no epoch, no stream sender, no
  teardown. The one Invariant that comes near is the hashing rule, and section 5.1 declines the
  construct that would have collided with it rather than carving an exception.
- **Compatibility.** Six methods that previously hung now raise. No signature changes. Python floor
  stays 3.9 (`set[str]` under `from __future__ import annotations` is fine).

---

## 7. Where each test goes

The Python analogue of CLAUDE.md's "Where a CLI test goes" rule: does the assertion's truth depend on
what the **server** puts on the wire?

- **No** -> `python/tests/unit/test_client.py` with an `httpx.MockTransport` fixture. Every stop, the
  message arms, the orderings, the request counts. All adversarial or impossible server behaviour,
  which is most of this slice.
- **Yes** -> `python/tests/integration/`. Exactly one assertion qualifies (T11 in section 9): that a
  *real* list handler answering with zero rows does not trip the empty-page stop. That is the
  inverted-ordering catastrophe, and its truth is a property of `buildPage`, not of a fixture.
  **Note the lane is not in CI** (recorded in the 2026-08-27 envelope-sweep retro and in
  `idea-2026-08-23-cli-tests-never-hit-real-server`), so it is a manual gate the conductor must run
  and report, not a check that will catch a regression later.
- **Fixtures must not be encoded through the SDK's own types.** `test_client.py`'s existing
  `_page_response` helper hand-writes `{"items", "next_cursor", "total"}` and is the right exemplar;
  keep using it. On the Go side, **do not** follow `TestFetchAllPages_ForwardsParams`, which encodes
  `PageEnvelope[item]{Total: 0}` through the production envelope type and so agrees with the decoder
  by construction. New Go fixtures write JSON bytes directly.
- **Every hanging-risk test needs a terminator.** This project has no `pytest-timeout`. Follow the
  existing cap tests: after the intended number of requests, the fixture returns HTTP 500, so an
  implementation missing the stop terminates with `ServerError` - which is not `ProtocolError`, so
  the test is RED rather than hung. Apply the same trick in Go with an `http.Error` past the count.

---

## 8. G1: the Go decision, argued

**Recommendation: fix `FetchAllPages` in this slice, as a sub-slice sequenced after the Python work
so it can be dropped without unwinding anything.**

### 8.1 Blast radius, measured

Repository-wide search for the symbol `FetchAllPages` (exported, unaliased, so a literal search is
adequate - a generic function has no dynamic dispatch and an alias definition would itself contain
the symbol). **Six production call sites, all in `internal/cli`; three test call sites in
`internal/relayclient/page_test.go`; zero in `internal/mcp`, `internal/discovery`, or anywhere else.**

| Call site | Command surface | `userLimit` | What it does with the result |
|---|---|---|---|
| `jobs.go` `doListJobs` | `relay list` | `*limitFlag` | prints a table, or the slice as JSON |
| `workers.go` `doWorkersList` | `relay workers list` (+ `--revoked`) | `*limitFlag` | prints a table, or the slice as JSON |
| `schedules.go` | `relay schedules list` | `*limitFlag` | prints a table |
| `reservations.go` | `relay reservations list` | `*limitFlag` | prints a table |
| `admin_users.go` | `relay admin users list` | `*limitFlag` | prints a table |
| `workers.go` `resolveWorkerIDIn` | `relay workers get/enable/disable/delete <hostname>`, `relay workers workspaces` (x2) | **`0`** | scans for a hostname match; on a miss, walks a **second** path |

The last row is the finding. Five of the six are explicit list commands where an operator asked to
see rows and can pass `--limit`. The sixth is an **implicit** full walk performed to turn a hostname
into an id, with `userLimit=0` so no caller flag can bound it, reached by six commands that have
nothing to do with listing. `relay workers delete myhost` against a server with a repeating cursor
hangs, and nothing in the command's surface suggests it is paging anything.

Every one of the six already handles a returned `error`, so a new error path needs **zero** caller
changes to compile or to surface. Verified at each site: five are `if err != nil { return err }`
(`admin_users.go` wrapping as `list users: %w`).

**The sixth is not, and the plan should look at it before review does.** `resolveWorkerIDIn` treats
a **fallback** path's error as soft - keyed on the loop index, deliberately, so a non-admin's 403 on
`/v1/workers/revoked` does not mask the primary list's miss. That is right for an auth error and
wrong for this one: a cursor-repeat on the revoked list would be swallowed and reported to the
operator as `no worker found with hostname "myhost"`. The fix does not *require* touching it - the
primary path, which is where the walk almost always is, reports fatally - and widening the
soft-error rule is a separate judgement about which errors are soft. **Name it as a known
consequence rather than discovering it in review**; the cheapest honest option is a comment at that
branch recording that a protocol error is currently soft there.

### 8.2 How the Go failure mode differs from Python's

- **Same loop, same gap.** `out = append(out, resp.Items...)` under an unbounded `for`, with the only
  exits being `userLimit` and an empty `NextCursor`. Unbounded requests and unbounded slice growth.
- **The cursor is a string, so "non-advancing" needs the same set-membership predicate** derived in
  section 5.1. Nothing about that derivation is language-specific; the server that issues the cursor
  is the same server.
- **Nothing outside bounds it either.** `NewClient` returns `&http.Client{}` - the zero value, no
  `Timeout` - and `cmd/relay/main.go` supplies a `signal.NotifyContext` with no deadline. Verified in
  both files, not inferred from the backlog item that reports them.
- **The one genuine mitigation Python lacks:** an interactive operator can Ctrl-C, and
  `signal.NotifyContext` means that actually cancels the request. A Python service calling
  `list_workers()` on a timer has no equivalent. This makes Go's severity lower. It does not make it
  zero: `relay` is used in scripts and CI steps where nobody is watching, and the process is wedged
  rather than failing.

### 8.3 The argument

**For fixing it here.**

1. **One design, two implementations, versus two designs.** Every question this spec settles -
   predicate, ordering, cap value, message arms - has the same answer on both sides because the same
   server issues the cursor. Splitting means a second spec, a second plan, a second verify fan-out,
   and a second chance to answer one of those questions differently.
2. **The asymmetry left behind is itself the hazard.** This item exists *because* `task_logs` got
   three stops and `_fetch_all`, in the same file, did not. The `relay logs` envelope drift existed
   because two neighbouring methods were fixed in June and a third was missed. Shipping a Python
   loop with three stops next to a Go loop with none, and a spec that says the gap is known,
   reproduces the exact pattern twice already retro'd on this repo.
3. **The cost is unusually low, and measurably so.** One function in one leaf package; six callers
   that need no edit; a default-lane `httptest` test file that needs no Docker and no database - the
   cheapest verification surface in the repository. The CLI integration lane needs **no** extension
   for this (and per `idea-2026-08-27-cli-lane-never-crosses-a-list-page-boundary` it never crosses a
   list page boundary anyway, so it would not exercise the change even if run).
4. **The exposure is wider than the item states** (section 3.3, section 8.1): six commands walk
   implicitly, unbounded by any user flag.

**Against, and the rebuttals.**

- *Lower severity - Ctrl-C exists.* True, and it is the reason Go is sequenced **second** and is
  droppable rather than being the headline. It is not a reason to leave the loop unbounded in
  scripts and CI steps.
- *Two languages widens the review surface and the gate set* (`make test` plus `make test-race`, on
  top of pytest/ruff/mypy). True. It is bounded: the Go diff is two files, `internal/cli` is not
  touched, and `make test-race` on Windows must go through the Linux container per CLAUDE.md.
- *It needs a malicious server, and for relay the server is relay.* The same objection applies to the
  Python half, which the user has already decided to fix. It argues about priority, not about which
  slice.

**Sequencing.** Python sub-slice first (it is the item's subject and the higher-severity half), Go
sub-slice second, with no shared files. If the Go half is dropped mid-flight, nothing in the Python
half unwinds and the decision is recorded here rather than lost.

### 8.4 What the Go fix looks like, and the one place it must NOT mirror Python

- Same six-step order as section 4.
- `maxListPages = 10000` as an unexported **package `var`** in `internal/relayclient`, so a test can
  shrink it - matching `internal/cli`'s `maxLogPages`, which is a `var` for exactly this reason and
  says so in its comment. **The shrinking test must not call `t.Parallel()`**, since the var is
  package-global state.
- `seen map[string]struct{}` on the raw cursor string, per 5.1. No `sha256`; see 5.1 for why the
  digest is declined and why that reason is partly the hashing invariant.
- Errors are plain `fmt.Errorf` carrying the `paginate %s:` prefix the existing transport-error path
  already uses, so an operator learns which endpoint stalled.
- **Return `nil, 0, err`, not the partial slice.** This is a deliberate asymmetry with Python's
  `.records` and it needs to be in the comment or a reviewer will read it as drift. Python's caller
  is a program that can use partial rows and has no other way to get them; Go's callers are five
  renderers that have printed nothing yet plus one id-resolver, none of which has anywhere to put a
  partial list, and the existing error path already returns `nil, 0`.
- **The page-cap message must NOT claim completeness, because Go cannot compute the distinct count.**
  `T` is a bare type parameter with no constraint and no `id`; reading one out would take reflection
  or a decode change, both of which couple the leaf package to its callers' row shapes. So the Go
  message reports rows collected and the server's reported `total` and asserts **neither**
  possibility - it does not blame the server, and it does not claim every row was collected. State
  in the comment that this is *why* it differs from the Python message, so the next reader does not
  "fix" the asymmetry by copying Python's wording onto a count that cannot support it. This is the
  same trap section 3.5 records `printTaskLogs` already falling into.
- Note that `FetchAllPages` captures `total` from the **first** page (`if first { total = resp.Total }`)
  while Python's cap arm reads the **current** page's total. Leave that as is - it is the existing
  contract of the Go return value - but the cap message should use the value it actually has and say
  which.

---

## 9. Test plan and mutation kill table

Names are indicative; the properties are the contract.

**Python, `python/tests/unit/test_client.py` (default lane):**

- **T1 repeat cursor.** Pages return items with `next_cursor: "SAME"`; HTTP 500 after request 3.
  Assert `ProtocolError`, message contains `SAME`, **request count == 2**.
- **T2 cycle of length 2.** Cursors `A, B, A`; 500 after request 4. Assert `ProtocolError` naming the
  repeated cursor, **request count == 3**. *This is the test that discriminates the set from a
  previous-cursor comparison* - under previous-only the walk never fires and terminates as
  `ServerError`.
- **T3 empty page advertising more.** Page 1 has rows and a cursor; page 2 has `items: []` with a
  non-empty cursor. Assert `ProtocolError`, message says the page was empty, **and `.records` holds
  page 1's rows**. Page 1 must be non-empty: an empty first page makes `.records == []` under both
  correct and mutated code, which is the vacuous-fixture trap recorded in the last retro.
- **T4 zero rows is not an error.** `items: []`, `next_cursor: ""`. Assert `== []`, no raise, one
  request. *Pins drained-above-empty.*
- **T5 page cap bounds requests.** `_MAX_LIST_PAGES` monkeypatched to 3, unique advancing cursors,
  500 past the cap. Assert `ProtocolError` mentioning the cap and **request count == 3**.
- **T6 cap does not blame the server when `total` is reached.** Cap 2, two pages of two
  distinct-id rows, `total: 4`, last page carries a cursor. Assert the message says every row was
  collected, does **not** say "may be longer", **and `.records` has 4 rows**. The outcome assertion
  is not optional - the sibling's version of this test was green because of the bug until it was
  added.
- **T7 distinct ids, not appended rows.** Cap 2; the server serves the **same two ids** twice behind
  **advancing** cursors; `total: 4`. `len(out) == 4 == total` but `distinct == 2`. Assert the
  message takes the blaming arm and reports 2.
- **T8 `limit` satisfied on page 2 by a page that repeats a cursor.** `limit=3`; page 1 two rows
  cursor `A`; page 2 two rows cursor `A` again. Assert 3 rows returned and **no raise**. *Pins the
  ordering; a page-1 fixture cannot.*
- **T9 an over-long cursor is truncated in the message.** A 5,000-character repeated cursor. Assert
  the message is bounded and reports the cursor's length.
- **T10 `list_jobs` specifically.** One end-to-end test through a public method (not `_fetch_all`
  directly) so the six methods are wired, not just the helper.

**Python, `python/tests/integration/` (manual gate, not in CI):**

- **T11 a real empty list does not raise.** Against a live `relay-server`, a list call whose filter
  matches nothing returns `[]`. This is the only assertion here whose truth depends on `buildPage`.

**Go, `internal/relayclient/page_test.go` (default lane, hand-written JSON bodies):**

- **G-T1..G-T5** mirror T1, T2, T3, T4, T5.
- **G-T6** mirrors T8: `userLimit` satisfied on page 2 by a page repeating a cursor.

**Mutation kill table.** Each row must have a non-empty red set, and each stop must own at least one
test no other row reddens.

| # | Mutation | Expected RED |
|---|---|---|
| M1 | delete the cursor-repeat stop | T1, T2 |
| M1b | **weaken** it to "equals the previous cursor" | **T2 only** |
| M2 | delete the empty-page stop | **T3 only** |
| M3 | delete the page cap | T5, T6, T7 |
| M3b | `pages >= cap` -> `pages > cap` | **T5 only** (request-count assertion) |
| M4 | move the empty-page stop above the drained return | **T4 only** (and T11) |
| M5 | `distinct` -> `len(out)` in the cap arm | **T7 only** |
| M6 | move the `limit` short-circuit below the stops | **T8 only** |
| M7 | drop `records=out` at each raise site (one at a time) | the matching records assertion in T3 / T6 |
| M8 | remove message truncation | **T9 only** |

Every mutation must be verified as **applied** before its result is believed - a harness that fails
to write the mutant reports "survived", and CRLF broke four in a row last session. Restore in a
`finally` and assert a clean tree. If the mutation runs against a copied tree, `PYTHONPATH` must
point at the copy and the run must print `relay.__file__` to prove which tree was loaded; the package
is installed editable and a copy alone is not isolation.

---

## 10. Non-goals, and how they interact

- **`body.get("next_cursor", "")` is not changed here.** That default reads a *dropped* key as
  "drained" and is the subject of the open item
  `bug-2026-08-27-python-sdk-page-cursor-defaults-to-drained`, which also covers `_get_page` and is
  therefore wider than this slice. **The two items edit the same statement**, so the plan must know:
  if that item lands first, `cursor = body["next_cursor"]` and this slice's `if not cursor` then
  tests a genuinely-empty cursor only, with no other change. If this slice lands first, that item's
  diff is one line inside a loop this slice reshapes. Sequence deliberately; do not let both be in
  flight.
  Note the *other* default this slice introduces, `body.get("total", 0)`, is **not** the same defect:
  a missing `total` falls into the blaming arm, which is the safe direction, whereas a missing
  `next_cursor` falls into "drained", which silently truncates. Say that in the comment so the two
  are not conflated by a later sweep.
- **No `_read_json` chokepoint.** `bug-2026-08-27-python-sdk-exceptions-escape-the-relayerror-hierarchy`
  and the response-bound item both want one, together. Out of scope; a `KeyError` from a missing
  `items` still escapes here, unchanged.
- **`printTaskLogs`'s completeness arm is not corrected here** - see section 11.
- **The CLI integration lane is not extended.** `idea-2026-08-27-cli-lane-never-crosses-a-list-page-boundary`
  owns that gap, and this fix's RED belongs in `page_test.go` regardless.
- **No response-size or wall-clock bound.** Owned by
  `bug-2026-08-26-relayclient-has-no-response-bound-and-no-client-timeout`, both languages.

---

## 11. Proposed backlog items

Proposals only - not filed. The human accepts.

1. **`printTaskLogs`'s "every one printed" rests on a non-distinct row count, and the O(1) fix also
   frees 35-70 MB in the Python port.** `logProgress.rows` counts rows *written*, so under a server
   re-serving pages behind an advancing cursor the Go cap message asserts completeness it has not
   verified - the same defect the Python `task_logs` port was refuted for last session and fixed with
   a distinct-`seq` set. The Go loop cannot afford that set (its O(one page) memory is a stated
   design property). A cheap exact alternative exists for an *ordered* cursor: count a row only when
   its `seq` exceeds the highest seq yet written. That is O(1), exact on a correct server, and
   under-counts (the safe direction) on a misbehaving one. The same counter would let Python's
   `task_logs` drop its 35-70 MB transient set for the identical guarantee. Two files, one property.
   *(Type: bug. Priority: low - it is a message-accuracy defect behind a misbehaving server.)*

2. **`internal/relayclient`'s existing test fixtures encode through the production envelope type.**
   `TestFetchAllPages_ForwardsParams` marshals `PageEnvelope[item]{Total: 0}` and so agrees with the
   decoder by construction on both the envelope keys and the item fields - the exact vacuous-fixture
   shape CLAUDE.md enumerates for `internal/cli`, in a package that rule's paragraph does not name.
   Worth a sweep of `internal/relayclient` and `internal/mcp` fixtures on the same axis (the MCP
   files use `PageEnvelope[map[string]any]`, which is a genuine simulator on the item axis and a
   tautology on the envelope axis). *(Type: idea. Priority: low.)*

---

## 12. Acceptance criteria

Carried forward **verbatim** from `bug-2026-08-27-python-sdk-fetch-all-has-no-termination-stops.md`:

- A repeating or non-advancing `next_cursor` terminates with a `ProtocolError` naming the cursor.
- A page cap bounds the request count, and the message does not blame the server when the
  envelope's own `total` says every row was collected.
- Each stop is pinned by a mutation that kills exactly one test.
- The Go `FetchAllPages` decision is made explicitly - fixed too, or declined in writing.

Derived, and required by this spec on top of those:

- `list_jobs()` (and each of the other five) against a list with **zero** matching rows returns `[]`
  and does not raise - proven both against a fixture and, once, against a live server.
- A caller-supplied `limit` satisfied on **page 2 or later** by a page that also trips a stop returns
  rows, not an error.
- Every raise site attaches the rows collected so far, and each attachment is pinned by a test whose
  expected value differs under the mutant (so the empty-page test's first page must be non-empty).
- The page-cap message's completeness claim is backed by a **distinct** count, and where no distinct
  count is available the message says so rather than printing a zero.
- The cursor named in a message is truncated to a bounded length.
- `_MAX_LIST_PAGES` is a class attribute read off `self`, and a test shrinks it.
- The comment names what the cap does **not** bound and points at
  `bug-2026-08-26-relayclient-has-no-response-bound-and-no-client-timeout` rather than restating its
  measurements.
- Gates: `pytest` on 3.13 **and** on the 3.9 CI floor, `ruff check src tests`, `mypy src`, the README
  guard in `test_packaging.py`. If the Go sub-slice ships: `make test`, `go vet`, and `-race` via the
  Linux container (or an explicit statement that `-race` did not run).

---

## 13. Open questions the plan inherits

These are recorded rather than guessed, so the plan phase decides them with evidence instead of
inheriting a silent assumption.

1. **The exact truncation threshold for a cursor in a message.** A real cursor is ~128 characters
   (a `{t,i,s}` JSON of ~96 bytes, base64'd), so any threshold at or above ~200 shows every
   legitimate cursor in full. The spec requires *a* bound and does not fix the number.
2. **Whether `errors.py` keeps its `TYPE_CHECKING` import of `LogRecord`.** Depends on whether the
   widened annotation still references it; ruff decides loudly either way, but the choice should be
   deliberate.
3. **Sequencing against `bug-2026-08-27-python-sdk-page-cursor-defaults-to-drained`** (section 10).
   Both edit the same statement. The plan should either sequence them explicitly or state that the
   other item stays untouched.
4. **Whether `resolveWorkerIDIn`'s soft-error branch is touched** (section 8.1). A protocol error on
   the fallback path is currently reported as "no worker found". Comment, widen, or leave.
5. **Whether the Go sub-slice ships in the same PR or a follow-up PR on the same branch.** The
   recommendation is same branch, separate commits, so the Go half can be dropped by dropping
   commits.
6. **Whether T11 (the live-server assertion) blocks the slice.** The Python integration lane is not
   in CI. If the conductor cannot stand up a server, the slice can ship on the fixture tests alone -
   but that must be *stated*, not silently skipped, and this spec's section 7 says why the assertion
   was wanted.
