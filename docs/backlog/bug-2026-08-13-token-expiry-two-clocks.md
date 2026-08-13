---
title: Token expiry is evaluated against two different clocks - Go's in BearerAuth, Postgres' in the sessions list
type: bug
status: open
created: 2026-08-13
priority: low
source: Phase 4 of the 2026-08-13-web-enabler-list-endpoints slice; pre-existing pattern, newly observable through GET /v1/auth/tokens
---

# Token expiry is evaluated against two different clocks

## Summary

Whether an API token is expired is decided in two places, against two different clocks, with no
relationship between them:

- **Authentication** uses the Go process clock:
  `row.ExpiresAt.Valid && row.ExpiresAt.Time.Before(time.Now())` (`internal/api/middleware.go:32`).
- **The sessions list** uses the database clock: `(expires_at IS NULL OR expires_at > NOW())`
  (`internal/store/query/tokens.sql:77`, `:97`, `:110`).

`relay-server` and Postgres are separate processes and, in any real deployment, separate hosts. Under
clock skew the two answers disagree for tokens inside the skew window.

## Repro / Symptoms

Let the app host's clock be ahead of the database's by `d`. For a token expiring within `d`:

- The token **authenticates** while the list **omits** it. A user opening the Sessions tab sees a
  session count one lower than reality, and the session they are currently using may be missing from
  the page listing their sessions - including, in the worst arrangement, the row flagged
  `is_current`.
- With the skew in the other direction the token is **listed** and then **401s** on the next request,
  so the tab shows a live session that is already dead.

Neither is dramatic and both self-correct as soon as the token is clearly on one side of the
boundary. The window equals the skew, so on an NTP-synchronized fleet it is milliseconds; on a
virtual machine that has just resumed from suspend it can be minutes.

## Context

**Pre-existing as a pattern, newly observable.** The two-clock split has existed since
`api_tokens.expires_at` was introduced; before `GET /v1/auth/tokens` there was no second reader to
disagree with `BearerAuth`, so it could not be seen. Any future reader of the column inherits the
same choice, which is the reason to write the rule down now rather than after the third one.

**Not a security issue.** Both clocks fail in the same direction with respect to authority: an
expired token still 401s at `BearerAuth` regardless of what the list says, and being absent from a
list grants nothing. The defect is a display inconsistency and a confusing one, not an authorization
gap.

Note that `expires_at` is written with the Go clock too: `issueToken` stamps `time.Now().Add(...)`
(`internal/api/auth.go`), so the stored value and the authentication check agree with each other and
the SQL predicate is the odd one out.

## Proposal

Pick one clock for the column and apply it everywhere. Two shapes:

- **A. Make the list use the Go clock.** Pass `time.Now()` from the handler as a query argument and
  compare `expires_at > sqlc.arg(now)` in all three statements
  (`internal/store/query/tokens.sql:77,97,110`). This aligns the read path with both the write path
  (`issueToken`) and the authoritative check (`BearerAuth`), and it makes the list's answer exactly
  the one `BearerAuth` would give. Recommended.
- **B. Make `BearerAuth` use the database clock.** Push the expiry predicate into
  `GetTokenWithUser` so the row simply does not come back when expired. Fewer moving parts and one
  round trip saved, but it changes the 401 message from `token expired` to `invalid token`, which is
  a deliberate distinction today, and it moves an authentication decision into SQL where it is
  harder to see.

Whichever is chosen, record the rule at both sites so the next reader of `expires_at` does not
re-introduce the split. The same question applies to `invites.expires_at` and
`agent_enrollments.expires_at`, which should be checked for the same divergence while this is open.

## Acceptance / Done When

- One clock decides `api_tokens` expiry, and both the authentication path and the list path use it.
- A test proves the discriminating case rather than the easy one: a token expiring inside a
  simulated skew window must be listed if and only if it authenticates. Asserting "an expired token
  is not listed" with a clearly-past timestamp passes both before and after the fix and is vacuous.
- `invites.expires_at` and `agent_enrollments.expires_at` are audited for the same split, and either
  fixed or recorded as consistent.
- The chosen rule is stated in a comment at both the write site and the read sites.

## Related

- Source: `internal/api/middleware.go:32-35` (the Go-clock check),
  `internal/store/query/tokens.sql:74-110` (the three Postgres-clock predicates),
  `internal/api/auth.go` (`issueToken`, which stamps with the Go clock)
- Found during: `docs/retros/2026-08-13-web-enabler-list-endpoints.md`
- The endpoint that made it observable, and the reason the NULL arm of that predicate exists:
  `docs/superpowers/specs/2026-08-13-web-enabler-list-endpoints.md` (decision 10)
- Same table, adjacent hygiene: [[idea-2026-08-13-reap-expired-invites-and-tokens]]

## Notes

Filed at low deliberately. It is real, it is cheap to fix, and it will never be anybody's top
priority - which is exactly the kind of item that is worth having written down with its
discriminating test named, so whoever next opens `tokens.sql` can close it in the same sitting.
