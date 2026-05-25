// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"encoding/json"
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
		{"remote-a2a", "RemoteA2A"},
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
		{"OAuth2", "oauth2"},
		{"UserEntraToken", "user-entra-token"},
		{"ProjectManagedIdentity", "project-managed-identity"},
		{"AgenticIdentityToken", "agentic-identity"},
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

func TestNormalizeAuthTypeToARM(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"oauth2", "OAuth2"},
		{"user-entra-token", "UserEntraToken"},
		{"project-managed-identity", "ProjectManagedIdentity"},
		{"agentic-identity", "AgenticIdentityToken"},
		{"unknown", "unknown"},
		{"", ""},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			require.Equal(t, tt.want, normalizeAuthTypeToARM(tt.input))
		})
	}
}

func TestBuildConnectionBody_OAuth2_NowUnsupported(t *testing.T) {
	// OAuth2 is now handled via raw REST, so buildConnectionBody should reject it
	_, err := buildConnectionBody(
		"RemoteTool", "https://example.com", "oauth2",
		"", nil, nil, "", "",
	)
	require.Error(t, err)
	require.Contains(t, err.Error(), "Unsupported auth type")
}

func TestBuildConnectionBody_UnsupportedAuthType(t *testing.T) {
	_, err := buildConnectionBody(
		"RemoteTool", "https://example.com", "invalid-type",
		"", nil, nil, "", "",
	)
	require.Error(t, err)
	require.Contains(t, err.Error(), "Unsupported auth type")
}

func TestRawConnectionBody_OAuth2_FullFields(t *testing.T) {
	props := rawConnectionProperties{
		AuthType:         "OAuth2",
		Category:         "RemoteTool",
		Target:           "https://api.githubcopilot.com/mcp/",
		AuthorizationURL: "https://github.com/login/oauth/authorize",
		TokenURL:         "https://github.com/login/oauth/access_token",
		RefreshURL:       "https://github.com/login/oauth/access_token",
		Scopes:           "read:user user:email",
		ConnectorName:    "github",
		Credentials: &rawCredentials{
			ClientID:     "test-cid",
			ClientSecret: "test-csec",
		},
	}
	body := rawConnectionBody{Properties: props}
	data, err := json.Marshal(body)
	require.NoError(t, err)

	var parsed map[string]any
	require.NoError(t, json.Unmarshal(data, &parsed))

	p := parsed["properties"].(map[string]any)
	require.Equal(t, "OAuth2", p["authType"])
	require.Equal(t, "https://github.com/login/oauth/authorize", p["authorizationUrl"])
	require.Equal(t, "https://github.com/login/oauth/access_token", p["tokenUrl"])
	require.Equal(t, "https://github.com/login/oauth/access_token", p["refreshUrl"])
	require.Equal(t, "read:user user:email", p["scopes"])
	require.Equal(t, "github", p["connectorName"])

	creds := p["credentials"].(map[string]any)
	require.Equal(t, "test-cid", creds["clientId"])
	require.Equal(t, "test-csec", creds["clientSecret"])
}

func TestRawConnectionBody_MarshalJSON(t *testing.T) {
	props := rawConnectionProperties{
		AuthType: "UserEntraToken",
		Category: "RemoteTool",
		Target:   "https://example.com",
		Audience: "https://mcp.ai.azure.com",
	}
	body := rawConnectionBody{Properties: props}
	data, err := json.Marshal(body)
	require.NoError(t, err)

	var parsed map[string]any
	require.NoError(t, json.Unmarshal(data, &parsed))

	p := parsed["properties"].(map[string]any)
	require.Equal(t, "UserEntraToken", p["authType"])
	require.Equal(t, "RemoteTool", p["category"])
	require.Equal(t, "https://example.com", p["target"])
	require.Equal(t, "https://mcp.ai.azure.com", p["audience"])
}

func TestRawConnectionBody_OmitsEmptyAudience(t *testing.T) {
	props := rawConnectionProperties{
		AuthType: "ProjectManagedIdentity",
		Category: "RemoteA2A",
		Target:   "https://example.com",
	}
	body := rawConnectionBody{Properties: props}
	data, err := json.Marshal(body)
	require.NoError(t, err)

	var parsed map[string]any
	require.NoError(t, json.Unmarshal(data, &parsed))

	p := parsed["properties"].(map[string]any)
	_, hasAudience := p["audience"]
	require.False(t, hasAudience, "audience should be omitted when empty")
}

func TestParseKVMap(t *testing.T) {
	tests := []struct {
		name  string
		pairs []string
		want  map[string]string
	}{
		{"nil", nil, nil},
		{"empty", []string{}, nil},
		{"single", []string{"k=v"}, map[string]string{"k": "v"}},
		{"value-with-equals", []string{"k=v=1"}, map[string]string{"k": "v=1"}},
		{"multiple", []string{"a=1", "b=2"}, map[string]string{"a": "1", "b": "2"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseKVMap(tt.pairs)
			require.Equal(t, tt.want, got)
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

func TestRawConnectionBody_OAuth2_ConnectorNameOnly(t *testing.T) {
	// When using a managed connector, only connectorName is set — no credentials.
	props := rawConnectionProperties{
		AuthType:      "OAuth2",
		Category:      "RemoteTool",
		Target:        "https://example.com",
		ConnectorName: "github",
	}
	body := rawConnectionBody{Properties: props}
	data, err := json.Marshal(body)
	require.NoError(t, err)

	var parsed map[string]any
	require.NoError(t, json.Unmarshal(data, &parsed))

	p := parsed["properties"].(map[string]any)
	require.Equal(t, "OAuth2", p["authType"])
	require.Equal(t, "github", p["connectorName"])
	_, hasCreds := p["credentials"]
	require.False(t, hasCreds, "credentials should be omitted for connector-name-only")
	_, hasAuthURL := p["authorizationUrl"]
	require.False(t, hasAuthURL, "authorizationUrl should be omitted for connector-name-only")
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
