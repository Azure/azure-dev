// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azdext

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

// -----------------------------------------------------------------------
// FrameworkServiceEnvelope tests
// -----------------------------------------------------------------------

func TestFrameworkServiceEnvelope_GetSetRequestId(t *testing.T) {
	env := NewFrameworkServiceEnvelope()
	msg := &FrameworkServiceMessage{RequestId: "req-123"}

	require.Equal(t, "req-123", env.GetRequestId(context.Background(), msg))

	env.SetRequestId(context.Background(), msg, "req-456")
	require.Equal(t, "req-456", msg.RequestId)
}

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

// -----------------------------------------------------------------------
// ServiceTargetEnvelope tests
// -----------------------------------------------------------------------

func TestServiceTargetEnvelope_GetSetRequestId(t *testing.T) {
	env := NewServiceTargetEnvelope()
	msg := &ServiceTargetMessage{RequestId: "st-123"}

	require.Equal(t, "st-123", env.GetRequestId(context.Background(), msg))

	env.SetRequestId(context.Background(), msg, "st-456")
	require.Equal(t, "st-456", msg.RequestId)
}

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

// -----------------------------------------------------------------------
// EventMessageEnvelope tests
// -----------------------------------------------------------------------

func TestEventMessageEnvelope_GetInnerMessage(t *testing.T) {
	env := NewEventMessageEnvelope()

	tests := []struct {
		name    string
		msg     *EventMessage
		wantNil bool
	}{
		{
			name: "SubscribeProjectEvent",
			msg: &EventMessage{
				MessageType: &EventMessage_SubscribeProjectEvent{
					SubscribeProjectEvent: &SubscribeProjectEvent{
						EventNames: []string{"provision"},
					},
				},
			},
		},
		{
			name: "InvokeProjectHandler",
			msg: &EventMessage{
				MessageType: &EventMessage_InvokeProjectHandler{
					InvokeProjectHandler: &InvokeProjectHandler{
						EventName: "provision",
					},
				},
			},
		},
		{
			name: "ProjectHandlerStatus",
			msg: &EventMessage{
				MessageType: &EventMessage_ProjectHandlerStatus{
					ProjectHandlerStatus: &ProjectHandlerStatus{
						EventName: "provision",
					},
				},
			},
		},
		{
			name: "SubscribeServiceEvent",
			msg: &EventMessage{
				MessageType: &EventMessage_SubscribeServiceEvent{
					SubscribeServiceEvent: &SubscribeServiceEvent{
						EventNames: []string{"deploy"},
					},
				},
			},
		},
		{
			name: "InvokeServiceHandler",
			msg: &EventMessage{
				MessageType: &EventMessage_InvokeServiceHandler{
					InvokeServiceHandler: &InvokeServiceHandler{
						EventName: "deploy",
					},
				},
			},
		},
		{
			name: "ServiceHandlerStatus",
			msg: &EventMessage{
				MessageType: &EventMessage_ServiceHandlerStatus{
					ServiceHandlerStatus: &ServiceHandlerStatus{
						EventName:   "deploy",
						ServiceName: "api",
					},
				},
			},
		},
		{
			name:    "NilMessageType",
			msg:     &EventMessage{},
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

func TestEventMessageEnvelope_NoOps(t *testing.T) {
	env := NewEventMessageEnvelope()
	msg := &EventMessage{}

	// SetRequestId is a no-op
	env.SetRequestId(context.Background(), msg, "ignored")

	// GetError always returns nil
	require.Nil(t, env.GetError(msg))

	// SetError is a no-op
	env.SetError(msg, &LocalError{Message: "ignored"})

	// IsProgressMessage always false
	require.False(t, env.IsProgressMessage(msg))

	// GetProgressMessage always empty
	require.Empty(t, env.GetProgressMessage(msg))

	// CreateProgressMessage always nil
	require.Nil(t, env.CreateProgressMessage("id", "msg"))
}

func TestEventMessageEnvelope_GetRequestId_NoContext(t *testing.T) {
	env := NewEventMessageEnvelope()

	// Without extension ID in context, should return ""
	msg := &EventMessage{
		MessageType: &EventMessage_SubscribeProjectEvent{
			SubscribeProjectEvent: &SubscribeProjectEvent{
				EventNames: []string{"provision"},
			},
		},
	}

	id := env.GetRequestId(context.Background(), msg)
	require.Empty(t, id)
}
