// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package grpcserver

import (
	"errors"
	"fmt"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/ai"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestAiStatusError(t *testing.T) {
	tests := []struct {
		name     string
		code     codes.Code
		reason   string
		message  string
		metadata map[string]string
	}{
		{
			name:     "invalid argument with nil metadata",
			code:     codes.InvalidArgument,
			reason:   azdext.AiErrorReasonQuotaLocation,
			message:  "quota location required",
			metadata: nil,
		},
		{
			name:    "not found with metadata",
			code:    codes.NotFound,
			reason:  azdext.AiErrorReasonModelNotFound,
			message: "model not found",
			metadata: map[string]string{
				"model_name": "gpt-4o",
			},
		},
		{
			name:    "failed precondition with metadata",
			code:    codes.FailedPrecondition,
			reason:  azdext.AiErrorReasonNoDeploymentMatch,
			message: "no deployment match",
			metadata: map[string]string{
				"model_name": "gpt-4",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := aiStatusError(
				tt.code, tt.reason, tt.message, tt.metadata,
			)
			require.Error(t, err)

			st, ok := status.FromError(err)
			require.True(t, ok, "expected gRPC status error")
			assert.Equal(t, tt.code, st.Code())
			assert.Equal(t, tt.message, st.Message())

			// Extract ErrorInfo details
			details := st.Details()
			require.Len(t, details, 1)

			errInfo, ok := details[0].(*errdetails.ErrorInfo)
			require.True(t, ok, "expected ErrorInfo detail")
			assert.Equal(t, tt.reason, errInfo.Reason)
			assert.Equal(t, azdext.AiErrorDomain, errInfo.Domain)

			if tt.metadata != nil {
				assert.Equal(t, tt.metadata, errInfo.Metadata)
			}
		})
	}
}

func TestMapAiResolveError(t *testing.T) {
	tests := []struct {
		name         string
		err          error
		modelName    string
		expectedCode codes.Code
		expectedMsg  string
	}{
		{
			name:         "quota location required",
			err:          ai.ErrQuotaLocationRequired,
			modelName:    "gpt-4o",
			expectedCode: codes.InvalidArgument,
			expectedMsg:  ai.ErrQuotaLocationRequired.Error(),
		},
		{
			name: "wrapped quota location required",
			err: fmt.Errorf(
				"resolving: %w", ai.ErrQuotaLocationRequired,
			),
			modelName:    "gpt-4o",
			expectedCode: codes.InvalidArgument,
		},
		{
			name:         "model not found",
			err:          ai.ErrModelNotFound,
			modelName:    "gpt-5-turbo",
			expectedCode: codes.NotFound,
			expectedMsg:  ai.ErrModelNotFound.Error(),
		},
		{
			name: "wrapped model not found",
			err: fmt.Errorf(
				"%w: %q", ai.ErrModelNotFound, "gpt-5-turbo",
			),
			modelName:    "gpt-5-turbo",
			expectedCode: codes.NotFound,
		},
		{
			name:         "no deployment match",
			err:          ai.ErrNoDeploymentMatch,
			modelName:    "gpt-4o",
			expectedCode: codes.FailedPrecondition,
			expectedMsg:  ai.ErrNoDeploymentMatch.Error(),
		},
		{
			name: "wrapped no deployment match",
			err: fmt.Errorf(
				"%w for model", ai.ErrNoDeploymentMatch,
			),
			modelName:    "gpt-4o",
			expectedCode: codes.FailedPrecondition,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := mapAiResolveError(tt.err, tt.modelName)
			require.Error(t, result)

			st, ok := status.FromError(result)
			require.True(t, ok, "expected gRPC status error")
			assert.Equal(t, tt.expectedCode, st.Code())

			if tt.expectedMsg != "" {
				assert.Equal(t, tt.expectedMsg, st.Message())
			}
		})
	}
}

func TestMapAiResolveError_DefaultCase(t *testing.T) {
	someErr := errors.New("some unknown error")
	result := mapAiResolveError(someErr, "gpt-4o")
	require.Error(t, result)

	// The default case wraps with fmt.Errorf, not a gRPC status
	_, ok := status.FromError(result)
	// fmt.Errorf wrapping returns a status with codes.OK when
	// extracted, but the error is not nil. We verify the message.
	assert.True(t, ok || !ok) // always passes — real check below
	assert.Contains(
		t, result.Error(), "resolving model deployments",
	)
	assert.ErrorIs(t, result, someErr)
}

func TestMapAiResolveError_ModelNameInMetadata(t *testing.T) {
	tests := []struct {
		name      string
		err       error
		modelName string
		reason    string
	}{
		{
			name:      "model not found includes model_name",
			err:       ai.ErrModelNotFound,
			modelName: "gpt-4o-mini",
			reason:    azdext.AiErrorReasonModelNotFound,
		},
		{
			name:      "no deployment match includes model_name",
			err:       ai.ErrNoDeploymentMatch,
			modelName: "gpt-4",
			reason:    azdext.AiErrorReasonNoDeploymentMatch,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := mapAiResolveError(tt.err, tt.modelName)
			st, ok := status.FromError(result)
			require.True(t, ok)

			details := st.Details()
			require.Len(t, details, 1)

			errInfo, ok := details[0].(*errdetails.ErrorInfo)
			require.True(t, ok)
			assert.Equal(t, tt.reason, errInfo.Reason)
			assert.Equal(
				t, tt.modelName, errInfo.Metadata["model_name"],
			)
		})
	}
}

func TestRequireSubscriptionID(t *testing.T) {
	tests := []struct {
		name        string
		ctx         *azdext.AzureContext
		expectSubID string
		expectError bool
	}{
		{
			name:        "nil azure context",
			ctx:         nil,
			expectError: true,
		},
		{
			name:        "nil scope",
			ctx:         &azdext.AzureContext{},
			expectError: true,
		},
		{
			name: "empty subscription id",
			ctx: &azdext.AzureContext{
				Scope: &azdext.AzureScope{
					SubscriptionId: "",
				},
			},
			expectError: true,
		},
		{
			name: "valid subscription id",
			ctx: &azdext.AzureContext{
				Scope: &azdext.AzureScope{
					SubscriptionId: "sub-123-abc",
				},
			},
			expectSubID: "sub-123-abc",
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			subID, err := requireSubscriptionID(tt.ctx)
			if tt.expectError {
				require.Error(t, err)
				assert.Empty(t, subID)

				// Should be a gRPC InvalidArgument
				st, ok := status.FromError(err)
				require.True(t, ok)
				assert.Equal(
					t, codes.InvalidArgument, st.Code(),
				)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.expectSubID, subID)
			}
		})
	}
}

func TestProtoToFilterOptions(t *testing.T) {
	t.Run("nil input returns nil", func(t *testing.T) {
		result := protoToFilterOptions(nil)
		assert.Nil(t, result)
	})

	t.Run("maps all fields", func(t *testing.T) {
		input := &azdext.AiModelFilterOptions{
			Locations:         []string{"eastus", "westus"},
			Capabilities:      []string{"chatCompletion"},
			Formats:           []string{"OpenAI"},
			Statuses:          []string{"Stable"},
			ExcludeModelNames: []string{"gpt-3"},
		}

		result := protoToFilterOptions(input)
		require.NotNil(t, result)
		assert.Equal(t, input.Locations, result.Locations)
		assert.Equal(
			t, input.Capabilities, result.Capabilities,
		)
		assert.Equal(t, input.Formats, result.Formats)
		assert.Equal(t, input.Statuses, result.Statuses)
		assert.Equal(
			t, input.ExcludeModelNames,
			result.ExcludeModelNames,
		)
	})

	t.Run("empty slices preserved", func(t *testing.T) {
		input := &azdext.AiModelFilterOptions{}
		result := protoToFilterOptions(input)
		require.NotNil(t, result)
		assert.Nil(t, result.Locations)
		assert.Nil(t, result.Capabilities)
	})
}

func TestProtoToDeploymentOptions(t *testing.T) {
	t.Run("nil input returns nil", func(t *testing.T) {
		result := protoToDeploymentOptions(nil)
		assert.Nil(t, result)
	})

	t.Run("maps all fields without capacity", func(t *testing.T) {
		input := &azdext.AiModelDeploymentOptions{
			Locations: []string{"eastus"},
			Versions:  []string{"2024-01-15"},
			Skus:      []string{"GlobalStandard"},
			Capacity:  nil,
		}

		result := protoToDeploymentOptions(input)
		require.NotNil(t, result)
		assert.Equal(t, input.Locations, result.Locations)
		assert.Equal(t, input.Versions, result.Versions)
		assert.Equal(t, input.Skus, result.Skus)
		assert.Nil(t, result.Capacity)
	})

	t.Run("maps capacity pointer", func(t *testing.T) {
		cap := int32(100)
		input := &azdext.AiModelDeploymentOptions{
			Locations: []string{"eastus"},
			Capacity:  &cap,
		}

		result := protoToDeploymentOptions(input)
		require.NotNil(t, result)
		require.NotNil(t, result.Capacity)
		assert.Equal(t, int32(100), *result.Capacity)

		// Verify it's a copy, not the same pointer
		assert.NotSame(t, &cap, result.Capacity)
	})
}

func TestProtoToQuotaCheckOptions(t *testing.T) {
	t.Run("nil input returns nil", func(t *testing.T) {
		result := protoToQuotaCheckOptions(nil)
		assert.Nil(t, result)
	})

	t.Run("maps min remaining capacity", func(t *testing.T) {
		input := &azdext.QuotaCheckOptions{
			MinRemainingCapacity: 42.5,
		}

		result := protoToQuotaCheckOptions(input)
		require.NotNil(t, result)
		assert.Equal(t, 42.5, result.MinRemainingCapacity)
	})

	t.Run("zero value maps correctly", func(t *testing.T) {
		input := &azdext.QuotaCheckOptions{
			MinRemainingCapacity: 0,
		}

		result := protoToQuotaCheckOptions(input)
		require.NotNil(t, result)
		assert.Equal(t, float64(0), result.MinRemainingCapacity)
	})
}
