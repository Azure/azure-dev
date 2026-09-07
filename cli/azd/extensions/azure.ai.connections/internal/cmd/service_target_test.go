// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"azure.ai.connections/internal/definition"

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
	assert.Equal(t, "CONNECTION_MY_CONNECTION_PROJECT_ENDPOINT", environment.setRequests[0].GetKey())
	assert.Empty(t, environment.setRequests[0].GetValue())
	assert.Equal(t, "staging", environment.setRequests[1].GetEnvName())
	assert.Equal(t, "CONNECTION_MY_CONNECTION_PROJECT_ENDPOINT", environment.setRequests[1].GetKey())
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
