// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"errors"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/structpb"
)

func TestDeployUpsertsConnectionFromServiceConfig(t *testing.T) {
	t.Parallel()

	props, err := structpb.NewStruct(map[string]any{
		"category": "RemoteTool",
		"target":   "https://example.test/mcp",
		"authType": "CustomKeys",
		"credentials": map[string]any{
			"keys": map[string]any{
				"x-api-key": "${CONNECTION_KEY}",
			},
		},
		"metadata": map[string]any{
			"region": "test",
		},
	})
	require.NoError(t, err)
	svc := &azdext.ServiceConfig{
		Name:                 "search-conn",
		Host:                 aiConnectionHost,
		AdditionalProperties: props,
		Environment:          map[string]string{"CONNECTION_KEY": "secret"},
	}
	var captured rawConnectionProperties
	var capturedName string
	var capturedEnvironment string
	target := &connectionServiceTarget{upsert: func(
		_ context.Context,
		environmentName string,
		name string,
		properties rawConnectionProperties,
	) error {
		capturedEnvironment = environmentName
		capturedName = name
		captured = properties
		return nil
	}}
	target.environment = "staging"

	var progressMsgs []string
	progress := func(msg string) { progressMsgs = append(progressMsgs, msg) }

	res, err := target.Deploy(t.Context(), svc, nil, nil, progress)
	require.NoError(t, err)
	require.NotNil(t, res)
	assert.Equal(t, "search-conn", capturedName)
	assert.Equal(t, "staging", capturedEnvironment)
	assert.Equal(t, "RemoteTool", captured.Category)
	assert.Equal(t, "https://example.test/mcp", captured.Target)
	assert.Equal(t, "CustomKeys", captured.AuthType)
	require.NotNil(t, captured.Credentials)
	assert.Equal(t, rawCredentials{"keys": map[string]any{"x-api-key": "secret"}}, *captured.Credentials)
	assert.Equal(t, map[string]string{"region": "test"}, captured.Metadata)
	require.Len(t, progressMsgs, 1)
	assert.Equal(t, "Upserting connection \"search-conn\"", progressMsgs[0])
}

func TestEnvironmentValuesUsesSelectedEnvironment(t *testing.T) {
	t.Parallel()

	environments := &recordingServiceEnvironmentReader{
		values: map[string]map[string]string{
			"default": {"VALUE": "wrong"},
			"staging": {"VALUE": "right"},
		},
	}
	target := &connectionServiceTarget{
		environment:   "staging",
		envClient:     environments,
		projectClient: missingServiceEnvReader{},
	}

	values, err := target.environmentValues(t.Context(), &azdext.ServiceConfig{Name: "connection"})
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"VALUE": "right"}, values)
	assert.Equal(t, 0, environments.currentCalls)
	assert.Equal(t, []string{"staging"}, environments.valuesRequests)
}

func TestConnectionServicePropertiesPreservesGenericAuthTypes(t *testing.T) {
	t.Parallel()

	authTypes := []string{
		"AAD", "PAT", "ServicePrincipal", "UsernamePassword",
		"AccessKey", "AccountKey", "SAS",
	}
	for _, authType := range authTypes {
		t.Run(authType, func(t *testing.T) {
			t.Parallel()

			props, err := structpb.NewStruct(map[string]any{
				"category": "AzureOpenAI",
				"target":   "https://example.test",
				"authType": authType,
				"credentials": map[string]any{
					"username": "${USERNAME}",
					"password": "${PASSWORD}",
					"options":  []any{"preserved", true, float64(3)},
				},
			})
			require.NoError(t, err)

			got, err := connectionServiceProperties(&azdext.ServiceConfig{
				Name:                 "generic",
				AdditionalProperties: props,
			}, map[string]string{"USERNAME": "user", "PASSWORD": "secret"})
			require.NoError(t, err)
			assert.Equal(t, authType, got.AuthType)
			require.NotNil(t, got.Credentials)
			assert.Equal(t, rawCredentials{
				"username": "user",
				"password": "secret",
				"options":  []any{"preserved", true, float64(3)},
			}, *got.Credentials)
		})
	}
}

func TestConnectionServicePropertiesPreservesEmptyOAuth2Credentials(t *testing.T) {
	t.Parallel()

	props, err := structpb.NewStruct(map[string]any{
		"category":      "RemoteTool",
		"target":        "https://example.test",
		"authType":      "OAuth2",
		"connectorName": "managed-connector",
	})
	require.NoError(t, err)

	got, err := connectionServiceProperties(&azdext.ServiceConfig{
		Name:                 "oauth",
		AdditionalProperties: props,
	}, nil)
	require.NoError(t, err)
	require.NotNil(t, got.Credentials)
	assert.Empty(t, *got.Credentials)
}

func TestParseConnectionServiceConfigFallsBackToLegacyConfig(t *testing.T) {
	t.Parallel()

	config, err := structpb.NewStruct(map[string]any{
		"category": "AzureOpenAI",
		"target":   "https://example.test",
	})
	require.NoError(t, err)

	input, err := parseConnectionServiceConfig(&azdext.ServiceConfig{Name: "legacy", Config: config})
	require.NoError(t, err)
	assert.Equal(t, "AzureOpenAI", input.Category)
	assert.Equal(t, "https://example.test", input.Target)
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

type recordingServiceEnvironmentReader struct {
	values         map[string]map[string]string
	currentCalls   int
	valuesRequests []string
}

func (r *recordingServiceEnvironmentReader) GetCurrent(
	context.Context,
	*azdext.EmptyRequest,
	...grpc.CallOption,
) (*azdext.EnvironmentResponse, error) {
	r.currentCalls++
	return nil, errors.New("GetCurrent must not be called for a selected environment")
}

func (r *recordingServiceEnvironmentReader) GetValues(
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

type missingServiceEnvReader struct{}

func (missingServiceEnvReader) GetServiceConfigValue(
	context.Context,
	*azdext.GetServiceConfigValueRequest,
	...grpc.CallOption,
) (*azdext.GetServiceConfigValueResponse, error) {
	return &azdext.GetServiceConfigValueResponse{}, nil
}
