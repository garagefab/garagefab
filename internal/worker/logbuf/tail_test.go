package logbuf

import (
	"strings"
	"testing"
)

// TestTailBuffer_BoundsAndKeepsTail verifies the buffer never exceeds its cap and always
// retains the most recent lines (NFR-5).
func TestTailBuffer_BoundsAndKeepsTail(t *testing.T) {
	tb := NewTailBuffer(32)
	for i := 0; i < 100; i++ {
		tb.WriteLine("0123456789")
	}
	got := tb.String()
	if len(got) > 32 {
		t.Fatalf("buffer exceeded cap: %d bytes", len(got))
	}
	if !strings.HasSuffix(got, "0123456789\n") {
		t.Fatalf("expected the most recent line to be retained, got %q", got)
	}
}

// TestTailBuffer_LineLargerThanMax verifies a single oversized line is trimmed to its tail.
func TestTailBuffer_LineLargerThanMax(t *testing.T) {
	tb := NewTailBuffer(10)
	tb.WriteLine("abcdefghijklmnop") // written as 17 bytes including "\n"
	got := tb.String()
	if got != "hijklmnop\n" {
		t.Fatalf("expected the last 10 bytes, got %q", got)
	}
}

// TestTailBuffer_Empty verifies a fresh buffer is empty.
func TestTailBuffer_Empty(t *testing.T) {
	if got := NewTailBuffer(8).String(); got != "" {
		t.Fatalf("expected empty buffer, got %q", got)
	}
}
