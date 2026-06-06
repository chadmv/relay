---
title: Pre-existing gofmt import-order issues in server.go and main.go
type: bug
status: closed
created: 2026-06-03
closed: 2026-06-05
priority: low
source: web front end auth slice final review
---

# Pre-existing gofmt import-order issues in server.go and main.go

## Summary
`gofmt -l` flags two files for import-group ordering: `internal/api/server.go` (`pgxpool` listed before `pgtype`) and `cmd/relay-server/main.go` (`relayv1` positioned before `relay/internal/api`). These predate the web front-end branch but surfaced while reviewing files it touched.

## Repro / Symptoms
- `gofmt -l internal/api/server.go cmd/relay-server/main.go` prints both paths (non-empty = unformatted).

## Acceptance / Done When
- `gofmt -l` (or `gofmt -w`) reports no issues for both files.

## Resolution (2026-06-05)
Reordered imports to satisfy gofmt: `pgtype` before `pgxpool` in `server.go`, and `relayv1` moved into path-sorted position in `main.go`. Fixes were applied as surgical edits rather than `gofmt -w` because the working tree checks out with CRLF line endings under `core.autocrlf=true`; `gofmt -w` would have rewritten every line to LF. The committed (LF) blob is now gofmt-clean, so CI on Linux passes. On a Windows checkout `gofmt -l` will still list both files solely due to CRLF terminators, which is a platform artifact, not a formatting defect.

## Related
- `internal/api/server.go`
- `cmd/relay-server/main.go`
- Retro: `docs/retros/2026-06-03-web-frontend-auth.md`
