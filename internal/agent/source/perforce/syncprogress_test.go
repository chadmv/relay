package perforce

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Each row names the rule it discriminates against, because the property being
// pinned is "the depot path ends at the FIRST # of a line beginning //" and
// several wrong rules agree with it on the happy path alone.
func TestSyncLineDepotPath(t *testing.T) {
	rows := []struct{ name, in, want string }{
		{"added", "//depot/x/a.ma#3 - added as /ws/a.ma", "//depot/x/a.ma"},
		// A filename may legitimately contain " - ", so a split on it truncates
		// the path to //depot/x/My File.
		{"dash_in_filename", `//depot/x/My File - Copy.ma#1 - updating C:\ws\My File - Copy.ma`, "//depot/x/My File - Copy.ma"},
		// p4 has many action verbs; an allow-list of the two common ones drops
		// this line entirely.
		{"deleted", "//depot/x/b.ma#2 - deleted as /ws/b.ma", "//depot/x/b.ma"},
		// The local half of the line is platform-shaped; the depot half is not.
		{"windows_local_path", `//depot/x/c.ma#5 - refreshing C:\ws\c.ma`, "//depot/x/c.ma"},
		{"not_a_file_line", "File(s) up-to-date.", ""},
		{"empty", "", ""},
		// A rule that falls back to " - " when there is no # invents a path out
		// of a line p4 did not format as one.
		{"no_rev_separator", "//depot/x/no-rev.ma - added as /ws/no-rev.ma", ""},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			assert.Equal(t, row.want, syncLineDepotPath(row.in))
		})
	}

	// The remaining two rows assert a property of the result rather than an
	// exact string: this text is p4-derived and reaches task_logs and then the
	// SPA, so a control byte could forge a second line and an unbounded path
	// could crowd out the fixed fields ahead of it.
	t.Run("control_bytes", func(t *testing.T) {
		got := syncLineDepotPath("//depot/x/a\rb.ma#3 - added as /ws/a.ma")
		assert.NotContains(t, got, "\r")
		for i := 0; i < len(got); i++ {
			require.GreaterOrEqual(t, got[i], byte(0x20),
				"byte %d of %q is a control byte", i, got)
		}
	})

	t.Run("clip_at_200", func(t *testing.T) {
		got := syncLineDepotPath("//depot/" + strings.Repeat("z", 400) + "#1 - added as /ws/z")
		assert.Equal(t, 200, len(got))
	})
}

// The third return value is the discriminating one: a non-file line must leave
// lastPath alone rather than clearing it, or the summary's trailing field goes
// blank every time p4 writes a totals or diagnostic line between two files.
func TestSyncProgress_CountsFilesAndOtherLines(t *testing.T) {
	sp := &syncProgress{}
	sp.onLine("//depot/x/a.ma#3 - added as /ws/a.ma")
	sp.onLine("File(s) up-to-date.")

	files, other, lastPath := sp.snapshot()
	assert.Equal(t, 1, files)
	assert.Equal(t, 1, other)
	assert.Equal(t, "//depot/x/a.ma", lastPath)
}
