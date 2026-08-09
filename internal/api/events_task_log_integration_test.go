//go:build integration

package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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

	// ?job_id= validation is deliberately UNCHANGED: an unknown job is still an
	// open, silently empty stream. It is an existing contract with existing
	// clients; the asymmetry is intentional and documented in README.md.
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
