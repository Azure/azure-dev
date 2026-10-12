// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package grpcserver

import (
	"context"
	"io"
	"testing"
	"time"

	"github.com/azure/azure-dev/cli/azd/internal/commandresult"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	v1beta "github.com/azure/azure-dev/cli/azd/pkg/azdext/contracts/v1beta"
	"github.com/azure/azure-dev/cli/azd/pkg/ext"
	"github.com/azure/azure-dev/cli/azd/pkg/extensions"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type betaEventTestStream = grpc.BidiStreamingClient[
	v1beta.EventMessage,
	v1beta.EventMessage,
]

func TestServer_BetaEventStreamCorrelatesSameServiceHooksOutOfOrder(t *testing.T) {
	projectConfig, ctx, stream := newAuthenticatedBetaEventStream(t, "test.beta.same")
	subscribeBetaServiceEvent(t, stream, "service-subscription")

	serviceConfig := projectConfig.Services["api"]
	firstCollector := commandresult.NewServiceEventMessageCollector()
	secondCollector := commandresult.NewServiceEventMessageCollector()
	firstDone := raiseBetaServiceEvent(
		commandresult.WithServiceEventMessageCollector(ctx, firstCollector),
		projectConfig,
		serviceConfig,
		"predeploy",
	)
	firstInvoke, err := stream.Recv()
	require.NoError(t, err)
	require.Equal(t, "api", firstInvoke.GetInvokeServiceHandler().GetService().GetName())

	secondDone := raiseBetaServiceEvent(
		commandresult.WithServiceEventMessageCollector(ctx, secondCollector),
		projectConfig,
		serviceConfig,
		"predeploy",
	)
	secondInvoke, err := stream.Recv()
	require.NoError(t, err)
	require.Equal(t, "api", secondInvoke.GetInvokeServiceHandler().GetService().GetName())
	require.NotEmpty(t, firstInvoke.GetRequestId())
	require.NotEqual(t, firstInvoke.GetRequestId(), secondInvoke.GetRequestId())

	require.NoError(t, stream.Send(betaServiceStatusMessage(
		secondInvoke.GetRequestId(),
		"predeploy",
		"api",
		"failed",
		"second invocation failed",
		"second invocation warning",
	)))
	require.ErrorContains(t, waitBetaServiceEvent(t, ctx, secondDone), "second invocation failed")
	select {
	case err := <-firstDone:
		t.Fatalf("first invocation completed with the second status: %v", err)
	default:
	}

	require.NoError(t, stream.Send(betaServiceStatusMessage(
		firstInvoke.GetRequestId(),
		"predeploy",
		"api",
		"completed",
		"",
		"first invocation completed",
	)))
	require.NoError(t, waitBetaServiceEvent(t, ctx, firstDone))
	require.Equal(t, []commandresult.ServiceEventMessage{{
		ExtensionID: "test.beta.same",
		ServiceName: "api",
		EventName:   "predeploy",
		Kind:        "warning",
		Message:     "first invocation completed",
	}}, firstCollector.Snapshot([]string{"api"}))
	require.Equal(t, []commandresult.ServiceEventMessage{{
		ExtensionID: "test.beta.same",
		ServiceName: "api",
		EventName:   "predeploy",
		Kind:        "warning",
		Message:     "second invocation warning",
	}}, secondCollector.Snapshot([]string{"api"}))

	require.NoError(t, stream.CloseSend())
	_, err = stream.Recv()
	require.ErrorIs(t, err, io.EOF)
}

func TestServer_BetaEventStreamRejectsMismatchedServiceStatusRequestID(t *testing.T) {
	projectConfig, ctx, stream := newAuthenticatedBetaEventStream(t, "test.beta.mismatched")
	subscribeBetaServiceEvent(t, stream, "service-subscription")

	apiDone := raiseBetaServiceEvent(
		ctx,
		projectConfig,
		projectConfig.Services["api"],
		"predeploy",
	)
	apiInvoke, err := stream.Recv()
	require.NoError(t, err)
	require.Equal(t, "api", apiInvoke.GetInvokeServiceHandler().GetService().GetName())

	webDone := raiseBetaServiceEvent(
		ctx,
		projectConfig,
		projectConfig.Services["web"],
		"predeploy",
	)
	webInvoke, err := stream.Recv()
	require.NoError(t, err)
	require.Equal(t, "web", webInvoke.GetInvokeServiceHandler().GetService().GetName())

	require.NoError(t, stream.Send(betaServiceStatusMessage(
		apiInvoke.GetRequestId(),
		"predeploy",
		"web",
		"completed",
		"",
		"",
	)))
	_, err = stream.Recv()
	require.Equal(t, codes.InvalidArgument, status.Code(err))
	require.Error(t, waitBetaServiceEvent(t, ctx, apiDone))
	require.Error(t, waitBetaServiceEvent(t, ctx, webDone))
}

func TestServer_BetaEventStreamRejectsAmbiguousIDlessServiceStatus(t *testing.T) {
	projectConfig, ctx, stream := newAuthenticatedBetaEventStream(t, "test.beta.ambiguous")
	subscribeBetaServiceEvent(t, stream, "service-subscription")

	serviceConfig := projectConfig.Services["api"]
	firstDone := raiseBetaServiceEvent(ctx, projectConfig, serviceConfig, "predeploy")
	_, err := stream.Recv()
	require.NoError(t, err)
	secondDone := raiseBetaServiceEvent(ctx, projectConfig, serviceConfig, "predeploy")
	_, err = stream.Recv()
	require.NoError(t, err)

	require.NoError(t, stream.Send(&v1beta.EventMessage{
		MessageType: &v1beta.EventMessage_ServiceHandlerStatus{
			ServiceHandlerStatus: &v1beta.ServiceHandlerStatus{
				EventName:   "predeploy",
				ServiceName: "api",
				Status:      "completed",
			},
		},
	}))
	_, err = stream.Recv()
	require.Equal(t, codes.InvalidArgument, status.Code(err))
	require.Error(t, waitBetaServiceEvent(t, ctx, firstDone))
	require.Error(t, waitBetaServiceEvent(t, ctx, secondDone))
}

func TestServer_BetaEventStreamDoesNotRouteLateCanceledResponse(t *testing.T) {
	projectConfig, ctx, stream := newAuthenticatedBetaEventStream(t, "test.beta.late")
	subscribeBetaServiceEvent(t, stream, "service-subscription")

	serviceConfig := projectConfig.Services["api"]
	firstCollector := commandresult.NewServiceEventMessageCollector()
	firstCtx, cancelFirst := context.WithCancel(
		commandresult.WithServiceEventMessageCollector(ctx, firstCollector),
	)
	firstDone := raiseBetaServiceEvent(firstCtx, projectConfig, serviceConfig, "predeploy")
	firstInvoke, err := stream.Recv()
	require.NoError(t, err)
	cancelFirst()
	require.ErrorIs(t, waitBetaServiceEvent(t, ctx, firstDone), context.Canceled)

	secondCollector := commandresult.NewServiceEventMessageCollector()
	secondCtx := commandresult.WithServiceEventMessageCollector(ctx, secondCollector)
	secondDone := raiseBetaServiceEvent(secondCtx, projectConfig, serviceConfig, "predeploy")
	secondInvoke, err := stream.Recv()
	require.NoError(t, err)
	require.NotEqual(t, firstInvoke.GetRequestId(), secondInvoke.GetRequestId())

	require.NoError(t, stream.Send(betaServiceStatusMessage(
		firstInvoke.GetRequestId(),
		"predeploy",
		"api",
		"completed",
		"",
		"late canceled warning",
	)))
	require.NoError(t, stream.Send(betaServiceStatusMessage(
		secondInvoke.GetRequestId(),
		"predeploy",
		"api",
		"completed",
		"",
		"current invocation warning",
	)))
	require.NoError(t, waitBetaServiceEvent(t, ctx, secondDone))
	require.Empty(t, firstCollector.Snapshot([]string{"api"}))
	require.Equal(t, []commandresult.ServiceEventMessage{{
		ExtensionID: "test.beta.late",
		ServiceName: "api",
		EventName:   "predeploy",
		Kind:        "warning",
		Message:     "current invocation warning",
	}}, secondCollector.Snapshot([]string{"api"}))
}

func TestServer_BetaEventStreamCompletesLegacyServiceHookWithoutAck(t *testing.T) {
	projectConfig, ctx, stream := newAuthenticatedBetaEventStream(t, "test.beta.legacy.service")
	subscribeBetaServiceEvent(t, stream, "")

	serviceConfig := projectConfig.Services["api"]
	invoked := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		ticker := time.NewTicker(time.Millisecond)
		defer ticker.Stop()
		for {
			err := serviceConfig.RaiseEvent(
				ctx,
				ext.Event("predeploy"),
				project.ServiceLifecycleEventArgs{
					Project:        projectConfig,
					Service:        serviceConfig,
					ServiceContext: project.NewServiceContext(),
				},
			)
			if err != nil {
				done <- err
				return
			}
			select {
			case <-invoked:
				done <- nil
				return
			default:
			}
			select {
			case <-ctx.Done():
				done <- ctx.Err()
				return
			case <-ticker.C:
			}
		}
	}()

	invoke, err := stream.Recv()
	require.NoError(t, err)
	require.NotNil(t, invoke.GetInvokeServiceHandler())
	require.Empty(t, invoke.GetRequestId())
	close(invoked)
	require.NoError(t, stream.Send(&v1beta.EventMessage{
		MessageType: &v1beta.EventMessage_ServiceHandlerStatus{
			ServiceHandlerStatus: &v1beta.ServiceHandlerStatus{
				EventName:   "predeploy",
				ServiceName: "api",
				Status:      "completed",
			},
		},
	}))
	require.NoError(t, waitBetaServiceEvent(t, ctx, done))
}

func newAuthenticatedBetaEventStream(
	t *testing.T,
	extensionID string,
) (*project.ProjectConfig, context.Context, betaEventTestStream) {
	t.Helper()

	extension := &extensions.Extension{
		Id:           extensionID,
		Version:      "1.0.0",
		Namespace:    "test",
		Capabilities: []extensions.CapabilityType{extensions.LifecycleEventsCapability},
	}
	service, _ := createTestEventService()
	service.extensionManager = newStreamTestExtensionManager(t, extension)
	server := newServerWithEventService(service)
	serverInfo, err := server.Start()
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, server.Stop())
	})

	client, err := azdext.NewAzdClient(azdext.WithAddress(serverInfo.Address))
	require.NoError(t, err)
	t.Cleanup(func() {
		client.Close()
	})
	accessToken, err := GenerateExtensionToken(extension, serverInfo)
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	t.Cleanup(cancel)
	ctx = azdext.WithAccessToken(ctx, accessToken)
	stream, err := client.EventsBeta().EventStream(ctx)
	require.NoError(t, err)
	projectConfig, err := service.lazyProject.GetValue()
	require.NoError(t, err)
	return projectConfig, ctx, stream
}

func subscribeBetaServiceEvent(
	t *testing.T,
	stream betaEventTestStream,
	requestID string,
) {
	t.Helper()

	message := &v1beta.EventMessage{
		MessageType: &v1beta.EventMessage_SubscribeServiceEvent{
			SubscribeServiceEvent: &v1beta.SubscribeServiceEvent{
				EventNames: []string{"predeploy"},
			},
		},
	}
	message.RequestId = requestID
	require.NoError(t, stream.Send(message))
	if requestID == "" {
		return
	}

	ack, err := stream.Recv()
	require.NoError(t, err)
	require.Equal(t, requestID, ack.GetRequestId())
	require.NotNil(t, ack.GetSubscribeServiceEventResponse())
}

func raiseBetaServiceEvent(
	ctx context.Context,
	projectConfig *project.ProjectConfig,
	serviceConfig *project.ServiceConfig,
	eventName string,
) chan error {
	done := make(chan error, 1)
	go func() {
		done <- serviceConfig.RaiseEvent(
			ctx,
			ext.Event(eventName),
			project.ServiceLifecycleEventArgs{
				Project:        projectConfig,
				Service:        serviceConfig,
				ServiceContext: project.NewServiceContext(),
			},
		)
	}()
	return done
}

func waitBetaServiceEvent(t *testing.T, ctx context.Context, done <-chan error) error {
	t.Helper()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		t.Fatalf("service event did not complete: %v", ctx.Err())
		return ctx.Err()
	}
}

func betaServiceStatusMessage(
	requestID string,
	eventName string,
	serviceName string,
	statusValue string,
	statusMessage string,
	warning string,
) *v1beta.EventMessage {
	messages := []*v1beta.ServiceEventMessage(nil)
	if warning != "" {
		messages = []*v1beta.ServiceEventMessage{{
			Kind:    v1beta.ServiceEventMessageKind_SERVICE_EVENT_MESSAGE_KIND_WARNING,
			Message: warning,
		}}
	}
	return &v1beta.EventMessage{
		RequestId: requestID,
		MessageType: &v1beta.EventMessage_ServiceHandlerStatus{
			ServiceHandlerStatus: &v1beta.ServiceHandlerStatus{
				EventName:   eventName,
				ServiceName: serviceName,
				Status:      statusValue,
				Message:     statusMessage,
				Messages:    messages,
			},
		},
	}
}
