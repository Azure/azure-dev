// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"errors"
	"fmt"
	"net"
	"testing"

	"azureaiagent/internal/pkg/agents/agent_yaml"
	"azureaiagent/internal/project"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
)

func TestResolveNoPromptCapacity(t *testing.T) {
	floatPtr := func(v float64) *float64 { return &v }

	tests := []struct {
		name         string
		candidate    *azdext.AiModelDeployment
		wantCapacity int32
		wantOk       bool
	}{
		{
			name: "uses existing positive capacity",
			candidate: &azdext.AiModelDeployment{
				Capacity: 10,
				Sku:      &azdext.AiModelSku{},
			},
			wantCapacity: 10,
			wantOk:       true,
		},
		{
			name: "zero capacity defaults to defaultDeploymentCapacity",
			candidate: &azdext.AiModelDeployment{
				Capacity: 0,
				Sku:      &azdext.AiModelSku{MinCapacity: 5},
			},
			wantCapacity: defaultDeploymentCapacity,
			wantOk:       true,
		},
		{
			name: "zero capacity with zero minCapacity defaults to defaultDeploymentCapacity",
			candidate: &azdext.AiModelDeployment{
				Capacity: 0,
				Sku:      &azdext.AiModelSku{MinCapacity: 0},
			},
			wantCapacity: defaultDeploymentCapacity,
			wantOk:       true,
		},
		{
			name: "negative capacity defaults to defaultDeploymentCapacity",
			candidate: &azdext.AiModelDeployment{
				Capacity: -3,
				Sku:      &azdext.AiModelSku{MinCapacity: 2},
			},
			wantCapacity: defaultDeploymentCapacity,
			wantOk:       true,
		},
		{
			name: "rounds up to capacity step",
			candidate: &azdext.AiModelDeployment{
				Capacity: 7,
				Sku:      &azdext.AiModelSku{CapacityStep: 5},
			},
			wantCapacity: 10,
			wantOk:       true,
		},
		{
			name: "already aligned to step",
			candidate: &azdext.AiModelDeployment{
				Capacity: 10,
				Sku:      &azdext.AiModelSku{CapacityStep: 5},
			},
			wantCapacity: 10,
			wantOk:       true,
		},
		{
			name: "enforces step alignment on defaultDeploymentCapacity",
			candidate: &azdext.AiModelDeployment{
				Capacity: 0,
				Sku:      &azdext.AiModelSku{MinCapacity: 10, CapacityStep: 3},
			},
			wantCapacity: 51, // default=50, rounded up to next step of 3
			wantOk:       true,
		},
		{
			name: "exceeds maxCapacity returns false",
			candidate: &azdext.AiModelDeployment{
				Capacity: 20,
				Sku:      &azdext.AiModelSku{MaxCapacity: 10},
			},
			wantCapacity: 0,
			wantOk:       false,
		},
		{
			name: "defaultDeploymentCapacity clamped to maxCapacity",
			candidate: &azdext.AiModelDeployment{
				Capacity: 0,
				Sku:      &azdext.AiModelSku{MaxCapacity: 30},
			},
			wantCapacity: 30,
			wantOk:       true,
		},
		{
			name: "defaultDeploymentCapacity clamped and step-aligned down",
			candidate: &azdext.AiModelDeployment{
				Capacity: 0,
				Sku:      &azdext.AiModelSku{MaxCapacity: 50, CapacityStep: 7},
			},
			wantCapacity: 49, // 50/7=7*7=49
			wantOk:       true,
		},
		{
			name: "exceeds remaining quota returns false",
			candidate: &azdext.AiModelDeployment{
				Capacity:       10,
				Sku:            &azdext.AiModelSku{},
				RemainingQuota: floatPtr(5),
			},
			wantCapacity: 0,
			wantOk:       false,
		},
		{
			name: "within remaining quota returns true",
			candidate: &azdext.AiModelDeployment{
				Capacity:       5,
				Sku:            &azdext.AiModelSku{},
				RemainingQuota: floatPtr(10),
			},
			wantCapacity: 5,
			wantOk:       true,
		},
		{
			name: "nil remaining quota is not checked",
			candidate: &azdext.AiModelDeployment{
				Capacity:       100,
				Sku:            &azdext.AiModelSku{MaxCapacity: 200},
				RemainingQuota: nil,
			},
			wantCapacity: 100,
			wantOk:       true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			capacity, ok := resolveNoPromptCapacity(tt.candidate)
			assert.Equal(t, tt.wantOk, ok)
			assert.Equal(t, tt.wantCapacity, capacity)
		})
	}
}

func TestSkuPriority(t *testing.T) {
	tests := []struct {
		name     string
		skuName  string
		wantPrio int
	}{
		{
			name:     "GlobalStandard is highest priority",
			skuName:  "GlobalStandard",
			wantPrio: 0,
		},
		{
			name:     "DataZoneStandard is second priority",
			skuName:  "DataZoneStandard",
			wantPrio: 1,
		},
		{
			name:     "Standard is third priority",
			skuName:  "Standard",
			wantPrio: 2,
		},
		{
			name:     "unknown SKU returns fallback priority",
			skuName:  "UnknownSku",
			wantPrio: len(defaultSkuPriority),
		},
		{
			name:     "empty string returns fallback priority",
			skuName:  "",
			wantPrio: len(defaultSkuPriority),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := skuPriority(tt.skuName)
			assert.Equal(t, tt.wantPrio, got)
		})
	}
}

func TestPersistFirstDeploymentName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		deployments []project.Deployment
		setEnvErr   error
		wantCalled  bool
		wantKey     string
		wantValue   string
		wantErr     bool
	}{
		{
			name:        "empty deployments does not call setter",
			deployments: []project.Deployment{},
			wantCalled:  false,
		},
		{
			name:        "nil deployments does not call setter",
			deployments: nil,
			wantCalled:  false,
		},
		{
			name: "single deployment persists its name",
			deployments: []project.Deployment{
				{Name: "gpt-4o"},
			},
			wantCalled: true,
			wantKey:    "AZURE_AI_MODEL_DEPLOYMENT_NAME",
			wantValue:  "gpt-4o",
		},
		{
			name: "multiple deployments persists first name only",
			deployments: []project.Deployment{
				{Name: "gpt-4o"},
				{Name: "text-embedding-ada-002"},
			},
			wantCalled: true,
			wantKey:    "AZURE_AI_MODEL_DEPLOYMENT_NAME",
			wantValue:  "gpt-4o",
		},
		{
			name: "setter error is propagated",
			deployments: []project.Deployment{
				{Name: "gpt-4o"},
			},
			setEnvErr:  errors.New("grpc unavailable"),
			wantCalled: true,
			wantKey:    "AZURE_AI_MODEL_DEPLOYMENT_NAME",
			wantValue:  "gpt-4o",
			wantErr:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var called bool
			var gotKey, gotValue string

			setter := func(_ context.Context, key, value string) error {
				called = true
				gotKey = key
				gotValue = value
				return tt.setEnvErr
			}

			err := persistFirstDeploymentName(t.Context(), setter, tt.deployments)

			assert.Equal(t, tt.wantCalled, called, "setter call expectation mismatch")

			if tt.wantCalled {
				assert.Equal(t, tt.wantKey, gotKey)
				assert.Equal(t, tt.wantValue, gotValue)
			}

			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

type processModelsAiModelServiceServer struct {
	azdext.UnimplementedAiModelServiceServer
	deployments     map[string][]*azdext.AiModelDeployment
	deploymentIndex int
}

func (s *processModelsAiModelServiceServer) ResolveModelDeployments(
	_ context.Context,
	req *azdext.ResolveModelDeploymentsRequest,
) (*azdext.ResolveModelDeploymentsResponse, error) {
	deployments := s.deployments[req.ModelName]
	if s.deploymentIndex >= len(deployments) {
		return &azdext.ResolveModelDeploymentsResponse{}, nil
	}

	return &azdext.ResolveModelDeploymentsResponse{
		Deployments: []*azdext.AiModelDeployment{deployments[s.deploymentIndex]},
	}, nil
}

func newProcessModelsTestAzdClient(
	t *testing.T,
	envServer azdext.EnvironmentServiceServer,
	workflowServer azdext.WorkflowServiceServer,
	aiModelServer azdext.AiModelServiceServer,
) *azdext.AzdClient {
	t.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	grpcServer := grpc.NewServer()
	azdext.RegisterEnvironmentServiceServer(grpcServer, envServer)
	azdext.RegisterWorkflowServiceServer(grpcServer, workflowServer)
	azdext.RegisterAiModelServiceServer(grpcServer, aiModelServer)

	go func() {
		_ = grpcServer.Serve(listener)
	}()

	t.Cleanup(func() {
		grpcServer.Stop()
		_ = listener.Close()
	})

	azdClient, err := azdext.NewAzdClient(azdext.WithAddress(listener.Addr().String()))
	require.NoError(t, err)
	t.Cleanup(func() {
		azdClient.Close()
	})

	return azdClient
}

func processModelsTestManifest() *agent_yaml.AgentManifest {
	environmentVariables := []agent_yaml.EnvironmentVariable{
		{Name: "PRIMARY_DEPLOYMENT", Value: "{{primary}}"},
		{Name: "SECONDARY_DEPLOYMENT", Value: "{{secondary}}"},
	}

	return &agent_yaml.AgentManifest{
		Name: "test-agent",
		Template: agent_yaml.ContainerAgent{
			AgentDefinition: agent_yaml.AgentDefinition{
				Name: "test-agent",
				Kind: agent_yaml.AgentKindHosted,
			},
			EnvironmentVariables: &environmentVariables,
		},
		Resources: []any{
			agent_yaml.ModelResource{
				Resource: agent_yaml.Resource{
					Name: "primary",
					Kind: agent_yaml.ResourceKindModel,
				},
				Id: "model-one",
			},
			agent_yaml.ModelResource{
				Resource: agent_yaml.Resource{
					Name: "secondary",
					Kind: agent_yaml.ResourceKindModel,
				},
				Id: "model-two",
			},
		},
	}
}

func TestProcessModelsUsesCanonicalIndexedDeploymentReferences(t *testing.T) {
	t.Parallel()

	envServer := &testEnvironmentServiceServer{
		values: map[string]map[string]string{
			"initial": {},
			"second":  {},
		},
	}
	aiModelServer := &processModelsAiModelServiceServer{
		deployments: map[string][]*azdext.AiModelDeployment{
			"model-one": {{
				ModelName: "initial-primary",
				Format:    "OpenAI",
				Version:   "1",
				Sku:       &azdext.AiModelSku{Name: "Standard"},
				Capacity:  10,
			}, {
				ModelName: "second-primary",
				Format:    "OpenAI",
				Version:   "1",
				Sku:       &azdext.AiModelSku{Name: "Standard"},
				Capacity:  10,
			}},
			"model-two": {{
				ModelName: "initial-secondary",
				Format:    "OpenAI",
				Version:   "1",
				Sku:       &azdext.AiModelSku{Name: "Standard"},
				Capacity:  10,
			}, {
				ModelName: "second-secondary",
				Format:    "OpenAI",
				Version:   "1",
				Sku:       &azdext.AiModelSku{Name: "Standard"},
				Capacity:  10,
			}},
		},
	}
	azdClient := newProcessModelsTestAzdClient(
		t,
		envServer,
		&testWorkflowServiceServer{},
		aiModelServer,
	)

	models := map[string]*azdext.AiModel{
		"model-one": {Name: "model-one", Locations: []string{"eastus"}},
		"model-two": {Name: "model-two", Locations: []string{"eastus"}},
	}

	process := func(
		environmentName string,
	) (*InitAction, *agent_yaml.AgentManifest, []project.Deployment) {
		action := &InitAction{
			azdClient: azdClient,
			azureContext: &azdext.AzureContext{
				Scope: &azdext.AzureScope{Location: "eastus"},
			},
			environment: &azdext.Environment{Name: environmentName},
			flags:       &initFlags{noPrompt: true},
			models: &modelSelector{
				azdClient:    azdClient,
				azureContext: &azdext.AzureContext{Scope: &azdext.AzureScope{Location: "eastus"}},
				environment:  &azdext.Environment{Name: environmentName},
				flags:        &initFlags{noPrompt: true},
				modelCatalog: models,
			},
		}

		manifest, deployments, err := action.ProcessModels(
			t.Context(),
			processModelsTestManifest(),
		)
		require.NoError(t, err)
		return action, manifest, deployments
	}

	initialAction, initialManifest, initialDeployments := process("initial")
	aiModelServer.deploymentIndex = 1
	secondAction, secondManifest, secondDeployments := process("second")

	require.Len(t, initialDeployments, 2)
	assert.Equal(t, "initial-primary", initialDeployments[0].Name)
	assert.Equal(t, "initial-secondary", initialDeployments[1].Name)
	require.Len(t, secondDeployments, 2)
	assert.Equal(t, "second-primary", secondDeployments[0].Name)
	assert.Equal(t, "second-secondary", secondDeployments[1].Name)

	persist := func(action *InitAction) map[string]string {
		values := map[string]string{}
		_, err := action.persistDeploymentConfigurations(
			t.Context(),
			func(_ context.Context, key, value string) error {
				values[key] = value
				return nil
			},
		)
		require.NoError(t, err)
		return values
	}
	initialEnvironment := persist(initialAction)
	secondEnvironment := persist(secondAction)
	assert.Equal(t, "initial-primary", initialEnvironment[deploymentNameEnvKey])
	assert.Equal(t, "initial-secondary", initialEnvironment[deploymentNameEnvKey+"_2"])
	assert.Equal(t, "second-primary", secondEnvironment[deploymentNameEnvKey])
	assert.Equal(t, "second-secondary", secondEnvironment[deploymentNameEnvKey+"_2"])

	assertManifestDeploymentReferences(t, initialManifest)
	assertManifestDeploymentReferences(t, secondManifest)
	assert.NotContains(t, environmentValue(initialManifest, "PRIMARY_DEPLOYMENT"), "initial-primary")
	assert.NotContains(t, environmentValue(secondManifest, "PRIMARY_DEPLOYMENT"), "initial-primary")
	assert.Equal(
		t,
		"second-primary",
		resolveManifestEnvironmentValue(
			t,
			"PRIMARY_DEPLOYMENT",
			environmentValue(secondManifest, "PRIMARY_DEPLOYMENT"),
			secondEnvironment,
		),
	)
	assert.Equal(
		t,
		"second-secondary",
		resolveManifestEnvironmentValue(
			t,
			"SECONDARY_DEPLOYMENT",
			environmentValue(secondManifest, "SECONDARY_DEPLOYMENT"),
			secondEnvironment,
		),
	)
}

func assertManifestDeploymentReferences(
	t *testing.T,
	manifest *agent_yaml.AgentManifest,
) {
	t.Helper()

	container, ok := manifest.Template.(agent_yaml.ContainerAgent)
	require.True(t, ok)
	require.NotNil(t, container.EnvironmentVariables)
	assert.Equal(
		t,
		"${AZURE_AI_MODEL_DEPLOYMENT_NAME}",
		environmentValue(manifest, "PRIMARY_DEPLOYMENT"),
	)
	assert.Equal(
		t,
		"${AZURE_AI_MODEL_DEPLOYMENT_NAME_2}",
		environmentValue(manifest, "SECONDARY_DEPLOYMENT"),
	)
}

func environmentValue(manifest *agent_yaml.AgentManifest, name string) string {
	container := manifest.Template.(agent_yaml.ContainerAgent)
	for _, variable := range *container.EnvironmentVariables {
		if variable.Name == name {
			return variable.Value
		}
	}
	return ""
}

func resolveManifestEnvironmentValue(
	t *testing.T,
	name string,
	value string,
	environment map[string]string,
) string {
	t.Helper()
	resolved, err := project.ResolveAgentEnvironmentVariable(
		name,
		value,
		nil,
		func(key string) string {
			return environment[key]
		},
	)
	require.NoError(t, err)
	return resolved
}

func TestUpdateEnvLocation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name             string
		selectedLocation string
		existingContext  *azdext.AzureContext
		wantLocation     string // expected azureContext.Scope.Location after call
	}{
		{
			name:             "sets AZURE_AI_DEPLOYMENTS_LOCATION and updates azureContext",
			selectedLocation: "westus2",
			existingContext:  &azdext.AzureContext{Scope: &azdext.AzureScope{Location: "eastus"}},
			wantLocation:     "westus2",
		},
		{
			name:             "nil azureContext gets initialized",
			selectedLocation: "swedencentral",
			existingContext:  nil,
			wantLocation:     "swedencentral",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			envName := "test-env"
			envServer := &testEnvironmentServiceServer{
				values: map[string]map[string]string{
					envName: {},
				},
			}
			azdClient := newTestAzdClient(t, envServer, &testWorkflowServiceServer{})

			ms := &modelSelector{
				azdClient:    azdClient,
				environment:  &azdext.Environment{Name: envName},
				azureContext: tt.existingContext,
			}

			err := ms.updateEnvLocation(t.Context(), tt.selectedLocation)
			require.NoError(t, err)

			// Verify env var was persisted
			assert.Equal(t, tt.selectedLocation, envServer.values[envName]["AZURE_AI_DEPLOYMENTS_LOCATION"])

			// Verify azureContext was updated
			require.NotNil(t, ms.azureContext)
			require.NotNil(t, ms.azureContext.Scope)
			assert.Equal(t, tt.wantLocation, ms.azureContext.Scope.Location)
		})
	}
}

func TestExistingDeploymentError(t *testing.T) {
	t.Parallel()

	deployment := &project.Deployment{
		Name: "my-gpt4",
		Model: project.DeploymentModel{
			Name:    "gpt-4",
			Format:  "OpenAI",
			Version: "2024-05-13",
		},
		Sku: project.DeploymentSku{
			Name:     "Standard",
			Capacity: 10,
		},
	}

	t.Run("errors.As unwraps existingDeploymentError", func(t *testing.T) {
		t.Parallel()

		err := &existingDeploymentError{Deployment: deployment}
		wrapped := fmt.Errorf("outer: %w", err)

		existing, ok := errors.AsType[*existingDeploymentError](wrapped)
		require.True(t, ok)
		assert.Equal(t, deployment, existing.Deployment)
	})

	t.Run("Error returns descriptive message", func(t *testing.T) {
		t.Parallel()

		err := &existingDeploymentError{Deployment: deployment}
		assert.Equal(t, "user selected existing deployment", err.Error())
	})

	t.Run("does not match errModelSkipped", func(t *testing.T) {
		t.Parallel()

		err := &existingDeploymentError{Deployment: deployment}
		assert.False(t, errors.Is(err, errModelSkipped))
	})
}
