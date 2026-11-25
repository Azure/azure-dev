// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azdext

import (
	"context"
	"errors"
	"net"
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
)

type mockInfraGenerationProvider struct {
	mock.Mock
}

func (m *mockInfraGenerationProvider) Generate(
	ctx context.Context, request *v1beta.InfraGenerateRequest,
) (*v1beta.InfraGenerateResponse, error) {
	args := m.Called(ctx, request)
	mockGenerateResult, _ := args.Get(0).(*v1beta.InfraGenerateResponse)
	return mockGenerateResult, args.Error(1)
}

type fakeInfraGenerationHost struct {
	v1beta.UnimplementedInfraGenerationServiceServer
	registrationResponse *v1beta.InfraGenerationMessage
	registrations        chan string

	// Stream puts its broker here when manager.Receive or manager.Ready opens the stream.
	// Tests use it to send generation requests from the host to the manager.
	brokers chan *grpcbroker.MessageBroker[v1beta.InfraGenerationMessage]
}

func newFakeInfraGenerationHost() *fakeInfraGenerationHost {
	return &fakeInfraGenerationHost{
		registrationResponse: &v1beta.InfraGenerationMessage{
			MessageType: &v1beta.InfraGenerationMessage_RegisterResponse{
				RegisterResponse: &v1beta.RegisterInfraGenerationProviderResponse{},
			},
		},
		registrations: make(chan string, 2),
		brokers:       make(chan *grpcbroker.MessageBroker[v1beta.InfraGenerationMessage], 1),
	}
}

func (h *fakeInfraGenerationHost) Stream(stream v1beta.InfraGenerationService_StreamServer) error {
	broker := grpcbroker.NewMessageBroker(stream, &InfraGenerationEnvelope{}, "host", nil)
	if err := broker.On(func(
		_ context.Context, request *v1beta.RegisterInfraGenerationProviderRequest,
	) (*v1beta.InfraGenerationMessage, error) {
		h.registrations <- request.Name
		return proto.Clone(h.registrationResponse).(*v1beta.InfraGenerationMessage), nil
	}); err != nil {
		return err
	}
	h.brokers <- broker
	return broker.Run(stream.Context())
}

func startFakeInfraGenerationHost(t *testing.T, host *fakeInfraGenerationHost) *infraGenerationManager {
	t.Helper()

	listener := bufconn.Listen(1024 * 1024)
	server := grpc.NewServer()
	v1beta.RegisterInfraGenerationServiceServer(server, host)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)

	connection, err := grpc.NewClient(
		"passthrough:///IGNOREDNOTUSED",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return listener.DialContext(ctx)
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, connection.Close()) })

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	manager := newInfraGenerationManager("test.ext", &AzdClient{connection: connection}, nil)
	// Keep listening in the background so the fake host can ask the manager to generate files.
	finished := make(chan error, 1)
	go func() { finished <- manager.Receive(ctx) }()

	t.Cleanup(func() {
		cancel()
		err := manager.Close()
		require.NoError(t, err)

		select {
		case err := <-finished:
			require.True(t, err == nil || errors.Is(err, context.Canceled), "receiver shutdown failed: %v", err)
		case <-time.After(5 * time.Second):
			t.Error("generation receiver did not stop")
		}
	})

	require.NoError(t, manager.Ready(ctx))
	return manager
}

func TestInfraGenerationManager_Stream(t *testing.T) {
	t.Parallel()

	host := newFakeInfraGenerationHost()
	manager := startFakeInfraGenerationHost(t, host)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	// Tell the host about the provider, but don't generate anything yet.
	provider := &mockInfraGenerationProvider{}
	require.NoError(t, manager.Register(ctx, "custom", provider))
	require.Equal(t, "custom", <-host.registrations)
	require.Empty(t, provider.Calls, "registration must not invoke generation")
	require.ErrorContains(t, manager.Register(ctx, "custom", provider), "already registered")

	request := &v1beta.InfraGenerateRequest{
		ProjectPath: t.TempDir(),
		OutputPath:  "infra/generated",
		Entry:       &v1beta.ProvisioningOptions{Provider: "custom"},
		Options:     &v1beta.InfraGenerateOptions{Force: true},
	}

	mockGenerateResult := &v1beta.InfraGenerateResponse{Entry: &v1beta.ProvisioningOptions{Provider: "bicep"}}
	// The provider should receive the host's request unchanged.
	provider.On("Generate", mock.Anything, mock.MatchedBy(func(got *v1beta.InfraGenerateRequest) bool {
		return proto.Equal(request, got)
	})).Return(mockGenerateResult, nil).Once()

	broker := <-host.brokers // triggers when the stream starts

	response, err := broker.SendAndWait(ctx, &v1beta.InfraGenerationMessage{
		RequestId: "generate-1",
		MessageType: &v1beta.InfraGenerationMessage_GenerateRequest{
			GenerateRequest: request,
		},
	})
	require.NoError(t, err)
	require.Equal(t, "generate-1", response.RequestId)
	require.True(t, proto.Equal(mockGenerateResult, response.GetGenerateResponse()))

	// Check that the host gets the same error code and message that the provider returned.
	provider.On("Generate", mock.Anything, mock.Anything).
		Return(nil, &ServiceError{Message: "generation failed", ErrorCode: "GenerationFailed"}).Once()

	_, err = broker.SendAndWait(ctx, &v1beta.InfraGenerationMessage{
		RequestId: "generate-2",
		MessageType: &v1beta.InfraGenerationMessage_GenerateRequest{
			GenerateRequest: request,
		},
	})

	serviceErr, ok := errors.AsType[*ServiceError](err)
	require.True(t, ok, "expected structured provider error, got %v", err)
	require.Equal(t, "GenerationFailed", serviceErr.ErrorCode)
	require.Equal(t, "generation failed", serviceErr.Message)

	provider.AssertExpectations(t)
}

func TestInfraGenerationManager_RoutesRequestsToRegisteredProviders(t *testing.T) {
	t.Parallel()
	host := newFakeInfraGenerationHost()
	manager := startFakeInfraGenerationHost(t, host)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	first := &mockInfraGenerationProvider{}
	second := &mockInfraGenerationProvider{}
	require.NoError(t, manager.Register(ctx, "first", first))
	require.NoError(t, manager.Register(ctx, "second", second))
	require.Equal(t, "first", <-host.registrations)
	require.Equal(t, "second", <-host.registrations)

	mockFirstGenerateResult := &v1beta.InfraGenerateResponse{Entry: &v1beta.ProvisioningOptions{Provider: "bicep"}}
	first.On("Generate", mock.Anything, mock.MatchedBy(func(req *v1beta.InfraGenerateRequest) bool {
		return req.GetEntry().GetProvider() == "first"
	})).Return(mockFirstGenerateResult, nil).Once()

	mockSecondGenerateResult := &v1beta.InfraGenerateResponse{Entry: &v1beta.ProvisioningOptions{Provider: "terraform"}}
	second.On("Generate", mock.Anything, mock.MatchedBy(func(req *v1beta.InfraGenerateRequest) bool {
		return req.GetEntry().GetProvider() == "second"
	})).Return(mockSecondGenerateResult, nil).Once()

	broker := <-host.brokers // triggers when the stream starts

	// Ask for the second provider first to check that the name, not registration order, decides who handles it.
	response, err := broker.SendAndWait(ctx, &v1beta.InfraGenerationMessage{
		RequestId: "second",
		MessageType: &v1beta.InfraGenerationMessage_GenerateRequest{
			GenerateRequest: &v1beta.InfraGenerateRequest{
				Entry: &v1beta.ProvisioningOptions{Provider: "second"}, Options: &v1beta.InfraGenerateOptions{},
			},
		},
	})
	require.NoError(t, err)
	require.Equal(t, "terraform", response.GetGenerateResponse().GetEntry().GetProvider())

	response, err = broker.SendAndWait(ctx, &v1beta.InfraGenerationMessage{
		RequestId: "first",
		MessageType: &v1beta.InfraGenerationMessage_GenerateRequest{
			GenerateRequest: &v1beta.InfraGenerateRequest{
				Entry: &v1beta.ProvisioningOptions{Provider: "first"}, Options: &v1beta.InfraGenerateOptions{},
			},
		},
	})
	require.NoError(t, err)
	require.Equal(t, "bicep", response.GetGenerateResponse().GetEntry().GetProvider())

	first.AssertExpectations(t)
	second.AssertExpectations(t)
}

func TestInfraGenerationManager_CancelsInFlightGeneration(t *testing.T) {
	t.Parallel()
	host := newFakeInfraGenerationHost()
	manager := startFakeInfraGenerationHost(t, host)
	ctx, stop := context.WithTimeout(t.Context(), 5*time.Second)
	defer stop()

	// Make the provider wait until it is told to cancel.
	provider := &mockInfraGenerationProvider{}
	started := make(chan struct{})
	providerCanceled := make(chan error, 1)
	provider.On("Generate", mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
		providerCtx := args.Get(0).(context.Context)
		close(started)
		<-providerCtx.Done()
		providerCanceled <- providerCtx.Err()
	}).Return(nil, context.Canceled).Once()
	require.NoError(t, manager.Register(ctx, "custom", provider))
	broker := <-host.brokers // triggers when the stream starts

	requestCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	finished := make(chan error, 1)
	go func() {
		_, err := broker.SendAndWait(requestCtx, &v1beta.InfraGenerationMessage{
			RequestId: "cancel-generation",
			MessageType: &v1beta.InfraGenerationMessage_GenerateRequest{
				GenerateRequest: &v1beta.InfraGenerateRequest{
					Entry: &v1beta.ProvisioningOptions{Provider: "custom"}, Options: &v1beta.InfraGenerateOptions{},
				},
			},
		})
		finished <- err
	}()

	// Wait for the provider to start so we cancel work that's already running.
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal("provider did not start")
	}

	cancel()

	select {
	case err := <-providerCanceled:
		require.ErrorIs(t, err, context.Canceled)
	case <-ctx.Done():
		t.Fatal("provider did not receive cancellation")
	}

	// Check that the provider confirmed cancellation, rather than the host just giving up waiting.
	select {
	case err := <-finished:
		require.ErrorIs(t, err, context.Canceled)
		require.False(t, grpcbroker.IsCancellationUnacknowledged(err))
	case <-ctx.Done():
		t.Fatal("generation request did not finish")
	}

	// We should still be able to generate files after canceling the previous request.
	mockGenerateResult := &v1beta.InfraGenerateResponse{Entry: &v1beta.ProvisioningOptions{Provider: "bicep"}}
	provider.On("Generate", mock.Anything, mock.Anything).
		Return(mockGenerateResult, nil).Once()
	response, err := broker.SendAndWait(ctx, &v1beta.InfraGenerationMessage{
		RequestId: "after-cancellation",
		MessageType: &v1beta.InfraGenerationMessage_GenerateRequest{
			GenerateRequest: &v1beta.InfraGenerateRequest{
				Entry: &v1beta.ProvisioningOptions{Provider: "custom"}, Options: &v1beta.InfraGenerateOptions{},
			},
		},
	})
	require.NoError(t, err)
	require.Equal(t, "bicep", response.GetGenerateResponse().GetEntry().GetProvider())
	provider.AssertExpectations(t)
}

func TestInfraGenerationManager_RegistrationFailureRollsBack(t *testing.T) {
	t.Parallel()

	t.Run("rejected", func(t *testing.T) {
		t.Parallel()
		host := newFakeInfraGenerationHost()
		host.registrationResponse = &v1beta.InfraGenerationMessage{
			Error: &v1beta.ExtensionError{Message: "registration rejected"},
		}
		manager := startFakeInfraGenerationHost(t, host)
		ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
		defer cancel()

		err := manager.Register(ctx, "custom", &mockInfraGenerationProvider{})
		require.ErrorContains(t, err, "registration rejected")

		// Registration failed, so the manager shouldn't keep the provider.
		_, registered := manager.providers.Lookup("custom")
		require.False(t, registered)
	})

	t.Run("unexpected response", func(t *testing.T) {
		t.Parallel()
		host := newFakeInfraGenerationHost()
		host.registrationResponse = &v1beta.InfraGenerationMessage{}
		manager := startFakeInfraGenerationHost(t, host)
		ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
		defer cancel()

		err := manager.Register(ctx, "custom", &mockInfraGenerationProvider{})
		require.ErrorContains(t, err, "expected infrastructure generation registration response")

		// Registration failed, so the manager shouldn't keep the provider.
		_, registered := manager.providers.Lookup("custom")
		require.False(t, registered)
	})
}

func TestInfraGenerationManager_RejectsInvalidRequests(t *testing.T) {
	t.Parallel()

	t.Run("nil request", func(t *testing.T) {
		t.Parallel()
		manager := newInfraGenerationManager("test.ext", nil, nil)

		// Call generate directly because we're checking bad requests, not how messages reach it.
		response, err := manager.generate(t.Context(), nil)
		require.EqualError(t, err, "generation requires an entry and a non-nil options message")
		require.Nil(t, response)
	})

	t.Run("missing entry", func(t *testing.T) {
		t.Parallel()
		manager := newInfraGenerationManager("test.ext", nil, nil)
		request := &v1beta.InfraGenerateRequest{Options: &v1beta.InfraGenerateOptions{}}

		// Call generate directly because we're checking bad requests, not how messages reach it.
		response, err := manager.generate(t.Context(), request)
		require.EqualError(t, err, "generation requires an entry and a non-nil options message")
		require.Nil(t, response)
	})

	t.Run("missing options", func(t *testing.T) {
		t.Parallel()
		manager := newInfraGenerationManager("test.ext", nil, nil)
		request := &v1beta.InfraGenerateRequest{Entry: &v1beta.ProvisioningOptions{Provider: "custom"}}

		// Call generate directly because we're checking bad requests, not how messages reach it.
		response, err := manager.generate(t.Context(), request)
		require.EqualError(t, err, "generation requires an entry and a non-nil options message")
		require.Nil(t, response)
	})

	t.Run("unregistered provider", func(t *testing.T) {
		t.Parallel()
		manager := newInfraGenerationManager("test.ext", nil, nil)
		request := &v1beta.InfraGenerateRequest{
			Entry: &v1beta.ProvisioningOptions{Provider: "missing"}, Options: &v1beta.InfraGenerateOptions{},
		}

		// Call generate directly because we're checking bad requests, not how messages reach it.
		response, err := manager.generate(t.Context(), request)
		require.EqualError(t, err, `infrastructure generation provider "missing" is not registered`)
		require.Nil(t, response)
	})
}

func TestInfraGenerationManager_RejectsInvalidResults(t *testing.T) {
	t.Parallel()

	t.Run("nil response", func(t *testing.T) {
		t.Parallel()
		provider := &mockInfraGenerationProvider{}
		var mockGenerateResult *v1beta.InfraGenerateResponse
		provider.On("Generate", mock.Anything, mock.Anything).Return(mockGenerateResult, nil).Once()
		manager := newInfraGenerationManager("test.ext", nil, nil)

		// Add the mock directly because we're checking bad results and don't need a stream.
		err := manager.providers.Register(t.Context(), "custom", provider,
			func(context.Context, string) error { return nil })
		require.NoError(t, err)

		// Call generate directly to check how it handles bad provider results without sending messages.
		response, err := manager.generate(t.Context(), &v1beta.InfraGenerateRequest{
			Entry: &v1beta.ProvisioningOptions{Provider: "custom"}, Options: &v1beta.InfraGenerateOptions{},
		})
		require.EqualError(t, err, "infrastructure generator returned no replacement provider entry")
		require.Nil(t, response)

		provider.AssertExpectations(t)
	})

	t.Run("missing entry", func(t *testing.T) {
		t.Parallel()
		provider := &mockInfraGenerationProvider{}
		mockGenerateResult := &v1beta.InfraGenerateResponse{}
		provider.On("Generate", mock.Anything, mock.Anything).Return(mockGenerateResult, nil).Once()
		manager := newInfraGenerationManager("test.ext", nil, nil)

		// Add the mock directly because we're checking bad results and don't need a stream.
		err := manager.providers.Register(t.Context(), "custom", provider,
			func(context.Context, string) error { return nil })
		require.NoError(t, err)

		// Call generate directly to check how it handles bad provider results without sending messages.
		response, err := manager.generate(t.Context(), &v1beta.InfraGenerateRequest{
			Entry: &v1beta.ProvisioningOptions{Provider: "custom"}, Options: &v1beta.InfraGenerateOptions{},
		})
		require.EqualError(t, err, "infrastructure generator returned no replacement provider entry")
		require.Nil(t, response)

		provider.AssertExpectations(t)
	})

	t.Run("blank provider", func(t *testing.T) {
		t.Parallel()
		provider := &mockInfraGenerationProvider{}
		mockGenerateResult := &v1beta.InfraGenerateResponse{
			Entry: &v1beta.ProvisioningOptions{Provider: " \t"},
		}
		provider.On("Generate", mock.Anything, mock.Anything).Return(mockGenerateResult, nil).Once()
		manager := newInfraGenerationManager("test.ext", nil, nil)

		// Add the mock directly because we're checking bad results and don't need a stream.
		err := manager.providers.Register(t.Context(), "custom", provider,
			func(context.Context, string) error { return nil })
		require.NoError(t, err)

		// Call generate directly to check how it handles bad provider results without sending messages.
		response, err := manager.generate(t.Context(), &v1beta.InfraGenerateRequest{
			Entry: &v1beta.ProvisioningOptions{Provider: "custom"}, Options: &v1beta.InfraGenerateOptions{},
		})
		require.EqualError(t, err, "infrastructure generator returned no replacement provider entry")
		require.Nil(t, response)

		provider.AssertExpectations(t)
	})
}

func TestInfraGenerationManager_RejectsInvalidRegistrationBeforeOpeningStream(t *testing.T) {
	t.Parallel()

	// Leave the client nil so the test fails if Register tries to open a stream before checking its inputs.
	var nilClient *AzdClient = nil
	manager := newInfraGenerationManager("test.ext", nilClient, nil)

	require.ErrorContains(t, manager.Register(t.Context(), " \t", &mockInfraGenerationProvider{}),
		"requires a provider name and implementation")
	require.ErrorContains(t, manager.Register(t.Context(), "custom", nil),
		"requires a provider name and implementation")
}
