package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGetTaskLogs_PassesParams(t *testing.T) {
	srv := httptest.NewServer(whoamiHandler(true, func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/v1/tasks/t1/logs", r.URL.Path)
		require.Equal(t, "5", r.URL.Query().Get("since_seq"))
		require.Equal(t, "10", r.URL.Query().Get("limit"))
		_ = json.NewEncoder(w).Encode(map[string]any{
			"items":    []map[string]any{{"seq": 6, "stream": "stdout", "content": "hi"}},
			"next_seq": 6, "total": 100,
		})
	}))
	defer srv.Close()

	s, _ := NewServer(srv.URL, "t")
	out, terr := s.callGetTaskLogs(context.Background(), getTaskLogsArgs{TaskID: "t1", SinceSeq: 5, Limit: 10})
	require.Nil(t, terr)
	require.Len(t, out["items"], 1)
	require.EqualValues(t, 6, out["next_seq"])
	require.EqualValues(t, 100, out["total"])
}

func TestGetTaskLogs_MissingID(t *testing.T) {
	s, _ := NewServer("http://x", "t")
	_, terr := s.callGetTaskLogs(context.Background(), getTaskLogsArgs{})
	require.Equal(t, "validation", terr.Code)
}

func TestGetTaskLogs_LimitTooBig(t *testing.T) {
	s, _ := NewServer("http://x", "t")
	_, terr := s.callGetTaskLogs(context.Background(), getTaskLogsArgs{TaskID: "x", Limit: 500})
	require.Equal(t, "validation", terr.Code)
}
