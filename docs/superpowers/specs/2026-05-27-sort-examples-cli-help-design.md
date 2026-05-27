# Sort examples for non-jobs list CLI help

**Date:** 2026-05-27
**Type:** Documentation
**Backlog item:** [bug-2026-05-27-sort-flag-cli-help-examples](../../backlog/bug-2026-05-27-sort-flag-cli-help-examples.md)
**Prior work:** [2026-05-26-list-endpoint-sort-design](2026-05-26-list-endpoint-sort-design.md) / [2026-05-27-list-endpoint-sort retro](../../retros/2026-05-27-list-endpoint-sort.md)

## Problem

The list-endpoint-sort feature added a `--sort` flag to five CLI subcommands but only `relay list` got its README help-block examples updated. Four sections show `--sort` in the per-endpoint allowlist table at README.md:1061 but have no example usage in their own `#### relay <cmd>` blocks:

- `relay workers list` (README.md:653)
- `relay reservations list` (README.md:740)
- `relay schedules list` (README.md:789)
- `relay admin users list` (README.md:882)

A user reading any of these four sections has to scroll to the global table to discover that the flag exists. The gap is purely documentation; the CLI flag itself works.

## Goal

Each of the four sections gains two `--sort` example lines in its ` ```sh ` block, matching the format and style of the existing `relay list` block (README.md:601-602).

## Non-goals

- No code changes. The CLI flag is already wired through `internal/cli/{workers,schedules,reservations,admin_users}.go`.
- No changes to the `#### Configurable sort order` table at README.md:1061. It is already correct and complete.
- No duplication of the "pre-feature server silent fallback" caveat (README.md:607). That note applies globally; repeating it per-section would be noise.
- No MCP doc changes. The sort drift test (`internal/mcp/sort_drift_test.go`) already covers MCP allowlist parity.

## Design

### Per-section additions

Each block gets exactly two lines appended to its existing ` ```sh ` example block: one ascending, one descending where a descending key is operationally useful; otherwise two ascending keys with different columns. Sort keys are taken from the per-endpoint allowlist at README.md:1067-1072.

**`relay workers list`** (README.md:657-661)
```
relay workers list --sort name             # alphabetical
relay workers list --sort -last_seen_at    # most-recently-seen first
```
Rationale: freshness-first is the operator's "find flaky agents" view. Direct lift from the backlog item's proposed example.

**`relay reservations list`** (README.md:742-744)
```
relay reservations list --sort name        # alphabetical
relay reservations list --sort starts_at   # chronological by start
```
Rationale: "what's coming up next" maps to ascending `starts_at`.

**`relay schedules list`** (README.md:793-795)
```
relay schedules list --sort name           # alphabetical
relay schedules list --sort next_run_at    # next-to-fire first
```
Rationale: "what fires next" is the schedule operator's primary question; `next_run_at` ascending answers it directly.

**`relay admin users list`** (README.md:888-892)
```
relay admin users list --sort email        # alphabetical by email
relay admin users list --sort name         # alphabetical by name
```
Rationale: the allowlist for users is `created_at`, `name`, `email` — neither `name` nor `email` has a strongly operational descending variant, so two ascending lookups cover the lookup-by-identity use case.

### Comment alignment

Each new line aligns its `#` comment column with the existing lines in its own block. The four blocks have different command-name lengths so the absolute column will differ across blocks but stay internally consistent within each block. The existing `relay list` block uses 31 columns of pre-comment width; I will match the surrounding block's width for each section, not impose a global width.

## Acceptance criteria

- Each of the four sections has exactly two new `--sort` example lines, placed at the end of the section's ` ```sh ` block.
- All sort keys used are members of the per-endpoint allowlist at README.md:1067-1072.
- `git diff --stat` shows only `README.md` changed.
- `make build` is still green (defensive; no code changes expected).

## Verification

1. Open README.md in a viewer; visually inspect each of the four sections to confirm the new lines render correctly and align with the surrounding examples.
2. `git diff README.md` — confirm 8 added lines (2 per section × 4 sections), zero removed lines, zero edits to unrelated sections.
3. `git diff --stat` — confirm only `README.md` appears.
4. `make build` — confirm no incidental code regression.

## Out of scope / future work

- Adding `--sort` to the MCP tool descriptions for `relay_list_*` is already covered by the prior feature work and is not in this scope.
- The Python SDK pagination gap ([bug-2026-05-26-python-sdk-list-pagination-envelope](../../backlog/bug-2026-05-26-python-sdk-list-pagination-envelope.md)) is unrelated.
