// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azdext

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestFrameworkServiceEnvelope_GetSetError(t *testing.T) {
	env := NewFrameworkServiceEnvelope()

	t.Run("NilError", func(t *testing.T) {
		msg := &FrameworkServiceMessage{}
		require.Nil(t, env.GetError(msg))
	})

	t.Run("RoundTripLocalError", func(t *testing.T) {
		msg := &FrameworkServiceMessage{}
		localErr := &LocalError{
			Message:  "validation failed",
			Code:     "invalid_config",
			Category: LocalErrorCategoryValidation,
		}
		env.SetError(msg, localErr)
		require.NotNil(t, msg.Error)

		unwrapped := env.GetError(msg)
		require.Error(t, unwrapped)
		require.Contains(t, unwrapped.Error(), "validation failed")
	})

	t.Run("RoundTripServiceError", func(t *testing.T) {
		msg := &FrameworkServiceMessage{}
		svcErr := &ServiceError{
			Message:     "not found",
			ErrorCode:   "NotFound",
			StatusCode:  404,
			ServiceName: "api.example.com",
		}
		env.SetError(msg, svcErr)
		require.NotNil(t, msg.Error)

		unwrapped := env.GetError(msg)
		require.Error(t, unwrapped)
		require.Contains(t, unwrapped.Error(), "not found")
	})
}

func TestFrameworkServiceEnvelope_GetInnerMessage(t *testing.T) {
	env := NewFrameworkServiceEnvelope()

	tests := []struct {
		name     string
		msg      *FrameworkServiceMessage
		wantNil  bool
		wantType string
	}{
		{
			name: "RegisterRequest",
			msg: &FrameworkServiceMessage{
				MessageType: &FrameworkServiceMessage_RegisterFrameworkServiceRequest{
					RegisterFrameworkServiceRequest: &RegisterFrameworkServiceRequest{},
				},
			},
			wantType: "RegisterFrameworkServiceRequest",
		},
		{
			name: "InitializeRequest",
			msg: &FrameworkServiceMessage{
				MessageType: &FrameworkServiceMessage_InitializeRequest{
					InitializeRequest: &FrameworkServiceInitializeRequest{},
				},
			},
			wantType: "FrameworkServiceInitializeRequest",
		},
		{
			name: "BuildRequest",
			msg: &FrameworkServiceMessage{
				MessageType: &FrameworkServiceMessage_BuildRequest{
					BuildRequest: &FrameworkServiceBuildRequest{},
				},
			},
			wantType: "FrameworkServiceBuildRequest",
		},
		{
			name: "PackageRequest",
			msg: &FrameworkServiceMessage{
				MessageType: &FrameworkServiceMessage_PackageRequest{
					PackageRequest: &FrameworkServicePackageRequest{},
				},
			},
			wantType: "FrameworkServicePackageRequest",
		},
		{
			name: "RestoreRequest",
			msg: &FrameworkServiceMessage{
				MessageType: &FrameworkServiceMessage_RestoreRequest{
					RestoreRequest: &FrameworkServiceRestoreRequest{},
				},
			},
			wantType: "FrameworkServiceRestoreRequest",
		},
		{
			name: "ProgressMessage",
			msg: &FrameworkServiceMessage{
				MessageType: &FrameworkServiceMessage_ProgressMessage{
					ProgressMessage: &FrameworkServiceProgressMessage{
						Message: "building...",
					},
				},
			},
			wantType: "FrameworkServiceProgressMessage",
		},
		{
			name:    "NilMessageType",
			msg:     &FrameworkServiceMessage{},
			wantNil: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			inner := env.GetInnerMessage(tt.msg)
			if tt.wantNil {
				require.Nil(t, inner)
			} else {
				require.NotNil(t, inner)
			}
		})
	}
}

func TestFrameworkServiceEnvelope_ProgressMessage(t *testing.T) {
	env := NewFrameworkServiceEnvelope()

	t.Run("IsProgressMessage_True", func(t *testing.T) {
		msg := &FrameworkServiceMessage{
			MessageType: &FrameworkServiceMessage_ProgressMessage{
				ProgressMessage: &FrameworkServiceProgressMessage{
					Message: "step 1/3",
				},
			},
		}
		require.True(t, env.IsProgressMessage(msg))
		require.Equal(t, "step 1/3", env.GetProgressMessage(msg))
	})

	t.Run("IsProgressMessage_False", func(t *testing.T) {
		msg := &FrameworkServiceMessage{
			MessageType: &FrameworkServiceMessage_BuildRequest{
				BuildRequest: &FrameworkServiceBuildRequest{},
			},
		}
		require.False(t, env.IsProgressMessage(msg))
		require.Empty(t, env.GetProgressMessage(msg))
	})

	t.Run("CreateProgressMessage", func(t *testing.T) {
		msg := env.CreateProgressMessage("req-1", "deploying...")
		require.NotNil(t, msg)
		require.Equal(t, "req-1", msg.RequestId)
		require.True(t, env.IsProgressMessage(msg))
		require.Equal(t, "deploying...", env.GetProgressMessage(msg))
	})
}
