// Workers list + detail wireframes — updated to chadmv/relay@master
//   - response shape: id, name, hostname, cpu_cores, ram_gb, gpu_count, gpu_model, os,
//                     max_slots, labels, status, last_seen_at
//   - admin actions: PATCH name/labels/max_slots · DELETE token (revoke)
//   - workspaces (per-worker): GET /v1/workers/{id}/workspaces · POST .../{short}/evict
//   - 'draining' is NOT a server status; we represent it as a label/UI state for the admin
//   - PAGINATION (SHIPPED): GET /v1/workers?limit=<1..200>&cursor=<opaque>
//       same envelope as /v1/jobs: { items, next_cursor, total }
//       cursor key = (created_at, id); table footer mirrors the jobs list
window.Workers = (function(){
  const C = Shell.chrome;

  // [name, status, hostname, cpu_cores, ram_gb, gpu, max_slots, used_slots, labels, os, last_seen_relative]
  const WORKERS = [
    ['render-rig-A',  'online',  'render-a.studio.dev',  32, 128, '2× RTX4090', 4, 2, ['linux','gpu','rack=A'],   'linux',   '0.3s'],
    ['render-rig-B',  'online',  'render-b.studio.dev',  32, 128, '1× RTX4090', 4, 1, ['linux','gpu','rack=A'],   'linux',   '0.4s'],
    ['render-rig-C',  'online',  'render-c.studio.dev',  64, 256, '4× A100',    8, 8, ['linux','gpu','hpc'],      'linux',   '0.2s'],
    ['render-rig-D',  'online',  'render-d.studio.dev',  64, 256, '4× A100',    8, 6, ['linux','gpu','hpc'],      'linux',   '0.3s'],
    ['render-rig-E',  'online',  'render-e.studio.dev',  32, 128, '2× RTX4090', 4, 4, ['linux','gpu','rack=B'],   'linux',   '0.5s'],
    ['render-rig-F',  'online',  'render-f.studio.dev',  32, 128, '2× RTX4090', 4, 0, ['linux','gpu','rack=B'],   'linux',   '0.4s'],
    ['render-rig-G',  'online',  'render-g.studio.dev',  32, 128, '1× RTX4090', 4, 3, ['linux','gpu','rack=B'],   'linux',   '0.7s'],
    ['gpu-box-01',    'online',  'gpu01.studio.dev',     16,  64, '1× RTX3090', 2, 1, ['linux','gpu'],            'linux',   '0.6s'],
    ['gpu-box-02',    'online',  'gpu02.studio.dev',     16,  64, '1× RTX3090', 2, 2, ['linux','gpu'],            'linux',   '0.5s'],
    ['gpu-box-03',    'online',  'gpu03.studio.dev',     16,  64, '1× RTX3090', 2, 0, ['linux','gpu'],            'linux',   '0.6s'],
    ['cpu-pool-01',   'online',  'cpu01.studio.dev',     48, 192, '—',          12, 9, ['linux','cpu'],            'linux',   '0.4s'],
    ['cpu-pool-02',   'online',  'cpu02.studio.dev',     48, 192, '—',          12,12, ['linux','cpu'],            'linux',   '0.4s'],
    ['cpu-pool-03',   'online',  'cpu03.studio.dev',     48, 192, '—',          12, 5, ['linux','cpu'],            'linux',   '0.5s'],
    ['studio-mac-01', 'online',  'studio-mac-01.local',  10,  32, '—',           2, 0, ['macos'],                  'darwin',  '1.1s'],
    ['studio-mac-02', 'online',  'studio-mac-02.local',  10,  32, '—',           2, 1, ['macos'],                  'darwin',  '1.4s'],
    ['ada-laptop',    'online',  'ada-mbp.local',        10,  32, '—',           1, 0, ['macos'],                  'darwin',  '0.9s'],
    ['mira-laptop',   'online',  'mira-mbp.local',       12,  32, '—',           1, 1, ['macos'],                  'darwin',  '0.7s'],
    ['ci-runner-1',   'online',  'ci-1.studio.dev',      16,  64, '—',           2, 0, ['linux','ci','draining'],  'linux',   '2.1s'],
    ['ci-runner-2',   'online',  'ci-2.studio.dev',      16,  64, '—',           2, 1, ['linux','ci'],             'linux',   '1.8s'],
    ['legacy-win-01', 'offline', 'legacy01.studio.dev',   8,  16, '—',           2, 0, ['windows'],                'windows', '2h ago'],
  ];

  // visual state from server status + labels
  function workerStatusBucket(status, labels) {
    if (status === 'offline') return 'offline';
    if (labels.includes('draining')) return 'draining';
    return 'online';
  }

  function row(w) {
    const [name, status, host, cores, ram, gpu, max, used, labels, os, last] = w;
    const bucket = workerStatusBucket(status, labels);
    const pillCls = bucket==='online' ? 'done' : bucket==='offline' ? 'failed' : 'running';
    const loadStr = bucket==='offline' ? '—' : (used===0 ? 'idle' : `${used}/${max} slots`);
    return `
      <tr>
        <td class="mono job-name">${name}</td>
        <td><span class="status ${pillCls}">${bucket}</span></td>
        <td class="small">${host}</td>
        <td class="mono small">${cores}c / ${ram}G</td>
        <td class="mono small">${gpu}</td>
        <td class="small">${loadStr}</td>
        <td class="mono small mute-txt" style="width:60px;">${last}</td>
        <td class="small">${labels.map(l=>{
          const cls = l==='draining' ? `chip` : `chip mute`;
          const style = l==='draining'
            ? `font-size:9.5px; padding:0 6px; transform:none; color:var(--warn); border-color:var(--warn)`
            : `font-size:9.5px; padding:0 6px; transform:none`;
          return `<span class="${cls}" style="${style}">${l}</span>`;
        }).join(' ')}</td>
      </tr>
    `;
  }

  // Variation 1: Table
  function v1Table() {
    return `
      <div class="screen wide">
        ${C('relay.studio.dev/workers')}
        <div class="screen-body">
          ${Shell.sidebar('workers')}
          ${Shell.topbar('workers')}
          <div class="main">
            <div class="row" style="justify-content:space-between; align-items:baseline;">
              <h1 class="page-title">Workers <span class="sub">20 total · 19 online · 11 busy · 1 draining</span></h1>
              <div class="row">
                <span class="chip dashed">☰ table</span>
                <span class="chip mute">⊞ grid</span>
                <button class="btn accent" title="POST /v1/agent-enrollments">+ enroll new agent</button>
              </div>
            </div>
            <div class="toolbar">
              <input class="search" value="🔍  filter by label, hostname, capability…" readonly />
              <span class="chip active">all</span>
              <span class="chip">online</span>
              <span class="chip">busy</span>
              <span class="chip">idle</span>
              <span class="chip">offline</span>
              <span class="chip dashed">+ has:gpu</span>
              <span class="chip dashed">+ rack=A</span>
            </div>
            <div class="box" style="padding:4px 8px; flex:1; overflow:hidden; display:flex; flex-direction:column;">
              <table class="tbl" style="flex:1;">
                <thead><tr><th>Name</th><th>Status</th><th>Hostname</th><th>CPU / RAM</th><th>GPU</th><th>Load</th><th>Last seen</th><th>Labels</th></tr></thead>
                <tbody>${WORKERS.map(row).join('')}</tbody>
              </table>
              <div class="row" style="justify-content:space-between; align-items:center; padding:6px 4px 2px; border-top:1.5px dashed var(--mute-2); margin-top:4px;">
                <span class="small mute-txt">showing <b>1–20</b> of <b>20</b> · cursor-paginated · <span class="mono">GET /v1/workers</span></span>
                <div class="row" style="gap:4px;">
                  <span class="chip mute">← prev</span>
                  <span class="chip mute" title="next_cursor empty — last page">next →</span>
                  <span class="chip dashed">page size: 50</span>
                </div>
              </div>
            </div>
          </div>
        </div>
        <div class="annot" style="top:18%; right:3%; font-size:12px;">'draining' is a<br/>label, not a<br/>server status</div>
      </div>
    `;
  }

  // Variation 2: Grid cards
  function card(w) {
    const [name, status, host, cores, ram, gpu, max, used, labels] = w;
    const bucket = workerStatusBucket(status, labels);
    const bg  = bucket==='online' && used===0 ? 'var(--ok-soft)'
              : bucket==='online'              ? 'var(--warn-soft)'
              : bucket==='draining'            ? 'var(--warn-soft)'
              : 'var(--paper-2)';
    const bd  = bucket==='online' && used===0 ? 'var(--ok)'
              : bucket==='online'              ? 'var(--warn)'
              : bucket==='draining'            ? 'var(--warn)'
              : 'var(--mute)';
    const pct = bucket==='offline' ? 0 : Math.round((used/Math.max(1,max))*100);
    return `
      <div class="box" style="padding:8px 10px; background:${bg}; border-color:${bd};">
        <div class="row" style="justify-content:space-between; align-items:baseline;">
          <span class="mono" style="font-weight:700;">${name}</span>
          <span class="status ${bucket==='online'?'done':bucket==='offline'?'failed':'running'}" style="font-size:10px">${bucket}</span>
        </div>
        <div class="small mute-txt" style="margin-bottom:4px;">${host}</div>
        <div class="mono" style="font-size:11px;">${cores}c / ${ram}G · ${gpu}</div>
        <div class="progress" style="margin:6px 0 4px;"><div class="fill" style="width:${pct}%"></div></div>
        <div class="row small" style="justify-content:space-between;">
          <span>${bucket==='offline'?'—':`${used}/${max} slots`}</span>
          <span class="mute-txt">${labels.filter(l=>l!=='draining').join(' · ')}</span>
        </div>
      </div>
    `;
  }

  function v2Grid() {
    return `
      <div class="screen wide">
        ${C('relay.studio.dev/workers?view=grid')}
        <div class="screen-body">
          ${Shell.sidebar('workers')}
          ${Shell.topbar('workers')}
          <div class="main">
            <div class="row" style="justify-content:space-between;">
              <h1 class="page-title">Workers <span class="sub">fleet at a glance</span></h1>
              <span class="chip">⊞ grid</span>
            </div>
            <div class="toolbar">
              <span class="chip active">all</span>
              <span class="chip dashed">+ gpu</span>
              <span class="chip dashed">+ linux</span>
              <span class="chip dashed">+ rack=A</span>
            </div>
            <div style="display:grid; grid-template-columns:repeat(4, 1fr); gap:8px; flex:1; overflow:hidden; align-content:start;">
              ${WORKERS.map(card).join('')}
            </div>
          </div>
        </div>
      </div>
    `;
  }

  // Variation 3: Worker detail — adds workspaces panel + admin actions
  function v3Detail() {
    return `
      <div class="screen wide">
        ${C('relay.studio.dev/workers/render-rig-A')}
        <div class="screen-body">
          ${Shell.sidebar('workers')}
          ${Shell.topbar('workers')}
          <div class="main">
            <div class="small mute-txt">← Workers /</div>
            <div class="row" style="justify-content:space-between; align-items:flex-start;">
              <div class="col" style="gap:2px;">
                <h1 class="page-title">render-rig-A <span class="status done" style="font-size:13px;">online</span></h1>
                <div class="small mute-txt">id <span class="mono">2bce…</span> · hostname <span class="mono">render-a.studio.dev</span> · linux · last_seen 0.3s ago</div>
              </div>
              <div class="row">
                <button class="btn ghost" title="add 'draining' label">drain</button>
                <button class="btn">edit labels</button>
                <button class="btn">rename</button>
                <button class="btn" style="color:var(--accent); border-color:var(--accent)" title="DELETE /v1/workers/{id}/token">revoke token</button>
              </div>
            </div>

            <div class="row" style="gap:8px; flex: 0 0 auto;">
              <div class="box filled" style="flex:1; padding:8px 10px;">
                <div class="box-label">cpu_cores · ram_gb</div>
                <div class="box-stat" style="font-size:22px;">32c · 128G</div>
                <div class="small mute-txt">os: linux</div>
              </div>
              <div class="box filled" style="flex:1; padding:8px 10px;">
                <div class="box-label">gpu_count · gpu_model</div>
                <div class="box-stat" style="font-size:22px;">2 × RTX4090</div>
                <div class="small mute-txt">nvidia-smi · cuda 12.3</div>
              </div>
              <div class="box filled" style="flex:1; padding:8px 10px;">
                <div class="box-label">slots · running / max</div>
                <div class="box-stat" style="font-size:22px;">2 / 4</div>
                <div class="progress" style="margin-top:4px;"><div class="fill" style="width:50%"></div></div>
                <div class="small mute-txt">PATCH max_slots</div>
              </div>
              <div class="box filled" style="flex:1; padding:8px 10px;">
                <div class="box-label">jobs today</div>
                <div class="box-stat" style="font-size:22px;">47</div>
                <div class="small mute-txt">3 failed · avg 4m 12s</div>
              </div>
            </div>

            <div class="row" style="gap:10px; flex:1; min-height:0; align-items:stretch;">
              <div class="col" style="flex:1; min-width:0;">
                <div class="box-label">current tasks</div>
                <div class="box" style="padding:4px 6px; flex: 0 0 auto;">
                  <table class="tbl">
                    <tbody>
                      <tr><td class="mono">frame-004</td><td class="small">shot-042</td><td><div class="progress"><div class="fill" style="width:68%"></div></div></td><td class="mono small right">68%</td></tr>
                      <tr><td class="mono">frame-012</td><td class="small">shot-042</td><td><div class="progress"><div class="fill" style="width:22%"></div></div></td><td class="mono small right">22%</td></tr>
                    </tbody>
                  </table>
                </div>

                <div class="box-label" style="margin-top:6px;">source workspaces <span class="small mute-txt">/v1/workers/.../workspaces</span></div>
                <div class="box" style="padding:4px 6px; flex:1; overflow:hidden;">
                  <table class="tbl">
                    <thead><tr><th>short_id</th><th>type</th><th>source_key</th><th>baseline</th><th>last_used</th><th></th></tr></thead>
                    <tbody>
                      <tr><td class="mono small">ws-a4f2</td><td class="small">perforce</td><td class="mono small">//depot/film-x/main</td><td class="mono small">@CL 81234</td><td class="small">2m ago</td><td class="right"><span class="chip mute" title="held by frame-004 — can't evict">held</span></td></tr>
                      <tr><td class="mono small">ws-7c91</td><td class="small">perforce</td><td class="mono small">//depot/film-x/teaser</td><td class="mono small">@CL 80991</td><td class="small">3h ago</td><td class="right"><span class="chip" style="color:var(--accent); border-color:var(--accent)">evict</span></td></tr>
                      <tr><td class="mono small">ws-1e0d</td><td class="small">perforce</td><td class="mono small">//depot/tools/main</td><td class="mono small">@CL 80012</td><td class="small">2d ago</td><td class="right"><span class="chip" style="color:var(--accent); border-color:var(--accent)">evict</span></td></tr>
                    </tbody>
                  </table>
                </div>
              </div>
              <div class="col" style="flex:1; min-width:0;">
                <div class="box-label">labels</div>
                <div class="box" style="padding:8px 10px;">
                  <div class="row wrap" style="gap:6px;">
                    <span class="chip ok">linux</span>
                    <span class="chip ok">gpu</span>
                    <span class="chip ok">cuda:12.3</span>
                    <span class="chip ok">rack=A</span>
                    <span class="chip dashed">+ add label</span>
                  </div>
                  <div class="small mute-txt" style="margin-top:6px;">PATCH /v1/workers/{id} · {"labels":{...}}</div>
                </div>

                <div class="box-label" style="margin-top:6px;">reservations</div>
                <div class="box" style="padding:8px 10px;">
                  <div class="small">▪ <b>vfx-sprint</b> (project=film-x) — explicit worker_ids · until May 07 18:00</div>
                  <div class="small mute-txt" style="margin-top:6px;">selectors are informational in v1; only worker_ids are enforced.</div>
                </div>

                <div class="box-label" style="margin-top:6px;">agent token</div>
                <div class="box" style="padding:8px 10px; border-color:var(--accent);">
                  <div class="small">long-lived agent token · last rotated 4d ago</div>
                  <div class="small mute-txt" style="margin-top:4px;">Revoking forces the agent to exit and re-enroll with a fresh token.</div>
                </div>
              </div>
            </div>
          </div>
        </div>
      </div>
    `;
  }

  function render(host){
    host.innerHTML = `
      <div class="section-intro">
        <h2>Workers — fleet view & detail</h2>
        <p>Server fields are now <span class="mono">cpu_cores · ram_gb · gpu_count · gpu_model · os · max_slots · labels · status · last_seen_at</span>. Status is just <b>online / offline</b>; <span class="mono">draining</span> is a UI-conferred label. Admin-only actions: rename, edit labels, set max_slots, revoke agent token, evict workspaces. <span class="scribble">"+ enroll new agent" hits POST /v1/agent-enrollments and shows the one-time token.</span></p>
      </div>
      <div class="variations stack">
        <div class="variant">
          <div class="variant-label"><span class="num">1</span>Dense table <span class="tag">default</span></div>
          <div class="variant-note">Now shows <em>last_seen</em>, decoupled status vs draining-label, and an "enroll agent" CTA.</div>
          ${v1Table()}
        </div>
        <div class="variant">
          <div class="variant-label"><span class="num">2</span>Card grid · slot-load colored</div>
          <div class="variant-note">Green=online-idle, amber=busy or draining, grey=offline. Bar = used/max slots.</div>
          ${v2Grid()}
        </div>
        <div class="variant">
          <div class="variant-label"><span class="num">3</span>Worker detail · with workspaces</div>
          <div class="variant-note">New <b>source workspaces</b> panel — per-row Evict (held workspaces refuse). Token revoke and PATCH actions surfaced.</div>
          ${v3Detail()}
        </div>
      </div>
    `;
  }

  return { render };
})();
