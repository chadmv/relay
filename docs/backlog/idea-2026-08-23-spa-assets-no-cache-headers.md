---
title: Embedded SPA assets ship with no cache headers, and index.html has no no-cache
type: idea
status: open
created: 2026-08-23
priority: low
source: 2026-08-23 deep roadmap refresh - gaps agent finding
---

# Embedded SPA assets ship with no cache headers, and index.html has no no-cache

## Summary
`embed.FS` files carry a zero mod time, so `http.FileServer` emits no `Last-Modified`/`ETag`, and
`webui.Handler` sets no `Cache-Control` (`web/embed.go:20-45`) - the content-hashed bundle and
eight woff2 fonts re-download on every full page load, while `index.html` (the one file that must
NOT be cached, since it names the hashed bundle) has no explicit `no-cache` either, leaving
stale-shell behavior after a redeploy to intermediary heuristics.

## Context
The tracked [[idea-2026-08-13-no-store-on-json-responses]] covers `writeJSON` JSON responses only;
this is the static-asset half. The fix is asymmetric by design: hashed assets under `/assets/` are
immutable (`Cache-Control: public, max-age=31536000, immutable`), while `index.html` gets
`no-cache` so a redeploy takes effect on the next load.

## Acceptance / Done When
- Hashed assets are served with a long immutable cache header; `index.html` with `no-cache`.
- A test asserts both headers on the embedded handler (RED at HEAD: neither present).

## Related
- `web/embed.go:20-45`, `web/dist/assets/` (hashed filenames)
- [[idea-2026-08-13-no-store-on-json-responses]] - the JSON half of the caching story; decide the two in one review so the policies do not contradict
