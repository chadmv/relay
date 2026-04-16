// internal/cli/client_test.go
package cli

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestStreamEvents_ParsesFrames(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "Bearer tok", r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "event: job\ndata: {\"status\":\"done\"}\n\n")
		fmt.Fprint(w, "event: task\ndata: {\"id\":\"abc\",\"status\":\"done\"}\n\n")
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "tok")
	var got []SSEEvent
	err := c.StreamEvents(context.Background(), "/v1/events", func(e SSEEvent) bool {
		got = append(got, e)
		return true // keep going until server closes
	})
	require.NoError(t, err)
	require.Len(t, got, 2)
	require.Equal(t, "job", got[0].Type)
	require.Equal(t, `{"status":"done"}`, got[0].Data)
	require.Equal(t, "task", got[1].Type)
}

func TestStreamEvents_HandlerReturnFalseStops(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "event: job\ndata: {\"status\":\"done\"}\n\n")
		fmt.Fprint(w, "event: job\ndata: {\"status\":\"failed\"}\n\n")
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "")
	count := 0
	_ = c.StreamEvents(context.Background(), "/v1/events", func(e SSEEvent) bool {
		count++
		return false // stop after first event
	})
	require.Equal(t, 1, count)
}

func TestClientDo_4xxReturnsErrorField(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprint(w, `{"error":"job not found"}`)
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "tok")
	err := c.do(context.Background(), "GET", "/v1/jobs/x", nil, nil)
	require.EqualError(t, err, "job not found")
}

func TestClientDo_5xxReturnsGenericMessage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, `{}`)
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "tok")
	err := c.do(context.Background(), "GET", "/v1/jobs/x", nil, nil)
	require.EqualError(t, err, "server error (500) — try again")
}
