---
title: "`relayclient.PageEnvelope` reads a dropped `next_cursor` as drained, and Go cannot fix it the way Python did"
type: bug
status: open
created: 2026-08-29
priority: medium
source: G2 of docs/superpowers/specs/2026-08-29-python-sdk-strict-page-envelope.md, declined in writing at that spec's gate
---

# `relayclient.PageEnvelope` reads a dropped `next_cursor` as drained

## Summary
The Python SDK's `Page` had `next_cursor: str = ""`, so a 200 whose envelope omitted the key
decoded to the empty string - which is the drained signal - and every walk returned page 1 and
reported success. That is closed on the Python side by making the field required.

`relayclient.PageEnvelope` has the identical latent defect and it is **not** closed:

```go
type PageEnvelope[T any] struct {
	Items      []T    `json:"items"`
	NextCursor string `json:"next_cursor"`
	Total      int64  `json:"total"`
}
```

`encoding/json` leaves an absent key at the zero value, so a dropped `next_cursor` is `""`, and
`FetchAllPages` reads `resp.NextCursor == ""` as drained on the line its own comment calls the
drained return. A dropped `total` is `0`, which the walk reports back to the caller as the row
count.

## Why the Python remedy does not port
Go has no required-field mechanism in `encoding/json`. There is no declaration that makes an absent
key an error the way a pydantic field without a default does. The remedy is a **type change** -
`NextCursor *string` plus an explicit nil check, or a custom `UnmarshalJSON` - and that is a
different shape of change from the two-line Python one, which is why the spec declined it rather
than mirroring it.

## The decision the fix has to make first
`internal/mcp` decodes `PageEnvelope[map[string]any]` **directly** at five sites and republishes
`next_cursor` to the model as part of its tool output. Those are not walks; they are single-page
reads whose output contract includes the cursor. A fix has to say what an absent key means there -
error, or an explicitly-null cursor in the tool result - and that is an MCP output-contract
decision, not a decoding one.

## Consumers (measured 2026-08-29, non-test)
- **`FetchAllPages`, six call sites**: `internal/cli/admin_users.go`, `jobs.go`, `reservations.go`,
  `schedules.go`, and `workers.go` twice. The second `workers.go` site is `resolveWorkerIDIn` with
  `userLimit=0`, which `relay workers get|enable|disable|delete <hostname>` reach **implicitly** -
  `--limit` cannot bound them, and the user never asked for a walk.
- **Direct `PageEnvelope` decodes, eight sites**: `internal/cli/admin_users.go` x3 and
  `internal/mcp/{jobs,reservations,resources,schedules_read,workers}.go` x1 each.

## Acceptance / Done When
- An envelope omitting `next_cursor` does not decode into a page that reports the list drained.
- The MCP output-contract decision above is made explicitly and written down, not left implicit in
  whatever the decoder happens to do.
- Reverting the fix turns a named test RED, and that test omits exactly one key so `next_cursor`
  and `total` stay separately pinned - the Python slice found that a fixture omitting both makes
  the two declarations indistinguishable.

## Related
- `internal/relayclient/page.go` `PageEnvelope`, `FetchAllPages`
- [[bug-2026-08-27-python-sdk-page-cursor-defaults-to-drained]] - the same defect, closed
- `docs/superpowers/specs/2026-08-29-python-sdk-strict-page-envelope.md` section 8

## Priority note
Medium rather than high, and the reason is the consumer. The Python SDK is imported by service
loops that act on a list; `PageEnvelope`'s consumers are an operator at a terminal, who sees a
short list, and the MCP tools, whose output a model reads. A silent truncation is worse in a loop
than at a prompt - but `resolveWorkerIDIn` is the exception, since a truncated walk there makes
`relay workers delete <hostname>` report "no worker found" for a worker that exists.
