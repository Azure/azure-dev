// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package client

import (
	"testing"
)

func TestParseTrackingEndpoint(t *testing.T) {
	tests := []struct {
		name         string
		endpoint     string
		wantBaseURL  string
		wantWorkPath string
		wantErr      bool
	}{
		{
			name:         "standard v1.0 endpoint",
			endpoint:     "azureml://eastus.api.azureml.ms/mlflow/v1.0/subscriptions/sub-id/resourceGroups/rg/providers/Microsoft.MachineLearningServices/workspaces/ws",
			wantBaseURL:  "https://eastus.api.azureml.ms",
			wantWorkPath: "/subscriptions/sub-id/resourceGroups/rg/providers/Microsoft.MachineLearningServices/workspaces/ws",
		},
		{
			name:         "v2.0 endpoint",
			endpoint:     "azureml://westus2.api.azureml.ms/mlflow/v2.0/subscriptions/sub-id/resourceGroups/rg/providers/Microsoft.MachineLearningServices/workspaces/ws",
			wantBaseURL:  "https://westus2.api.azureml.ms",
			wantWorkPath: "/subscriptions/sub-id/resourceGroups/rg/providers/Microsoft.MachineLearningServices/workspaces/ws",
		},
		{
			name:         "endpoint with query params stripped",
			endpoint:     "azureml://eastus.api.azureml.ms/mlflow/v1.0/subscriptions/sub/resourceGroups/rg/providers/Microsoft.MachineLearningServices/workspaces/ws?extra=param",
			wantBaseURL:  "https://eastus.api.azureml.ms",
			wantWorkPath: "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.MachineLearningServices/workspaces/ws",
		},
		{
			name:     "missing mlflow prefix",
			endpoint: "azureml://eastus.api.azureml.ms/other/v1.0/subscriptions/sub/resourceGroups/rg",
			wantErr:  true,
		},
		{
			name:     "empty string",
			endpoint: "",
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			baseURL, workPath, err := parseTrackingEndpoint(tt.endpoint)
			if tt.wantErr {
				if err == nil {
					t.Errorf("parseTrackingEndpoint(%q) expected error, got baseURL=%q workPath=%q", tt.endpoint, baseURL, workPath)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseTrackingEndpoint(%q) unexpected error: %v", tt.endpoint, err)
			}
			if baseURL != tt.wantBaseURL {
				t.Errorf("baseURL = %q, want %q", baseURL, tt.wantBaseURL)
			}
			if workPath != tt.wantWorkPath {
				t.Errorf("workPath = %q, want %q", workPath, tt.wantWorkPath)
			}
		})
	}
}
