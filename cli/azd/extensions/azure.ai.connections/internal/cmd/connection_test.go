// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"strings"
	"testing"

	"azure.ai.connections/internal/pkg/connections"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
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
		// Already PascalCase ΓÇö pass through
		{"RemoteTool", "RemoteTool"},
		// Unknown kind ΓÇö pass through
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
		// Unknown ΓÇö pass through
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

func TestOAuth2Validation(t *testing.T) {
	runValidation := func(flags *connectionCreateFlags) error {
		action := &ConnectionCreateAction{flags: flags}
		// Run calls resolveConnectionContext which needs real infra, so we only
		// test the validation prefix by calling Run and checking for validation errors.
		// Any error that is NOT a validation error means we passed validation.
		err := action.Run(t.Context())
		return err
	}

	isValidationError := func(err error, substr string) bool {
		return err != nil && strings.Contains(err.Error(), substr)
	}

	t.Run("reject connector-name combined with BYO flags", func(t *testing.T) {
		flags := &connectionCreateFlags{
			kind:             "remote-tool",
			target:           "https://example.com",
			authType:         "oauth2",
			connectorName:    "github",
			authorizationURL: "https://example.com/auth",
		}
		err := runValidation(flags)
		require.True(t, isValidationError(err, "--connector-name cannot be combined with"))
	})

	t.Run("reject empty oauth2 - neither connector nor BYO", func(t *testing.T) {
		flags := &connectionCreateFlags{
			kind:     "remote-tool",
			target:   "https://example.com",
			authType: "oauth2",
		}
		err := runValidation(flags)
		require.True(t, isValidationError(err, "OAuth2 auth requires either"))
	})

	t.Run("reject partial BYO - missing required fields", func(t *testing.T) {
		flags := &connectionCreateFlags{
			kind:             "remote-tool",
			target:           "https://example.com",
			authType:         "oauth2",
			authorizationURL: "https://example.com/auth",
			// missing token-url, client-id, client-secret
		}
		err := runValidation(flags)
		require.True(t, isValidationError(err, "Missing: --token-url"))
	})

	t.Run("accept connector-name only", func(t *testing.T) {
		flags := &connectionCreateFlags{
			kind:          "remote-tool",
			target:        "https://example.com",
			authType:      "oauth2",
			connectorName: "github",
		}
		err := runValidation(flags)
		// Should pass validation ΓÇö any error here is from resolveConnectionContext, not validation
		require.False(t, isValidationError(err, "connector-name"))
		require.False(t, isValidationError(err, "Missing"))
	})

	t.Run("accept full BYO without optional refresh-url", func(t *testing.T) {
		flags := &connectionCreateFlags{ //nolint:gosec // test credentials, not real
			kind:             "remote-tool",
			target:           "https://example.com",
			authType:         "oauth2",
			authorizationURL: "https://example.com/auth",
			tokenURL:         "https://example.com/token",
			clientID:         "cid",
			clientSecret:     "csec",
		}
		err := runValidation(flags)
		// Should pass validation ΓÇö any error here is from resolveConnectionContext, not validation
		require.False(t, isValidationError(err, "Missing"))
		require.False(t, isValidationError(err, "requires"))
	})

	t.Run("accept full BYO with all fields", func(t *testing.T) {
		flags := &connectionCreateFlags{ //nolint:gosec // test credentials, not real
			kind:             "remote-tool",
			target:           "https://example.com",
			authType:         "oauth2",
			authorizationURL: "https://example.com/auth",
			tokenURL:         "https://example.com/token",
			refreshURL:       "https://example.com/refresh",
			scopes:           []string{"read", "write"},
			clientID:         "cid",
			clientSecret:     "csec",
		}
		err := runValidation(flags)
		require.False(t, isValidationError(err, "Missing"))
		require.False(t, isValidationError(err, "requires"))
	})

	t.Run("reject oauth2 flags with non-oauth2 auth type", func(t *testing.T) {
		flags := &connectionCreateFlags{
			kind:     "remote-tool",
			target:   "https://example.com",
			authType: "api-key",
			key:      "abc",
			scopes:   []string{"read"},
		}
		err := runValidation(flags)
		require.True(t, isValidationError(err, "only valid with --auth-type oauth2"))
	})
}

func TestRawConnectionBody_OAuth2_FullFields(t *testing.T) {
	// BYO OAuth2 ΓÇö no ConnectorName (CLI makes connector-name and BYO mutually exclusive).
	props := rawConnectionProperties{ //nolint:gosec // test credentials, not real
		AuthType:         "OAuth2",
		Category:         "RemoteTool",
		Target:           "https://api.githubcopilot.com/mcp/",
		AuthorizationURL: "https://github.com/login/oauth/authorize",
		TokenURL:         "https://github.com/login/oauth/access_token",
		RefreshURL:       "https://github.com/login/oauth/access_token",
		Scopes:           []string{"read:user", "user:email"},
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

	// scopes must be a JSON array
	scopesList, ok := p["scopes"].([]any)
	require.True(t, ok, "scopes should be a JSON array")
	require.Equal(t, []any{"read:user", "user:email"}, scopesList)

	// No connectorName in BYO mode
	_, hasConnector := p["connectorName"]
	require.False(t, hasConnector, "connectorName should be omitted in BYO mode")

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
	// Regression test for #8418. When using a managed connector (gateway_connector),
	// connectorName is set and no --client-id/--client-secret are supplied. The
	// ConnectionCreateAction.Run path must still populate props.Credentials so the
	// PUT body marshals to `"credentials": {}` — otherwise the ARM API rejects with
	// `400 ValidationError: Credentials Property can't be empty for auth type OAuth2`.
	//
	// We exercise the same decision Run makes via buildOAuth2Credentials and then
	// marshal a full body so this guards both the predicate and the resulting JSON.
	props := rawConnectionProperties{
		AuthType:      "OAuth2",
		Category:      "RemoteTool",
		Target:        "https://example.com",
		ConnectorName: "github",
		Credentials:   buildOAuth2Credentials("oauth2", "", ""),
	}
	require.NotNil(t, props.Credentials,
		"buildOAuth2Credentials must return a non-nil object for --auth-type oauth2 "+
			"with no client id/secret (managed-connector path)")

	body := rawConnectionBody{Properties: props}
	data, err := json.Marshal(body)
	require.NoError(t, err)

	var parsed map[string]any
	require.NoError(t, json.Unmarshal(data, &parsed))

	p := parsed["properties"].(map[string]any)
	require.Equal(t, "OAuth2", p["authType"])
	require.Equal(t, "github", p["connectorName"])

	creds, hasCreds := p["credentials"]
	require.True(t, hasCreds, "credentials must be present (empty object) for managed-connector OAuth2")
	credsMap, ok := creds.(map[string]any)
	require.True(t, ok, "credentials should marshal as a JSON object")
	require.Empty(t, credsMap, "credentials should be an empty object when no clientId/clientSecret supplied")

	// The raw JSON should literally contain `"credentials":{}`.
	require.Contains(t, string(data), `"credentials":{}`)

	_, hasAuthURL := p["authorizationUrl"]
	require.False(t, hasAuthURL, "authorizationUrl should be omitted for connector-name-only")
}

func TestBuildOAuth2Credentials(t *testing.T) {
	tests := []struct {
		name         string
		authType     string
		clientID     string
		clientSecret string
		wantNil      bool
		wantID       string
		wantSecret   string
	}{
		{
			name:     "oauth2 managed connector emits empty object",
			authType: "oauth2",
			wantNil:  false,
		},
		{
			name:         "oauth2 BYO emits client id and secret",
			authType:     "oauth2",
			clientID:     "test-cid",
			clientSecret: "test-csec", //nolint:gosec // test credential, not real
			wantNil:      false,
			wantID:       "test-cid",
			wantSecret:   "test-csec",
		},
		{
			name:     "api-key with no creds returns nil",
			authType: "api-key",
			wantNil:  true,
		},
		{
			name:     "user-entra-token with no creds returns nil",
			authType: "user-entra-token",
			wantNil:  true,
		},
		{
			name:     "non-oauth2 with client id still emits creds",
			authType: "api-key",
			clientID: "cid",
			wantNil:  false,
			wantID:   "cid",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildOAuth2Credentials(tt.authType, tt.clientID, tt.clientSecret)
			if tt.wantNil {
				require.Nil(t, got)
				return
			}
			require.NotNil(t, got)
			require.Equal(t, tt.wantID, got.ClientID)
			require.Equal(t, tt.wantSecret, got.ClientSecret)
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

func TestPrintDetail_IncludesMetadata(t *testing.T) {
	v1 := "value1"
	v2 := "value2"
	result := connectionDetailResult{
		Name:     "test-conn",
		Kind:     "RemoteTool",
		AuthType: "None",
		Target:   "https://example.com",
		Metadata: map[string]*string{
			"key1": &v1,
			"key2": &v2,
		},
	}

	// Capture stdout
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err := printDetail(result, "table")
	require.NoError(t, err)

	require.NoError(t, w.Close())
	os.Stdout = old

	var buf bytes.Buffer
	_, _ = io.Copy(&buf, r)
	output := buf.String()

	require.Contains(t, output, "Metadata:")
	require.Contains(t, output, "key1: value1")
	require.Contains(t, output, "key2: value2")
}

func TestPrintDetail_OmitsEmptyMetadata(t *testing.T) {
	result := connectionDetailResult{
		Name:     "test-conn",
		Kind:     "RemoteTool",
		AuthType: "None",
		Target:   "https://example.com",
	}

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err := printDetail(result, "table")
	require.NoError(t, err)

	require.NoError(t, w.Close())
	os.Stdout = old

	var buf bytes.Buffer
	_, _ = io.Copy(&buf, r)
	output := buf.String()

	require.NotContains(t, output, "Metadata:")
}

func TestPrintDetail_JSON_IncludesMetadata(t *testing.T) {
	v1 := "val1"
	result := connectionDetailResult{
		Name:     "test-conn",
		Kind:     "RemoteTool",
		AuthType: "None",
		Target:   "https://example.com",
		Metadata: map[string]*string{
			"foo": &v1,
		},
	}

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err := printDetail(result, "json")
	require.NoError(t, err)

	require.NoError(t, w.Close())
	os.Stdout = old

	var buf bytes.Buffer
	_, _ = io.Copy(&buf, r)

	var parsed map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &parsed))

	meta, ok := parsed["metadata"].(map[string]any)
	require.True(t, ok, "metadata should be present in JSON output")
	require.Equal(t, "val1", meta["foo"])
}

func TestUpdateValidation_NoFieldsToUpdate(t *testing.T) {
	action := &ConnectionUpdateAction{
		flags: &connectionUpdateFlags{
			name: "test-conn",
		},
	}
	err := action.Run(t.Context())
	require.Error(t, err)
	require.Contains(t, err.Error(), "No fields to update")
	var localErr *azdext.LocalError
	require.ErrorAs(t, err, &localErr)
	require.Contains(t, localErr.Suggestion, "--metadata")
}

func TestUpdateValidation_MetadataAloneIsValid(t *testing.T) {
	action := &ConnectionUpdateAction{
		flags: &connectionUpdateFlags{
			name:            "test-conn",
			metadata:        []string{"type=gateway_connector"},
			metadataChanged: true,
		},
	}
	err := action.Run(t.Context())
	// Should pass validation — any error here is from resolveConnectionContext, not validation
	require.NotContains(t, err.Error(), "No fields to update")
}
