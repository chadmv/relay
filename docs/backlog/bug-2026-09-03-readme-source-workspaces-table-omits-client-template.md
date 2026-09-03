---
title: README's source-workspaces table omits client_template, which validateSourceSpec enforces
type: bug
status: open
created: 2026-09-03
priority: low
source: fan-in of the 2026-09-02 web-frontend batch
---

# README's source-workspaces table omits client_template, which validateSourceSpec enforces

## Summary
jobspec.go validates a client_template field on the Perforce source block and rejects an invalid one, but README's source-workspaces table does not list the field, so a consumer implementing against the document cannot know the field exists or what value the validator accepts.

## Context
From the FB spec's survey of the source block while cutting the Perforce builder from lane FB.

## Proposal
Add the row with the accepted shape and the error the validator returns.

## Related
- README.md, internal/jobspec/jobspec.go
- [[feature-2026-09-03-perforce-source-builder-in-the-new-job-builder]]
