// Package factory_test contains unit tests for the probe.json validator (PRB-1, spec §6.3).
package factory_test

import (
	"errors"
	"testing"

	"github.com/garagefab/garagefab/internal/factory"
)

func TestValidateProbeJSON_PR01(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr error
	}{
		{
			name:  "valid",
			input: `{"schema_version":1,"command":"go test ./...","files":["a_test.go"],"description":"repro"}`,
		},
		{
			name:    "empty content",
			input:   "",
			wantErr: nil, // handled as a generic error, asserted below by non-nil
		},
		{
			name:    "bad json",
			input:   `{"schema_version":`,
			wantErr: factory.ErrProbeBadJSON,
		},
		{
			name:    "wrong version",
			input:   `{"schema_version":2,"command":"go test ./...","files":["a_test.go"]}`,
			wantErr: factory.ErrProbeInvalidVersion,
		},
		{
			name:    "empty command",
			input:   `{"schema_version":1,"command":"  ","files":["a_test.go"]}`,
			wantErr: factory.ErrProbeEmptyCommand,
		},
		{
			name:    "empty files",
			input:   `{"schema_version":1,"command":"go test ./...","files":[]}`,
			wantErr: factory.ErrProbeEmptyFiles,
		},
		{
			name:    "absolute path",
			input:   `{"schema_version":1,"command":"go test ./...","files":["/etc/passwd"]}`,
			wantErr: factory.ErrProbeInvalidPath,
		},
		{
			name:    "parent traversal",
			input:   `{"schema_version":1,"command":"go test ./...","files":["../secret.go"]}`,
			wantErr: factory.ErrProbeInvalidPath,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			report, err := factory.ValidateProbeJSON([]byte(tc.input))
			if tc.name == "valid" {
				if err != nil {
					t.Fatalf("expected valid probe, got error: %v", err)
				}
				if report == nil || report.Command != "go test ./..." {
					t.Fatalf("unexpected report: %+v", report)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected an error, got nil")
			}
			if tc.wantErr != nil && !errors.Is(err, tc.wantErr) {
				t.Fatalf("expected error %v, got %v", tc.wantErr, err)
			}
		})
	}
}
