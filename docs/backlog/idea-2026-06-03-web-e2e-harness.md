---
title: Add an end-to-end (Playwright) test harness for the web UI
type: idea
status: open
created: 2026-06-03
priority: medium
source: web front end auth slice retro
---

# Add an end-to-end (Playwright) test harness for the web UI

## Summary
The auth slice shipped with thorough Vitest + RTL + MSW unit tests, yet two real integration bugs slipped through: the invented auth response contract (mocks mirrored a shape the real backend never returns) and the missing post-login redirect (no app-level navigation test existed). A browser-driven E2E harness exercising the SPA against a real `relay-server` would catch this class of bug.

## Proposal
Stand up Playwright running the built SPA (or `make web-dev`) against a real `relay-server` + Postgres, covering at least: login -> lands on /jobs, logout -> back to /auth, and the register flows. Decide whether to seed via the bootstrap admin or a test fixture. Likely worth doing once a data-bearing page (Workers/Jobs) exists so the E2E surface is meaningful.

## Related
- Retro: `docs/retros/2026-06-03-web-frontend-auth.md`
- `web/` (Vitest unit tests today; no browser E2E)
