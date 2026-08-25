---
title: A first boot that commits the token but fails to persist it locks the hostname out permanently
type: idea
status: open
created: 2026-08-25
priority: medium
source: 2026-08-25 auto-enroll-guards slice - a window the create-only rule opened, disclosed but not closed
---

# A first boot that commits the token but fails to persist it locks the hostname out permanently

## Summary

`autoEnrollAndRegister` commits the `workers` row **and** its `agent_token_hash` before
`finishRegister` sends the `RegisterResponse`, and well before the agent persists the token
(`internal/agent/agent.go`, `a.creds.Persist`). If the stream dies in between, or the persist fails -
a read-only or full state directory on first boot is the realistic case - the hostname is claimed with
a live credential the agent never received.

That machine is then refused by **both** paths: by auto-enroll because a row exists, and by the
enrollment-token path because `agent_token_hash` is non-NULL. Recovery is revoke-then-token, which
needs an admin.

## Context

**This window is new.** Before the 2026-08-25 create-only rule, the agent's retry self-healed: the
token-less register hit `UpsertWorkerByHostname`'s `DO UPDATE`, rotated the token, and the second
attempt persisted it. The guard that closed hostname takeover is what removed the idempotent retry.

The trade is still right - the alternative is the takeover bug - but the cost was disclosed in README
rather than closed, and it is a different case from the one the spec's cost list names ("an agent that
lost its state directory"). This is a *first* boot that never completed.

## Proposal

Two levels, and they are not alternatives:

- **Narrow it cheaply.** Have the agent persist the token before anything else that can fail with it
  in hand. This shrinks the window without closing it - the stream can still die before the response
  arrives.
- **Close it properly.** A server-side notion of an unconfirmed first registration: a row created by
  auto-enroll is provisional until the agent proves it holds the token (the first successful
  reconnect), and a provisional row does not claim the hostname against a later token-less attempt
  from the same source.

**The trap to decide deliberately:** "a row whose agent never confirmed" is exactly the shape of an
attacker's junk row. Making provisional rows re-claimable is the recovery a stranded operator wants
and also a way to recycle a claimed hostname. Whatever ships must state which it is optimising for.
Note the overlap with the TTL reaper item - one mechanism may serve both.

## Acceptance / Done When

- A first boot whose token persist fails can retry successfully without an admin, OR the case is
  refused deliberately with a stated reason and a documented recovery that does not need an admin.
- The chosen mechanism cannot be used to reclaim a hostname whose agent DID confirm.
- README's auto-enrollment cost paragraph is updated from "disclosed" to whatever ships.

## Related

- `internal/worker/handler.go` (`autoEnrollAndRegister`, `finishRegister`), `internal/agent/agent.go`
- [[idea-2026-08-25-ttl-reaper-for-never-reconnected-workers]] - same population, possibly one mechanism
- [[bug-2026-08-25-no-worker-delete-at-any-layer]] - the admin-side recovery this avoids needing
- `docs/retros/2026-08-25-auto-enroll-guards.md`
