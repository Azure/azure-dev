// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package spring

import (
	"context"
	"errors"
	"net"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
)

type lifecycleAccountServer struct {
	azdext.UnimplementedAccountServiceServer
	principalType  azdext.PrincipalType
	objectID       string
	tenantID       string
	tenantError    error
	principalCalls int
	tenantCalls    int
}

func (s *lifecycleAccountServer) GetCurrentPrincipal(
	context.Context, *azdext.GetCurrentPrincipalRequest,
) (*azdext.GetCurrentPrincipalResponse, error) {
	s.principalCalls++
	return &azdext.GetCurrentPrincipalResponse{ObjectId: s.objectID, PrincipalType: s.principalType}, nil
}

func (s *lifecycleAccountServer) LookupTenant(
	context.Context, *azdext.LookupTenantRequest,
) (*azdext.LookupTenantResponse, error) {
	s.tenantCalls++
	return &azdext.LookupTenantResponse{TenantId: s.tenantID}, s.tenantError
}

func lifecycleTestClient(t *testing.T, register func(*grpc.Server)) *azdext.AzdClient {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	server := grpc.NewServer()
	register(server)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)
	client, err := azdext.NewAzdClient(azdext.WithAddress(listener.Addr().String()))
	require.NoError(t, err)
	t.Cleanup(client.Close)
	return client
}

func TestDeveloperIdentityResolvesHostUserAndAccessTenantWithoutAdministratorName(t *testing.T) {
	for _, test := range []struct {
		name        string
		principal   azdext.PrincipalType
		objectID    string
		tenantID    string
		tenantError error
		wantError   string
	}{
		{name: "User", principal: azdext.PrincipalType_PRINCIPAL_TYPE_USER,
			objectID: testSpringClientID, tenantID: "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"},
		{name: "service principal", principal: azdext.PrincipalType_PRINCIPAL_TYPE_SERVICE_PRINCIPAL,
			wantError: "principal to be a user"},
		{name: "missing object ID", principal: azdext.PrincipalType_PRINCIPAL_TYPE_USER, wantError: "object ID"},
		{name: "missing tenant", principal: azdext.PrincipalType_PRINCIPAL_TYPE_USER,
			objectID: testSpringClientID, wantError: "tenant"},
		{name: "tenant lookup fails", principal: azdext.PrincipalType_PRINCIPAL_TYPE_USER,
			objectID: testSpringClientID, tenantError: errors.New("host lookup failed"), wantError: "host lookup failed"},
	} {
		t.Run(test.name, func(t *testing.T) {
			account := &lifecycleAccountServer{
				principalType: test.principal, objectID: test.objectID,
				tenantID: test.tenantID, tenantError: test.tenantError,
			}
			client := lifecycleTestClient(t, func(server *grpc.Server) {
				azdext.RegisterAccountServiceServer(server, account)
			})
			identity, err := newDeveloperIdentityResolver(client).Resolve(t.Context(), "subscription")
			if test.wantError != "" {
				require.ErrorContains(t, err, test.wantError)
				require.Nil(t, identity)
			} else {
				require.NoError(t, err)
				require.Equal(t, test.objectID, identity.ObjectID)
				require.Equal(t, test.tenantID, identity.UserAccessTenantID)
				require.NotEqual(t, test.objectID, identity.UserAccessTenantID)
			}
			require.Equal(t, 1, account.principalCalls)
		})
	}
}
