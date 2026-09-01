package scheduler

import "testing"

// TestJobAndTaskURL covers the joining rule and its single gate: ANY empty
// argument yields "", so the decision "this field goes on the wire empty" lives
// in one place rather than at the call site.
func TestJobAndTaskURL(t *testing.T) {
	const jobID = "11111111-2222-3333-4444-555555555555"
	const taskID = "66666666-7777-8888-9999-aaaaaaaaaaaa"

	t.Run("job URL from a bare base", func(t *testing.T) {
		got := jobURL("https://relay.example.com", jobID)
		want := "https://relay.example.com/jobs/" + jobID
		if got != want {
			t.Fatalf("jobURL = %q, want %q", got, want)
		}
	})

	t.Run("task URL from a bare base", func(t *testing.T) {
		got := taskURL("https://relay.example.com", jobID, taskID)
		want := "https://relay.example.com/jobs/" + jobID + "/tasks/" + taskID
		if got != want {
			t.Fatalf("taskURL = %q, want %q", got, want)
		}
	})

	t.Run("a path prefix is preserved with exactly one slash", func(t *testing.T) {
		// parsePublicURL guarantees no trailing slash, so no separator logic
		// belongs here. This is the leg that reddens if somebody adds some.
		got := jobURL("https://ops.example.com/relay", jobID)
		want := "https://ops.example.com/relay/jobs/" + jobID
		if got != want {
			t.Fatalf("jobURL = %q, want %q", got, want)
		}
	})

	t.Run("an empty base yields no URL at all", func(t *testing.T) {
		if got := jobURL("", jobID); got != "" {
			t.Fatalf("jobURL with no base = %q, want %q", got, "")
		}
		if got := taskURL("", jobID, taskID); got != "" {
			t.Fatalf("taskURL with no base = %q, want %q", got, "")
		}
	})

	t.Run("an empty id yields no URL at all", func(t *testing.T) {
		// Never render https://relay.example.com/jobs/ - a link to a page that
		// does not exist is worse than no link, and the absent-or-non-empty rule
		// is what lets a consumer write exactly one check.
		if got := jobURL("https://relay.example.com", ""); got != "" {
			t.Fatalf("jobURL with no job id = %q, want %q", got, "")
		}
		if got := taskURL("https://relay.example.com", "", taskID); got != "" {
			t.Fatalf("taskURL with no job id = %q, want %q", got, "")
		}
		if got := taskURL("https://relay.example.com", jobID, ""); got != "" {
			t.Fatalf("taskURL with no task id = %q, want %q", got, "")
		}
	})
}
