---
title: Pre-existing gofmt import-order issues in server.go and main.go
type: bug
status: open
created: 2026-06-03
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

## Related
- `internal/api/server.go`
- `cmd/relay-server/main.go`
- Retro: `docs/retros/2026-06-03-web-frontend-auth.md`
