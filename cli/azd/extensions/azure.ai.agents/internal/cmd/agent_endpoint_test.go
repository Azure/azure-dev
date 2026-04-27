// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"strings"
	"testing"
)

func TestParseAgentEndpoint(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		input         string
		wantProject   string
		wantAgentName string
		wantErrSubstr string
	}{
		{
			name:          "full deploy-printed endpoint with version",
			input:         "https://acct.services.ai.azure.com/api/projects/proj/agents/my-agent/versions/1",
			wantProject:   "https://acct.services.ai.azure.com/api/projects/proj",
			wantAgentName: "my-agent",
		},
		{
			name:          "without version suffix",
			input:         "https://acct.services.ai.azure.com/api/projects/proj/agents/my-agent",
			wantProject:   "https://acct.services.ai.azure.com/api/projects/proj",
			wantAgentName: "my-agent",
		},
		{
			name:          "trailing slash",
			input:         "https://acct.services.ai.azure.com/api/projects/proj/agents/my-agent/versions/1/",
			wantProject:   "https://acct.services.ai.azure.com/api/projects/proj",
			wantAgentName: "my-agent",
		},
		{
			name:          "agent name with hyphens",
			input:         "https://x.services.ai.azure.com/api/projects/y/agents/agent-with-many-hyphens-1/versions/2",
			wantProject:   "https://x.services.ai.azure.com/api/projects/y",
			wantAgentName: "agent-with-many-hyphens-1",
		},
		{
			name:          "leading/trailing whitespace tolerated",
			input:         "   https://acct.services.ai.azure.com/api/projects/proj/agents/my-agent   ",
			wantProject:   "https://acct.services.ai.azure.com/api/projects/proj",
			wantAgentName: "my-agent",
		},
		{
			name:          "empty",
			input:         "",
			wantErrSubstr: "empty",
		},
		{
			name:          "no scheme",
			input:         "acct.services.ai.azure.com/api/projects/proj/agents/my-agent",
			wantErrSubstr: "scheme",
		},
		{
			name:          "wrong scheme",
			input:         "ftp://acct.services.ai.azure.com/api/projects/proj/agents/my-agent",
			wantErrSubstr: "scheme",
		},
		{
			name:          "http scheme rejected",
			input:         "http://acct.services.ai.azure.com/api/projects/proj/agents/my-agent",
			wantErrSubstr: "https",
		},
		{
			name:          "missing /agents/ segment",
			input:         "https://acct.services.ai.azure.com/api/projects/proj",
			wantErrSubstr: "/agents/",
		},
		{
			name:          "agent name empty",
			input:         "https://acct.services.ai.azure.com/api/projects/proj/agents/",
			wantErrSubstr: "agent name",
		},
		{
			name:          "unexpected suffix after agent name",
			input:         "https://acct.services.ai.azure.com/api/projects/proj/agents/my-agent/endpoint",
			wantErrSubstr: "versions",
		},
		{
			name:          "versions without value",
			input:         "https://acct.services.ai.azure.com/api/projects/proj/agents/my-agent/versions/",
			wantErrSubstr: "versions",
		},
		{
			name:          "trailing junk after versions rejected",
			input:         "https://acct.services.ai.azure.com/api/projects/proj/agents/my-agent/versions/1/extra",
			wantErrSubstr: "versions",
		},
		{
			name:          "agent name with unsupported characters rejected",
			input:         "https://acct.services.ai.azure.com/api/projects/proj/agents/my%20agent/versions/1",
			wantErrSubstr: "unsupported characters",
		},
		{
			name:          "agent name with dot rejected",
			input:         "https://acct.services.ai.azure.com/api/projects/proj/agents/bad.name/versions/1",
			wantErrSubstr: "unsupported characters",
		},
		{
			name:          "query string and fragment stripped",
			input:         "https://acct.services.ai.azure.com/api/projects/proj/agents/my-agent/versions/1?x=y#frag",
			wantProject:   "https://acct.services.ai.azure.com/api/projects/proj",
			wantAgentName: "my-agent",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			gotProject, gotAgent, err := parseAgentEndpoint(tc.input)
			if tc.wantErrSubstr != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tc.wantErrSubstr)
				}
				if !strings.Contains(err.Error(), tc.wantErrSubstr) {
					t.Fatalf("error %q does not contain %q", err.Error(), tc.wantErrSubstr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if gotProject != tc.wantProject {
				t.Errorf("projectEndpoint: got %q, want %q", gotProject, tc.wantProject)
			}
			if gotAgent != tc.wantAgentName {
				t.Errorf("agentName: got %q, want %q", gotAgent, tc.wantAgentName)
			}
		})
	}
}
