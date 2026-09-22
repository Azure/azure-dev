// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azdext

import (
	"errors"
	v1beta "github.com/azure/azure-dev/cli/azd/pkg/azdext/contracts/v1beta"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/known/structpb"
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

func TestBetaServiceTargetEnvelope_ErrorsAndProgress(t *testing.T) {
	t.Parallel()
	envelope := NewBetaServiceTargetEnvelope()
	message := &v1beta.ServiceTargetMessage{}
	require.NoError(t, envelope.GetError(message))
	envelope.SetError(message, &ServiceError{Message: "preview failed", ErrorCode: "PreviewFailed"})
	serviceError, ok := errors.AsType[*ServiceError](envelope.GetError(message))
	require.True(t, ok)
	require.Equal(t, "PreviewFailed", serviceError.ErrorCode)
	require.Equal(t, "preview failed", serviceError.Message)
	envelope.SetError(message, nil)
	require.Nil(t, message.Error)
	require.NoError(t, envelope.GetError(message))
	require.Nil(t, envelope.GetInnerMessage(message))

	progress := envelope.CreateProgressMessage("request", "packaging")
	require.True(t, envelope.IsProgressMessage(progress))
	require.Equal(t, "packaging", envelope.GetProgressMessage(progress))
	require.Equal(t, "request", envelope.GetRequestId(t.Context(), progress))
	require.Equal(t, "request", progress.GetProgressMessage().RequestId)
	require.Same(t, progress.GetProgressMessage(), envelope.GetInnerMessage(progress))
}

func TestBetaServiceTargetEnvelope_PreviewRoundTrip(t *testing.T) {
	t.Parallel()

	data, err := structpb.NewStruct(map[string]any{
		"image":    "registry.example.com/app:latest",
		"replicas": 2,
		"build":    false,
		"settings": map[string]any{"mode": "preview"},
		"changes":  []any{"create", "configure"},
	})
	require.NoError(t, err)
	request := &v1beta.ServiceTargetPreviewRequest{
		ServiceConfig: &v1beta.ServiceConfig{Name: "web-service", Host: "custom"},
	}

	response := &v1beta.ServiceTargetPreviewResponse{
		Result: &v1beta.ServiceDeployPreviewResult{Message: "Deployment preview", Data: data},
	}
	tests := []struct {
		name        string
		message     *v1beta.ServiceTargetMessage
		inner       proto.Message
		fieldName   protoreflect.Name
		fieldNumber protoreflect.FieldNumber
	}{
		{
			name: "Request",
			message: &v1beta.ServiceTargetMessage{
				MessageType: &v1beta.ServiceTargetMessage_PreviewRequest{PreviewRequest: request},
			},
			inner:       request,
			fieldName:   "preview_request",
			fieldNumber: 21,
		},
		{
			name: "Response",
			message: &v1beta.ServiceTargetMessage{
				MessageType: &v1beta.ServiceTargetMessage_PreviewResponse{PreviewResponse: response},
			},
			inner:       response,
			fieldName:   "preview_response",
			fieldNumber: 22,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			env := NewBetaServiceTargetEnvelope()
			env.SetRequestId(t.Context(), tt.message, "preview-1")
			require.Equal(t, tt.fieldNumber, tt.message.ProtoReflect().Descriptor().Fields().ByName(tt.fieldName).Number())
			wire, err := proto.Marshal(tt.message)
			require.NoError(t, err)
			var decoded v1beta.ServiceTargetMessage
			require.NoError(t, proto.Unmarshal(wire, &decoded))
			require.True(t, proto.Equal(tt.message, &decoded))
			inner, ok := env.GetInnerMessage(&decoded).(proto.Message)
			require.True(t, ok, "the envelope must expose the preview request or response to the broker")
			require.True(t, proto.Equal(tt.inner, inner))
			require.Equal(t, "preview-1", env.GetRequestId(t.Context(), &decoded))
			require.False(t, env.IsProgressMessage(&decoded))
			require.Empty(t, env.GetProgressMessage(&decoded))
			require.NoError(t, env.GetError(&decoded))
		})
	}
}
