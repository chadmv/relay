---
title: setSpecField writes through regexp.ReplaceAll, so a $ in the value expands and every duplicate field is rewritten
type: bug
status: open
created: 2026-09-04
priority: low
source: Phase 4 combined lens on the workspace clobber slice; both halves measured
---

# setSpecField writes through regexp.ReplaceAll, so a $ in the value expands and every duplicate field is rewritten

## Summary

`setSpecField` edits a p4 client spec by building a line regex and calling
`regexp.ReplaceAll`. That gives it two behaviours nobody at a call site would expect:
the *replacement template* expands `$name` and `${name}` rather than writing the value
literally, and it rewrites **every** matching line rather than the one a reader found.

## Repro / Symptoms

Measured directly against the helper:

    setSpecField Root="no$1clobber" -> "Root:\tno\n"
    setSpecField Root="/mnt/$1data" -> "Root:\t/mnt/\n"
    setSpecField Root="a$b"         -> "Root:\ta\n"
    setSpecField Root="$0"          -> "Root:\tRoot:\told\n"   (re-inserts the matched line)

And the read/write asymmetry, with the clobber knob on:

    IN : "...Options:\tnoclobber nocompress\nView:\tx\nOptions:\tunlocked\n"
    OUT: "...Options:\tclobber nocompress\nView:\tx\nOptions:\tclobber nocompress\n"

The reader takes the first match; the writer rewrites both, so the second line's
`unlocked` is destroyed and replaced by a copy of the first.

## Context

**Neither half is reachable today, which is why this is low and why it is worth writing
down rather than fixing in passing.**

- The `$` half: the four call sites pass a derived path, two empty strings, a
  `"relay-task-"`-prefixed task id, and an `Options:` value that has already passed an
  alphabetic-token check. The one genuinely operator-controlled value is `Root`, from
  `RELAY_WORKSPACE_ROOT` - so `RELAY_WORKSPACE_ROOT=/mnt/$1data` silently writes
  `Root:` as `/mnt/data`, a wrong workspace root with no error.
- The duplicate half: `p4 client -o` emits one `Options:` field, and `View:` and
  `Description:` continuation lines are tab-indented so they cannot match a `^`-anchored
  field pattern.

What makes it worth an item is that both behaviours are invisible at the call site. A
future caller passing a value with a `$` in it, or a helper reading one field and writing
another, inherits them silently.

## Proposal

Write literally. `regexp.ReplaceAllLiteral` removes the `$` class outright; if the
replacement must stay a string, escape `$` as `$$` at the boundary. For the duplicate
case, either use `ReplaceAllFunc` with a first-match-only guard, or have the readers and
the writer agree - `FindAll` with a length check would fold a duplicated field into the
existing malformed-line warning path.

## Acceptance / Done When

- A value containing `$1`, `$0` or `$name` is written literally, proven by a test.
- A spec with two matching field lines either has only the intended one rewritten, or is
  refused - decided in writing either way.

## Related

- `internal/agent/source/perforce/client.go` (`setSpecField`, `withClobberOption`)
- [[feature-2026-09-03-workspace-clobber-option]] - the slice that measured both halves
