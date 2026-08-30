---
title: Every list handler's limit+1 over-fetch and buildPage routing is a load-bearing universal pinned by nothing
type: idea
status: open
created: 2026-08-30
source: comment-policy retrofit; page.go and client.py condensed the census that carried it
---

# Every list handler's limit+1 over-fetch and buildPage routing is a load-bearing universal pinned by nothing

## Summary
Both SDK page-walk caps (`internal/relayclient/page.go`, `python/src/relay/client.py`) document that
reaching the cap on a LIST walk means the server is misbehaving. That conclusion rests on a
universal: every list query fetches `LIMIT page_limit + 1` rows and every list handler emits its
cursor through `buildPage`, so an honest list drains at its last full page. The property is split
across two surfaces (the SQL `+ 1` in each `internal/store/query/*.sql` list query, and `buildPage`'s
`hasMore` trim in `internal/api/pagination.go`), and no test guards the complement: a future list
handler that pages without the over-fetch makes a full-multiple-length list hit the cap on an honest
server.

## Context
The 2026-08-30 comment retrofit struck the census form of this claim from both files per the new
CLAUDE.md comment policy; the policy's remedy for a load-bearing universal is a structural guard.

## Acceptance / Done When
- A structural test pins the property across both surfaces: every list query under
  `internal/store/query/` that takes `page_limit` carries `+ 1`, and every list handler that returns
  a paged envelope routes through `buildPage` (or the guard states an explicit allow-listed exception).

## Related
- [[bug-2026-08-29-go-pageenvelope-reads-a-dropped-next-cursor-as-drained]]
- [[idea-2026-08-27-cli-lane-never-crosses-a-list-page-boundary]]
- internal/relayclient/page.go, python/src/relay/client.py (the two cap comments)
