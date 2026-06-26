---
title: Add relay://workers/{id} MCP resource template
type: idea
status: open
created: 2026-06-25
source: mcp-resource-templates-cache spec (proposed but scoped out of acceptance)
---

# Add relay://workers/{id} MCP resource template

## Summary

The MCP resource-templates cycle (2026-06-25) shipped `relay://jobs/{id}` and `relay://tasks/{id}`
resource templates plus a reusable `readEntityByID` helper (`internal/mcp/resources.go`).
`relay://workers/{id}` was in the original proposal but deliberately scoped out of the accepted
design, leaving a parity gap: an MCP client can read a single job or task by id but not a single
worker. The backend endpoint already exists (`GET /v1/workers/{id}`, `internal/api/server.go` ->
`handleGetWorker` in `internal/api/workers.go`) and the helper is reusable as-is, so this is a small
parity follow-up.

## Proposal

Register one more resource template in `Server.registerResourcesImpl`
(`internal/mcp/resources.go`), mirroring the jobs/tasks templates exactly:

```go
s.mcp.AddResourceTemplate(&mcpsdk.ResourceTemplate{
    URITemplate: "relay://workers/{id}",
    Name:        "worker",
    Title:       "Relay Worker",
    Description: "A single relay worker by id.",
    MIMEType:    "application/json",
}, func(ctx context.Context, req *mcpsdk.ReadResourceRequest) (*mcpsdk.ReadResourceResult, error) {
    return s.readEntityByID(ctx, req.Params.URI, "relay://workers/", "/v1/workers/")
})
```

No new helper is needed - `readEntityByID` already does `url.PathEscape` of the id, GETs through
the `s.do` chokepoint, and maps a backend 404 to `ResourceNotFoundError`. Update README.md to list
the new template alongside jobs/tasks.

## Acceptance / Done When

- `relay://workers/{id}` is registered and reading it returns the single-worker JSON from
  `GET /v1/workers/{id}` with the same content shape as the jobs/tasks templates.
- An unknown worker id returns `ResourceNotFoundError`, an empty/malformed id returns
  `ResourceNotFoundError`, and a traversal/encoded id is contained by the existing
  `url.PathEscape` (covered by a test mirroring the jobs/tasks template tests).
- README.md lists `relay://workers/{id}` among the MCP resource templates.
- No Invariant touched (pure reuse of the existing helper and an existing read endpoint).

## Related

- `internal/mcp/resources.go` - `registerResourcesImpl`, the jobs/tasks templates, and the reusable
  `readEntityByID` helper to call
- `internal/api/server.go` - `GET /v1/workers/{id}` route registration
- `internal/api/workers.go` - `handleGetWorker`, the backing handler
- `README.md` - MCP resources/templates section to update
