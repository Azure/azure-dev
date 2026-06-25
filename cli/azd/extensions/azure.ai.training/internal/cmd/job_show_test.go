// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"io"
	"os"
	"strings"
	"testing"

	"azure.ai.training/pkg/models"
)

// captureStdout runs fn while redirecting os.Stdout to a pipe, then returns
// everything fn wrote. A goroutine drains the pipe into a strings.Builder so
// large writes can't deadlock on the OS pipe buffer. os.Stdout is restored
// immediately after fn returns so any subsequent writes in the same test
// (e.g. debug logging, a second captureStdout call) behave normally; t.Cleanup
// remains as a panic safety net.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	orig := os.Stdout
	os.Stdout = w

	// Drain in a goroutine so large output can't deadlock on a full pipe buffer.
	var sb strings.Builder
	copyDone := make(chan error, 1)
	go func() {
		_, copyErr := io.Copy(&sb, r)
		_ = r.Close()
		copyDone <- copyErr
	}()

	// Panic safety net only; the happy path restores stdout below.
	t.Cleanup(func() {
		os.Stdout = orig
		_ = w.Close()
	})

	func() {
		defer func() { _ = w.Close() }()
		fn()
	}()
	os.Stdout = orig

	if err := <-copyDone; err != nil {
		t.Fatalf("capture stdout: %v", err)
	}
	return sb.String()
}

func TestPrintJobDetails_FoundryPortalURI(t *testing.T) {
	const portal = "https://ai.azure.com/build/jobs/test-job"

	tests := []struct {
		name     string
		services map[string]any
		wantURL  string // expected value after "Foundry Portal URI:"
	}{
		{
			name: "Studio service present prints URL",
			services: map[string]any{
				"Studio": map[string]any{
					"endpoint":       portal,
					"jobServiceType": "Studio",
					"status":         "Running",
				},
			},
			wantURL: portal,
		},
		{
			name:     "no services map prints dash",
			services: nil,
			wantURL:  "-",
		},
		{
			name: "services without Studio entry prints dash",
			services: map[string]any{
				"Tracking": map[string]any{"endpoint": "https://tracking/x"},
			},
			wantURL: "-",
		},
		{
			name: "Studio entry with empty endpoint prints dash",
			services: map[string]any{
				"Studio": map[string]any{"endpoint": ""},
			},
			wantURL: "-",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			d := &jobDetails{
				Job: &models.JobResource{
					Name: "test-job",
					Properties: models.CommandJob{
						JobType:     "Command",
						DisplayName: "Test Job",
						Description: "unit-test job",
						Status:      "Completed",
						Services:    tc.services,
					},
				},
				Metrics: map[string]*models.MetricsFullResponse{},
			}

			out := captureStdout(t, func() { printJobDetails(d) })

			if !strings.Contains(out, "Foundry Portal URI:") {
				t.Fatalf("expected Foundry Portal URI line to always be present, got:\n%s", out)
			}
			var got string
			for line := range strings.SplitSeq(out, "\n") {
				if _, after, ok := strings.Cut(line, "Foundry Portal URI:"); ok {
					got = strings.TrimSpace(after)
					break
				}
			}
			if got != tc.wantURL {
				t.Errorf("Foundry Portal URI value = %q, want %q\nfull output:\n%s", got, tc.wantURL, out)
			}
		})
	}
}
