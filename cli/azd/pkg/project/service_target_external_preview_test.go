// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"errors"
	"io"
	"log"
	"testing"
	"time"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/extensions"
	"github.com/azure/azure-dev/cli/azd/pkg/grpcbroker"
	"github.com/azure/azure-dev/cli/azd/pkg/lazy"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/structpb"
)

type externalPreviewStream struct {
	ctx       context.Context
	requests  chan *azdext.ServiceTargetMessage
	responses chan *azdext.ServiceTargetMessage
	response  *azdext.ServiceTargetMessage
	sendErr   error
}

func (s *externalPreviewStream) Send(request *azdext.ServiceTargetMessage) error {
	s.requests <- request
	if s.sendErr != nil {
		return s.sendErr
	}
	if s.response != nil {
		response := proto.Clone(s.response).(*azdext.ServiceTargetMessage)
		response.RequestId = request.RequestId
		s.responses <- response
	}
	return nil
}

func (s *externalPreviewStream) Recv() (*azdext.ServiceTargetMessage, error) {
	select {
	case <-s.ctx.Done():
		return nil, io.EOF
	case response := <-s.responses:
		return response, nil
	}
}

func newExternalPreviewTarget(
	t *testing.T,
	response *azdext.ServiceTargetMessage,
	sendErr error,
) (*ExternalServiceTarget, *externalPreviewStream) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	stream := &externalPreviewStream{
		ctx:       ctx,
		requests:  make(chan *azdext.ServiceTargetMessage, 4),
		responses: make(chan *azdext.ServiceTargetMessage, 4),
		response:  response,
		sendErr:   sendErr,
	}
	broker := grpcbroker.NewMessageBroker(
		stream, azdext.NewServiceTargetEnvelope(), "preview-test", log.New(io.Discard, "", 0),
	)
	done := make(chan error, 1)
	go func() { done <- broker.Run(ctx) }()
	require.NoError(t, broker.Ready(ctx))
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				require.ErrorIs(t, err, context.Canceled)
			}
		case <-time.After(time.Second):
			t.Error("preview broker did not stop")
		}
	})
	env := environment.NewWithValues("test", map[string]string{"IMAGE_NAME": "example/image:v1"})
	target := NewExternalServiceTarget(
		"custom", ServiceTargetKind("custom"), &extensions.Extension{Id: "test.extension"},
		broker, nil, nil, lazy.From(env), true,
	).(*ExternalServiceTarget)
	return target, stream
}

func TestExternalServiceTargetPreviewFailsClosed(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		support []bool
	}{
		{name: "LegacyConstructor"},
		{name: "ExplicitlyUnsupported", support: []bool{false}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			// Nil broker and environment ensure unsupported preview cannot send messages or resolve state.
			target := NewExternalServiceTarget(
				"custom", ServiceTargetKind("custom"), nil, nil, nil, nil, nil, tc.support...,
			)
			previewer, ok := target.(ServiceTargetPreviewer)
			require.True(t, ok)
			capability, ok := target.(ServiceTargetPreviewCapability)
			require.True(t, ok)
			require.False(t, capability.SupportsPreview())
			result, err := previewer.Preview(t.Context(), &ServiceConfig{Name: "api"})
			require.ErrorContains(t, err, "does not support deployment preview")
			require.Nil(t, result)
		})
	}
}

func TestExternalServiceTargetPreviewUsesOnlyPreviewRequest(t *testing.T) {
	t.Parallel()
	data, err := structpb.NewStruct(map[string]any{
		"service": "api",
		"plan": map[string]any{
			"operation": "create",
			"count":     2,
			"enabled":   true,
			"warnings":  []any{"image will be built"},
		},
	})
	require.NoError(t, err)
	target, stream := newExternalPreviewTarget(t, &azdext.ServiceTargetMessage{
		MessageType: &azdext.ServiceTargetMessage_PreviewResponse{
			PreviewResponse: &azdext.ServiceTargetPreviewResponse{
				Result: &azdext.ServiceDeployPreviewResult{Message: "Read-only plan", Data: data},
			},
		},
	}, nil)
	service := &ServiceConfig{Name: "api", Host: "custom", Image: osutil.NewExpandableString("${IMAGE_NAME}")}
	result, err := target.Preview(t.Context(), service)
	require.NoError(t, err)
	require.Equal(t, "Read-only plan", result.Message)
	require.True(t, target.SupportsPreview())
	require.Equal(t, data.AsMap(), result.Data)
	require.Len(t, stream.requests, 1, "preview must not send Initialize, Package, Publish, or Deploy requests")
	request := <-stream.requests
	require.NotEmpty(t, request.RequestId)
	require.NotNil(t, request.GetPreviewRequest())
	require.Equal(t, "api", request.GetPreviewRequest().GetServiceConfig().Name)
	require.Equal(t, "example/image:v1", request.GetPreviewRequest().GetServiceConfig().Image)
	require.Nil(t, request.GetInitializeRequest())
	require.Nil(t, request.GetGetTargetResourceRequest())
	require.Nil(t, request.GetDeployRequest())
	require.Equal(t, osutil.NewExpandableString("${IMAGE_NAME}"), service.Image)
}

func TestExternalServiceTargetPreviewInvalidResponse(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name     string
		response *azdext.ServiceTargetMessage
	}{
		{
			name: "MissingResponse",
			response: &azdext.ServiceTargetMessage{
				MessageType: &azdext.ServiceTargetMessage_InitializeResponse{
					InitializeResponse: &azdext.ServiceTargetInitializeResponse{},
				},
			},
		},
		{
			name: "MissingResult",
			response: &azdext.ServiceTargetMessage{
				MessageType: &azdext.ServiceTargetMessage_PreviewResponse{
					PreviewResponse: &azdext.ServiceTargetPreviewResponse{},
				},
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			target, stream := newExternalPreviewTarget(t, tc.response, nil)
			result, err := target.Preview(t.Context(), &ServiceConfig{Name: "api"})
			require.Nil(t, result)
			require.ErrorContains(t, err, "invalid preview response: missing preview result")
			require.Len(t, stream.requests, 1)
		})
	}
}

func TestExternalServiceTargetPreviewEmptyData(t *testing.T) {
	t.Parallel()
	target, _ := newExternalPreviewTarget(t, &azdext.ServiceTargetMessage{
		MessageType: &azdext.ServiceTargetMessage_PreviewResponse{
			PreviewResponse: &azdext.ServiceTargetPreviewResponse{
				Result: &azdext.ServiceDeployPreviewResult{},
			},
		},
	}, nil)
	result, err := target.Preview(t.Context(), &ServiceConfig{Name: "api"})
	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotNil(t, result.Data)
	require.Empty(t, result.Data)
}

func TestExternalServiceTargetPreviewErrors(t *testing.T) {
	t.Parallel()
	t.Run("Send", func(t *testing.T) {
		t.Parallel()
		expectedErr := errors.New("stream send failed")
		target, _ := newExternalPreviewTarget(t, nil, expectedErr)
		_, err := target.Preview(t.Context(), &ServiceConfig{Name: "api"})
		require.ErrorIs(t, err, expectedErr)
	})
	t.Run("Provider", func(t *testing.T) {
		t.Parallel()
		target, _ := newExternalPreviewTarget(t, &azdext.ServiceTargetMessage{
			Error: azdext.WrapError(errors.New("provider read failed")),
		}, nil)
		_, err := target.Preview(t.Context(), &ServiceConfig{Name: "api"})
		require.ErrorContains(t, err, "provider read failed")
	})
	t.Run("Cancellation", func(t *testing.T) {
		t.Parallel()
		target, _ := newExternalPreviewTarget(t, nil, nil)
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		_, err := target.Preview(ctx, &ServiceConfig{Name: "api"})
		require.ErrorIs(t, err, context.Canceled)
	})
	t.Run("InvalidConfig", func(t *testing.T) {
		t.Parallel()
		target, stream := newExternalPreviewTarget(t, nil, nil)
		_, err := target.Preview(t.Context(), &ServiceConfig{Name: "api", Config: map[string]any{"bad": make(chan int)}})
		require.ErrorContains(t, err, "converting service config")
		require.Empty(t, stream.requests)
	})
	t.Run("NilConfig", func(t *testing.T) {
		t.Parallel()
		target, stream := newExternalPreviewTarget(t, nil, nil)
		_, err := target.Preview(t.Context(), nil)
		require.ErrorContains(t, err, "service configuration is required")
		require.Empty(t, stream.requests)
	})
}
