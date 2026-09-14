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

	"github.com/azure/azure-dev/cli/azd/pkg/grpcbroker"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
)

type eventRoundTrip struct {
	progress []string
	response *EventMessage
	err      error
}

type eventIntegrationServer struct {
	UnimplementedEventServiceServer

	extensionID    string
	projectTrigger chan struct{}
	serviceTrigger chan struct{}
	projectResults chan eventRoundTrip
	serviceResults chan eventRoundTrip
}

func (s *eventIntegrationServer) EventStream(
	stream grpc.BidiStreamingServer[EventMessage, EventMessage],
) error {
	broker := grpcbroker.NewMessageBroker(
		stream,
		newEventMessageEnvelope(s.extensionID),
		"test-host",
		nil,
	)

	if err := broker.On(func(
		ctx context.Context,
		msg *SubscribeProjectEvent,
	) (*EventMessage, error) {
		<-s.projectTrigger

		progress := []string{}
		response, err := broker.SendAndWaitWithProgress(
			ctx,
			&EventMessage{
				MessageType: &EventMessage_InvokeProjectHandler{
					InvokeProjectHandler: &InvokeProjectHandler{
						EventName: "predeploy",
						Project:   &ProjectConfig{Name: "test-project"},
					},
				},
			},
			func(message string) {
				progress = append(progress, message)
			},
		)
		s.projectResults <- eventRoundTrip{
			progress: progress,
			response: response,
			err:      err,
		}
		return nil, err
	}); err != nil {
		return err
	}

	if err := broker.On(func(
		ctx context.Context,
		msg *SubscribeServiceEvent,
	) (*EventMessage, error) {
		<-s.serviceTrigger

		progress := []string{}
		response, err := broker.SendAndWaitWithProgress(
			ctx,
			&EventMessage{
				MessageType: &EventMessage_InvokeServiceHandler{
					InvokeServiceHandler: &InvokeServiceHandler{
						EventName: "predeploy",
						Project:   &ProjectConfig{Name: "test-project"},
						Service:   &ServiceConfig{Name: "api"},
						ServiceContext: &ServiceContext{
							Package: []*Artifact{},
						},
					},
				},
			},
			func(message string) {
				progress = append(progress, message)
			},
		)
		s.serviceResults <- eventRoundTrip{
			progress: progress,
			response: response,
			err:      err,
		}
		return nil, err
	}); err != nil {
		return err
	}

	return broker.Run(stream.Context())
}

func startEventIntegrationServer(
	t *testing.T,
	extensionID string,
) (*AzdClient, *eventIntegrationServer, func()) {
	t.Helper()

	listener := bufconn.Listen(1024 * 1024)
	server := &eventIntegrationServer{
		extensionID:    extensionID,
		projectTrigger: make(chan struct{}),
		serviceTrigger: make(chan struct{}),
		projectResults: make(chan eventRoundTrip, 1),
		serviceResults: make(chan eventRoundTrip, 1),
	}
	grpcServer := grpc.NewServer()
	RegisterEventServiceServer(grpcServer, server)

	go func() {
		_ = grpcServer.Serve(listener)
	}()

	//nolint:staticcheck // Required for the bufconn test pattern.
	connection, err := grpc.DialContext(
		t.Context(),
		"bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return listener.DialContext(ctx)
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	require.NoError(t, err)

	client := &AzdClient{connection: connection}
	cleanup := func() {
		client.Close()
		grpcServer.Stop()
		require.NoError(t, listener.Close())
	}

	return client, server, cleanup
}

func waitForEventRoundTrip(t *testing.T, results <-chan eventRoundTrip) eventRoundTrip {
	t.Helper()

	select {
	case result := <-results:
		return result
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for lifecycle handler response")
		return eventRoundTrip{}
	}
}

func TestEventManager_EventOutputRoundTripWithoutClaims(t *testing.T) {
	const extensionID = "microsoft.azd.demo"

	client, server, cleanup := startEventIntegrationServer(t, extensionID)
	defer cleanup()

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	eventManager := NewEventManager(extensionID, client, nil)
	receiveErr := make(chan error, 1)
	go func() {
		receiveErr <- eventManager.Receive(ctx)
	}()
	require.NoError(t, eventManager.Ready(ctx))

	projectHandler := func(ctx context.Context, args *ProjectEventArgs) error {
		_, err := EventOutput(ctx).Write([]byte("project warning\n"))
		return err
	}
	require.NoError(t, eventManager.AddProjectEventHandler(ctx, "predeploy", projectHandler))
	close(server.projectTrigger)

	projectResult := waitForEventRoundTrip(t, server.projectResults)
	require.NoError(t, projectResult.err)
	require.Equal(t, []string{"project warning\n"}, projectResult.progress)
	require.NotNil(t, projectResult.response)
	require.Equal(
		t,
		"completed",
		projectResult.response.GetProjectHandlerStatus().GetStatus(),
	)

	serviceHandler := func(ctx context.Context, args *ServiceEventArgs) error {
		_, err := EventOutput(ctx).Write([]byte("service warning\n"))
		return err
	}
	require.NoError(t, eventManager.AddServiceEventHandler(ctx, "predeploy", serviceHandler, nil))
	close(server.serviceTrigger)

	serviceResult := waitForEventRoundTrip(t, server.serviceResults)
	require.NoError(t, serviceResult.err)
	require.Equal(t, []string{"service warning\n"}, serviceResult.progress)
	require.NotNil(t, serviceResult.response)
	require.Equal(
		t,
		"completed",
		serviceResult.response.GetServiceHandlerStatus().GetStatus(),
	)

	eventManager.Close()
	cancel()
	select {
	case err := <-receiveErr:
		require.True(t, errors.Is(err, context.Canceled) || errors.Is(err, io.EOF))
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for event manager shutdown")
	}
}
