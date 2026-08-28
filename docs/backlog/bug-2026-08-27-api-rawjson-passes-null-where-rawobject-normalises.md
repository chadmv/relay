---
title: internal/api rawJSON passes a stored JSON null through to clients at five sites, while rawObject normalises it at two
type: bug
status: open
created: 2026-08-27
priority: medium
source: Live integration lane while fixing bug-2026-08-25-python-sdk-task-logs-iterates-envelope-keys
---

# `rawJSON` passes a stored JSON `null` through where `rawObject` normalises it

## Summary
`internal/api/server.go` has two helpers for jsonb columns. `rawObject` normalises `null` to `{}`
and its own comment says why - "so a client never receives a null where an object is expected (the
web job-detail page did `Object.entries()` on it and crashed)". It is used at two sites. `rawJSON`
passes `null` straight through, at five. Every client then has to defend itself separately, and one
did not.

## Repro / Symptoms
Against a live `relay-server`, submit a job with no labels and `GET /v1/jobs`:

```
"labels":null
```

Confirmed by raw curl. The Python SDK modelled `Job.labels` as a required dict, so `list_jobs()`
raised `pydantic.ValidationError` for that job. Creating a reservation without a selector
reproduces it for `Reservation.selector`.

The five `rawJSON` sites:

| wire field | site |
|---|---|
| `Task.commands` | `internal/api/jobs.go` `toTaskResponse` |
| `Job.labels` | `internal/api/jobs.go` `toJobResponse` |
| `Reservation.selector` | `internal/api/reservations.go` |
| `ScheduledJob.job_spec` | `internal/api/scheduled_jobs.go` |
| `Worker.labels` | `internal/api/workers.go` |

Two are reachable today (`Job.labels`, `Reservation.selector` - a handler marshals a Go nil map when
the field is omitted). Three are not, by inspection: worker registration never assembles a nil map,
and `jobspec.Validate` rejects a task with zero commands before storage.

## Context
Found by the live integration lane, and **only** by it. Four reading-based review lenses passed the
same code, because a client-side sweep that checks each response's CONTAINER shape does not check
each FIELD's nullability.

The Python SDK was fixed client-side in the slice above (all five coerced defensively). This item is
the server-side half: whether `rawJSON` should simply be `rawObject` at the object-typed sites.

Note `rawJSON`'s empty-bytes fallback returns `{}` even for ARRAY-typed fields, so a zero-length
`commands` column would arrive as `{}` and still fail a list-typed client. Worth settling in the
same pass.

## Proposal
Decide per site whether the column is object-typed or array-typed, normalise `null` and empty to the
matching empty value, and leave one helper rather than two that differ by an undocumented judgment
call. If `rawJSON` must stay for a genuinely nullable field, say which field and why at the call
site.

## Acceptance / Done When
- No `/v1` response can carry a JSON `null` for a field a client models as an object or array, or
  the exceptions are named at their call sites.
- A test covers the empty-bytes and literal-`null` inputs for both helpers.

## Related
- `internal/api/server.go` `rawJSON`, `rawObject`; `internal/api/rawjson_test.go`
- `python/src/relay/models.py` `_empty_on_null` - the client-side defence, which records which two
  of the five are observed on a live wire and which three are insurance
- [[bug-2026-08-25-python-sdk-task-logs-iterates-envelope-keys]]

## 2026-08-27: a third client hit it, and the workaround is now load-bearing in Go too

From the CLI real-server integration lane slice ([[idea-2026-08-23-cli-tests-never-hit-real-server]]).

The item was filed off the Python SDK. The Go CLI has now hit the same field. `relay get --json`
and `relay list --json` were silently dropping `labels` entirely (along with six other fields);
adding it forced the same decision the Python SDK faced, and for the same reason:

`internal/cli/jobs.go`'s `jobResp.Labels` had to be typed `json.RawMessage` rather than
`map[string]string`. A typed map decodes the wire's `null` to a nil map and re-encodes it as
`null` too, which happens to round-trip - but any consumer typing it as a required object breaks,
which is exactly the Python failure. `json.RawMessage` is the only mirror type that carries the
server's actual bytes without a client-side opinion.

Measured on a live server, a job submitted with no labels:

```
"labels":null, ... "env":{}, "requires":{}
```

`env` and `requires` go through `rawObject` and are normalised; `labels` goes through `rawJSON` and
is not. Three clients now defend against this separately - the Python SDK via `_empty_on_null`, the
web SPA via `?? []`-style guards, and the Go CLI via `json.RawMessage` - which is precisely the
"every client has to defend itself separately" cost the Summary predicts.

The mechanical detail, confirmed while writing the CLI comment: `jobcreate.CreateJobFromSpec` does
`json.Marshal` on a **nil map**, producing the four bytes `null`, and `rawJSON` floors only an
**empty** slice to `{}` - four bytes is not empty, so the literal null survives to the wire.

Add to Related: `internal/cli/jobs.go` (`jobResp.Labels`),
`internal/cli/jobs_integration_test.go` (`TestIntegration_GetJobJSON_LabelsWhenTheJobHasNone`).
