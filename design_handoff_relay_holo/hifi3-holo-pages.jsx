/* global React, HiFi2Shared, HoloPalettes */
const { useState, useRef, useLayoutEffect, useEffect } = React;
const { StatusDot, Spark, DAGSVG, J: JOB_DETAIL, TASKS, LOG, SPARK } = HiFi2Shared;

// ============================================================================
// HOLO — propagated to all 5 screens. Topbar only. Palette-driven.
// ============================================================================
function hexToRgba(c, a) {
  if (typeof c !== 'string') return `rgba(167,139,250,${a})`;
  if (c.startsWith('rgba') || c.startsWith('rgb(')) return c;
  let h = c.replace('#','');
  if (h.length === 3) h = h.split('').map(x=>x+x).join('');
  const r = parseInt(h.slice(0,2),16), g = parseInt(h.slice(2,4),16), b = parseInt(h.slice(4,6),16);
  return `rgba(${r},${g},${b},${a})`;
}

function hexToHsl(hex){
  let r=parseInt(hex.slice(1,3),16)/255, g=parseInt(hex.slice(3,5),16)/255, b=parseInt(hex.slice(5,7),16)/255;
  const max=Math.max(r,g,b), min=Math.min(r,g,b);
  let h=0,s=0,l=(max+min)/2;
  if(max!==min){
    const d=max-min;
    s=l>0.5?d/(2-max-min):d/(max+min);
    switch(max){
      case r: h=(g-b)/d+(g<b?6:0); break;
      case g: h=(b-r)/d+2; break;
      case b: h=(r-g)/d+4; break;
    }
    h*=60;
  }
  return [h,s,l];
}
function hslToHex(h,s,l){
  const c=(1-Math.abs(2*l-1))*s;
  const x=c*(1-Math.abs(((h/60)%2)-1));
  const m=l-c/2;
  let r=0,g=0,b=0;
  if(h<60){ r=c;g=x; } else if(h<120){ r=x;g=c; } else if(h<180){ g=c;b=x; }
  else if(h<240){ g=x;b=c; } else if(h<300){ r=x;b=c; } else { r=c;b=x; }
  const f=(n)=>Math.round((n+m)*255).toString(16).padStart(2,'0');
  return '#'+f(r)+f(g)+f(b);
}
function shiftHue(hex, deg){
  const [h,s,l]=hexToHsl(hex);
  return hslToHex((h+deg+360)%360, s, l);
}

function makeTokens(palette, hueOffsets){
  const p = palette || ['#A78BFA','#60A5FA','rgba(167,139,250,0.28)','rgba(96,165,250,0.25)'];
  const accent = p[0];
  const off = hueOffsets || { cpu: 55, mem: -55, gpuMem: 20 };
  return {
    bg:'#080612', bg2:'rgba(255,255,255,0.04)', bg3:'rgba(255,255,255,0.07)',
    border: hexToRgba(p[0], 0.18), borderHot: hexToRgba(p[1], 0.55),
    fg:'#EDE9FE', fgMute:'#9E96B6', fgDim:'#5C5675',
    accent, accentB: p[1], ambientA: p[2], ambientB: p[3],
    cGpu:    accent,
    cCpu:    shiftHue(accent, off.cpu),
    cMem:    shiftHue(accent, off.mem),
    cGpuMem: shiftHue(accent, off.gpuMem),
    ok:'#6EE7B7', warn:'#FCD34D', err:'#FB7185',
    mono: "'JetBrains Mono', ui-monospace, monospace",
    sans: "'Space Grotesk', system-ui, sans-serif",
  };
}

function glassPanel(C){
  return {
    background:`linear-gradient(180deg, rgba(255,255,255,0.06), rgba(255,255,255,0.02))`,
    border:`1px solid ${C.border}`,
    backdropFilter:'blur(8px)',
    borderRadius:14,
    boxShadow:`inset 0 1px 0 rgba(255,255,255,0.08), 0 8px 32px rgba(0,0,0,0.4)`,
    position:'relative',
  };
}

// ── SORT CONTROL ────────────────────────────────────────────────────────────
// Surfaces the server-side ?sort= allowlist that the paginated list endpoints
// gained (jobs / scheduled-jobs / agent-enrollments / reservations). Every menu
// row is exactly ONE allowed (key, direction) pair — the mono token on the
// right is the literal value the client sends as ?sort=. Selecting a sort drops
// any in-flight cursor, because cursors are issued for a specific sort and the
// server 400s a sort/cursor mismatch.
function SortControl({ C, options, value, onChange, disabled, disabledHint, width }) {
  const [open, setOpen] = useState(false);
  const ref = useRef(null);
  useEffect(() => {
    if (!open) return;
    const onDoc = (e) => { if (ref.current && !ref.current.contains(e.target)) setOpen(false); };
    const onKey = (e) => { if (e.key === 'Escape') setOpen(false); };
    document.addEventListener('mousedown', onDoc);
    document.addEventListener('keydown', onKey);
    return () => { document.removeEventListener('mousedown', onDoc); document.removeEventListener('keydown', onKey); };
  }, [open]);
  const active = options.find(o => o.value === value) || options[0];
  const arrow = active.value[0] === '-' ? '↓' : '↑';
  return (
    <div ref={ref} style={{position:'relative', flex:'none'}}>
      <button
        onClick={() => { if (!disabled) setOpen(v => !v); }}
        title={disabled ? disabledHint : 'Sort — maps to ?sort= on the list endpoint'}
        style={{
          display:'flex', alignItems:'center', gap:8, padding:'6px 12px',
          borderRadius:999, cursor: disabled ? 'not-allowed' : 'pointer',
          background: open ? hexToRgba(C.accent,0.14) : 'rgba(0,0,0,0.25)',
          border:`1px solid ${open ? hexToRgba(C.accent,0.5) : C.border}`,
          color: disabled ? C.fgDim : C.fg, opacity: disabled ? 0.5 : 1,
          backdropFilter:'blur(8px)',
        }}>
        <span style={{fontFamily:C.mono, fontSize:9.5, letterSpacing:'0.18em', color:C.fgMute}}>SORT</span>
        <span style={{fontFamily:C.sans, fontSize:12, whiteSpace:'nowrap'}}>{active.label}</span>
        <span style={{fontFamily:C.mono, fontSize:12, color: disabled ? C.fgDim : C.accent}}>{disabled ? '⊘' : arrow}</span>
      </button>
      {open && !disabled && (
        <div style={{
          position:'absolute', top:'calc(100% + 6px)', right:0, zIndex:40,
          minWidth: width || 252, padding:6, borderRadius:12,
          background:'rgba(12,10,24,0.97)', border:`1px solid ${C.border}`,
          boxShadow:'0 18px 50px rgba(0,0,0,0.55)', backdropFilter:'blur(16px)',
        }}>
          <div style={{fontFamily:C.mono, fontSize:9, letterSpacing:'0.18em', color:C.fgDim, padding:'6px 10px 8px'}}>
            ORDER BY · ?sort=
          </div>
          {options.map(o => {
            const on = o.value === value;
            return (
              <button key={o.value} onClick={() => { onChange(o.value); setOpen(false); }}
                onMouseEnter={e => { if (!on) e.currentTarget.style.background = 'rgba(255,255,255,0.05)'; }}
                onMouseLeave={e => { if (!on) e.currentTarget.style.background = 'transparent'; }}
                style={{
                  display:'flex', alignItems:'center', gap:10, width:'100%',
                  padding:'8px 10px', borderRadius:8, border:'none', cursor:'pointer', textAlign:'left',
                  background: on ? hexToRgba(C.accent,0.16) : 'transparent',
                  color: on ? C.fg : C.fgMute,
                }}>
                <span style={{width:12, flex:'none', color: on ? C.accent : 'transparent', fontFamily:C.mono, fontSize:12}}>✓</span>
                <span style={{flex:1, fontFamily:C.sans, fontSize:12.5}}>{o.label}</span>
                <span style={{fontFamily:C.mono, fontSize:10, letterSpacing:'0.03em', color: on ? C.accentB : C.fgDim}}>{o.value}</span>
              </button>
            );
          })}
        </div>
      )}
    </div>
  );
}

// Priority rank — higher = more urgent, so "priority ↓" puts critical first.
const PRIORITY_RANK = { critical: 3, high: 2, normal: 1, low: 0 };

// "HH:MM" wall-clock → minutes since midnight (jobs sample's created/updated proxy).
function clockToMin(s) {
  const m = /^(\d{1,2}):(\d{2})/.exec(String(s || ''));
  return m ? (+m[1]) * 60 + (+m[2]) : null;
}

// Relative span ("in 7m", "2h 14m", "8m ago", "1d") → signed minutes (future +, past −).
function relMin(s) {
  s = String(s || '').trim();
  if (!s || s === '—') return null;
  const past = /ago/.test(s);
  let total = 0, found = false, mm;
  const re = /(\d+)\s*([dhm])/g;
  while ((mm = re.exec(s))) { found = true; const n = +mm[1]; total += mm[2] === 'd' ? n * 1440 : mm[2] === 'h' ? n * 60 : n; }
  if (!found) return null;
  return past ? -total : total;
}

// "May 01 09:00" → comparable number. "—"/blank → null.
const MONTHS = { Jan:1, Feb:2, Mar:3, Apr:4, May:5, Jun:6, Jul:7, Aug:8, Sep:9, Oct:10, Nov:11, Dec:12 };
function dateToNum(s) {
  const m = /([A-Za-z]{3})\s+(\d{1,2})\s+(\d{1,2}):(\d{2})/.exec(String(s || '').trim());
  if (!m) return null;
  return (MONTHS[m[1]] || 0) * 1e6 + (+m[2]) * 1e4 + (+m[3]) * 100 + (+m[4]);
}

// Stable sort honoring the server's NULL placement: DESC NULLS LAST / ASC NULLS
// FIRST. getVal(row, index) returns number | string | null.
function sortRows(rows, getVal, desc) {
  const dec = rows.map((row, i) => ({ row, i, v: getVal(row, i) }));
  dec.sort((a, b) => {
    const an = a.v === null || a.v === undefined, bn = b.v === null || b.v === undefined;
    if (an && bn) return a.i - b.i;
    if (an) return desc ? 1 : -1;
    if (bn) return desc ? -1 : 1;
    let c;
    if (typeof a.v === 'number' && typeof b.v === 'number') c = a.v - b.v;
    else c = String(a.v).localeCompare(String(b.v));
    if (c === 0) return a.i - b.i; // id tiebreaker
    return desc ? -c : c;
  });
  return dec.map(d => d.row);
}

// applySort(rows, "-name", { name:(r)=>... }) → sorted copy (or rows if no arm).
function applySort(rows, token, keyMap) {
  const desc = token[0] === '-';
  const key = desc ? token.slice(1) : token;
  const getVal = keyMap[key];
  return getVal ? sortRows(rows, getVal, desc) : rows;
}

function UserMenu({ C, email, onLogout, onNavigate }) {
  const [open, setOpen] = useState(false);
  const ref = useRef(null);
  useEffect(()=>{
    if(!open) return;
    const onDoc = (e)=>{ if(ref.current && !ref.current.contains(e.target)) setOpen(false); };
    document.addEventListener('mousedown', onDoc);
    const onKey = (e)=>{ if(e.key==='Escape') setOpen(false); };
    document.addEventListener('keydown', onKey);
    return ()=>{ document.removeEventListener('mousedown', onDoc); document.removeEventListener('keydown', onKey); };
  },[open]);
  const initials = (email||'?').split('@')[0].slice(0,2).toUpperCase();
  const items = [
    { key:'profile',  label:'Profile',          hint:'name, avatar',           icon:'◐' },
    { key:'password', label:'Change password',  hint:'PUT /users/me/password', icon:'⌥' },
    { key:'sessions', label:'Sessions',         hint:'3 active · 30-day TTL',  icon:'≡' },
  ];
  const goTo = (key) => { setOpen(false); onNavigate && onNavigate(key); };
  return (
    <div ref={ref} style={{position:'relative'}}>
      <button onClick={()=>setOpen(v=>!v)} style={{
        display:'flex',alignItems:'center',gap:8,
        padding:'4px 10px 4px 4px',borderRadius:999,
        background: open ? hexToRgba(C.accent,0.14) : hexToRgba(C.accent,0.08),
        border:`1px solid ${open ? hexToRgba(C.accent,0.45) : C.border}`,
        color:C.fgMute,fontFamily:C.mono,fontSize:10,letterSpacing:'0.12em',
        cursor:'pointer',outline:'none',transition:'all 120ms ease',
      }}>
        <span style={{width:22,height:22,borderRadius:'50%',
          background:`linear-gradient(135deg, ${C.accent}, ${C.accentB})`,
          display:'grid',placeItems:'center',color:'#fff',fontSize:9,
          fontWeight:700,letterSpacing:'0.04em',
          boxShadow:`inset 0 1px 0 rgba(255,255,255,0.25)`}}>{initials}</span>
        <span style={{color:C.fg}}>{email}</span>
        <span style={{fontSize:8,opacity:0.6,
          transform: open ? 'rotate(180deg)' : 'rotate(0)',
          transition:'transform 120ms ease'}}>▾</span>
      </button>

      {open && (
        <div style={{
          ...glassPanel(C),
          background:'rgba(14, 12, 30, 0.96)',
          position:'absolute', top:'calc(100% + 8px)', right:0, width:280,
          padding:'8px 6px', zIndex:50,
          boxShadow:`0 16px 48px rgba(0,0,0,0.5), inset 0 1px 0 rgba(255,255,255,0.08)`,
        }}>
          {/* Account header */}
          <div style={{display:'flex',alignItems:'center',gap:10,
            padding:'8px 10px 10px',
            borderBottom:`1px solid ${C.border}`,marginBottom:6}}>
            <span style={{width:32,height:32,borderRadius:'50%',
              background:`linear-gradient(135deg, ${C.accent}, ${C.accentB})`,
              display:'grid',placeItems:'center',color:'#fff',fontSize:12,
              fontWeight:700,letterSpacing:'0.04em',flex:'none',
              boxShadow:`inset 0 1px 0 rgba(255,255,255,0.25)`}}>{initials}</span>
            <div style={{display:'flex',flexDirection:'column',minWidth:0}}>
              <span style={{fontSize:12.5,color:C.fg,whiteSpace:'nowrap',
                overflow:'hidden',textOverflow:'ellipsis'}}>{email}</span>
              <span style={{fontFamily:C.mono,fontSize:9.5,letterSpacing:'0.16em',color:C.fgMute,marginTop:2}}>
                ADMIN · relay.studio.dev
              </span>
            </div>
          </div>

          {/* Menu items */}
          {items.map(it=>(
            <div key={it.key} onClick={()=>goTo(it.key)} style={{
              display:'grid',gridTemplateColumns:'18px 1fr auto',gap:10,alignItems:'center',
              padding:'8px 10px',borderRadius:6,cursor:'pointer',
              fontSize:12.5,color:C.fg,
            }}
              onMouseEnter={e=>e.currentTarget.style.background='rgba(255,255,255,0.05)'}
              onMouseLeave={e=>e.currentTarget.style.background='transparent'}>
              <span style={{fontFamily:C.mono,fontSize:11,color:C.fgMute,textAlign:'center'}}>{it.icon}</span>
              <span>{it.label}</span>
              {it.hint && <span style={{fontFamily:C.mono,fontSize:9.5,letterSpacing:'0.06em',color:C.fgDim,whiteSpace:'nowrap'}}>{it.hint}</span>}
            </div>
          ))}

          {/* Divider + logout */}
          <div style={{height:1,background:C.border,margin:'6px 4px'}}/>
          <div onClick={()=>{setOpen(false); onLogout && onLogout();}} style={{
            display:'grid',gridTemplateColumns:'18px 1fr auto',gap:10,alignItems:'center',
            padding:'8px 10px',borderRadius:6,cursor:'pointer',
            fontSize:12.5,color:C.err,
          }}
            onMouseEnter={e=>e.currentTarget.style.background=hexToRgba(C.err,0.08)}
            onMouseLeave={e=>e.currentTarget.style.background='transparent'}>
            <span style={{fontFamily:C.mono,fontSize:11,textAlign:'center'}}>⎋</span>
            <span>Log out</span>
            <span style={{fontFamily:C.mono,fontSize:9.5,letterSpacing:'0.06em',color:hexToRgba(C.err,0.6),whiteSpace:'nowrap'}}>DELETE /auth/token</span>
          </div>
        </div>
      )}
    </div>
  );
}

function HoloShell({ C, route, setRoute, children, hideNav, onProfileNavigate }) {
  const NAV = [['jobs','Jobs'],['workers','Workers'],['schedules','Schedules'],['admin','Admin']];
  return (
    <div style={{
      width:'100%', height:'100%',
      background:`
        radial-gradient(900px 600px at 85% 0%, ${C.ambientB}, transparent 60%),
        radial-gradient(700px 500px at 0% 100%, ${C.ambientA}, transparent 55%),
        radial-gradient(500px 400px at 50% 50%, rgba(236,72,153,0.10), transparent 70%),
        ${C.bg}`,
      fontFamily:C.sans, color:C.fg, position:'relative', overflow:'hidden',
      display:'flex', flexDirection:'column',
    }}>
      {!hideNav && (
        <div style={{
          display:'flex',alignItems:'center', gap:24, padding:'12px 22px',
          borderBottom:`1px solid ${C.border}`,
          background:'rgba(255,255,255,0.025)',
          backdropFilter:'blur(10px)', position:'relative', zIndex:10,
        }}>
          <div style={{display:'flex',alignItems:'center',gap:9, fontFamily:C.mono, fontSize:11, letterSpacing:'0.18em'}}>
            <span style={{width:20,height:20,borderRadius:5,
              background:`linear-gradient(135deg, ${C.accent}, ${C.accentB})`,
              display:'grid',placeItems:'center',color:'#fff',fontWeight:700,fontSize:11,
              }}>R</span>
            <span style={{
              color: C.accent,
              fontWeight:600,
            }}>RELAY</span>
            <span style={{color:C.fgDim}}>2.4.1</span>
          </div>
          <nav style={{display:'flex',gap:2}}>
            {NAV.map(([k,n])=>(
              <a key={k} onClick={()=>setRoute(k)} style={{
                fontFamily:C.sans, fontSize:13, padding:'7px 14px',
                color: k===route?C.fg:C.fgMute, cursor:'pointer',
                position:'relative', letterSpacing:'0.02em',
                borderBottom: k===route?`2px solid ${C.accent}`:'2px solid transparent',
              }}>{n}</a>
            ))}
          </nav>
          <div style={{marginLeft:'auto', display:'flex', alignItems:'center', gap:14, fontFamily:C.mono, fontSize:10, color:C.fgMute, letterSpacing:'0.12em'}}>
            <span style={{display:'flex',alignItems:'center',gap:6}}>
              <span style={{width:6,height:6,borderRadius:'50%',background:C.ok}}/> SYNC OK
            </span>
            <UserMenu C={C} email="mira@studio.dev"
              onNavigate={onProfileNavigate}
              onLogout={()=>setRoute && setRoute('auth')}/>
          </div>
        </div>
      )}
      <div style={{flex:1, minHeight:0, position:'relative', zIndex:1, display:'flex', flexDirection:'column'}}>
        {children}
      </div>
    </div>
  );
}

// ── AUTH ────────────────────────────────────────────────────────────────────
// Auth model: POST /v1/auth/login takes email + password and returns a
// 30-day bearer token. In the browser the token lives in cookie/localStorage;
// in the CLI (`relay login`) it gets written to ~/.relay/config.json.
// Either way, the user never pastes a token here — login is password-first.
function HoloAuth({ C, onSignIn, allowSelfRegister }) {
  const SERVER = 'relay.studio.dev';
  return (
    <div style={{flex:1, display:'grid',placeItems:'center', padding:40, position:'relative'}}>
      {/* Server indicator — self-hosted, so make it visible */}
      <div style={{position:'absolute',top:24,left:28,display:'flex',alignItems:'center',gap:8,
        fontFamily:C.mono,fontSize:10,letterSpacing:'0.18em',color:C.fgMute}}>
        <span style={{width:6,height:6,borderRadius:'50%',background:C.ok,
          boxShadow:`0 0 8px ${C.ok}`}}/>
        <span>COORDINATOR · {SERVER}</span>
      </div>
      <div style={{position:'absolute',top:24,right:28,fontFamily:C.mono,fontSize:10,
        letterSpacing:'0.18em',color:C.fgMute}}>RELAY · 2.4.1</div>

      <div style={{...glassPanel(C), padding:'40px 44px', width:440, textAlign:'center'}}>
        <div style={{display:'flex',justifyContent:'center',marginBottom:24}}>
          <div style={{width:56,height:56,borderRadius:14,
            background:`linear-gradient(135deg, ${C.accent}, ${C.accentB})`,
            display:'grid',placeItems:'center', color:'#fff',fontWeight:700,fontSize:24,
            boxShadow:`inset 0 1px 0 rgba(255,255,255,0.2)`}}>R</div>
        </div>
        <h1 style={{margin:'0 0 8px',fontSize:28,fontWeight:400,letterSpacing:'-0.01em'}}>Sign in</h1>
        <p style={{margin:'0 0 24px',color:C.fgMute,fontSize:13,lineHeight:1.5}}>
          Email and password — same credentials as <span style={{fontFamily:C.mono,fontSize:12,color:C.fg}}>relay login</span>.
        </p>
        <div style={{display:'flex',flexDirection:'column',gap:12,textAlign:'left'}}>
          <div>
            <div style={{fontFamily:C.mono,fontSize:10,letterSpacing:'0.16em',color:C.fgMute,marginBottom:6}}>EMAIL</div>
            <input defaultValue="mira@studio.dev" style={{
              width:'100%',background:'rgba(0,0,0,0.3)',border:`1px solid ${C.border}`,
              padding:'11px 14px',borderRadius:8,color:C.fg,fontFamily:C.mono,fontSize:13,outline:'none',
            }}/>
          </div>
          <div>
            <div style={{display:'flex',justifyContent:'space-between',alignItems:'baseline',marginBottom:6}}>
              <div style={{fontFamily:C.mono,fontSize:10,letterSpacing:'0.16em',color:C.fgMute}}>PASSWORD</div>
              <a style={{fontFamily:C.mono,fontSize:10,letterSpacing:'0.12em',color:C.fgMute,
                cursor:'pointer',textDecoration:'none'}}>forgot?</a>
            </div>
            <input type="password" defaultValue="••••••••••••" style={{
              width:'100%',background:'rgba(0,0,0,0.3)',border:`1px solid ${C.border}`,
              padding:'11px 14px',borderRadius:8,color:C.fg,fontFamily:C.mono,fontSize:13,outline:'none',
            }}/>
          </div>
          <button onClick={onSignIn} style={{
            marginTop:6, padding:'12px 16px', borderRadius:8, border:'none',
            background:`linear-gradient(90deg, ${C.accent}, ${C.accentB})`,
            color:'#fff', fontWeight:600, fontSize:14, letterSpacing:'0.02em', cursor:'pointer',
          }}>Sign in →</button>

          {/* Session note — mirrors the CLI behavior so users understand what they get */}
          <div style={{display:'flex',alignItems:'center',gap:8,marginTop:4,padding:'8px 10px',
            borderRadius:6,background:'rgba(255,255,255,0.02)',border:`1px solid ${C.border}`,
            fontFamily:C.mono,fontSize:10.5,letterSpacing:'0.04em',color:C.fgMute,lineHeight:1.5}}>
            <span style={{fontSize:11,color:C.accentB}}>ⓘ</span>
            <span>Issues a 30-day bearer token · manage in Profile → Sessions</span>
          </div>

          <div style={{textAlign:'center',marginTop:6,fontSize:12,color:C.fgMute,
            display:'flex',alignItems:'center',justifyContent:'center',gap:8,flexWrap:'wrap'}}>
            <span>No account?</span>
            <a style={{color:C.accentB,cursor:'pointer'}}>
              {allowSelfRegister ? 'Create an account →' : 'Register with invite →'}
            </a>
            {allowSelfRegister && (
              <span title="RELAY_ALLOW_SELF_REGISTER=true" style={{
                fontFamily:C.mono,fontSize:9.5,letterSpacing:'0.14em',
                padding:'2px 6px',borderRadius:3,
                background:hexToRgba(C.ok,0.12),color:C.ok,
                border:`1px solid ${hexToRgba(C.ok,0.35)}`}}>OPEN</span>
            )}
          </div>
        </div>
      </div>

      {/* CLI hint — small, ambient, for users coming from the terminal */}
      <div style={{position:'absolute',bottom:24,left:'50%',transform:'translateX(-50%)',
        display:'flex',alignItems:'center',gap:10,
        fontFamily:C.mono,fontSize:10.5,letterSpacing:'0.06em',color:C.fgMute,opacity:0.7}}>
        <span style={{opacity:0.5}}>terminal user?</span>
        <span style={{padding:'4px 8px',borderRadius:4,
          background:'rgba(0,0,0,0.35)',border:`1px solid ${C.border}`,color:C.fg}}>
          <span style={{color:C.ok}}>$</span> relay login
        </span>
        <span style={{opacity:0.5}}>→ writes ~/.relay/config.json</span>
      </div>
    </div>
  );
}

// ── JOBS LIST ───────────────────────────────────────────────────────────────
const JOBS_SAMPLE = [
  ['9F4E1C','film-x / shot-042 render','running',72,'14:22','14m','48/64','mira@studio.dev','high',null],
  ['A812EF','nightly etl — march','running',38,'12:24','2h 14m','19/50','system','normal','nightly-etl'],
  ['7B2901','blender-denoise batch','running',91,'13:58','38m','210/230','ada@studio.dev','normal',null],
  ['C41A02','ci: relay-server #2341','done',100,'14:30','4m 12s','12/12','ci-bot@studio.dev','low',null],
  ['C41A01','ci: relay-agent #2340','failed',100,'14:28','1m 08s','8/12','ci-bot@studio.dev','low',null],
  ['D99001','frames 1-1000 / teaser','queued',0,'14:31','—','0/1000','ada@studio.dev','normal',null],
  ['E10AA0','ml-eval — resnet50','done',100,'Apr 16','18m 02s','8/8','jin@studio.dev','normal','weekly-eval'],
  ['F0021B','proxy-encode s03e01','cancelled',44,'14:10','7m','11/25','mira@studio.dev','normal',null],
  ['9F4E1B','shot-041 render','done',100,'13:30','22m 04s','64/64','mira@studio.dev','high',null],
  ['DB4015','db-backup','done',100,'04:15','6m 52s','3/3','system','normal','db-backup'],
];
function HoloJobsList({ C, D, onOpen }) {
  D = D || {pad:'22px 26px',gap:16,rowPad:'10px 18px',rowFs:11.5,nameFs:13,laneCardPad:'10px 12px',barH:22};
  const [filter, setFilter] = useState('all');
  const [view, setView] = useState('table');
  const [tWindow, setTWindow] = useState('24h');
  const [mineOnly, setMineOnly] = useState(false);
  const [sort, setSort] = useState('-created_at');
  const ME = 'mira@studio.dev';
  let filtered = filter==='all' ? JOBS_SAMPLE : JOBS_SAMPLE.filter(j => j[2]===filter || (filter==='active' && (j[2]==='running'||j[2]==='queued')));
  if (mineOnly) filtered = filtered.filter(j => j[7] === ME);
  const VIEWS = [['table','☰','Table'],['lanes','⊞','Lanes'],['timeline','⌚','Timeline']];

  // ?sort= on /v1/jobs is allowed ONLY on the unfiltered list — the server 400s
  // sort+status together. So sorting is locked to the default while a status
  // chip is active, and picking a status chip snaps sort back to default.
  // (row: [id,name,st,pct,start,dur,tasks,owner,prio,sched])
  const JOBS_SORT = [
    { value:'-created_at', label:'Newest' },
    { value:'created_at',  label:'Oldest' },
    { value:'name',        label:'Name A→Z' },
    { value:'-name',       label:'Name Z→A' },
    { value:'-priority',   label:'Priority high→low' },
    { value:'priority',    label:'Priority low→high' },
    { value:'status',      label:'Status A→Z' },
    { value:'-status',     label:'Status Z→A' },
    { value:'-updated_at', label:'Recently updated' },
    { value:'updated_at',  label:'Least recently updated' },
  ];
  const jobsKeyMap = {
    created_at: (j) => clockToMin(j[4]),
    updated_at: (j) => clockToMin(j[4]),
    name:       (j) => j[1].toLowerCase(),
    priority:   (j) => PRIORITY_RANK[j[8]] ?? -1,
    status:     (j) => j[2],
  };
  const statusFiltered = filter !== 'all';
  const effSort = statusFiltered ? '-created_at' : sort;
  const sorted = applySort(filtered, effSort, jobsKeyMap);
  const pickFilter = (k) => { setFilter(k); if (k !== 'all') setSort('-created_at'); };
  return (
    <div style={{flex:1, padding:D.pad, display:'flex',flexDirection:'column',gap:D.gap, minHeight:0}}>
      <div style={{display:'flex',alignItems:'flex-end',gap:24,flexWrap:'wrap'}}>
        <div>
          <div style={{fontFamily:C.mono,fontSize:11,letterSpacing:'0.18em',color:C.fgMute,marginBottom:4}}>OVERVIEW</div>
          <h1 style={{margin:0,fontSize:32,fontWeight:400,letterSpacing:'-0.02em'}}>Jobs</h1>
        </div>

        <div style={{display:'flex',gap:18,fontFamily:C.mono,fontSize:11,color:C.fgMute,letterSpacing:'0.14em'}}>
          <span><b style={{color:C.accent,fontWeight:600,fontSize:18}}>3</b> RUNNING</span>
          <span><b style={{color:C.warn,fontWeight:600,fontSize:18}}>1</b> QUEUED</span>
          <span><b style={{color:C.ok,fontWeight:600,fontSize:18}}>487</b> DONE · 24H</span>
          <span><b style={{color:C.err,fontWeight:600,fontSize:18}}>12</b> FAILED · 24H</span>
        </div>

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
      </div>

      {/* View-specific toolbar */}
      <div style={{display:'flex',gap:8,marginTop:4,alignItems:'center',flexWrap:'wrap'}}>
        {view === 'table' && [['all','All'],['running','Running'],['queued','Queued'],['done','Done'],['failed','Failed']].map(([k,n])=>(
          <button key={k} onClick={()=>pickFilter(k)} style={{
            padding:'6px 14px', borderRadius:999, fontFamily:C.sans, fontSize:12, cursor:'pointer',
            background: filter===k?`linear-gradient(90deg, ${hexToRgba(C.accent,0.25)}, ${hexToRgba(C.accentB,0.18)})`:'rgba(255,255,255,0.04)',
            border:`1px solid ${filter===k?C.accent+'66':C.border}`,
            color: filter===k?C.fg:C.fgMute,
            backdropFilter:'blur(8px)',
          }}>{n}</button>
        ))}
        {view === 'lanes' && (
          <div style={{display:'flex',alignItems:'center',gap:10,padding:'6px 14px',borderRadius:999,
            background:'rgba(255,255,255,0.04)',border:`1px solid ${C.border}`,backdropFilter:'blur(8px)'}}>
            <span style={{fontFamily:C.mono,fontSize:10,letterSpacing:'0.16em',color:C.fgMute}}>CARDS / LANE</span>
            <button style={stepBtn(C)}>−</button>
            <span style={{fontFamily:C.mono,fontSize:13,color:C.fg,minWidth:18,textAlign:'center'}}>10</span>
            <button style={stepBtn(C)}>+</button>
          </div>
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
          <span style={{fontSize:13}}>◔</span> My jobs
        </button>
        {view === 'table' && (
          <SortControl C={C} options={JOBS_SORT} value={effSort} onChange={setSort}
            disabled={statusFiltered}
            disabledHint="Sorting is unavailable while a status filter is active — the server rejects sort + status together. Switch to All to sort."/>
        )}
      </div>

      {view === 'lanes' && <HoloLanes C={C} D={D} onOpen={onOpen}/>}
      {view === 'timeline' && <HoloTimeline C={C} window={tWindow}/>}
      {view === 'table' && (
      <div style={{...glassPanel(C), flex:1, minHeight:0, display:'flex',flexDirection:'column', overflow:'hidden'}}>
        <div style={{display:'grid',gridTemplateColumns:'90px 1fr 110px 140px 90px 80px 130px 32px',
          fontFamily:C.mono,fontSize:10,letterSpacing:'0.16em',color:C.fgMute,
          padding:'12px 18px',borderBottom:`1px solid ${C.border}`}}>
          <span>ID</span><span>NAME</span><span>STATUS</span><span>PROGRESS</span><span>STARTED</span><span>DUR</span><span>OWNER</span><span/>
        </div>
        <div style={{flex:1,minHeight:0,overflow:'auto'}}>
          {sorted.map((j,i)=>{
            const [id,name,st,pct,start,dur,tasks,owner,prio,sched] = j;
            const sc = st==='done'?C.ok: st==='running'?C.accent : st==='failed'?C.err: st==='queued'?C.warn: C.fgMute;
            const fillBg = st==='done'?C.ok : st==='failed'?C.err : st==='cancelled'?C.fgDim
              : `linear-gradient(90deg,${C.accent},${C.accentB})`;
            return (
              <div key={id} onClick={()=>onOpen(id)} style={{
                display:'grid',gridTemplateColumns:'90px 1fr 110px 140px 90px 80px 130px 32px',
                alignItems:'center',padding:D.rowPad,
                borderBottom:`1px solid ${hexToRgba(C.accent,0.06)}`,
                fontFamily:C.mono,fontSize:D.rowFs, cursor:'pointer',
                background: st==='running'?hexToRgba(C.accent,0.04):'transparent',
              }}>
                <span style={{color:C.fgMute}}>{id}</span>
                <span style={{display:'flex',alignItems:'center',gap:8,minWidth:0}}>
                  <span style={{color:C.fg,fontFamily:C.sans,fontSize:D.nameFs,
                    overflow:'hidden',textOverflow:'ellipsis',whiteSpace:'nowrap'}}>{name}</span>
                  {sched && (
                    <span title={`from schedule: ${sched}`} style={{
                      display:'inline-flex',alignItems:'center',gap:4,flex:'none',
                      padding:'1px 7px',borderRadius:999,
                      background:hexToRgba(C.accentB,0.12),
                      border:`1px solid ${hexToRgba(C.accentB,0.4)}`,
                      color:C.accentB, fontFamily:C.mono, fontSize:9.5, letterSpacing:'0.04em',
                    }}>
                      <span style={{fontSize:10}}>⟳</span>{sched}
                    </span>
                  )}
                </span>
                <span style={{display:'flex',alignItems:'center',gap:8,color:sc,letterSpacing:'0.06em'}}>
                  <StatusDot status={st} size={6}/> {st}
                </span>
                <span style={{display:'grid',gridTemplateColumns:'1fr 36px',gap:8,alignItems:'center',paddingRight:18}}>
                  <span style={{position:'relative',height:4,background:'rgba(255,255,255,0.08)',
                    borderRadius:2,overflow:'hidden'}}>
                    <span style={{position:'absolute',inset:0,width:`${pct}%`,
                      background:fillBg, borderRadius:2,
                      }}/>
                  </span>
                  <span style={{textAlign:'right',color:C.fg}}>{pct}%</span>
                </span>
                <span style={{color:C.fgMute}}>{start}</span>
                <span style={{color:C.fgMute}}>{dur}</span>
                <span style={{color:C.fgMute,fontSize:11}}>{owner}</span>
                <span style={{color:C.fgDim,textAlign:'right'}}>›</span>
              </div>
            );
          })}
        </div>
        <div style={{display:'flex',justifyContent:'space-between',alignItems:'center',
          padding:'10px 18px',borderTop:`1px solid ${C.border}`,
          fontFamily:C.mono,fontSize:10.5,letterSpacing:'0.08em',color:C.fgMute}}>
          <span>SHOWING <span style={{color:C.fg}}>1–{sorted.length}</span> OF <span style={{color:C.fg}}>2,341</span> · SORT <span style={{color:C.accentB}}>{effSort}</span> · CURSOR PAGINATED</span>
          <div style={{display:'flex',gap:6}}>
            <button style={{...pillBtn(C,'ghost'),padding:'4px 12px',fontSize:11,opacity:0.5}}>← prev</button>
            <button style={{...pillBtn(C,'ghost'),padding:'4px 12px',fontSize:11}}>next 50 →</button>
          </div>
        </div>
      </div>
      )}
    </div>
  );
}

function stepBtn(C){
  return {width:20,height:20,borderRadius:6,border:`1px solid ${C.border}`,
    background:'rgba(255,255,255,0.05)',color:C.fg,cursor:'pointer',
    fontFamily:C.mono,fontSize:13,lineHeight:1,padding:0};
}

// ── SWIMLANES ───────────────────────────────────────────────────────────────
function HoloLanes({ C, D, onOpen }) {
  D = D || {laneCardPad:'10px 12px'};
  const lanes = [
    {key:'queued', label:'Pending / Queued', total:5, color:C.warn,
      jobs:[['D99001','frames 1-1000 / teaser',0,'14:31','—','ada@studio.dev'],
            ['D99002','frames 1001-2000',0,'14:32','—','ada@studio.dev']]},
    {key:'running', label:'Running', total:3, color:C.accent,
      jobs:[['9F4E1C','film-x / shot-042 render',72,'14:22','14m','mira@studio.dev'],
            ['A812EF','nightly etl — march',38,'12:24','2h 14m','system'],
            ['7B2901','blender-denoise batch',91,'13:58','38m','ada@studio.dev']]},
    {key:'done', label:'Done · 24h', total:487, color:C.ok,
      jobs:[['C41A02','ci: relay-server #2341',100,'14:30','4m 12s','ci-bot@studio.dev'],
            ['9F4E1B','shot-041 render',100,'13:30','22m 04s','mira@studio.dev'],
            ['DB4015','db-backup',100,'04:15','6m 52s','system'],
            ['E10AA0','ml-eval — resnet50',100,'Apr 16','18m 02s','jin@studio.dev']]},
    {key:'failed', label:'Failed / Timed out', total:12, color:C.err,
      jobs:[['C41A01','ci: relay-agent #2340',100,'14:28','1m 08s','ci-bot@studio.dev'],
            ['L1NT01','lint-and-vet',100,'14:18','0m 28s','ci-bot@studio.dev'],
            ['T0UT01','oom-canary',100,'13:02','1h 00m','jin@studio.dev']]},
  ];
  return (
    <div style={{flex:1,minHeight:0,display:'grid',gridTemplateColumns:'repeat(4,1fr)',gap:12}}>
      {lanes.map(lane => {
        const overflow = lane.total - lane.jobs.length;
        return (
          <div key={lane.key} style={{...glassPanel(C),padding:'14px 12px',
            display:'flex',flexDirection:'column',minHeight:0,
            boxShadow:`inset 0 1px 0 rgba(255,255,255,0.08), 0 8px 32px rgba(0,0,0,0.4), inset 2px 0 0 ${hexToRgba(lane.color,0.6)}`}}>
            
            <div style={{display:'flex',justifyContent:'space-between',alignItems:'center',marginBottom:10,padding:'0 4px'}}>
              <span style={{display:'flex',alignItems:'center',gap:8}}>
                <span style={{width:6,height:6,borderRadius:'50%',background:lane.color}}/>
                <span style={{fontFamily:C.mono,fontSize:10.5,letterSpacing:'0.16em',color:C.fgMute}}>
                  {lane.label.toUpperCase()}
                </span>
              </span>
              <span style={{fontFamily:C.mono,fontSize:11,color:lane.color,fontWeight:600}}>{lane.total}</span>
            </div>
            <div style={{flex:1,minHeight:0,overflow:'auto',display:'flex',flexDirection:'column',gap:8,padding:'0 2px'}}>
              {lane.jobs.map(j => {
                const [id,name,pct,start,dur,owner] = j;
                return (
                  <div key={id} onClick={()=>onOpen(id)} style={{
                    background:'rgba(255,255,255,0.04)',
                    border:`1px solid ${C.border}`, borderRadius:8, padding:D.laneCardPad,
                    cursor:'pointer', backdropFilter:'blur(6px)',
                  }}>
                    <div style={{display:'flex',justifyContent:'space-between',alignItems:'baseline',marginBottom:6,gap:8}}>
                      <span style={{fontSize:12,color:C.fg,overflow:'hidden',textOverflow:'ellipsis',whiteSpace:'nowrap'}}>{name}</span>
                      <span style={{fontFamily:C.mono,fontSize:9.5,color:C.fgDim,letterSpacing:'0.08em',flex:'none'}}>{id}</span>
                    </div>
                    <div style={{position:'relative',height:3,background:'rgba(255,255,255,0.08)',borderRadius:2,overflow:'hidden',marginBottom:6}}>
                      <span style={{position:'absolute',inset:0,width:`${pct}%`,
                        background: lane.key==='done'?C.ok : lane.key==='failed'?C.err :
                          `linear-gradient(90deg,${C.accent},${C.accentB})`,
                        borderRadius:2}}/>
                    </div>
                    <div style={{display:'flex',justifyContent:'space-between',
                      fontFamily:C.mono,fontSize:10,color:C.fgMute}}>
                      <span>{dur}</span>
                      <span>{owner}</span>
                    </div>
                  </div>
                );
              })}
              {overflow > 0 && (
                <button style={{
                  padding:'10px 12px', borderRadius:8, cursor:'pointer',
                  background:`linear-gradient(180deg, ${hexToRgba(lane.color,0.05)}, transparent)`,
                  border:`1px dashed ${hexToRgba(lane.color,0.4)}`,
                  color:lane.color, fontFamily:C.mono, fontSize:11, letterSpacing:'0.08em',
                  textAlign:'center',
                }}>
                  + {overflow} MORE →
                </button>
              )}
            </div>
          </div>
        );
      })}
    </div>
  );
}

// ── TIMELINE ────────────────────────────────────────────────────────────────
function HoloTimeline({ C, window: w }) {
  const bars = [
    ['film-x / shot-042','running',30,45,72],
    ['nightly etl ⟳','running',5,85,38],
    ['blender-denoise','running',55,28,91],
    ['ci: relay-server #2341','done',48,5,100],
    ['ci: relay-agent #2340','failed',46,4,100],
    ['ml-eval ⟳','done',18,12,100],
    ['oom-canary','failed',28,22,100],
    ['shot-041 render','done',38,14,100],
    ['db-backup','done',8,4,100],
    ['proxy-encode','cancelled',72,8,44],
    ['cache-warm','done',12,3,100],
    ['frames teaser','queued',92,3,0],
  ];
  const ticks = w==='6h' ? ['-6h','-4h','-2h','-1h','now']
              : w==='7d' ? ['-7d','-5d','-3d','-1d','now']
              : ['00:00','06:00','12:00','18:00','now'];
  return (
    <div style={{...glassPanel(C),flex:1,minHeight:0,display:'flex',flexDirection:'column',overflow:'hidden'}}>
      <div style={{padding:'14px 20px',borderBottom:`1px solid ${C.border}`,
        display:'flex',justifyContent:'space-between',alignItems:'center'}}>
        <span style={{fontSize:13,color:C.fg}}>Timeline · last <span style={{color:C.accent}}>{w}</span></span>
        <span style={{fontFamily:C.mono,fontSize:10,letterSpacing:'0.16em',color:C.fgMute}}>TIME-WINDOWED · NO PAGINATION</span>
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
                  <span style={{color:C.fgMute}}>· {pct}%</span>
                </div>
              </div>
            </div>
          );
        })}
      </div>
    </div>
  );
}

function MiniTelemetry({ name, gpuNow, cpuNow, ramGb, hasGpu, stale, offline, C, layout }) {
  // layout: 'grid' (stacked rows) | 'table' (two compact spans)
  if (offline) {
    return (
      <div style={{display:'flex',flexDirection:'column',gap:6,opacity:0.5}}>
        <MiniRow label="GPU" value="—" series={null} color={C.cGpu} dim C={C}/>
        <MiniRow label="CPU" value="—" series={null} color={C.cCpu} dim C={C}/>
        <MiniRow label="MEM" value="—" series={null} color={C.cMem} dim C={C}/>
      </div>
    );
  }
  const samples = React.useMemo(
    () => fakeTelemetry(name, gpuNow, cpuNow, ramGb || 64, hasGpu),
    [name, gpuNow, cpuNow, ramGb, hasGpu]
  );
  const cpuSeries = samples.map(s => s.cpu_pct);
  const gpuSeries = samples.map(s => s.gpu_util_pct);
  const memSeries = samples.map(s => (s.mem_used / s.mem_total) * 100);
  const last = samples[samples.length-1];
  const memPct = (last.mem_used / last.mem_total) * 100;
  const memDetail = `${(last.mem_used/(1024**3)).toFixed(0)}/${ramGb}G`;
  if (layout === 'table') {
    // Return null — table renders rows themselves so it can place spans in the right grid cells
    return null;
  }
  return (
    <div style={{display:'flex',flexDirection:'column',gap:6, opacity: stale?0.55:1}}>
      {hasGpu && <MiniRow label="GPU" value={gpuNow} series={gpuSeries} color={C.cGpu} stale={stale} C={C}/>}
      <MiniRow label="CPU" value={cpuNow} series={cpuSeries} color={C.cCpu} stale={stale} C={C}/>
      <MiniRow label="MEM" value={memDetail} series={memSeries} color={C.cMem} stale={stale} C={C}/>
      {stale && (
        <div style={{fontFamily:C.mono,fontSize:9,letterSpacing:'0.14em',color:C.warn,marginTop:2}}>
          ⚠ STALE · NO TELEMETRY
        </div>
      )}
    </div>
  );
}

function MiniRow({ label, value, series, stale, dim, color, C }) {
  const lineColor = stale ? C.fgMute : (color || C.accentB);
  return (
    <div style={{display:'grid',gridTemplateColumns:'34px 1fr 28px',gap:6,alignItems:'center',
      fontFamily:C.mono,fontSize:10}}>
      <span style={{color:C.fgMute,letterSpacing:'0.12em', color: stale?C.fgMute : (color || C.fgMute)}}>{label}</span>
      <span style={{height:16,display:'flex',alignItems:'center'}}>
        {series ? (
          <Spark data={series} w={120} h={16} color={lineColor}/>
        ) : (
          <span style={{height:2,background:C.border,width:'100%'}}/>
        )}
      </span>
      <span style={{textAlign:'right',color:dim?C.fgDim:(stale?C.fgMute:C.fg)}}>
        {typeof value === 'number' ? `${value}%` : value}
      </span>
    </div>
  );
}

function RunningCell({ job, onOpenTask, C }) {
  if (job === '—') return <span style={{color:C.fgDim}}>idle</span>;
  // Split on ' · ' to separate the task-id list from the trailing job descriptor.
  const dotIdx = job.indexOf(' · ');
  const head = dotIdx >= 0 ? job.slice(0, dotIdx) : job;
  const tail = dotIdx >= 0 ? job.slice(dotIdx) : '';
  const tokens = head.split(/,\s*/);
  const clickStyle = {
    color:C.accent, cursor:'pointer', textDecoration:'underline',
    textDecorationColor:hexToRgba(C.accent,0.35), textUnderlineOffset:2,
  };
  return (
    <span style={{display:'inline-flex',alignItems:'center',gap:6,minWidth:0,overflow:'hidden',textOverflow:'ellipsis',whiteSpace:'nowrap'}}>
      <span style={{color:C.accent,flex:'none'}}>▶</span>
      <span style={{minWidth:0,overflow:'hidden',textOverflow:'ellipsis'}}>
        {tokens.map((t,i)=>{
          const isTaskId = /^t-\w+$/i.test(t);
          return (
            <React.Fragment key={t+i}>
              {i>0 && <span style={{color:C.fgDim}}>, </span>}
              {isTaskId ? (
                <span onClick={(e)=>{e.stopPropagation(); onOpenTask && onOpenTask(t);}}
                  style={clickStyle}>{t}</span>
              ) : (
                <span onClick={(e)=>{e.stopPropagation(); onOpenTask && onOpenTask(null);}}
                  style={{...clickStyle, color:C.fg, textDecorationColor:hexToRgba(C.fg,0.3)}}>{t}</span>
              )}
            </React.Fragment>
          );
        })}
        <span style={{color:C.fgMute}}>{tail}</span>
      </span>
    </span>
  );
}
// ── WORKERS ─────────────────────────────────────────────────────────────────
// [name, status, job(s), gpu, cpu, spec, uptime, offline, used, max, labels, lastSeenSec]
const WORKERS_SAMPLE = [
  ['farm-west-04','online · busy','t-049, t-050 · shot-042',91,64,'cuda · 24GB','17h', false, 2, 2, ['gpu','cuda:12.3','rack=A'], 3],
  ['farm-west-09','online · busy','t-051 · shot-042',88,71,'cuda · 24GB','17h', false, 1, 2, ['gpu','cuda:12.3','rack=A'], 4],
  ['farm-west-12','online · busy','t-052, t-053, t-054',94,68,'cuda · 24GB','17h', false, 3, 4, ['gpu','cuda:12.3','rack=A','priority'], 2],
  ['farm-west-01','online · busy','a812ef · etl',42,78,'cpu · 64GB','3d 04h', false, 1, 8, ['cpu','avx512','rack=A'], 5],
  ['farm-west-03','online · idle','—',2,8,'cuda · 24GB','17h', false, 0, 2, ['gpu','cuda:12.3','rack=A','draining'], 3],
  ['farm-east-02','online · stale','t-061 · etl',64,42,'cuda · 16GB','9h', false, 1, 2, ['gpu','cuda:12.3','rack=B'], 47],
  ['farm-east-05','online · busy','7b2901 · denoise',76,54,'cuda · 24GB','9h', false, 1, 2, ['gpu','cuda:12.3','rack=B'], 4],
  ['farm-east-11','disabled','—',0,0,'cuda · 24GB','22h', false, 0, 2, ['gpu','cuda:12.3','rack=B'], 1],
  ['farm-east-08','offline','—',0,0,'cuda · 16GB','—', true, 0, 2, ['gpu','cuda:12.3','rack=B'], null],
];
function HoloWorkers({ C, D, onOpen, onOpenTask }) {
  D = D || {pad:'22px 26px',gap:16,rowPad:'10px 18px'};
  const [view, setView] = useState('grid');
  const VIEWS = [['table','☰','Table'],['grid','⊞','Grid']];
  return (
    <div style={{flex:1, padding:D.pad, display:'flex',flexDirection:'column',gap:D.gap, minHeight:0}}>
      <div style={{display:'flex',alignItems:'flex-end',gap:24,flexWrap:'wrap'}}>
        <div>
          <div style={{fontFamily:C.mono,fontSize:11,letterSpacing:'0.18em',color:C.fgMute,marginBottom:4}}>FLEET</div>
          <h1 style={{margin:0,fontSize:32,fontWeight:400,letterSpacing:'-0.02em'}}>Workers</h1>
        </div>
        <div style={{display:'flex',gap:18,fontFamily:C.mono,fontSize:11,color:C.fgMute,letterSpacing:'0.14em'}}>
          <span><b style={{color:C.ok,fontWeight:600,fontSize:18}}>16</b> ONLINE</span>
          <span><b style={{color:C.accent,fontWeight:600,fontSize:18}}>13</b> BUSY</span>
          <span><b style={{color:C.fg,fontWeight:600,fontSize:18}}>2</b> IDLE</span>
          <span><b style={{color:C.warn,fontWeight:600,fontSize:18}}>1</b> STALE</span>
          <span><b style={{color:C.fgMute,fontWeight:600,fontSize:18}}>1</b> DISABLED</span>
          <span><b style={{color:C.err,fontWeight:600,fontSize:18}}>2</b> OFFLINE</span>
        </div>
        <div style={{marginLeft:'auto',display:'flex',padding:3,borderRadius:999,
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
            }}><span style={{fontSize:13}}>{icon}</span> {label}</button>
          ))}
        </div>
      </div>

      {view === 'grid' && (
        <div style={{flex:1,minHeight:0,overflow:'auto', display:'grid',
          gridTemplateColumns:'repeat(auto-fill, minmax(280px, 1fr))', gap:12, alignContent:'start'}}>
          {WORKERS_SAMPLE.map((w,i)=>{
            const [name,status,job,gpu,cpu,spec,uptime,offline,used,max,labels,lastSec] = w;
            const stale = status.includes('stale');
            const disabled = status === 'disabled';
            const isCuda = spec.startsWith('cuda');
            const ramGb = isCuda ? 128 : 64;
            const sc = offline ? C.err : disabled ? C.fgMute : stale ? C.warn : status.includes('busy') ? C.accent : C.ok;
            return (
              <div key={name} onClick={()=>onOpen && onOpen(name)} style={{...glassPanel(C), padding:'14px 16px',
                opacity: offline?0.55: disabled?0.7 :1, cursor:'pointer'}}>
                <div style={{display:'flex',justifyContent:'space-between',alignItems:'baseline',marginBottom:10}}>
                  <span style={{fontFamily:C.mono,fontSize:13,color:C.fg,letterSpacing:'0.04em'}}>{name}</span>
                  <span style={{fontFamily:C.mono,fontSize:9.5,letterSpacing:'0.16em',color:sc,
                    display:'flex',alignItems:'center',gap:6}}>
                    <span style={{width:6,height:6,borderRadius:'50%',background:sc}}/>
                    {status.toUpperCase()}
                  </span>
                </div>
                <div style={{display:'flex',justifyContent:'space-between',alignItems:'center',marginBottom:10,minHeight:18,gap:10}}>
                  <span style={{fontSize:12,color:C.fgMute,minWidth:0,overflow:'hidden'}}>
                    {disabled ? <span style={{fontFamily:C.mono,fontSize:10.5,letterSpacing:'0.06em',color:C.fgMute}}>scheduler · paused by admin</span> : <RunningCell job={job} onOpenTask={onOpenTask} C={C}/>}
                  </span>
                  <SlotIndicator used={used} max={max} C={C} offline={offline || disabled}/>
                </div>
                <MiniTelemetry name={name} gpuNow={gpu} cpuNow={cpu} ramGb={ramGb}
                  hasGpu={isCuda} stale={stale} offline={offline || disabled} C={C} layout="grid"/>
                {labels && labels.length > 0 && (
                  <div style={{display:'flex',flexWrap:'wrap',gap:4,marginTop:10}}>
                    {labels.map(l => {
                      const isDraining = l === 'draining';
                      const col = isDraining ? C.warn : C.accent;
                      return (
                        <span key={l} style={{
                          padding:'2px 8px',borderRadius:999,
                          background: hexToRgba(col, 0.1),
                          border: `1px solid ${hexToRgba(col, 0.4)}`,
                          fontFamily:C.mono,fontSize:9.5,letterSpacing:'0.04em',color:col,
                        }}>{l}</span>
                      );
                    })}
                  </div>
                )}
                <div style={{marginTop:10,paddingTop:10,borderTop:`1px solid ${C.border}`,
                  fontFamily:C.mono,fontSize:10,color:C.fgMute,letterSpacing:'0.06em',
                  display:'flex',justifyContent:'space-between'}}>
                  <span>{spec}</span>
                  <span>up {uptime}</span>
                </div>
              </div>
            );
          })}
        </div>
      )}

      {view === 'table' && (
        <div style={{...glassPanel(C), flex:1, minHeight:0, display:'flex',flexDirection:'column', overflow:'hidden'}}>
          <div style={{display:'grid',gridTemplateColumns:'1fr 84px 90px 1.1fr 84px 84px 110px 90px 1.2fr 50px',
            fontFamily:C.mono,fontSize:10,letterSpacing:'0.16em',color:C.fgMute,
            padding:'12px 18px',borderBottom:`1px solid ${C.border}`}}>
            <span>NAME</span><span>STATUS</span><span>SLOTS</span><span>RUNNING</span><span>GPU</span><span>CPU</span><span>MEM</span><span>SPEC</span><span>LABELS</span><span style={{textAlign:'right'}}>UP</span>
          </div>
          <div style={{flex:1,minHeight:0,overflow:'auto'}}>
            {WORKERS_SAMPLE.map((w,i)=>{
              const [name,status,job,gpu,cpu,spec,uptime,offline,used,max,labels,lastSec] = w;
              const stale = status.includes('stale');
              const disabled = status === 'disabled';
              const isCuda = spec.startsWith('cuda');
              const ramGb = isCuda ? 128 : 64;
              const sc = offline ? C.err : disabled ? C.fgMute : stale ? C.warn : status.includes('busy') ? C.accent : C.ok;
              const samples = !offline && !disabled ? fakeTelemetry(name, gpu, cpu, ramGb, isCuda) : null;
              const cpuSeries = samples ? samples.map(s=>s.cpu_pct) : null;
              const gpuSeries = samples ? samples.map(s=>s.gpu_util_pct) : null;
              const memSeries = samples ? samples.map(s=>(s.mem_used/s.mem_total)*100) : null;
              const memNow = samples ? Math.round((samples[samples.length-1].mem_used/samples[samples.length-1].mem_total)*100) : 0;
              return (
                <div key={name} onClick={()=>onOpen && onOpen(name)} style={{
                  display:'grid',gridTemplateColumns:'1fr 84px 90px 1.1fr 84px 84px 110px 90px 1.2fr 50px',
                  alignItems:'center', padding:D.rowPad,
                  borderBottom:`1px solid ${hexToRgba(C.accent,0.06)}`,
                  fontFamily:C.mono,fontSize:11.5,
                  opacity: offline?0.55: disabled?0.7 :1, cursor:'pointer',
                  background: status.includes('busy')?hexToRgba(C.accent,0.04):'transparent',
                }}>
                  <span style={{color:C.fg,letterSpacing:'0.04em'}}>{name}</span>
                  <span style={{display:'flex',alignItems:'center',gap:6,color:sc,letterSpacing:'0.06em',fontSize:10.5}}>
                    <span style={{width:6,height:6,borderRadius:'50%',background:sc}}/>
                    {status.split(' · ').pop()}
                  </span>
                  <span><SlotIndicator used={used} max={max} C={C} offline={offline || disabled}/></span>
                  <span style={{minWidth:0,overflow:'hidden',fontSize:11}}>
                    <RunningCell job={job} onOpenTask={onOpenTask} C={C}/>
                  </span>
                  <span><TableSpark series={gpuSeries} value={gpu} color={C.cGpu} stale={stale} offline={offline || disabled} C={C}/></span>
                  <span><TableSpark series={cpuSeries} value={cpu} color={C.cCpu} stale={stale} offline={offline || disabled} C={C}/></span>
                  <span><TableSpark series={memSeries} value={memNow} color={C.cMem} valueLabel={offline||disabled?'—':`${Math.round(memNow*ramGb/100)}/${ramGb}G`} stale={stale} offline={offline || disabled} C={C}/></span>
                  <span style={{color:C.fgMute,fontSize:10.5,letterSpacing:'0.04em'}}>{spec}</span>
                  <span style={{display:'flex',flexWrap:'wrap',gap:4,overflow:'hidden',maxHeight:22}}>
                    {(labels||[]).map(l => {
                      const isDraining = l === 'draining';
                      const col = isDraining ? C.warn : C.accent;
                      return (
                        <span key={l} style={{
                          padding:'1px 7px',borderRadius:999,
                          background: hexToRgba(col, 0.1),
                          border: `1px solid ${hexToRgba(col, 0.4)}`,
                          fontFamily:C.mono,fontSize:9.5,letterSpacing:'0.04em',color:col,
                          whiteSpace:'nowrap',
                        }}>{l}</span>
                      );
                    })}
                  </span>
                  <span style={{color:C.fgMute,textAlign:'right',fontSize:10.5}}>{uptime}</span>
                </div>
              );
            })}
          </div>
          <div style={{display:'flex',justifyContent:'space-between',alignItems:'center',
            padding:'10px 18px',borderTop:`1px solid ${C.border}`,
            fontFamily:C.mono,fontSize:10.5,letterSpacing:'0.08em',color:C.fgMute}}>
            <span>SHOWING <span style={{color:C.fg}}>1–{WORKERS_SAMPLE.length}</span> OF <span style={{color:C.fg}}>20</span> · CURSOR PAGINATED</span>
            <div style={{display:'flex',gap:6}}>
              <button style={{...pillBtn(C,'ghost'),padding:'4px 12px',fontSize:11,opacity:0.5}}>← prev</button>
              <button style={{...pillBtn(C,'ghost'),padding:'4px 12px',fontSize:11}}>next 20 →</button>
            </div>
          </div>
        </div>
      )}
    </div>
  );
}
function SlotIndicator({ used, max, C, offline }) {
  return (
    <span style={{fontFamily:C.mono,fontSize:11,color:offline?C.fgDim:C.fgMute,letterSpacing:'0.04em'}}>
      <span style={{color: offline?C.fgDim : used===0?C.fgMute:C.fg}}>{used}</span>
      <span style={{color:C.fgDim}}> / </span>
      {max}
    </span>
  );
}
function TableSpark({ series, value, valueLabel, color, stale, offline, C }) {
  if (offline) return <span style={{color:C.fgDim,fontFamily:C.mono,fontSize:10.5,paddingRight:24}}>—</span>;
  const display = valueLabel != null ? valueLabel : `${value}%`;
  return (
    <span style={{display:'inline-flex',alignItems:'center',gap:8,paddingRight:24}}>
      <Spark data={series} w={40} h={16} color={stale?C.fgMute:(color||C.accentB)}/>
      <span style={{color:stale?C.fgMute:C.fg,fontSize:10.5,whiteSpace:'nowrap'}}>{display}</span>
    </span>
  );
}
function InlineBar({ value, C }) {
  return (
    <span style={{display:'grid',gridTemplateColumns:'1fr 32px',gap:8,alignItems:'center',paddingRight:14}}>
      <span style={{position:'relative',height:4,background:'rgba(255,255,255,0.06)',borderRadius:2,overflow:'hidden'}}>
        <span style={{position:'absolute',inset:0,width:`${value}%`,
          background:`linear-gradient(90deg, ${C.accent}, ${C.accentB})`,borderRadius:2}}/>
      </span>
      <span style={{textAlign:'right',color:C.fg,fontSize:10.5}}>{value}%</span>
    </span>
  );
}
function Bar({ label, value, C }) {
  return (
    <div style={{display:'grid',gridTemplateColumns:'40px 1fr 40px',gap:8,alignItems:'center',
      fontFamily:C.mono,fontSize:10}}>
      <span style={{color:C.fgMute,letterSpacing:'0.12em'}}>{label}</span>
      <span style={{position:'relative',height:4,background:'rgba(255,255,255,0.06)',borderRadius:2,overflow:'hidden'}}>
        <span style={{position:'absolute',inset:0,width:`${value}%`,
          background:`linear-gradient(90deg, ${C.accent}, ${C.accentB})`,borderRadius:2}}/>
      </span>
      <span style={{textAlign:'right',color:C.fg}}>{value}%</span>
    </div>
  );
}

// ── WORKER DETAIL ───────────────────────────────────────────────────────────
// Fake last-30m telemetry stream — deterministic per worker, modeled on the
// real /v1/workers/{id}/metrics response shape (cpu_pct, mem_used/total,
// gpu_util_pct, gpu_mem_used/total). Sample interval 10s (180 samples / 30m).
function fakeTelemetry(name, gpuNow, cpuNow, ramGb, hasGpu) {
  let seed = 0;
  for (let i=0;i<name.length;i++) seed = (seed*31 + name.charCodeAt(i)) >>> 0;
  const rand = () => { seed = (seed * 1664525 + 1013904223) >>> 0; return (seed & 0xffff)/0xffff; };
  const memTotal = ramGb * 1024 * 1024 * 1024;
  const memBaseFrac = 0.35 + rand()*0.25;
  const gpuMemTotal = hasGpu ? (24 * 1024 * 1024 * 1024) : 0;
  const N = 180;
  const samples = [];
  let cpu = cpuNow * (0.6 + rand()*0.3);
  let gpu = gpuNow * (0.6 + rand()*0.3);
  let mem = memBaseFrac;
  const t0 = Date.now() - N*10*1000;
  for (let i=0;i<N;i++) {
    cpu = Math.max(2, Math.min(99, cpu + (rand()-0.5)*8 + (cpuNow - cpu)*0.04));
    gpu = Math.max(0, Math.min(99, gpu + (rand()-0.5)*10 + (gpuNow - gpu)*0.04));
    mem = Math.max(0.2, Math.min(0.95, mem + (rand()-0.5)*0.015));
    samples.push({
      t: new Date(t0 + i*10000).toISOString(),
      cpu_pct: cpu,
      mem_used: Math.round(memTotal * mem),
      mem_total: memTotal,
      gpu: hasGpu,
      gpu_util_pct: gpu,
      gpu_mem_used: hasGpu ? Math.round(gpuMemTotal * (0.3 + (gpu/100)*0.55)) : 0,
      gpu_mem_total: gpuMemTotal,
    });
  }
  // Pin the latest sample to the current "now" values so the bars match the list
  const last = samples[samples.length-1];
  last.cpu_pct = cpuNow;
  last.gpu_util_pct = gpuNow;
  return samples;
}

function TelemetryPanel({ samples, hasGpu, stale, C }) {
  const last = samples[samples.length-1];
  const memPct = (last.mem_used / last.mem_total) * 100;
  const gpuMemPct = hasGpu && last.gpu_mem_total
    ? (last.gpu_mem_used / last.gpu_mem_total) * 100 : 0;
  const rows = [
    { label:'CPU',     value: last.cpu_pct,       series: samples.map(s=>s.cpu_pct), color: C.cCpu },
    { label:'MEM',     value: memPct,             series: samples.map(s=>(s.mem_used/s.mem_total)*100),
      color: C.cMem,
      detail: `${(last.mem_used/(1024**3)).toFixed(1)} / ${(last.mem_total/(1024**3)).toFixed(0)} GB` },
    ...(hasGpu ? [
      { label:'GPU',     value: last.gpu_util_pct,  series: samples.map(s=>s.gpu_util_pct), color: C.cGpu },
      { label:'GPU MEM', value: gpuMemPct,          series: samples.map(s=>s.gpu_mem_total?(s.gpu_mem_used/s.gpu_mem_total)*100:0),
        color: C.cGpuMem,
        detail: `${(last.gpu_mem_used/(1024**3)).toFixed(1)} / ${(last.gpu_mem_total/(1024**3)).toFixed(0)} GB` },
    ] : [
      { label:'GPU',     value: 0, series: samples.map(()=>0), color: C.cGpu, detail:'no gpu' },
    ]),
  ];
  return (
    <div style={{display:'flex',flexDirection:'column',gap:10,opacity: stale?0.65:1}}>
      {rows.map(r => (
        <div key={r.label} style={{display:'grid',gridTemplateColumns:'58px 1fr 70px 44px',gap:10,alignItems:'center'}}>
          <span style={{fontFamily:C.mono,fontSize:10,color: stale?C.fgMute:(r.color||C.fgMute),letterSpacing:'0.14em'}}>{r.label}</span>
          <span style={{position:'relative',height:4,background:'rgba(255,255,255,0.06)',borderRadius:2,overflow:'hidden'}}>
            <span style={{position:'absolute',inset:0,width:`${r.value}%`,
              background: stale ? hexToRgba(C.fgMute, 0.4) : (r.color || C.accent),
              borderRadius:2}}/>
          </span>
          <span style={{justifySelf:'end'}}>
            <Spark data={r.series} w={70} h={18} color={stale?C.fgMute:(r.color||C.accentB)}/>
          </span>
          <span style={{textAlign:'right',fontFamily:C.mono,fontSize:11,color: stale?C.fgMute:C.fg}}>
            {r.detail ? <span style={{fontSize:9.5,color:C.fgMute,letterSpacing:'0.04em'}}>{r.detail}</span> : `${r.value.toFixed(0)}%`}
          </span>
        </div>
      ))}
    </div>
  );
}

function HoloWorkerDetail({ C, D, workerName, onBack, onOpenTask }) {
  D = D || {pad:'18px 22px',gap:14,rowPad:'10px 18px'};
  const w = WORKERS_SAMPLE.find(x => x[0] === workerName) || WORKERS_SAMPLE[0];
  const [name, status, job, gpu, cpu, spec, uptime, offline, used, max, labels, lastSec] = w;
  // Local toggle for the Enable/Disable button. Initial value reflects the sample data.
  const [isDisabled, setIsDisabled] = useState(status === 'disabled');
  const stale = status.includes('stale');
  const sc = offline ? C.err : isDisabled ? C.fgMute : stale ? C.warn : status.includes('busy') ? C.accent : C.ok;
  const statusLabel = isDisabled
    ? 'disabled'
    : (status === 'disabled' ? 'idle' : status.split(' · ').pop());
  const isCuda = spec.startsWith('cuda');
  const vramGb = parseInt(spec.match(/(\d+)GB/)?.[1] || '24', 10);
  const ramGb = isCuda ? 128 : 64;
  const cores = isCuda ? 32 : 64;
  const gpuModel = isCuda ? `${vramGb >= 24 ? '2× RTX4090' : '1× RTX4080'}` : '— · no gpu';
  const hostname = `${name}.studio.dev`;

  // Derive task rows from the worker's running string so ids match what the
  // list view shows. Falls back to placeholder ids if the string is missing.
  let parsedTaskIds = [];
  let parsedParent = 'shot-042';
  if (job && job !== '—') {
    const dotIdx = job.indexOf(' · ');
    const head = dotIdx >= 0 ? job.slice(0, dotIdx) : job;
    parsedParent = dotIdx >= 0 ? job.slice(dotIdx + 3) : '—';
    parsedTaskIds = head.split(/,\s*/).filter(Boolean);
  }
  const fallbackIds = ['frame-004','frame-012','frame-018','frame-024'];
  const taskProgress = [68,22,41,55];
  const tasks = used > 0 ? Array.from({length: used}).map((_,i) => {
    const id = parsedTaskIds[i] || fallbackIds[i] || `task-${i+1}`;
    return [id, parsedParent, taskProgress[i] || 30];
  }) : [];

  const workspaces = [
    ['ws-a4f2','perforce','//depot/film-x/main','@CL 81234','2m ago','held'],
    ['ws-7c91','perforce','//depot/film-x/teaser','@CL 80991','3h ago','evict'],
    ['ws-1e0d','perforce','//depot/tools/main','@CL 80012','2d ago','evict'],
  ];

  return (
    <div style={{flex:1, padding:D.pad, display:'flex',flexDirection:'column',gap:D.gap, minHeight:0}}>
      {/* breadcrumb + header row */}
      <div style={{display:'flex',alignItems:'center',gap:10}}>
        <a onClick={onBack} style={{fontSize:12,color:C.fgMute,cursor:'pointer'}}>← Workers</a>
        <span style={{color:C.fgDim}}>/</span>
        <span style={{fontFamily:C.mono,fontSize:14,color:C.fg,letterSpacing:'0.04em'}}>{name}</span>
        {isDisabled && (
          <span style={{display:'inline-flex',alignItems:'center',gap:6,padding:'3px 10px',borderRadius:999,
            background:hexToRgba(sc,0.15),border:`1px solid ${sc}55`,
            fontFamily:C.mono,fontSize:10,letterSpacing:'0.16em',color:sc}}>
            <span style={{width:6,height:6,borderRadius:'50%',background:sc}}/>
            {statusLabel.toUpperCase()}
          </span>
        )}
        <span style={{marginLeft:'auto',display:'flex',gap:8}}>
          <button onClick={()=>setIsDisabled(v=>!v)} style={isDisabled
            ? {...pillBtn(C,'primary')}
            : {...pillBtn(C,'ghost'),
                background:hexToRgba(C.fgMute,0.12),border:`1px solid ${hexToRgba(C.fgMute,0.5)}`,color:C.fg}
          } title={isDisabled
            ? 'POST /v1/workers/:name/enable — resume scheduling'
            : 'POST /v1/workers/:name/disable — stop scheduling, keep heartbeat'}>
            {isDisabled ? 'Enable' : 'Disable'}
          </button>
          <button style={pillBtn(C,'ghost')}>Drain</button>
          <button style={pillBtn(C,'ghost')}>Edit labels</button>
          <button style={pillBtn(C,'ghost')}>Rename</button>
          <button style={{...pillBtn(C,'ghost'), background:'rgba(251,113,133,0.12)',border:`1px solid ${C.err}55`,color:C.err}}>Revoke token</button>
        </span>
      </div>

      <div style={{fontFamily:C.mono,fontSize:11,color:C.fgMute,letterSpacing:'0.06em'}}>
        id <span style={{color:C.fg}}>2bce…f9a1</span> · hostname <span style={{color:C.fg}}>{hostname}</span> · os linux · last seen <span style={{color: stale?C.warn:C.fg}}>{offline ? '14m ago' : stale ? `${lastSec}s ago · stale` : '0.3s ago'}</span>
      </div>

      {/* KPI row */}
      <div style={{display:'grid',gridTemplateColumns:'repeat(4,1fr)',gap:12}}>
        {[
          {label:'CPU · RAM', val:`${cores}c · ${ramGb}G`, sub:'os: linux'},
          {label:'GPU', val: gpuModel, sub:'nvidia-smi · cuda 12.3'},
          {label:'Slots', val:`${used} / ${max}`, sub:'PATCH max_slots', slots:true},
          {label:'Jobs today', val:'47', sub:'3 failed · avg 4m 12s'},
        ].map((k,i)=>(
          <div key={k.label} style={{...glassPanel(C), padding:'12px 14px',display:'flex',flexDirection:'column',gap:4}}>
            <div style={{fontFamily:C.mono,fontSize:10,letterSpacing:'0.16em',color:C.fgMute}}>{k.label.toUpperCase()}</div>
            <div style={{fontFamily:C.mono,fontSize:22,fontWeight:300,color:C.fg,letterSpacing:'-0.01em'}}>{k.val}</div>
            {k.slots && (
              <div style={{position:'relative',height:4,background:'rgba(255,255,255,0.08)',borderRadius:2,overflow:'hidden',margin:'2px 0 4px'}}>
                <span style={{position:'absolute',inset:0,width:`${(used/max)*100}%`,
                  background:`linear-gradient(90deg,${C.accent},${C.accentB})`,borderRadius:2}}/>
              </div>
            )}
            <div style={{fontFamily:C.mono,fontSize:10,color:C.fgMute,letterSpacing:'0.04em'}}>{k.sub}</div>
          </div>
        ))}
      </div>

      {/* Two-column body */}
      <div style={{flex:1,minHeight:0,display:'grid',gridTemplateColumns:'1fr 1fr',gap:12}}>
        {/* Left column */}
        <div style={{display:'flex',flexDirection:'column',gap:12,minHeight:0}}>
          <div style={{...glassPanel(C),display:'flex',flexDirection:'column'}}>
            <div style={{padding:'10px 16px',borderBottom:`1px solid ${C.border}`,display:'flex',justifyContent:'space-between',alignItems:'center'}}>
              <span style={{fontSize:13}}>Current tasks</span>
              <span style={{fontFamily:C.mono,fontSize:10,color:C.fgMute,letterSpacing:'0.14em'}}>{used} OF {max} SLOTS</span>
            </div>
            <div style={{padding:'8px 16px',display:'flex',flexDirection:'column',gap:4}}>
              {tasks.length === 0 ? (
                <div style={{fontFamily:C.mono,fontSize:11,color:C.fgDim,padding:'8px 0',letterSpacing:'0.04em'}}>
                  {offline ? 'worker offline — no tasks' : 'idle — no tasks assigned'}
                </div>
              ) : tasks.map(([id,parent,pct])=>(
                <div key={id}
                  onClick={()=>onOpenTask && onOpenTask(/^t-\w+$/i.test(id) ? id : null)}
                  style={{display:'grid',gridTemplateColumns:'90px 90px 1fr 40px',
                  alignItems:'center',gap:10,padding:'6px 0',fontFamily:C.mono,fontSize:11,
                  cursor:'pointer',
                  borderBottom:`1px solid ${C.border}`}}>
                  <span style={{color:C.accent,letterSpacing:'0.04em',textDecoration:'underline',textDecorationColor:hexToRgba(C.accent,0.35),textUnderlineOffset:2}}>{id}</span>
                  <span style={{color:C.fgMute}}>{parent}</span>
                  <span style={{position:'relative',height:3,background:'rgba(255,255,255,0.08)',borderRadius:2,overflow:'hidden'}}>
                    <span style={{position:'absolute',inset:0,width:`${pct}%`,
                      background:`linear-gradient(90deg,${C.accent},${C.accentB})`,borderRadius:2}}/>
                  </span>
                  <span style={{textAlign:'right',color:C.fg}}>{pct}%</span>
                </div>
              ))}
            </div>
          </div>

          <div style={{...glassPanel(C),display:'flex',flexDirection:'column',flex:1,minHeight:0}}>
            <div style={{padding:'10px 16px',borderBottom:`1px solid ${C.border}`,display:'flex',justifyContent:'space-between',alignItems:'center'}}>
              <span style={{fontSize:13}}>Source workspaces</span>
              <span style={{fontFamily:C.mono,fontSize:10,color:C.fgMute,letterSpacing:'0.14em'}}>/v1/workers/.../workspaces</span>
            </div>
            <div style={{flex:1,minHeight:0,overflow:'auto'}}>
              <div style={{display:'grid',gridTemplateColumns:'80px 70px 1fr 90px 70px 60px',
                fontFamily:C.mono,fontSize:9.5,letterSpacing:'0.14em',color:C.fgMute,
                padding:'10px 16px',borderBottom:`1px solid ${C.border}`}}>
                <span>SHORT_ID</span><span>TYPE</span><span>SOURCE_KEY</span><span>BASELINE</span><span>USED</span><span style={{textAlign:'right'}}>ACTION</span>
              </div>
              {workspaces.map(([sid,type,key,baseline,last,action])=>(
                <div key={sid} style={{display:'grid',gridTemplateColumns:'80px 70px 1fr 90px 70px 60px',
                  alignItems:'center',padding:'7px 16px',fontFamily:C.mono,fontSize:11,
                  borderBottom:`1px solid ${hexToRgba(C.accent,0.06)}`}}>
                  <span style={{color:C.fg,letterSpacing:'0.04em'}}>{sid}</span>
                  <span style={{color:C.fgMute}}>{type}</span>
                  <span style={{color:C.fg,overflow:'hidden',textOverflow:'ellipsis',whiteSpace:'nowrap'}}>{key}</span>
                  <span style={{color:C.fgMute}}>{baseline}</span>
                  <span style={{color:C.fgMute}}>{last}</span>
                  <span style={{textAlign:'right'}}>
                    {action === 'held' ? (
                      <span style={{padding:'2px 8px',borderRadius:999,fontSize:9.5,letterSpacing:'0.14em',
                        background:'rgba(255,255,255,0.04)',border:`1px solid ${C.border}`,color:C.fgMute}}>HELD</span>
                    ) : (
                      <span style={{padding:'2px 8px',borderRadius:999,fontSize:9.5,letterSpacing:'0.14em',cursor:'pointer',
                        background:hexToRgba(C.accent,0.12),border:`1px solid ${hexToRgba(C.accent,0.5)}`,color:C.accent}}>EVICT</span>
                    )}
                  </span>
                </div>
              ))}
            </div>
          </div>
        </div>

        {/* Right column */}
        <div style={{display:'flex',flexDirection:'column',gap:12,minHeight:0}}>
          <div style={{...glassPanel(C),padding:'14px 16px'}}>
            <div style={{fontSize:13,marginBottom:10,display:'flex',justifyContent:'space-between',alignItems:'center'}}>
              <span>Labels</span>
              <span style={{fontFamily:C.mono,fontSize:10,color:C.fgMute,letterSpacing:'0.14em'}}>PATCH /v1/workers</span>
            </div>
            <div style={{display:'flex',flexWrap:'wrap',gap:6}}>
              {['linux', isCuda?'gpu':'cpu', isCuda?'cuda:12.3':'avx512', `rack=${name.includes('east')?'B':'A'}`, name.split('-')[0]].map(l=>(
                <span key={l} style={{padding:'4px 10px',borderRadius:999,
                  background:hexToRgba(C.accent,0.1),border:`1px solid ${hexToRgba(C.accent,0.4)}`,
                  fontFamily:C.mono,fontSize:10.5,letterSpacing:'0.04em',color:C.accent}}>{l}</span>
              ))}
              <span style={{padding:'4px 10px',borderRadius:999,
                background:'transparent',border:`1px dashed ${C.border}`,
                fontFamily:C.mono,fontSize:10.5,letterSpacing:'0.04em',color:C.fgMute,cursor:'pointer'}}>+ add label</span>
            </div>
          </div>

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
              selectors are informational in v1 · only worker_ids are enforced.
            </div>
          </div>

          <div style={{...glassPanel(C),padding:'14px 16px',border:`1px solid ${hexToRgba(C.accent,0.4)}`}}>
            <div style={{fontSize:13,marginBottom:8,display:'flex',justifyContent:'space-between',alignItems:'center'}}>
              <span>Agent token</span>
              <span style={{fontFamily:C.mono,fontSize:10,color:C.fgMute,letterSpacing:'0.14em'}}>ROTATED 4D AGO</span>
            </div>
            <div style={{fontFamily:C.mono,fontSize:11,color:C.fg,padding:'8px 10px',borderRadius:6,
              background:'rgba(0,0,0,0.3)',border:`1px solid ${C.border}`,letterSpacing:'0.04em'}}>
              <span style={{color:C.fgMute}}>tok_</span>rly_2bce__••••••••••••<span style={{color:C.fgMute}}>__f9a1</span>
            </div>
            <div style={{fontFamily:C.mono,fontSize:10,color:C.fgDim,letterSpacing:'0.04em',marginTop:8,lineHeight:1.6}}>
              Long-lived agent token. Revoking forces the agent to exit and re-enroll with a fresh token.
            </div>
          </div>

          <div style={{...glassPanel(C),padding:'14px 16px',flex:1,minHeight:0,display:'flex',flexDirection:'column'}}>
            <div style={{display:'flex',justifyContent:'space-between',alignItems:'baseline',marginBottom:10}}>
              <span style={{fontSize:13}}>Utilization · last 30m</span>
              <span style={{fontFamily:C.mono,fontSize:10,color: stale?C.warn:C.fgMute,letterSpacing:'0.14em'}}>
                {offline ? 'NO DATA · OFFLINE' : stale ? `STALE · ${lastSec}S SINCE SAMPLE` : 'GET /v1/workers/{id}/metrics'}
              </span>
            </div>
            {offline ? (
              <div style={{flex:1,display:'grid',placeItems:'center',color:C.fgDim,
                fontFamily:C.mono,fontSize:11,letterSpacing:'0.06em'}}>
                samples: [] · agent disconnected
              </div>
            ) : (
              <TelemetryPanel
                samples={fakeTelemetry(name, gpu, cpu, ramGb, isCuda)}
                hasGpu={isCuda}
                stale={stale}
                C={C}
              />
            )}
            <div style={{marginTop:'auto',paddingTop:10,borderTop:`1px solid ${C.border}`,
              fontFamily:C.mono,fontSize:10,color:C.fgMute,letterSpacing:'0.06em',
              display:'flex',justifyContent:'space-between'}}>
              <span>
                {offline ? 'agent offline'
                  : stale ? <span style={{color:C.warn}}>⚠ telemetry stale (RELAY_TELEMETRY_STALE_AFTER=30s)</span>
                  : 'sampled 3s ago · every 10s'}
              </span>
              <span>up {uptime}</span>
            </div>
          </div>
        </div>
      </div>
    </div>
  );
}

// ── SCHEDULES ───────────────────────────────────────────────────────────────────────
// [name, cron, tz, overlap, enabled, next, last, lastJobId, lastJobStatus, owner]
const SCHEDULES = [
  ['nightly-render',  '0 2 * * *',     'America/Los_Angeles', 'skip',  true,  'tonight 02:00 PT',    'yesterday 02:00 PT', '9f4e1c', 'done',   'mira@studio.dev'],
  ['weekly-eval',     '@every 1h',     'UTC',                 'allow', true,  'in 24m',              '36m ago',            'a221fe', 'done',   'jin@studio.dev'],
  ['nightly-etl',     '0 1 * * *',     'America/Los_Angeles', 'skip',  true,  'tonight 01:00 PT',    'today 01:00 PT',     'a812ef', 'running','system'],
  ['db-backup',       '15 4 * * 1-5',  'Europe/London',       'skip',  true,  'tomorrow 04:15 BST',  'today 04:15 BST',    '3e08ca', 'done',   'system'],
  ['cache-warm',      '0 */4 * * *',   'UTC',                 'skip',  true,  'in 2h 14m',           '1h 46m ago',         'cc2401', 'done',   'system'],
  ['smoke-tests',     '@hourly',       'UTC',                 'skip',  false, '— (disabled)',         '3d ago',             '7c01b2', 'failed', 'ci-bot@studio.dev'],
  ['gpu-burnin',      '0 6 * * 0',     'America/Los_Angeles', 'allow', true,  'Sun 06:00 PT',        '7d ago',             '4422de', 'done',   'ada@studio.dev'],
  ['index-refresh',   '@every 30m',    'UTC',                 'skip',  true,  'in 12m',              '18m ago',            'b13ad2', 'done',   'jin@studio.dev'],
  ['retention-sweep', '30 3 * * *',    'UTC',                 'skip',  false, '— (disabled)',         '14d ago',            'd902a1', 'done',   'system'],
  ['lint-and-vet',    '@every 15m',    'UTC',                 'skip',  true,  'in 7m',               '8m ago',             'a91baa', 'failed', 'ci-bot@studio.dev'],
];
function HoloSchedules({ C, D, onOpenJob, onEdit }) {
  D = D || {pad:'22px 26px',gap:16,rowPad:'10px 18px'};
  const [filter, setFilter] = React.useState('all');
  const [sort, setSort] = React.useState('-created_at');
  const rows = SCHEDULES.filter(s => filter==='all' ? true : filter==='enabled' ? s[4] : !s[4]);
  // row: [name,cron,tz,overlap,enabled,next,last,jobId,jobStatus,owner]
  const SCHED_SORT = [
    { value:'-created_at',  label:'Newest' },
    { value:'created_at',   label:'Oldest' },
    { value:'name',         label:'Name A→Z' },
    { value:'-name',        label:'Name Z→A' },
    { value:'next_run_at',  label:'Next run soonest' },
    { value:'-next_run_at', label:'Next run latest' },
    { value:'-updated_at',  label:'Recently run' },
    { value:'updated_at',   label:'Least recently run' },
  ];
  const schedKeyMap = {
    created_at:  (s,i) => -i,            // sample order ≈ creation order
    name:        (s) => s[0].toLowerCase(),
    next_run_at: (s) => relMin(s[5]),    // 'in 7m' → +7 ; paused '—' → null
    updated_at:  (s) => relMin(s[6]),    // last run '8m ago' → −8
  };
  const sorted = applySort(rows, sort, schedKeyMap);
  const counts = {
    all: SCHEDULES.length,
    enabled: SCHEDULES.filter(s=>s[4]).length,
    disabled: SCHEDULES.filter(s=>!s[4]).length,
  };
  return (
    <div style={{flex:1, padding:D.pad, display:'flex',flexDirection:'column',gap:D.gap, minHeight:0}}>
      <div style={{display:'flex',alignItems:'flex-end',gap:24,flexWrap:'wrap'}}>
        <div>
          <div style={{fontFamily:C.mono,fontSize:11,letterSpacing:'0.18em',color:C.fgMute,marginBottom:4}}>RECURRING</div>
          <h1 style={{margin:0,fontSize:32,fontWeight:400,letterSpacing:'-0.02em'}}>Schedules</h1>
        </div>
        <div style={{display:'flex',gap:18,fontFamily:C.mono,fontSize:11,color:C.fgMute,letterSpacing:'0.14em'}}>
          <span><b style={{color:C.ok,fontWeight:600,fontSize:18}}>{counts.enabled}</b> ENABLED</span>
          <span><b style={{color:C.fg,fontWeight:600,fontSize:18}}>{counts.disabled}</b> PAUSED</span>
          <span><b style={{color:C.err,fontWeight:600,fontSize:18}}>2</b> FAILED · 24H</span>
        </div>
      </div>

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

      <div style={{...glassPanel(C), flex:1, minHeight:0, display:'flex',flexDirection:'column', overflow:'hidden'}}>
        <div style={{display:'grid',gridTemplateColumns:'1.2fr 120px 130px 80px 1.1fr 1.1fr 130px 130px 140px',
          fontFamily:C.mono,fontSize:10,letterSpacing:'0.16em',color:C.fgMute,
          padding:'12px 18px',borderBottom:`1px solid ${C.border}`}}>
          <span>NAME</span><span>CRON</span><span>TZ</span><span>OVERLAP</span><span>NEXT RUN</span><span>LAST RUN</span><span>LAST JOB</span><span>OWNER</span><span style={{textAlign:'right'}}>ACTIONS</span>
        </div>
        <div style={{flex:1,minHeight:0,overflow:'auto'}}>
          {sorted.map((s,i)=>{
            const [name,cron,tz,overlap,enabled,next,last,jobId,jobStatus,owner] = s;
            const jc = jobStatus==='done'?C.ok: jobStatus==='running'?C.accent: jobStatus==='failed'?C.err: C.fgMute;
            return (
              <div key={name} style={{
                display:'grid',gridTemplateColumns:'1.2fr 120px 130px 80px 1.1fr 1.1fr 130px 130px 140px',
                alignItems:'center', padding:D.rowPad,
                borderBottom:`1px solid ${hexToRgba(C.accent,0.06)}`,
                fontFamily:C.mono,fontSize:11.5,
                opacity: enabled?1:0.55,
              }}>
                <span style={{display:'flex',alignItems:'center',gap:8,minWidth:0}}>
                  <span style={{width:6,height:6,borderRadius:'50%',background:enabled?C.ok:C.fgDim}}/>
                  <span style={{color:C.fg,fontFamily:C.sans,fontSize:13,overflow:'hidden',textOverflow:'ellipsis',whiteSpace:'nowrap'}}>{name}</span>
                </span>
                <span style={{color:C.fg,letterSpacing:'0.04em'}}>{cron}</span>
                <span style={{color:C.fgMute,fontSize:10.5,letterSpacing:'0.04em',overflow:'hidden',textOverflow:'ellipsis',whiteSpace:'nowrap'}}>{tz}</span>
                <span>
                  <span style={{padding:'1px 8px',borderRadius:999,fontSize:9.5,letterSpacing:'0.14em',
                    background:'rgba(255,255,255,0.04)',border:`1px solid ${C.border}`,
                    color: overlap==='allow'?C.accent:C.fgMute,textTransform:'uppercase'}}>{overlap}</span>
                </span>
                <span style={{color:enabled?C.fg:C.fgDim,fontSize:11,letterSpacing:'0.02em'}}>
                  {enabled ? <span style={{color:C.accentB}}>▸</span> : null} {next}
                </span>
                <span style={{color:C.fgMute,fontSize:11,letterSpacing:'0.02em'}}>{last}</span>
                <span onClick={()=>onOpenJob && onOpenJob(jobId)} style={{
                  display:'inline-flex',alignItems:'center',gap:6,cursor:'pointer',
                  color:jc,letterSpacing:'0.04em',
                }}>
                  <span style={{width:6,height:6,borderRadius:'50%',background:jc}}/>
                  <span style={{textDecoration:'underline',textDecorationColor:hexToRgba(jc,0.35),textUnderlineOffset:2}}>{jobId}</span>
                </span>
                <span style={{color:C.fgMute,fontSize:10.5,letterSpacing:'0.02em',overflow:'hidden',textOverflow:'ellipsis',whiteSpace:'nowrap'}}>{owner}</span>
                <span style={{display:'flex',justifyContent:'flex-end',gap:6}}>
                  {enabled ? (
                    <button style={miniBtn(C, 'accent')}>Run now</button>
                  ) : (
                    <button style={miniBtn(C, 'ghost')}>Enable</button>
                  )}
                  <button onClick={()=>onEdit && onEdit(name)} style={miniBtn(C, 'ghost')}>Edit</button>
                </span>
              </div>
            );
          })}
        </div>
        <div style={{display:'flex',justifyContent:'space-between',alignItems:'center',
          padding:'10px 18px',borderTop:`1px solid ${C.border}`,
          fontFamily:C.mono,fontSize:10.5,letterSpacing:'0.08em',color:C.fgMute}}>
          <span>SHOWING <span style={{color:C.fg}}>1–{sorted.length}</span> OF <span style={{color:C.fg}}>{counts.all}</span> · SORT <span style={{color:C.accentB}}>{sort}</span> · OWNED + ADMINISTRATIVE</span>
          <div style={{display:'flex',gap:6}}>
            <button style={{...pillBtn(C,'ghost'),padding:'4px 12px',fontSize:11,opacity:0.5}}>← prev</button>
            <button style={{...pillBtn(C,'ghost'),padding:'4px 12px',fontSize:11}}>next 50 →</button>
          </div>
        </div>
      </div>

      <div style={{fontFamily:C.mono,fontSize:10,color:C.fgDim,letterSpacing:'0.06em',display:'flex',gap:14}}>
        <span>▸ <span style={{color:C.fgMute}}>Run now</span> is admin-only · submits a fresh job from the stored job_spec, attributed to the schedule's owner.</span>
      </div>
    </div>
  );
}
function miniBtn(C, kind) {
  const base = {padding:'4px 10px',borderRadius:6,fontFamily:C.sans,fontSize:11,cursor:'pointer',border:'none'};
  if (kind === 'accent') return {...base,
    background:`linear-gradient(90deg, ${hexToRgba(C.accent,0.22)}, ${hexToRgba(C.accentB,0.18)})`,
    border:`1px solid ${hexToRgba(C.accent,0.5)}`,
    color:C.fg, fontWeight:500,
  };
  return {...base, background:'rgba(255,255,255,0.04)',border:`1px solid ${C.border}`,color:C.fgMute};
}

// ── SCHEDULE DETAIL ────────────────────────────────────────────────────────
// Stored at /v1/schedules/:name. Inline-edit cron / tz / overlap / spec.
// Recent run history is just last N triggered jobs filtered by source=schedule.
function HoloScheduleDetail({ C, D, scheduleName, onBack, onOpenJob }) {
  D = D || {pad:'18px 22px',gap:14,rowPad:'10px 18px'};
  const s = SCHEDULES.find(x => x[0] === scheduleName) || SCHEDULES[0];
  const [name, cron, tz, overlap, enabled0, next, last, lastJobId, lastJobStatus, owner] = s;

  // Local edit state
  const [isEnabled, setIsEnabled] = useState(enabled0);
  const [cronExpr, setCronExpr] = useState(cron);
  const [tzVal, setTzVal] = useState(tz);
  const [overlapVal, setOverlapVal] = useState(overlap);

  const sc = !isEnabled ? C.fgDim : lastJobStatus==='failed' ? C.err : lastJobStatus==='running' ? C.accent : C.ok;

  // Sample recent runs — deterministic-ish from schedule name
  const RUNS = [
    [last,            '22m 04s', lastJobStatus,                          lastJobId, owner],
    ['yesterday',     '24m 12s', lastJobStatus==='failed'?'done':'done', '9f4d22',  owner],
    ['2d ago',        '21m 48s', 'done',                                 '9f4c01',  owner],
    ['3d ago',        '4m 02s',  'failed',                               '9f4b91',  owner],
    ['4d ago',        '22m 36s', 'done',                                 '9f4a55',  owner],
    ['5d ago',        '23m 12s', 'done',                                 '9f48ee',  owner],
    ['6d ago',        '21m 56s', 'done',                                 '9f47a2',  owner],
    ['7d ago',        '22m 20s', 'done',                                 '9f4621',  owner],
  ];
  const NEXT_FIRES = [
    [next, 'next run'],
    ['+1 day',  'after that'],
    ['+2 days', ''],
    ['+3 days', ''],
    ['+4 days', ''],
  ];

  // Stub job spec — what the schedule submits on each tick.
  const SPEC = `name: ${name}
priority: normal
schedule:
  cron: "${cronExpr}"
  tz:   "${tzVal}"
  overlap: ${overlapVal}
tasks:
  count: 1
  command: ["./run-${name}.sh"]
  resources:
    cpu: 4
    mem: 8Gi
labels:
  source: schedule
  schedule_name: ${name}`;

  const isReadonly = false;
  const inputBase = {
    width:'100%',background:'rgba(0,0,0,0.3)',border:`1px solid ${C.border}`,
    padding:'9px 12px',borderRadius:6,color:C.fg,fontFamily:C.mono,fontSize:12.5,outline:'none',
  };

  return (
    <div style={{flex:1, padding:D.pad, display:'flex',flexDirection:'column',gap:D.gap, minHeight:0}}>
      {/* breadcrumb + header */}
      <div style={{display:'flex',alignItems:'center',gap:10,flexWrap:'wrap'}}>
        <a onClick={onBack} style={{fontSize:12,color:C.fgMute,cursor:'pointer'}}>← Schedules</a>
        <span style={{color:C.fgDim}}>/</span>
        <span style={{fontFamily:C.mono,fontSize:14,color:C.fg,letterSpacing:'0.04em'}}>{name}</span>
        <span style={{display:'inline-flex',alignItems:'center',gap:6,padding:'3px 10px',borderRadius:999,
          background:hexToRgba(sc,0.15),border:`1px solid ${sc}55`,
          fontFamily:C.mono,fontSize:10,letterSpacing:'0.16em',color:sc}}>
          <span style={{width:6,height:6,borderRadius:'50%',background:sc}}/>
          {isEnabled ? 'ENABLED' : 'PAUSED'}
        </span>
        <span style={{marginLeft:'auto',display:'flex',gap:8}}>
          <button style={pillBtn(C,'primary')}>Run now</button>
          <button onClick={()=>setIsEnabled(v=>!v)} style={isEnabled
            ? {...pillBtn(C,'ghost'), background:hexToRgba(C.warn,0.12),border:`1px solid ${hexToRgba(C.warn,0.5)}`,color:C.warn}
            : {...pillBtn(C,'ghost'), background:hexToRgba(C.ok,0.12),border:`1px solid ${hexToRgba(C.ok,0.5)}`,color:C.ok}
          } title={isEnabled
            ? 'POST /v1/schedules/:name/pause'
            : 'POST /v1/schedules/:name/resume'}>
            {isEnabled ? 'Pause' : 'Resume'}
          </button>
          <button style={{...pillBtn(C,'ghost'), background:'rgba(251,113,133,0.12)',border:`1px solid ${C.err}55`,color:C.err}}>Delete</button>
        </span>
      </div>

      <div style={{fontFamily:C.mono,fontSize:11,color:C.fgMute,letterSpacing:'0.06em',display:'flex',gap:18,flexWrap:'wrap'}}>
        <span>owner <span style={{color:C.fg}}>{owner}</span></span>
        <span>created <span style={{color:C.fg}}>2025-08-14</span></span>
        <span>updated <span style={{color:C.fg}}>4d ago</span></span>
        <span>fires <span style={{color:C.fg}}>{isEnabled ? next : '—'}</span></span>
      </div>

      {/* body grid */}
      <div style={{flex:1,minHeight:0,display:'grid',gridTemplateColumns:'1.2fr 1fr',gap:14}}>
        {/* Left column: editable spec + job spec */}
        <div style={{display:'flex',flexDirection:'column',gap:14,minHeight:0}}>
          <div style={{...glassPanel(C),padding:'16px 20px',display:'flex',flexDirection:'column',gap:14}}>
            <div style={{display:'flex',justifyContent:'space-between',alignItems:'baseline'}}>
              <span style={{fontSize:13,color:C.fg}}>Trigger</span>
              <span style={{fontFamily:C.mono,fontSize:10,letterSpacing:'0.06em',color:C.fgDim}}>PATCH /v1/schedules/{name}</span>
            </div>

            <div>
              <div style={{display:'flex',justifyContent:'space-between',marginBottom:6}}>
                <span style={{fontFamily:C.mono,fontSize:10,letterSpacing:'0.16em',color:C.fgMute}}>CRON</span>
                <a style={{fontFamily:C.mono,fontSize:10,letterSpacing:'0.04em',color:C.accentB,cursor:'pointer'}}>crontab.guru ↗</a>
              </div>
              <input value={cronExpr} onChange={e=>setCronExpr(e.target.value)} disabled={isReadonly} style={inputBase}/>
              <div style={{fontFamily:C.mono,fontSize:10.5,color:C.fgDim,marginTop:4,letterSpacing:'0.04em'}}>
                ▸ {explainCron(cronExpr)}
              </div>
            </div>

            <div style={{display:'grid',gridTemplateColumns:'1fr 1fr',gap:12}}>
              <div>
                <div style={{fontFamily:C.mono,fontSize:10,letterSpacing:'0.16em',color:C.fgMute,marginBottom:6}}>TIMEZONE</div>
                <select value={tzVal} onChange={e=>setTzVal(e.target.value)} style={{...inputBase,paddingRight:30}}>
                  {['UTC','America/Los_Angeles','America/New_York','Europe/London','Europe/Berlin','Asia/Tokyo'].map(z=>
                    <option key={z} value={z}>{z}</option>)}
                </select>
              </div>
              <div>
                <div style={{fontFamily:C.mono,fontSize:10,letterSpacing:'0.16em',color:C.fgMute,marginBottom:6}}>OVERLAP</div>
                <div style={{display:'flex',gap:6}}>
                  {['skip','allow','queue'].map(opt=>(
                    <button key={opt} onClick={()=>setOverlapVal(opt)} style={{
                      flex:1,padding:'9px 8px',borderRadius:6,fontFamily:C.mono,fontSize:11,cursor:'pointer',letterSpacing:'0.04em',
                      background: overlapVal===opt
                        ? `linear-gradient(90deg, ${hexToRgba(C.accent,0.25)}, ${hexToRgba(C.accentB,0.18)})`
                        : 'rgba(0,0,0,0.3)',
                      border:`1px solid ${overlapVal===opt ? C.accent+'66' : C.border}`,
                      color: overlapVal===opt ? C.fg : C.fgMute,
                    }}>{opt}</button>
                  ))}
                </div>
              </div>
            </div>

            <div style={{display:'flex',gap:8,marginTop:4}}>
              <button style={pillBtn(C,'primary')}>Save changes</button>
              <button style={pillBtn(C,'ghost')}>Cancel</button>
            </div>
          </div>

          {/* Job spec */}
          <div style={{...glassPanel(C),flex:1,minHeight:0,display:'flex',flexDirection:'column'}}>
            <div style={{padding:'12px 18px',borderBottom:`1px solid ${C.border}`,display:'flex',justifyContent:'space-between',alignItems:'center'}}>
              <span style={{fontSize:13,color:C.fg}}>Job spec</span>
              <span style={{display:'flex',gap:8,alignItems:'center'}}>
                <span style={{fontFamily:C.mono,fontSize:10,letterSpacing:'0.06em',color:C.fgDim}}>YAML · submitted per tick</span>
                <button style={{...miniBtn(C,'ghost')}}>Edit</button>
              </span>
            </div>
            <pre style={{margin:0,padding:'14px 18px',fontFamily:C.mono,fontSize:12,lineHeight:1.6,
              color:C.fg,whiteSpace:'pre',overflow:'auto',background:'rgba(0,0,0,0.25)',flex:1}}>{SPEC}</pre>
          </div>
        </div>

        {/* Right column: runs + next fires */}
        <div style={{display:'flex',flexDirection:'column',gap:14,minHeight:0}}>
          <div style={{...glassPanel(C),padding:'14px 18px'}}>
            <div style={{display:'flex',justifyContent:'space-between',alignItems:'baseline',marginBottom:10}}>
              <span style={{fontSize:13,color:C.fg}}>Next fires</span>
              <span style={{fontFamily:C.mono,fontSize:10,letterSpacing:'0.06em',color:C.fgDim}}>{isEnabled ? `tz: ${tzVal}` : 'paused — no fires queued'}</span>
            </div>
            <div style={{display:'flex',flexDirection:'column',gap:4}}>
              {NEXT_FIRES.map(([when,note],i)=>(
                <div key={i} style={{display:'flex',justifyContent:'space-between',alignItems:'center',
                  padding:'7px 10px',borderRadius:6,
                  background: i===0 ? hexToRgba(C.accent,0.08) : 'rgba(255,255,255,0.02)',
                  border:`1px solid ${i===0 ? hexToRgba(C.accent,0.35) : C.border}`,
                  opacity: isEnabled?1:0.45}}>
                  <span style={{fontFamily:C.mono,fontSize:11.5,color:i===0?C.fg:C.fgMute,letterSpacing:'0.04em'}}>
                    {i===0 && <span style={{color:C.accentB,marginRight:6}}>▸</span>}
                    {when}
                  </span>
                  <span style={{fontFamily:C.mono,fontSize:10,letterSpacing:'0.06em',color:C.fgDim}}>{note}</span>
                </div>
              ))}
            </div>
          </div>

          <div style={{...glassPanel(C),flex:1,minHeight:0,display:'flex',flexDirection:'column'}}>
            <div style={{padding:'12px 18px',borderBottom:`1px solid ${C.border}`,display:'flex',justifyContent:'space-between',alignItems:'center'}}>
              <span style={{fontSize:13,color:C.fg}}>Recent runs</span>
              <span style={{fontFamily:C.mono,fontSize:10,letterSpacing:'0.06em',color:C.fgDim}}>
                GET /v1/jobs?source=schedule&name={name}
              </span>
            </div>
            <div style={{display:'grid',gridTemplateColumns:'1.3fr 90px 90px 110px 1fr',
              fontFamily:C.mono,fontSize:10,letterSpacing:'0.16em',color:C.fgMute,
              padding:'10px 18px',borderBottom:`1px solid ${C.border}`}}>
              <span>STARTED</span><span>DURATION</span><span>STATUS</span><span>JOB ID</span><span>OWNER</span>
            </div>
            <div style={{flex:1,minHeight:0,overflow:'auto'}}>
              {RUNS.map((r,i)=>{
                const [when,dur,st,jid,by] = r;
                const jc = st==='done'?C.ok: st==='failed'?C.err: st==='running'?C.accent: C.fgMute;
                return (
                  <div key={i} style={{display:'grid',gridTemplateColumns:'1.3fr 90px 90px 110px 1fr',
                    alignItems:'center',padding:'9px 18px',
                    borderBottom:`1px solid ${hexToRgba(C.accent,0.06)}`,
                    fontFamily:C.mono,fontSize:11.5}}>
                    <span style={{color:C.fg,letterSpacing:'0.02em'}}>{when}</span>
                    <span style={{color:C.fgMute,fontSize:11}}>{dur}</span>
                    <span style={{display:'inline-flex',alignItems:'center',gap:6,color:jc,fontSize:10.5,letterSpacing:'0.06em'}}>
                      <span style={{width:6,height:6,borderRadius:'50%',background:jc}}/>{st}
                    </span>
                    <span onClick={()=>onOpenJob && onOpenJob(jid)} style={{
                      color:jc,letterSpacing:'0.04em',cursor:'pointer',
                      textDecoration:'underline',textDecorationColor:hexToRgba(jc,0.35),textUnderlineOffset:2,
                    }}>{jid}</span>
                    <span style={{color:C.fgMute,fontSize:10.5,overflow:'hidden',textOverflow:'ellipsis',whiteSpace:'nowrap'}}>{by}</span>
                  </div>
                );
              })}
            </div>
          </div>
        </div>
      </div>
    </div>
  );
}
// Tiny human-readable cron hint. Best-effort, not exhaustive.
function explainCron(expr) {
  if (!expr) return '';
  if (expr.startsWith('@every '))  return `every ${expr.slice(7)}`;
  if (expr === '@hourly')          return 'at minute 0 of every hour';
  if (expr === '@daily')           return 'at 00:00 every day';
  if (expr === '@weekly')          return 'at 00:00 every Sunday';
  const parts = expr.split(/\s+/);
  if (parts.length !== 5) return 'custom cron expression';
  const [m, h, dom, mon, dow] = parts;
  const t = (m === '0' && /^\d+$/.test(h)) ? `${h.padStart(2,'0')}:00` : null;
  if (t && dom === '*' && mon === '*' && dow === '*') return `every day at ${t}`;
  if (t && dom === '*' && mon === '*' && /^\d+-\d+$/.test(dow)) return `weekdays at ${t}`;
  if (t && /^\d+$/.test(dom) && mon === '*' && dow === '*') return `at ${t} on day ${dom} of every month`;
  if (t && dom === '*' && mon === '*' && /^\d+$/.test(dow)) return `at ${t} on ${['Sun','Mon','Tue','Wed','Thu','Fri','Sat'][parseInt(dow,10)]||'?'}`;
  if (m === '0' && /^\*\/\d+$/.test(h)) return `every ${h.slice(2)}h on the hour`;
  return 'custom cron expression';
}

// ── ADMIN ───────────────────────────────────────────────────────────────────
// Mirrors every admin surface in chadmv/relay@master:
//   • Users           — GET/POST /v1/users · PATCH · POST archive/unarchive · POST password-reset
//   • Invites         — POST /v1/invites (email optional, 72h default, max 720h)
//   • Agent enrolls   — POST /v1/agent-enrollments (hostname_hint, ttl; default 24h, max 7d)
//   • Reservations    — admin-only /v1/reservations
//   • Server          — env config (CORS, rate limits, telemetry, bootstrap admin, self-register)
const USERS = [
  // [email, name, role, created, sessions, lastLogin, archived]
  ['mira@studio.dev',    'Mira K.',     'admin',   '2024-09-12', 3, '17m ago',  false],
  ['ada@studio.dev',     'Ada L.',      'member',  '2025-02-04', 2, '1h ago',   false],
  ['jin@studio.dev',     'Jin O.',      'member',  '2025-06-21', 1, '3h ago',   false],
  ['ci-bot@studio.dev',  'CI Bot',      'service', '2025-08-30', 1, '—',        false],
  ['ben@studio.dev',     'Ben R.',      'member',  '2026-04-15', 0, 'never',    false],
  ['old@studio.dev',     'Former User', 'member',  '2024-03-01', 0, '4mo ago',  true ],
];

const INVITES = [
  // [tokenShort, email, expiresIn, createdBy, status]
  ['rly_inv_ab32…', 'newhire@studio.dev', '68h',     'mira@studio.dev', 'active'],
  ['rly_inv_7d91…', '— (open)',           '3d 14h',  'mira@studio.dev', 'active'],
  ['rly_inv_c402…', 'partner@vendor.io',  'expired', 'mira@studio.dev', 'expired'],
  ['rly_inv_88a1…', 'ben@studio.dev',     'redeemed','mira@studio.dev', 'redeemed'],
];

const ENROLLMENTS = [
  // [tokenShort, hostnameHint, expiresIn, createdBy, status]
  ['rly_ae_91ad…', 'farm-west-13', '21h 42m', 'mira@studio.dev', 'active'],
  ['rly_ae_22f0…', '—',            '6d 03h',  'mira@studio.dev', 'active'],
  ['rly_ae_4e12…', 'farm-east-06', '< 1h',    'mira@studio.dev', 'expiring'],
];

const RESERVATIONS = [
  // [name, project, workerIds, selector, starts, ends]
  ['vfx-sprint',   'film-x',  ['2bce…','7c91…'], 'rack=A · gpu',  'May 01 09:00', 'May 14 18:00'],
  ['ml-bench',     'ml-eval', ['1e0d…'],         '—',             'May 12 14:00', 'May 15 02:00'],
  ['ci-pinned',    'ci',      ['a4f2…','8b30…','6c42…'], '—',     '— · open',     '— · open'],
];

function HoloAdmin({ C, D }) {
  D = D || {pad:'22px 26px',gap:14,rowPad:'10px 18px'};
  const [tab, setTab] = useState('users');
  const [showArchived, setShowArchived] = useState(false);
  const [modal, setModal] = useState(null); // 'invite' | 'enroll' | null

  const TABS = [
    ['users',       'Users',          USERS.filter(u=>showArchived||!u[6]).length],
    ['invites',     'Invites',        INVITES.filter(i=>i[4]==='active').length],
    ['enrollments', 'Agent enrolls',  ENROLLMENTS.length],
    ['reservations','Reservations',   RESERVATIONS.length],
    ['server',      'Server',         null],
  ];

  return (
    <div style={{flex:1, padding:D.pad, display:'flex',flexDirection:'column',gap:D.gap, minHeight:0}}>
      {/* Header */}
      <div style={{display:'flex',alignItems:'flex-end',gap:24,flexWrap:'wrap'}}>
        <div>
          <div style={{fontFamily:C.mono,fontSize:11,letterSpacing:'0.18em',color:C.fgMute,marginBottom:4}}>SETTINGS · ADMIN ONLY</div>
          <h1 style={{margin:0,fontSize:32,fontWeight:400,letterSpacing:'-0.02em'}}>Admin</h1>
        </div>
        <div style={{marginLeft:'auto',display:'flex',gap:14,fontFamily:C.mono,fontSize:11,color:C.fgMute,letterSpacing:'0.06em'}}>
          {[
            ['VERSION',  'relay-server 2.4.1'],
            ['BUILD',    'd03b7efc · go1.22'],
            ['DB',       'postgres 16 · 12c'],
            ['UPTIME',   '7d 14h'],
          ].map(([k,v])=>(
            <div key={k} style={{display:'flex',flexDirection:'column',alignItems:'flex-end',gap:1}}>
              <span style={{fontSize:9,letterSpacing:'0.16em'}}>{k}</span>
              <span style={{color:C.fg,fontSize:12}}>{v}</span>
            </div>
          ))}
        </div>
      </div>

      {/* Tabs */}
      <div style={{display:'flex',gap:6,padding:3,borderRadius:999,alignSelf:'flex-start',
        background:'rgba(0,0,0,0.3)',border:`1px solid ${C.border}`,backdropFilter:'blur(8px)'}}>
        {TABS.map(([k,n,count])=>(
          <button key={k} onClick={()=>setTab(k)} style={{
            padding:'6px 14px', borderRadius:999, border:'none', cursor:'pointer',
            fontFamily:C.sans, fontSize:12, letterSpacing:'0.02em',
            display:'flex',alignItems:'center',gap:6,
            background: tab===k?`linear-gradient(90deg, ${C.accent}, ${C.accentB})`:'transparent',
            color: tab===k?'#fff':C.fgMute,
            fontWeight: tab===k?600:400,
          }}>
            {n}
            {count!=null && <span style={{
              fontFamily:C.mono,fontSize:10,padding:'1px 7px',borderRadius:999,
              background: tab===k?'rgba(0,0,0,0.25)':'rgba(255,255,255,0.05)',
              color: tab===k?'#fff':C.fgMute, letterSpacing:'0.04em',
            }}>{count}</span>}
          </button>
        ))}
      </div>

      {/* Tab body */}
      <div style={{flex:1,minHeight:0,display:'flex',flexDirection:'column',gap:12,minHeight:0}}>
        {tab === 'users' && <AdminUsers C={C} D={D} showArchived={showArchived} setShowArchived={setShowArchived}/>}
        {tab === 'invites' && <AdminInvites C={C} D={D} onNew={()=>setModal('invite')}/>}
        {tab === 'enrollments' && <AdminEnrollments C={C} D={D} onNew={()=>setModal('enroll')}/>}
        {tab === 'reservations' && <AdminReservations C={C} D={D}/>}
        {tab === 'server' && <AdminServer C={C}/>}
      </div>

      {modal && <AdminTokenModal C={C} kind={modal} onClose={()=>setModal(null)}/>}
    </div>
  );
}

function AdminUsers({ C, D, showArchived, setShowArchived }) {
  const rows = USERS.filter(u => showArchived || !u[6]);
  return (
    <>
      <div style={{display:'flex',gap:10,alignItems:'center'}}>
        <span style={{fontSize:13,color:C.fgMute,fontFamily:C.mono,letterSpacing:'0.06em'}}>GET /v1/users</span>
        <label style={{display:'flex',alignItems:'center',gap:6,fontSize:12,color:C.fgMute,cursor:'pointer',marginLeft:14}}>
          <input type="checkbox" checked={showArchived} onChange={e=>setShowArchived(e.target.checked)}
            style={{accentColor:C.accent}}/>
          include archived <span style={{color:C.fgDim,fontFamily:C.mono,fontSize:11}}>?include_archived=true</span>
        </label>
        <input placeholder="?email=… exact match" style={{
          marginLeft:'auto', minWidth:240, padding:'7px 14px',borderRadius:999,
          background:'rgba(0,0,0,0.25)',border:`1px solid ${C.border}`,
          color:C.fg,fontFamily:C.sans,fontSize:12,outline:'none',
        }}/>
        <button style={pillBtn(C,'primary')}>+ Create user</button>
      </div>

      <div style={{...glassPanel(C), flex:1, minHeight:0, display:'flex',flexDirection:'column', overflow:'hidden'}}>
        <div style={{display:'grid',gridTemplateColumns:'1.4fr 1fr 110px 100px 110px 100px 220px',
          fontFamily:C.mono,fontSize:10,letterSpacing:'0.16em',color:C.fgMute,
          padding:'12px 18px',borderBottom:`1px solid ${C.border}`}}>
          <span>EMAIL</span><span>NAME</span><span>ROLE</span><span>CREATED</span><span>SESSIONS</span><span>LAST LOGIN</span><span style={{textAlign:'right'}}>ACTIONS</span>
        </div>
        <div style={{flex:1,minHeight:0,overflow:'auto'}}>
          {rows.map((u,i)=>{
            const [email,name,role,created,sessions,last,archived] = u;
            const roleColor = role==='admin'?C.accent: role==='service'?C.accentB: C.fgMute;
            return (
              <div key={email} style={{
                display:'grid',gridTemplateColumns:'1.4fr 1fr 110px 100px 110px 100px 220px',
                alignItems:'center',padding:D.rowPad,
                borderBottom:`1px solid ${hexToRgba(C.accent,0.06)}`,
                fontFamily:C.mono,fontSize:11.5,
                opacity: archived?0.55:1,
              }}>
                <span style={{display:'flex',alignItems:'center',gap:10,minWidth:0}}>
                  <span style={{width:24,height:24,borderRadius:6,flex:'none',
                    background:`linear-gradient(135deg, ${hexToRgba(C.accent,0.45)}, ${hexToRgba(C.accentB,0.28)})`,
                    display:'grid',placeItems:'center',color:'#fff',fontWeight:600,fontSize:11}}>{email[0].toUpperCase()}</span>
                  <span style={{color:C.fg,fontFamily:C.sans,fontSize:12.5,overflow:'hidden',textOverflow:'ellipsis',whiteSpace:'nowrap'}}>{email}</span>
                </span>
                <span style={{color:C.fgMute,fontSize:12,fontFamily:C.sans,overflow:'hidden',textOverflow:'ellipsis',whiteSpace:'nowrap'}}>{name}</span>
                <span>
                  <span style={{padding:'1px 8px',borderRadius:999,fontSize:9.5,letterSpacing:'0.14em',
                    background:hexToRgba(roleColor,0.12),border:`1px solid ${hexToRgba(roleColor,0.5)}`,
                    color:roleColor,textTransform:'uppercase'}}>{role}</span>
                </span>
                <span style={{color:C.fgMute,fontSize:10.5}}>{created}</span>
                <span style={{color:sessions>0?C.fg:C.fgDim,fontSize:10.5}}>{sessions} active</span>
                <span style={{color:C.fgMute,fontSize:10.5}}>{last}</span>
                <span style={{display:'flex',justifyContent:'flex-end',gap:6}}>
                  {archived ? (
                    <button style={miniBtn(C,'accent')}>Unarchive</button>
                  ) : (
                    <>
                      <button style={miniBtn(C,'ghost')}>Reset pw</button>
                      <button style={miniBtn(C,'ghost')}>Rename</button>
                      <button style={{...miniBtn(C,'ghost'),
                        background:'rgba(251,113,133,0.1)',border:`1px solid ${hexToRgba(C.err,0.4)}`,color:C.err}}>Archive</button>
                    </>
                  )}
                </span>
              </div>
            );
          })}
        </div>
        <PageFooter C={C} count={rows.length} total={USERS.length} endpoint="/v1/users"/>
      </div>

      <div style={{fontFamily:C.mono,fontSize:10,color:C.fgDim,letterSpacing:'0.04em',lineHeight:1.7}}>
        ▸ <span style={{color:C.fgMute}}>Archive</span> revokes all of the target's API tokens · forces re-login.
        Server guards prevent archiving yourself or the last remaining admin.
        Password reset revokes the target's sessions too.
      </div>
    </>
  );
}

function AdminInvites({ C, D, onNew }) {
  return (
    <>
      <div style={{display:'flex',gap:10,alignItems:'center'}}>
        <span style={{fontSize:13,color:C.fgMute,fontFamily:C.mono,letterSpacing:'0.06em'}}>POST /v1/invites · default 72h · max 720h</span>
        <span style={{marginLeft:'auto'}}/>
        <button onClick={onNew} style={pillBtn(C,'primary')}>+ Create invite</button>
      </div>

      <div style={{...glassPanel(C),flex:1,minHeight:0,display:'flex',flexDirection:'column',overflow:'hidden'}}>
        <div style={{display:'grid',gridTemplateColumns:'1.4fr 1.4fr 110px 1fr 100px 120px',
          fontFamily:C.mono,fontSize:10,letterSpacing:'0.16em',color:C.fgMute,
          padding:'12px 18px',borderBottom:`1px solid ${C.border}`}}>
          <span>TOKEN PREFIX</span><span>BINDS TO</span><span>EXPIRES</span><span>CREATED BY</span><span>STATUS</span><span style={{textAlign:'right'}}>ACTIONS</span>
        </div>
        <div style={{flex:1,minHeight:0,overflow:'auto'}}>
          {INVITES.map((inv,i)=>{
            const [token,email,exp,by,status] = inv;
            const sc = status==='active'?C.ok: status==='expiring'?C.warn: status==='expired'?C.err: C.fgMute;
            return (
              <div key={token+i} style={{
                display:'grid',gridTemplateColumns:'1.4fr 1.4fr 110px 1fr 100px 120px',
                alignItems:'center',padding:D.rowPad,
                borderBottom:`1px solid ${hexToRgba(C.accent,0.06)}`,
                fontFamily:C.mono,fontSize:11.5,
                opacity: status==='redeemed'||status==='expired'?0.55:1,
              }}>
                <span style={{color:C.fg,letterSpacing:'0.04em',overflow:'hidden',textOverflow:'ellipsis',whiteSpace:'nowrap'}}>{token}</span>
                <span style={{color:email.startsWith('—')?C.fgDim:C.fg,fontSize:11}}>{email}</span>
                <span style={{color:status==='active'?C.fg:C.fgMute,fontSize:11}}>{exp}</span>
                <span style={{color:C.fgMute,fontSize:11,overflow:'hidden',textOverflow:'ellipsis',whiteSpace:'nowrap'}}>{by}</span>
                <span>
                  <span style={{padding:'1px 8px',borderRadius:999,fontSize:9.5,letterSpacing:'0.14em',
                    background:hexToRgba(sc,0.12),border:`1px solid ${hexToRgba(sc,0.5)}`,
                    color:sc,textTransform:'uppercase'}}>{status}</span>
                </span>
                <span style={{textAlign:'right',color:C.fgDim,fontSize:10.5,letterSpacing:'0.04em'}}>
                  {status==='active' ? <span>copy token only on creation</span> : <span>—</span>}
                </span>
              </div>
            );
          })}
        </div>
        <PageFooter C={C} count={INVITES.length} total={INVITES.length} endpoint="/v1/invites"/>
      </div>

      <div style={{fontFamily:C.mono,fontSize:10,color:C.fgDim,letterSpacing:'0.04em',lineHeight:1.7}}>
        ▸ Invites are <b style={{color:C.fgMute}}>one-time</b>. Server returns the raw token only at creation — there's no revoke endpoint in v1; expiry or redemption are the only terminal states.
        Email binding pins the invite to one address.
      </div>
    </>
  );
}

function AdminEnrollments({ C, D, onNew }) {
  const [sort, setSort] = useState('-created_at');
  // row: [token,hint,exp,by,status]
  const ENROLL_SORT = [
    { value:'-created_at', label:'Newest' },
    { value:'created_at',  label:'Oldest' },
    { value:'expires_at',  label:'Expires soonest' },
    { value:'-expires_at', label:'Expires last' },
  ];
  const enrollKeyMap = {
    created_at: (e,i) => -i,
    expires_at: (e) => relMin(e[2]),   // '21h 42m' → +1302
  };
  const sorted = applySort(ENROLLMENTS, sort, enrollKeyMap);
  return (
    <>
      <div style={{display:'flex',gap:10,alignItems:'center'}}>
        <span style={{fontSize:13,color:C.fgMute,fontFamily:C.mono,letterSpacing:'0.06em'}}>POST /v1/agent-enrollments · default 24h · max 7d</span>
        <span style={{marginLeft:'auto'}}/>
        <SortControl C={C} options={ENROLL_SORT} value={sort} onChange={setSort}/>
        <button onClick={onNew} style={pillBtn(C,'primary')}>+ Enroll agent</button>
      </div>

      <div style={{...glassPanel(C),flex:1,minHeight:0,display:'flex',flexDirection:'column',overflow:'hidden'}}>
        <div style={{display:'grid',gridTemplateColumns:'1.4fr 1.2fr 110px 1fr 110px 120px',
          fontFamily:C.mono,fontSize:10,letterSpacing:'0.16em',color:C.fgMute,
          padding:'12px 18px',borderBottom:`1px solid ${C.border}`}}>
          <span>TOKEN PREFIX</span><span>HOSTNAME HINT</span><span>EXPIRES</span><span>CREATED BY</span><span>STATUS</span><span style={{textAlign:'right'}}>ACTIONS</span>
        </div>
        <div style={{flex:1,minHeight:0,overflow:'auto'}}>
          {sorted.map((en,i)=>{
            const [token,hint,exp,by,status] = en;
            const sc = status==='active'?C.ok: status==='expiring'?C.warn: C.fgMute;
            return (
              <div key={token+i} style={{
                display:'grid',gridTemplateColumns:'1.4fr 1.2fr 110px 1fr 110px 120px',
                alignItems:'center',padding:D.rowPad,
                borderBottom:`1px solid ${hexToRgba(C.accent,0.06)}`,
                fontFamily:C.mono,fontSize:11.5,
              }}>
                <span style={{color:C.fg,letterSpacing:'0.04em',overflow:'hidden',textOverflow:'ellipsis',whiteSpace:'nowrap'}}>{token}</span>
                <span style={{color:hint==='—'?C.fgDim:C.fg,fontSize:11}}>{hint}</span>
                <span style={{color:status==='expiring'?C.warn:C.fg,fontSize:11}}>{exp}</span>
                <span style={{color:C.fgMute,fontSize:11,overflow:'hidden',textOverflow:'ellipsis',whiteSpace:'nowrap'}}>{by}</span>
                <span>
                  <span style={{padding:'1px 8px',borderRadius:999,fontSize:9.5,letterSpacing:'0.14em',
                    background:hexToRgba(sc,0.12),border:`1px solid ${hexToRgba(sc,0.5)}`,
                    color:sc,textTransform:'uppercase'}}>{status}</span>
                </span>
                <span style={{textAlign:'right',color:C.fgDim,fontSize:10.5,letterSpacing:'0.04em'}}>
                  consumed on first agent connect
                </span>
              </div>
            );
          })}
        </div>
        <PageFooter C={C} count={ENROLLMENTS.length} total={ENROLLMENTS.length} endpoint="/v1/agent-enrollments (active only)" sort={sort}/>
      </div>

      <div style={{fontFamily:C.mono,fontSize:10,color:C.fgDim,letterSpacing:'0.04em',lineHeight:1.7}}>
        ▸ Distinct from user invites — these bootstrap a <span style={{color:C.fgMute,fontFamily:C.mono}}>relay-agent</span> process.
        Set the printed token as <span style={{color:C.fgMute,fontFamily:C.mono}}>RELAY_AGENT_ENROLLMENT_TOKEN</span> on first boot.
        The agent receives a long-lived token in exchange and the enrollment is consumed.
      </div>
    </>
  );
}

function AdminReservations({ C, D }) {
  const [sort, setSort] = useState('-created_at');
  // row: [name,proj,wids,sel,starts,ends] — starts/ends nullable ('—')
  const RES_SORT = [
    { value:'-created_at', label:'Newest' },
    { value:'created_at',  label:'Oldest' },
    { value:'name',        label:'Name A→Z' },
    { value:'-name',       label:'Name Z→A' },
    { value:'starts_at',   label:'Starts soonest' },
    { value:'-starts_at',  label:'Starts latest' },
    { value:'ends_at',     label:'Ends soonest' },
    { value:'-ends_at',    label:'Ends latest' },
  ];
  const resKeyMap = {
    created_at: (r,i) => -i,
    name:       (r) => r[0].toLowerCase(),
    starts_at:  (r) => dateToNum(r[4]),   // '—' → null (NULLS LAST/FIRST)
    ends_at:    (r) => dateToNum(r[5]),
  };
  const sorted = applySort(RESERVATIONS, sort, resKeyMap);
  return (
    <>
      <div style={{display:'flex',gap:10,alignItems:'center'}}>
        <span style={{fontSize:13,color:C.fgMute,fontFamily:C.mono,letterSpacing:'0.06em'}}>GET /v1/reservations · admin-only</span>
        <span style={{marginLeft:'auto'}}/>
        <SortControl C={C} options={RES_SORT} value={sort} onChange={setSort}/>
        <button style={pillBtn(C,'primary')}>+ Reserve workers</button>
      </div>

      <div style={{...glassPanel(C),flex:1,minHeight:0,display:'flex',flexDirection:'column',overflow:'hidden'}}>
        <div style={{display:'grid',gridTemplateColumns:'1.2fr 1fr 1.4fr 1fr 1fr 1fr 80px',
          fontFamily:C.mono,fontSize:10,letterSpacing:'0.16em',color:C.fgMute,
          padding:'12px 18px',borderBottom:`1px solid ${C.border}`}}>
          <span>NAME</span><span>PROJECT</span><span>WORKER IDS</span><span>SELECTOR</span><span>STARTS</span><span>ENDS</span><span style={{textAlign:'right'}}>ACT.</span>
        </div>
        <div style={{flex:1,minHeight:0,overflow:'auto'}}>
          {sorted.map((r,i)=>{
            const [name,proj,wids,sel,starts,ends] = r;
            return (
              <div key={name} style={{
                display:'grid',gridTemplateColumns:'1.2fr 1fr 1.4fr 1fr 1fr 1fr 80px',
                alignItems:'center',padding:D.rowPad,
                borderBottom:`1px solid ${hexToRgba(C.accent,0.06)}`,
                fontFamily:C.mono,fontSize:11.5,
              }}>
                <span style={{color:C.fg,fontSize:12,fontFamily:C.sans,fontWeight:500}}>{name}</span>
                <span style={{color:C.accentB,fontSize:11,letterSpacing:'0.04em'}}>{proj}</span>
                <span style={{display:'flex',flexWrap:'wrap',gap:4}}>
                  {wids.map(w=>(
                    <span key={w} style={{padding:'1px 7px',borderRadius:4,
                      background:'rgba(255,255,255,0.04)',border:`1px solid ${C.border}`,
                      fontSize:10,color:C.fg}}>{w}</span>
                  ))}
                </span>
                <span style={{color:sel==='—'?C.fgDim:C.fgMute,fontSize:10.5}}>{sel}</span>
                <span style={{color:C.fgMute,fontSize:10.5}}>{starts}</span>
                <span style={{color:C.fgMute,fontSize:10.5}}>{ends}</span>
                <span style={{textAlign:'right'}}>
                  <button style={{...miniBtn(C,'ghost'),
                    background:'rgba(251,113,133,0.1)',border:`1px solid ${hexToRgba(C.err,0.4)}`,color:C.err}}>Delete</button>
                </span>
              </div>
            );
          })}
        </div>
        <PageFooter C={C} count={RESERVATIONS.length} total={RESERVATIONS.length} endpoint="/v1/reservations" sort={sort}/>
      </div>

      <div style={{fontFamily:C.mono,fontSize:10,color:C.fgDim,letterSpacing:'0.04em',lineHeight:1.7}}>
        ▸ Selector is <b style={{color:C.fgMute}}>informational only</b> in v1 — only explicit <span style={{color:C.fgMute,fontFamily:C.mono}}>worker_ids</span> lists are enforced by the scheduler.
      </div>
    </>
  );
}

function AdminServer({ C }) {
  const env = [
    { group: 'Auth',       items: [
      ['RELAY_BOOTSTRAP_ADMIN',       '(consumed)',      'Cleared from process env after admin was created on startup.'],
      ['RELAY_ALLOW_SELF_REGISTER',   'false',           'POST /v1/auth/register requires an invite_token.'],
      ['RELAY_LOGIN_RATE_LIMIT',      '10:1m',           'Per-IP rate limit for POST /v1/auth/login.'],
      ['RELAY_REGISTER_RATE_LIMIT',   '5:1m',            'Per-IP rate limit for POST /v1/auth/register.'],
    ]},
    { group: 'Fleet',      items: [
      ['RELAY_WORKER_GRACE_WINDOW',   '2m',              'How long to wait before requeueing tasks from a disconnected agent.'],
      ['RELAY_TELEMETRY_WINDOW',      '30m',             'Retention window for the in-memory worker utilization ring buffer.'],
      ['RELAY_TELEMETRY_STALE_AFTER', '30s',             'Connected worker is marked stale if no telemetry for longer than this.'],
      ['RELAY_TELEMETRY_INTERVAL',    '10s',             'How often the agent samples + reports utilization (agent-side).'],
    ]},
    { group: 'Storage',    items: [
      ['RELAY_DATABASE_URL',          'postgres://relay:****@db-host:5432/relay', 'Connection string.'],
      ['RELAY_DB_MAX_CONNS',          '25',              'PostgreSQL connection pool size.'],
    ]},
    { group: 'Network',    items: [
      ['RELAY_HTTP_ADDR',             ':8080',           'HTTP server bind address.'],
      ['RELAY_GRPC_ADDR',             ':9090',           'gRPC server bind address (agent connections).'],
      ['RELAY_CORS_ORIGINS',          'https://relay.studio.dev', 'Same-origin only when empty; wildcard rejected.'],
    ]},
  ];
  return (
    <div style={{...glassPanel(C),flex:1,minHeight:0,overflow:'auto',padding:'18px 22px'}}>
      <div style={{display:'flex',justifyContent:'space-between',alignItems:'baseline',marginBottom:14}}>
        <div>
          <div style={{fontSize:14,marginBottom:2}}>Server configuration</div>
          <div style={{fontFamily:C.mono,fontSize:11,color:C.fgMute,letterSpacing:'0.04em'}}>
            Read-only · derived from process env at startup · restart to change.
          </div>
        </div>
        <span style={{fontFamily:C.mono,fontSize:10,padding:'3px 10px',borderRadius:999,
          background:hexToRgba(C.ok,0.12),border:`1px solid ${hexToRgba(C.ok,0.5)}`,
          color:C.ok,letterSpacing:'0.14em'}}>● HEALTHY</span>
      </div>
      <div style={{display:'grid',gridTemplateColumns:'1fr 1fr',gap:18}}>
        {env.map(({group,items}) => (
          <section key={group}>
            <div style={{fontFamily:C.mono,fontSize:10,letterSpacing:'0.18em',color:C.fgMute,
              paddingBottom:6,marginBottom:8,borderBottom:`1px solid ${C.border}`}}>{group.toUpperCase()}</div>
            <div style={{display:'flex',flexDirection:'column',gap:10}}>
              {items.map(([k,v,desc])=>(
                <div key={k}>
                  <div style={{display:'grid',gridTemplateColumns:'1fr auto',gap:10,alignItems:'baseline'}}>
                    <span style={{fontFamily:C.mono,fontSize:11,color:C.fg,letterSpacing:'0.04em',overflow:'hidden',textOverflow:'ellipsis',whiteSpace:'nowrap'}}>{k}</span>
                    <span style={{fontFamily:C.mono,fontSize:11.5,color:C.accent,letterSpacing:'0.04em'}}>{v}</span>
                  </div>
                  <div style={{fontSize:10.5,color:C.fgMute,marginTop:2,lineHeight:1.5}}>{desc}</div>
                </div>
              ))}
            </div>
          </section>
        ))}
      </div>
    </div>
  );
}

function AdminTokenModal({ C, kind, onClose }) {
  const isInvite = kind === 'invite';
  return (
    <div style={{position:'absolute',inset:0,background:'rgba(0,0,0,0.55)',
      display:'grid',placeItems:'center',backdropFilter:'blur(4px)',zIndex:100}}
      onClick={onClose}>
      <div onClick={e=>e.stopPropagation()} style={{...glassPanel(C),
        padding:'22px 26px',width:480,maxWidth:'90%',display:'flex',flexDirection:'column',gap:14}}>
        <div style={{display:'flex',justifyContent:'space-between',alignItems:'flex-start'}}>
          <div>
            <div style={{fontFamily:C.mono,fontSize:10,letterSpacing:'0.18em',color:C.fgMute,marginBottom:4}}>
              POST {isInvite ? '/v1/invites' : '/v1/agent-enrollments'}
            </div>
            <h3 style={{margin:0,fontSize:18,fontWeight:500,letterSpacing:'-0.01em'}}>
              {isInvite ? 'Create user invite' : 'Enroll new agent'}
            </h3>
          </div>
          <button onClick={onClose} style={{background:'transparent',border:'none',color:C.fgMute,cursor:'pointer',fontSize:18,padding:4}}>×</button>
        </div>

        <div style={{display:'flex',flexDirection:'column',gap:12}}>
          <label style={{display:'flex',flexDirection:'column',gap:6}}>
            <span style={{fontFamily:C.mono,fontSize:10,letterSpacing:'0.16em',color:C.fgMute}}>
              {isInvite ? 'EMAIL · OPTIONAL · BINDS INVITE' : 'HOSTNAME_HINT · OPTIONAL'}
            </span>
            <input placeholder={isInvite?'partner@vendor.io':'farm-west-13'} style={{
              padding:'8px 12px',borderRadius:6,background:'rgba(0,0,0,0.3)',
              border:`1px solid ${C.border}`,color:C.fg,fontFamily:C.sans,fontSize:13,outline:'none',
            }}/>
          </label>
          <label style={{display:'flex',flexDirection:'column',gap:6}}>
            <span style={{fontFamily:C.mono,fontSize:10,letterSpacing:'0.16em',color:C.fgMute}}>
              EXPIRES_IN · DEFAULT {isInvite?'72h · MAX 720h':'24h · MAX 168h (7d)'}
            </span>
            <div style={{display:'flex',gap:6}}>
              {(isInvite ? ['24h','72h','7d','30d'] : ['1h','24h','3d','7d']).map((v,i)=>(
                <button key={v} style={{
                  flex:1,padding:'6px 10px',borderRadius:6,cursor:'pointer',
                  background: i===1?`linear-gradient(90deg, ${hexToRgba(C.accent,0.25)}, ${hexToRgba(C.accentB,0.18)})`:'rgba(255,255,255,0.04)',
                  border:`1px solid ${i===1?C.accent+'66':C.border}`,
                  color: i===1?C.fg:C.fgMute, fontFamily:C.mono,fontSize:11,letterSpacing:'0.06em',
                }}>{v}</button>
              ))}
            </div>
          </label>
        </div>

        <div style={{padding:'10px 12px',borderRadius:6,
          background:hexToRgba(C.warn,0.08),border:`1px solid ${hexToRgba(C.warn,0.4)}`,
          fontFamily:C.mono,fontSize:10.5,color:C.warn,letterSpacing:'0.04em',lineHeight:1.6}}>
          ⚠ The raw token is returned <b>once</b>. It cannot be retrieved again — copy it from the success toast.
        </div>

        <div style={{display:'flex',gap:8,justifyContent:'flex-end',marginTop:4}}>
          <button onClick={onClose} style={pillBtn(C,'ghost')}>Cancel</button>
          <button style={pillBtn(C,'primary')}>{isInvite?'Generate invite':'Enroll'}</button>
        </div>
      </div>
    </div>
  );
}

function PageFooter({ C, count, total, endpoint, sort }) {
  return (
    <div style={{display:'flex',justifyContent:'space-between',alignItems:'center',
      padding:'10px 18px',borderTop:`1px solid ${C.border}`,
      fontFamily:C.mono,fontSize:10.5,letterSpacing:'0.08em',color:C.fgMute}}>
      <span>SHOWING <span style={{color:C.fg}}>1–{count}</span> OF <span style={{color:C.fg}}>{total}</span> · <span style={{color:C.fgMute}}>{endpoint}</span>{sort && <> · SORT <span style={{color:C.accentB}}>{sort}</span></>} · CURSOR PAGINATED</span>
      <div style={{display:'flex',gap:6}}>
        <button style={{...pillBtn(C,'ghost'),padding:'4px 12px',fontSize:11,opacity:0.5}}>← prev</button>
        <button style={{...pillBtn(C,'ghost'),padding:'4px 12px',fontSize:11}}>size: 50</button>
      </div>
    </div>
  );
}

function pillBtn(C, kind){
  const base = {padding:'8px 16px',borderRadius:999,fontFamily:C.sans,
    fontSize:12,letterSpacing:'0.02em',cursor:'pointer',backdropFilter:'blur(8px)',border:'none'};
  if(kind==='primary') return {...base,
    background:`linear-gradient(90deg, ${C.accent}, ${C.accentB})`,
    color:'#fff', fontWeight:600,
  };
  return {...base, background:'rgba(255,255,255,0.05)',border:`1px solid ${C.border}`,color:C.fg};
}

// ── JOB DETAIL (compact, propagated from Holo variant) ──────────────────────
function HoloJobDetail({ C, D, dag, onBack, selectedTask, setSelectedTask, onExpandLog, onOpenWorker }) {
  D = D || {pad:'18px 22px',gap:14,rowPad:'10px 18px'};
  const [pane, setPane] = useState('logs');
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

  const tabBtn = (active) => ({
    ...pillBtn(C,'ghost'),
    background: active ? `linear-gradient(90deg, ${hexToRgba(C.accent,0.25)}, ${hexToRgba(C.accentB,0.18)})` : 'rgba(255,255,255,0.05)',
    border: `1px solid ${active ? C.accent+'66' : C.border}`,
    color: active ? C.fg : C.fgMute,
  });

  const selTask = TASKS.find(t => t[0] === selectedTask) || TASKS[0];
  const [selId, selIdx, selSt, selPct, selWorker, selDur] = selTask;
  const selColor = selSt==='done'?C.ok: selSt==='running'?C.accent : selSt==='failed'?C.err : C.fgMute;
  const filteredLog = LOG.filter(l => l[3].toLowerCase().includes(selId.toLowerCase()));
  return (
    <div style={{flex:1, padding:D.pad, display:'flex',flexDirection:'column',gap:D.gap, minHeight:0}}>
      <div style={{display:'flex',alignItems:'center',gap:10}}>
        <a onClick={onBack} style={{fontSize:12,color:C.fgMute,cursor:'pointer'}}>← Jobs</a>
        <span style={{color:C.fgDim}}>/</span>
        <span style={{fontFamily:C.mono,fontSize:13,letterSpacing:'0.06em',color:C.accent}}>{JOB_DETAIL.id}</span>
        <span style={{fontSize:16,letterSpacing:'-0.01em'}}>{JOB_DETAIL.name}</span>
        <span style={{marginLeft:'auto',display:'flex',gap:8}}>
          <button onClick={()=>setPane('spec')} style={tabBtn(pane==='spec')}>Spec</button>
          <button onClick={()=>setPane('logs')} style={tabBtn(pane==='logs')}>Logs</button>
          <button style={pillBtn(C,'ghost')}>Retry</button>
          <button style={{...pillBtn(C,'ghost'), background:'rgba(251,113,133,0.12)',border:`1px solid ${C.err}55`,color:C.err}}>Abort</button>
        </span>
      </div>

      <div ref={containerRef} style={{flex:1, minHeight:0, display:'flex', alignItems:'stretch', position:'relative'}}>
        <div style={{flex:1, minWidth:0, display:'flex',flexDirection:'column', gap:14, paddingRight:14}}>
          <div style={{...glassPanel(C),padding:'12px 18px',display:'grid',gridTemplateColumns:'auto 1fr',gap:18,alignItems:'center'}}>
            <Donut value={JOB_DETAIL.pct} C={C}/>
            <div>
              <div style={{display:'flex',gap:8,marginBottom:10}}>
                <span style={{display:'inline-flex',alignItems:'center',gap:8,padding:'3px 10px',borderRadius:999,
                  background:hexToRgba(C.accent,0.15),border:`1px solid ${C.accent}55`,
                  fontFamily:C.mono,fontSize:10,letterSpacing:'0.16em',color:C.accent}}>
                  <span style={{width:6,height:6,borderRadius:'50%',background:C.accent}}/>
                  RUNNING
                </span>
              </div>
              <div style={{display:'grid',gridTemplateColumns:'repeat(4,1fr)',gap:14}}>
                {[
                  ['Elapsed', JOB_DETAIL.elapsed, false],
                  ['ETA', JOB_DETAIL.eta, true],
                  ['Tasks', `${JOB_DETAIL.tasksDone}/${JOB_DETAIL.tasksTotal}`, false],
                  ['Owner', JOB_DETAIL.owner, false, true],
                ].map(([k,v,hot,small])=>(
                  <div key={k}>
                    <div style={{fontSize:10,letterSpacing:'0.16em',color:C.fgMute,fontFamily:C.mono,marginBottom:4}}>{k.toUpperCase()}</div>
                    <div style={{fontSize: small ? 13 : 20, fontFamily: small ? C.sans : C.mono, fontWeight:300,
                      color: hot ? C.accent : C.fg,
                      overflow:'hidden',textOverflow:'ellipsis',whiteSpace:'nowrap',
                    }}>{v}</div>
                  </div>
                ))}
              </div>
            </div>
          </div>

          {dag && (
            <div style={{...glassPanel(C),padding:'14px 16px',flex:'none'}}>
              <div style={{display:'flex',alignItems:'center',justifyContent:'space-between',marginBottom:6}}>
                <span style={{fontSize:13}}>Pipeline</span>
                <span style={{fontFamily:C.mono,fontSize:10,color:C.fgMute,letterSpacing:'0.16em'}}>STAGE 4 / 8</span>
              </div>
              <DAGSVG tokens={{
                edge:hexToRgba(C.accent,0.2), edgeActive:C.accent,
                nodeBg:'rgba(255,255,255,0.06)', ok:C.ok, run:C.accent, idle:C.fgDim,
                fg:C.fg, font:C.mono, radius:6,
              }} h={150}/>
            </div>
          )}

          <div style={{...glassPanel(C),flex:1,minHeight:0,display:'flex',flexDirection:'column'}}>
            <div style={{padding:'12px 18px',borderBottom:`1px solid ${C.border}`,display:'flex',justifyContent:'space-between'}}>
              <span style={{fontSize:13}}>Tasks · {JOB_DETAIL.tasksDone} / {JOB_DETAIL.tasksTotal}</span>
              <span style={{fontFamily:C.mono,fontSize:10,color:C.fgMute,letterSpacing:'0.14em'}}>4 ACTIVE · 12 QUEUED · CLICK TO STREAM</span>
            </div>
            <div style={{flex:1,minHeight:0,overflow:'auto',padding:'4px 0'}}>
              {TASKS.slice(0,12).map(([id,idx,st,pct,worker,dur])=>{
                const sc = st==='done'?C.ok: st==='running'?C.accent : C.fgMute;
                const active = id === selectedTask;
                return (
                  <div key={id} onClick={()=>setSelectedTask(id)} style={{display:'grid',gridTemplateColumns:'56px 96px 1fr 100px 50px',
                    alignItems:'center',padding:'6px 16px',fontFamily:C.mono,fontSize:11, cursor:'pointer',
                    borderLeft:`2px solid ${active ? C.accent : 'transparent'}`,
                    background: active ? hexToRgba(C.accent,0.08) : 'transparent',
                    borderBottom:`1px solid ${C.border}`}}>
                    <span style={{color:active?C.fg:C.fgMute}}>#{idx}</span>
                    <span style={{display:'flex',alignItems:'center',gap:8,color:sc,letterSpacing:'0.08em'}}>
                      <StatusDot status={st} size={6}/> {st}
                    </span>
                    <span
                      onClick={(e)=>{
                        e.stopPropagation();
                        if (worker && onOpenWorker) onOpenWorker(worker);
                      }}
                      style={{
                        color: st==='queued'?C.fgMute:C.accent,
                        cursor: worker ? 'pointer' : 'default',
                        textDecoration: worker ? 'underline' : 'none',
                        textDecorationColor: hexToRgba(C.accent,0.35),
                        textUnderlineOffset: 2,
                      }}>{worker}</span>
                    <span style={{position:'relative',height:3,background:'rgba(255,255,255,0.08)',borderRadius:2,overflow:'hidden'}}>
                      <span style={{position:'absolute',inset:0,width:`${pct}%`,
                        background: st==='done'?C.ok:`linear-gradient(90deg,${C.accent},${C.accentB})`,borderRadius:2}}/>
                    </span>
                    <span style={{textAlign:'right',color:C.fgMute}}>{dur}</span>
                  </div>
                );
              })}
            </div>
          </div>
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
          <div style={{...glassPanel(C),flex:1,minHeight:0,display:'flex',flexDirection:'column'}}>
            <div style={{padding:'12px 16px',borderBottom:`1px solid ${C.border}`,display:'flex',justifyContent:'space-between',alignItems:'center',gap:10}}>
              {pane === 'logs' ? (
                <span style={{display:'flex',alignItems:'center',gap:8,minWidth:0}}>
                  <span style={{fontSize:13}}>Log stream</span>
                  <span style={{color:C.fgDim,fontSize:13}}>·</span>
                  <span style={{display:'inline-flex',alignItems:'center',gap:6,padding:'2px 8px',borderRadius:999,
                    background:hexToRgba(selColor,0.12),border:`1px solid ${hexToRgba(selColor,0.4)}`,
                    fontFamily:C.mono,fontSize:10,letterSpacing:'0.08em',color:selColor}}>
                    <StatusDot status={selSt} size={5}/>
                    {selId}
                  </span>
                </span>
              ) : (
                <span style={{fontSize:13}}>Job spec</span>
              )}
              <span style={{display:'flex',alignItems:'center',gap:8}}>
                {pane==='logs' ? (
                  <span style={{display:'inline-flex',alignItems:'center',gap:6,fontFamily:C.mono,fontSize:10,letterSpacing:'0.16em',color:C.ok}}>
                    <span style={{width:5,height:5,borderRadius:'50%',background:C.ok}}/> LIVE
                  </span>
                ) : (
                  <span style={{fontFamily:C.mono,fontSize:10,letterSpacing:'0.16em',color:C.fgMute}}>READ-ONLY</span>
                )}
                {pane==='logs' && (
                  <button onClick={onExpandLog} title="Open full task log"
                    style={{padding:'4px 8px',borderRadius:6,border:`1px solid ${C.border}`,
                      background:'rgba(255,255,255,0.05)',color:C.fg,cursor:'pointer',
                      fontFamily:C.mono,fontSize:13,lineHeight:1}}>⤢</button>
                )}
              </span>
            </div>
            {pane === 'logs' ? (
              <div style={{padding:'10px 14px',fontFamily:C.mono,fontSize:10.5,lineHeight:1.7,color:C.fgMute,
                flex:1,minHeight:0,overflow:'auto'}}>
                {filteredLog.length === 0 && (
                  <div style={{color:C.fgMute,padding:'8px 0',fontStyle:'italic'}}>
                    No log entries yet for {selId} · waiting for stream…
                  </div>
                )}
                {filteredLog.map((l,i)=>{
                  const lvl = l[1].trim();
                  const lvlC = lvl==='WARN'?C.warn : lvl==='DEBUG'?C.fgMute : C.accent;
                  return (
                    <div key={i} style={{padding:'2px 0'}}>
                      <span style={{color:C.fgDim}}>{l[0]} </span>
                      <span style={{color:lvlC,fontWeight:600}}>{lvl} </span>
                      <span style={{color:C.fg}}><span style={{color:C.accentB}}>{l[2]}</span> {l[3]}</span>
                    </div>
                  );
                })}
              </div>
            ) : (
              <div style={{flex:1,minHeight:0,overflow:'auto',padding:'14px 16px',
                fontFamily:C.mono,fontSize:11,lineHeight:1.85,color:C.fg}}>
                {[
                  ['image', JOB_DETAIL.image],
                  ['runtime', JOB_DETAIL.runtime],
                  ['cluster', JOB_DETAIL.cluster],
                  ['parallelism', String(JOB_DETAIL.parallelism)],
                  ['priority', JOB_DETAIL.priority],
                  ['owner', JOB_DETAIL.owner],
                  ['source', JOB_DETAIL.source],
                ].map(([k,v])=>(
                  <div key={k} style={{display:'grid',gridTemplateColumns:'92px 1fr',gap:8,padding:'2px 0'}}>
                    <span style={{color:C.fgMute}}>{k}</span>
                    <span style={{color:C.fg}}>{v}</span>
                  </div>
                ))}
                <div style={{marginTop:12,paddingTop:10,borderTop:`1px solid ${C.border}`}}>
                  <div style={{fontFamily:C.sans,fontSize:11,letterSpacing:'0.16em',
                    color:C.fgMute,marginBottom:6}}>COMMAND</div>
                  <div style={{padding:'10px 12px',borderRadius:6,
                    background:'rgba(0,0,0,0.3)',border:`1px solid ${C.border}`,
                    color:C.fg,whiteSpace:'pre-wrap',wordBreak:'break-word'}}>
                    <span style={{color:C.accent}}>$</span> {JOB_DETAIL.cmd}
                  </div>
                </div>
                <div style={{marginTop:12,paddingTop:10,borderTop:`1px solid ${C.border}`}}>
                  <div style={{fontFamily:C.sans,fontSize:11,letterSpacing:'0.16em',
                    color:C.fgMute,marginBottom:6}}>ENV</div>
                  {[
                    ['SCENE','scene.blend'],
                    ['CYCLES_DEVICE','CUDA'],
                    ['FRAME_START','1'],
                    ['FRAME_END','1000'],
                    ['OUTPUT','s3://renders/'],
                  ].map(([k,v])=>(
                    <div key={k} style={{padding:'2px 0'}}>
                      <span style={{color:C.accentB}}>{k}</span>
                      <span style={{color:C.fgDim}}>=</span>
                      <span style={{color:C.fg}}>{v}</span>
                    </div>
                  ))}
                </div>
              </div>
            )}
          </div>
        </div>
      </div>
    </div>
  );
}

// ── FULL-SCREEN TASK LOG ───────────────────────────────────────────────────
function HoloTaskLog({ C, D, taskId, onBack }) {
  D = D || {pad:'18px 22px',gap:14,rowPad:'10px 18px'};
  const task = TASKS.find(t => t[0] === taskId) || TASKS[0];
  const [id, idx, st, pct, worker, dur] = task;
  const sc = st==='done'?C.ok : st==='running'?C.accent : st==='failed'?C.err : C.fgMute;
  const baseLog = LOG.filter(l => l[3].toLowerCase().includes(id.toLowerCase()));
  // Extend with plausible per-task lines for the full-screen view
  const extended = [
    ...baseLog,
    ['14:36:25','INFO ', id, 'render · frame 624 / 1000 · 62%'],
    ['14:36:26','DEBUG','gpu ', `${worker} · cuda 0 · util 91% · vram 18.2/24 GB`],
    ['14:36:27','INFO ', id, 'cycles · 320 samples · denoiser=optix'],
    ['14:36:28','INFO ', id, 'render · frame 625 / 1000 · 62%'],
    ['14:36:30','DEBUG','io  ', 'write s3://renders/frame_0624.exr · 12.4 MB'],
    ['14:36:31','INFO ', id, 'render · frame 626 / 1000 · 63%'],
    ['14:36:33','WARN ','net ', 'retry 1/3 · s3://renders/ · 503'],
    ['14:36:34','INFO ','net ', 'recovered · 217ms'],
    ['14:36:35','INFO ', id, 'render · frame 627 / 1000 · 63%'],
    ['14:36:37','INFO ', id, 'render · frame 628 / 1000 · 63%'],
    ['14:36:39','DEBUG','mem ', `rss 4.1 GB · peak 4.3 GB`],
    ['14:36:41','INFO ', id, 'render · frame 629 / 1000 · 63%'],
  ];
  return (
    <div style={{flex:1, padding:D.pad, display:'flex',flexDirection:'column',gap:D.gap, minHeight:0}}>
      <div style={{display:'flex',alignItems:'center',gap:10,flexWrap:'wrap'}}>
        <a onClick={onBack} style={{fontSize:12,color:C.fgMute,cursor:'pointer'}}>← Job detail</a>
        <span style={{color:C.fgDim}}>/</span>
        <span style={{fontFamily:C.mono,fontSize:13,letterSpacing:'0.06em',color:C.fgMute}}>{JOB_DETAIL.id}</span>
        <span style={{color:C.fgDim}}>/</span>
        <span style={{fontFamily:C.mono,fontSize:13,letterSpacing:'0.06em',color:C.accent}}>{id}</span>
        <span style={{fontSize:16,letterSpacing:'-0.01em'}}>task log</span>
        <span style={{display:'inline-flex',alignItems:'center',gap:6,padding:'3px 10px',borderRadius:999,
          background:hexToRgba(sc,0.12),border:`1px solid ${hexToRgba(sc,0.4)}`,
          fontFamily:C.mono,fontSize:10,letterSpacing:'0.16em',color:sc}}>
          <StatusDot status={st} size={6}/> {st.toUpperCase()}
        </span>
        <span style={{fontFamily:C.mono,fontSize:11,color:C.fgMute,letterSpacing:'0.06em'}}>#{idx} · {worker} · {pct}% · {dur}</span>
        <span style={{marginLeft:'auto',display:'flex',gap:8}}>
          <button style={pillBtn(C,'ghost')}>↧ Download</button>
          <button style={pillBtn(C,'ghost')}>Follow tail</button>
        </span>
      </div>

      <div style={{...glassPanel(C),flex:1,minHeight:0,display:'flex',flexDirection:'column'}}>
        <div style={{padding:'12px 18px',borderBottom:`1px solid ${C.border}`,display:'flex',justifyContent:'space-between',alignItems:'center'}}>
          <span style={{fontFamily:C.mono,fontSize:11,letterSpacing:'0.06em',color:C.fgMute}}>
            /v1/events?task_id={id} · single-task stream
          </span>
          <span style={{display:'inline-flex',alignItems:'center',gap:6,fontFamily:C.mono,fontSize:10,letterSpacing:'0.16em',color:C.ok}}>
            <span style={{width:5,height:5,borderRadius:'50%',background:C.ok}}/> LIVE
          </span>
        </div>
        <div style={{flex:1,minHeight:0,overflow:'auto',padding:'14px 22px',fontFamily:C.mono,fontSize:12,lineHeight:1.7,color:C.fgMute}}>
          {extended.length === 0 && (
            <div style={{color:C.fgMute,fontStyle:'italic'}}>No log entries yet for {id}…</div>
          )}
          {extended.map((l,i)=>{
            const lvl = l[1].trim();
            const lvlC = lvl==='WARN'?C.warn : lvl==='DEBUG'?C.fgMute : C.accent;
            return (
              <div key={i} style={{padding:'2px 0',display:'grid',gridTemplateColumns:'72px 56px 56px 1fr',gap:10}}>
                <span style={{color:C.fgDim}}>{l[0]}</span>
                <span style={{color:lvlC,fontWeight:600}}>{lvl}</span>
                <span style={{color:C.accentB}}>{l[2]}</span>
                <span style={{color:C.fg}}>{l[3]}</span>
              </div>
            );
          })}
        </div>
      </div>
    </div>
  );
}

function Donut({ value, C }) {
  const r=38, cx=48, cy=48, circ=2*Math.PI*r;
  const off = circ * (1 - value/100);
  return (
    <svg width="96" height="96" viewBox="0 0 96 96">
      <defs>
        <linearGradient id="hp-grad" x1="0" y1="0" x2="1" y2="1">
          <stop offset="0%" stopColor={C.accent}/>
          <stop offset="100%" stopColor={C.accentB}/>
        </linearGradient>
      </defs>
      <circle cx={cx} cy={cy} r={r} fill="none" stroke={C.bg3} strokeWidth="5"/>
      <circle cx={cx} cy={cy} r={r} fill="none" stroke="url(#hp-grad)" strokeWidth="5"
        strokeDasharray={circ} strokeDashoffset={off}
        transform={`rotate(-90 ${cx} ${cy})`} strokeLinecap="round"
        />
      <text x={cx} y={cy} textAnchor="middle" fontFamily={C.mono} fontSize="22" fill={C.fg} fontWeight="300" letterSpacing="-0.02em">{value}</text>
      <text x={cx} y={cy+13} textAnchor="middle" fontFamily={C.mono} fontSize="8" fill={C.fgMute} letterSpacing="0.18em">PERCENT</text>
    </svg>
  );
}

// ── PROFILE ─────────────────────────────────────────────────────────────────
// Logged-in user's own settings. Three tabs:
//   • Profile   — display name + email (PATCH /v1/users/me)
//   • Password  — current + new (PUT /v1/users/me/password) — revokes other sessions
//   • Sessions  — list active bearer tokens (GET /v1/auth/tokens),
//                 revoke one (DELETE /v1/auth/token/{id}),
//                 revoke all-but-current (DELETE /v1/auth/tokens)
const ME_PROFILE = {
  email: 'mira@studio.dev',
  name: 'Mira Sato',
  role: 'admin',
  created: '2025-04-02',
  lastLogin: '14:18 · today',
  loginCount: 47,
};
const ME_SESSIONS = [
  // [id, kind, agent, ip, location, createdAgo, lastActiveAgo, expiresIn, current]
  ['tok_9a4f', 'web', 'Chrome 132 · macOS',         '192.168.1.42',  'Brooklyn, NY',   '4d ago',   'just now', '26d', true ],
  ['tok_7b21', 'cli', 'relay CLI 2.4.1 · macOS',    '192.168.1.42',  'Brooklyn, NY',   '12d ago',  '2h ago',   '18d', false],
  ['tok_3c08', 'web', 'Chrome 131 · iPad',          '73.45.122.18',  'Manhattan, NY',  '22d ago',  '3d ago',   '8d',  false],
];

function HoloProfile({ C, D, initialTab, onBack }) {
  D = D || {pad:'22px 26px',gap:14,rowPad:'10px 18px'};
  const [tab, setTab] = useState(initialTab || 'profile');
  useEffect(()=>{ if(initialTab) setTab(initialTab); }, [initialTab]);
  const TABS = [
    ['profile',  'Profile',         null],
    ['password', 'Password',        null],
    ['sessions', 'Sessions',        ME_SESSIONS.length],
  ];
  const initials = ME_PROFILE.name.split(' ').map(s=>s[0]).join('').slice(0,2).toUpperCase();
  return (
    <div style={{flex:1, padding:D.pad, display:'flex',flexDirection:'column',gap:D.gap, minHeight:0}}>
      {/* Header */}
      <div style={{display:'flex',alignItems:'flex-end',gap:24,flexWrap:'wrap'}}>
        <div>
          <div style={{fontFamily:C.mono,fontSize:11,letterSpacing:'0.18em',color:C.fgMute,marginBottom:4,
            display:'flex',alignItems:'center',gap:10}}>
            <a onClick={onBack} style={{cursor:'pointer',color:C.fgMute,textDecoration:'none'}}>← BACK</a>
            <span style={{opacity:0.5}}>·</span>
            <span>YOUR ACCOUNT</span>
          </div>
          <h1 style={{margin:0,fontSize:32,fontWeight:400,letterSpacing:'-0.02em',display:'flex',alignItems:'center',gap:14}}>
            <span style={{width:40,height:40,borderRadius:10,
              background:`linear-gradient(135deg, ${C.accent}, ${C.accentB})`,
              display:'grid',placeItems:'center',color:'#fff',fontSize:15,fontWeight:700,letterSpacing:'0.04em',
              boxShadow:`inset 0 1px 0 rgba(255,255,255,0.25)`}}>{initials}</span>
            <span>{ME_PROFILE.name}</span>
          </h1>
        </div>
        <div style={{marginLeft:'auto',display:'flex',gap:14,fontFamily:C.mono,fontSize:11,color:C.fgMute,letterSpacing:'0.06em'}}>
          {[
            ['EMAIL',       ME_PROFILE.email],
            ['ROLE',        ME_PROFILE.role.toUpperCase()],
            ['MEMBER SINCE', ME_PROFILE.created],
            ['LAST LOGIN',  ME_PROFILE.lastLogin],
          ].map(([k,v])=>(
            <div key={k} style={{display:'flex',flexDirection:'column',alignItems:'flex-end',gap:1}}>
              <span style={{fontSize:9,letterSpacing:'0.16em'}}>{k}</span>
              <span style={{color:C.fg,fontSize:12}}>{v}</span>
            </div>
          ))}
        </div>
      </div>

      {/* Tabs */}
      <div style={{display:'flex',gap:6,padding:3,borderRadius:999,alignSelf:'flex-start',
        background:'rgba(0,0,0,0.3)',border:`1px solid ${C.border}`,backdropFilter:'blur(8px)'}}>
        {TABS.map(([k,n,count])=>(
          <button key={k} onClick={()=>setTab(k)} style={{
            padding:'6px 14px', borderRadius:999, border:'none', cursor:'pointer',
            fontFamily:C.sans, fontSize:12, letterSpacing:'0.02em',
            display:'flex',alignItems:'center',gap:6,
            background: tab===k?`linear-gradient(90deg, ${C.accent}, ${C.accentB})`:'transparent',
            color: tab===k?'#fff':C.fgMute,
            fontWeight: tab===k?600:400,
          }}>
            {n}
            {count!=null && <span style={{
              fontFamily:C.mono,fontSize:10,padding:'1px 7px',borderRadius:999,
              background: tab===k?'rgba(0,0,0,0.25)':'rgba(255,255,255,0.05)',
              color: tab===k?'#fff':C.fgMute, letterSpacing:'0.04em',
            }}>{count}</span>}
          </button>
        ))}
      </div>

      {/* Body */}
      <div style={{flex:1,minHeight:0,display:'flex',flexDirection:'column',gap:12}}>
        {tab === 'profile' && <ProfileIdentity C={C} D={D}/>}
        {tab === 'password' && <ProfilePassword C={C} D={D}/>}
        {tab === 'sessions' && <ProfileSessions C={C} D={D}/>}
      </div>
    </div>
  );
}

function ProfileFieldLabel({ C, children, hint }) {
  return (
    <div style={{display:'flex',justifyContent:'space-between',alignItems:'baseline',marginBottom:6}}>
      <span style={{fontFamily:C.mono,fontSize:10,letterSpacing:'0.16em',color:C.fgMute}}>{children}</span>
      {hint && <span style={{fontFamily:C.mono,fontSize:10,letterSpacing:'0.04em',color:C.fgDim}}>{hint}</span>}
    </div>
  );
}
function ProfileInput({ C, type='text', defaultValue, placeholder, locked }) {
  return (
    <input type={type} defaultValue={defaultValue} placeholder={placeholder} disabled={locked} style={{
      width:'100%',background: locked ? 'rgba(0,0,0,0.15)' : 'rgba(0,0,0,0.3)',
      border:`1px solid ${C.border}`,
      padding:'11px 14px',borderRadius:8,
      color: locked ? C.fgMute : C.fg,
      fontFamily:C.mono,fontSize:13,outline:'none',
      cursor: locked ? 'not-allowed' : 'text',
    }}/>
  );
}

function ProfileIdentity({ C }) {
  return (
    <div style={{display:'flex',gap:14,minHeight:0}}>
      <div style={{...glassPanel(C),padding:'22px 26px',flex:'1 1 0',maxWidth:560,
        display:'flex',flexDirection:'column',gap:16}}>
        <div style={{display:'flex',justifyContent:'space-between',alignItems:'baseline'}}>
          <span style={{fontSize:13,color:C.fg}}>Identity</span>
          <span style={{fontFamily:C.mono,fontSize:10,letterSpacing:'0.06em',color:C.fgDim}}>PATCH /v1/users/me</span>
        </div>

        <div>
          <ProfileFieldLabel C={C}>DISPLAY NAME</ProfileFieldLabel>
          <ProfileInput C={C} defaultValue={ME_PROFILE.name}/>
        </div>

        <div>
          <ProfileFieldLabel C={C} hint="identity · contact your admin to change">EMAIL</ProfileFieldLabel>
          <ProfileInput C={C} defaultValue={ME_PROFILE.email} locked/>
        </div>

        <div style={{display:'flex',alignItems:'center',gap:10,padding:'10px 12px',borderRadius:6,
          background:'rgba(255,255,255,0.02)',border:`1px solid ${C.border}`}}>
          <span style={{padding:'2px 8px',borderRadius:999,fontFamily:C.mono,fontSize:9.5,letterSpacing:'0.14em',
            background:hexToRgba(C.accent,0.12),color:C.accent,border:`1px solid ${hexToRgba(C.accent,0.5)}`}}>
            ADMIN
          </span>
          <span style={{fontSize:12,color:C.fgMute,lineHeight:1.5}}>
            Role is server-side only — promote/demote from <span style={{color:C.fg}}>Admin → Users</span>.
          </span>
        </div>

        <div style={{display:'flex',gap:8,marginTop:4}}>
          <button style={pillBtn(C,'primary')}>Save changes</button>
          <button style={pillBtn(C,'ghost')}>Cancel</button>
        </div>
      </div>

      {/* Side card: activity */}
      <div style={{...glassPanel(C),padding:'18px 20px',width:280,flex:'none',
        display:'flex',flexDirection:'column',gap:12}}>
        <div style={{fontSize:13,color:C.fg}}>Activity</div>
        {[
          ['Member since', ME_PROFILE.created],
          ['Last login',   ME_PROFILE.lastLogin],
          ['Login count',  ME_PROFILE.loginCount + ' (90d)'],
          ['Active sessions', ME_SESSIONS.length],
        ].map(([k,v])=>(
          <div key={k} style={{display:'flex',justifyContent:'space-between',alignItems:'baseline'}}>
            <span style={{fontFamily:C.mono,fontSize:10,letterSpacing:'0.14em',color:C.fgMute}}>{k.toUpperCase()}</span>
            <span style={{fontSize:12.5,color:C.fg}}>{v}</span>
          </div>
        ))}
        <div style={{height:1,background:C.border,margin:'2px 0'}}/>
        <div style={{fontFamily:C.mono,fontSize:10,color:C.fgDim,letterSpacing:'0.04em',lineHeight:1.6}}>
          ▸ Email is the stable identifier for invite binding and audit logs — only admins can rename users.
        </div>
      </div>
    </div>
  );
}

function ProfilePassword({ C }) {
  return (
    <div style={{display:'flex',gap:14,minHeight:0}}>
      <div style={{...glassPanel(C),padding:'22px 26px',flex:'1 1 0',maxWidth:560,
        display:'flex',flexDirection:'column',gap:14}}>
        <div style={{display:'flex',justifyContent:'space-between',alignItems:'baseline'}}>
          <span style={{fontSize:13,color:C.fg}}>Change password</span>
          <span style={{fontFamily:C.mono,fontSize:10,letterSpacing:'0.06em',color:C.fgDim}}>PUT /v1/users/me/password</span>
        </div>

        <div>
          <ProfileFieldLabel C={C}>CURRENT PASSWORD</ProfileFieldLabel>
          <ProfileInput C={C} type="password" placeholder="••••••••"/>
        </div>
        <div>
          <ProfileFieldLabel C={C} hint="min 8 chars">NEW PASSWORD</ProfileFieldLabel>
          <ProfileInput C={C} type="password" placeholder="••••••••••"/>
        </div>
        <div>
          <ProfileFieldLabel C={C}>CONFIRM NEW PASSWORD</ProfileFieldLabel>
          <ProfileInput C={C} type="password" placeholder="••••••••••"/>
        </div>

        {/* Strength hint */}
        <div style={{display:'flex',alignItems:'center',gap:8,fontFamily:C.mono,fontSize:10.5,
          letterSpacing:'0.06em',color:C.fgMute}}>
          <span style={{display:'flex',gap:3}}>
            <span style={{width:24,height:4,borderRadius:2,background:C.ok}}/>
            <span style={{width:24,height:4,borderRadius:2,background:C.ok}}/>
            <span style={{width:24,height:4,borderRadius:2,background:C.warn,opacity:0.6}}/>
            <span style={{width:24,height:4,borderRadius:2,background:C.border}}/>
          </span>
          <span style={{color:C.ok}}>strong</span>
          <span style={{opacity:0.6}}>· 12 chars · mixed case · 1 number</span>
        </div>

        <div style={{display:'flex',alignItems:'flex-start',gap:8,padding:'10px 12px',borderRadius:6,
          background:hexToRgba(C.warn,0.08),border:`1px solid ${hexToRgba(C.warn,0.35)}`}}>
          <span style={{color:C.warn,fontFamily:C.mono,fontSize:11,lineHeight:1.4}}>⚠</span>
          <span style={{fontSize:12,color:C.fg,lineHeight:1.5}}>
            All of your <b>other</b> sessions will be signed out on success.
            Your current browser session stays active.
          </span>
        </div>

        <div style={{display:'flex',gap:8,marginTop:4}}>
          <button style={pillBtn(C,'primary')}>Update password</button>
          <button style={pillBtn(C,'ghost')}>Cancel</button>
        </div>
      </div>

      <div style={{...glassPanel(C),padding:'18px 20px',width:280,flex:'none',
        display:'flex',flexDirection:'column',gap:12}}>
        <div style={{fontSize:13,color:C.fg}}>Forgot your password?</div>
        <div style={{fontSize:12,color:C.fgMute,lineHeight:1.55}}>
          Relay doesn't email password reset links. If you're locked out, an admin can issue a forced reset:
        </div>
        <div style={{fontFamily:C.mono,fontSize:10.5,padding:'8px 10px',borderRadius:6,
          background:'rgba(0,0,0,0.35)',border:`1px solid ${C.border}`,color:C.fgMute,letterSpacing:'0.04em'}}>
          POST /v1/users/password-reset
        </div>
        <div style={{fontFamily:C.mono,fontSize:10,color:C.fgDim,letterSpacing:'0.04em',lineHeight:1.6}}>
          ▸ Admin sets a temporary password and shares it out-of-band; you'll be prompted to change it on next login.
        </div>
      </div>
    </div>
  );
}

function ProfileSessions({ C, D }) {
  return (
    <>
      <div style={{display:'flex',gap:10,alignItems:'center'}}>
        <span style={{fontSize:13,color:C.fgMute,fontFamily:C.mono,letterSpacing:'0.06em'}}>
          GET /v1/auth/tokens · 30-day TTL · sliding window on use
        </span>
        <span style={{marginLeft:'auto'}}/>
        <button style={{...pillBtn(C,'ghost'),
          background:hexToRgba(C.err,0.1),border:`1px solid ${hexToRgba(C.err,0.45)}`,color:C.err}}>
          Sign out everywhere else
        </button>
      </div>

      <div style={{...glassPanel(C), flex:1, minHeight:0, display:'flex',flexDirection:'column', overflow:'hidden'}}>
        <div style={{display:'grid',gridTemplateColumns:'48px 1.5fr 1.1fr 1fr 110px 110px 90px 110px',
          fontFamily:C.mono,fontSize:10,letterSpacing:'0.16em',color:C.fgMute,
          padding:'12px 18px',borderBottom:`1px solid ${C.border}`}}>
          <span>KIND</span><span>AGENT</span><span>IP · LOCATION</span><span>CREATED</span>
          <span>LAST ACTIVE</span><span>EXPIRES IN</span><span>STATUS</span><span style={{textAlign:'right'}}>ACTIONS</span>
        </div>
        <div style={{flex:1,minHeight:0,overflow:'auto'}}>
          {ME_SESSIONS.map((s,i)=>{
            const [id,kind,agent,ip,loc,created,last,exp,current] = s;
            const isCli = kind === 'cli';
            return (
              <div key={id} style={{
                display:'grid',gridTemplateColumns:'48px 1.5fr 1.1fr 1fr 110px 110px 90px 110px',
                alignItems:'center',padding:D.rowPad,
                borderBottom:`1px solid ${hexToRgba(C.accent,0.06)}`,
                fontFamily:C.mono,fontSize:11.5,
                background: current ? hexToRgba(C.accent,0.05) : 'transparent',
              }}>
                <span>
                  <span style={{display:'inline-grid',placeItems:'center',width:26,height:26,borderRadius:6,
                    background:isCli ? hexToRgba(C.accentB,0.15) : hexToRgba(C.accent,0.15),
                    border:`1px solid ${isCli ? hexToRgba(C.accentB,0.4) : hexToRgba(C.accent,0.4)}`,
                    color: isCli ? C.accentB : C.accent,
                    fontSize:11}}>{isCli ? '›_' : '◐'}</span>
                </span>
                <span style={{display:'flex',flexDirection:'column',gap:2,minWidth:0}}>
                  <span style={{color:C.fg,fontSize:12,overflow:'hidden',textOverflow:'ellipsis',whiteSpace:'nowrap'}}>{agent}</span>
                  <span style={{color:C.fgDim,fontSize:10,letterSpacing:'0.04em'}}>{id}</span>
                </span>
                <span style={{display:'flex',flexDirection:'column',gap:2,minWidth:0}}>
                  <span style={{color:C.fg,fontSize:11}}>{ip}</span>
                  <span style={{color:C.fgDim,fontSize:10,letterSpacing:'0.04em'}}>{loc}</span>
                </span>
                <span style={{color:C.fgMute,fontSize:11}}>{created}</span>
                <span style={{color:C.fg,fontSize:11}}>{last}</span>
                <span style={{color: parseInt(exp,10) < 10 ? C.warn : C.fgMute, fontSize:11}}>{exp}</span>
                <span>
                  {current ? (
                    <span style={{padding:'1px 8px',borderRadius:999,fontSize:9.5,letterSpacing:'0.14em',
                      background:hexToRgba(C.ok,0.12),border:`1px solid ${hexToRgba(C.ok,0.5)}`,
                      color:C.ok}}>CURRENT</span>
                  ) : (
                    <span style={{padding:'1px 8px',borderRadius:999,fontSize:9.5,letterSpacing:'0.14em',
                      background:'rgba(255,255,255,0.05)',border:`1px solid ${C.border}`,
                      color:C.fgMute}}>ACTIVE</span>
                  )}
                </span>
                <span style={{display:'flex',justifyContent:'flex-end'}}>
                  {current ? (
                    <span style={{fontFamily:C.mono,fontSize:10,color:C.fgDim,letterSpacing:'0.04em'}}>this session</span>
                  ) : (
                    <button style={{...miniBtn(C,'ghost'),
                      background:hexToRgba(C.err,0.1),border:`1px solid ${hexToRgba(C.err,0.4)}`,color:C.err}}>
                      Revoke
                    </button>
                  )}
                </span>
              </div>
            );
          })}
        </div>
        <div style={{display:'flex',justifyContent:'space-between',alignItems:'center',
          padding:'10px 18px',borderTop:`1px solid ${C.border}`,
          fontFamily:C.mono,fontSize:10,color:C.fgDim,letterSpacing:'0.06em'}}>
          <span>SHOWING <span style={{color:C.fg}}>1–{ME_SESSIONS.length}</span> OF <span style={{color:C.fg}}>{ME_SESSIONS.length}</span> · ALL OF YOUR ACTIVE TOKENS</span>
          <span style={{color:C.fgDim}}>tokens roll over 30 days from last use</span>
        </div>
      </div>

      <div style={{fontFamily:C.mono,fontSize:10,color:C.fgDim,letterSpacing:'0.04em',lineHeight:1.7}}>
        ▸ <span style={{color:C.fgMute}}>Revoke</span> immediately invalidates the token — that device/CLI gets a 401 on its next request.
        Revoking the <span style={{color:C.fgMute}}>CLI session</span> means you'll need to <span style={{color:C.fg,fontFamily:C.mono}}>relay login</span> again, which rewrites <span style={{color:C.fg,fontFamily:C.mono}}>~/.relay/config.json</span>.
        Sign out everywhere else hits <span style={{color:C.fg,fontFamily:C.mono}}>DELETE /v1/auth/tokens</span>.
      </div>
    </>
  );
}

// ── ROUTER ──────────────────────────────────────────────────────────────────
function HoloApp({ palette, dag, startRoute, density, hueOffsets, allowSelfRegister }) {
  const C = makeTokens(palette, hueOffsets);
  const D = density === 'compact' ? {
    pad:'14px 20px', gap:10, rowPad:'5px 18px', rowFs:11, nameFs:12,
    laneCardPad:'7px 10px', barH:18,
  } : {
    pad:'22px 26px', gap:16, rowPad:'10px 18px', rowFs:11.5, nameFs:13,
    laneCardPad:'10px 12px', barH:22,
  };
  const [route, setRoute] = useState(startRoute || 'jobs');
  const [selectedTask, setSelectedTask] = useState(() => {
    const r = TASKS.find(t => t[2] === 'running');
    return r ? r[0] : (TASKS[0] && TASKS[0][0]);
  });
  const [selectedWorker, setSelectedWorker] = useState(WORKERS_SAMPLE[0][0]);
  const [selectedSchedule, setSelectedSchedule] = useState(SCHEDULES[0][0]);
  const [profileTab, setProfileTab] = useState('profile');
  const isAuth = route === 'auth';
  const goProfile = (key) => {
    setProfileTab(key === 'profile' ? 'profile' : key);
    setRoute('profile');
  };
  return (
    <HoloShell C={C} route={route} setRoute={setRoute} hideNav={isAuth} onProfileNavigate={goProfile}>
      {route === 'auth' && <HoloAuth C={C} onSignIn={()=>setRoute('jobs')} allowSelfRegister={allowSelfRegister}/>}
      {route === 'jobs' && <HoloJobsList C={C} D={D} onOpen={()=>setRoute('job-detail')}/>}
      {route === 'job-detail' && <HoloJobDetail C={C} D={D} dag={dag} onBack={()=>setRoute('jobs')}
        selectedTask={selectedTask} setSelectedTask={setSelectedTask}
        onExpandLog={()=>setRoute('task-log')}
        onOpenWorker={(n)=>{setSelectedWorker(n);setRoute('worker-detail');}}/>}
      {route === 'task-log' && <HoloTaskLog C={C} D={D} taskId={selectedTask} onBack={()=>setRoute('job-detail')}/>}
      {route === 'workers' && <HoloWorkers C={C} D={D}
        onOpen={(n)=>{setSelectedWorker(n);setRoute('worker-detail');}}
        onOpenTask={(taskId)=>{if(taskId)setSelectedTask(taskId);setRoute('job-detail');}}/>}
      {route === 'worker-detail' && <HoloWorkerDetail C={C} D={D} workerName={selectedWorker}
        onBack={()=>setRoute('workers')}
        onOpenTask={(taskId)=>{if(taskId)setSelectedTask(taskId);setRoute('job-detail');}}/>}
      {route === 'admin' && <HoloAdmin C={C} D={D}/>}
      {route === 'profile' && <HoloProfile C={C} D={D} initialTab={profileTab} onBack={()=>setRoute('jobs')}/>}
      {route === 'schedules' && <HoloSchedules C={C} D={D}
        onOpenJob={()=>setRoute('job-detail')}
        onEdit={(n)=>{setSelectedSchedule(n);setRoute('schedule-detail');}}/>}
      {route === 'schedule-detail' && <HoloScheduleDetail C={C} D={D}
        scheduleName={selectedSchedule}
        onBack={()=>setRoute('schedules')}
        onOpenJob={()=>setRoute('job-detail')}/>}
    </HoloShell>
  );
}

function Empty({ C, title, sub }) {
  return (
    <div style={{flex:1,display:'grid',placeItems:'center'}}>
      <div style={{textAlign:'center'}}>
        <div style={{fontFamily:C.mono,fontSize:11,letterSpacing:'0.22em',color:C.fgMute,marginBottom:8}}>SECTION</div>
        <h1 style={{margin:'0 0 8px',fontSize:36,fontWeight:300,letterSpacing:'-0.02em'}}>{title}</h1>
        <p style={{margin:0,color:C.fgMute,fontSize:13}}>{sub}</p>
      </div>
    </div>
  );
}

window.HoloApp = HoloApp;
