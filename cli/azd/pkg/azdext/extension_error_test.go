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

				// Unspecified origin falls back to ExtServiceError
				var svcErr *ServiceError
				require.ErrorAs(t, goErr, &svcErr)
				assert.Equal(t, "simple error", svcErr.Message)
			},
		},
		{
			name: "ExtServiceError",
			inputErr: &ServiceError{
				Message:     "Rate limit exceeded",
				Details:     "Too many requests",
				ErrorCode:   "RateLimitExceeded",
				StatusCode:  429,
				ServiceName: "openai.azure.com",
			},
			verify: func(t *testing.T, protoErr *ExtensionError, goErr error) {
				assert.Equal(t, "Rate limit exceeded", protoErr.GetMessage())
				assert.Equal(t, "Too many requests", protoErr.GetDetails())
				assert.Equal(t, ErrorOrigin_ERROR_ORIGIN_SERVICE, protoErr.GetOrigin())

				svcDetail := protoErr.GetServiceError()
				require.NotNil(t, svcDetail)
				assert.Equal(t, "RateLimitExceeded", svcDetail.GetErrorCode())
				assert.Equal(t, int32(429), svcDetail.GetStatusCode())
				assert.Equal(t, "openai.azure.com", svcDetail.GetServiceName())

				var svcErr *ServiceError
				require.ErrorAs(t, goErr, &svcErr)
				assert.Equal(t, "Rate limit exceeded", svcErr.Message)
				assert.Equal(t, "Too many requests", svcErr.Details)
				assert.Equal(t, "RateLimitExceeded", svcErr.ErrorCode)
				assert.Equal(t, 429, svcErr.StatusCode)
				assert.Equal(t, "openai.azure.com", svcErr.ServiceName)
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
