---
title: schedrunner logs operator-controlled schedule names raw at 13 sites
type: bug
status: open
created: 2026-08-28
priority: low
source: Phase 4 fix round of the unfireable-schedule-visibility slice (2026-08-28)
---

# schedrunner logs operator-controlled schedule names raw at 13 sites

## Summary
`internal/schedrunner` writes `row.Name` into the server log unescaped at 13 sites - 11 in
`runner.go`, 2 in `startup_validation.go` - and `handleCreateScheduledJob` validates `name` only
as non-empty, while `handlePatchScheduledJob` does not check it at all. A schedule name carrying
U+202E, a C1 control, or a newline therefore reorders or forges lines in an operator's terminal
when they read the server log, and any authenticated user who can create a schedule can put one
there.

## Context
Found during the Phase 4 fix round of [[bug-2026-08-23-unfireable-schedule-is-invisible]], while
closing the same class one layer over. That slice widened `sanitizeFailureText`
(`internal/schedrunner/failure.go`) and `terminalSafeLine` (`internal/cli/schedules.go`) to cover
C1 controls and bidi overrides, and added `sanitizeServerText` (`internal/relayclient/sanitize.go`)
for text arriving from a server. All three cover values RENDERED to an operator. None covers
values LOGGED by the server, which reach an operator's terminal by a different route and are read
with the same eyes.

**Deliberately not fixed in that PR.** 12 of the 13 sites predate the branch, so the property is
neither introduced nor worsened by it, and closing it properly is one helper plus 13 call sites
plus a test whose subject is the property - a slice of its own rather than a coda to a
40-commit PR. Fixing only the new site was considered and rejected: a spelling-level fix to a
property-level problem leaves the package inconsistent and the next site is written wrong by
default.

**One site is internally incoherent and is the natural place to start.** The `AdvanceScheduledJobAfterFailure`
error log added by that slice logs the SANITIZED failure `text` next to the UNSANITIZED
`row.Name` on the same line. It was left that way on purpose, because making it the lone exception
among 11 siblings in one file would raise a question the file cannot answer - but it is the
clearest statement of the gap.

## Repro / Symptoms
1. As any authenticated user, create a schedule whose `name` contains U+202E (or `\n`, or U+009B).
2. Cause it to be logged - any of the 13 sites will do; the skip path and the failure path are the
   easiest to reach.
3. Read the server log in a terminal. The line renders reordered, or the newline forges what looks
   like a second log line with attacker-chosen content.

## Proposal
One unexported helper in `internal/schedrunner` applying the same rune set the other three sites
already share (`r < 0x20`, `0x7f-0x9f`, `0x200e-0x200f`, `0x202a-0x202e`, `0x2066-0x2069`), applied
at all 13 sites in one commit. The rune set now has three copies; a fourth is acceptable for the
same reason the third was (a client package importing schedrunner would be worse), but the new
comment must name the others, as the existing three do.

A test whose subject is the PROPERTY rather than one call site - assert no `log.Printf` in the
package interpolates a name-shaped value without routing it through the helper - would stop the
14th site being written wrong. An AST guard in the style of
`cmd/relay-server/schedrunner_startup_wiring_test.go` is the idiom this repo already uses for that.

Bounding `name` at the write sites is the deeper fix and is worth considering with it: create
rejects only `""` and PATCH does not even do that, so a name is currently bounded only by
`maxBodyBytes`.

## Acceptance / Done When
- No site in `internal/schedrunner` logs an operator-supplied name without sanitizing it.
- A test fails when a new unsanitized log site is added, rather than only when a known one regresses.

## Related
- `internal/schedrunner/runner.go`, `internal/schedrunner/startup_validation.go`,
  `internal/schedrunner/failure.go`, `internal/api/scheduled_jobs.go`
- [[bug-2026-08-15-cli-prints-unvalidated-worker-hostname-unescaped]] - the same class on the CLI
  surface; these two should settle on one rune set and one argument
- [[bug-2026-08-23-unfireable-schedule-is-invisible]] - the slice that closed the rendered half and
  disclosed this one
- [[bug-2026-08-25-hostname-is-unvalidated-and-reaches-a-unique-index]] - the sibling
  unvalidated-identifier item, on a different field
