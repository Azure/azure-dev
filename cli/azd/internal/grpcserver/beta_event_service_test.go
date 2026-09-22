// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package grpcserver

import (
	"context"
	"errors"
	"io"
	"net"
	"testing"
	"time"

	"github.com/azure/azure-dev/cli/azd/internal/guidance"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	v1beta "github.com/azure/azure-dev/cli/azd/pkg/azdext/contracts/v1beta"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/ext"
	"github.com/azure/azure-dev/cli/azd/pkg/extensions"
	"github.com/azure/azure-dev/cli/azd/pkg/grpcbroker"
	"github.com/azure/azure-dev/cli/azd/pkg/lazy"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

type testExtensionLookup struct {
	extension *extensions.Extension
}

func (l testExtensionLookup) GetInstalled(
	extensions.FilterOptions,
) (*extensions.Extension, error) {
	return l.extension, nil
}

func TestBetaEventServiceRegistersBrokerHandlers(t *testing.T) {
	service, _ := createTestEventService()
	service.extensionManager = testExtensionLookup{
		extension: &extensions.Extension{
			Id: "test.extension",
			Capabilities: []extensions.CapabilityType{
				extensions.LifecycleEventsCapability,
			},
		},
	}
	betaService := &betaEventService{service: service}
	ctx := extensions.WithClaimsContext(t.Context(), &extensions.ExtensionClaims{
		RegisteredClaims: jwt.RegisteredClaims{Subject: "test.extension"},
	})
	stream := &MockBidiStreamingServer[*v1beta.EventMessage, *v1beta.EventMessage]{
		ctx: ctx,
	}
	stream.On("Recv").Return(&v1beta.EventMessage{}, io.EOF).Once()

	require.NoError(t, betaService.EventStream(stream))
	stream.AssertExpectations(t)
}

type scriptedBetaEventStream struct {
	ctx    context.Context
	recvCh chan *v1beta.EventMessage
	sendFn func(*v1beta.EventMessage) error
}

func (s *scriptedBetaEventStream) Send(msg *v1beta.EventMessage) error {
	return s.sendFn(msg)
}

func (s *scriptedBetaEventStream) Recv() (*v1beta.EventMessage, error) {
	msg, ok := <-s.recvCh
	if !ok {
		return nil, io.EOF
	}
	return msg, nil
}

func (s *scriptedBetaEventStream) SetHeader(metadata.MD) error {
	return nil
}

func (s *scriptedBetaEventStream) SendHeader(metadata.MD) error {
	return nil
}

func (s *scriptedBetaEventStream) SetTrailer(metadata.MD) {}

func (s *scriptedBetaEventStream) Context() context.Context {
	return s.ctx
}

func (s *scriptedBetaEventStream) SendMsg(any) error {
	return nil
}

func (s *scriptedBetaEventStream) RecvMsg(any) error {
	return nil
}

func TestBetaEventServiceSubscriptionAcknowledgement(t *testing.T) {
	tests := []struct {
		name         string
		request      *v1beta.EventMessage
		wantResponse func(*testing.T, *v1beta.EventMessage)
	}{
		{
			name: "success",
			request: &v1beta.EventMessage{
				RequestId: "request-success",
				MessageType: &v1beta.EventMessage_SubscribeProjectEvent{
					SubscribeProjectEvent: &v1beta.SubscribeProjectEvent{
						EventNames: []string{"postdeploy"},
					},
				},
			},
			wantResponse: func(t *testing.T, response *v1beta.EventMessage) {
				require.Equal(t, "request-success", response.RequestId)
				require.NotNil(t, response.GetSubscribeProjectEventResponse())
				require.Nil(t, response.GetError())
			},
		},
		{
			name: "registration error",
			request: &v1beta.EventMessage{
				RequestId: "request-error",
				MessageType: &v1beta.EventMessage_SubscribeProjectEvent{
					SubscribeProjectEvent: &v1beta.SubscribeProjectEvent{
						EventNames: []string{""},
					},
				},
			},
			wantResponse: func(t *testing.T, response *v1beta.EventMessage) {
				require.Equal(t, "request-error", response.RequestId)
				require.Nil(t, response.GetSubscribeProjectEventResponse())
				require.Contains(t,
					response.GetError().GetMessage(),
					"event name at index 0 cannot be empty")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service, _ := createTestEventService()
			extension := createTestExtension()
			extension.Capabilities = []extensions.CapabilityType{
				extensions.LifecycleEventsCapability,
			}
			service.extensionManager = testExtensionLookup{extension: extension}
			streamCtx := extensionClaimsContext(t.Context(), extension.Id)
			sent := make(chan *v1beta.EventMessage, 1)
			stream := &scriptedBetaEventStream{
				ctx:    streamCtx,
				recvCh: make(chan *v1beta.EventMessage, 1),
				sendFn: func(msg *v1beta.EventMessage) error {
					sent <- msg
					return nil
				},
			}
			done := make(chan error, 1)
			go func() {
				done <- (&betaEventService{service: service}).EventStream(stream)
			}()

			stream.recvCh <- tt.request
			tt.wantResponse(t, <-sent)
			close(stream.recvCh)
			require.NoError(t, <-done)
		})
	}
}

func TestBetaEventServiceProjectHandlerCommitsFollowUp(t *testing.T) {
	service, _ := createTestEventService()
	extension := createTestExtension()
	streamCtx := extensions.WithClaimsContext(t.Context(), &extensions.ExtensionClaims{
		RegisteredClaims: jwt.RegisteredClaims{Subject: extension.Id},
	})
	stream := &scriptedBetaEventStream{
		ctx:    streamCtx,
		recvCh: make(chan *v1beta.EventMessage, 1),
	}
	followUpErr := make(chan error, 1)
	stream.sendFn = func(msg *v1beta.EventMessage) error {
		invoke := msg.GetInvokeProjectHandler()
		if invoke == nil {
			followUpErr <- errors.New("expected project invocation")
			return nil
		}
		_, err := NewFollowUpService(service.followUps).SetFollowUp(
			streamCtx,
			&v1beta.SetFollowUpRequest{
				InvocationId: invoke.InvocationId,
				Text:         "Run azd show",
			},
		)
		followUpErr <- err
		stream.recvCh <- &v1beta.EventMessage{
			MessageType: &v1beta.EventMessage_ProjectHandlerStatus{
				ProjectHandlerStatus: &v1beta.ProjectHandlerStatus{
					EventName: invoke.EventName,
					Status:    "completed",
				},
			},
		}
		return nil
	}

	brokerCtx, cancel := context.WithCancel(streamCtx)
	broker := grpcbroker.NewMessageBroker(
		stream,
		azdext.NewBetaEventMessageEnvelope(),
		extension.Id,
		nil,
	)
	go func() {
		_ = broker.Run(brokerCtx)
	}()
	require.NoError(t, broker.Ready(t.Context()))
	t.Cleanup(func() {
		close(stream.recvCh)
		cancel()
	})

	projectConfig, err := service.lazyProject.GetValue()
	require.NoError(t, err)
	handler := (&betaEventService{service: service}).createProjectHandler(
		streamCtx,
		extension,
		"postdeploy",
		broker,
	)
	collector := guidance.NewFollowUpCollector()
	handlerCtx := guidance.WithFollowUpCollector(t.Context(), collector)

	err = handler(handlerCtx, project.ProjectLifecycleEventArgs{
		Project: projectConfig,
		Args:    map[string]any{"layer": "app"},
	})
	require.NoError(t, err)
	require.NoError(t, <-followUpErr)
	require.Equal(t, "Run azd show", collector.Text())
}

type failingReloadEnvironmentManager struct {
	noOpEnvironmentManager
	failAt int
	calls  int
}

func (m *failingReloadEnvironmentManager) Reload(
	context.Context,
	*environment.Environment,
) error {
	m.calls++
	if m.calls == m.failAt {
		return errors.New("reload failed")
	}
	return nil
}

func TestBetaEventServiceProjectHandlerDiscardsFollowUp(t *testing.T) {
	tests := []struct {
		name         string
		status       string
		sendErr      error
		reloadFailAt int
		wantErr      bool
	}{
		{name: "failed status", status: "failed", wantErr: true},
		{name: "incomplete status", status: "running"},
		{name: "disconnect", sendErr: io.ErrClosedPipe, wantErr: true},
		{name: "cancellation", sendErr: context.Canceled, wantErr: true},
		{name: "reload failure", status: "completed", reloadFailAt: 2, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service, _ := createTestEventService()
			if tt.reloadFailAt > 0 {
				manager := &failingReloadEnvironmentManager{failAt: tt.reloadFailAt}
				service.lazyEnvManager = lazy.NewLazy(func() (environment.Manager, error) {
					return manager, nil
				})
			}

			extension := createTestExtension()
			streamCtx := extensionClaimsContext(t.Context(), extension.Id)
			stream := &scriptedBetaEventStream{
				ctx:    streamCtx,
				recvCh: make(chan *v1beta.EventMessage, 1),
			}
			var invocationID string
			var setErr error
			stream.sendFn = func(msg *v1beta.EventMessage) error {
				invoke := msg.GetInvokeProjectHandler()
				require.NotNil(t, invoke)
				invocationID = invoke.InvocationId
				_, setErr = NewFollowUpService(service.followUps).SetFollowUp(
					streamCtx,
					&v1beta.SetFollowUpRequest{
						InvocationId: invocationID,
						Text:         "discard me",
					},
				)
				if tt.sendErr != nil {
					return tt.sendErr
				}
				stream.recvCh <- &v1beta.EventMessage{
					MessageType: &v1beta.EventMessage_ProjectHandlerStatus{
						ProjectHandlerStatus: &v1beta.ProjectHandlerStatus{
							EventName: invoke.EventName,
							Status:    tt.status,
							Message:   "handler failed",
						},
					},
				}
				return nil
			}

			brokerCtx, cancel := context.WithCancel(streamCtx)
			broker := grpcbroker.NewMessageBroker(
				stream,
				azdext.NewBetaEventMessageEnvelope(),
				extension.Id,
				nil,
			)
			go func() {
				_ = broker.Run(brokerCtx)
			}()
			require.NoError(t, broker.Ready(t.Context()))
			t.Cleanup(func() {
				close(stream.recvCh)
				cancel()
			})

			projectConfig, err := service.lazyProject.GetValue()
			require.NoError(t, err)
			handler := (&betaEventService{service: service}).createProjectHandler(
				streamCtx,
				extension,
				"postdeploy",
				broker,
			)
			collector := guidance.NewFollowUpCollector()
			handlerCtx := guidance.WithFollowUpCollector(t.Context(), collector)

			err = handler(handlerCtx, project.ProjectLifecycleEventArgs{
				Project: projectConfig,
			})
			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
			require.NoError(t, setErr)
			require.NotEmpty(t, invocationID)
			require.Empty(t, collector.Text())

			_, err = NewFollowUpService(service.followUps).SetFollowUp(
				streamCtx,
				&v1beta.SetFollowUpRequest{
					InvocationId: invocationID,
					Text:         "too late",
				},
			)
			require.Equal(t, codes.FailedPrecondition, status.Code(err))
		})
	}
}

type betaClaimsServerStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (s *betaClaimsServerStream) Context() context.Context {
	return s.ctx
}

type readyExtensionService struct {
	azdext.UnimplementedExtensionServiceServer
	ready chan struct{}
}

func (s *readyExtensionService) Ready(
	context.Context,
	*azdext.ReadyRequest,
) (*azdext.ReadyResponse, error) {
	close(s.ready)
	return &azdext.ReadyResponse{}, nil
}

func TestBetaEventServicePreviewSDKFollowUpEndToEnd(t *testing.T) {
	service, _ := createTestEventService()
	extension := createTestExtension()
	extension.Capabilities = []extensions.CapabilityType{
		extensions.LifecycleEventsCapability,
	}
	service.extensionManager = testExtensionLookup{extension: extension}

	withClaims := func(ctx context.Context) context.Context {
		return extensionClaimsContext(ctx, extension.Id)
	}
	server := grpc.NewServer(
		grpc.UnaryInterceptor(func(
			ctx context.Context,
			req any,
			info *grpc.UnaryServerInfo,
			handler grpc.UnaryHandler,
		) (any, error) {
			return handler(withClaims(ctx), req)
		}),
		grpc.StreamInterceptor(func(
			srv any,
			stream grpc.ServerStream,
			info *grpc.StreamServerInfo,
			handler grpc.StreamHandler,
		) error {
			return handler(srv, &betaClaimsServerStream{
				ServerStream: stream,
				ctx:          withClaims(stream.Context()),
			})
		}),
	)
	implementations := stableServiceImplementations()
	implementations[BetaEventService] = service
	implementations[BetaFollowUpService] = NewFollowUpService(service.followUps)
	require.NoError(t, registerBetaServices(
		server,
		implementations,
		map[BetaService]any{
			BetaEventService: &betaEventService{service: service},
		},
	))
	readyService := &readyExtensionService{ready: make(chan struct{})}
	azdext.RegisterExtensionServiceServer(server, readyService)

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	go func() {
		_ = server.Serve(listener)
	}()
	t.Cleanup(func() {
		server.Stop()
		_ = listener.Close()
	})

	client, err := azdext.NewAzdClient(
		azdext.WithAddress(listener.Addr().String()),
	)
	require.NoError(t, err)
	t.Cleanup(func() {
		client.Close()
	})

	t.Setenv("AZD_ACCESS_TOKEN", "test-token")
	host := azdext.NewExtensionHost(client)
	host.WithPreviewProjectEventHandler(
		"postdeploy",
		func(_ context.Context, args *azdext.PreviewProjectEventArgs) error {
			if args.FollowUp == nil {
				return errors.New("follow-up contribution is unavailable")
			}
			return args.FollowUp.Set("Run azd show")
		},
	)

	hostCtx, cancel := context.WithCancel(
		azdext.WithAccessToken(t.Context(), "test-token"),
	)
	hostDone := make(chan error, 1)
	go func() {
		hostDone <- host.Run(hostCtx)
	}()
	<-readyService.ready

	projectConfig, err := service.lazyProject.GetValue()
	require.NoError(t, err)
	collector := guidance.NewFollowUpCollector()
	eventCtx := guidance.WithFollowUpCollector(t.Context(), collector)
	require.NoError(t, projectConfig.RaiseEvent(
		eventCtx,
		ext.Event("postdeploy"),
		project.ProjectLifecycleEventArgs{Project: projectConfig},
	))
	require.Equal(t, "Run azd show", collector.Text())

	cancel()
	require.NoError(t, <-hostDone)
}

func TestBetaEventServiceServiceHandlerUsesBetaMessages(t *testing.T) {
	service, _ := createTestEventService()
	extension := createTestExtension()
	projectConfig, err := service.lazyProject.GetValue()
	require.NoError(t, err)
	serviceConfig := projectConfig.Services["api"]
	streamCtx := extensions.WithClaimsContext(t.Context(), &extensions.ExtensionClaims{
		RegisteredClaims: jwt.RegisteredClaims{Subject: extension.Id},
	})
	stream := &scriptedBetaEventStream{
		ctx:    streamCtx,
		recvCh: make(chan *v1beta.EventMessage, 1),
	}
	sendErr := make(chan error, 1)
	stream.sendFn = func(msg *v1beta.EventMessage) error {
		invoke := msg.GetInvokeServiceHandler()
		if invoke == nil || invoke.Service == nil {
			sendErr <- errors.New("expected service invocation")
			return nil
		}
		if invoke.Service.Name != serviceConfig.Name {
			sendErr <- errors.New("service name was not preserved")
			return nil
		}
		sendErr <- nil
		stream.recvCh <- &v1beta.EventMessage{
			MessageType: &v1beta.EventMessage_ServiceHandlerStatus{
				ServiceHandlerStatus: &v1beta.ServiceHandlerStatus{
					EventName:   invoke.EventName,
					ServiceName: invoke.Service.Name,
					Status:      "completed",
				},
			},
		}
		return nil
	}

	brokerCtx, cancel := context.WithCancel(streamCtx)
	broker := grpcbroker.NewMessageBroker(
		stream,
		azdext.NewBetaEventMessageEnvelope(),
		extension.Id,
		nil,
	)
	go func() {
		_ = broker.Run(brokerCtx)
	}()
	require.NoError(t, broker.Ready(t.Context()))
	t.Cleanup(func() {
		close(stream.recvCh)
		cancel()
	})

	handler := (&betaEventService{service: service}).createServiceHandler(
		streamCtx,
		serviceConfig,
		extension,
		"prepackage",
		broker,
	)
	err = handler(t.Context(), project.ServiceLifecycleEventArgs{
		Project:        projectConfig,
		Service:        serviceConfig,
		ServiceContext: project.NewServiceContext(),
	})
	require.NoError(t, err)
	require.NoError(t, <-sendErr)
}

func TestBetaEventServiceProjectHandlerUsesInvocationCancellation(t *testing.T) {
	service, _ := createTestEventService()
	extension := createTestExtension()
	streamCtx := extensionClaimsContext(t.Context(), extension.Id)
	projectConfig, err := service.lazyProject.GetValue()
	require.NoError(t, err)

	requireBetaHandlerStopsOnCancel(t, streamCtx, extension.Id, func(
		ctx context.Context,
		broker *grpcbroker.MessageBroker[v1beta.EventMessage],
	) error {
		handler := (&betaEventService{service: service}).createProjectHandler(
			streamCtx,
			extension,
			"postdeploy",
			broker,
		)
		return handler(ctx, project.ProjectLifecycleEventArgs{Project: projectConfig})
	})
}

func TestBetaEventServiceServiceHandlerUsesInvocationCancellation(t *testing.T) {
	service, _ := createTestEventService()
	extension := createTestExtension()
	streamCtx := extensionClaimsContext(t.Context(), extension.Id)
	projectConfig, err := service.lazyProject.GetValue()
	require.NoError(t, err)
	serviceConfig := projectConfig.Services["api"]

	requireBetaHandlerStopsOnCancel(t, streamCtx, extension.Id, func(
		ctx context.Context,
		broker *grpcbroker.MessageBroker[v1beta.EventMessage],
	) error {
		handler := (&betaEventService{service: service}).createServiceHandler(
			streamCtx,
			serviceConfig,
			extension,
			"prepackage",
			broker,
		)
		return handler(ctx, project.ServiceLifecycleEventArgs{
			Project:        projectConfig,
			Service:        serviceConfig,
			ServiceContext: project.NewServiceContext(),
		})
	})
}

func requireBetaHandlerStopsOnCancel(
	t *testing.T,
	streamCtx context.Context,
	extensionID string,
	run func(context.Context, *grpcbroker.MessageBroker[v1beta.EventMessage]) error,
) {
	t.Helper()

	invoked := make(chan struct{})
	stream := &scriptedBetaEventStream{
		ctx:    streamCtx,
		recvCh: make(chan *v1beta.EventMessage),
		sendFn: func(*v1beta.EventMessage) error {
			close(invoked)
			return nil
		},
	}
	brokerCtx, stopBroker := context.WithCancel(streamCtx)
	broker := grpcbroker.NewMessageBroker(
		stream,
		azdext.NewBetaEventMessageEnvelope(),
		extensionID,
		nil,
	)
	go func() {
		_ = broker.Run(brokerCtx)
	}()
	require.NoError(t, broker.Ready(t.Context()))
	t.Cleanup(func() {
		close(stream.recvCh)
		stopBroker()
	})

	invocationCtx, cancelInvocation := context.WithCancel(t.Context())
	handlerDone := make(chan error, 1)
	go func() {
		handlerDone <- run(invocationCtx, broker)
	}()

	select {
	case <-invoked:
	case <-time.After(time.Second):
		t.Fatal("handler did not send its invocation")
	}
	cancelInvocation()

	select {
	case err := <-handlerDone:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(time.Second):
		t.Fatal("handler did not stop after its invocation was canceled")
	}
}

func TestUnwrapBetaErrorPreservesStructuredDetails(t *testing.T) {
	t.Parallel()

	err := unwrapBetaError(&v1beta.ExtensionError{
		Message:    "preview handler failed",
		Suggestion: "Try again",
		Origin:     v1beta.ErrorOrigin_ERROR_ORIGIN_LOCAL,
		Source: &v1beta.ExtensionError_LocalError{
			LocalError: &v1beta.LocalErrorDetail{
				Code:       "preview_failed",
				Category:   "user",
				CauseTypes: []string{"*example.Cause"},
			},
		},
	})

	localErr, ok := errors.AsType[*azdext.LocalError](err)
	require.True(t, ok)
	require.Equal(t, "preview_failed", localErr.Code)
	require.Equal(t, azdext.LocalErrorCategoryUser, localErr.Category)
	require.Equal(t, []string{"*example.Cause"}, localErr.CauseTypes)
	require.Equal(t, "Try again", localErr.Suggestion)
}
