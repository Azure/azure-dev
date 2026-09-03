// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
	target := &connectionServiceTarget{upsert: func(
		_ context.Context,
		name string,
		properties rawConnectionProperties,
	) error {
		capturedName = name
		captured = properties
		return nil
	}}

	var progressMsgs []string
	progress := func(msg string) { progressMsgs = append(progressMsgs, msg) }

	res, err := target.Deploy(t.Context(), svc, nil, nil, progress)
	require.NoError(t, err)
	require.NotNil(t, res)
	assert.Equal(t, "search-conn", capturedName)
	assert.Equal(t, "RemoteTool", captured.Category)
	assert.Equal(t, "https://example.test/mcp", captured.Target)
	assert.Equal(t, "CustomKeys", captured.AuthType)
	require.NotNil(t, captured.Credentials)
	assert.Equal(t, rawCredentials{"keys": map[string]any{"x-api-key": "secret"}}, *captured.Credentials)
	assert.Equal(t, map[string]string{"region": "test"}, captured.Metadata)
	require.Len(t, progressMsgs, 1)
	assert.Equal(t, "Upserting connection \"search-conn\"", progressMsgs[0])
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
