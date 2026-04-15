package api

import (
	"fmt"
	"net/http"
	"strings"
)

func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	jobID := r.URL.Query().Get("job_id")
	ch, cancel := s.broker.Subscribe(jobID)
	defer cancel()

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming not supported")
		return
	}
	flusher.Flush()

	for {
		select {
		case <-r.Context().Done():
			return
		case e := <-ch:
			// Replace newlines in data to keep SSE frame valid.
			// Per SSE spec, each line in the data value needs its own "data:" prefix.
			dataStr := strings.ReplaceAll(string(e.Data), "\n", "\ndata: ")
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", e.Type, dataStr)
			flusher.Flush()
		}
	}
}
