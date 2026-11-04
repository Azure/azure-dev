// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"errors"
	"io"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"azureaiagent/internal/featureflags"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	v1beta "github.com/azure/azure-dev/cli/azd/pkg/azdext/contracts/v1beta"
	foundryTelemetry "github.com/azure/azure-dev/cli/azd/pkg/foundry/telemetry"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

type rbacMessageAccountServer struct {
	azdext.UnimplementedAccountServiceServer
	calls atomic.Int32
}

func (*rbacMessageAccountServer) LookupTenant(
	context.Context, *azdext.LookupTenantRequest,
) (*azdext.LookupTenantResponse, error) {
	return &azdext.LookupTenantResponse{TenantId: "organizations"}, nil
}

func (s *rbacMessageAccountServer) GetCurrentPrincipal(
	context.Context, *azdext.GetCurrentPrincipalRequest,
) (*azdext.GetCurrentPrincipalResponse, error) {
	s.calls.Add(1)
	return nil, status.Error(codes.PermissionDenied, "https://user:password@example.invalid/?sig=secret#fragment")
}

type rbacMessageStableEvents struct {
	azdext.UnimplementedEventServiceServer
	registrations chan *azdext.EventMessage
	requests      chan *azdext.EventMessage
	responses     chan *azdext.EventMessage
}

func (s *rbacMessageStableEvents) EventStream(stream azdext.EventService_EventStreamServer) error {
	if s.requests != nil {
		return s.interactiveStream(stream)
	}
	for {
		message, err := stream.Recv()
		if err != nil {
			return err
		}
		select {
		case s.registrations <- message:
		case <-stream.Context().Done():
			return stream.Context().Err()
		}
	}
}

func (s *rbacMessageStableEvents) interactiveStream(stream azdext.EventService_EventStreamServer) error {
	received := make(chan *azdext.EventMessage)
	go func() {
		defer close(received)
		for {
			message, err := stream.Recv()
			if err != nil {
				return
			}
			select {
			case received <- message:
			case <-stream.Context().Done():
				return
			}
		}
	}()
	for {
		select {
		case <-stream.Context().Done():
			return stream.Context().Err()
		case request := <-s.requests:
			if err := stream.Send(request); err != nil {
				return err
			}
		case response, ok := <-received:
			if !ok {
				return nil
			}
			target := s.responses
			if response.GetSubscribeProjectEvent() != nil || response.GetSubscribeServiceEvent() != nil {
				target = s.registrations
			}
			select {
			case target <- response:
			case <-stream.Context().Done():
				return stream.Context().Err()
			}
		}
	}
}

type rbacMessageBetaEvents struct {
	v1beta.UnimplementedEventServiceServer
	registrations chan *v1beta.SubscribeServiceEvent
	requests      chan *v1beta.EventMessage
	responses     chan *v1beta.EventMessage
}

func (s *rbacMessageBetaEvents) EventStream(stream v1beta.EventService_EventStreamServer) error {
	subscribe, err := stream.Recv()
	if err != nil {
		return err
	}
	if err := stream.Send(&v1beta.EventMessage{
		RequestId: subscribe.GetRequestId(),
		MessageType: &v1beta.EventMessage_SubscribeServiceEventResponse{
			SubscribeServiceEventResponse: &v1beta.SubscribeServiceEventResponse{},
		},
	}); err != nil {
		return err
	}
	s.registrations <- subscribe.GetSubscribeServiceEvent()
	for {
		select {
		case <-stream.Context().Done():
			return stream.Context().Err()
		case request := <-s.requests:
			if err := stream.Send(request); err != nil {
				return err
			}
			response, err := stream.Recv()
			if err != nil {
				return err
			}
			select {
			case s.responses <- response:
			case <-stream.Context().Done():
				return stream.Context().Err()
			}
		}
	}
}

type rbacMessageTargets struct {
	azdext.UnimplementedServiceTargetServiceServer
}

func (*rbacMessageTargets) Stream(stream azdext.ServiceTargetService_StreamServer) error {
	for {
		request, err := stream.Recv()
		if err != nil {
			return err
		}
		if err := stream.Send(&azdext.ServiceTargetMessage{
			RequestId: request.GetRequestId(),
			MessageType: &azdext.ServiceTargetMessage_RegisterServiceTargetResponse{
				RegisterServiceTargetResponse: &azdext.RegisterServiceTargetResponse{},
			},
		}); err != nil {
			return err
		}
	}
}

type rbacMessageExtension struct {
	azdext.UnimplementedExtensionServiceServer
	ready chan struct{}
}

func (s *rbacMessageExtension) Ready(
	context.Context, *azdext.ReadyRequest,
) (*azdext.ReadyResponse, error) {
	close(s.ready)
	return &azdext.ReadyResponse{}, nil
}

func TestAgentListenerSnapshotReporting(t *testing.T) {
	t.Setenv(featureflags.MemorySnapshot.EnvVar, "true")
	t.Setenv("AGENT_DEFINITION_PATH", "")
	service := &azdext.ServiceConfig{
		Name: "worker", Host: AiAgentHost, RelativePath: ".",
		AdditionalProperties: mustStruct(t, map[string]any{
			"kind": "hosted", "name": "worker", "enableMemorySnapshot": true,
			"protocols": []any{map[string]any{"protocol": "responses", "version": "1.0.0"}},
		}),
		Environment: map[string]string{"INVALID-NAME": "value"},
	}
	config := &azdext.ProjectConfig{Path: t.TempDir(), Services: map[string]*azdext.ServiceConfig{"worker": service}}
	env := &testEnvironmentServiceServer{
		current: &azdext.Environment{Name: "test"},
		values:  map[string]map[string]string{"test": {"AZURE_SUBSCRIPTION_ID": "sub"}},
	}
	stable := &rbacMessageStableEvents{
		registrations: make(chan *azdext.EventMessage, 8), requests: make(chan *azdext.EventMessage, 1),
		responses: make(chan *azdext.EventMessage, 2),
	}
	beta := &rbacMessageBetaEvents{
		registrations: make(chan *v1beta.SubscribeServiceEvent, 2),
		requests:      make(chan *v1beta.EventMessage, 1), responses: make(chan *v1beta.EventMessage, 1),
	}
	extension := &rbacMessageExtension{ready: make(chan struct{})}
	recorder := &containerModeRPCRecorder{}
	server := grpc.NewServer()
	azdext.RegisterProjectServiceServer(server, &helpersProjectServer{project: config})
	azdext.RegisterEnvironmentServiceServer(server, env)
	azdext.RegisterAccountServiceServer(server, &rbacMessageAccountServer{})
	azdext.RegisterEventServiceServer(server, stable)
	v1beta.RegisterEventServiceServer(server, beta)
	azdext.RegisterServiceTargetServiceServer(server, &rbacMessageTargets{})
	azdext.RegisterExtensionServiceServer(server, extension)
	azdext.RegisterTelemetryServiceServer(server, recorder)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)
	t.Setenv("AZD_SERVER", listener.Addr().String())
	t.Setenv("AZD_ACCESS_TOKEN", "test-token")
	configured := make(chan *azdext.ExtensionHost, 1)
	command := newAgentListenCommand(func(host *azdext.ExtensionHost, reporter foundryTelemetry.Reporter) {
		configureExtensionHostWithTelemetry(host, newAgentContextReporter(), reporter)
		configured <- host
	})
	command.SetArgs([]string{})
	command.SetOut(io.Discard)
	command.SetErr(io.Discard)
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = command.ExecuteContext(ctx)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("listener did not stop")
		}
	})
	select {
	case <-extension.ready:
	case <-done:
		t.Fatal("listener exited before registration")
	case <-ctx.Done():
		t.Fatal("listener did not register")
	}
	host := <-configured
	targets := host.ServiceTargets()
	require.Len(t, targets, 1)
	_, err = targets[0].Factory().Deploy(ctx, service, nil, nil, func(string) {})
	require.ErrorContains(t, err, "INVALID-NAME")
	stable.requests <- &azdext.EventMessage{
		MessageType: &azdext.EventMessage_InvokeProjectHandler{
			InvokeProjectHandler: &azdext.InvokeProjectHandler{EventName: "postdeploy", Project: config},
		},
	}
	for {
		select {
		case response := <-stable.responses:
			if result := response.GetProjectHandlerStatus(); result != nil && result.GetStatus() == "completed" {
				require.Nil(t, result.GetError())
				requests := recorder.snapshot()
				require.Len(t, requests, 1)
				require.Equal(t, "agent.snapshot.deploy.completed", requests[0].EventName)
				require.Equal(t, "failed", requests[0].Attributes["agent.deploy.outcome"])
				server.Stop()
				return
			}
		case <-ctx.Done():
			t.Fatal("project postdeploy did not finish")
		}
	}
}

func TestAgentRBACMessages(t *testing.T) {
	t.Setenv("AZD_AGENT_SKIP_ROLE_ASSIGNMENTS", "")
	t.Setenv("AGENT_DEFINITION_PATH", "")
	t.Setenv("AZD_NO_PROMPT", "true")
	env := &testEnvironmentServiceServer{
		current: &azdext.Environment{Name: "dev"},
		values: map[string]map[string]string{"dev": {
			"AZURE_AI_PROJECT_ID": "/subscriptions/sub/resourceGroups/rg/providers/" +
				"Microsoft.CognitiveServices/accounts/account/projects/project",
		}},
	}
	account := &rbacMessageAccountServer{}
	stable := &rbacMessageStableEvents{registrations: make(chan *azdext.EventMessage, 8)}
	beta := &rbacMessageBetaEvents{
		registrations: make(chan *v1beta.SubscribeServiceEvent, 2),
		requests:      make(chan *v1beta.EventMessage, 1),
		responses:     make(chan *v1beta.EventMessage, 1),
	}
	extension := &rbacMessageExtension{ready: make(chan struct{})}
	server := grpc.NewServer()
	azdext.RegisterEnvironmentServiceServer(server, env)
	azdext.RegisterAccountServiceServer(server, account)
	azdext.RegisterEventServiceServer(server, stable)
	v1beta.RegisterEventServiceServer(server, beta)
	azdext.RegisterServiceTargetServiceServer(server, &rbacMessageTargets{})
	azdext.RegisterExtensionServiceServer(server, extension)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	serverDone := make(chan error, 1)
	go func() { serverDone <- server.Serve(listener) }()
	t.Cleanup(func() {
		server.Stop()
		if serveErr := <-serverDone; serveErr != nil {
			require.ErrorIs(t, serveErr, grpc.ErrServerStopped)
		}
	})
	client, err := azdext.NewAzdClient(azdext.WithAddress(listener.Addr().String()))
	require.NoError(t, err)
	t.Cleanup(client.Close)
	svc := &azdext.ServiceConfig{
		Name: "assistant", Host: AiAgentHost,
		AdditionalProperties: mustStruct(t, map[string]any{
			"kind": "prompt", "name": "assistant", "model": "gpt-4.1", "instructions": "Help the user.",
		}),
	}
	project := &azdext.ProjectConfig{
		Path: t.TempDir(), Services: map[string]*azdext.ServiceConfig{"assistant": svc},
	}
	response, err := predeployHandler(t.Context(), client, &azdext.ServiceEventArgs{
		Project: project, Service: svc,
	})
	require.NoError(t, err)
	require.Len(t, response.Messages, 1)
	message := response.Messages[0]
	require.Equal(t, azdext.BetaServiceEventMessageWarning, message.Kind)
	require.Equal(t,
		"Could not resolve current principal: PermissionDenied; skipping RBAC pre-flight check.", message.Message)
	require.NotEmpty(t, message.Suggestion)

	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	host := azdext.NewExtensionHost(client)
	configureExtensionHost(host)
	hostDone := make(chan error, 1)
	go func() { hostDone <- host.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-hostDone:
			require.True(t, err == nil || status.Code(err) == codes.Canceled || errors.Is(err, io.EOF), "%v", err)
		case <-time.After(5 * time.Second):
			t.Error("extension host did not stop")
		}
	})
	select {
	case <-extension.ready:
	case err := <-hostDone:
		t.Fatalf("extension host exited before ready: %v", err)
	case <-ctx.Done():
		t.Fatal("extension host did not register")
	}
	registration := <-beta.registrations
	require.Equal(t, []string{"predeploy"}, registration.GetEventNames())
	require.Equal(t, AiAgentHost, registration.GetHost())
	require.Empty(t, registration.GetLanguage())
	require.Empty(t, beta.registrations)
	var projectEvents []string
	for range 6 {
		select {
		case registration := <-stable.registrations:
			if service := registration.GetSubscribeServiceEvent(); service != nil {
				require.Equal(t, []string{"postdeploy"}, service.GetEventNames())
				require.Equal(t, AiAgentHost, service.GetHost())
			} else {
				projectEvents = append(projectEvents, registration.GetSubscribeProjectEvent().GetEventNames()...)
			}
		case <-ctx.Done():
			t.Fatal("missing stable registration")
		}
	}
	require.ElementsMatch(t, []string{"preprovision", "postprovision", "postdeploy", "predown", "postdown"}, projectEvents)
	require.Empty(t, stable.registrations)
	for _, serviceName := range []string{"assistant", "second-assistant"} {
		wireService := &v1beta.ServiceConfig{}
		data, err := proto.Marshal(svc)
		require.NoError(t, err)
		require.NoError(t, proto.Unmarshal(data, wireService))
		wireService.Name = serviceName
		requestID := "predeploy-" + serviceName
		beta.requests <- &v1beta.EventMessage{
			RequestId: requestID,
			MessageType: &v1beta.EventMessage_InvokeServiceHandler{
				InvokeServiceHandler: &v1beta.InvokeServiceHandler{
					EventName: "predeploy", Service: wireService,
					Project: &v1beta.ProjectConfig{Path: project.Path, Services: map[string]*v1beta.ServiceConfig{
						serviceName: wireService,
					}},
				},
			},
		}
		select {
		case wireResponse := <-beta.responses:
			require.Equal(t, requestID, wireResponse.GetRequestId())
			result := wireResponse.GetServiceHandlerStatus()
			require.Equal(t, "predeploy", result.GetEventName())
			require.Equal(t, serviceName, result.GetServiceName())
			require.Equal(t, "completed", result.GetStatus())
			require.Nil(t, result.GetError())
			require.Len(t, result.GetMessages(), 1)
			require.Equal(t, v1beta.ServiceEventMessageKind_SERVICE_EVENT_MESSAGE_KIND_WARNING,
				result.Messages[0].GetKind())
			require.Equal(t, message.Message, result.Messages[0].GetMessage())
			require.Equal(t, message.Suggestion, result.Messages[0].GetSuggestion())
		case <-ctx.Done():
			t.Fatal("predeploy invocation did not complete")
		}
	}
	require.Equal(t, int32(3), account.calls.Load())
	require.Empty(t, beta.responses)
}
