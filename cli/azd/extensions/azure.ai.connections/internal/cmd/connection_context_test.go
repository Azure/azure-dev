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
)

// TestNewCredential covers both credential shapes: the default (home) tenant
// when no tenant is resolved, and a tenant-scoped credential for multi-tenant /
// guest users. The tenant-scoped branch is what fixes "Tenant provided in token
// does not match resource token".
func TestNewCredential(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		tenantID string
	}{
		{name: "default tenant", tenantID: ""},
		{name: "scoped tenant", tenantID: "11111111-1111-1111-1111-111111111111"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			cred, err := newCredential(tc.tenantID)
			require.NoError(t, err)
			require.NotNil(t, cred)
		})
	}
}

func TestResolveEnvContextUsesSelectedEnvironment(t *testing.T) {
	t.Parallel()

	environments := &recordingEnvironmentContextReader{
		values: map[string]map[string]string{
			"default": {
				"AZURE_AI_PROJECT_ID":   "default-project",
				"AZURE_SUBSCRIPTION_ID": "default-subscription",
			},
			"staging": {
				"AZURE_AI_PROJECT_ID":   "staging-project",
				"AZURE_SUBSCRIPTION_ID": "staging-subscription",
			},
		},
	}
	accounts := &recordingTenantLookup{tenantID: "staging-tenant"}

	resolved := resolveEnvContextWithClients(t.Context(), "staging", environments, accounts)
	assert.Equal(t, "staging-project", resolved.projectID)
	assert.Equal(t, "staging-tenant", resolved.tenantID)
	assert.Equal(t, 0, environments.currentCalls)
	assert.Equal(t, []string{"staging", "staging"}, environments.valueEnvironments)
	assert.Equal(t, "staging-subscription", accounts.subscriptionID)
}

type recordingEnvironmentContextReader struct {
	values            map[string]map[string]string
	currentCalls      int
	valueEnvironments []string
}

func (r *recordingEnvironmentContextReader) GetCurrent(
	context.Context,
	*azdext.EmptyRequest,
	...grpc.CallOption,
) (*azdext.EnvironmentResponse, error) {
	r.currentCalls++
	return nil, errors.New("GetCurrent must not be called for a selected environment")
}

func (r *recordingEnvironmentContextReader) GetValue(
	_ context.Context,
	request *azdext.GetEnvRequest,
	_ ...grpc.CallOption,
) (*azdext.KeyValueResponse, error) {
	r.valueEnvironments = append(r.valueEnvironments, request.GetEnvName())
	return &azdext.KeyValueResponse{Value: r.values[request.GetEnvName()][request.GetKey()]}, nil
}

type recordingTenantLookup struct {
	tenantID       string
	subscriptionID string
}

func (r *recordingTenantLookup) LookupTenant(
	_ context.Context,
	request *azdext.LookupTenantRequest,
	_ ...grpc.CallOption,
) (*azdext.LookupTenantResponse, error) {
	r.subscriptionID = request.GetSubscriptionId()
	return &azdext.LookupTenantResponse{TenantId: r.tenantID}, nil
}
