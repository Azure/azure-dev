// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azdext

import (
	"context"
	"errors"
	"io"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/structpb"
)

type fakeServiceTargetServer struct {
	UnimplementedServiceTargetServiceServer
	registrations    chan *RegisterServiceTargetRequest
	previewRequest   *ServiceTargetPreviewRequest
	previewResponses chan *ServiceTargetMessage
}

func (s *fakeServiceTargetServer) Stream(
	stream grpc.BidiStreamingServer[ServiceTargetMessage, ServiceTargetMessage],
) error {
	for {
		message, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}

		registration := message.GetRegisterServiceTargetRequest()
		if registration == nil {
			s.previewResponses <- message
			continue
		}

		s.registrations <- registration
		if err := stream.Send(&ServiceTargetMessage{
			RequestId: message.RequestId,
			MessageType: &ServiceTargetMessage_RegisterServiceTargetResponse{
				RegisterServiceTargetResponse: &RegisterServiceTargetResponse{},
			},
		}); err != nil {
			return err
		}
		if s.previewRequest != nil {
			if err := stream.Send(&ServiceTargetMessage{
				RequestId: "preview-1",
				MessageType: &ServiceTargetMessage_PreviewRequest{
					PreviewRequest: s.previewRequest,
				},
			}); err != nil {
				return err
			}
		}
	}
}

func startServiceTargetTestManager(
	t *testing.T,
	request *ServiceTargetPreviewRequest,
) (*ServiceTargetManager, *fakeServiceTargetServer, context.Context) {
	t.Helper()

	server := &fakeServiceTargetServer{
		registrations:    make(chan *RegisterServiceTargetRequest, 1),
		previewRequest:   request,
		previewResponses: make(chan *ServiceTargetMessage, 1),
	}
	listener := bufconn.Listen(1024 * 1024)
	grpcServer := grpc.NewServer()
	RegisterServiceTargetServiceServer(grpcServer, server)
	go func() { _ = grpcServer.Serve(listener) }()
	t.Cleanup(grpcServer.Stop)

	connection, err := grpc.NewClient(
		"passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return listener.DialContext(ctx)
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, connection.Close()) })

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	manager := NewServiceTargetManager("test.ext", &AzdClient{connection: connection}, nil)
	receiverDone := make(chan error, 1)
	go func() { receiverDone <- manager.Receive(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-receiverDone:
			if !errors.Is(err, context.Canceled) {
				assert.NoError(t, err)
			}
		case <-time.After(5 * time.Second):
			t.Error("service target receiver did not stop")
		}
		assert.NoError(t, manager.Close())
	})
	require.NoError(t, manager.Ready(ctx))
	return manager, server, ctx
}

func TestServiceTargetManager_Register_PreviewCapability(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name            string
		supportsPreview []bool
		wantPreview     bool
	}{
		{name: "Default"},
		{name: "ExplicitFalse", supportsPreview: []bool{false}},
		{name: "ExplicitTrue", supportsPreview: []bool{true}, wantPreview: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			manager, server, ctx := startServiceTargetTestManager(t, nil)
			var factoryCalls atomic.Int32
			factory := func() ServiceTargetProvider {
				factoryCalls.Add(1)
				return &mockServiceTargetPreviewProvider{}
			}
			require.NoError(t, manager.Register(ctx, factory, "custom", tt.supportsPreview...))
			registration := <-server.registrations
			assert.Equal(t, "custom", registration.Host)
			assert.Equal(t, tt.wantPreview, registration.GetSupportsPreview())
			assert.EqualValues(t, 2, registration.ProtoReflect().Descriptor().Fields().ByName("supports_preview").Number())
			assert.Zero(t, factoryCalls.Load(), "registration must not construct providers to detect preview support")
			assert.True(t, manager.componentManager.HasFactory("custom"))
		})
	}
}

func TestServiceTargetManager_PreviewRequest_Stream(t *testing.T) {
	t.Parallel()

	data, err := structpb.NewStruct(map[string]any{
		"image":   "registry.example.com/app:latest",
		"changes": []any{"create", "configure"},
	})
	require.NoError(t, err)
	result := &ServiceDeployPreviewResult{Message: "Deployment preview", Data: data}
	tests := []struct {
		name          string
		providerError error
	}{
		{name: "ResponseData"},
		{name: "ProviderError", providerError: errors.New("preview failed")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			serviceConfig := createTestServiceConfigForServiceTarget("web-service", "custom")
			manager, server, ctx := startServiceTargetTestManager(t, &ServiceTargetPreviewRequest{
				ServiceConfig: serviceConfig,
			})
			provider := &mockServiceTargetPreviewProvider{}
			provider.On("Preview", ctx, mock.MatchedBy(func(actual *ServiceConfig) bool {
				return proto.Equal(serviceConfig, actual)
			})).Return(result, tt.providerError).Once()
			require.NoError(t, manager.Register(ctx, func() ServiceTargetProvider { return provider }, "custom", true))

			select {
			case response := <-server.previewResponses:
				assert.Equal(t, "preview-1", response.RequestId)
				assert.Nil(t, response.GetDeployResponse())
				if tt.providerError != nil {
					require.ErrorContains(t, NewServiceTargetEnvelope().GetError(response), tt.providerError.Error())
					assert.Nil(t, response.GetPreviewResponse())
				} else {
					require.NoError(t, NewServiceTargetEnvelope().GetError(response))
					require.NotNil(t, response.GetPreviewResponse())
					assert.True(t, proto.Equal(result, response.GetPreviewResponse().Result))
				}
			case <-ctx.Done():
				t.Fatalf("preview response was not dispatched: %v", ctx.Err())
			}

			for _, method := range []string{"Initialize", "GetTargetResource", "Endpoints", "Package", "Publish", "Deploy"} {
				provider.AssertNumberOfCalls(t, method, 0)
			}
			provider.AssertExpectations(t)
			_, err := manager.componentManager.GetInstance(serviceConfig.Name)
			require.ErrorContains(t, err, "no service target instance found")
		})
	}
}
