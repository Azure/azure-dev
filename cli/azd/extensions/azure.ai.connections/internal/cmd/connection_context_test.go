// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
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

	environment := &recordingEnvironmentContextReader{values: map[string]string{
		"AZURE_AI_PROJECT_ID":   "/subscriptions/sub/resourceGroups/rg/providers/provider/accounts/account/projects/project",
		"AZURE_SUBSCRIPTION_ID": "sub",
	}}
	account := &recordingTenantLookup{}

	resolved := resolveEnvContextWithClients(t.Context(), "staging", environment, account)

	assert.Equal(t, environment.values["AZURE_AI_PROJECT_ID"], resolved.projectID)
	assert.Equal(t, "tenant", resolved.tenantID)
	assert.Equal(t, 0, environment.currentCalls)
	assert.Equal(t, []string{"staging", "staging"}, environment.requestedEnvironments)
	assert.Equal(t, []string{"sub"}, account.subscriptions)
}

type recordingEnvironmentContextReader struct {
	values                map[string]string
	currentCalls          int
	requestedEnvironments []string
}

func (r *recordingEnvironmentContextReader) GetCurrent(
	context.Context,
	*azdext.EmptyRequest,
	...grpc.CallOption,
) (*azdext.EnvironmentResponse, error) {
	r.currentCalls++
	return &azdext.EnvironmentResponse{Environment: &azdext.Environment{Name: "default"}}, nil
}

func (r *recordingEnvironmentContextReader) GetValue(
	_ context.Context,
	request *azdext.GetEnvRequest,
	_ ...grpc.CallOption,
) (*azdext.KeyValueResponse, error) {
	r.requestedEnvironments = append(r.requestedEnvironments, request.GetEnvName())
	return &azdext.KeyValueResponse{Value: r.values[request.GetKey()]}, nil
}

type recordingTenantLookup struct {
	subscriptions []string
}

func (r *recordingTenantLookup) LookupTenant(
	_ context.Context,
	request *azdext.LookupTenantRequest,
	_ ...grpc.CallOption,
) (*azdext.LookupTenantResponse, error) {
	r.subscriptions = append(r.subscriptions, request.GetSubscriptionId())
	return &azdext.LookupTenantResponse{TenantId: "tenant"}, nil
}
