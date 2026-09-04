---
title: classifyP4Error still misclassifies when p4 echoes the offending path into its own stderr
type: bug
status: open
created: 2026-09-03
priority: medium
source: Phase 4 security lens and round-4 re-verify of the prepare-failure-visibility batch, 2026-09-03
---

# classifyP4Error still misclassifies when p4 echoes the offending path into its own stderr

## Summary

`classifiableText` excludes the command args from classification - it returns `""` when no
`*p4CommandError` is in the chain, and the type keeps args, underlying error and stderr in separate
fields - so a depot path can no longer reach the matcher through argv. **stderr is still classified
by design, and p4 routinely quotes the failing path back in its error text.** A job spec naming a
stream or sync path such as `//depot/disk full/...` therefore still produces an out-of-disk
diagnosis in the error classes where p4 echoes the path.

## Repro / Symptoms

Reproduced 2026-09-03 against the shipped classifier. With args `["-c", "relay_host_ab12", "sync",
"//depot/disk full/...#head"]` and a stderr of `Path '//depot/disk full/...' is not under client's
root.`, `classifyP4Error` returns the out-of-disk message. The paired negative - the same args with
a stderr that does not echo the path - correctly passes through unclassified, which is what the
args exclusion bought.

Reachable by any authenticated user who can `POST /v1/jobs`: `validateSourceSpec` constrains a
depot path to a `//` prefix and an under-stream check, with no character set and no space ban.

## Context

Severity is lower than when this class was first found, because the batch removed the destructive
remedy. The message no longer tells an operator to raise `RELAY_WORKSPACE_MIN_FREE_GB`, which
evicts other tenants' warm workspaces and, on a deployment where it is unset, turns the sweeper on
for the first time (`cmd/relay-agent/main.go` gates the sweeper on `maxAge > 0 || minFreeGB > 0`).
What remains is a misleading diagnosis rather than a remedy that favours the forger.

**The guard hazard that hid this class for three review rounds:** every fixture in
`TestClassifyP4Error` modelled a producer shape production never emits - hand-written
`fmt.Errorf("p4 sync: %w", errors.New("exit status 1 (stderr: ...)"))` - until `fakeRunner` was
changed to return the production error type. Any new test here must be built through the production
constructor, or it will pin nothing for the same reason.

## Proposal

Options, cheapest first, none of them complete on its own:

1. **Suppress when the phrase also appears in the args.** Narrows it further and fails closed into
   "no classification" rather than a wrong one. Does not close it: an attacker can seek phrases
   that reach stderr by routes other than argv - a client name, a label, a depot file literally so
   named.
2. **Classify on p4 exit codes or tagged output (`p4 -ztag`)** rather than substring matching over
   text an attacker can influence. This is the structural fix and the only one that closes the
   class; it is also the largest, since every existing classification is a substring match.

## Acceptance / Done When

- A job spec whose depot path contains a classification phrase does not produce that
  classification, in the error classes where p4 echoes the path into stderr.
- The discriminating test is built through the production error constructor, not a hand-written
  fixture.

## Notes

Measured against a live p4d r25.2 while reviewing the client-path slice. Three corrections and one
new channel; the item's diagnosis stands, its repro and one of its remedies do not.

**The stated repro does not fire.** It assumes a stream name carrying a classification phrase
survives into stderr. p4 normalizes it first:

    p4 client -o -S "//test/disk full" relay_probe
    Stream '//test/disk_full' doesn't exist.

So the `client -o -S` channel is closed by p4's own name normalization, and a fix aimed at it would
pin nothing.

**The live channels are the sync/resolve path and the local path.** `validateSourceSpec` applies no
character set to a sync path, and an `@` in one makes p4 echo the tail with a non-zero exit:

    p4 -c <client> changes -m1 "//<client>/a@no space left on device/...#head"
    Unintelligible revision specification 'no space left on device/...#head'
    exit=1

Fed through the production constructor, that classifies as out-of-disk. A second channel nothing
had enumerated: `Can't clobber writable file <local path>` (exit 1) puts the LOCAL path in stderr,
and the local path is the workspace root joined with the caller's own remainder - so a path segment
named `disk full` reaches it, and no client-path rewrite can remove it.

**Proposal option 1 does not close it.** Suppressing a match that also appears in the args catches
the revision-specification cases but not the clobber class, where the stderr carries a Windows local
path that appears in no argv element at all. Option 2 remains the only closing fix.

**The client-path slice is not even a partial mitigation.** It removes the stream name from the sync
and resolve argv but passes the sub-stream remainder through byte for byte, and it made
`CreateStreamClient` run on every Prepare rather than only on cold create, so the full stream name
reaches argv more often than before. The stderr for the revision-specification case is byte-identical
before and after. This item must not be closed on that work.


## Related

- `internal/agent/source/perforce/diagnostics.go` - `classifiableText`, `classifyP4Error`
- `internal/agent/source/perforce/client.go` - `p4CommandError`, `execRunner`
- `internal/jobspec/jobspec.go` - `validateSourceSpec`, the absent character set
- [[feature-2026-09-03-classify-out-of-disk-p4-errors]] - the item whose implementation surfaced this
- [[bug-2026-09-03-prepare-failure-error-message-is-discarded]] - made the classified string readable
