// Admin overview + invites + agent enrollments + scheduled jobs + users
// Updated to chadmv/relay@main:
//   - Invites: POST /v1/invites  (email optional, expires_in default 72h, max 720h)
//   - Agent enrollments (NEW SURFACE): POST /v1/agent-enrollments  (hostname_hint, ttl_seconds; default 24h, max 7d)
//     Listed via GET /v1/agent-enrollments (active = unconsumed + unexpired) — PAGINATED
//   - Scheduled jobs (NEW SURFACE): cron + tz + overlap_policy + enabled, per-owner — PAGINATED
//     (admins see all schedules; owners see only theirs — same envelope)
//   - Users (admin): list + create + archive/unarchive + admin password reset — PAGINATED
//     (?include_archived=true switches to ListUsersIncludingArchivedPage; ?email=<exact> wraps a single hit in the same envelope)
//   - All list endpoints share { items, next_cursor, total } + ?limit=&?cursor= contract
window.Admin = (function(){
  const C = Shell.chrome;

  // Table density now lives in styles.css (.screen .tbl) — applied globally.

  function sparkline(w=120,h=32, points='0,24 10,18 20,22 30,12 40,14 50,8 60,10 70,6 80,14 90,9 100,4 110,8 120,2') {
    return `<svg viewBox="0 0 ${w} ${h}" width="100%" height="${h}" preserveAspectRatio="none">
      <polyline points="${points}" fill="none" stroke="var(--ink)" stroke-width="1.5" stroke-linejoin="round"/>
      <polyline points="${points} ${w},${h} 0,${h}" fill="rgba(230,236,245,0.08)" stroke="none"/>
    </svg>`;
  }

  function bars() {
    return `<svg viewBox="0 0 200 60" width="100%" height="60">
      ${Array.from({length:24}).map((_,i)=>{
        const h = 8 + Math.abs(Math.sin(i*0.6))*40 + (i%5===0?8:0);
        return `<rect x="${i*8+2}" y="${60-h}" width="5" height="${h}" fill="${i>=20?'var(--accent)':'var(--ink)'}" opacity="${i>=20?0.95:0.85}"/>`;
      }).join('')}
      <line x1="0" y1="60" x2="200" y2="60" stroke="var(--ink)" stroke-width="1.2" opacity="0.6"/>
    </svg>`;
  }

  // ────────────────────────── Overview ──────────────────────────
  function dashboard() {
    return `
      <div class="screen wide">
        ${C('relay.studio.dev/overview')}
        <div class="screen-body">
          ${Shell.sidebar('overview')}
          ${Shell.topbar('overview')}
          <div class="main">
            <div class="row" style="justify-content:space-between; align-items:baseline;">
              <h1 class="page-title">Fleet overview <span class="sub">last 24h · live</span></h1>
              <div class="row">
                <span class="chip">1h</span><span class="chip active">24h</span><span class="chip">7d</span>
                <button class="btn ghost">export csv</button>
              </div>
            </div>

            <div class="row" style="gap:8px; flex: 0 0 auto;">
              <div class="box filled" style="flex:1; padding:8px 10px;">
                <div class="box-label">jobs running</div>
                <div class="box-stat">3</div>
                <div class="small mute-txt">5 queued · 7 failed/timed_out today</div>
              </div>
              <div class="box filled" style="flex:1; padding:8px 10px;">
                <div class="box-label">workers online</div>
                <div class="box-stat">6 <span style="font-size:14px; color:var(--mute)">/ 7</span></div>
                <div class="small mute-txt">4 busy · 2 idle · 1 draining</div>
              </div>
              <div class="box filled" style="flex:1; padding:8px 10px;">
                <div class="box-label">throughput · tasks/min</div>
                <div class="box-stat">42.8</div>
                <div style="margin-top:2px">${sparkline()}</div>
              </div>
              <div class="box filled" style="flex:1; padding:8px 10px;">
                <div class="box-label">failure rate · 24h</div>
                <div class="box-stat" style="color:var(--accent)">2.4%</div>
                <div class="small mute-txt">↑ 0.6 vs yesterday</div>
              </div>
              <div class="box filled" style="flex:1; padding:8px 10px;">
                <div class="box-label">queue depth</div>
                <div class="box-stat">27</div>
                <div class="small mute-txt">avg wait 38s</div>
              </div>
            </div>

            <div class="row" style="gap:10px; flex:1; min-height:0; align-items:stretch;">
              <div class="col" style="flex:1.2; min-width:0;">
                <div class="box" style="padding:8px 10px; flex: 0 0 auto;">
                  <div class="row" style="justify-content:space-between;">
                    <div class="box-label">tasks completed · hourly</div>
                    <div class="small mute-txt">red = failures + timeouts</div>
                  </div>
                  ${bars()}
                </div>
                <div class="box" style="padding:6px 8px; flex:1; overflow:hidden;">
                  <div class="box-label" style="padding:2px 4px;">active jobs <span class="small mute-txt">· status ∈ {running, dispatched, queued}</span></div>
                  <table class="tbl adm-tbl">
                    <thead><tr><th>Job</th><th>Status</th><th>Progress</th><th></th><th>Owner email</th></tr></thead>
                    <tbody>
                      <tr><td class="job-name">film-x / shot-042</td><td><span class="status running"></span></td><td><div class="progress"><div class="fill" style="width:72%"></div></div></td><td class="mono small right">72%</td><td class="small">mira@studio.dev</td></tr>
                      <tr><td class="job-name">nightly etl <span class="chip mute" style="font-size:9px; padding:0 4px; transform:none">⟳ schedule</span></td><td><span class="status running"></span></td><td><div class="progress"><div class="fill" style="width:38%"></div></div></td><td class="mono small right">38%</td><td class="small">system</td></tr>
                      <tr><td class="job-name">blender-denoise</td><td><span class="status running"></span></td><td><div class="progress"><div class="fill" style="width:91%"></div></div></td><td class="mono small right">91%</td><td class="small">ada@studio.dev</td></tr>
                      <tr><td class="job-name">teaser frames</td><td><span class="status pending">queued</span></td><td><div class="progress"><div class="fill" style="width:0%"></div></div></td><td class="mono small right">0%</td><td class="small">ada@studio.dev</td></tr>
                    </tbody>
                  </table>
                </div>
              </div>

              <div class="col" style="flex:1; min-width:0;">
                <div class="box" style="padding:8px 10px; flex: 0 0 auto;">
                  <div class="box-label">worker load · used / max_slots</div>
                  <div class="col" style="gap:4px; margin-top:4px;">
                    ${[['render-rig-C',100,'8/8'],['render-rig-A',50,'2/4'],['render-rig-B',25,'1/4'],['ada-laptop',0,'0/1'],['ci-runner-1',0,'draining']].map(([n,p,t])=>`
                      <div class="row" style="gap:6px; align-items:center;">
                        <span class="mono small" style="width:90px;">${n}</span>
                        <div class="progress grow"><div class="fill" style="width:${p}%"></div></div>
                        <span class="mono small" style="width:54px; text-align:right;">${t}</span>
                      </div>`).join('')}
                  </div>
                </div>
                <div class="box" style="padding:8px 10px; flex:1;">
                  <div class="box-label">recent failures</div>
                  <div class="col" style="gap:5px; margin-top:4px;">
                    <div class="small">▪ <b>ci-agent #2340</b> · failed exit 1 · 2m ago</div>
                    <div class="small">▪ <b>oom-canary</b> · timed_out · 18m ago</div>
                    <div class="small">▪ <b>encode-4k-12</b> · failed (OOM) · 41m ago</div>
                    <div class="small">▪ <b>proxy s03e01</b> · cancelled by ada · 1h ago</div>
                  </div>
                  <div class="sep"></div>
                  <div class="box-label">alerts <span class="chip accent" style="font-size:9px; margin-left:4px;">2</span></div>
                  <div class="small" style="margin-top:4px; color:var(--accent);">▲ legacy-win-01 offline 2h — past worker_grace_window</div>
                  <div class="small" style="margin-top:2px; color:var(--accent);">▲ 1 enrollment token expires in &lt;1h (worker-08)</div>
                </div>
              </div>
            </div>
          </div>
        </div>
      </div>
    `;
  }

  // ────────────────────────── Invites ──────────────────────────
  function invites() {
    return `
      <div class="screen">
        ${C('relay.studio.dev/admin/invites')}
        <div class="screen-body">
          ${Shell.sidebar('invites')}
          ${Shell.topbar('invites')}
          <div class="main">
            <div class="row" style="justify-content:space-between;">
              <div class="col" style="gap:0;">
                <h1 class="page-title">User invites <span class="sub">admin only</span></h1>
                <div class="small mute-txt">one-time tokens redeemed at <span class="mono">/register</span> · default 72h · max 720h</div>
              </div>
              <button class="btn accent">+ create invite</button>
            </div>
            <div class="box" style="padding:4px 8px; flex:1; overflow:hidden;">
              <table class="tbl adm-tbl">
                <thead><tr><th>Email binding</th><th>Status</th><th>Created by</th><th>Created</th><th>Expires</th><th>Token</th><th></th></tr></thead>
                <tbody>
                  <tr>
                    <td><span class="chip dashed">any email</span></td>
                    <td><span class="status pending">unused</span></td>
                    <td class="small">mira@studio.dev</td>
                    <td class="small mute-txt">4h ago</td>
                    <td class="small">in 68h</td>
                    <td class="mono small">rl_invt_1a2b…•••</td>
                    <td class="right"><span class="chip">copy</span></td>
                  </tr>
                  <tr>
                    <td class="mono small">ada@studio.dev</td>
                    <td><span class="status done">redeemed</span></td>
                    <td class="small">mira@studio.dev</td>
                    <td class="small mute-txt">2d ago</td>
                    <td class="small mute-txt">—</td>
                    <td class="mono small mute-txt">used by ada · 2d ago</td>
                    <td></td>
                  </tr>
                  <tr>
                    <td class="mono small">jin@studio.dev</td>
                    <td><span class="status pending">unused</span></td>
                    <td class="small">ada@studio.dev</td>
                    <td class="small mute-txt">10h ago</td>
                    <td class="small">in 14h</td>
                    <td class="mono small">rl_invt_9f4e…•••</td>
                    <td class="right"><span class="chip">copy</span></td>
                  </tr>
                  <tr>
                    <td><span class="chip dashed">any email</span></td>
                    <td><span class="status cancelled">expired</span></td>
                    <td class="small">mira@studio.dev</td>
                    <td class="small mute-txt">5d ago</td>
                    <td class="small mute-txt">—</td>
                    <td class="mono small mute-txt">expired friday</td>
                    <td></td>
                  </tr>
                </tbody>
              </table>
            </div>
            <div class="row" style="justify-content:space-between; align-items:center; padding:4px 2px 0;">
              <span class="small mute-txt">showing <b>1–4</b> of <b>4</b> · <span class="mono">GET /v1/invites</span> · cursor-paginated</span>
              <div class="row" style="gap:4px;"><span class="chip mute">← prev</span><span class="chip mute">next →</span><span class="chip dashed">size: 50</span></div>
            </div>
            <div class="small mute-txt" style="margin-top:6px;">
              ⚠ Tokens shown only at create-time. Server stores SHA-256 hash; lost tokens must be re-issued. Revoke isn't a server endpoint in v1 — let it expire.
            </div>
          </div>
        </div>
      </div>
    `;
  }

  // Create-invite modal — UI matches POST /v1/invites body
  function createInviteModal() {
    return `
      <div class="screen">
        ${C('relay.studio.dev/admin/invites?new')}
        <div class="screen-body" style="position:relative;">
          ${Shell.sidebar('invites')}
          ${Shell.topbar('invites')}
          <div class="main" style="filter:blur(0.5px); opacity:0.55;">
            <h1 class="page-title">User invites</h1>
            <div class="box" style="flex:1; padding:10px;"><div class="ph block"></div></div>
          </div>
          <div style="position:absolute; inset:0; background:rgba(26,26,26,0.2); display:flex; align-items:center; justify-content:center;">
            <div style="width:340px; background:var(--paper); border:2px solid var(--ink); border-radius:10px; padding:16px 18px; box-shadow:4px 4px 0 var(--ink);">
              <div class="page-title" style="font-size:24px;">New invite</div>
              <div class="small mute-txt" style="margin-bottom:10px;">Token shown <b>once</b> — copy immediately.</div>
              <label class="box-label">Bind to email <span class="small">(optional · case-insensitive match at register)</span></label>
              <div class="box dashed" style="padding:4px 8px; margin:3px 0 10px; font-size:12px; color:var(--mute)">jin@studio.dev</div>
              <label class="box-label">expires_in</label>
              <div class="row" style="gap:4px; margin:3px 0 14px;">
                <span class="chip">24h</span>
                <span class="chip active">72h</span>
                <span class="chip">7d</span>
                <span class="chip dashed">custom · max 720h</span>
              </div>
              <button class="btn primary" style="width:100%;">generate token →</button>
            </div>
          </div>
        </div>
      </div>
    `;
  }

  // ────────────────────────── Agent Enrollments (NEW) ──────────────────────────
  function enrollments() {
    return `
      <div class="screen">
        ${C('relay.studio.dev/admin/enrollments')}
        <div class="screen-body">
          ${Shell.sidebar('settings')}
          ${Shell.topbar('settings')}
          <div class="main">
            <div class="row" style="justify-content:space-between;">
              <div class="col" style="gap:0;">
                <h1 class="page-title">Agent enrollments <span class="sub">admin only</span></h1>
                <div class="small mute-txt">one-time tokens for new <span class="mono">relay-agent</span> processes · default 24h · max 7d</div>
              </div>
              <button class="btn accent">+ enroll agent</button>
            </div>
            <div class="box" style="padding:4px 8px; flex:1; overflow:hidden;">
              <table class="tbl adm-tbl">
                <thead><tr><th>Hostname hint</th><th>Status</th><th>Created by</th><th>Created</th><th>Expires</th><th>Token</th></tr></thead>
                <tbody>
                  <tr>
                    <td class="mono small">worker-08</td>
                    <td><span class="status pending">pending</span></td>
                    <td class="small">mira@studio.dev</td>
                    <td class="small mute-txt">12m ago</td>
                    <td class="small" style="color:var(--accent)">in 47m</td>
                    <td class="mono small">rl_enr_b3c0…•••</td>
                  </tr>
                  <tr>
                    <td class="mono small">render-rig-D</td>
                    <td><span class="status pending">pending</span></td>
                    <td class="small">mira@studio.dev</td>
                    <td class="small mute-txt">3h ago</td>
                    <td class="small">in 21h</td>
                    <td class="mono small">rl_enr_8a44…•••</td>
                  </tr>
                  <tr>
                    <td><span class="chip dashed">any host</span></td>
                    <td><span class="status pending">pending</span></td>
                    <td class="small">ada@studio.dev</td>
                    <td class="small mute-txt">1d ago</td>
                    <td class="small">in 6d</td>
                    <td class="mono small">rl_enr_f902…•••</td>
                  </tr>
                </tbody>
              </table>
            </div>
            <div class="row" style="justify-content:space-between; align-items:center; padding:4px 2px 0;">
              <span class="small mute-txt">showing <b>1–3</b> of <b>3</b> · <span class="mono">GET /v1/agent-enrollments</span> (active only) · cursor-paginated</span>
              <div class="row" style="gap:4px;"><span class="chip mute">← prev</span><span class="chip mute">next →</span><span class="chip dashed">size: 50</span></div>
            </div>
            <div class="small mute-txt" style="margin-top:6px;">
              ▸ Operator copies token, sets <span class="mono">RELAY_AGENT_ENROLLMENT_TOKEN</span> on the new machine, runs <span class="mono">relay-agent</span> once. Token consumed atomically on first connect.
            </div>
          </div>
        </div>
      </div>
    `;
  }

  // ────────────────────────── Scheduled Jobs (NEW) ──────────────────────────
  function schedules() {
    return `
      <div class="screen wide">
        ${C('relay.studio.dev/schedules')}
        <div class="screen-body">
          ${Shell.sidebar('overview')}
          ${Shell.topbar('overview')}
          <div class="main">
            <div class="row" style="justify-content:space-between; align-items:baseline;">
              <div class="col" style="gap:0;">
                <h1 class="page-title">Scheduled jobs <span class="sub">your schedules · admins see all</span></h1>
                <div class="small mute-txt">cron-triggered job templates · min interval 30s · no catch-up after downtime</div>
              </div>
              <button class="btn accent">+ create schedule</button>
            </div>
            <div class="toolbar">
              <span class="chip active">all</span>
              <span class="chip">enabled</span>
              <span class="chip">disabled</span>
            </div>
            <div class="box" style="padding:4px 8px; flex:1; overflow:hidden;">
              <table class="tbl adm-tbl">
                <thead><tr><th>Name</th><th>Cron</th><th>Tz</th><th>Overlap</th><th>Enabled</th><th>Next run</th><th>Last run</th><th>Last job</th><th></th></tr></thead>
                <tbody>
                  <tr>
                    <td class="job-name">nightly-render</td>
                    <td class="mono small">0 2 * * *</td>
                    <td class="small">America/Los_Angeles</td>
                    <td><span class="chip mute" style="font-size:10px; padding:0 5px; transform:none">skip</span></td>
                    <td><span class="status done">on</span></td>
                    <td class="small">tonight 02:00 PT</td>
                    <td class="small mute-txt">yesterday 02:00 PT</td>
                    <td class="mono small"><span class="status done"></span> 9f4e1c</td>
                    <td class="right actions"><span class="chip">run now</span> <span class="chip">edit</span></td>
                  </tr>
                  <tr>
                    <td class="job-name">weekly-eval</td>
                    <td class="mono small">@every 1h</td>
                    <td class="small">UTC</td>
                    <td><span class="chip mute" style="font-size:10px; padding:0 5px; transform:none">allow</span></td>
                    <td><span class="status done">on</span></td>
                    <td class="small">in 24m</td>
                    <td class="small mute-txt">36m ago</td>
                    <td class="mono small"><span class="status done"></span> a221fe</td>
                    <td class="right actions"><span class="chip">run now</span> <span class="chip">edit</span></td>
                  </tr>
                  <tr>
                    <td class="job-name">smoke-tests</td>
                    <td class="mono small">@hourly</td>
                    <td class="small">UTC</td>
                    <td><span class="chip mute" style="font-size:10px; padding:0 5px; transform:none">skip</span></td>
                    <td><span class="status cancelled">paused</span></td>
                    <td class="small mute-txt">— (disabled)</td>
                    <td class="small mute-txt">3d ago</td>
                    <td class="mono small"><span class="status failed"></span> 7c01b2</td>
                    <td class="right actions"><span class="chip">enable</span> <span class="chip">edit</span></td>
                  </tr>
                  <tr>
                    <td class="job-name">db-backup</td>
                    <td class="mono small">15 4 * * 1-5</td>
                    <td class="small">Europe/London</td>
                    <td><span class="chip mute" style="font-size:10px; padding:0 5px; transform:none">skip</span></td>
                    <td><span class="status done">on</span></td>
                    <td class="small">tomorrow 04:15 BST</td>
                    <td class="small mute-txt">today 04:15 BST</td>
                    <td class="mono small"><span class="status done"></span> 3e08ca</td>
                    <td class="right actions"><span class="chip">run now</span> <span class="chip">edit</span></td>
                  </tr>
                </tbody>
              </table>
            </div>
            <div class="row" style="justify-content:space-between; align-items:center; padding:4px 2px 0;">
              <span class="small mute-txt">showing <b>1–4</b> of <b>4</b> · admins see all owners; non-admins scoped to <span class="mono">owner_id = me</span> · cursor-paginated</span>
              <div class="row" style="gap:4px;"><span class="chip mute">← prev</span><span class="chip mute">next →</span><span class="chip dashed">size: 50</span></div>
            </div>
            <div class="small mute-txt" style="margin-top:6px;">
              ▸ <b>run now</b> is admin-only — submits a fresh job using the schedule's stored <span class="mono">job_spec</span>, attributed to the schedule's owner.
            </div>
          </div>
        </div>
      </div>
    `;
  }

  // ────────────────────────── Users management (NEW surface in UI) ──────────────────────────
  function users() {
    return `
      <div class="screen">
        ${C('relay.studio.dev/admin/users')}
        <div class="screen-body">
          ${Shell.sidebar('settings')}
          ${Shell.topbar('settings')}
          <div class="main">
            <div class="row" style="justify-content:space-between;">
              <div class="col" style="gap:0;">
                <h1 class="page-title">Users <span class="sub">admin only</span></h1>
                <div class="small mute-txt">create directly, archive (soft-delete), force password reset</div>
              </div>
              <div class="row">
                <span class="chip">show archived</span>
                <button class="btn accent">+ create user</button>
              </div>
            </div>
            <div class="box" style="padding:4px 8px; flex:1; overflow:hidden;">
              <table class="tbl adm-tbl">
                <thead><tr><th>Email</th><th>Name</th><th>Admin</th><th>Created</th><th>Status</th><th></th></tr></thead>
                <tbody>
                  <tr>
                    <td class="mono small">mira@studio.dev</td>
                    <td class="small">Mira K.</td>
                    <td><span class="chip" style="font-size:10px; padding:0 5px; transform:none; color:var(--accent); border-color:var(--accent)">admin</span></td>
                    <td class="small mute-txt">2024-09-12</td>
                    <td><span class="status done">active</span></td>
                    <td class="right"><span class="chip">reset pw</span> <span class="chip">rename</span></td>
                  </tr>
                  <tr>
                    <td class="mono small">ada@studio.dev</td>
                    <td class="small">Ada Lovelace</td>
                    <td><span class="small mute-txt">—</span></td>
                    <td class="small mute-txt">2026-04-15</td>
                    <td><span class="status done">active</span></td>
                    <td class="right"><span class="chip">reset pw</span> <span class="chip">rename</span> <span class="chip" style="color:var(--accent); border-color:var(--accent)">archive</span></td>
                  </tr>
                  <tr>
                    <td class="mono small">jin@studio.dev</td>
                    <td class="small">Jin O.</td>
                    <td><span class="small mute-txt">—</span></td>
                    <td class="small mute-txt">2026-02-04</td>
                    <td><span class="status done">active</span></td>
                    <td class="right"><span class="chip">reset pw</span> <span class="chip">rename</span> <span class="chip" style="color:var(--accent); border-color:var(--accent)">archive</span></td>
                  </tr>
                  <tr>
                    <td class="mono small mute-txt">old.intern@studio.dev</td>
                    <td class="small mute-txt">Sam (intern)</td>
                    <td><span class="small mute-txt">—</span></td>
                    <td class="small mute-txt">2025-06-01</td>
                    <td><span class="status cancelled">archived</span></td>
                    <td class="right"><span class="chip">unarchive</span></td>
                  </tr>
                </tbody>
              </table>
            </div>
            <div class="row" style="justify-content:space-between; align-items:center; padding:4px 2px 0;">
              <span class="small mute-txt">showing <b>1–4</b> of <b>4</b> · <span class="mono">?include_archived=true</span> active · cursor-paginated</span>
              <div class="row" style="gap:4px;"><span class="chip mute">← prev</span><span class="chip mute">next →</span><span class="chip dashed">size: 50</span></div>
            </div>
            <div class="small mute-txt" style="margin-top:6px;">
              ▸ Archive revokes all of the target's tokens. Reset password also revokes their tokens — they're forced back through <span class="mono">/login</span>. Cannot archive yourself or the last active admin (server-enforced).
            </div>
          </div>
        </div>
      </div>
    `;
  }

  function render(host){
    host.innerHTML = `
      <div class="section-intro">
        <h2>Admin surfaces</h2>
        <p>Three admin tables grew or appeared in the codebase: <b>user invites</b>, <b>agent enrollments</b> (new — separate token flow for <span class="mono">relay-agent</span> bootstrap), and <b>users</b> (archive/unarchive + admin password reset). Plus <b>scheduled jobs</b>, a per-user surface that admins can see across owners. <span class="scribble">Sidebar gating: admin-only items hide for non-admins.</span></p>
      </div>

      <div class="variations stack">
        <div class="variant">
          <div class="variant-label"><span class="num">1</span>Admin overview dashboard</div>
          <div class="variant-note">KPI tiles + throughput + worker_load + alerts. New alert: enrollment tokens about to expire.</div>
          ${dashboard()}
        </div>
        <div class="variant">
          <div class="variant-label"><span class="num">2</span>Scheduled jobs <span class="tag">new surface</span></div>
          <div class="variant-note">Cron + timezone + overlap policy + enabled toggle + run-now (admin). Last-run links to the job that came out.</div>
          ${schedules()}
        </div>
      </div>

      <div class="variations cols-2">
        <div class="variant">
          <div class="variant-label"><span class="num">3</span>User invites</div>
          <div class="variant-note">No revoke endpoint in v1; expired/redeemed are the only terminal states. Email binding is optional.</div>
          ${invites()}
        </div>
        <div class="variant">
          <div class="variant-label"><span class="num">4</span>Create invite (modal)</div>
          <div class="variant-note">Body matches <span class="mono">POST /v1/invites</span>. Token rendered once, copy-only — never retrievable.</div>
          ${createInviteModal()}
        </div>
      </div>

      <div class="variations cols-2">
        <div class="variant">
          <div class="variant-label"><span class="num">5</span>Agent enrollments <span class="tag">new surface</span></div>
          <div class="variant-note">Distinct from user invites. Used to bootstrap a new <span class="mono">relay-agent</span>; token consumed on first agent connect.</div>
          ${enrollments()}
        </div>
        <div class="variant">
          <div class="variant-label"><span class="num">6</span>Users management <span class="tag">new surface</span></div>
          <div class="variant-note">Create / archive / unarchive / admin-reset password. Last-active-admin and self-archive guarded server-side.</div>
          ${users()}
        </div>
      </div>
    `;
  }

  return { render };
})();
