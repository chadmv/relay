// Sign-in and invite-onboarding wireframes
// Updated to match the current auth model in chadmv/relay@master:
//   - POST /v1/auth/login   : email + password (existing accounts only)
//   - POST /v1/auth/register: email + password + name + invite_token
//                             (invite optional iff RELAY_ALLOW_SELF_REGISTER=true)
//   - Tokens are 30-day bearer; password >= 8 chars
//   - PUT  /v1/users/me/password (current + new)
//   - DELETE /v1/auth/token  /  DELETE /v1/auth/tokens   (logout, logout-all)
//   - POST /v1/users/password-reset (admin-only forced reset)
window.Auth = (function(){
  const C = Shell.chrome;

  // V1: Centered sign-in (login only — no invite field on this screen)
  function signInCentered() {
    return `
      <div class="screen">
        ${C('https://relay.studio.dev/login')}
        <div class="screen-body" style="align-items:center; justify-content:center; background:var(--paper);">
          <div style="width: 320px; padding: 20px; border: 2px solid var(--ink); border-radius: 10px; background: var(--paper); box-shadow: 3px 3px 0 var(--ink);">
            <div style="font-family:var(--display); font-size:44px; font-weight:700; line-height:0.9; margin-bottom:4px;">relay<span style="color:var(--accent)">.</span></div>
            <div class="small" style="margin-bottom:14px">Sign in to the coordinator</div>
            <label class="box-label">Email</label>
            <div class="box" style="padding:4px 8px; margin:4px 0 10px; font-size:12px;">you@studio.dev</div>
            <label class="box-label">Password</label>
            <div class="box" style="padding:4px 8px; margin:4px 0 12px; font-size:12px; letter-spacing:2px;">••••••••</div>
            <button class="btn primary" style="width:100%; font-size:14px; padding:6px">Sign in →</button>
            <div class="small" style="text-align:center; margin-top:12px;">
              New here? <span style="color:var(--accent)">Create an account</span> with an invite.
            </div>
            <div class="small mute-txt" style="text-align:center; margin-top:8px; font-size:10px;">
              connected to <span class="mono">${location.host||'relay.studio.dev'}</span>
            </div>
          </div>
        </div>
        <div class="annot" style="top: 24%; right: 4%; font-size:13px;">no token field —<br/>password‑first now</div>
      </div>
    `;
  }

  // V2: Split brand / form — sign-in with link out to register
  function signInSplit() {
    return `
      <div class="screen">
        ${C('https://relay.studio.dev/login')}
        <div class="screen-body">
          <div style="flex:1; background:var(--ink); color:var(--paper); padding: 20px 24px; display:flex; flex-direction:column; justify-content:space-between;">
            <div style="font-family:var(--display); font-size:52px; line-height:0.9; font-weight:700;">relay<span style="color:var(--accent)">.</span></div>
            <div>
              <div style="font-family:var(--display); font-size:32px; line-height:1; margin-bottom:6px;">distributed tasks,<br/>without the yak‑shave.</div>
              <div style="font-family:var(--hand); font-size:12px; opacity:0.7; margin-top:10px;">
                ▪ 14 workers online &nbsp; ▪ 2,341 jobs this month
              </div>
            </div>
            <div style="font-family:var(--hand); font-size:11px; opacity:0.5;">v1.2.0 · self-hosted</div>
          </div>
          <div style="flex:1; padding: 24px 28px; display:flex; flex-direction:column; justify-content:center;">
            <div class="page-title">Sign in</div>
            <div class="page-sub">need an account? <span style="color:var(--accent)">register with an invite →</span></div>
            <div style="margin-top:14px;">
              <label class="box-label">Email</label>
              <div class="box" style="padding:5px 8px; margin:4px 0 10px; font-size:12px;">ada@studio.dev</div>
              <label class="box-label">Password</label>
              <div class="box" style="padding:5px 8px; margin:4px 0 16px; font-size:12px; letter-spacing:2px;">••••••••••</div>
              <button class="btn primary" style="width:100%; padding:6px">Sign in →</button>
              <div class="small mute-txt" style="margin-top:10px; font-size:10px;">
                tokens last 30 days · sessions managed in profile
              </div>
            </div>
          </div>
        </div>
      </div>
    `;
  }

  // V3: CLI-style terminal — mirrors `relay register` flow
  function signInTerminal() {
    return `
      <div class="screen">
        ${C('https://relay.studio.dev/login')}
        <div class="screen-body" style="align-items:center; justify-content:center; background:var(--paper-2); padding:20px;">
          <div style="width: 460px; background:#1f1c17; border:2px solid var(--ink); border-radius:8px; padding:12px 16px; box-shadow: 4px 4px 0 var(--ink); color:#e9e5d7; font-family:'Kalam', monospace; font-size:12px; line-height:1.5;">
            <div style="display:flex; gap:4px; margin-bottom:8px;">
              <span style="width:8px;height:8px;border-radius:50%;background:#ff5f56;"></span>
              <span style="width:8px;height:8px;border-radius:50%;background:#ffbd2e;"></span>
              <span style="width:8px;height:8px;border-radius:50%;background:#27c93f;"></span>
              <span style="margin-left:8px; font-size:10px; opacity:0.5;">~/relay — register</span>
            </div>
            <div><span style="color:#b5e28c">$</span> relay register</div>
            <div style="opacity:0.6">Server URL [http://localhost:8080]: <span style="color:#8db5ff">↵</span></div>
            <div style="opacity:0.6">Email: <span style="color:#fff">ada@studio.dev</span></div>
            <div style="opacity:0.6">Display name [ada@studio.dev]: <span style="color:#fff">Ada Lovelace</span></div>
            <div style="opacity:0.6">Invite token: <span style="color:#fff">rl_invt_1a2b3c…</span></div>
            <div style="opacity:0.6">Password: <span style="color:#fff">••••••••</span></div>
            <div><span style="color:#b5e28c">✓</span> account created · token saved to ~/.relay/config.json</div>
            <div style="margin-top:6px"><span style="color:#b5e28c">$</span> relay login   <span style="opacity:0.5"># existing accounts</span></div>
            <div style="margin-top:8px"><span style="color:#b5e28c">$</span> <span style="animation:blink 1s infinite steps(2)">▊</span></div>
          </div>
        </div>
        <div class="annot" style="top: 24%; right: 4%; font-size:13px;">browser mirrors<br/>register / login split</div>
      </div>
    `;
  }

  // V4: Register / invite redemption (new user, invite-required mode)
  function registerInvite() {
    return `
      <div class="screen">
        ${C('https://relay.studio.dev/register')}
        <div class="screen-body" style="align-items:center; justify-content:center; background:var(--paper);">
          <div style="width: 380px; padding: 20px; border: 2px solid var(--ink); border-radius: 10px; box-shadow: 3px 3px 0 var(--ink);">
            <div class="margin-note" style="margin-top:0">Create your relay account</div>
            <div class="small" style="margin-bottom:14px;">Invited by <b>mira@studio.dev</b> · token expires in <b>69h</b>.</div>

            <label class="box-label">Display name</label>
            <div class="box" style="padding:4px 8px; margin:4px 0 10px; font-size:12px;">Ada Lovelace</div>

            <label class="box-label">Email <span style="color:var(--mute)">(must match invite)</span></label>
            <div class="box filled" style="padding:4px 8px; margin:4px 0 10px; font-size:12px; color:var(--mute)">ada@studio.dev</div>

            <label class="box-label">Invite token</label>
            <div class="box mono dashed" style="padding:4px 8px; margin:4px 0 10px; font-size:11px; color:var(--accent)">rl_invt_1a2b3c4d…</div>

            <label class="box-label">Password <span class="small">(min 8 chars)</span></label>
            <div class="box" style="padding:4px 8px; margin:4px 0 14px; font-size:12px; letter-spacing:2px;">••••••••••</div>

            <button class="btn primary" style="width:100%;">Create account →</button>
            <div class="small mute-txt" style="text-align:center; margin-top:8px; font-size:10px;">
              Already have an account? <span style="color:var(--accent)">Sign in</span>
            </div>
          </div>
        </div>
      </div>
    `;
  }

  // V5: Self-serve register (when RELAY_ALLOW_SELF_REGISTER=true → no invite field)
  function registerOpen() {
    return `
      <div class="screen">
        ${C('https://relay.studio.dev/register')}
        <div class="screen-body" style="align-items:center; justify-content:center; background:var(--paper);">
          <div style="width: 360px; padding: 20px; border: 2px solid var(--ink); border-radius: 10px; box-shadow: 3px 3px 0 var(--ink);">
            <div class="margin-note" style="margin-top:0">Sign up</div>
            <div class="small" style="margin-bottom:14px;">
              Open registration is enabled on this server.
              <span class="tag" style="font-size:10px;">RELAY_ALLOW_SELF_REGISTER</span>
            </div>

            <label class="box-label">Display name</label>
            <div class="box" style="padding:4px 8px; margin:4px 0 10px; font-size:12px;">Ada Lovelace</div>

            <label class="box-label">Email</label>
            <div class="box" style="padding:4px 8px; margin:4px 0 10px; font-size:12px;">ada@studio.dev</div>

            <label class="box-label">Password <span class="small">(min 8 chars)</span></label>
            <div class="box" style="padding:4px 8px; margin:4px 0 14px; font-size:12px; letter-spacing:2px;">••••••••••</div>

            <button class="btn primary" style="width:100%;">Create account →</button>
            <div class="small mute-txt" style="text-align:center; margin-top:8px; font-size:10px;">
              You'll be a non-admin user. Admin promotion is server-side only.
            </div>
          </div>
        </div>
        <div class="annot" style="top: 22%; right: 4%; font-size:13px;">no invite field<br/>when self‑serve is on</div>
      </div>
    `;
  }

  // V6: Change password (signed-in user)
  function changePassword() {
    return `
      <div class="screen">
        ${C('https://relay.studio.dev/profile/password')}
        <div class="screen-body" style="background:var(--paper); padding:20px 24px;">
          <div class="page-title">Change password</div>
          <div class="page-sub">Other sessions will be signed out.</div>
          <div style="max-width:360px; margin-top:14px;">
            <label class="box-label">Current password</label>
            <div class="box" style="padding:4px 8px; margin:4px 0 10px; font-size:12px; letter-spacing:2px;">••••••••</div>

            <label class="box-label">New password <span class="small">(min 8 chars)</span></label>
            <div class="box" style="padding:4px 8px; margin:4px 0 10px; font-size:12px; letter-spacing:2px;">••••••••••</div>

            <label class="box-label">Confirm new password</label>
            <div class="box" style="padding:4px 8px; margin:4px 0 14px; font-size:12px; letter-spacing:2px;">••••••••••</div>

            <div style="display:flex; gap:8px;">
              <button class="btn primary">Update password</button>
              <button class="btn">Cancel</button>
            </div>
            <div class="small mute-txt" style="margin-top:14px; font-size:10px;">
              Sign out everywhere from <span style="color:var(--accent)">Profile → Sessions</span>.
            </div>
          </div>
        </div>
      </div>
    `;
  }

  function render(host) {
    host.innerHTML = `
      <div class="section-intro">
        <h2>Sign in, register & password</h2>
        <p>The auth model is now <b>email + password</b>. Login and register are separate endpoints; new accounts need an <span style="color:var(--accent)">invite token</span> unless the server has <span class="mono">RELAY_ALLOW_SELF_REGISTER=true</span>. Tokens last 30 days. <span class="scribble">Updated to match master.</span></p>
      </div>

      <div class="variations cols-3">
        <div class="variant">
          <div class="variant-label"><span class="num">1</span>Sign in — centered <span class="tag" style="color:var(--ok); border-color:var(--ok)">♥ picked</span></div>
          <div class="variant-note">Login only. Registration moved to its own screen. <b style="color:var(--ok)">Carry to hi-fi.</b></div>
          ${signInCentered()}
        </div>
        <div class="variant">
          <div class="variant-label"><span class="num">2</span>Sign in — split brand</div>
          <div class="variant-note">Same fields, more reassurance for first visit.</div>
          ${signInSplit()}
        </div>
        <div class="variant">
          <div class="variant-label"><span class="num">3</span>CLI mirror <span class="tag">nerdy</span></div>
          <div class="variant-note">Echoes <span class="mono">relay register</span> / <span class="mono">relay login</span>.</div>
          ${signInTerminal()}
        </div>
      </div>

      <div class="variations cols-3">
        <div class="variant">
          <div class="variant-label"><span class="num">4</span>Register — invite required</div>
          <div class="variant-note">Default mode. Email is locked when invite is bound to an address.</div>
          ${registerInvite()}
        </div>
        <div class="variant">
          <div class="variant-label"><span class="num">5</span>Register — self-serve</div>
          <div class="variant-note">When the server allows open sign-up. No invite field shown.</div>
          ${registerOpen()}
        </div>
        <div class="variant">
          <div class="variant-label"><span class="num">6</span>Change password</div>
          <div class="variant-note">Profile screen. Revokes other sessions on success (per <span class="mono">PUT /v1/users/me/password</span>).</div>
          ${changePassword()}
        </div>
      </div>

      <div class="variations cols-1">
        <div class="variant" style="align-self:stretch;">
          <div class="variant-label"><span class="num">·</span>Notes & edge cases (updated)</div>
          <div class="box filled" style="padding:14px 16px; font-size:13px; line-height:1.5;">
            <div style="display:grid; grid-template-columns:1fr 1fr; gap:18px;">
              <div>
                <div class="margin-note" style="margin-top:0">Auth model</div>
                <ul style="padding-left:18px; margin:6px 0 0;">
                  <li><b>Login</b> = email + password only. No invite field on the login screen.</li>
                  <li><b>Register</b> = email + name + password + (optional) invite token.</li>
                  <li>Invite mode is the default; self-serve only when <span class="mono">RELAY_ALLOW_SELF_REGISTER=true</span>.</li>
                  <li>Password must be ≥ 8 characters (server-enforced).</li>
                  <li>Bearer tokens last <b>30 days</b> — surface this near the sign-in button.</li>
                </ul>
              </div>
              <div>
                <div class="margin-note" style="margin-top:0">Errors & states</div>
                <ul style="padding-left:18px; margin:6px 0 0;">
                  <li><b>401</b> on login → generic "invalid email or password" (server doesn't distinguish unknown user).</li>
                  <li><b>409</b> on register → "email already registered" — offer Sign in link.</li>
                  <li><b>400</b> "invite expired" / "invite already used" / "invite not valid for this email" — show inline under token field.</li>
                  <li>Archived users get the same generic 401 → no UI to distinguish.</li>
                  <li>Login is rate-limited (<span class="mono">RELAY_LOGIN_RATE_LIMIT</span>, default 10/min/IP) — show a "try again in Xs" hint on 429.</li>
                </ul>
              </div>
              <div>
                <div class="margin-note" style="margin-top:0; color:var(--ok); border-color:var(--ok);">Token & session UX</div>
                <ul style="padding-left:18px; margin:6px 0 0;">
                  <li>Invite token field: monospace, paste-friendly, masks middle chars after paste.</li>
                  <li>Profile → Sessions: list all live tokens; "Sign out everywhere" hits <span class="mono">DELETE /v1/auth/tokens</span>.</li>
                  <li>Change password silently revokes all other tokens — call this out in the helper text.</li>
                  <li>30-day expiry → silent re-login prompt when token has &lt;3 days left.</li>
                </ul>
              </div>
              <div>
                <div class="margin-note" style="margin-top:0">Admin-adjacent flows</div>
                <ul style="padding-left:18px; margin:6px 0 0;">
                  <li>Admin password reset (<span class="mono">POST /v1/users/password-reset</span>) lives in <b>Admin → Users</b>, not here. Forces the target to re-login everywhere.</li>
                  <li>Bootstrap admin (<span class="mono">RELAY_BOOTSTRAP_ADMIN</span>) is a server env var — no first-run wizard in the UI; the account already exists by the time anyone hits the login screen.</li>
                  <li>Archived accounts can't log in (silent 401) — no "your account is disabled" message by design.</li>
                </ul>
              </div>
            </div>
          </div>
        </div>
      </div>
    `;
  }

  return { render };
})();
