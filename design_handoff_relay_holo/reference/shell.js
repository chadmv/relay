// Shell renderers — return HTML strings for reuse across wireframes.
// activeKey: one of 'overview' | 'jobs' | 'workers' | 'reservations' | 'invites' | 'settings'
window.Shell = (function(){

  const NAV = [
    ['overview',    'Overview'],
    ['jobs',        'Jobs'],
    ['workers',     'Workers'],
    ['reservations','Reservations'],
  ];
  const NAV_SECONDARY = [
    ['invites',  'Invites'],
    ['settings', 'Settings'],
  ];

  function sidebar(activeKey, opts={}) {
    const items = NAV.map(([k,label]) => `
      <div class="nav-item ${k===activeKey?'active':''}"><span class="ico"></span>${label}</div>
    `).join('');
    const secondary = NAV_SECONDARY.map(([k,label]) => `
      <div class="nav-item ${k===activeKey?'active':''} ${opts.adminMuted?'muted':''}"><span class="ico"></span>${label}</div>
    `).join('');
    const user = opts.user || {name:'ada@studio.dev', role:'admin', initial:'A'};
    return `
      <aside class="side-nav" data-requires-nav="sidebar">
        <div class="logo">relay<span class="dot">.</span></div>
        ${items}
        <div class="nav-divider"></div>
        ${secondary}
        <div class="nav-user">
          <span class="avatar">${user.initial}</span>
          <div style="min-width:0; overflow:hidden;">
            <div style="overflow:hidden; text-overflow:ellipsis; white-space:nowrap;">${user.name}</div>
            <div class="small" style="color:var(--accent)">${user.role}</div>
          </div>
        </div>
      </aside>
    `;
  }

  function topbar(activeKey, opts={}) {
    const all = [...NAV, ...NAV_SECONDARY];
    const items = all.map(([k,label]) => `
      <div class="nav-item ${k===activeKey?'active':''}">${label}</div>
    `).join('');
    const user = opts.user || {name:'ada', initial:'A'};
    return `
      <header class="top-nav" data-requires-nav="topbar">
        <div class="logo">relay<span class="dot">.</span></div>
        <div class="top-nav-items">${items}</div>
        <span class="chip mute">${user.name}</span>
        <span class="avatar">${user.initial}</span>
      </header>
    `;
  }

  function chrome(url) {
    return `
      <div class="chrome">
        <div class="dot"></div><div class="dot"></div><div class="dot"></div>
        <div class="url">${url}</div>
      </div>
    `;
  }

  // Returns <aside>+<main class="main"> wrapper — caller provides main content.
  function withNav(activeKey, mainHtml, opts={}) {
    return `
      ${sidebar(activeKey, opts)}
      ${topbar(activeKey, opts)}
      <div class="main">${mainHtml}</div>
    `;
  }

  return { sidebar, topbar, chrome, withNav };
})();
