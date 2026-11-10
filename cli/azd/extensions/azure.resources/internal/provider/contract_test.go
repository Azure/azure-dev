// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package provider

import (
	"context"
	"errors"
	"net"
	"sync"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/structpb"
)

var _ azdext.ProvisioningProvider = (*Provider)(nil)

type initializationHost struct {
	azdext.UnimplementedEnvironmentServiceServer
	azdext.UnimplementedAccountServiceServer
	mu        sync.Mutex
	values    map[string]string
	prompts   int
	locations int
	promptErr error
	project   map[string]any
}

func (s *initializationHost) GetConfigString(
	context.Context, *azdext.GetConfigStringRequest,
) (*azdext.GetConfigStringResponse, error) {
	return &azdext.GetConfigStringResponse{}, nil
}

func (s *initializationHost) GetCurrent(context.Context, *azdext.EmptyRequest) (*azdext.EnvironmentResponse, error) {
	return &azdext.EnvironmentResponse{Environment: &azdext.Environment{Name: "dev"}}, nil
}

func (s *initializationHost) GetValues(
	context.Context, *azdext.GetEnvironmentRequest,
) (*azdext.KeyValueListResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := &azdext.KeyValueListResponse{}
	for key, value := range s.values {
		result.KeyValues = append(result.KeyValues, &azdext.KeyValue{Key: key, Value: value})
	}
	return result, nil
}

func (s *initializationHost) SetValue(_ context.Context, req *azdext.SetEnvRequest) (*azdext.EmptyResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.values[req.Key] = req.Value
	return &azdext.EmptyResponse{}, nil
}

type initializationPrompts struct {
	azdext.UnimplementedPromptServiceServer
	host *initializationHost
}

func (p *initializationPrompts) PromptSubscription(
	context.Context, *azdext.PromptSubscriptionRequest,
) (*azdext.PromptSubscriptionResponse, error) {
	s := p.host
	s.mu.Lock()
	defer s.mu.Unlock()
	s.prompts++
	if s.promptErr != nil {
		return nil, s.promptErr
	}
	if s.prompts > 1 {
		return nil, errors.New("later layer must reuse the saved subscription")
	}
	return &azdext.PromptSubscriptionResponse{Subscription: &azdext.Subscription{
		Id: "sub", UserTenantId: "access-tenant", TenantId: "resource-tenant",
	}}, nil
}

func (p *initializationPrompts) PromptLocation(
	_ context.Context, req *azdext.PromptLocationRequest,
) (*azdext.PromptLocationResponse, error) {
	s := p.host
	s.mu.Lock()
	defer s.mu.Unlock()
	s.locations++
	if req.AzureContext.Scope.SubscriptionId != "sub" || req.AzureContext.Scope.TenantId != "access-tenant" {
		return nil, errors.New("location prompt must use the selected scope and access tenant")
	}
	return &azdext.PromptLocationResponse{Location: &azdext.Location{Name: "eastus"}}, nil
}

func (s *initializationHost) LookupTenant(
	context.Context, *azdext.LookupTenantRequest,
) (*azdext.LookupTenantResponse, error) {
	return &azdext.LookupTenantResponse{TenantId: "access-tenant"}, nil
}

func (s *initializationHost) GetCurrentPrincipal(
	context.Context, *azdext.GetCurrentPrincipalRequest,
) (*azdext.GetCurrentPrincipalResponse, error) {
	return &azdext.GetCurrentPrincipalResponse{
		ObjectId: "principal", PrincipalType: azdext.PrincipalType_PRINCIPAL_TYPE_USER,
	}, nil
}

type initializationProject struct {
	azdext.UnimplementedProjectServiceServer
	project map[string]any
}

func (s *initializationProject) GetConfigValue(
	_ context.Context, request *azdext.GetProjectConfigValueRequest,
) (*azdext.GetProjectConfigValueResponse, error) {
	raw, found := s.project[request.Path]
	if !found {
		return &azdext.GetProjectConfigValueResponse{}, nil
	}
	value, err := structpb.NewValue(raw)
	return &azdext.GetProjectConfigValueResponse{Found: true, Value: value}, err
}

type initializationUserConfig struct {
	azdext.UnimplementedUserConfigServiceServer
}

func (s *initializationUserConfig) GetString(
	context.Context, *azdext.GetUserConfigStringRequest,
) (*azdext.GetUserConfigStringResponse, error) {
	return &azdext.GetUserConfigStringResponse{}, nil
}

func (s *initializationProject) Get(context.Context, *azdext.EmptyRequest) (*azdext.GetProjectResponse, error) {
	return &azdext.GetProjectResponse{Project: &azdext.ProjectConfig{Name: "example"}}, nil
}

func newInitializationClient(t *testing.T, host *initializationHost) *azdext.AzdClient {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	server := grpc.NewServer()
	azdext.RegisterEnvironmentServiceServer(server, host)
	azdext.RegisterAccountServiceServer(server, host)
	azdext.RegisterUserConfigServiceServer(server, &initializationUserConfig{})
	azdext.RegisterPromptServiceServer(server, &initializationPrompts{host: host})
	azdext.RegisterProjectServiceServer(server, &initializationProject{project: host.project})
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)
	client, err := azdext.NewAzdClient(azdext.WithAddress(listener.Addr().String()))
	require.NoError(t, err)
	t.Cleanup(client.Close)
	return client
}

func TestInitializationSavesScopeForLaterProviders(t *testing.T) {
	t.Setenv("AZURE_SUBSCRIPTION_ID", "")
	t.Setenv("AZURE_LOCATION", "")
	host := &initializationHost{values: map[string]string{}}
	client := newInitializationClient(t, host)

	first := New(client)
	require.NoError(t, first.Initialize(t.Context(), t.TempDir(), testOptions(t, "data")))
	t.Cleanup(func() { require.NoError(t, first.Close()) })
	require.NotEmpty(t, first.tempDir)
	require.Equal(t, "sub", host.values["AZURE_SUBSCRIPTION_ID"])
	require.Equal(t, "eastus", host.values["AZURE_LOCATION"])

	projectPath := t.TempDir()
	for _, name := range []string{"app", "models"} {
		options := testOptions(t, name)
		p := New(client)
		err := p.Initialize(t.Context(), projectPath, options)
		require.NoError(t, errors.Join(err, p.Close()))
	}
	require.Equal(t, 1, host.prompts)
	require.Equal(t, 1, host.locations)
}

func TestInitializationScopeFailureCleansUp(t *testing.T) {
	t.Setenv("AZURE_SUBSCRIPTION_ID", "")
	t.Setenv("AZURE_LOCATION", "")
	expected := errors.New("interactive input required")
	host := &initializationHost{values: map[string]string{}, promptErr: expected}
	p := New(newInitializationClient(t, host))
	require.ErrorContains(t, p.Initialize(t.Context(), t.TempDir(), testOptions(t, "data")), expected.Error())
	require.Empty(t, p.tempDir)
	require.Empty(t, host.values)
	require.Zero(t, host.locations)
	require.NoError(t, p.Close())
}

func TestInitializationRejectsMultipleResourcesLayersBeforeAzureCalls(t *testing.T) {
	host := &initializationHost{values: map[string]string{}, project: map[string]any{
		"infra": map[string]any{"provider": "resources", "layers": []any{
			map[string]any{"name": "data"}, map[string]any{"name": "app"},
		}},
	}}
	p := New(newInitializationClient(t, host))
	require.ErrorContains(t, p.Initialize(t.Context(), t.TempDir(), testOptions(t, "data")),
		"only one resources infrastructure entry")
	require.Empty(t, p.tempDir)
	require.Empty(t, host.values)
	require.Zero(t, host.prompts)
}
