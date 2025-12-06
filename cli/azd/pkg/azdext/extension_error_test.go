// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azdext

import (
	"errors"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/stretchr/testify/assert"
)

func TestExtensionError_RoundTrip(t *testing.T) {
	tests := []struct {
		name                    string
		inputErr                error
		wantInfo                errorInfo
		useErrorStringAsMessage bool
	}{
		{
			name:     "NilError",
			inputErr: nil,
			wantInfo: errorInfo{},
		},
		{
			name:     "SimpleError",
			inputErr: errors.New("simple error"),
			wantInfo: errorInfo{
				message: "simple error",
			},
		},
		{
			name: "ExtensionResponseError",
			inputErr: &ExtensionResponseError{
				Message:     "Rate limit exceeded",
				Details:     "Too many requests",
				ErrorCode:   "RateLimitExceeded",
				StatusCode:  429,
				ServiceName: "openai.azure.com",
			},
			wantInfo: errorInfo{
				message:    "Rate limit exceeded",
				details:    "Too many requests",
				errorCode:  "RateLimitExceeded",
				statusCode: 429,
				service:    "openai.azure.com",
			},
		},
		{
			name: "AzCoreResponseError",
			inputErr: &azcore.ResponseError{
				ErrorCode:  "ResourceNotFound",
				StatusCode: 404,
			},
			wantInfo: errorInfo{
				errorCode:  "ResourceNotFound",
				statusCode: 404,
			},
			useErrorStringAsMessage: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.useErrorStringAsMessage {
				tt.wantInfo.message = tt.inputErr.Error()
			}

			// Helper to verify the proto message content
			type protoMessage interface {
				GetMessage() string
				GetDetails() string
				GetErrorCode() string
				GetStatusCode() int32
				GetServiceName() string
			}
			verifyProto := func(t *testing.T, msg protoMessage) {
				assert.NotNil(t, msg)
				assert.Equal(t, tt.wantInfo.message, msg.GetMessage())
				assert.Equal(t, tt.wantInfo.details, msg.GetDetails())
				assert.Equal(t, tt.wantInfo.errorCode, msg.GetErrorCode())
				assert.Equal(t, tt.wantInfo.statusCode, msg.GetStatusCode())
				assert.Equal(t, tt.wantInfo.service, msg.GetServiceName())
			}

			// Helper to verify the unwrapped error
			verifyUnwrapped := func(t *testing.T, err error) {
				var extErr *ExtensionResponseError
				if assert.ErrorAs(t, err, &extErr) {
					assert.Equal(t, tt.wantInfo.message, extErr.Message)
					assert.Equal(t, tt.wantInfo.details, extErr.Details)
					assert.Equal(t, tt.wantInfo.errorCode, extErr.ErrorCode)
					assert.Equal(t, int(tt.wantInfo.statusCode), extErr.StatusCode)
					assert.Equal(t, tt.wantInfo.service, extErr.ServiceName)
				}
			}

			// Test ServiceTarget wrapping
			stMsg := WrapErrorForServiceTarget(tt.inputErr)
			if tt.inputErr == nil {
				assert.Nil(t, stMsg)
			} else {
				verifyProto(t, stMsg)
				verifyUnwrapped(t, UnwrapErrorFromServiceTarget(stMsg))
			}

			// Test FrameworkService wrapping
			fsMsg := WrapErrorForFrameworkService(tt.inputErr)
			if tt.inputErr == nil {
				assert.Nil(t, fsMsg)
			} else {
				verifyProto(t, fsMsg)
				verifyUnwrapped(t, UnwrapErrorFromFrameworkService(fsMsg))
			}
		})
	}
}
