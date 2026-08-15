---
title: The CLI prints the unvalidated worker hostname with %s, so terminal-escape injection survives from the gRPC wire to an operator's terminal
type: bug
status: open
created: 2026-08-15
priority: medium
source: Phase 6 of the 2026-08-15-tasklog-err-limiter-keying slice; the same value, a different sink
---

# The CLI prints the unvalidated worker hostname with %s, so terminal-escape injection survives from the gRPC wire to an operator's terminal

## Summary

`reg.Hostname` arrives on the `RegisterRequest` as a caller-supplied proto string. It is **validated
nowhere**: no length check, no character class, no server-side normalization. It is bounded only by
gRPC's 4 MiB default receive limit. It is then persisted by `UpsertWorkerByHostname`, returned by the
JSON API, and rendered by the CLI with a bare `%s`:

- `internal/cli/workers.go`, `doWorkersList`'s revoked branch:
  `fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", wk.ID, wk.Name, wk.Hostname, wk.RevokedAt)`
- `internal/cli/workers.go`, `doWorkersGet`: `fmt.Fprintf(w, "Hostname:  %s\n", wk.Hostname)`

So an agent that registers with a hostname containing ANSI escape sequences, carriage returns or
newlines controls bytes written directly to an operator's terminal. Newlines forge extra table rows in
`relay workers list`; `\r` overwrites the line; a CSI sequence can hide, recolor or relocate output, and
on some terminals set the window title.

The same applies to the other agent-supplied strings on that struct - `wk.Name`, `wk.Os`,
`wk.GpuModel` - all of which reach the same two functions through the same `%s`.

## Repro / Symptoms

1. With `RELAY_ALLOW_AUTO_ENROLL` on (or with any valid enrollment token), connect and register with
   `Hostname: "real-host\n00000000-0000-0000-0000-000000000000\tadmin-box\tonline"`.
2. Run `relay workers list`. The forged row appears as a genuine table row.
3. Or register with `Hostname: "host\x1b[2K\x1b[1A"` and watch `relay workers get <id>` erase the line
   above it.

Nothing on the server side rejects, escapes or truncates the value at any point in that path. Note the
tabwriter makes step 2 slightly better than it looks (column alignment may give the forgery away) and
slightly worse than it looks (a `\t` in the hostname shifts every subsequent column).

## Context

Found while writing up the 2026-08-15 log-budget slice. That slice fixed the **one sink it owned**: the
auto-enroll audit line in `internal/worker/handler.go` now renders the hostname with `%q` + `clipID`,
because that line is the only record anywhere that a token-less enrollment happened and a forgeable one
corrupts the audit trail of the mechanism it documents. Fixing that sink is what established that the
value is attacker-controlled and unvalidated end to end; it did nothing for any other sink, and the CLI
is the sink with an escape-sequence interpreter attached.

Two sinks that are **not** affected, checked so nobody re-derives it:

- The web SPA renders worker fields as React text nodes, which escape by construction. A hostname
  containing markup or escapes renders as literal characters.
- The JSON API is fine: `encoding/json` escapes control characters, so the value is transported
  faithfully and inertly. **The defect is entirely at the rendering boundary**, which is the correct
  place to fix a rendering defect - and is also why the API and the SPA being safe is not evidence that
  the CLI is.

This is adjacent to but distinct from [[bug-2026-08-12-auto-enroll-hostname-takeover]]. That item is
about the hostname's use as an **identity key** (naming an in-use hostname seizes that worker). This one
is about its **content** reaching a renderer. They share a cause - nothing validates the field - and
should probably share a fix on the server side, but either can ship without the other.

## Proposal

Two halves. Decide whether they are one slice.

**Half A - the renderer (this is the bug).** Escape at every CLI print site that renders an
agent-supplied string. Options, in preference order:

- A small `safeTerm(s string) string` helper in `internal/cli` that strips or escapes C0/C1 control
  characters and clips to a sane width, applied to `Hostname`, `Name`, `Os` and `GpuModel` in both
  functions. Prefer a shared helper to per-site `%q`, because `%q` also adds quotes to every value in a
  table an operator reads by eye, which is a real usability cost on a field that is almost always benign.
- Whichever is chosen, apply it in **both** `doWorkersList` branches and in `doWorkersGet`, and grep for
  other CLI surfaces that render agent-supplied strings before assuming these are the only two.
- `--json` output must stay unescaped-but-encoded: `json.NewEncoder` already handles it, and mangling it
  would break machine consumers.

**Half B - the server (this is the other half).** Validate `Hostname` at registration: reject control
characters and cap the length. Settle before implementing:

- **Reject or normalize?** Rejecting is cleaner and fails closed, but it turns a cosmetic problem into a
  registration failure for an agent whose hostname was previously accepted. Normalizing hides the
  attempt.
- **Where.** It must apply to all three register paths (`enrollAndRegister`, `reconnectAndRegister`,
  `autoEnrollAndRegister`), which argues for `authenticateAndRegister` or a validator called from all
  three, not a check bolted onto one.
- **Existing rows.** A validator at registration does nothing for hostnames already in the table. Decide
  whether Half A is required regardless, which it probably is - **a renderer that trusts its data
  because an upstream validator exists is exactly the coupling that breaks when the validator moves.**

## Acceptance / Done When

- `relay workers list` and `relay workers get` render a hostname containing `\n`, `\r`, `\t` and a CSI
  sequence without emitting any of them to the terminal, proven by a test asserting on the writer's
  bytes that is RED against today's code.
- The same treatment covers `Name`, `Os` and `GpuModel`, or their exclusion is a stated decision.
- `--json` output is byte-unchanged.
- If Half B lands: `Hostname` is validated identically on all three registration paths, proven by a test
  per path, and README's auto-enroll section states the rule.
- Half A's tests do not depend on Half B, so the renderer stays correct if the validator is ever relaxed.

## Related

- Source: `internal/cli/workers.go` (`doWorkersList`, `doWorkersGet`), `internal/worker/handler.go`
  (`autoEnrollAndRegister`'s already-fixed audit line, and the three register paths that accept
  `reg.Hostname` unvalidated), `internal/store/query/workers.sql` (`UpsertWorkerByHostname`)
- Same value used as an identity key: [[bug-2026-08-12-auto-enroll-hostname-takeover]]
- The slice that fixed the one log sink and established the exposure:
  `docs/superpowers/specs/2026-08-15-tasklog-err-limiter-keying.md`,
  `docs/retros/2026-08-15-tasklog-err-limiter-keying.md`
- The reason a per-connection bound does not help here:
  [[bug-2026-08-15-grpc-connection-admission-is-unbounded]]

## Notes

The generalizable rule: **escape at the sink, and enumerate the sinks.** The 2026-08-15 slice correctly
applied `%q` + `clipID` at the sink it owned and, equally correctly, did not go looking for others - but
"we escaped it at the log site" reads, three months later, as "the hostname is escaped". It is not. The
value is attacker-controlled at rest in the database, and every renderer inherits that.

Filed at medium rather than high: it requires a credential (or auto-enroll, off by default), the impact
is on an operator's terminal rather than on data integrity, and the forged-row version is detectable by
a careful reader. It is filed as a bug rather than an idea because there is a concrete wrong behaviour
with a concrete repro, and because the fix is small and local.
