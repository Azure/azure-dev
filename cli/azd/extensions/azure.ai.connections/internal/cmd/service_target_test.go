// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"net"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"azure.ai.connections/internal/definition"
	"azure.ai.connections/internal/exterrors"
	"azure.ai.connections/internal/foundry/projectctx"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/structpb"
)

func TestDeployUpsertsLogicalConnectionAndPublishesMarker(t *testing.T) {
	t.Parallel()

	props, err := structpb.NewStruct(map[string]any{
		"name":     "Private Registry",
		"category": "ContainerRegistry",
		"target":   "https://registry.example",
		"authType": "ServicePrincipal",
		"credentials": map[string]any{
			"clientId":     "${CLIENT_ID}",
			"clientSecret": "${CLIENT_SECRET}",
		},
	})
	require.NoError(t, err)
	svc := &azdext.ServiceConfig{
		Name:                 "PrivateRegistry",
		Host:                 aiConnectionHost,
		AdditionalProperties: props,
		Environment: map[string]string{
			"CLIENT_ID":     "client",
			"CLIENT_SECRET": "secret",
		},
	}
	var capturedName string
	var capturedProperties rawConnectionProperties
	var markerEnvironment, markerName, markerProject string
	target := &connectionServiceTarget{
		projectClient: &recordingProjectConfigReader{path: t.TempDir()},
		environment:   "staging",
		upsert: func(
			_ context.Context,
			environmentName string,
			name string,
			properties rawConnectionProperties,
		) (string, error) {
			assert.Equal(t, "staging", environmentName)
			capturedName = name
			capturedProperties = properties
			return "https://account.services.ai.azure.com/api/projects/project", nil
		},
		publishMarker: func(_ context.Context, environmentName, name, projectEndpoint string) error {
			markerEnvironment = environmentName
			markerName = name
			markerProject = projectEndpoint
			return nil
		},
	}

	var progressMsgs []string
	progress := func(msg string) { progressMsgs = append(progressMsgs, msg) }

	res, err := target.Deploy(t.Context(), svc, nil, nil, progress)
	require.NoError(t, err)
	require.NotNil(t, res)
	assert.Equal(t, "Private Registry", capturedName)
	assert.Equal(t, "ContainerRegistry", capturedProperties.Category)
	assert.Equal(t, "ServicePrincipal", capturedProperties.AuthType)
	require.NotNil(t, capturedProperties.Credentials)
	assert.Equal(t, rawCredentials{
		"clientId": "client", "clientSecret": "secret",
	}, *capturedProperties.Credentials)
	assert.Equal(t, "staging", markerEnvironment)
	assert.Equal(t, "PrivateRegistry", markerName)
	assert.Equal(t, "https://account.services.ai.azure.com/api/projects/project", markerProject)
	require.Len(t, progressMsgs, 1)
	assert.Contains(t, progressMsgs[0], "Private Registry")
}

func TestDeployValidatesResolvedDefinitionBeforeUpsert(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name   string
		fields map[string]any
		code   string
		field  string
	}{
		{"missing category", map[string]any{"category": ""}, exterrors.CodeMissingConnectionField, "category"},
		{"missing target", map[string]any{"target": ""}, exterrors.CodeMissingConnectionField, "target"},
		{"expanded blank target", map[string]any{"target": "${BLANK}"}, exterrors.CodeMissingConnectionField, "target"},
		{"unknown auth", map[string]any{"authType": "invalid"}, exterrors.CodeInvalidAuthType, "authType"},
		{"api key missing", map[string]any{"authType": "ApiKey"}, exterrors.CodeMissingConnectionField, "credentials.key"},
		{
			"api key expands to blank", map[string]any{
				"authType": "ApiKey", "credentials": map[string]any{"key": "${BLANK}"},
			}, exterrors.CodeMissingConnectionField, "credentials.key",
		},
		{
			"custom keys missing", map[string]any{"authType": "CustomKeys"},
			exterrors.CodeMissingConnectionField, "credentials",
		},
		{
			"custom nested keys empty", map[string]any{
				"authType": "CustomKeys", "credentials": map[string]any{"keys": map[string]any{}},
			}, exterrors.CodeMissingConnectionField, "credentials",
		},
		{"oauth missing", map[string]any{"authType": "OAuth2"}, exterrors.CodeMissingConnectionField, "OAuth2"},
		{
			"oauth conflicting modes", map[string]any{ //nolint:gosec // Synthetic OAuth values, not credentials.
				"authType": "OAuth2", "connectorName": "github", "tokenUrl": "https://example.test/token",
			}, exterrors.CodeConflictingArguments, "connectorName",
		},
		{
			"oauth client secret missing", map[string]any{ //nolint:gosec // Synthetic OAuth values, not credentials.
				"authType": "OAuth2", "authorizationUrl": "https://example.test/auth",
				"tokenUrl": "https://example.test/token", "credentials": map[string]any{"clientId": "client"},
			}, exterrors.CodeMissingConnectionField, "credentials.clientSecret",
		},
		{
			"managed oauth with arbitrary credentials", map[string]any{
				"authType": "OAuth2", "connectorName": "github",
				"credentials": map[string]any{"nested": map[string]any{"value": "synthetic-private-value"}},
			}, exterrors.CodeConflictingArguments, "connectorName",
		},
		{"oauth fields on None", map[string]any{"scopes": []any{"read"}}, exterrors.CodeConflictingArguments, "scopes"},
		{"audience on None", map[string]any{"audience": "audience"}, exterrors.CodeConflictingArguments, "audience"},
	} {
		for _, fromFile := range []bool{false, true} {
			name := tt.name + "/inline"
			if fromFile {
				name = tt.name + "/ref"
			}
			t.Run(name, func(t *testing.T) {
				t.Parallel()
				root := t.TempDir()
				values := map[string]any{"category": "RemoteTool", "target": "https://example.test", "authType": "None"}
				maps.Copy(values, tt.fields)
				// Synthetic credential-bearing metadata must never appear in validation errors.
				values["metadata"] = map[string]any{"private": "synthetic-private-value"}
				if fromFile {
					raw, err := json.Marshal(values)
					require.NoError(t, err)
					require.NoError(t, os.WriteFile(filepath.Join(root, "connection.json"), raw, 0o600))
					values = map[string]any{"$ref": "./connection.json"}
				}
				props, err := structpb.NewStruct(values)
				require.NoError(t, err)
				upserted, published := false, false
				target := &connectionServiceTarget{
					projectClient: &recordingProjectConfigReader{path: root},
					environment:   "staging",
					upsert: func(context.Context, string, string, rawConnectionProperties) (string, error) {
						upserted = true
						return "https://account.services.ai.azure.com/api/projects/project", nil
					},
					publishMarker: func(context.Context, string, string, string) error { published = true; return nil },
				}
				result, err := target.Deploy(t.Context(), &azdext.ServiceConfig{
					Name: "connection", Host: aiConnectionHost, AdditionalProperties: props,
					Environment: map[string]string{"BLANK": " \t"},
				}, nil, nil, nil)
				require.Nil(t, result)
				localErr := requireConnectionValidationError(t, err, tt.code, tt.field)
				assert.NotContains(t, localErr.Message+localErr.Suggestion, "synthetic-private-value")
				assert.False(t, upserted, "invalid definitions must fail before ARM context resolution or PUT")
				assert.False(t, published, "invalid definitions must not publish readiness")
			})
		}
	}
}

func TestDeployValidatesAfterReferenceOverlayAndExpansion(t *testing.T) {
	t.Parallel()
	for _, authType := range []string{
		"None", "ApiKey", "CustomKeys", "OAuth2", "ManagedIdentity", "ServicePrincipal",
	} {
		t.Run(authType, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			// The referenced file alone is incomplete; the overlay supplies required fields.
			require.NoError(t, os.WriteFile(filepath.Join(root, "connection.json"), []byte(`{"target":""}`), 0o600))
			credentials := map[string]any{
				"clientId": "${CLIENT_ID}", "clientSecret": "${CLIENT_SECRET}",
				"nested": []any{"${CLIENT_ID}", true, float64(3)},
			}
			values := map[string]any{
				"$ref": "./connection.json", "category": "RemoteTool", "target": "${TARGET}", "authType": authType,
			}
			switch authType {
			case "ApiKey":
				credentials["key"] = "${{connections.source.credentials.key}}"
			case "CustomKeys":
				credentials["keys"] = map[string]any{"x-key": "${CLIENT_SECRET}"}
			case "OAuth2":
				values["connectorName"] = "${CONNECTOR}"
				credentials = nil
			case "None":
				credentials = nil
			}
			if credentials != nil {
				values["credentials"] = credentials
			}
			props, err := structpb.NewStruct(values)
			require.NoError(t, err)
			var captured rawConnectionProperties
			upserted, published := false, false
			target := &connectionServiceTarget{
				projectClient: &recordingProjectConfigReader{path: root}, environment: "staging",
				upsert: func(
					_ context.Context, environment, name string, properties rawConnectionProperties,
				) (string, error) {
					upserted = true
					assert.Equal(t, "staging", environment)
					assert.Equal(t, "connection", name)
					captured = properties
					return "https://account.services.ai.azure.com/api/projects/project", nil
				},
				publishMarker: func(context.Context, string, string, string) error { published = true; return nil },
			}
			result, err := target.Deploy(t.Context(), &azdext.ServiceConfig{
				Name: "connection", Host: aiConnectionHost, AdditionalProperties: props,
				Environment: map[string]string{
					"TARGET": "https://example.test", "CLIENT_ID": "client",
					"CLIENT_SECRET": "secret", "CONNECTOR": "github",
				},
			}, nil, nil, nil)
			require.NoError(t, err)
			require.NotNil(t, result)
			assert.True(t, upserted)
			assert.True(t, published)
			assert.Equal(t, authType, captured.AuthType)
			assert.Equal(t, "https://example.test", captured.Target)
			if credentials != nil {
				require.NotNil(t, captured.Credentials)
				assert.Equal(t, []any{"client", true, float64(3)}, (*captured.Credentials)["nested"])
			}
			if authType == "ApiKey" {
				assert.Equal(t, "${{connections.source.credentials.key}}", (*captured.Credentials)["key"])
			}
			if authType == "CustomKeys" {
				assert.Equal(t, map[string]any{"x-key": "secret"}, (*captured.Credentials)["keys"])
			}
			if authType == "OAuth2" {
				assert.Equal(t, "github", captured.ConnectorName)
				require.NotNil(t, captured.Credentials)
				assert.Empty(t, *captured.Credentials, "managed connectors must send credentials: {}")
			}
		})
	}
}

func TestDeployMissingEnvironmentEndpointDoesNotUpsertOrPublish(t *testing.T) {
	const production = "https://production.services.ai.azure.com/api/projects/production"
	t.Setenv("FOUNDRY_PROJECT_ENDPOINT", production)
	t.Setenv("AZURE_AI_PROJECT_ENDPOINT", production)
	original := projectctx.ReadAzdHostedSourcesFunc
	projectctx.ReadAzdHostedSourcesFunc = func(context.Context, string) (projectctx.AzdHostedSources, error) {
		t.Error("deploy consulted the standalone cascade")
		// Stop a regressed implementation before it can issue a real Azure call.
		return projectctx.AzdHostedSources{}, errors.New("standalone cascade must not be used")
	}
	t.Cleanup(func() { projectctx.ReadAzdHostedSourcesFunc = original })

	for _, selection := range []string{"staging", ""} {
		t.Run("selection="+selection, func(t *testing.T) {
			expectedEnvironment := selection
			if expectedEnvironment == "" {
				expectedEnvironment = "default"
			}
			environment := &missingEndpointEnvironmentServer{t: t, name: expectedEnvironment}
			server := grpc.NewServer()
			azdext.RegisterEnvironmentServiceServer(server, environment)
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			require.NoError(t, err)
			go func() { _ = server.Serve(listener) }()
			t.Cleanup(func() { server.Stop(); _ = listener.Close() })
			t.Setenv("AZD_SERVER", listener.Addr().String())
			client, err := azdext.NewAzdClient(azdext.WithAddress(listener.Addr().String()))
			require.NoError(t, err)
			t.Cleanup(func() { client.Close() })

			// Keep the production upsert callback so this test covers the wiring
			// from Deploy through connection context to the strict resolver.
			target, ok := newConnectionServiceTarget(client, selection).(*connectionServiceTarget)
			require.True(t, ok)
			target.projectClient = &recordingProjectConfigReader{path: t.TempDir()}
			target.envClient = &recordingServiceEnvironmentClient{}
			published := false
			target.publishMarker = func(context.Context, string, string, string) error {
				published = true
				return nil
			}
			props, err := structpb.NewStruct(map[string]any{
				"category": "RemoteTool", "target": "https://example.test/mcp", "authType": "None",
			})
			require.NoError(t, err)
			result, err := target.Deploy(t.Context(), &azdext.ServiceConfig{
				Name: "search", Host: aiConnectionHost, AdditionalProperties: props,
				Environment: map[string]string{"FOUNDRY_PROJECT_ENDPOINT": production},
			}, nil, nil, nil)
			require.Error(t, err)
			require.Nil(t, result)
			var localErr *azdext.LocalError
			require.ErrorAs(t, err, &localErr)
			assert.Equal(t, exterrors.CodeMissingProjectEndpoint, localErr.Code)
			assert.Contains(t, localErr.Message, expectedEnvironment)
			assert.False(t, published)
			assert.Equal(t, int32(1), environment.calls.Load())
		})
	}
}

type missingEndpointEnvironmentServer struct {
	azdext.UnimplementedEnvironmentServiceServer
	t     *testing.T
	name  string
	calls atomic.Int32
}

func (s *missingEndpointEnvironmentServer) GetValues(
	_ context.Context, request *azdext.GetEnvironmentRequest,
) (*azdext.KeyValueListResponse, error) {
	s.calls.Add(1)
	assert.Equal(s.t, s.name, request.GetName())
	return &azdext.KeyValueListResponse{}, nil
}

func TestParseConnectionServiceConfigResolvesFileRefs(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "connection.yaml"), []byte(`
name: Search Connection
category: CognitiveSearch
target: https://from-file.example
authType: ApiKey
credentials:
  key: ${{connections.search.credentials.key}}
`), 0o600))
	props, err := structpb.NewStruct(map[string]any{
		"$ref":   "./connection.yaml",
		"target": "https://overlay.example",
	})
	require.NoError(t, err)
	target := &connectionServiceTarget{projectClient: &recordingProjectConfigReader{path: root}}

	input, err := target.parseConnectionServiceConfig(t.Context(), &azdext.ServiceConfig{
		Name:                 "SearchConnection",
		AdditionalProperties: props,
	})
	require.NoError(t, err)
	assert.Equal(t, "Search Connection", input.Name)
	assert.Equal(t, "CognitiveSearch", input.Category)
	assert.Equal(t, "https://overlay.example", input.Target)
	assert.Equal(t, "${{connections.search.credentials.key}}", input.Credentials["key"])
}

func TestEnvironmentValuesFallbackAndIsolation(t *testing.T) {
	t.Setenv("PROCESS_ONLY", "process")
	t.Setenv("PERSISTED", "process-must-not-win")
	environment := &recordingServiceEnvironmentClient{values: map[string]map[string]string{
		"staging": {"PERSISTED": "azd"},
	}}

	t.Run("omitted env uses selected azd environment then process", func(t *testing.T) {
		target := &connectionServiceTarget{
			projectClient: &recordingProjectConfigReader{},
			envClient:     environment,
		}
		values, err := target.environmentValues(
			t.Context(), &azdext.ServiceConfig{Name: "search"}, "staging",
		)
		require.NoError(t, err)
		assert.Equal(t, "azd", values["PERSISTED"])
		assert.Equal(t, "process", values["PROCESS_ONLY"])
		assert.Equal(t, []string{"staging"}, environment.valuesRequests)
	})

	t.Run("explicit empty env is isolated", func(t *testing.T) {
		target := &connectionServiceTarget{
			projectClient: &recordingProjectConfigReader{envDeclared: true},
			envClient:     environment,
		}
		values, err := target.environmentValues(
			t.Context(), &azdext.ServiceConfig{Name: "isolated"}, "staging",
		)
		require.NoError(t, err)
		assert.Empty(t, values)
	})
}

func TestConnectionServicePropertiesPreservesAuthAndCredentials(t *testing.T) {
	t.Parallel()

	for _, authType := range []string{
		"ApiKey", "CustomKeys", "AAD", "PAT", "ServicePrincipal",
		"UsernamePassword", "AccessKey", "AccountKey", "SAS",
	} {
		t.Run(authType, func(t *testing.T) {
			t.Parallel()
			properties, err := connectionServiceProperties("generic", &definition.Definition{
				Category: "AzureOpenAI",
				Target:   "https://example.test",
				AuthType: authType,
				Credentials: map[string]any{
					"username": "${USERNAME}",
					"nested":   []any{"preserved", true, float64(3)},
				},
			}, map[string]string{"USERNAME": "user"})
			require.NoError(t, err)
			assert.Equal(t, authType, properties.AuthType)
			require.NotNil(t, properties.Credentials)
			assert.Equal(t, rawCredentials{
				"username": "user",
				"nested":   []any{"preserved", true, float64(3)},
			}, *properties.Credentials)
		})
	}
}

func TestSetConnectionProjectMarkerCommitsToSelectedEnvironment(t *testing.T) {
	t.Parallel()

	environment := &recordingServiceEnvironmentClient{}
	target := &connectionServiceTarget{envClient: environment}
	err := target.setConnectionProjectMarker(
		t.Context(),
		"staging",
		"my connection",
		"https://account.services.ai.azure.com/api/projects/project/",
	)
	require.NoError(t, err)
	require.Len(t, environment.setRequests, 2)
	assert.Equal(t, "staging", environment.setRequests[0].GetEnvName())
	assert.Equal(t, "CONNECTION_V2_6D7920636F6E6E656374696F6E_PROJECT_ENDPOINT", environment.setRequests[0].GetKey())
	assert.Empty(t, environment.setRequests[0].GetValue())
	assert.Equal(t, "staging", environment.setRequests[1].GetEnvName())
	assert.Equal(t, "CONNECTION_V2_6D7920636F6E6E656374696F6E_PROJECT_ENDPOINT", environment.setRequests[1].GetKey())
	assert.Equal(t,
		"https://account.services.ai.azure.com/api/projects/project",
		environment.setRequests[1].GetValue(),
	)
}

// TestPackagePublish_AreNoOps verifies the remaining lifecycle methods a
// connection has no build/publish artifact for return empty results.
func TestPackagePublish_AreNoOps(t *testing.T) {
	t.Parallel()

	target := &connectionServiceTarget{}
	svc := &azdext.ServiceConfig{Name: "search-conn", Host: aiConnectionHost}

	pkg, err := target.Package(t.Context(), svc, nil, nil)
	require.NoError(t, err)
	assert.NotNil(t, pkg)

	pub, err := target.Publish(t.Context(), svc, nil, nil, nil, nil)
	require.NoError(t, err)
	assert.NotNil(t, pub)

	endpoints, err := target.Endpoints(t.Context(), svc, nil)
	require.NoError(t, err)
	assert.Nil(t, endpoints)
}

type recordingProjectConfigReader struct {
	path        string
	envDeclared bool
}

func (r *recordingProjectConfigReader) Get(
	context.Context,
	*azdext.EmptyRequest,
	...grpc.CallOption,
) (*azdext.GetProjectResponse, error) {
	return &azdext.GetProjectResponse{Project: &azdext.ProjectConfig{Path: r.path}}, nil
}

func (r *recordingProjectConfigReader) GetServiceConfigValue(
	context.Context,
	*azdext.GetServiceConfigValueRequest,
	...grpc.CallOption,
) (*azdext.GetServiceConfigValueResponse, error) {
	return &azdext.GetServiceConfigValueResponse{Found: r.envDeclared}, nil
}

type recordingServiceEnvironmentClient struct {
	values         map[string]map[string]string
	valuesRequests []string
	setRequests    []*azdext.SetEnvRequest
}

func (r *recordingServiceEnvironmentClient) GetCurrent(
	context.Context,
	*azdext.EmptyRequest,
	...grpc.CallOption,
) (*azdext.EnvironmentResponse, error) {
	return &azdext.EnvironmentResponse{Environment: &azdext.Environment{Name: "default"}}, nil
}

func (r *recordingServiceEnvironmentClient) GetValues(
	_ context.Context,
	request *azdext.GetEnvironmentRequest,
	_ ...grpc.CallOption,
) (*azdext.KeyValueListResponse, error) {
	r.valuesRequests = append(r.valuesRequests, request.GetName())
	response := &azdext.KeyValueListResponse{}
	for key, value := range r.values[request.GetName()] {
		response.KeyValues = append(response.KeyValues, &azdext.KeyValue{Key: key, Value: value})
	}
	return response, nil
}

func (r *recordingServiceEnvironmentClient) SetValue(
	_ context.Context,
	request *azdext.SetEnvRequest,
	_ ...grpc.CallOption,
) (*azdext.EmptyResponse, error) {
	r.setRequests = append(r.setRequests, request)
	return &azdext.EmptyResponse{}, nil
}
