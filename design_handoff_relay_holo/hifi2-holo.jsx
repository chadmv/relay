/* global React, HiFi2Shared */
const { StatusDot, Spark, DAGSVG, Chrome, J, TASKS, LOG, SPARK } = HiFi2Shared;

// ============================================================================
// V3 — HOLO.  Translucent layered glass. Blue/violet gradient borders. Depth.
// ============================================================================
function HoloVariant({ nav='sidebar', dag=true, palette }) {
  // Default palette: Aurora (violet/blue). Caller can pass [accent, accentB, ambientA, ambientB].
  const p = palette || ['#A78BFA', '#60A5FA', 'rgba(167,139,250,0.28)', 'rgba(96,165,250,0.25)'];
  const C = {
    bg:'#080612', bg2:'rgba(255,255,255,0.04)', bg3:'rgba(255,255,255,0.07)',
    border: hexToRgba(p[0], 0.18), borderHot: hexToRgba(p[1], 0.55),
    fg:'#EDE9FE', fgMute:'#9E96B6', fgDim:'#5C5675',
    accent: p[0], accentB: p[1],
    ambientA: p[2], ambientB: p[3],
    ok:'#6EE7B7', warn:'#FCD34D', err:'#FB7185',
  };
  const mono = "'JetBrains Mono', ui-monospace, monospace";
  const sans = "'Space Grotesk', system-ui, sans-serif";

  const glassPanel = {
    background:`linear-gradient(180deg, rgba(255,255,255,0.06), rgba(255,255,255,0.02))`,
    border:`1px solid ${C.border}`,
    backdropFilter:'blur(8px)',
    borderRadius:14,
    boxShadow:`inset 0 1px 0 rgba(255,255,255,0.08), 0 8px 32px rgba(0,0,0,0.4)`,
    position:'relative',
  };

  return (
    <div style={{width:'100%',height:'100%',
      background:`
        radial-gradient(900px 600px at 85% 0%, ${C.ambientB}, transparent 60%),
        radial-gradient(700px 500px at 0% 100%, ${C.ambientA}, transparent 55%),
        radial-gradient(500px 400px at 50% 50%, rgba(236,72,153,0.10), transparent 70%),
        ${C.bg}`,
      fontFamily:sans, color:C.fg, position:'relative', overflow:'hidden'}}>

      {/* drifting orbs */}
      <div style={{position:'absolute',inset:0,pointerEvents:'none'}}>
      </div>

      <Chrome variant="holo" nav={nav} accent={C.accent} mono={mono} sans={sans}
        surface={{bg2:'rgba(255,255,255,0.03)', bg3:'rgba(255,255,255,0.07)', border:C.border}} fg={C.fg} fgMute={C.fgMute}>

        <div style={{padding:'18px 22px', display:'flex',alignItems:'center',gap:14}}>
          <span style={{fontFamily:mono,fontSize:11,letterSpacing:'0.18em',color:C.fgMute}}>JOB</span>
          <span style={{fontFamily:mono,fontSize:14,letterSpacing:'0.06em',
            background:`linear-gradient(90deg, ${C.accent}, ${C.accentB})`,
            WebkitBackgroundClip:'text',WebkitTextFillColor:'transparent'}}>{J.id}</span>
          <span style={{fontSize:18,letterSpacing:'-0.01em',color:C.fg}}>{J.name}</span>
          <span style={{marginLeft:'auto',display:'flex',gap:8}}>
            <button style={pill(C,'ghost')}>Logs</button>
            <button style={pill(C,'ghost')}>Retry</button>
            <button style={pill(C,'danger')}>Abort</button>
          </span>
        </div>

        <div style={{flex:1,minHeight:0,padding:'4px 22px 22px',display:'grid',
          gridTemplateColumns:'1fr 1fr 320px',gap:14}}>

          {/* CENTER-LEFT — Hero status panel + DAG */}
          <div style={{display:'flex',flexDirection:'column',gap:14, gridColumn:'1 / span 2', minHeight:0}}>

            {/* Hero status */}
            <div style={{...glassPanel, padding:'22px 26px', display:'grid',gridTemplateColumns:'auto 1fr', gap:28, alignItems:'center'}}>
              {/* Donut */}
              <Donut value={J.pct} accent={C.accent} accentB={C.accentB} bgRing={C.bg3} mono={mono} fg={C.fg} fgMute={C.fgMute}/>
              <div>
                <div style={{display:'flex',alignItems:'center',gap:10,marginBottom:14}}>
                  <span style={{display:'inline-flex',alignItems:'center',gap:8,padding:'4px 10px',
                    borderRadius:999,background:`${C.accent}22`,border:`1px solid ${C.accent}55`,
                    fontFamily:mono,fontSize:10,letterSpacing:'0.18em',color:C.accent}}>
                    <span style={{width:6,height:6,borderRadius:'50%',background:C.accent}}/>
                    RUNNING
                  </span>
                  <span style={{fontFamily:mono,fontSize:11,color:C.fgMute,letterSpacing:'0.14em'}}>FRAME {J.tasksDone} OF {J.tasksTotal}</span>
                </div>
                <div style={{display:'grid',gridTemplateColumns:'repeat(4,1fr)',gap:18}}>
                  {[
                    ['Elapsed', J.elapsed],
                    ['ETA', J.eta, true],
                    ['GPU', '91%'],
                    ['Throughput', '3.4/s'],
                  ].map(([k,v,hot])=>(
                    <div key={k}>
                      <div style={{fontSize:10,letterSpacing:'0.16em',color:C.fgMute,marginBottom:4,fontFamily:mono}}>{k.toUpperCase()}</div>
                      <div style={{fontSize:22,fontFamily:mono,fontWeight:300,
                        color: hot ? 'transparent' : C.fg,
                        background: hot ? `linear-gradient(90deg, ${C.accent}, ${C.accentB})` : 'none',
                        WebkitBackgroundClip: hot ? 'text' : 'unset',
                        WebkitTextFillColor: hot ? 'transparent' : C.fg,
                      }}>{v}</div>
                    </div>
                  ))}
                </div>
              </div>
            </div>

            {dag && (
              <div style={{...glassPanel, padding:'18px 18px 8px', flex:1, minHeight:0, display:'flex', flexDirection:'column'}}>
                <div style={{display:'flex',alignItems:'center',justifyContent:'space-between',marginBottom:6}}>
                  <span style={{fontSize:13,letterSpacing:'0.04em',color:C.fg}}>Pipeline</span>
                  <span style={{fontFamily:mono,fontSize:10,color:C.fgMute,letterSpacing:'0.16em'}}>STAGE 4 / 8</span>
                </div>
                <DAGSVG tokens={{
                  edge: hexToRgba(C.accent, 0.2), edgeActive:C.accent,
                  nodeBg:'rgba(255,255,255,0.06)', ok:'#6EE7B7', run:C.accent, idle:C.fgDim,
                  fg:C.fg, font:mono, radius:6,
                }} h={170}/>
              </div>
            )}

            {!dag && (
              <div style={{...glassPanel, padding:'18px 18px', flex:1, minHeight:0, display:'flex', flexDirection:'column'}}>
                <div style={{fontSize:13,letterSpacing:'0.04em',color:C.fg,marginBottom:12}}>Tasks</div>
                <div style={{flex:1,minHeight:0,overflow:'auto'}}>
                  {TASKS.slice(0,12).map(([id,idx,st,pct,worker,dur])=>(
                    <TaskRow key={id} idx={idx} st={st} pct={pct} worker={worker} dur={dur} C={C} mono={mono}/>
                  ))}
                </div>
              </div>
            )}
          </div>

          {/* RIGHT — Logs */}
          <div style={{...glassPanel, display:'flex',flexDirection:'column',minHeight:0}}>
            <div style={{padding:'14px 16px 8px',display:'flex',alignItems:'center',justifyContent:'space-between',
              borderBottom:`1px solid ${C.border}`}}>
              <span style={{fontSize:13,color:C.fg}}>Log stream</span>
              <span style={{display:'inline-flex',alignItems:'center',gap:6,fontFamily:mono,fontSize:10,letterSpacing:'0.16em',color:C.ok}}>
                <span style={{width:5,height:5,borderRadius:'50%',background:C.ok}}/> LIVE
              </span>
            </div>
            <div style={{padding:'10px 14px',fontFamily:mono,fontSize:10.5,lineHeight:1.7,color:C.fgMute,
              minHeight:0,overflow:'auto',flex:1}}>
              {LOG.map((l,i)=>{
                const lvlC = l[1].trim()==='WARN'?C.warn : l[1].trim()==='DEBUG'?C.fgMute : C.accent;
                return (
                  <div key={i} style={{padding:'2px 0'}}>
                    <span style={{color:C.fgDim}}>{l[0]} </span>
                    <span style={{color:lvlC,fontWeight:600,letterSpacing:'0.04em'}}>{l[1].trim()} </span>
                    <span style={{color:C.fg}}><span style={{color:C.accentB}}>{l[2]}</span> {l[3]}</span>
                  </div>
                );
              })}
            </div>
          </div>

          {/* Bottom row — Tasks (when DAG on) + Spec */}
          {dag && (
            <div style={{...glassPanel, gridColumn:'1 / span 2', display:'flex',flexDirection:'column',minHeight:0,maxHeight:200}}>
              <div style={{padding:'12px 18px 6px',fontSize:13,color:C.fg,borderBottom:`1px solid ${C.border}`,
                display:'flex',justifyContent:'space-between',alignItems:'center'}}>
                <span>Tasks · {J.tasksDone} / {J.tasksTotal}</span>
                <span style={{fontFamily:mono,fontSize:10,color:C.fgMute,letterSpacing:'0.14em'}}>4 ACTIVE · 12 QUEUED</span>
              </div>
              <div style={{flex:1,minHeight:0,overflow:'auto',padding:'4px 18px'}}>
                {TASKS.slice(0,8).map(([id,idx,st,pct,worker,dur])=>(
                  <TaskRow key={id} idx={idx} st={st} pct={pct} worker={worker} dur={dur} C={C} mono={mono}/>
                ))}
              </div>
            </div>
          )}

          <div style={{...glassPanel, padding:'14px 16px', fontFamily:mono, fontSize:11, lineHeight:1.85, color:C.fg}}>
            <div style={{fontFamily:sans,fontSize:13,marginBottom:10,color:C.fg}}>Spec</div>
            {[
              ['image',J.image],['runtime',J.runtime],['cluster',J.cluster],
              ['owner',J.owner],['priority',J.priority],
            ].map(([k,v])=>(
              <div key={k} style={{display:'grid',gridTemplateColumns:'80px 1fr',gap:8,padding:'1px 0'}}>
                <span style={{color:C.fgMute,letterSpacing:'0.06em'}}>{k}</span>
                <span style={{wordBreak:'break-all'}}>{v}</span>
              </div>
            ))}
            <div style={{marginTop:10,paddingTop:10,borderTop:`1px solid ${C.border}`}}>
              <div style={{fontSize:10,letterSpacing:'0.16em',color:C.fgMute,marginBottom:6}}>GPU 60s</div>
              <Spark data={SPARK} w={272} h={42} color={C.accent}/>
            </div>
          </div>
        </div>
      </Chrome>
    </div>
  );
}

function pill(C, kind){
  const base = {padding:'7px 14px',borderRadius:999,fontFamily:"'Space Grotesk',system-ui,sans-serif",
    fontSize:12,letterSpacing:'0.02em',cursor:'pointer',backdropFilter:'blur(8px)'};
  if(kind==='danger') return {...base, background:'rgba(251,113,133,0.12)',border:`1px solid ${C.err}55`,color:C.err};
  return {...base, background:'rgba(255,255,255,0.05)',border:`1px solid ${C.border}`,color:C.fg};
}

function TaskRow({ idx, st, pct, worker, dur, C, mono }){
  const sc = st==='done'?C.ok: st==='running'?C.accent : C.fgMute;
  return (
    <div style={{display:'grid',gridTemplateColumns:'56px 96px 1fr 100px 50px',
      alignItems:'center', padding:'6px 0', fontFamily:mono, fontSize:11, color:C.fg,
      borderBottom:`1px solid ${C.border}`}}>
      <span style={{color:C.fgMute}}>#{idx}</span>
      <span style={{display:'flex',alignItems:'center',gap:8,color:sc,letterSpacing:'0.08em'}}>
        <StatusDot status={st} size={6}/> {st}
      </span>
      <span style={{color:st==='queued'?C.fgMute:C.fg}}>{worker}</span>
      <span style={{position:'relative',height:3,background:'rgba(255,255,255,0.08)',borderRadius:2,overflow:'hidden'}}>
        <span style={{position:'absolute',inset:0,width:`${pct}%`,
          background: st==='done'?C.ok:`linear-gradient(90deg,${C.accent},${C.accentB})`,
          borderRadius:2}}/>
      </span>
      <span style={{textAlign:'right',color:C.fgMute}}>{dur}</span>
    </div>
  );
}

function Donut({ value, accent, accentB, bgRing, mono, fg, fgMute }) {
  const r=58, cx=72, cy=72, circ=2*Math.PI*r;
  const off = circ * (1 - value/100);
  return (
    <svg width="144" height="144" viewBox="0 0 144 144">
      <defs>
        <linearGradient id="holo-grad" x1="0" y1="0" x2="1" y2="1">
          <stop offset="0%" stopColor={accent}/>
          <stop offset="100%" stopColor={accentB}/>
        </linearGradient>
      </defs>
      <circle cx={cx} cy={cy} r={r} fill="none" stroke={bgRing} strokeWidth="6"/>
      <circle cx={cx} cy={cy} r={r} fill="none" stroke="url(#holo-grad)" strokeWidth="6"
        strokeDasharray={circ} strokeDashoffset={off}
        transform={`rotate(-90 ${cx} ${cy})`} strokeLinecap="round"
        />
      <text x={cx} y={cy-2} textAnchor="middle" fontFamily={mono} fontSize="32" fill={fg} fontWeight="300" letterSpacing="-0.02em">{value}</text>
      <text x={cx} y={cy+18} textAnchor="middle" fontFamily={mono} fontSize="10" fill={fgMute} letterSpacing="0.18em">PERCENT</text>
    </svg>
  );
}

// Convert #RRGGBB or #RGB to rgba(r,g,b,a) — falls through if already rgba.
function hexToRgba(c, a) {
  if (typeof c !== 'string') return `rgba(167,139,250,${a})`;
  if (c.startsWith('rgba') || c.startsWith('rgb(')) return c;
  let h = c.replace('#','');
  if (h.length === 3) h = h.split('').map(x=>x+x).join('');
  const r = parseInt(h.slice(0,2),16), g = parseInt(h.slice(2,4),16), b = parseInt(h.slice(4,6),16);
  return `rgba(${r},${g},${b},${a})`;
}

window.HoloVariant = HoloVariant;
window.HoloPalettes = {
  mono:   { name:'Mono',    colors:['#38BDF8','#38BDF8'], ambient:['rgba(56,189,248,0.24)','rgba(56,189,248,0.18)'] },
};
