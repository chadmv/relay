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
