---
date: 2026-09-10
topic: pgdsn-root-guard
branch: claude/roadmap-now-dependencies-581b21
range: a9fc0553..1bdac00a
---

# Session Retro: 2026-09-10 - pgdsn Root Guard

**TL;DR:** One of the project's own test-suite safety checks failed for anyone running the tests as
the root user - which is exactly what happens inside the Linux container the project documentation
recommends for running tests on this Windows machine. The check was comparing a value it injected
against a value derived from the operating system account, and when the account happened to be named
"root" the two collided by accident. The fix was to inject a value no account can be named. Review
then found the first version of that fix had quietly weakened the check itself, and a second commit
repaired it.

## Handoff

Item 1 of 6 in the roadmap Now batch. Closes
[[bug-2026-09-04-pgdsn-user-guard-is-red-for-any-run-as-root]]. Merged as PR #206, two commits.

`TestAssertDSNTargetsDatabase_UserArmCatchesQueryOverrideOnNoUserinfoDSN` injected `?user=root` and
asserted `wantDefaultUser` did not return `"root"`. The DSN carries no userinfo, so
`wantDefaultUser` legitimately falls through to `pgx.ParseConfig`, whose default user derives from
the OS account. Green on `ubuntu-latest` only because that runner is `runner`. The injected literal
is now `const injectedUser = "pgdsn-guard:injected-user"`, and the colon is the discriminator - it is
`/etc/passwd`'s field separator and illegal in a Windows SAM account name, so no host can produce it.
Verified in-container that even `useradd --badname "a:b"` cannot write it.

**The second commit is the one worth knowing about.** The first fix percent-encoded the colon via
`url.QueryEscape`. `wantDefaultUser` returns `cfg.User` from `pgx.ParseConfig`, which DECODES the
query, so a mutant adopting the query WITHOUT decoding returns the `%3A` form while `cfg.User`
carries the colon form: they differ, so `NotEqual` passes; they differ from each other, so the arm
still fires and `require.True` passes too. Both assertions pass while the function does the exact
thing the test forbids. RFC 3986 permits a colon in a query (it is in `pchar`) and `net/url` and
`pgx` both carry it raw, so the escape was dropped and `dsn` returned to a `const`.

Acceptance criterion 2 is met: `go test -race ./... -count=1 -timeout 900s` in `golang:1.26` as
root, 23 packages ok, zero data races, exit 0.

Next entry point: the two slice-2 items, `feature-2026-09-04-wall-clock-deadline-on-the-boot-sweep`
and `idea-2026-09-04-a-job-author-controls-how-many-p4-clients-each-agent-creates`.

## What Was Built

- One const carrying a colon, feeding both the DSN and the assertion, so the two cannot drift apart.
- A comment stating why the input discriminates - without it the next reader shortens the literal
  back to something an account can be named and regenerates the bug.

## Key Decisions

- **Refuse a discriminator an account could carry, rather than special-casing root.** The property
  under test is that `wantDefaultUser` ignores `?user=`, not that it ignores `root` specifically.
- **A sibling with the same shape was left alone, after testing the premise.**
  `DerivesOSUserWhenDSNCarriesNone/explicit_userinfo_wins` has the injected-equals-asserted shape,
  but deleting `wantDefaultUser`'s userinfo branch leaves it PASSING - `pgx.ParseConfig` reads the
  same userinfo back out of the URL, so the OS account is on no code path for that input. A comment
  justifying a change there would have been a false claim.
- All eight guards in the file were enumerated by shape and each recorded as fixed or checked, in
  the commit message rather than in a comment.

## What Went Wrong and What Changes

Ledger: the prior retro's entries were all promoted, so none are carried. Promoted lessons that
fired here: [[reference_injected_value_must_not_share_a_literal_with_the_assertion]] is the defect
itself; [[reference_a_mutation_proof_must_leave_a_test_behind]] and
[[reference_verify_the_mutation_applied]] both fired in the second commit;
[[feedback_combined_review_trivial_tasks]] was exercised again - a test-only change got one combined
lens, and that lens is what found the regression.

- **The fix weakened the guard it was fixing, and the conductor's own review pass argued it was
  safe.** The `/code-review` pass reasoned that the assertion pair covered the undecoded-value case
  because `cfg.User` "would carry the same form". It does not - `pgx` decodes. The combined lens
  refuted it with a probe and a two-environment mutation table.
  -> **What changes:** when a fix changes the ENCODING of a test's input, re-run the mutation the
  test exists to catch before believing an argument that the assertions still cover it. An argument
  about what two derived values would both do is exactly the shape a probe settles in one command.
- **A rate-limit kill destroyed six agents mid-flight, two in dangerous states.** One was mid-mutation
  battery, one mid-edit. `SendMessage` was disabled, so resuming was impossible.
  -> **What changes:** when agents die en masse, inspect every worktree's `git status` and `git log`
  BEFORE re-dispatching anything - a committed implementation and a left-applied mutation look
  identical in a task notification. Here the battery agent had committed first, so nothing was
  stranded. (promoted to feedback memory)
- **Mutation testing ran inside a worktree another lens was reading**, producing two false signals in
  that lens - a garbled assertion and an `undefined: i` build error - before it worked out what was
  happening. The conductor was about to make the same mistake.
  -> **What changes:** a review lens that mutates must use `go test -overlay` or a copy outside the
  repo, never copy-and-restore in place, whenever any sibling agent can read that tree. Copy-and-
  restore is still a shared-state mutation for the duration of the window. [[feedback_mutation_testing_needs_isolated_tree]]

## Files Most Touched

- `internal/testsupport/pgdsn/pgdsn_guards_test.go` - the only file changed; 14 insertions, 6
  deletions in the first commit, one line in the second.
