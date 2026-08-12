//go:build integration

package worker_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"sync"
	"testing"
	"time"

	"relay/internal/api"
	"relay/internal/events"
	relayv1 "relay/internal/proto/relayv1"
	"relay/internal/relayclient"
	"relay/internal/store"
	"relay/internal/tokenhash"
	"relay/internal/worker"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// These are the only tests that exercise the whole path end to end:
// worker.Handler.handleTaskLog -> the fenced insert -> Broker.Publish ->
// api.handleEvents -> HTTP SSE -> relayclient.StreamEvents.
//
// They live in package worker_test rather than api_test because HandleTaskLog is
// exported only through internal/worker/export_test.go, which is not visible to
// other packages. worker_test importing internal/api is not a cycle: internal/api
// imports internal/worker, but this is an external test package.

type e2eLogLine struct {
	TaskID    string    `json:"task_id"`
	JobID     string    `json:"job_id"`
	Seq       int64     `json:"seq"`
	Stream    string    `json:"stream"`
	Content   string    `json:"content"`
	CreatedAt time.Time `json:"created_at"`
}

// e2eAPIServer wires a real api.Server over the same pool and broker the worker
// handler publishes into, and returns it plus an authenticated bearer token.
func e2eAPIServer(t *testing.T, ctx context.Context, q *store.Queries, pool *pgxpool.Pool, broker *events.Broker, email string) (*api.Server, string) {
	t.Helper()
	user, err := q.CreateUserWithPassword(ctx, store.CreateUserWithPasswordParams{
		Name: "e2e", Email: email, IsAdmin: false, PasswordHash: "x",
	})
	require.NoError(t, err)

	raw := make([]byte, 16)
	_, err = rand.Read(raw)
	require.NoError(t, err)
	rawHex := hex.EncodeToString(raw)
	_, err = q.CreateToken(ctx, store.CreateTokenParams{
		UserID: user.ID, TokenHash: tokenhash.Hash(rawHex), ExpiresAt: pgtype.Timestamptz{},
	})
	require.NoError(t, err)

	srv := api.New(pool, q, broker, worker.NewRegistry(), nil, 0, 0, 0, 0)
	return srv, rawHex
}

// seedClaimedTaskForUser is seedClaimedTask but reuses an existing user so the
// job's submitted_by is a real row. It forwards the task's assignee id.
func seedClaimedTaskForUser(t *testing.T, ctx context.Context, q *store.Queries, email, hostname string) (jobID, taskID, workerID pgtype.UUID, epoch int32) {
	return seedClaimedTask(t, ctx, q, email, hostname)
}

func TestEndToEnd_AgentChunkReachesASubscribedSSEClient(t *testing.T) {
	q, pool := newTestStore(t)
	ctx := context.Background()
	broker := events.NewBroker()
	h := worker.NewHandler(q, pool, worker.NewRegistry(), broker, func() {})

	jobIDU, taskIDU, workerID, epoch := seedClaimedTaskForUser(t, ctx, q, "e2e-seed@example.com", "w-e2e1")
	jobID := h.UUIDStringForTest(jobIDU)
	taskID := h.UUIDStringForTest(taskIDU)

	srv, token := e2eAPIServer(t, ctx, q, pool, broker, "e2e-logs@example.com")
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	// A single connection carrying BOTH families: the job's status events and the
	// selected task's logs. This is the shape the job-detail log tab uses, and it
	// is why ?follow=1 was rejected - that would need two connections per screen.
	streamCtx, streamCancel := context.WithCancel(ctx)
	defer streamCancel()
	c := relayclient.NewClient(ts.URL, token)

	subscribed := make(chan struct{})
	var mu sync.Mutex
	var logs []e2eLogLine
	var statusTypes []string
	streamErr := make(chan error, 1)

	go func() {
		streamErr <- c.StreamEvents(streamCtx,
			"/v1/events?job_id="+jobID+"&task_id="+taskID,
			func() bool { close(subscribed); return true },
			func(e relayclient.SSEEvent) bool {
				mu.Lock()
				defer mu.Unlock()
				switch e.Type {
				case "task_log":
					var l e2eLogLine
					if err := json.Unmarshal([]byte(e.Data), &l); err != nil {
						t.Errorf("unmarshal task_log frame: %v", err)
						return false
					}
					logs = append(logs, l)
				default:
					statusTypes = append(statusTypes, e.Type)
				}
				return true
			})
	}()

	// onSubscribed fires after the 200, and handleEvents subscribes before it
	// flushes, so the subscription is live here. No sleep needed.
	<-subscribed

	const n = 5
	for i := 0; i < n; i++ {
		h.HandleTaskLog(ctx, workerID, &relayv1.TaskLogChunk{
			TaskId:  taskID,
			Stream:  relayv1.LogStream_LOG_STREAM_STDOUT,
			Content: []byte(fmt.Sprintf("line %d\nmore %d\n", i, i)),
			Epoch:   int64(epoch),
		})
	}

	require.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(logs) == n
	}, 10*time.Second, 10*time.Millisecond, "not all task_log frames arrived")

	mu.Lock()
	got := append([]e2eLogLine(nil), logs...)
	mu.Unlock()

	// Payload symmetry with the polling endpoint, asserted against the real
	// response rather than a hand-written expectation.
	req := httptest.NewRequest("GET", "/v1/tasks/"+taskID+"/logs?limit=200", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	var page struct {
		Items []struct {
			Seq       int64     `json:"seq"`
			Stream    string    `json:"stream"`
			Content   string    `json:"content"`
			CreatedAt time.Time `json:"created_at"`
		} `json:"items"`
		NextSeq int64 `json:"next_seq"`
		Total   int64 `json:"total"`
	}
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&page))
	require.Len(t, page.Items, n)

	for i := range got {
		assert.Equal(t, taskID, got[i].TaskID)
		assert.Equal(t, jobID, got[i].JobID)
		assert.Equal(t, page.Items[i].Seq, got[i].Seq, "seq must be the polling endpoint's seq")
		assert.Equal(t, page.Items[i].Stream, got[i].Stream)
		assert.Equal(t, page.Items[i].Content, got[i].Content)
		assert.Equal(t, fmt.Sprintf("line %d\nmore %d\n", i, i), got[i].Content,
			"multi-line content survives SSE framing intact")
		if i > 0 {
			assert.Greater(t, got[i].Seq, got[i-1].Seq, "seq increases monotonically in publish order")
		}
	}

	// Status events on the SAME connection are unaffected. Positive control that
	// this stream can carry status at all.
	broker.Publish(events.Event{Type: "job", JobID: jobID, Data: []byte(`{"id":"x","status":"done"}`)})
	require.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		for _, ty := range statusTypes {
			if ty == "job" {
				return true
			}
		}
		return false
	}, 5*time.Second, 10*time.Millisecond, "status events must still be delivered alongside logs")

	streamCancel()
	<-streamErr
}

func TestEndToEnd_BackfillJoinIsGaplessAndDeduped(t *testing.T) {
	q, pool := newTestStore(t)
	ctx := context.Background()
	broker := events.NewBroker()
	h := worker.NewHandler(q, pool, worker.NewRegistry(), broker, func() {})

	_, taskIDU, workerID, epoch := seedClaimedTaskForUser(t, ctx, q, "e2e-bf-seed@example.com", "w-e2e2")
	taskID := h.UUIDStringForTest(taskIDU)

	srv, token := e2eAPIServer(t, ctx, q, pool, broker, "e2e-backfill@example.com")
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	// Step 1 of the documented contract: SUBSCRIBE FIRST and buffer events.
	streamCtx, streamCancel := context.WithCancel(ctx)
	defer streamCancel()
	c := relayclient.NewClient(ts.URL, token)
	subscribed := make(chan struct{})
	var mu sync.Mutex
	var buffered []e2eLogLine
	streamErr := make(chan error, 1)
	go func() {
		streamErr <- c.StreamEvents(streamCtx, "/v1/events?task_id="+taskID,
			func() bool { close(subscribed); return true },
			func(e relayclient.SSEEvent) bool {
				if e.Type != "task_log" {
					return true
				}
				var l e2eLogLine
				if err := json.Unmarshal([]byte(e.Data), &l); err != nil {
					t.Errorf("unmarshal task_log frame: %v", err)
					return false
				}
				mu.Lock()
				buffered = append(buffered, l)
				mu.Unlock()
				return true
			})
	}()
	<-subscribed

	const total = 20
	// Emit half, page, then emit the rest - so the pages and the events overlap,
	// which is exactly the window a reversed order would leave a hole in. Each
	// index is emitted exactly once, so the row count is an exact assertion.
	emit := func(from, to int) {
		for i := from; i < to; i++ {
			h.HandleTaskLog(ctx, workerID, &relayv1.TaskLogChunk{
				TaskId: taskID, Content: []byte(fmt.Sprintf("chunk-%02d\n", i)), Epoch: int64(epoch),
			})
		}
	}
	emit(0, 10)

	// Step 2: page ?since_seq=0 until next_seq == 0, recording maxSeq.
	type item struct {
		Seq     int64  `json:"seq"`
		Content string `json:"content"`
	}
	var backfill []item
	var maxSeq int64
	sinceSeq := int64(0)
	next := 10
	for {
		req := httptest.NewRequest("GET",
			fmt.Sprintf("/v1/tasks/%s/logs?limit=3&since_seq=%d", taskID, sinceSeq), nil)
		req.Header.Set("Authorization", "Bearer "+token)
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)
		require.Equal(t, http.StatusOK, rec.Code)
		var page struct {
			Items   []item `json:"items"`
			NextSeq int64  `json:"next_seq"`
		}
		require.NoError(t, json.NewDecoder(rec.Body).Decode(&page))
		backfill = append(backfill, page.Items...)
		for _, it := range page.Items {
			if it.Seq > maxSeq {
				maxSeq = it.Seq
			}
		}
		if page.NextSeq == 0 {
			break
		}
		sinceSeq = page.NextSeq
		if next < total { // keep producing while the client pages
			emit(next, next+1)
			next++
		}
	}
	emit(next, total)

	// Wait for the whole live stream, not for a count that is already satisfied.
	// The subscription went live before the first emit and nothing is dropped
	// here (20 events against a 64-slot buffer that is actively drained), so
	// every chunk must arrive as an event. Waiting on `total` rather than on
	// `total-len(backfill)` matters: buffered counts ALL events, so the smaller
	// threshold is met while the tail is still in flight and the merge below
	// would then run against an incomplete buffer and under-count.
	require.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(buffered) == total
	}, 10*time.Second, 10*time.Millisecond, "live events did not cover the tail")

	// Step 3: render the backfill, then apply events with seq > maxSeq.
	merged := append([]item(nil), backfill...)
	mu.Lock()
	for _, l := range buffered {
		if l.Seq > maxSeq {
			merged = append(merged, item{Seq: l.Seq, Content: l.Content})
		}
	}
	mu.Unlock()
	sort.Slice(merged, func(i, j int) bool { return merged[i].Seq < merged[j].Seq })

	// The reconstruction must equal the DB exactly: no gap, no duplicate.
	rows, err := q.GetTaskLogs(ctx, taskIDU)
	require.NoError(t, err)
	require.Len(t, rows, total, "every emitted chunk must have been persisted")
	require.Len(t, merged, len(rows), "backfill+events must reconstruct every row exactly once")
	for i := range rows {
		assert.Equal(t, rows[i].ID, merged[i].Seq)
		assert.Equal(t, rows[i].Content, merged[i].Content)
	}

	// Await the stream goroutine before returning. Its handler calls t.Errorf, and
	// that panics with "Log in goroutine after test has completed" if it fires
	// after this function returns.
	streamCancel()
	<-streamErr
}
