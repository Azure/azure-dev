// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package grpcserver

import (
	"errors"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/ai"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestNewAiModelService(t *testing.T) {
	t.Parallel()
	svc := NewAiModelService(nil)
	require.NotNil(t, svc)
}

// --- ListModels validation ---

func TestAiModelService_ListModels_NilAzureContext(t *testing.T) {
	t.Parallel()
	svc := NewAiModelService(ai.NewAiModelService(nil, nil))
	_, err := svc.ListModels(t.Context(), &azdext.ListModelsRequest{
		AzureContext: nil,
	})
	require.Error(t, err)
}

func TestAiModelService_ListModels_EmptySubscriptionID(t *testing.T) {
	t.Parallel()
	svc := NewAiModelService(ai.NewAiModelService(nil, nil))
	_, err := svc.ListModels(t.Context(), &azdext.ListModelsRequest{
		AzureContext: &azdext.AzureContext{
			Scope: &azdext.AzureScope{SubscriptionId: ""},
		},
	})
	require.Error(t, err)
}

// --- ResolveModelDeployments validation ---

func TestAiModelService_ResolveModelDeployments_NilAzureContext(t *testing.T) {
	t.Parallel()
	svc := NewAiModelService(ai.NewAiModelService(nil, nil))
	_, err := svc.ResolveModelDeployments(t.Context(), &azdext.ResolveModelDeploymentsRequest{
		AzureContext: nil,
	})
	require.Error(t, err)
}

func TestAiModelService_ResolveModelDeployments_EmptySubscriptionID(t *testing.T) {
	t.Parallel()
	svc := NewAiModelService(ai.NewAiModelService(nil, nil))
	_, err := svc.ResolveModelDeployments(t.Context(), &azdext.ResolveModelDeploymentsRequest{
		AzureContext: &azdext.AzureContext{
			Scope: &azdext.AzureScope{SubscriptionId: ""},
		},
	})
	require.Error(t, err)
}

// --- ListUsages validation ---

func TestAiModelService_ListUsages_NilAzureContext(t *testing.T) {
	t.Parallel()
	svc := NewAiModelService(ai.NewAiModelService(nil, nil))
	_, err := svc.ListUsages(t.Context(), &azdext.ListUsagesRequest{
		AzureContext: nil,
	})
	require.Error(t, err)
}

func TestAiModelService_ListUsages_EmptyLocation(t *testing.T) {
	t.Parallel()
	svc := NewAiModelService(ai.NewAiModelService(nil, nil))
	_, err := svc.ListUsages(t.Context(), &azdext.ListUsagesRequest{
		AzureContext: &azdext.AzureContext{
			Scope: &azdext.AzureScope{SubscriptionId: "sub-123"},
		},
		Location: "",
	})
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	require.Equal(t, codes.InvalidArgument, st.Code())
}

// --- ListLocationsWithQuota validation ---

func TestAiModelService_ListLocationsWithQuota_NilAzureContext(t *testing.T) {
	t.Parallel()
	svc := NewAiModelService(ai.NewAiModelService(nil, nil))
	_, err := svc.ListLocationsWithQuota(t.Context(), &azdext.ListLocationsWithQuotaRequest{
		AzureContext: nil,
	})
	require.Error(t, err)
}

func TestAiModelService_ListLocationsWithQuota_EmptySubscriptionID(t *testing.T) {
	t.Parallel()
	svc := NewAiModelService(ai.NewAiModelService(nil, nil))
	_, err := svc.ListLocationsWithQuota(t.Context(), &azdext.ListLocationsWithQuotaRequest{
		AzureContext: &azdext.AzureContext{
			Scope: &azdext.AzureScope{SubscriptionId: ""},
		},
	})
	require.Error(t, err)
}

// --- ListModelLocationsWithQuota validation ---

func TestAiModelService_ListModelLocationsWithQuota_NilAzureContext(t *testing.T) {
	t.Parallel()
	svc := NewAiModelService(ai.NewAiModelService(nil, nil))
	_, err := svc.ListModelLocationsWithQuota(t.Context(), &azdext.ListModelLocationsWithQuotaRequest{
		AzureContext: nil,
	})
	require.Error(t, err)
}

func TestAiModelService_ListModelLocationsWithQuota_EmptyModelName(t *testing.T) {
	t.Parallel()
	svc := NewAiModelService(ai.NewAiModelService(nil, nil))
	_, err := svc.ListModelLocationsWithQuota(t.Context(), &azdext.ListModelLocationsWithQuotaRequest{
		AzureContext: &azdext.AzureContext{
			Scope: &azdext.AzureScope{SubscriptionId: "sub-123"},
		},
		ModelName: "",
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "model_name is required")
}

// --- mapAiResolveError tests ---

func TestMapAiResolveError_QuotaLocationRequired(t *testing.T) {
	t.Parallel()
	err := mapAiResolveError(ai.ErrQuotaLocationRequired, "gpt-4")
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	require.Equal(t, codes.InvalidArgument, st.Code())
}

func TestMapAiResolveError_ModelNotFound(t *testing.T) {
	t.Parallel()
	err := mapAiResolveError(ai.ErrModelNotFound, "gpt-999")
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	require.Equal(t, codes.NotFound, st.Code())
}

func TestMapAiResolveError_NoDeploymentMatch(t *testing.T) {
	t.Parallel()
	err := mapAiResolveError(ai.ErrNoDeploymentMatch, "gpt-4")
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	require.Equal(t, codes.FailedPrecondition, st.Code())
}

func TestMapAiResolveError_DefaultError(t *testing.T) {
	t.Parallel()
	err := mapAiResolveError(errors.New("something else"), "gpt-4")
	require.Error(t, err)
	require.Contains(t, err.Error(), "resolving model deployments")
}

func TestAiStatusError_WithDetails(t *testing.T) {
	t.Parallel()
	err := aiStatusError(codes.NotFound, "test_reason", "test message", map[string]string{"key": "val"})
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	require.Equal(t, codes.NotFound, st.Code())
}

func TestAiStatusError_NilMetadata(t *testing.T) {
	t.Parallel()
	err := aiStatusError(codes.Internal, "test", "msg", nil)
	require.Error(t, err)
}
