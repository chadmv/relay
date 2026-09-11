---
title: The Python SDK's Sync docstring enumerates the server-side sibling rules and is now one short
type: idea
status: open
created: 2026-09-10
priority: low
source: Found by the correctness lens on the source.sync entry-count bound (PR #207)
---

# The Python SDK's Sync docstring enumerates the server-side sibling rules and is now one short

## Summary

`Sync`'s docstring in `python/src/relay/models.py` says every rule needing an entry's SIBLINGS
belongs to the server, and then enumerates them. The sync entry-count bound is a new member of that
set, so the enumeration reads as complete and is not.

## Context

The enumeration was DELETED rather than extended when the bound shipped, which is this project's
deletion-first remedy for prose - so the docstring is no longer false. What remains is that the
general sentence alone is slightly thinner than it could be, and the next sibling rule will face the
same choice.

The Python model does mirror the LOWER end of the range (`_at_least_one_sync`), so a caller pre-flights
the empty case locally and gets a server 400 for the over-count case. That asymmetry is deliberate and
matches the policy that count ceilings belong to the server.

**Do not re-add an enumeration and do not copy the number.** A count in the SDK is a second source of
truth for a bound the server owns, and the Go/Python pair has no lockstep guard.

## Proposal

One clause generalising what the server owns, with no list and no number. Or leave it; this is
recorded mainly so the next person does not "fix" it by re-adding the census.

## Related

- `python/src/relay/models.py` (`Sync`)
- [[idea-2026-08-27-sdk-copies-of-server-vocabularies-are-unregistered]]
- [[idea-2026-09-04-nothing-guards-the-go-python-job-spec-type-pair]]
