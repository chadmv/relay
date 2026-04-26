---
title: No transaction wrapping enrollment + token set
type: bug
status: open
created: 2026-04-25
source: 2026-04-22 security-hardening-pass2 retro — Known Limitations
---

# No transaction wrapping enrollment + token set

## Summary
**No transaction wrapping enrollment + token set**: `UpsertWorkerByHostname`, `ConsumeAgentEnrollment`, and `SetWorkerAgentToken` are three separate DB calls. A crash between consume and set-token would leave the enrollment consumed but no token written. The agent would be stuck until an admin issues a new enrollment. A future improvement is to wrap these in a single transaction.
