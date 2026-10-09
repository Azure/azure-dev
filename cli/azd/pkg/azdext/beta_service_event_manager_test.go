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
	"github.com/azure/azure-dev/cli/azd/pkg/errorhandler"
	"github.com/azure/azure-dev/cli/azd/pkg/grpcbroker"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/proto"
)

type betaServiceEventTestHost struct {
	v1beta.UnimplementedEventServiceServer
	registrations chan *v1beta.SubscribeServiceEvent
	brokers       chan *grpcbroker.MessageBroker[v1beta.EventMessage]
	acknowledge   func(context.Context) (*v1beta.EventMessage, error)
}

func (h *betaServiceEventTestHost) EventStream(stream v1beta.EventService_EventStreamServer) error {
	broker := grpcbroker.NewMessageBroker(stream, &betaSDKEventMessageEnvelope{}, "host", nil)
	defer broker.Close()
	if err := broker.On(func(
		ctx context.Context, request *v1beta.SubscribeServiceEvent,
	) (*v1beta.EventMessage, error) {
		h.registrations <- request
		if h.acknowledge != nil {
			return h.acknowledge(ctx)
		}
		return &v1beta.EventMessage{
			MessageType: &v1beta.EventMessage_SubscribeServiceEventResponse{
				SubscribeServiceEventResponse: &v1beta.SubscribeServiceEventResponse{},
			},
		}, nil
	}); err != nil {
		return err
	}
	h.brokers <- broker
	return broker.Run(stream.Context())
}

func newBetaServiceEventTestClient(t *testing.T, host *betaServiceEventTestHost) *AzdClient {
	t.Helper()
	host.registrations = make(chan *v1beta.SubscribeServiceEvent, 4)
	host.brokers = make(chan *grpcbroker.MessageBroker[v1beta.EventMessage], 1)
	listener := bufconn.Listen(1024 * 1024)
	server := grpc.NewServer()
	v1beta.RegisterEventServiceServer(server, host)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)
	connection, err := grpc.NewClient(
		"passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return listener.DialContext(ctx)
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, connection.Close()) })
	client := newTestAzdClient()
	client.connection = connection
	return client
}

func TestBetaServiceEventManagerStream(t *testing.T) {
	t.Parallel()
	host := &betaServiceEventTestHost{}
	client := newBetaServiceEventTestClient(t, host)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	manager := newBetaServiceEventManager("test.extension", client, nil)
	done := make(chan error, 1)
	go func() { done <- manager.Receive(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			require.True(t, err == nil || errors.Is(err, context.Canceled) || status.Code(err) == codes.Canceled, "%v", err)
		case <-time.After(5 * time.Second):
			t.Error("beta service event receiver did not stop")
		}
		require.NoError(t, manager.Close())
		require.NoError(t, manager.Close())
		require.Nil(t, manager.broker)
	})
	require.NoError(t, manager.Ready(ctx))
	handler := func(_ context.Context, args *ServiceEventArgs) (*BetaServiceEventResponse, error) {
		return &BetaServiceEventResponse{
			Messages: []BetaServiceEventMessage{{
				Kind:    BetaServiceEventMessageInfo,
				Message: args.Service.Name + " deployed",
				Links:   []errorhandler.ErrorLink{{URL: "https://example.com/docs", Title: "Deployment guide"}},
			}},
		}, nil
	}
	require.NoError(t, manager.AddBetaServiceEventHandler(
		ctx, "postdeploy", handler, &ServiceEventOptions{Host: "containerapp", Language: "python"},
	))
	registration := <-host.registrations
	require.Equal(t, []string{"postdeploy"}, registration.GetEventNames())
	require.Equal(t, "containerapp", registration.GetHost())
	require.Equal(t, "python", registration.GetLanguage())
	require.ErrorContains(t, manager.AddBetaServiceEventHandler(ctx, "postdeploy", handler, nil), "already registered")
	require.NoError(t, manager.AddBetaServiceEventHandler(ctx, "predeploy", handler, nil))
	registration = <-host.registrations
	require.Empty(t, registration.GetHost())
	require.Empty(t, registration.GetLanguage())
	broker := <-host.brokers
	response, err := broker.SendAndWait(ctx, &v1beta.EventMessage{
		RequestId: "deploy-1",
		MessageType: &v1beta.EventMessage_InvokeServiceHandler{
			InvokeServiceHandler: &v1beta.InvokeServiceHandler{
				EventName: "postdeploy",
				Project:   &v1beta.ProjectConfig{Name: "project"},
				Service:   &v1beta.ServiceConfig{Name: "api"},
			},
		},
	})
	require.NoError(t, err)
	require.Equal(t, "deploy-1", response.GetRequestId())
	handlerStatus := response.GetServiceHandlerStatus()
	require.Equal(t, "completed", handlerStatus.GetStatus())
	require.Equal(t, "api", handlerStatus.GetServiceName())
	require.Equal(t, []*v1beta.ServiceEventMessage{{
		Kind:    v1beta.ServiceEventMessageKind_SERVICE_EVENT_MESSAGE_KIND_INFO,
		Message: "api deployed",
		Links:   []*v1beta.ErrorLink{{Url: "https://example.com/docs", Title: "Deployment guide"}},
	}}, handlerStatus.GetMessages())
}

func TestBetaServiceEventManagerRegistrationFailures(t *testing.T) {
	tests := []struct {
		name        string
		acknowledge func(context.Context) (*v1beta.EventMessage, error)
		wantError   string
	}{
		{
			name: "host rejects subscription",
			acknowledge: func(context.Context) (*v1beta.EventMessage, error) {
				return nil, &LocalError{Message: "subscription rejected", Code: "unsupported_event"}
			},
			wantError: "failed to register beta service event",
		},
		{
			name: "wrong acknowledgement type",
			acknowledge: func(context.Context) (*v1beta.EventMessage, error) {
				return &v1beta.EventMessage{
					MessageType: &v1beta.EventMessage_SubscribeProjectEventResponse{
						SubscribeProjectEventResponse: &v1beta.SubscribeProjectEventResponse{},
					},
				}, nil
			},
			wantError: "invalid acknowledgement",
		},
		{
			name: "acknowledgement deadline",
			acknowledge: func(ctx context.Context) (*v1beta.EventMessage, error) {
				<-ctx.Done()
				return nil, ctx.Err()
			},
			wantError: "timed out waiting",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			host := &betaServiceEventTestHost{acknowledge: test.acknowledge}
			client := newBetaServiceEventTestClient(t, host)
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			manager := newBetaServiceEventManager("test.extension", client, nil)
			done := make(chan error, 1)
			go func() { done <- manager.Receive(ctx) }()
			t.Cleanup(func() {
				cancel()
				select {
				case err := <-done:
					require.True(t, err == nil || errors.Is(err, context.Canceled) ||
						status.Code(err) == codes.Canceled, "%v", err)
				case <-time.After(5 * time.Second):
					t.Error("beta service event receiver did not stop")
				}
				require.NoError(t, manager.Close())
			})
			require.NoError(t, manager.Ready(ctx))
			handler := func(context.Context, *ServiceEventArgs) (*BetaServiceEventResponse, error) {
				return nil, nil
			}
			ackCtx, ackCancel := context.WithTimeout(ctx, time.Second)
			defer ackCancel()
			err := manager.AddBetaServiceEventHandler(ackCtx, "predeploy", handler, nil)
			require.ErrorContains(t, err, test.wantError)
			require.NotContains(t, manager.serviceEvents, "predeploy")
		})
	}
}

func TestBetaServiceEventManagerInvalidRegistration(t *testing.T) {
	manager := newBetaServiceEventManager("test.extension", nil, nil)
	handler := func(context.Context, *ServiceEventArgs) (*BetaServiceEventResponse, error) {
		return nil, nil
	}
	require.ErrorContains(t, manager.AddBetaServiceEventHandler(t.Context(), "prepackage", handler, nil), "not supported")
	require.ErrorContains(t, manager.AddBetaServiceEventHandler(t.Context(), "predeploy", nil, nil), "handler is required")
	require.ErrorContains(t,
		manager.AddBetaServiceEventHandler(t.Context(), "predeploy", handler, nil), "client is required",
	)
	require.ErrorContains(t, manager.Receive(t.Context()), "client is required")
	require.ErrorContains(t, manager.Ready(t.Context()), "client is required")
	require.NoError(t, manager.Close())
}

func TestExtensionHostBetaServiceEvents(t *testing.T) {
	t.Parallel()
	remote := &betaServiceEventTestHost{}
	client := newBetaServiceEventTestClient(t, remote)
	ready := make(chan struct{})
	extensionClient := &MockExtensionServiceClient{}
	client.extensionClient = extensionClient
	extensionClient.
		On("Ready", mock.Anything, mock.Anything, mock.Anything).
		Run(func(mock.Arguments) { close(ready) }).
		Return(&ReadyResponse{}, nil)
	host := NewExtensionHost(client).WithBetaServiceEventHandler(
		"predeploy",
		func(context.Context, *ServiceEventArgs) (*BetaServiceEventResponse, error) { return nil, nil },
		nil,
	)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- host.Run(ctx) }()
	select {
	case <-ready:
	case err := <-done:
		t.Fatalf("host stopped before readiness: %v", err)
	case <-ctx.Done():
		t.Fatal("host did not become ready")
	}
	cancel()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("host did not stop")
	}
	require.Equal(t, []string{"predeploy"}, (<-remote.registrations).GetEventNames())
}

func TestBetaServiceEventManagerInvalidInvocations(t *testing.T) {
	tests := []struct {
		name      string
		request   *v1beta.InvokeServiceHandler
		wantError string
	}{
		{name: "nil invocation", wantError: "invocation is required"},
		{
			name:      "missing project",
			request:   &v1beta.InvokeServiceHandler{EventName: "predeploy"},
			wantError: "has no project",
		},
		{
			name: "missing service",
			request: &v1beta.InvokeServiceHandler{
				EventName: "predeploy", Project: &v1beta.ProjectConfig{},
			},
			wantError: "has no service",
		},
		{
			name: "invalid project text",
			request: &v1beta.InvokeServiceHandler{
				EventName: "predeploy", Project: &v1beta.ProjectConfig{Name: string([]byte{0xff})},
				Service: &v1beta.ServiceConfig{},
			},
			wantError: "convert beta project config",
		},
		{
			name: "invalid service text",
			request: &v1beta.InvokeServiceHandler{
				EventName: "predeploy", Project: &v1beta.ProjectConfig{},
				Service: &v1beta.ServiceConfig{Name: string([]byte{0xff})},
			},
			wantError: "convert beta service config",
		},
		{
			name: "invalid context text",
			request: &v1beta.InvokeServiceHandler{
				EventName: "predeploy", Project: &v1beta.ProjectConfig{},
				Service: &v1beta.ServiceConfig{},
				ServiceContext: &v1beta.ServiceContext{
					Package: []*v1beta.Artifact{{Location: string([]byte{0xff})}},
				},
			},
			wantError: "convert beta service context",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			manager := newBetaServiceEventManager("test.extension", nil, nil)
			manager.serviceEvents["predeploy"] = func(
				context.Context, *ServiceEventArgs,
			) (*BetaServiceEventResponse, error) {
				t.Fatal("invalid invocation reached handler")
				return nil, nil
			}
			response, err := manager.onInvokeServiceHandler(t.Context(), test.request)
			require.ErrorContains(t, err, test.wantError)
			require.Nil(t, response)
		})
	}
	manager := newBetaServiceEventManager("test.extension", nil, nil)
	response, err := manager.onInvokeServiceHandler(t.Context(), &v1beta.InvokeServiceHandler{EventName: "postdeploy"})
	require.NoError(t, err)
	require.True(t, proto.Equal(&v1beta.EventMessage{}, response))
}

func TestBetaServiceEventManagerValidatesReturnedMessages(t *testing.T) {
	tests := []struct {
		name       string
		handlerErr error
	}{
		{name: "successful handler"},
		{name: "failed handler", handlerErr: errors.New("deployment failed")},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			manager := newBetaServiceEventManager("test.extension", nil, nil)
			manager.serviceEvents["postdeploy"] = func(
				_ context.Context, args *ServiceEventArgs,
			) (*BetaServiceEventResponse, error) {
				require.Equal(t, "image", args.ServiceContext.GetPackage()[0].GetLocation())
				return &BetaServiceEventResponse{Messages: []BetaServiceEventMessage{
					{Kind: "unsupported", Message: "invalid kind"},
					{Kind: BetaServiceEventMessageInfo, Message: " \t\n"},
					{Kind: BetaServiceEventMessageWarning, Message: "keep this warning"},
				}}, test.handlerErr
			}
			response, err := manager.onInvokeServiceHandler(t.Context(), &v1beta.InvokeServiceHandler{
				EventName: "postdeploy", Project: &v1beta.ProjectConfig{}, Service: &v1beta.ServiceConfig{Name: "api"},
				ServiceContext: &v1beta.ServiceContext{Package: []*v1beta.Artifact{{Location: "image"}}},
			})
			require.NoError(t, err)
			handlerStatus := response.GetServiceHandlerStatus()
			require.Equal(t, "failed", handlerStatus.GetStatus())
			require.Contains(t, handlerStatus.GetMessage(), "message 1: unsupported message kind")
			require.Contains(t, handlerStatus.GetMessage(), "message 2: message text is required")
			if test.handlerErr != nil {
				require.Contains(t, handlerStatus.GetMessage(), test.handlerErr.Error())
			}
			require.Len(t, handlerStatus.GetMessages(), 1)
			require.Equal(t, "keep this warning", handlerStatus.GetMessages()[0].GetMessage())
			require.NotNil(t, handlerStatus.GetError())
		})
	}
}

func TestBetaServiceEventErrors(t *testing.T) {
	localErr := &LocalError{
		Message: "invalid config", Code: "invalid_config", Category: LocalErrorCategoryUser,
		CauseTypes: []string{" *ConfigError ", "*ConfigError"},
	}
	wrapped := wrapBetaServiceEventError(localErr)
	require.Equal(t, "invalid_config", wrapped.GetLocalError().GetCode())
	require.NotEmpty(t, wrapped.GetLocalError().GetCauseTypes())
	for _, exitCode := range []*int{nil, new(2)} {
		toolErr := &ToolError{
			Message: "build failed", ToolName: "docker", Kind: ToolErrorKindFailed, ExitCode: exitCode,
		}
		wrapped = wrapBetaServiceEventError(toolErr)
		require.Equal(t, "docker", wrapped.GetToolError().GetToolName())
		require.Equal(t, "failed", wrapped.GetToolError().GetFailureKind())
		if exitCode == nil {
			require.Nil(t, wrapped.GetToolError().ExitCode)
		} else {
			require.EqualValues(t, *exitCode, wrapped.GetToolError().GetExitCode())
		}
	}
	require.Nil(t, wrapBetaServiceEventError(nil))
	envelope := &betaSDKEventMessageEnvelope{}
	require.NoError(t, envelope.GetError(nil))
	require.NoError(t, envelope.GetError(&v1beta.EventMessage{}))
	message := &v1beta.EventMessage{}
	envelope.SetError(message, localErr)
	restored, ok := errors.AsType[*LocalError](envelope.GetError(message))
	require.True(t, ok)
	require.Equal(t, localErr.Code, restored.Code)
	envelope.SetError(message, nil)
	require.Nil(t, message.GetError())
	message.Error = &v1beta.ExtensionError{Message: string([]byte{0xff})}
	require.EqualError(t, envelope.GetError(message), string([]byte{0xff}))
}

func TestBetaSDKEventMessageEnvelope(t *testing.T) {
	envelope := &betaSDKEventMessageEnvelope{}
	require.Empty(t, envelope.GetRequestId(t.Context(), nil))
	message := &v1beta.EventMessage{}
	envelope.SetRequestId(t.Context(), message, "invocation")
	require.Equal(t, "invocation", envelope.GetRequestId(t.Context(), message))
	require.Nil(t, envelope.GetInnerMessage(nil))
	require.Nil(t, envelope.GetInnerMessage(message))
	require.False(t, envelope.IsProgressMessage(message))
	require.Empty(t, envelope.GetProgressMessage(message))
	require.Nil(t, envelope.CreateProgressMessage("invocation", "output"))

	tests := []struct {
		name    string
		message *v1beta.EventMessage
		inner   proto.Message
	}{
		{
			name: "project subscription",
			message: &v1beta.EventMessage{MessageType: &v1beta.EventMessage_SubscribeProjectEvent{
				SubscribeProjectEvent: &v1beta.SubscribeProjectEvent{},
			}},
			inner: &v1beta.SubscribeProjectEvent{},
		},
		{
			name: "project invocation",
			message: &v1beta.EventMessage{MessageType: &v1beta.EventMessage_InvokeProjectHandler{
				InvokeProjectHandler: &v1beta.InvokeProjectHandler{},
			}},
			inner: &v1beta.InvokeProjectHandler{},
		},
		{
			name: "project status",
			message: &v1beta.EventMessage{MessageType: &v1beta.EventMessage_ProjectHandlerStatus{
				ProjectHandlerStatus: &v1beta.ProjectHandlerStatus{},
			}},
			inner: &v1beta.ProjectHandlerStatus{},
		},
		{
			name: "service subscription",
			message: &v1beta.EventMessage{MessageType: &v1beta.EventMessage_SubscribeServiceEvent{
				SubscribeServiceEvent: &v1beta.SubscribeServiceEvent{},
			}},
			inner: &v1beta.SubscribeServiceEvent{},
		},
		{
			name: "service invocation",
			message: &v1beta.EventMessage{MessageType: &v1beta.EventMessage_InvokeServiceHandler{
				InvokeServiceHandler: &v1beta.InvokeServiceHandler{},
			}},
			inner: &v1beta.InvokeServiceHandler{},
		},
		{
			name: "service status",
			message: &v1beta.EventMessage{MessageType: &v1beta.EventMessage_ServiceHandlerStatus{
				ServiceHandlerStatus: &v1beta.ServiceHandlerStatus{},
			}},
			inner: &v1beta.ServiceHandlerStatus{},
		},
		{
			name: "project acknowledgement",
			message: &v1beta.EventMessage{MessageType: &v1beta.EventMessage_SubscribeProjectEventResponse{
				SubscribeProjectEventResponse: &v1beta.SubscribeProjectEventResponse{},
			}},
			inner: &v1beta.SubscribeProjectEventResponse{},
		},
		{
			name: "service acknowledgement",
			message: &v1beta.EventMessage{MessageType: &v1beta.EventMessage_SubscribeServiceEventResponse{
				SubscribeServiceEventResponse: &v1beta.SubscribeServiceEventResponse{},
			}},
			inner: &v1beta.SubscribeServiceEventResponse{},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			inner, ok := envelope.GetInnerMessage(test.message).(proto.Message)
			require.True(t, ok)
			require.IsType(t, test.inner, inner)
			require.True(t, proto.Equal(test.inner, inner))
		})
	}
}

func TestBetaServiceEventManagerReturnsStructuredMessages(t *testing.T) {
	tests := []struct {
		name        string
		handlerErr  error
		wantStatus  string
		wantMessage string
	}{
		{
			name:       "completed handler",
			wantStatus: "completed",
		},
		{
			name:        "failed handler retains messages",
			handlerErr:  errors.New("deployment policy check failed"),
			wantStatus:  "failed",
			wantMessage: "deployment policy check failed",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			manager := newBetaServiceEventManager("test.extension", nil, nil)
			manager.serviceEvents["postdeploy"] = func(
				context.Context,
				*ServiceEventArgs,
			) (*BetaServiceEventResponse, error) {
				return &BetaServiceEventResponse{
					Messages: []BetaServiceEventMessage{{
						Kind:       BetaServiceEventMessageWarning,
						Message:    "Review the access policy.",
						Suggestion: "Update the policy before the next deployment.",
					}},
				}, test.handlerErr
			}

			response, err := manager.onInvokeServiceHandler(
				t.Context(),
				&v1beta.InvokeServiceHandler{
					EventName: "postdeploy",
					Project:   &v1beta.ProjectConfig{},
					Service:   &v1beta.ServiceConfig{Name: "api"},
				},
			)
			require.NoError(t, err)
			status := response.GetServiceHandlerStatus()
			require.NotNil(t, status)
			require.Equal(t, "postdeploy", status.GetEventName())
			require.Equal(t, "api", status.GetServiceName())
			require.Equal(t, test.wantStatus, status.GetStatus())
			require.Equal(t, test.wantMessage, status.GetMessage())
			if test.handlerErr == nil {
				require.Nil(t, status.GetError())
			} else {
				require.Equal(t, test.handlerErr.Error(), status.GetError().GetMessage())
			}
			require.Equal(t, []*v1beta.ServiceEventMessage{{
				Kind:       v1beta.ServiceEventMessageKind_SERVICE_EVENT_MESSAGE_KIND_WARNING,
				Message:    "Review the access policy.",
				Suggestion: "Update the policy before the next deployment.",
			}}, status.GetMessages())
		})
	}
}
