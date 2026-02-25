// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azdext

import (
	"errors"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExtensionError_RoundTrip(t *testing.T) {
	tests := []struct {
		name     string
		inputErr error
		wantNil  bool
		verify   func(t *testing.T, protoErr *ExtensionError, goErr error)
	}{
		{
			name:     "NilError",
			inputErr: nil,
			wantNil:  true,
		},
		{
			name:     "SimpleError",
			inputErr: errors.New("simple error"),
			verify: func(t *testing.T, protoErr *ExtensionError, goErr error) {
				assert.Equal(t, "simple error", protoErr.GetMessage())
				assert.Equal(t, ErrorOrigin_ERROR_ORIGIN_UNSPECIFIED, protoErr.GetOrigin())
				assert.Nil(t, protoErr.GetSource())

				assert.Equal(t, "simple error", goErr.Error())
			},
		},
		{
			name: "ExtServiceError",
			inputErr: &ServiceError{
				Message:     "Rate limit exceeded",
				ErrorCode:   "RateLimitExceeded",
				StatusCode:  429,
				ServiceName: "openai.azure.com",
				Suggestion:  "Retry with exponential backoff",
			},
			verify: func(t *testing.T, protoErr *ExtensionError, goErr error) {
				assert.Equal(t, "Rate limit exceeded", protoErr.GetMessage())
				assert.Equal(t, "Retry with exponential backoff", protoErr.GetSuggestion())
				assert.Equal(t, ErrorOrigin_ERROR_ORIGIN_SERVICE, protoErr.GetOrigin())

				svcDetail := protoErr.GetServiceError()
				require.NotNil(t, svcDetail)
				assert.Equal(t, "RateLimitExceeded", svcDetail.GetErrorCode())
				assert.Equal(t, int32(429), svcDetail.GetStatusCode())
				assert.Equal(t, "openai.azure.com", svcDetail.GetServiceName())

				var svcErr *ServiceError
				require.ErrorAs(t, goErr, &svcErr)
				assert.Equal(t, "Rate limit exceeded", svcErr.Message)
				assert.Equal(t, "RateLimitExceeded", svcErr.ErrorCode)
				assert.Equal(t, 429, svcErr.StatusCode)
				assert.Equal(t, "openai.azure.com", svcErr.ServiceName)
				assert.Equal(t, "Retry with exponential backoff", svcErr.Suggestion)
			},
		},
		{
			name: "ExtLocalError",
			inputErr: &LocalError{
				Message:    "invalid config",
				Code:       "invalid_config",
				Category:   LocalErrorCategoryValidation,
				Suggestion: "Add the missing required field",
			},
			verify: func(t *testing.T, protoErr *ExtensionError, goErr error) {
				assert.Equal(t, ErrorOrigin_ERROR_ORIGIN_LOCAL, protoErr.GetOrigin())
				assert.Equal(t, "Add the missing required field", protoErr.GetSuggestion())

				localDetail := protoErr.GetLocalError()
				require.NotNil(t, localDetail)
				assert.Equal(t, "invalid_config", localDetail.GetCode())
				assert.Equal(t, "validation", localDetail.GetCategory())

				var localErr *LocalError
				require.ErrorAs(t, goErr, &localErr)
				assert.Equal(t, "invalid_config", localErr.Code)
				assert.Equal(t, LocalErrorCategoryValidation, localErr.Category)
				assert.Equal(t, "Add the missing required field", localErr.Suggestion)
			},
		},
		{
			name: "AzCoreResponseError",
			inputErr: &azcore.ResponseError{
				ErrorCode:  "ResourceNotFound",
				StatusCode: 404,
			},
			verify: func(t *testing.T, protoErr *ExtensionError, goErr error) {
				assert.Equal(t, ErrorOrigin_ERROR_ORIGIN_SERVICE, protoErr.GetOrigin())

				svcDetail := protoErr.GetServiceError()
				require.NotNil(t, svcDetail)
				assert.Equal(t, "ResourceNotFound", svcDetail.GetErrorCode())
				assert.Equal(t, int32(404), svcDetail.GetStatusCode())

				var svcErr *ServiceError
				require.ErrorAs(t, goErr, &svcErr)
				assert.Equal(t, "ResourceNotFound", svcErr.ErrorCode)
				assert.Equal(t, 404, svcErr.StatusCode)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			protoErr := WrapError(tt.inputErr)

			if tt.wantNil {
				assert.Nil(t, protoErr)
				assert.Nil(t, UnwrapError(nil))
				return
			}

			require.NotNil(t, protoErr)
			goErr := UnwrapError(protoErr)
			require.NotNil(t, goErr)

			tt.verify(t, protoErr, goErr)
		})
	}
}
