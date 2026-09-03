# Lane MF: the worker reservations panel, the login user object, the RegisterScreen config fetch, and the job-detail resizable split

Date: 2026-09-02
Branch: `claude/web2-mf-misc-frontend`
Worktree: `.claude/worktrees/web2-mf`, at `origin/main` (carries lane SB's merged
reservations `?worker_id=` filter and auth `user` body, lane JB's jobs filters,
lane TB's table changes, and the dialog guard)
Author: relay-tpm (autonomous gate mode; no human answered questions during this
flow, so every question in the Decisions section was decided here and the calls a
human might make the other way are listed under Escalations)

## Why this lane exists

Four unrelated frontend follow-ups, three of which were blocked on a server
capability that lane SB has now merged, and one of which is a deferred design
affordance. Nothing under `internal/`, `cmd/` or `python/` changes.

Backlog items in scope:

- `docs/backlog/feature-2026-06-05-worker-detail-reservations-panel.md`
- `docs/backlog/idea-2026-06-03-login-return-user-object.md`
- `docs/backlog/idea-2026-08-09-registerscreen-config-fetch-unify.md`
- `docs/backlog/idea-2026-07-01-job-detail-resizable-split.md`

They share no file. That is the lane's organising property and it is what makes
the four-commit sequence below independently green at every step.

## The hi-fi, quoted

Two substitutions keep this file ASCII and are the only alterations to the quoted
text: the middot separator the hi-fi uses inside display strings is replaced by a
hyphen, and each non-ASCII pictograph is replaced by a bracketed description. No
structure, property, value or guard is changed. Everything below is a quotation
from `design_handoff_relay_holo/hifi3-holo-pages.jsx`, not a paraphrase of a
backlog item.

### The worker detail Reservations panel, in full

It sits in the right-hand column of the worker detail body, between the Labels
panel and the Agent token panel.

```jsx
<div style={{...glassPanel(C),padding:'14px 16px'}}>
  <div style={{fontSize:13,marginBottom:8}}>Reservations</div>
  <div style={{display:'flex',alignItems:'center',gap:10,padding:'8px 10px',borderRadius:6,
    background:'rgba(0,0,0,0.25)',border:`1px solid ${C.border}`}}>
    <span style={{width:6,height:6,borderRadius:'50%',background:C.accentB}}/>
    <span style={{fontFamily:C.mono,fontSize:11.5,color:C.fg,letterSpacing:'0.04em'}}>vfx-sprint</span>
    <span style={{fontFamily:C.mono,fontSize:11,color:C.fgMute}}>project=film-x</span>
    <span style={{marginLeft:'auto',fontFamily:C.mono,fontSize:10.5,color:C.fgMute,letterSpacing:'0.04em'}}>until May 14 18:00</span>
  </div>
  <div style={{fontFamily:C.mono,fontSize:10,color:C.fgDim,letterSpacing:'0.04em',marginTop:8}}>
    selectors are informational in v1 - only worker_ids are enforced.
  </div>
</div>
```

What that settles, and what it does not:

- The panel carries **four data slots per row**: a coloured dot, the reservation
  name in mono, a `project=` pair in mono, and a right-pushed end-time phrase.
- The dot's colour is a **fixed constant** in the mock (`C.accentB`), not derived
  from anything.
- The panel is a **stack of bordered rows**, not a table: no header row, no
  columns, no alignment shared between rows.
- The footnote is **part of the panel**, not decoration. See Decision 4 - it is
  the panel's correctness statement, and it survives verbatim.
- The mock has exactly **one row**, so it says nothing about how two rows align,
  about an empty state, about an error state, or about a short page.

### The job detail split, in full

The container, the two panes and the handle:

```jsx
const containerRef = useRef(null);
const [rightW, setRightW] = useState(null);

useLayoutEffect(() => {
  if (rightW === null && containerRef.current) {
    const w = containerRef.current.clientWidth;
    setRightW(Math.max(360, Math.floor((w - 14) / 2)));
  }
});

const onDragStart = (e) => {
  if (rightW === null) return;
  const startX = e.clientX;
  const startW = rightW;
  const containerW = containerRef.current ? containerRef.current.clientWidth : 1000;
  const onMove = (ev) => {
    const dx = startX - ev.clientX;
    const newW = Math.min(containerW - 420, Math.max(340, startW + dx));
    setRightW(newW);
  };
  const onUp = () => {
    window.removeEventListener('mousemove', onMove);
    window.removeEventListener('mouseup', onUp);
    document.body.style.cursor = '';
    document.body.style.userSelect = '';
  };
  window.addEventListener('mousemove', onMove);
  window.addEventListener('mouseup', onUp);
  document.body.style.cursor = 'col-resize';
  document.body.style.userSelect = 'none';
  e.preventDefault();
};
```

```jsx
<div ref={containerRef} style={{flex:1, minHeight:0, display:'flex', alignItems:'stretch', position:'relative'}}>
  <div style={{flex:1, minWidth:0, display:'flex',flexDirection:'column', gap:14, paddingRight:14}}>
    ...the left pane...
  </div>

  {/* Drag handle */}
  <div onMouseDown={onDragStart} style={{
    width:6, flex:'none', cursor:'col-resize',
    alignSelf:'stretch', position:'relative',
    background:'transparent',
  }} title="Drag to resize">
    <div style={{position:'absolute',top:'50%',left:'50%',transform:'translate(-50%,-50%)',
      width:2, height:36, borderRadius:2, background:hexToRgba(C.accent,0.3)}}/>
  </div>

  <div style={{flex:'none', width: rightW || 360, minWidth:340, paddingLeft:8, display:'flex',flexDirection:'column'}}>
    ...the right pane...
  </div>
</div>
```

What that settles, and what it does not:

- The handle is **6 CSS pixels wide**, stretches to the container height, is
  transparent, and paints a 2-by-36 pixel rounded bar centred inside it at 30
  percent accent opacity. The cursor is the column-resize cursor. That is the
  visual specification and this lane reproduces it.
- The **right** pane is the sized one; the left pane is the flexible remainder.
- The mock sizes it in **CSS pixels, not percent**, with a 340 pixel floor on the
  right pane and a 420 pixel floor on the left (expressed as a container-relative
  ceiling on the right pane).
- The handle has **no role, no tabindex, no key handler, no ARIA and no
  persistence.** Every accessibility requirement in the backlog item is an
  addition to the hi-fi, not a quotation of it, and the item should not be read as
  saying the design specifies them. It specifies the pointer drag and the visual
  treatment only.
- `document.body.style.userSelect` is set for the duration of the drag. That
  detail is worth keeping and is not obvious; without it a drag selects the text
  of both panes.

## What I verified against the tree, and what I refuted

A backlog proposal is not a contract. Every bullet below was checked in this
worktree.

1. **Refuted (the reservations item's premise, now stale).** "The reservations
   endpoint is global and not queryable by worker, so this needs a server-side
   lookup or a documented client-side filter before the panel can be built." Lane
   SB shipped the server-side lookup. `parseReservationFilters` reads a single
   optional `worker_id`, `handleListReservations` threads `filters.WorkerID`
   through `CountReservations` and all eight sort arms, and the README states the
   contract. The client-side-filter branch of the proposal is dead and must not be
   built.
2. **Confirmed, and it is the panel's whole meaning.** `Dispatcher.selectWorker`
   builds `reservedIDs` from `res.WorkerIds` only and `continue`s past any worker
   in that set, for **every** task. It never reads `selector` and never matches a
   reservation's project against a task. So a row in this panel means "this worker
   is skipped by the scheduler", not "this worker is held for project X", and the
   hi-fi's footnote is a true and load-bearing statement rather than a caption.
   See Decision 4.
3. **Refuted (the wireframe's framing).** The item says the wireframe shows "the
   reservations that apply to the worker being viewed". `?worker_id=` matches
   array containment on `worker_ids` and nothing else, so a selector-only
   reservation that a reader would say "applies" to this worker is **not**
   returned. That is correct rather than a gap, because a selector enforces
   nothing - but the panel must not advertise itself as showing everything that
   applies.
4. **Confirmed.** An id that names no worker returns an empty page with
   `total: 0`, not a 404, and an empty `?worker_id=` value is treated as absent.
   A non-UUID value is `400 invalid worker_id; expected a UUID`, which this client
   cannot produce because the value is a route id the worker fetch has already
   resolved.
5. **Confirmed.** `authResponse` carries `User userResponse` with no omitempty,
   built by `newAuthResponse` from `toUserResponse`, on all three write sites
   (invite register, self-serve register, login). `TestAuthLogin_UserMatchesUsersMe`
   pins it against the live `GET /v1/users/me` response. The README's register
   example shows the full body including `archived_at: null`.
6. **Refuted (the login item's note).** The note says the payload "must include
   `created_at` or the header breaks on a fresh login" and lists the shape as
   `id, email, name, is_admin` plus `created_at`. The wire shape has a **sixth**
   key, `archived_at`, which the item never mentions. It is a pointer with no
   omitempty, so the key is always present and is `null` on every row this
   endpoint can return. `web/src/lib/types.ts` deliberately does not model it, with
   a stated reason; Decision 7 re-derives that reason for the new producer rather
   than inheriting it.
7. **Refuted (the RegisterScreen item's acceptance criterion).** "The existing
   `RegisterScreen` tests, **including the fail-closed-on-error behaviour**, still
   pass unchanged." There is no fail-closed-on-error test in
   `RegisterScreen.test.tsx`. Its five tests are: invite field hidden when
   self-register is on, shown when off, an inline invite error on 400, the
   email-exists 409 path, and the autofocus pin. The `catch` arm that sets `false`
   is **untested at HEAD**. So the criterion cannot be satisfied as written, and
   this lane must add that test rather than claim it kept passing.
8. **Refuted (the RegisterScreen item's dichotomy).** "Confirm the two call sites'
   semantics can genuinely be reconciled ... If they cannot be reconciled cleanly,
   the honest outcome is to keep the two call sites." The two semantics are
   properties of the two **consumers**, not of the fetch. `useServerConfig`
   returns the raw query state and fabricates nothing; the sign-up screen derives a
   fail-closed `false` from `isError`, and the admin tab renders its error strip.
   One client, two policies, no fabrication in the shared layer. The item's
   either-or is a false one.
9. **Confirmed.** A `QueryClientProvider` is in scope at `/register`. `App.tsx`
   nests `QueryClientProvider` outside `BrowserRouter`, which is outside
   `AuthProvider`, which wraps `AppRoutes`; `/register` sits inside
   `PublicOnlyRoute` beneath all three. `RegisterScreen.test.tsx`'s own
   `renderRegister` helper already wraps in a provider too.
10. **Confirmed, and it changes behaviour.** The shared client sets `retry: 1`.
    Today's raw fetch fails closed after one failed request; through the hook a
    transient failure is retried once before the fail-closed value is derived. That
    is one extra request and a longer blank screen on a hard failure, in exchange
    for a correct answer on a blip. Decision 8 takes it and does not override the
    default, because a per-call-site retry override would re-introduce the
    divergence the item exists to remove.
11. **Refuted (the lane brief's suggestion, and this is the JF dependency
    question).** Lane JF's `usePersistedChoice` is
    `usePersistedChoice<T extends string>(key, allowed: readonly T[], fallback: T)`
    - a membership test against a finite allow-list of strings. A split width is a
    clamped integer over a continuous range; expressing 41 admissible percentages
    as a string allow-list is not a reuse, it is a defeat of the hook's own
    validation idea. **This lane therefore does not consume `usePersistedChoice`
    and does not wait for lane JF to merge.** See Decision 13 for what it uses
    instead and why it is not filed as a third divergent copy.
12. **Confirmed.** No file this lane touches is touched by lane JF. JF's changed
    list is the jobs list surface (`JobsPage`, `JobsTable`, `JobsLanes`, jobs
    `api.ts`, `useJobs`, `useJobLanes`, new timeline files), `WorkersPage.tsx`,
    `web/src/lib/usePersistedChoice.ts` and `web/e2e/surfaces.ts`. This lane
    touches `JobDetailPage.tsx`, `WorkerDetailPage.tsx`, the auth pair,
    `web/src/admin/reservations/api.ts` and `web/e2e/keyboard.spec.ts`. Disjoint.
13. **Confirmed.** `/workers/:id` is **not in `web/e2e/surfaces.ts` at all**, and
    the e2e README names it as one of five pages the harness never visits, adding
    that it is the one where the gap matters most. No agent runs in slice 1, so no
    worker row exists and no path leads to the page. The seeded reservation is
    selector-only with no `worker_ids`, so even a fabricated worker would render
    this panel's empty state. Decision 6 says what the human pass must be.
14. **Refuted (the framing in the layout item and the lane brief).** "A third
    panel needs the layout gate." The worker detail page's right column already
    renders a Reservations `Panel`; this lane changes its **contents**, not the
    panel count. The added width risk is nil by construction - the shared `Table`
    wraps its subtree in a horizontal scroll wrapper, and `surfaces.ts` records
    measured evidence that widening a table's own minimum width changes no
    document measurement. The real added risks are **height** and **clipping
    inside a new scroller**, and neither is what a document-overflow gate
    measures. The human pass must be scoped to those two things.
15. **Confirmed.** `web/src/test/setup.ts` runs MSW with `onUnhandledRequest:
    'error'`, so "this route was never called" is assertable. Two existing tests
    prefer a counting handler over relying on the error, because a count names the
    route; this lane follows that.
16. **Confirmed.** No test anywhere in `web/src` pins the job detail page's
    current fixed column widths. The split can be re-expressed without editing a
    test.
17. **Confirmed.** `AuthProvider.test.tsx`'s login fixture and
    `AuthProvider.crossgen.test.tsx`'s two login fixtures all return a body with
    no `user` key and register a `GET /v1/users/me` handler. With the fallback of
    Decision 7 they stay byte-identical and green. That is a property of the
    fallback, not a coincidence, and it is why the new positive test must remove
    the `/users/me` handler rather than merely add a `user` key.
18. **Checked and found nothing.** No resizer, splitter or `role="separator"`
    anywhere under `web/src`; no drag-handling code of any kind; no `matchMedia`
    consumer; no numeric localStorage value. Everything in Decision 13 is new.

## The server contract this lane consumes

Restated from the README's Reservations and Public sections and from
`internal/api/reservations.go` and `internal/api/auth.go`. Nothing here is new.

`GET /v1/reservations` - admin-only, paginated.

| Parameter | Meaning | Absent means |
|---|---|---|
| `worker_id` | Matches reservations whose `worker_ids` array contains that id. Composes with `limit`, `cursor` and `sort`. | No worker filter |

- An id naming no worker, or a worker no reservation targets, is an **empty page
  with `total: 0`**, never a 404.
- An empty value is treated as absent; a non-UUID value is
  `400 invalid worker_id; expected a UUID`.
- Repeating the parameter is a 400. `URLSearchParams.set` cannot repeat one.
- The containment test is a scan and is not index-served.
- Row shape: `id`, `name`, `selector` (present, possibly the literal `null`),
  `worker_ids` (always an array), `user_id`, `created_at` always present;
  `project`, `starts_at`, `ends_at` are pointers with omitempty, so the **key is
  absent** when NULL - never `null`.

`POST /v1/auth/login` and both arms of `POST /v1/auth/register` return
`{ token, expires_at, user }` where `user` is exactly the `GET /v1/users/me`
body: `id`, `email`, `name`, `is_admin`, `created_at`, `archived_at`.

`GET /v1/config` returns `{ allow_self_register }` and requires no bearer token.
The value is read from process environment at startup and cannot change without a
server restart.

## Decisions

Autonomous run. Each question, its options, the choice and the reason.

### Item 1: the worker detail reservations panel

**1. Is the panel a table or the hi-fi's stack of rows?**

- Options: (a) the shared `Table` primitive with columns; (b) the hi-fi's
  bordered-row stack; (c) reuse `ReservationsTable` from the admin tab.
- Chosen: **(a)**.
- Why (c) is out first: `ReservationsTable` is eight columns over 690 pixels of
  fixed track with a 980 pixel minimum width and a per-row Delete button. It is
  built for a full-width admin page. Dropping it into a half-width detail column
  would put the app's second-widest table inside the page with the least
  headroom, and would put a destructive control on a read-oriented page. The hi-fi
  agrees: it draws a compact list here and a full table on the admin page, which
  is a deliberate distinction, not an omission.
- Why (a) over (b): the page's two other data panels are both `Table` consumers,
  and `WorkerDetailPage.test.tsx` carries a structural test that walks every
  `role="table"` up to its `data-panel-title` ancestor and asserts the accessible
  name matches the title. A stack of `div`s joins nothing, aligns nothing between
  rows, and gives a screen reader no column names. Every datum the hi-fi shows
  survives the change of frame: the dot becomes the status treatment, the name is
  a cell, `project=film-x` becomes a PROJECT cell, and `until May 14 18:00`
  becomes an ENDS cell. The deviation is the **frame**, not the content.
- Consequence: the page's table count goes from two to three, and three existing
  tests in `WorkerDetailPage.test.tsx` assert against the old count or the old
  placeholder copy. They change; see the gate below.

**2. Which columns, and what does an absent `ends_at` render?**

- Four columns: NAME, PROJECT, STATUS, ENDS. NAME and PROJECT are flexible and
  truncate; STATUS and ENDS are fixed tracks summing to 200 pixels, and the
  component's own `MIN_W` constant is a 460 pixel literal - below both sibling
  panels' minimums on the same page. **This is arithmetic, not a measurement**;
  see Decision 6.
- STATUS is a `Chip` carrying the derived `ACTIVE` / `SCHEDULED` / `ENDED` word,
  reusing `deriveStatus` and `statusTone` unchanged. It is a chip with text and
  not the hi-fi's bare dot: the hi-fi's dot is a fixed colour that encodes
  nothing, and a colour-only signal that did encode something would be
  unreadable to a screen reader. `now` is injected as a prop, as
  `ReservationsTable` does it, so the pill is a pure function of props and a test
  supplies a fixed `Date`; the page supplies `useNow` at 60 seconds.
- ENDS renders `formatDateTime(ends_at)` when the key is present and the literal
  `no end` in muted text when it is absent. This deviates from
  `ReservationsTable`, which renders a plain hyphen for every absent optional, and
  the reason is specific to this panel: there, STARTS and ENDS are read as a pair
  and a hyphen is read across it; here, the absence **is** the fact - an
  open-ended reservation excludes this worker from dispatch indefinitely, and a
  hyphen would read as a missing value.
- No SELECTOR column and no WORKERS column. Every row in this panel names this
  worker by construction, and the selector enforces nothing (Decision 4).
- No Delete control. Deleting a reservation from a worker's page would act on
  every other worker the reservation names, invisibly. The admin tab owns that.

**3. Sort, page size, query key, and what the footer says.**

- Sort: the endpoint default `-created_at`, the same value the admin table
  defaults to. Ordering by derived status would be nicer and is **not a server
  capability**; reordering one page of it client-side would present a local
  ordering as a global one.
- Page size: the existing 50, unchanged. No new `limit` parameter on
  `listReservations`, so its current URL is byte-identical for the admin caller.
  No pager in a side panel: the footer states a short page the way
  `WorkerTasksPanel` does, `showing R of T`, and only when `next_cursor` is
  non-empty or `R < total`.
- Query key: `['reservations', 'worker', workerId]`. The prefix is deliberate -
  `useReservationActions` invalidates the bare `['reservations']` prefix, so an
  admin's create or delete refreshes this panel too. The second element cannot
  collide with the admin key `['reservations', sort, cursor]` because `'worker'`
  is not a member of the `ReservationSort` union; that is checked, not assumed,
  because a collision would silently serve one panel the other's page.
- No `refetchInterval`, matching `useReservations`: reservations change only when
  an admin changes them. Freshness of the STATUS pill comes from the `useNow`
  tick, which issues no request.
- No `enabled` gate is needed and none is added. Unlike `useWorkerTasks`, this
  hook is mounted by a component that renders **below** the page's loading and
  not-found early returns and inside the admin branch, so a 404 worker and a
  non-admin viewer both mount nothing and issue nothing. That is asserted, not
  assumed; see AC-4 and AC-5.

**4. What the footer says, and why the hi-fi's footnote survives verbatim.**

The panel's footer carries two lines:

- The hi-fi's own line, kept as it is already rendered on the page today:
  selectors are informational in v1, only `worker_ids` are enforced. It is the
  panel's **correctness statement**, not a caption. `?worker_id=` filters on array
  containment alone, so a selector-only reservation is absent from this list; the
  footnote is what tells the reader that the absence is correct rather than a
  missing row. Removing it would turn a true panel into a misleading one.
- When one or more rows derive to `ACTIVE`: a line saying that the scheduler skips
  this worker while any reservation is active. It says nothing about whether the
  worker is otherwise dispatchable - status, disabled flag, labels and free slots
  are all separate gates - and it is absent entirely when no row is active, rather
  than issuing an all-clear the page cannot back.
- The panel's `meta` becomes the endpoint and its filter. The current
  `RESERVATIONS ENDPOINT PENDING` string and the two placeholder lines under it
  are deleted.

**5. Empty, loading and error states.**

Copied structurally from `WorkerTasksPanel`, including the reason its comment
gives: the loading line, the error banner and the empty line are **siblings** of
the `role="table"` subtree, never children, because none is a valid child of a
table and the header row must remain present in every state. Empty copy: no
reservation targets this worker. The error banner carries the message and a Retry,
as the tasks panel's does.

**6. The layout gate this panel cannot have, and the human pass that replaces
it.**

`/workers/:id` is unreachable in the harness (refutation 13). This lane adds **no
e2e surface for it** and does not attempt to fabricate one. Fabricating a worker
would mean intercepting four endpoints and rendering a page made entirely of mock
data, which is a different thing from `injectScheduleFailure` rewriting one field
of one real row, and `surfaces.ts` states the rule that makes that distinction.

What the lane provides instead:

- The arithmetic budget, written in the component beside its `MIN_W` constant: a
  200 pixel fixed-track sum under a 460 pixel minimum, with both flexible cells
  truncating, which is the precondition the `Table` comment states for the budget
  to hold. **Stated as arithmetic.** No comment in this lane may describe it as
  measured.
- A jsdom structural pin that the new table joins the page's existing
  panel-title guard (AC-7), which catches a renamed title or a dropped panel but
  says nothing about pixels, because jsdom performs no layout.

**The human pass, stated as a merge requirement rather than a nicety.** Before
this lane merges, one person runs a local server with a real agent enrolled and a
real reservation naming that worker, opens `/workers/:id` at 320, 375 and 1280,
and reports three things: whether the right column's stack still fits the fold at
1280, whether the new table's horizontal scroller clips a reservation name to
nothing at 320, and whether the ACTIVE row is legible against the panel's
background. The first two are what the harness cannot see - a document-overflow
gate cannot tell "fits" from "clipped behind a scroller", which the e2e README
states as its own known limit. If nobody runs that pass, the honest report is
that the populated worker detail page remains unmeasured at every width, which is
already true at HEAD and which
`docs/backlog/idea-2026-09-02-measure-the-populated-worker-detail-panels.md`
already records.

### Item 2: the login and register user object

**7. Does `applyAuth` trust the body's `user`, and what happens when it is
absent?**

- Options: (a) use `res.user` unconditionally; (b) use it when present and fetch
  `/users/me` otherwise; (c) keep fetching `/users/me` always and ignore the new
  field.
- Chosen: **(b)**.
- Why not (a): a client that has no fallback breaks against an older server, and
  the SPA is served by the binary but a browser tab can outlive a deploy in the
  other direction - a tab loaded from a new binary can be pointed at an older one
  through a proxy, and the failure mode of (a) is a signed-in session with a null
  user, which renders a profile header off an undefined row. The fallback costs
  one branch.
- Why not (c): the round trip is the whole point of the item, and it is not free
  - it is a second request on the critical path of every sign-in, and it widens
  the window between `setToken` and `setStatus('authenticated')` during which the
  session is half-established.
- The guard is a **shape check, not a presence check**: the body's user is used
  only when it is an object whose `id` and `created_at` are non-empty strings.
  Those two are chosen deliberately - `id` is what every downstream identity
  comparison uses, and `created_at` is what the profile header renders. An absent
  `created_at` produces an invalid date rendered as text, which is a silently
  wrong page rather than an error. Anything else falls back to `/users/me`,
  which is the fail-closed direction: the worst case is the round trip we have
  today.
- **The guard's shape is the original defect's shape.** The failure this fallback
  exists for is an absent field, so the check tests for an absent field. It does
  not test for a network error or a non-2xx, because those are already thrown by
  `apiFetch` before `applyAuth` runs.
- `applyAuth` reuses `applyUser`, as the item's own 2026-08-13 note asks, rather
  than reaching for `setUser` a second way.

**8. Does `/users/me` stay the identity source on reload?**

Yes, and nothing about that path changes. `AuthProvider`'s mount effect still
reads the token and hydrates from `GET /users/me`, and that stays the single
source of identity for every session that is resumed rather than created. The
change is narrowly to the two paths that have just been handed an authoritative
row by the server that issued the token. Stated plainly so a future reader does
not conclude the SPA has two identity sources: it has one, plus one hand-off at
the moment of issue.

**9. What this does to the cross-generation 401 fix, and what it does not.**

- `AuthProvider`'s `onUnauthorized` listener is **not touched**. Its identity
  fence compares the token a request carried against `getToken()` read fresh, and
  the comment explaining why it reads fresh rather than from a React-committed
  mirror cites `applyAuth`'s window: the token is stored and only then is
  `/users/me` awaited. With a body-carried user that window closes to nothing for
  the fast path - which makes the fence's job easier, never harder, and changes
  no comparison it performs.
- The comment in question stays accurate for the crossgen file, whose fixtures
  carry no `user` and therefore still take the awaiting path. **A future edit that
  adds `user` to those fixtures makes that comment stale**, so this spec records
  the coupling rather than leaving it to be rediscovered.
- `archived_at` stays unmodelled in `web/src/lib/types.ts`, and the reason is
  re-derived for the new producer rather than inherited: `handleLogin` refuses an
  archived user before issuing a token, and both register arms create the row they
  return, so the field can only ever be `null` on the two new producers exactly as
  it can on `GET /users/me`. The existing comment's argument transfers; the check
  is recorded here so nobody has to take that on trust.

### Item 3: the RegisterScreen config fetch

**10. Hook or guard-only?**

- Chosen: **the hook**, on the reconciliation of refutation 8. `RegisterScreen`
  consumes `useServerConfig()` directly, not a wrapper over it. A wrapper would be
  a third module for one boolean.
- The screen derives its state from the query rather than mirroring it into
  `useState`: while the query is pending and has not errored, the current blank
  placeholder renders; on error, `selfRegister` is `false`, which shows the invite
  field; on success it is `data.allow_self_register`. There is no `setState` after
  an await left anywhere, so the missing cancellation guard is not fixed - it
  ceases to exist, which is what the item's second acceptance criterion allows for
  in its parenthetical. The test says so explicitly rather than asserting a
  no-op.
- The `useEffect` and the `useState<boolean | null>` are both deleted, along with
  the `apiFetch` and `ConfigResponse` imports if they become unused.
- **The `autoFocus` comment must be re-read, not copied.** It explains that the
  form renders on a later commit than the component because the early return holds
  until `/config` resolves, and that this is what discriminates the attribute from
  a mount effect. That remains true through the hook - the early return is still
  there and still holds - so the comment stands and the focus test stays green.
  It is called out because it is the kind of comment a refactor invalidates
  silently.
- Both call sites gain a one-line comment naming the other, which the item asks
  for in its "two with an explicit comment" branch. Having one client does not
  make the divergent **policies** self-evident, and the policies are the part a
  future editor gets wrong.

**11. Retry, and the one test-harness edit.**

- The shared client's `retry: 1` is inherited, not overridden (refutation 10).
- `RegisterScreen.test.tsx`'s `renderRegister` helper constructs a bare
  `new QueryClient()`, which retries three times with backoff. The new
  fail-closed test would take seconds and be timing-sensitive. The helper gains
  `{ defaultOptions: { queries: { retry: false } } }`. This is inert for all five
  existing tests, every one of which answers `/config` successfully, and no
  existing assertion changes. That is the **only** edit to an existing test file
  in this item, and it is a harness change with a zero-assertion diff.

### Item 4: the job detail resizable split

**12. Percent or pixels, and which pane is sized?**

- The hi-fi sizes the **right** pane in **pixels**. This lane sizes the **left**
  pane in **percent** and lets the right pane take the remainder.
- Why percent: the item asks for `aria-valuenow` / `aria-valuemin` /
  `aria-valuemax`, and those three are only meaningful together if the range is
  stable. A pixel range whose maximum is the container width minus a constant
  changes on every window resize, so a persisted pixel value restored into a
  narrower window is out of its own range. A percent range is 30 to 70 always,
  survives a resize, survives a restore, and reads correctly aloud.
- Why the left pane: one number to persist, one number in `aria-valuenow`, and
  the right pane needs no width at all. Sizing both is two numbers that can
  disagree.
- Range: minimum 30, maximum 70, default **55**, which reproduces today's ratio
  exactly, so a user with nothing persisted sees a byte-identical layout.
- Below the large breakpoint the two panes stack and there is no split. The
  separator is hidden there with a breakpoint-prefixed display utility, and no
  width is applied. A separator that resizes nothing is a dead control.
- The percentage reaches CSS as a **custom property set in an inline style on the
  split container**, consumed by a breakpoint-prefixed arbitrary-value width
  utility written as a literal in the component. Two consequences, both
  deliberate: the utility is a literal so Tailwind v4's static scan emits it, and
  the value only applies at and above the breakpoint, which an inline width could
  not express.

**13. Where the persisted value lives, and the relationship to lane JF.**

- Refutation 11 rules out `usePersistedChoice`. This lane adds
  `web/src/jobs/splitWidth.ts` (pure: the constants, `clampSplit`,
  `parseStoredSplit`, `splitFromPointer`) and `web/src/jobs/useSplitWidth.ts` (the
  React state, the storage read in a lazy initializer, and an explicit persist
  call). Both live under `web/src/jobs/`, not `web/src/lib/`, so the lane shares no
  file with JF and can be written, reviewed and merged in any order relative to
  it.
- **It is not filed as a third divergent copy of a persisted preference.** The
  house rule the JF item cites is to extract before the third consumer; this is
  the **first** numeric persisted value in the app, and the second one should
  drive the extraction, on its own evidence. A follow-up proposal records that so
  the decision is revisited rather than forgotten.
- Storage key `relay.jobs.detailSplit`, matching the existing key style. The read
  is inside a `try` and validates that the parsed value is an integer inside the
  range; anything else yields the default. The write is inside a `try` so a
  storage failure loses the preference and does not lose the drag.
- The preference is per user, not per job. A layout choice is about the reader,
  not the row.

**14. Keyboard, ARIA and the separator's own semantics.**

- `role="separator"` with `tabIndex={0}`, `aria-orientation="vertical"`
  (`separator`'s implicit orientation is horizontal, and this one runs vertically
  between two horizontally-arranged panes, so it must be stated),
  `aria-valuenow` / `aria-valuemin` / `aria-valuemax` as integers, an
  `aria-label` naming what it resizes, and an `aria-valuetext` reading as two
  percentages with the two pane names - a bare number announces nothing about
  which side grows.
- Keys: Left and Right adjust by a step of 2; Home and End jump to the minimum
  and the maximum. Each calls `preventDefault` so the page does not scroll or
  navigate under the press. Up and Down are **not** bound: this separator is
  vertical, and binding the cross-axis keys would make the announced orientation a
  lie.
- The `title` attribute the hi-fi uses is kept for the pointer affordance, in
  addition to the accessible name, not instead of it.

**15. Pointer drag, and the invariant it has to respect.**

- Pointer Events (`pointerdown` / `pointermove` / `pointerup` / `pointercancel`),
  not the hi-fi's mouse events, so touch and pen work.
- **Window listeners, not `setPointerCapture`.** The capture API is the tidier
  shape and jsdom's support for it must be checked before it is relied on; rather
  than write two code paths behind a capability check, this lane takes the one
  path the hi-fi already proves and jsdom can drive. The implementer must still
  confirm the pointer-event constructors are available in the installed jsdom
  before writing the test in AC-17, and must say so if they are not, rather than
  substituting a click.
- **The generation-ordering invariant has a live subject here.** The listeners are
  the resource and the drag flag is the generation. Three rules: the flag is
  cleared in the same function that removes the listeners; `pointercancel` is
  handled as well as `pointerup`, or a cancelled drag (a context menu, a
  browser gesture) leaves the listeners armed and the next pointer move keeps
  resizing with no button held; and the effect that owns the listeners removes
  them on unmount, or a drag interrupted by a navigation writes state into an
  unmounted component. Handling only `pointerup` is the mutation AC-18 kills.
- `userSelect` is set on the document body for the duration of the drag and
  restored on end, as the hi-fi does, and restored on the unmount path too.
- **Persistence is committed once per gesture, not per move.** A pointer move
  fires at the display refresh rate; writing storage on each one is dozens of
  synchronous writes a second. State updates on move, storage is written on
  `pointerup` and `pointercancel`, and on each key press (one press is one
  gesture). AC-19 pins the write count.

**16. What the browser has to assert, and what jsdom cannot.**

jsdom performs no layout: every `getBoundingClientRect` is zero, so a pointer
position cannot be converted to a percentage there and a column's width cannot be
read back. The split therefore has two test halves:

- jsdom owns the **pure arithmetic** (clamp, parse, pointer-to-percent given an
  injected rect), the **ARIA structure**, the **key handling**, the **persistence
  round trip**, the **write count**, and the **listener teardown**.
- The browser owns the **only** assertions that require layout: that a key press
  moves a real column, that a real mouse drag moves it in the right direction,
  that the clamp holds against a real drag past the edge, that the value survives
  a real reload, and that the separator is absent from the accessibility tree at a
  narrow viewport, which no jsdom test can see because no stylesheet is loaded
  there.

The new browser cases go in `web/e2e/keyboard.spec.ts`, which already carries a
`job-detail` describe against the existing populated `job-detail` surface, tagged
so both engines run it. No new entry in `surfaces.ts` is needed: `layout.spec.ts`
already measures `job-detail` at 320, 375 and 1280, and the separator does not
render at the first two.

## Design

### Files

New:

- `web/src/workers/WorkerReservationsPanel.tsx` and its test. Exports
  `WORKER_RESERVATIONS_PANEL_TITLE`.
- `web/src/workers/useWorkerReservations.ts` and its test.
- `web/src/jobs/splitWidth.ts` and `splitWidth.test.ts` - pure.
- `web/src/jobs/useSplitWidth.ts` and `useSplitWidth.test.ts`.
- `web/src/jobs/JobDetailPage.split.test.tsx` - the separator's structure, keys
  and persistence in the page.
- `web/src/auth/AuthProvider.userobject.test.tsx` - the body-carried user, the
  fallback, and the malformed-user fallback.

Changed:

- `web/src/admin/reservations/api.ts` - one optional trailing `worker_id` on
  `ListReservationsParams`, set only when non-empty.
- `web/src/admin/reservations/api.test.ts` - two added cases, no existing case
  edited.
- `web/src/workers/WorkerDetailPage.tsx` - the placeholder panel's contents.
- `web/src/workers/WorkerDetailPage.test.tsx` - three tests edited, see the gate.
- `web/src/lib/types.ts` - `user?: User` on `LoginResponse`.
- `web/src/auth/AuthProvider.tsx` - `applyAuth`.
- `web/src/auth/RegisterScreen.tsx` - the hook.
- `web/src/auth/RegisterScreen.test.tsx` - the harness retry setting, one added
  test.
- `web/src/admin/server/useServerConfig.ts` - one comment naming the second
  consumer.
- `web/src/jobs/JobDetailPage.tsx` - the split container, the separator, the two
  pane widths, and the deletion of the backend-blocked comment.
- `web/e2e/keyboard.spec.ts` - one added describe and one narrow-viewport case.

### Component shapes

```
WorkerDetailPage (right column, admin branch)
 \- Panel title=WORKER_RESERVATIONS_PANEL_TITLE meta="GET /v1/reservations?worker_id="
     \- WorkerReservationsPanel workerId now
         |- Table label=WORKER_RESERVATIONS_PANEL_TITLE
         |   \- rows: NAME | PROJECT | STATUS chip | ENDS
         |- loading line        (sibling of the table)
         |- error banner + Retry(sibling)
         |- "showing R of T"    (sibling, only on a short page)
         |- active-exclusion line (sibling, only when a row is ACTIVE)
         \- the hi-fi footnote  (sibling, always)
```

```
JobDetailPage body
 \- div  (stacked below the breakpoint, a row above it; inline style sets the split custom property)
     |- left pane   width from the custom property at the breakpoint and above
     |- separator   role=separator, tabIndex 0, hidden below the breakpoint
     \- right pane  flexible remainder, zero minimum
```

### Data flow

`useWorkerReservations(workerId)` wraps `useQuery` on
`['reservations', 'worker', workerId]` calling
`listReservations({ sort: '-created_at', cursor: '', workerId })`. No interval,
no placeholder data - the key changes only when the route id does, and there is no
previous page worth showing under a different worker's name.

`useSplitWidth()` returns `{ width, setWidth, persist }`. `width` is initialised
from storage through `parseStoredSplit`; `setWidth` clamps and sets state;
`persist` writes the current value inside a `try`. The page's key handler calls
`setWidth` then `persist`; the pointer handler calls `setWidth` on each move and
`persist` once on end.

### Accessibility

- The new table is named by its panel title through the same shared `Table`
  primitive as its two siblings, so it inherits the scroll wrapper's group role
  and label, which is what makes its clipped columns keyboard-reachable.
- The STATUS chip carries a word, not only a colour.
- The separator is a focus stop with a full ARIA value range and a value text
  naming both panes.
- Nothing else on either page changes tab order.

### Load, failure and threat

- **Load.** One extra request per admin visit to a worker detail page,
  unpolled. The containment predicate is a scan and is not index-served, which
  the README states; the scan is over the reservations table, which is small by
  construction (it is created by hand by admins) and is bounded further by
  `limit=50`. The login change **removes** a request from every sign-in. The
  RegisterScreen change makes the sign-up path's config fetch cache-shared with
  the admin tab and adds at most one retry. The split adds no request at all.
- **Failure.** The reservations panel fails inside its own panel with a Retry and
  does not disturb the page. A config fetch failure fails closed to the
  invite-required form; the server enforces the invite requirement regardless, so
  a wrong client hint is a nuisance, never a bypass. A malformed or absent login
  user falls back to the existing round trip. A storage failure in the split loses
  a preference.
- **Threat model.** No new endpoint and no new privilege. `GET /v1/reservations`
  is admin-only server-side; the UI gate is a convenience, and a non-admin who
  forced the component to mount would receive a 403 rather than data. The only
  new value on the wire is `worker_id`, which is the route id already in the
  address bar. The panel renders reservation names and projects, both
  admin-authored free text, through React's escaping; nothing is rendered with
  markup interpolation. The auth change causes the login response body to be
  **read** where it was previously ignored - it introduces no new secret, since
  the same object was already fetched from `/users/me` one request later, and the
  token handling is untouched. Nothing in this lane writes to the server.
- **Invariants.** Six of the seven are backend-shaped and untouched. The seventh
  in its frontend form - end the generation before releasing the resource - has
  exactly one live subject in this lane, and it is the drag: Decision 15. The
  401 listener's identity fence is read and left alone, and Decision 9 records
  why the change makes its job strictly easier.

## What must NOT change, as a checkable gate

- **Zero-line diff:** `web/src/auth/AuthProvider.crossgen.test.tsx`,
  `web/src/auth/AuthProvider.session.test.tsx`,
  `web/src/auth/AuthProvider.test.tsx`, `web/src/auth/LoginScreen.test.tsx`,
  `web/src/auth/authTokenSecrecy.test.tsx`,
  `web/src/auth/authArrivalFocus.test.tsx`,
  `web/src/admin/reservations/ReservationsTab.test.tsx`,
  `ReservationsTab.pager.test.tsx`, `ReservationsTable.test.tsx`,
  `useReservations.test.tsx`, `web/src/admin/server/*.test.tsx`,
  `web/src/workers/WorkerTasksPanel.test.tsx`,
  `web/src/workers/WorkspacesPanel.test.tsx`,
  `web/src/jobs/JobDetailPage.test.tsx`, and every existing case in
  `web/src/admin/reservations/api.test.ts`.
  `AuthProvider.test.tsx` and `AuthProvider.crossgen.test.tsx` staying green
  **unedited** is the load-bearing one: their login fixtures carry no `user`, so
  they are the fallback's own regression suite (refutation 17).
- **Exactly four existing tests change, and each for a stated reason:**
  - `WorkerDetailPage.test.tsx`, `the reservations panel contains no fabricated
    reservation rows`. Its subject - a placeholder that fabricates nothing -
    is gone. It is **rewritten, not deleted**, and keeps its point: with an empty
    reservations page the table count becomes three and the row count is three
    header rows, so a fabricated row still shows up as a fourth. Its name changes
    to say what it now pins.
  - `WorkerDetailPage.test.tsx`, `admins see the action bar, the Source
    workspaces panel, and the reservations placeholder`. The placeholder
    assertion becomes an assertion about the real panel.
  - `WorkerDetailPage.test.tsx`, `every table on the page is named by its own
    panel title`. The count goes from two to three. The count assertion is the
    part that must not be softened - its own comment explains that without it the
    loop passes vacuously.
  - `WorkerDetailPage.test.tsx`, `non-admins see none of the action controls and
    never fetch workspaces`. The absent placeholder string becomes the absent
    panel title, plus a request count of zero on the reservations route.
- `RegisterScreen.test.tsx`'s helper gains a retry setting with no assertion
  change (Decision 11). That is a harness edit, not a behaviour edit.
- Any other edit to an existing test is a behaviour change and must be justified
  in review, not absorbed.

## Acceptance criteria

Each names its test and the mutation that test kills. MSW fixtures are
hand-written object literals carrying every key the server sends, and none is
marshalled through the app's own response type - a fixture built from
`ReservationsPage` or `LoginResponse` agrees with the decoder by construction and
can never detect drift in either direction.

| # | Criterion | Test | Mutation it kills |
|---|---|---|---|
| AC-1 | `listReservations` sends `worker_id` when given one and omits the parameter entirely when not, leaving `sort`, `limit` and `cursor` byte-identical | `admin/reservations/api.test.ts` - two added cases | send an empty `worker_id`; send it unconditionally |
| AC-2 | The panel renders one row per reservation with the name, the project, the derived status word and the end time, from a hand-written fixture carrying every server key including a row with `project`, `starts_at` and `ends_at` **absent** | `WorkerReservationsPanel.test.tsx` - "renders a row per reservation" | read `ends_at` as `null` rather than absent; drop the status column |
| AC-3 | An absent `ends_at` renders the no-end token, not a hyphen and not the string `undefined` | same file - "an open-ended reservation says so" | render the shared absent-value hyphen |
| AC-4 | A non-admin mounts no panel and issues **zero** requests to the reservations route (counted by a handler, not inferred) | `WorkerDetailPage.test.tsx` - the edited non-admin test | move the panel outside the admin branch |
| AC-5 | A 404 worker issues zero requests to the reservations route | `WorkerDetailPage.test.tsx` - added to the existing poll-stop test's file as its own case | hoist the hook above the not-found early return |
| AC-6 | An empty page renders the empty line, the header row, and the footnote - and no fabricated row | `WorkerReservationsPanel.test.tsx` - "an empty result says no reservation targets this worker" | render a placeholder row |
| AC-7 | Every table on the worker detail page is named by its own panel title, and there are exactly **three** | `WorkerDetailPage.test.tsx` - the edited structural test | rename the panel title without renaming the table label |
| AC-8 | A short page states `showing R of T`; a complete page states nothing | `WorkerReservationsPanel.test.tsx` - "a short page says so" | compare `next_cursor` only, or drop the footer |
| AC-9 | The active-exclusion line appears when a row derives ACTIVE and is absent when every row is ENDED or SCHEDULED, with `now` injected as a fixed date | same file - "an active reservation states the dispatch consequence" | render the line unconditionally |
| AC-10 | The hi-fi footnote about selectors renders in every state, including the empty one | same file - "the selector footnote is always present" | move the footnote inside the rows branch |
| AC-11 | `login` sets the user from the response body and makes **no** `GET /users/me` request; the test registers a counting handler and asserts zero | `AuthProvider.userobject.test.tsx` - "login uses the user object in the body" | keep the unconditional round trip |
| AC-12 | A login body with **no** `user` key still authenticates, by falling back to `/users/me` | same file - "an older server without a user object still signs in" | drop the fallback |
| AC-13 | A login body whose `user` lacks `created_at` (or `id`) falls back rather than committing a partial row | same file - "a malformed user object falls back" | guard on presence of `user` alone |
| AC-14 | `register` takes the same path as `login` for both branches | same file - "register uses the user object too" | apply the change to `login` only |
| AC-15 | `RegisterScreen` shows the invite field when `/config` fails, and issues no state update after unmount because there is no post-await `setState` left to make | `RegisterScreen.test.tsx` - added "a failed config fetch shows the invite field" | derive `true` on error; restore the raw effect |
| AC-16 | The separator exposes `role="separator"`, a vertical orientation, a tab stop, and value/min/max that agree with the rendered split; Left, Right, Home and End each move `aria-valuenow` by the documented amount and clamp at both ends | `JobDetailPage.split.test.tsx` - "the separator exposes its range and responds to keys" | drop the clamp; bind the cross-axis keys; omit the orientation |
| AC-17 | A pointer drag with an injected container rect moves the split in the direction of travel and clamps | `useSplitWidth.test.ts` plus `splitWidth.test.ts` - "a pointer position maps to a clamped percentage" | invert the sign; drop the clamp |
| AC-18 | A `pointercancel` ends the drag exactly as `pointerup` does, and unmounting mid-drag removes the listeners | `useSplitWidth.test.ts` - "a cancelled drag disarms" and "unmount disarms" | handle `pointerup` only; drop the unmount cleanup |
| AC-19 | Storage is written **once** per gesture: once per key press, once per completed drag regardless of how many moves it contained | `useSplitWidth.test.ts` - "persistence is once per gesture" | persist inside the move handler |
| AC-20 | A stored value that is absent, unparseable, non-integer or outside the range yields the 55 default, and a throwing storage read does not throw out of the hook | `splitWidth.test.ts` and `useSplitWidth.test.ts` - four cases | accept any stored number; remove the try |
| AC-21 | Browser: a real Tab press reaches the separator, ArrowRight increases `aria-valuenow` **and** the tasks column's measured width | `web/e2e/keyboard.spec.ts` - "a key press moves a real column" | update the ARIA value without applying the width |
| AC-22 | Browser: a real mouse drag moves the boundary in the direction of travel, and a drag far past the edge stops at `aria-valuemin` with the column width stopping too | same file - "a real drag moves and clamps the split" | remove the clamp from the pointer path only |
| AC-23 | Browser: the value survives `page.reload()` | same file - "the split survives a reload" | drop the write |
| AC-24 | Browser: at a 375 pixel viewport the separator resolves **zero** times, because the panes are stacked | same file, its own narrow-viewport describe - "no separator where there is no split" | render the separator at every width |
| AC-25 | Zero files changed under `internal/`, `cmd/`, `python/`; exactly the four existing-test edits listed above and no others | review, plus the full `web/src` suite green | - |

## Sequencing

Four commits, each independently green under `npm test`, `npx tsc -b --force` and
`npm run build`. The order is chosen so the two that need no browser land first.

1. **Auth user object.** `web/src/lib/types.ts`, `AuthProvider.tsx`, one new test
   file. Touches nothing else. AC-11 through AC-14.
2. **RegisterScreen onto the hook.** `RegisterScreen.tsx`, its test,
   `useServerConfig.ts`'s comment. Touches nothing commit 1 touched except the
   `web/src/auth/` directory. AC-15.
3. **Worker reservations panel.** `admin/reservations/api.ts` and its test, two
   new `workers/` files and their tests, `WorkerDetailPage.tsx` and its test.
   AC-1 through AC-10.
4. **Job detail resizable split.** Four new `jobs/` files, `JobDetailPage.tsx`,
   `web/e2e/keyboard.spec.ts`. AC-16 through AC-24.

`make test-e2e` runs once, after commit 4. Commits 1 to 3 add no browser
assertion, so running it earlier measures nothing new; say that in the PR rather
than reporting a lane that was not the subject.

**Parallelism.** All four commits can proceed concurrently with lane JF: the file
sets are disjoint (refutation 12), including the e2e directory, where JF edits
`surfaces.ts` and this lane edits `keyboard.spec.ts`. Lane SF's file set was not
available to this spec; before starting, check it against this lane's changed list
above, and raise rather than merge blind if it names
`web/src/admin/reservations/api.ts`, `web/src/auth/AuthProvider.tsx` or
`web/e2e/keyboard.spec.ts`.

## Gates

- `cd web && npm test`
- `cd web && npx tsc -b --force`
- `cd web && npm run build`
- `make test-e2e`, after commit 4 only. Needs Docker Desktop running and a
  Postgres at `postgres://relay:relay@127.0.0.1:5432` - `docker start
  relay-postgres`, or `scripts/dev.ps1` once to create it. Browsers installed once
  with `cd web && npx playwright install chromium webkit`. Run it from Git Bash.
  If `make` is not on PATH, use the MSYS2 copy with the variable forwarding
  `web/e2e/README.md` documents.
- `git checkout -- web/dist/` before assembling the PR. `web/dist` is tracked but
  not maintained per-PR, and `make test-e2e` writes into it.
- The human layout pass of Decision 6, reported in the PR with its three answers.
  If it was not run, say so plainly.
- No Go gate is required or run: this lane changes no Go file. Say that in the PR
  rather than reporting a Go lane that was not the subject.

## Risks

- `WorkerDetailPage.tsx` is the file commit 3 edits, and it is the same file the
  previous batch's tasks-panel lane edited. Its test file carries four assertions
  about the placeholder that is being removed; a merge that resolves the source
  textually without re-running that file will leave a green-looking suite around a
  page that no longer matches it.
- The split's browser cases are the first in this repo to drive a real mouse drag.
  If `page.mouse` interaction proves flaky against the split at the default
  viewport, the honest outcome is to keep AC-21, AC-23 and AC-24 and report AC-22
  as not achieved, **not** to weaken it into a click.
- The reservations panel ships with no measured layout evidence at any width. That
  is stated in Decision 6 and it is the lane's largest residual.
- The installed jsdom's pointer-event support is unverified in this spec.
  Decision 15 requires the implementer to check it before writing AC-17 and AC-18,
  and to say so if it is missing rather than substituting a weaker event.

## Backlog items this closes

Closing each is required scope, through `/backlog close <fragment>`, which does
the `git mv` into `docs/backlog/closed/`. A hand-edited `status` leaves the file
in the open directory and `/backlog list` reports it malformed.

1. `feature-2026-06-05-worker-detail-reservations-panel` - note that the
   server-side filter branch of the proposal is what shipped, that the
   client-side-filter alternative is now dead, and that the panel shows
   reservations naming this worker in `worker_ids`, not everything a reader would
   say applies to it.
2. `idea-2026-06-03-login-return-user-object` - note that the server half shipped
   in lane SB, that the wire shape has a sixth key the item never listed
   (`archived_at`), that `/users/me` remains the identity source on reload, and
   that an absent or malformed `user` falls back to the round trip.
3. `idea-2026-08-09-registerscreen-config-fetch-unify` - note that the item's
   either-or was refuted (one client, two consumer policies), that its
   fail-closed-on-error acceptance criterion described a test that did not exist
   and one was added, and that the cancellation guard was removed rather than
   fixed because the post-await state update ceased to exist.
4. `idea-2026-07-01-job-detail-resizable-split` - note the three deliberate
   deviations from the hi-fi: percent rather than pixels, the left pane sized
   rather than the right, and the whole accessibility surface added rather than
   quoted. Note also that it did **not** wait for the shared-primitives
   extraction it pairs with.

Explicitly **not** closed:
`idea-2026-09-02-measure-the-populated-worker-detail-panels` stays open, and this
lane makes its subject larger rather than smaller.

## Proposed follow-up backlog items

Proposals only. The human accepts or drops each; none is filed by this lane.

1. **Amendment, not a new item - `idea-2026-09-02-measure-the-populated-worker-detail-panels`.**
   Its summary names the current-tasks panel as the unmeasured addition. The
   reservations panel is now a second one in the other column. Amend the item to
   name both, and to record refutation 14: the risk on this page is height and
   in-scroller clipping, which the existing gate cannot measure, not document
   width, which it can.
2. **idea - extract a guarded numeric persisted-value hook.** This lane ships the
   first numeric one under `web/src/jobs/`; lane JF ships the first string one
   under `web/src/lib/`. When a second numeric consumer appears, the two should be
   reconciled on that consumer's evidence (Decision 13).
3. **idea - the reservations containment filter is an unindexed scan.** The README
   says so and suggests a GIN index on `worker_ids`. The worker detail page now
   issues one such scan per admin page view, which is the first automatic consumer
   of it.
4. **idea - `deriveStatus` reads the browser clock, and now does so on two
   pages.** A badly skewed browser mislabels a row on the admin table and now
   also states a dispatch consequence on the worker page. The server exposes no
   reservation status to prefer; if one is ever added, both consumers switch.
5. **idea - a worker excluded by an active reservation still shows a Slots KPI
   that reads as idle.** `0 / 4` on a reserved worker is true and misleading. The
   KPI could carry the reservation state, which is a design question this lane
   deliberately did not answer inside a panel.
6. **idea - the split resizer is a candidate shared primitive.** The closed item
   pairs with `idea-2026-06-26-shared-holo-design-primitives`; the schedule detail
   page is the obvious second consumer and does not exist yet in split form.
7. **idea - no e2e coverage exists for `/register`.** The e2e README lists it as
   one of five unvisited pages, and this lane changes its data path without any
   browser assertion over it.

## Escalations

Calls a human might reasonably make the other way.

1. **Render the reservations panel as the hi-fi's row stack rather than a table**
   (Decision 1). It matches the design handoff exactly and keeps the page's table
   count at two, at the cost of leaving the panel outside the structural
   panel-title guard and giving a screen reader no column names. I chose
   consistency with the two sibling panels on the same page.
2. **Do not add the panel at all until the harness can render a populated worker
   detail page** (Decision 6). It is the only option that never ships an
   unmeasured layout onto the page with the least headroom. I judged that a panel
   the page already renders as a placeholder, whose new table cannot widen the
   document by construction, does not justify blocking on a harness slice that has
   its own backlog item.
3. **Use the body-carried user unconditionally, with no fallback** (Decision 7).
   It is simpler and the server always sends the field today. I kept the fallback
   because it is one branch and it is what keeps two existing test files
   byte-identical, which is itself the regression evidence.
4. **Keep two `/v1/config` call sites and add only the cancellation guard**
   (Decision 10), which is the item's own escape hatch. It keeps the sign-up path
   off TanStack entirely and avoids inheriting the shared client's retry. I judged
   the divergence the real cost.
5. **Persist the split in pixels like the hi-fi** (Decision 12). Pixels are what
   the design specifies and what a user's muscle memory is about. Percent wins on
   the ARIA range being stable across resizes, and a human who weighs fidelity to
   the handoff more heavily would choose pixels and a resize-recomputed maximum.
6. **A split range other than 30 to 70, or a step other than 2** (Decision 12 and
   14). These are the whole feel of the control in two numbers.
7. **Use pointer capture rather than window listeners** (Decision 15), accepting a
   capability check and possibly a second code path in jsdom. It is the tidier
   API and would remove the unmount-cleanup question entirely.
