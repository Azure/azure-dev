// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"testing"

	"azureaiagent/internal/connections/pkg/connections"

	"github.com/stretchr/testify/require"
)

func TestParseEndpointComponents(t *testing.T) {
	tests := []struct {
		name        string
		endpoint    string
		wantAccount string
		wantProject string
		wantErr     bool
	}{
		{
			name:        "standard endpoint",
			endpoint:    "https://myaccount.services.ai.azure.com/api/projects/myproject",
			wantAccount: "myaccount",
			wantProject: "myproject",
		},
		{
			name:        "endpoint with trailing slash",
			endpoint:    "https://myaccount.services.ai.azure.com/api/projects/myproject/",
			wantAccount: "myaccount",
			wantProject: "myproject",
		},
		{
			name:     "missing project segment",
			endpoint: "https://myaccount.services.ai.azure.com/api/",
			wantErr:  true,
		},
		{
			name:     "empty endpoint",
			endpoint: "",
			wantErr:  true,
		},
		{
			name:     "no host",
			endpoint: "/api/projects/myproject",
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			account, project, err := parseEndpointComponents(tt.endpoint)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.wantAccount, account)
			require.Equal(t, tt.wantProject, project)
		})
	}
}

func TestParseARMResourceID(t *testing.T) {
	tests := []struct {
		name       string
		resourceID string
		wantSub    string
		wantRG     string
		wantAcct   string
		wantProj   string
		wantErr    bool
	}{
		{
			name: "full resource ID",
			resourceID: "/subscriptions/sub-123/resourceGroups/rg-test/" +
				"providers/Microsoft.CognitiveServices/accounts/acct1/projects/proj1/" +
				"connections/conn1",
			wantSub:  "sub-123",
			wantRG:   "rg-test",
			wantAcct: "acct1",
			wantProj: "proj1",
		},
		{
			name:       "missing subscription",
			resourceID: "/resourceGroups/rg/providers/Microsoft.CognitiveServices/accounts/a/projects/p",
			wantErr:    true,
		},
		{
			name:       "empty string",
			resourceID: "",
			wantErr:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := parseARMResourceID(tt.resourceID)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.wantSub, result.SubscriptionID)
			require.Equal(t, tt.wantRG, result.ResourceGroup)
			require.Equal(t, tt.wantAcct, result.AccountName)
			require.Equal(t, tt.wantProj, result.ProjectName)
		})
	}
}

func TestNormalizeKind(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"remote-tool", "RemoteTool"},
		{"cognitive-search", "CognitiveSearch"},
		{"api-key", "ApiKey"},
		{"app-insights", "AppInsights"},
		{"ai-services", "AIServices"},
		{"container-registry", "ContainerRegistry"},
		{"custom-keys", "CustomKeys"},
		// Already PascalCase — pass through
		{"RemoteTool", "RemoteTool"},
		// Unknown kind — pass through
		{"my-custom-kind", "my-custom-kind"},
		// Empty
		{"", ""},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			require.Equal(t, tt.want, normalizeKind(tt.input))
		})
	}
}

func TestNormalizeAuthType(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"ApiKey", "api-key"},
		{"CustomKeys", "custom-keys"},
		{"None", "none"},
		// Unknown — pass through
		{"AAD", "AAD"},
		{"", ""},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			require.Equal(t, tt.want, normalizeAuthType(tt.input))
		})
	}
}

func TestParseKVPtrMap(t *testing.T) {
	tests := []struct {
		name  string
		pairs []string
		want  map[string]string // compare dereferenced values
	}{
		{
			name:  "single pair",
			pairs: []string{"key1=value1"},
			want:  map[string]string{"key1": "value1"},
		},
		{
			name:  "multiple pairs",
			pairs: []string{"a=1", "b=2"},
			want:  map[string]string{"a": "1", "b": "2"},
		},
		{
			name:  "value with equals sign",
			pairs: []string{"key=val=ue"},
			want:  map[string]string{"key": "val=ue"},
		},
		{
			name:  "empty value",
			pairs: []string{"key="},
			want:  map[string]string{"key": ""},
		},
		{
			name:  "malformed pair skipped",
			pairs: []string{"noequals", "good=val"},
			want:  map[string]string{"good": "val"},
		},
		{
			name:  "nil input",
			pairs: nil,
			want:  nil,
		},
		{
			name:  "empty slice",
			pairs: []string{},
			want:  nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := parseKVPtrMap(tt.pairs)
			if tt.want == nil {
				require.Nil(t, result)
				return
			}
			require.Len(t, result, len(tt.want))
			for k, wantV := range tt.want {
				require.NotNil(t, result[k], "missing key %q", k)
				require.Equal(t, wantV, *result[k])
			}
		})
	}
}

func TestBuildCredentialReferences(t *testing.T) {
	tests := []struct {
		name     string
		connName string
		creds    *connections.ConnectionCredentials
		want     map[string]string
	}{
		{
			name:     "api key only",
			connName: "my-conn",
			creds: &connections.ConnectionCredentials{
				Key: "secret",
			},
			want: map[string]string{
				"key": "${{connections.my-conn.credentials.key}}",
			},
		},
		{
			name:     "custom keys",
			connName: "test-conn",
			creds: &connections.ConnectionCredentials{
				CustomKeys: map[string]string{
					"x-api-key": "val1",
					"token":     "val2",
				},
			},
			want: map[string]string{
				"x-api-key": "${{connections.test-conn.credentials.x-api-key}}",
				"token":     "${{connections.test-conn.credentials.token}}",
			},
		},
		{
			name:     "both key and custom keys",
			connName: "mixed",
			creds: &connections.ConnectionCredentials{
				Key:        "apikey",
				CustomKeys: map[string]string{"extra": "v"},
			},
			want: map[string]string{
				"key":   "${{connections.mixed.credentials.key}}",
				"extra": "${{connections.mixed.credentials.extra}}",
			},
		},
		{
			name:     "nil creds",
			connName: "x",
			creds:    nil,
			want:     nil,
		},
		{
			name:     "empty creds",
			connName: "x",
			creds:    &connections.ConnectionCredentials{},
			want:     nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := buildCredentialReferences(tt.connName, tt.creds)
			if tt.want == nil {
				require.Nil(t, result)
				return
			}
			require.Equal(t, tt.want, result)
		})
	}
}
