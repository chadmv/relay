# New Job submit form (+ New job) - design

Date: 2026-07-01
Status: proposed
Owner: relay-tpm
Backlog: `docs/backlog/feature-2026-07-01-job-submit-new-job-form.md`

## Summary

Add a "+ New job" flow to the web SPA: an entry-point button on the jobs list
that opens a dedicated `/jobs/new` route hosting a plain-text job-spec editor.
The user authors a job-spec as JSON in the exact shape `POST /v1/jobs` accepts,
the form parses and lightly validates it client-side before submit, POSTs it,
surfaces backend `400` validation errors inline, and on success navigates to the
created job's detail page (`/jobs/:id`).

This is frontend-only. The `POST /v1/jobs` endpoint already exists and is
unchanged by this work.

This slice deliberately ships the simplest tractable editor - a JSON textarea -
not a structured/visual DAG form-builder. The form-builder is a much larger
surface and is called out as a deferred follow-up (see Open decisions).

## Verified backend contract

Verified against `internal/api/jobs.go` (`handleCreateJob`, `createJobRequest`,
`taskSpec`, `jobResponse`), `internal/api/job_spec.go` (the type aliases and
`ValidateJobSpec`), `internal/jobspec/jobspec.go` (`Validate` and its rules),
the route table in `internal/api/server.go`, the JSON helpers in
`internal/api/server.go` (`readJSON`, `writeError`), and the CLI submit path in
`internal/cli/jobs.go` (`doSubmit`).

### Route and permission

- Route: `mux.Handle("POST /v1/jobs", auth(http.HandlerFunc(s.handleCreateJob)))`.
  It is `auth`-only, NOT `admin`-gated. Any authenticated user may create a job.
  The created job's `submitted_by` is set to the caller (`u.ID`).
- There is no owner/admin restriction on creation; the SPA must NOT hide the
  "+ New job" affordance behind an admin gate.

### Request body (JSON only)

The handler reads the body via `readJSON` (JSON decode, `1 MiB` cap). It is JSON,
not YAML. The body decodes into `createJobRequest`, which maps 1:1 onto the
`jobspec.JobSpec` types:

- `name` (string, required)
- `priority` (string, optional; one of `low` | `normal` | `high`; empty is
  allowed and defaults to `normal` server-side)
- `labels` (object of string -> string, optional)
- `tasks` (array, required, at least one) - each task:
  - `name` (string, required, unique within the job)
  - `command` (array of strings, legacy single argv) OR `commands` (array of
    argv arrays). Exactly one of the two must be set; setting both is rejected;
    each argv must be non-empty. A single `command` is normalized server-side
    into a one-element `commands`.
  - `env` (object string -> string, optional)
  - `requires` (object string -> string, optional; agent-capability match)
  - `timeout_seconds` (integer or null, optional)
  - `retries` (integer, optional, default 0)
  - `depends_on` (array of task names, optional; every name must refer to
    another task in the job; the graph must be acyclic)
  - `source` (object, optional; Perforce workspace prep - `type: "perforce"`,
    `stream`, `sync[]`, `unshelves[]`, `workspace_exclusive`, `client_template`).
    Validated by `validateSourceSpec` only when present.

The CLI (`relay submit <job.json>`) reads a JSON file and POSTs it as the body
unchanged (`doSubmit` in `internal/cli/jobs.go`). JSON is therefore the
project's native authoring format for a job-spec; the web editor should accept
the same JSON so a spec file that works with `relay submit` also works when
pasted into the form.

### Validation rules (server-side, from `jobspec.Validate`)

The client should pre-validate a minimal subset of these; the server is the
source of truth for the rest. The full server rules are:

- `name` required.
- at least one task required.
- `priority` must be `""`/`low`/`normal`/`high`.
- each task `name` required and unique.
- each task must set exactly one of `command`/`commands`, with non-empty argv.
- every `depends_on` name must reference an existing task.
- the `depends_on` graph must be acyclic (a cycle lists the involved tasks).
- if `source` is present, it must be a valid Perforce spec.

### Responses

- `201 Created` on success, body is a `jobResponse`: `{ id, name, priority,
  status, submitted_by, submitted_by_email, labels, tasks, created_at,
  updated_at }`. The `id` is the new job's UUID; the SPA navigates to
  `/jobs/${id}`.
- `400 Bad Request`:
  - malformed JSON body -> `{ "error": "invalid request body" }` (from
    `readJSON`).
  - failed spec validation -> `{ "error": "<validation message>" }` (from
    `ValidateJobSpec`, e.g. `"at least one task is required"`,
    `"duplicate task name: build"`, `"unknown depends_on: setup"`,
    `"dependency cycle detected involving tasks: a, b"`,
    `"invalid priority \"urgent\": must be low, normal, or high"`).
- `413 Request Entity Too Large` -> `{ "error": "request body too large" }` if
  the body exceeds `1 MiB`.
- `401 Unauthorized` if the token is missing/invalid.
- `500 Internal Server Error` -> `{ "error": "<msg>" }` on a DB/transaction
  failure.

Error body shape is a single top-level `error` string in ALL cases (there is no
per-field error map). The SPA's `apiFetch` already extracts that string into
`ApiError.code` / `ApiError.message` (see `web/src/lib/api.ts`). There is no
structured field-level error to bind to individual inputs; the whole message is
surfaced as one inline banner.

## Design decisions (delegated to TPM, decided here)

### Editor format: JSON textarea (not YAML)

Decision: a plain-text JSON editor (a `<textarea>` with monospace styling), not
YAML and not a structured form.

Rationale:

- The API accepts JSON only. `relay submit` authors JSON. Matching that format
  means a spec that works in the CLI works in the form and vice versa, with zero
  translation layer to keep in sync.
- No YAML parser is a web dependency today (`web/package.json` has no `js-yaml`
  or equivalent). Choosing JSON avoids adding a dependency and its bundle/audit
  cost for this first slice.
- `JSON.parse` is built-in and gives a precise parse error (message + position)
  for the client-side "is this valid JSON" check.

A YAML option or a structured builder are deferred follow-ups (Open decisions).

### Route (not modal): a dedicated `/jobs/new` route

Decision: a dedicated protected route `/jobs/new` rendering a `NewJobPage`,
rather than an in-place modal on the jobs list.

Rationale:

- Linkable and shareable (a deep link to "start a new job"), bookmarkable, and
  survives a refresh - a modal loses the drafted spec on reload.
- A full-height editor page suits a multi-line spec far better than a modal.
- Simpler routing/testing story that matches the existing page-per-view
  structure (`JobsPage`, `JobDetailPage`, etc.) in `web/src/app/router.tsx`.
- Ordering caveat: the new route MUST be declared BEFORE `/jobs/:id` in the
  `Routes` (or use a non-colliding path) so `react-router` does not match
  `new` as an `:id`. React Router v7 ranks static segments above dynamic ones,
  so `/jobs/new` wins over `/jobs/:id` regardless of order, but we still declare
  it adjacent to the other `/jobs*` routes for clarity, and a test asserts
  `/jobs/new` renders the form (not the detail page attempting to fetch a job
  with id `"new"`).

## Frontend design

### New files

- `web/src/jobs/NewJobPage.tsx` - the page: entry heading, back link, textarea
  editor, submit button, inline errors, success navigation.
- `web/src/jobs/useCreateJob.ts` - the mutation hook.
- `web/src/jobs/specTemplate.ts` - the prefilled starter template string and the
  minimal client-side validation helper.
- Add `createJob(spec)` to `web/src/jobs/api.ts`.
- Register `/jobs/new` in `web/src/app/router.tsx` under `ProtectedRoute`.

### Entry point

Add a "+ New job" button to `JobsPage.tsx`, matching the Holo reference
(`design_handoff_relay_holo/reference/screens/jobs-list.js`, which places a
`<button class="btn accent">+ New job</button>` in the header action row). Place
it in the existing top header row (the `ml-auto` cluster that currently holds the
"live / auto-refreshing" indicator), as a `react-router` `<Link to="/jobs/new">`
styled to match the accent `Button` (or the shared `Button` wrapped in a Link).
It is shown to every authenticated user (creation is not admin-gated).

### API client

Add to `web/src/jobs/api.ts`, mirroring the `apiFetch` + `json` convention used
throughout (e.g. how other POSTs pass `json`):

- A `JobSpec` request type (or reuse a locally-declared shape) with `name`,
  optional `priority`, optional `labels`, and `tasks[]`. Since the editor sends
  raw parsed JSON, the client type can be permissive: the function accepts the
  parsed object (`unknown`/`Record<string, unknown>`) and posts it verbatim.
  Keeping it loose avoids duplicating the full task schema in TypeScript for a
  first slice where the server is the validator of record.
- `createJob(spec: unknown): Promise<JobDetail>` calling
  `apiFetch<JobDetail>('/jobs', { method: 'POST', json: spec })`.
  The response is a `jobResponse`; `JobDetail` (already defined) is the closest
  existing type and carries the `id` the caller needs.

`apiFetch` sets `Content-Type: application/json` and serializes `json` for us;
the mutation only needs the parsed object.

### Mutation hook (`useCreateJob`)

A `useMutation` following the SPA's invalidate-on-success convention (see
`web/src/jobs/useJobActions.ts`):

- `mutationFn: (spec: unknown) => createJob(spec)`.
- `onSuccess: (job) => { qc.invalidateQueries({ queryKey: ['jobs'] });
  qc.invalidateQueries({ queryKey: ['job-stats'] }); navigate(\`/jobs/${job.id}\`) }`.
  - `['jobs']` (bare prefix) refreshes any list views keyed
    `['jobs', sort, status, cursor]` so the new job appears on return/next poll.
  - `['job-stats']` MUST be invalidated explicitly - it is decoupled from
    `['jobs']` (asserted by `web/src/jobs/queryKeyDecoupling.test.tsx`), so the
    KPI strip's queued count would otherwise stay stale after a create.
  - navigation to `/jobs/${job.id}` is the primary success effect.
- No optimistic update.

`navigate` comes from `useNavigate()`; the hook can take it as a parameter or
call it internally - decide at implementation time to keep the hook testable
(prefer passing `navigate` in or returning the mutation and navigating in the
page's `onSuccess` so the hook has no router dependency).

### Editor and client-side validation

`NewJobPage` holds the editor text in local state, prefilled with a starter
template. On submit it runs a two-stage local check BEFORE calling the mutation:

1. Parse: `JSON.parse(text)`. On failure, show "Invalid JSON: <parser message>"
   inline and do not submit.
2. Minimal shape check (fast feedback for the most common mistakes; the server
   remains the authority):
   - top-level `name` is a non-empty string.
   - `tasks` is a non-empty array.
   (Deeper checks - unique task names, `command` xor `commands`, dependency
   cycles - are left to the server to avoid re-implementing `jobspec.Validate`
   in TypeScript and letting the two drift. The server's message is surfaced
   inline, so the user still gets a specific reason.)

On both local-check pass, call `create.mutate(parsed)`.

Starter template (prefilled into the textarea) - a minimal, valid, single-task
spec the user edits, chosen so an unedited submit succeeds and demonstrates the
shape:

```json
{
  "name": "my-job",
  "priority": "normal",
  "tasks": [
    {
      "name": "hello",
      "command": ["echo", "hello world"]
    }
  ]
}
```

The template intentionally uses the single `command` form (simplest) and omits
optional fields (`labels`, `env`, `requires`, `timeout_seconds`, `retries`,
`depends_on`, `source`). A short helper line under the editor links to the
job-spec fields (or names them) so users can discover `commands`, `depends_on`,
etc. without an in-app schema.

### Submit and error handling

- Submit button is disabled while `create.isPending`.
- Two error surfaces, both inline (single banner styled
  `rounded-card border border-err/40 bg-err/10 text-err`, matching
  `JobActions`):
  - client parse/shape error (from step 1/2 above) - shown immediately, no
    request made.
  - server error (`create.error`, an `ApiError`) - its `message` (which is the
    server's `error` string, e.g. `"duplicate task name: build"`) is shown. This
    directly satisfies the acceptance criterion "backend validation errors
    surface inline rather than as an opaque failure."
- Distinguish the two only by source; a single banner slot is enough. Clear a
  stale server error on the next submit attempt (`create.reset()` before
  re-validating), matching the worker/job action pattern.

### Success navigation

On `201`, the response carries `id`. Navigate to `/jobs/${id}` (the existing
`JobDetailPage` route). The user lands on the freshly created job's detail page,
which polls and shows tasks coming online. This satisfies "on success the user
is navigated to the created job's detail page."

### Permission gating

None beyond authentication. Creation is `auth`-only server-side, so the button
and the `/jobs/new` route are available to every logged-in user (both sit under
the existing `ProtectedRoute`). Do NOT admin-gate this flow.

## States and edge cases

- Empty/whitespace editor: local parse fails -> "Invalid JSON" banner; no
  request.
- Valid JSON but missing `name` or empty `tasks`: local shape check fails with a
  targeted message; no request.
- Valid-looking JSON that the server rejects (duplicate task name, unknown
  `depends_on`, cycle, both `command` and `commands`, bad priority, invalid
  source): `400` -> the server's specific message in the banner; the user's text
  is preserved so they can fix it in place.
- Oversize spec (> 1 MiB): `413` -> "request body too large" surfaced in the
  banner (the same inline path; no special-casing needed).
- Network/`500`: `create.error` message in the banner; text preserved; user can
  retry.
- `401` mid-submit: `apiFetch` already fires the global unauthorized listeners
  (redirect to login); no special handling here.
- Double-submit: button disabled while pending guards against a second POST.
- Navigating away with unsaved text: acceptable to lose it for this slice (no
  draft persistence); note as a possible follow-up, not in scope.
- The `/jobs/new` vs `/jobs/:id` route collision: covered by a test asserting
  `/jobs/new` renders the form.

## Test plan (Vitest + Testing Library + MSW)

Add `web/src/jobs/NewJobPage.test.tsx`, extend the api-client test, and add a
router test for the `/jobs/new` path.

1. Entry point: render `JobsPage`; assert a "+ New job" control linking to
   `/jobs/new` is present and visible to a non-admin user.
2. Route renders the form (collision guard): navigate to `/jobs/new`; assert the
   editor renders and NO `GET /v1/jobs/new` request is made (i.e. the detail
   page did not treat `new` as an id).
3. Prefilled template: assert the textarea is prefilled with the starter
   template and that submitting it unedited issues `POST /v1/jobs` with that body.
4. Happy path: edit to a valid spec, submit; assert `POST /v1/jobs` was called
   with the parsed JSON body; mock a `201` with `{ id: "job-123", ... }`; assert
   navigation to `/jobs/job-123`; assert `['jobs']` and `['job-stats']` are both
   invalidated (spy on `invalidateQueries` or assert refetch of each).
5. Local parse error: enter invalid JSON (e.g. trailing comma), submit; assert an
   "Invalid JSON" banner and that NO `POST` was made.
6. Local shape error - missing name: submit `{"tasks":[...]}`; assert a targeted
   banner and no POST. Missing/empty tasks: submit `{"name":"x","tasks":[]}`;
   assert a targeted banner and no POST.
7. Server validation error surfaced inline: mock `POST /v1/jobs` -> `400`
   `{"error":"duplicate task name: build"}`; submit a locally-valid spec; assert
   the exact server message appears in the banner, NO navigation occurs, and the
   editor text is preserved.
8. Oversize/`413`: mock `413 {"error":"request body too large"}`; assert the
   message surfaces inline (same banner path).
9. Pending state: assert the submit button is disabled while the mutation is in
   flight (delayed MSW handler), preventing a double POST.
10. Error reset: after a `400`, fix the spec and resubmit; assert the stale error
    banner is cleared and the retry POST fires.

## Invariants and system-design lens

- Single job-spec pipeline: the SPA posts raw JSON to `POST /v1/jobs`, which runs
  `ValidateJobSpec` -> `CreateJobFromSpec` - the one shared ingestion path. The
  client does NOT define a parallel spec struct or a parallel task-creation path;
  it deliberately keeps its TypeScript type permissive and defers to the server
  validator, so new `TaskSpec` fields need no client change to be accepted.
- Epoch fence: creation goes through `CreateJobFromSpec`, which inserts tasks in
  the standard path; nothing in this frontend slice touches task status or
  epochs.
- Single JSON entry point: request bodies are still read only via `readJSON`
  server-side; the client just sends JSON through `apiFetch`. No new server entry
  point is added.
- Threat model: creation is authenticated but not privileged; the server sets
  `submitted_by` to the caller, so a user cannot forge ownership. The `1 MiB`
  body cap already bounds a hostile spec; the client adds no bypass. Client-side
  validation is a UX affordance only - the server is the authority, so a crafted
  request that skips the SPA is still fully validated.
- Load/failure mode: a single POST; on failure the UI shows the server's message
  and preserves the draft. No optimistic state to reconcile. Large specs are
  bounded by the server cap and surfaced as an inline error rather than an opaque
  failure.

## Open decisions

1. Editor format: JSON textarea (this spec) vs YAML vs a structured/visual
   form-builder. Recommendation: JSON now (matches the API and `relay submit`,
   adds no dependency, built-in parse errors). A YAML mode would need a parser
   dependency and a YAML->JSON conversion that stays faithful to the spec shape.
   A structured DAG form-builder (per-task rows, dependency picker, source
   builder) is the biggest follow-up and should be its own backlog item and spec;
   it is explicitly out of scope here.
2. Route vs modal: dedicated `/jobs/new` route (this spec) vs a modal on the
   jobs list. Recommendation: route (linkable, refresh-safe, full-height editor,
   matches page-per-view structure). A modal is viable later if a lightweight
   "quick submit" is wanted alongside the full page.
3. Depth of client-side validation: this spec validates only "valid JSON" +
   "name present" + "non-empty tasks", deferring task-level checks (unique names,
   command xor commands, dependency cycles) to the server to avoid duplicating
   and drifting from `jobspec.Validate`. If richer instant feedback is wanted, a
   later slice could port a subset of the rules to TypeScript - but that
   reintroduces a drift risk against the single job-spec pipeline and should be a
   conscious follow-up decision.
4. Starter template contents: a single-task `echo` spec (this spec) vs a richer
   multi-task example showing `depends_on` and `commands`. Recommendation: the
   minimal valid single-task template (an unedited submit succeeds), with a
   fields hint linking to the fuller shape, to keep the first-run experience
   frictionless.
5. Draft persistence: not in scope; navigating away loses editor text. A
   `localStorage` draft is a possible small follow-up if users report losing work.
```
