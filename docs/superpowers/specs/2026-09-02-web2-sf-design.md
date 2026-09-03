# Lane SF: the Schedules page summary strip, filters, search and LAST JOB link

Date: 2026-09-02
Branch: `claude/web2-sf-schedules-frontend`
Worktree: `.claude/worktrees/web2-sf`, at `origin/main` (which carries lane SB's
merged `GET /v1/scheduled-jobs/stats`, `?enabled=`, `?q=` and `last_job_status`)
Author: relay-tpm (autonomous gate mode; no human answered questions during this
flow, so every question in the Decisions section was decided here, and the calls a
human might reasonably make the other way are listed under Escalations)

## Why this lane exists

Four Schedules-page features were deferred on 2026-06-05 because the server could
not express them. `SchedulesPage.tsx` still carries a comment block saying so, and
naming the three backlog items by path. Lane SB shipped the server half of all
four. This lane is the frontend half. **Nothing under `internal/`, `cmd/` or
`python/` changes.**

Backlog items in scope:

- `docs/backlog/idea-2026-06-05-schedules-stats-endpoint.md`
- `docs/backlog/idea-2026-06-05-failed-24h-stat.md`
- `docs/backlog/idea-2026-06-05-last-job-link-status.md`
- `docs/backlog/idea-2026-06-05-schedules-filter-search.md`

## The hi-fi, quoted

The previous batch's retro recorded a spec that rejected a hi-fi mechanism it had
never read, so every claim below about the design handoff is a quotation from
`design_handoff_relay_holo/hifi3-holo-pages.jsx` (`HoloSchedules`), not a
paraphrase of a backlog item. Two substitutions keep this file ASCII and are the
only alterations: each non-ASCII pictograph is replaced by a bracketed description
of it, and the middot the hi-fi uses inside display strings is replaced by a
hyphen. No structure, property, value or guard is changed.

**The summary strip. Three numbers, and the third is a literal.**

```jsx
const counts = {
  all: SCHEDULES.length,
  enabled: SCHEDULES.filter(s=>s[4]).length,
  disabled: SCHEDULES.filter(s=>!s[4]).length,
};
```

```jsx
<div style={{display:'flex',gap:18,fontFamily:C.mono,fontSize:11,color:C.fgMute,letterSpacing:'0.14em'}}>
  <span><b style={{color:C.ok,fontWeight:600,fontSize:18}}>{counts.enabled}</b> ENABLED</span>
  <span><b style={{color:C.fg,fontWeight:600,fontSize:18}}>{counts.disabled}</b> PAUSED</span>
  <span><b style={{color:C.err,fontWeight:600,fontSize:18}}>2</b> FAILED - 24H</span>
</div>
```

**The toolbar row: three chips carrying counts, a search input pushed right, and
the sort control.**

```jsx
<div style={{display:'flex',gap:8,alignItems:'center',flexWrap:'wrap'}}>
  {[['all','All'],['enabled','Enabled'],['disabled','Disabled']].map(([k,n])=>(
    <button key={k} onClick={()=>setFilter(k)} style={{
      padding:'6px 14px', borderRadius:999, fontFamily:C.sans, fontSize:12, cursor:'pointer',
      background: filter===k?`linear-gradient(90deg, ${hexToRgba(C.accent,0.25)}, ${hexToRgba(C.accentB,0.18)})`:'rgba(255,255,255,0.04)',
      border:`1px solid ${filter===k?C.accent+'66':C.border}`,
      color: filter===k?C.fg:C.fgMute,
      backdropFilter:'blur(8px)',
    }}>{n} <span style={{color:C.fgDim,marginLeft:4}}>{counts[k]}</span></button>
  ))}
  <input placeholder="Filter by name, owner, cron..." style={{
    marginLeft:'auto', minWidth:240, padding:'7px 14px',borderRadius:999,
    background:'rgba(0,0,0,0.25)',border:`1px solid ${C.border}`,
  }}/>
  <SortControl C={C} options={SCHED_SORT} value={sort} onChange={setSort}/>
</div>
```

The filter itself, which is where the tri-state lives:

```jsx
const [filter, setFilter] = React.useState('all');
const rows = SCHEDULES.filter(s => filter==='all' ? true : filter==='enabled' ? s[4] : !s[4]);
```

**The LAST JOB cell. A dot whose colour is derived from the job status, and an
underlined short id, both inside one clickable span.**

```jsx
const jc = jobStatus==='done'?C.ok: jobStatus==='running'?C.accent: jobStatus==='failed'?C.err: C.fgMute;
```

```jsx
<span onClick={()=>onOpenJob && onOpenJob(jobId)} style={{
  display:'inline-flex',alignItems:'center',gap:6,cursor:'pointer',
  color:jc,letterSpacing:'0.04em',
}}>
  <span style={{width:6,height:6,borderRadius:'50%',background:jc}}/>
  <span style={{textDecoration:'underline',textDecorationColor:hexToRgba(jc,0.35),textUnderlineOffset:2}}>{jobId}</span>
</span>
```

For contrast, the row's OTHER dot - the one the shipped table already draws, which
carries `enabled` and not job status:

```jsx
<span style={{width:6,height:6,borderRadius:'50%',background:enabled?C.ok:C.fgDim}}/>
```

**The footer, whose denominator is the whole sample.**

```jsx
<span>SHOWING <span style={{color:C.fg}}>1-{sorted.length}</span> OF <span style={{color:C.fg}}>{counts.all}</span> - SORT <span style={{color:C.accentB}}>{sort}</span> - OWNED + ADMINISTRATIVE</span>
```

What the quotation settles, and what it does not:

- The strip is **one wrapping flex row of mono text**, each number a bold span at
  18px in a status colour, followed by an uppercase label. It is not a panel, not
  a card grid, and not the four-up `KpiStat` row the Jobs page uses.
- The strip has **three entries and no total**; the total lives in the footer.
- The chips are pills carrying **a count each**, in a dimmer colour, after the
  label.
- The search input is pushed right with an automatic left margin and carries a
  **minimum width of 240 pixels**.
- The LAST JOB cell's **only** carrier of job status is the dot's colour, plus the
  same colour applied to the id text. There is no status word anywhere in the row.
- The FAILED number is a **hard-coded literal `2`** in a mock with no data behind
  it, so the hi-fi settles the label and the placement and settles nothing about
  the unit.

## What I verified against the tree, and what I refuted

A backlog proposal is not a contract, and neither is a hi-fi. Every bullet was
checked in this worktree.

1. **Refuted (the stats-endpoint item).** "consume it on SchedulesPage with a
   page-scoped fallback until the first response." The fallback is the wrong call
   *in this lane*, and the reason is created by the other three items shipping at
   the same time. See Decision 2.
2. **Refuted (the last-job-link item).** "so the row can render a colored status
   dot like the Holo design." A colour is not an acceptable sole carrier, and this
   table already has a documented precedent for that exact judgement: the FAILING
   chip's comment in `SchedulesTable.tsx` says "TEXT, NOT A COLOUR. A bare colour
   is not accessible, and the dot's two states are already spoken for by
   `enabled`." A second colour-only dot in the same row would reintroduce what
   that comment rejected, and it would sit four columns away from a dot that means
   something else entirely. See Decision 6.
3. **Refuted (the hi-fi).** The clickable `<span onClick=...>`. `SchedulesTable`
   already carries the counter-example and its reason: the Edit control is a
   `<Link>` "not a useNavigate handler on a button, so middle-click and
   open-in-new-tab work". The LAST JOB cell is a `<Link>` for the same reason.
4. **Refuted (the hi-fi).** The per-chip counts. `/stats` takes no filters, so a
   chip count is a fleet-wide number that ignores `q`. A count printed on a
   control reads as a promise about what clicking it returns, and with a search
   active that promise is false - "Enabled 12" beside a list that says "of 3
   matching". See Decision 4.
5. **Refuted (the hi-fi), on the label only.** `FAILED - 24H`. The strip now
   carries two failure numbers that are counted in different units and over
   different windows, and the README says so in its own words: `failed_runs_24h`
   is over jobs and windowed, `failing` is over schedules and "**Not windowed**".
   Two adjacent uppercase labels that both begin FAIL are the shape of a number
   nobody can read. See Decision 3.
6. **Confirmed, against the expectation set by lane JF.** The hi-fi's
   `placeholder="Filter by name, owner, cron..."` is **accurate here**. The
   README's Filtering the schedules list table gives the `q` axes as the schedule
   `name`, the owner's `email` and the `cron_expr`, and it adds that "The cron
   axis matches the stored text verbatim, so `@daily` is found by `daily`". Lane
   JF had to refute the analogous jobs placeholder because it promised id search
   the server does not do; a reviewer arriving from that spec should not expect
   the same refutation here. The placeholder ships close to verbatim.
7. **Confirmed.** `enabled` is a genuine tri-state, and the Disabled chip is
   therefore a real server filter rather than a client-side no-op.
   `parseScheduleFilters` reads `qs.Get("enabled")`, treats the empty value as
   absent, and otherwise produces a pointer to the parsed bool, with the comment
   "enabled=false is the real request 'only paused schedules', so it must produce
   a pointer to false and never be folded into absent."
8. **Confirmed, and it is the single largest constraint on this lane's test
   diff.** `web/src/test/setup.ts` calls `server.listen({ onUnhandledRequest:
   'error' })`. MSW path matching is exact, so a handler registered for
   `/v1/scheduled-jobs` does **not** answer `/v1/scheduled-jobs/stats`. Adding a
   second query to `SchedulesPage` therefore makes every existing page test emit
   an unhandled request. See Decision 11.
9. **Confirmed.** `web/src/lib/useDebouncedValue.ts` exists, and `UsersTab` uses
   it behind a `debounceMs` prop defaulting to 300 whose comment says the prop
   exists "only so tests can shrink it and stay on real timers". Lane JF adopts
   the same seam on the Jobs page. This lane reuses both rather than inventing a
   third debounce.
10. **Confirmed.** `UsersTab.pickEmail` sets the raw input state and calls
    `pager.resetPaging()` in the same handler. `useCursorPager`'s own doc comment
    already instructs consumers to do this on "a status filter, on
    include_archived or on a debounced search box", and explains why the hook
    deliberately does not watch a sort argument. **No change to
    `useCursorPager.ts` is needed or wanted.**
11. **Confirmed.** `GET /v1/scheduled-jobs` has no rate limit. `RateLimit` appears
    at exactly two mux registrations in `internal/api/server.go`, both on
    `POST /v1/auth/...`. The debounce must not be advertised as a bound anywhere
    in this lane's code, comments or copy.
12. **Confirmed, and it changes the LAST JOB cell's design.** `ScheduleRunsPanel`
    already renders a job status in this exact product area, as a dot plus the
    verbatim status word, using `statusColor` from `web/src/jobs/status.ts`. The
    new cell reuses that helper and that shape rather than inventing a second
    mapping. `statusColor` has a `default` branch, so a status outside the
    five-value union renders muted rather than blank.
13. **Refuted (a tempting assumption about the e2e gate).** Widening the table's
    minimum-width constant cannot make `layout.spec.ts` go red. `surfaces.ts`
    records that measurement directly on the `schedules-failing` surface: widening
    it to 2400 "changes NOTHING here", because `Table` wraps the whole grid in a
    horizontal scroll wrapper, so anything that widens the grid scrolls inside
    that wrapper instead of widening the document. The LAST JOB column widening in
    Decision 6 is therefore **invisible to the gate**, and this spec says so
    rather than claiming coverage it does not have.
14. **Refuted (the current page comment), by construction.** The comment block in
    `SchedulesPage.tsx` says the chips, the search and the FAILED-24H stat are
    "backend-blocked and deliberately omitted", and that the strip is page-scoped
    "until the stats endpoint lands". All four claims are false after this lane.
    The block is deleted, not amended.
15. **Refuted (an existing test's premise).** `SchedulesPage.test.tsx` contains
    `does not render the backend-blocked filter chips, search, or FAILED-24H
    stat`, asserting the absence of an Enabled button, a Disabled button, a
    searchbox role, and any text matching failed-24h. All four assertions become
    false. The test is **deleted**, not narrowed; its replacements are the
    positive assertions in the two new page test files.
16. **Checked and found nothing to reuse.** There is no shared search-input
    component under `web/src`. `UsersTab` inlines its own. Lane JF is concurrently
    inlining a second on the Jobs page. This lane inlines a third and does **not**
    extract, because two concurrent lanes cannot both own the extraction; the
    extraction is proposed as a follow-up item naming all three call sites, so the
    decision is conditioned on something findable rather than on a memory.
17. **Recorded, not acted on.** The README's Filtering the schedules list table
    says `q` has a "maximum 200 characters", while `parseFilterQ` counts runes
    (`utf8.RuneCountInString(needle) > maxFilterQRunes`). The two agree for every
    BMP string and disagree for astral-plane text. This lane's client cap is safe
    in either reading (Decision 5), and no comment or copy this lane ships may
    restate the server's bound as "characters".

## The server contract this lane consumes

Restated from the README's Scheduled Jobs section and
`internal/api/scheduled_jobs.go`, so the Decisions can be checked against it.
Nothing here is new.

`GET /v1/scheduled-jobs/stats` - authenticated, **not** admin-only. Fleet-wide for
an admin, scoped to `owner_id` for everyone else. Five keys, all always present,
all non-negative integers, no `omitempty` anywhere in `scheduledJobStatsResponse`:

| Field | Definition |
|---|---|
| `enabled` | Schedules in scope with `enabled = true`. |
| `paused` | Schedules in scope with `enabled = false`. Exactly `NOT enabled`. |
| `total` | `enabled + paused`, computed from the two buckets. |
| `failed_runs_24h` | **Jobs** a schedule in scope produced with `status = failed`, last updated within 24 hours, **including `run-now` jobs**. Excludes `cancelled`. |
| `failing` | **Schedules** in scope carrying a `last_error`. **Not windowed.** |

**It accepts no filters.** `stats.total` equals the list's `total` only when no
filter is active. The README's own instruction: "Read `total` off the list for 'N
matching' and off `/stats` for the strip."

`GET /v1/scheduled-jobs` list filters, which AND together and compose with
`limit`, `cursor` and `sort`:

| Parameter | Meaning | Absent means |
|---|---|---|
| `enabled` | `true` / `false`. A genuine tri-state: `enabled=false` means only paused. `?enabled=` (empty) is absent. | No enabled filter |
| `q` | Case-insensitive substring of the schedule `name`, the owner's `email`, or the `cron_expr`. `%` and `_` are literal. Empty or whitespace-only is absent. | No text filter |

- A repeated `enabled` or `q` is a 400. The SPA builds query strings with
  `URLSearchParams`, which cannot repeat one.
- `total` on the list counts every row matching every active filter.
- **A cursor carries no record of the filters that were active**, and the server
  does not reject a mismatched one. Dropping the cursor on a filter change is the
  client's job.
- Error bodies: `invalid enabled; expected true or false`, `q is too long;
  maximum 200 characters`, `q is not valid UTF-8`.

`last_job_status` on the list, the get, the create and the PATCH:

- Taken verbatim from the job's own vocabulary. It agrees with `status` on
  `GET /v1/jobs/{id}` and is **not** the `pending` to `queued` rename that
  `GET /v1/jobs/stats` performs.
- **Present exactly when `last_job_id` is present.** The two keys appear together
  or neither appears. Absent means the schedule has never had a fire that produced
  a job. It never means "unknown" and never means "healthy": a failed lookup is a
  500, because `fillLastJobStatuses` "FAILS THE REQUEST RATHER THAN DEGRADING".
- `last_error` and `last_job_status` are independent axes and **both may be
  present at once**. The README states the correct reading: "the last job it
  produced finished successfully, and the most recent attempt produced no job".
- **`run-now` does not move it.** `POST /v1/scheduled-jobs/{id}/run-now` creates a
  job carrying `scheduled_job_id` but updates neither `last_job_id` nor
  `last_run_at`.

## Decisions

Autonomous run. Each question, its options, the choice and the reason.

**1. Where do the enabled filter and the search text live?**

- Options: (a) page-level state in `SchedulesPage`, carried in the query key;
  (b) URL search parameters; (c) persisted in local storage.
- Chosen: **(a)**, matching lane JF's Decision 1 for the Jobs page.
- Why: (b) would make this the first URL state in a SPA that has none - a search
  for `useSearchParams`, `createSearchParams` and `location.search` under
  `web/src` still returns nothing - and that is its own design decision with its
  own interactions with the pager and the sort control. (c) makes a filter
  survive a reload, which turns "the list looks empty" into a bug report.
- Composition, exhaustively. `q` is the debounced, trimmed value; `enabledKey` is
  one of `all`, `enabled`, `disabled`.

| State | Request | Query key |
|---|---|---|
| No filter | `sort=<sort>&limit=50[&cursor=]` | `['schedules', sort, cursor, 'all', '']` |
| Enabled chip | `...&enabled=true` | `['schedules', sort, cursor, 'enabled', q]` |
| Disabled chip | `...&enabled=false` | `['schedules', sort, cursor, 'disabled', q]` |
| Search | `...&q=<trimmed>` | `['schedules', sort, cursor, enabledKey, q]` |

  The key holds the **chip key**, not the wire value, so the three states are
  three distinct strings and no `undefined` or boolean has to be hashed. `q` is
  omitted from the query string entirely when empty after trimming; the server
  would treat `q=` as absent either way, so this is hygiene, not correctness.

**2. Does the summary strip keep a page-scoped fallback?**

- Options: (a) fleet-wide from `/stats`, with the existing page-scoped
  `countEnabled` as a fallback until the first response, mirroring
  `WorkersPage`; (b) fleet-wide only, with a placeholder until the first
  response; (c) show both numbers.
- Chosen: **(b). The fallback is deleted and `countEnabled` is removed from
  `SchedulesPage`.**
- Why, and this refutes the backlog item's own proposal. Three reasons, and the
  first two exist only because the other items in this lane ship at the same time.
  - **The fallback would be a filtered number under a fleet-wide label.** The
    page-scoped count is computed from `data.items`, and after this lane
    `data.items` is a filtered page. Click Disabled, and the fallback strip would
    read `0 ENABLED` for a farm with twelve enabled schedules. `WorkersPage` has
    no filter, so its fallback cannot do this and its precedent does not transfer.
  - **Two of the four numbers have no page-scoped analogue at all.**
    `failed_runs_24h` is counted over jobs, which the schedules list does not
    carry. `failing` could be approximated by counting `last_error` on the loaded
    page, and that approximation is exactly the "fabricated stat reads as broken"
    hazard the page's own comment already names. A strip that is half real and
    half guessed, with nothing distinguishing which is which, is worse than a
    strip that says it does not know yet.
  - **The window the fallback covers is nearly empty anyway.** `SchedulesPage`
    early-returns a skeleton while `isLoading && !data`, so the strip is not on
    screen until the list has landed. Both queries mount in the same render, so by
    the time the header exists the stats response is usually in.
- **The placeholder is a hyphen, and the four tiles keep their labels.** A tile
  that vanishes until its number arrives changes the strip's width mid-measure
  (see the e2e change below) and reads as a missing feature.
- **A stats error is stated, not swallowed.** When the stats query has errored and
  has no data, the strip renders the four hyphens plus one mono line reading
  `counts unavailable`. It is text, not a colour, and it does not offer a Retry:
  the query polls, so a retry button would be a second, weaker copy of the poll.
  The list is unaffected - a stats failure must never blank the table.

**3. Does `failing` get a tile, and how are the two failure numbers labelled?**

- Options: (a) three tiles, matching the hi-fi, dropping `failing`; (b) four
  tiles; (c) four tiles with `failing` hidden when zero.
- Chosen: **(b), four tiles**, with labels that name their units.
- Why not (a): `failing` is the only fleet-wide answer to "is anything broken right
  now". The row-level FAILING chip this table already draws is page-scoped, so on
  page 3 of a filtered list it cannot answer that question. And `failing` counts a
  failure class that is invisible to `failed_runs_24h` by construction - the
  README says a spawn failure "never becomes a job, so it is invisible to any
  count over jobs".
- Why not (c): a tile that disappears at zero cannot be told from a tile that was
  never built, which is the same information loss as the fabricated stat.
- **The labels deviate from the hi-fi and the deviation is the point.** Shipped
  labels, all ASCII, no separator glyph: `ENABLED`, `PAUSED`, `FAILED RUNS 24H`,
  `FAILING SCHEDULES`. The hi-fi's `FAILED - 24H` names neither the unit nor the
  window, and beside a second FAIL-prefixed number it invites the reading that one
  is a subset of the other. It is not: one counts jobs in a 24-hour window
  including `run-now` jobs, the other counts schedules and is not windowed. The
  noun in each label is the whole fix.
- Colours follow the hi-fi's mapping where it exists - the ok token for ENABLED,
  the default foreground for PAUSED, the error token for FAILED RUNS 24H - and
  FAILING SCHEDULES takes the warn token, so the two failure numbers are also
  distinguishable without reading. Colour is never the sole carrier: each number
  has its label beside it.

**4. Do the chips carry counts?**

- Options: (a) counts from `/stats`, as the hi-fi draws them; (b) no counts;
  (c) counts only when no search is active.
- Chosen: **(b), no counts.**
- Why: `/stats` accepts no filters, so a chip count ignores `q`. A number printed
  on a control reads as a promise about the result of activating it, and with a
  search active that promise is false. (c) makes the counts appear and disappear
  as the user types, which is worse than never having them: it draws the eye to
  the exact moment the number stops being trustworthy.
- The information is not lost. `enabled` and `paused` are already two of the four
  strip tiles, three feet away, under labels that say what set they count.

**5. The search box: debounce, cursor reset, length cap, minimum.**

- **300 ms**, through `useDebouncedValue`, with a `debounceMs` prop on
  `SchedulesPage` defaulting to 300 purely as a test seam - the `UsersTab` shape,
  copied deliberately rather than re-derived. Introducing a second debounce
  constant is how two numbers drift.
- **The debounce is not a bound and must never be described as one.** `GET
  /v1/scheduled-jobs` has no rate limit (verification 11), and a caller that is
  not a typing human is unaffected by a client-side timer. It reduces how many
  scans one person's typing generates, and nothing else. A server-side control is
  proposed as a follow-up.
- **The cursor resets in the raw input handler**, not in an effect keyed on the
  debounced value. This is the load-bearing half. An effect keyed on the debounced
  value runs *after* the render that already issued a query with the new `q` and
  the old cursor, so exactly one request escapes carrying a cursor minted under
  different filters. The chip handler resets in its own click handler for the same
  reason.
- **A maximum length of 200 on the input.** The browser counts UTF-16 code units
  and the server counts runes, so a 200-code-unit cap is at or below the server's
  200-rune bound for every string and can never produce the 400; for astral-plane
  text it truncates earlier than the server would, which is the safe direction.
  The acceptance criterion is written as a structural pin on the attribute, not as
  a claim about what a browser does with it.
- **No minimum length.** A one-character search is legitimate, a client floor is
  trivially bypassed, and calling it a cost control would be the same mistake as
  advertising the debounce.
- **The input carries no fixed minimum width**, deviating from the hi-fi's 240
  pixels and from `UsersTab`'s copy of the same control. This toolbar carries
  three chips and the sort control as well, and the simplest thing that cannot
  overflow at a 320-pixel viewport is a flex item with a zero minimum that takes
  the remaining space and wraps to its own line when there is none. Stated as a
  deliberate difference, not an oversight, and it makes no claim about
  `/admin/users`, which measures green today.

**6. The LAST JOB cell: what it renders, and in which of four states.**

The cell becomes a `<Link>` to `/jobs/:id` containing three children in one line:
a dot, the eight-character short id, and **the status word as text**.

- **The word is not optional.** A dot colour is the hi-fi's only carrier, and this
  table's own FAILING chip comment already rejected that reasoning once, in this
  file, for this row. It is worse here than it was there, because the row already
  has a dot four columns to the left that means `enabled` - so a second dot with
  a different vocabulary and no label is not merely inaccessible, it is ambiguous
  to a sighted reader too.
- **The mapping is `statusColor` from `web/src/jobs/status.ts`**, reused exactly as
  `ScheduleRunsPanel` reuses it in this same feature area. No second mapping. A
  status outside the five-value union takes that helper's default branch and
  renders muted, with the verbatim word still carrying the fact.
- **The four states, exhaustively.**

| State | Renders | Means |
|---|---|---|
| No `last_job_id` | `-`, exactly as today | No scheduled fire has produced a job |
| `last_job_id` and `last_job_status` | Link: dot, short id, status word | The last scheduled fire's job, and its current status |
| `last_job_id`, no `last_job_status` | Link: short id only. **No dot, no word.** | Unreachable in production |
| `last_error` set | **Unchanged by this cell.** The FAILING chip in the NAME cell carries it | Both may be true at once |

  The third row is the one a reviewer should read twice. The pairing is a server
  invariant, so the state cannot occur; the renderer nevertheless must not invent
  a neutral dot or the word "unknown" for it, because a grey dot is a fact-shaped
  object and drawing one from an absent key is the exact defect
  `fillLastJobStatuses` refuses to commit server-side. Rendering the bare link is
  a fail-quiet, and the test that pins it says so.

  The fourth row is a contract, not an omission. The README: a schedule can carry
  `last_job_status: "done"` and a `last_error` at the same time and "That is not a
  contradiction." A test renders exactly that row and asserts both marks.

- **`run-now` makes this cell lag, and this lane states it rather than fixing it.**
  `POST /run-now` creates a job but advances neither `last_job_id` nor
  `last_run_at`, so immediately after an operator clicks Run now, LAST JOB still
  describes the previous scheduled fire. One short comment on the cell records the
  hazard and points at the README's Scheduled Jobs section; no UI copy claims the
  cell is live, and nothing here tries to patch it client-side. A follow-up item is
  proposed.
- **The column widens.** The status word does not fit beside a dot and eight
  monospace characters in the current LAST JOB track. That track grows from 110 to
  150 pixels and the table's minimum-width constant grows from 1040 to 1080. The
  grid template stays nine tracks and the row stays nine cells - this is a width
  change, not a column change. **The e2e layout gate cannot see it** (verification
  13): the grid lives inside its own horizontal scroller, so a wider grid scrolls
  rather than widening the document. The screenshots are the artifact.
- Options considered and rejected: stacking the id and the word on two lines
  (grows every row's height, including the majority that have no last job); an
  `aria-label` carrying the word with only the dot visible (fixes the screen
  reader and leaves a colour-blind sighted user with nothing); dropping the id and
  showing only the word (the id is the link's identity and its only affordance for
  recognising a run).

**7. How the strip and the footer avoid reading as a contradiction.**

Two totals now appear on one screen from two sources. Unlabelled, they look like a
bug the first time a filter is active.

- The strip's fifth element is a muted caption sourced from `/stats`, reading
  `<total> SCHEDULES TOTAL` when no filter is active and
  `<total> SCHEDULES TOTAL (UNFILTERED)` when one is. The parenthetical appears at
  the exact moment the two numbers can disagree, and it answers the question where
  it is asked.
- The footer's range text gains one word on the same condition: `SHOWING x-y of N`
  becomes `SHOWING x-y of N MATCHING`. The existing zero-row branch (`0 of N`)
  gets the same treatment.
- Both are driven by one derived boolean, `filterActive = enabledKey !== 'all' ||
  q !== ''`, so they cannot disagree with each other.
- The footer's existing localization is preserved: both numbers still go through
  the same thousands-separated formatting, which four existing tests pin.

**8. The empty state must distinguish "none" from "none matching".**

`SchedulesTable` renders the literal `No schedules yet.` when handed zero rows.
Under an active filter that sentence is false. The table gains an optional
`emptyMessage` prop **defaulting to its current string**, so its existing tests
keep their behaviour, and `SchedulesPage` passes `No schedules match these
filters.` when `filterActive`. `UsersTab` already makes this distinction.

**9. The stats query: cadence, key, and what it deliberately omits.**

- `useScheduleStats(intervalMs = 10000)`, key `['schedules', 'stats']`,
  `queryFn: getScheduleStats`.
- **10 seconds, matching `useSchedules`**, not the 3 seconds `useWorkerStats`
  uses. The rule is that a strip refreshes with the list it sits above;
  `useSchedules`' own comment gives the reason for 10s ("Schedules are low-churn").
  A strip that refreshes faster than its list produces a header that has moved on
  from the rows underneath it.
- **No `placeholderData: keepPreviousData`.** `useWorkerStats` carries it, and
  copying it here would be cargo. The key is constant - `/stats` takes no filters
  and no cursor - so nothing ever mints a new key and the option is inert. Stated
  so a reviewer does not add it back for symmetry.
- The query is not gated by `enabled`: there is one view on this page and the
  strip is always mounted.
- Cost: one additional request per mounted Schedules page per 10 seconds, serving
  one SQL statement (`ScheduledJobCounts`, one row, with `failed_runs_24h` as a
  scalar subquery so the whole census shares one snapshot).

**10. Generation ordering, in its frontend form.**

The Invariant says end the generation before releasing the resource. Its frontend
subject here is the page walk: a filter change must end the previous walk before
its fetch is released. **The existing pager already handles this, and the way it
does is worth writing down, because the wrong shape is the one a reviewer would
reach for first.**

- `pickEnabled` and `pickSearch` each set their filter state **and** call
  `pager.resetPaging()` in the same event handler. React batches both into one
  render, so the next render issues exactly one request, under a key that carries
  the new filter and an empty cursor. There is no intermediate render in which the
  new filter travels with the old cursor.
- The forbidden shape is an effect keyed on the debounced value: it runs after the
  render that already issued the query, so exactly one request goes out with the
  new `q` and a cursor minted under the old filters. The reset lives in the raw
  handler for this reason and no other (Decision 5).
- Response ordering needs no work of its own. TanStack keys every request, so a
  late response for the old key cannot be written into the new key's cache entry.
  There is no last-writer-wins window to fence.
- The pager's buttons remain disabled while `isPlaceholderData` is true, which is
  the existing guard against advancing from a page that is being replaced.

**11. How the existing tests survive a second query on the page.**

Verification 8 is the constraint: `onUnhandledRequest: 'error'`, and MSW does not
match `/v1/scheduled-jobs/stats` against a `/v1/scheduled-jobs` handler.

- Options: (a) edit all twelve existing page tests to register a stats handler;
  (b) one `beforeEach` per existing page test file; (c) a default handler in
  `web/src/test/msw.ts`.
- Chosen: **(b).**
- Why not (c): a global default would answer this endpoint for every test in the
  suite forever, so a page that stopped requesting stats entirely would still
  pass, and the `onUnhandledRequest: 'error'` signal - which is what caught this
  in the first place - would be permanently disabled for one endpoint.
- Why not (a): twelve near-identical edits to a file whose byte-for-byte stability
  licensed an earlier refactor, for a reason that is not about any of those tests.
- (b) is a four-line addition per file. `server.use` inside `beforeEach` works
  because `setup.ts` resets handlers in `afterEach`, and a per-test `server.use`
  still takes priority, since runtime handlers are prepended.
- **Every fixture body is hand-written JSON.** The stats body registered in the
  `beforeEach` is an object literal carrying all five keys with no type
  annotation naming the production interface. This is the project rule: a fixture
  marshalled through the response type agrees with the decoder by construction and
  can never detect drift in either direction.

**12. Client argument shape.**

`listSchedules` and `useSchedules` each gain filters as a **trailing optional
parameter with a default**, so every existing call site compiles unchanged. This
matters concretely: `useSchedules.test.tsx` calls
`useSchedules('-created_at', undefined, 20)` - the interval is the third
positional argument - so filters become the fourth. The cost is a hook at four
positional parameters with a mixed-purpose ordering, which is at the edge of
readable; the benefit is that the two hook tests take a zero-line diff while their
subject's behaviour changes. Converting to an options object is proposed as a
follow-up so the choice is revisited on its own evidence.

## Design

### Files

New:

- `web/src/schedules/scheduleFilters.ts` and `scheduleFilters.test.ts` - the chip
  vocabulary and the wire mapping. `ENABLED_FILTERS` as a const tuple of
  `{ key, label }`, `EnabledFilterKey` derived from it, and
  `enabledParam(key): 'true' | 'false' | undefined`. Pure, no React. Small on
  purpose: it is the one piece of this lane where a wrong value is silent (a
  Disabled chip that sends nothing looks like an All chip) and a pure function is
  where that can be pinned without a render.
- `web/src/schedules/useScheduleStats.ts` and `useScheduleStats.test.tsx`.
- `web/src/schedules/SchedulesSummary.tsx` and `SchedulesSummary.test.tsx` - the
  strip. Presentational: takes `stats` (possibly undefined), `statsFailed`, and
  `filterActive`. It owns no query, so its four states - loading, loaded, failed,
  filtered - are four renders in a test with no MSW at all.
- `web/src/schedules/SchedulesPage.filters.test.tsx` - the chips, the search box,
  the cursor resets, the empty message, the footer wording.
- `web/src/schedules/SchedulesPage.stats.test.tsx` - the strip wired to the
  endpoint: fleet-wide numbers that disagree with the page, the placeholder before
  the first response, the failure line, and the unfiltered caption.

Changed:

- `web/src/schedules/api.ts` - `ScheduleStats` interface; `getScheduleStats()`;
  `last_job_status?: JobStatus` on `Schedule`, imported from `../jobs/api` and
  documented with the pairing contract; `listSchedules` gains the trailing filters
  argument.
- `web/src/schedules/useSchedules.ts` - filters in the key and the call.
- `web/src/schedules/SchedulesPage.tsx` - the strip from `SchedulesSummary`, the
  chip row, the search input, `filterActive`, the empty message, the footer
  wording, the deletion of `countEnabled` and of the backend-blocked comment
  block.
- `web/src/schedules/SchedulesTable.tsx` - the LAST JOB cell, the `emptyMessage`
  prop, the LAST JOB track width and the minimum-width constant.
- `web/src/schedules/api.test.ts` - appended cases for the new client functions.
- `web/e2e/surfaces.ts` - the two `ready` gates and one coverage-limit comment.
- Three existing test files, narrowly: see the gate below.

### Component tree

```
SchedulesPage
 |- header row (wraps)
 |   |- Eyebrow / h1
 |   |- SchedulesSummary            four tiles + the total caption
 |   \- Sort select                 unchanged
 |- toolbar row (wraps)
 |   |- All / Enabled / Disabled    three aria-pressed buttons in a named group
 |   \- search input                type=search, named, length-capped
 |- action error strip              unchanged
 \- SchedulesTable + footer         emptyMessage; footer gains MATCHING
```

`SchedulesTable`'s LAST JOB cell:

```
TableCell
 \- last_job_id ? Link to /jobs/:id : "-"
     |- dot span        (aria-hidden; colour from statusColor)   only when paired
     |- short id text
     \- status word     (verbatim, from last_job_status)         only when paired
```

The dot is `aria-hidden` because the word beside it says the same thing; leaving
it exposed would make the link's accessible name carry an empty element for no
gain.

### Data flow

`SchedulesPage` owns `sort`, `enabledKey`, `qInput` (raw), `q` (debounced and
trimmed), the pager and `pendingId`. It derives `filterActive` and passes it to
exactly two places, the summary and the footer. Nothing below the page reads or
writes a filter.

```
const q = useDebouncedValue(qInput, debounceMs).trim()
const { data, ... } = useSchedules(sort, pager.cursor, undefined, { enabledKey, q })
const stats = useScheduleStats()
```

`listSchedules` builds `sort` and `limit` always, `cursor` when non-empty,
`enabled` from `enabledParam(enabledKey)` when defined, and `q` when non-empty -
each through `URLSearchParams.set`, which cannot repeat a parameter.

### Accessibility

- The chip row is a `role="group"` named `Schedule status filter`, with three
  buttons carrying `aria-pressed` - the shape the Jobs view switch already ships.
- The search input has an accessible name (`Search schedules`), `type="search"` so
  it resolves as a searchbox, a placeholder naming name, owner and cron, and a
  maximum length.
- The strip is plain text: each number is adjacent to its own uppercase label, so
  no tile depends on its colour. The failure line is a sentence, not an icon.
- The LAST JOB link's accessible name is the short id followed by the status word,
  so a screen-reader user gets the status without the dot. The dot is
  `aria-hidden`.
- The row has four tab stops today: the name link, the Run now button, the
  Enable/Disable button and the Edit link. The LAST JOB link makes five. One added
  stop per row is the price of the cell being a link rather than a click handler,
  and it is what buys middle-click and open-in-new-tab (refutation 3).

### Layout, at 320, 375 and 1280

- Both rows wrap. The header row already wraps; it gains one tile and one caption.
  The toolbar row is new and holds three short pills and one flexible input with a
  zero minimum.
- The table's grid is unchanged in structure; one track grows by 40 pixels and the
  minimum-width constant by the same. **The gate cannot see this** and the spec
  does not pretend otherwise (verification 13).
- The gate is `layout.spec.ts`: document, header and main scroll widths at or
  below the client width at 320, 375 and 1280, across both schedules surfaces.

### Load, failure and threat

- **Load.** One extra request per mounted page per 10 seconds, one SQL statement.
  `?q=` is a `strpos` scan across three axes and is not index-servable, by the
  same design decision lane JB made for jobs; a `q` that matches nothing pays a
  full scan of the in-scope rows. There is no rate limit on this endpoint. The
  debounce reduces how many scans one typing user generates and bounds nothing.
  `?enabled=` is a boolean predicate and is cheap.
- **Failure.** A stats failure degrades the strip to hyphens plus one sentence and
  leaves the table untouched. A list failure keeps the existing whole-page error
  with Retry. Neither can blank the other. A silent failed *refresh* over existing
  data remains invisible, which is a gap this page shares with every other list in
  the app; it is proposed as a follow-up rather than solved unevenly here.
- **Threat model.** No new endpoint and no new write. `/stats` is owner-scoped
  server-side from the bearer token, and its handler refuses an unresolved
  identity before building the scope precisely so a zero UUID cannot become the
  fleet-wide sentinel - the client sends no scope and cannot influence it. `q` is
  the user's own text sent as a query parameter and matched with `strpos`, where
  `%` and `_` are literal, so there is no client-side pattern to escape. The one
  field this lane newly renders is `last_job_status`, which is server-controlled
  vocabulary, not operator text - unlike `last_error`, whose untrusted-text
  handling is already documented on `Schedule` and is unchanged here. No token, id
  or email is rendered anywhere it was not already rendered.
- **Invariants.** Six of the seven are backend-shaped and untouched; no Go changes.
  The seventh in its frontend form is Decision 10, and the honest statement is that
  the existing pager already satisfies it - this lane's obligation is to put the
  reset in the raw handler and to not introduce an effect that undoes it.

## What must NOT change, as a checkable gate

- **Zero-line diff:** `web/src/schedules/useSchedules.test.tsx`,
  `web/src/schedules/useSchedule.test.tsx`,
  `web/src/schedules/useScheduleActions.test.tsx`,
  `web/src/schedules/useScheduleRuns.test.tsx`,
  `web/src/schedules/format.test.ts`, `web/src/schedules/ScheduleRunsPanel.test.tsx`,
  every `ScheduleDetailPage*.test.tsx`, `web/src/lib/useCursorPager.test.ts`,
  `web/src/lib/useDebouncedValue.test.ts`, and everything under `web/src/jobs/`
  and `web/src/workers/`. Decision 12 is what makes the hook files achievable
  rather than aspirational.
- **Exactly three existing test files change, and each edit is enumerated here:**
  - `web/src/schedules/SchedulesPage.test.tsx`: one `beforeEach` registering a
    hand-written stats body (Decision 11); the deletion of the test named `does
    not render the backend-blocked filter chips, search, or FAILED-24H stat`
    (refutation 15); and `last_job_status: 'done'` added to the one fixture item
    that carries a `last_job_id`, so the fixture stops encoding a state the server
    cannot produce. The test named `renders schedules and the page-scoped summary`
    is **renamed** and its `2 schedules` assertion re-pointed at the stats-sourced
    caption - its old name describes behaviour this lane deliberately removes, and
    leaving a correct-looking test under a false name is the wrong-prose defect in
    its most durable form.
  - `web/src/schedules/SchedulesPage.pager.test.tsx`: the same `beforeEach`, and
    nothing else. Its sort-reset test must stay green untouched: it is the
    existing proof that `chooseSort` resets the pager, and the new filter resets
    are modelled on it.
  - `web/src/schedules/SchedulesTable.test.tsx`: two lines. `last_job_status:
    'done'` in the shared `sched()` helper, and `last_job_status: undefined`
    alongside `last_job_id: undefined` in the test named `missing last_job_id
    renders a dash`, so neither fixture holds an unpaired combination. Everything
    else in that file is appended.
- Any other edit to an existing test is a behaviour change and must be justified
  in review, not absorbed.
- **Every MSW fixture in this lane is hand-written JSON carrying every key the
  server sends**, declared without a type annotation naming the production
  interface. For a schedule item that means all of `id`, `name`, `owner_id`,
  `owner_email`, `cron_expr`, `timezone`, `job_spec`, `overlap_policy`, `enabled`,
  `next_run_at`, `created_at`, `updated_at`, plus the optional keys the case is
  about - and `last_job_id` and `last_job_status` appear **together or not at
  all**, because that is the contract the renderer is built on. For the stats body
  it means all five keys, always, with no omissions: the response carries no
  `omitempty`, so a fixture that drops one is testing an impossible response.

## Acceptance criteria

Each names its test and the mutation that test kills.

| # | Criterion | Test | Mutation it kills |
|---|---|---|---|
| AC-1 | The strip's four numbers come from `/v1/scheduled-jobs/stats`, not from the loaded page. The fixture's stats numbers are all different from the page's row counts, so a page-derived implementation cannot pass | `SchedulesPage.stats.test.tsx` - "the strip shows fleet-wide counts, not page counts" | compute the strip from `data.items` |
| AC-2 | Before the first stats response the strip shows a hyphen per tile and no zero | `SchedulesSummary.test.tsx` - "an absent stats response renders placeholders, not zeros" | fall back to `0` |
| AC-3 | A failed stats query renders the placeholder plus a `counts unavailable` line, and the table still renders its rows | `SchedulesPage.stats.test.tsx` - "a stats failure does not blank the table" | let the stats error take the page's error branch |
| AC-4 | Four tiles are present and each names its unit: enabled, paused, failed runs in 24h, failing schedules | `SchedulesSummary.test.tsx` - "the strip names four numbers and their units" | drop the `failing` tile; relabel it to a bare FAILED |
| AC-5 | With a filter active, the strip's total caption says it is unfiltered and the footer says MATCHING; with no filter, neither word appears | `SchedulesPage.filters.test.tsx` - "the two totals label themselves when they can disagree" | hard-code either caption |
| AC-6 | The Enabled chip sends `enabled=true`, Disabled sends `enabled=false`, All sends no `enabled` at all | `SchedulesPage.filters.test.tsx` - "each chip sends its own enabled value" plus `scheduleFilters.test.ts` | make `enabledParam('disabled')` return undefined - the silent failure, where Disabled behaves as All |
| AC-7 | Clicking a chip after paging forward drops the cursor. **The test pages forward first**; without that the cursor is already empty and the mutation is a no-op that a neighbouring test would appear to kill | `SchedulesPage.filters.test.tsx` - "filtering after paging forward drops the cursor" | remove `pager.resetPaging()` from the chip handler |
| AC-8 | Typing sends the trimmed text as `q`, and an empty box sends no `q` key | `SchedulesPage.filters.test.tsx` - "the search box sends q, and omits it when empty" | send `q` unconditionally |
| AC-9 | A burst of keystrokes produces exactly one request, carrying the final value (rendered with a small `debounceMs`, on real timers, as `UsersTab.test.tsx` does) | `SchedulesPage.filters.test.tsx` - "a burst of keystrokes issues one request" | replace `useDebouncedValue` with an identity function |
| AC-10 | Searching after paging forward drops the cursor, and the reset happens on the raw keystroke: no request is ever observed carrying both a non-empty `q` and a cursor | `SchedulesPage.filters.test.tsx` - "no request carries a new q with an old cursor" | move the reset into an effect keyed on the debounced value |
| AC-11 | The search input has an accessible name, resolves as a searchbox, names name, owner and cron in its placeholder, and carries a maximum length of 200. Recorded as a structural pin: it cannot establish what a browser does with the attribute | `SchedulesPage.filters.test.tsx` - "the search box is named, is a searchbox, and is length-capped" | remove the attribute or the label |
| AC-12 | The chip row is a named group of three `aria-pressed` buttons and exactly one is pressed at a time | `SchedulesPage.filters.test.tsx` - "the chips are a named group with one pressed" | drop the group name; press on more than one |
| AC-13 | With a filter active and zero rows, the table says no schedules match; with no filter it keeps saying none exist | `SchedulesPage.filters.test.tsx` - "an empty filtered table says no schedules match" | hard-code the empty message |
| AC-14 | A paired `last_job_id`/`last_job_status` renders a link to `/jobs/:id` whose text carries both the short id and the status word | `SchedulesTable.test.tsx` - "the LAST JOB cell links to the job and names its status in text" | render the dot only; render a `<span>` with an onClick instead of a link |
| AC-15 | The status word is present for every member of the job vocabulary, including one whose colour maps to the default branch, so the word is proven to be the carrier rather than a side effect of the colour switch | `SchedulesTable.test.tsx` - "every job status reaches the cell as a word" | derive the word from the colour mapping's known cases only |
| AC-16 | An absent `last_job_id` renders a hyphen and no link | `SchedulesTable.test.tsx` - the existing dash test, plus one added link-absence assertion | render an empty link |
| AC-17 | A `last_job_id` with no `last_job_status` renders the link with no dot and no word - it does not invent a neutral dot or the string "unknown" | `SchedulesTable.test.tsx` - "an unpaired last_job_id draws no status" | render a muted dot for the absent key |
| AC-18 | A row carrying both `last_error` and `last_job_status` shows the FAILING chip **and** the last-job status; the two axes do not suppress each other | `SchedulesTable.test.tsx` - "a failing schedule can still have a healthy last job" | suppress the LAST JOB cell when `last_error` is set |
| AC-19 | The row still holds exactly nine cells and the header nine columnheaders after the cell change | `SchedulesTable.test.tsx` - the existing arity tests, extended to a paired row | add the status word as a tenth cell |
| AC-20 | `useScheduleStats` requests `/v1/scheduled-jobs/stats` and refetches on its interval | `useScheduleStats.test.tsx` - "fetches stats and refetches on the interval" | drop the `refetchInterval` |
| AC-21 | The list query key carries the filters, so changing a filter refetches rather than serving the previous filter's cache | `SchedulesPage.filters.test.tsx` - "the filter is in the query key" | key on sort and cursor only |
| AC-22 | `getScheduleStats` decodes all five keys, and an error envelope rejects with `ApiError` | `api.test.ts` - two appended cases | swallow the error status |
| AC-23 | The existing pager, sort-reset, footer-range and thousands-separator behaviour is unchanged | the existing `SchedulesPage.test.tsx` and `SchedulesPage.pager.test.tsx` suites, edited only as enumerated above | any regression in the pager chain |
| AC-24 | Neither schedules surface overflows the document, header or main at 320, 375 or 1280, **with the strip populated** | `web/e2e/layout.spec.ts` via the strengthened `ready` gates | give the toolbar input a fixed minimum width that cannot wrap |
| AC-25 | Zero files changed under `internal/`, `cmd/`, `python/`; the three existing-test files edited exactly as enumerated and no others | review, plus the full `web/src` suite green | - |

### The e2e change

**No new surface.** The chips, the search input and the four-tile strip all live
in the header and toolbar of the existing `schedules` surface, which
`layout.spec.ts` already measures at three widths - and that toolbar is exactly
where the added-width risk is. Adding a surface for a filtered state would need a
`prepare` that fabricates one, and the filter is deliberately not persisted
(Decision 1), so there is no preference key to set the way `jobs-lanes` does.

**Both schedules surfaces' `ready` gates are strengthened**, and this is the
substantive part of the e2e change. Today `schedules` waits only for the schedule
link. The strip renders four hyphens until the stats response lands and then
renders numbers, which **changes its width**; a measurement taken on the link
alone can be taken against the placeholder strip and report a width the shipped
page never has. Each gate gains a wait for a digit in the ENABLED tile, so the
width measured is the populated strip's. This is the "measure the populated
state" rule applied to a strip that has two populations rather than one.

**One coverage limit is recorded in `surfaces.ts`, in the `schedules` surface's
comment.** The seeded schedule has never fired, so its `last_job_id` is absent and
the LAST JOB cell renders the hyphen at every width. The populated cell - link,
dot and status word, the widest thing the new column can hold - **is not covered
by any browser test**, and cannot be in slice 1: producing it needs a scheduler
fire, which needs an agent, which is the slice-2 gap the file already records for
`/workers`. The comment must say this in the surface's own terms, so a later
reader does not mistake a populated-table pass for a populated-cell pass.

**No `keyboard.spec.ts` case is added.** Every schedules row already carries
several focusable controls, so rows are reachable by ordinary tabbing; the new
link adds one stop to a row that already had four and introduces no wrapper tab
stop.

## Gates

- `cd web && npm test`
- `cd web && npx tsc -b --force`
- `cd web && npm run build`
- `make test-e2e`, which needs Docker Desktop running and a Postgres at
  `postgres://relay:relay@127.0.0.1:5432` - `docker start relay-postgres`, or
  `scripts/dev.ps1` once to create it. Browsers installed once with
  `cd web && npx playwright install chromium webkit`. Run it from Git Bash; if
  `make` is not on PATH, use the MSYS2 copy with the variable forwarding
  `web/e2e/README.md` documents.
- `git checkout -- web/dist/` before assembling the PR. `web/dist` is tracked but
  not maintained per-PR, and `make test-e2e` writes into it.
- **No Go gate is required or run: this lane changes no Go file.** Say that
  plainly in the PR rather than reporting a Go lane that was not the subject.

## Risks and the merge surface

- `web/src/lib/useDebouncedValue.ts`'s doc comment says the hook is "Used by the
  admin Users tab's exact-email filter" - a census of other code that becomes
  false the moment this lane or lane JF merges. Whichever of the two merges second
  owns the one-line correction to name no consumer at all. This lane must check
  the sentence before assuming it needs changing; a hand-edit by both lanes is a
  conflict over a cosmetic fix.
- `SchedulesPage.tsx` and `SchedulesTable.tsx` are this lane's alone within the
  batch, but `web/src/jobs/status.ts` is imported here and owned by the jobs
  lanes. This lane only reads `statusColor` and must not modify it.
- The LAST JOB column widening is the change least visible to any gate. Whoever
  reviews should open the 1280 screenshot for both schedules surfaces.
- Residual: if the strengthened `ready` gate proves flaky because a stats response
  is slower than the list under CI load, the fix is a longer wait on the digit,
  not a return to the link-only gate. Reverting the gate silently restores a
  measurement of the wrong population.

## Backlog items this closes

Closing each is required scope, through `/backlog close <fragment>`, which does
the `git mv` into `docs/backlog/closed/`. A hand-edited `status` leaves the file
in the open directory and `/backlog list` reports it malformed.

1. `idea-2026-06-05-schedules-stats-endpoint` - the resolution must record that
   the item's own proposed page-scoped fallback was **refuted and not built**, and
   why (Decision 2), so a later reader does not file the fallback as missing work.
2. `idea-2026-06-05-failed-24h-stat` - record that the shipped strip carries two
   failure numbers, not one, and that the hi-fi's label was renamed to name the
   unit.
3. `idea-2026-06-05-last-job-link-status` - record that the shipped cell carries
   the status **word**, not only the coloured dot the item asked for, and that the
   cell is a link rather than the hi-fi's click handler.
4. `idea-2026-06-05-schedules-filter-search` - record that `enabled` is a genuine
   tri-state, that the hi-fi's per-chip counts were deliberately not built, and
   that the debounce is an additive measure rather than a bound.

## Proposed follow-up backlog items

Proposals only. The human accepts or drops each; none is filed by this lane.

1. **bug, low - `run-now` does not advance `last_job_id`, so the Schedules list's
   LAST JOB cell lags an interactive run.** The README documents the behaviour, so
   this is a product question rather than a wrong-prose defect: either `run-now`
   should update the pointer, or the column should be named for scheduled fires.
   Naming it here rather than fixing it in a frontend lane.
2. **idea - extract a shared search input.** Three inline copies after this batch:
   `UsersTab`, `JobsPage` (lane JF) and `SchedulesPage`. The house rule is extract
   at the third. Deferred here because two concurrent lanes cannot both own it.
3. **idea - convert `useSchedules` and `listSchedules` to an options object.**
   Decision 12 appended a fourth positional parameter after an interval. The
   refactor's gate is a byte-identical test diff, which is exactly what this lane
   could not afford while changing the same hook's behaviour.
4. **bug, medium - `GET /v1/scheduled-jobs` has no rate limit and `?q=` is an
   unindexed three-axis scan.** Sibling of the same finding on the jobs list. The
   client debounce is not a control and this lane must not be read as adding one.
5. **idea - a silent failed refresh is invisible on every list page.** The
   schedules strip inherits it along with the table. One indicator, one place.
6. **idea - filter results are not announced to a screen reader.** Changing a chip
   or a search box replaces a table with no live region. Worth one decision
   covering the jobs and schedules pages together rather than two.
7. **idea - the populated LAST JOB cell has no browser coverage.** Blocked on the
   slice-2 agent harness, which is the same blocker as the `/workers` populated
   state. Filing it so the gap has an id the surface comment can cite.

## Escalations

Calls a human might reasonably make the other way.

1. **Keep the page-scoped fallback** (Decision 2), accepting a filtered number
   under a fleet-wide label for one round trip, in exchange for a strip that never
   shows a hyphen. I judged a briefly-wrong number worse than a briefly-absent one,
   and the two tiles with no page analogue settle it - but a reviewer who weighs
   first-paint completeness more heavily would restore it for the two tiles that
   can be approximated.
2. **Three tiles, not four** (Decision 3). The hi-fi draws three, and a reviewer
   could reasonably hold that `failing` belongs on a schedule's own row and not in
   a fleet summary, especially at a 320-pixel width where four tiles wrap.
3. **Chip counts from `/stats`** (Decision 4), accepting that they ignore `q`.
   They are useful the large majority of the time, when no search is active.
4. **The status word as an `aria-label` rather than visible text** (Decision 6).
   It keeps the LAST JOB column at its current width and the table at its current
   minimum, at the cost of leaving a colour-blind sighted user with a dot and
   nothing else. I ranked the sighted case as decisive; a reviewer optimising for
   the table's already-worst-in-app width might not.
5. **A different word than MATCHING in the footer, or none** (Decision 7). The
   contradiction could equally be resolved by removing the total from the strip
   entirely and leaving one total on the page.
6. **A global default stats handler in `web/src/test/msw.ts`** (Decision 11). It
   is a two-line change instead of a per-file one, and it costs the
   unhandled-request signal for that endpoint forever. I ranked the signal higher.
7. **Persist the filter across visits.** I chose not to; a reviewer who thinks of
   Disabled as a mode rather than a query might prefer that it survives a reload.
