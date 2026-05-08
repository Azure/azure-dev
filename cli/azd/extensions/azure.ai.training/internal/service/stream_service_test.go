// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package service

import (
	"context"
	"testing"
	"time"
)

func TestPollInterval_SigmoidBoundaries(t *testing.T) {
	tests := []struct {
		name        string
		elapsedSec  float64
		wantMinSec  float64
		wantMaxSec  float64
		description string
	}{
		{
			name:        "start returns minimum",
			elapsedSec:  0,
			wantMinSec:  2.0,
			wantMaxSec:  2.0,
			description: "at t=0 the sigmoid value is ~0.59s, clamped to 2s minimum",
		},
		{
			name:        "10 seconds still near minimum",
			elapsedSec:  10,
			wantMinSec:  2.0,
			wantMaxSec:  3.0,
			description: "early in the curve, interval stays low",
		},
		{
			name:        "60 seconds mid range",
			elapsedSec:  60,
			wantMinSec:  5.0,
			wantMaxSec:  15.0,
			description: "at 60s the sigmoid is in the transition zone",
		},
		{
			name:        "120 seconds approaching max",
			elapsedSec:  120,
			wantMinSec:  45.0,
			wantMaxSec:  60.0,
			description: "at 120s the sigmoid approaches the 60s asymptote",
		},
		{
			name:        "300 seconds at max",
			elapsedSec:  300,
			wantMinSec:  59.0,
			wantMaxSec:  60.0,
			description: "well past the inflection, saturated at max",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			startTime := time.Now().Add(-time.Duration(tt.elapsedSec * float64(time.Second)))
			got := pollInterval(startTime)
			gotSec := got.Seconds()
			if gotSec < tt.wantMinSec || gotSec > tt.wantMaxSec {
				t.Errorf("pollInterval(elapsed=%vs) = %vs, want [%v, %v]s",
					tt.elapsedSec, gotSec, tt.wantMinSec, tt.wantMaxSec)
			}
		})
	}
}

func TestPollInterval_NeverBelowMinimum(t *testing.T) {
	for elapsed := 0; elapsed <= 300; elapsed += 5 {
		startTime := time.Now().Add(-time.Duration(elapsed) * time.Second)
		got := pollInterval(startTime)
		if got < 2*time.Second {
			t.Errorf("pollInterval(elapsed=%ds) = %v, below 2s minimum", elapsed, got)
		}
		if got > 60*time.Second {
			t.Errorf("pollInterval(elapsed=%ds) = %v, above 60s maximum", elapsed, got)
		}
	}
}

func TestSleepWithContext_CancelledImmediately(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel before sleeping

	start := time.Now()
	err := sleepWithContext(ctx, 10*time.Second)
	elapsed := time.Since(start)

	if err == nil {
		t.Error("expected error from cancelled context, got nil")
	}
	if elapsed > 500*time.Millisecond {
		t.Errorf("sleepWithContext took %v, should have returned immediately", elapsed)
	}
}

func TestSleepWithContext_CompletesNormally(t *testing.T) {
	ctx := context.Background()
	start := time.Now()
	err := sleepWithContext(ctx, 50*time.Millisecond)
	elapsed := time.Since(start)

	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if elapsed < 50*time.Millisecond {
		t.Errorf("sleepWithContext returned too early: %v", elapsed)
	}
}

func TestFilterLogFiles_PrimaryPattern(t *testing.T) {
	tests := []struct {
		name     string
		logFiles map[string]string
		want     []string
	}{
		{
			name:     "empty map returns nil",
			logFiles: map[string]string{},
			want:     nil,
		},
		{
			name: "matches single user_logs std_log",
			logFiles: map[string]string{
				"user_logs/std_log.txt": "https://blob.core/std_log.txt?sas",
			},
			want: []string{"user_logs/std_log.txt"},
		},
		{
			name: "matches std_log_ps variant",
			logFiles: map[string]string{
				"user_logs/std_log_ps.txt": "https://blob.core/std_log_ps.txt?sas",
			},
			want: []string{"user_logs/std_log_ps.txt"},
		},
		{
			name: "excludes activity logs and system files",
			logFiles: map[string]string{
				"user_logs/std_log.txt":                         "https://blob.core/std_log.txt?sas",
				"system_logs/virtualcluster_activity_log.txt":   "https://blob.core/activity.txt?sas",
				"system_logs/cs_capability/cs-capability-0.txt": "https://blob.core/cs.txt?sas",
			},
			want: []string{"user_logs/std_log.txt"},
		},
		{
			name: "multiple user_logs sorted alphabetically",
			logFiles: map[string]string{
				"user_logs/std_log.txt":    "https://blob.core/std_log.txt?sas",
				"user_logs/std_log_ps.txt": "https://blob.core/std_log_ps.txt?sas",
			},
			want: []string{"user_logs/std_log.txt", "user_logs/std_log_ps.txt"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := filterLogFiles(tt.logFiles)
			if !slicesEqual(got, tt.want) {
				t.Errorf("filterLogFiles() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestFilterLogFiles_FallbackToLegacy(t *testing.T) {
	tests := []struct {
		name     string
		logFiles map[string]string
		want     []string
	}{
		{
			name: "falls back to azureml-logs when no user_logs match",
			logFiles: map[string]string{
				"azureml-logs/70_driver_log.txt":  "https://blob.core/70.txt?sas",
				"azureml-logs/55_azureml-log.txt": "https://blob.core/55.txt?sas",
			},
			want: []string{"azureml-logs/55_azureml-log.txt", "azureml-logs/70_driver_log.txt"},
		},
		{
			name: "prefers user_logs over azureml-logs when both exist",
			logFiles: map[string]string{
				"user_logs/std_log.txt":          "https://blob.core/std_log.txt?sas",
				"azureml-logs/70_driver_log.txt": "https://blob.core/70.txt?sas",
			},
			want: []string{"user_logs/std_log.txt"},
		},
		{
			name: "no matching files returns nil",
			logFiles: map[string]string{
				"system_logs/something.txt": "https://blob.core/sys.txt?sas",
				"random_file.log":           "https://blob.core/random.txt?sas",
			},
			want: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := filterLogFiles(tt.logFiles)
			if !slicesEqual(got, tt.want) {
				t.Errorf("filterLogFiles() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestExtractServiceEndpoint(t *testing.T) {
	tests := []struct {
		name        string
		services    map[string]any
		serviceName string
		want        string
	}{
		{
			name:        "nil services",
			services:    nil,
			serviceName: "Studio",
			want:        "",
		},
		{
			name:        "missing service",
			services:    map[string]any{"Tracking": map[string]any{"endpoint": "https://tracking"}},
			serviceName: "Studio",
			want:        "",
		},
		{
			name:        "service not a map",
			services:    map[string]any{"Studio": "not-a-map"},
			serviceName: "Studio",
			want:        "",
		},
		{
			name:        "missing endpoint key",
			services:    map[string]any{"Studio": map[string]any{"other": "value"}},
			serviceName: "Studio",
			want:        "",
		},
		{
			name:        "endpoint not a string",
			services:    map[string]any{"Studio": map[string]any{"endpoint": 42}},
			serviceName: "Studio",
			want:        "",
		},
		{
			name: "valid Studio endpoint",
			services: map[string]any{
				"Studio": map[string]any{
					"endpoint": "https://ml.azure.com/runs/my-job?wsid=/sub/rg/ws",
				},
			},
			serviceName: "Studio",
			want:        "https://ml.azure.com/runs/my-job?wsid=/sub/rg/ws",
		},
		{
			name: "valid Tracking endpoint",
			services: map[string]any{
				"Tracking": map[string]any{
					"endpoint": "azureml://eastus.api.azureml.ms/mlflow/v1.0/sub/rg/ws",
				},
			},
			serviceName: "Tracking",
			want:        "azureml://eastus.api.azureml.ms/mlflow/v1.0/sub/rg/ws",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractServiceEndpoint(tt.services, tt.serviceName)
			if got != tt.want {
				t.Errorf("extractServiceEndpoint(%q) = %q, want %q", tt.serviceName, got, tt.want)
			}
		})
	}
}

func TestTerminalAndActiveStates(t *testing.T) {
	// Verify terminal states don't overlap with active states
	for state := range terminalStates {
		if activeStates[state] {
			t.Errorf("state %q is both terminal and active", state)
		}
	}

	// Verify expected terminal states
	expectedTerminal := []string{"Completed", "Failed", "Canceled", "NotResponding", "Paused"}
	for _, s := range expectedTerminal {
		if !terminalStates[s] {
			t.Errorf("expected %q to be a terminal state", s)
		}
	}

	// Verify expected active states
	expectedActive := []string{"NotStarted", "Queued", "Preparing", "Provisioning", "Starting", "Running", "Finalizing"}
	for _, s := range expectedActive {
		if !activeStates[s] {
			t.Errorf("expected %q to be an active state", s)
		}
	}
}

// slicesEqual compares two string slices including nil vs empty.
func slicesEqual(a, b []string) bool {
	if len(a) == 0 && len(b) == 0 {
		// Treat nil and empty as equal
		return (a == nil) == (b == nil) || (len(a) == 0 && len(b) == 0)
	}
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
