// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"net"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
)

type azureContextPromptServer struct {
	azdext.UnimplementedPromptServiceServer
	subscription      *azdext.Subscription
	location          *azdext.Location
	subscriptionCalls int
	locationCalls     int
	locationRequest   *azdext.PromptLocationRequest
}

func (s *azureContextPromptServer) PromptSubscription(
	context.Context,
	*azdext.PromptSubscriptionRequest,
) (*azdext.PromptSubscriptionResponse, error) {
	s.subscriptionCalls++
	return &azdext.PromptSubscriptionResponse{
		Subscription: s.subscription,
	}, nil
}

func (s *azureContextPromptServer) PromptLocation(
	_ context.Context,
	request *azdext.PromptLocationRequest,
) (*azdext.PromptLocationResponse, error) {
	s.locationCalls++
	s.locationRequest = request
	return &azdext.PromptLocationResponse{Location: s.location}, nil
}

type azureContextAccountServer struct {
	azdext.UnimplementedAccountServiceServer
	tenantID string
	calls    int
	request  *azdext.LookupTenantRequest
}

func (s *azureContextAccountServer) LookupTenant(
	_ context.Context,
	request *azdext.LookupTenantRequest,
) (*azdext.LookupTenantResponse, error) {
	s.calls++
	s.request = request
	return &azdext.LookupTenantResponse{TenantId: s.tenantID}, nil
}

func newAzureContextClient(
	t *testing.T,
	promptServer azdext.PromptServiceServer,
	accountServer azdext.AccountServiceServer,
) *azdext.AzdClient {
	t.Helper()

	server := grpc.NewServer()
	azdext.RegisterPromptServiceServer(server, promptServer)
	azdext.RegisterAccountServiceServer(server, accountServer)

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	go func() {
		_ = server.Serve(listener)
	}()
	t.Cleanup(func() {
		server.Stop()
		_ = listener.Close()
	})

	client, err := azdext.NewAzdClient(
		azdext.WithAddress(listener.Addr().String()),
	)
	require.NoError(t, err)
	t.Cleanup(client.Close)
	return client
}

func TestResolveAzureContextUsesEnvironmentSubscription(t *testing.T) {
	promptServer := &azureContextPromptServer{
		location: &azdext.Location{Name: "eastus"},
	}
	accountServer := &azureContextAccountServer{tenantID: "user-tenant"}
	client := newAzureContextClient(t, promptServer, accountServer)
	target := &resolvedProject{Mode: projectModeNew}
	values := map[string]string{
		"AZURE_SUBSCRIPTION_ID": "environment-subscription",
		"AZURE_TENANT_ID":       "environment-tenant",
	}

	require.NoError(t, resolveAzureContext(
		t.Context(),
		client,
		target,
		values,
		false,
	))

	assert.Equal(t, "environment-subscription", target.SubscriptionId)
	assert.Equal(t, "user-tenant", target.UserTenantId)
	assert.Equal(t, "environment-subscription",
		promptServer.locationRequest.AzureContext.Scope.SubscriptionId)
	assert.Equal(t, "user-tenant",
		promptServer.locationRequest.AzureContext.Scope.TenantId)
	assert.Equal(t, "environment-subscription",
		accountServer.request.SubscriptionId)
	assert.Equal(t, 0, promptServer.subscriptionCalls)
	assert.Equal(t, 1, accountServer.calls)
	assert.Equal(t, 1, promptServer.locationCalls)
	assert.Equal(t, "eastus", target.Location)
}

func TestResolveAzureContextPrefersTargetSubscription(t *testing.T) {
	promptServer := &azureContextPromptServer{
		location: &azdext.Location{Name: "westus"},
	}
	accountServer := &azureContextAccountServer{tenantID: "looked-up-tenant"}
	client := newAzureContextClient(t, promptServer, accountServer)
	target := &resolvedProject{
		Mode:           projectModeNew,
		SubscriptionId: "target-subscription",
		UserTenantId:   "target-tenant",
	}
	values := map[string]string{
		"AZURE_SUBSCRIPTION_ID": "environment-subscription",
		"AZURE_TENANT_ID":       "environment-tenant",
	}

	require.NoError(t, resolveAzureContext(
		t.Context(),
		client,
		target,
		values,
		false,
	))

	assert.Equal(t, "target-subscription",
		promptServer.locationRequest.AzureContext.Scope.SubscriptionId)
	assert.Equal(t, "target-tenant",
		promptServer.locationRequest.AzureContext.Scope.TenantId)
	assert.Equal(t, 0, promptServer.subscriptionCalls)
	assert.Equal(t, 0, accountServer.calls)
	assert.Equal(t, "westus", target.Location)
}

func TestResolveAzureContextPromptsForSubscription(t *testing.T) {
	promptServer := &azureContextPromptServer{
		subscription: &azdext.Subscription{
			Id:           "prompted-subscription",
			UserTenantId: "prompted-tenant",
		},
		location: &azdext.Location{Name: "centralus"},
	}
	accountServer := &azureContextAccountServer{tenantID: "unused-tenant"}
	client := newAzureContextClient(t, promptServer, accountServer)
	target := &resolvedProject{Mode: projectModeNew}

	require.NoError(t, resolveAzureContext(
		t.Context(),
		client,
		target,
		map[string]string{},
		false,
	))

	assert.Equal(t, "prompted-subscription", target.SubscriptionId)
	assert.Equal(t, "prompted-tenant", target.UserTenantId)
	assert.Equal(t, "prompted-subscription",
		promptServer.locationRequest.AzureContext.Scope.SubscriptionId)
	assert.Equal(t, "prompted-tenant",
		promptServer.locationRequest.AzureContext.Scope.TenantId)
	assert.Equal(t, 1, promptServer.subscriptionCalls)
	assert.Equal(t, 0, accountServer.calls)
	assert.Equal(t, "centralus", target.Location)
}

func TestResolveAzureContextNoPromptWithCompleteValues(t *testing.T) {
	target := &resolvedProject{Mode: projectModeNew}
	require.NoError(t, resolveAzureContext(
		t.Context(),
		nil,
		target,
		map[string]string{
			"AZURE_SUBSCRIPTION_ID": "subscription",
			"AZURE_LOCATION":        "eastus",
		},
		true,
	))
}
