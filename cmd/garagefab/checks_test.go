package main

import (
	"testing"
)

func TestParseGitVersion(t *testing.T) {
	tests := []struct {
		output string
		valid  bool
	}{
		{"git version 2.30.0", true},
		{"git version 2.34.1 (Apple Git-147)", true},
		{"git version 2.54.0", true},
		{"git version 3.0.0", true},
		{"git version 2.29.9", false},
		{"git version 1.9.5", false},
		{"invalid output", false},
	}

	for _, tt := range tests {
		err := validateGitVersionOutput(tt.output)
		if (err == nil) != tt.valid {
			t.Errorf("validateGitVersionOutput(%q): expected valid=%v, got err=%v", tt.output, tt.valid, err)
		}
	}
}
