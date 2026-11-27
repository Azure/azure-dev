// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"azureaiagent/internal/exterrors"
	"azureaiagent/internal/inittelemetry"
	"azureaiagent/internal/project"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
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
				Sku:      &azdext.AiModelSku{MinCapacity: 10, CapacityStep: 7},
			},
			wantCapacity: 248, // baseline=10, (250-10)=240, step-aligned down is 10 + 34*7 = 248
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
			name: "defaultDeploymentCapacity clamped to remaining quota",
			candidate: &azdext.AiModelDeployment{
				Capacity:       0,
				Sku:            &azdext.AiModelSku{MinCapacity: 1, CapacityStep: 1},
				RemainingQuota: floatPtr(150),
			},
			wantCapacity: 150,
			wantOk:       true,
		},
		{
			name: "defaultDeploymentCapacity clamped and step-aligned down to remaining quota",
			candidate: &azdext.AiModelDeployment{
				Capacity:       0,
				Sku:            &azdext.AiModelSku{MinCapacity: 10, CapacityStep: 10},
				RemainingQuota: floatPtr(155),
			},
			wantCapacity: 150,
			wantOk:       true,
		},
		{
			name: "defaultDeploymentCapacity clamped relative to non-multiple minCapacity and remaining quota",
			candidate: &azdext.AiModelDeployment{
				Capacity:       0,
				Sku:            &azdext.AiModelSku{MinCapacity: 1, CapacityStep: 10},
				RemainingQuota: floatPtr(155),
			},
			wantCapacity: 151,
			wantOk:       true,
		},
		{
			name: "defaultDeploymentCapacity clamped by both maxCapacity and remaining quota",
			candidate: &azdext.AiModelDeployment{
				Capacity:       0,
				Sku:            &azdext.AiModelSku{MinCapacity: 1, MaxCapacity: 120, CapacityStep: 10},
				RemainingQuota: floatPtr(155),
			},
			wantCapacity: 111,
			wantOk:       true,
		},
		{
			name: "defaultDeploymentCapacity remaining quota below minCapacity returns false",
			candidate: &azdext.AiModelDeployment{
				Capacity:       0,
				Sku:            &azdext.AiModelSku{MinCapacity: 20, CapacityStep: 10},
				RemainingQuota: floatPtr(15),
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

type capacityFallbackTestServer struct {
	azdext.UnimplementedPromptServiceServer
	azdext.UnimplementedAiModelServiceServer

	promptRequests  []*azdext.PromptAiDeploymentRequest
	resolveRequests []*azdext.ResolveModelDeploymentsRequest
	firstErrReason  string
	fallbackCap     int32
	remainingQuota  *float64
	sku             *azdext.AiModelSku
	candidates      []*azdext.AiModelDeployment
}

func newAiReasonError(reason string) error {
	st, err := status.New(codes.FailedPrecondition, reason).WithDetails(&errdetails.ErrorInfo{
		Reason: reason,
		Domain: azdext.AiErrorDomain,
	})
	if err != nil {
		panic(err)
	}
	return st.Err()
}

func (s *capacityFallbackTestServer) PromptAiDeployment(
	_ context.Context,
	req *azdext.PromptAiDeploymentRequest,
) (*azdext.PromptAiDeploymentResponse, error) {
	s.promptRequests = append(s.promptRequests, req)
	if len(s.candidates) > 0 {
		return s.promptFromCandidates(req)
	}
	if len(s.promptRequests) == 1 && s.firstErrReason != "" &&
		req.GetOptions().GetCapacity() == defaultDeploymentCapacity {
		return nil, newAiReasonError(s.firstErrReason)
	}
	capVal := defaultDeploymentCapacity
	if req.GetOptions().Capacity != nil {
		capVal = req.GetOptions().GetCapacity()
	} else if s.fallbackCap > 0 {
		capVal = s.fallbackCap
	}
	return &azdext.PromptAiDeploymentResponse{
		Deployment: &azdext.AiModelDeployment{
			ModelName: req.GetModelName(),
			Format:    "OpenAI",
			Version:   "2025-04-14",
			Sku:       &azdext.AiModelSku{Name: "GlobalStandard"},
			Capacity:  capVal,
		},
	}, nil
}

func (s *capacityFallbackTestServer) promptFromCandidates(
	req *azdext.PromptAiDeploymentRequest,
) (*azdext.PromptAiDeploymentResponse, error) {
	opts := req.GetOptions()
	var matched []*azdext.AiModelDeployment
	for _, c := range s.candidates {
		if len(opts.GetVersions()) > 0 && c.GetVersion() != opts.GetVersions()[0] {
			continue
		}
		if len(opts.GetSkus()) > 0 && c.GetSku().GetName() != opts.GetSkus()[0] {
			continue
		}
		sku := c.GetSku()
		capVal := sku.GetDefaultCapacity()
		if opts.Capacity != nil {
			if capacityValidForSku(sku, opts.GetCapacity()) {
				capVal = opts.GetCapacity()
			}
		}
		if c.RemainingQuota != nil && float64(capVal) > *c.RemainingQuota {
			continue
		}
		matched = append(matched, cloneDeploymentWithCapacity(c, capVal))
	}
	if len(matched) == 0 {
		return nil, newAiReasonError(azdext.AiErrorReasonNoValidSkus)
	}
	// Simulate user selecting the last available SKU when unscoped.
	return &azdext.PromptAiDeploymentResponse{
		Deployment: matched[len(matched)-1],
	}, nil
}

func (s *capacityFallbackTestServer) ResolveModelDeployments(
	_ context.Context,
	req *azdext.ResolveModelDeploymentsRequest,
) (*azdext.ResolveModelDeploymentsResponse, error) {
	s.resolveRequests = append(s.resolveRequests, req)
	if req.GetOptions().Capacity != nil && len(s.resolveRequests) == 1 && s.firstErrReason != "" {
		return nil, newAiReasonError(s.firstErrReason)
	}
	if len(s.candidates) > 0 {
		return &azdext.ResolveModelDeploymentsResponse{
			Deployments: s.candidates,
		}, nil
	}
	capVal := defaultDeploymentCapacity
	if req.GetOptions().Capacity == nil || s.fallbackCap > 0 {
		if req.GetOptions().Capacity == nil || s.sku != nil {
			capVal = s.fallbackCap
		}
	}
	sku := s.sku
	if sku == nil {
		sku = &azdext.AiModelSku{
			Name:            "GlobalStandard",
			MinCapacity:     1,
			MaxCapacity:     1000,
			CapacityStep:    1,
			DefaultCapacity: 10,
		}
	}
	return &azdext.ResolveModelDeploymentsResponse{
		Deployments: []*azdext.AiModelDeployment{
			{
				ModelName:      req.GetModelName(),
				Format:         "OpenAI",
				Version:        "2025-04-14",
				Sku:            sku,
				Capacity:       capVal,
				RemainingQuota: s.remainingQuota,
			},
		},
	}, nil
}

func newCapacityFallbackAzdClient(t *testing.T, srv *capacityFallbackTestServer) *azdext.AzdClient {
	t.Helper()

	grpcServer := grpc.NewServer()
	azdext.RegisterPromptServiceServer(grpcServer, srv)
	azdext.RegisterAiModelServiceServer(grpcServer, srv)

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	go func() { _ = grpcServer.Serve(listener) }()
	t.Cleanup(func() {
		grpcServer.Stop()
		_ = listener.Close()
	})

	client, err := azdext.NewAzdClient(azdext.WithAddress(listener.Addr().String()))
	require.NoError(t, err)
	t.Cleanup(func() { client.Close() })
	return client
}

func TestPromptAiDeploymentWithFallback(t *testing.T) {
	azureCtx := &azdext.AzureContext{
		Scope: &azdext.AzureScope{SubscriptionId: "sub", Location: "eastus2"},
	}

	t.Run("succeeds with defaultDeploymentCapacity on first call", func(t *testing.T) {
		srv := &capacityFallbackTestServer{}
		client := newCapacityFallbackAzdClient(t, srv)
		ms := &modelSelector{azdClient: client, azureContext: azureCtx}

		resp, err := ms.promptAiDeploymentWithFallback(t.Context(), "gpt-5.4-mini", "eastus2")
		require.NoError(t, err)
		require.Len(t, srv.promptRequests, 1)
		require.NotNil(t, srv.promptRequests[0].GetOptions().Capacity)
		assert.Equal(t, defaultDeploymentCapacity, srv.promptRequests[0].GetOptions().GetCapacity())
		assert.Equal(t, defaultDeploymentCapacity, resp.GetDeployment().GetCapacity())
	})

	for _, reason := range []string{
		azdext.AiErrorReasonNoValidSkus,
		azdext.AiErrorReasonNoDeploymentMatch,
	} {
		t.Run("prompts with clamped quota capacity on "+reason, func(t *testing.T) {
			srv := &capacityFallbackTestServer{
				firstErrReason: reason,
				fallbackCap:    10,
				remainingQuota: new(float64(120)),
			}
			client := newCapacityFallbackAzdClient(t, srv)
			ms := &modelSelector{azdClient: client, azureContext: azureCtx}

			resp, err := ms.promptAiDeploymentWithFallback(t.Context(), "gpt-5", "eastus2")
			require.NoError(t, err)
			require.Len(t, srv.promptRequests, 1)
			require.NotNil(t, srv.promptRequests[0].GetOptions().Capacity)
			assert.Equal(t, int32(120), srv.promptRequests[0].GetOptions().GetCapacity())
			assert.Empty(t, srv.promptRequests[0].GetOptions().GetVersions())
			assert.Equal(t, []string{"GlobalStandard"}, srv.promptRequests[0].GetOptions().GetSkus())
			assert.Equal(t, int32(120), resp.GetDeployment().GetCapacity())
		})

		t.Run("scopes prompt to selected candidate across differing SKU steps on "+reason, func(t *testing.T) {
			srv := &capacityFallbackTestServer{
				firstErrReason: reason,
				candidates: []*azdext.AiModelDeployment{
					{
						ModelName: "gpt-5",
						Format:    "OpenAI",
						Version:   "2025-08-07",
						Sku: &azdext.AiModelSku{
							Name:            "GlobalStandard",
							MinCapacity:     1,
							MaxCapacity:     1000,
							CapacityStep:    1,
							DefaultCapacity: 10,
						},
						Capacity:       10,
						RemainingQuota: new(float64(120)),
					},
					{
						ModelName: "gpt-5",
						Format:    "OpenAI",
						Version:   "2025-08-07",
						Sku: &azdext.AiModelSku{
							Name:            "Standard",
							MinCapacity:     10,
							MaxCapacity:     1000,
							CapacityStep:    20,
							DefaultCapacity: 10,
						},
						Capacity:       10,
						RemainingQuota: new(float64(100)),
					},
				},
			}
			client := newCapacityFallbackAzdClient(t, srv)
			ms := &modelSelector{azdClient: client, azureContext: azureCtx}

			resp, err := ms.promptAiDeploymentWithFallback(t.Context(), "gpt-5", "eastus2")
			require.NoError(t, err)
			require.Len(t, srv.promptRequests, 1)
			assert.Equal(t, []string{"2025-08-07"}, srv.promptRequests[0].GetOptions().GetVersions())
			assert.Equal(t, []string{"GlobalStandard"}, srv.promptRequests[0].GetOptions().GetSkus())
			assert.Equal(t, int32(120), srv.promptRequests[0].GetOptions().GetCapacity())
			assert.Equal(t, "GlobalStandard", resp.GetDeployment().GetSku().GetName())
			assert.Equal(t, int32(120), resp.GetDeployment().GetCapacity())
		})

		t.Run("prefers GlobalStandard clamped over DataZoneStandard or GlobalBatch at 250 on "+reason, func(t *testing.T) {
			srv := &capacityFallbackTestServer{
				candidates: []*azdext.AiModelDeployment{
					{
						ModelName: "gpt-6-luna",
						Format:    "OpenAI",
						Version:   "2026-01-01",
						Sku: &azdext.AiModelSku{
							Name:            "GlobalBatch",
							MinCapacity:     1,
							MaxCapacity:     1000,
							CapacityStep:    1,
							DefaultCapacity: 10,
						},
						Capacity:       10,
						RemainingQuota: new(float64(1000)),
					},
					{
						ModelName: "gpt-6-luna",
						Format:    "OpenAI",
						Version:   "2026-01-01",
						Sku: &azdext.AiModelSku{
							Name:            "DataZoneStandard",
							MinCapacity:     1,
							MaxCapacity:     1000,
							CapacityStep:    1,
							DefaultCapacity: 10,
						},
						Capacity:       10,
						RemainingQuota: new(float64(233)),
					},
					{
						ModelName: "gpt-6-luna",
						Format:    "OpenAI",
						Version:   "2026-01-01",
						Sku: &azdext.AiModelSku{
							Name:            "GlobalStandard",
							MinCapacity:     1,
							MaxCapacity:     1000,
							CapacityStep:    1,
							DefaultCapacity: 10,
						},
						Capacity:       10,
						RemainingQuota: new(float64(125)),
					},
				},
			}
			client := newCapacityFallbackAzdClient(t, srv)
			ms := &modelSelector{azdClient: client, azureContext: azureCtx}

			resp, err := ms.promptAiDeploymentWithFallback(t.Context(), "gpt-6-luna", "eastus2")
			require.NoError(t, err)
			require.Len(t, srv.promptRequests, 1)
			assert.Equal(t, []string{"2026-01-01"}, srv.promptRequests[0].GetOptions().GetVersions())
			assert.Equal(t, []string{"GlobalStandard"}, srv.promptRequests[0].GetOptions().GetSkus())
			assert.Equal(t, int32(125), srv.promptRequests[0].GetOptions().GetCapacity())
			assert.Equal(t, "GlobalStandard", resp.GetDeployment().GetSku().GetName())
			assert.Equal(t, int32(125), resp.GetDeployment().GetCapacity())
		})

		t.Run("falls back to nil capacity when clamped capacity unavailable on "+reason, func(t *testing.T) {
			srv := &capacityFallbackTestServer{
				firstErrReason: reason,
				fallbackCap:    120,
			}
			client := newCapacityFallbackAzdClient(t, srv)
			ms := &modelSelector{azdClient: client, azureContext: azureCtx}

			resp, err := ms.promptAiDeploymentWithFallback(t.Context(), "gpt-5", "eastus2")
			require.NoError(t, err)
			require.Len(t, srv.promptRequests, 2)
			require.NotNil(t, srv.promptRequests[0].GetOptions().Capacity)
			assert.Equal(t, defaultDeploymentCapacity, srv.promptRequests[0].GetOptions().GetCapacity())
			assert.Nil(t, srv.promptRequests[1].GetOptions().Capacity)
			assert.Equal(t, int32(120), resp.GetDeployment().GetCapacity())
		})
	}
}

func TestResolveModelDeploymentFallbackClampsToRemainingQuota(t *testing.T) {
	azureCtx := &azdext.AzureContext{
		Scope: &azdext.AzureScope{SubscriptionId: "sub", Location: "eastus2"},
	}
	model := &azdext.AiModel{
		Name:      "gpt-5",
		Locations: []string{"eastus2"},
		Versions: []*azdext.AiModelVersion{
			{Version: "2025-04-14", IsDefault: true},
		},
	}

	srv := &capacityFallbackTestServer{
		fallbackCap:    10,
		remainingQuota: new(float64(150)),
	}
	client := newCapacityFallbackAzdClient(t, srv)

	deployment, err := resolveModelDeployment(t.Context(), client, azureCtx, model, "eastus2")
	require.NoError(t, err)
	require.Len(t, srv.resolveRequests, 1)
	assert.Nil(t, srv.resolveRequests[0].GetOptions().Capacity)
	assert.Equal(t, int32(150), deployment.GetCapacity())
}

func TestResolveModelDeploymentClampsWhenCoreSubstitutesDefaultCapacity(t *testing.T) {
	azureCtx := &azdext.AzureContext{
		Scope: &azdext.AzureScope{SubscriptionId: "sub", Location: "eastus2"},
	}
	model := &azdext.AiModel{
		Name:      "gpt-5",
		Locations: []string{"eastus2"},
		Versions: []*azdext.AiModelVersion{
			{Version: "2025-04-14", IsDefault: true},
		},
	}

	t.Run("clamps to maxCapacity instead of retaining substituted SKU default 10", func(t *testing.T) {
		srv := &capacityFallbackTestServer{
			fallbackCap:    10,
			remainingQuota: new(float64(150)),
			sku: &azdext.AiModelSku{
				Name:            "GlobalStandard",
				MinCapacity:     1,
				MaxCapacity:     100,
				CapacityStep:    1,
				DefaultCapacity: 10,
			},
		}
		client := newCapacityFallbackAzdClient(t, srv)

		deployment, err := resolveModelDeployment(t.Context(), client, azureCtx, model, "eastus2")
		require.NoError(t, err)
		require.Len(t, srv.resolveRequests, 1)
		assert.Equal(t, int32(100), deployment.GetCapacity())
	})

	t.Run("clamps to baseline-relative step instead of retaining substituted SKU default 11", func(t *testing.T) {
		srv := &capacityFallbackTestServer{
			fallbackCap:    11,
			remainingQuota: new(float64(155)),
			sku: &azdext.AiModelSku{
				Name:            "GlobalStandard",
				MinCapacity:     1,
				MaxCapacity:     1000,
				CapacityStep:    10,
				DefaultCapacity: 11,
			},
		}
		client := newCapacityFallbackAzdClient(t, srv)

		deployment, err := resolveModelDeployment(t.Context(), client, azureCtx, model, "eastus2")
		require.NoError(t, err)
		require.Len(t, srv.resolveRequests, 1)
		assert.Equal(t, int32(151), deployment.GetCapacity())
	})

	t.Run("clamps downward to 200 instead of rounding up to 300 when quota is 1000", func(t *testing.T) {
		srv := &capacityFallbackTestServer{
			fallbackCap:    100,
			remainingQuota: new(float64(1000)),
			sku: &azdext.AiModelSku{
				Name:            "GlobalStandard",
				MinCapacity:     100,
				MaxCapacity:     1000,
				CapacityStep:    100,
				DefaultCapacity: 100,
			},
		}
		client := newCapacityFallbackAzdClient(t, srv)

		deployment, err := resolveModelDeployment(t.Context(), client, azureCtx, model, "eastus2")
		require.NoError(t, err)
		require.Len(t, srv.resolveRequests, 1)
		assert.Equal(t, int32(200), deployment.GetCapacity())
	})
}

type resolveDeploymentStep struct {
	deployments []*azdext.AiModelDeployment
	err         error
}

type promptDeploymentStep struct {
	deployment *azdext.AiModelDeployment
	err        error
}

type unifiedModelTestServer struct {
	azdext.UnimplementedPromptServiceServer
	azdext.UnimplementedAiModelServiceServer

	catalogModels              []*azdext.AiModel
	promptedModel              *azdext.AiModel
	promptedModels             []*azdext.AiModel
	resolvedCandidates         map[string][]*azdext.AiModelDeployment
	resolveSequence            []resolveDeploymentStep
	resolveErr                 error
	resolveErrByModel          map[string]error
	promptDeployment           *azdext.AiModelDeployment
	promptDeploymentSequence   []promptDeploymentStep
	promptDeploymentErr        error
	promptDeploymentErrByModel map[string]error
	selectResponses            []int32
	locationWithQuotaResult    string

	selectRequests            []*azdext.SelectRequest
	textPromptRequests        []*azdext.PromptRequest
	promptAiModelRequests     []*azdext.PromptAiModelRequest
	promptDeploymentRequests  []*azdext.PromptAiDeploymentRequest
	resolveDeploymentRequests []*azdext.ResolveModelDeploymentsRequest
	locationWithQuotaRequests []*azdext.PromptAiModelLocationWithQuotaRequest
	listModelsCalls           int
	resolveCalls              int
}

func (s *unifiedModelTestServer) Select(
	_ context.Context,
	req *azdext.SelectRequest,
) (*azdext.SelectResponse, error) {
	idx := len(s.selectRequests)
	s.selectRequests = append(s.selectRequests, req)
	if idx >= len(s.selectResponses) {
		return nil, status.Error(codes.FailedPrecondition, "unexpected Select prompt")
	}
	return &azdext.SelectResponse{Value: new(s.selectResponses[idx])}, nil
}

func (s *unifiedModelTestServer) Prompt(
	_ context.Context,
	req *azdext.PromptRequest,
) (*azdext.PromptResponse, error) {
	s.textPromptRequests = append(s.textPromptRequests, req)
	return nil, status.Error(codes.FailedPrecondition, "unexpected text Prompt")
}

func (s *unifiedModelTestServer) PromptAiModel(
	_ context.Context,
	req *azdext.PromptAiModelRequest,
) (*azdext.PromptAiModelResponse, error) {
	idx := len(s.promptAiModelRequests)
	s.promptAiModelRequests = append(s.promptAiModelRequests, req)
	var model *azdext.AiModel
	if idx < len(s.promptedModels) {
		model = s.promptedModels[idx]
	} else {
		model = s.promptedModel
	}
	if model == nil {
		model = &azdext.AiModel{
			Name:      defaultAgentModel,
			Locations: []string{"eastus2"},
			Versions:  []*azdext.AiModelVersion{{Version: "2026-01-01", IsDefault: true}},
		}
	}
	return &azdext.PromptAiModelResponse{Model: model}, nil
}

func (s *unifiedModelTestServer) PromptAiDeployment(
	_ context.Context,
	req *azdext.PromptAiDeploymentRequest,
) (*azdext.PromptAiDeploymentResponse, error) {
	idx := len(s.promptDeploymentRequests)
	s.promptDeploymentRequests = append(s.promptDeploymentRequests, req)
	if idx < len(s.promptDeploymentSequence) {
		step := s.promptDeploymentSequence[idx]
		if step.err != nil {
			return nil, step.err
		}
		return &azdext.PromptAiDeploymentResponse{Deployment: step.deployment}, nil
	}
	if err, ok := s.promptDeploymentErrByModel[req.GetModelName()]; ok && err != nil {
		return nil, err
	}
	if s.promptDeploymentErr != nil {
		return nil, s.promptDeploymentErr
	}
	dep := s.promptDeployment
	if dep == nil {
		dep = &azdext.AiModelDeployment{
			ModelName: req.GetModelName(),
			Format:    "OpenAI",
			Version:   "2026-01-01",
			Sku:       &azdext.AiModelSku{Name: "GlobalStandard"},
			Capacity:  defaultDeploymentCapacity,
		}
	}
	return &azdext.PromptAiDeploymentResponse{Deployment: dep}, nil
}

func (s *unifiedModelTestServer) PromptAiModelLocationWithQuota(
	_ context.Context,
	req *azdext.PromptAiModelLocationWithQuotaRequest,
) (*azdext.PromptAiModelLocationWithQuotaResponse, error) {
	s.locationWithQuotaRequests = append(s.locationWithQuotaRequests, req)
	return &azdext.PromptAiModelLocationWithQuotaResponse{
		Location: &azdext.Location{Name: s.locationWithQuotaResult},
	}, nil
}

func (s *unifiedModelTestServer) ListModels(
	_ context.Context,
	_ *azdext.ListModelsRequest,
) (*azdext.ListModelsResponse, error) {
	s.listModelsCalls++
	return &azdext.ListModelsResponse{Models: s.catalogModels}, nil
}

func (s *unifiedModelTestServer) ResolveModelDeployments(
	_ context.Context,
	req *azdext.ResolveModelDeploymentsRequest,
) (*azdext.ResolveModelDeploymentsResponse, error) {
	idx := len(s.resolveDeploymentRequests)
	s.resolveCalls++
	s.resolveDeploymentRequests = append(s.resolveDeploymentRequests, req)
	if idx < len(s.resolveSequence) {
		step := s.resolveSequence[idx]
		if step.err != nil {
			return nil, step.err
		}
		return &azdext.ResolveModelDeploymentsResponse{Deployments: step.deployments}, nil
	}
	if err, ok := s.resolveErrByModel[req.GetModelName()]; ok && err != nil {
		return nil, err
	}
	if s.resolveErr != nil {
		return nil, s.resolveErr
	}
	var location string
	if locs := req.GetOptions().GetLocations(); len(locs) > 0 {
		location = locs[0]
	}
	if s.resolvedCandidates != nil {
		if byKey, ok := s.resolvedCandidates[req.GetModelName()+"@"+location]; ok {
			return &azdext.ResolveModelDeploymentsResponse{Deployments: byKey}, nil
		}
		if byName, ok := s.resolvedCandidates[req.GetModelName()]; ok {
			return &azdext.ResolveModelDeploymentsResponse{Deployments: byName}, nil
		}
		return &azdext.ResolveModelDeploymentsResponse{}, nil
	}
	return &azdext.ResolveModelDeploymentsResponse{
		Deployments: []*azdext.AiModelDeployment{
			{
				ModelName: req.GetModelName(),
				Format:    "OpenAI",
				Version:   "2026-01-01",
				Sku: &azdext.AiModelSku{
					Name:            "GlobalStandard",
					MinCapacity:     1,
					MaxCapacity:     1000,
					CapacityStep:    1,
					DefaultCapacity: 10,
				},
				RemainingQuota: new(float64(500)),
			},
		},
	}, nil
}

func newUnifiedModelAzdClient(
	t *testing.T,
	envSrv *testEnvironmentServiceServer,
	aiPromptSrv *unifiedModelTestServer,
) *azdext.AzdClient {
	t.Helper()

	grpcServer := grpc.NewServer()
	azdext.RegisterEnvironmentServiceServer(grpcServer, envSrv)
	azdext.RegisterPromptServiceServer(grpcServer, aiPromptSrv)
	azdext.RegisterAiModelServiceServer(grpcServer, aiPromptSrv)

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	go func() { _ = grpcServer.Serve(listener) }()
	t.Cleanup(func() {
		grpcServer.Stop()
		_ = listener.Close()
	})

	client, err := azdext.NewAzdClient(azdext.WithAddress(listener.Addr().String()))
	require.NoError(t, err)
	t.Cleanup(func() { client.Close() })
	return client
}

func TestConfigureAgentModelDeployment(t *testing.T) {
	const envName = "test-env"
	existingProject := &FoundryProjectInfo{
		SubscriptionId:    "sub",
		ResourceGroupName: "rg",
		AccountName:       "acct",
		ProjectName:       "proj",
		Location:          "eastus2",
	}
	existingDeployments := []FoundryDeploymentInfo{
		{
			Name:        "live-gpt4o",
			ModelName:   "gpt-4o",
			ModelFormat: "OpenAI",
			Version:     "2024-08-06",
			SkuName:     "GlobalStandard",
			SkuCapacity: 50,
		},
	}
	defaultCatalog := []*azdext.AiModel{
		{
			Name:      defaultAgentModel,
			Locations: []string{"eastus2"},
			Versions:  []*azdext.AiModelVersion{{Version: "2026-01-01", IsDefault: true}},
		},
	}

	tests := []struct {
		name                   string
		allowSkip              bool
		deferred               bool
		selectedProject        *FoundryProjectInfo
		projectDeployments     []FoundryDeploymentInfo
		flags                  initFlags
		selectResponses        []int32
		catalogModels          []*azdext.AiModel
		wantNilDeployment      bool
		wantDeploymentName     string
		wantProvision          bool
		wantCapacity           int
		wantErrCode            string
		wantModelSelectSamples int
		wantDepSelectSamples   int
		wantSelectMessages     []string
		wantCatalogDefault     string
		wantDeferredWarning    bool
	}{
		{
			name:                   "prompt new project goes straight to catalog with gpt-5.4-mini default and auto-SKU",
			allowSkip:              false,
			wantDeploymentName:     defaultAgentModel,
			wantProvision:          true,
			wantCapacity:           int(defaultDeploymentCapacity),
			wantModelSelectSamples: 1,
			wantCatalogDefault:     defaultAgentModel,
		},
		{
			name:               "hosted new project offers new or skip and returns nil on skip",
			allowSkip:          true,
			selectResponses:    []int32{1},
			wantNilDeployment:  true,
			wantSelectMessages: []string{"How would you like to configure model(s) for your agent?"},
		},
		{
			name:                   "hosted new project selecting deploy new uses gpt-5.4-mini default and auto-SKU",
			allowSkip:              true,
			selectResponses:        []int32{0},
			wantDeploymentName:     defaultAgentModel,
			wantProvision:          true,
			wantCapacity:           int(defaultDeploymentCapacity),
			wantModelSelectSamples: 1,
			wantSelectMessages:     []string{"How would you like to configure model(s) for your agent?"},
			wantCatalogDefault:     defaultAgentModel,
		},
		{
			name:                 "prompt existing project flattened 1-step selects existing deployment",
			allowSkip:            false,
			selectedProject:      existingProject,
			projectDeployments:   existingDeployments,
			selectResponses:      []int32{0},
			wantDeploymentName:   "live-gpt4o",
			wantProvision:        false,
			wantCapacity:         50,
			wantDepSelectSamples: 1,
			wantSelectMessages:   []string{"Select a model deployment"},
		},
		{
			name:                   "prompt existing project flattened 1-step deploy new proceeds to catalog",
			allowSkip:              false,
			selectedProject:        existingProject,
			projectDeployments:     existingDeployments,
			selectResponses:        []int32{1},
			wantDeploymentName:     defaultAgentModel,
			wantProvision:          true,
			wantCapacity:           int(defaultDeploymentCapacity),
			wantDepSelectSamples:   1,
			wantModelSelectSamples: 1,
			wantSelectMessages:     []string{"Select a model deployment"},
			wantCatalogDefault:     defaultAgentModel,
		},
		{
			name:                 "hosted existing project flattened 1-step skip returns nil",
			allowSkip:            true,
			selectedProject:      existingProject,
			projectDeployments:   existingDeployments,
			selectResponses:      []int32{2},
			wantNilDeployment:    true,
			wantDepSelectSamples: 1,
			wantSelectMessages:   []string{"Select a model deployment"},
		},
		{
			name:                   "prompt existing project with 0 deployments proceeds straight to catalog",
			allowSkip:              false,
			selectedProject:        existingProject,
			projectDeployments:     nil,
			wantDeploymentName:     defaultAgentModel,
			wantProvision:          true,
			wantCapacity:           int(defaultDeploymentCapacity),
			wantDepSelectSamples:   1,
			wantModelSelectSamples: 1,
			wantCatalogDefault:     defaultAgentModel,
		},
		{
			name:                 "hosted existing project with 0 deployments offers new or skip",
			allowSkip:            true,
			selectedProject:      existingProject,
			projectDeployments:   nil,
			selectResponses:      []int32{1},
			wantNilDeployment:    true,
			wantDepSelectSamples: 1,
			wantSelectMessages:   []string{"How would you like to configure model(s) for your agent?"},
		},
		{
			name:                   "interactive explicit --model skips all menus and confirmation prompt",
			allowSkip:              false,
			selectedProject:        existingProject,
			projectDeployments:     existingDeployments,
			flags:                  initFlags{model: defaultAgentModel},
			catalogModels:          defaultCatalog,
			wantDeploymentName:     defaultAgentModel,
			wantProvision:          true,
			wantCapacity:           int(defaultDeploymentCapacity),
			wantModelSelectSamples: 1,
		},
		{
			name:                 "explicit --model-deployment takes precedence over --model without extra menus",
			allowSkip:            true,
			selectedProject:      existingProject,
			projectDeployments:   existingDeployments,
			flags:                initFlags{modelDeployment: "live-gpt4o", model: defaultAgentModel},
			wantDeploymentName:   "live-gpt4o",
			wantProvision:        false,
			wantCapacity:         50,
			wantDepSelectSamples: 1,
		},
		{
			name:                 "explicit --model-deployment on empty project returns CodeModelDeploymentNotFound",
			allowSkip:            false,
			selectedProject:      existingProject,
			projectDeployments:   nil,
			flags:                initFlags{modelDeployment: "missing"},
			wantErrCode:          exterrors.CodeModelDeploymentNotFound,
			wantDepSelectSamples: 1,
		},
		{
			name:        "explicit --model-deployment with new project fails for prompt",
			allowSkip:   false,
			flags:       initFlags{modelDeployment: "live-gpt4o"},
			wantErrCode: exterrors.CodeInvalidParameter,
		},
		{
			name:        "explicit --model-deployment with new project fails for hosted",
			allowSkip:   true,
			flags:       initFlags{modelDeployment: "live-gpt4o"},
			wantErrCode: exterrors.CodeInvalidParameter,
		},
		{
			name:              "no-prompt without flags skips for hosted",
			allowSkip:         true,
			flags:             initFlags{noPrompt: true},
			wantNilDeployment: true,
		},
		{
			name:        "no-prompt without flags returns validation error for prompt",
			allowSkip:   false,
			flags:       initFlags{noPrompt: true},
			wantErrCode: exterrors.CodeInvalidParameter,
		},
		{
			name:                   "no-prompt with --model auto-picks SKU and capacity",
			allowSkip:              false,
			flags:                  initFlags{noPrompt: true, model: defaultAgentModel},
			catalogModels:          defaultCatalog,
			wantDeploymentName:     defaultAgentModel,
			wantProvision:          true,
			wantCapacity:           int(defaultDeploymentCapacity),
			wantModelSelectSamples: 1,
		},
		{
			name:                   "no-prompt with --model missing from catalog fails with CodeModelResolutionFailed",
			allowSkip:              false,
			flags:                  initFlags{noPrompt: true, model: "unknown-model"},
			catalogModels:          defaultCatalog,
			wantErrCode:            exterrors.CodeModelResolutionFailed,
			wantModelSelectSamples: 1,
		},
		{
			name:      "no-prompt with --model unavailable in location fails with CodeModelResolutionFailed",
			allowSkip: false,
			flags:     initFlags{noPrompt: true, model: "gpt-west-only"},
			catalogModels: []*azdext.AiModel{
				{Name: "gpt-west-only", Locations: []string{"westus2"}},
			},
			wantErrCode:            exterrors.CodeModelResolutionFailed,
			wantModelSelectSamples: 1,
		},
		{
			name:                 "no-prompt with --model-deployment resolves existing deployment",
			allowSkip:            false,
			selectedProject:      existingProject,
			projectDeployments:   existingDeployments,
			flags:                initFlags{noPrompt: true, modelDeployment: "live-gpt4o"},
			wantDeploymentName:   "live-gpt4o",
			wantProvision:        false,
			wantCapacity:         50,
			wantDepSelectSamples: 1,
		},
		{
			name:                "deferred Azure context warns and returns nil for hosted",
			allowSkip:           true,
			deferred:            true,
			flags:               initFlags{noPrompt: true, model: defaultAgentModel},
			wantNilDeployment:   true,
			wantDeferredWarning: true,
		},
		{
			name:        "deferred Azure context with --model-deployment rejects new project target",
			allowSkip:   true,
			deferred:    true,
			flags:       initFlags{noPrompt: true, modelDeployment: "live-gpt4o"},
			wantErrCode: exterrors.CodeInvalidParameter,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			envSrv := &testEnvironmentServiceServer{
				values: map[string]map[string]string{envName: {}},
			}
			aiSrv := &unifiedModelTestServer{
				catalogModels:   tt.catalogModels,
				selectResponses: tt.selectResponses,
			}
			client := newUnifiedModelAzdClient(t, envSrv, aiSrv)
			azureCtx := &azdext.AzureContext{
				Scope: &azdext.AzureScope{SubscriptionId: "sub", Location: "eastus2"},
			}
			loader := func(
				context.Context, azcore.TokenCredential, string, string, string,
			) ([]FoundryDeploymentInfo, error) {
				return tt.projectDeployments, nil
			}

			ctx, timing := inittelemetry.NewContext(t.Context())
			flags := tt.flags
			var (
				got       *project.Deployment
				provision bool
			)
			out, err := captureStdout(t, func() error {
				var runErr error
				got, provision, runErr = configureAgentModelDeployment(ctx, agentModelConfigOptions{
					AzdClient:            client,
					AzureContext:         azureCtx,
					Env:                  &azdext.Environment{Name: envName},
					Flags:                &flags,
					SelectedProject:      tt.selectedProject,
					DeferredAzureContext: tt.deferred,
					AllowSkip:            tt.allowSkip,
					loadDeployments:      loader,
				})
				return runErr
			})

			recorder := &initTimingRecorder{}
			timing.Flush(ctx, recorder)
			require.Len(
				t,
				recordedStepDurations(t, recorder, inittelemetry.ModelSelection),
				tt.wantModelSelectSamples,
			)
			require.Len(
				t,
				recordedStepDurations(t, recorder, inittelemetry.ModelDeploymentSelection),
				tt.wantDepSelectSamples,
			)

			// Deployment name is never prompted via free-text Prompt.
			require.Empty(t, aiSrv.textPromptRequests)

			if tt.wantErrCode != "" {
				localErr, ok := errors.AsType[*azdext.LocalError](err)
				require.True(t, ok)
				require.Equal(t, tt.wantErrCode, localErr.Code)
				return
			}

			require.NoError(t, err)
			require.Equal(t, tt.wantProvision, provision)

			gotMessages := make([]string, len(aiSrv.selectRequests))
			for i, req := range aiSrv.selectRequests {
				gotMessages[i] = req.GetOptions().GetMessage()
			}
			if len(tt.wantSelectMessages) == 0 {
				require.Empty(t, gotMessages)
			} else {
				require.Equal(t, tt.wantSelectMessages, gotMessages)
			}

			if tt.wantCatalogDefault != "" {
				require.Len(t, aiSrv.promptAiModelRequests, 1)
				require.Equal(t, tt.wantCatalogDefault, aiSrv.promptAiModelRequests[0].GetDefaultValue())
			}

			if tt.wantDeferredWarning {
				require.Contains(t, out, "Model configuration was deferred")
			}

			if tt.wantNilDeployment {
				require.Nil(t, got)
				return
			}

			require.NotNil(t, got)
			require.Equal(t, tt.wantDeploymentName, got.Name)
			require.Equal(t, tt.wantCapacity, got.Sku.Capacity)
		})
	}
}

func TestConfigureAgentModelDeployment_UnavailableLocationRecoveryUpdatesScopeLocation(t *testing.T) {
	resetRegionsCache(t, []string{"japaneast", "eastus2"})
	const envName = "recovery-env"
	envSrv := &testEnvironmentServiceServer{
		values: map[string]map[string]string{envName: {}},
	}
	aiSrv := &unifiedModelTestServer{
		catalogModels: []*azdext.AiModel{
			{
				Name:      "gpt-5-recovery",
				Locations: []string{"eastus2"},
				Versions:  []*azdext.AiModelVersion{{Version: "2026-01-01", IsDefault: true}},
			},
		},
		// Choose index 2: "Choose a different location for gpt-5-recovery"
		selectResponses:         []int32{2},
		locationWithQuotaResult: "eastus2",
		resolvedCandidates: map[string][]*azdext.AiModelDeployment{
			"gpt-5-recovery@eastus2": {
				{
					ModelName: "gpt-5-recovery",
					Format:    "OpenAI",
					Version:   "2026-01-01",
					Sku: &azdext.AiModelSku{
						Name:         "GlobalStandard",
						MinCapacity:  1,
						MaxCapacity:  1000,
						CapacityStep: 1,
					},
					RemainingQuota: new(float64(300)),
				},
			},
		},
	}
	client := newUnifiedModelAzdClient(t, envSrv, aiSrv)
	azureCtx := &azdext.AzureContext{
		Scope: &azdext.AzureScope{SubscriptionId: "sub", Location: "japaneast"},
	}

	ctx, timing := inittelemetry.NewContext(t.Context())
	var (
		got       *project.Deployment
		provision bool
	)
	_, err := captureStdout(t, func() error {
		var runErr error
		got, provision, runErr = configureAgentModelDeployment(ctx, agentModelConfigOptions{
			AzdClient:    client,
			AzureContext: azureCtx,
			Env:          &azdext.Environment{Name: envName},
			Flags:        &initFlags{model: "gpt-5-recovery"},
			AllowSkip:    false,
		})
		return runErr
	})

	require.NoError(t, err)
	require.True(t, provision)
	require.NotNil(t, got)
	require.Equal(t, "gpt-5-recovery", got.Name)
	require.Equal(t, int(defaultDeploymentCapacity), got.Sku.Capacity)

	// Confirm no "Model '<X>' is selected." prompt was shown and no text prompt ran.
	require.Len(t, aiSrv.selectRequests, 1)
	require.Equal(t, "What would you like to do?", aiSrv.selectRequests[0].GetOptions().GetMessage())
	require.Empty(t, aiSrv.promptDeploymentRequests)
	require.Empty(t, aiSrv.textPromptRequests)

	// Confirm location recovery updated both azureContext.Scope.Location and env.
	require.Equal(t, "eastus2", azureCtx.Scope.Location)
	require.Equal(t, "eastus2", envSrv.values[envName]["AZURE_AI_DEPLOYMENTS_LOCATION"])

	recorder := &initTimingRecorder{}
	timing.Flush(ctx, recorder)
	require.Len(t, recordedStepDurations(t, recorder, inittelemetry.ModelSelection), 1)
}

func TestConfigureAgentModelDeployment_RecoveryCatalogPromptsDefaultToDefaultAgentModel(t *testing.T) {
	const envName = "alt-model-env"

	for _, tc := range []struct {
		name              string
		regionChoiceIndex int32
		wantLocations     []string
	}{
		{
			name:              "current region filter",
			regionChoiceIndex: 0, // "Models available in my current region (eastus2)"
			wantLocations:     []string{"eastus2"},
		},
		{
			name:              "all available models leaves locations unfiltered",
			regionChoiceIndex: 1, // "All available models"
			wantLocations:     nil,
		},
	} {
		t.Run("not-in-catalog alternative model prompt defaults to gpt-5.4-mini ("+tc.name+")", func(t *testing.T) {
			envSrv := &testEnvironmentServiceServer{
				values: map[string]map[string]string{envName: {}},
			}
			aiSrv := &unifiedModelTestServer{
				catalogModels: []*azdext.AiModel{},
				// 0: "Select a different model", tc.regionChoiceIndex: "Which models would you like to explore?"
				selectResponses: []int32{0, tc.regionChoiceIndex},
			}
			client := newUnifiedModelAzdClient(t, envSrv, aiSrv)
			azureCtx := &azdext.AzureContext{
				Scope: &azdext.AzureScope{SubscriptionId: "sub", Location: "eastus2"},
			}

			_, err := captureStdout(t, func() error {
				_, _, runErr := configureAgentModelDeployment(t.Context(), agentModelConfigOptions{
					AzdClient:    client,
					AzureContext: azureCtx,
					Env:          &azdext.Environment{Name: envName},
					Flags:        &initFlags{model: "missing-model"},
					AllowSkip:    false,
				})
				return runErr
			})
			require.NoError(t, err)
			require.Len(t, aiSrv.selectRequests, 2)
			assert.Equal(
				t,
				"Would you like to select a different model or exit?",
				aiSrv.selectRequests[0].GetOptions().GetMessage(),
			)
			assert.Equal(
				t,
				"Which models would you like to explore?",
				aiSrv.selectRequests[1].GetOptions().GetMessage(),
			)
			require.Len(t, aiSrv.promptAiModelRequests, 1)
			assert.Equal(t, defaultAgentModel, aiSrv.promptAiModelRequests[0].GetDefaultValue())
			if len(tc.wantLocations) == 0 {
				assert.Empty(t, aiSrv.promptAiModelRequests[0].GetFilter().GetLocations())
			} else {
				assert.Equal(t, tc.wantLocations, aiSrv.promptAiModelRequests[0].GetFilter().GetLocations())
			}
		})
	}

	t.Run("unavailable-location same-region catalog prompt defaults to gpt-5.4-mini", func(t *testing.T) {
		envSrv := &testEnvironmentServiceServer{
			values: map[string]map[string]string{envName: {}},
		}
		aiSrv := &unifiedModelTestServer{
			catalogModels: []*azdext.AiModel{
				{Name: "gpt-west-only", Locations: []string{"westus2"}},
			},
			// 0: "Choose a different model in eastus2"
			selectResponses: []int32{0},
		}
		client := newUnifiedModelAzdClient(t, envSrv, aiSrv)
		azureCtx := &azdext.AzureContext{
			Scope: &azdext.AzureScope{SubscriptionId: "sub", Location: "eastus2"},
		}

		_, err := captureStdout(t, func() error {
			_, _, runErr := configureAgentModelDeployment(t.Context(), agentModelConfigOptions{
				AzdClient:    client,
				AzureContext: azureCtx,
				Env:          &azdext.Environment{Name: envName},
				Flags:        &initFlags{model: "gpt-west-only"},
				AllowSkip:    false,
			})
			return runErr
		})
		require.NoError(t, err)
		require.Len(t, aiSrv.promptAiModelRequests, 1)
		require.Equal(t, defaultAgentModel, aiSrv.promptAiModelRequests[0].GetDefaultValue())
	})

	t.Run("quota fallback prompts PromptAiDeployment when auto-pick has no valid candidate", func(t *testing.T) {
		envSrv := &testEnvironmentServiceServer{
			values: map[string]map[string]string{envName: {}},
		}
		aiSrv := &unifiedModelTestServer{
			catalogModels: []*azdext.AiModel{
				{Name: defaultAgentModel, Locations: []string{"eastus2"}},
			},
			// Empty candidates force fallback to PromptAiDeployment.
			resolvedCandidates: map[string][]*azdext.AiModelDeployment{},
			promptDeployment: &azdext.AiModelDeployment{
				ModelName: defaultAgentModel,
				Format:    "OpenAI",
				Version:   "2026-01-01",
				Sku:       &azdext.AiModelSku{Name: "Standard"},
				Capacity:  30,
			},
		}
		client := newUnifiedModelAzdClient(t, envSrv, aiSrv)
		azureCtx := &azdext.AzureContext{
			Scope: &azdext.AzureScope{SubscriptionId: "sub", Location: "eastus2"},
		}

		got, provision, err := configureAgentModelDeployment(t.Context(), agentModelConfigOptions{
			AzdClient:    client,
			AzureContext: azureCtx,
			Env:          &azdext.Environment{Name: envName},
			Flags:        &initFlags{model: defaultAgentModel},
			AllowSkip:    false,
		})
		require.NoError(t, err)
		require.True(t, provision)
		require.NotNil(t, got)
		require.Equal(t, defaultAgentModel, got.Name)
		require.Equal(t, "Standard", got.Sku.Name)
		require.Equal(t, 30, got.Sku.Capacity)
		require.Len(t, aiSrv.promptDeploymentRequests, 1)
		require.Empty(t, aiSrv.textPromptRequests)
	})
}

func TestConfigureAgentModelDeployment_ExistingDeploymentDiscoveryFailures(t *testing.T) {
	const envName = "discovery-env"
	existingProject := &FoundryProjectInfo{
		SubscriptionId:    "sub",
		ResourceGroupName: "rg",
		AccountName:       "acct",
		ProjectName:       "proj",
		Location:          "eastus2",
	}

	t.Run("prompt warns on loader error and falls back to catalog selection", func(t *testing.T) {
		envSrv := &testEnvironmentServiceServer{
			values: map[string]map[string]string{envName: {}},
		}
		aiSrv := &unifiedModelTestServer{}
		client := newUnifiedModelAzdClient(t, envSrv, aiSrv)
		azureCtx := &azdext.AzureContext{
			Scope: &azdext.AzureScope{SubscriptionId: "sub", Location: "eastus2"},
		}
		loader := func(
			context.Context, azcore.TokenCredential, string, string, string,
		) ([]FoundryDeploymentInfo, error) {
			return nil, errors.New("deployments/read permission denied")
		}

		var (
			got       *project.Deployment
			provision bool
		)
		out, err := captureStdout(t, func() error {
			var runErr error
			got, provision, runErr = configureAgentModelDeployment(t.Context(), agentModelConfigOptions{
				AzdClient:       client,
				AzureContext:    azureCtx,
				Env:             &azdext.Environment{Name: envName},
				Flags:           &initFlags{},
				SelectedProject: existingProject,
				AllowSkip:       false,
				loadDeployments: loader,
			})
			return runErr
		})

		require.NoError(t, err)
		require.Contains(t, out, "Could not list existing model deployments:")
		require.NotContains(t, out, "No existing deployments found.")
		require.True(t, provision)
		require.NotNil(t, got)
		require.Equal(t, defaultAgentModel, got.Name)
		require.Len(t, aiSrv.promptAiModelRequests, 1)
		require.Equal(t, defaultAgentModel, aiSrv.promptAiModelRequests[0].GetDefaultValue())
	})

	t.Run("hosted warns on loader error and offers new or skip menu", func(t *testing.T) {
		for _, tt := range []struct {
			name          string
			selectIdx     int32
			wantSkip      bool
			wantProvision bool
		}{
			{name: "skip", selectIdx: 1, wantSkip: true},
			{name: "deploy new", selectIdx: 0, wantProvision: true},
		} {
			t.Run(tt.name, func(t *testing.T) {
				envSrv := &testEnvironmentServiceServer{
					values: map[string]map[string]string{envName: {}},
				}
				aiSrv := &unifiedModelTestServer{
					selectResponses: []int32{tt.selectIdx},
				}
				client := newUnifiedModelAzdClient(t, envSrv, aiSrv)
				azureCtx := &azdext.AzureContext{
					Scope: &azdext.AzureScope{SubscriptionId: "sub", Location: "eastus2"},
				}
				loader := func(
					context.Context, azcore.TokenCredential, string, string, string,
				) ([]FoundryDeploymentInfo, error) {
					return nil, errors.New("transient ARM error")
				}

				var (
					got       *project.Deployment
					provision bool
				)
				out, err := captureStdout(t, func() error {
					var runErr error
					got, provision, runErr = configureAgentModelDeployment(t.Context(), agentModelConfigOptions{
						AzdClient:       client,
						AzureContext:    azureCtx,
						Env:             &azdext.Environment{Name: envName},
						Flags:           &initFlags{},
						SelectedProject: existingProject,
						AllowSkip:       true,
						loadDeployments: loader,
					})
					return runErr
				})

				require.NoError(t, err)
				require.Contains(t, out, "Could not list existing model deployments:")
				require.Len(t, aiSrv.selectRequests, 1)
				require.Equal(
					t,
					"How would you like to configure model(s) for your agent?",
					aiSrv.selectRequests[0].GetOptions().GetMessage(),
				)
				if tt.wantSkip {
					require.Nil(t, got)
					require.False(t, provision)
				} else {
					require.NotNil(t, got)
					require.True(t, provision)
					require.Equal(t, defaultAgentModel, got.Name)
				}
			})
		}
	})

	t.Run("cancellation and context errors from loader propagate immediately", func(t *testing.T) {
		envSrv := &testEnvironmentServiceServer{
			values: map[string]map[string]string{envName: {}},
		}
		aiSrv := &unifiedModelTestServer{}
		client := newUnifiedModelAzdClient(t, envSrv, aiSrv)
		azureCtx := &azdext.AzureContext{
			Scope: &azdext.AzureScope{SubscriptionId: "sub", Location: "eastus2"},
		}

		// Subcase 1: gRPC cancellation returned by loader
		_, _, err := configureAgentModelDeployment(t.Context(), agentModelConfigOptions{
			AzdClient:       client,
			AzureContext:    azureCtx,
			Env:             &azdext.Environment{Name: envName},
			Flags:           &initFlags{},
			SelectedProject: existingProject,
			AllowSkip:       true,
			loadDeployments: func(
				context.Context, azcore.TokenCredential, string, string, string,
			) ([]FoundryDeploymentInfo, error) {
				return nil, status.Error(codes.Canceled, "cancelled")
			},
		})
		localErr, ok := errors.AsType[*azdext.LocalError](err)
		require.True(t, ok)
		require.Equal(t, exterrors.CodeCancelled, localErr.Code)
		require.Empty(t, aiSrv.selectRequests)
		require.Empty(t, aiSrv.promptAiModelRequests)

		// Subcase 2: parent context deadline exceeded
		expiredParent, cancelParent := context.WithDeadline(t.Context(), time.Now().Add(-time.Second))
		defer cancelParent()
		_, _, err = configureAgentModelDeployment(expiredParent, agentModelConfigOptions{
			AzdClient:       client,
			AzureContext:    azureCtx,
			Env:             &azdext.Environment{Name: envName},
			Flags:           &initFlags{},
			SelectedProject: existingProject,
			AllowSkip:       false,
			loadDeployments: func(
				ctx context.Context, _ azcore.TokenCredential, _, _, _ string,
			) ([]FoundryDeploymentInfo, error) {
				return nil, ctx.Err()
			},
		})
		require.ErrorIs(t, err, context.DeadlineExceeded)
		require.Empty(t, aiSrv.selectRequests)
		require.Empty(t, aiSrv.promptAiModelRequests)
	})

	t.Run("explicit --model-deployment fails strictly on loader error", func(t *testing.T) {
		envSrv := &testEnvironmentServiceServer{
			values: map[string]map[string]string{envName: {}},
		}
		aiSrv := &unifiedModelTestServer{}
		client := newUnifiedModelAzdClient(t, envSrv, aiSrv)
		azureCtx := &azdext.AzureContext{
			Scope: &azdext.AzureScope{SubscriptionId: "sub", Location: "eastus2"},
		}

		_, _, err := configureAgentModelDeployment(t.Context(), agentModelConfigOptions{
			AzdClient:       client,
			AzureContext:    azureCtx,
			Env:             &azdext.Environment{Name: envName},
			Flags:           &initFlags{modelDeployment: "my-deployment"},
			SelectedProject: existingProject,
			AllowSkip:       true,
			loadDeployments: func(
				context.Context, azcore.TokenCredential, string, string, string,
			) ([]FoundryDeploymentInfo, error) {
				return nil, errors.New("deployments/read permission denied")
			},
		})
		require.ErrorContains(t, err, "failed to list deployments")
		require.ErrorContains(t, err, "deployments/read permission denied")
		require.Empty(t, aiSrv.selectRequests)
		require.Empty(t, aiSrv.promptAiModelRequests)
	})
}

func assertFixedLocationRecoverySelect(t *testing.T, req *azdext.SelectRequest, location string) {
	t.Helper()

	opts := req.GetOptions()
	require.NotNil(t, opts)
	assert.Equal(t, "What would you like to do?", opts.GetMessage())
	require.NotNil(t, opts.SelectedIndex)
	assert.Equal(t, int32(1), opts.GetSelectedIndex())

	choices := opts.GetChoices()
	require.Len(t, choices, 2)
	assert.Equal(t, "Choose a different model in "+location, choices[0].GetLabel())
	assert.Equal(t, "model", choices[0].GetValue())
	assert.Equal(t, "Exit setup", choices[1].GetLabel())
	assert.Equal(t, "exit", choices[1].GetValue())
}

func TestConfigureAgentModelDeployment_SelectedProjectLocksLocation(t *testing.T) {
	const (
		envName         = "fixed-loc-env"
		projectLocation = "swedencentral"
	)
	selectedProject := &FoundryProjectInfo{
		SubscriptionId:    "sub",
		ResourceGroupName: "rg",
		AccountName:       "acct",
		ProjectName:       "proj",
		Location:          projectLocation,
	}
	inRegionReplacement := &azdext.AiModel{
		Name:      defaultAgentModel,
		Locations: []string{projectLocation},
		Versions:  []*azdext.AiModelVersion{{Version: "2026-01-01", IsDefault: true}},
	}
	exhaustedModel := &azdext.AiModel{
		Name:      "gpt-exhausted",
		Locations: []string{projectLocation, "eastus2"},
		Versions:  []*azdext.AiModelVersion{{Version: "2026-01-01", IsDefault: true}},
	}
	emptyLoader := func(
		context.Context, azcore.TokenCredential, string, string, string,
	) ([]FoundryDeploymentInfo, error) {
		return nil, nil
	}

	type fixedLocationRunResult struct {
		deployment *project.Deployment
		provision  bool
		stdout     string
		err        error
	}

	runWithSelectedProject := func(
		t *testing.T,
		flags initFlags,
		aiSrv *unifiedModelTestServer,
	) fixedLocationRunResult {
		t.Helper()

		envSrv := &testEnvironmentServiceServer{
			values: map[string]map[string]string{envName: {}},
		}
		client := newUnifiedModelAzdClient(t, envSrv, aiSrv)
		azureCtx := &azdext.AzureContext{
			Scope: &azdext.AzureScope{SubscriptionId: "sub", Location: projectLocation},
		}

		var res fixedLocationRunResult
		res.stdout, res.err = captureStdout(t, func() error {
			var runErr error
			res.deployment, res.provision, runErr = configureAgentModelDeployment(
				t.Context(),
				agentModelConfigOptions{
					AzdClient:       client,
					AzureContext:    azureCtx,
					Env:             &azdext.Environment{Name: envName},
					Flags:           &flags,
					SelectedProject: selectedProject,
					AllowSkip:       false,
					loadDeployments: emptyLoader,
				},
			)
			return runErr
		})

		assert.Empty(t, aiSrv.locationWithQuotaRequests)
		assert.NotContains(t, res.stdout, "WARNING: If you switch locations:")
		assert.NotContains(t, envSrv.values[envName], "AZURE_AI_DEPLOYMENTS_LOCATION")
		assert.Equal(t, projectLocation, azureCtx.Scope.Location)

		return res
	}

	t.Run("explicit --model unavailable in project location resolves in-region replacement", func(t *testing.T) {
		aiSrv := &unifiedModelTestServer{
			catalogModels: []*azdext.AiModel{
				{Name: "gpt-west-only", Locations: []string{"westus2"}},
				inRegionReplacement,
			},
			promptedModel:   inRegionReplacement,
			selectResponses: []int32{0}, // "Choose a different model in swedencentral"
		}

		res := runWithSelectedProject(t, initFlags{model: "gpt-west-only"}, aiSrv)
		require.NoError(t, res.err)
		require.True(t, res.provision)
		require.NotNil(t, res.deployment)
		assert.Equal(t, defaultAgentModel, res.deployment.Name)
		assert.Equal(t, int(defaultDeploymentCapacity), res.deployment.Sku.Capacity)

		require.Len(t, aiSrv.selectRequests, 1)
		assertFixedLocationRecoverySelect(t, aiSrv.selectRequests[0], projectLocation)

		require.Len(t, aiSrv.promptAiModelRequests, 1)
		assert.Equal(t, []string{projectLocation}, aiSrv.promptAiModelRequests[0].GetFilter().GetLocations())
		assert.Equal(t, defaultAgentModel, aiSrv.promptAiModelRequests[0].GetDefaultValue())
	})

	t.Run("explicit --model unavailable in project location exits cleanly on Exit setup", func(t *testing.T) {
		aiSrv := &unifiedModelTestServer{
			catalogModels: []*azdext.AiModel{
				{Name: "gpt-west-only", Locations: []string{"westus2"}},
			},
			selectResponses: []int32{1}, // "Exit setup" (pre-selected at index 1)
		}

		res := runWithSelectedProject(t, initFlags{model: "gpt-west-only"}, aiSrv)
		require.ErrorContains(t, res.err, "model unavailable in current location and no alternative selected, exiting")
		assert.Nil(t, res.deployment)
		assert.False(t, res.provision)

		require.Len(t, aiSrv.selectRequests, 1)
		assertFixedLocationRecoverySelect(t, aiSrv.selectRequests[0], projectLocation)
		assert.Empty(t, aiSrv.promptAiModelRequests)
	})

	for _, reason := range []string{
		azdext.AiErrorReasonNoValidSkus,
		azdext.AiErrorReasonNoDeploymentMatch,
	} {
		quotaErr := newAiReasonError(reason)

		for _, entry := range []struct {
			name            string
			flags           initFlags
			catalogModels   []*azdext.AiModel
			promptedModels  []*azdext.AiModel
			wantModelPrompt int
		}{
			{
				name:            "explicit --model",
				flags:           initFlags{model: exhaustedModel.Name},
				catalogModels:   []*azdext.AiModel{exhaustedModel, inRegionReplacement},
				promptedModels:  []*azdext.AiModel{inRegionReplacement},
				wantModelPrompt: 1,
			},
			{
				name:            "catalog-picked model",
				flags:           initFlags{},
				promptedModels:  []*azdext.AiModel{exhaustedModel, inRegionReplacement},
				wantModelPrompt: 2,
			},
		} {
			t.Run("quota exhaustion with "+entry.name+" on "+reason, func(t *testing.T) {
				aiSrv := &unifiedModelTestServer{
					catalogModels:              entry.catalogModels,
					promptedModels:             entry.promptedModels,
					resolveErrByModel:          map[string]error{exhaustedModel.Name: quotaErr},
					promptDeploymentErrByModel: map[string]error{exhaustedModel.Name: quotaErr},
					selectResponses:            []int32{0}, // "Choose a different model in swedencentral"
				}

				res := runWithSelectedProject(t, entry.flags, aiSrv)
				require.NoError(t, res.err)
				require.True(t, res.provision)
				require.NotNil(t, res.deployment)
				assert.Equal(t, defaultAgentModel, res.deployment.Name)

				// Confirm quota recovery followed a deployment attempt for the exhausted model in projectLocation.
				require.NotEmpty(t, aiSrv.resolveDeploymentRequests)
				assert.Equal(t, exhaustedModel.Name, aiSrv.resolveDeploymentRequests[0].GetModelName())
				assert.Equal(
					t,
					[]string{projectLocation},
					aiSrv.resolveDeploymentRequests[0].GetOptions().GetLocations(),
				)
				require.NotEmpty(t, aiSrv.promptDeploymentRequests)
				assert.Equal(t, exhaustedModel.Name, aiSrv.promptDeploymentRequests[0].GetModelName())
				assert.Equal(
					t,
					[]string{projectLocation},
					aiSrv.promptDeploymentRequests[0].GetOptions().GetLocations(),
				)

				require.Len(t, aiSrv.selectRequests, 1)
				assertFixedLocationRecoverySelect(t, aiSrv.selectRequests[0], projectLocation)

				require.Len(t, aiSrv.promptAiModelRequests, entry.wantModelPrompt)
				for _, modelReq := range aiSrv.promptAiModelRequests {
					assert.Equal(t, []string{projectLocation}, modelReq.GetFilter().GetLocations())
				}
			})
		}
	}

	t.Run("explicit --model not in catalog skips region prompt and filters to project location", func(t *testing.T) {
		aiSrv := &unifiedModelTestServer{
			catalogModels:   []*azdext.AiModel{inRegionReplacement},
			promptedModel:   inRegionReplacement,
			selectResponses: []int32{0}, // "Select a different model" (no second region-scope prompt)
		}

		res := runWithSelectedProject(t, initFlags{model: "not-in-catalog"}, aiSrv)
		require.NoError(t, res.err)
		require.True(t, res.provision)
		require.NotNil(t, res.deployment)
		assert.Equal(t, defaultAgentModel, res.deployment.Name)

		require.Len(t, aiSrv.selectRequests, 1)
		assert.Equal(
			t,
			"Would you like to select a different model or exit?",
			aiSrv.selectRequests[0].GetOptions().GetMessage(),
		)

		require.Len(t, aiSrv.promptAiModelRequests, 1)
		assert.Equal(t, []string{projectLocation}, aiSrv.promptAiModelRequests[0].GetFilter().GetLocations())
		assert.Equal(t, defaultAgentModel, aiSrv.promptAiModelRequests[0].GetDefaultValue())
	})
}

func TestIsBatchModelSku(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		sku  *azdext.AiModelSku
		want bool
	}{
		{
			name: "nil SKU is not batch",
			sku:  nil,
			want: false,
		},
		{
			name: "GlobalStandard is not batch",
			sku: &azdext.AiModelSku{
				Name:      "GlobalStandard",
				UsageName: "OpenAI.GlobalStandard.gpt-5.4-mini",
			},
			want: false,
		},
		{
			name: "DataZoneStandard is not batch",
			sku: &azdext.AiModelSku{
				Name:      "DataZoneStandard",
				UsageName: "OpenAI.DataZoneStandard.gpt-5.4-mini",
			},
			want: false,
		},
		{
			name: "Standard is not batch",
			sku: &azdext.AiModelSku{
				Name:      "Standard",
				UsageName: "OpenAI.Standard.gpt-4o",
			},
			want: false,
		},
		{
			name: "real-time SKU whose model name contains batch is not batch",
			sku: &azdext.AiModelSku{
				Name:      "GlobalStandard",
				UsageName: "OpenAI.GlobalStandard.example-batch-aware",
			},
			want: false,
		},
		{
			name: "bare Batch SKU name is batch",
			sku: &azdext.AiModelSku{
				Name: "Batch",
			},
			want: true,
		},
		{
			name: "GlobalBatch is batch",
			sku: &azdext.AiModelSku{
				Name:      "GlobalBatch",
				UsageName: "OpenAI.GlobalBatch.gpt-5.4-mini",
			},
			want: true,
		},
		{
			name: "DataZoneBatch is batch",
			sku: &azdext.AiModelSku{
				Name:      "DataZoneBatch",
				UsageName: "OpenAI.DataZoneBatch.gpt-4.1-mini",
			},
			want: true,
		},
		{
			name: "Batch case-insensitive with whitespace is batch",
			sku: &azdext.AiModelSku{
				Name: "  globalbatch  ",
			},
			want: true,
		},
		{
			name: "usage name containing batch is batch",
			sku: &azdext.AiModelSku{
				Name:      "CustomTier",
				UsageName: "OpenAI.GlobalBatch.gpt-4o",
			},
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, isBatchModelSku(tt.sku))
		})
	}
}

func TestSelectBestModelDeploymentCandidate_IgnoresBatchSkus(t *testing.T) {
	t.Parallel()

	model := &azdext.AiModel{
		Name:      defaultAgentModel,
		Locations: []string{"eastus2"},
		Versions: []*azdext.AiModelVersion{
			{Version: "2026-01-01", IsDefault: true},
		},
	}
	batchDeployments := []*azdext.AiModelDeployment{
		nil,
		{
			ModelName: defaultAgentModel,
			Format:    "OpenAI",
			Version:   "2026-01-01",
			Sku: &azdext.AiModelSku{
				Name:            "GlobalBatch",
				UsageName:       "OpenAI.GlobalBatch.gpt-5.4-mini",
				MinCapacity:     1,
				MaxCapacity:     200000,
				CapacityStep:    1,
				DefaultCapacity: 10,
			},
			RemainingQuota: new(float64(200000)),
		},
		{
			ModelName: defaultAgentModel,
			Format:    "OpenAI",
			Version:   "2026-01-01",
			Sku: &azdext.AiModelSku{
				Name:            "DataZoneBatch",
				UsageName:       "OpenAI.DataZoneBatch.gpt-5.4-mini",
				MinCapacity:     1,
				MaxCapacity:     200000,
				CapacityStep:    1,
				DefaultCapacity: 10,
			},
			RemainingQuota: new(float64(200000)),
		},
	}

	t.Run("returns nil when only GlobalBatch and DataZoneBatch candidates exist", func(t *testing.T) {
		t.Parallel()
		got := selectBestModelDeploymentCandidate(model, batchDeployments)
		assert.Nil(t, got)
	})

	t.Run("returns nil when only batch usage name candidate exists", func(t *testing.T) {
		t.Parallel()
		got := selectBestModelDeploymentCandidate(model, []*azdext.AiModelDeployment{
			{
				ModelName: defaultAgentModel,
				Format:    "OpenAI",
				Version:   "2026-01-01",
				Sku: &azdext.AiModelSku{
					Name:         "CustomSku",
					UsageName:    "OpenAI.GlobalBatch.gpt-5.4-mini",
					MinCapacity:  1,
					MaxCapacity:  1000,
					CapacityStep: 1,
				},
				RemainingQuota: new(float64(200000)),
			},
		})
		assert.Nil(t, got)
	})

	t.Run("selects real-time Standard SKU over higher-quota batch SKUs", func(t *testing.T) {
		t.Parallel()
		candidates := append(append([]*azdext.AiModelDeployment{}, batchDeployments...), &azdext.AiModelDeployment{
			ModelName: defaultAgentModel,
			Format:    "OpenAI",
			Version:   "2026-01-01",
			Sku: &azdext.AiModelSku{
				Name:            "Standard",
				UsageName:       "OpenAI.Standard.gpt-5.4-mini",
				MinCapacity:     1,
				MaxCapacity:     1000,
				CapacityStep:    1,
				DefaultCapacity: 10,
			},
			RemainingQuota: new(float64(80)),
		})
		got := selectBestModelDeploymentCandidate(model, candidates)
		require.NotNil(t, got)
		assert.Equal(t, "Standard", got.GetSku().GetName())
		assert.Equal(t, int32(80), got.GetCapacity())
	})
}

func TestConfigureAgentModelDeployment_BatchOnlyQuota(t *testing.T) {
	resetRegionsCache(t, []string{"eastus2", "swedencentral"})
	const envName = "batch-quota-env"

	batchOnlyForModel := func(modelName string) []*azdext.AiModelDeployment {
		return []*azdext.AiModelDeployment{
			{
				ModelName: modelName,
				Format:    "OpenAI",
				Version:   "2026-01-01",
				Sku: &azdext.AiModelSku{
					Name:            "GlobalBatch",
					UsageName:       "OpenAI.GlobalBatch." + modelName,
					MinCapacity:     1,
					MaxCapacity:     200000,
					CapacityStep:    1,
					DefaultCapacity: 10,
				},
				RemainingQuota: new(float64(200000)),
			},
			{
				ModelName: modelName,
				Format:    "OpenAI",
				Version:   "2026-01-01",
				Sku: &azdext.AiModelSku{
					Name:            "DataZoneBatch",
					UsageName:       "OpenAI.DataZoneBatch." + modelName,
					MinCapacity:     1,
					MaxCapacity:     200000,
					CapacityStep:    1,
					DefaultCapacity: 10,
				},
				RemainingQuota: new(float64(200000)),
			},
		}
	}

	realTimeForModel := func(modelName string, quota float64) []*azdext.AiModelDeployment {
		return []*azdext.AiModelDeployment{
			{
				ModelName: modelName,
				Format:    "OpenAI",
				Version:   "2026-01-01",
				Sku: &azdext.AiModelSku{
					Name:            "GlobalStandard",
					UsageName:       "OpenAI.GlobalStandard." + modelName,
					MinCapacity:     1,
					MaxCapacity:     1000,
					CapacityStep:    1,
					DefaultCapacity: 10,
				},
				RemainingQuota: new(quota),
			},
		}
	}

	primaryModel := &azdext.AiModel{
		Name:      defaultAgentModel,
		Locations: []string{"eastus2", "swedencentral"},
		Versions:  []*azdext.AiModelVersion{{Version: "2026-01-01", IsDefault: true}},
	}
	replacementModel := &azdext.AiModel{
		Name:      "gpt-4.1-mini",
		Locations: []string{"eastus2", "swedencentral"},
		Versions:  []*azdext.AiModelVersion{{Version: "2026-01-01", IsDefault: true}},
	}
	existingProject := &FoundryProjectInfo{
		SubscriptionId:    "sub",
		ResourceGroupName: "rg",
		AccountName:       "acct",
		ProjectName:       "proj",
		Location:          "swedencentral",
	}
	emptyLoader := func(
		context.Context, azcore.TokenCredential, string, string, string,
	) ([]FoundryDeploymentInfo, error) {
		return nil, nil
	}

	interactiveCases := []struct {
		name               string
		initialLocation    string
		selectedProject    *FoundryProjectInfo
		flags              initFlags
		promptedModels     []*azdext.AiModel
		selectResponses    []int32
		locationWithQuota  string
		resolvedCandidates map[string][]*azdext.AiModelDeployment
		wantModelName      string
		wantCapacity       int
		wantLocation       string
		wantFixedLocation  bool
	}{
		{
			name:              "new project with explicit --model skips PromptAiDeployment and switches location",
			initialLocation:   "eastus2",
			flags:             initFlags{model: defaultAgentModel},
			selectResponses:   []int32{2}, // "Choose a different location for gpt-5.4-mini"
			locationWithQuota: "swedencentral",
			resolvedCandidates: map[string][]*azdext.AiModelDeployment{
				defaultAgentModel + "@eastus2":       batchOnlyForModel(defaultAgentModel),
				defaultAgentModel + "@swedencentral": realTimeForModel(defaultAgentModel, 300),
			},
			wantModelName: defaultAgentModel,
			wantCapacity:  int(defaultDeploymentCapacity),
			wantLocation:  "swedencentral",
		},
		{
			name:            "new project with catalog-picked model skips PromptAiDeployment and recovers real-time model",
			initialLocation: "eastus2",
			flags:           initFlags{},
			promptedModels:  []*azdext.AiModel{primaryModel, replacementModel},
			selectResponses: []int32{0}, // "Choose a different model in eastus2"
			resolvedCandidates: map[string][]*azdext.AiModelDeployment{
				defaultAgentModel + "@eastus2": batchOnlyForModel(defaultAgentModel),
				"gpt-4.1-mini@eastus2":         realTimeForModel("gpt-4.1-mini", 180),
			},
			wantModelName: "gpt-4.1-mini",
			wantCapacity:  180,
			wantLocation:  "eastus2",
		},
		{
			name:            "existing project with explicit --model skips PromptAiDeployment and recovers in-region model",
			initialLocation: "swedencentral",
			selectedProject: existingProject,
			flags:           initFlags{model: defaultAgentModel},
			promptedModels:  []*azdext.AiModel{replacementModel},
			selectResponses: []int32{0}, // "Choose a different model in swedencentral"
			resolvedCandidates: map[string][]*azdext.AiModelDeployment{
				defaultAgentModel + "@swedencentral": batchOnlyForModel(defaultAgentModel),
				"gpt-4.1-mini@swedencentral":         realTimeForModel("gpt-4.1-mini", 200),
			},
			wantModelName:     "gpt-4.1-mini",
			wantCapacity:      200,
			wantLocation:      "swedencentral",
			wantFixedLocation: true,
		},
		{
			name:            "existing project with catalog-picked model skips PromptAiDeployment and recovers in-region model",
			initialLocation: "swedencentral",
			selectedProject: existingProject,
			flags:           initFlags{},
			promptedModels:  []*azdext.AiModel{primaryModel, replacementModel},
			selectResponses: []int32{0}, // "Choose a different model in swedencentral"
			resolvedCandidates: map[string][]*azdext.AiModelDeployment{
				defaultAgentModel + "@swedencentral": batchOnlyForModel(defaultAgentModel),
				"gpt-4.1-mini@swedencentral":         realTimeForModel("gpt-4.1-mini", 250),
			},
			wantModelName:     "gpt-4.1-mini",
			wantCapacity:      250,
			wantLocation:      "swedencentral",
			wantFixedLocation: true,
		},
	}

	for _, tc := range interactiveCases {
		t.Run(tc.name, func(t *testing.T) {
			envSrv := &testEnvironmentServiceServer{
				values: map[string]map[string]string{envName: {}},
			}
			aiSrv := &unifiedModelTestServer{
				catalogModels:           []*azdext.AiModel{primaryModel, replacementModel},
				promptedModels:          tc.promptedModels,
				selectResponses:         tc.selectResponses,
				locationWithQuotaResult: tc.locationWithQuota,
				resolvedCandidates:      tc.resolvedCandidates,
			}
			client := newUnifiedModelAzdClient(t, envSrv, aiSrv)
			azureCtx := &azdext.AzureContext{
				Scope: &azdext.AzureScope{SubscriptionId: "sub", Location: tc.initialLocation},
			}
			flags := tc.flags

			var (
				got       *project.Deployment
				provision bool
			)
			out, err := captureStdout(t, func() error {
				var runErr error
				got, provision, runErr = configureAgentModelDeployment(t.Context(), agentModelConfigOptions{
					AzdClient:       client,
					AzureContext:    azureCtx,
					Env:             &azdext.Environment{Name: envName},
					Flags:           &flags,
					SelectedProject: tc.selectedProject,
					AllowSkip:       false,
					loadDeployments: emptyLoader,
				})
				return runErr
			})

			require.NoError(t, err)
			require.True(t, provision)
			require.NotNil(t, got)
			assert.Equal(t, tc.wantModelName, got.Name)
			assert.Equal(t, "GlobalStandard", got.Sku.Name)
			assert.Equal(t, tc.wantCapacity, got.Sku.Capacity)

			// PromptAiDeployment must not be called when only batch SKUs have quota.
			assert.Empty(t, aiSrv.promptDeploymentRequests)
			assert.Contains(
				t,
				out,
				"Not enough available quota to deploy model '"+defaultAgentModel+"' in '"+tc.initialLocation+"'.",
			)
			assert.Equal(t, tc.wantLocation, azureCtx.Scope.Location)

			require.Len(t, aiSrv.selectRequests, 1)
			if tc.wantFixedLocation {
				assertFixedLocationRecoverySelect(t, aiSrv.selectRequests[0], tc.initialLocation)
			} else {
				assert.Equal(t, "What would you like to do?", aiSrv.selectRequests[0].GetOptions().GetMessage())
			}
		})
	}

	for _, tc := range []struct {
		name            string
		selectedProject *FoundryProjectInfo
		location        string
	}{
		{name: "no-prompt new project fails with CodeModelResolutionFailed", location: "eastus2"},
		{
			name:            "no-prompt existing project fails with CodeModelResolutionFailed",
			selectedProject: existingProject,
			location:        "swedencentral",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			envSrv := &testEnvironmentServiceServer{
				values: map[string]map[string]string{envName: {}},
			}
			aiSrv := &unifiedModelTestServer{
				catalogModels: []*azdext.AiModel{primaryModel},
				resolvedCandidates: map[string][]*azdext.AiModelDeployment{
					defaultAgentModel: batchOnlyForModel(defaultAgentModel),
				},
			}
			client := newUnifiedModelAzdClient(t, envSrv, aiSrv)
			azureCtx := &azdext.AzureContext{
				Scope: &azdext.AzureScope{SubscriptionId: "sub", Location: tc.location},
			}

			var (
				got       *project.Deployment
				provision bool
			)
			_, err := captureStdout(t, func() error {
				var runErr error
				got, provision, runErr = configureAgentModelDeployment(t.Context(), agentModelConfigOptions{
					AzdClient:       client,
					AzureContext:    azureCtx,
					Env:             &azdext.Environment{Name: envName},
					Flags:           &initFlags{noPrompt: true, model: defaultAgentModel},
					SelectedProject: tc.selectedProject,
					AllowSkip:       false,
					loadDeployments: emptyLoader,
				})
				return runErr
			})

			localErr, ok := errors.AsType[*azdext.LocalError](err)
			require.True(t, ok)
			assert.Equal(t, exterrors.CodeModelResolutionFailed, localErr.Code)
			assert.Nil(t, got)
			assert.False(t, provision)
			assert.Empty(t, aiSrv.promptDeploymentRequests)
		})
	}

	for _, tc := range []struct {
		name                string
		fallbackDeployments []*azdext.AiModelDeployment
	}{
		{
			name: "fallback mixed real-time and batch scopes every PromptAiDeployment attempt to non-batch SKUs",
			fallbackDeployments: append(
				batchOnlyForModel(defaultAgentModel),
				realTimeForModel(defaultAgentModel, 300)...,
			),
		},
		{
			name:                "fallback real-time-only result scopes every PromptAiDeployment attempt to non-batch SKUs",
			fallbackDeployments: realTimeForModel(defaultAgentModel, 300),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			envSrv := &testEnvironmentServiceServer{
				values: map[string]map[string]string{envName: {}},
			}
			aiSrv := &unifiedModelTestServer{
				catalogModels: []*azdext.AiModel{primaryModel},
				resolveSequence: []resolveDeploymentStep{
					{err: newAiReasonError(azdext.AiErrorReasonNoDeploymentMatch)},
					{deployments: tc.fallbackDeployments},
				},
				promptDeploymentSequence: []promptDeploymentStep{
					{err: newAiReasonError(azdext.AiErrorReasonNoValidSkus)},
					{
						deployment: &azdext.AiModelDeployment{
							ModelName: defaultAgentModel,
							Format:    "OpenAI",
							Version:   "2026-01-01",
							Sku:       &azdext.AiModelSku{Name: "GlobalStandard"},
							Capacity:  100,
						},
					},
				},
			}
			client := newUnifiedModelAzdClient(t, envSrv, aiSrv)
			azureCtx := &azdext.AzureContext{
				Scope: &azdext.AzureScope{SubscriptionId: "sub", Location: "eastus2"},
			}

			got, provision, err := configureAgentModelDeployment(t.Context(), agentModelConfigOptions{
				AzdClient:    client,
				AzureContext: azureCtx,
				Env:          &azdext.Environment{Name: envName},
				Flags:        &initFlags{model: defaultAgentModel},
				AllowSkip:    false,
			})
			require.NoError(t, err)
			require.True(t, provision)
			require.NotNil(t, got)
			assert.Equal(t, "GlobalStandard", got.Sku.Name)
			assert.Equal(t, 100, got.Sku.Capacity)

			require.Len(t, aiSrv.resolveDeploymentRequests, 2)
			require.Len(t, aiSrv.promptDeploymentRequests, 2)
			require.NotNil(t, aiSrv.promptDeploymentRequests[0].GetOptions().Capacity)
			assert.Equal(t, defaultDeploymentCapacity, aiSrv.promptDeploymentRequests[0].GetOptions().GetCapacity())
			assert.Equal(t, []string{"GlobalStandard"}, aiSrv.promptDeploymentRequests[0].GetOptions().GetSkus())
			assert.Nil(t, aiSrv.promptDeploymentRequests[1].GetOptions().Capacity)
			assert.Equal(t, []string{"GlobalStandard"}, aiSrv.promptDeploymentRequests[1].GetOptions().GetSkus())
		})
	}

	t.Run("fallback batch-only result skips PromptAiDeployment and enters quota recovery", func(t *testing.T) {
		envSrv := &testEnvironmentServiceServer{
			values: map[string]map[string]string{envName: {}},
		}
		aiSrv := &unifiedModelTestServer{
			catalogModels:   []*azdext.AiModel{primaryModel, replacementModel},
			promptedModels:  []*azdext.AiModel{replacementModel},
			selectResponses: []int32{0}, // "Choose a different model in eastus2"
			resolveSequence: []resolveDeploymentStep{
				{err: newAiReasonError(azdext.AiErrorReasonNoDeploymentMatch)},
				{deployments: batchOnlyForModel(defaultAgentModel)},
			},
			resolvedCandidates: map[string][]*azdext.AiModelDeployment{
				"gpt-4.1-mini@eastus2": realTimeForModel("gpt-4.1-mini", 150),
			},
		}
		client := newUnifiedModelAzdClient(t, envSrv, aiSrv)
		azureCtx := &azdext.AzureContext{
			Scope: &azdext.AzureScope{SubscriptionId: "sub", Location: "eastus2"},
		}

		var (
			got       *project.Deployment
			provision bool
		)
		out, err := captureStdout(t, func() error {
			var runErr error
			got, provision, runErr = configureAgentModelDeployment(t.Context(), agentModelConfigOptions{
				AzdClient:    client,
				AzureContext: azureCtx,
				Env:          &azdext.Environment{Name: envName},
				Flags:        &initFlags{model: defaultAgentModel},
				AllowSkip:    false,
			})
			return runErr
		})

		require.NoError(t, err)
		require.True(t, provision)
		require.NotNil(t, got)
		assert.Equal(t, "gpt-4.1-mini", got.Name)
		assert.Equal(t, "GlobalStandard", got.Sku.Name)
		assert.Equal(t, 150, got.Sku.Capacity)
		require.Len(t, aiSrv.resolveDeploymentRequests, 3)
		assert.Empty(t, aiSrv.promptDeploymentRequests)
		assert.Contains(t, out, "Not enough available quota to deploy model '"+defaultAgentModel+"' in 'eastus2'.")
	})

	t.Run("interactive fallback rejects batch SKU from PromptAiDeployment and enters quota recovery", func(t *testing.T) {
		envSrv := &testEnvironmentServiceServer{
			values: map[string]map[string]string{envName: {}},
		}
		aiSrv := &unifiedModelTestServer{
			catalogModels:   []*azdext.AiModel{primaryModel, replacementModel},
			promptedModels:  []*azdext.AiModel{replacementModel},
			selectResponses: []int32{0}, // "Choose a different model in eastus2"
			resolvedCandidates: map[string][]*azdext.AiModelDeployment{
				// Empty raw candidates for primaryModel triggers PromptAiDeployment fallback.
				"gpt-4.1-mini@eastus2": realTimeForModel("gpt-4.1-mini", 120),
			},
			promptDeployment: &azdext.AiModelDeployment{
				ModelName: defaultAgentModel,
				Format:    "OpenAI",
				Version:   "2026-01-01",
				Sku: &azdext.AiModelSku{
					Name:      "GlobalBatch",
					UsageName: "OpenAI.GlobalBatch." + defaultAgentModel,
				},
				Capacity: 250,
			},
		}
		client := newUnifiedModelAzdClient(t, envSrv, aiSrv)
		azureCtx := &azdext.AzureContext{
			Scope: &azdext.AzureScope{SubscriptionId: "sub", Location: "eastus2"},
		}

		var (
			got       *project.Deployment
			provision bool
		)
		out, err := captureStdout(t, func() error {
			var runErr error
			got, provision, runErr = configureAgentModelDeployment(t.Context(), agentModelConfigOptions{
				AzdClient:    client,
				AzureContext: azureCtx,
				Env:          &azdext.Environment{Name: envName},
				Flags:        &initFlags{model: defaultAgentModel},
				AllowSkip:    false,
			})
			return runErr
		})

		require.NoError(t, err)
		require.True(t, provision)
		require.NotNil(t, got)
		assert.Equal(t, "gpt-4.1-mini", got.Name)
		assert.Equal(t, "GlobalStandard", got.Sku.Name)
		assert.Equal(t, 120, got.Sku.Capacity)
		require.Len(t, aiSrv.promptDeploymentRequests, 1)
		assert.Contains(t, out, "Not enough available quota to deploy model '"+defaultAgentModel+"' in 'eastus2'.")
	})
}
