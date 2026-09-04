package perforce

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"relay/internal/agent/source"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// threeSyncedFiles is the counter state the renderer table asserts against:
// three depot-path lines, none of them "other", the last one c.ma.
func threeSyncedFiles() *syncProgress {
	sp := &syncProgress{}
	sp.onLine("//depot/x/a.ma#3 - added as /ws/a.ma")
	sp.onLine("//depot/x/b.ma#1 - updating /ws/b.ma")
	sp.onLine("//depot/x/c.ma#2 - refreshing /ws/c.ma")
	return sp
}

// The summary is an operator-facing contract: five fields, fixed order, never
// omitted. The full-contract row asserts exact equality rather than Contains
// because a Contains check cannot see a field reordering, and the field order
// is the operator's question order (is it moving, will it fit, where is it).
func TestProvider_SyncSummaryRendersFiveFixedFields(t *testing.T) {
	t.Run("full_contract", func(t *testing.T) {
		p := New(Config{
			Root: t.TempDir(), Hostname: "h", Client: &Client{r: newFakeP4Fixture(t)},
			FreeDiskGB: func(string) (int64, error) { return 811, nil },
		})
		assert.Equal(t,
			"[sync] 4m30s; 3 files; 0 other lines; 811 GB free; last //depot/x/c.ma",
			p.syncSummary("[sync]", threeSyncedFiles(), 270*time.Second))
	})

	// A nil FreeDiskGB is a supported production state: every in-package test
	// constructs Config without it, and so does any embedder that has no
	// platform helper to pass.
	t.Run("nil_free_disk", func(t *testing.T) {
		p := New(Config{Root: t.TempDir(), Hostname: "h", Client: &Client{r: newFakeP4Fixture(t)}})
		assert.Contains(t, p.syncSummary("[sync]", threeSyncedFiles(), time.Second), "- GB free")
	})

	// The counter is what makes the disable sticky: one error must stop the
	// sampling for the rest of the sync, or a wedged volume is re-probed once
	// per heartbeat for the length of a multi-hour transfer.
	t.Run("erroring_free_disk_is_sampled_once", func(t *testing.T) {
		calls := 0
		p := New(Config{
			Root: t.TempDir(), Hostname: "h", Client: &Client{r: newFakeP4Fixture(t)},
			FreeDiskGB: func(string) (int64, error) { calls++; return 0, fmt.Errorf("statfs: boom") },
		})
		sp := threeSyncedFiles()
		assert.Contains(t, p.syncSummary("[sync]", sp, time.Second), "- GB free")
		assert.Contains(t, p.syncSummary("[sync]", sp, 2*time.Second), "- GB free")
		assert.Equal(t, 1, calls, "the free-disk probe must be disabled after its first error")
	})

	t.Run("no_file_line_yet", func(t *testing.T) {
		p := New(Config{Root: t.TempDir(), Hostname: "h", Client: &Client{r: newFakeP4Fixture(t)}})
		assert.Contains(t, p.syncSummary("[sync]", &syncProgress{}, time.Second), "last -")
	})

	t.Run("zero_elapsed", func(t *testing.T) {
		p := New(Config{Root: t.TempDir(), Hostname: "h", Client: &Client{r: newFakeP4Fixture(t)}})
		assert.Contains(t, p.syncSummary("[sync]", &syncProgress{}, 0), "[sync] 0s;")
	})

	t.Run("complete_prefix_carries_all_five_fields", func(t *testing.T) {
		p := New(Config{
			Root: t.TempDir(), Hostname: "h", Client: &Client{r: newFakeP4Fixture(t)},
			FreeDiskGB: func(string) (int64, error) { return 811, nil },
		})
		assert.Equal(t,
			"[sync] complete: 1s; 3 files; 0 other lines; 811 GB free; last //depot/x/c.ma",
			p.syncSummary("[sync] complete:", threeSyncedFiles(), time.Second))
	})

	// The number in the log must be the number the sweeper and
	// RELAY_WORKSPACE_MIN_FREE_GB act on, so the probe reads the provider root
	// rather than a per-workspace subdirectory.
	t.Run("free_disk_reads_the_provider_root", func(t *testing.T) {
		root := t.TempDir()
		var got string
		p := New(Config{
			Root: root, Hostname: "h", Client: &Client{r: newFakeP4Fixture(t)},
			FreeDiskGB: func(path string) (int64, error) { got = path; return 1, nil },
		})
		p.syncSummary("[sync]", threeSyncedFiles(), time.Second)
		require.Equal(t, root, got)
	})
}

// progressRecorder is mutex-guarded because these tests run Prepare on its own
// goroutine and read the lines from the test goroutine while it is still
// writing. snapshot copies under the lock so no caller holds the slice header
// the writer is appending to.
type progressRecorder struct {
	mu    sync.Mutex
	lines []string
}

func (r *progressRecorder) add(s string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.lines = append(r.lines, s)
}

func (r *progressRecorder) snapshot() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.lines...)
}

func (r *progressRecorder) count(sub string) int {
	return countLinesContaining(r.snapshot(), sub)
}

// waitFor polls until a line containing sub appears, or fails the test at the
// deadline. Bounded rather than an open loop: a regression here is a hang, and
// a hung test reports nothing.
func (r *progressRecorder) waitFor(t *testing.T, sub string, within time.Duration) []string {
	t.Helper()
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		if lines := r.snapshot(); countLinesContaining(lines, sub) > 0 {
			return lines
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("no progress line containing %q within %v, got: %v", sub, within, r.snapshot())
	return nil
}

// useTestTicker swaps the heartbeat's ticker seam for an UNBUFFERED channel the
// test drives. Unbuffered is load-bearing for the concurrency guard: with the
// production single-caller loop each send blocks until the previous progress
// call returns, so ticks cannot overlap on their own.
func useTestTicker(t *testing.T) chan time.Time {
	t.Helper()
	ch := make(chan time.Time)
	prev := newSyncTicker
	newSyncTicker = func(time.Duration) (<-chan time.Time, func()) { return ch, func() {} }
	t.Cleanup(func() { newSyncTicker = prev })
	return ch
}

// useSteppingClock makes syncNow advance by step on every call, so an elapsed
// field is deterministic rather than wall-clock.
func useSteppingClock(t *testing.T, step time.Duration) {
	t.Helper()
	var mu sync.Mutex
	n := 0
	prev := syncNow
	syncNow = func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		n++
		return time.Unix(0, 0).Add(time.Duration(n) * step)
	}
	t.Cleanup(func() { syncNow = prev })
}

type prepResult struct {
	h   source.Handle
	err error
}

// The heartbeat must be driven by the TIMER, not by p4's output: the whole
// point is telling a live multi-hour transfer from a wedged one, and a wedged
// one writes nothing. The stream fixture therefore blocks and emits ZERO lines,
// and the first assertion - no summary before any tick - is what refuses an
// implementation that emits at the top of its loop and only then waits.
func TestProvider_ARunningSyncEmitsOneSummaryPerTickWithNoP4Output(t *testing.T) {
	fr, syncKey, spec := syncFixture(t)
	release := make(chan struct{})
	fr.setStreamBlock(syncKey, release)

	tick := useTestTicker(t)
	useSteppingClock(t, time.Second)

	p := New(Config{
		Root: t.TempDir(), Hostname: "h", Client: &Client{r: fr},
		SyncHeartbeatInterval: 30 * time.Second,
		FreeDiskGB:            func(string) (int64, error) { return 811, nil },
	})
	rec := &progressRecorder{}
	res := make(chan prepResult, 1)
	go func() {
		h, err := p.Prepare(context.Background(), "task-1", spec, rec.add)
		res <- prepResult{h, err}
	}()

	// Wait until the sync is actually in flight, so "no summary yet" cannot be
	// satisfied by Prepare simply not having reached the sync.
	rec.waitFor(t, "[sync] starting", 5*time.Second)
	time.Sleep(100 * time.Millisecond)
	require.Equal(t, 0, rec.count("0 files"),
		"no tick has been delivered, so no heartbeat may have been emitted, got: %v", rec.snapshot())

	tick <- time.Now()
	rec.waitFor(t, "0 files", 5*time.Second)
	var heartbeat string
	for _, l := range rec.snapshot() {
		if strings.Contains(l, "0 files") {
			heartbeat = l
		}
	}
	assert.Contains(t, heartbeat, "0 other lines")
	assert.Contains(t, heartbeat, "811 GB free")
	assert.Contains(t, heartbeat, "last -")

	close(release)
	r := <-res
	require.NoError(t, r.err)
	defer r.h.Finalize(context.Background())

	lines := rec.snapshot()
	require.Equal(t, 1, countLinesContaining(lines, "[sync] complete:"),
		"got: %v", lines)
	for _, l := range lines {
		if strings.Contains(l, "[sync] complete:") {
			assert.Contains(t, l, "0 files")
			assert.Contains(t, l, "0 other lines")
			assert.Contains(t, l, "811 GB free")
			assert.Contains(t, l, "last -")
		}
	}
}
