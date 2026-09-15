// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azdext

import (
	"context"
	"errors"
	"io"
	"net"
	"testing"
	"time"

	v1beta "github.com/azure/azure-dev/cli/azd/pkg/azdext/contracts/v1beta"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/types/known/structpb"
)

type fakeBetaServiceTargetServer struct {
	v1beta.UnimplementedServiceTargetServiceServer
	registrations    chan *v1beta.RegisterServiceTargetRequest
	previewRequest   *v1beta.ServiceTargetPreviewRequest
	previewResponses chan *v1beta.ServiceTargetMessage
}

func (s *fakeBetaServiceTargetServer) Stream(
	stream grpc.BidiStreamingServer[v1beta.ServiceTargetMessage, v1beta.ServiceTargetMessage],
) error {
	for {
		message, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		if registration := message.GetRegisterServiceTargetRequest(); registration != nil {
			s.registrations <- registration
			if err := stream.Send(&v1beta.ServiceTargetMessage{
				RequestId: message.RequestId,
				MessageType: &v1beta.ServiceTargetMessage_RegisterServiceTargetResponse{
					RegisterServiceTargetResponse: &v1beta.RegisterServiceTargetResponse{},
				},
			}); err != nil {
				return err
			}
			if s.previewRequest != nil {
				if err := stream.Send(&v1beta.ServiceTargetMessage{
					RequestId: "preview-1",
					MessageType: &v1beta.ServiceTargetMessage_PreviewRequest{
						PreviewRequest: s.previewRequest,
					},
				}); err != nil {
					return err
				}
			}
			continue
		}
		s.previewResponses <- message
	}
}

func startBetaServiceTargetTestManager(
	t *testing.T,
	request *v1beta.ServiceTargetPreviewRequest,
) (*BetaServiceTargetManager, *fakeBetaServiceTargetServer, context.Context) {
	t.Helper()
	server := &fakeBetaServiceTargetServer{
		registrations:    make(chan *v1beta.RegisterServiceTargetRequest, 1),
		previewRequest:   request,
		previewResponses: make(chan *v1beta.ServiceTargetMessage, 1),
	}
	listener := bufconn.Listen(1024 * 1024)
	grpcServer := grpc.NewServer()
	v1beta.RegisterServiceTargetServiceServer(grpcServer, server)
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
	manager := NewBetaServiceTargetManager("test.ext", &AzdClient{connection: connection}, nil)
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

func TestBetaServiceTargetManagerRegistersPreviewCapability(t *testing.T) {
	t.Parallel()
	manager, server, ctx := startBetaServiceTargetTestManager(t, nil)
	require.NoError(t, manager.Register(ctx, func() ServiceTargetProvider {
		return &mockServiceTargetPreviewProvider{}
	}, "custom"))
	registration := <-server.registrations
	assert.Equal(t, "custom", registration.Host)
	assert.True(t, registration.SupportsPreview)
	assert.True(t, manager.handler.componentManager.HasFactory("custom"))
}

func TestBetaServiceTargetManagerDispatchesPreview(t *testing.T) {
	t.Parallel()
	data, err := structpb.NewStruct(map[string]any{"change": "create"})
	require.NoError(t, err)
	betaConfig := &v1beta.ServiceConfig{Name: "web-service", Host: "custom"}
	manager, server, ctx := startBetaServiceTargetTestManager(t, &v1beta.ServiceTargetPreviewRequest{
		ServiceConfig: betaConfig,
	})
	provider := &mockServiceTargetPreviewProvider{}
	provider.On("Preview", ctx, mock.MatchedBy(func(config *ServiceConfig) bool {
		return config.Name == betaConfig.Name && config.Host == betaConfig.Host
	})).Return(&ServiceDeployPreviewResult{Message: "Deployment preview", Data: data}, nil).Once()
	require.NoError(t, manager.Register(ctx, func() ServiceTargetProvider { return provider }, "custom"))

	select {
	case response := <-server.previewResponses:
		require.NoError(t, NewBetaServiceTargetEnvelope().GetError(response))
		require.Equal(t, "Deployment preview", response.GetPreviewResponse().GetResult().GetMessage())
		require.Equal(t, data.AsMap(), response.GetPreviewResponse().GetResult().GetData().AsMap())
	case <-ctx.Done():
		t.Fatalf("preview response was not dispatched: %v", ctx.Err())
	}
	provider.AssertExpectations(t)
}
