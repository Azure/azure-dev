// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package resources

import (
	"context"
	"maps"
	"net"
	"testing"

	"azure.resources/internal/resources/scaffold"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/structpb"
)

type parameterHost struct {
	location  string
	promptErr error
	setErr    error
	requests  []*azdext.PromptAiLocationWithQuotaRequest
	writes    []*azdext.SetEnvRequest
	values    map[string]string
}

type parameterPrompts struct {
	azdext.UnimplementedPromptServiceServer
	host *parameterHost
}

type parameterEnvironment struct {
	azdext.UnimplementedEnvironmentServiceServer
	host *parameterHost
}

func (p *parameterPrompts) PromptAiLocationWithQuota(
	_ context.Context, request *azdext.PromptAiLocationWithQuotaRequest,
) (*azdext.PromptAiLocationWithQuotaResponse, error) {
	h := p.host
	h.requests = append(h.requests, request)
	if h.promptErr != nil {
		return nil, h.promptErr
	}
	return &azdext.PromptAiLocationWithQuotaResponse{Location: &azdext.Location{Name: h.location}}, nil
}

func (e *parameterEnvironment) SetValue(
	_ context.Context, request *azdext.SetEnvRequest,
) (*azdext.EmptyResponse, error) {
	h := e.host
	h.writes = append(h.writes, request)
	if h.setErr != nil {
		return nil, h.setErr
	}
	h.values[request.Key] = request.Value
	return &azdext.EmptyResponse{}, nil
}

func newParameterClient(t *testing.T, host *parameterHost) *azdext.AzdClient {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	server := grpc.NewServer()
	azdext.RegisterEnvironmentServiceServer(server, &parameterEnvironment{host: host})
	azdext.RegisterPromptServiceServer(server, &parameterPrompts{host: host})
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)
	client, err := azdext.NewAzdClient(azdext.WithAddress(listener.Addr().String()))
	require.NoError(t, err)
	t.Cleanup(client.Close)
	return client
}

func aiParameterOptions(t *testing.T, name string) *azdext.ProvisioningOptions {
	t.Helper()
	config := Config{"foundry": {
		Type: AIProject,
		Models: []AiServicesModel{
			{Name: "chat", Sku: AiServicesModelSku{UsageName: "chat-quota", Capacity: 10}},
			{Name: "other-chat", Sku: AiServicesModelSku{UsageName: "chat-quota", Capacity: 20}},
			{Name: "embedding", Sku: AiServicesModelSku{UsageName: "embedding-quota", Capacity: 5}},
		},
	}}
	raw, err := config.Raw()
	require.NoError(t, err)
	value, err := structpb.NewStruct(raw)
	require.NoError(t, err)
	return &azdext.ProvisioningOptions{Name: name, Config: value}
}

func TestParameterResolverPromptsWithCombinedQuotaAndPersistsLocation(t *testing.T) {
	host := &parameterHost{location: "westus3", values: map[string]string{}}
	resolver := NewParameterResolver(newParameterClient(t, host))
	require.NoError(t, resolver.Configure(aiParameterOptions(t, "models")))
	require.Empty(t, host.requests)
	require.Empty(t, host.writes)
	values := map[string]string{"AZURE_ENV_NAME": "dev", "AZURE_SUBSCRIPTION_ID": "sub", "AZURE_LOCATION": "eastus"}
	var mapping []string

	value, prompted, err := resolver.ResolveParameter(t.Context(), "aiDeploymentsLocation", values, &mapping)
	require.NoError(t, err)
	require.Equal(t, "westus3", value)
	require.True(t, prompted)
	key := scaffold.EntryEnvKey("models", "AZURE_AI_DEPLOYMENTS_LOCATION")
	require.Equal(t, []string{key}, mapping)
	require.Equal(t, "westus3", values[key])
	require.Equal(t, "westus3", host.values[key])
	require.Len(t, host.writes, 1)
	require.Equal(t, "dev", host.writes[0].EnvName)
	require.Equal(t, key, host.writes[0].Key)
	require.Equal(t, "westus3", host.writes[0].Value)
	require.Len(t, host.requests, 1)
	request := host.requests[0]
	require.Equal(t, "sub", request.AzureContext.Scope.SubscriptionId)
	require.Equal(t, "eastus", request.DefaultValue)
	require.Equal(t, []*azdext.QuotaRequirement{
		{UsageName: "OpenAI.S0.AccountCount", MinCapacity: 1},
		{UsageName: "chat-quota", MinCapacity: 30},
		{UsageName: "embedding-quota", MinCapacity: 5},
	}, request.Requirements)
}

func TestParameterResolverReusesSavedLocationWithoutHostCalls(t *testing.T) {
	resolver := NewParameterResolver(nil)
	require.NoError(t, resolver.Configure(aiParameterOptions(t, "models")))
	key := scaffold.EntryEnvKey("models", "AZURE_AI_DEPLOYMENTS_LOCATION")
	values := map[string]string{key: "westus3"}
	before := maps.Clone(values)
	var mapping []string

	value, prompted, err := resolver.ResolveParameter(t.Context(), "aiDeploymentsLocation", values, &mapping)
	require.NoError(t, err)
	require.Equal(t, "westus3", value)
	require.False(t, prompted)
	require.Equal(t, []string{key}, mapping)
	require.Equal(t, before, values)
}

func TestAILocationsAreEntryLocal(t *testing.T) {
	host := &parameterHost{location: "westus3", values: map[string]string{}}
	resolver := NewParameterResolver(newParameterClient(t, host))
	require.NoError(t, resolver.Configure(aiParameterOptions(t, "second")))
	firstKey := scaffold.EntryEnvKey("first", "AZURE_AI_DEPLOYMENTS_LOCATION")
	secondKey := scaffold.EntryEnvKey("second", "AZURE_AI_DEPLOYMENTS_LOCATION")
	values := map[string]string{firstKey: "eastus2"}
	var mapping []string

	value, prompted, err := resolver.ResolveParameter(t.Context(), "aiDeploymentsLocation", values, &mapping)
	require.NoError(t, err)
	require.Equal(t, "westus3", value)
	require.True(t, prompted)
	require.Equal(t, []string{secondKey}, mapping)
	require.Equal(t, "eastus2", values[firstKey])
	require.Equal(t, "westus3", values[secondKey])
	require.Len(t, host.requests, 1)
	require.NotContains(t, host.values, firstKey)
}

func TestAILocationPersistenceFailureIsNotCached(t *testing.T) {
	host := &parameterHost{
		location: "westus3", values: map[string]string{}, setErr: status.Error(codes.Internal, "save failed"),
	}
	resolver := NewParameterResolver(newParameterClient(t, host))
	require.NoError(t, resolver.Configure(aiParameterOptions(t, "models")))
	values := map[string]string{"AZURE_ENV_NAME": "dev"}
	before := maps.Clone(values)
	var mapping []string

	value, _, err := resolver.ResolveParameter(t.Context(), "aiDeploymentsLocation", values, &mapping)
	require.ErrorContains(t, err, "save failed")
	require.Equal(t, codes.Internal, status.Code(err))
	require.Nil(t, value)
	require.Equal(t, before, values)
	require.Empty(t, host.values)

	host.setErr = nil
	mapping = nil
	value, prompted, err := resolver.ResolveParameter(t.Context(), "aiDeploymentsLocation", values, &mapping)
	require.NoError(t, err)
	require.Equal(t, "westus3", value)
	require.True(t, prompted)
	key := scaffold.EntryEnvKey("models", "AZURE_AI_DEPLOYMENTS_LOCATION")
	require.Equal(t, []string{key}, mapping)
	require.Equal(t, "westus3", values[key])
	require.Equal(t, "westus3", host.values[key])
	require.Len(t, host.requests, 2)
	require.Len(t, host.writes, 2)
}

func TestAILocationPromptFailures(t *testing.T) {
	for _, test := range []struct {
		name string
		err  error
		code codes.Code
	}{
		{name: "no locations", err: status.Error(codes.NotFound, "no locations with quota"), code: codes.NotFound},
		{name: "no prompt", err: status.Error(codes.FailedPrecondition, "interactive input required"),
			code: codes.FailedPrecondition},
		{name: "canceled", err: status.Error(codes.Canceled, "canceled"), code: codes.Canceled},
		{name: "empty selection"},
	} {
		t.Run(test.name, func(t *testing.T) {
			host := &parameterHost{promptErr: test.err, values: map[string]string{}}
			resolver := NewParameterResolver(newParameterClient(t, host))
			require.NoError(t, resolver.Configure(aiParameterOptions(t, "models")))
			values := map[string]string{"AZURE_ENV_NAME": "dev"}
			before := maps.Clone(values)
			var mapping []string

			value, _, err := resolver.ResolveParameter(t.Context(), "aiDeploymentsLocation", values, &mapping)
			require.Error(t, err)
			require.Nil(t, value)
			require.Equal(t, before, values)
			require.Empty(t, host.writes)
			require.Len(t, host.requests, 1)
			if test.err != nil {
				require.Equal(t, test.code, status.Code(err))
			} else {
				require.ErrorContains(t, err, "location selection is empty")
			}
		})
	}
}

func TestParameterResolverRejectsInvalidOptions(t *testing.T) {
	config, err := structpb.NewStruct(map[string]any{"cache": map[string]any{"type": "unsupported"}})
	require.NoError(t, err)
	for _, test := range []struct {
		name    string
		options *azdext.ProvisioningOptions
	}{
		{name: "nil options"},
		{name: "invalid resource config", options: &azdext.ProvisioningOptions{Config: config}},
	} {
		t.Run(test.name, func(t *testing.T) {
			resolver := NewParameterResolver(nil)
			require.Error(t, resolver.Configure(test.options))
		})
	}
}

func TestAIQuotaRequirementsForModelFreeProject(t *testing.T) {
	requirements, err := aiQuotaRequirements(Config{"foundry": {Type: AIProject}})
	require.NoError(t, err)
	require.Len(t, requirements, 1)
	require.Equal(t, "OpenAI.S0.AccountCount", requirements[0].UsageName)
	require.Equal(t, 1.0, requirements[0].MinCapacity)
}

func TestAIQuotaRequirementsSkipUnmanagedAIProjects(t *testing.T) {
	for _, config := range []Config{
		{"cache": {Type: Redis}},
		{"foundry": {Type: AIProject, Existing: true}},
	} {
		requirements, err := aiQuotaRequirements(config)
		require.NoError(t, err)
		require.Nil(t, requirements)
	}
}

func TestParameterResolverRejectsInvalidModelQuotaBeforeHostCalls(t *testing.T) {
	for _, sku := range []AiServicesModelSku{
		{Capacity: 10},
		{UsageName: "chat-quota"},
		{UsageName: "chat-quota", Capacity: -1},
	} {
		config := Config{"foundry": {
			Type:   AIProject,
			Models: []AiServicesModel{{Name: "chat", Sku: sku}},
		}}
		raw, err := config.Raw()
		require.NoError(t, err)
		value, err := structpb.NewStruct(raw)
		require.NoError(t, err)
		resolver := NewParameterResolver(nil)
		err = resolver.Configure(&azdext.ProvisioningOptions{Config: value})
		require.ErrorContains(t, err, "requires a quota usage name and positive capacity")
	}
}

func TestParameterResolverLeavesUnmanagedParametersToBicep(t *testing.T) {
	for _, test := range []struct {
		name       string
		parameters []string
		config     map[string]any
	}{
		{name: "no resource config", parameters: []string{"location", "aiDeploymentsLocation"}},
		{name: "managed AI project", parameters: []string{"location"},
			config: map[string]any{"foundry": map[string]any{"type": string(AIProject)}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			var config *structpb.Struct
			if test.config != nil {
				var err error
				config, err = structpb.NewStruct(test.config)
				require.NoError(t, err)
			}
			resolver := NewParameterResolver(nil)
			require.NoError(t, resolver.Configure(&azdext.ProvisioningOptions{Config: config}))
			for _, name := range test.parameters {
				mapping := []string{}
				value, prompted, err := resolver.ResolveParameter(t.Context(), name, map[string]string{}, &mapping)
				require.NoError(t, err)
				require.Nil(t, value)
				require.False(t, prompted)
				require.Empty(t, mapping)
			}
		})
	}
}

func TestParameterResolverReconfigurationClearsAIRequirements(t *testing.T) {
	config, err := structpb.NewStruct(map[string]any{"foundry": map[string]any{"type": string(AIProject)}})
	require.NoError(t, err)
	resolver := NewParameterResolver(nil)
	require.NoError(t, resolver.Configure(&azdext.ProvisioningOptions{Name: "first", Config: config}))
	require.NotEmpty(t, resolver.requirements)
	firstKey := resolver.locationKey

	require.NoError(t, resolver.Configure(&azdext.ProvisioningOptions{Name: "second"}))
	require.Nil(t, resolver.requirements)
	require.NotEqual(t, firstKey, resolver.locationKey)
	var mapping []string
	value, prompted, err := resolver.ResolveParameter(t.Context(), "aiDeploymentsLocation", map[string]string{}, &mapping)
	require.NoError(t, err)
	require.Nil(t, value)
	require.False(t, prompted)
	require.Empty(t, mapping)
}
