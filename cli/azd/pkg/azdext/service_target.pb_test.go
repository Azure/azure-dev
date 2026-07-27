// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azdext

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestServiceTargetEnvelope_GetSetError(t *testing.T) {
	env := NewServiceTargetEnvelope()

	t.Run("NilError", func(t *testing.T) {
		msg := &ServiceTargetMessage{}
		require.Nil(t, env.GetError(msg))
	})

	t.Run("RoundTripError", func(t *testing.T) {
		msg := &ServiceTargetMessage{}
		svcErr := &ServiceError{
			Message:   "deploy failed",
			ErrorCode: "DeploymentFailed",
		}
		env.SetError(msg, svcErr)
		require.NotNil(t, msg.Error)

		unwrapped := env.GetError(msg)
		require.Error(t, unwrapped)
		require.Contains(t, unwrapped.Error(), "deploy failed")
	})
}

func TestServiceTargetEnvelope_GetInnerMessage(t *testing.T) {
	env := NewServiceTargetEnvelope()

	tests := []struct {
		name    string
		msg     *ServiceTargetMessage
		wantNil bool
	}{
		{
			name: "RegisterRequest",
			msg: &ServiceTargetMessage{
				MessageType: &ServiceTargetMessage_RegisterServiceTargetRequest{
					RegisterServiceTargetRequest: &RegisterServiceTargetRequest{},
				},
			},
		},
		{
			name: "DeployRequest",
			msg: &ServiceTargetMessage{
				MessageType: &ServiceTargetMessage_DeployRequest{
					DeployRequest: &ServiceTargetDeployRequest{},
				},
			},
		},
		{
			name: "GetTargetResourceRequest",
			msg: &ServiceTargetMessage{
				MessageType: &ServiceTargetMessage_GetTargetResourceRequest{
					GetTargetResourceRequest: &GetTargetResourceRequest{},
				},
			},
		},
		{
			name: "PackageRequest",
			msg: &ServiceTargetMessage{
				MessageType: &ServiceTargetMessage_PackageRequest{
					PackageRequest: &ServiceTargetPackageRequest{},
				},
			},
		},
		{
			name: "PublishRequest",
			msg: &ServiceTargetMessage{
				MessageType: &ServiceTargetMessage_PublishRequest{
					PublishRequest: &ServiceTargetPublishRequest{},
				},
			},
		},
		{
			name: "EndpointsRequest",
			msg: &ServiceTargetMessage{
				MessageType: &ServiceTargetMessage_EndpointsRequest{
					EndpointsRequest: &ServiceTargetEndpointsRequest{},
				},
			},
		},
		{
			name: "ProgressMessage",
			msg: &ServiceTargetMessage{
				MessageType: &ServiceTargetMessage_ProgressMessage{
					ProgressMessage: &ServiceTargetProgressMessage{
						Message: "progress",
					},
				},
			},
		},
		{
			name:    "NilMessageType",
			msg:     &ServiceTargetMessage{},
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

func TestServiceTargetEnvelope_ProgressMessage(t *testing.T) {
	env := NewServiceTargetEnvelope()

	t.Run("IsProgressMessage_True", func(t *testing.T) {
		msg := &ServiceTargetMessage{
			MessageType: &ServiceTargetMessage_ProgressMessage{
				ProgressMessage: &ServiceTargetProgressMessage{
					Message: "deploying...",
				},
			},
		}
		require.True(t, env.IsProgressMessage(msg))
		require.Equal(t, "deploying...", env.GetProgressMessage(msg))
	})

	t.Run("IsProgressMessage_False", func(t *testing.T) {
		msg := &ServiceTargetMessage{
			MessageType: &ServiceTargetMessage_DeployRequest{
				DeployRequest: &ServiceTargetDeployRequest{},
			},
		}
		require.False(t, env.IsProgressMessage(msg))
		require.Empty(t, env.GetProgressMessage(msg))
	})

	t.Run("CreateProgressMessage", func(t *testing.T) {
		msg := env.CreateProgressMessage("st-1", "packaging...")
		require.NotNil(t, msg)
		require.Equal(t, "st-1", msg.RequestId)
		require.True(t, env.IsProgressMessage(msg))
		require.Equal(t, "packaging...", env.GetProgressMessage(msg))
	})
}
