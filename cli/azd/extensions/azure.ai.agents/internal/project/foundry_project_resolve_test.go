// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import "testing"

func TestParseFoundryEndpoint(t *testing.T) {
	tests := []struct {
		name        string
		endpoint    string
		wantAccount string
		wantProject string
		wantErr     bool
	}{
		{
			name:        "standard endpoint",
			endpoint:    "https://my-account.services.ai.azure.com/api/projects/my-project",
			wantAccount: "my-account",
			wantProject: "my-project",
		},
		{
			name:        "trailing slash",
			endpoint:    "https://acct.services.ai.azure.com/api/projects/proj/",
			wantAccount: "acct",
			wantProject: "proj",
		},
		{
			name:        "uppercase host",
			endpoint:    "https://Acct.Services.AI.Azure.Com/api/projects/Proj",
			wantAccount: "Acct",
			wantProject: "Proj",
		},
		{
			name:     "empty",
			endpoint: "",
			wantErr:  true,
		},
		{
			name:     "non-foundry host",
			endpoint: "https://example.com/api/projects/proj",
			wantErr:  true,
		},
		{
			name:     "missing project",
			endpoint: "https://acct.services.ai.azure.com/api/projects",
			wantErr:  true,
		},
		{
			name:     "missing project segment",
			endpoint: "https://acct.services.ai.azure.com/",
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			account, project, err := parseFoundryEndpoint(tt.endpoint)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("parseFoundryEndpoint(%q) expected error, got none", tt.endpoint)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseFoundryEndpoint(%q) unexpected error: %v", tt.endpoint, err)
			}
			if account != tt.wantAccount {
				t.Errorf("account = %q, want %q", account, tt.wantAccount)
			}
			if project != tt.wantProject {
				t.Errorf("project = %q, want %q", project, tt.wantProject)
			}
		})
	}
}
