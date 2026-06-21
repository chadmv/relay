---
date: 2026-06-21
topic: paginate-revoked-workers-list
branch: claude/distracted-allen-9c27c1
pr: "2026-06-21 / paginate-revoked-workers-list"
merge: "2026-06-21 / paginate-revoked-workers-list"
---

# Session Retro: 2026-06-21 - paginate the revoked-workers list

**TL;DR:** Closed `bug-2026-06-05-paginate-revoked-workers-list`. The revoked-workers list fetched only
the first page despite `GET /v1/workers/revoked` being fully cursor-paginated. Added prev/next
pagination mirroring JobsPage, and promoted the `computePageRange` helper to a shared
`web/src/lib/pageRange.ts`. Autopilot batch, item 6 of 7.

## What Was Built

- `web/src/lib/pageRange.ts` (new) - `computePageRange` promoted here from `jobs/pageRange.ts`; the old
  path re-exports it so `jobs/pageRange.test.ts` and JobsPage stay green.
- `web/src/workers/api.ts`, `useRevokedWorkers.ts` - `listRevokedWorkers`/`useRevokedWorkers` take a
  cursor and key on `['workers', 'revoked', cursor]`.
- `web/src/workers/WorkersPage.tsx` - a separate revoked cursor/stack/offsets state, prev/next buttons
  both gated on `isPlaceholderData`, and an `X-Y of total` footer (plain hyphen).
- New hook test + WorkersPage tests (cursor sent, in-flight button guard, footer range, empty case).

## Key Decisions

- **Mirror the sibling, do not reinvent.** The engineer's first step was reading JobsPage + useJobs to
  copy the exact cursor-stack/offsets/in-flight-guard pattern rather than authoring a parallel one. The
  reviewer confirmed `revokedNext`/`revokedPrev` are a line-for-line mirror, including the
  plain-setter-not-functional-updater choice that avoids a StrictMode double-fire.
- **Promote pageRange to lib/ on the second consumer.** When a second page needed `computePageRange`,
  the engineer moved it to `web/src/lib/` (the same lib that now holds the shared `time.ts` from item 3)
  with a re-export shim at the old path - the established "extract on second use" pattern, keeping the
  JobsPage importer and its tests green.
- **Verify the wire shape, do not assume.** The hook's `next_cursor`/`total`/`items` reads were checked
  against the actual Go `handleListRevokedWorkers` + `page[T]` JSON tags, not taken on faith.

## Process Note

- Stateful pagination again -> full adversarial code review (offset-stack correctness, the PR #65
  in-flight guard on BOTH buttons, query-key collision analysis, the pageRange move not regressing the
  just-merged JobsPage). Returned clean.

## Backlog Triage

- No new items. One non-blocking cosmetic nit from review: WorkersPage uses HTML-entity arrows
  (`&larr;`/`&rarr;`) while JobsPage uses literal `←`/`→` glyphs - functionally identical, not worth a
  change. Not filed.
