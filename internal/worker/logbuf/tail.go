// Package logbuf provides a bounded, thread-safe tail buffer for streaming process output.
//
// ==============================================================================
// ARCHITECTURAL ROLE & PATTERNS:
// Infrastructure Utility — Bounded Output Buffering (NFR-5).
//
// The agent and command runners stream potentially unbounded output to a log file. They must not
// retain that output in memory, or a chatty process could exhaust the daemon's RSS. TailBuffer
// keeps only the most recent N bytes, so error summaries stay useful while memory stays bounded.
//
// JAVA / ENTERPRISE COMPARISON:
// Analogous to a Guava `EvictingQueue` or a fixed-size ring buffer used for backpressure while
// draining an InputStream, without materializing the whole stream.
//
// GO CONCEPTS:
//   - A `sync.Mutex` guards the byte slice because the buffer is written by two pipe-reader
//     goroutines (stdout/stderr) and read after they join.
//
// ==============================================================================
package logbuf

import "sync"

// DefaultMaxBytes is the default bound for a TailBuffer (64 KiB), matching the agent stream path.
const DefaultMaxBytes = 64 * 1024

// TailBuffer is a thread-safe bounded FIFO buffer that retains only the most recent bytes.
type TailBuffer struct {
	mu   sync.Mutex
	data []byte
	max  int
}

// NewTailBuffer creates a TailBuffer capped at max bytes.
func NewTailBuffer(max int) *TailBuffer {
	return &TailBuffer{max: max}
}

// WriteLine appends a line to the buffer, evicting the oldest bytes when the cap is exceeded.
func (t *TailBuffer) WriteLine(line string) {
	t.mu.Lock()
	defer t.mu.Unlock()

	b := []byte(line + "\n")
	if len(b) >= t.max {
		t.data = append([]byte(nil), b[len(b)-t.max:]...)
		return
	}
	t.data = append(t.data, b...)
	if len(t.data) > t.max {
		overflow := len(t.data) - t.max
		newData := make([]byte, t.max)
		copy(newData, t.data[overflow:])
		t.data = newData
	}
}

// String returns the buffered tail as a string.
func (t *TailBuffer) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return string(t.data)
}
