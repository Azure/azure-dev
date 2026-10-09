// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package grpcserver

import (
	"context"
	"errors"
	"io"
	"net"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/azure/azure-dev/cli/azd/internal/commandresult"
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
		require.NotEmpty(t, msg.RequestId)
		require.NotEqual(t, invoke.InvocationId, msg.RequestId)
		_, err := NewCommandResultService(service.followUps).SetFollowUp(
			streamCtx,
			&v1beta.SetFollowUpRequest{
				InvocationId: invoke.InvocationId,
				Text:         "Run azd show",
			},
		)
		followUpErr <- err
		stream.recvCh <- &v1beta.EventMessage{
			RequestId: msg.RequestId,
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
		newBetaEventMessageEnvelope(),
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
	collector := commandresult.NewFollowUpCollector()
	handlerCtx := commandresult.WithFollowUpCollector(t.Context(), collector)

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
				_, setErr = NewCommandResultService(service.followUps).SetFollowUp(
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
					RequestId: msg.RequestId,
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
				newBetaEventMessageEnvelope(),
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
			collector := commandresult.NewFollowUpCollector()
			handlerCtx := commandresult.WithFollowUpCollector(t.Context(), collector)

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

			_, err = NewCommandResultService(service.followUps).SetFollowUp(
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

func TestBetaEventServiceBetaClientFollowUpAndMessagesEndToEnd(t *testing.T) {
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
	implementations[BetaCommandResultService] = NewCommandResultService(service.followUps)
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

	streamCtx, cancel := context.WithTimeout(
		azdext.WithAccessToken(t.Context(), "test-token"),
		10*time.Second,
	)
	defer cancel()
	stream, err := client.EventsBeta().EventStream(streamCtx)
	require.NoError(t, err)
	require.NoError(t, stream.Send(&v1beta.EventMessage{
		RequestId: "subscribe-1",
		MessageType: &v1beta.EventMessage_SubscribeProjectEvent{
			SubscribeProjectEvent: &v1beta.SubscribeProjectEvent{
				EventNames: []string{"postdeploy"},
			},
		},
	}))
	ack, err := stream.Recv()
	require.NoError(t, err)
	require.Equal(t, "subscribe-1", ack.GetRequestId())
	require.NotNil(t, ack.GetSubscribeProjectEventResponse())
	require.NoError(t, stream.Send(&v1beta.EventMessage{
		RequestId: "service-subscribe-1",
		MessageType: &v1beta.EventMessage_SubscribeServiceEvent{
			SubscribeServiceEvent: &v1beta.SubscribeServiceEvent{
				EventNames: []string{"predeploy"},
			},
		},
	}))
	serviceAck, err := stream.Recv()
	require.NoError(t, err)
	require.Equal(t, "service-subscribe-1", serviceAck.GetRequestId())
	require.NotNil(t, serviceAck.GetSubscribeServiceEventResponse())

	_, err = client.Extension().Ready(streamCtx, &azdext.ReadyRequest{})
	require.NoError(t, err)
	<-readyService.ready

	projectConfig, err := service.lazyProject.GetValue()
	require.NoError(t, err)
	followUpCollector := commandresult.NewFollowUpCollector()
	messageCollector := commandresult.NewServiceEventMessageCollector()
	eventCtx := commandresult.WithFollowUpCollector(t.Context(), followUpCollector)
	eventCtx = commandresult.WithServiceEventMessageCollector(eventCtx, messageCollector)
	eventDone := make(chan error, 1)
	go func() {
		eventDone <- projectConfig.RaiseEvent(
			eventCtx,
			ext.Event("postdeploy"),
			project.ProjectLifecycleEventArgs{Project: projectConfig},
		)
	}()
	invocation, err := stream.Recv()
	require.NoError(t, err)
	handler := invocation.GetInvokeProjectHandler()
	require.NotNil(t, handler)
	require.Equal(t, "postdeploy", handler.GetEventName())
	require.NotEmpty(t, handler.GetInvocationId())
	_, err = client.CommandResult().SetFollowUp(
		streamCtx,
		&v1beta.SetFollowUpRequest{
			InvocationId: handler.GetInvocationId(),
			Text:         "Run azd show",
		},
	)
	require.NoError(t, err)
	require.NoError(t, stream.Send(&v1beta.EventMessage{
		RequestId: invocation.GetRequestId(),
		MessageType: &v1beta.EventMessage_ProjectHandlerStatus{
			ProjectHandlerStatus: &v1beta.ProjectHandlerStatus{
				EventName: "postdeploy",
				Status:    "completed",
			},
		},
	}))
	require.NoError(t, <-eventDone)
	require.Equal(t, "Run azd show", followUpCollector.Text())

	serviceConfig := projectConfig.Services["api"]
	require.NotNil(t, serviceConfig)
	serviceDone := make(chan error, 1)
	go func() {
		serviceDone <- serviceConfig.RaiseEvent(
			eventCtx,
			ext.Event("predeploy"),
			project.ServiceLifecycleEventArgs{
				Project:        projectConfig,
				Service:        serviceConfig,
				ServiceContext: project.NewServiceContext(),
			},
		)
	}()
	serviceInvocation, err := stream.Recv()
	require.NoError(t, err)
	serviceHandler := serviceInvocation.GetInvokeServiceHandler()
	require.NotNil(t, serviceHandler)
	require.Equal(t, "api", serviceHandler.GetService().GetName())
	require.Equal(t, "predeploy", serviceHandler.GetEventName())
	require.NotEmpty(t, serviceInvocation.GetRequestId())
	require.NoError(t, stream.Send(&v1beta.EventMessage{
		RequestId: serviceInvocation.GetRequestId(),
		MessageType: &v1beta.EventMessage_ServiceHandlerStatus{
			ServiceHandlerStatus: &v1beta.ServiceHandlerStatus{
				EventName:   "predeploy",
				ServiceName: "api",
				Status:      "completed",
				Messages: []*v1beta.ServiceEventMessage{{
					Kind:    v1beta.ServiceEventMessageKind_SERVICE_EVENT_MESSAGE_KIND_WARNING,
					Message: "RBAC warning retained alongside follow-up",
				}},
			},
		},
	}))
	require.NoError(t, <-serviceDone)
	require.Equal(t, []commandresult.ServiceEventMessage{{
		ExtensionID: extension.Id,
		ServiceName: "api",
		EventName:   "predeploy",
		Kind:        "warning",
		Message:     "RBAC warning retained alongside follow-up",
	}}, messageCollector.Snapshot([]string{"api"}))
}

func TestBetaEventServiceLegacyBetaClientProjectHandlerCompletes(t *testing.T) {
	service, _ := createTestEventService()
	extension := createTestExtension()
	extension.Capabilities = []extensions.CapabilityType{
		extensions.LifecycleEventsCapability,
	}
	service.extensionManager = testExtensionLookup{extension: extension}

	streamCtx, cancel := context.WithCancel(extensionClaimsContext(t.Context(), extension.Id))
	t.Cleanup(cancel)
	baseStream := &scriptedBetaEventStream{
		ctx:    streamCtx,
		recvCh: make(chan *v1beta.EventMessage, 1),
	}
	invocations := make(chan *v1beta.EventMessage, 1)
	baseStream.sendFn = func(message *v1beta.EventMessage) error {
		if message.GetInvokeProjectHandler() == nil {
			return errors.New("expected project invocation")
		}
		invocations <- message
		return nil
	}
	baseStream.recvCh <- &v1beta.EventMessage{
		MessageType: &v1beta.EventMessage_SubscribeProjectEvent{
			SubscribeProjectEvent: &v1beta.SubscribeProjectEvent{
				EventNames: []string{"postdeploy"},
			},
		},
	}
	closeStream := sync.OnceFunc(func() { close(baseStream.recvCh) })
	t.Cleanup(closeStream)

	streamDone := make(chan error, 1)
	go func() {
		streamDone <- (&betaEventService{service: service}).EventStream(baseStream)
	}()

	projectConfig, err := service.lazyProject.GetValue()
	require.NoError(t, err)

	var invocation *v1beta.EventMessage
	var eventDone chan error
	timeout := time.After(5 * time.Second)
	for invocation == nil {
		eventDone = make(chan error, 1)
		go func(done chan<- error) {
			done <- projectConfig.RaiseEvent(
				streamCtx,
				ext.Event("postdeploy"),
				project.ProjectLifecycleEventArgs{Project: projectConfig},
			)
		}(eventDone)

		select {
		case invocation = <-invocations:
		case err := <-eventDone:
			require.NoError(t, err)
			select {
			case <-timeout:
				t.Fatal("legacy client did not receive a project invocation")
			case <-time.After(10 * time.Millisecond):
			}
		case <-timeout:
			t.Fatal("legacy subscription was not processed")
		}
	}
	require.Empty(t, invocation.GetRequestId())
	require.Empty(t, invocation.GetInvokeProjectHandler().GetInvocationId())

	baseStream.recvCh <- &v1beta.EventMessage{
		MessageType: &v1beta.EventMessage_ProjectHandlerStatus{
			ProjectHandlerStatus: &v1beta.ProjectHandlerStatus{
				EventName: "postdeploy",
				Status:    "completed",
			},
		},
	}
	select {
	case err := <-eventDone:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("legacy project handler status did not complete the hook")
	}

	closeStream()
	require.NoError(t, <-streamDone)
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
			RequestId: msg.RequestId,
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
		newBetaEventMessageEnvelope(),
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
		newBetaServiceEventCorrelations(),
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
			newBetaServiceEventCorrelations(),
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
		newBetaEventMessageEnvelope(),
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

func TestServer_BetaEventStreamCollectsServiceMessages(t *testing.T) {
	extension := &extensions.Extension{
		Id:           "test.beta.events",
		Version:      "1.0.0",
		Namespace:    "test",
		Capabilities: []extensions.CapabilityType{extensions.LifecycleEventsCapability},
	}
	service, _ := createTestEventService()
	service.extensionManager = newStreamTestExtensionManager(t, extension)
	server := newServerWithEventService(service)

	serverInfo, err := server.Start()
	require.NoError(t, err)
	defer func() {
		require.NoError(t, server.Stop())
	}()

	accessToken, err := GenerateExtensionToken(extension, serverInfo)
	require.NoError(t, err)
	client, err := azdext.NewAzdClient(azdext.WithAddress(serverInfo.Address))
	require.NoError(t, err)
	defer func() {
		client.Close()
	}()

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	ctx = azdext.WithAccessToken(ctx, accessToken)
	stream, err := client.EventsBeta().EventStream(ctx)
	require.NoError(t, err)

	require.NoError(t, stream.Send(&v1beta.EventMessage{
		RequestId: "project-subscription",
		MessageType: &v1beta.EventMessage_SubscribeProjectEvent{
			SubscribeProjectEvent: &v1beta.SubscribeProjectEvent{
				EventNames: []string{"predeploy"},
			},
		},
	}))
	projectAck, err := stream.Recv()
	require.NoError(t, err)
	require.Equal(t, "project-subscription", projectAck.GetRequestId())
	require.NotNil(t, projectAck.GetSubscribeProjectEventResponse())

	projectConfig, err := service.lazyProject.GetValue()
	require.NoError(t, err)
	projectDone := make(chan error, 1)
	go func() {
		projectDone <- projectConfig.RaiseEvent(
			ctx,
			ext.Event("predeploy"),
			project.ProjectLifecycleEventArgs{Project: projectConfig},
		)
	}()

	projectInvoke, err := stream.Recv()
	require.NoError(t, err)
	projectRequest := projectInvoke.GetInvokeProjectHandler()
	require.NotNil(t, projectRequest)
	require.NotEmpty(t, projectInvoke.GetRequestId())
	require.NoError(t, stream.Send(&v1beta.EventMessage{
		RequestId: projectInvoke.GetRequestId(),
		MessageType: &v1beta.EventMessage_ProjectHandlerStatus{
			ProjectHandlerStatus: &v1beta.ProjectHandlerStatus{
				EventName: "predeploy",
				Status:    "failed",
				Message:   "project hook failed",
			},
		},
	}))
	require.ErrorContains(
		t,
		<-projectDone,
		"extension test.beta.events project hook predeploy failed: project hook failed",
	)

	require.NoError(t, stream.Send(&v1beta.EventMessage{
		RequestId: "service-subscription",
		MessageType: &v1beta.EventMessage_SubscribeServiceEvent{
			SubscribeServiceEvent: &v1beta.SubscribeServiceEvent{
				EventNames: []string{"predeploy"},
				Language:   "ts",
				Host:       "containerapp",
			},
		},
	}))
	serviceAck, err := stream.Recv()
	require.NoError(t, err)
	require.Equal(t, "service-subscription", serviceAck.GetRequestId())
	require.NotNil(t, serviceAck.GetSubscribeServiceEventResponse())

	serviceConfig := projectConfig.Services["api"]
	require.NotNil(t, serviceConfig)
	collector := commandresult.NewServiceEventMessageCollector()
	eventCtx := commandresult.WithServiceEventMessageCollector(ctx, collector)
	serviceDone := make(chan error, 1)
	go func() {
		serviceDone <- serviceConfig.RaiseEvent(
			eventCtx,
			ext.Event("predeploy"),
			project.ServiceLifecycleEventArgs{
				Project:        projectConfig,
				Service:        serviceConfig,
				ServiceContext: project.NewServiceContext(),
			},
		)
	}()

	serviceInvoke, err := stream.Recv()
	require.NoError(t, err)
	serviceRequest := serviceInvoke.GetInvokeServiceHandler()
	require.NotNil(t, serviceRequest)
	require.Equal(t, "api", serviceRequest.GetService().GetName())
	require.NotEmpty(t, serviceInvoke.GetRequestId())
	require.NoError(t, stream.Send(&v1beta.EventMessage{
		RequestId: serviceInvoke.GetRequestId(),
		MessageType: &v1beta.EventMessage_ServiceHandlerStatus{
			ServiceHandlerStatus: &v1beta.ServiceHandlerStatus{
				EventName:   "predeploy",
				ServiceName: "api",
				Status:      "completed",
				Messages: []*v1beta.ServiceEventMessage{{
					Kind:    v1beta.ServiceEventMessageKind_SERVICE_EVENT_MESSAGE_KIND_WARNING,
					Message: "service RBAC warning",
				}},
			},
		},
	}))
	require.NoError(t, <-serviceDone)
	require.Equal(t, []commandresult.ServiceEventMessage{{
		ExtensionID: extension.Id,
		ServiceName: "api",
		EventName:   "predeploy",
		Kind:        "warning",
		Message:     "service RBAC warning",
	}}, collector.Snapshot([]string{"api"}))

	disconnectedDone := make(chan error, 1)
	go func() {
		disconnectedDone <- projectConfig.RaiseEvent(
			ctx,
			ext.Event("predeploy"),
			project.ProjectLifecycleEventArgs{Project: projectConfig},
		)
	}()

	disconnectedInvoke, err := stream.Recv()
	require.NoError(t, err)
	require.NotNil(t, disconnectedInvoke.GetInvokeProjectHandler())
	require.NoError(t, stream.CloseSend())
	require.Error(t, <-disconnectedDone)
}

func TestServer_BetaEventStreamCorrelatesConcurrentServiceHooks(t *testing.T) {
	extension := &extensions.Extension{
		Id:           "test.beta.concurrent",
		Version:      "1.0.0",
		Namespace:    "test",
		Capabilities: []extensions.CapabilityType{extensions.LifecycleEventsCapability},
	}
	service, _ := createTestEventService()
	service.extensionManager = newStreamTestExtensionManager(t, extension)
	server := newServerWithEventService(service)

	serverInfo, err := server.Start()
	require.NoError(t, err)
	defer func() {
		require.NoError(t, server.Stop())
	}()

	accessToken, err := GenerateExtensionToken(extension, serverInfo)
	require.NoError(t, err)
	client, err := azdext.NewAzdClient(azdext.WithAddress(serverInfo.Address))
	require.NoError(t, err)
	defer client.Close()

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	ctx = azdext.WithAccessToken(ctx, accessToken)
	stream, err := client.EventsBeta().EventStream(ctx)
	require.NoError(t, err)
	collector := commandresult.NewServiceEventMessageCollector()
	eventCtx := commandresult.WithServiceEventMessageCollector(ctx, collector)
	require.NoError(t, stream.Send(&v1beta.EventMessage{
		RequestId: "service-subscription",
		MessageType: &v1beta.EventMessage_SubscribeServiceEvent{
			SubscribeServiceEvent: &v1beta.SubscribeServiceEvent{
				EventNames: []string{"predeploy"},
			},
		},
	}))
	ack, err := stream.Recv()
	require.NoError(t, err)
	require.Equal(t, "service-subscription", ack.GetRequestId())
	require.NotNil(t, ack.GetSubscribeServiceEventResponse())

	projectConfig, err := service.lazyProject.GetValue()
	require.NoError(t, err)
	results := make(map[string]chan error)
	for _, name := range []string{"api", "web"} {
		serviceConfig := projectConfig.Services[name]
		require.NotNil(t, serviceConfig)
		done := make(chan error, 1)
		results[name] = done
		go func() {
			done <- serviceConfig.RaiseEvent(
				eventCtx,
				ext.Event("predeploy"),
				project.ServiceLifecycleEventArgs{
					Project:        projectConfig,
					Service:        serviceConfig,
					ServiceContext: project.NewServiceContext(),
				},
			)
		}()
	}

	// Both hooks must be in flight before either receives a status.
	invocations := make([]*v1beta.EventMessage, 2)
	names := make([]string, 2)
	for index := range invocations {
		invocations[index], err = stream.Recv()
		require.NoError(t, err)
		invocation := invocations[index].GetInvokeServiceHandler()
		require.NotNil(t, invocation)
		require.Equal(t, "predeploy", invocation.GetEventName())
		require.NotEmpty(t, invocations[index].GetRequestId())
		names[index] = invocation.GetService().GetName()
	}
	require.ElementsMatch(t, []string{"api", "web"}, names)
	require.NotEqual(t, invocations[0].GetRequestId(), invocations[1].GetRequestId())
	for _, done := range results {
		select {
		case err := <-done:
			t.Fatalf("hook completed before returning a status: %v", err)
		default:
		}
	}

	// Complete in reverse receive order with different outcomes.
	for _, index := range []int{1, 0} {
		status := "completed"
		message := ""
		if index == 1 {
			status = "failed"
			message = names[index] + " hook failed"
		}
		require.NoError(t, stream.Send(&v1beta.EventMessage{
			RequestId: invocations[index].GetRequestId(),
			MessageType: &v1beta.EventMessage_ServiceHandlerStatus{
				ServiceHandlerStatus: &v1beta.ServiceHandlerStatus{
					EventName:   "predeploy",
					ServiceName: names[index],
					Status:      status,
					Message:     message,
					Messages: []*v1beta.ServiceEventMessage{
						{
							Kind:    v1beta.ServiceEventMessageKind_SERVICE_EVENT_MESSAGE_KIND_WARNING,
							Message: names[index] + " first warning",
						},
						{
							Kind:    v1beta.ServiceEventMessageKind_SERVICE_EVENT_MESSAGE_KIND_WARNING,
							Message: names[index] + " second warning",
						},
					},
				},
			},
		}))
		select {
		case err := <-results[names[index]]:
			if index == 1 {
				require.ErrorContains(t, err,
					"service hook "+names[index]+".predeploy failed: "+message)
			} else {
				require.NoError(t, err)
			}
		case <-ctx.Done():
			t.Fatalf("hook %s did not complete: %v", names[index], ctx.Err())
		}
		if index == 1 {
			select {
			case err := <-results[names[0]]:
				t.Fatalf("first hook consumed the second hook's status: %v", err)
			default:
			}
		}
	}

	require.Equal(t, []commandresult.ServiceEventMessage{
		{
			ExtensionID: extension.Id,
			ServiceName: "api",
			EventName:   "predeploy",
			Kind:        "warning",
			Message:     "api first warning",
		},
		{
			ExtensionID: extension.Id,
			ServiceName: "api",
			EventName:   "predeploy",
			Kind:        "warning",
			Message:     "api second warning",
		},
		{
			ExtensionID: extension.Id,
			ServiceName: "web",
			EventName:   "predeploy",
			Kind:        "warning",
			Message:     "web first warning",
		},
		{
			ExtensionID: extension.Id,
			ServiceName: "web",
			EventName:   "predeploy",
			Kind:        "warning",
			Message:     "web second warning",
		},
	}, collector.Snapshot([]string{"api", "web"}))
	require.NoError(t, stream.CloseSend())
	message, err := stream.Recv()
	require.Nil(t, message)
	require.ErrorIs(t, err, io.EOF)
}

func TestServer_BetaEventStreamCompletesLegacyHookWithoutAck(t *testing.T) {
	extension := &extensions.Extension{
		Id:           "test.beta.legacy",
		Version:      "1.0.0",
		Namespace:    "test",
		Capabilities: []extensions.CapabilityType{extensions.LifecycleEventsCapability},
	}
	service, _ := createTestEventService()
	service.extensionManager = newStreamTestExtensionManager(t, extension)
	server := newServerWithEventService(service)

	serverInfo, err := server.Start()
	require.NoError(t, err)
	defer func() {
		require.NoError(t, server.Stop())
	}()

	accessToken, err := GenerateExtensionToken(extension, serverInfo)
	require.NoError(t, err)
	client, err := azdext.NewAzdClient(azdext.WithAddress(serverInfo.Address))
	require.NoError(t, err)
	defer func() {
		client.Close()
	}()

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	ctx = azdext.WithAccessToken(ctx, accessToken)
	stream, err := client.EventsBeta().EventStream(ctx)
	require.NoError(t, err)
	require.NoError(t, stream.Send(&v1beta.EventMessage{
		MessageType: &v1beta.EventMessage_SubscribeProjectEvent{
			SubscribeProjectEvent: &v1beta.SubscribeProjectEvent{
				EventNames: []string{"predeploy"},
			},
		},
	}))

	projectConfig, err := service.lazyProject.GetValue()
	require.NoError(t, err)
	invoked := make(chan struct{})
	projectDone := make(chan error, 1)
	// Retry until the legacy subscription actually invokes the hook.
	go func() {
		ticker := time.NewTicker(time.Millisecond)
		defer ticker.Stop()
		for {
			err := projectConfig.RaiseEvent(
				ctx,
				ext.Event("predeploy"),
				project.ProjectLifecycleEventArgs{Project: projectConfig},
			)
			if err != nil {
				projectDone <- err
				return
			}
			select {
			case <-invoked:
				projectDone <- nil
				return
			default:
			}
			select {
			case <-ctx.Done():
				projectDone <- ctx.Err()
				return
			case <-ticker.C:
			}
		}
	}()

	message, err := stream.Recv()
	require.NoError(t, err)
	invocation := message.GetInvokeProjectHandler()
	require.NotNil(t, invocation, "the first response must be an invocation, not an acknowledgement")
	require.Equal(t, "predeploy", invocation.GetEventName())
	require.Empty(t, message.GetRequestId())
	close(invoked)
	require.NoError(t, stream.Send(&v1beta.EventMessage{
		MessageType: &v1beta.EventMessage_ProjectHandlerStatus{
			ProjectHandlerStatus: &v1beta.ProjectHandlerStatus{
				EventName: "predeploy",
				Status:    "completed",
			},
		},
	}))
	select {
	case err := <-projectDone:
		require.NoError(t, err)
	case <-ctx.Done():
		t.Fatal("legacy hook did not complete after returning status without request_id")
	}

	require.NoError(t, stream.CloseSend())
	message, err = stream.Recv()
	require.Nil(t, message)
	require.ErrorIs(t, err, io.EOF)
}

func newServerWithEventService(eventService azdext.EventServiceServer) *Server {
	return NewServer(
		azdext.UnimplementedProjectServiceServer{},
		azdext.UnimplementedEnvironmentServiceServer{},
		azdext.UnimplementedPromptServiceServer{},
		azdext.UnimplementedUserConfigServiceServer{},
		azdext.UnimplementedDeploymentServiceServer{},
		eventService,
		v1beta.UnimplementedComposeServiceServer{},
		azdext.UnimplementedWorkflowServiceServer{},
		azdext.UnimplementedExtensionServiceServer{},
		azdext.UnimplementedServiceTargetServiceServer{},
		azdext.UnimplementedFrameworkServiceServer{},
		azdext.UnimplementedContainerServiceServer{},
		azdext.UnimplementedAccountServiceServer{},
		azdext.UnimplementedAiModelServiceServer{},
		v1beta.UnimplementedCopilotServiceServer{},
		azdext.UnimplementedProvisioningServiceServer{},
		azdext.UnimplementedValidationServiceServer{},
		v1beta.UnimplementedTelemetryServiceServer{},
		v1beta.UnimplementedCommandResultServiceServer{},
	)
}

func TestCollectBetaServiceEventMessagesRedactsLinkCredentials(t *testing.T) {
	rawURL := (&url.URL{
		Scheme:   "https",
		User:     url.UserPassword("test-user", "test-password"),
		Host:     "example.com",
		Path:     "/docs",
		RawQuery: "sig=secret",
		Fragment: "fragment",
	}).String()
	collector := commandresult.NewServiceEventMessageCollector()
	ctx := commandresult.WithServiceEventMessageCollector(t.Context(), collector)

	err := collectBetaServiceEventMessages(
		ctx,
		"test.extension",
		"postdeploy",
		"api",
		&v1beta.ServiceHandlerStatus{
			EventName:   "postdeploy",
			ServiceName: "api",
			Status:      "completed",
			Messages: []*v1beta.ServiceEventMessage{{
				Kind:    v1beta.ServiceEventMessageKind_SERVICE_EVENT_MESSAGE_KIND_WARNING,
				Message: "Review the deployment settings.",
				Links: []*v1beta.ErrorLink{{
					Title: "Deployment guide",
					Url:   rawURL,
				}},
			}},
		},
	)
	require.NoError(t, err)

	messages := collector.Snapshot([]string{"api"})
	require.Len(t, messages, 1)
	require.Equal(t, "https://example.com/docs", messages[0].Links[0].URL)
	for _, secret := range []string{"user", "password", "sig", "secret", "fragment"} {
		require.NotContains(t, messages[0].Links[0].URL, secret)
	}
}
