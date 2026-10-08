// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package grpcserver

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	v1beta "github.com/azure/azure-dev/cli/azd/pkg/azdext/contracts/v1beta"
	"github.com/azure/azure-dev/cli/azd/pkg/ext"
	"github.com/azure/azure-dev/cli/azd/pkg/extensions"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockinput"
	"github.com/stretchr/testify/require"
)

func TestServer_BetaEventStreamRetainsDeployHookOutput(t *testing.T) {
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
		MessageType: &v1beta.EventMessage_HandlerOutput{
			HandlerOutput: &v1beta.HandlerOutput{Output: "project RBAC warning\n"},
		},
	}))
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
	serviceDone := make(chan error, 1)
	go func() {
		serviceDone <- serviceConfig.RaiseEvent(
			ctx,
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
		MessageType: &v1beta.EventMessage_HandlerOutput{
			HandlerOutput: &v1beta.HandlerOutput{Output: "service RBAC warning\n"},
		},
	}))
	require.NoError(t, stream.Send(&v1beta.EventMessage{
		RequestId: serviceInvoke.GetRequestId(),
		MessageType: &v1beta.EventMessage_ServiceHandlerStatus{
			ServiceHandlerStatus: &v1beta.ServiceHandlerStatus{
				EventName:   "predeploy",
				ServiceName: "api",
				Status:      "completed",
			},
		},
	}))
	require.NoError(t, <-serviceDone)

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
	require.NoError(t, stream.Send(&v1beta.EventMessage{
		RequestId: disconnectedInvoke.GetRequestId(),
		MessageType: &v1beta.EventMessage_HandlerOutput{
			HandlerOutput: &v1beta.HandlerOutput{Output: "disconnected hook warning\n"},
		},
	}))
	require.NoError(t, stream.CloseSend())
	require.Error(t, <-disconnectedDone)

	console := service.console.(*mockinput.MockConsole)
	output := strings.Join(console.Output(), "\n")
	require.Contains(t, output, "project RBAC warning")
	require.Contains(t, output, "service RBAC warning")
	require.Contains(t, output, "disconnected hook warning")
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
				ctx,
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

	for _, part := range []string{"first", "second"} {
		for index, invocation := range invocations {
			require.NoError(t, stream.Send(&v1beta.EventMessage{
				RequestId: invocation.GetRequestId(),
				MessageType: &v1beta.EventMessage_HandlerOutput{
					HandlerOutput: &v1beta.HandlerOutput{
						Output: names[index] + " " + part + " warning\n",
					},
				},
			}))
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

	console := service.console.(*mockinput.MockConsole)
	require.ElementsMatch(t, []string{
		"api first warning\napi second warning",
		"web first warning\nweb second warning",
	}, console.Output())
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
