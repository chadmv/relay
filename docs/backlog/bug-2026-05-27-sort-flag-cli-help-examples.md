---
title: Add --sort example usage to non-jobs list CLI help
type: bug
status: open
created: 2026-05-27
priority: low
source: list-endpoint-sort retro (docs/retros/2026-05-27-list-endpoint-sort.md)
---

# Add --sort example usage to non-jobs list CLI help

## Summary

The `--sort` flag was added to five list subcommands (`relay list`, `relay workers list`, `relay schedules list`, `relay reservations list`, `relay admin users list`) but only `relay list` got its README help-text examples updated. The other four show `--sort` in the flag table but have no example usage.

## Proposal

Add a one-line `--sort` example to each of the four affected commands' help-text sections in `README.md`. Example for workers:

```
relay workers list --sort name             # alphabetical
relay workers list --sort -last_seen_at    # most-recently-seen first
```

Pick endpoint-appropriate example keys per the per-endpoint allowlist (already documented in the README's "Configurable sort order" table).

## Acceptance / Done When

- Each of the four affected commands has at least one `--sort` example in its CLI section of `README.md`.

## Related

- `README.md` — section under `#### Configurable sort order` already has the allowlist; examples are the gap
- `internal/cli/{workers,schedules,reservations,admin_users}.go` — the commands themselves are already updated
