package agent

import (
	"context"
	"os"
	"runtime"
	"strings"
	"testing"

	"relay/internal/agent/source"
	relayv1 "relay/internal/proto/relayv1"

	"github.com/stretchr/testify/require"
)

// identityVars is the exact set the coordinator injects. The helper reports the
// child's own environment block, so ABSENT and PRESENT-BUT-EMPTY stay
// distinguishable - the distinction the absent-or-non-empty rule turns on, and
// one that `echo $VAR` cannot make on either platform. It reports a
// case-INSENSITIVE match under whatever name the child actually carries, so a
// spec key that differs only in case is visible to an assertion; os/exec's
// dedup rule folds case on Windows only, and the two platforms must be told
// apart here rather than assumed.
var identityVars = []string{"RELAY_TASK_ID", "RELAY_JOB_ID", "RELAY_JOB_URL", "RELAY_TASK_URL"}

const identityLinePrefix = "relayenv "

// TestRunnerEnvHelperProcess IS NOT A TEST. It is the subprocess the tests in
// this file exec, in the shape of TestCRLFHelperProcess: the test binary
// re-execs itself so that what is observed is the CHILD's environment, never
// cmd.Env. os/exec deduplicates at Start time, so cmd.Env legitimately holds
// both a spec entry and the coordinator's and an assertion on it proves nothing.
//
// os.Exit(0) IS NOT OPTIONAL: without it the testing framework appends "PASS" to
// the very stdout the parent parses.
func TestRunnerEnvHelperProcess(t *testing.T) {
	if os.Getenv("RELAY_ENV_HELPER") == "" {
		return // an ordinary test run; this process is not the helper
	}
	for _, e := range os.Environ() {
		k, v, _ := strings.Cut(e, "=")
		for _, want := range identityVars {
			if strings.EqualFold(k, want) {
				_, _ = os.Stdout.WriteString(identityLinePrefix + k + "=" + v + "\n")
				break
			}
		}
	}
	os.Exit(0)
}

// identityHelperCmd returns the argv and the env that re-exec this test binary
// as the helper above. os.Args[0] under `go test` is the built test binary, an
// absolute path. The sentinel travels through DispatchTask.Env, so the parent's
// own environment is never mutated by it, and it is not one of identityVars so
// it can never be mistaken for the subject.
func identityHelperCmd() ([]string, map[string]string) {
	return []string{os.Args[0], "-test.run=^TestRunnerEnvHelperProcess$"},
		map[string]string{"RELAY_ENV_HELPER": "1"}
}

// runIdentityHelper dispatches task through a real Runner and returns the
// child's view: present names mapped to their values, absent names missing from
// the map entirely. ambient entries are exported into the AGENT's own process
// environment first, which is how the inheritance test drives its input.
func runIdentityHelper(
	t *testing.T,
	task *relayv1.DispatchTask,
	provider source.Provider,
	ambient map[string]string,
) map[string]string {
	t.Helper()
	// Hermetic: whatever the developer's shell exports must not decide the
	// result. t.Setenv registers the restore whether or not the name was
	// originally set, so the Unsetenv is safe to pair with it.
	for _, k := range identityVars {
		t.Setenv(k, "")
		os.Unsetenv(k)
	}
	for k, v := range ambient {
		t.Setenv(k, v)
	}

	sendCh := make(chan *relayv1.AgentMessage, 256)
	r, runCtx := newRunner(task.TaskId, task.Epoch, sendCh, context.Background(), 0)
	if provider != nil {
		r.SetProviderForTest(provider)
	}
	r.Run(runCtx, task) // blocks until the subprocess has exited

	// Run has returned, so every message is already in the buffer; a
	// non-blocking drain is deterministic where a timeout would race the spawn.
	var out strings.Builder
drain:
	for {
		select {
		case m := <-sendCh:
			if l := m.GetTaskLog(); l != nil && l.Stream == relayv1.LogStream_LOG_STREAM_STDOUT {
				out.Write(l.Content)
			}
		default:
			break drain
		}
	}

	got := map[string]string{}
	for _, line := range strings.Split(out.String(), "\n") {
		if !strings.HasPrefix(line, identityLinePrefix) {
			continue
		}
		k, v, ok := strings.Cut(strings.TrimPrefix(line, identityLinePrefix), "=")
		require.True(t, ok, "malformed helper line %q", line)
		got[k] = v
	}
	return got
}

// spoofingHandle is a source.Handle whose workspace environment tries to set the
// coordinator's own names. runner_test.go's fakeHandle returns a fixed one-key
// map with no seam behind it, and other tests depend on that.
type spoofingHandle struct{}

func (spoofingHandle) WorkingDir() string { return "" }
func (spoofingHandle) Env() map[string]string {
	return map[string]string{
		"RELAY_JOB_URL": "https://evil.example/",
		"RELAY_TASK_ID": "SPOOFED-BY-WORKSPACE",
	}
}
func (spoofingHandle) Finalize(ctx context.Context) error { return nil }
func (spoofingHandle) Inventory() source.InventoryEntry {
	return source.InventoryEntry{SourceType: "perforce", SourceKey: "//s/x"}
}

func TestRunner_InjectsTheDispatchedIdentityIntoTheChildEnvironment(t *testing.T) {
	argv, env := identityHelperCmd()
	got := runIdentityHelper(t, &relayv1.DispatchTask{
		TaskId:   "task-abc",
		JobId:    "job-xyz",
		JobUrl:   "https://relay.example.com/jobs/job-xyz",
		TaskUrl:  "https://relay.example.com/jobs/job-xyz/tasks/task-abc",
		Commands: []*relayv1.CommandLine{{Argv: argv}},
		Env:      env,
	}, nil, nil)

	// The whole map, not four Contains: the two URLs differ from each other, so
	// this also refuses a transposed JobUrl/TaskUrl pair - two same-typed
	// adjacent arguments that no compiler can tell apart.
	require.Equal(t, map[string]string{
		"RELAY_TASK_ID":  "task-abc",
		"RELAY_JOB_ID":   "job-xyz",
		"RELAY_JOB_URL":  "https://relay.example.com/jobs/job-xyz",
		"RELAY_TASK_URL": "https://relay.example.com/jobs/job-xyz/tasks/task-abc",
	}, got)
}

// TestRunner_CoordinatorIdentityBeatsSpecEnv is the headline security test. A
// job spec is authored by any authenticated user; a downstream notifier reading
// RELAY_JOB_URL posts a link other humans click.
func TestRunner_CoordinatorIdentityBeatsSpecEnv(t *testing.T) {
	argv, env := identityHelperCmd()
	for _, k := range identityVars {
		env[k] = "https://evil.example/SPOOFED"
	}
	got := runIdentityHelper(t, &relayv1.DispatchTask{
		TaskId:   "task-abc",
		JobId:    "job-xyz",
		JobUrl:   "https://relay.example.com/jobs/job-xyz",
		TaskUrl:  "https://relay.example.com/jobs/job-xyz/tasks/task-abc",
		Commands: []*relayv1.CommandLine{{Argv: argv}},
		Env:      env,
	}, nil, nil)

	require.Equal(t, map[string]string{
		"RELAY_TASK_ID":  "task-abc",
		"RELAY_JOB_ID":   "job-xyz",
		"RELAY_JOB_URL":  "https://relay.example.com/jobs/job-xyz",
		"RELAY_TASK_URL": "https://relay.example.com/jobs/job-xyz/tasks/task-abc",
	}, got, "a job spec's env must not decide what a notifier posts as a link")
}

// TestRunner_CoordinatorIdentityBeatsWorkspaceEnv is the second precedence leg
// and it is the one that discriminates. Without it, moving the identity block
// to sit BETWEEN the two merge loops passes everything else in this file.
func TestRunner_CoordinatorIdentityBeatsWorkspaceEnv(t *testing.T) {
	argv, env := identityHelperCmd()
	got := runIdentityHelper(t, &relayv1.DispatchTask{
		TaskId:   "task-abc",
		JobId:    "job-xyz",
		JobUrl:   "https://relay.example.com/jobs/job-xyz",
		TaskUrl:  "https://relay.example.com/jobs/job-xyz/tasks/task-abc",
		Commands: []*relayv1.CommandLine{{Argv: argv}},
		Env:      env,
		Source: &relayv1.SourceSpec{Provider: &relayv1.SourceSpec_Perforce{
			Perforce: &relayv1.PerforceSource{Stream: "//s/x"},
		}},
	}, &fakeProvider{handle: spoofingHandle{}}, nil)

	require.Equal(t, map[string]string{
		"RELAY_TASK_ID":  "task-abc",
		"RELAY_JOB_ID":   "job-xyz",
		"RELAY_JOB_URL":  "https://relay.example.com/jobs/job-xyz",
		"RELAY_TASK_URL": "https://relay.example.com/jobs/job-xyz/tasks/task-abc",
	}, got, "the workspace provider's env is merged after the spec's and must lose too")
}

// TestRunner_UnconfiguredPublicURLLeavesTheURLNamesAbsentAndTheIdsPresent is a
// CONJUNCTION on purpose. Before the change all four names are absent, so the
// absence half alone is green against an unmodified tree and is not a criterion.
func TestRunner_UnconfiguredPublicURLLeavesTheURLNamesAbsentAndTheIdsPresent(t *testing.T) {
	argv, env := identityHelperCmd()
	got := runIdentityHelper(t, &relayv1.DispatchTask{
		TaskId:   "task-abc",
		JobId:    "job-xyz",
		Commands: []*relayv1.CommandLine{{Argv: argv}}, // JobUrl and TaskUrl deliberately unset
		Env:      env,
	}, nil, nil)

	require.Equal(t, map[string]string{
		"RELAY_TASK_ID": "task-abc",
		"RELAY_JOB_ID":  "job-xyz",
	}, got, "the URL names must be ABSENT, not present and empty, while both ids are still there")
}

// TestRunner_AnEmptyDispatchedIdProducesNoVariableAtAll pins the guard on the id
// half. relay's own dispatcher always populates both, so the discriminating
// input is a dispatch that does not - which is what a future peer on this wire,
// or a hand-built message, can produce.
func TestRunner_AnEmptyDispatchedIdProducesNoVariableAtAll(t *testing.T) {
	argv, env := identityHelperCmd()
	got := runIdentityHelper(t, &relayv1.DispatchTask{
		TaskId:   "task-abc",
		JobId:    "", // the subject
		Commands: []*relayv1.CommandLine{{Argv: argv}},
		Env:      env,
	}, nil, nil)

	require.Equal(t, map[string]string{"RELAY_TASK_ID": "task-abc"}, got,
		"relay must never set one of these names to the empty string: a consumer gets one "+
			"check, not a second one for 'set but blank'")
}

// TestRunner_UnconfiguredCoordinatorStillRefusesASpecEnvURL is the case
// append-last cannot cover: with no RELAY_PUBLIC_URL there is no relay value to
// append, so the spec's entry is the ONLY occurrence and os/exec has nothing to
// dedup it against. The guarantee has to hold on a default-configured server or
// it is not a guarantee.
func TestRunner_UnconfiguredCoordinatorStillRefusesASpecEnvURL(t *testing.T) {
	argv, env := identityHelperCmd()
	env["RELAY_JOB_URL"] = "https://evil.example/jobs/job-xyz"
	env["RELAY_TASK_URL"] = "https://evil.example/jobs/job-xyz/tasks/task-abc"
	got := runIdentityHelper(t, &relayv1.DispatchTask{
		TaskId:   "task-abc",
		JobId:    "job-xyz",
		Commands: []*relayv1.CommandLine{{Argv: argv}}, // JobUrl and TaskUrl deliberately unset
		Env:      env,
	}, nil, nil)

	require.Equal(t, map[string]string{
		"RELAY_TASK_ID": "task-abc",
		"RELAY_JOB_ID":  "job-xyz",
	}, got, "a job spec must not decide the link a notifier posts on a server with no public URL")
}

// TestRunner_ASpecEnvEmptyValueCannotCreateASetButBlankName is the same hole
// aimed at the absent-or-non-empty rule rather than at the value: an empty spec
// entry needs no configured coordinator to beat, because relay's own guard
// already declines to append an empty value.
func TestRunner_ASpecEnvEmptyValueCannotCreateASetButBlankName(t *testing.T) {
	argv, env := identityHelperCmd()
	for _, k := range identityVars {
		env[k] = ""
	}
	got := runIdentityHelper(t, &relayv1.DispatchTask{
		TaskId:   "task-abc",
		JobId:    "job-xyz",
		Commands: []*relayv1.CommandLine{{Argv: argv}},
		Env:      env,
	}, nil, nil)

	require.Equal(t, map[string]string{
		"RELAY_TASK_ID": "task-abc",
		"RELAY_JOB_ID":  "job-xyz",
	}, got, "a spec entry must not turn a name relay leaves absent into a set-but-blank one")
}

// TestRunner_UnconfiguredCoordinatorStillRefusesAWorkspaceEnvURL is the same
// hole on the second merge loop. spoofingHandle sets RELAY_JOB_URL, for which
// this dispatch carries no coordinator value at all.
func TestRunner_UnconfiguredCoordinatorStillRefusesAWorkspaceEnvURL(t *testing.T) {
	argv, env := identityHelperCmd()
	got := runIdentityHelper(t, &relayv1.DispatchTask{
		TaskId:   "task-abc",
		JobId:    "job-xyz",
		Commands: []*relayv1.CommandLine{{Argv: argv}}, // JobUrl and TaskUrl deliberately unset
		Env:      env,
		Source: &relayv1.SourceSpec{Provider: &relayv1.SourceSpec_Perforce{
			Perforce: &relayv1.PerforceSource{Stream: "//s/x"},
		}},
	}, &fakeProvider{handle: spoofingHandle{}}, nil)

	require.Equal(t, map[string]string{
		"RELAY_TASK_ID": "task-abc",
		"RELAY_JOB_ID":  "job-xyz",
	}, got, "a workspace provider must not supply a name the coordinator owns")
}

// TestRunner_TheReservedNamesAreCaseFoldedExactlyWhereOsExecFoldsThem pins the
// platform asymmetry rather than asserting it in prose. The strip has to match
// os/exec's own duplicate-key rule in BOTH directions: fold on Windows, where
// relay_job_url and RELAY_JOB_URL are one variable and the spec's spelling would
// otherwise be the value the child resolves; do not fold elsewhere, where they
// are two variables and stripping the lower-case one would delete a spec entry
// relay has no claim on. An unconfigured coordinator is what discriminates - a
// configured one hides the Windows leg behind exec's own dedup.
func TestRunner_TheReservedNamesAreCaseFoldedExactlyWhereOsExecFoldsThem(t *testing.T) {
	argv, env := identityHelperCmd()
	env["relay_job_url"] = "https://evil.example/jobs/job-xyz"
	got := runIdentityHelper(t, &relayv1.DispatchTask{
		TaskId:   "task-abc",
		JobId:    "job-xyz",
		Commands: []*relayv1.CommandLine{{Argv: argv}}, // JobUrl deliberately unset
		Env:      env,
	}, nil, nil)

	want := map[string]string{
		"RELAY_TASK_ID": "task-abc",
		"RELAY_JOB_ID":  "job-xyz",
	}
	if runtime.GOOS != "windows" {
		want["relay_job_url"] = "https://evil.example/jobs/job-xyz"
	}
	require.Equal(t, want, got)
}

// TestRunner_ACoordinatorValueBeatsAnInheritedOne is what still makes the
// append's POSITION load-bearing now that the two merge loops are stripped:
// os.Environ() is inherited unfiltered, so the block must be appended after it
// or os/exec's last-duplicate-wins rule hands the child the agent operator's
// stale value instead of this dispatch's.
func TestRunner_ACoordinatorValueBeatsAnInheritedOne(t *testing.T) {
	argv, env := identityHelperCmd()
	got := runIdentityHelper(t, &relayv1.DispatchTask{
		TaskId:   "task-abc",
		JobId:    "job-xyz",
		JobUrl:   "https://relay.example.com/jobs/job-xyz",
		Commands: []*relayv1.CommandLine{{Argv: argv}},
		Env:      env,
	}, nil, map[string]string{"RELAY_JOB_URL": "https://stale.example/jobs/old"})

	require.Equal(t, map[string]string{
		"RELAY_TASK_ID": "task-abc",
		"RELAY_JOB_ID":  "job-xyz",
		"RELAY_JOB_URL": "https://relay.example.com/jobs/job-xyz",
	}, got)
}

// TestRunner_AnAgentProcessEnvValueSurvivesWhenTheCoordinatorHasNone documents a
// known limitation AS A BEHAVIOUR. The strip covers the dispatched spec env and
// the workspace provider's env; os.Environ() is inherited unfiltered. The trust
// boundary this feature defends is the job spec author, not the agent operator,
// who already chooses the binary and owns the machine. A later slice that
// decides to strip the inherited environment too must redden HERE rather than
// changing this silently.
func TestRunner_AnAgentProcessEnvValueSurvivesWhenTheCoordinatorHasNone(t *testing.T) {
	argv, env := identityHelperCmd()
	got := runIdentityHelper(t, &relayv1.DispatchTask{
		TaskId:   "task-abc",
		JobId:    "job-xyz",
		Commands: []*relayv1.CommandLine{{Argv: argv}}, // no JobUrl
		Env:      env,
	}, nil, map[string]string{"RELAY_JOB_URL": "https://inherited.example/"})

	require.Equal(t, map[string]string{
		"RELAY_TASK_ID": "task-abc",
		"RELAY_JOB_ID":  "job-xyz",
		"RELAY_JOB_URL": "https://inherited.example/",
	}, got)
}

// equalsKeyHandle is the workspace-provider half of the "=" bypass below.
type equalsKeyHandle struct{}

func (equalsKeyHandle) WorkingDir() string { return "" }
func (equalsKeyHandle) Env() map[string]string {
	return map[string]string{"RELAY_TASK_URL=https://evil.example/jobs/j/tasks/t?s": "1"}
}
func (equalsKeyHandle) Finalize(ctx context.Context) error { return nil }
func (equalsKeyHandle) Inventory() source.InventoryEntry {
	return source.InventoryEntry{SourceType: "perforce", SourceKey: "//s/x"}
}

// TestRunner_ASpecEnvKeyContainingAnEqualsCannotSupplyAReservedName pins the
// class the whole-key predicate cannot see. os/exec splits an entry at its
// FIRST "=", so "RELAY_JOB_URL=https://evil.example/x?t" as a KEY is a distinct
// string to any predicate that compares the whole key and the same variable to
// the child; the map's value becomes the query parameter, so the attacker
// controls the URL end to end with no residue. An unconfigured coordinator is
// what discriminates: with no relay value to append there is nothing for
// os/exec to dedup the forged entry against.
func TestRunner_ASpecEnvKeyContainingAnEqualsCannotSupplyAReservedName(t *testing.T) {
	argv, env := identityHelperCmd()
	env["RELAY_JOB_URL=https://evil.example/jobs/job-xyz?t"] = "1"
	got := runIdentityHelper(t, &relayv1.DispatchTask{
		TaskId:   "task-abc",
		JobId:    "job-xyz",
		Commands: []*relayv1.CommandLine{{Argv: argv}}, // JobUrl deliberately unset
		Env:      env,
	}, nil, nil)

	require.Equal(t, map[string]string{
		"RELAY_TASK_ID": "task-abc",
		"RELAY_JOB_ID":  "job-xyz",
	}, got, "a spec key carrying its own '=' must not become a name relay owns")
}

// TestRunner_AWorkspaceEnvKeyContainingAnEqualsCannotSupplyAReservedName is the
// same class on the second merge loop, which has its own copy of the guard.
func TestRunner_AWorkspaceEnvKeyContainingAnEqualsCannotSupplyAReservedName(t *testing.T) {
	argv, env := identityHelperCmd()
	got := runIdentityHelper(t, &relayv1.DispatchTask{
		TaskId:   "task-abc",
		JobId:    "job-xyz",
		Commands: []*relayv1.CommandLine{{Argv: argv}}, // TaskUrl deliberately unset
		Env:      env,
		Source: &relayv1.SourceSpec{Provider: &relayv1.SourceSpec_Perforce{
			Perforce: &relayv1.PerforceSource{Stream: "//s/x"},
		}},
	}, &fakeProvider{handle: equalsKeyHandle{}}, nil)

	require.Equal(t, map[string]string{
		"RELAY_TASK_ID": "task-abc",
		"RELAY_JOB_ID":  "job-xyz",
	}, got, "a workspace key carrying its own '=' must not become a name relay owns")
}
