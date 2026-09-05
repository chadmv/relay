---
title: Nothing keeps Go's jobspec.SyncEntry and the Python SDK's Sync in step, and a guard under python/** could not fire
type: idea
status: open
created: 2026-09-04
priority: medium
source: The sync-spec exclusion paths slice (PR #203), which added the third field to that pair and named the gap rather than papering over it
---

# Nothing keeps Go's `jobspec.SyncEntry` and the Python SDK's `Sync` in step

## Summary

`internal/jobspec/jobspec.go`'s `SyncEntry` and `python/src/relay/models.py`'s `Sync` are a
hand-maintained pair. Both carry three fields today (`path`, `rev`, `exclude`), and nothing asserts
that. A Go commit adding a fourth cannot redden anything under `python/`, so the SDK silently stops
being able to express a spec the server accepts.

## Context

PR #203 added `exclude` to both sides and reported the gap rather than closing it.

**The obvious guard cannot work, and that is the interesting part.**
`.github/workflows/python.yml` filters on `paths: - "python/**"`, so a guard living in the Python
test suite would not run on the Go commit that breaks the pair. Per CLAUDE.md's rule that a guard
must live in a lane that runs on the commits that can break its property, this one has to sit on the
**Go** side and read `python/src/relay/models.py` as data.

**The technique already exists in this repo, one type over.**
`TestSourceSpecToProto_SyncEntryArityMatches` (`internal/scheduler/source_proto_test.go`) pins the
Go-to-proto half of exactly this pair by reflecting over both structs and comparing
`NumField`, with a failure message naming the hand-written copier. It runs in the default lane. What
is missing is the same assertion against the Python model - which needs a parse of a `.py` file
rather than reflection, and that is the whole design question.

**This does not subsume [[idea-2026-08-27-sdk-copies-of-server-vocabularies-are-unregistered]], and
that item does not subsume this.** That one is about copies of server *value vocabularies* - job
statuses, event types - and its remedy is to register the SDK copy in the existing lockstep guard's
site list and failure message. This one is about a *struct's field set*, and its remedy is an arity
assertion. Same class, different subject, different technique; they should probably settle on one
answer for how a Go-side guard reads Python, which is a reason to schedule them near each other
rather than to merge them.

Adjacent and already filed:
[[bug-2026-09-04-python-client-template-regex-has-drifted-from-go]] is one instance of the pair
drifting - Go was tightened to refuse a leading hyphen and the SDK pattern was not - and closing it
does not close the class.

## Proposal

Sketch only; the mechanism is the work.

A Go test that reads `python/src/relay/models.py` and asserts the field set of `Sync` matches
`jobspec.SyncEntry`'s json tags. Options, cheapest first:

1. **A regex or line scan** over the class body. Cheap, and brittle in the way this project has been
   bitten by before - it is a parse of another language's source by pattern match.
2. **A generated manifest**: the SDK emits its model field sets to a small JSON file checked into
   the repo, and the Go guard compares against that. Moves the brittleness to a build step that
   fails loudly.
3. **Accept that no real check is affordable and name the consumers instead.** The vocabularies item
   argues this explicitly: a guard that cannot see the producer is worth less than one that names
   the consumers it cannot check, and an explicit "these copies exist and are not verified here"
   line still beats silence.

Whichever is taken, decide it for the pair as a whole rather than for `SyncEntry` alone - `Job`,
`Task` and `Source` have the same shape.

## Acceptance / Done When

- Adding a field to `jobspec.SyncEntry` without adding it to Python's `Sync` turns a test RED, in a
  lane that runs on the Go commit - or, if option 3 is taken, the decision and the unverified
  consumers are written down where a Go author will read them.
- The guard names the pair and the hand-maintained copy in its failure message, as the proto arity
  guard does.
- The choice applies to the other Go/Python model pairs, not just this one.

## Related

- `internal/jobspec/jobspec.go` (`SyncEntry`), `python/src/relay/models.py` (`Sync`)
- `internal/scheduler/source_proto_test.go` (`TestSourceSpecToProto_SyncEntryArityMatches`) - the
  precedent, on the Go-to-proto half
- `.github/workflows/python.yml` - the `paths` filter that rules out the obvious placement
- [[idea-2026-08-27-sdk-copies-of-server-vocabularies-are-unregistered]] - same class, different
  subject and remedy; settle the "how does a Go guard read Python" question once across both
- [[bug-2026-09-04-python-client-template-regex-has-drifted-from-go]] - a live instance of the pair
  drifting
- [[idea-2026-08-27-hand-written-to-spec-dict-mappers-need-an-arity-check]] - an arity gap INSIDE
  the SDK (model fields vs serialized keys), not across the language boundary
