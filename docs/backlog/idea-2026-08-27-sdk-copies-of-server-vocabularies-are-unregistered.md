---
title: The Python SDK holds three copies of server-side vocabularies that no lockstep guard knows about
type: idea
status: open
created: 2026-08-27
priority: medium
source: Phase 4 invariants lens while fixing bug-2026-08-25-python-sdk-task-logs-iterates-envelope-keys
---

# SDK copies of server vocabularies are invisible to the lockstep guards

## Summary
`internal/store/jobs_status_vocabulary_lockstep_test.go` is the project's registry of everything
that slices `jobs.status`. Its failure message enumerates six sites and says "Revisit ALL OF THEM."
The Python SDK holds a seventh and the guard does not know it exists. Two more SDK vocabularies have
the same problem.

## Context
Three unregistered copies:

- **`_TERMINAL_JOB_STATUSES`** (`python/src/relay/client.py`) is a byte-for-byte twin of
  `terminalStatuses` (`internal/mcp/wait.go`), consumed by `Client.wait()`. The guard's own
  description of the registered twin applies verbatim: "a new terminal status omitted there means
  the tool polls a finished job until its timeout and answers as though it never finished."
- **`python/README.md`'s "Following a job" snippet** ships
  `terminal = {relay.JobStatus.DONE, relay.JobStatus.FAILED, relay.JobStatus.CANCELLED}` as
  copy-paste guidance, minting an eighth copy in every user's codebase.
- **`EventType`** (`python/src/relay/models.py`). Its test asserts set equality against a Python
  literal in the same file, so it detects an SDK-side edit and nothing else. If
  `internal/api/events.go` gains a sixth frame type the test stays green - and
  `.github/workflows/python.yml` path-filters the SDK lane to `python/**`, so it would not even run
  on that commit. The test's docstring has been corrected to claim only what it pins; the reach gap
  is this item.

## Proposal
Register the SDK copies where a Go change forces a read. The cheapest version is to add
`python/src/relay/client.py:_TERMINAL_JOB_STATUSES` to the site list and failure message in
`jobs_status_vocabulary_lockstep_test.go` alongside `terminalStatuses`, and a comparable
registration for the event vocabulary.

A guard that cannot see the producer is worth less than one that names the consumers it cannot
check - so if a real cross-language check is too costly, an explicit "these consumers exist and are
not verified here" line still beats silence.

## Acceptance / Done When
- Adding a job status or an event type surfaces the Python consumers, or names them as unchecked.
- The README snippet is either registered or rewritten so it stops minting new copies.

## Related
- `internal/store/jobs_status_vocabulary_lockstep_test.go`; `internal/mcp/wait.go`
- `python/src/relay/client.py` `_TERMINAL_JOB_STATUSES`; `python/src/relay/models.py` `EventType`
- [[idea-2026-08-23-integration-only-guards-ci-never-runs]] - both Go guards are integration-tagged
  and CI runs neither
