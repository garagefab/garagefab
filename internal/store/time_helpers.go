// Package store provides SQLite time serialization and deserialization helpers.
//
// ==============================================================================
// SQLITE TIMESTAMP STORAGE & ISO 8601 IN GO:
//
//  1. Why Strings for SQLite Timestamps?
//     SQLite does not have a dedicated `DATETIME` storage class (it only has NULL,
//     INTEGER, REAL, TEXT, and BLOB).
//     By storing dates as UTC ISO 8601 / RFC 3339 strings (`YYYY-MM-DDTHH:MM:SS.NNNNNNNNNZ`),
//     SQLite can:
//     - Sort dates accurately using standard alphabetical index ordering (`ORDER BY created_at DESC`).
//     - Filter ranges using string comparisons (`WHERE created_at > ?`).
//     - Use built-in SQLite date/time functions (`strftime`, `datetime`).
//
//  2. Go's Time Formatting Reference Time:
//     Unlike Java (`yyyy-MM-dd HH:mm:ss`), Go uses a unique mnemonic reference time:
//     "Mon Jan 2 15:04:05 MST 2006" (Unix numbers 1, 2, 3, 4, 5, 6, 7).
//     `time.RFC3339Nano` is predefined as "2006-01-02T15:04:05.999999999Z07:00".
//
// ==============================================================================
package store

import "time"

// formatTime converts a Go time.Time object to a normalized UTC RFC3339Nano string.
// If the time is zero (unset), it returns an empty string.
func formatTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339Nano)
}

// parseTime attempts to parse an SQLite date string into a Go time.Time object.
// It supports RFC3339Nano, standard RFC3339, and classic SQL datetime formats.
func parseTime(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	// Try RFC3339Nano first (sub-second precision)
	if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return t
	}
	// Fallback to standard RFC3339 (second precision)
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t
	}
	// Fallback to classic SQL "YYYY-MM-DD HH:MM:SS"
	if t, err := time.Parse("2006-01-02 15:04:05", s); err == nil {
		return t
	}
	return time.Time{}
}
