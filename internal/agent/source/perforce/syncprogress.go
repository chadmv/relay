package perforce

import (
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"
)

// syncLineDepotPathMax bounds the depot path a summary line carries. The path is
// p4-derived text on its way to task_logs and then to the SPA, and it is the
// only input-derived field in the line.
const syncLineDepotPathMax = 200

// syncLineDepotPath returns the depot path a p4 sync output line names, or ""
// for a line that is not a file line.
//
// The path ends at the FIRST '#', not at the first " - ": p4 requires @ # % *
// to be escaped inside a depot path, so the first '#' in a line beginning "//"
// is always the rev separator, whereas a filename may legitimately contain
// " - " (My File - Copy.ma) and a split there truncates the path. TestSyncLineDepotPath.
//
// THE FILTER AND THE CLIP ARE BOTH RUNE-WISE, and a byte-wise version of either
// is a live defect rather than an untidiness. This text reaches
// task_logs.content, a Postgres TEXT column, through a proto field of type
// bytes, so nothing on the wire rejects invalid UTF-8 on its behalf: a NUL is
// SQLSTATE 22021, a half rune is a 22021-class encoding error too, and either
// one fails the INSERT for the WHOLE batched chunk. lastPath is sticky, so a
// single poisoned path silences the task log for the rest of the sync - which
// makes it reachable by whoever chose the filename in the depot.
//
// ToValidUTF8 therefore runs before the walk, dropping high bytes that arrived
// whole from a non-unicode-mode server; the category test drops Cc, Cf, Zl and
// Zp, which covers NUL, DEL, the C1 range, U+2028/U+2029 and the bidi overrides
// that would otherwise forge a line break or reorder the rendered path; and the
// bound is checked before each write so the result never ends mid-rune.
func syncLineDepotPath(line string) string {
	if !strings.HasPrefix(line, "//") {
		return ""
	}
	i := strings.IndexByte(line, '#')
	if i < 0 {
		return ""
	}
	var b strings.Builder
	for _, r := range strings.ToValidUTF8(line[:i], "") {
		if unicode.In(r, unicode.Cc, unicode.Cf, unicode.Zl, unicode.Zp) {
			continue
		}
		if b.Len()+utf8.RuneLen(r) > syncLineDepotPathMax {
			break
		}
		b.WriteRune(r)
	}
	return b.String()
}

// syncProgress counts what a running p4 sync writes to stdout. onLine runs on
// the sync goroutine and snapshot on Prepare's; the mutex is what makes that
// legal. It holds no line buffer: p4 prints one line per file and a large sync
// is millions of them.
type syncProgress struct {
	mu               sync.Mutex
	files            int    // lines that parsed as a depot path
	other            int    // lines that did not
	lastPath         string // sanitized and clipped; see syncLineDepotPath
	freeDiskDisabled bool
}

// onLine records one line of p4's stdout. A non-file line leaves lastPath
// alone, so the trailing summary field keeps naming the last file p4 touched.
// TestSyncProgress_CountsFilesAndOtherLines.
func (s *syncProgress) onLine(line string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	path := syncLineDepotPath(line)
	if path == "" {
		s.other++
		return
	}
	s.files++
	s.lastPath = path
}

// snapshot returns a value copy: no caller holds a pointer into the struct
// while the sync goroutine is still writing to it.
func (s *syncProgress) snapshot() (files, other int, lastPath string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.files, s.other, s.lastPath
}

// freeDiskIsDisabled and disableFreeDisk are a sticky latch: one free-disk
// error stops the renderer sampling for the rest of the sync, so a wedged
// volume is not re-probed once per heartbeat for the length of a multi-hour
// transfer. TestProvider_SyncSummaryRendersFiveFixedFields.
func (s *syncProgress) freeDiskIsDisabled() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.freeDiskDisabled
}

func (s *syncProgress) disableFreeDisk() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.freeDiskDisabled = true
}
