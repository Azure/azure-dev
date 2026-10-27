// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"net"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/authorization/armauthorization/v3"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type developerRBACAccountServer struct {
	azdext.UnimplementedAccountServiceServer
}

func (s *developerRBACAccountServer) GetCurrentPrincipal(
	_ context.Context, req *azdext.GetCurrentPrincipalRequest,
) (*azdext.GetCurrentPrincipalResponse, error) {
	if req.GetSubscriptionId() != "selected-subscription" {
		return nil, status.Error(codes.InvalidArgument, "subscription is required")
	}
	return &azdext.GetCurrentPrincipalResponse{
		ObjectId: "resource-tenant-object-id", PrincipalType: azdext.PrincipalType_PRINCIPAL_TYPE_USER,
	}, nil
}

func TestDeveloperRBACPrincipalUsesStableRPC(t *testing.T) {
	methods := make(chan string, 2)
	server := grpc.NewServer(grpc.UnaryInterceptor(
		func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
			methods <- info.FullMethod
			return handler(ctx, req)
		},
	))
	azdext.RegisterAccountServiceServer(server, &developerRBACAccountServer{})
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() {
		server.Stop()
		_ = listener.Close()
	})
	client, err := azdext.NewAzdClient(azdext.WithAddress(listener.Addr().String()))
	require.NoError(t, err)
	t.Cleanup(client.Close)

	id, principalType, err := developerRBACPrincipal(t.Context(), client.Account(), "selected-subscription")
	require.NoError(t, err)
	assert.Equal(t, "resource-tenant-object-id", id)
	assert.Equal(t, armauthorization.PrincipalTypeUser, principalType)
	assert.Equal(t, "/azd.extensions.v1.AccountService/GetCurrentPrincipal", <-methods)

	_, _, err = developerRBACPrincipal(t.Context(), client.Account(), "")
	require.Equal(t, codes.InvalidArgument, status.Code(err))
	assert.Equal(t, "/azd.extensions.v1.AccountService/GetCurrentPrincipal", <-methods)
}

type developerRBACAccountClient struct {
	azdext.AccountServiceClient
	response       *azdext.GetCurrentPrincipalResponse
	err            error
	subscriptionID string
}

func (c *developerRBACAccountClient) GetCurrentPrincipal(
	_ context.Context, req *azdext.GetCurrentPrincipalRequest, _ ...grpc.CallOption,
) (*azdext.GetCurrentPrincipalResponse, error) {
	c.subscriptionID = req.GetSubscriptionId()
	return c.response, c.err
}

func TestDeveloperRBACPrincipal(t *testing.T) {
	lookupErr := status.Error(codes.PermissionDenied, "subscription unavailable")
	tests := []struct {
		name     string
		response *azdext.GetCurrentPrincipalResponse
		err      error
		wantType armauthorization.PrincipalType
		wantErr  string
	}{
		{
			name: "resource tenant guest user",
			response: &azdext.GetCurrentPrincipalResponse{
				ObjectId: "resource-tenant-object-id", PrincipalType: azdext.PrincipalType_PRINCIPAL_TYPE_USER,
			},
			wantType: armauthorization.PrincipalTypeUser,
		},
		{
			name: "service principal",
			response: &azdext.GetCurrentPrincipalResponse{
				ObjectId: "service-principal-object-id", PrincipalType: azdext.PrincipalType_PRINCIPAL_TYPE_SERVICE_PRINCIPAL,
			},
			wantType: armauthorization.PrincipalTypeServicePrincipal,
		},
		{name: "lookup failure", err: lookupErr, wantErr: "subscription unavailable"},
		{name: "empty ID", response: &azdext.GetCurrentPrincipalResponse{}, wantErr: "empty current principal object ID"},
		{
			name: "unsupported type", response: &azdext.GetCurrentPrincipalResponse{ObjectId: "object-id"},
			wantErr: "unsupported current principal type",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			account := &developerRBACAccountClient{response: tt.response, err: tt.err}
			id, principalType, err := developerRBACPrincipal(t.Context(), account, "selected-subscription")
			assert.Equal(t, "selected-subscription", account.subscriptionID)
			if tt.wantErr != "" {
				require.ErrorContains(t, err, tt.wantErr)
				assert.Empty(t, id)
				assert.Empty(t, principalType)
				if tt.err != nil {
					assert.ErrorIs(t, err, tt.err)
				}
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.response.ObjectId, id)
			assert.Equal(t, tt.wantType, principalType)
		})
	}
}

func TestNormalizeLoginServer(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"plain login server", "crfoo.azurecr.io", "crfoo.azurecr.io"},
		{"with https prefix", "https://crfoo.azurecr.io", "crfoo.azurecr.io"},
		{"with http prefix", "http://crfoo.azurecr.io", "crfoo.azurecr.io"},
		{"with trailing slash", "crfoo.azurecr.io/", "crfoo.azurecr.io"},
		{"uppercase domain", "CrFoo.AzureCR.io", "crfoo.azurecr.io"},
		{"uppercase HTTPS prefix", "HTTPS://crfoo.azurecr.io", "crfoo.azurecr.io"},
		{"mixed case prefix and domain", "Https://CrFoo.AzureCR.io/", "crfoo.azurecr.io"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := normalizeLoginServer(tt.input)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestDeveloperRBACRoleConstants(t *testing.T) {
	// ACR roles
	assert.Equal(t, "fb382eab-e894-4461-af04-94435c366c3f", roleContainerRegistryTasksContributor)
	assert.Equal(t, "2efddaa5-3f1f-4df3-97df-af3f13818f4c", roleContainerRegistryRepositoryContributor)
	assert.Equal(t, "8311e382-0749-4cb8-b61a-304f252e45ec", roleAcrPush)
	assert.Equal(t, "2a1e307c-b015-4ebd-883e-5b7698a07328", roleAcrRepositoryWriter)

	// Superset roles
	assert.Equal(t, "8e3af657-a8ff-443c-a75c-2fe8c4bcb635", roleOwner)
	assert.Equal(t, "b24988ac-6180-42a0-ab88-20f7382dd24c", roleContributor)

	// Role-assignment write roles
	assert.Equal(t, "18d7d88d-d35e-4fb5-a5c3-7773c20a72d9", roleUserAccessAdministrator)
	assert.Equal(t, "f58310d9-a9f6-439a-9e8d-f62e7b41a168", roleRBACAdministrator)
	assert.Equal(t, "eadc314b-1a2d-4efa-be10-5d325db5065e", roleAzureAIProjectManager)
	assert.Equal(t, "e47c6f54-e4a2-4754-9501-8e0985b135e1", roleAzureAIAccountOwner)

	// AI roles
	assert.Equal(t, "64702f94-c441-49e6-a78b-ef80e0188fee", roleAzureAIDeveloper)
	assert.Equal(t, "a97b65f3-24c7-4388-baec-2e87135dc908", roleCognitiveServicesUser)
	assert.Equal(t, "c883944f-8b7b-4483-af10-35834be79c4a", roleFoundryOwner)
}

func TestSufficientRoleLists(t *testing.T) {
	// Verify the sufficient role lists contain expected entries.
	assert.Contains(t, sufficientACRRoles, roleOwner)
	assert.Contains(t, sufficientACRRoles, roleContributor)
	assert.Contains(t, sufficientACRRoles, roleAcrPush)
	assert.Contains(t, sufficientACRRoles, roleContainerRegistryTasksContributor)
	assert.Contains(t, sufficientACRRoles, roleContainerRegistryRepositoryContributor)

	// sufficientAIUserRoles must contain only roles whose dataActions cover AIServices.*
	assert.Contains(t, sufficientAIUserRoles, roleAzureAIUser)           // Foundry User: CogSvc.*
	assert.Contains(t, sufficientAIUserRoles, roleCognitiveServicesUser) // backward compat: CogSvc.*
	assert.Contains(t, sufficientAIUserRoles, roleAzureAIProjectManager) // Foundry Project Manager: CogSvc.*
	assert.Contains(t, sufficientAIUserRoles, roleFoundryOwner)          // Foundry Owner: CogSvc.*
	// Owner/Contributor have no dataActions and must NOT be treated as sufficient
	assert.NotContains(t, sufficientAIUserRoles, roleOwner)
	assert.NotContains(t, sufficientAIUserRoles, roleContributor)
	// Azure AI Developer only covers OpenAI/Speech/ContentSafety/MaaS — NOT AIServices.*
	assert.NotContains(t, sufficientAIUserRoles, roleAzureAIDeveloper)

	// Role-assignment write: Owner, UAA, RBAC Admin, Foundry Project Manager, Foundry Account Owner;
	// Contributor must NOT be included.
	assert.Contains(t, sufficientRoleAssignWriteRoles, roleOwner)
	assert.Contains(t, sufficientRoleAssignWriteRoles, roleUserAccessAdministrator)
	assert.Contains(t, sufficientRoleAssignWriteRoles, roleRBACAdministrator)
	assert.Contains(t, sufficientRoleAssignWriteRoles, roleAzureAIProjectManager)
	assert.Contains(t, sufficientRoleAssignWriteRoles, roleAzureAIAccountOwner)
	assert.Contains(t, sufficientRoleAssignWriteRoles, roleFoundryOwner) // Foundry Owner has roleAssignments/write
	assert.NotContains(t, sufficientRoleAssignWriteRoles, roleContributor)

	// ABAC ACR roles: Owner, RepositoryWriter, RepositoryContributor; AcrPush must NOT be included.
	assert.Contains(t, sufficientACRAbacRoles, roleOwner)
	assert.Contains(t, sufficientACRAbacRoles, roleAcrRepositoryWriter)
	assert.Contains(t, sufficientACRAbacRoles, roleContainerRegistryRepositoryContributor)
	assert.NotContains(t, sufficientACRAbacRoles, roleAcrPush)
}
