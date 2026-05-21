// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package utils

import "testing"

func TestServiceEndpoint(t *testing.T) {
	tests := []struct {
		name     string
		services map[string]any
		key      string
		want     string
	}{
		{
			name:     "nil services",
			services: nil,
			key:      "Tracking",
			want:     "",
		},
		{
			name:     "missing key",
			services: map[string]any{"Studio": map[string]any{"endpoint": "https://s"}},
			key:      "Tracking",
			want:     "",
		},
		{
			name:     "service is not a map",
			services: map[string]any{"Tracking": "https://t"},
			key:      "Tracking",
			want:     "",
		},
		{
			name:     "missing endpoint key",
			services: map[string]any{"Tracking": map[string]any{"other": "value"}},
			key:      "Tracking",
			want:     "",
		},
		{
			name:     "endpoint is non-string",
			services: map[string]any{"Tracking": map[string]any{"endpoint": 42}},
			key:      "Tracking",
			want:     "",
		},
		{
			name: "valid Studio endpoint",
			services: map[string]any{
				"Studio": map[string]any{
					"endpoint": "https://ml.azure.com/runs/my-job?wsid=/sub/rg/ws",
				},
			},
			key:  "Studio",
			want: "https://ml.azure.com/runs/my-job?wsid=/sub/rg/ws",
		},
		{
			name: "valid Tracking endpoint",
			services: map[string]any{
				"Tracking": map[string]any{
					"endpoint": "azureml://eastus.api.azureml.ms/mlflow/v1.0/sub/rg/ws",
				},
			},
			key:  "Tracking",
			want: "azureml://eastus.api.azureml.ms/mlflow/v1.0/sub/rg/ws",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ServiceEndpoint(tc.services, tc.key)
			if got != tc.want {
				t.Fatalf("ServiceEndpoint(%q) = %q, want %q", tc.key, got, tc.want)
			}
		})
	}
}
