---
title: web-ci.yml runs the last Node-20 action majors and pins a Node version nobody has verified against the types it checks
type: idea
status: open
created: 2026-08-24
priority: medium
source: GitHub deprecation annotation on the first green run of the 2026-08-24 web-e2e-harness slice
---

# web-ci.yml runs the last Node-20 action majors, and its Node pin is unverified

## Summary

Two separable problems in the same file, both visible on its first run.

**1. Deprecated action majors.** `.github/workflows/web-ci.yml` uses `actions/setup-node@v4`,
`actions/cache@v4` and `actions/upload-artifact@v4`. GitHub annotates every run:

> Node.js 20 is deprecated. The following actions target Node.js 20 but are being forced to run on
> Node.js 24.

The workflow is also internally inconsistent - it already uses `actions/checkout@v5`, and `go-ci.yml`
is on `checkout@v5` / `setup-go@v6`. So three of four actions are a major behind their siblings in the
same repository.

**2. An unverified Node pin.** The job pins `node-version: 22` while `web/package.json` carries
`@types/node` at a much newer major. `tsc -b` therefore type-checks the harness against an API surface
several majors ahead of the runtime that executes it. Nothing currently depends on the difference, but
a harness file using an API added after 22 would compile clean in CI and fail at runtime in the same
job.

## Context

Filed rather than fixed during the slice that created the workflow, deliberately: bumping an action
major is a change whose correctness cannot be established from memory, and the run was green. A wrong
guess at a replacement major breaks CI for everyone, which is worse than a deprecation warning.

**Do not hardcode a target major from memory when acting on this.** Check what majors actually exist
at the time, and prefer matching whatever `go-ci.yml` is on so the two workflows stay consistent.

## Proposal

- Bump the three actions to their current majors, verifying each against its own release notes rather
  than assuming the next integer exists.
- Decide the Node pin against `@types/node`: either raise `node-version` to match the types' major, or
  pin `@types/node` down to the runtime's. Matching downward is the safer default, because it makes
  `tsc` reject an API the runtime does not have rather than accept one it does not.
- While there: confirm the cache key still behaves after the `cache` bump - it currently keys on
  `runner.os` plus the resolved Playwright version.

## Acceptance / Done When

- A `web-ci` run produces no deprecation annotation.
- `tsc -b` checks the harness against the same Node major the job runs, in whichever direction that is
  reconciled, and a comment says which and why.
- `go-ci.yml` and `web-ci.yml` do not disagree about the major of any action they share.

## Related

- `.github/workflows/web-ci.yml`, `.github/workflows/go-ci.yml`
- `web/package.json` - the `@types/node` entry
- [[idea-2026-08-23-integration-only-guards-ci-never-runs]] - the item this workflow half-closes
