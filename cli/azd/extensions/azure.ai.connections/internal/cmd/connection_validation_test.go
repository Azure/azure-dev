// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"encoding/json"
	"errors"
	"testing"

	"azure.ai.connections/internal/exterrors"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/require"
)

func requireConnectionValidationError(t *testing.T, err error, code, field string) *azdext.LocalError {
	t.Helper()
	require.Error(t, err)
	localErr, ok := errors.AsType[*azdext.LocalError](err)
	require.True(t, ok, "expected structured validation error")
	require.Equal(t, azdext.LocalErrorCategoryValidation, localErr.Category)
	require.Equal(t, code, localErr.Code)
	require.Contains(t, localErr.Message, field)
	require.NotEmpty(t, localErr.Suggestion)
	return localErr
}

func TestValidateConnectionPropertiesServiceAuthTypes(t *testing.T) {
	t.Parallel()

	for _, authType := range []string{
		"AAD", "AccessKey", "AccountKey", "AgenticIdentity", "AgenticIdentityToken",
		"ApiKey", "CustomKeys", "ManagedIdentity", "None", "OAuth2", "PAT",
		"ProjectManagedIdentity", "SAS", "ServicePrincipal", "UserEntraToken", "UsernamePassword",
	} {
		t.Run(authType, func(t *testing.T) {
			t.Parallel()
			props := rawConnectionProperties{
				AuthType: authType,
				Category: "ContainerRegistry",
				Target:   "https://example.test",
				Metadata: map[string]string{"owner": "platform"},
				Credentials: &rawCredentials{
					"clientId": "test-client", "clientSecret": "test-secret",
					"nested": map[string]any{"values": []any{"preserved", true, float64(3)}},
				},
			}
			switch authType {
			case "ApiKey":
				(*props.Credentials)["key"] = "${{connections.source.credentials.key}}"
			case "CustomKeys":
				(*props.Credentials)["keys"] = map[string]string{"Authorization": "Bearer test-token"}
			case "OAuth2":
				props.ConnectorName = "github"
				props.Credentials = &rawCredentials{}
			}
			before, err := json.Marshal(props)
			require.NoError(t, err)
			require.NoError(t, validateConnectionProperties(props))
			after, err := json.Marshal(props)
			require.NoError(t, err)
			require.JSONEq(t, string(before), string(after), "validation must not mutate properties")
			require.Equal(t, authType, props.AuthType, "validation must not rename service auth types")
		})
	}
}

func TestValidateConnectionPropertiesRequiredFields(t *testing.T) {
	t.Parallel()

	for _, field := range []string{"category", "target"} {
		for _, value := range []string{"", " \t\n"} {
			t.Run(field+"="+value, func(t *testing.T) {
				t.Parallel()
				props := rawConnectionProperties{AuthType: "None", Category: "RemoteTool", Target: "https://example.test"}
				if field == "category" {
					props.Category = value
				} else {
					props.Target = value
				}
				err := validateConnectionProperties(props)
				requireConnectionValidationError(t, err, exterrors.CodeMissingConnectionField, field)
			})
		}
	}
}

func TestValidateConnectionPropertiesCredentials(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		authType    string
		credentials *rawCredentials
		valid       bool
	}{
		{"api missing", "ApiKey", nil, false},
		{"api nil map", "ApiKey", new(rawCredentials), false},
		{"api empty object", "ApiKey", &rawCredentials{}, false},
		{"api empty key", "ApiKey", &rawCredentials{"key": ""}, false},
		{"api blank key", "ApiKey", &rawCredentials{"key": " \t\n"}, false},
		{"api null key", "ApiKey", &rawCredentials{"key": nil}, false},
		{"api numeric key", "ApiKey", &rawCredentials{"key": 42}, false},
		{"api object key", "ApiKey", &rawCredentials{"key": map[string]any{"value": "secret"}}, false},
		{"api valid key", "ApiKey", &rawCredentials{"key": "secret"}, true},
		{"api server reference", "ApiKey", &rawCredentials{"key": "${{connections.source.credentials.key}}"}, true},
		{"custom missing", "CustomKeys", nil, false},
		{"custom nil map", "CustomKeys", new(rawCredentials), false},
		{"custom empty object", "CustomKeys", &rawCredentials{}, false},
		{"custom flat", "CustomKeys", &rawCredentials{"Authorization": "Bearer secret"}, true},
		{"custom legacy key named keys", "CustomKeys", &rawCredentials{"keys": "secret"}, true},
		{"custom flat empty name", "CustomKeys", &rawCredentials{"": "secret"}, false},
		{"custom flat blank name", "CustomKeys", &rawCredentials{" \t": "secret"}, false},
		{"custom flat blank value", "CustomKeys", &rawCredentials{"Authorization": " \t"}, false},
		{"custom flat invalid value", "CustomKeys", &rawCredentials{"Authorization": false}, false},
		{"custom nested object", "CustomKeys", &rawCredentials{"keys": map[string]any{"x-key": "secret"}}, true},
		{"custom nested strings", "CustomKeys", &rawCredentials{"keys": map[string]string{"x-key": "secret"}}, true},
		{"custom nested raw", "CustomKeys", &rawCredentials{"keys": rawCredentials{"x-key": "secret"}}, true},
		{"custom nested nil", "CustomKeys", &rawCredentials{"keys": nil}, false},
		{"custom nested nil object", "CustomKeys", &rawCredentials{"keys": map[string]any(nil)}, false},
		{"custom nested empty object", "CustomKeys", &rawCredentials{"keys": map[string]any{}}, false},
		{"custom nested empty strings", "CustomKeys", &rawCredentials{"keys": map[string]string{}}, false},
		{"custom nested array", "CustomKeys", &rawCredentials{"keys": []any{"secret"}}, false},
		{"custom nested number", "CustomKeys", &rawCredentials{"keys": 42}, false},
		{"custom nested blank name", "CustomKeys", &rawCredentials{"keys": map[string]any{" ": "secret"}}, false},
		{"custom nested blank value", "CustomKeys", &rawCredentials{"keys": map[string]string{"x-key": " "}}, false},
		{"custom nested nonstring value", "CustomKeys", &rawCredentials{"keys": map[string]any{"x-key": 42}}, false},
		{
			"custom one empty entry", "CustomKeys",
			&rawCredentials{"keys": map[string]any{"valid": "secret", "empty": ""}}, false,
		},
		{
			"custom empty nested cannot use outer credential", "CustomKeys",
			&rawCredentials{"keys": map[string]any{}, "outer": "secret"}, false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			props := rawConnectionProperties{
				AuthType: tt.authType, Category: "RemoteTool", Target: "https://example.test", Credentials: tt.credentials,
			}
			before, err := json.Marshal(props)
			require.NoError(t, err)
			err = validateConnectionProperties(props)
			if tt.valid {
				require.NoError(t, err)
			} else {
				requireConnectionValidationError(t, err, exterrors.CodeMissingConnectionField, "credentials")
			}
			after, err := json.Marshal(props)
			require.NoError(t, err)
			require.JSONEq(t, string(before), string(after))
		})
	}
}

func TestValidateConnectionPropertiesOAuth2RequiredFields(t *testing.T) {
	t.Parallel()

	for _, field := range []string{"authorizationUrl", "tokenUrl", "credentials.clientId", "credentials.clientSecret"} {
		for _, value := range []string{"", " \t\n"} {
			t.Run(field+"="+value, func(t *testing.T) {
				t.Parallel()
				props := rawConnectionProperties{ //nolint:gosec // Synthetic OAuth credentials for validation tests.
					AuthType: "OAuth2", Category: "RemoteTool", Target: "https://example.test",
					AuthorizationURL: "https://example.test/auth", TokenURL: "https://example.test/token",
					Credentials: &rawCredentials{"clientId": "client", "clientSecret": "secret"},
				}
				switch field {
				case "authorizationUrl":
					props.AuthorizationURL = value
				case "tokenUrl":
					props.TokenURL = value
				case "credentials.clientId":
					(*props.Credentials)["clientId"] = value
				case "credentials.clientSecret":
					(*props.Credentials)["clientSecret"] = value
				}
				err := validateConnectionProperties(props)
				requireConnectionValidationError(t, err, exterrors.CodeMissingConnectionField, field)
			})
		}
	}
	for _, credentials := range []*rawCredentials{
		nil, new(rawCredentials), {},
		{"clientId": 42, "clientSecret": "secret"},
		{"clientId": "client", "clientSecret": map[string]any{"value": "secret"}},
	} {
		props := rawConnectionProperties{ //nolint:gosec // Synthetic OAuth credentials for validation tests.
			AuthType: "OAuth2", Category: "RemoteTool", Target: "https://example.test",
			AuthorizationURL: "https://example.test/auth", TokenURL: "https://example.test/token",
			Credentials: credentials,
		}
		err := validateConnectionProperties(props)
		requireConnectionValidationError(t, err, exterrors.CodeMissingConnectionField, "credentials.")
	}
}

func TestValidateConnectionPropertiesOAuthFieldConflicts(t *testing.T) {
	t.Parallel()

	fields := []struct {
		name string
		set  func(*rawConnectionProperties)
	}{
		{"authorizationUrl", func(p *rawConnectionProperties) { p.AuthorizationURL = "https://example.test/auth" }},
		{"tokenUrl", func(p *rawConnectionProperties) { p.TokenURL = "https://example.test/token" }},
		{"refreshUrl", func(p *rawConnectionProperties) { p.RefreshURL = "https://example.test/refresh" }},
		{"scopes", func(p *rawConnectionProperties) { p.Scopes = []string{"read"} }},
		{"connectorName", func(p *rawConnectionProperties) { p.ConnectorName = "github" }},
		{"credentials.clientId", func(p *rawConnectionProperties) { (*p.Credentials)["clientId"] = "" }},
		{"credentials.clientSecret", func(p *rawConnectionProperties) { (*p.Credentials)["clientSecret"] = nil }},
	}
	for _, authType := range []string{
		"None", "ApiKey", "ManagedIdentity", "ServicePrincipal", "OAuth2", "ProjectManagedIdentity",
		"UserEntraToken", "AgenticIdentityToken", "AAD", "PAT", "AccessKey", "AccountKey", "SAS", "UsernamePassword",
	} {
		for _, field := range fields {
			t.Run(authType+"/"+field.name, func(t *testing.T) {
				t.Parallel()
				props := rawConnectionProperties{
					AuthType: authType, Category: "RemoteTool", Target: "https://example.test",
					Credentials: &rawCredentials{"key": "secret"},
				}
				if authType == "OAuth2" {
					props.ConnectorName = "github"
					props.Credentials = &rawCredentials{}
				}
				field.set(&props)
				err := validateConnectionProperties(props)
				if authType == "OAuth2" && field.name == "connectorName" {
					require.NoError(t, err)
				} else if authType != "OAuth2" &&
					(field.name == "credentials.clientId" || field.name == "credentials.clientSecret") {
					// Credential field names do not make generic service auth OAuth2-only.
					require.NoError(t, err)
				} else {
					requireConnectionValidationError(t, err, exterrors.CodeConflictingArguments, field.name)
				}
			})
		}
	}

	props := rawConnectionProperties{
		AuthType: "OAuth2", Category: "RemoteTool", Target: "https://example.test", ConnectorName: " \t\n",
	}
	err := validateConnectionProperties(props)
	requireConnectionValidationError(t, err, exterrors.CodeMissingConnectionField, "connectorName")
}

func TestValidateConnectionPropertiesAudience(t *testing.T) {
	t.Parallel()

	for _, authType := range []string{
		"ProjectManagedIdentity", "UserEntraToken", "AgenticIdentityToken", "AgenticIdentity",
		"ManagedIdentity", "ServicePrincipal", "AAD", "PAT", "AccessKey", "AccountKey", "SAS", "UsernamePassword",
		"None", "ApiKey", "CustomKeys", "OAuth2",
	} {
		t.Run(authType, func(t *testing.T) {
			t.Parallel()
			props := rawConnectionProperties{
				AuthType: authType, Category: "RemoteTool", Target: "https://example.test", Audience: "audience",
			}
			err := validateConnectionProperties(props)
			switch authType {
			case "ProjectManagedIdentity", "UserEntraToken", "AgenticIdentityToken", "AgenticIdentity":
				require.NoError(t, err)
			default:
				requireConnectionValidationError(t, err, exterrors.CodeConflictingArguments, "audience")
			}
		})
	}
}

func TestConnectionValidationErrorsDoNotDiscloseValues(t *testing.T) {
	t.Parallel()

	const secret = "sensitive-value-must-not-appear"
	for _, authType := range []string{"ApiKey", "CustomKeys", "OAuth2", "None", secret} {
		t.Run(authType, func(t *testing.T) {
			t.Parallel()
			props := rawConnectionProperties{
				AuthType: authType, Category: secret, Target: "https://user:" + secret + "@example.test?sig=" + secret,
				Credentials: &rawCredentials{secret: secret}, Metadata: map[string]string{secret: secret},
			}
			code, field := exterrors.CodeMissingConnectionField, "credentials"
			switch authType {
			case "CustomKeys":
				(*props.Credentials)[secret] = ""
			case "OAuth2":
				props.ConnectorName = secret
				props.AuthorizationURL = secret
				code, field = exterrors.CodeConflictingArguments, "connectorName"
			case "None":
				props.Audience = secret
				code, field = exterrors.CodeConflictingArguments, "audience"
			case secret:
				code, field = exterrors.CodeInvalidAuthType, "authType"
			}
			localErr := requireConnectionValidationError(t, validateConnectionProperties(props), code, field)
			for _, text := range []string{localErr.Error(), localErr.Message, localErr.Suggestion} {
				require.NotContains(t, text, secret)
				require.NotContains(t, text, "https://")
				require.NotContains(t, text, "--", "shared errors must use semantic field labels")
			}
		})
	}
}

func TestConnectionCreatePropertiesMatchesSDK(t *testing.T) {
	t.Parallel()

	for _, authType := range []string{"", "none", "api-key", "custom-keys"} {
		t.Run(authType, func(t *testing.T) {
			t.Parallel()
			flags := &connectionCreateFlags{
				kind: "remote-tool", target: "https://example.test", authType: authType,
				key: " secret ", customKeys: []string{"x-key=value=with=equals", "Authorization=Bearer secret"},
				metadata: []string{"owner=old", "owner=platform"},
			}
			props, err := connectionCreateProperties(flags)
			require.NoError(t, err)
			require.NoError(t, validateConnectionProperties(props))
			sdkBody, err := buildConnectionBody(
				flags.kind, flags.target, flags.authType, flags.key, flags.customKeys, flags.metadata, "", "",
			)
			require.NoError(t, err)
			expected, err := json.Marshal(sdkBody)
			require.NoError(t, err)
			actual, err := json.Marshal(rawConnectionBody{Properties: props})
			require.NoError(t, err)
			require.JSONEq(t, string(expected), string(actual))
		})
	}
}

func TestConnectionCreatePropertiesRawAuth(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct{ cli, arm string }{
		{"oauth2", "OAuth2"}, {"user-entra-token", "UserEntraToken"},
		{"project-managed-identity", "ProjectManagedIdentity"}, {"agentic-identity", "AgenticIdentityToken"},
	} {
		t.Run(tt.cli, func(t *testing.T) {
			t.Parallel()
			flags := &connectionCreateFlags{kind: "remote-a2a", target: "https://example.test", authType: tt.cli}
			if tt.cli == "oauth2" {
				flags.connectorName = "github"
			} else {
				flags.audience = "audience"
			}
			props, err := connectionCreateProperties(flags)
			require.NoError(t, err)
			require.Equal(t, tt.arm, props.AuthType)
			require.Equal(t, "RemoteA2A", props.Category)
			require.Equal(t, flags.target, props.Target)
			require.Equal(t, flags.audience, props.Audience)
			require.NoError(t, validateConnectionProperties(props))
			if tt.cli == "oauth2" {
				require.NotNil(t, props.Credentials)
				data, err := json.Marshal(props)
				require.NoError(t, err)
				require.Contains(t, string(data), `"credentials":{}`)
			} else {
				require.Nil(t, props.Credentials)
			}
		})
	}
}

func TestConnectionCreatePropertiesPreservesOAuthValues(t *testing.T) {
	t.Parallel()

	flags := &connectionCreateFlags{ //nolint:gosec // Synthetic OAuth credentials for preservation assertions.
		kind: "remote-tool", target: "https://example.test", authType: "oauth2",
		authorizationURL: "https://example.test/auth", tokenURL: "https://example.test/token",
		refreshURL: "https://example.test/refresh", scopes: []string{"read", "write"},
		clientID: " client ", clientSecret: " secret ", metadata: []string{"owner=platform"},
	}
	props, err := connectionCreateProperties(flags)
	require.NoError(t, err)
	require.NoError(t, validateConnectionProperties(props))
	require.Equal(t, flags.authorizationURL, props.AuthorizationURL)
	require.Equal(t, flags.tokenURL, props.TokenURL)
	require.Equal(t, flags.refreshURL, props.RefreshURL)
	require.Equal(t, flags.scopes, props.Scopes)
	require.Equal(t, flags.clientID, props.Credentials.clientIDOrEmpty())
	require.Equal(t, flags.clientSecret, props.Credentials.clientSecretOrEmpty())
	require.Equal(t, map[string]string{"owner": "platform"}, props.Metadata)
	props.Scopes[0] = "changed"
	require.Equal(t, "read", flags.scopes[0], "mapping must not alias the flags' scopes")
}

func TestConnectionCreateActionRejectsInvalidInputBeforeContextResolution(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		flags connectionCreateFlags
		code  string
		field string
	}{
		{"category", connectionCreateFlags{}, exterrors.CodeMissingConnectionField, "category"},
		{"target", connectionCreateFlags{kind: "remote-tool"}, exterrors.CodeMissingConnectionField, "target"},
		{
			"api key", connectionCreateFlags{kind: "remote-tool", target: "https://example.test", authType: "api-key"},
			exterrors.CodeMissingConnectionField, "credentials.key",
		},
		{
			"custom keys",
			connectionCreateFlags{kind: "remote-tool", target: "https://example.test", authType: "custom-keys"},
			exterrors.CodeMissingConnectionField, "credentials",
		},
		{
			"malformed custom pair",
			connectionCreateFlags{authType: "custom-keys", customKeys: []string{"secret-no-equals"}},
			exterrors.CodeMissingConnectionField, "credentials.keys",
		},
		{
			"unsupported CLI type", connectionCreateFlags{authType: "ManagedIdentity"},
			exterrors.CodeInvalidAuthType, "auth type",
		},
		{
			"client ID non OAuth", connectionCreateFlags{authType: "none", clientID: "secret-client"},
			exterrors.CodeConflictingArguments, "--client-id",
		},
		{
			"client secret non OAuth", connectionCreateFlags{authType: "none", clientSecret: "secret-client"},
			exterrors.CodeConflictingArguments, "--client-secret",
		},
		{
			"explicit empty client ID", connectionCreateFlags{authType: "none", clientIDChanged: true},
			exterrors.CodeConflictingArguments, "--client-id",
		},
		{
			"explicit empty client secret", connectionCreateFlags{authType: "none", secretChanged: true},
			exterrors.CodeConflictingArguments, "--client-secret",
		},
		{
			"OAuth missing", connectionCreateFlags{kind: "remote-tool", target: "https://example.test", authType: "oauth2"},
			exterrors.CodeMissingConnectionField, "connectorName",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := connectionCreateProperties(&tt.flags)
			if err != nil {
				requireConnectionValidationError(t, err, tt.code, tt.field)
			}
			action := &ConnectionCreateAction{flags: &tt.flags}
			localErr := requireConnectionValidationError(t, action.Run(t.Context()), tt.code, tt.field)
			require.NotContains(t, localErr.Error(), "secret-no-equals")
			require.NotContains(t, localErr.Error(), "secret-client")
		})
	}
}
