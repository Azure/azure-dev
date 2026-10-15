// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package provisioning

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"azure.ai.projects/internal/synthesis"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type scopedProjectDeploymentsEnvServer struct {
	resolveEnvStubEnvServer
	expectedName string
}

func (s *scopedProjectDeploymentsEnvServer) GetValues(
	ctx context.Context, request *azdext.GetEnvironmentRequest,
) (*azdext.KeyValueListResponse, error) {
	if request.GetName() != s.expectedName {
		return nil, fmt.Errorf("unexpected environment %q, want %q", request.GetName(), s.expectedName)
	}
	return s.resolveEnvStubEnvServer.GetValues(ctx, request)
}

func TestProjectDeploymentsRoundTrip(t *testing.T) {
	t.Parallel()
	deployments := []synthesis.Deployment{{
		Name: `chat\"quoted`,
		Model: synthesis.DeploymentModel{
			Name: "model\nname", Format: "OpenAI", Version: `v\"1`,
		},
		Sku: synthesis.DeploymentSku{Name: "Standard", Capacity: 10},
	}}
	encoded, err := EncodeProjectDeployments(deployments)
	require.NoError(t, err)
	raw, err := json.Marshal(deployments)
	require.NoError(t, err)
	for _, value := range []string{encoded, string(raw)} {
		actual, present, err := DecodeProjectDeployments(value)
		require.NoError(t, err)
		assert.True(t, present)
		assert.Equal(t, deployments, actual)
	}
	encoded, err = EncodeProjectDeployments(nil)
	require.NoError(t, err)
	assert.Equal(t, "[]", encoded)
}

func TestDecodeProjectDeploymentsPresence(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		value   string
		present bool
	}{
		{"", false}, {" \t\n", false}, {"[]", true}, {"  []  ", true},
	} {
		t.Run(test.value, func(t *testing.T) {
			deployments, present, err := DecodeProjectDeployments(test.value)
			require.NoError(t, err)
			assert.Equal(t, test.present, present)
			assert.Empty(t, deployments)
			if present {
				assert.NotNil(t, deployments)
			}
		})
	}
}

func TestDecodeProjectDeploymentsRejectsMalformed(t *testing.T) {
	t.Parallel()
	for _, value := range []string{
		"null", "{}", `"[]"`, "true", "1", "[", `[{"name":}]`, `[{\"name\":}]`,
		`["chat"]`, `[null]`, `[{"sku":{"capacity":"ten"}}]`, "[] trailing", `[{\"name\":\"\x40\"}]`,
	} {
		t.Run(value, func(t *testing.T) {
			_, present, err := DecodeProjectDeployments(value)
			assert.True(t, present)
			require.Error(t, err)
			assert.Contains(t, err.Error(), ProjectDeploymentsEnvKey)
			assert.Contains(t, err.Error(), "azd env set")
		})
	}
}
