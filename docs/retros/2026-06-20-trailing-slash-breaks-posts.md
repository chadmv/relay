---
date: 2026-06-20
topic: trailing-slash-breaks-posts
branch: claude/dazzling-easley-b7e79e
pr: "2026-06-20 / trailing-slash-breaks-posts"
merge: "2026-06-20 / trailing-slash-breaks-posts"
---

# Session Retro: 2026-06-20 - Trailing slash breaks POSTs

**TL;DR:** Closed `bug-2026-06-10-trailing-slash-breaks-posts` with a one-line
fix: `relayclient.NewClient` now normalizes the base URL via
`strings.TrimRight(serverURL, "/")`. A trailing slash in `RELAY_URL` (easy to
type at the `relay login` prompt) produced `//v1/...`, which the server
301-redirected; Go's `http.Client` downgrades a POST to a body-less GET on a
301, which then 405s - surfacing as an opaque "request failed (405)" on login.

## What Was Built

- `internal/relayclient/client.go:33`: normalize the base URL at the single
  constructor, so all callers (login, register, mcp/server, `cfg.NewClient`) are
  covered. Backed by a table test (no/single/multiple trailing slashes) and a
  behavioral test asserting a POST through a trailing-slash base reaches the
  clean path and stays a POST (not redirect-downgraded to GET).

## Key Decisions

- Fix at the constructor rather than at each call site: one normalization point
  covers every consumer and keeps the contract in one place.

## Backlog Triage

- None. One-line fix; nothing surfaced. No new items filed.
