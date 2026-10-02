// Package server implements Server-Sent Events (SSE) streaming for real-time frontend updates.
//
// ==============================================================================
// ARCHITECTURAL ROLE & SSE STREAMING PROTOCOL:
// Live Activity Feed & SSE Hub (LOG-4, spec §7.3).
//
// What is Server-Sent Events (SSE)?
// SSE (HTML5 `EventSource`) is a lightweight, one-way HTTP streaming protocol where
// the server pushes textual events to the browser over a single persistent HTTP connection.
//
// GO CONCEPTS & JAVA / SPRING SSEMITTER COMPARISON:
//
//  1. `http.Flusher` Interface:
//     In Go, `http.ResponseWriter` buffers data internally for network efficiency.
//     For real-time streaming, we type-assert `flusher, ok := w.(http.Flusher)` and call
//     `flusher.Flush()` immediately after writing each message, forcing the bytes out
//     over the TCP socket. (In Spring MVC, this is handled by `SseEmitter.send()`).
//
//  2. Client Disconnect Detection (`r.Context().Done()`):
//     When the browser tab closes, Go automatically cancels `r.Context()`.
//     The handler's `select` loop detects `<-r.Context().Done()` and returns cleanly,
//     preventing memory leaks or wasted CPU cycles.
//
//  3. Heartbeat Comments (`: heartbeat\n\n`):
//     Under the SSE specification, lines starting with a colon `:` are comments.
//     Sending a heartbeat comment every 15 seconds prevents intermediate proxies, NAT
//     gateways, and load balancers from closing idle connections.
//
// ==============================================================================
package server

import (
	"fmt"
	"net/http"
	"strconv"
	"time"
)

// handleEventsSSE streams global events via Server-Sent Events (LOG-4, spec §7.3).
func (s *Server) handleEventsSSE(w http.ResponseWriter, r *http.Request) {
	// Type-assert ResponseWriter to Flusher for chunked streaming
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming unsupported", http.StatusInternalServerError)
		return
	}

	// Set mandatory SSE headers
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	flusher.Flush() // Flush initial 200 OK headers immediately

	// Parse Last-Event-ID header (for reconnection) or ?since= query parameter
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

	// 1. Backfill: Send all missed events since Last-Event-ID
	events, err := s.DB.Events().ListEventsSince(r.Context(), sinceID, 200)
	if err == nil {
		for _, e := range events {
			// SSE Protocol Format: id, event, data, double newline
			fmt.Fprintf(w, "id: %d\nevent: %s\ndata: %s\n\n", e.ID, e.Type, e.Payload)
			if e.ID > sinceID {
				sinceID = e.ID
			}
		}
		flusher.Flush()
	}

	// Tickers for polling new events (1s) and sending keep-alive heartbeats (15s)
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	heartbeatTicker := time.NewTicker(15 * time.Second)
	defer heartbeatTicker.Stop()

	// 2. Continuous Event Loop
	for {
		select {
		case <-r.Context().Done():
			// Browser closed tab or navigated away: exit loop and free goroutine
			return

		case <-heartbeatTicker.C:
			// Send comment heartbeat line (spec §7.3)
			fmt.Fprintf(w, ": heartbeat\n\n")
			flusher.Flush()

		case <-ticker.C:
			// Poll for newly created events in database
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
