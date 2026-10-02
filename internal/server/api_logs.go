// Package server implements HTTP routing, middleware, and REST API handlers.
//
// ==============================================================================
// ARCHITECTURAL ROLE & LOG STREAMING PROTOCOL:
// On-Demand Step Log Retrieval & Real-Time SSE Streaming (LOG-5, UI-7, spec §7.2, §7.3).
//
// Role:
//   - LOG-5: Step logs are never served by default; fetched strictly on demand when requested.
//   - UI-7: If a step is running, new log lines stream within 1 second via Server-Sent Events.
//     If a step is finished, the stored log file is served.
//
// Protocol (spec §7.3):
// - Event: `log` with JSON payload `{"line": N, "ts": "...", "text": "..."}`
// - Event: `end` with JSON payload `{"result": "success"}`
//
// ==============================================================================
package server

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/garagefab/garagefab/internal/store"
)

// handleGetStepLog handles GET /api/jobs/{id}/steps/{stepId}/log (LOG-5, UI-7, spec §7.2).
func (s *Server) handleGetStepLog(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	jobID, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Error(w, "Invalid job ID", http.StatusBadRequest)
		return
	}

	stepIDStr := chi.URLParam(r, "stepId")
	stepID, err := strconv.ParseInt(stepIDStr, 10, 64)
	if err != nil {
		http.Error(w, "Invalid step ID", http.StatusBadRequest)
		return
	}

	step, err := s.DB.StepRuns().GetStepRun(r.Context(), stepID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			http.Error(w, "Step run not found", http.StatusNotFound)
			return
		}
		http.Error(w, "Failed to retrieve step run: "+err.Error(), http.StatusInternalServerError)
		return
	}

	if step.JobID != jobID {
		http.Error(w, "Step does not belong to job", http.StatusNotFound)
		return
	}

	// Check if caller requests real-time SSE stream (UI-7, spec §7.3)
	if strings.Contains(r.Header.Get("Accept"), "text/event-stream") {
		s.streamStepLogSSE(w, r, step)
		return
	}

	// Otherwise, serve plain text logs with optional ?offset= query param
	s.serveStepLogPlain(w, r, step)
}

// serveStepLogPlain returns plain text logs supporting byte offset query param.
func (s *Server) serveStepLogPlain(w http.ResponseWriter, r *http.Request, step *store.StepRun) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")

	if step.LogPath == "" {
		w.WriteHeader(http.StatusOK)
		return
	}

	f, err := os.Open(step.LogPath)
	if err != nil {
		if os.IsNotExist(err) {
			w.WriteHeader(http.StatusOK)
			return
		}
		http.Error(w, "Failed to open log file: "+err.Error(), http.StatusInternalServerError)
		return
	}
	defer f.Close()

	// Parse optional ?offset= query parameter (byte offset)
	if offsetStr := r.URL.Query().Get("offset"); offsetStr != "" {
		if offset, err := strconv.ParseInt(offsetStr, 10, 64); err == nil && offset > 0 {
			_, _ = f.Seek(offset, io.SeekStart)
		}
	}

	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, f)
}

// streamStepLogSSE streams step logs in real time via Server-Sent Events (LOG-5, UI-7, spec §7.3).
func (s *Server) streamStepLogSSE(w http.ResponseWriter, r *http.Request, step *store.StepRun) {
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

	lineNum := 0
	var filePos int64 = 0

	// Helper to read new lines from file
	readNewLines := func() bool {
		if step.LogPath == "" {
			return false
		}
		f, err := os.Open(step.LogPath)
		if err != nil {
			return false
		}
		defer f.Close()

		if filePos > 0 {
			if _, err := f.Seek(filePos, io.SeekStart); err != nil {
				return false
			}
		}

		scanner := bufio.NewScanner(f)
		hasLines := false
		for scanner.Scan() {
			hasLines = true
			lineNum++
			lineText := scanner.Text()

			payload, _ := json.Marshal(map[string]any{
				"line": lineNum,
				"ts":   time.Now().UTC().Format(time.RFC3339),
				"text": lineText,
			})
			fmt.Fprintf(w, "id: %d\nevent: log\ndata: %s\n\n", lineNum, string(payload))
		}

		newPos, err := f.Seek(0, io.SeekCurrent)
		if err == nil {
			filePos = newPos
		}

		if hasLines {
			flusher.Flush()
		}
		return hasLines
	}

	// 1. Initial read of existing log content
	readNewLines()

	// If step already completed or failed, close stream with end event
	if step.Status != store.StepStatusRunning {
		fmt.Fprintf(w, "event: end\ndata: {\"result\":\"%s\"}\n\n", step.Status)
		flusher.Flush()
		return
	}

	// 2. Step is running: poll file and database status periodically (UI-7: updates within 1s)
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	statusTicker := time.NewTicker(1 * time.Second)
	defer statusTicker.Stop()

	for {
		select {
		case <-r.Context().Done():
			return

		case <-ticker.C:
			readNewLines()

		case <-statusTicker.C:
			currentStep, err := s.DB.StepRuns().GetStepRun(r.Context(), step.ID)
			if err == nil && currentStep != nil && currentStep.Status != store.StepStatusRunning {
				// Flush any final lines
				readNewLines()
				fmt.Fprintf(w, "event: end\ndata: {\"result\":\"%s\"}\n\n", currentStep.Status)
				flusher.Flush()
				return
			}
		}
	}
}
