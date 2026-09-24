// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azdext

import (
	"context"
	"errors"
	"net"
	"sync/atomic"
	"testing"
	"time"

	v1beta "github.com/azure/azure-dev/cli/azd/pkg/azdext/contracts/v1beta"
	"github.com/azure/azure-dev/cli/azd/pkg/grpcbroker"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/structpb"
)

type mockServiceTargetPreviewProvider struct {
	MockServiceTargetProvider
}

func (m *mockServiceTargetPreviewProvider) Preview(
	ctx context.Context,
	serviceConfig *v1beta.ServiceConfig,
) (*v1beta.ServiceDeployPreviewResult, error) {
	args := m.Called(ctx, serviceConfig)
	result, _ := args.Get(0).(*v1beta.ServiceDeployPreviewResult)
	return result, args.Error(1)
}

func TestServiceTargetPreviewManager_OnPreview(t *testing.T) {
	t.Parallel()

	result := &v1beta.ServiceDeployPreviewResult{Message: "1 change"}
	tests := []struct {
		name          string
		request       *v1beta.ServiceTargetPreviewRequest
		provider      func() ServiceTargetProvider
		result        *v1beta.ServiceDeployPreviewResult
		providerError error
		wantError     string
	}{
		{name: "Success", result: result},
		{name: "ProviderError", providerError: errors.New("remote lookup failed"), wantError: "remote lookup failed"},
		{name: "NilResult", wantError: "service target 'custom' returned a nil deployment preview result"},
		{
			name:      "Unsupported",
			provider:  func() ServiceTargetProvider { return &MockServiceTargetProvider{} },
			wantError: "service target 'custom' does not support deployment preview",
		},
		{
			name:      "NoFactory",
			request:   &v1beta.ServiceTargetPreviewRequest{ServiceConfig: &v1beta.ServiceConfig{Host: "missing"}},
			wantError: "no preview factory registered for service target: missing",
		},
		{
			name:      "NilServiceConfig",
			request:   &v1beta.ServiceTargetPreviewRequest{},
			wantError: "service config is required for preview request",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			serviceConfig := &v1beta.ServiceConfig{Name: "api", Host: "custom"}
			request := tt.request
			if request == nil {
				request = &v1beta.ServiceTargetPreviewRequest{ServiceConfig: serviceConfig}
			}

			var providers []*mockServiceTargetPreviewProvider
			factory := tt.provider
			if factory == nil {
				factory = func() ServiceTargetProvider {
					provider := &mockServiceTargetPreviewProvider{}
					provider.On("Preview", mock.Anything, serviceConfig).Return(tt.result, tt.providerError)
					providers = append(providers, provider)
					return provider
				}
			}

			manager := newServiceTargetPreviewManager("test.ext", nil, nil)
			manager.factories["custom"] = factory

			for range 2 {
				response, err := manager.onPreview(t.Context(), request)
				if tt.wantError != "" {
					require.EqualError(t, err, tt.wantError)
					require.Nil(t, response)
					continue
				}

				require.NoError(t, err)
				require.Same(t, tt.result, response.GetPreviewResponse().GetResult())
			}

			// Each preview must use a fresh provider and never touch the deployment lifecycle.
			if tt.provider == nil && tt.request == nil {
				require.Len(t, providers, 2)
				require.NotSame(t, providers[0], providers[1])
			}
			for _, provider := range providers {
				require.Len(t, provider.Calls, 1)
				provider.AssertExpectations(t)
			}
		})
	}
}

// fakePreviewHost plays the azd side of the v1beta deployment preview stream.
type fakePreviewHost struct {
	v1beta.UnimplementedServiceTargetServiceServer
	registrations chan *v1beta.RegisterServiceTargetRequest
	brokers       chan *grpcbroker.MessageBroker[v1beta.ServiceTargetMessage]
}

func (h *fakePreviewHost) Stream(stream v1beta.ServiceTargetService_StreamServer) error {
	broker := grpcbroker.NewMessageBroker(stream, NewServiceTargetPreviewEnvelope(), "host", nil)
	if err := broker.On(func(
		ctx context.Context,
		req *v1beta.RegisterServiceTargetRequest,
	) (*v1beta.ServiceTargetMessage, error) {
		h.registrations <- req
		return &v1beta.ServiceTargetMessage{
			MessageType: &v1beta.ServiceTargetMessage_RegisterServiceTargetResponse{
				RegisterServiceTargetResponse: &v1beta.RegisterServiceTargetResponse{},
			},
		}, nil
	}); err != nil {
		return err
	}

	h.brokers <- broker
	return broker.Run(stream.Context())
}

func TestServiceTargetPreviewManager_Stream(t *testing.T) {
	t.Parallel()

	host := &fakePreviewHost{
		registrations: make(chan *v1beta.RegisterServiceTargetRequest, 1),
		brokers:       make(chan *grpcbroker.MessageBroker[v1beta.ServiceTargetMessage], 1),
	}
	listener := bufconn.Listen(1024 * 1024)
	grpcServer := grpc.NewServer()
	v1beta.RegisterServiceTargetServiceServer(grpcServer, host)
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
	t.Cleanup(func() { require.NoError(t, connection.Close()) })

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	manager := newServiceTargetPreviewManager("test.ext", &AzdClient{connection: connection}, nil)
	t.Cleanup(func() { require.NoError(t, manager.Close()) })
	go func() { _ = manager.Receive(ctx) }()
	require.NoError(t, manager.Ready(ctx))

	data, err := structpb.NewStruct(map[string]any{"action": "create"})
	require.NoError(t, err)
	result := &v1beta.ServiceDeployPreviewResult{Message: "would create", Data: data}
	var factoryCalls atomic.Int32
	require.NoError(t, manager.Register(ctx, func() ServiceTargetProvider {
		factoryCalls.Add(1)
		provider := &mockServiceTargetPreviewProvider{}
		provider.On("Preview", mock.Anything, mock.Anything).Return(result, nil)
		return provider
	}, "custom"))

	registration := <-host.registrations
	require.Equal(t, "custom", registration.GetHost())
	require.True(t, registration.GetSupportsPreview())
	require.Zero(t, factoryCalls.Load(), "registration must not construct providers")

	broker := <-host.brokers
	response, err := broker.SendAndWait(ctx, &v1beta.ServiceTargetMessage{
		RequestId: "preview-1",
		MessageType: &v1beta.ServiceTargetMessage_PreviewRequest{
			PreviewRequest: &v1beta.ServiceTargetPreviewRequest{
				ServiceConfig: &v1beta.ServiceConfig{Name: "api", Host: "custom"},
			},
		},
	})
	require.NoError(t, err)
	require.True(t, proto.Equal(result, response.GetPreviewResponse().GetResult()))
	require.EqualValues(t, 1, factoryCalls.Load())
}

func TestServiceTargetPreviewEnvelope_Error(t *testing.T) {
	t.Parallel()

	envelope := NewServiceTargetPreviewEnvelope()
	message := &v1beta.ServiceTargetMessage{}
	require.NoError(t, envelope.GetError(message))

	envelope.SetError(message, &ServiceError{Message: "preview failed", ErrorCode: "PreviewFailed"})
	serviceError, ok := errors.AsType[*ServiceError](envelope.GetError(message))
	require.True(t, ok)
	require.Equal(t, "PreviewFailed", serviceError.ErrorCode)
	require.Equal(t, "preview failed", serviceError.Message)

	envelope.SetError(message, nil)
	require.Nil(t, message.Error)
}

func TestExtensionHost_WithBetaServiceTargetPreview(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		previewErr error
	}{
		{name: "Registered"},
		// Hosts without the preview override reject the registration; the extension must still start.
		{name: "PreviewUnavailable", previewErr: errors.New("provider demo already registered")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			registered := make(chan string, 2)
			newRegistrar := func(channel string, err error) *MockServiceTargetRegistrar {
				registrar := &MockServiceTargetRegistrar{}
				registrar.On("Register", mock.Anything, mock.Anything, "demo").
					Run(func(args mock.Arguments) { registered <- channel }).
					Return(err)
				registrar.On("Ready", mock.Anything).Return(nil)
				registrar.On("Receive", mock.Anything).Run(func(args mock.Arguments) {
					<-args.Get(0).(context.Context).Done()
				}).Return(nil)
				registrar.On("Close").Return(nil)
				return registrar
			}

			host := NewExtensionHost(newTestAzdClient())
			host.serviceTargetManager = newRegistrar("stable", nil)
			host.serviceTargetPreviewManager = newRegistrar("preview", tt.previewErr)
			host.WithBetaServiceTargetPreview("demo", func() ServiceTargetProvider {
				return &mockServiceTargetPreviewProvider{}
			})
			require.Len(t, host.ServiceTargets(), 1)

			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			done := make(chan error, 1)
			go func() { done <- host.Run(ctx) }()

			// The preview registration must follow the stable registration of the same host.
			require.Equal(t, "stable", <-registered)
			require.Equal(t, "preview", <-registered)
			cancel()
			require.NoError(t, <-done)
		})
	}
}
