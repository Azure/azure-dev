// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"net"
	"testing"

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
