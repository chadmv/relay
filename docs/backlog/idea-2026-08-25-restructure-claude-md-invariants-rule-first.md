---
title: Restructure the Invariants section of CLAUDE.md rule-first
type: idea
status: open
created: 2026-08-25
source: /doctor context audit, 2026-08-25 session
---

# Restructure the Invariants section of CLAUDE.md rule-first

## Summary
The Invariants section is 11,509 chars (~2.9k est tokens), 57% of CLAUDE.md, and the Epoch fence bullet alone is 6,885 chars - five distinguishable rules (fence-or-end-or-predicate; currency-not-identity; identity-not-honesty; allow-lists-not-deny-lists; the AppendTaskLog/ListOverdueAssignedTasks carve-outs) braided into one paragraph with incident history inline (six date stamps).

## Proposal
Restructure to rule-first sub-bullets: statement, one-line rationale, named counter-example, pointer to the retro that recorded the story. Expected ~11.5k -> ~7k chars, saving ~1.1k est tokens per session with zero rules lost.

## Context
Must run as its own reviewed slice: wrong prose about correct code is this repo's dominant defect class, and these paragraphs are what the review lenses read as ground truth - the rewrite needs its own review pass comparing rule-for-rule against the original.

## Acceptance / Done When
- Every rule in the current section is present in the restructured one, verified rule-for-rule against the original by an independent review pass.
- Incident narratives are replaced by pointers to the retros that recorded them; no rule loses its named counter-example.
- The section's size drops materially (target ~7k chars) with no invariant weakened or dropped.
