---
title: "A sweep test that fails if dialog semantics appear outside DialogShell"
type: idea
status: open
created: 2026-08-09
priority: low
source: spec follow-up from the dialog-hardening work (2026-08-09)
---

# A sweep test that fails if dialog semantics appear outside DialogShell

## Summary
The dialog-hardening work's acceptance criterion 5 was a set of manual `rg` sweeps proving that
`role="dialog"`, `aria-modal` and the scrim class string `fixed inset-0 z-50 ...` appear in
`web/src/components/dialog/DialogShell.tsx` and nowhere else. That is a real invariant with nothing
enforcing it: it was checked once, by hand, at the end of one iteration. The next hand-rolled dialog
reintroduces exactly the accumulation this work removed, silently and greenly.

## Context
The app reached **three** hand-rolled dialog implementations before the problem was worth fixing, and
each one was added by someone who had no reason to know the others existed. Nothing about that
dynamic changed when the shell landed - the shell only reset the count to one.

The Table iteration's lesson applies directly: a rule that lives only in a comment is a rule the next
caller does not read. There, the durable fix was type-level
(`Omit<ComponentPropsWithoutRef<'div'>, 'role'>` made the hazard unrepresentable). Here the
type-level equivalent is much weaker: nothing stops a new component from writing `role="dialog"` on a
`div` of its own, because it never has to touch `DialogShell`'s props to do it. So the enforcement has
to be a sweep.

## Proposal
One test file (suggested: `web/src/components/dialog/dialogShellIsSole.test.ts`) that reads the
source tree and asserts:

- `role="dialog"` appears in `DialogShell.tsx` only, among `web/src/**/*.tsx` source files. Test files
  use `getByRole('dialog')`, which does not match the attribute form.
- `aria-modal` appears in `DialogShell.tsx` only, among source files.
- The scrim string `fixed inset-0 z-50` appears in `DialogShell.tsx` only.
- `addEventListener('keydown'` appears only in `DialogShell.tsx` and `web/src/shell/UserMenu.tsx`
  (the latter is a popup, not a modal, and an explicit non-goal of the hardening work). Allowlist it
  by name, so a *third* keydown listener trips the test.

Each assertion needs a message that says what to do instead ("compose `DialogShell`"), not just what
failed. Pair each with a positive control so the sweep cannot go vacuous if the glob or the path
changes - the standing lesson from the last two iterations is that an absence assertion whose probe
has silently stopped reaching anything passes forever.

Open question for whoever picks this up: a Vitest file reading the tree with `fs`/`fast-glob` is the
low-ceremony option and matches how the acceptance sweep was actually run; an ESLint rule is more
idiomatic but adds config surface. Either is fine, and the choice should be made once for this and
for any future sweep of the same shape.

## Acceptance / Done When
- A test fails when a component outside `DialogShell.tsx` declares `role="dialog"`, `aria-modal`, the
  scrim class string, or a third `document` keydown listener.
- Proven RED by temporarily adding each violation, one at a time, with the failures recorded.
- Each assertion has a positive control proving the sweep still reaches the source tree.

## Related
- `web/src/components/dialog/DialogShell.tsx`
- `docs/superpowers/specs/2026-08-09-dialog-hardening.md` acceptance criterion 5 (the manual sweeps
  this would make durable)
- Same lesson, different enforcement mechanism:
  [[idea-2026-06-05-shared-accessible-table-primitive]] (closed)
- Shipped the shell this protects: [[idea-2026-07-01-confirmdialog-focus-trap-hardening]]
- **A second sweep of the identical shape**, and the place to settle the Vitest-versus-ESLint
  question once for both: [[idea-2026-08-13-field-error-wiring-audit]]
