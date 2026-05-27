// Job detail — updated to match chadmv/relay@master
//   - tasks now have `commands` (array of command arrays), retry_count/retries, worker_id, source spec
//   - statuses: pending | queued | dispatched | running | done | failed | timed_out | cancelled
//   - cancel: DELETE /v1/jobs/{id}?force=true (force skips pipe-drain + workspace cleanup)
//   - logs via SSE: GET /v1/events?job_id=...  / per-task GET /v1/tasks/{id}/logs
//   - source workspace info (perforce stream, sync, unshelves) shown in task focus view
window.JobDetail = (function(){
  const C = Shell.chrome;

  // Table density lives in styles.css (.screen .tbl) — applied globally.


  // Toggle handler for the live-log / spec tabs in v1Split.
  // Defined as a global because screens are rendered via innerHTML, which
  // skips inline <script> tags — so we can't bind in a string template.
  window.__jdTab = function(btn, tab) {
    const root = btn.closest('.jd-right');
    if (!root) return;
    root.dataset.tab = tab;
    root.querySelectorAll('.jd-tab').forEach(x => x.classList.toggle('active', x.dataset.tab === tab));
    root.querySelectorAll('.jd-when-log').forEach(x => x.style.display = tab === 'log' ? '' : 'none');
    root.querySelectorAll('.jd-when-spec').forEach(x => x.style.display = tab === 'spec' ? '' : 'none');
  };

  // [name, status, pct, dur, depends_on, retries(used/max), worker]
  const TASKS = [
    ['frame-001',  'done',       100, '2m 14s', [],                   '0/2', 'worker-03'],
    ['frame-002',  'done',       100, '2m 09s', [],                   '0/2', 'worker-04'],
    ['frame-003',  'done',       100, '2m 20s', [],                   '1/2', 'worker-03'],
    ['frame-004',  'running',     68, '1m 32s', [],                   '0/2', 'worker-03'],
    ['frame-005',  'running',     52, '58s',    [],                   '0/2', 'worker-04'],
    ['frame-006',  'dispatched',  10, '4s',     [],                   '0/2', 'worker-02'],
    ['denoise-all','pending',      0, '—',      ['frame-001..006'],   '0/0', '—'],
    ['encode-mp4', 'pending',      0, '—',      ['denoise-all'],      '0/0', '—'],
  ];

  function statusClass(s) {
    if (s==='running'||s==='dispatched') return 'running';
    if (s==='pending'||s==='queued')     return 'pending';
    if (s==='done')                      return 'done';
    if (s==='failed'||s==='timed_out')   return 'failed';
    if (s==='cancelled')                 return 'cancelled';
    return '';
  }

  function taskRow(t, compact=false) {
    const [name, status, pct, dur, deps, retries, worker] = t;
    const cls = statusClass(status);
    return `
      <tr>
        <td class="mono job-name">${name}</td>
        <td><span class="status ${cls}" title="${status}"></span></td>
        <td style="width:80px;"><div class="progress"><div class="fill ${cls==='done'?'ok':cls==='failed'?'fail':''}" style="width:${pct}%"></div></div></td>
        <td class="mono small" style="width:44px;">${pct}%</td>
        <td class="mono small" style="width:54px;">${dur}</td>
        ${compact?'':`
          <td class="mono small mute-txt" style="width:74px;">${worker==='—'?'—':worker}</td>
          <td class="mono small mute-txt" style="width:46px;" title="retry_count / retries">${retries}</td>
          <td class="small mute-txt">${deps.length?'← '+deps.join(', '):'—'}</td>
        `}
      </tr>
    `;
  }

  function dagSvg() {
    return `
      <svg viewBox="0 0 520 140" width="100%" height="100%" style="font-family:var(--hand); font-size:10px;">
        <defs>
          <marker id="ah" markerWidth="8" markerHeight="8" refX="7" refY="4" orient="auto">
            <path d="M0,0 L8,4 L0,8 z" fill="#1A1A1A"/>
          </marker>
        </defs>
        ${[0,1,2,3,4,5].map((i,idx)=>{
          const cy = 20 + (idx%3)*35;
          const cx = 50 + Math.floor(idx/3)*110;
          const col = idx<3?'#4A7C59':'#C78A1E';
          const bg  = idx<3?'#D6E5DB':'#F4E4C1';
          return `
            <g>
              <rect x="${cx-42}" y="${cy-12}" width="84" height="22" rx="4" fill="${bg}" stroke="${col}" stroke-width="1.5"/>
              <text x="${cx}" y="${cy+3}" text-anchor="middle" fill="#1A1A1A">frame-00${idx+1}</text>
            </g>
            <path d="M ${cx+42} ${cy} C ${cx+70} ${cy}, ${cx+90} 70, ${cx+170} 70" stroke="#1A1A1A" stroke-width="1.2" fill="none" marker-end="url(#ah)" stroke-dasharray="${idx<3?'0':'3 3'}"/>
          `;
        }).join('')}
        <rect x="278" y="58" width="94" height="24" rx="4" fill="#FAF8F3" stroke="#8A857A" stroke-width="1.5" stroke-dasharray="4 3"/>
        <text x="325" y="74" text-anchor="middle" fill="#1A1A1A">denoise-all</text>
        <path d="M 372 70 C 400 70, 420 70, 450 70" stroke="#1A1A1A" stroke-width="1.2" fill="none" marker-end="url(#ah)" stroke-dasharray="3 3"/>
        <rect x="450" y="58" width="70" height="24" rx="4" fill="#FAF8F3" stroke="#8A857A" stroke-width="1.5" stroke-dasharray="4 3"/>
        <text x="485" y="74" text-anchor="middle" fill="#1A1A1A">encode-mp4</text>
      </svg>
    `;
  }

  // Log lines reflect the v1 SSE format from `relay logs`:
  //   [task-name stdout|stderr] line
  function logLines() {
    return `
      <span class="ln"><span class="tag">[frame-004 stdout]</span> Blender 4.0, blender.org</span>
      <span class="ln"><span class="tag">[frame-004 stdout]</span> Read blend: scene.blend</span>
      <span class="ln"><span class="tag">[frame-004 stdout]</span> Fra:4 Mem:482M | Compositing | Denoise</span>
      <span class="ln"><span class="tag">[frame-005 stdout]</span> Fra:5 Mem:441M | Scene, ViewLayer | Rendering 34/256</span>
      <span class="ln mute">[frame-004 stderr] Warning: deprecated API — GL_POINTS</span>
      <span class="ln"><span class="tag">[frame-006 stdout]</span> Fra:6 Mem:398M | Rendering 18/256</span>
      <span class="ln ok">[frame-003 done] exit 0 · 2m 20s · worker-03 · retry 1/2</span>
      <span class="ln"><span class="tag">[frame-005 stdout]</span> Fra:5 Mem:450M | Rendering 48/256</span>
      <span class="ln"><span class="tag">[frame-004 stdout]</span> Fra:4 Mem:490M | Saved: /tmp/frame-004.exr</span>
    `;
  }

  // V1: Split pane — task list LEFT, log RIGHT
  function v1Split() {
    return `
      <div class="screen wide">
        ${C('relay.studio.dev/jobs/9f4e1c')}
        <div class="screen-body">
          ${Shell.sidebar('jobs')}
          ${Shell.topbar('jobs')}
          <div class="main">
            <div class="row" style="justify-content:space-between; align-items:flex-start;">
              <div class="col" style="gap:0;">
                <div class="small mute-txt">← Jobs / film-x /</div>
                <h1 class="page-title">shot-042 render
                  <span class="status running" style="font-size:13px; margin-left:6px;">running</span>
                </h1>
                <div class="small mute-txt">id <span class="mono">9f4e1c</span> · submitted by <b>mira@studio.dev</b> · priority high · labels <span class="chip mute" style="transform:none; font-size:10px;">project=film-x</span></div>
                <div class="row" style="gap:6px; margin-top:4px; align-items:center;">
                  <span class="chip mute" style="transform:none; font-size:10px;">source · perforce</span>
                  <span class="mono small">//depot/film-x/main</span>
                  <span class="small mute-txt">@CL</span><span class="mono small">81234</span>
                  <span class="small mute-txt">· workspace</span><span class="mono small">ws-a4f2</span>
                  <span class="chip dashed" style="transform:none; font-size:10px;" title="from job_spec.source — surfaces only when set">spec.source</span>
                </div>
              </div>
              <div class="row">
                <span class="chip active dag-only">graph</span>
                <span class="chip list-only" style="display:none">list</span>
                <button class="btn ghost">cancel</button>
                <button class="btn" style="color:var(--accent); border-color:var(--accent)" title="?force=true — skip pipe drain + workspace cleanup">force cancel</button>
              </div>
            </div>

            <div class="row" style="align-items:stretch; gap:10px; flex:1; min-height:0;">
              <div class="col" style="flex: 1 1 55%; min-width:0;">
                <div class="box" style="padding:6px 8px; flex-shrink:0;">
                  <div class="row" style="justify-content:space-between; align-items:center;">
                    <span class="box-label">overall · 5/8 tasks active · 3 done</span>
                    <span class="small mono">ETA 6m 12s</span>
                  </div>
                  <div class="progress" style="margin-top:4px;"><div class="fill" style="width:55%"></div></div>
                </div>
                <div class="box dag-only" style="padding:6px; flex: 0 0 auto; height: 140px; overflow:hidden;">
                  <div class="box-label" style="margin-bottom:2px;">task graph <span class="small" style="color:var(--mute); font-weight:400">· solid edges done · dashed waiting</span></div>
                  ${dagSvg()}
                </div>
                <div class="box" style="padding:4px 6px; flex:1; overflow:hidden; min-height:0;">
                  <div class="row" style="justify-content:space-between; padding:2px 4px;">
                    <span class="box-label">tasks · 8</span>
                    <span class="small mute-txt">▸ click to filter log</span>
                  </div>
                  <table class="tbl jd-tbl">
                    <thead><tr><th>name</th><th>•</th><th>progress</th><th></th><th>dur</th><th>worker</th><th>retry</th><th>deps</th></tr></thead>
                    <tbody>${TASKS.map(t=>taskRow(t,false)).join('')}</tbody>
                  </table>
                </div>
              </div>

              <div class="col jd-right" style="flex: 1 1 45%; min-width:0;" data-tab="log">
                <div class="row" style="gap:6px; align-items:center; flex-shrink:0;">
                  <span class="chip active jd-tab" data-tab="log" onclick="window.__jdTab(this,'log')" style="font-size:10px; cursor:pointer;">live log</span>
                  <span class="chip jd-tab" data-tab="spec" onclick="window.__jdTab(this,'spec')" style="font-size:10px; cursor:pointer;" title="commands array from job_spec.tasks[*].commands">spec</span>
                  <span class="grow"></span>
                  <span class="jd-when-log small mute-txt">/v1/events?job_id=…</span>
                  <span class="jd-when-log chip" style="font-size:10px;">stdout</span>
                  <span class="jd-when-log chip" style="font-size:10px;">stderr</span>
                  <span class="jd-when-log chip mute" style="font-size:10px;">↧ follow</span>
                  <span class="jd-when-spec small mute-txt" style="display:none">read-only · from job_spec</span>
                </div>
                <div class="jd-when-log logpane" style="flex:1; overflow:hidden;">${logLines()}</div>
                <div class="jd-when-spec col" style="display:none; flex:1; overflow:auto; gap:6px; min-height:0;">
                  <div class="box filled" style="padding:6px 10px;">
                    <div class="box-label">source · perforce</div>
                    <div class="mono small">//depot/film-x/main @CL 81234</div>
                    <div class="small mute-txt" style="margin-top:2px;">workspace ws-a4f2 · synced 2m ago</div>
                  </div>
                  <div class="box" style="padding:6px 10px;">
                    <div class="box-label">commands <span class="small mute-txt">(per-task array — runs in order, fail-fast)</span></div>
                    <div class="mono small" style="margin-top:4px; line-height:1.7;">
                      <div class="small mute-txt" style="margin-top:2px;">tasks[0] · frame-004</div>
                      <div>▸ <span style="color:var(--mute)">[0]</span> p4 sync //depot/film-x/main/...@81234</div>
                      <div>▸ <span style="color:var(--mute)">[1]</span> blender -b scene.blend -f 4 --engine CYCLES</div>
                      <div>▸ <span style="color:var(--mute)">[2]</span> oiiotool /tmp/frame-004.exr -o frame-004.png</div>
                      <div class="small mute-txt" style="margin-top:6px;">tasks[1] · frame-005</div>
                      <div>▸ <span style="color:var(--mute)">[0]</span> p4 sync //depot/film-x/main/...@81234</div>
                      <div>▸ <span style="color:var(--mute)">[1]</span> blender -b scene.blend -f 5 --engine CYCLES</div>
                      <div>▸ <span style="color:var(--mute)">[2]</span> oiiotool /tmp/frame-005.exr -o frame-005.png</div>
                      <div class="small mute-txt" style="margin-top:6px;">tasks[2..7] · frame-006…011 <span class="chip dashed" style="font-size:10px; transform:none">same shape</span></div>
                    </div>
                  </div>
                  <div class="box" style="padding:6px 10px;">
                    <div class="box-label">env <span class="small mute-txt">(merged: job_spec.env + task.env)</span></div>
                    <div class="mono small" style="margin-top:4px;">SCENE=scene.blend<br/>CYCLES_DEVICE=CUDA<br/>P4USER=relay-agent</div>
                  </div>
                </div>
                <!-- script removed; toggle now uses window.__jdTab installed by job-detail.js init below -->
              </div>
            </div>
          </div>
        </div>
        <div class="annot" style="top:14%; right:3%; font-size:12px;">force cancel = ?force=true<br/>skips drain &amp; workspace<br/>cleanup</div>
      </div>
    `;
  }

  // V2: Log-dominant
  function v2LogBottom() {
    return `
      <div class="screen wide">
        ${C('relay.studio.dev/jobs/9f4e1c?view=v2')}
        <div class="screen-body">
          ${Shell.sidebar('jobs')}
          ${Shell.topbar('jobs')}
          <div class="main">
            <div class="row" style="justify-content:space-between;">
              <div class="col" style="gap:0;">
                <h1 class="page-title">shot-042 render <span class="status running" style="font-size:13px;">running</span></h1>
                <div class="small mute-txt">source <span class="mono">//depot/film-x/main</span> @CL <span class="mono">81234</span> · ws <span class="mono">ws-a4f2</span></div>
              </div>
              <div class="row">
                <span class="chip mute">5/8 active · 3 done · ETA 6m</span>
                <button class="btn ghost">cancel</button>
                <button class="btn" style="color:var(--accent); border-color:var(--accent)">force</button>
              </div>
            </div>
            <div class="row" style="gap:10px; flex: 0 0 38%; align-items:stretch;">
              <div class="box" style="flex:1; padding:4px 6px; overflow:hidden;">
                <div class="box-label" style="padding:2px 4px;">tasks · 8</div>
                <table class="tbl jd-tbl"><tbody>${TASKS.map(t=>taskRow(t,true)).join('')}</tbody></table>
              </div>
              <div class="box dag-only" style="flex:1; padding:6px; overflow:hidden;">
                <div class="box-label">DAG</div>
                ${dagSvg()}
              </div>
            </div>
            <div class="logpane" style="flex:1; overflow:hidden;">
              <div style="display:flex; gap:8px; font-size:10px; opacity:0.7; border-bottom:1px solid #333; margin-bottom:4px; padding-bottom:3px;">
                <span>▶ live log — SSE · all 8 tasks</span>
                <span style="margin-left:auto;">stdout · stderr · ↧ follow</span>
              </div>
              ${logLines()}
            </div>
          </div>
        </div>
      </div>
    `;
  }

  // V3: Single-task drill-down — adds source/workspace + commands array
  function v3Focus() {
    return `
      <div class="screen wide">
        ${C('relay.studio.dev/jobs/9f4e1c/tasks/frame-004')}
        <div class="screen-body">
          ${Shell.sidebar('jobs')}
          ${Shell.topbar('jobs')}
          <div class="main">
            <div class="small mute-txt">← shot-042 render / tasks /</div>
            <h1 class="page-title">frame-004 <span class="status running" style="font-size:13px;">running 68%</span></h1>
            <div class="row" style="gap:8px; flex: 0 0 auto;">
              <div class="box filled" style="flex:1; padding:6px 10px;">
                <div class="box-label">worker</div>
                <div class="mono" style="font-size:12px;">worker-03 · 32c / 128G / 1× RTX4090</div>
              </div>
              <div class="box filled" style="flex:1; padding:6px 10px;">
                <div class="box-label">retries · timeout</div>
                <div class="mono" style="font-size:12px;">retry 0 / 2 · timeout 1h</div>
              </div>
              <div class="box filled" style="flex:1; padding:6px 10px;">
                <div class="box-label">requires</div>
                <div class="mono small">{"gpu":"true"}</div>
              </div>
              <div class="box filled" style="flex:1; padding:6px 10px;">
                <div class="box-label">source · perforce</div>
                <div class="mono small" style="white-space:nowrap; overflow:hidden; text-overflow:ellipsis;">//depot/film-x/main @CL 81234</div>
              </div>
            </div>

            <div class="row" style="gap:10px; flex: 0 0 auto; align-items:stretch;">
              <div class="box" style="flex:2; padding:6px 10px;">
                <div class="box-label">commands <span class="small mute-txt">(array — runs in order, fail-fast)</span></div>
                <div class="mono small" style="margin-top:4px; line-height:1.6;">
                  <div>▸ <span style="color:var(--mute)">[0]</span> p4 sync //depot/film-x/main/...@81234</div>
                  <div>▸ <span style="color:var(--mute)">[1]</span> blender -b scene.blend -f 4 --engine CYCLES</div>
                  <div>▸ <span style="color:var(--mute)">[2]</span> oiiotool /tmp/frame-004.exr -o frame-004.png</div>
                </div>
              </div>
              <div class="box" style="flex:1; padding:6px 10px;">
                <div class="box-label">env</div>
                <div class="mono small" style="margin-top:4px;">
                  SCENE=scene.blend<br/>
                  CYCLES_DEVICE=CUDA<br/>
                  P4USER=relay-agent
                </div>
              </div>
            </div>

            <div class="row" style="gap:10px; flex:1; min-height:0; align-items:stretch;">
              <div class="col" style="flex: 0 0 180px;">
                <div class="box-label">sibling tasks</div>
                <div class="col" style="gap:3px;">
                  ${TASKS.slice(0,8).map(t=>`
                    <div class="box ${t[0]==='frame-004'?'accent':'dashed'}" style="padding:3px 6px; font-size:11px; display:flex; justify-content:space-between; align-items:center;">
                      <span class="mono">${t[0]}</span>
                      <span class="status ${statusClass(t[1])}"></span>
                    </div>`).join('')}
                </div>
              </div>
              <div class="col grow" style="min-width:0;">
                <div class="row" style="gap:6px;">
                  <span class="chip active">stdout + stderr</span>
                  <span class="chip">stdout</span>
                  <span class="chip">stderr</span>
                  <span class="chip">env</span>
                  <span class="grow"></span>
                  <span class="chip mute">⬇ /v1/tasks/.../logs</span>
                </div>
                <div class="logpane" style="flex:1; overflow:hidden;">${logLines()}</div>
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
        <h2>Job detail — tasks, DAG, logs</h2>
        <p>Tasks now carry <span class="mono">commands</span> (array of command arrays — run sequentially), per-task <span class="mono">retry_count</span>, optional <span class="mono">source</span> workspace spec (Perforce v1), and the <span class="mono">worker_id</span> they ran on. <span class="scribble">Cancel has two flavors: graceful (drain logs &amp; cleanup workspace) vs <b>force</b> (?force=true).</span></p>
      </div>
      <div class="variations stack">
        <div class="variant">
          <div class="variant-label"><span class="num">1</span>Split pane · tasks ← → log <span class="tag" style="color:var(--ok); border-color:var(--ok)">♥ picked</span></div>
          <div class="variant-note">Adds worker + retry columns. DAG strip above task list. Cancel + force-cancel both surfaced. <b style="color:var(--ok)">Confirmed default.</b></div>
          ${v1Split()}
        </div>
        <div class="variant">
          <div class="variant-label"><span class="num">2</span>Log-dominant · log gets bottom half</div>
          <div class="variant-note">Best for tail-watching. Compact task table on top.</div>
          ${v2LogBottom()}
        </div>
        <div class="variant">
          <div class="variant-label"><span class="num">3</span>Single-task drill-down</div>
          <div class="variant-note">Renders the new shape: <span class="mono">commands[]</span> sequential block, <span class="mono">requires</span>, <span class="mono">source</span> (perforce stream + CL), retries used/max.</div>
          ${v3Focus()}
        </div>
      </div>
    `;
  }

  return { render };
})();
