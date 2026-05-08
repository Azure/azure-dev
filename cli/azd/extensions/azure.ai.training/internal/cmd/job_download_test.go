// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"testing"

	"azure.ai.training/pkg/models"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSelectDownloadMode(t *testing.T) {
	tests := []struct {
		name        string
		outputName  string
		all         bool
		wantNamed   bool
		wantAll     bool
		wantDefault bool
	}{
		{
			name:        "no flags -> default only",
			wantDefault: true,
		},
		{
			name:        "output-name=default treated as no flag -> default only",
			outputName:  "default",
			wantDefault: true,
		},
		{
			name:        "empty output-name -> default only",
			outputName:  "",
			wantDefault: true,
		},
		{
			name:       "named output -> named only",
			outputName: "my-output",
			wantNamed:  true,
		},
		{
			name:    "all -> every named + default",
			all:     true,
			wantAll: true,
		},
		// NB: cobra's MarkFlagsMutuallyExclusive prevents (all && outputName) at
		// the CLI layer, so we don't test that combination here.
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			named, all, def := selectDownloadMode(tt.outputName, tt.all)
			assert.Equal(t, tt.wantNamed, named, "wantNamed")
			assert.Equal(t, tt.wantAll, all, "wantAll")
			assert.Equal(t, tt.wantDefault, def, "wantDefault")
		})
	}
}

func TestExtractTrackingEndpoint(t *testing.T) {
	const wantEndpoint = "https://eastus.api.azureml.ms"

	tests := []struct {
		name      string
		job       *models.JobResource
		want      string
		wantErr   bool
		errSubstr string
	}{
		{
			name:      "nil job",
			job:       nil,
			wantErr:   true,
			errSubstr: "job is nil",
		},
		{
			name: "missing services",
			job: &models.JobResource{
				Properties: models.CommandJob{},
			},
			wantErr:   true,
			errSubstr: "missing properties.services.Tracking",
		},
		{
			name: "tracking entry missing",
			job: &models.JobResource{
				Properties: models.CommandJob{
					Services: map[string]any{"Other": map[string]any{"endpoint": "x"}},
				},
			},
			wantErr:   true,
			errSubstr: "missing properties.services.Tracking",
		},
		{
			name: "tracking has wrong shape (string instead of map)",
			job: &models.JobResource{
				Properties: models.CommandJob{
					Services: map[string]any{"Tracking": "not-a-map"},
				},
			},
			wantErr:   true,
			errSubstr: "unexpected shape",
		},
		{
			name: "endpoint missing inside tracking",
			job: &models.JobResource{
				Properties: models.CommandJob{
					Services: map[string]any{"Tracking": map[string]any{"protocol": "https"}},
				},
			},
			wantErr:   true,
			errSubstr: "endpoint missing",
		},
		{
			name: "endpoint is empty string",
			job: &models.JobResource{
				Properties: models.CommandJob{
					Services: map[string]any{"Tracking": map[string]any{"endpoint": ""}},
				},
			},
			wantErr:   true,
			errSubstr: "endpoint missing",
		},
		{
			name: "endpoint is non-string type",
			job: &models.JobResource{
				Properties: models.CommandJob{
					Services: map[string]any{"Tracking": map[string]any{"endpoint": 123}},
				},
			},
			wantErr:   true,
			errSubstr: "endpoint missing",
		},
		{
			name: "valid endpoint",
			job: &models.JobResource{
				Properties: models.CommandJob{
					Services: map[string]any{"Tracking": map[string]any{"endpoint": wantEndpoint}},
				},
			},
			want: wantEndpoint,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := extractTrackingEndpoint(tt.job)
			if tt.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errSubstr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}
