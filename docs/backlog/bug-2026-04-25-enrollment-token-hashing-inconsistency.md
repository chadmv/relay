---
title: Enrollment token hashing inconsistency vs CLAUDE.md doc
type: bug
status: open
created: 2026-04-25
source: 2026-04-22 security-hardening-pass2 retro — Known Limitations
---

# Enrollment token hashing inconsistency vs CLAUDE.md doc

## Summary
**Enrollment token hashing inconsistency**: the CLAUDE.md token-format doc specifies SHA-256 of the hex-encoded bytes; enrollment tokens hash the raw string instead. Both the server and tests are internally consistent, but the deviation from the documented pattern is a future maintenance hazard.
