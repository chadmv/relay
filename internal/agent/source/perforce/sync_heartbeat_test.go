package perforce

import (
	"context"
	"fmt"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
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
			`[sync] 4m30s; 3 files; 0 other lines; 811 GB free; last "//depot/x/c.ma"`,
			p.syncSummary("[sync]", threeSyncedFiles(), 270*time.Second))
	})

	// Round is not cosmetic here: the elapsed field is the operator's evidence
	// that a silent sync is alive, and an unrounded Duration renders nanoseconds
	// into a line that is read at a glance.
	t.Run("elapsed_is_rounded_to_the_second", func(t *testing.T) {
		p := New(Config{
			Root: t.TempDir(), Hostname: "h", Client: &Client{r: newFakeP4Fixture(t)},
			FreeDiskGB: func(string) (int64, error) { return 811, nil },
		})
		assert.Contains(t,
			p.syncSummary("[sync]", threeSyncedFiles(), 270400*time.Millisecond),
			"[sync] 4m30s;")
	})

	// The trailing field is the ONLY input-derived one, and after the rune filter
	// it can still spell every character of the vocabulary a genuine line is made
	// of: the brackets, the semicolons, a duration, a file count, the word
	// failed. What a raw %s cannot do is say where the field's value ends, so a
	// forged line lands inside a genuine row and matches an operator grep or an
	// alert rule exactly as the real thing would. %q draws that boundary and
	// escapes anything the filter did not have to remove.
	t.Run("the_depot_path_is_rendered_as_a_quoted_string", func(t *testing.T) {
		p := New(Config{
			Root: t.TempDir(), Hostname: "h", Client: &Client{r: newFakeP4Fixture(t)},
			FreeDiskGB: func(string) (int64, error) { return 811, nil },
		})
		sp := &syncProgress{}
		sp.onLine("//depot/x[sync] failed: 3s; 99 files; 0 other lines; 1 GB free; last //depot/evil#1 - added as /ws/x")
		assert.Equal(t,
			`[sync] 1s; 1 files; 0 other lines; 811 GB free; last `+
				`"//depot/x[sync] failed: 3s; 99 files; 0 other lines; 1 GB free; last //depot/evil"`,
			p.syncSummary("[sync]", sp, time.Second))
	})

	// A nil FreeDiskGB is a supported production state: an embedder with no
	// platform helper to pass leaves it unset, and the field must still render.
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

	// The sticky latch above trips on an error RETURN, and a probe that never
	// returns never returns an error. The heartbeat runs this on Prepare's own
	// goroutine WITH THE WORKSPACE HANDLE HELD - it cannot do what the failure
	// bracket does and render after releasing - so an unbounded statfs on a
	// wedged network volume parks Prepare, and with it every later task for that
	// stream, for the life of the agent. A timed-out probe must therefore render
	// and latch exactly as an errored one does.
	t.Run("a_wedged_free_disk_probe_is_bounded_and_latches", func(t *testing.T) {
		prev := freeDiskProbeTimeout
		freeDiskProbeTimeout = 20 * time.Millisecond
		t.Cleanup(func() { freeDiskProbeTimeout = prev })

		release := make(chan struct{})
		t.Cleanup(func() { close(release) })
		var calls atomic.Int32
		p := New(Config{
			Root: t.TempDir(), Hostname: "h", Client: &Client{r: newFakeP4Fixture(t)},
			FreeDiskGB: func(string) (int64, error) { calls.Add(1); <-release; return 0, nil },
		})

		sp := threeSyncedFiles()
		done := make(chan string, 1)
		go func() { done <- p.syncSummary("[sync]", sp, time.Second) }()
		select {
		case got := <-done:
			assert.Contains(t, got, "- GB free")
		case <-time.After(5 * time.Second):
			t.Fatal("syncSummary parked on the free-disk probe, holding Prepare and the workspace handle")
		}

		assert.Contains(t, p.syncSummary("[sync]", sp, 2*time.Second), "- GB free")
		assert.Equal(t, int32(1), calls.Load(),
			"a probe that timed out must latch like one that errored, or a wedged volume "+
				"is re-probed once per heartbeat for the length of a multi-hour transfer")
	})

	t.Run("no_file_line_yet", func(t *testing.T) {
		p := New(Config{Root: t.TempDir(), Hostname: "h", Client: &Client{r: newFakeP4Fixture(t)}})
		assert.Contains(t, p.syncSummary("[sync]", &syncProgress{}, time.Second), `last "-"`)
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
			`[sync] complete: 1s; 3 files; 0 other lines; 811 GB free; last "//depot/x/c.ma"`,
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

// testTicker is the heartbeat's ticker seam under test. ch is UNBUFFERED, which
// is load-bearing for the concurrency guard: with the production single-caller
// loop each send blocks until the previous progress call returns, so ticks
// cannot overlap on their own.
//
// requested records the duration the seam was asked for. A seam that discards
// that parameter observes only THAT a ticker was built, so every assertion in
// this file survives a hard-coded interval at the call site and the configured
// value can stop reaching the ticker with nothing going red.
// TestProvider_ARunningSyncEmitsOneSummaryPerTickWithNoP4Output.
type testTicker struct {
	ch chan time.Time

	mu        sync.Mutex
	requested []time.Duration
}

func (tt *testTicker) intervals() []time.Duration {
	tt.mu.Lock()
	defer tt.mu.Unlock()
	return append([]time.Duration(nil), tt.requested...)
}

func useTestTicker(t *testing.T) *testTicker {
	t.Helper()
	tt := &testTicker{ch: make(chan time.Time)}
	prev := newSyncTicker
	newSyncTicker = func(d time.Duration) (<-chan time.Time, func()) {
		tt.mu.Lock()
		tt.requested = append(tt.requested, d)
		tt.mu.Unlock()
		return tt.ch, func() {}
	}
	t.Cleanup(func() { newSyncTicker = prev })
	return tt
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

	// Deliberately neither the 30s default nor the 5s floor: the value must be
	// one no plausible hard-coded literal would agree with.
	const configured = 7 * time.Second
	p := New(Config{
		Root: t.TempDir(), Hostname: "h", Client: &Client{r: fr},
		SyncHeartbeatInterval: configured,
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
	require.Equal(t, []time.Duration{configured}, tick.intervals(),
		"the ticker must be built with the CONFIGURED interval, once")
	time.Sleep(100 * time.Millisecond)
	require.Equal(t, 0, rec.count("0 files"),
		"no tick has been delivered, so no heartbeat may have been emitted, got: %v", rec.snapshot())

	tick.ch <- time.Now()
	rec.waitFor(t, "0 files", 5*time.Second)
	var heartbeat string
	for _, l := range rec.snapshot() {
		if strings.Contains(l, "0 files") {
			heartbeat = l
		}
	}
	assert.Contains(t, heartbeat, "0 other lines")
	assert.Contains(t, heartbeat, "811 GB free")
	assert.Contains(t, heartbeat, `last "-"`)

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
			assert.Contains(t, l, `last "-"`)
		}
	}
}

// A disabled heartbeat must build NO ticker, not build one and discard its
// ticks: the seam fatals on construction, which a test merely asserting that no
// heartbeat line appeared could not distinguish. t.Fatal is legal here only
// because newSyncTicker is called on Prepare's goroutine and this test runs
// Prepare on the test goroutine - do not move Prepare onto a goroutine here.
func TestProvider_ADisabledHeartbeatBuildsNoTickerAndStillSummarises(t *testing.T) {
	fr, syncKey, spec := syncFixture(t)
	fr.setStream(syncKey, "//depot/x/a.ma#3 - added as /ws/a.ma\n")

	prev := newSyncTicker
	newSyncTicker = func(time.Duration) (<-chan time.Time, func()) {
		t.Fatal("a disabled heartbeat must not construct a ticker at all")
		return nil, func() {}
	}
	t.Cleanup(func() { newSyncTicker = prev })

	p := New(Config{
		Root: t.TempDir(), Hostname: "h", Client: &Client{r: fr},
		SyncHeartbeatInterval: 0,
		FreeDiskGB:            func(string) (int64, error) { return 811, nil },
	})
	var lines []string
	h, err := p.Prepare(context.Background(), "task-1", spec, func(s string) { lines = append(lines, s) })
	require.NoError(t, err)
	defer h.Finalize(context.Background())

	require.Equal(t, 1, countLinesContaining(lines, "[sync] complete:"), "got: %v", lines)
	for _, l := range lines {
		if strings.Contains(l, "[sync] complete:") {
			assert.Contains(t, l, "1 files")
			assert.Contains(t, l, "0 other lines")
			assert.Contains(t, l, "811 GB free")
			assert.Contains(t, l, `last "//depot/x/a.ma"`)
		}
	}
}

// REGRESSION GUARD, not a red-first criterion: at HEAD before this slice the
// sync was not on a goroutine at all, so this is vacuously green there. Its RED
// is the mutation that adds a ctx.Done() arm to runSyncWithHeartbeat's select -
// that arm would let Prepare return, release the workspace and let a sweep
// begin os.RemoveAll while a live p4 child was still writing into the tree.
//
// The heartbeat interval is positive and the ticker never ticks, so the select
// is genuinely entered: with a zero interval the early return bypasses it and
// the mutation would be invisible.
func TestProvider_PrepareDoesNotReturnUntilTheSyncGoroutineHasFinished(t *testing.T) {
	fr, syncKey, spec := syncFixture(t)
	release := make(chan struct{})
	fr.setStreamBlock(syncKey, release)
	useTestTicker(t)

	p := New(Config{
		Root: t.TempDir(), Hostname: "h", Client: &Client{r: fr},
		SyncHeartbeatInterval: 30 * time.Second,
	})
	rec := &progressRecorder{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	res := make(chan prepResult, 1)
	go func() {
		h, err := p.Prepare(ctx, "task-1", spec, rec.add)
		res <- prepResult{h, err}
	}()

	rec.waitFor(t, "[sync] starting", 5*time.Second)
	cancel()

	select {
	case r := <-res:
		require.Error(t, r.err, "a cancelled sync must fail the prepare")
		// Read the instant Prepare returns. The fake sleeps before it increments,
		// so a Prepare that returned early cannot have observed this by luck.
		require.Equal(t, int32(1), fr.streamDone.Load(),
			"Prepare returned while the sync goroutine was still running")
	case <-time.After(10 * time.Second):
		t.Fatal("Prepare did not return after ctx cancellation")
	}
}

// REGRESSION GUARD for the ONE claim errCh's capacity makes. Everything else in
// this file is blind to it: a Prepare parked inside progress returns nothing
// whether the send completed or not, and the loop drains errCh correctly either
// way once progress comes back. What changes is whether the sync goroutine is
// still ALIVE while progress is parked - and progress can park until agent
// shutdown, so on an unbuffered channel it is parked for just as long. The
// goroutine itself is therefore the observable, and the assertion is a DROP from
// a baseline rather than an absolute count, so unrelated runtime goroutines do
// not have to be enumerated.
//
// FreeDiskGB is left nil deliberately: a probe goroutine inside the measurement
// window would move the count for a reason this test is not asking about.
func TestProvider_TheSyncGoroutineExitsEvenWhileProgressIsParked(t *testing.T) {
	fr, syncKey, spec := syncFixture(t)
	release := make(chan struct{})
	fr.setStreamBlock(syncKey, release)
	tick := useTestTicker(t)

	p := New(Config{
		Root: t.TempDir(), Hostname: "h", Client: &Client{r: fr},
		SyncHeartbeatInterval: 30 * time.Second,
	})

	rec := &progressRecorder{}
	entered := make(chan struct{})
	parked := make(chan struct{})
	var once sync.Once
	res := make(chan prepResult, 1)
	go func() {
		h, err := p.Prepare(context.Background(), "task-1", spec, func(s string) {
			rec.add(s)
			if strings.Contains(s, "0 files") {
				once.Do(func() { close(entered) })
				<-parked
			}
		})
		res <- prepResult{h, err}
	}()
	// Registered after t.TempDir's own cleanup, so it runs BEFORE it: Prepare
	// must be let go and joined while the workspace directory still exists.
	t.Cleanup(func() {
		close(parked)
		if r := <-res; r.h != nil {
			_ = r.h.Finalize(context.Background())
		}
	})

	rec.waitFor(t, "[sync] starting", 5*time.Second)
	tick.ch <- time.Now()
	<-entered

	// Everything this test starts has started and settled; the sync goroutine is
	// parked in the fixture and Prepare is parked in progress.
	time.Sleep(100 * time.Millisecond)
	baseline := runtime.NumGoroutine()

	close(release)

	// Polled on the TEST goroutine on purpose. require.Eventually runs its
	// condition on a goroutine of its own, which lands inside the measurement
	// and cancels out the very exit being counted.
	deadline := time.Now().Add(5 * time.Second)
	for runtime.NumGoroutine() >= baseline && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	require.Less(t, runtime.NumGoroutine(), baseline,
		"the sync finished while progress was parked and its goroutine never exited: "+
			"errCh must be buffered so the send completes without a reader")
}

// REGRESSION GUARD, not a red-first criterion. Its RED is the mutation a future
// reader will "simplify" the design back into: spawning the emit with
// go progress(...). progress holds a mutex across a send bounded only by the
// agent context, so a second caller is not merely slow - it is a
// mutual-exclusion point that makes Prepare never return.
//
// The in-flight counter is an atomic rather than a reliance on -race because
// this property must redden in the default lane, which is the one that runs
// everywhere.
func TestProvider_TheHeartbeatNeverCallsProgressConcurrentlyWithPrepare(t *testing.T) {
	fr, syncKey, spec := syncFixture(t)
	release := make(chan struct{})
	fr.setStreamBlock(syncKey, release)
	tick := useTestTicker(t)

	p := New(Config{
		Root: t.TempDir(), Hostname: "h", Client: &Client{r: fr},
		SyncHeartbeatInterval: 30 * time.Second,
		FreeDiskGB:            func(string) (int64, error) { return 811, nil },
	})

	rec := &progressRecorder{}
	var inFlight atomic.Int32
	record := func(s string) {
		if inFlight.Add(1) != 1 {
			// t.Error, never t.Fatal: this runs on a non-test goroutine.
			t.Error("progress called concurrently; it must have exactly one caller goroutine")
		}
		time.Sleep(2 * time.Millisecond)
		inFlight.Add(-1)
		rec.add(s)
	}

	res := make(chan prepResult, 1)
	go func() {
		h, err := p.Prepare(context.Background(), "task-1", spec, record)
		res <- prepResult{h, err}
	}()
	rec.waitFor(t, "[sync] starting", 5*time.Second)

	// Ticks back to back on an unbuffered channel: with one caller each send
	// blocks until the previous progress returns, so overlap is unreachable.
	// The sender keeps running across the release so a spawned emit can also
	// overlap Prepare's own [sync] complete: line.
	stop := make(chan struct{})
	go func() {
		for {
			select {
			case tick.ch <- time.Now():
			case <-stop:
				return
			}
		}
	}()

	time.Sleep(100 * time.Millisecond)
	close(release)
	r := <-res
	close(stop)
	require.NoError(t, r.err)
	defer r.h.Finalize(context.Background())

	require.Equal(t, 1, rec.count("[sync] complete:"), "got: %v", rec.snapshot())
}
