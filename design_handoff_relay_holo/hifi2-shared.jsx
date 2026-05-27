/* global React, RelayJob */
const { useState, useMemo } = React;

// ============================================================================
// Shared bits
// ============================================================================
const J = RelayJob.JOB;
const TASKS = RelayJob.TASKS;
const DAG = RelayJob.DAG;
const LOG = RelayJob.LOG;
const SPARK = RelayJob.SPARK;

// Status palettes (semantic; each variant tints them)
function StatusDot({ status, size=8, glow=false }) {
  const c = {
    done:'#5BE0A6', running:'#39D6FF', queued:'#9BA3B0',
    failed:'#FF5C7A', cancelled:'#777', timed_out:'#FF8A3D',
  }[status] || '#9BA3B0';
  return <span style={{
    width:size,height:size,borderRadius:'50%',background:c,display:'inline-block',
    boxShadow: glow?`0 0 ${size}px ${c}, 0 0 ${size*2}px ${c}88`:'none',
    flex:'none',
  }} />;
}

function Spark({ data, w=160, h=28, color='#39D6FF' }) {
  const max = Math.max(...data), min = Math.min(...data);
  const path = data.map((v,i)=>{
    const x = (i/(data.length-1))*w;
    const y = h - ((v-min)/(max-min || 1))*h;
    return `${i?'L':'M'}${x.toFixed(1)} ${y.toFixed(1)}`;
  }).join(' ');
  const fill = `${path} L${w} ${h} L0 ${h} Z`;
  return (
    <svg width={w} height={h} viewBox={`0 0 ${w} ${h}`} style={{display:'block'}}>
      <defs>
        <linearGradient id={`sg-${color.slice(1)}`} x1="0" x2="0" y1="0" y2="1">
          <stop offset="0%" stopColor={color} stopOpacity="0.35"/>
          <stop offset="100%" stopColor={color} stopOpacity="0"/>
        </linearGradient>
      </defs>
      <path d={fill} fill={`url(#sg-${color.slice(1)})`} />
      <path d={path} fill="none" stroke={color} strokeWidth="1.25" />
    </svg>
  );
}

// DAG renderer — used by all variants. Caller passes color tokens.
function DAGSVG({ tokens, w=900, h=180 }) {
  const rectW = 96, rectH = 32;
  const maxX = Math.max(...DAG.nodes.map(n=>n[3]));
  const maxY = Math.max(...DAG.nodes.map(n=>n[4]));
  const padX = rectW/2 + 12;
  const cellW = (w - padX*2) / Math.max(maxX-1, 1);
  const cellH = h / (maxY + 1);
  const pos = (x,y) => ({ cx: padX + cellW*(x-1), cy: cellH*(y+0.5) });
  return (
    <svg width="100%" height={h} viewBox={`0 0 ${w} ${h}`} preserveAspectRatio="xMidYMid meet">
      {DAG.edges.map(([a,b],i)=>{
        const an = DAG.nodes.find(n=>n[0]===a), bn = DAG.nodes.find(n=>n[0]===b);
        const ap = pos(an[3],an[4]), bp = pos(bn[3],bn[4]);
        const x1 = ap.cx + rectW/2, x2 = bp.cx - rectW/2;
        const mx = (x1+x2)/2;
        const d = `M${x1} ${ap.cy} C${mx} ${ap.cy}, ${mx} ${bp.cy}, ${x2} ${bp.cy}`;
        const active = an[2]==='done' || an[2]==='running';
        return <path key={i} d={d} fill="none"
          stroke={active?tokens.edgeActive:tokens.edge}
          strokeWidth={active?1.5:1} strokeDasharray={active?'':'3 3'} />;
      })}
      {DAG.nodes.map(([id,lbl,st,x,y])=>{
        const p = pos(x,y);
        const sc = { done:tokens.ok, running:tokens.run, queued:tokens.idle }[st];
        return (
          <g key={id} transform={`translate(${p.cx-rectW/2}, ${p.cy-rectH/2})`}>
            <rect width={rectW} height={rectH} rx={tokens.radius||0}
              fill={tokens.nodeBg} stroke={sc} strokeWidth="1" />
            <circle cx={10} cy={rectH/2} r={3} fill={sc} />
            <text x={20} y={rectH/2+1} dominantBaseline="middle"
              fontFamily={tokens.font} fontSize="11" fill={tokens.fg}
              letterSpacing="0.02em">{lbl}</text>
            {st==='running' && (
              <rect x={0} y={rectH-2} width={rectW*0.6} height={2} fill={sc}>
                <animate attributeName="x" values={`0;${rectW*0.4};0`} dur="2s" repeatCount="indefinite"/>
              </rect>
            )}
          </g>
        );
      })}
    </svg>
  );
}

// Tiny brackets — for sci-fi corner decorations
function Brackets({ color, inset=0, len=14, weight=1.5 }) {
  const c = { position:'absolute', width:len, height:len, borderColor:color, borderStyle:'solid' };
  return (
    <>
      <span style={{...c, top:inset, left:inset, borderWidth:`${weight}px 0 0 ${weight}px`}}/>
      <span style={{...c, top:inset, right:inset, borderWidth:`${weight}px ${weight}px 0 0`}}/>
      <span style={{...c, bottom:inset, left:inset, borderWidth:`0 0 ${weight}px ${weight}px`}}/>
      <span style={{...c, bottom:inset, right:inset, borderWidth:`0 ${weight}px ${weight}px 0`}}/>
    </>
  );
}

// Top-level chrome shared by all variants — the nav surface adapts
function Chrome({ children, variant, nav, accent, mono, sans, surface, fgMute, fg }) {
  const NAV_ITEMS = ['Jobs','Workers','Schedules','Storage','Admin'];
  if (nav === 'sidebar') {
    return (
      <div style={{display:'grid', gridTemplateColumns:'200px 1fr', height:'100%', color:fg}}>
        <aside style={{
          borderRight:`1px solid ${surface.border}`,
          background:surface.bg2, padding:'16px 14px',
          display:'flex', flexDirection:'column', gap:18,
        }}>
          <div style={{display:'flex',alignItems:'center',gap:8, fontFamily:mono, fontSize:11, letterSpacing:'0.18em', color:fgMute}}>
            <span style={{width:18,height:18,border:`1.5px solid ${accent}`,
              display:'grid',placeItems:'center',color:accent,fontWeight:700,fontSize:11}}>R</span>
            <span>RELAY · 2.4.1</span>
          </div>
          <nav style={{display:'flex',flexDirection:'column',gap:2}}>
            {NAV_ITEMS.map((n,i)=>(
              <a key={n} style={{
                fontFamily:sans, fontSize:13, padding:'7px 10px',
                color: i===0?fg:fgMute,
                background: i===0?surface.bg3:'transparent',
                borderLeft: i===0?`2px solid ${accent}`:'2px solid transparent',
                textDecoration:'none', letterSpacing:'0.02em',
              }}>{n}</a>
            ))}
          </nav>
          <div style={{marginTop:'auto', fontFamily:mono, fontSize:10, color:fgMute, lineHeight:1.6}}>
            <div>NODE · CTRL-01</div>
            <div>UPTIME · 41d 02h</div>
            <div style={{display:'flex',alignItems:'center',gap:6,marginTop:6}}>
              <span style={{width:6,height:6,borderRadius:'50%',background:'#5BE0A6'}}/> SYNC OK
            </div>
          </div>
        </aside>
        <div style={{minWidth:0, display:'flex', flexDirection:'column'}}>{children}</div>
      </div>
    );
  }
  // topbar
  return (
    <div style={{display:'flex', flexDirection:'column', height:'100%', color:fg}}>
      <div style={{
        display:'flex',alignItems:'center', gap:24, padding:'10px 18px',
        borderBottom:`1px solid ${surface.border}`,
        background:surface.bg2,
      }}>
        <div style={{display:'flex',alignItems:'center',gap:8, fontFamily:mono, fontSize:11, letterSpacing:'0.18em', color:fgMute}}>
          <span style={{width:18,height:18,border:`1.5px solid ${accent}`,
            display:'grid',placeItems:'center',color:accent,fontWeight:700,fontSize:11}}>R</span>
          <span>RELAY · 2.4.1</span>
        </div>
        <nav style={{display:'flex',gap:2}}>
          {NAV_ITEMS.map((n,i)=>(
            <a key={n} style={{
              fontFamily:sans, fontSize:13, padding:'6px 12px',
              color: i===0?fg:fgMute,
              borderBottom: i===0?`2px solid ${accent}`:'2px solid transparent',
              textDecoration:'none', letterSpacing:'0.02em',
            }}>{n}</a>
          ))}
        </nav>
        <div style={{marginLeft:'auto', display:'flex', alignItems:'center', gap:14, fontFamily:mono, fontSize:10, color:fgMute}}>
          <span style={{display:'flex',alignItems:'center',gap:6}}>
            <span style={{width:6,height:6,borderRadius:'50%',background:'#5BE0A6'}}/> SYNC OK
          </span>
          <span>CTRL-01</span>
          <span style={{color:fg}}>mira@studio.dev</span>
        </div>
      </div>
      <div style={{minWidth:0, flex:1, display:'flex', flexDirection:'column'}}>{children}</div>
    </div>
  );
}

window.HiFi2Shared = { StatusDot, Spark, DAGSVG, Brackets, Chrome, J, TASKS, DAG, LOG, SPARK };
