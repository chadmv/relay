---
title: A pagination error on the revoked-workers fallback path is reported as "no worker found with hostname"
type: bug
status: open
created: 2026-08-29
priority: low
source: Phase 4 correctness and security lenses while fixing bug-2026-08-27-python-sdk-fetch-all-has-no-termination-stops
---

# A pagination error on the fallback path is reported as a miss

## Summary
`resolveWorkerIDIn` tries `/v1/workers` then `/v1/workers/revoked`. An error on the PRIMARY path is
fatal; an error on a FALLBACK path `break`s and falls through to `no worker found with hostname %q`.
That soft rule was written for a 403 - the revoked list is admin-only and a non-admin should see the
primary list's miss rather than an auth error about an endpoint they did not ask for. It now also
swallows a pagination error, which is a wrong diagnosis rather than merely a terse one.

## Repro / Symptoms
Since `FetchAllPages` grew termination stops, a server that repeats a cursor or drives the walk to
the page cap on `/v1/workers/revoked` makes `relay workers delete <hostname>` print:

```
no worker found with hostname "myhost"
```

The operator is told the worker does not exist when the client in fact refused to page the list.

## Context
Found while closing [[bug-2026-08-27-python-sdk-fetch-all-has-no-termination-stops]] and recorded as
a KNOWN CONSEQUENCE in a comment at the branch rather than fixed, deliberately. Two review lenses
independently judged leaving it defensible: the blast radius is one command, the outcome is a
refusal rather than a wrong action, and it strictly improves on the pre-slice behaviour, which was
an unbounded hang. `relayclient.ResponseError` exists with a `StatusCode` field, so the narrowing
below names a real type.

The branch is keyed on the INDEX rather than the path string, and it is `break`, not `continue` - so
a fallback error also skips every LATER path. There are only two paths today, so that is currently
hypothetical, but it sits awkwardly beside the older comment three lines above reasoning about "a
third path's 500 or 403".

## Proposal
Decide which error classes are soft. Plausibly: a `relayclient.ResponseError` carrying 401 or 403
stays soft; everything else becomes fatal. That is a separate judgement with its own `internal/cli`
tests, which is why it was not folded into the slice that found it.

## Acceptance / Done When
- A pagination failure on a fallback path surfaces as itself, not as a hostname miss.
- The 403 case the soft rule exists for still behaves as it does today, pinned by a test.
- The `break`-versus-`continue` question is answered in writing, even if the answer is "break is
  right and here is why".

## Related
- `internal/cli/workers.go` `resolveWorkerIDIn`
- `internal/relayclient/page.go` `FetchAllPages`
- [[idea-2026-08-26-should-worker-subcommands-resolve-revoked-hostnames]] - adjacent but different:
  that item asks whether these subcommands SHOULD resolve revoked hostnames at all, this one is
  about how a failure while doing so is reported
- [[bug-2026-08-27-python-sdk-fetch-all-has-no-termination-stops]] - closed; added the stops
