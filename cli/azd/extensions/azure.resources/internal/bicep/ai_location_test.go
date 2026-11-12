// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package bicep

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"azure.resources/internal/resources"
	"azure.resources/internal/resources/scaffold"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/structpb"
)

func aiConfig() resources.Config {
	return resources.Config{"foundry": {
		Type: resources.AIProject,
		Models: []resources.AiServicesModel{
			{Name: "chat", Sku: resources.AiServicesModelSku{UsageName: "chat-quota", Capacity: 10}},
			{Name: "other-chat", Sku: resources.AiServicesModelSku{UsageName: "chat-quota", Capacity: 20}},
			{Name: "embedding", Sku: resources.AiServicesModelSku{UsageName: "embedding-quota", Capacity: 5}},
		},
	}}
}

func aiOptions(t *testing.T, config resources.Config) *azdext.ProvisioningOptions {
	t.Helper()
	raw, err := config.Raw()
	require.NoError(t, err)
	value, err := structpb.NewStruct(raw)
	require.NoError(t, err)
	return &azdext.ProvisioningOptions{Path: "testdata", Config: value}
}

func newAIProvider(t *testing.T, client *azdext.AzdClient, name string) *Provider {
	t.Helper()
	options := aiOptions(t, aiConfig())
	options.Name = name
	resolver := resources.NewParameterResolver(client)
	require.NoError(t, resolver.Configure(options))
	p := New(client, resolver)
	options.Path = t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(options.Path, "main.parameters.json"),
		[]byte(`{"parameters": {}}`), 0o600))
	require.NoError(t, p.Initialize(t.Context(), ".", options))
	t.Cleanup(func() { require.NoError(t, p.Close()) })
	p.scope = &scope{subscriptionID: "sub", location: "eastus"}
	p.azure = &fakeBackend{deployment: deployedFixture()}
	p.compile = func(context.Context, string) ([]byte, error) {
		// Prompt requirements must come from resource config, even with no Bicep metadata.
		return []byte(`{
			"$schema": "https://schema.management.azure.com/schemas/2018-05-01/subscriptionDeploymentTemplate.json#",
			"parameters": {"aiDeploymentsLocation": {"type": "string"}}
		}`), nil
	}
	return p
}

func TestAIParametersUseResourceConfiguration(t *testing.T) {
	prompts := &promptServer{aiLocation: "westus3"}
	client := newHostClient(t, &environmentServer{values: map[string]string{}}, prompts)
	p := newAIProvider(t, client, "")
	parameters, err := p.Parameters(t.Context())
	require.NoError(t, err)
	require.Len(t, parameters, 1)
	require.Equal(t, "aiDeploymentsLocation", parameters[0].Name)
	require.Equal(t, "westus3", parameters[0].Value)
	require.True(t, parameters[0].LocalPrompt)
	require.True(t, parameters[0].UsingEnvVarMapping)
	require.Equal(t, []string{scaffold.EntryEnvKey("", "AZURE_AI_DEPLOYMENTS_LOCATION")}, parameters[0].EnvVarMapping)
	require.Equal(t, map[string]any{"value": "westus3"}, p.params["aiDeploymentsLocation"])
	require.Len(t, prompts.aiRequests, 1)

	repeated, err := p.Parameters(t.Context())
	require.NoError(t, err)
	require.Equal(t, parameters, repeated)
	require.Len(t, prompts.aiRequests, 1)
}

func TestAILocationPersistsAcrossProviderInstances(t *testing.T) {
	prompts := &promptServer{aiLocation: "westus3"}
	env := &environmentServer{values: map[string]string{
		"AZURE_SUBSCRIPTION_ID": "sub", "AZURE_LOCATION": "eastus",
	}}
	client := newHostClient(t, env, prompts)
	key := scaffold.EntryEnvKey("models", "AZURE_AI_DEPLOYMENTS_LOCATION")
	for i := range 2 {
		p := newAIProvider(t, client, "models")
		p.scope = nil
		parameters, err := p.Parameters(t.Context())
		require.NoError(t, err)
		require.Equal(t, "westus3", env.values[key])
		require.Equal(t, "westus3", parameters[0].Value)
		require.Equal(t, []string{key}, parameters[0].EnvVarMapping)
		require.True(t, parameters[0].UsingEnvVarMapping)
		require.Equal(t, i == 0, parameters[0].LocalPrompt)
		require.Len(t, prompts.aiRequests, 1)
		// A later non-interactive provision must never need the prompt.
		prompts.aiErr = status.Error(codes.FailedPrecondition, "interactive input required")
	}
}

func TestAIStateDoesNotPromptForDeploymentLocation(t *testing.T) {
	prompts := &promptServer{}
	client := newHostClient(t, &environmentServer{values: map[string]string{}}, prompts)
	p := newAIProvider(t, client, "")
	result, err := p.State(t.Context(), nil)
	require.NoError(t, err)
	require.NotNil(t, result.State)
	require.Empty(t, prompts.aiRequests)
}

func TestResourceResolverPreservesNormalBicepParameterResolution(t *testing.T) {
	prompts := &promptServer{aiLocation: "westus3"}
	client := newHostClient(t, &environmentServer{values: map[string]string{"AZURE_LOCATION": "eastus"}}, prompts)
	p := newAIProvider(t, client, "")
	p.compile = func(context.Context, string) ([]byte, error) {
		return []byte(`{
			"$schema": "https://schema.management.azure.com/schemas/2018-05-01/subscriptionDeploymentTemplate.json#",
			"parameters": {"aiDeploymentsLocation": {"type": "string"}, "location": {"type": "string"}}
		}`), nil
	}
	require.NoError(t, os.WriteFile(filepath.Join(p.path, "main.parameters.json"),
		[]byte(`{"parameters": {"location": {"value": "${AZURE_LOCATION}"}}}`), 0o600))

	parameters, err := p.Parameters(t.Context())
	require.NoError(t, err)
	require.Len(t, parameters, 2)
	require.Equal(t, "location", parameters[1].Name)
	require.Equal(t, "eastus", parameters[1].Value)
	require.Equal(t, []string{"AZURE_LOCATION"}, parameters[1].EnvVarMapping)
	require.True(t, parameters[1].UsingEnvVarMapping)
	require.False(t, parameters[1].LocalPrompt)
	require.Len(t, prompts.aiRequests, 1)
}

func TestAIDestroyDoesNotPromptForDeploymentLocation(t *testing.T) {
	prompts := &promptServer{}
	client := newHostClient(t, &environmentServer{values: map[string]string{}}, prompts)
	p := newAIProvider(t, client, "")
	_, err := p.Destroy(t.Context(), &azdext.ProvisioningDestroyOptions{Force: true}, nil)
	require.NoError(t, err)
	require.Empty(t, prompts.aiRequests)
}
