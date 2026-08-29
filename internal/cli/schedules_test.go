package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"relay/internal/relayclient"
)

func TestSchedulesList_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "GET", r.Method)
		require.Equal(t, "/v1/scheduled-jobs", r.URL.Path)
		require.Equal(t, "Bearer tkn", r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(relayclient.PageEnvelope[scheduleResp]{
			Items: []scheduleResp{
				{ID: "abc", Name: "n", CronExpr: "@hourly", Timezone: "UTC", Enabled: true},
			},
			Total: 1,
		})
	}))
	defer srv.Close()
	cfg := &Config{ServerURL: srv.URL, Token: "tkn"}

	var buf bytes.Buffer
	err := doSchedules(context.Background(), cfg, []string{"list"}, &buf)
	require.NoError(t, err)
	require.Contains(t, buf.String(), "abc")
	require.Contains(t, buf.String(), "n")
}

func TestSchedulesCreate_Success(t *testing.T) {
	var receivedBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "POST", r.Method)
		require.Equal(t, "/v1/scheduled-jobs", r.URL.Path)
		require.NoError(t, json.NewDecoder(r.Body).Decode(&receivedBody))
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, `{"id":"abc","name":"nightly","cron_expr":"@hourly"}`)
	}))
	defer srv.Close()
	cfg := &Config{ServerURL: srv.URL, Token: "tkn"}

	dir := t.TempDir()
	specPath := filepath.Join(dir, "spec.json")
	spec := `{"name":"r","tasks":[{"name":"t","command":["echo","hi"]}]}`
	require.NoError(t, os.WriteFile(specPath, []byte(spec), 0600))

	var buf bytes.Buffer
	err := doSchedules(context.Background(), cfg,
		[]string{"create", "--name", "nightly", "--cron", "@hourly", "--spec", specPath},
		&buf)
	require.NoError(t, err)
	require.Equal(t, "nightly", receivedBody["name"])
	require.Equal(t, "@hourly", receivedBody["cron_expr"])
	require.Contains(t, buf.String(), "abc")
}

func TestSchedulesDelete_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "DELETE", r.Method)
		require.Equal(t, "/v1/scheduled-jobs/abc", r.URL.Path)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	cfg := &Config{ServerURL: srv.URL, Token: "tkn"}

	var buf bytes.Buffer
	err := doSchedules(context.Background(), cfg, []string{"delete", "abc"}, &buf)
	require.NoError(t, err)
	require.Contains(t, strings.ToLower(buf.String()), "deleted")
}

func TestSchedulesRunNow_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "POST", r.Method)
		require.Equal(t, "/v1/scheduled-jobs/abc/run-now", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, `{"id":"jobxyz","name":"r","status":"pending"}`)
	}))
	defer srv.Close()
	cfg := &Config{ServerURL: srv.URL, Token: "tkn"}

	var buf bytes.Buffer
	err := doSchedules(context.Background(), cfg, []string{"run-now", "abc"}, &buf)
	require.NoError(t, err)
	require.Contains(t, buf.String(), "jobxyz")
}

func TestSchedulesList_SortFlag(t *testing.T) {
	var capturedRawQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedRawQuery = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(relayclient.PageEnvelope[scheduleResp]{Items: []scheduleResp{}, Total: 0})
	}))
	defer srv.Close()

	cfg := &Config{ServerURL: srv.URL, Token: "tkn"}
	var buf bytes.Buffer
	err := doSchedules(context.Background(), cfg, []string{"list", "--sort", "-priority"}, &buf)
	require.NoError(t, err)
	require.Contains(t, capturedRawQuery, "sort=-priority")
}

func TestSchedulesUnknownSubcommand(t *testing.T) {
	cfg := &Config{ServerURL: "http://x", Token: "t"}
	err := doSchedules(context.Background(), cfg, []string{"bogus"}, io.Discard)
	require.Error(t, err)
}

// THE FIXTURE BODY IS HAND-WRITTEN JSON, NOT relayclient.PageEnvelope[scheduleResp]
// AND NOT scheduleResp. A fixture marshalled through the CLI's own response
// struct agrees with the decoder by construction, on the field names AND on the
// omitempty behaviour, so it can never detect drift in either direction. This
// file already carries such vacuous fixtures; do not add another. The exemplar
// to copy is writeTaskLogPage's locally-declared logRow in
// internal/cli/logs_test.go - read that one narrowly, since the same file gets
// it wrong 23 times for its job bodies.
func TestSchedulesShow_PrintsLastRunAndTheFailureWithItsProvenance(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "GET", r.Method)
		require.Equal(t, "/v1/scheduled-jobs/abc", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{
			"id":"abc","name":"nightly","cron_expr":"@hourly","timezone":"UTC",
			"overlap_policy":"skip","enabled":true,
			"next_run_at":"2099-01-01T00:00:00Z",
			"last_run_at":"2026-08-01T03:00:00Z",
			"last_error":"task t: retries must be between 0 and 10",
			"last_error_at":"2026-08-28T11:59:00Z"
		}`)
	}))
	defer srv.Close()
	cfg := &Config{ServerURL: srv.URL, Token: "tkn"}

	var buf bytes.Buffer
	require.NoError(t, doSchedules(context.Background(), cfg, []string{"show", "abc"}, &buf))
	out := buf.String()

	// LAST RUN IS THE OTHER HALF OF THE SIGNAL. Without it a failing schedule
	// looks identical to a healthy one in the single command an operator runs to
	// inspect a schedule: Next keeps advancing and nothing says the last actual
	// run was three weeks ago. It is one line and the field was already in the
	// struct, unprinted.
	require.Contains(t, out, "Last run:")
	require.Contains(t, out, "2026-08-01T03:00:00Z")

	// THE PROVENANCE PREFIX IS PART OF THE CONTRACT, not decoration. The text is
	// derived from the stored job_spec and embeds a task name the schedule's
	// owner chose, so an admin inspecting another user's schedule is reading
	// partly attacker-chosen prose. Naming where it came from is what stops
	// crafted text reading like relay's own output.
	require.Contains(t, out, "Last error (from the stored job_spec")
	require.Contains(t, out, "task t: retries must be between 0 and 10")
	require.Contains(t, out, "2026-08-28T11:59:00Z")

	// THE REMEDY MUST BE NAMED WHERE THE SIGNAL IS READ, and it must be a
	// command that exists. run-now returns the UNTRUNCATED message; the stored
	// value is capped at 1 KB.
	require.Contains(t, out, "relay schedules run-now abc")
}

// THE ABSENCE CASE. A healthy schedule's output must not grow an empty label:
// absent means healthy and there is nothing to say.
func TestSchedulesShow_HealthyScheduleMentionsNoFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{
			"id":"abc","name":"nightly","cron_expr":"@hourly","timezone":"UTC",
			"overlap_policy":"skip","enabled":true,
			"next_run_at":"2099-01-01T00:00:00Z"
		}`)
	}))
	defer srv.Close()
	cfg := &Config{ServerURL: srv.URL, Token: "tkn"}

	var buf bytes.Buffer
	require.NoError(t, doSchedules(context.Background(), cfg, []string{"show", "abc"}, &buf))
	out := buf.String()

	require.NotContains(t, out, "Last error")
	require.NotContains(t, out, "run-now")
	require.NotContains(t, out, "Last run:",
		"an absent last_run_at prints nothing, matching how Next is already handled")
	// CONTROL: the command still works and still prints the fields it always did.
	require.Contains(t, out, "Name:     nightly")
	require.Contains(t, out, "Enabled:  true")
}

// AN EXPLICITLY EMPTY last_error IS NOT A FAILURE, and this is the axis the
// original defect had: absent, empty and present are three states, not two. The
// server's write site never stores "", so a body carrying one is either a
// different server or a bug; either way it must read as healthy rather than
// print a labelled blank. A backstop that treated "" as a failure - or one that
// could not tell "" from absent - would re-create the original defect one layer
// up, which is why this case is asserted separately from the absence case above.
func TestSchedulesShow_AnEmptyLastErrorStringIsTreatedAsHealthy(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{
			"id":"abc","name":"nightly","cron_expr":"@hourly","timezone":"UTC",
			"overlap_policy":"skip","enabled":true,
			"next_run_at":"2099-01-01T00:00:00Z",
			"last_error":"","last_error_at":"2026-08-28T11:59:00Z"
		}`)
	}))
	defer srv.Close()
	cfg := &Config{ServerURL: srv.URL, Token: "tkn"}

	var buf bytes.Buffer
	require.NoError(t, doSchedules(context.Background(), cfg, []string{"show", "abc"}, &buf))
	out := buf.String()

	require.NotContains(t, out, "Last error")
	require.NotContains(t, out, "Failed at:",
		"last_error_at without a last_error says nothing an operator can act on")
	require.NotContains(t, out, "run-now")
}

// THE CLI IS A TERMINAL RENDERER AND last_error IS THE ONLY UNTRUSTED PROSE IT
// ECHOES, so the value is stripped HERE as well as at the server's write site.
// The duplication is deliberate and is not belt-and-braces about one process:
//
//   - THE SANITIZER RUNS IN A DIFFERENT PROCESS, reached over a ServerURL the
//     operator sets from a config file or RELAY_URL. `relay` decodes whatever
//     that endpoint sends. "The server strips control characters" is a claim
//     about a peer, and a client cannot verify a peer's claim by trusting it.
//   - THE PROVENANCE PREFIX DOES NOT SURVIVE A NEWLINE. `show` prints one
//     "Label: value" per line, so a value containing \n forges further lines in
//     relay's own output - the prefix names the provenance of the FIRST line and
//     says nothing about the ones the value invented. Display-layer
//     impersonation is the one real risk this field carries, and a label that a
//     newline can escape is not a mitigation.
//
// The poisoned value is asserted on THREE axes because they fail independently:
// the escape bytes are gone, the forged chrome line is not a line, and the
// legible content survived (a sanitizer that returned "" would pass the first
// two).
func TestSchedulesShow_TheFailureTextCannotForgeLinesOrEmitEscapes(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{
			"id":"abc","name":"nightly","cron_expr":"@hourly","timezone":"UTC",
			"overlap_policy":"skip","enabled":true,
			"next_run_at":"2099-01-01T00:00:00Z",
			"last_error":"task \u001b[31mt\u001b[0m: bad\nEnabled:  false\rand\u0007done"
		}`)
	}))
	defer srv.Close()
	cfg := &Config{ServerURL: srv.URL, Token: "tkn"}

	var buf bytes.Buffer
	require.NoError(t, doSchedules(context.Background(), cfg, []string{"show", "abc"}, &buf))
	out := buf.String()

	require.NotContains(t, out, "\x1b",
		"an ESC reaching the terminal is ANSI injection: it can repaint or hide relay's own output")
	require.False(t, strings.ContainsAny(out[:len(out)-1], "\x00\x07\x0d"),
		"no control character from the server's value may reach the terminal")

	// THE FORGED CHROME LINE. The value contains "Enabled:  false", which is
	// byte-identical to what doSchedulesShow prints for a disabled schedule. It
	// must not be able to BE a line: the real Enabled line above says true.
	for _, line := range strings.Split(out, "\n") {
		require.NotEqual(t, "Enabled:  false", line,
			"the failure text must not be able to start a line, or it can impersonate relay's own fields")
	}
	require.Contains(t, out, "Enabled:  true", "CONTROL: the real Enabled line is untouched")

	// AND THE CONTENT SURVIVES. A sanitizer that dropped the value entirely would
	// satisfy every assertion above and destroy the signal this slice exists for.
	require.Contains(t, out, "task ")
	require.Contains(t, out, "bad")
	require.Contains(t, out, "done")
}

// A SEVENTH COLUMN, NOT A MARKER APPENDED TO THE NEXT CELL. The spec left the
// choice to the planner; a separate column is taken because NEXT is a timestamp
// that internal/cli/schedules_integration_test.go matches with a regex, and
// appending prose to it would make one cell mean two things. tabwriter has no
// layout budget to blow, unlike the SPA's nine-column grid, so the argument that
// forced a chip there does not apply here.
//
// It must be TEXT and it must be visible WITHOUT --json: the whole point is that
// an operator scanning the list sees WHICH schedule to suspect. run-now already
// explains one you have already picked out.
func TestSchedulesList_StateColumnDistinguishesAFailingSchedule(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// HAND-WRITTEN, not marshalled through relayclient.PageEnvelope[scheduleResp].
		_, _ = io.WriteString(w, `{"items":[
			{"id":"s1","name":"broken","cron_expr":"@hourly","timezone":"UTC","enabled":true,
			 "next_run_at":"2099-01-01T00:00:00Z",
			 "last_error":"task t: retries must be between 0 and 10",
			 "last_error_at":"2026-08-28T12:00:00Z"},
			{"id":"s2","name":"fine","cron_expr":"@hourly","timezone":"UTC","enabled":true,
			 "next_run_at":"2099-01-01T00:00:00Z"}
		],"next_cursor":"","total":2}`)
	}))
	defer srv.Close()
	cfg := &Config{ServerURL: srv.URL, Token: "tkn"}

	var buf bytes.Buffer
	require.NoError(t, doSchedules(context.Background(), cfg, []string{"list"}, &buf))
	out := buf.String()

	require.Contains(t, out, "STATE", "the header must name the new column")
	require.Contains(t, out, "FAILING")

	// A COLUMN, NOT A SUFFIX ON NEXT. Asserted on the header rather than on a row
	// because the row's own cells cannot be split reliably (NEXT renders as
	// "2099-01-01 00:00", which contains a space). If STATE were appended to the
	// NEXT cell the header would still read "... NEXT" and this goes red.
	var header string
	for _, l := range strings.Split(out, "\n") {
		if strings.Contains(l, "CRON") {
			header = l
		}
	}
	require.NotEmpty(t, header)
	fields := strings.Fields(header)
	require.Equal(t, []string{"ID", "NAME", "CRON", "TZ", "ENABLED", "NEXT", "STATE"}, fields,
		"STATE is its own trailing column; NEXT must keep meaning exactly one thing")

	// PER-ROW, NOT PER-TABLE. Without this the two assertions above would pass on
	// an implementation that marked every row.
	var brokenLine, fineLine string
	for _, l := range strings.Split(out, "\n") {
		if strings.Contains(l, "broken") {
			brokenLine = l
		}
		if strings.Contains(l, "fine") {
			fineLine = l
		}
	}
	require.NotEmpty(t, brokenLine)
	require.NotEmpty(t, fineLine)
	require.Contains(t, brokenLine, "FAILING")
	require.NotContains(t, fineLine, "FAILING",
		"a healthy row must not be marked, or the marker tells an operator nothing")
	require.Contains(t, fineLine, "OK")
}

// THE EMPTY-STRING ROW, on the DISCOVERY surface. list and show must partition
// absent/empty/present identically or an operator gets a FAILING row whose show
// output says nothing is wrong. Both read scheduleResp.hasFailure for exactly
// this reason; this test is what makes that shared read load-bearing.
func TestSchedulesList_AnEmptyLastErrorStringIsNotAFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"items":[
			{"id":"s3","name":"blank","cron_expr":"@hourly","timezone":"UTC","enabled":true,
			 "next_run_at":"2099-01-01T00:00:00Z","last_error":"","last_error_at":"2026-08-28T12:00:00Z"}
		],"next_cursor":"","total":1}`)
	}))
	defer srv.Close()
	cfg := &Config{ServerURL: srv.URL, Token: "tkn"}

	var buf bytes.Buffer
	require.NoError(t, doSchedules(context.Background(), cfg, []string{"list"}, &buf))
	require.NotContains(t, buf.String(), "FAILING")
	require.Contains(t, buf.String(), "OK")
}

// It mirrors doSchedulesCreate's read-parse-send exactly: read the file,
// json.Unmarshal into map[string]any to confirm it PARSES, put the object on the
// body. THE SERVER REMAINS THE VALIDATOR OF RECORD - the CLI checks syntax,
// never semantics - so the bound the operator tripped is reported by the one
// place that owns it, and its 400 renders verbatim.
func TestSchedulesUpdate_SpecFlagSendsTheJobSpec(t *testing.T) {
	var receivedBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "PATCH", r.Method)
		require.Equal(t, "/v1/scheduled-jobs/abc", r.URL.Path)
		require.NoError(t, json.NewDecoder(r.Body).Decode(&receivedBody))
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"abc","name":"nightly","cron_expr":"@hourly","timezone":"UTC","enabled":true,"next_run_at":"2099-01-01T00:00:00Z"}`)
	}))
	defer srv.Close()
	cfg := &Config{ServerURL: srv.URL, Token: "tkn"}

	dir := t.TempDir()
	specPath := filepath.Join(dir, "spec.json")
	require.NoError(t, os.WriteFile(specPath,
		[]byte(`{"name":"repaired","tasks":[{"name":"t","command":["echo","hi"],"retries":3}]}`), 0600))

	var buf bytes.Buffer
	require.NoError(t, doSchedules(context.Background(), cfg,
		[]string{"update", "abc", "--spec", specPath}, &buf))

	spec, ok := receivedBody["job_spec"].(map[string]any)
	require.True(t, ok, "the PATCH body must carry job_spec as an object, got %#v", receivedBody["job_spec"])
	require.Equal(t, "repaired", spec["name"])

	// NOTHING ELSE MAY RIDE ALONG. Sending cron_expr or timezone the user did not
	// supply recomputes next_run_at server-side, pushing the next fire out by up
	// to a full period on a schedule the operator is trying to REPAIR, not
	// reschedule. patchScheduledJobRequest is all pointers, so an omitted key
	// means leave alone - which is only true if the CLI actually omits it.
	require.NotContains(t, receivedBody, "cron_expr")
	require.NotContains(t, receivedBody, "timezone")
	require.NotContains(t, receivedBody, "enabled")
	require.NotContains(t, receivedBody, "overlap_policy")
}

// SYNTAX ONLY, AND THE REFUSAL COMES BEFORE ANY REQUEST. That is why this test
// belongs in the default lane by definition: no server is involved in the
// outcome.
func TestSchedulesUpdate_SpecFlagRefusesUnparseableJSONWithoutCallingTheServer(t *testing.T) {
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	}))
	defer srv.Close()
	cfg := &Config{ServerURL: srv.URL, Token: "tkn"}

	dir := t.TempDir()
	specPath := filepath.Join(dir, "spec.json")
	require.NoError(t, os.WriteFile(specPath, []byte(`{"name": `), 0600))

	err := doSchedules(context.Background(), cfg,
		[]string{"update", "abc", "--spec", specPath}, io.Discard)
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid spec JSON")
	require.False(t, called, "an unparseable spec must be refused before any request is made")
}

func TestSchedulesUpdate_SpecFlagReportsAMissingFile(t *testing.T) {
	cfg := &Config{ServerURL: "http://127.0.0.1:1", Token: "tkn"}
	err := doSchedules(context.Background(), cfg,
		[]string{"update", "abc", "--spec", filepath.Join(t.TempDir(), "nope.json")}, io.Discard)
	require.Error(t, err)
	require.Contains(t, err.Error(), "read spec file")
}

// --spec COMBINES with the existing flags rather than replacing them, so an
// operator can repair the spec and re-enable in one call.
func TestSchedulesUpdate_SpecFlagCombinesWithEnable(t *testing.T) {
	var receivedBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, json.NewDecoder(r.Body).Decode(&receivedBody))
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"abc","name":"nightly","cron_expr":"@hourly","timezone":"UTC","enabled":true,"next_run_at":"2099-01-01T00:00:00Z"}`)
	}))
	defer srv.Close()
	cfg := &Config{ServerURL: srv.URL, Token: "tkn"}

	dir := t.TempDir()
	specPath := filepath.Join(dir, "spec.json")
	require.NoError(t, os.WriteFile(specPath,
		[]byte(`{"name":"repaired","tasks":[{"name":"t","command":["echo","hi"]}]}`), 0600))

	require.NoError(t, doSchedules(context.Background(), cfg,
		[]string{"update", "abc", "--spec", specPath, "--enable"}, io.Discard))

	require.Contains(t, receivedBody, "job_spec")
	require.Equal(t, true, receivedBody["enabled"])
}

// THE ADVERTISEMENT, PINNED. README names `relay schedules update <id> --spec
// FILE` in two places and `relay schedules show` prints a remedy that assumes
// this command can repair a spec. "The remedy is named" and "the remedy exists"
// are different properties, and the second is the one an operator finds out
// about at the worst moment. This asserts the spelling the docs promise, in the
// default lane, so a rename of the flag reddens the same commit that breaks the
// prose rather than being discovered by a human reading it.
func TestSchedulesUpdate_UsageNamesTheSpecFlag(t *testing.T) {
	cfg := &Config{ServerURL: "http://127.0.0.1:1", Token: "tkn"}
	err := doSchedules(context.Background(), cfg, []string{"update"}, io.Discard)
	require.Error(t, err)
	require.Contains(t, err.Error(), "--spec FILE",
		"README tells an operator to run `relay schedules update <id> --spec FILE`; the usage "+
			"string is the CLI's own statement of that spelling")
}

// THE STATE COLUMN IS A TRUST SIGNAL AND EVERY OTHER CELL ON ITS ROW IS
// ATTACKER-CHOSEN. Schedule names are unvalidated - handleCreateScheduledJob
// rejects only "" and the PATCH handler does not even do that - so the name a
// schedule's owner picked is rendered into the same line as the STATE cell that
// says whether relay can still use that schedule.
//
// The forge is not subtle: a newline in the name ends the row early, so the real
// FAILING cell is pushed onto a junk continuation line and the attacker supplies
// their own line carrying their own ID and "OK". An operator scanning `relay
// schedules list` for what to suspect reads the forged line and moves on. The
// attacker can tune the padding exactly by running the command against their own
// account first.
//
// ASSERTED STRUCTURALLY, on the LINE COUNT, because that is the property the
// forge violates and no assertion about the CONTENT of a line can cover it: the
// attacker chooses the content. Two schedules render two rows, plus Total and
// the header, and nothing a schedule's own fields contain may add a fifth line.
//
// The content assertions after it are load-bearing in the other direction: a
// sanitizer that returned "" would satisfy the line count and destroy the
// signal.
func TestSchedulesList_AScheduleSOwnFieldsCannotForgeARow(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// HAND-WRITTEN, never marshalled through the CLI's own scheduleResp.
		// s1 is really FAILING and its NAME carries the forged row.
		_, _ = io.WriteString(w, `{"items":[
			{"id":"s1","name":"evil\ns1        pwned     @hourly  UTC  true   2099-01-01 00:00  OK","cron_expr":"@hourly","timezone":"UTC","enabled":true,
			 "next_run_at":"2099-01-01T00:00:00Z",
			 "last_error":"task t: retries must be between 0 and 10"},
			{"id":"s2","name":"fine","cron_expr":"@ho\u202eurly","timezone":"\u001b[2Kbogus","enabled":true,
			 "next_run_at":"2099-01-01T00:00:00Z"}
		],"next_cursor":"","total":2}`)
	}))
	defer srv.Close()
	cfg := &Config{ServerURL: srv.URL, Token: "tkn"}

	var buf bytes.Buffer
	require.NoError(t, doSchedules(context.Background(), cfg, []string{"list"}, &buf))
	out := buf.String()

	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	require.Len(t, lines, 4,
		"Total + header + one line per schedule; a schedule's own fields must not be able to add a line:\n%s", out)

	require.NotContains(t, out, "\x1b",
		"an ESC in any rendered cell is ANSI injection, not only in last_error")
	require.NotContains(t, out, "\u202e",
		"a bidi override in any cell reorders every glyph after it on that line, the STATE cell included")

	// THE REAL STATE STAYS ON THE REAL ROW. Without the line-count assertion above
	// this would still pass on the forged output, because the junk continuation
	// line also contains "s1".
	require.Contains(t, lines[2], "FAILING")
	require.Contains(t, lines[3], "OK")

	// CONTROL: the legible part of every poisoned cell survives.
	require.Contains(t, out, "evil")
	require.Contains(t, out, "pwned", "the sanitizer neuters the newline, it does not censor the name")
	require.Contains(t, out, "bogus")
}

// SHOW HAS THE SAME GAP AND IT WAS NOT CLOSED. doSchedulesShow prints one
// "Label: value" per line and passes terminalSafeLine over exactly one of those
// values. Name, Cron and Timezone come from the same untrusted row and are
// printed raw, so any of them can forge a "Last error ..." line - the very line
// whose provenance prefix this slice added.
func TestSchedulesShow_TheOtherFieldsCannotForgeLinesEither(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{
			"id":"abc","name":"nightly\nEnabled:  false","cron_expr":"@hourly",
			"timezone":"UTC\u001b[31m","overlap_policy":"skip","enabled":true,
			"next_run_at":"2099-01-01T00:00:00Z"
		}`)
	}))
	defer srv.Close()
	cfg := &Config{ServerURL: srv.URL, Token: "tkn"}

	var buf bytes.Buffer
	require.NoError(t, doSchedules(context.Background(), cfg, []string{"show", "abc"}, &buf))
	out := buf.String()

	require.NotContains(t, out, "\x1b")
	for _, line := range strings.Split(out, "\n") {
		require.NotEqual(t, "Enabled:  false", line,
			"a schedule's name must not be able to impersonate one of relay's own field lines")
	}
	require.Contains(t, out, "Enabled:  true", "CONTROL: the real Enabled line is untouched")
	require.Contains(t, out, "nightly", "CONTROL: the legible name survives")
}

// C0 AND DEL WERE NOT THE WHOLE CLASS. The original predicate was
// `r < 0x20 || r == 0x7f`, which lets two further families through:
//
//   - C1 CONTROLS (U+0080-U+009F). U+009B IS the single-character CSI: a
//     terminal decoding the stream as Latin-1, and some that accept the UTF-8
//     spelling directly, start an escape sequence on it with no ESC in sight.
//     Asserting only "no \x1b" is therefore not asserting "no escape sequence".
//   - BIDI OVERRIDES (U+200E/U+200F, U+202A-U+202E, U+2066-U+2069). These reorder
//     the glyphs AFTER them without changing any byte the eye can find, so
//     "FAILING" can be made to render as "GNILIAF" - or, more usefully to an
//     attacker, a name can swallow the provenance prefix that precedes it into a
//     right-to-left run.
//
// TAB IS DELIBERATELY IN THIS TEST AND IS ALREADY GREEN. It matters for a
// different reason - a raw tab in a cell shifts tabwriter's columns - and the
// existing `r < 0x20` predicate already covers it at 0x09. Pinning it here says
// the column-shift property is covered by the C0 arm on purpose rather than by
// accident, so nobody narrows that arm later.
func TestTerminalSafeLine_CoversC1AndBidiControlsNotJustC0(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   string
		want string
	}{
		{"C1 CSI U+009B", "a\u009b31mb", "a 31mb"},
		{"C1 range low U+0080", "a\u0080b", "a b"},
		{"C1 range high U+009F", "a\u009fb", "a b"},
		{"DEL U+007F", "a\u007fb", "a b"},
		{"RLO U+202E", "a\u202eb", "a b"},
		{"LRE U+202A", "a\u202ab", "a b"},
		{"PDF U+202C", "a\u202cb", "a b"},
		{"RLM U+200F", "a\u200fb", "a b"},
		{"LRM U+200E", "a\u200eb", "a b"},
		{"LRI U+2066", "a\u2066b", "a b"},
		{"PDI U+2069", "a\u2069b", "a b"},
		{"tab U+0009 (already covered by the C0 arm)", "a\tb", "a b"},
		{"newline U+000A (already covered by the C0 arm)", "a\nb", "a b"},
	} {
		require.Equal(t, tc.want, terminalSafeLine(tc.in), tc.name)
	}

	// CONTROL: PRINTABLE NON-ASCII IS NOT A CONTROL CHARACTER. A predicate that
	// mapped everything above U+007F would satisfy every case above and destroy
	// the field for every operator who does not write in English.
	require.Equal(t, "café 日本語 ✓ \u00a0", terminalSafeLine("café 日本語 ✓ \u00a0"),
		"printable non-ASCII, U+00A0 included, must survive verbatim")
}
// RUN-NOW IS THE REMEDY THIS SLICE ADVERTISES, AND IT WAS THE UNSANITIZED ROUTE.
// `relay schedules show` prints the stored failure through terminalSafeLine and
// then prints "Re-check with: relay schedules run-now <id>" underneath it. That
// second command answers a stored-spec validation failure with the raw
// jobspec.Validate message, task name and all - so before this fix the same
// attacker-chosen prose had two paths to an admin's terminal, one sanitized and
// one not, and the PR pointed the operator at the unsanitized one as
// authoritative.
//
// THE ASSERTION IS ON THE ERROR doSchedules RETURNS, which is what
// cli.Dispatch prints as "error: <msg>". A newline in it forges a line that
// carries no prefix at all.
//
// The sanitizer itself lives in internal/relayclient, at the point where
// json.Decode turns the wire's escapes back into bytes; this test is the wiring
// guard that says the CLI actually gets the sanitized value, which no test in
// that package can establish.
func TestSchedulesRunNow_ServerErrorReachesTheTerminalSanitized(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/v1/scheduled-jobs/abc/run-now", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"error":"invalid job_spec: task \u001b[31mevil\u001b[0m: retries must be between 0 and 10\nSchedule abc is healthy.\u009b2K"}`)
	}))
	defer srv.Close()
	cfg := &Config{ServerURL: srv.URL, Token: "tkn"}

	err := doSchedules(context.Background(), cfg, []string{"run-now", "abc"}, io.Discard)
	require.Error(t, err)
	msg := err.Error()

	require.NotContains(t, msg, "\x1b", "an ESC in a printed error is ANSI injection")
	require.NotContains(t, msg, "\u009b", "U+009B is the single-character CSI")
	require.NotContains(t, msg, "\n",
		"a newline forges a line under no \"error: \" prefix at all")

	// CONTROL: the message an operator needs is intact and untruncated - run-now
	// is documented as returning the full message, unlike the stored 1 KB value.
	require.Contains(t, msg, "invalid job_spec: task ")
	require.Contains(t, msg, "retries must be between 0 and 10")
	require.Contains(t, msg, "Schedule abc is healthy.",
		"the forged sentence is neutered by losing its line, not by being censored")
}
