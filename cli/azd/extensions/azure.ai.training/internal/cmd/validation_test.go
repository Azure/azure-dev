// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestParseProjectEndpoint covers the endpoint parser used by both the init
// flow and the new --subscription / --project-endpoint override path in
// validateOrInitEnvironment. Failures here propagate as user-visible errors,
// so all the success and failure modes are pinned down.
func TestParseProjectEndpoint(t *testing.T) {
	tests := []struct {
		name            string
		endpoint        string
		wantAccount     string
		wantProject     string
		wantErr         bool
		wantErrContains string
	}{
		{
			name:        "services.ai.azure.com endpoint",
			endpoint:    "https://my-account.services.ai.azure.com/api/projects/my-project",
			wantAccount: "my-account",
			wantProject: "my-project",
		},
		{
			name:        "cognitiveservices.azure.com endpoint",
			endpoint:    "https://other-account.cognitiveservices.azure.com/api/projects/other-project",
			wantAccount: "other-account",
			wantProject: "other-project",
		},
		{
			name:        "trailing slash on project segment is tolerated",
			endpoint:    "https://acc.services.ai.azure.com/api/projects/proj/",
			wantAccount: "acc",
			wantProject: "proj",
		},
		{
			name:            "missing /api/projects/ segment",
			endpoint:        "https://acc.services.ai.azure.com/foo/bar/baz",
			wantErr:         true,
			wantErrContains: "expected format /api/projects/{project-name}",
		},
		{
			name:            "wrong path order",
			endpoint:        "https://acc.services.ai.azure.com/projects/api/proj",
			wantErr:         true,
			wantErrContains: "expected format /api/projects/{project-name}",
		},
		{
			name:            "missing project name",
			endpoint:        "https://acc.services.ai.azure.com/api/projects/",
			wantErr:         true,
			wantErrContains: "expected format /api/projects/{project-name}",
		},
		{
			name:            "no path at all",
			endpoint:        "https://acc.services.ai.azure.com",
			wantErr:         true,
			wantErrContains: "expected format /api/projects/{project-name}",
		},
		{
			name:            "empty hostname",
			endpoint:        "https:///api/projects/proj",
			wantErr:         true,
			wantErrContains: "cannot extract account name",
		},
		{
			name:        "http scheme accepted (parser is scheme-agnostic)",
			endpoint:    "http://acc.services.ai.azure.com/api/projects/proj",
			wantAccount: "acc",
			wantProject: "proj",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			account, project, err := parseProjectEndpoint(tc.endpoint)
			if tc.wantErr {
				require.Error(t, err)
				if tc.wantErrContains != "" {
					require.Contains(t, err.Error(), tc.wantErrContains)
				}
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.wantAccount, account)
			require.Equal(t, tc.wantProject, project)
		})
	}
}

// TestSanitizeEnvironmentName covers the helper used by implicitInit to
// derive an azd environment name from a project name. azd env names must be
// lowercase letters, numbers, and hyphens only, and must start/end with an
// alphanumeric character.
func TestSanitizeEnvironmentName(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "already valid", input: "my-project", want: "my-project"},
		{name: "uppercase lowered", input: "MyProject", want: "myproject"},
		{name: "spaces become hyphens", input: "my project name", want: "my-project-name"},
		{name: "underscores become hyphens", input: "my_project_name", want: "my-project-name"},
		{name: "special chars stripped", input: "my.project!name@123", want: "myprojectname123"},
		{name: "consecutive hyphens collapsed", input: "my---project", want: "my-project"},
		{name: "leading/trailing hyphens trimmed", input: "-my-project-", want: "my-project"},
		{name: "all special chars falls back to default", input: "!@#$%", want: "training-env"},
		{name: "empty string falls back to default", input: "", want: "training-env"},
		{name: "mixed messy input", input: "  My_Crazy.Project!Name  ", want: "my-crazyprojectname"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := sanitizeEnvironmentName(tc.input)
			require.Equal(t, tc.want, got)
		})
	}
}
