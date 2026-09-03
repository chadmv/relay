---
title: react-router 7.16.0 ships five advisories in the production bundle, one a high open redirect
type: bug
status: open
created: 2026-09-03
priority: high
source: fan-in of the 2026-09-02 web-frontend batch
---

# react-router 7.16.0 ships five advisories in the production bundle, one a high open redirect

## Summary
npm audit at lane T's HEAD reported react-router and react-router-dom 7.16.0 carrying five advisories that reach the production bundle, including GHSA-wrjc-x8rr-h8h6 (open redirect, high). The fix versions are inside the package.json ^7.1.1 range, so this is a lockfile bump with no API change. Lane T (PR #177) was scoped to vite, vitest and plugin-react and left it.

## Context
The item lane T closed had inherited an audit count of 5 that nobody re-measured for three months; the count at HEAD was 11, six of them unrelated to the tooling bump.

## Proposal
npm update react-router react-router-dom, confirm the advisories clear, run the SPA and e2e suites.

## Related
- web/package.json, web/package-lock.json
