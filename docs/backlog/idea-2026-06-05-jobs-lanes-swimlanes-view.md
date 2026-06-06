---
title: Jobs Lanes (swimlanes-by-status) view
type: idea
status: open
created: 2026-06-05
source: jobs-list-frontend retro (2026-06-05)
---

# Jobs Lanes (swimlanes-by-status) view

## Summary
Add the Lanes (swimlanes-by-status) view from the design handoff to the Jobs page. Each lane is a separate `GET /v1/jobs?status=<s>&limit=<perLane>` call, capped per-lane (default 10, min 3, max 50), with a "+N more →" overflow linking to the table filtered by that status.

## Context
Deferred from the first jobs-list slice (Table view only). The hi-fi `HoloLanes` component in `design_handoff_relay_holo/hifi3-holo-pages.jsx` is the reference.

## Related
- `web/src/jobs/` feature
- `design_handoff_relay_holo/reference/screens/jobs-list.js`
- `docs/retros/2026-06-05-web-jobs-list.md`
