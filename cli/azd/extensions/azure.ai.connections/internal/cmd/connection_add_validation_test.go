// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"strings"
	"testing"

	"azure.ai.connections/internal/definition"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateConnectionDefinitionForAuthoring(t *testing.T) {
	tests := []struct {
		name       string
		input      func() *definition.Definition
		wantVars   []string
		wantErr    bool
		errNotText string
	}{
		{
			name: "static definition",
			input: func() *definition.Definition {
				return validConnectionAuthoringDefinition("None")
			},
			wantVars: []string{},
		},
		{
			name: "environment references are sorted and deduplicated",
			input: func() *definition.Definition {
				input := validConnectionAuthoringDefinition("None")
				input.Target = "${TARGET}"
				input.Credentials = map[string]any{
					"key":      "${API_KEY}",
					"nested":   []any{"${TARGET}", map[string]any{"secret": "${SECRET}"}},
					"optional": "${OPTIONAL:-}",
				}
				input.Metadata = map[string]string{"endpoint": "${META}"}
				return input
			},
			wantVars: []string{"API_KEY", "META", "SECRET", "TARGET"},
		},
		{
			name: "target template defers nonblank validation",
			input: func() *definition.Definition {
				input := validConnectionAuthoringDefinition("None")
				input.Target = "${TARGET:-}"
				return input
			},
			wantVars: []string{},
		},
		{
			name: "blank category is rejected despite target template",
			input: func() *definition.Definition {
				input := validConnectionAuthoringDefinition("None")
				input.Category = "  "
				input.Target = "${TARGET}"
				return input
			},
			wantErr: true,
		},
		{
			name: "blank literal target is rejected",
			input: func() *definition.Definition {
				input := validConnectionAuthoringDefinition("None")
				input.Target = " \t"
				return input
			},
			wantErr: true,
		},
		{
			name: "malformed template error omits its contents",
			input: func() *definition.Definition {
				input := validConnectionAuthoringDefinition("None")
				input.Credentials = map[string]any{"key": "${SENSITIVE"}
				return input
			},
			wantErr:    true,
			errNotText: "SENSITIVE",
		},
		{
			name: "blank auth type defaults to none",
			input: func() *definition.Definition {
				return validConnectionAuthoringDefinition("")
			},
			wantVars: []string{},
		},
		{
			name: "unsupported auth type is rejected",
			input: func() *definition.Definition {
				return validConnectionAuthoringDefinition("Unsupported")
			},
			wantErr: true,
		},
		{
			name: "api key accepts an unresolved template",
			input: func() *definition.Definition {
				input := validConnectionAuthoringDefinition("ApiKey")
				input.Credentials = map[string]any{"key": "${API_KEY}"}
				return input
			},
			wantVars: []string{"API_KEY"},
		},
		{
			name: "api key accepts an optional unresolved template",
			input: func() *definition.Definition {
				input := validConnectionAuthoringDefinition("ApiKey")
				input.Credentials = map[string]any{"key": "${API_KEY:-}"}
				return input
			},
			wantVars: []string{},
		},
		{
			name: "api key rejects a missing key",
			input: func() *definition.Definition {
				return validConnectionAuthoringDefinition("ApiKey")
			},
			wantErr: true,
		},
		{
			name: "api key rejects a non-string key",
			input: func() *definition.Definition {
				input := validConnectionAuthoringDefinition("ApiKey")
				input.Credentials = map[string]any{"key": 12}
				return input
			},
			wantErr: true,
		},
		{
			name: "api key rejects a blank literal",
			input: func() *definition.Definition {
				input := validConnectionAuthoringDefinition("ApiKey")
				input.Credentials = map[string]any{"key": " \t"}
				return input
			},
			wantErr: true,
		},
		{
			name: "custom keys accepts nested string values",
			input: func() *definition.Definition {
				input := validConnectionAuthoringDefinition("CustomKeys")
				input.Credentials = map[string]any{
					"keys": map[string]any{"customerId": "${SEARCH_VALUE}"},
				}
				return input
			},
			wantVars: []string{"SEARCH_VALUE"},
		},
		{
			name: "nested custom keys retain deployment validation semantics",
			input: func() *definition.Definition {
				input := validConnectionAuthoringDefinition("CustomKeys")
				input.Credentials = map[string]any{
					"keys":    map[string]any{"apiKey": "literal-key"},
					"ignored": 12,
				}
				return input
			},
			wantVars: []string{},
		},
		{
			name: "custom keys accepts legacy flat keys entry",
			input: func() *definition.Definition {
				input := validConnectionAuthoringDefinition("CustomKeys")
				input.Credentials = map[string]any{"keys": "literal-key"}
				return input
			},
			wantVars: []string{},
		},
		{
			name: "custom keys rejects an empty nested map",
			input: func() *definition.Definition {
				input := validConnectionAuthoringDefinition("CustomKeys")
				input.Credentials = map[string]any{"keys": map[string]any{}}
				return input
			},
			wantErr: true,
		},
		{
			name: "custom keys rejects a non-string nested value",
			input: func() *definition.Definition {
				input := validConnectionAuthoringDefinition("CustomKeys")
				input.Credentials = map[string]any{
					"keys": map[string]any{"apiKey": 12},
				}
				return input
			},
			wantErr: true,
		},
		{
			name: "literal audience is rejected for none auth",
			input: func() *definition.Definition {
				input := validConnectionAuthoringDefinition("None")
				input.Audience = "https://example.test"
				return input
			},
			wantErr: true,
		},
		{
			name: "audience template defers auth restriction",
			input: func() *definition.Definition {
				input := validConnectionAuthoringDefinition("None")
				input.Audience = "${AUDIENCE}"
				return input
			},
			wantVars: []string{"AUDIENCE"},
		},
		{
			name: "literal oauth URL is rejected for non-oauth auth",
			input: func() *definition.Definition {
				input := validConnectionAuthoringDefinition("None")
				input.AuthorizationURL = "https://example.test/authorize"
				return input
			},
			wantErr: true,
		},
		{
			name: "oauth URL template defers auth restriction",
			input: func() *definition.Definition {
				input := validConnectionAuthoringDefinition("None")
				input.AuthorizationURL = "${AUTH_URL:-}"
				return input
			},
			wantVars: []string{},
		},
		{
			name: "non-oauth scopes are always rejected",
			input: func() *definition.Definition {
				input := validConnectionAuthoringDefinition("None")
				input.Scopes = []string{"${SCOPE:-}"}
				return input
			},
			wantErr: true,
		},
		{
			name: "managed oauth accepts a literal connector",
			input: func() *definition.Definition {
				input := validConnectionAuthoringDefinition("OAuth2")
				input.ConnectorName = "connector"
				return input
			},
			wantVars: []string{},
		},
		{
			name: "managed oauth rejects credential entries",
			input: func() *definition.Definition {
				input := validConnectionAuthoringDefinition("OAuth2")
				input.ConnectorName = "connector"
				input.Credentials = map[string]any{"clientId": "${CLIENT_ID}"}
				return input
			},
			wantErr: true,
		},
		{
			name: "managed oauth rejects whitespace connector",
			input: func() *definition.Definition {
				input := validConnectionAuthoringDefinition("OAuth2")
				input.ConnectorName = " \t"
				return input
			},
			wantErr: true,
		},
		{
			name: "managed oauth defers a connector template",
			input: func() *definition.Definition {
				input := validConnectionAuthoringDefinition("OAuth2")
				input.ConnectorName = "${CONNECTOR}"
				return input
			},
			wantVars: []string{"CONNECTOR"},
		},
		{
			name: "templated connector accepts complete byo fields",
			input: func() *definition.Definition {
				input := validConnectionAuthoringDefinition("OAuth2")
				input.ConnectorName = "${CONNECTOR:-}"
				input.AuthorizationURL = "https://example.test/authorize"
				input.TokenURL = "https://example.test/token"
				input.Credentials = map[string]any{
					"clientId":     "${CLIENT_ID}",
					"clientSecret": "${CLIENT_SECRET}",
				}
				return input
			},
			wantVars: []string{"CLIENT_ID", "CLIENT_SECRET"},
		},
		{
			name: "templated connector rejects fixed incomplete byo fields",
			input: func() *definition.Definition {
				input := validConnectionAuthoringDefinition("OAuth2")
				input.ConnectorName = "${CONNECTOR:-}"
				input.Credentials = map[string]any{"clientId": "${CLIENT_ID}"}
				return input
			},
			wantErr: true,
		},
		{
			name: "byo oauth requires all fixed fields",
			input: func() *definition.Definition {
				input := validConnectionAuthoringDefinition("OAuth2")
				input.AuthorizationURL = "https://example.test/authorize"
				input.TokenURL = "https://example.test/token"
				input.Credentials = map[string]any{"clientId": "${CLIENT_ID}"}
				return input
			},
			wantErr: true,
		},
		{
			name: "byo oauth accepts unresolved required values",
			input: func() *definition.Definition {
				input := validConnectionAuthoringDefinition("OAuth2")
				input.AuthorizationURL = "${AUTH_URL}"
				input.TokenURL = "${TOKEN_URL}"
				input.Credentials = map[string]any{
					"clientId":     "${CLIENT_ID}",
					"clientSecret": "${CLIENT_SECRET}",
				}
				return input
			},
			wantVars: []string{"AUTH_URL", "CLIENT_ID", "CLIENT_SECRET", "TOKEN_URL"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			variables, _, err := validateConnectionDefinitionForAuthoring(test.input())
			if test.wantErr {
				require.Error(t, err)
				if test.errNotText != "" {
					assert.NotContains(t, err.Error(), test.errNotText)
				}
				return
			}
			require.NoError(t, err)
			require.Equal(t, test.wantVars, variables)
		})
	}
}

func validConnectionAuthoringDefinition(authType string) *definition.Definition {
	return &definition.Definition{
		Category: "CognitiveSearch",
		Target:   "https://example.test",
		AuthType: authType,
	}
}

func TestValidateConnectionDefinitionForAuthoringDoesNotExposeTemplateText(t *testing.T) {
	input := validConnectionAuthoringDefinition("None")
	input.Target = "${MALFORMED_SECRET"
	_, _, err := validateConnectionDefinitionForAuthoring(input)
	require.Error(t, err)
	assert.False(t, strings.Contains(err.Error(), "MALFORMED_SECRET"))
}

func TestValidateConnectionDefinitionForAuthoringSeparatesOptionalReferences(t *testing.T) {
	input := validConnectionAuthoringDefinition("None")
	input.Target = "${OPTIONAL_TARGET:-https://example.test}"

	required, referenced, err := validateConnectionDefinitionForAuthoring(input)
	require.NoError(t, err)
	assert.Empty(t, required)
	assert.Equal(t, []string{"OPTIONAL_TARGET"}, referenced)
}

func TestValidateConnectionDefinitionForAuthoringDoesNotExposeMapKeys(t *testing.T) {
	credentials := validConnectionAuthoringDefinition("CustomKeys")
	credentials.Credentials = map[string]any{"PRIVATE_MARKER": "${BROKEN"}
	metadata := validConnectionAuthoringDefinition("None")
	metadata.Metadata = map[string]string{"PRIVATE_MARKER": "${BROKEN"}

	for name, input := range map[string]*definition.Definition{
		"credentials": credentials,
		"metadata":    metadata,
	} {
		t.Run(name, func(t *testing.T) {
			_, _, err := validateConnectionDefinitionForAuthoring(input)
			require.Error(t, err)
			assert.Contains(t, err.Error(), `"`+name+`"`)
			assert.NotContains(t, err.Error(), "PRIVATE_MARKER")
			assert.NotContains(t, err.Error(), "BROKEN")
		})
	}
}

func TestValidateConnectionDefinitionForAuthoringRejectsNonBlankTemplates(t *testing.T) {
	tests := []struct {
		name  string
		input func() *definition.Definition
	}{
		{
			name: "audience with literal prefix requires identity auth",
			input: func() *definition.Definition {
				input := validConnectionAuthoringDefinition("None")
				input.Audience = "https://example.test/${TENANT}"
				return input
			},
		},
		{
			name: "OAuth2 URL with literal prefix requires OAuth2 auth",
			input: func() *definition.Definition {
				input := validConnectionAuthoringDefinition("None")
				input.TokenURL = "https://login.example.test/${TENANT}/token"
				return input
			},
		},
		{
			name: "connectorName with literal prefix conflicts with BYO fields",
			input: func() *definition.Definition {
				input := validConnectionAuthoringDefinition("OAuth2")
				input.ConnectorName = "connector-${SUFFIX}"
				input.Scopes = []string{"read"}
				return input
			},
		},
		{
			name: "templated connectorName with literal URL prefix needs BYO fields",
			input: func() *definition.Definition {
				input := validConnectionAuthoringDefinition("OAuth2")
				input.ConnectorName = "${CONNECTOR}"
				input.TokenURL = "https://login.example.test/${TENANT}/token"
				return input
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, _, err := validateConnectionDefinitionForAuthoring(test.input())
			require.Error(t, err)
		})
	}
}

func TestValidateConnectionDefinitionForAuthoringDefersBlankableTemplates(t *testing.T) {
	input := validConnectionAuthoringDefinition("None")
	input.Audience = " ${AUDIENCE} "
	input.TokenURL = "${TOKEN_URL:-}"

	required, _, err := validateConnectionDefinitionForAuthoring(input)
	require.NoError(t, err)
	assert.Equal(t, []string{"AUDIENCE"}, required)
}
