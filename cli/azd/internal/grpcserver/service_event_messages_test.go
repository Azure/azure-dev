// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package grpcserver

import (
	"context"
	"net"
	"net/url"
	"testing"
	"time"

	"github.com/azure/azure-dev/cli/azd/internal/commandresult"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/ext"
	"github.com/azure/azure-dev/cli/azd/pkg/extensions"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
)

func TestEventService_createServiceEventHandler_ServiceEventMessages(t *testing.T) {
	credentialURL := (&url.URL{
		Scheme:   "https",
		User:     url.UserPassword("test-user", "test-password"),
		Host:     "example.com",
		Path:     "/docs",
		RawQuery: "sig=secret",
		Fragment: "fragment",
	}).String()
	warning := &azdext.ServiceEventMessage{
		Kind:       azdext.ServiceEventMessageKind_SERVICE_EVENT_MESSAGE_KIND_WARNING,
		Message:    "Assign the required role.",
		Suggestion: "Ask an owner to grant access.",
		Links:      []*azdext.ErrorLink{{Title: "Role guide", Url: credentialURL}},
	}
	retained := []commandresult.ServiceEventMessage{{
		ExtensionID: "test.extension",
		ServiceName: "api",
		EventName:   "predeploy",
		Kind:        "warning",
		Message:     "Assign the required role.",
		Suggestion:  "Ask an owner to grant access.",
		Links:       []commandresult.ServiceEventMessageLink{{Title: "Role guide", URL: "https://example.com/docs"}},
	}}

	tests := []struct {
		name         string
		eventName    string
		status       string
		messages     []*azdext.ServiceEventMessage
		noCollector  bool
		wantErr      string
		wantMessages []commandresult.ServiceEventMessage
	}{
		{
			name:         "completed handler messages are retained with redacted links",
			eventName:    "predeploy",
			status:       "completed",
			messages:     []*azdext.ServiceEventMessage{warning},
			wantMessages: retained,
		},
		{
			name:      "failed handler messages are retained alongside the failure",
			eventName: "postdeploy",
			status:    "failed",
			messages: []*azdext.ServiceEventMessage{{
				Kind:    azdext.ServiceEventMessageKind_SERVICE_EVENT_MESSAGE_KIND_INFO,
				Message: "Deployment guidance.",
			}},
			wantErr: "service hook api.postdeploy failed",
			wantMessages: []commandresult.ServiceEventMessage{{
				ExtensionID: "test.extension",
				ServiceName: "api",
				EventName:   "postdeploy",
				Kind:        "info",
				Message:     "Deployment guidance.",
			}},
		},
		{
			name:      "messages are rejected for unsupported events",
			eventName: "prepackage",
			status:    "completed",
			messages:  []*azdext.ServiceEventMessage{warning},
			wantErr:   `messages are not supported for service event "prepackage"`,
		},
		{
			name:      "unspecified kind is rejected",
			eventName: "predeploy",
			status:    "completed",
			messages:  []*azdext.ServiceEventMessage{{Message: "No kind"}},
			wantErr:   "service event message 1 has unsupported kind",
		},
		{
			name:        "messages require a deploy command",
			eventName:   "predeploy",
			status:      "completed",
			messages:    []*azdext.ServiceEventMessage{warning},
			noCollector: true,
			wantErr:     "service event messages require a deploy command",
		},
		{
			name:        "statuses without messages keep existing behavior",
			eventName:   "predeploy",
			status:      "completed",
			noCollector: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service, _ := createTestEventService()
			extension := createTestExtension()
			projectConfig, err := service.lazyProject.GetValue()
			require.NoError(t, err)
			serviceConfig := projectConfig.Services["api"]
			require.NotNil(t, serviceConfig)

			broker, cleanup := createBrokerForEventHandler(
				t,
				extension.Id,
				func(msg *azdext.EventMessage) *azdext.EventMessage {
					invoke := msg.GetInvokeServiceHandler()
					require.NotNil(t, invoke)
					return &azdext.EventMessage{
						MessageType: &azdext.EventMessage_ServiceHandlerStatus{
							ServiceHandlerStatus: &azdext.ServiceHandlerStatus{
								EventName:   invoke.GetEventName(),
								ServiceName: invoke.GetService().GetName(),
								Status:      tt.status,
								Message:     "handler failed",
								Messages:    tt.messages,
							},
						},
					}
				},
			)
			defer cleanup()

			collector := commandresult.NewServiceEventMessageCollector()
			ctx := t.Context()
			if !tt.noCollector {
				ctx = commandresult.WithServiceEventMessageCollector(ctx, collector)
			}

			handler := service.createServiceEventHandler(serviceConfig, extension, tt.eventName, broker)
			err = handler(ctx, project.ServiceLifecycleEventArgs{
				Project:        projectConfig,
				Service:        serviceConfig,
				ServiceContext: project.NewServiceContext(),
			})
			if tt.wantErr != "" {
				require.ErrorContains(t, err, tt.wantErr)
			} else {
				require.NoError(t, err)
			}
			if len(tt.wantMessages) == 0 {
				require.Empty(t, collector.Snapshot([]string{"api"}))
			} else {
				require.Equal(t, tt.wantMessages, collector.Snapshot([]string{"api"}))
			}
		})
	}
}

func TestStableSDKServiceEventResponseHandlerEndToEnd(t *testing.T) {
	service, _ := createTestEventService()
	extension := createTestExtension()
	extension.Capabilities = []extensions.CapabilityType{extensions.LifecycleEventsCapability}
	service.extensionManager = testExtensionLookup{extension: extension}

	withClaims := func(ctx context.Context) context.Context {
		return extensionClaimsContext(ctx, extension.Id)
	}
	server := grpc.NewServer(grpc.StreamInterceptor(func(
		srv any,
		stream grpc.ServerStream,
		_ *grpc.StreamServerInfo,
		handler grpc.StreamHandler,
	) error {
		return handler(srv, &betaClaimsServerStream{
			ServerStream: stream,
			ctx:          withClaims(stream.Context()),
		})
	}))
	azdext.RegisterEventServiceServer(server, service)

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	go func() {
		_ = server.Serve(listener)
	}()
	t.Cleanup(func() {
		server.Stop()
		_ = listener.Close()
	})

	client, err := azdext.NewAzdClient(azdext.WithAddress(listener.Addr().String()))
	require.NoError(t, err)
	t.Cleanup(func() {
		client.Close()
	})

	streamCtx, cancel := context.WithTimeout(azdext.WithAccessToken(t.Context(), "test-token"), 10*time.Second)
	defer cancel()

	eventManager := azdext.NewEventManager(extension.Id, client, nil)
	t.Cleanup(func() {
		_ = eventManager.Close()
	})
	receiveDone := make(chan error, 1)
	go func() {
		receiveDone <- eventManager.Receive(streamCtx)
	}()
	require.NoError(t, eventManager.Ready(streamCtx))
	require.NoError(t, eventManager.AddServiceEventResponseHandler(
		streamCtx,
		"predeploy",
		func(_ context.Context, args *azdext.ServiceEventArgs) (*azdext.ServiceEventResponse, error) {
			return &azdext.ServiceEventResponse{
				Messages: []*azdext.ServiceEventMessage{{
					Kind:    azdext.ServiceEventMessageKind_SERVICE_EVENT_MESSAGE_KIND_WARNING,
					Message: "RBAC warning for " + args.Service.GetName(),
				}},
			}, nil
		},
		nil,
	))

	projectConfig, err := service.lazyProject.GetValue()
	require.NoError(t, err)
	serviceConfig := projectConfig.Services["api"]
	require.NotNil(t, serviceConfig)

	collector := commandresult.NewServiceEventMessageCollector()
	eventCtx := commandresult.WithServiceEventMessageCollector(t.Context(), collector)

	// The stable subscription is not acknowledged, so raise the event until the host has registered the handler.
	require.Eventually(t, func() bool {
		require.NoError(t, serviceConfig.RaiseEvent(
			eventCtx,
			ext.Event("predeploy"),
			project.ServiceLifecycleEventArgs{
				Project:        projectConfig,
				Service:        serviceConfig,
				ServiceContext: project.NewServiceContext(),
			},
		))
		return len(collector.Snapshot([]string{"api"})) > 0
	}, 5*time.Second, 10*time.Millisecond)

	require.Equal(t, []commandresult.ServiceEventMessage{{
		ExtensionID: extension.Id,
		ServiceName: "api",
		EventName:   "predeploy",
		Kind:        "warning",
		Message:     "RBAC warning for api",
	}}, collector.Snapshot([]string{"api"}))

	cancel()
	select {
	case <-receiveDone:
	case <-time.After(5 * time.Second):
		t.Fatal("event manager did not stop after cancellation")
	}
}
