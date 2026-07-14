// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package provisioning

import (
	"context"
	"net"
	"strings"
	"testing"

	"azure.ai.projects/internal/synthesis"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
)

// kvEnvServer is an environment service stub that returns per-key values,
// used to drive brownfieldACRRequested's env reads.
type kvEnvServer struct {
	azdext.UnimplementedEnvironmentServiceServer
	values map[string]string
}

func (s *kvEnvServer) GetValue(
	_ context.Context, req *azdext.GetEnvRequest,
) (*azdext.KeyValueResponse, error) {
	return &azdext.KeyValueResponse{Value: s.values[req.Key]}, nil
}

func newKVEnvClient(t *testing.T, values map[string]string) *azdext.AzdClient {
	t.Helper()
	srv := grpc.NewServer()
	azdext.RegisterEnvironmentServiceServer(srv, &kvEnvServer{values: values})

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

func TestBrownfieldACRName(t *testing.T) {
	t.Parallel()

	p := &FoundryProvisioningProvider{
		envName:            "dev",
		brownfieldEndpoint: "https://acct.services.ai.azure.com/api/projects/my-project",
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
		envName:            "prod",
		brownfieldEndpoint: p.brownfieldEndpoint,
	}
	assert.NotEqual(t, name, other.brownfieldACRName("acct"))
}

func TestBrownfieldProjectName(t *testing.T) {
	t.Parallel()

	// Prefers the name parsed from the endpoint.
	p := &FoundryProvisioningProvider{
		foundryName:        "fallback",
		brownfieldEndpoint: "https://acct.services.ai.azure.com/api/projects/my-project",
	}
	assert.Equal(t, "my-project", p.brownfieldProjectName())

	// Falls back to foundryName when the endpoint has no project segment.
	p2 := &FoundryProvisioningProvider{
		foundryName:        "fallback",
		brownfieldEndpoint: "https://acct.services.ai.azure.com/",
	}
	assert.Equal(t, "fallback", p2.brownfieldProjectName())
}

func TestBrownfieldDeploymentName(t *testing.T) {
	t.Parallel()

	// Short env name: full "<name>-brownfield" fits under 64 chars.
	short := &FoundryProvisioningProvider{envName: "dev", projectPath: "/p"}
	name := short.brownfieldDeploymentName()
	assert.LessOrEqual(t, len(name), 64)
	assert.True(t, strings.HasSuffix(name, "-brownfield"), "got %q", name)
	assert.Equal(t, short.deploymentName()+"-brownfield", name)

	// Long env name: must be capped at 64 while keeping the suffix.
	long := &FoundryProvisioningProvider{
		envName:     "agent-framework-agent-basic-invocations-dev",
		projectPath: "/some/long/project/path",
	}
	lname := long.brownfieldDeploymentName()
	assert.LessOrEqual(t, len(lname), 64, "got %q (len %d)", lname, len(lname))
	assert.True(t, strings.HasSuffix(lname, "-brownfield"), "got %q", lname)
}

func TestBrownfieldParams(t *testing.T) {
	t.Parallel()

	deployments := []synthesis.Deployment{{Name: "gpt-4o-mini"}}

	t.Run("without ACR still carries projectName for the existing project resource", func(t *testing.T) {
		t.Parallel()
		// The brownfield template declares `foundryAccountPreview::project` as an
		// unconditional existing resource, so projectName must be supplied even
		// when no ACR is created (model-deployments-only reconcile). Regression
		// test for the InvalidTemplate failure where the name collapsed to
		// "<account>/" because projectName was omitted.
		p := &FoundryProvisioningProvider{
			envName:               "dev",
			brownfieldEndpoint:    "https://acct.services.ai.azure.com/api/projects/my-project",
			brownfieldDeployments: deployments,
		}
		params, err := p.brownfieldParams(t.Context(), "acct", "rg", false)
		require.NoError(t, err)

		assert.Equal(t, map[string]any{"value": "acct"}, params["accountName"])
		assert.Equal(t, map[string]any{"value": deployments}, params["deployments"])
		assert.Equal(t, map[string]any{"value": "[]"}, params["connections"])
		assert.Equal(t, map[string]any{"value": "my-project"}, params["projectName"])
		assert.NotContains(t, params, "includeAcr")
		assert.NotContains(t, params, "acrName")
	})

	t.Run("connections without ACR carry connections and set projectName", func(t *testing.T) {
		t.Parallel()
		conns := []synthesis.Connection{{Name: "search-conn", Category: "CognitiveSearch"}}
		p := &FoundryProvisioningProvider{
			envName:               "dev",
			brownfieldEndpoint:    "https://acct.services.ai.azure.com/api/projects/my-project",
			brownfieldConnections: conns,
		}
		params, err := p.brownfieldParams(t.Context(), "acct", "rg", false)
		require.NoError(t, err)

		assert.Equal(
			t,
			map[string]any{"value": `[{"name":"search-conn","category":"CognitiveSearch","target":"","authType":""}]`},
			params["connections"],
		)
		// Connections are project-scoped, so projectName must be supplied even
		// without ACR.
		assert.Equal(t, map[string]any{"value": "my-project"}, params["projectName"])
		assert.NotContains(t, params, "includeAcr")
	})

	t.Run("with ACR adds registry params", func(t *testing.T) {
		t.Parallel()
		p := &FoundryProvisioningProvider{
			envName:            "dev",
			brownfieldEndpoint: "https://acct.services.ai.azure.com/api/projects/my-project",
			azdClient:          newKVEnvClient(t, map[string]string{"AZURE_LOCATION": "westus2"}),
		}
		params, err := p.brownfieldParams(t.Context(), "acct", "rg", true)
		require.NoError(t, err)

		assert.Equal(t, map[string]any{"value": true}, params["includeAcr"])
		assert.Equal(t, map[string]any{"value": "my-project"}, params["projectName"])
		assert.Equal(t, map[string]any{"value": "westus2"}, params["location"])
		assert.Equal(t, map[string]any{"value": p.brownfieldACRName("acct")}, params["acrName"])
	})

	t.Run("omits location when unresolved so template default applies", func(t *testing.T) {
		t.Parallel()
		// AZURE_LOCATION unset and no usable credential => brownfieldLocation
		// returns ""; the param must be omitted, not set to "".
		p := &FoundryProvisioningProvider{
			envName:            "dev",
			brownfieldEndpoint: "https://acct.services.ai.azure.com/api/projects/my-project",
			azdClient:          newKVEnvClient(t, map[string]string{}),
		}
		params, err := p.brownfieldParams(t.Context(), "acct", "rg", true)
		require.NoError(t, err)

		assert.Contains(t, params, "includeAcr")
		assert.NotContains(t, params, "location")
	})
}

// TestBrownfieldReconcileMessage covers every combination the caller can
// reach (deployBrownfield's own guard skips provisioning entirely when all
// three are false, so at least one is always true here). Regression guard
// for a live-tested bug: a brownfield project declaring only a connection
// (no deployments, no ACR) previously printed "reconciling declared model
// deployments..." even though zero deployments existed.
func TestBrownfieldReconcileMessage(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		hasDeployments bool
		createACR      bool
		hasConnections bool
		want           string
	}{
		{
			name:           "connections only (the live-tested regression case)",
			hasConnections: true,
			want:           "Using existing Foundry project; reconciling connections...",
		},
		{
			name:           "deployments only",
			hasDeployments: true,
			want:           "Using existing Foundry project; reconciling model deployments...",
		},
		{
			name:      "ACR only",
			createACR: true,
			want:      "Using existing Foundry project; reconciling container registry...",
		},
		{
			name:           "deployments and ACR",
			hasDeployments: true,
			createACR:      true,
			want:           "Using existing Foundry project; reconciling model deployments, container registry...",
		},
		{
			name:           "deployments and connections",
			hasDeployments: true,
			hasConnections: true,
			want:           "Using existing Foundry project; reconciling model deployments, connections...",
		},
		{
			name:           "ACR and connections",
			createACR:      true,
			hasConnections: true,
			want:           "Using existing Foundry project; reconciling container registry, connections...",
		},
		{
			name:           "all three",
			hasDeployments: true,
			createACR:      true,
			hasConnections: true,
			want:           "Using existing Foundry project; reconciling model deployments, container registry, connections...",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := brownfieldReconcileMessage(tt.hasDeployments, tt.createACR, tt.hasConnections)
			assert.Equal(t, tt.want, got)
			// Never claim to reconcile something that isn't actually pending.
			if !tt.hasDeployments {
				assert.NotContains(t, got, "model deployments")
			}
			if !tt.createACR {
				assert.NotContains(t, got, "container registry")
			}
			if !tt.hasConnections {
				assert.NotContains(t, got, "connections")
			}
		})
	}
}
