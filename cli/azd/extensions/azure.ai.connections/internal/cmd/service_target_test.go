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
	var captured *connectionCreateFlags
	target := &connectionServiceTarget{upsert: func(_ context.Context, flags *connectionCreateFlags) error {
		captured = flags
		return nil
	}}

	var progressMsgs []string
	progress := func(msg string) { progressMsgs = append(progressMsgs, msg) }

	res, err := target.Deploy(t.Context(), svc, nil, nil, progress)
	require.NoError(t, err)
	require.NotNil(t, res)
	require.NotNil(t, captured)
	assert.Equal(t, "search-conn", captured.name)
	assert.Equal(t, "RemoteTool", captured.kind)
	assert.Equal(t, "https://example.test/mcp", captured.target)
	assert.Equal(t, "custom-keys", captured.authType)
	assert.Equal(t, []string{"x-api-key=secret"}, captured.customKeys)
	assert.Equal(t, []string{"region=test"}, captured.metadata)
	assert.True(t, captured.force)
	assert.True(t, captured.suppressOutput)
	require.Len(t, progressMsgs, 1)
	assert.Equal(t, "Upserting connection \"search-conn\"", progressMsgs[0])
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
