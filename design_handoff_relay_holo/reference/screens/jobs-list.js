// Jobs list (home) wireframes — updated to match chadmv/relay@main
//   - Status set: pending · queued · dispatched · running · done · failed · timed_out · cancelled
//   - Job rows carry submitted_by_email (user-friendly owner column)
//   - Priority: high | normal | low
//   - Filter: ?status= and ?scheduled_job_id= are server-supported; rest are client-side
//   - Cancel returns 409 if job already terminal; ?force=true skips drain/cleanup
//   - PAGINATION (SHIPPED — internal/api/pagination.go + jobs.go):
//       GET /v1/jobs?limit=<1..200>&cursor=<opaque>
//       default limit=50, max=200; bad limit/cursor → 400
//       response envelope: { items: [...], next_cursor: "...", total: 2341 }
//       cursor is base64url(JSON{t,i}) of the last-seen (created_at,id) — opaque to clients
//       stable under concurrent inserts (page 2 stays bounded by page 1's cursor)
//       same shape on ?status= and ?scheduled_job_id= branches
//     Swimlanes: each lane is a separate /v1/jobs?status=<s>&limit=<perLane> call,
//       capped (default 10, min 3, max 50) — overflow shows "+ N more →" linking to the
//       table filtered by that status. The ?limit= cap is what enforces the per-lane size.
//     Timeline view is time-windowed (6h/24h/7d) so it doesn't need cursors.
window.JobsList = (function(){
  const C = Shell.chrome;

  // [name, status, pct, started, dur, tasks, owner_email, priority, source]
  const SAMPLE_JOBS = [
    ['film-x / shot-042 render', 'running',    72, 'Apr 17 · 14:22', '14m',     '48/64 tasks', 'mira@studio.dev','high',  'cli'],
    ['nightly etl — march',      'running',    38, 'Apr 17 · 12:24', '2h 14m',  '19/50 tasks', 'system',         'normal','schedule:nightly-etl'],
    ['blender-denoise batch',    'running',    91, 'Apr 17 · 13:58', '38m',     '210/230',     'ada@studio.dev', 'normal','cli'],
    ['ci: relay-server #2341',   'done',      100, 'Apr 17 · 14:30', '4m 12s',  '12/12',       'ci-bot@studio.dev','low','cli'],
    ['ci: relay-agent #2340',    'failed',    100, 'Apr 17 · 14:28', '1m 08s',  '8/12 ❌',     'ci-bot@studio.dev','low','cli'],
    ['frames 1-1000 / teaser',   'queued',      0, 'Apr 17 · 14:31', '—',       '0/1000',      'ada@studio.dev', 'normal','cli'],
    ['ml-eval — resnet50',       'done',      100, 'Apr 16 · 23:45', '18m 02s', '8/8',         'jin@studio.dev', 'normal','schedule:weekly-eval'],
    ['proxy-encode s03e01',      'cancelled', 44, 'Apr 17 · 14:10', '7m',       '11/25',       'mira@studio.dev','normal','cli'],
    ['oom-canary',               'timed_out', 100, 'Apr 17 · 13:02', '1h 00m',  '1/1',         'jin@studio.dev', 'low',   'cli'],
    ['ci: relay-server #2339',   'done',      100, 'Apr 17 · 13:55', '3m 42s',  '12/12',       'ci-bot@studio.dev','low','cli'],
    ['ci: relay-agent #2338',    'done',      100, 'Apr 17 · 13:54', '2m 18s',  '12/12',       'ci-bot@studio.dev','low','cli'],
    ['shot-041 render',          'done',      100, 'Apr 17 · 13:30', '22m 04s', '64/64',       'mira@studio.dev','high','cli'],
    ['shot-040 render',          'done',      100, 'Apr 17 · 12:50', '19m 38s', '64/64',       'mira@studio.dev','high','cli'],
    ['nightly etl — feb',        'done',      100, 'Apr 17 · 02:00', '1h 48m',  '50/50',       'system',        'normal','schedule:nightly-etl'],
    ['ml-eval — bert-base',      'done',      100, 'Apr 17 · 01:14', '12m 09s', '6/6',         'jin@studio.dev','normal','schedule:weekly-eval'],
    ['db-backup',                'done',      100, 'Apr 17 · 04:15', '6m 52s',  '3/3',         'system',        'normal','schedule:db-backup'],
    ['proxy-encode s03e02',      'done',      100, 'Apr 17 · 11:08', '14m 21s', '25/25',       'mira@studio.dev','normal','cli'],
    ['frames 500-999 / s02',     'done',      100, 'Apr 17 · 09:45', '1h 12m',  '500/500',     'ada@studio.dev', 'normal','cli'],
    ['cache-warm',               'done',      100, 'Apr 17 · 08:00', '4m 40s',  '8/8',         'system',        'low',   'schedule:cache-warm'],
    ['lint-and-vet',             'failed',    100, 'Apr 17 · 14:18', '0m 28s',  '1/2 ❌',      'ci-bot@studio.dev','low','cli'],
    ['smoke-tests',              'cancelled', 18, 'Apr 17 · 14:02', '2m',       '2/14',        'jin@studio.dev', 'low',   'cli'],
  ];

  function statusClass(s) {
    if (s==='running' || s==='dispatched') return 'running';
    if (s==='pending' || s==='queued')     return 'pending';
    if (s==='done')                        return 'done';
    if (s==='failed' || s==='timed_out')   return 'failed';
    if (s==='cancelled')                   return 'cancelled';
    return '';
  }

  function jobRow(j, compact) {
    const [name, status, pct, started, dur, tasks, owner, priority, source] = j;
    const cls = statusClass(status);
    const fillCls = cls==='done'?'ok':(cls==='failed'?'fail':'');
    const prChip = priority==='high' ? `<span class="chip" style="font-size:9.5px; padding:0 5px; transform:none; color:var(--accent); border-color:var(--accent)">HIGH</span>`
                  : priority==='low' ? `<span class="chip mute" style="font-size:9.5px; padding:0 5px; transform:none">low</span>`
                  : '';
    const srcMark = source.startsWith('schedule:')
      ? `<span class="chip mute" style="font-size:9.5px; padding:0 5px; transform:none" title="from schedule">⟳ ${source.slice(9)}</span>`
      : '';
    return `
      <tr>
        <td class="job-name">${name} ${prChip} ${srcMark}</td>
        <td><span class="status ${cls}">${status}</span></td>
        <td style="width:90px;">
          <div class="progress"><div class="fill ${fillCls}" style="width:${pct}%;"></div></div>
        </td>
        <td class="mono mute-txt" style="width:50px;">${pct}%</td>
        <td class="mono small" style="width:92px;">${started}</td>
        <td class="mono" style="width:60px;">${dur}</td>
        ${compact?'':`<td class="small">${tasks}</td><td class="small">${owner}</td>`}
      </tr>
    `;
  }

  // Variation 1: Classic dense table — paginated, view-toggle present
  function v1Table() {
    return `
      <div class="screen">
        ${C('relay.studio.dev/jobs?limit=50&cursor=eyJ0IjoiMjAyNi0wNC0xN1QxMjowMzo0OS4yMVoiLCJpIjoiM2Y4Yi05Y2EwIn0')}
        <div class="screen-body">
          ${Shell.sidebar('jobs')}
          ${Shell.topbar('jobs')}
          <div class="main">
            <div class="row" style="justify-content:space-between; align-items:baseline;">
              <h1 class="page-title">Jobs <span class="sub">2,341 total · 3 running · 1 queued</span></h1>
              <div class="row">
                <span class="chip active">☰ table</span>
                <span class="chip">⊞ lanes</span>
                <span class="chip">⌚ timeline</span>
                <button class="btn accent">+ New job</button>
              </div>
            </div>
            <div class="toolbar">
              <input class="search" value="🔍  search by name, label, owner email…" readonly />
              <span class="chip active">all</span>
              <span class="chip">running</span>
              <span class="chip">queued</span>
              <span class="chip">done</span>
              <span class="chip">failed</span>
              <span class="chip">cancelled</span>
              <span class="chip dashed">+ label:project=film‑x</span>
              <span class="grow"></span>
              <span class="chip" style="border-color:var(--accent); color:var(--accent)">👤 mine only</span>
            </div>
            <div class="box" style="padding:4px 8px; flex:1; overflow:hidden; display:flex; flex-direction:column;">
              <table class="tbl" style="flex:1;">
                <thead><tr>
                  <th>Job</th><th>Status</th><th>Progress</th><th></th><th>Started</th><th>Duration</th><th>Tasks</th><th>Owner email</th>
                </tr></thead>
                <tbody>${SAMPLE_JOBS.map(j=>jobRow(j,false)).join('')}</tbody>
              </table>
              <div class="row" style="justify-content:space-between; align-items:center; padding:6px 4px 2px; border-top:1.5px dashed var(--mute-2); margin-top:4px;">
                <span class="small mute-txt">showing <b>1–50</b> of <b>2,341</b> · cursor-paginated · sorted by created_at desc, id desc</span>
                <div class="row" style="gap:4px;">
                  <span class="chip mute" title="prev requires keeping a cursor stack client-side — no server prev_cursor">← prev</span>
                  <span class="chip" title="GET /v1/jobs?cursor=eyJ0IjoiMjAy…&limit=50">next 50 →</span>
                  <span class="chip dashed" title="server clamps to [1, 200]">page size: 50</span>
                </div>
              </div>
            </div>
          </div>
        </div>
        <div class="annot" style="top:36%; right:2%; font-size:11px; max-width:160px; line-height:1.25;">opaque cursor —<br/>base64url(JSON{t,i})<br/>survives mid-page<br/>inserts ✓</div>
      </div>
    `;
  }

  // Variation 2: Swimlanes by status — each lane independently capped, "+N more"
  // Per-lane cap is user-customizable; default 10. Stored in localStorage('relay.lanesPerLane')
  // and surfaced as a "cards/lane: [10]" stepper in the toolbar.
  function v2Swimlanes() {
    const lane = (label, status, total, items, cap) => {
      const shown = items.slice(0, cap);
      const overflow = total - shown.length;
      return `
      <div class="col" style="flex:1; min-width:0;">
        <div class="row" style="justify-content:space-between; align-items:center; padding:0 2px;">
          <span class="box-title" style="font-size:18px;">${label}</span>
          <span class="chip mute">${total}</span>
        </div>
        <div class="col" style="gap:6px;">${shown.map(j=>`
          <div class="box" style="padding:6px 8px;">
            <div style="font-size:11.5px; font-weight:700;">${j[0]}</div>
            <div class="progress" style="margin:4px 0;"><div class="fill ${status==='done'?'ok':status==='failed'?'fail':''}" style="width:${j[2]}%"></div></div>
            <div class="row small" style="justify-content:space-between;">
              <span>${j[4]}</span><span class="mute-txt">${j[6]}</span>
            </div>
          </div>`).join('')}
          ${overflow > 0 ? `
            <div class="box dashed" style="padding:6px 8px; text-align:center; color:var(--mute); font-size:11px;">
              + ${overflow} more
              <div class="small" style="color:var(--accent); margin-top:2px;">view all in table →</div>
            </div>` : ''}
        </div>
      </div>
    `;
    };
    const cap = 10;
    const fakeDone   = Array.from({length:487}, (_,i)=>['ci-build #'+(2341-i), 'done', 100, '', i+'m', '', 'ci-bot@studio.dev', 'low', 'cli']);
    const fakeFailed = [SAMPLE_JOBS[4], SAMPLE_JOBS[8], ...Array.from({length:10},(_,i)=>['ci #'+(2300-i),'failed',100,'',i+'h','','ci-bot@studio.dev','low','cli'])];
    return `
      <div class="screen">
        ${C('relay.studio.dev/jobs?view=lanes&per_lane=10')}
        <div class="screen-body">
          ${Shell.sidebar('jobs')}
          ${Shell.topbar('jobs')}
          <div class="main">
            <div class="row" style="justify-content:space-between; align-items:baseline;">
              <h1 class="page-title">Jobs <span class="sub">kanban · per-lane cap configurable</span></h1>
              <div class="row">
                <span class="chip">☰ table</span>
                <span class="chip active">⊞ lanes</span>
                <span class="chip">⌚ timeline</span>
                <span class="chip dashed" title="max cards shown per lane · stored per-user">
                  cards/lane:
                  <span style="display:inline-flex; align-items:center; gap:3px; margin-left:4px;">
                    <span class="mono" style="cursor:pointer; padding:0 4px; border:1px dashed var(--mute); border-radius:3px;">−</span>
                    <input type="text" value="${cap}" style="width:24px; text-align:center; background:transparent; border:none; font-family:var(--hand); font-weight:700; font-size:13px; color:var(--ink);" />
                    <span class="mono" style="cursor:pointer; padding:0 4px; border:1px dashed var(--mute); border-radius:3px;">+</span>
                  </span>
                </span>
                <button class="btn accent">+ New</button>
              </div>
            </div>
            <div class="row" style="gap:10px; align-items:stretch; flex:1; overflow:hidden;">
              ${lane('Pending / Queued','pending', 5,[SAMPLE_JOBS[5]], cap)}
              ${lane('Running','running', 3, [SAMPLE_JOBS[0],SAMPLE_JOBS[1],SAMPLE_JOBS[2]], cap)}
              ${lane('Done','done', 487, fakeDone, cap)}
              ${lane('Failed / Timed out','failed', 12, fakeFailed, cap)}
            </div>
          </div>
        </div>
        <div class="annot" style="top:18%; right:3%; font-size:12px; max-width:140px;">cards/lane stepper —<br/>persists per user.<br/>min 3, max 50.</div>
      </div>
    `;
  }

  // Variation 3: Timeline / gantt-ish — already time-windowed
  function v3Timeline() {
    const bar = (name, start, width, status, pct) => {
      const cls = statusClass(status);
      const fill = cls==='done'?'var(--ok-soft)':cls==='failed'?'var(--accent-soft)':cls==='cancelled'?'var(--paper-2)':'var(--warn-soft)';
      const bd = cls==='done'?'var(--ok)':cls==='failed'?'var(--accent)':cls==='cancelled'?'var(--mute)':'var(--warn)';
      return `
      <div style="display:grid; grid-template-columns: 130px 1fr; gap:8px; align-items:center; padding:3px 0; border-bottom:1px dashed var(--mute-2);">
        <div style="font-size:11px; font-weight:700; overflow:hidden; text-overflow:ellipsis; white-space:nowrap;">${name}</div>
        <div style="position:relative; height:16px;">
          <div style="position:absolute; left:${start}%; width:${width}%; height:100%;
                      background:${fill}; border:1.3px solid ${bd};
                      border-radius:3px; display:flex; align-items:center; padding-left:6px;
                      font-size:10px; overflow:hidden; white-space:nowrap;">
            ${pct?pct+'%':''}
          </div>
        </div>
      </div>`;
    };
    return `
      <div class="screen">
        ${C('relay.studio.dev/jobs?view=timeline&window=24h')}
        <div class="screen-body">
          ${Shell.sidebar('jobs')}
          ${Shell.topbar('jobs')}
          <div class="main">
            <div class="row" style="justify-content:space-between; align-items:baseline;">
              <h1 class="page-title">Jobs <span class="sub">last 24h · time-windowed (no pagination needed)</span></h1>
              <div class="row">
                <span class="chip">☰ table</span>
                <span class="chip">⊞ lanes</span>
                <span class="chip active">⌚ timeline</span>
                <span class="chip">6h</span><span class="chip active">24h</span><span class="chip">7d</span>
                <button class="btn accent">+ New</button>
              </div>
            </div>
            <div class="box" style="padding:10px; flex:1; overflow:hidden;">
              <div class="row small" style="justify-content:space-between; margin-bottom:6px; color:var(--mute);">
                <span>00:00</span><span>06:00</span><span>12:00</span><span>18:00</span><span style="color:var(--accent)">now</span>
              </div>
              <div style="border-top:1.5px solid var(--ink); padding-top:4px;">
                ${bar('film‑x / shot‑042', 30, 45, 'running', 72)}
                ${bar('nightly etl ⟳',    5, 85, 'running', 38)}
                ${bar('blender-denoise', 55, 28, 'running', 91)}
                ${bar('ci #2341',        48, 5,  'done', 100)}
                ${bar('ci #2340',        46, 4,  'failed', 100)}
                ${bar('teaser frames',   92, 3,  'queued', 0)}
                ${bar('ml-eval ⟳',       18, 12, 'done', 100)}
                ${bar('oom-canary',      28, 22, 'timed_out', 100)}
                ${bar('proxy-encode',    72, 8,  'cancelled', 44)}
              </div>
            </div>
          </div>
        </div>
      </div>
    `;
  }

  function render(host) {
    host.innerHTML = `
      <div class="section-intro">
        <h2>Jobs — the home view</h2>
        <p>List-first with filters that map to <span class="mono">GET /v1/jobs?status=…</span>. Status set is now <b>pending · queued · dispatched · running · done · failed · timed_out · cancelled</b>; we collapse to 4 visual buckets. Owner = <span class="mono">submitted_by_email</span>. <span class="scribble">⟳ chip marks jobs spawned by a schedule.</span></p>
      </div>
      <div class="legend">
        <b>Status colors:</b>
        <span><span class="dot" style="background:var(--warn); color:var(--warn)"></span> running / dispatched</span>
        <span><span class="dot" style="background:var(--ok); color:var(--ok)"></span> done</span>
        <span><span class="dot" style="background:var(--accent); color:var(--accent)"></span> failed / timed_out</span>
        <span><span class="dot" style="color:var(--mute)"></span> pending · queued · cancelled</span>
      </div>

      <div class="box filled" style="padding:10px 12px; margin-bottom:10px; font-size:12px; line-height:1.45;">
        <div class="margin-note" style="margin-top:0; color:var(--ok); border-color:var(--ok);">Pagination model — shipped ✓</div>
        <ul style="padding-left:18px; margin:4px 0 6px;">
          <li><b>Endpoint:</b> <span class="mono">GET /v1/jobs?limit=&lt;1..200&gt;&amp;cursor=&lt;opaque&gt;</span> · default limit <b>50</b>, server clamps; bad limit or bad cursor → <span class="mono">400</span>.</li>
          <li><b>Response:</b> <span class="mono">{ items, next_cursor, total }</span> — total is unbounded; empty <span class="mono">next_cursor</span> means last page.</li>
          <li><b>Cursor:</b> opaque <span class="mono">base64url(JSON&lbrace;t,i&rbrace;)</span> of last-seen <span class="mono">(created_at, id)</span>. Page 2 stays stable when rows are inserted mid-paging — covered by <span class="mono">jobs_pagination_test.go::TestListJobs_StableUnderInsertMidPage</span>.</li>
          <li><b>Filter branches:</b> <span class="mono">?status=</span> and <span class="mono">?scheduled_job_id=</span> share the same envelope; <span class="mono">scheduled_job_id</span> auth-gates before paginating (404 for non-owners, never an empty page).</li>
          <li><b>Swimlanes:</b> each lane fires <span class="mono">?status=&lt;s&gt;&amp;limit=&lt;perLane&gt;</span>; per-lane cap is a user-controlled stepper (default 10, min 3, max 50; clamp at 200 server-side). Overflow → <b>+ N more →</b> link to the table filtered by status.</li>
          <li><b>Timeline:</b> bounded by the 6h/24h/7d window — no cursors needed.</li>
          <li><b>UX gap:</b> server returns only <span class="mono">next_cursor</span>, never <span class="mono">prev_cursor</span> — client maintains a cursor stack to back up. "jump to page N" isn't possible (and shouldn't be — it'd defeat the stable-paging guarantee).</li>
        </ul>
      </div>

      <div class="variations stack">
        <div class="variant">
          <div class="variant-label"><span class="num">1</span>Dense table with saved filters <span class="tag" style="color:var(--ok); border-color:var(--ok)">♥ default</span></div>
          <div class="variant-note">Power-user default. Owner = email (<span class="mono">submitted_by_email</span>). View toggles to lanes/timeline. Pagination footer shows total + page controls.</div>
          ${v1Table()}
        </div>
        <div class="variant">
          <div class="variant-label"><span class="num">2</span>Swimlanes by status <span class="tag">capped lanes</span></div>
          <div class="variant-note">Dispatched merges with running; failed merges with timed_out. <b>cards/lane stepper</b> in toolbar (default 10) — overflow becomes "+ N more →" to filtered table. Solves the "500 done jobs crushes the lane" problem.</div>
          ${v2Swimlanes()}
        </div>
        <div class="variant">
          <div class="variant-label"><span class="num">3</span>Timeline view <span class="tag">pairs with admin dash</span></div>
          <div class="variant-note">Time-windowed by 6h/24h/7d — no pagination needed. Reveals overlap + throughput at a glance; worse for scanning names.</div>
          ${v3Timeline()}
        </div>
      </div>
    `;
  }

  return { render };
})();
