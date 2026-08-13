---
title: Audit the form-error surfaces that bypass Field, and pin the announcement with a sweep test
type: idea
status: open
created: 2026-08-13
priority: low
source: Phase 6 triage of the 2026-08-12-profile-pages slice, where Field gained role="alert" and none of its consumers' tests moved
---

# Audit the form-error surfaces that bypass Field, and pin the announcement with a sweep test

## Summary

`web/src/components/Field.tsx` now gives its error text `role="alert"` and pushes the error's `id`
onto the control's `aria-describedby` (`Field.tsx:19-27,38-42`). It shipped **without** both, and
eight consumers passed an `error` prop for months with nothing announcing it and nothing associating
it with the input. The gap was invisible: adding the behaviour moved **zero lines** in any consumer's
tests, because not one of them had ever asserted that an error was announced.

The same gap is still open in the error surfaces that do **not** go through `Field`. Eleven of them
render a mutation or validation error into a bare `<div className="... text-err">` with no
`role="alert"`, while six comparable surfaces do have it. A screen-reader user gets an announcement
or not depending on which file the developer copied from.

## Context

Found while fixing a review finding in the profile slice: `PasswordTab`'s client guard failures were
never announced, while the server's 403 in the *same form* was - because the 403 rendered through the
component's own `role="alert"` div and the guards rendered through `Field`. Two error surfaces, one
form, opposite behaviour. Fixing `Field` closed that instance and left the pattern.

**Already correct** (`role="alert"` present): `web/src/auth/LoginScreen.tsx:61`,
`web/src/auth/RegisterScreen.tsx:106`, `web/src/jobs/NewJobPage.tsx:62`,
`web/src/schedules/ScheduleDetailPage.tsx:180`, `web/src/schedules/ScheduleTriggerForm.tsx:157`,
`web/src/admin/server/ErrorStrip.tsx:19`, plus the three new `web/src/profile/` tabs and `Field`
itself.

**Not announced** - enumerated from the tree rather than estimated, since a finding's stated scope is
a starting point and not a census:

| site | what it renders |
|---|---|
| `web/src/admin/users/UsersTab.tsx:264` | action error banner (archive / promote / reset) |
| `web/src/admin/users/ResetPasswordDialog.tsx:84` | the dialog's own mutation error |
| `web/src/admin/users/CreateUserForm.tsx:89` | form-level error |
| `web/src/admin/enrollments/CreateEnrollmentForm.tsx:88` | create mutation error |
| `web/src/admin/reservations/CreateReservationForm.tsx:162` | the validation bullet list |
| `web/src/admin/reservations/CreateReservationForm.tsx:169` | create mutation error |
| `web/src/schedules/SchedulesPage.tsx:166` | action error banner |
| `web/src/workers/WorkerActions.tsx:96` | action error banner |
| `web/src/workers/WorkerLabels.tsx:106` | label update error |
| `web/src/workers/WorkspacesPanel.tsx:65` | evict error |
| `web/src/jobs/JobActions.tsx:65` | action error banner |

`ResetPasswordDialog.tsx:84` is the one worth looking at first: it is inside a modal, so it is both
unannounced *and* the surface where a missed error is most costly - the user is looking at a dialog
whose button appears to do nothing.

A separate, weaker category is the page-level *fetch* error card that replaces the whole page on a
failed load (`web/src/jobs/JobsPage.tsx:98`, `web/src/workers/WorkersPage.tsx:174`,
`web/src/admin/users/UsersTab.tsx:150`, `web/src/admin/reservations/ReservationsTab.tsx:136`,
`web/src/schedules/SchedulesPage.tsx:110`, and the three detail pages' retryable cards). Those
replace the page's content rather than appearing beside a control, so a live region is arguably the
wrong tool and a focus move may be the right one. **Decide that question separately; do not
mechanically stamp `role="alert"` on them.**

## Proposal

Three parts, and the third is the durable one.

1. **Route what can be routed through `Field`.** Any error that is *about a specific control* belongs
   in that control's `Field`, which now wires both the announcement and the `aria-describedby`
   association. `CreateReservationForm.tsx:162`'s validation list is the clearest candidate.
2. **For the rest, add `role="alert"` at the site.** The action-error banners are not about one
   control, so `Field` is the wrong home; they need the attribute directly, matching
   `ScheduleDetailPage.tsx:180`, which is the existing correct instance of exactly that banner.
3. **Add a sweep test so the twelfth surface cannot ship silent.** Same shape as the already-filed
   [[idea-2026-08-09-dialog-shell-sweep-test]], and the choice between a Vitest file reading the tree
   and an ESLint rule should be made once for both. The assertion: within `web/src/**/*.tsx` source
   files, every element whose `className` contains `text-err` and which renders an error *value*
   either carries `role="alert"` or is inside `Field`. Allowlist the page-level fetch cards by name
   and by reason, so the allowlist is the record of the decision from the Context section above.

Each assertion needs a message saying what to do instead, and a positive control proving the sweep
still reaches the source tree - the standing lesson being that an absence assertion whose probe has
stopped reaching anything passes forever.

## Acceptance / Done When

- Every error surface that appears *in response to a user action* is announced, either through
  `Field` or through its own `role="alert"`.
- A test fails when a new unannounced action-error surface is added. Proven RED by adding one
  temporarily, with the failure recorded.
- The page-level fetch-error cards have an explicit, written decision - announce, move focus, or
  deliberately neither - rather than being left in the ambiguous middle.
- `Field` itself keeps a direct test for the announcement and the `aria-describedby` association, so
  the behaviour cannot be removed as silently as it was missing. `web/src/components/Field.test.tsx`
  is the home for it.

## Related

- Source: `web/src/components/Field.tsx:19-42` (the wiring that now exists), and the eleven sites in
  the table above
- Where the gap was found: `docs/retros/2026-08-12-profile-pages.md` (Problem 3 and Findings Triage),
  `web/src/profile/PasswordTab.tsx:20-26`
- Same enforcement shape, same open question about Vitest-reads-the-tree versus ESLint:
  [[idea-2026-08-09-dialog-shell-sweep-test]]
- Adjacent accessibility loose ends on a different surface:
  [[idea-2026-08-09-table-accessible-name-consistency]], [[idea-2026-08-09-sort-caret-in-accessible-name]]

## Notes

Low priority because nothing is broken for a sighted mouse user and every message is on screen. It is
filed anyway because of what the discovery says about the codebase's test coverage rather than about
the messages themselves: **a shared primitive shipped missing an accessibility behaviour and stayed
green across eight consumers indefinitely.** The consumers could not catch it - they assert the error
*text* is present, which is true either way. Only a test at the primitive, or a sweep across the
surfaces that bypass it, can.
