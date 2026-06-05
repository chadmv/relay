# Session Retro: 2026-06-04 — Token-less Worker Auto-Enrollment

## What Was Built

An opt-in mode that lets worker agents enroll with no token, for trusted private
networks where network reachability is the trust boundary. Shipped as PR #11.

- Server env var `RELAY_ALLOW_AUTO_ENROLL` (default `false`, fail-closed). When
  on, an agent connecting with no credential (unset gRPC `oneof credential`) is
  enrolled by hostname and issued a normal long-lived agent token - no
  enrollment record consumed. Later reconnects use the issued token unchanged.
- A `revoked` worker is not revived: a row-locked status check
  (`GetWorkerByHostnameForUpdate`, `FOR UPDATE`) inside the enroll transaction,
  placed before the upsert so `finishRegister` can't flip status to online.
- Agent no longer exits when it has no token; it connects token-lessly. Both
  rejection paths (`auto-enroll disabled`, `worker revoked`) use
  `codes.Unauthenticated`, which the existing reconnect loop treats as terminal.
- README updated (server/agent env vars, quickstart, revocation nuance).

Built through the full superpowers flow: brainstorming → spec → plan →
subagent-driven development (implementer + spec review + quality review per
task) → final holistic review → PR.

## Key Decisions

- **Network reachability is the trust boundary.** Single server-wide flag, no
  CIDR allowlist or approval queue. Keeps the feature simple for its intended
  trusted-LAN/VPC use case.
- **Token still issued on join.** Auto-enroll only skips the bootstrap secret;
  the normal token lifecycle and revocation semantics are preserved afterward.
- **Pure-implicit agent behavior, no agent-side opt-in flag.** Initially
  recommended an agent flag for "fail-loud," but the user pushed back: the
  server flag already provides fail-loud (it rejects with `Unauthenticated`,
  which the agent loop treats as terminal). Conceded - the agent flag only
  mattered for mixed fleets, which are explicitly out of scope.
- **Refuse to revive revoked workers.** Revocation is the one deliberate manual
  kill-switch once auto-enroll is on; an explicit admin action should outrank a
  passive reconnect.
- **`Unauthenticated`, not `PermissionDenied`, for the revoked rejection.**
  Discovered during planning that the agent reconnect loop exits on
  `Unauthenticated` but retries other codes forever. Using `Unauthenticated` for
  both rejections gives correct terminal-exit with zero agent-loop changes.
- **Unset oneof = "no credential" signal.** No proto change needed.

## Problems Encountered

- **My first design recommendation was weaker than I presented it.** I argued an
  agent-side opt-in flag bought fail-loud; the user correctly identified that the
  server already provides it. Good reminder to pressure-test my own "pro"
  arguments before leading with them.
- **README contradiction caught in spec review.** The new revocation note ("auto-
  enroll does not revive a revoked worker") sat three lines from existing text
  ("re-enrollment clears the revoked state"). Reworded the new note to mark
  itself as the explicit exception (token vs token-less re-enrollment).
- **Em dash slipped into new code.** The agent log line copied the original
  line's em dash, violating the no-em-dash rule. Caught in final review and
  fixed; left the pre-existing em dashes elsewhere untouched (surgical scope).
- **Windows/sqlc CRLF artifact.** `sqlc generate` rewrote line endings on ~11
  unrelated `internal/store/*.go` files. Handled by staging only the two
  intended files and restoring the rest.

## Known Limitations

- **Revocation is a speed-bump, not a wall.** Identity is keyed by hostname, so a
  renamed revoked host can rejoin as a new worker. Acceptable for a trusted-
  network feature, but worth noting.
- **No mixed-fleet support.** The server flag is global; you cannot run some
  token-based and some token-less agents against the same server.

## Open Questions

- See [`idea-2026-06-04-gitignore-bin-build-artifacts`](../backlog/idea-2026-06-04-gitignore-bin-build-artifacts.md) - Gitignore bin/ and root *.exe build artifacts
- See [`idea-2026-06-04-cidr-allowlist-auto-enroll`](../backlog/idea-2026-06-04-cidr-allowlist-auto-enroll.md) - CIDR allowlist for auto-enroll defense-in-depth

## Improvement Goals

- The subagent two-stage review (spec then quality) earned its keep this
  session: it caught the README contradiction and forced an explicit judgment
  call on the vestigial error return. Worth continuing to lean on for
  multi-task plans.

## Files Most Touched

- `internal/worker/handler.go` (+75) - `AllowAutoEnroll` field, `remoteAddr`
  helper, `autoEnrollAndRegister`, revoked guard, dispatch on unset credential.
- `internal/worker/handler_auth_test.go` (+148) - four integration tests:
  disabled-rejects, new-host-issues-token, revoked-rejected, token-rotation.
- `internal/store/workers.sql.go` (+31) - generated `GetWorkerByHostnameForUpdate`.
- `internal/agent/agent.go` (±5) - `buildRegisterRequest` leaves credential unset.
- `internal/agent/lifetime_test.go` (+11) - no-credential unit test.
- `cmd/relay-server/main.go` (+8) - parse `RELAY_ALLOW_AUTO_ENROLL`.
- `cmd/relay-agent/main.go` (±3) - relax the no-credentials startup gate.
- `internal/store/query/workers.sql` (+3) - the `FOR UPDATE` query source.
- `README.md` (+11) - server/agent docs, quickstart, revocation nuance.
- `docs/superpowers/specs|plans/2026-06-04-auto-enroll-mode*` - spec + plan.

## Commit Range

6db732613f34623439cf42da30e691ecb489d70d..a34e6e8648a8277593eb791032943c42ebbb153a
