---
title: CIDR allowlist for auto-enroll defense-in-depth
type: idea
status: open
created: 2026-06-04
source: deferred during auto-enroll design (retro 2026-06-04-auto-enroll-mode)
---

# CIDR allowlist for auto-enroll defense-in-depth

## Summary
Should a future iteration add a CIDR allowlist as defense-in-depth on top of the network-trust assumption for `RELAY_ALLOW_AUTO_ENROLL`? Considered and deliberately deferred during the auto-enroll design; the current model relies solely on network reachability to the gRPC server as the trust boundary.

## Related
- `docs/superpowers/specs/2026-06-04-auto-enroll-mode-design.md` (Non-Goals: per-host allowlisting)
- `docs/retros/2026-06-04-auto-enroll-mode.md`
