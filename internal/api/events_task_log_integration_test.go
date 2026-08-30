//go:build integration

package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"relay/internal/api"
	"relay/internal/events"
	"relay/internal/store"
	"relay/internal/worker"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newTestServerWithBroker is newTestServer but hands back the broker so a test
// can publish directly and drive the SSE handler's delivery and drop paths
// without an agent, plus the pool so a test can build a worker.Handler against
// the SAME database. Do not call newTestPool twice - that spins up a second
// Postgres container with a different database.
func newTestServerWithBroker(t *testing.T) (*api.Server, *store.Queries, *events.Broker, *pgxpool.Pool) {
	t.Helper()
	pool := newTestPool(t)
	q := store.New(pool)
	broker := events.NewBroker()
	srv := api.New(pool, q, broker, worker.NewRegistry(), nil, 0, 0, 0, 0)
	return srv, q, broker, pool
}

// seedTaskViaAPI submits a one-task job and returns (jobID, taskID) as strings.
func seedTaskViaAPI(t *testing.T, srv *api.Server, token string) (string, string) {
	t.Helper()
	body := `{"name":"j","tasks":[{"name":"t","command":["echo","hi"]}]}`
	req := httptest.NewRequest("POST", "/v1/jobs", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	var job map[string]any
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&job))
	return job["id"].(string), job["tasks"].([]any)[0].(map[string]any)["id"].(string)
}

func TestEvents_TaskIDValidation(t *testing.T) {
	srv, q, _, _ := newTestServerWithBroker(t)
	user := createTestUser(t, q, "Alice", "sse-valid@example.com", false)
	token := createTestToken(t, q, user.ID)
	_, taskID := seedTaskViaAPI(t, srv, token)

	// Each probe is bounded. A rejection is written and returns immediately, well
	// inside the budget; a handler that wrongly accepts the parameter would
	// otherwise stream forever, because a httptest request's context is never
	// cancelled. Bounding it here means a lost validation fails this test instead
	// of hanging the whole package.
	do := func(query string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("GET", "/v1/events"+query, nil)
		req.Header.Set("Authorization", "Bearer "+token)
		ctx, cancel := context.WithTimeout(req.Context(), 2*time.Second)
		defer cancel()
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req.WithContext(ctx))
		return rec
	}

	// Malformed UUID -> 400. A typo must not yield a stream that hangs open
	// forever looking like "the task has no output".
	rec := do("?task_id=not-a-uuid")
	assert.Equal(t, http.StatusBadRequest, rec.Code)

	// Well-formed but unknown -> 404.
	rec = do("?task_id=11111111-1111-1111-1111-111111111111")
	assert.Equal(t, http.StatusNotFound, rec.Code)

	// ?job_id= VALIDATION is deliberately unchanged, and this assertion is the
	// proof that the 2026-08-30 canonicalisation was additive: `not-a-uuid` is
	// 10 bytes, so pgtype.UUID.Scan takes its default branch, canonicalJobIDFilter
	// returns the string untouched, and this is still an open silently empty
	// stream rather than a 400. It is an existing contract with existing clients.
	// The asymmetry with task_id is intentional and is about REJECTION only -
	// both parameters are canonicalised. See
	// TestEvents_JobIDSpellingIsCanonicalisedNotRejected.
	// (Served with a cancelled context so the handler returns immediately.)
	req := httptest.NewRequest("GET", "/v1/events?job_id=not-a-uuid", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	ctx, cancel := context.WithCancel(req.Context())
	cancel()
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req.WithContext(ctx))
	assert.NotEqual(t, http.StatusBadRequest, rec.Code)

	// Valid task -> a live SSE stream. Positive control for the two rejections
	// above: it proves the handler can reach 200 on this same path at all.
	req = httptest.NewRequest("GET", "/v1/events?task_id="+taskID, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	streamCtx, streamCancel := context.WithCancel(req.Context())
	defer streamCancel()
	gw := newGateWriter()
	close(gw.release)
	done := make(chan struct{})
	go func() { defer close(done); srv.Handler().ServeHTTP(gw, req.WithContext(streamCtx)) }()
	// A recorded Flush means the handler got past Subscribe, i.e. it reached the
	// streaming path rather than an error return.
	require.Eventually(t, func() bool { return gw.flushed() >= 1 },
		5*time.Second, 5*time.Millisecond, "handler never started the stream")
	assert.Equal(t, "text/event-stream", gw.header().Get("Content-Type"))
	streamCancel()
	<-done
}

// gateWriter is an http.ResponseWriter whose first Write blocks until release is
// closed. That pins handleEvents inside one write for as long as the test needs,
// so the broker can be filled and the subscription drop-closed deterministically
// - no sleeps and no assumption about how fast the handler drains.
type gateWriter struct {
	mu      sync.Mutex
	hdr     http.Header
	buf     bytes.Buffer
	flushes int
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func newGateWriter() *gateWriter {
	return &gateWriter{
		hdr:     make(http.Header),
		entered: make(chan struct{}),
		release: make(chan struct{}),
	}
}

func (g *gateWriter) Header() http.Header { return g.hdr }
func (g *gateWriter) WriteHeader(int)     {}

// Flush records that it happened. handleEvents calls Subscribe and then
// immediately flushes, before its first receive, so "this writer has seen a
// flush" is an exact, deterministic barrier for "this subscription is live".
// That replaces a wall-clock sleep, which could not distinguish a slow
// subscribe from a broken one.
func (g *gateWriter) Flush() {
	g.mu.Lock()
	g.flushes++
	g.mu.Unlock()
}

func (g *gateWriter) flushed() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.flushes
}

func (g *gateWriter) Write(p []byte) (int, error) {
	g.once.Do(func() { close(g.entered) })
	<-g.release
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.buf.Write(p)
}

func (g *gateWriter) body() string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.buf.String()
}

// header returns a snapshot of the headers. handleEvents sets them from its own
// goroutine, so reading g.hdr directly would be a data race under -race.
func (g *gateWriter) header() http.Header {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.hdr.Clone()
}

func TestEvents_DroppedFrameOnSlowConsumer(t *testing.T) {
	srv, q, broker, _ := newTestServerWithBroker(t)
	user := createTestUser(t, q, "Alice", "sse-dropped@example.com", false)
	token := createTestToken(t, q, user.ID)
	_, taskID := seedTaskViaAPI(t, srv, token)

	req := httptest.NewRequest("GET", "/v1/events?task_id="+taskID, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	gw := newGateWriter()
	done := make(chan struct{})
	go func() { defer close(done); srv.Handler().ServeHTTP(gw, req) }()

	require.Eventually(t, func() bool { return broker.HasLogSubscriber(taskID) },
		5*time.Second, 5*time.Millisecond, "handler never subscribed")

	pub := func() {
		broker.Publish(events.Event{
			Type: events.TypeTaskLog, TaskID: taskID, Data: []byte(`{"seq":1}`),
		})
	}
	// One event, which the handler pops and then blocks writing.
	pub()
	select {
	case <-gw.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("handler never entered Write")
	}
	// 70 more: 64 fill the buffer, the next is a drop-close.
	for i := 0; i < 70; i++ {
		pub()
	}
	require.False(t, broker.HasLogSubscriber(taskID), "broker should have dropped the stalled subscriber")

	close(gw.release)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("handler did not return after the broker closed its channel")
	}

	body := gw.body()
	const droppedFrame = "event: dropped\ndata: {\"reason\":\"slow_consumer\"}\n\n"
	assert.Contains(t, body, droppedFrame)
	assert.True(t, strings.HasSuffix(body, droppedFrame),
		"the dropped frame must be the LAST frame written")
}

func TestEvents_NoDroppedFrameWhenTheClientDisconnects(t *testing.T) {
	srv, q, broker, _ := newTestServerWithBroker(t)
	user := createTestUser(t, q, "Alice", "sse-nodrop@example.com", false)
	token := createTestToken(t, q, user.ID)
	_, taskID := seedTaskViaAPI(t, srv, token)

	req := httptest.NewRequest("GET", "/v1/events?task_id="+taskID, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	ctx, cancel := context.WithCancel(req.Context())
	gw := newGateWriter()
	close(gw.release) // never block; we want the handler to write normally
	done := make(chan struct{})
	go func() { defer close(done); srv.Handler().ServeHTTP(gw, req.WithContext(ctx)) }()

	require.Eventually(t, func() bool { return broker.HasLogSubscriber(taskID) },
		5*time.Second, 5*time.Millisecond)

	// Positive control first: one real frame proves the write path works, so the
	// "no dropped frame" assertion below cannot pass because nothing was written.
	broker.Publish(events.Event{
		Type: events.TypeTaskLog, TaskID: taskID,
		Data: []byte(`{"seq":1,"stream":"stdout","content":"line one\nline two\n"}`),
	})
	require.Eventually(t, func() bool { return strings.Contains(gw.body(), "event: task_log") },
		5*time.Second, 5*time.Millisecond)

	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("handler did not return on client disconnect")
	}
	assert.NotContains(t, gw.body(), "event: dropped",
		"a client that went away must not be told it fell behind")

	// Framing: a marshalled payload contains no literal newline (json escapes \n),
	// so each task_log event is exactly ONE data: line. handleEvents re-prefixes
	// literal newlines with "data: ", so a hand-concatenated payload would split
	// here and corrupt every multi-line chunk.
	frame := gw.body()
	i := strings.Index(frame, "event: task_log")
	require.GreaterOrEqual(t, i, 0)
	frame = frame[i:]
	assert.Equal(t, 1, strings.Count(frame, "data: "), "exactly one data: line per task_log frame")
	line := strings.TrimPrefix(strings.SplitN(strings.SplitN(frame, "\n", 2)[1], "\n", 2)[0], "data: ")
	var payload map[string]any
	require.NoError(t, json.Unmarshal([]byte(line), &payload))
	assert.Equal(t, "line one\nline two\n", payload["content"], "newlines survive the round trip")
}

func TestEvents_DeliveryMatrix(t *testing.T) {
	srv, q, broker, _ := newTestServerWithBroker(t)
	user := createTestUser(t, q, "Alice", "sse-matrix@example.com", false)
	token := createTestToken(t, q, user.ID)
	jobID, taskID := seedTaskViaAPI(t, srv, token)

	// Four concurrent subscriptions through the real HTTP handler.
	open := func(query string) (*gateWriter, func()) {
		req := httptest.NewRequest("GET", "/v1/events"+query, nil)
		req.Header.Set("Authorization", "Bearer "+token)
		ctx, cancel := context.WithCancel(req.Context())
		gw := newGateWriter()
		close(gw.release)
		done := make(chan struct{})
		go func() { defer close(done); srv.Handler().ServeHTTP(gw, req.WithContext(ctx)) }()
		return gw, func() { cancel(); <-done }
	}

	global, closeGlobal := open("")
	defer closeGlobal()
	jobOnly, closeJobOnly := open("?job_id=" + jobID)
	defer closeJobOnly()
	taskOnly, closeTaskOnly := open("?task_id=" + taskID)
	defer closeTaskOnly()
	both, closeBoth := open("?job_id=" + jobID + "&task_id=" + taskID)
	defer closeBoth()

	// Barrier: every one of the four subscriptions must be live before anything is
	// published, or a subscriber could "miss" an event merely for having
	// subscribed late and the negative assertions below would pass for the wrong
	// reason. A recorded Flush proves Subscribe already returned.
	for i, gw := range []*gateWriter{global, jobOnly, taskOnly, both} {
		gw, i := gw, i
		require.Eventually(t, func() bool { return gw.flushed() >= 1 },
			5*time.Second, 5*time.Millisecond,
			"subscription %d never became live", i)
	}
	require.True(t, broker.HasLogSubscriber(taskID), "the two task-scoped subscriptions must be registered")

	broker.Publish(events.Event{Type: "task", JobID: jobID, Data: []byte(`{"id":"x","status":"running"}`)})
	broker.Publish(events.Event{Type: events.TypeTaskLog, JobID: jobID, TaskID: taskID, Data: []byte(`{"seq":1}`)})

	// Positive expectations first, so the negatives below cannot pass vacuously.
	// Match "event: task\n" with the newline, never the bare prefix: "event: task"
	// is a prefix of "event: task_log", so a subscription that received ONLY log
	// frames would satisfy a prefix match and this control would pass while the
	// status routing was broken.
	for name, gw := range map[string]*gateWriter{"global": global, "jobOnly": jobOnly, "both": both} {
		gw := gw
		require.Eventually(t, func() bool { return strings.Contains(gw.body(), "event: task\n") },
			5*time.Second, 5*time.Millisecond, name+" should receive the status event")
	}
	for name, gw := range map[string]*gateWriter{"taskOnly": taskOnly, "both": both} {
		gw := gw
		require.Eventually(t, func() bool { return strings.Contains(gw.body(), "event: task_log") },
			5*time.Second, 5*time.Millisecond, name+" should receive the task_log event")
	}

	// Negatives. This is the whole point: a plain GET /v1/events (which relay
	// watch opens) must never become a cluster-wide log firehose.
	assert.NotContains(t, global.body(), "task_log", "a global subscriber must receive NO task_log frames")
	assert.NotContains(t, jobOnly.body(), "task_log", "a job-scoped subscriber must receive NO task_log frames")
	assert.NotContains(t, taskOnly.body(), "event: task\n",
		"?task_id= alone must not become a global status subscription")
}

// TestEvents_JobIDSpellingIsCanonicalisedNotRejected is the headline test for
// bug-2026-08-27-python-sdk-follow-job-hangs-on-noncanonical-job-id.
//
// GET /v1/jobs/{id} accepts every spelling pgtype.UUID.Scan takes; the broker's
// status filter is an exact string compare against a JobID that every publisher
// builds with uuidStr. So before this slice, an id that answered 200 on the REST
// route subscribed to a filter nothing could ever match - an open, silently
// empty SSE stream, forever, on a client with no read timeout.
//
// THE UNDERSCORE CASE IS FIRST AND IS NOT DECORATION. pgtype.UUID.Scan slices
// indexes 8, 13, 18 and 23 out of the 36-byte form WITHOUT EXAMINING THEM, so
// any byte may sit there. That row is the one no client-side canonicaliser built
// on Python's uuid.UUID can normalise, and it is therefore the single assertion
// that discriminates a server-side fix from every SDK-side one. A discriminating
// input placed last cannot detect an early-exit defect, so it leads.
//
// Every probe is built through url.Values. Concatenating the spelling into the
// query string directly is wrong for at least one spelling in the sibling test
// below - a raw `+` decodes to a SPACE - and a negative assertion on the wrong
// string passes for the wrong reason.
//
// Bounding: a httptest request's context is never cancelled, so a handler that
// streams forever would hang the package rather than fail the test. Each subtest
// owns a cancel and every wait is a bounded require.Eventually.
func TestEvents_JobIDSpellingIsCanonicalisedNotRejected(t *testing.T) {
	srv, q, broker, _ := newTestServerWithBroker(t)
	user := createTestUser(t, q, "Alice", "sse-spelling@example.com", false)
	token := createTestToken(t, q, user.ID)
	jobID, _ := seedTaskViaAPI(t, srv, token)

	for _, sp := range []struct{ name, id string }{
		{"underscore separators", strings.ReplaceAll(jobID, "-", "_")},
		{"uppercase", strings.ToUpper(jobID)},
		{"dashless", strings.ReplaceAll(jobID, "-", "")},
	} {
		sp := sp
		t.Run(sp.name, func(t *testing.T) {
			require.NotEqual(t, jobID, sp.id,
				"the probe must differ from the canonical spelling, or this subtest proves nothing; "+
					"for the uppercase row this can only happen if gen_random_uuid() produced 32 "+
					"decimal digits, about 1 in 6.7 million - re-run")

			vals := url.Values{"job_id": {sp.id}}
			req := httptest.NewRequest("GET", "/v1/events?"+vals.Encode(), nil)
			req.Header.Set("Authorization", "Bearer "+token)
			ctx, cancel := context.WithCancel(req.Context())
			gw := newGateWriter()
			close(gw.release) // never block; the handler should write normally
			done := make(chan struct{})
			go func() { defer close(done); srv.Handler().ServeHTTP(gw, req.WithContext(ctx)) }()
			defer func() { cancel(); <-done }()

			// A recorded Flush is the file's deterministic barrier for "this
			// subscription is live": handleEvents subscribes and then flushes,
			// before its first receive. No sleeps.
			require.Eventually(t, func() bool { return gw.flushed() >= 1 },
				5*time.Second, 5*time.Millisecond, "the subscription never became live")

			// The canonical spelling is what every production publisher emits.
			broker.Publish(events.Event{
				Type:  "job",
				JobID: jobID,
				Data:  []byte(`{"status":"done","probe":"canonicalised"}`),
			})

			require.Eventually(t, func() bool {
				return strings.Contains(gw.body(), `"probe":"canonicalised"`)
			}, 5*time.Second, 5*time.Millisecond,
				"a spelling GET /v1/jobs/{id} ACCEPTS must subscribe to the job it names")
		})
	}
}

// TestEvents_JobIDRejectedSpellingsAreNotCanonicalised is the other direction of
// the item's acceptance criterion, and the test that kills the one mutation that
// makes this slice WORSE than doing nothing.
//
// uuidStr returns "" for an invalid pgtype.UUID, and events.Filter{JobID: ""} is
// the broker's BROADCAST scope (internal/events/broker.go: "empty = broadcast to
// all"). So `u, _ := parseUUID(raw); return uuidStr(u)` - one deleted guard -
// turns every unparseable ?job_id= into a whole-cluster status feed. This test
// asserts SCOPE, not the absence of an error: a fail-open here is an escalation
// and nothing about it looks like a failure.
//
// Each probe is ?job_id= ONLY. Publish's status branch skips a filter whose
// JobID is "" and whose TaskID is not, so a probe carrying a task_id would be
// routed as a log tail under the mutation and the kill would be vacuous. Do not
// add a task_id to these.
//
// Each probe is built through url.Values because a raw `+` in a query string
// decodes to a SPACE - the sign-prefixed row would otherwise arrive as a
// 33-byte string with a leading space, still rejected, but not the string this
// test claims to be about.
//
// The four spellings are the ones Python's uuid.UUID ACCEPTS and this server
// does not (spec section 4.4). For the sign-prefixed row uuid.UUID does not
// merely over-accept: it resolves to a DIFFERENT uuid than the string names.
// That is why no canonicaliser may live in a client.
//
// A rejected spelling is still not a 4xx: the flushed()>=1 barrier and the
// Content-Type check below are the observable form of that, because gateWriter
// discards the status code.
func TestEvents_JobIDRejectedSpellingsAreNotCanonicalised(t *testing.T) {
	srv, q, broker, _ := newTestServerWithBroker(t)
	user := createTestUser(t, q, "Alice", "sse-rejected@example.com", false)
	token := createTestToken(t, q, user.ID)
	jobID, _ := seedTaskViaAPI(t, srv, token)

	open := func(t *testing.T, spelling string) *gateWriter {
		t.Helper()
		vals := url.Values{"job_id": {spelling}}
		req := httptest.NewRequest("GET", "/v1/events?"+vals.Encode(), nil)
		req.Header.Set("Authorization", "Bearer "+token)
		ctx, cancel := context.WithCancel(req.Context())
		gw := newGateWriter()
		close(gw.release)
		done := make(chan struct{})
		go func() { defer close(done); srv.Handler().ServeHTTP(gw, req.WithContext(ctx)) }()
		t.Cleanup(func() { cancel(); <-done })
		require.Eventually(t, func() bool { return gw.flushed() >= 1 },
			5*time.Second, 5*time.Millisecond,
			"the subscription never became live for %q - a rejected spelling must still open a stream", spelling)
		assert.Equal(t, "text/event-stream", gw.header().Get("Content-Type"),
			"%q must be an open stream, never a 4xx", spelling)
		return gw
	}

	// Positive control first, so the negatives below cannot pass vacuously.
	control := open(t, jobID)

	dashless := strings.ReplaceAll(jobID, "-", "")
	rejected := map[string]*gateWriter{}
	for name, spelling := range map[string]string{
		"brace wrapped":   "{" + jobID + "}",
		"urn prefixed":    "urn:uuid:" + jobID,
		"trailing hyphen": jobID + "-",
		"sign prefixed":   "+" + dashless[:31],
	} {
		rejected[name] = open(t, spelling)
	}

	broker.Publish(events.Event{
		Type:  "job",
		JobID: jobID,
		Data:  []byte(`{"status":"done","probe":"scope"}`),
	})

	require.Eventually(t, func() bool { return strings.Contains(control.body(), `"probe":"scope"`) },
		5*time.Second, 5*time.Millisecond,
		"the canonical control never received the frame - every negative below would be vacuous")

	for name, gw := range rejected {
		name, gw := name, gw
		assert.Never(t, func() bool { return strings.Contains(gw.body(), `"probe":"scope"`) },
			500*time.Millisecond, 25*time.Millisecond,
			"%s: a spelling the server REJECTS must not be silently widened into one it accepts. "+
				"Receiving this frame means the filter became \"\", which is the broker's BROADCAST scope",
			name)
	}
}
