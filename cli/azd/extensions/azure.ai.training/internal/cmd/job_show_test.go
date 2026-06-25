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
// everything fn wrote. The write end is closed (so the reader unblocks) and
// the original stdout is restored even if fn panics, via t.Cleanup, so the
// helper is safe to use in table-driven tests.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	orig := os.Stdout
	os.Stdout = w
	t.Cleanup(func() {
		os.Stdout = orig
		_ = r.Close()
	})

	done := make(chan string, 1)
	go func() {
		b, _ := io.ReadAll(r)
		done <- string(b)
	}()

	func() {
		defer func() { _ = w.Close() }()
		fn()
	}()

	return <-done
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
