package server

import (
	"fmt"
	"net/http"
	"strconv"
	"time"
)

// handleEventsSSE streams global events via Server-Sent Events (LOG-4, spec §7.3).
func (s *Server) handleEventsSSE(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming unsupported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	// Parse Last-Event-ID header or ?since= query parameter
	var sinceID int64
	if lastIDStr := r.Header.Get("Last-Event-ID"); lastIDStr != "" {
		if id, err := strconv.ParseInt(lastIDStr, 10, 64); err == nil {
			sinceID = id
		}
	} else if sinceStr := r.URL.Query().Get("since"); sinceStr != "" {
		if id, err := strconv.ParseInt(sinceStr, 10, 64); err == nil {
			sinceID = id
		}
	}

	// 1. Send all events since Last-Event-ID
	events, err := s.DB.Events().ListEventsSince(r.Context(), sinceID, 200)
	if err == nil {
		for _, e := range events {
			fmt.Fprintf(w, "id: %d\nevent: %s\ndata: %s\n\n", e.ID, e.Type, e.Payload)
			if e.ID > sinceID {
				sinceID = e.ID
			}
		}
		flusher.Flush()
	}

	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	heartbeatTicker := time.NewTicker(15 * time.Second)
	defer heartbeatTicker.Stop()

	for {
		select {
		case <-r.Context().Done():
			return

		case <-heartbeatTicker.C:
			// Send heartbeat comment (spec §7.3)
			fmt.Fprintf(w, ": heartbeat\n\n")
			flusher.Flush()

		case <-ticker.C:
			newEvents, err := s.DB.Events().ListEventsSince(r.Context(), sinceID, 50)
			if err == nil && len(newEvents) > 0 {
				for _, e := range newEvents {
					fmt.Fprintf(w, "id: %d\nevent: %s\ndata: %s\n\n", e.ID, e.Type, e.Payload)
					if e.ID > sinceID {
						sinceID = e.ID
					}
				}
				flusher.Flush()
			}
		}
	}
}
