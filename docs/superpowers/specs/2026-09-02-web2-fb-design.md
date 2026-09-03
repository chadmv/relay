# Lane FB - structured job-spec form builder for New Job - design

Date: 2026-09-02
Status: proposed
Owner: relay-tpm
Gate mode: autonomous
Backlog: `docs/backlog/idea-2026-07-01-job-spec-form-builder.md`
Predecessor spec: `docs/superpowers/specs/2026-07-01-job-submit-form-design.md` (the raw-JSON
editor this lane augments)

## Process note

This spec was produced by running the brainstorming flow end to end in autonomous gate mode. Two
steps have no interactive counterpart in that mode and are recorded rather than silently skipped:

- The visual-companion offer was not made. It is an offer to a human who can open a browser; there
  is no such reader in this session. The visual questions it would have covered are answered below
  against the hi-fi source and the shipped primitives instead.
- The clarifying questions were asked and answered by the author. Each one appears in the Decisions
  section as a question, its options, and the reason the option was taken, so the reader can
  disagree with a specific choice rather than with the whole document.

The user-review gate at the end of the flow still applies: this document is written, not accepted.
The conductor commits it; a human reviews it before Phase 2 writes a plan.

## Summary

Add a structured form builder to the existing `/jobs/new` page. The builder authors a job spec
through labelled controls - job name, priority, labels, and a repeater of task rows carrying name,
one or more commands, env, requires, timeout, retries and a dependency picker - and submits the
result through the unchanged `POST /v1/jobs`. The raw JSON editor stays, as a second mode on the
same page, and remains the authority of last resort for anything the builder cannot type.

The Perforce source builder is explicitly NOT in this lane; it is filed as its own follow-up. A
spec with no `source` is fully authorable in the builder, which is what the backlog item's first
acceptance bullet asks for.

Frontend only. No Go file changes. No new endpoint, no new query parameter, no schema change.

## Verified backend contract

Read directly, not taken from the backlog item: `internal/jobspec/jobspec.go` (`JobSpec`,
`TaskSpec`, `SourceSpec`, `SyncEntry`, `Validate`, `normalizeTaskCommands`, `detectCycle`,
`validateSourceSpec`, and the four count and range bounds), `internal/api/jobs.go`
(`handleCreateJob`), `internal/api/server.go` (`readJSON`, `writeError`, `maxBodyBytes`), the
`relay submit` and Source-workspaces sections of README.md, and `web/src/lib/api.ts` (`ApiError`).

### Shape

Job level: `name` (required), `priority` (optional; empty, `low`, `normal`, `high`), `labels`
(optional string map), `tasks` (required, at least one).

Task level: `name` (required, unique in the job); exactly one of `command` (one argv) or `commands`
(argv arrays) - setting both is refused, and a lone `command` is normalized server-side into a
one-element `commands`; `env` (string map); `requires` (string map); `timeout_seconds` (integer or
absent); `retries` (integer); `depends_on` (task names); `source` (optional Perforce block).

Source level, all of which the builder must be able to REFUSE on import even though it cannot yet
author it: `type` (must be `perforce`), `stream` (required, must begin with a double slash), `sync`
(at least one entry of `path` plus `rev`, where `path` must be the stream itself, the stream
followed by an ellipsis segment, or a path under the stream, and `rev` must match one of four
shapes - a head token, an at-sign followed by digits, an at-sign followed by label characters, or a
hash followed by digits), `unshelves` (positive integers), `workspace_exclusive` (boolean),
`client_template` (a restricted character set).

### Bounds the client must NOT encode

`retries` 0 to 10, `timeout_seconds` 0 to 604800, tasks per job at most 5000, commands per task at
most 500, commands per job at most 25000. Every one of these is a policy number that
`jobspec.Validate` also applies retroactively to STORED scheduled-job specs, and `specTemplate.ts`
already carries the reason they must not be mirrored client-side: a number written in the SPA makes
it refuse a spec the server would accept, or accept one it would refuse, on the first release that
moves either. Design consequence, stated here so it is not rediscovered at review: the builder's
numeric inputs carry no `min`, no `max`, and no length attribute derived from any of these.

### Error surface

Every failure is a single top-level `error` string. `writeError` produces `{"error": msg}` and
there is no per-field error map anywhere in the response. `readJSON` answers a malformed or
mistyped body with `invalid request body` (400) and an oversize body with `request body too large`
(413), both before `ValidateJobSpec` runs. `ApiError` carries the server's string verbatim in
`code` and a status-prefixed copy in `message`.

`readJSON` does NOT call `DisallowUnknownFields`. A body carrying keys the server does not know is
accepted and the unknown keys are ignored. This single fact drives the coexistence decision below.

### Messages the server actually produces

Quoted because a later decision turns on never matching them: `name is required`; `at least one
task is required`; `at most 5000 tasks are allowed, got N`; `invalid priority "x": must be low,
normal, or high`; `task name is required`; `task T: set either command or commands, not both`;
`task T: commands is required`; `task T: commands[i]: argv must not be empty`; `duplicate task
name: T`; `task T: at most 500 commands are allowed, got N`; `at most 25000 commands in total
across all tasks are allowed`; `task T: retries must be between 0 and 10`; `task T: timeout_seconds
must be between 0 and 604800 (0 or omitted means no deadline)`; `unknown depends_on: D`;
`dependency cycle detected involving tasks: a, b`; and the source family under a `task T:` prefix.

## What the hi-fi shows, and what it does not

**There is no New Job dialog in the hi-fi, and no form builder of any kind.** Searched
`design_handoff_relay_holo/hifi3-holo-pages.jsx` for every spelling of new job, dialog, modal, and
job spec. The results are three surfaces, none of which is this one.

1. The only job-spec surface the hi-fi draws is READ-ONLY, on the schedule detail page. It is the
   shape the builder's JSON preview borrows:

```jsx
          {/* Job spec */}
          <div style={{...glassPanel(C),flex:1,minHeight:0,display:'flex',flexDirection:'column'}}>
            <div style={{padding:'12px 18px',borderBottom:`1px solid ${C.border}`,display:'flex',justifyContent:'space-between',alignItems:'center'}}>
              <span style={{fontSize:13,color:C.fg}}>Job spec</span>
              <span style={{display:'flex',gap:8,alignItems:'center'}}>
                <span style={{fontFamily:C.mono,fontSize:10,letterSpacing:'0.06em',color:C.fgDim}}>YAML - submitted per tick</span>
                <button style={{...miniBtn(C,'ghost')}}>Edit</button>
              </span>
            </div>
            <pre style={{margin:0,padding:'14px 18px',fontFamily:C.mono,fontSize:12,lineHeight:1.6,
              color:C.fg,whiteSpace:'pre',overflow:'auto',background:'rgba(0,0,0,0.25)',flex:1}}>{SPEC}</pre>
          </div>
```

   Note the mock labels that spec YAML. Relay accepts JSON only. This is the same class of hi-fi
   divergence `ScheduleTriggerForm` already recorded when it declined to render the mock's third
   overlap option, and it is resolved the same way: follow the server, not the mock.

2. The only form-shaped mock is `AdminTokenModal`, which is where the label-over-input and
   segmented-choice styling comes from:

```jsx
          <label style={{display:'flex',flexDirection:'column',gap:6}}>
            <span style={{fontFamily:C.mono,fontSize:10,letterSpacing:'0.16em',color:C.fgMute}}>
              {isInvite ? 'EMAIL - OPTIONAL - BINDS INVITE' : 'HOSTNAME_HINT - OPTIONAL'}
            </span>
            <input placeholder={isInvite?'partner@vendor.io':'farm-west-13'} style={{
              padding:'8px 12px',borderRadius:6,background:'rgba(0,0,0,0.3)',
              border:`1px solid ${C.border}`,color:C.fg,fontFamily:C.sans,fontSize:13,outline:'none',
            }}/>
          </label>
```

   and, for a small set of mutually exclusive values:

```jsx
            <div style={{display:'flex',gap:6}}>
              {(isInvite ? ['24h','72h','7d','30d'] : ['1h','24h','3d','7d']).map((v,i)=>(
                <button key={v} style={{
                  flex:1,padding:'6px 10px',borderRadius:6,cursor:'pointer',
                  background: i===1?`linear-gradient(90deg, ${hexToRgba(C.accent,0.25)}, ${hexToRgba(C.accentB,0.18)})`:'rgba(255,255,255,0.04)',
                  border:`1px solid ${i===1?C.accent+'66':C.border}`,
                  color: i===1?C.fg:C.fgMute, fontFamily:C.mono,fontSize:11,letterSpacing:'0.06em',
                }}>{v}</button>
              ))}
            </div>
```

   with its footer pairing a ghost dismiss and a primary confirm:

```jsx
        <div style={{display:'flex',gap:8,justifyContent:'flex-end',marginTop:4}}>
          <button onClick={onClose} style={pillBtn(C,'ghost')}>Cancel</button>
          <button style={pillBtn(C,'primary')}>{isInvite?'Generate invite':'Enroll'}</button>
        </div>
```

3. The wireframe reference draws only the ENTRY point, already shipped:

```html
                <button class="btn accent">+ New job</button>
```

   **Read that layer narrowly.** `design_handoff_relay_holo/reference/` is the hand-drawn sketch
   layer, which is structure-only; its stylesheet is a paper-and-ink pastiche and says nothing
   about how anything in this product looks:

```css
.btn {
  font-family: var(--hand);
  font-size: 12px;
  padding: 4px 12px;
  border: 1.8px solid var(--ink);
  border-radius: 8px;
  background: var(--paper);
  cursor: pointer;
  transform: rotate(-0.3deg);
  box-shadow: 2px 2px 0 var(--shadow);
}
.btn.accent {
  background: var(--accent);
  color: var(--paper);
  border-color: var(--accent);
  box-shadow: 2px 2px 0 var(--shadow);
}
```

   The authoritative styling for the same control is the hi-fi's own helper, which the shipped
   `PillButton` implements variant for variant:

```jsx
function pillBtn(C, kind){
  const base = {padding:'8px 16px',borderRadius:999,fontFamily:C.sans,
    fontSize:12,letterSpacing:'0.02em',cursor:'pointer',backdropFilter:'blur(8px)',border:'none'};
  if(kind==='primary') return {...base,
    background:`linear-gradient(90deg, ${C.accent}, ${C.accentB})`,
    color:'#fff', fontWeight:600,
  };
  return {...base, background:'rgba(255,255,255,0.05)',border:`1px solid ${C.border}`,color:C.fg};
}
```

**So the builder is designed from the shipped primitives, not from a mock.** The primitives it
composes, named so the planner does not invent new ones:

- `web/src/components/holo/GlassPanel.tsx` - the panel each section sits in.
- `web/src/components/holo/Eyebrow.tsx` - the small uppercase section label.
- `web/src/components/holo/PillButton.tsx` - every action, including the per-row add and remove
  controls, with the primary variant reserved for Create job.
- `web/src/components/Field.tsx` - the label, the optional hint, and the error wiring. It already
  renders the mono uppercase micro-label the hi-fi's modal draws, already sets `role="alert"` on
  its error text, and already clones the control to add `aria-describedby`.
- `web/src/components/Input.tsx` - the text control `Field` wraps.
- `web/src/schedules/ScheduleTriggerForm.tsx` is the closest shipped sibling and the pattern to
  follow wholesale: `Field` plus `Input`, a segmented group of buttons carrying `aria-pressed` for
  a small closed value set, no client-side re-validation of a server rule, and the server's message
  rendered verbatim INSIDE the form rather than in a page-level banner.

## What I refuted

A backlog proposal is not a contract. Each bullet below was checked against the tree.

1. **There is no New Job dialog.** The lane brief and the item both say dialog. The shipped surface
   is a route page, `NewJobPage` at `/jobs/new`, chosen in the 2026-07-01 spec for reasons that got
   stronger rather than weaker (linkable, survives a reload, full-height editor). A builder is more
   content than a textarea, not less. **Consequence for the sweep guard**: this lane adds no
   dialog, so `dialogShellIsSole.guard.test.ts` is satisfied without an allowlist entry and without
   composing `DialogShell`. That is a vacuous pass, and saying so is the point - a reader must not
   come away believing the guard was exercised. The one way this lane could engage it is by
   building the dependency picker as a floating overlay; the design below makes it inline
   specifically so that never happens, and if a future revision reaches for a portal, the guard's
   portal assertion goes red naming the file.
2. **"Replace or augment the raw JSON textarea" - replace is not viable.** `readJSON` does not
   reject unknown fields, and `createJob(spec: unknown)` posts the parsed object verbatim precisely
   so a new `TaskSpec` field needs no client change. Deleting JSON mode would remove the only route
   for any spec the builder cannot type - which in this slice includes every spec with a `source`,
   and after this slice includes every spec using a field added to `jobspec.TaskSpec` after the
   builder was written.
3. **The item's source citation is off by one file.** It names `internal/api/job_spec.go
   (jobspec.Validate)`. The validator lives in `internal/jobspec/jobspec.go`; the api package
   re-exports it. Minor, and worth correcting because the bounds and their reasoning - the part of
   the contract this lane must not copy - are only readable in the real file.
4. **"Surfaces backend validation errors inline" cannot mean per-field.** Verified: one top-level
   `error` string in every branch, no field map anywhere. Inline here means "in the form, beside
   the submit control", which is what the JSON editor already does.
5. **The hi-fi's job-spec panel is labelled YAML.** Relay accepts JSON only. Do not read the mock
   as a deferred YAML feature.
6. **"Timeout" and "retries" as per-task fields is correct, and their bounds are real** - but the
   builder must not encode them. See the Bounds section above; this refutes the natural reading of
   the item's per-task-rows bullet, which would put a range on the input.

## Decisions

### D1 - How do the JSON editor and the builder coexist?

**Question.** Where does authority live between the JSON text and the structured form, and what
happens to a hand-authored spec the builder cannot represent?

**Options.**
(a) Builder only. Delete the textarea.
(b) Two modes with two-way automatic sync: every builder edit rewrites the JSON, every JSON edit
    re-parses into the builder.
(c) Two modes, ONE automatic direction. Builder state always renders into a read-only JSON preview.
    Going the other way is an explicit user action that either fully models the text or REFUSES
    with a reason, leaving the text untouched.

**Decision: (c).**

**Reason.** The hazard here is not that the builder validates too much. It is that a typed builder
silently DROPS what it cannot type, and the server cannot tell you it happened, because it ignores
unknown keys. Under (b) that loss is continuous and invisible: one keystroke in a builder field
rewrites the JSON and the dropped key is gone with no event anywhere. Under (a) there is no route
left for an unrepresentable spec at all, and the first field added to `jobspec.TaskSpec` after this
lane makes the SPA strictly less capable than `relay submit` with no signal. Under (c) the lossy
direction is the only one that is automatic, and it is lossy by construction only in the sense that
the builder can emit exactly what it can model - so nothing the user typed is ever discarded
without them being told.

**The refusal rule is the load-bearing half of this decision.** Entering builder mode parses the
text and attempts to model every key. The switch is refused, naming the first offending path, when:

- any key is present that the builder does not know, at the job level, the task level, or inside a
  `source` block for as long as the source builder is unbuilt (for example `tasks[2].widget`, or
  `tasks[0].source`);
- a task sets both `command` and `commands`, which the builder has no state to represent (the
  server also refuses this, and the user is told the builder cannot model it, not that it is
  invalid);
- the top level is not an object, or `tasks` is not an array of objects.

In every case the user stays in JSON mode with their text byte-identical. The builder fails CLOSED
against a field it has never heard of, which is the property that keeps this design safe as
`jobspec.TaskSpec` grows.

**Default mode: builder.** The item's premise is job creation without hand-authoring JSON, so the
builder is what `/jobs/new` opens in. JSON mode is one control away and the draft survives the
switch. The cost is real and is called out as escalation E1.

### D2 - What is the data model between form state and the wire spec?

**Question.** Does the form hold the wire shape directly, or its own shape with one mapping?

**Options.**
(a) Form state IS the wire object; controls read and write into it.
(b) A distinct `BuilderState`, plus one mapping function each way, in one module.

**Decision: (b).** One module, provisionally `web/src/jobs/specBuilder.ts`, exporting
`BuilderState`, `toSpec(state)` and `fromSpec(json)`.

**Reason.** Three things the wire shape cannot hold, each of which is a defect if it is missing.
First, the user's raw text for numeric fields: a half-typed number is not an integer, and the state
must be able to hold what was typed without inventing a value. Second, stable per-row identity: the
dependency picker and the label associations both need an identity that survives a rename and a
remove, which a name string cannot provide (see D3). Third, the command spelling flag: `command`
and `commands` are two spellings of the same thing, and only a flag can round-trip a
single-element `commands` back out as `commands` (see D6). Putting the mapping in one tested
module also means the mapping is testable without rendering anything, which is where most of the
kill-power in the test plan lives.

**Emission rules, which are part of the contract of `toSpec`.**

- An untouched optional emits no key at all - no empty object for `env`, `requires` or `labels`, no
  null for `timeout_seconds`, no empty string for `priority`. A builder-authored minimal job is
  therefore byte-comparable with a hand-written one and with `STARTER_TEMPLATE`.
- A blank argv token is dropped, and a command left with no tokens is omitted; a task whose
  commands are all omitted emits neither `command` nor `commands` and earns the server's `commands
  is required`. None of that is silent: the read-only preview shows exactly the object that will be
  posted, so a dropped blank is visible before submit.
- A numeric field whose text parses as a JSON number emits that number. Empty emits no key. Any
  other text is emitted verbatim, so the server refuses it rather than the client inventing a
  value - see escalation E2 for what that costs.

**Tested against the server's own examples**, not against invented fixtures: the README `relay
submit` job file, `STARTER_TEMPLATE` from `specTemplate.ts`, and the README `source` example (which
must take the refusal path in this slice).

### D3 - How does the dependency picker work, and where are cycles rejected?

**Question.** How is `depends_on` authored, and which side detects a cycle?

**Options.**
(a) A free-text comma-separated list. No better than JSON.
(b) A multi-select over the other task rows, storing the selected NAMES.
(c) A multi-select over the other task rows, storing stable row IDS and resolving to names at
    emission time.
(d) A graph editor.

**Decision: (c), and cycles are rejected SERVER-side only.**

The picker is an inline group of toggles inside the task row - one per other task, labelled with
that task's current name. It is not a dialog, not a popover and not a portal, which is what keeps
the dialog sweep guard out of this lane's way (see refutation 1).

**Reason for (c) over (b).** Storing names re-creates the dangling-reference bug on every rename:
rename `a` to `alpha` and every task that depended on it still emits `a`, earning `unknown
depends_on: a` from a spec the user believes they just fixed. Storing row ids and resolving at
emission makes that class unreachable from the builder, without teaching the client any rule the
server owns. (d) is a different product.

**Reason for server-side cycle rejection.** `detectCycle` is Kahn's algorithm over the whole graph,
and its message names the participating tasks. Re-implementing it in TypeScript is the parallel
validator the single job-spec pipeline invariant forbids, and this repo already has the evidence
for what that costs: `internal/jobspec/jobspec.go` records that the Python SDK reproduced five
message texts verbatim and that the dependency messages have ALREADY drifted between the two. A
third copy joins a coupling that is known-broken. A cycle is only creatable deliberately, the round
trip is one request, and the server's answer names the tasks. That is a good trade.

A task is not offered as a dependency of itself. That is an affordance, not a rule: `jobspec` has
no self-dependency message, so a self-edge reaches `detectCycle` and comes back as a cycle naming
the task. Nothing client-side asserts otherwise.

### D4 - How does the server's 400 map to a field?

**Question.** Given one top-level `error` string and no field map, does the builder attach the
message to a control?

**Options.**
(a) Match the message text against known Go format strings and bind it to a control.
(b) Render it verbatim, once, in the form, beside the submit control.
(c) Hybrid: render verbatim, and additionally highlight a control when the text matches.

**Decision: (b). No message-text matching, ever, including in the hybrid form.**

**Reason.** The messages are Go format strings with nothing on the Go side protecting their
wording, and the codebase already carries a worked example of what happens when a client couples to
them - the Python SDK's copies, two of which are already out of step with the server. A TypeScript
regex over `duplicate task name: %s` is the same defect with a fresh spelling, and its failure mode
is silent: on the release that rewords a message, the field highlight simply stops appearing, and
nothing goes red. (c) is not a safe middle: the highlight is exactly the part that needs the match.

Placement follows `ScheduleTriggerForm`'s recorded reason - an error routed to a page-level banner
can be rendered somewhere the user is not looking - so the alert sits in the form, immediately
above the submit row, with `role="alert"`, and it renders `ApiError.message` for continuity with
the shipped tests that assert against the status-prefixed string.

One thing the builder buys for free here and should be credited with: the `invalid request body`
class - a JSON syntax error, or a string where a number belongs - is almost entirely eliminated in
builder mode, because the builder controls the encoding. What remains of it is escalation E2.

### D5 - Does the Perforce source builder ship in this lane?

**Question.** The item is one medium item with three named parts. Is the source builder one of
them, here?

**Options.**
(a) Ship all three parts in this lane.
(b) Ship the task rows and the dependency picker; file the source builder as a follow-up.

**Decision: (b).**

**Reason, three concrete ones rather than a size adjective.** First, `source` is a second nested
repeater with its own grammar: `sync` is a list of two-field records, `unshelves` is a list of
integers, `rev` has four accepted shapes, and `path` has a containment rule against `stream`. That
is another full add/remove/focus/announce surface on top of the task repeater this lane already
builds, and the containment rule is the exact kind of thing a builder is tempted to pre-check,
which is where the invariant gets bent. Second, it is the only part of the builder whose output is
unverifiable in this repo's harnesses: `make test-e2e` runs no `relay-agent` at all, and the
Perforce path needs a p4d and a ticket, so a source block authored in the SPA could be shown to
serialize but never shown to work. Third - and this is what makes deferring safe rather than merely
convenient - the D1 refusal rule means a spec carrying a `source` cannot enter builder mode at all
until the follow-up lands. Nothing about a source is silently dropped in the interim; it is
refused, by name, and the user stays in the JSON editor that has always handled it.

This decision is conditioned on the follow-up being findable, so the follow-up is filed rather than
promised: see Follow-ups.

### D6 - How are commands authored, given that argv is an array?

**Question.** A command is an array of strings. A text input is a string. What is the mapping?

**Options.**
(a) One text input per command, split on whitespace.
(b) One text input per command, parsed with shell-style quoting.
(c) One input per argv token, in a small inline repeater, with a read-only joined preview.
(d) One text input per command holding a JSON array literal.

**Decision: (c).**

**Reason.** (a) is a correctness bug the first time anyone types a path with a space in it, and it
is a silent one - the spec serializes, dispatches, and fails on the agent. (b) invents a quoting
grammar and makes the SPA its owner: nothing in relay's Go, from `jobspec` through the dispatcher
to `Runner`, splits or unquotes anything, so a rule invented here would be relay's de facto quoting
rule with no server-side counterpart and no test that could pin it. (d) is JSON with extra steps,
inside the surface whose entire purpose is not writing JSON. (c) is verbose, and it is exactly the
wire model: a program plus arguments, each one its own value, with an argument containing a space
expressible and visibly so. The joined preview mirrors what `SpecTab` already renders for a task's
commands today, so the reading and the writing surfaces agree.

**Single versus multiple commands.** A new task row starts in the `command` spelling with one argv,
matching `STARTER_TEMPLATE` and the README example. An explicit control adds a second command and
promotes the task to the `commands` spelling. The promotion is recorded in a per-row flag and is
NOT reversed by removing the second command, because the flag exists to round-trip an imported
spec: a task imported as `commands` with exactly one entry must be re-emitted as `commands`, and a
count-derived rule would silently rewrite the user's spelling. The builder never sets both, so
`set either command or commands, not both` is unreachable from the builder - and still enforced by
the server for every other ingest path.

### D7 - What does the builder show for env keys today?

**Question.** The agent silently discards two classes of env key. Does the builder say anything?

**Facts, verified.** Nothing anywhere in the ingest pipeline inspects env keys. `jobspec.TaskSpec`
declares `Env` as a plain string map and `Validate` never looks at it. The shipped behaviour lives
at the far end: `Runner` strips the four reserved identity names (`RELAY_JOB_ID`, `RELAY_TASK_ID`,
`RELAY_JOB_URL`, `RELAY_TASK_URL`) from a spec's env, and refuses any key containing an equals
sign, both silently. A key containing a NUL byte fails the task at start with an empty error
message. So today the JSON editor shows the user nothing at all about env keys, the server accepts
whatever they typed, and the loss happens later with no signal. The open item
`docs/backlog/feature-2026-09-01-validate-env-keys-in-the-job-spec-pipeline.md` is where that gap
is tracked, and its own body records why it was not folded into its originating slice: tightening
`jobspec.Validate` is retroactive over stored scheduled-job specs.

**Options.** (a) Nothing. (b) A non-blocking inline note beside a key that contains an equals sign
or matches a reserved name. (c) A client-side refusal.

**Decision: (a). The builder posts env keys verbatim and says nothing about them.**

**Reason.** (c) is a second validator outright. (b) is subtler and still wrong: the four reserved
names are an agent-side constant with no wire representation, so a hint would be a hand-copied
constant on a separate release cadence, and a hint that goes stale is worse than no hint - it tells
the user a key is fine when the agent will drop it. The correct home for that knowledge is the
server, at submit time, which is precisely the open item. When that item lands, the builder
inherits a 400 through the same error surface with ZERO client change, which is the whole reason
this design keeps the client schema-free. Revisit only if the reserved list gains a wire
representation; see escalation E3.

**Duplicate keys are a different matter and DO get a note.** Two rows with the same key cannot both
survive into a JSON object; the last one wins, silently. That is a statement about the BUILDER's
own encoding, not about any server rule, it cannot drift against anything, and it never blocks
submit. So a duplicated key renders a short inline note saying the last row wins. The JSON editor
has the identical hazard today via `JSON.parse`; this makes it visible rather than introducing it.

### D8 - Accessibility of dynamic rows

**Question.** Rows appear and disappear. What is announced, where does focus go, and how are labels
associated?

**Decision.**

- **Ids are keyed by a stable row id, never by index.** `task-<rowId>-name` and so on. An
  index-keyed id re-associates every label below a removed row, so the control a screen reader
  announces after a remove is not the one it names.
- **Each task row is a group with an accessible name** derived from the row's current name, falling
  back to a positional name when the field is empty, so a screen reader's group list distinguishes
  them.
- **Every remove control carries a per-row accessible name** ("Remove task <name>"), never a bare
  glyph, so a button list is navigable.
- **Add moves focus to the new row's name input.** Remove moves focus to the next row's remove
  control if one exists, else the previous row's, else the Add task control. Focus never falls to
  the document body - the same class of silent regression `DialogShell`'s landmark fallback exists
  to prevent, one level down.
- **One polite live region per page**, following the shipped `role="status"` with `aria-live` set
  to polite precedent in `StatSection` and `HealthPill`, announcing "Task N added" and "Task
  <name> removed". One region, shared by the task repeater and the nested env, requires and command
  repeaters, so two announcements never race in two regions.
- The same three rules - labelled, named remove, defined focus target - apply to the nested
  repeaters.

### D9 - Where does the state live when the surface goes away?

**Question.** The page can be navigated away from or reloaded. Is the draft persisted?

**Options.** (a) Component state; the draft dies with the page, as today. (b) A `localStorage`
draft.

**Decision: (a), unchanged from the 2026-07-01 spec's open decision.**

**Reason.** A draft store has its own questions - per-user keying, staleness, clearing on success -
and one that this repo takes seriously enough to have built a whole secrecy test suite around: a
job spec's `env` values are exactly where somebody puts a token, and a draft store writes them to
disk, unexpiring, readable by anything with access to the origin's storage. That is a deliberate
decision with a threat model, not a small convenience, so it does not ride along in a lane about
form controls.

Within the page, the mode switch preserves everything: builder to JSON is total, and JSON to
builder refuses rather than drops, so a full switch cycle loses nothing the builder can represent
and never damages text it cannot.

### D10 - Priority and labels

Priority is a segmented group of the three server values with `aria-pressed`, matching
`ScheduleTriggerForm`'s overlap group and the hi-fi's segmented buttons. It has four states -
unset, low, normal, high - where unset emits no key and a fresh builder preselects `normal` so a
builder-authored minimal job matches `STARTER_TEMPLATE`. No fourth value is offered: the server's
set is closed, and `ScheduleTriggerForm` already set the precedent of declining to render a hi-fi
option the server refuses.

Labels are a key-value repeater with the same rules as env.

## Slice boundary

**In this lane.**

- Builder mode on `/jobs/new`, default, with a mode switch to the existing JSON editor.
- Job name, priority, labels.
- Task rows: name; commands as an argv token repeater with single and multi-command spellings; env;
  requires; timeout; retries; dependency picker.
- Read-only JSON preview of exactly what will be posted.
- Refusing import from JSON to builder, naming the first unrepresentable path.
- `specBuilder.ts` with `toSpec` and `fromSpec`, tested against the server's own examples.
- Accessibility work in D8.
- Server error rendered verbatim, in the form.
- An e2e surface and a dedicated e2e spec at 320, 375 and 1280.

**Not in this lane, each with its reason recorded above.**

- The Perforce source builder (D5). Filed.
- Draft persistence (D9). The 2026-07-01 spec's open decision 5 stands.
- Any client-side semantic validation: cycles, uniqueness, ranges, env-key rules, source rules.
- Any per-field binding of a server message (D4).
- Any change to `POST /v1/jobs`, `jobspec`, or the CLI.

## Test plan and acceptance criteria

Every criterion names its test and the mutation it kills. Unit tests are Vitest plus Testing
Library plus MSW, following the shipped conventions in `web/src/jobs/`. Where a test needs a
discriminating input, that input goes FIRST in the test body, not last, so an early-exit mutation
cannot pass by never reaching it.

**AC1 - an untouched optional emits no key.** `specBuilder.test.ts`: a fresh builder with one named
task carrying one argv emits exactly `name`, `priority` and `tasks`, and the task object has
exactly `name` and `command`. *Kills*: emitting an empty `env` or `labels` object, a null
`timeout_seconds`, or an empty-string `priority`.

**AC2 - round trip against the server's own examples.** `specBuilder.test.ts`: for the README
`relay submit` job file and for `STARTER_TEMPLATE`, `fromSpec` succeeds and `toSpec` of the result
is deep-equal to the input. *Kills*: normalizing `command` into `commands` on import; dropping
`env`, `requires`, `labels` or `depends_on` on import; reordering or defaulting any field.

**AC3 - the README source example is REFUSED, not partially modelled.** `specBuilder.test.ts`:
`fromSpec` of the README source example returns not-ok and the message names `tasks[0].source`.
*Kills*: an import that models the keys it knows and ignores the rest - the silent-drop defect this
whole design is organized around.

**AC4 - an unknown key is refused, at either level.** `specBuilder.test.ts`: a task carrying a key
no builder field maps to is refused naming `tasks[0].<key>`; a job-level unknown key is refused
naming that key. *Kills*: a permissive import that keeps known keys and discards the rest. This is
the guard that makes the builder fail closed when `jobspec.TaskSpec` gains a field, so it is the
single most load-bearing test in the lane.

**AC5 - the builder never blocks a submit on a server rule.** `NewJobPage` builder tests, four
siblings, each asserting the POST is issued and the server's verbatim message is rendered:
(a) a dependency cycle, answered with `dependency cycle detected involving tasks: a, b`;
(b) two tasks with the same name, answered with `duplicate task name: build`;
(c) retries typed as 99, answered with `task t: retries must be between 0 and 10`;
(d) a task with every command left blank, answered with `task t: commands is required`.
*Kills*: a client-side Kahn cycle check; a client-side uniqueness check; `min` and `max` attributes
or a range check on the numeric inputs; a client-side required-command refusal. Each of these is a
parallel validator, and each would keep the test green only by never issuing the request - which is
exactly what the assertion is on.

**AC6 - env keys are posted verbatim.** `NewJobPage` builder test: an env key containing an equals
sign and an env key spelling a reserved identity name are both present, unaltered, in the POST
body, and no refusal, hint or note about either appears. *Kills*: a client-side env-key regex; a
reserved-name warning built from a copied constant.

**AC7 - a dependency follows a rename.** `specBuilder.test.ts`: with task B depending on task A,
renaming A rewrites B's emitted `depends_on`. *Kills*: storing the selection as a name string at
pick time, which would emit the stale name and earn `unknown depends_on`.

**AC8 - the command spelling round-trips.** `specBuilder.test.ts`: a task imported with `commands`
holding exactly one argv re-emits as `commands`; a task imported with `command` re-emits as
`command`. *Kills*: a count-derived rule that rewrites a single-element `commands` into `command`.

**AC9 - argv is never split.** `specBuilder.test.ts` and one `NewJobPage` builder test: an argument
typed with an internal space is emitted as ONE argv element. *Kills*: whitespace splitting; any
shell-quoting parser.

**AC10 - the mode switch is one-directional and refusing.** Two `NewJobPage` tests: (a) edit in
builder mode, switch to JSON, and the text parses to an object deep-equal to what `toSpec`
produces; (b) in JSON mode, add an unknown key, attempt the switch, and the page stays in JSON mode
with a refusal naming the path and the textarea's value byte-identical to what was typed. *Kills*:
a two-way sync that overwrites the JSON text from builder state on mode entry - which would destroy
the unrepresentable text and is the whole reason (c) beat (b) in D1.

**AC11 - dynamic-row accessibility.** Three `NewJobPage` builder tests: (a) adding a task moves
focus to the new row's name input and the live region's text names the added task; (b) removing the
middle of three rows moves focus to the next row's remove control, the removed row's fields are
gone, and each surviving row's name input is still reachable BY ITS LABEL scoped to that row;
(c) removing the last remaining removable row moves focus to the Add task control. *Kills*:
dropping the focus call, which leaves focus on the document body; index-keyed control ids, which
survive (a) and (c) but fail the label association in (b).

**AC12 - the submit path is unchanged under builder mode.** `NewJobPage` builder tests: exactly one
POST per click; navigation to `/jobs/:id` only after a 201; the submit control disabled while
pending; a stale server error cleared on the next submit. *Kills*: a double POST on a double click;
navigation fired optimistically before the response.

**AC13 - the 400 is passed through, never parsed.** `NewJobPage` builder test, with the
discriminating input first: a 400 whose body is a string no `jobspec` rule produces renders that
exact string in the alert, and no control is marked invalid. *Kills*: any message-to-field mapping,
which would either swallow the unfamiliar string or attach it to an arbitrary control.

**AC14 - the JSON-mode tests survive.** The shipped `NewJobPage.test.tsx` tests keep asserting the
same properties, re-homed behind a render helper that puts the page in JSON mode. They pin real
server-contract behaviour - the verbatim 400, the 413, the pending disable, the error reset, the
route-collision guard - and none of that is superseded by the builder. *Kills*: quietly dropping a
test whose selector broke rather than re-homing it.

**AC15 - narrow-viewport behaviour, in a real browser.** Two parts.
(a) A new `job-new-builder` surface in `web/e2e/surfaces.ts`, population `populated`, whose `ready`
    adds a second task row and gates on that row's own name input being visible - so the surface
    measures the POPULATED builder, not an empty one wearing its name. `layout.spec.ts` then
    asserts at 320, 375 and 1280 that the document, the header and the main region do not overflow,
    and writes its screenshots.
(b) A new `web/e2e/job-builder.spec.ts` running at the same three widths asserting, with two task
    rows present, that a row's remove control and the Create job control are both
    `toBeInViewport()`. This is deliberately NOT a width comparison: the harness's own README
    records that a `scrollWidth` gate cannot tell "fits" from "clipped behind a scroller", and a
    row of inputs is exactly the shape that clips. *Kills*: a row layout that overflows into its own
    horizontal scroller, which every assertion in (a) would report as passing.

**Gate note for (a) and (b):** a `test.fixme` in that directory must cite a filed backlog item id.
If either part cannot be made to pass, it is a finding, not a skip.

## Gates

Run from `web/` unless noted. All must be green before the lane is offered for integration.

- `npm test`
- `npx tsc -b --force`
- `npm run build`
- `make test-e2e` (from the repo root, in Git Bash; needs the Postgres container and the Playwright
  browsers - see `web/e2e/README.md`, including the note that rebuilding `relay-server` without a
  fresh `make web-build` silently embeds the tracked placeholder and produces a wall of
  unrelated-looking timeouts)
- `git checkout -- web/dist/` before assembling the PR. `web/dist` is tracked and not maintained
  per-PR; `make test-e2e` restores the placeholder on exit, and any other build leaves a diff that
  does not belong in this lane.

Hygiene for any programmatic edit to a tracked text file in this lane: check the diffstat against
the size of the intended change, confirm `git ls-files --eol` reads `i/lf` on every touched path,
and confirm the file still decodes as UTF-8. `gofmt -l` is not a signal on this tree.

## Invariants and system-design lens

**Single job-spec pipeline.** This is the invariant the whole design turns on. The SPA gains a
richer input surface and no new authority: it posts through `createJob(spec: unknown)` to the one
ingest path, and it implements no rule `jobspec.Validate` owns - no cycle detection, no uniqueness
check, no range, no source grammar, no env-key rule. The builder's client-side work is structural
only: it decides what SHAPE a value has, never whether the value is allowed. The one place a typed
client could still drift is silent omission, and that is closed from the other end by the D1
refusal rule plus AC3 and AC4: a key the builder does not know stops the import instead of
disappearing from it.

**End the generation before releasing the resource.** No async lifecycle is added. The mutation is
a single POST through the shipped `useCreateJob`; there is no stream, no abort controller, no
subscription. Nothing in this lane acquires a resource whose callbacks could outlive it.

**One bounded sender, epoch fence, identity-checked teardown.** Not touched; no gRPC, no task
status write, no worker state.

**Single JSON entry point.** Server-side unchanged - bodies still arrive only through `readJSON`,
which keeps the 1 MiB cap and the 413.

**Load and failure modes.** One request per submit, unchanged in count and shape. The builder can
express a larger body than a human would hand-type - a task repeater makes 200 rows a matter of
clicking - so the practical failure mode moves slightly toward the 413 and the count bounds. Both
are already enforced, both surface as a verbatim message through the same alert, and the builder
adds no client-side cap that could disagree with them. Worth stating plainly for the reviewer: the
builder is a faster way to reach `at most 5000 tasks are allowed` than the textarea was, and that
is the bound working, not a regression.

**Threat model.** Creation is authenticated and unprivileged; `submitted_by` is set server-side
from the caller, so nothing here can forge ownership. The builder introduces no new persistence
(D9), so no spec value - including a token typed into an env value - is written anywhere outside
the page's own memory. Nothing user-supplied is rendered as markup: the JSON preview and the error
alert are both text nodes. The error alert renders a server-authored string, which is the same
string the shipped page renders today.

**What a hostile client gains: nothing.** Every client-side affordance in this design is a
convenience over a request an attacker can already make directly with curl. That is the reason none
of the affordances may become the enforcement point.

## Escalations

**E1 - defaulting to builder mode rewrites the assertion base of `NewJobPage.test.tsx`.** The
shipped tests obtain the editor as the sole textbox on the page; with the builder rendered first,
that selector resolves to many controls. AC14 requires re-homing them behind a JSON-mode helper
rather than deleting any. If the conductor prefers zero churn there, the alternative is defaulting
to JSON with an opt-in switch - which costs the item's own premise. Recommendation: builder
default, and budget the re-homing.

**E2 - a non-numeric numeric still produces an opaque 400.** A value that is not a number where the
server expects one fails in the JSON decoder, so `readJSON` answers `invalid request body` before
`jobspec.Validate` runs and no message can name the field. The builder narrows this class almost to
nothing by owning the encoding, and the residual is not fixable client-side without adding the
client-side range and type checks this design refuses. `numberOf` recognizes an integer-shaped
string ahead of `JSON.parse`, so a leading zero like `"07"` - not valid JSON, since JSON forbids a
leading zero before more digits - no longer falls into this class; it parses to the plain integer
and reaches `jobspec.Validate` like any other value. The residual that remains, by design, is a
decimal typed into an integer-only field: `"2.5"` for `retries` still emits the JSON number `2.5`,
and Go's own decode of a fractional number into an `int32` field fails in the JSON decoder exactly
like any other shape mismatch - the builder does not distinguish "this field wants an integer" from
"this field wants any JSON number," and teaching it that distinction is the same client-side range
and type check this design refuses everywhere else. The principled fix is a server-side typed
decode error; it is out of this lane, and it is raised here rather than filed because it is a
design question about the API's error contract, not a defect.

**E3 - the reserved identity env names have no wire representation.** They are an agent-side
constant. Any client hint about them is a hand-copied constant on a separate release cadence, which
is why D7 declines to render one. If a hint is wanted later, the enabler is exposing the list from
the server - `GET /v1/config` is the existing shape for that kind of disclosure. Raised as a
question, not filed as work.

**E4 - README's source table omits `client_template`.** `validateSourceSpec` accepts and enforces
it (a restricted character set), the field is on `SourceSpec`, and the table under Source
workspaces lists five fields without it. A consumer implementing against the prose does not know
the field exists. This is a docs-versus-code disagreement, which this project treats as a defect
rather than a nicety. Proposed as a backlog item for human accept, not filed by this lane, since it
is a backend documentation fix with no frontend component.

## Follow-ups

**Filed by this lane, because a decision is conditioned on it** (D5): a follow-up item for the
Perforce source builder in the New Job builder, carrying the source schema, the reason it was cut
(a second nested repeater with a four-shape `rev` grammar and a stream-containment rule, and no
harness in this repo that can execute a source), and the property that makes the deferral safe (a
spec with a `source` is REFUSED entry into builder mode, never silently stripped).

**Proposed, awaiting human accept:** E4's README `client_template` gap.

## Open question for the reviewer

The one place this design chose usability over economy is D6's argv token repeater: three levels of
nesting (task, command, argument) is a lot of controls for what a user thinks of as one command
line. The alternative that saves a level is a quoting rule, and the reason it was refused is that
relay's Go has no such rule anywhere, so the SPA would become its owner. If a reviewer knows of a
splitting convention the project already commits to somewhere, that changes this decision and only
this one.
