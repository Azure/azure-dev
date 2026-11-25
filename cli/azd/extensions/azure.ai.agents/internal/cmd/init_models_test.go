// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"azureaiagent/internal/project"
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"net"
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
			assert.Empty(t, srv.promptRequests[0].GetOptions().GetSkus())
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
