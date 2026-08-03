// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package provisioning

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"testing"

	"azure.ai.projects/internal/synthesis"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
)

func brownfieldResult(
	endpoint string,
	deployments []synthesis.Deployment,
	connections []synthesis.Connection,
) *synthesis.Result {
	if deployments == nil {
		deployments = []synthesis.Deployment{}
	}
	if connections == nil {
		connections = []synthesis.Connection{}
	}
	connections, credentials := synthesis.SplitConnectionCredentials(connections)
	return &synthesis.Result{
		Mode:                  synthesis.ModeBrownfield,
		Endpoint:              endpoint,
		Deployments:           deployments,
		Connections:           connections,
		ConnectionCredentials: credentials,
	}
}

// kvEnvServer is an environment service stub that returns per-key values,
// used to drive brownfieldACRRequested's env reads.
type kvEnvServer struct {
	azdext.UnimplementedEnvironmentServiceServer
	values map[string]string
}

type kvAccountServer struct {
	azdext.UnimplementedAccountServiceServer
}

func (s *kvAccountServer) LookupTenant(
	context.Context, *azdext.LookupTenantRequest,
) (*azdext.LookupTenantResponse, error) {
	return &azdext.LookupTenantResponse{TenantId: "tenant-id"}, nil
}

func (s *kvEnvServer) GetValue(
	_ context.Context, req *azdext.GetEnvRequest,
) (*azdext.KeyValueResponse, error) {
	return &azdext.KeyValueResponse{Value: s.values[req.Key]}, nil
}

func (s *kvEnvServer) GetValues(
	_ context.Context, _ *azdext.GetEnvironmentRequest,
) (*azdext.KeyValueListResponse, error) {
	values := make([]*azdext.KeyValue, 0, len(s.values))
	for key, value := range s.values {
		values = append(values, &azdext.KeyValue{Key: key, Value: value})
	}
	return &azdext.KeyValueListResponse{KeyValues: values}, nil
}

func newKVEnvClient(t *testing.T, values map[string]string) *azdext.AzdClient {
	t.Helper()
	srv := grpc.NewServer()
	azdext.RegisterEnvironmentServiceServer(srv, &kvEnvServer{values: values})
	azdext.RegisterAccountServiceServer(srv, &kvAccountServer{})

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(func() {
		srv.Stop()
		_ = lis.Close()
	})

	client, err := azdext.NewAzdClient(azdext.WithAddress(lis.Addr().String()))
	require.NoError(t, err)
	t.Cleanup(func() { client.Close() })
	return client
}

func TestBrownfieldACRRequested(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		values map[string]string
		want   bool
	}{
		{
			name:   "acr pending and no endpoint => create",
			values: map[string]string{"AI_AGENT_PENDING_PROVISION": "acr"},
			want:   true,
		},
		{
			name: "acr pending among others and no endpoint => create",
			values: map[string]string{
				"AI_AGENT_PENDING_PROVISION": "model_deployment,acr,app_insights",
			},
			want: true,
		},
		{
			name: "endpoint already set => skip even if acr pending",
			values: map[string]string{
				"AI_AGENT_PENDING_PROVISION":        "acr",
				"AZURE_CONTAINER_REGISTRY_ENDPOINT": "myreg.azurecr.io",
			},
			want: false,
		},
		{
			name:   "acr not pending => skip",
			values: map[string]string{"AI_AGENT_PENDING_PROVISION": "model_deployment"},
			want:   false,
		},
		{
			name:   "empty pending => skip",
			values: map[string]string{"AI_AGENT_PENDING_PROVISION": ""},
			want:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			p := &FoundryProvisioningProvider{
				envName:   "dev",
				azdClient: newKVEnvClient(t, tt.values),
			}
			assert.Equal(t, tt.want, p.brownfieldACRRequested(t.Context()))
		})
	}
}

func TestBrownfieldNeedsProvisioning(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		result      *synthesis.Result
		createACR   bool
		existingACR *existingACR
		onDisk      bool
		want        bool
	}{
		{name: "endpoint only", result: brownfieldResult("https://acct.services.ai.azure.com/api/projects/p", nil, nil)},
		{
			name: "model deployment",
			result: brownfieldResult("https://acct.services.ai.azure.com/api/projects/p",
				[]synthesis.Deployment{{Name: "model"}}, nil),
			want: true,
		},
		{
			name: "connection",
			result: brownfieldResult("https://acct.services.ai.azure.com/api/projects/p",
				nil, []synthesis.Connection{{Name: "connection"}}),
			want: true,
		},
		{
			name:      "pending ACR",
			result:    brownfieldResult("https://acct.services.ai.azure.com/api/projects/p", nil, nil),
			createACR: true,
			want:      true,
		},
		{
			name:        "existing ACR needs connection",
			result:      brownfieldResult("https://acct.services.ai.azure.com/api/projects/p", nil, nil),
			existingACR: &existingACR{name: "acr"},
			want:        true,
		},
		{
			name:        "existing ACR already configured",
			result:      brownfieldResult("https://acct.services.ai.azure.com/api/projects/p", nil, nil),
			existingACR: &existingACR{name: "acr", connectionName: "acr-conn"},
		},
		{
			name:   "on-disk Bicep",
			result: brownfieldResult("https://acct.services.ai.azure.com/api/projects/p", nil, nil),
			onDisk: true,
			want:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			projectPath := t.TempDir()
			if tt.onDisk {
				infraDir := filepath.Join(projectPath, onDiskInfraDir)
				require.NoError(t, os.MkdirAll(infraDir, 0o750))
				require.NoError(t, os.WriteFile(filepath.Join(infraDir, onDiskBicepFile), nil, 0o600))
			}
			p := &FoundryProvisioningProvider{
				projectPath:         projectPath,
				synthResult:         tt.result,
				brownfieldCreateACR: tt.createACR,
				brownfieldACR:       tt.existingACR,
			}
			assert.Equal(t, tt.want, p.brownfieldNeedsProvisioning())
		})
	}
}

func TestBrownfieldNoWorkSkipsARM(t *testing.T) {
	t.Parallel()
	p := &FoundryProvisioningProvider{
		synthResult: brownfieldResult("https://acct.services.ai.azure.com/api/projects/p", nil, nil),
	}

	state, err := p.State(t.Context(), nil)
	require.NoError(t, err)
	assert.Equal(t, "p", state.State.Outputs["AZURE_AI_PROJECT_NAME"].Value)

	deployed, err := p.Deploy(t.Context(), func(string) {})
	require.NoError(t, err)
	assert.Equal(t, "p", deployed.Deployment.Outputs["AZURE_AI_PROJECT_NAME"].Value)

	preview, err := p.Preview(t.Context(), func(string) {})
	require.NoError(t, err)
	assert.Empty(t, preview.Preview.Changes)
}

func TestBrownfieldNoWorkIncludesTenantOutput(t *testing.T) {
	t.Parallel()
	p := &FoundryProvisioningProvider{
		envName: "dev",
		azdClient: newKVEnvClient(t, map[string]string{
			"AZURE_AI_PROJECT_ID": "/subscriptions/sub/resourceGroups/rg/providers/" +
				"Microsoft.CognitiveServices/accounts/acct/projects/p",
		}),
		synthResult: brownfieldResult("https://acct.services.ai.azure.com/api/projects/p", nil, nil),
	}

	deployed, err := p.Deploy(t.Context(), func(string) {})
	require.NoError(t, err)
	assert.Equal(t, "tenant-id", deployed.Deployment.Outputs[envKeyTenantID].Value)
}

func TestBrownfieldPendingACRUsesCreateMode(t *testing.T) {
	t.Parallel()
	p := &FoundryProvisioningProvider{
		envName:             "dev",
		brownfieldAccount:   "account",
		brownfieldCreateACR: true,
		synthResult:         brownfieldResult("https://account.services.ai.azure.com/api/projects/p", nil, nil),
	}

	params := p.armParameters()
	assert.Equal(t, "create", params["acrMode"].(map[string]any)["value"])
	assert.NotEmpty(t, params["acrName"].(map[string]any)["value"])
}

func TestBrownfieldExistingACRNeedsConfiguration(t *testing.T) {
	t.Parallel()
	resourceID := "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.ContainerRegistry/registries/acr"

	needs := &FoundryProvisioningProvider{envName: "dev", azdClient: newKVEnvClient(t, map[string]string{
		"AZURE_CONTAINER_REGISTRY_RESOURCE_ID": resourceID,
		"AZURE_CONTAINER_REGISTRY_ENDPOINT":    "acr.azurecr.io",
	})}
	got, err := needs.brownfieldExistingACRNeedsConfiguration(t.Context())
	require.NoError(t, err)
	assert.True(t, got)

	configured := &FoundryProvisioningProvider{envName: "dev", azdClient: newKVEnvClient(t, map[string]string{
		"AZURE_CONTAINER_REGISTRY_RESOURCE_ID": resourceID,
		"AZURE_CONTAINER_REGISTRY_ENDPOINT":    "acr.azurecr.io",
		"AZURE_AI_PROJECT_ACR_CONNECTION_NAME": "acr-conn",
	})}
	got, err = configured.brownfieldExistingACRNeedsConfiguration(t.Context())
	require.NoError(t, err)
	assert.False(t, got)
}

func TestBrownfieldExistingACRValidation(t *testing.T) {
	t.Parallel()
	validID := "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.ContainerRegistry/registries/acr"

	missingEndpoint := &FoundryProvisioningProvider{envName: "dev", azdClient: newKVEnvClient(t, map[string]string{
		"AZURE_CONTAINER_REGISTRY_RESOURCE_ID": validID,
	})}
	_, err := missingEndpoint.brownfieldExistingACR(t.Context())
	require.ErrorContains(t, err, "AZURE_CONTAINER_REGISTRY_ENDPOINT")

	wrongType := &FoundryProvisioningProvider{envName: "dev", azdClient: newKVEnvClient(t, map[string]string{
		"AZURE_CONTAINER_REGISTRY_RESOURCE_ID": "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.Storage/storageAccounts/notacr",
		"AZURE_CONTAINER_REGISTRY_ENDPOINT":    "notacr.azurecr.io",
	})}
	_, err = wrongType.brownfieldExistingACR(t.Context())
	require.ErrorContains(t, err, "not a container registry")
}

func TestBrownfieldACRName(t *testing.T) {
	t.Parallel()

	p := &FoundryProvisioningProvider{
		envName: "dev",
		synthResult: brownfieldResult(
			"https://acct.services.ai.azure.com/api/projects/my-project", nil, nil),
	}
	name := p.brownfieldACRName("acct")

	// ACR names must be 5-50 chars, alphanumeric only.
	assert.GreaterOrEqual(t, len(name), 5)
	assert.LessOrEqual(t, len(name), 50)
	for _, r := range name {
		isLowerAlnum := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
		assert.True(t, isLowerAlnum, "ACR name %q must be lowercase alphanumeric, found %q", name, string(r))
	}

	// Deterministic across calls with the same inputs.
	assert.Equal(t, name, p.brownfieldACRName("acct"))

	// Different env or account changes the name (collision avoidance).
	other := &FoundryProvisioningProvider{
		envName:     "prod",
		synthResult: p.synthResult,
	}
	assert.NotEqual(t, name, other.brownfieldACRName("acct"))
}

func TestBrownfieldProjectName(t *testing.T) {
	t.Parallel()

	// Prefers the name parsed from the endpoint.
	p := &FoundryProvisioningProvider{
		foundryName: "fallback",
		synthResult: brownfieldResult(
			"https://acct.services.ai.azure.com/api/projects/my-project", nil, nil),
	}
	assert.Equal(t, "my-project", p.brownfieldProjectName())

	// Falls back to foundryName when the endpoint has no project segment.
	p2 := &FoundryProvisioningProvider{
		foundryName: "fallback",
		synthResult: brownfieldResult(
			"https://acct.services.ai.azure.com/", nil, nil),
	}
	assert.Equal(t, "fallback", p2.brownfieldProjectName())
}

func TestBrownfieldStateMergesPersistedDeploymentOutputs(t *testing.T) {
	t.Parallel()
	p := &FoundryProvisioningProvider{
		tenantID: "tenant",
		synthResult: brownfieldResult(
			"https://acct.services.ai.azure.com/api/projects/my-project", nil, nil),
	}
	properties := &armresources.DeploymentPropertiesExtended{
		Outputs: map[string]any{
			"AZURE_CONTAINER_REGISTRY_ENDPOINT": map[string]any{"type": "String", "value": "acr.azurecr.io"},
			"EMPTY":                             map[string]any{"type": "String", "value": ""},
		},
		OutputResources: []*armresources.ResourceReference{{ID: new("/subscriptions/sub/resourceGroups/rg/providers/Test/type/name")}},
	}

	state := p.brownfieldState(properties).State

	assert.Equal(t, "my-project", state.Outputs["AZURE_AI_PROJECT_NAME"].Value)
	assert.Equal(t, "acr.azurecr.io", state.Outputs["AZURE_CONTAINER_REGISTRY_ENDPOINT"].Value)
	assert.NotContains(t, state.Outputs, "EMPTY")
	require.Len(t, state.Resources, 1)

	empty := p.brownfieldState(nil).State
	assert.Contains(t, empty.Outputs, "FOUNDRY_PROJECT_ENDPOINT")
	assert.Empty(t, empty.Resources)
}

func TestBrownfieldDeploymentResultRedactsSecretsAndPreservesCanonicalOutputs(t *testing.T) {
	t.Parallel()
	p := &FoundryProvisioningProvider{
		tenantID: "tenant",
		synthResult: brownfieldResult(
			"https://acct.services.ai.azure.com/api/projects/current-project", nil, nil),
	}
	src := &templateSource{
		armTemplate: map[string]any{"parameters": map[string]any{
			"connectionCredentials": map[string]any{"type": "secureObject"},
			"customSecret":          map[string]any{"type": "secureString"},
			"location":              map[string]any{"type": "string"},
		}},
		parameters: map[string]any{
			"connectionCredentials": map[string]any{"value": map[string]any{"key": "secret"}},
			"customSecret":          map[string]any{"value": "secret"},
			"location":              map[string]any{"value": "eastus"},
		},
	}
	properties := &armresources.DeploymentPropertiesExtended{Outputs: map[string]any{
		"FOUNDRY_PROJECT_ENDPOINT": map[string]any{"type": "String", "value": ""},
		"AZURE_AI_PROJECT_NAME":    map[string]any{"type": "String", "value": "stale-project"},
	}}

	result := p.deploymentResult(src, properties)
	assert.NotContains(t, result.Parameters, "connectionCredentials")
	assert.NotContains(t, result.Parameters, "customSecret")
	assert.Equal(t, "eastus", result.Parameters["location"].Value)
	assert.Equal(t, "https://acct.services.ai.azure.com/api/projects/current-project",
		result.Outputs["FOUNDRY_PROJECT_ENDPOINT"].Value)
	assert.Equal(t, "current-project", result.Outputs["AZURE_AI_PROJECT_NAME"].Value)
	assert.Equal(t, "tenant", result.Outputs[envKeyTenantID].Value)
}

func TestMergeUnifiedParametersProtectsTargeting(t *testing.T) {
	t.Parallel()
	host := map[string]any{
		"foundryAccountName": map[string]any{"value": "host-account"},
		"foundryProjectName": map[string]any{"value": "host-project"},
		"deployments":        map[string]any{"value": []any{"host"}},
	}
	user := map[string]any{
		"foundryAccountName": map[string]any{"value": "user-account"},
		"foundryProjectName": map[string]any{"value": "user-project"},
		"deployments":        map[string]any{"value": []any{"user"}},
		"acrMode":            map[string]any{"value": "existing"},
	}

	got := mergeUnifiedParameters(user, host)

	assert.Equal(t, host["foundryAccountName"], got["foundryAccountName"])
	assert.Equal(t, host["foundryProjectName"], got["foundryProjectName"])
	assert.Equal(t, user["deployments"], got["deployments"])
	assert.NotContains(t, got, "acrMode")
}
