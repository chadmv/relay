package perforce

import (
	"strings"
	"sync"
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
// " - " (My File - Copy.ma) and a split there truncates the path. Control bytes
// are stripped before the clip, so a path padded with them cannot smuggle
// content past the bound. TestSyncLineDepotPath.
func syncLineDepotPath(line string) string {
	if !strings.HasPrefix(line, "//") {
		return ""
	}
	i := strings.IndexByte(line, '#')
	if i < 0 {
		return ""
	}
	var b strings.Builder
	for j := 0; j < i; j++ {
		if line[j] >= 0x20 {
			b.WriteByte(line[j])
		}
	}
	out := b.String()
	if len(out) > syncLineDepotPathMax {
		out = out[:syncLineDepotPathMax]
	}
	return out
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
