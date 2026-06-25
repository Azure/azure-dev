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
// everything fn wrote. Used because printJobDetails writes directly to stdout.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	orig := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdout = w

	done := make(chan string, 1)
	go func() {
		b, _ := io.ReadAll(r)
		done <- string(b)
	}()

	fn()

	_ = w.Close()
	os.Stdout = orig
	return <-done
}

func TestPrintJobDetails_FoundryPortalUri(t *testing.T) {
	const portal = "https://ai.azure.com/build/jobs/test-job"

	tests := []struct {
		name     string
		services map[string]any
		wantURL  string // expected value after "Foundry Portal Uri:"
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

			if !strings.Contains(out, "Foundry Portal Uri:") {
				t.Fatalf("expected Foundry Portal Uri line to always be present, got:\n%s", out)
			}
			var got string
			for _, line := range strings.Split(out, "\n") {
				if i := strings.Index(line, "Foundry Portal Uri:"); i >= 0 {
					got = strings.TrimSpace(line[i+len("Foundry Portal Uri:"):])
					break
				}
			}
			if got != tc.wantURL {
				t.Errorf("Foundry Portal Uri value = %q, want %q\nfull output:\n%s", got, tc.wantURL, out)
			}
		})
	}
}
