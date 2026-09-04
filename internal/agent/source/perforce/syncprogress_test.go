package perforce

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
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

	// The remaining rows assert properties of the result rather than an exact
	// string. This text is p4-derived and reaches task_logs.content, which is a
	// Postgres TEXT column, over a proto field of type bytes - so nothing between
	// the depot and the INSERT rejects invalid UTF-8, and the whole batched chunk
	// is dropped when Postgres does. lastPath is sticky, so one poisoned path
	// silences the task log for the rest of the sync.
	t.Run("removed_runes", func(t *testing.T) {
		rows := []struct{ name, in string }{
			{"cr", "\r"},
			{"nul_is_sqlstate_22021", "\x00"},
			{"del", "\u007f"},
			{"c1_next_line", "\u0085"},
			{"line_separator", "\u2028"},
			{"paragraph_separator", "\u2029"},
			{"bidi_override", "\u202e"},
			{"zero_width_joiner_is_a_format_rune", "\u200d"},
		}
		for _, row := range rows {
			t.Run(row.name, func(t *testing.T) {
				got := syncLineDepotPath("//depot/x/a" + row.in + "b.ma#3 - added as /ws/a.ma")
				assert.Equal(t, "//depot/x/ab.ma", got)
			})
		}
	})

	// A non-unicode-mode p4 server hands back raw high bytes. They arrive WHOLE
	// rather than being manufactured by the clip, so a rune-boundary walk-back
	// alone does not see them.
	t.Run("invalid_utf8_arriving_whole", func(t *testing.T) {
		got := syncLineDepotPath("//depot/" + string([]byte{0x80, 0xff}) + "q.ma#1 - added as /ws/q")
		assert.True(t, utf8.ValidString(got), "got % x", got)
		assert.Equal(t, "//depot/q.ma", got)
	})

	// Pure ASCII, so every rune is one byte and the bound is reachable exactly.
	// The row beneath this one can only bracket the result to within a rune, and
	// on its own leaves the constant free to move by one. 200 is written as a
	// literal on purpose: spelling it syncLineDepotPathMax would move the
	// expectation with the thing under test and pin nothing.
	t.Run("clip_at_200_exactly_when_every_rune_is_one_byte", func(t *testing.T) {
		got := syncLineDepotPath("//depot/" + strings.Repeat("z", 400) + "#1 - added as /ws/z")
		assert.Equal(t, 200, len(got))
	})

	// The bound is a BYTE bound over text that need not be ASCII. The input puts
	// a two-byte rune astride byte 200, which is the position a raw slice halves.
	t.Run("clip_at_200", func(t *testing.T) {
		got := syncLineDepotPath("//depot/" + strings.Repeat("z", 191) + "\u00e9" + strings.Repeat("z", 400) + "#1 - added as /ws/z")
		assert.True(t, utf8.ValidString(got), "the clip must land on a rune boundary, got % x", got)
		assert.LessOrEqual(t, len(got), 200)
		// A clip that lands short by more than one rune is a different rule; a
		// clip that returns "" would satisfy the two assertions above alone.
		assert.GreaterOrEqual(t, len(got), 198)
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
