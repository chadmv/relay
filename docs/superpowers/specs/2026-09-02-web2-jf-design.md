# Lane JF: the Jobs page search box, My-jobs toggle and Timeline view

Date: 2026-09-02
Branch: `claude/web2-jf-jobs-frontend`
Worktree: `.claude/worktrees/web2-jf`, at `origin/main` (carries lane JB's merged
`GET /v1/jobs` filters and lane TB's table changes)
Author: relay-tpm (autonomous gate mode; no human answered questions during this
flow, so every question in the Decisions section was decided here and the calls a
human might make the other way are listed under Escalations)

## Why this lane exists

Three Jobs-page features were deferred on 2026-06-05 because the server could not
express them, and the page still carries a comment saying so. Lane JB shipped the
server half: `GET /v1/jobs` now accepts `q`, `mine`, `since` and `until`. This lane
is the frontend half of all three, and nothing under `internal/` or `python/`
changes.

Backlog items in scope:

- `docs/backlog/idea-2026-06-05-job-search-box-q-filter.md`
- `docs/backlog/idea-2026-06-05-my-jobs-toggle-mine-filter.md`
- `docs/backlog/idea-2026-06-05-jobs-timeline-view.md`
- `docs/backlog/idea-2026-09-02-extract-usepersistedview.md` (see Decision 12)

## The hi-fi, quoted

The previous batch's retro recorded a spec that rejected a hi-fi mechanism it had
not read, so every claim below about the design handoff is a quotation from
`design_handoff_relay_holo/hifi3-holo-pages.jsx`, not a paraphrase of a backlog
item. Two substitutions keep this file ASCII and are the only alterations: each
non-ASCII pictograph is replaced by a bracketed description of it, and the middot
separator the hi-fi uses inside display strings is replaced by a hyphen. No
structure, property, value or guard is changed.

**`HoloJobsList` holds four pieces of state and a three-value view list.**

```jsx
const [filter, setFilter] = useState('all');
const [view, setView] = useState('table');
const [tWindow, setTWindow] = useState('24h');
const [mineOnly, setMineOnly] = useState(false);
const [sort, setSort] = useState('-created_at');
const ME = 'mira@studio.dev';
let filtered = filter==='all' ? JOBS_SAMPLE : JOBS_SAMPLE.filter(j => j[2]===filter || (filter==='active' && (j[2]==='running'||j[2]==='queued')));
if (mineOnly) filtered = filtered.filter(j => j[7] === ME);
const VIEWS = [['table','[list glyph]','Table'],['lanes','[grid glyph]','Lanes'],['timeline','[watch glyph]','Timeline']];
```

**The view toggle is a pill group in the page header's right-hand cluster.**

```jsx
<div style={{marginLeft:'auto',display:'flex',gap:10,alignItems:'center'}}>
  {/* View toggle */}
  <div style={{display:'flex',padding:3,borderRadius:999,
    background:'rgba(0,0,0,0.3)',border:`1px solid ${C.border}`,
    backdropFilter:'blur(8px)'}}>
    {VIEWS.map(([k,icon,label])=>(
      <button key={k} onClick={()=>setView(k)} style={{
        padding:'6px 14px', borderRadius:999, border:'none', cursor:'pointer',
        fontFamily:C.sans, fontSize:12, letterSpacing:'0.02em',
        display:'flex',alignItems:'center',gap:6,
        background: view===k?`linear-gradient(90deg, ${C.accent}, ${C.accentB})`:'transparent',
        color: view===k?'#fff':C.fgMute,
        fontWeight: view===k?600:400,
        boxShadow:'none',
      }}><span style={{fontSize:13}}>{icon}</span> {label}</button>
    ))}
  </div>
</div>
```

**The toolbar row is one wrapping flex row. Which controls are view-gated, and
which are not, is the load-bearing fact.** The chips, the cards-per-lane stepper,
the window picker and the sort control each carry a `view ===` guard. The search
input and the My-jobs button carry none.

```jsx
{/* View-specific toolbar */}
<div style={{display:'flex',gap:8,marginTop:4,alignItems:'center',flexWrap:'wrap'}}>
  {view === 'table' && [['all','All'],['running','Running'],['queued','Queued'],['done','Done'],['failed','Failed']].map(([k,n])=>(
    ...chip button...
  ))}
  {view === 'lanes' && (
    ...CARDS / LANE stepper...
  )}
  {view === 'timeline' && (
    <div style={{display:'flex',padding:3,borderRadius:999,
      background:'rgba(255,255,255,0.04)',border:`1px solid ${C.border}`,backdropFilter:'blur(8px)'}}>
      {['6h','24h','7d'].map(w => (
        <button key={w} onClick={()=>setTWindow(w)} style={{
          padding:'5px 14px',borderRadius:999,border:'none',cursor:'pointer',
          fontFamily:C.mono,fontSize:11,letterSpacing:'0.08em',
          background: tWindow===w ? hexToRgba(C.accent,0.2):'transparent',
          color: tWindow===w ? C.fg : C.fgMute,
        }}>{w}</button>
      ))}
    </div>
  )}
  <input placeholder="Filter by name, owner, id..." style={{
    marginLeft:'auto', minWidth:240, padding:'7px 14px',borderRadius:999,
    background:'rgba(0,0,0,0.25)',border:`1px solid ${C.border}`,
    color:C.fg,fontFamily:C.sans,fontSize:12,outline:'none',
  }}/>
  <button onClick={()=>setMineOnly(v=>!v)} style={{
    padding:'6px 14px', borderRadius:999, fontFamily:C.sans, fontSize:12, cursor:'pointer',
    display:'flex',alignItems:'center',gap:6,
    background: mineOnly?`linear-gradient(90deg, ${hexToRgba(C.accent,0.3)}, ${hexToRgba(C.accentB,0.22)})`:'rgba(255,255,255,0.04)',
    border:`1px solid ${mineOnly?C.accent:hexToRgba(C.accent,0.4)}`,
    color: mineOnly?C.fg:C.accent,
    backdropFilter:'blur(8px)',
    boxShadow:'none',
  }}>
    <span style={{fontSize:13}}>[quadrant-circle glyph]</span> My jobs
  </button>
  {view === 'table' && (
    <SortControl C={C} options={JOBS_SORT} value={effSort} onChange={setSort}
      disabled={statusFiltered}
      disabledHint="Sorting is unavailable while a status filter is active - the server rejects sort + status together. Switch to All to sort."/>
  )}
</div>

{view === 'lanes' && <HoloLanes C={C} D={D} onOpen={onOpen}/>}
{view === 'timeline' && <HoloTimeline C={C} window={tWindow}/>}
{view === 'table' && (
  ...the table panel...
)}
```

**`HoloTimeline` in full.**

```jsx
function HoloTimeline({ C, window: w }) {
  const bars = [
    ['film-x / shot-042','running',30,45,72],
    ['nightly etl [repeat glyph]','running',5,85,38],
    ...
    ['frames teaser','queued',92,3,0],
  ];
  const ticks = w==='6h' ? ['-6h','-4h','-2h','-1h','now']
              : w==='7d' ? ['-7d','-5d','-3d','-1d','now']
              : ['00:00','06:00','12:00','18:00','now'];
  return (
    <div style={{...glassPanel(C),flex:1,minHeight:0,display:'flex',flexDirection:'column',overflow:'hidden'}}>
      <div style={{padding:'14px 20px',borderBottom:`1px solid ${C.border}`,
        display:'flex',justifyContent:'space-between',alignItems:'center'}}>
        <span style={{fontSize:13,color:C.fg}}>Timeline - last <span style={{color:C.accent}}>{w}</span></span>
        <span style={{fontFamily:C.mono,fontSize:10,letterSpacing:'0.16em',color:C.fgMute}}>TIME-WINDOWED - NO PAGINATION</span>
      </div>
      {/* Tick row */}
      <div style={{padding:'10px 20px 0', display:'grid',
        gridTemplateColumns:'160px 1fr', gap:14, alignItems:'center'}}>
        <span/>
        <div style={{position:'relative',height:18,
          borderBottom:`1px solid ${C.border}`}}>
          {ticks.map((t,i)=>{
            const left = (i/(ticks.length-1))*100;
            const isNow = t==='now';
            return (
              <div key={t} style={{position:'absolute',left:`${left}%`,top:0,
                transform: i===ticks.length-1?'translateX(-100%)':i===0?'none':'translateX(-50%)',
                fontFamily:C.mono,fontSize:9.5,letterSpacing:'0.14em',
                color: isNow?C.accent:C.fgMute}}>{t.toUpperCase()}</div>
            );
          })}
        </div>
      </div>
      {/* Bars */}
      <div style={{flex:1,minHeight:0,overflow:'auto',padding:'8px 20px 16px'}}>
        {bars.map((b,i)=>{
          const [name,st,start,width,pct] = b;
          const sc = st==='done'?C.ok : st==='running'?C.accent : st==='failed'?C.err :
            st==='cancelled'?C.fgDim : C.warn;
          const fillBg = st==='running' ? `linear-gradient(90deg, ${hexToRgba(C.accent,0.25)}, ${hexToRgba(C.accentB,0.4)})`
            : hexToRgba(sc,0.18);
          return (
            <div key={i} style={{display:'grid',gridTemplateColumns:'160px 1fr',gap:14,alignItems:'center',
              padding:'7px 0',borderBottom:`1px solid ${hexToRgba(C.accent,0.06)}`}}>
              <div style={{fontSize:12,color:C.fg,overflow:'hidden',textOverflow:'ellipsis',whiteSpace:'nowrap'}}>{name}</div>
              <div style={{position:'relative',height:22}}>
                {/* now line */}
                <div style={{position:'absolute',right:0,top:-4,bottom:-4,width:1,
                  background:hexToRgba(C.accent,0.5)}}/>
                <div style={{position:'absolute',left:`${start}%`,width:`${width}%`,height:'100%',
                  background:fillBg,
                  border:`1px solid ${sc}`,borderRadius:4,
                  boxShadow:'none',
                  display:'flex',alignItems:'center',padding:'0 8px',gap:6,
                  fontFamily:C.mono,fontSize:10,letterSpacing:'0.04em',color:C.fg,
                  overflow:'hidden',whiteSpace:'nowrap',
                }}>
                  {st==='running' && <span style={{width:5,height:5,borderRadius:'50%',
                    background:sc}}/>}
                  <span style={{color:sc,fontWeight:600,letterSpacing:'0.08em'}}>{st.toUpperCase()}</span>
                  <span style={{color:C.fgMute}}>- {pct}%</span>
                </div>
              </div>
            </div>
          );
        })}
      </div>
    </div>
  );
}
```

What that quotation settles, and what it does not:

- The timeline is **DOM boxes positioned by percentage**, not an SVG. Percentage
  left and width on absolutely-positioned children inside a relatively-positioned
  track, one track per row, two-column grid with a fixed name column.
- The bar carries **text inside it**: the uppercase status and the percentage.
- There is a **one-pixel vertical rule pinned to the right edge of every track**,
  described in its own comment as the now line.
- The window picker has exactly **three values, `6h`, `24h`, `7d`**, and is a pill
  group like the view switch.
- The search input and the My-jobs button are **not view-gated**.
- The bar list is its own **vertical scroll container** inside a panel with a
  fixed height, because the whole hi-fi page is a fixed-height application shell.
  The shipped SPA is not; see Decision 10.

## What I verified against the tree, and what I refuted

A backlog proposal is not a contract, and neither is a hi-fi. Every bullet below
was checked in this worktree.

1. **Refuted (the Timeline item).** "Being window-bounded, it needs no cursor
   pagination." Lane JB refuted this on the server side and this lane inherits the
   consequence: a window bounds elapsed time, not cardinality. The endpoint stays
   paged, the client walks, and the walk is capped. See Decisions 6 and 7.
2. **Refuted (the Timeline item).** "Requires a new server endpoint or query
   parameter." Half true and now moot: query parameters only, already shipped.
   Nothing in this lane touches Go.
3. **Refuted (the hi-fi).** The timeline panel's badge reads
   `TIME-WINDOWED - NO PAGINATION`. That claim is false against the shipped
   endpoint and must not be reproduced. The panel's right-hand caption states the
   walk instead: the number drawn, the number matching, and whether the walk was
   truncated.
4. **Refuted (the hi-fi).** The `24h` tick row is `['00:00','06:00','12:00','18:00','now']`
   while the `6h` and `7d` rows are relative. Absolute wall-clock labels at fixed
   quarter positions are correct only for a calendar-aligned midnight-to-now
   window; this window is rolling and ends at the anchor. All three tick rows
   become relative. See Decision 11.
5. **Refuted (the hi-fi).** `placeholder="Filter by name, owner, id..."`. The
   server's `q` matches the job `name` or the submitter's `email` and nothing
   else - `strpos(lower(j.name), ...)` or `strpos(lower(u.email), ...)`, per the
   README's Filtering the jobs list table. A placeholder promising id search is a
   wrong-prose defect on arrival. The placeholder names name and owner email only.
6. **Refuted (the hi-fi), partially.** Five tick labels do not fit the track this
   layout leaves at a 320 pixel viewport. Three ticks at every width; see
   Decision 11.
7. **Refuted (an existing test's premise), and this one bites.**
   `web/src/jobs/JobsPage.lanes.test.tsx` contains a test named `a stored value
   that is not the literal lanes falls back to the table view` whose stored value
   is the string `timeline`. The moment `timeline` becomes a real view, that test
   asserts the opposite of the behaviour it names. It must be edited, and its
   stored value must become a string outside the allow-list.
8. **Refuted (the lane brief's own option).** "useQueries per page" is not
   buildable. Page N+1's cursor is only knowable from page N's response, so the
   set of queries cannot be enumerated before the walk runs. `useQueries` takes a
   static array. The walk is sequential by construction; the only question is
   where the sequence lives. See Decision 6.
9. **Confirmed.** `apiFetch` is declared `ApiOptions extends Omit<RequestInit, 'body'>`
   and spreads `...rest` into `fetch`, so it already accepts and forwards an
   `AbortSignal`. No change to `web/src/lib/api.ts` is needed for the walk to be
   cancellable.
10. **Confirmed, and it is why the debounce must not be advertised as a control.**
    `RateLimit` is applied at exactly two mux registrations in
    `internal/api/server.go`, both on `POST /v1/auth/...`. `GET /v1/jobs` has no
    rate limit, which is what the README's `?q=` cost note says.
11. **Confirmed.** `web/src/lib/useDebouncedValue.ts` exists and is used by
    `UsersTab` with a `debounceMs` prop defaulting to 300 as a test seam. This
    lane reuses both the hook and the prop convention rather than inventing a
    second debounce.
12. **Confirmed.** The SPA still has zero occurrences of `useSearchParams`,
    `createSearchParams` and `location.search` under `web/src`. There is no URL
    state to put a filter in, and this lane does not introduce any.
13. **Confirmed.** `parsePage` rejects a `limit` outside `[1, 200]` with
    `400 invalid limit`; it does not clamp. The timeline's page size of 200 is the
    ceiling, not a suggestion.
14. **Checked and found nothing.** No existing timeline, gantt or chart component
    anywhere under `web/src`; no date-range picker; no charting dependency in
    `web/package.json` to reuse or to avoid.

## The server contract this lane consumes

Summarised from the README's Filtering the jobs list section and
`internal/api/job_filters.go`. Nothing here is new; it is restated so the
Decisions below can be checked against it.

| Parameter | Meaning | Absent means |
|---|---|---|
| `q` | Case-insensitive substring of job `name` or submitter `email`. `%` and `_` are literal. Maximum 200 runes. Whitespace-only is absent. | No text filter |
| `mine` | `true` restricts to jobs submitted by the bearer token's user. `false` equals absent. | No owner filter |
| `since` | RFC3339 with an offset or `Z`. Bounds `created_at` inclusively. | Window open at the start |
| `until` | RFC3339, same format. Bounds `created_at` exclusively. | Window open at the end |

- All four AND together and compose with `limit`, `cursor`, `sort`, `status` and
  `scheduled_job_id`. They are **not** part of the sort-versus-filter 400; only
  `status` and `scheduled_job_id` are.
- `total` counts every row matching every active filter.
- An empty value is treated as absent, so a cleared box may send `q=` safely.
- A repeated `q`, `mine`, `since` or `until` is a 400. The SPA builds query
  strings with `URLSearchParams.set`, so it cannot repeat one.
- **A cursor carries no record of the filters that were active.** The server does
  not reject a mismatched one. Dropping the cursor when a filter changes is the
  client's job.
- The default sort is `-created_at`. The timeline sends no `sort` at all and
  relies on that default, so that adding a `status` filter to the timeline later
  cannot silently turn into the sort-versus-filter 400.

## Decisions

Autonomous run. Each question, its options, the choice and the reason.

**1. Where do the search box and the My-jobs toggle live in the URL, the state
tree and the query key?**

- Options: (a) page-level state threaded into whichever view is showing;
  (b) per-view state; (c) URL search parameters.
- Chosen: **(a)**.
- Why: (c) would make this lane the first to introduce URL state in a SPA that has
  none, which is its own design decision with its own interactions with the pager,
  the sort control and the view switch. (b) means switching views silently drops
  the user's search. (a) matches the hi-fi's ungated placement of both controls.
- Composition, exhaustively. `q` is the debounced, trimmed value; `mine` is a
  boolean.

| View | Request | Query key |
|---|---|---|
| Table, no chip | `limit=50&sort=<sort>[&cursor=][&q=][&mine=true]` | `['jobs', sort, status, cursor, q, mine]` |
| Table, chip active | `limit=50&status=<s>[&cursor=][&q=][&mine=true]` | same |
| Lanes | `limit=10&status=<s>[&q=][&mine=true]` per lane | `['job-lanes', status, limit, q, mine]` |
| Timeline | `limit=200&since=<iso>&until=<iso>[&cursor=][&q=][&mine=true]` | `['job-timeline', since, until, q, mine]` |

  No view ever sends `sort` together with `status`. The timeline sends neither
  `sort` nor `status`. `since` and `until` are sent by the timeline only; the
  window picker is view-gated in the hi-fi and stays so here.

**2. Do `q` and `mine` apply to the Lanes view as well?**

- Options: (a) yes, threaded into all five lane queries; (b) no, and hide both
  controls in lanes view; (c) show them disabled in lanes view.
- Chosen: **(a)**.
- Why: (c) is a dead control, which the JobsPage comment this lane deletes already
  names as reading like a breakage. (b) makes a page-level filter silently exempt
  one view, which is the worse lie. The load objection to (a) does not survive
  being written down with numbers: the five status partitions are disjoint and
  together cover the table, so five status-scoped `q` scans cost about one
  whole-table scan, the same as the table view's single unscoped `q` scan. The
  multiplier is in the request count, not the row work.
- Consequence, and it is a real one: the lane header caption changes from a bare
  `N total` to `N total` or `N matching` depending on whether a filter is active,
  because with a filter the number is no longer that status's all-time count.
- Second consequence: the lane query key stops being constant, so
  `placeholderData: keepPreviousData` stops being inert and is re-added to
  `useJobLanes`. `docs/backlog/idea-2026-09-02-cards-per-lane-stepper.md`
  predicted this would happen when the cap entered the key; it happens here for a
  different reason, and the item's parenthetical should not be read as remaining
  work after this lane.

**3. Debounce value, and where it lives.**

- Options: 250 ms (the README's floor), 300 ms (the shipped `UsersTab` value),
  500 ms.
- Chosen: **300 ms**, via `useDebouncedValue` in `JobsPage`, with a `debounceMs`
  prop on `JobsPage` defaulting to 300 purely as a test seam, exactly as
  `UsersTab` does it.
- Why: introducing a second debounce constant when the app already has one is how
  two numbers drift. 300 sits above the README's recommended 250 floor.
- **The debounce is not a bound and must never be described as one.** The server
  applies no rate limit to `GET /v1/jobs` (verified above), and a caller that is
  not a typing user is unaffected by a client-side timer. It reduces how many
  scans one person's typing generates. It is an additive measure, and the README
  says so. Any comment or copy this lane ships must say the same. The honest
  remedy is a server-side control and is proposed as a follow-up item.
- `mine` is not debounced: it is one click producing one state change.

**4. Does the client cap `q` length, and does it impose a minimum?**

- Options on the maximum: (a) nothing, let the 400 surface; (b) an input
  `maxLength` of 200; (c) client-side validation with its own message.
- Chosen: **(b)**, with the mismatch stated rather than claimed away. The browser
  counts UTF-16 code units and the server counts runes, so a 200-code-unit bound
  is at or below the server's 200-rune bound and can never produce the 400. It can
  truncate a string of astral-plane characters earlier than the server would. That
  is the safe direction; the component comment says so, and the acceptance
  criterion is written as a structural pin on the attribute, not as a claim about
  what a browser does with it.
- Chosen on the minimum: **no minimum length**. A single-character search is
  legitimate, a client-side floor is trivially bypassed, and pretending it is a
  cost control would be the same mistake as advertising the debounce.

**5. Cursor reset: on the raw keystroke or on the debounced value?**

- Options: (a) reset in the input's change handler, on the raw value; (b) reset in
  an effect keyed on the debounced value.
- Chosen: **(a)**, matching `UsersTab`'s `pickEmail`.
- Why: (b) is the broken direction. An effect keyed on the debounced value runs
  after the render that already issued a query with the new `q` and the old
  cursor, so exactly one request goes out carrying a cursor minted under different
  filters. (a) costs at most one extra first-page fetch of data the user is
  already looking at. The My-jobs toggle resets in its own click handler for the
  same reason.
- Switching views does **not** reset the pager: the table's cursor state is still
  valid for the table's key, and returning to it hits the cache.

**6. How does the Timeline walk pages?**

- Options: (a) `useQueries`, one query per page; (b) one `useQuery` whose `queryFn`
  walks sequentially to a page cap; (c) `useInfiniteQuery` with a manual
  auto-advance.
- Chosen: **(b)**.
- Why: (a) is not buildable (refutation 8: cursors are only knowable one page at a
  time). (c) gives per-page cache entries and a `fetchNextPage` the view would
  have to drive from an effect, which is a second async lifecycle to fence for no
  benefit, since the chart is only ever drawn from the whole walk. (b) has one
  key, one cache entry, one loading state, one error, and the whole walk is
  described by one function that a test can drive through MSW.
- The `queryFn` returns `{ jobs, total, truncated }`.
- **The walk must consume the `AbortSignal` the `queryFn` is given**, pass it to
  every page fetch, and check `signal.aborted` before issuing the next page. This
  is the frontend form of the CLAUDE.md rule that a generation ends before the
  resource is released: a window change, a debounce landing, a My-jobs click or a
  view switch all mint a new key, and the previous walk must stop before the new
  one starts competing with it for the browser's connections. TanStack cancels an
  inactive query's promise **only if the signal was consumed**, so consuming it is
  what turns cancellation on. The implementer must **measure** whether the key
  change alone aborts the previous walk; if it does not, the hook cancels the
  previous key explicitly before the new key mounts. Record which of the two it
  was; do not assume.

**7. What is the page cap, and what does the affordance say?**

- Chosen: `TIMELINE_PAGE_SIZE = 200` (the server maximum, so the fewest requests a
  window can cost) and `TIMELINE_MAX_PAGES = 3`, a ceiling of **600 jobs drawn**.
- Why those numbers, with their inputs. Three sequential round trips is a bounded
  first-paint latency. At the 15-second refresh of Decision 8, the worst case is
  3 requests and 600 enriched rows per 15 seconds, against the table view's
  5 requests and 250 rows per 15 seconds at its 3-second cadence: fewer requests,
  2.4 times the rows. Six hundred bars is already about sixteen screens of
  vertical scroll at the row height this design uses, so the cap sits past the
  point of usefulness rather than short of it. Two pages (400) was the alternative
  and is a defensible different call.
- **The truncation flag comes from the cursor, never from arithmetic.**
  `truncated` is true when the walk stopped because it hit the page cap while the
  last page still carried a `next_cursor`. It is emphatically **not**
  `drawn < total`: jobs are created during the walk, so `total` grows under it and
  a drained window would raise a false banner. `total` is used only for the number
  in the banner, taken from the last response fetched.
- Because the sort is `created_at DESC`, the 600 drawn are the 600 **newest** in
  the window. The truncation is deterministic and its direction is stated in the
  banner text, not left for the reader to infer.
- The affordance, when `truncated` is true:
  - Text: `Showing the 600 most recent of N jobs created in the last <window>.`
  - One button. If a shorter window exists, it reads `Show last 6 hours` (or
    `Show last 24 hours`) and selects it. If the window is already the shortest,
    it reads `Open the Table view` and calls the same `chooseView('table')` the
    lanes overflow control uses, because at the 6-hour window there is no
    narrowing left and the paged table is the surface that can show all of them.
    That last branch is the one most likely to be forgotten, and it is the only
    one where the view cannot fix itself.

**8. What is the time anchor, and how often does the Timeline refresh?**

- Options: (a) a `refetchInterval` like every other list, with the window
  recomputed from a live clock; (b) no `refetchInterval`, with a quantized anchor
  whose advance is the refresh; (c) a fixed anchor taken once per window
  selection.
- Chosen: **(b)**. `ANCHOR_STEP_MS = 15000`. The anchor is
  `Math.floor(now / ANCHOR_STEP_MS) * ANCHOR_STEP_MS`, driven by the existing
  `useNow` hook at the same interval; `until` is the anchor and `since` is
  `anchor - WINDOW_MS[window]`.
- Why: (a) with a live clock changes the query key on every render, which is an
  unbounded fan-out of walks; with a `refetchInterval` on a stable key it re-walks
  up to three pages every three seconds. (c) is the honest-looking option that
  drifts: the anchor stays fixed while the drawn "now" edge advances, so after ten
  minutes a 6-hour window is drawing 6 hours and 10 minutes of axis under a label
  saying 6 hours. (b) keeps the axis and the query identical at all times, at the
  cost of a bounded staleness: **a job created less than 15 seconds ago is not yet
  in the window.** That number is stated in the panel caption's own terms (the
  axis end is shown as a wall-clock time) rather than left implicit.
- Rejected variant, recorded because it is the tempting one: omit `until` and let
  the axis end at the anchor while the query is open-ended. That shows the newest
  jobs about 12 seconds sooner and corrupts everything downstream - rows arrive
  that fall outside the drawn axis, and `total` then counts rows the chart does
  not draw, which is the input to the truncation banner.
- **The timeline query sets an explicit short `gcTime` of 60 seconds.** Every
  anchor step mints a new key, so at the client default of five minutes the cache
  would accumulate about twenty abandoned entries each holding up to 600 job rows.
  This is the kind of consequence a per-tick key has that nothing else in the app
  has, so it is a requirement, not a tuning note. `placeholderData: keepPreviousData`
  keeps the chart from blanking as the anchor advances.

**9. What does a bar draw for a job with no `finished_at`, or no `started_at`?**

Given the axis `[since, until)`:

- `t0 = started_at ?? created_at`, `t1 = finished_at ?? until`.
- Left and width are percentages of `(until - since)`, each clamped to `[0, 100]`.
- The bar element carries a **minimum width in CSS pixels** (3) rather than a
  minimum percentage, so a four-second job inside a seven-day window is still
  visible without any percentage arithmetic that could push a bar past the right
  edge. The track has hidden overflow, so a minimum-width bar at the far right is
  clipped rather than widening the row.
- A job with `started_at` and no `finished_at` (running) runs to the right edge,
  where the hi-fi's one-pixel now rule terminates it.
- A job with **no `started_at`** (pending, or cancelled before it started) has
  `t0 == t1 == created_at` and therefore draws the minimum-width marker at its
  submission time. **The row text says `queued` and the marker is an instant, not
  a duration** - this is stated in the row's own text, because a reader who sees a
  small box on a gantt reads it as a short piece of work.
- Every bar's exact numbers are in the row's text (status, percent complete,
  duration through the existing `formatDuration`), so the geometry is a positional
  summary and never the only carrier of a fact.

**10. The Timeline and the Table / Lanes switch, and the table poll.**

- The persisted view gains a third value: `'table' | 'lanes' | 'timeline'`, with
  `table` still the default and anything outside the allow-list falling back to it.
- **Exactly one of the three data sources is enabled at a time.** `useJobs` is
  already gated by `enabled`; it becomes `view === 'table'`. `useJobLanes` is
  already gated; it stays `view === 'lanes'`. `useJobTimeline` takes the same
  parameter and gets `view === 'timeline'`. Without this the timeline view polls a
  50-row enriched page nobody is looking at, which is the exact argument lane F
  made for lanes.
- The live indicator's `polling` expression becomes a three-way. Forgetting the
  third branch leaves the dot permanently dark beside text claiming the page
  auto-refreshes, which is why it gets its own acceptance criterion rather than a
  passing mention.
- **The Timeline view is not a vertical scroll container.** The hi-fi's bar list
  is `overflow:'auto'` inside a fixed-height application shell; the shipped SPA
  scrolls its whole page. Nesting a vertical scroller in a vertical scroller is
  what lane F rejected for the lanes stack, and with a 600-row cap the page scroll
  is the right affordance. Consequence, stated rather than solved: the tick row
  scrolls out of view on a long list. It is not made sticky in this slice; the
  panel header carries the axis endpoints in text, so the information is not lost.

**11. Ticks and axis labelling.**

- Three ticks at every window and every width: the window start, the midpoint and
  `NOW` - for example `-24H`, `-12H`, `NOW`.
- Why three, refuting the hi-fi's five: at a 320 pixel viewport this layout leaves
  roughly 140 pixels of track, and five mono labels collide there. The alternative
  is a breakpoint, which lane F rejected for lanes on the grounds that it makes
  the widths measured at 320 and 375 different from the widths shipped at 1280.
  One tick set that is correct at every width beats a breakpoint.
- Why relative rather than the hi-fi's absolute 24-hour labels: refutation 4.
- The tick row is `aria-hidden`. A bare sequence of relative offsets read aloud is
  noise, and the same information is in the panel's text description.
- The axis endpoints are rendered as local wall-clock through the existing
  `formatDateTime` helper, which is built from `Date` getters rather than `Intl`
  precisely so its output does not depend on the runner's locale.

**12. Is this the moment to extract the persisted view switch?**

`docs/backlog/idea-2026-09-02-extract-usepersistedview.md` says the house rule is
extract before the third consumer, and that JobsPage is the second.

- Chosen: **yes, extract it here, and close the item.** This lane brings the third
  and fourth consumers at once - the jobs view gains a third value, and the
  timeline window is a second persisted choice with the identical shape.
- The hook is `web/src/lib/usePersistedChoice.ts`:
  `usePersistedChoice<T extends string>(key: string, allowed: readonly T[], fallback: T): [T, (v: T) => void]`.
  It reads lazily in a `useState` initializer inside a `try`, validates membership
  against the allow-list, and writes inside a `try` so a storage failure does not
  take the click with it.
- **The name deviates from the item's `usePersistedView`** because the third
  consumer is a time window, not a view. The close note must say so, so the item
  does not read as unsatisfied.
- Adopted at three call sites: `relay.jobs.view`, `relay.jobs.timeline.window`,
  `relay.workers.view`. Adopting it in `WorkersPage` is required scope, not
  optional: the item's substance is that Jobs guards its storage access and
  Workers does not, and a hook adopted on one side only leaves the divergence and
  adds a third shape. The item also asks for the group role and name on both
  switches; `WorkersPage`'s grid/table pair has neither today, and gains both.
- Gate: this half is a behaviour-preserving refactor, so `WorkersPage`'s existing
  tests take a **zero-line diff**, and each re-wired behaviour is mutated to prove
  the tests still see it.

**13. Empty states must distinguish "none" from "none matching".**

`JobsTable` renders the literal `No jobs yet.` when it is handed zero rows. Under
a search that sentence is false: there are jobs, none match. `UsersTab` already
makes this distinction. `JobsTable` gains an optional `emptyMessage` prop
defaulting to its current string, so its own tests keep a zero-line diff, and
`JobsPage` passes a filter-aware message. The lanes and the timeline carry the
same distinction in their own empty copy.

**14. The Timeline shows jobs CREATED in the window, not jobs ACTIVE in it.**

`since` and `until` bound `created_at`. A job submitted ten days ago and still
running does **not** appear in a 24-hour window. This is a genuine limitation of
the only predicate the server offers, and lane JB ruled out predicating on
`started_at` and `finished_at` because they come from the list query's lateral
aggregate. The panel caption therefore says **created**, not a bare "last 24
hours". Getting this wrong would be a wrong-prose defect on a correct
implementation, which this repo records as its dominant class. An activity-overlap
window is proposed as a follow-up.

**15. One error for the whole Timeline, unlike the Lanes view.**

A lane owns its error because five lanes are five independent questions. The
timeline is one question about one window, and a partial walk drawn under the
window's own label is a chart that lies. The walk fails as a unit and the panel
shows the message and a Retry, mirroring the table view's `error && !data` rule so
a failed refresh over existing data keeps the existing data visible. A silent
failed refresh is a pre-existing gap the table view shares; it is proposed as a
follow-up rather than solved unevenly here.

**16. New hook and client arguments are appended, not restructured.**

`listJobs`, `listJobsByStatus`, `useJobs` and `useJobLanes` each gain `q` and
`mine` as **trailing optional parameters with defaults**, so every existing call
site and every existing test call compiles unchanged and the zero-line-diff gate
below is achievable. The cost is that `useJobs` reaches seven positional
parameters, which is at the edge of readable. The alternative - one options object
per hook - is cleaner to read and would force an edit to `useJobs.test.tsx` and
`useJobs.enabled.test.tsx`, which are two of the files this lane is meant to leave
alone while it changes their subject's behaviour. Appended parameters win here;
converting these two hooks to an options object is proposed as a follow-up so the
choice is revisited on its own evidence rather than inside this lane.

## Design

### Files

New:

- `web/src/lib/usePersistedChoice.ts` and `usePersistedChoice.test.ts`
- `web/src/jobs/timelineWindow.ts` and `timelineWindow.test.ts` - the window
  vocabulary and all the time arithmetic: `TIMELINE_WINDOWS` as a const tuple with
  `TimelineWindow` derived from it, `WINDOW_MS`, `WINDOW_LABEL`, `NEXT_SHORTER`
  (a `Record<TimelineWindow, TimelineWindow | null>`, so a window added without an
  answer is a tsc error), `TICKS`, `ANCHOR_STEP_MS`, `TIMELINE_PAGE_SIZE`,
  `TIMELINE_MAX_PAGES`, and `windowBounds(window, nowMs)` returning the quantized
  `{ sinceIso, untilIso }`. Pure, no React.
- `web/src/jobs/timelineGeometry.ts` and `timelineGeometry.test.ts` -
  `barGeometry(job, sinceMs, untilMs)` returning `{ leftPct, widthPct, instant }`.
  Pure.
- `web/src/jobs/useJobTimeline.ts` and `useJobTimeline.test.tsx` - the anchor, the
  `useQuery`, and the walk.
- `web/src/jobs/JobsTimeline.tsx` and `JobsTimeline.test.tsx` - the view.
- `web/src/jobs/JobsPage.timeline.test.tsx` - the view switch's third value, the
  table poll, the live dot.
- `web/src/jobs/JobsPage.filters.test.tsx` - the search box and the toggle across
  the three views.

Changed:

- `web/src/jobs/api.ts` - `q` and `mine` arguments on `listJobs` and
  `listJobsByStatus`; a new `listJobsInWindow`.
- `web/src/jobs/useJobs.ts` - `q`, `mine` in the key and the call.
- `web/src/jobs/useJobLanes.ts` - `q`, `mine` in the key and the call, plus
  `placeholderData`.
- `web/src/jobs/JobsLanes.tsx` - the lane caption's `total`/`matching` wording.
- `web/src/jobs/JobsTable.tsx` - the `emptyMessage` prop.
- `web/src/jobs/JobsPage.tsx` - the search input, the toggle, the third view, the
  window picker, the three-way `polling`, the empty message, and the deletion of
  the backend-blocked comment block.
- `web/src/workers/WorkersPage.tsx` - adopt `usePersistedChoice`, add the group
  role and name.
- `web/e2e/surfaces.ts` - one new surface.
- Two existing tests, narrowly: see the gate below.

### Component tree

```
JobsPage
 |- pageHeader
 |   |- Eyebrow / h1 / KPI strip
 |   |- live indicator (data-live)
 |   |- ViewSwitch                     three aria-pressed buttons in a named group
 |   \- "+ New job"
 |- toolbar row (wraps)
 |   |- view === 'table'    : status chips
 |   |- view === 'timeline' : WindowPicker (named group, three aria-pressed buttons)
 |   |- search input        (all views)
 |   |- My jobs toggle      (all views)
 |   \- view === 'table'    : SortControl
 |- view === 'timeline' : JobsTimeline
 |- view === 'lanes'    : JobsLanes           (unchanged shape; gains q/mine)
 \- view === 'table'    : JobsTable + footer  (unchanged shape; gains emptyMessage)
```

`JobsTimeline` internals:

```
GlassPanel
 |- header: "Timeline - jobs created in the last 24 hours"
 |          axis endpoints as local wall-clock; drawn/total caption
 |- truncation banner (when truncated): text + one button
 |- tick row (aria-hidden): three labels over a bottom-bordered rail
 \- ul
     \- li  (grid: name column | track)
         |- Link to /jobs/:id            the row's only tab stop
         \- track (position: relative, overflow hidden)
             |- now rule (aria-hidden, one pixel, pinned right)
             \- bar (aria-hidden, absolute, left/width percentages, minimum width)
         \- row text: "running - 72% - 14m"
```

### Data flow

`JobsPage` owns `qInput` (raw), `q` (debounced and trimmed), `mine`, `view`,
`timelineWindow`, `sort`, `filter` and the pager. It passes `q` and `mine` into all
three hooks. Nothing below `JobsPage` reads or writes a filter.

`useJobTimeline(enabled, window, q, mine)`:

```
anchorMs = floor(useNow(ANCHOR_STEP_MS).getTime() / ANCHOR_STEP_MS) * ANCHOR_STEP_MS
{ sinceIso, untilIso } = windowBounds(window, anchorMs)
useQuery({
  queryKey: ['job-timeline', sinceIso, untilIso, q, mine],
  queryFn: ({ signal }) => walkJobWindow({ sinceIso, untilIso, q, mine }, signal),
  enabled,
  gcTime: 60_000,
  placeholderData: keepPreviousData,
})
```

`walkJobWindow` fetches `listJobsInWindow(... cursor ...)` at
`TIMELINE_PAGE_SIZE`, appends, and repeats while all of: the last page's
`next_cursor` is non-empty; fewer than `TIMELINE_MAX_PAGES` pages have been
fetched; and `signal.aborted` is false. It returns
`{ jobs, total: lastPage.total, truncated: pagesFetched === TIMELINE_MAX_PAGES && lastPage.next_cursor !== '' }`.
Every page carries the identical `since`, `until`, `q`, `mine` by closure, and no
`sort` and no `status`.

### Accessibility

- The view switch is a `role="group"` named `Jobs view` with three
  `aria-pressed` buttons - the shape lane F shipped, extended by one.
- The window picker is a `role="group"` named `Timeline window` with three
  `aria-pressed` buttons, matching it.
- The search input has an accessible name (`Search jobs`), `type="search"` so it
  resolves as a searchbox, a placeholder naming name and owner email only, and a
  maximum length.
- The My-jobs toggle is a button with `aria-pressed`, named `My jobs`.
- The timeline is a `<section>` with an accessible name, containing a text
  paragraph that states the count, the axis endpoints and the ordering. That text
  is what a screen-reader user gets on entering the region; the bars are
  `aria-hidden` decoration.
- Each timeline row is an `<li>` containing one `<a>` (the job name, the only tab
  stop in the row) followed by the status, percent and duration as text.
- **The bars are not tab stops.** A per-bar stop would double the tab count and
  expose nothing the row text does not already carry, which is the same reasoning
  lane F used to decline a keyboard case for the lanes cards.
- The timeline is not a scroll container, so it needs no wrapper tab stop. That is
  a deliberate difference from `JobsLanes` and `components/holo/Table`, both of
  which carry one because they are scrollers with states that have no focusable
  descendants.

### Layout, at 320, 375 and 1280

- The toolbar row wraps. **The search input carries no fixed minimum width.**
  `UsersTab`'s copy of this control has one of 240 pixels; that is deliberately
  not copied here, because this toolbar is more crowded and the simplest thing
  that cannot overflow at 320 is a flex item with a zero minimum that takes the
  remaining space and wraps to its own full-width line when there is none. This is
  a difference from the existing page, not an oversight, and it is not a claim
  about `/admin/users`, which is measured green at 320 today.
- The timeline row is one two-column grid at every width: a fixed name track of
  about nine root-em units with a zero minimum and truncation, and the track
  taking the rest. No breakpoint. The panel has no minimum width, so it cannot
  widen the document.
- Honest limit: at 320 the track is roughly 140 pixels, which is a coarse axis for
  a seven-day window. This design does not invent a separate narrow-viewport
  timeline; it renders the same thing smaller and says so.
- The gate is `layout.spec.ts`: document, `<header>` and `<main>` scroll widths at
  or below the client width at 320, 375 and 1280.

### Load, failure and threat

- **Load.** Only one of the three views fetches at a time. The timeline's worst
  case is 3 requests and 600 enriched rows per 15 seconds; the table's steady
  state is 5 requests and 250 rows per 15 seconds. `since`/`until` on the default
  sort are served by the existing `created_at` index, so the timeline's window
  predicate is the cheap one. `q` is not index-servable at all, by design (lane JB
  chose `strpos` over an escapable `ILIKE`), and a `q` that matches nothing pays a
  full walk. The debounce reduces how many of those a typing user generates and
  bounds nothing else.
- **Failure.** A lane's error is contained to that lane. The timeline's error is
  the whole view, by Decision 15. A table error is unchanged. A storage failure in
  `usePersistedChoice` loses the preference for the session and does not lose the
  click.
- **Threat model.** No new endpoint. `mine` sends only the literal `true`; the
  identity is resolved from the bearer token server-side, so nothing the client
  sends can select another user's jobs. `q` is the user's own text, sent as a
  query parameter, matched with `strpos` where `%` and `_` are literal - there is
  no client-side pattern to escape. `since` and `until` are derived from the
  client's clock and are not privileged: a caller can already request any window
  by hand. No token, id or email is rendered anywhere it was not already rendered
  by `JobsTable`. Nothing in this lane writes.
- **Invariants.** Six of the seven are backend-shaped and untouched. The seventh
  in its frontend form - end the generation before releasing the resource - has a
  real subject here for the first time in a jobs-page slice: the timeline's page
  walk is an async continuation that a window change, a debounce landing, a
  toggle click or a view switch must end before its next fetch is released. That
  is Decision 6, and it is the only place in this lane where a missing line
  produces a silent extra load rather than a visible defect.

## What must NOT change, as a checkable gate

- **Zero-line diff:** `web/src/jobs/JobsTable.test.tsx`,
  `web/src/jobs/JobsPage.pager.test.tsx`, `web/src/jobs/useJobs.test.tsx`,
  `web/src/jobs/useJobs.enabled.test.tsx`, `web/src/jobs/status.test.ts`,
  `web/src/jobs/queryKeyDecoupling.test.tsx`, `web/src/jobs/lanes.test.ts`,
  `web/src/lib/useCursorPager.test.ts`, `web/src/lib/useDebouncedValue.test.ts`,
  and every existing test under `web/src/workers/`. Decision 16 is what makes this
  achievable rather than aspirational.
- **Exactly two existing tests change, and both are refuted premises:**
  - `web/src/jobs/JobsPage.test.tsx`, the test named `does not render the
    backend-blocked Timeline view, My-jobs, or search controls`. All three of its
    absence assertions become false. The test is **deleted**, not narrowed - there
    is nothing left of its subject - and its replacements are the positive
    assertions in the two new test files.
  - `web/src/jobs/JobsPage.lanes.test.tsx`, the test named `a stored value that is
    not the literal lanes falls back to the table view`. Its stored value changes
    from `timeline` to a string outside the allow-list, and its name changes to
    say allow-list rather than "the literal lanes". The property it pins is
    preserved and strengthened.
- Any other edit to an existing test is a behaviour change and must be justified
  in review, not absorbed.

## Acceptance criteria

Each names its test and the mutation that test kills.

| # | Criterion | Test | Mutation it kills |
|---|---|---|---|
| AC-1 | Typing in the search box sends `q` with the trimmed text on the table request | `JobsPage.filters.test.tsx` - "the search box sends q on the table request" | drop `q` from the query string builder in `listJobs` |
| AC-2 | A burst of keystrokes produces exactly one request carrying the final value | `JobsPage.filters.test.tsx` - "a burst of keystrokes issues one request" (rendered with a small `debounceMs`, on real timers, as `UsersTab.test.tsx` does) | replace `useDebouncedValue` with an identity function |
| AC-3 | Changing the search text drops the cursor. **The test pages forward first**; without that the cursor is already empty and the mutation is a no-op a neighbouring test would appear to kill | `JobsPage.filters.test.tsx` - "searching after paging forward drops the cursor" | remove `pager.resetPaging()` from the search change handler |
| AC-4 | The My-jobs toggle sends `mine=true` when pressed and omits it when not, and drops the cursor. Same paging-first requirement | `JobsPage.filters.test.tsx` - "My jobs sends mine=true and drops the cursor" | send `mine` unconditionally; remove the reset |
| AC-5 | `q` and `mine` reach all three views: the table request, all five lane requests, and every timeline page request | `JobsPage.filters.test.tsx` - "every view's request carries the active filters" | drop `q` from `listJobsByStatus`; drop `mine` from `listJobsInWindow` |
| AC-6 | With a filter active and zero rows, the table says no jobs match, not that there are none | `JobsPage.filters.test.tsx` - "an empty filtered table says no jobs match" | hard-code `JobsTable`'s empty message |
| AC-7 | The search input has an accessible name, resolves as a searchbox, names only name and owner email in its placeholder, and carries a maximum length of 200. Recorded as a structural pin: it cannot establish what a browser does with the attribute | `JobsPage.filters.test.tsx` - "the search box is named, is a searchbox, and is length-capped" | remove the attribute or the label |
| AC-8 | The view switch offers three options, persists the choice, and a remount restores it | `JobsPage.timeline.test.tsx` - "the view switch persists timeline" | drop `timeline` from the allow-list |
| AC-9 | A stored value outside the allow-list falls back to the table view | `JobsPage.lanes.test.tsx` - the edited fallback test, plus `usePersistedChoice.test.ts` | accept any stored string |
| AC-10 | In timeline view no unfiltered 50-row request and no per-lane request is issued | `JobsPage.timeline.test.tsx` - "the timeline view issues no table or lane request" | `enabled: true` on `useJobs` |
| AC-11 | The live indicator is lit while the timeline is fetching (asserted through a data attribute, not a class string) | `JobsPage.timeline.test.tsx` - "the live indicator tracks the timeline query" | drop the timeline branch from the `polling` expression |
| AC-12 | Every timeline page request carries `since`, `until`, `limit=200` and no `sort` and no `status`; page two carries the identical filters plus the cursor | `useJobTimeline.test.tsx` - "the walk repeats its filters on every page" | send the filters on page one only |
| AC-13 | The walk stops at the page cap: three requests and no fourth | `useJobTimeline.test.tsx` - "the walk stops at the page cap" | raise `TIMELINE_MAX_PAGES` |
| AC-14 | Truncation is decided by the last page's cursor, not by `drawn < total`. The fixture drains in two pages while `total` grows between them, and no banner appears | `useJobTimeline.test.tsx` - "a window that drains while total grows is not truncated" | `truncated = drawn < total` |
| AC-15 | A window change stops the previous walk: after switching, no further request carries the old `since` | `useJobTimeline.test.tsx` - "changing the window ends the previous walk" | stop passing `signal` to the page fetches |
| AC-16 | `windowBounds` is quantized: two calls milliseconds apart return identical bounds, and the half-open interval matches the server's | `timelineWindow.test.ts` - "the anchor is quantized" and "since is until minus the window" | remove the flooring |
| AC-17 | Every window has a shorter neighbour or an explicit null, and every window has ticks and a label; adding one without them is a tsc error | `timelineWindow.test.ts` - "every window has a label, ticks and a narrowing answer" | make the records partial |
| AC-18 | Bar geometry: a job with no `started_at` is an instant at `created_at`; a running job reaches the right edge; a finished job's span matches its start and finish; everything is clamped to the axis | `timelineGeometry.test.ts` - four named cases | swap the `started_at ?? created_at` fallback; drop the clamp |
| AC-19 | Each timeline row is a list item whose only tab stop is a link to that job, with the status and duration as text | `JobsTimeline.test.tsx` - "each row links to its job and states its status in text" | make the bar focusable; drop the row text |
| AC-20 | The timeline is a named region whose text states the count, the axis endpoints and that the window is over creation time | `JobsTimeline.test.tsx` - "the timeline describes its axis in text" | change the caption to say "the last 24 hours" without "created" |
| AC-21 | A truncated 7d window offers a narrowing button that selects 24h; a truncated 6h window offers the table view instead | `JobsTimeline.test.tsx` - "truncation offers the next shorter window, and the table at the shortest" | return a shorter window for `6h` |
| AC-22 | An empty window says so, and says it differently when a filter is active | `JobsTimeline.test.tsx` - "an empty window distinguishes none from none matching" | hard-code one message |
| AC-23 | A failed walk shows one error and a Retry for the whole view, and existing data survives a failed refresh | `JobsTimeline.test.tsx` - "a failed walk shows one error with retry" | render partial rows on error |
| AC-24 | The lane caption reads "matching" while a filter is active and "total" otherwise | `JobsLanes.test.tsx` - one added case | hard-code "total" |
| AC-25 | `usePersistedChoice` returns the fallback for an absent, invalid or throwing read, and a write failure does not throw | `usePersistedChoice.test.ts` - four cases | remove the try, remove the allow-list check |
| AC-26 | The Workers page keeps its behaviour after adopting the hook, with a zero-line test diff, and both switches carry a group role and name | existing `web/src/workers/` suite, plus one added group-role case | drop the group name |
| AC-27 | The timeline surface does not overflow the document, `<header>` or `<main>` at 320, 375 or 1280 | `web/e2e/layout.spec.ts` via a new `jobs-timeline` entry in `surfaces.ts` | give the timeline panel a minimum width |
| AC-28 | Zero files changed under `internal/`, `cmd/`, `python/`; the two existing-test edits above and no others | review, plus the full `web/src` suite green | - |

### The e2e surface

One new entry in `web/e2e/surfaces.ts`, following `jobs-lanes` exactly:

```
name: 'jobs-timeline', path: () => '/jobs', population: 'populated',
prepare: (p) => p.addInitScript(() => window.localStorage.setItem('relay.jobs.view', 'timeline')),
ready: gate on the timeline region containing the link named seed.jobName
```

`prepare` is justified under the file's own rule: it fabricates no data, it sets
the same preference key the shipped switch writes, and it must run before the
SPA's first render, which is what `addInitScript` is for. The `ready` gate is
scoped to the timeline region rather than to a bare link, so a run where the
seeded job is not drawn fails loudly instead of measuring an empty timeline under
a populated name. The seeded job is created at seed time and never leaves
`pending` (no agent runs in slice 1), so it falls inside the default 24-hour
window and draws as the instant marker of Decision 9 - which makes this surface
the only automated check that the never-started case renders at all.

**State the limit in the surface's comment.** What it establishes: the timeline
does not widen the document, the header or the main region at three widths. What
it cannot: whether a bar is legible, or whether the name column has truncated a
job name to nothing. This view is not a horizontal scroller, so the gate's known
blind spot does not apply the way it does to `jobs-lanes`; but the bar track has
hidden overflow, which is a clip of the same kind, one level down. The screenshots
are the artifact and someone has to open them.

**No `keyboard.spec.ts` case is added.** The two existing cases exist because
their tables have zero focusable elements per row. Every timeline row has a link,
so the rows are reachable by ordinary tabbing, and there is no wrapper tab stop to
exercise because the view is not a scroller.

**No new surface is added for the search box or the toggle.** Both live in the
existing `jobs` surface's toolbar row, which that surface already measures at
three widths - and the toolbar is exactly where the added-width risk is.

## Gates

- `cd web && npm test`
- `cd web && npx tsc -b --force`
- `cd web && npm run build`
- `make test-e2e`, which needs Docker Desktop running and a Postgres at
  `postgres://relay:relay@127.0.0.1:5432` - `docker start relay-postgres`, or
  `scripts/dev.ps1` once to create it. Browsers installed once with
  `cd web && npx playwright install chromium webkit`. Run it from Git Bash. If
  `make` is not on PATH, use the MSYS2 copy with the variable forwarding
  `web/e2e/README.md` documents.
- `git checkout -- web/dist/` before assembling the PR. `web/dist` is tracked but
  not maintained per-PR, and `make test-e2e` writes into it.
- No Go gate is required or run: this lane changes no Go file. Say that plainly in
  the PR rather than reporting a Go lane that was not the subject.

## Risks and the merge surface

- `web/src/jobs/JobsPage.tsx` is the file every jobs lane touches. This lane edits
  the header cluster, the toolbar row, the hook calls and adds a branch. Any
  concurrent lane editing the table branch or the pagination footer will conflict
  textually; the resolution is mechanical.
- `web/src/workers/WorkersPage.tsx` is edited only by Decision 12's refactor. If a
  concurrent lane is editing that file, raise it rather than merging blind - the
  refactor's whole gate is that Workers' behaviour is unchanged, and that argument
  does not survive a three-way merge nobody re-ran the tests against.
- The `useJobLanes` and `listJobsByStatus` changes touch a view that merged
  yesterday. Its tests must stay green unedited apart from AC-24's addition.
- Residual: if the implementer finds that TanStack does not abort the previous
  walk on a key change (Decision 6), the hook needs an explicit cancel, which is a
  larger change than the design shows. Measure it early, not at the end.

## Backlog items this closes

Closing each is required scope, through `/backlog close <fragment>`, which does
the `git mv` into `docs/backlog/closed/`. A hand-edited `status` leaves the file
in the open directory and `/backlog list` reports it malformed.

1. `idea-2026-06-05-job-search-box-q-filter` - note in the resolution that the
   composition question the item raised is answered in the README's Filtering the
   jobs list section and in Decision 1 here, and that the debounce is an additive
   measure rather than a bound.
2. `idea-2026-06-05-my-jobs-toggle-mine-filter` - note that the shipped parameter
   is `mine=true` resolved from the token, not the `submitted_by` the generic
   filters item proposed.
3. `idea-2026-06-05-jobs-timeline-view` - note the three things the shipped view
   deliberately does not do: it is paged and capped rather than unpaginated, it
   windows on creation time rather than activity, and its tick row is three
   relative labels rather than the hi-fi's five.
4. `idea-2026-09-02-extract-usepersistedview` - note the rename to
   `usePersistedChoice` and its reason, and that both pages adopted it.

Explicitly **not** closed and **not** touched:
`idea-2026-09-02-jobs-lanes-fluid-columns-at-desktop` (this lane changes no lanes
layout, and the timeline is deliberately not a second horizontal scroller) and
`idea-2026-09-02-cards-per-lane-stepper` (still open; its parenthetical about
`keepPreviousData` is satisfied here for a different reason and is no longer
remaining work).

## Proposed follow-up backlog items

Proposals only. The human accepts or drops each; none is filed by this lane.

1. **bug, medium - `GET /v1/jobs` has no rate limit and `?q=` is an unindexed
   scan.** Sibling of the open `post-v1-jobs-is-not-rate-limited`. The README's
   own cost note says the bound is table size and client behaviour. The client
   debounce is not a control and this lane must not be read as having added one.
2. **idea - the Timeline windows on creation time, so a long-running job that
   started before the window is invisible.** Needs a server predicate over the
   lateral aggregate's `started_at`/`finished_at`, which lane JB ruled out; that
   probably means the CTE restructuring lane JB also proposed.
3. **idea - a silent failed refresh is invisible in all three jobs views.** The
   table view has had this gap since it shipped; the timeline inherits it. One
   indicator, one place.
4. **idea - filter results are not announced to a screen reader.** Changing a
   search box changes a table with no live region. Related to the open
   route-change focus and announcement policy item; worth one decision covering
   both rather than two.
5. **idea - the timeline tick row scrolls away on a long list.** A sticky rail is
   the obvious answer and needs a check that no ancestor clips it.
6. **idea - the timeline at 320 gives a seven-day axis about 140 pixels.** If
   narrow viewports matter for this view, it needs a different narrow treatment,
   not a smaller one.
7. **idea - `useJobs` reaches seven positional parameters** (Decision 16).
   Converting it and `useJobLanes` to an options object is a behaviour-preserving
   refactor whose gate is a byte-identical test diff, which is exactly what this
   lane could not afford while changing their behaviour.

## Escalations

Calls a human might reasonably make the other way.

1. **Do not thread `q` and `mine` into the Lanes view** (Decision 2). It keeps
   this lane off a surface that merged yesterday and keeps the lane caption's
   meaning fixed, at the cost of two page-level controls that silently do nothing
   in one of three views. I judged the silent exemption worse.
2. **Do not extract `usePersistedChoice` here** (Decision 12). Deferring keeps
   the lane narrower and off `WorkersPage` entirely, at the cost of shipping a
   fourth divergent copy of a switch an open item already says should have been
   extracted at the third.
3. **A page cap of 400 rather than 600** (Decision 7), or a cap expressed in rows
   rather than pages. Six hundred is already past readable; a reviewer who weighs
   the first-paint latency of three sequential round trips more heavily than the
   completeness of the window would pick two pages.
4. **Five ticks with a breakpoint rather than three at every width**
   (Decision 11). The hi-fi draws five and this is a desktop-first product; I
   chose one tick set that is correct everywhere over a breakpoint that makes the
   measured widths differ from the shipped ones.
5. **Persist the timeline window, or do not** (Decision 12's second key). I
   persist it for the same reason the view is persisted. A human might prefer that
   a time window is per-visit state and always opens at 24 hours.
6. **An anchor step other than 15 seconds** (Decision 8). It is the whole
   staleness budget of the view and the whole refresh cost, in one number. A
   shorter step makes the timeline livelier and re-walks more often; a longer one
   is cheaper and staler.
7. **Show the timeline's partial walk on a mid-walk failure** (Decision 15)
   rather than failing the view. Partial data is better than none for an operator
   who just wants to see what is running; it is worse for anyone who reads the
   window label as a claim about completeness.
8. **An options object instead of appended parameters** (Decision 16), accepting
   the edit to two tests this lane's gate protects. Cleaner code now, a weaker
   claim that the hooks' existing behaviour is unchanged.

## Amended after review

A combined review of the implemented lane reproduced structural defects in
jsdom that this spec's design did not anticipate. Three Decisions above are
superseded by the fix; each is stated here rather than edited in place, so the
original reasoning stays legible next to what replaced it.

**Decision 8 (the time anchor and the refresh cadence) is superseded.** The
chosen design - no `refetchInterval`, a `useNow`-driven render tick whose
advance changed the query key - had a liveness bug the spec did not predict:
every tick minted a new key, so a walk slower than one `ANCHOR_STEP_MS` never
completed. The tick abandoned the in-flight query, its consumed signal
aborted, and the next tick restarted the walk at page 1 - forever, for any
walk slower than a tick. The key is now STABLE per window and filters; the
anchor is computed once, inside the queryFn, at the moment each fetch starts,
and travels back out on the walk's own result so the caption always describes
the rows it looks at. The refresh is now `refetchInterval: ANCHOR_STEP_MS`,
which TanStack does not fire while a fetch is already in flight for the same
key - the mechanism this spec reached for (a ticking key) is exactly the one
that could not deliver the property the spec wanted (a live, un-abandoned
walk). The staleness budget itself is unchanged: `ANCHOR_STEP_MS` is still the
whole number, now stated as one interval plus however long the refresh's walk
takes, rather than as "how often the query key moves."

**Decision 15 (one error for the whole Timeline) is narrowed.** The design
was right that a partial walk under the window's own label is a chart that
lies, and that stays true. What the design did not distinguish is a REFRESH
failing versus a FIRST fetch failing: `error: query.data ? null : ...`
suppressed the error field whenever any data - including a `keepPreviousData`
placeholder - was present, so a failed background refresh was invisible: the
caption kept describing bounds as though the fetch had succeeded. The error
is no longer suppressed by data's presence. When rows exist alongside an
error, the view now shows both - the rows, plus a "Refresh failed, showing
results as of `<untilIso>`" line and Retry - rather than either the full rows
with no failure indicator or the full error block with no rows. The full
error block (no rows, one message, one Retry) is unchanged for the case the
original Decision was written for: a first fetch, or every attempt so far,
failing. Getting the stale rows to actually stay on screen needed an
additional mechanism the review did not ask for by name: `keepPreviousData`
turned out to fill `query.data` only while the new key's fetch is PENDING, not
once it definitively resolves to an error, so the hook now also keeps its own
`lastSuccess` reference to the last walk that returned data, independent of
TanStack's placeholder lifecycle.

**Decision 5 (cursor reset on the raw keystroke) is extended, not reversed.**
The spec's choice - reset in the change handler, on the raw value, never in an
effect on the debounced value - is unchanged and still correct for the reason
given: an effect on the debounced value fires after the render that already
issued a query pairing the new filter with the old cursor. What the spec did
not consider is the window BETWEEN a keystroke and the debounce landing: the
per-keystroke reset already drops the cursor immediately, but the QUERY still
reads the old debounced value until the timer fires, so a click on next/prev
inside that window can mint a new cursor - one belonging to whatever filters
were active a moment ago - and the debounced value then carries it into a
request for the filters that are about to become current. The fix adds a
second, narrower mechanism on top of the original one rather than replacing
it: next/prev are now disabled for the width of that window, and a debounced-
value-change effect resets the pager as a second line of defence. Extracted to
`web/src/lib/useDebouncedPagingGuard.ts` as a shared shape, since the same
race applies wherever a page owns both a debounced filter and a cursor pager -
the Schedules page's own search box, when it lands, is expected to reuse it
rather than reintroduce the race independently.
