// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"encoding/json"
	"maps"
	"math"
	"os"
	"path/filepath"
	"testing"

	"azure.ai.connections/internal/definition"
	"azure.ai.connections/internal/exterrors"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/foundry"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.yaml.in/yaml/v3"
	"google.golang.org/protobuf/types/known/structpb"
)

func TestParseConnectionServiceConfigRejectsUnknownFields(t *testing.T) {
	t.Parallel()
	for _, source := range []string{"inline", "config", "json", "yaml"} {
		for _, field := range []string{"authTyp", "credential", "unexpected"} {
			t.Run(source+"/"+field, func(t *testing.T) {
				t.Parallel()
				target, svc := connectionConfigParsingFixture(t, map[string]any{
					"category": "RemoteTool", "target": "https://example.test",
					field:         "synthetic-private-value",
					"credentials": map[string]any{"key": "synthetic-private-value"},
				}, source)

				input, err := target.parseConnectionServiceConfig(t.Context(), svc)
				require.Nil(t, input, "misspelled authType must not produce an unauthenticated definition")
				localErr := requireConnectionValidationError(t, err, exterrors.CodeInvalidParameter, field)
				assert.IsType(t, &azdext.LocalError{}, err)
				assert.Contains(t, localErr.Message, svc.GetName())
				assert.NotContains(t, localErr.Message+localErr.Suggestion, "synthetic-private-value")
			})
		}
	}
}

func TestParseConnectionServiceConfigRejectsCoreFields(t *testing.T) {
	t.Parallel()
	for _, source := range []string{"inline", "config", "json", "yaml"} {
		for _, field := range []string{
			"env", "host", "uses", "project", "language", "image", "docker", "k8s",
			"infra", "hooks", "resourceGroup", "resourceName", "apiVersion", "dist",
			"module", "config", "condition", "remoteBuild",
		} {
			t.Run(source+"/"+field, func(t *testing.T) {
				t.Parallel()
				target, svc := connectionConfigParsingFixture(t, map[string]any{
					"category": "RemoteTool", "target": "https://example.test", "authType": "None",
					field: nil, // Presence, even with a null value, must not be silently discarded.
				}, source)

				input, err := target.parseConnectionServiceConfig(t.Context(), svc)
				require.Nil(t, input)
				localErr := requireConnectionValidationError(t, err, exterrors.CodeInvalidParameter, field)
				assert.Contains(t, localErr.Message, svc.GetName())
				assert.Contains(t, localErr.Message, "core-owned")
				assert.Contains(t, localErr.Suggestion, field)
				assert.Contains(t, localErr.Suggestion, "azure.yaml")
				assert.Contains(t, localErr.Suggestion, "azd core can evaluate")
			})
		}
	}
}

func TestParseConnectionServiceConfigPreservesDefinitionFieldsAndSchema(t *testing.T) {
	t.Parallel()
	for _, source := range []string{"inline", "config", "json", "yaml"} {
		t.Run(source, func(t *testing.T) {
			t.Parallel()
			credentials := map[string]any{
				"key": "${API_KEY}", "clientSecret": "${{connections.source.credentials.key}}",
				"nested": map[string]any{
					"authTyp": "arbitrary-key", "env": map[string]any{}, "host": "nested-host",
					"uses": []any{"preserved", true, float64(3), nil}, "$schema": "nested-schema",
				},
			}
			values := map[string]any{ //nolint:gosec // Synthetic references used for parser preservation tests.
				"$schema": "https://example.test/connection.schema.json",
				"name":    "Logical Connection", "category": "RemoteTool", "target": "${TARGET}",
				"authType": "OAuth2", "credentials": credentials,
				"metadata": map[string]any{"authTyp": "custom", "env": "metadata", "$schema": "metadata-schema"},
				"audience": "${AUDIENCE}", "authorizationUrl": "${AUTHORIZATION_URL}",
				"tokenUrl": "${TOKEN_URL}", "refreshUrl": "${REFRESH_URL}",
				"scopes": []any{"read", "${SCOPE}"}, "connectorName": "${CONNECTOR}",
			}
			target, svc := connectionConfigParsingFixture(t, values, source)

			input, err := target.parseConnectionServiceConfig(t.Context(), svc)
			require.NoError(t, err)
			assert.Equal(t, &definition.Definition{ //nolint:gosec // Synthetic references, not credentials.
				Name: "Logical Connection", Category: "RemoteTool", Target: "${TARGET}", AuthType: "OAuth2",
				Credentials: credentials,
				Metadata:    map[string]string{"authTyp": "custom", "env": "metadata", "$schema": "metadata-schema"},
				Audience:    "${AUDIENCE}", AuthorizationURL: "${AUTHORIZATION_URL}",
				TokenURL: "${TOKEN_URL}", RefreshURL: "${REFRESH_URL}",
				Scopes: []string{"read", "${SCOPE}"}, ConnectorName: "${CONNECTOR}",
			}, input, "parsing must preserve all definition fields without expanding environment expressions")
		})
	}
}

func TestParseConnectionServiceConfigResolvesCredentialFileRefs(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	definitionDir := filepath.Join(root, "definitions")
	require.NoError(t, os.MkdirAll(definitionDir, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(definitionDir, "connection.yaml"), []byte(`
category: RemoteTool
target: https://example.test
authType: ServicePrincipal
credentials:
  $ref: ./credentials.yaml
`), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(definitionDir, "credentials.yaml"), []byte(`
clientId: ${CLIENT_ID}
clientSecret: ${CLIENT_SECRET}
nested:
  env: {}
  host: nested-host
  authTyp: arbitrary-key
`), 0o600))
	props, err := structpb.NewStruct(map[string]any{
		"$ref": "./definitions/connection.yaml", "name": "Overlay Name",
	})
	require.NoError(t, err)
	target := &connectionServiceTarget{projectClient: &recordingProjectConfigReader{path: root}}

	input, err := target.parseConnectionServiceConfig(t.Context(), &azdext.ServiceConfig{
		Name: "search", Host: aiConnectionHost, AdditionalProperties: props,
	})
	require.NoError(t, err)
	assert.Equal(t, "Overlay Name", input.Name)
	assert.Equal(t, "ServicePrincipal", input.AuthType)
	assert.Equal(t, map[string]any{
		"clientId": "${CLIENT_ID}", "clientSecret": "${CLIENT_SECRET}",
		"nested": map[string]any{"env": map[string]any{}, "host": "nested-host", "authTyp": "arbitrary-key"},
	}, input.Credentials)
}

func TestParseConnectionServiceConfigRejectsReferencedEnvWithAmbientCredentials(t *testing.T) {
	t.Setenv("CONNECTION_PARSE_TEST_KEY", "synthetic-process-key")
	for _, source := range []string{"json", "yaml"} {
		for _, tt := range []struct {
			name string
			env  map[string]any
		}{
			{"empty", map[string]any{}},
			{"populated", map[string]any{"CONNECTION_PARSE_TEST_KEY": "synthetic-declared-key"}},
		} {
			t.Run(source+"/"+tt.name, func(t *testing.T) {
				target, svc := connectionConfigParsingFixture(t, map[string]any{
					"category": "RemoteTool", "target": "https://example.test", "authType": "ApiKey",
					"credentials": map[string]any{"key": "${CONNECTION_PARSE_TEST_KEY}"}, "env": tt.env,
				}, source)

				input, err := target.parseConnectionServiceConfig(t.Context(), svc)
				require.Nil(t, input, "referenced env must fail before ambient credentials could be selected")
				localErr := requireConnectionValidationError(t, err, exterrors.CodeInvalidParameter, "env")
				assert.Contains(t, localErr.Suggestion, "azure.yaml")
				assert.NotContains(t, localErr.Message+localErr.Suggestion, "synthetic-process-key")
				assert.NotContains(t, localErr.Message+localErr.Suggestion, "synthetic-declared-key")
			})
		}
	}
}

func TestParseConnectionServiceConfigAllowsCoreEnvironment(t *testing.T) {
	t.Parallel()
	for _, source := range []string{"inline", "config", "json", "yaml"} {
		t.Run(source, func(t *testing.T) {
			t.Parallel()
			target, svc := connectionConfigParsingFixture(t, map[string]any{
				"category": "RemoteTool", "target": "https://example.test", "authType": "ApiKey",
				"credentials": map[string]any{"key": "${API_KEY}"},
			}, source)
			for _, environment := range []map[string]string{{}, {"API_KEY": "core-evaluated-value"}} {
				svc.Environment = maps.Clone(environment)
				input, err := target.parseConnectionServiceConfig(t.Context(), svc)
				require.NoError(t, err)
				assert.Equal(t, "${API_KEY}", input.Credentials["key"])
				assert.Equal(t, environment, svc.GetEnvironment(), "core's evaluated environment must remain untouched")
			}
		})
	}
}

func TestParseConnectionServiceConfigTypeErrorsDoNotLeakValues(t *testing.T) {
	t.Parallel()
	for _, source := range []string{"inline", "config", "json", "yaml"} {
		for _, tt := range []struct {
			field string
			value any
		}{
			{"authType", []any{"synthetic-private-value"}},
			{"credentials", "synthetic-private-value"},
			{"target", map[string]any{"key": "synthetic-private-value"}},
			{"scopes", "synthetic-private-value"},
			{"metadata", map[string]any{"private": float64(987654321)}},
		} {
			t.Run(source+"/"+tt.field, func(t *testing.T) {
				t.Parallel()
				values := map[string]any{
					"category": "RemoteTool", "target": "https://example.test", "authType": "ApiKey",
					"credentials": map[string]any{"key": "synthetic-private-value"},
				}
				values[tt.field] = tt.value
				target, svc := connectionConfigParsingFixture(t, values, source)

				input, err := target.parseConnectionServiceConfig(t.Context(), svc)
				require.Nil(t, input)
				localErr := requireConnectionValidationError(t, err, exterrors.CodeInvalidParameter, tt.field)
				assert.Contains(t, localErr.Message, svc.GetName())
				assert.NotContains(t, localErr.Message+localErr.Suggestion, "synthetic-private-value")
				assert.NotContains(t, localErr.Message+localErr.Suggestion, "987654321")
			})
		}
	}
}

func TestParseConnectionServiceConfigPreservesStructuredReferenceErrors(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name   string
		values map[string]any
	}{
		{"missing file", map[string]any{"$ref": "./missing.yaml"}},
		{"invalid reference type", map[string]any{"$ref": true}},
		{"empty reference", map[string]any{"$ref": ""}},
		{"nested reference", map[string]any{"credentials": map[string]any{"$ref": "./missing.yaml"}}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			props, err := structpb.NewStruct(tt.values)
			require.NoError(t, err)
			_, expectedErr := foundry.ResolveFileRefs(props.AsMap(), root)
			expected := requireConnectionValidationError(t, expectedErr, foundry.CodeInvalidFileRef, "$ref")
			target := &connectionServiceTarget{projectClient: &recordingProjectConfigReader{path: root}}

			input, err := target.parseConnectionServiceConfig(t.Context(), &azdext.ServiceConfig{
				Name: "search", AdditionalProperties: props,
			})
			require.Nil(t, input)
			actual := requireConnectionValidationError(t, err, foundry.CodeInvalidFileRef, "$ref")
			assert.IsType(t, &azdext.LocalError{}, err, "the classified error must not be wrapped")
			assert.Equal(t, expected.Message, actual.Message)
			assert.Equal(t, expected.Category, actual.Category)
			assert.Equal(t, expected.Code, actual.Code)
			assert.Equal(t, expected.Suggestion, actual.Suggestion)
			assert.Equal(t, expected.Error(), err.Error())
		})
	}
}

func TestDecodeResolvedConnectionDefinitionDoesNotMutateResolvedMap(t *testing.T) {
	t.Parallel()
	values := map[string]any{
		"$schema": "https://example.test/connection.schema.json", "name": "search",
		"credentials": map[string]any{"nested": map[string]any{"$schema": "credential-metadata", "env": "value"}},
	}
	before, err := json.Marshal(values)
	require.NoError(t, err)
	input, err := decodeResolvedConnectionDefinition("search", values)
	require.NoError(t, err)
	assert.Equal(t, values["credentials"], input.Credentials)
	after, err := json.Marshal(values)
	require.NoError(t, err)
	assert.JSONEq(t, string(before), string(after))
}

func TestDecodeResolvedConnectionDefinitionRejectsNonJSONValues(t *testing.T) {
	t.Parallel()
	input, err := decodeResolvedConnectionDefinition("search", map[string]any{
		"credentials": map[string]any{"private": "synthetic-private-value", "invalid": math.Inf(1)},
	})
	require.Nil(t, input)
	localErr := requireConnectionValidationError(t, err, exterrors.CodeInvalidParameter, "search")
	assert.NotContains(t, localErr.Message+localErr.Suggestion, "synthetic-private-value")
}

func connectionConfigParsingFixture(
	t *testing.T,
	values map[string]any,
	source string,
) (*connectionServiceTarget, *azdext.ServiceConfig) {
	t.Helper()
	root := t.TempDir()
	if source == "json" || source == "yaml" {
		var content []byte
		var err error
		if source == "json" {
			content, err = json.Marshal(values)
		} else {
			content, err = yaml.Marshal(values)
		}
		require.NoError(t, err)
		name := "connection." + source
		require.NoError(t, os.WriteFile(filepath.Join(root, name), content, 0o600))
		values = map[string]any{"$ref": "./" + name}
	}
	props, err := structpb.NewStruct(values)
	require.NoError(t, err)
	svc := &azdext.ServiceConfig{Name: "search", Host: aiConnectionHost}
	if source == "config" {
		svc.Config = props
	} else {
		svc.AdditionalProperties = props
	}
	return &connectionServiceTarget{projectClient: &recordingProjectConfigReader{path: root}}, svc
}
