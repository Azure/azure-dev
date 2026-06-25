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
		name        string
		services    map[string]any
		wantContain bool
	}{
		{
			name: "Studio service present prints Foundry Portal Uri",
			services: map[string]any{
				"Studio": map[string]any{
					"endpoint":       portal,
					"jobServiceType": "Studio",
					"status":         "Running",
				},
			},
			wantContain: true,
		},
		{
			name:        "no services map omits line",
			services:    nil,
			wantContain: false,
		},
		{
			name: "services without Studio entry omits line",
			services: map[string]any{
				"Tracking": map[string]any{"endpoint": "https://tracking/x"},
			},
			wantContain: false,
		},
		{
			name: "Studio entry with empty endpoint omits line",
			services: map[string]any{
				"Studio": map[string]any{"endpoint": ""},
			},
			wantContain: false,
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

			hasLine := strings.Contains(out, "Foundry Portal Uri:") &&
				strings.Contains(out, portal)
			if tc.wantContain && !hasLine {
				t.Errorf("expected Foundry Portal Uri line with %q, got:\n%s", portal, out)
			}
			if !tc.wantContain && strings.Contains(out, "Foundry Portal Uri:") {
				t.Errorf("did not expect Foundry Portal Uri line, got:\n%s", out)
			}
		})
	}
}
