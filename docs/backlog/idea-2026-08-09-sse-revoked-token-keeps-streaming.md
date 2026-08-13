---
title: A revoked token keeps receiving SSE events for the life of the held connection
type: idea
status: open
created: 2026-08-09
priority: low
source: Phase 4 review of the SSE task-log publishing iteration (2026-08-09)
---

# A revoked token keeps receiving SSE events for the life of the held connection

## Summary
`GET /v1/events` authenticates once, at connect, via the `auth(...)` middleware. The handler then
holds the connection open indefinitely, so a token that is revoked or expires mid-stream keeps
receiving events until the client disconnects. Revocation is effective against every other endpoint
immediately and against this one not at all.

## Context
Pre-existing and inherent to how long-lived streaming interacts with per-request auth, but noted
during the 2026-08-09 SSE task-log publishing review because that work makes the streamed content
more sensitive: the stream now carries `task_log` frames, i.e. raw subprocess output, rather than
only `{id, status}` status payloads.

Concretely, the paths that revoke access today - `handleArchiveUser` deleting the target's API
tokens, and the admin password reset deleting all of a user's tokens - each assume that dropping the
token ends the user's reach. For a held `/v1/events` connection it does not.

**Update, 2026-08-13:** the profile Sessions tab adds a third revocation path and the first
self-service one - `DELETE /v1/auth/tokens` destroys every token the caller has, including the one
that authenticated any stream they currently hold. So the user most likely to hit this is now the
user who just deliberately signed themselves out everywhere, which raises how surprising the
behaviour is without changing its severity.

## Proposal
Options, roughly in increasing cost:

- **Re-validate periodically.** The handler already has a loop; re-check the token every N seconds
  (or every K events) and close the stream when it no longer resolves. Cheap, bounded staleness,
  one extra query per interval per connection.
- **Cap connection lifetime.** Close the stream after a fixed duration and require the client to
  reconnect. Simple and it bounds exposure without a per-connection auth query, but it churns
  every consumer and interacts with the "no `Last-Event-ID` resume" property documented in the
  README.
- **Revocation signal.** Have the revocation paths notify held connections for that user - the
  broker already exists as a fan-out mechanism. Most precise and most work; needs care to avoid
  coupling token lifecycle to the event broker.

Whichever is chosen, decide it against a stated tolerance for staleness rather than picking the
cheapest by default. Note that the SPA reconnects on its own, so a lifetime cap may be nearly free
in practice for the only current consumer - worth checking before assuming it is disruptive.

## Acceptance / Done When
- A token revoked mid-stream stops receiving events within a stated, documented bound.
- The bound is documented in the README's events section alongside the existing drop/reconnect
  semantics.
- No per-event auth query on the hot path (log frames can be high-frequency, so a check per event
  would put a DB round trip on the fan-out path).

## Related
- Source: `internal/api/events.go` (`handleEvents`), `internal/api/server.go` (the `auth(...)`
  wrapping of `GET /v1/events`), `internal/api/users.go` (the archive and password-reset paths that
  delete tokens)
- Context: `docs/superpowers/specs/2026-08-09-sse-task-log-publishing.md`
- Adjacent: [[bug-2026-08-09-tasklog-append-unauthenticated-epoch-zero]] (the write-side sibling
  found in the same review)
- The client-side half of the same revocation story, and the other consumer of the 401 fire site
  (`web/src/lib/api.ts:127-129` is `apiStream`'s): [[bug-2026-08-13-cross-generation-401-clears-a-new-session]]

## Notes
Filed as an idea rather than a bug because the current behavior is a conscious consequence of
per-request auth on a long-lived stream, not an oversight in a specific handler, and because the
right bound is a product decision rather than an obvious fix.
