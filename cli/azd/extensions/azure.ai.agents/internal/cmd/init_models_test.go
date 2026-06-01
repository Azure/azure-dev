// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"azureaiagent/internal/project"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
			name: "zero capacity defaults to max(minCapacity, 1)",
			candidate: &azdext.AiModelDeployment{
				Capacity: 0,
				Sku:      &azdext.AiModelSku{MinCapacity: 5},
			},
			wantCapacity: 5,
			wantOk:       true,
		},
		{
			name: "zero capacity with zero minCapacity defaults to 1",
			candidate: &azdext.AiModelDeployment{
				Capacity: 0,
				Sku:      &azdext.AiModelSku{MinCapacity: 0},
			},
			wantCapacity: 1,
			wantOk:       true,
		},
		{
			name: "negative capacity defaults to max(minCapacity, 1)",
			candidate: &azdext.AiModelDeployment{
				Capacity: -3,
				Sku:      &azdext.AiModelSku{MinCapacity: 2},
			},
			wantCapacity: 2,
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
			name: "enforces minCapacity then step alignment",
			candidate: &azdext.AiModelDeployment{
				Capacity: 0,
				Sku:      &azdext.AiModelSku{MinCapacity: 10, CapacityStep: 3},
			},
			wantCapacity: 12, // min=10, rounded up to next step of 3
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

func TestNewModelSelector_PopulatesSupportedRegions(t *testing.T) {
	resetRegionsCache(t, []string{"eastus2", "westus3"})

	ms, err := newModelSelector(
		t.Context(),
		nil, // azdClient is unused by construction
		&azdext.AzureContext{Scope: &azdext.AzureScope{Location: "eastus2"}},
		&azdext.Environment{Name: "test-env"},
		&initFlags{},
	)
	require.NoError(t, err)
	require.NotNil(t, ms)
	assert.Equal(t, []string{"eastus2", "westus3"}, ms.supportedRegions)
	// Mutating the returned slice must not affect the global cache.
	ms.supportedRegions[0] = "mutated"
	again, err := supportedRegionsForInit(t.Context())
	require.NoError(t, err)
	assert.Equal(t, []string{"eastus2", "westus3"}, again)
}

func TestGetModelSelector_MemoizesAcrossCalls(t *testing.T) {
	resetRegionsCache(t, []string{"eastus2"})

	action := &InitAction{
		azureContext: &azdext.AzureContext{Scope: &azdext.AzureScope{Location: "eastus2"}},
		environment:  &azdext.Environment{Name: "test-env"},
		flags:        &initFlags{},
	}

	first, err := action.getModelSelector(t.Context())
	require.NoError(t, err)
	require.NotNil(t, first)
	assert.Equal(t, []string{"eastus2"}, first.supportedRegions)

	// Mutate selector state so we can confirm the same instance is returned —
	// memoization is what preserves modelCatalog/locationWarningShown across
	// the per-model loop in ProcessModels.
	first.locationWarningShown = true
	first.modelCatalog = map[string]*azdext.AiModel{"gpt-4.1-mini": {Name: "gpt-4.1-mini"}}

	second, err := action.getModelSelector(t.Context())
	require.NoError(t, err)
	assert.Same(t, first, second, "getModelSelector must return the cached instance")
	assert.True(t, second.locationWarningShown)
	assert.Contains(t, second.modelCatalog, "gpt-4.1-mini")
}

func TestNewModelSelector_PropagatesContextCancellation(t *testing.T) {
	// Empty cache forces a fetch; canceled ctx makes the select in
	// supportedRegionsForInit return ctx.Err without waiting on the background fetch.
	resetRegionsCache(t, nil)

	// Point the fetch at a server that hangs long enough that the canceled-ctx
	// branch wins the select even on slow CI. The fetch goroutine itself uses
	// context.WithoutCancel and will eventually time out via the fetch's own
	// timeout; we don't wait for it.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	t.Cleanup(server.Close)

	prev := hostedAgentRegionsURL
	hostedAgentRegionsURL = server.URL
	t.Cleanup(func() { hostedAgentRegionsURL = prev })

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	ms, err := newModelSelector(
		ctx,
		nil,
		&azdext.AzureContext{Scope: &azdext.AzureScope{Location: "eastus2"}},
		&azdext.Environment{Name: "test-env"},
		&initFlags{},
	)
	require.Error(t, err)
	require.ErrorIs(t, err, context.Canceled)
	assert.Nil(t, ms)
}
