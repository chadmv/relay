---
title: Enrollment token hashing inconsistency vs CLAUDE.md doc
type: bug
status: closed
created: 2026-04-25
closed: 2026-04-26
resolution: fixed
source: 2026-04-22 security-hardening-pass2 retro — Known Limitations
---

# Enrollment token hashing inconsistency vs CLAUDE.md doc

## Summary
**Enrollment token hashing inconsistency**: the CLAUDE.md token-format doc specifies SHA-256 of the hex-encoded bytes; enrollment tokens hash the raw string instead. Both the server and tests are internally consistent, but the deviation from the documented pattern is a future maintenance hazard.

## Resolution
Introduced internal/tokenhash.Hash as the single canonical implementation of the SHA-256-of-hex hashing pattern. All 8 production call sites and all test helpers now use this function, eliminating the risk of future divergence. CLAUDE.md updated to reference tokenhash.Hash as the authoritative implementation. Audit confirmed all existing sites were already computing the same hash — no behavior change.
