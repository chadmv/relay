---
title: Settle one rune set for operator-controlled text rendered to a human, once, across every surface that prints it
type: idea
status: open
created: 2026-08-28
priority: medium
source: /roadmap suggested action, 2026-08-28 - raised while grouping three open items and found to cover four
---

# Settle one rune set for operator-controlled text rendered to a human, once, across every surface that prints it

## Summary
Several open items are the same question on different surfaces: when relay prints text a user chose
into a place a human reads, which characters are dangerous, and what does it do with them? Each item
can only be fixed by answering that question, so fixing them separately means answering it separately
- and the answers will be nearly the same, which is worse than obviously different because nobody
notices. **Decide the character set and the argument once. The call-site edits are the mechanical
half.**

## Context
Terminals do not merely display text. Certain characters are instructions - move the cursor, start a
line, change colour, reverse reading direction - so a value a user picked can make relay's own output
say something untrue. Browsers honour a subset of the same characters.

**The divergence is measured, not predicted.** The schedule-failure slice
([[bug-2026-08-23-unfireable-schedule-is-invisible]], PR #159) had to write one rune set into three
packages in a single change - `internal/schedrunner/failure.go`, `internal/cli/schedules.go` and
`internal/relayclient/sanitize.go` - because the packages cannot cleanly share a helper (a client
package importing the scheduler would be worse than the duplication). The best available mitigation
was to make each copy's comment name the other two. That is one decision, three copies, one PR. A
fourth copy written six weeks later by someone reading only their own item will not match.

**A note inside an item is the weaker form of this, and it has a track record here.** The project
already uses "decide this together with X" cross-references. [[idea-2026-08-09-sse-revoked-token-keeps-streaming]]
and [[idea-2026-08-21-revoked-agent-credential-survives-on-a-held-connection]] carry exactly such a
note - "the two must settle on ONE staleness tolerance; deciding them apart guarantees two numbers" -
and both are still open, the first since 2026-08-09. The note is correct and nothing acted on it,
because a note inside an item is not schedulable: it only fires if someone happens to pick up that
particular item. This item exists so the decision has a status of its own.

## The surfaces this covers
The first three are the same defect. The fourth shares the question but not the whole remedy, and
the distinction is worth keeping rather than flattening:

- [[bug-2026-08-28-schedrunner-logs-operator-controlled-schedule-names-raw]] - 13 sites log a schedule
  name raw. Names are validated only as non-empty on create, and not at all on PATCH.
- [[bug-2026-08-15-cli-prints-unvalidated-worker-hostname-unescaped]] - the CLI prints a worker
  hostname unescaped.
- [[bug-2026-08-25-hostname-is-unvalidated-and-reaches-a-unique-index]] - hostnames are unvalidated
  at the point they are accepted, which is the upstream half of the one above.
- [[bug-2026-08-26-relay-logs-treats-a-chunk-as-a-line-and-forges-lines]] - **adjacent, and found by
  the duplicate check that produced this item rather than by anyone's memory.** `printTaskLogs`
  writes one prefix and one newline per database ROW, but a row is a chunk rather than a line, so a
  job's own stdout can forge convincing log lines with a bare newline. Its primary fix is structural
  (stop treating a chunk as a line), not a character filter - but it must not land on a different
  answer to "what is a dangerous character" than the other three.

Note that the surviving set was **found by searching for the shape, not recalled**: this item was
drafted as covering three surfaces and the fourth turned up in its own duplicate check. Treat the
list as the four found so far rather than as complete, and search again before closing.

## Proposal
Produce one short decision document or one commented constant, and cite it from every site:

- **The set.** The schedule slice's current answer is `r < 0x20`, `0x7f-0x9f`, `0x200e-0x200f`,
  `0x202a-0x202e`, `0x2066-0x2069` - C0 controls and DEL, the C1 range, and the bidirectional
  overrides and isolates. That is a candidate, not a conclusion; it was chosen against one threat
  model on one surface.
- **The action.** Replace with a space, drop, or escape visibly? The schedule slice replaces with a
  space, which preserves column alignment in a table. A log line may want something else.
- **The boundary.** Sanitize at the write site, at the render site, or both? The slice chose the
  write site for the stored column and the un-escaping site for values arriving from a server, and
  those are different answers for different reasons - so "where" is part of the decision, not a
  detail.
- **Where the duplication is allowed to live**, and how copies stay in step. Three exist today by
  necessity; a fourth should be a deliberate choice with a named reason.

## Acceptance / Done When
- One place states the character set, the action, the boundary rule and the reason for each.
- Each of the four items above references it rather than re-deciding.
- A new site printing operator-controlled text has an obvious thing to copy, and a reviewer has one
  thing to check it against.

## Related
- `internal/schedrunner/failure.go` (`sanitizeFailureText`), `internal/cli/schedules.go`
  (`terminalSafeLine`), `internal/relayclient/sanitize.go` (`sanitizeServerText`) - the three
  existing copies
- [[bug-2026-08-23-unfireable-schedule-is-invisible]] - the slice that produced them, and whose
  Phase 4 review raised this
- `docs/retros/2026-08-28-unfireable-schedule-visibility.md`
