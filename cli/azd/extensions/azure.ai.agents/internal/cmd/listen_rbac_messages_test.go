// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"errors"
	"io"
	"net"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"azureaiagent/internal/featureflags"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	v1beta "github.com/azure/azure-dev/cli/azd/pkg/azdext/contracts/v1beta"
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
	responses     chan *azdext.EventMessage
	streams       chan azdext.EventService_EventStreamServer
}

func (s *rbacMessageStableEvents) EventStream(stream azdext.EventService_EventStreamServer) error {
	select {
	case s.streams <- stream:
	case <-stream.Context().Done():
		return stream.Context().Err()
	}
	for {
		message, err := stream.Recv()
		if err != nil {
			return err
		}
		messages := s.registrations
		if message.GetServiceHandlerStatus() != nil {
			messages = s.responses
		}
		select {
		case messages <- message:
		case <-stream.Context().Done():
			return stream.Context().Err()
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

func TestAgentRBACMessages(t *testing.T) {
	for _, tt := range []struct {
		name    string
		value   *string
		enabled bool
	}{
		{name: "default"},
		{name: "disabled", value: new("false")},
		{name: "empty", value: new("")},
		{name: "invalid", value: new("on")},
		{name: "enabled", value: new("true"), enabled: true},
		{name: "enabled alias", value: new("1"), enabled: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			setPreviewProcess(t, "true")
			if tt.value == nil {
				require.NoError(t, os.Unsetenv(featureflags.RBACWarnings.EnvVar))
			} else {
				t.Setenv(featureflags.RBACWarnings.EnvVar, *tt.value)
			}
			testAgentRBACMessages(t, tt.enabled)
		})
	}
}

func testAgentRBACMessages(t *testing.T, betaEnabled bool) {
	t.Helper()
	t.Setenv("NO_COLOR", "1")
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
	stable := &rbacMessageStableEvents{
		registrations: make(chan *azdext.EventMessage, 10),
		responses:     make(chan *azdext.EventMessage, 1),
		streams:       make(chan azdext.EventService_EventStreamServer, 1),
	}
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
	if betaEnabled {
		v1beta.RegisterEventServiceServer(server, beta)
	}
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
	if betaEnabled {
		select {
		case registration := <-beta.registrations:
			require.Equal(t, []string{"predeploy"}, registration.GetEventNames())
			require.Equal(t, AiAgentHost, registration.GetHost())
			require.Empty(t, registration.GetLanguage())
		case <-ctx.Done():
			t.Fatal("missing beta registration")
		}
	}
	require.Empty(t, beta.registrations)
	var stableStream azdext.EventService_EventStreamServer
	select {
	case stableStream = <-stable.streams:
	case <-ctx.Done():
		t.Fatal("missing stable stream")
	}
	stableRegistrations := 7
	wantServiceEvents := []string{"prebuild", "prepackage", "postdeploy"}
	if !betaEnabled {
		stableRegistrations++
		wantServiceEvents = append(wantServiceEvents, "predeploy")
	}
	var projectEvents, serviceEvents []string
	for range stableRegistrations {
		select {
		case registration := <-stable.registrations:
			if service := registration.GetSubscribeServiceEvent(); service != nil {
				serviceEvents = append(serviceEvents, service.GetEventNames()...)
				require.Equal(t, AiAgentHost, service.GetHost())
				require.Empty(t, service.GetLanguage())
			} else {
				projectEvents = append(projectEvents, registration.GetSubscribeProjectEvent().GetEventNames()...)
			}
		case <-ctx.Done():
			t.Fatal("missing stable registration")
		}
	}
	require.ElementsMatch(t, []string{"preprovision", "postprovision", "predown", "postdown"}, projectEvents)
	require.ElementsMatch(t, wantServiceEvents, serviceEvents)
	require.Empty(t, stable.registrations)
	stdout, stderr, err := captureStdoutAndStderr(t, func() error {
		for _, serviceName := range []string{"assistant", "second-assistant", "invalid-assistant"} {
			wireService := &azdext.ServiceConfig{}
			proto.Merge(wireService, svc)
			wireService.Name = serviceName
			invalid := serviceName == "invalid-assistant"
			if invalid {
				wireService.AdditionalProperties = mustStruct(t, map[string]any{"kind": "invalid"})
			}
			requestID := "predeploy-" + serviceName
			request := &azdext.EventMessage{
				MessageType: &azdext.EventMessage_InvokeServiceHandler{
					InvokeServiceHandler: &azdext.InvokeServiceHandler{
						EventName: "predeploy", Service: wireService,
						Project: &azdext.ProjectConfig{Path: project.Path, Services: map[string]*azdext.ServiceConfig{
							serviceName: wireService,
						}},
					},
				},
			}
			if betaEnabled {
				betaRequest := &v1beta.EventMessage{}
				data, err := proto.Marshal(request)
				require.NoError(t, err)
				require.NoError(t, proto.Unmarshal(data, betaRequest))
				betaRequest.RequestId = requestID
				beta.requests <- betaRequest
				select {
				case wireResponse := <-beta.responses:
					require.Equal(t, requestID, wireResponse.GetRequestId())
					result := wireResponse.GetServiceHandlerStatus()
					require.Equal(t, "predeploy", result.GetEventName())
					require.Equal(t, serviceName, result.GetServiceName())
					if invalid {
						require.NotNil(t, result.GetError())
						require.Empty(t, result.GetMessages())
						continue
					}
					require.Equal(t, "completed", result.GetStatus())
					require.Nil(t, result.GetError())
					require.Len(t, result.GetMessages(), 1)
					require.Equal(t, v1beta.ServiceEventMessageKind_SERVICE_EVENT_MESSAGE_KIND_WARNING,
						result.Messages[0].GetKind())
					require.Equal(t, message.Message, result.Messages[0].GetMessage())
					require.Equal(t, message.Suggestion, result.Messages[0].GetSuggestion())
				case <-ctx.Done():
					t.Fatal("beta predeploy invocation did not complete")
				}
			} else {
				require.NoError(t, stableStream.Send(request))
				select {
				case wireResponse := <-stable.responses:
					result := wireResponse.GetServiceHandlerStatus()
					require.Equal(t, "predeploy", result.GetEventName())
					require.Equal(t, serviceName, result.GetServiceName())
					if invalid {
						require.NotNil(t, result.GetError())
						continue
					}
					require.Equal(t, "completed", result.GetStatus())
					require.Nil(t, result.GetError())
				case <-ctx.Done():
					t.Fatal("stable predeploy invocation did not complete")
				}
			}
		}
		return nil
	})
	require.NoError(t, err)
	if betaEnabled {
		require.NotContains(t, stdout, message.Message)
		require.NotContains(t, stdout, message.Suggestion)
		require.NotContains(t, stderr, message.Message)
		require.NotContains(t, stderr, message.Suggestion)
	} else {
		require.Equal(t, 2, strings.Count(stdout, message.Message))
		require.Equal(t, 2, strings.Count(stdout, message.Suggestion))
		require.NotContains(t, stderr, message.Message)
		require.NotContains(t, stderr, message.Suggestion)
	}
	for _, sensitive := range []string{"example.invalid", "sig=secret", "fragment"} {
		require.NotContains(t, stdout, sensitive)
		require.NotContains(t, stderr, sensitive)
	}
	require.Equal(t, int32(3), account.calls.Load())
	require.Empty(t, beta.registrations)
	require.Empty(t, beta.responses)
	require.Empty(t, stable.responses)
}

func captureStdoutAndStderr(t *testing.T, run func() error) (string, string, error) {
	t.Helper()

	oldStdout := os.Stdout
	oldStderr := os.Stderr
	stdoutReader, stdoutWriter, err := os.Pipe()
	require.NoError(t, err)
	stderrReader, stderrWriter, err := os.Pipe()
	require.NoError(t, err)
	defer func() {
		os.Stdout = oldStdout
		os.Stderr = oldStderr
	}()
	defer stdoutReader.Close()
	defer stderrReader.Close()

	os.Stdout = stdoutWriter
	os.Stderr = stderrWriter
	runErr := run()
	require.NoError(t, stdoutWriter.Close())
	require.NoError(t, stderrWriter.Close())

	stdoutOutput, err := io.ReadAll(stdoutReader)
	require.NoError(t, err)
	stderrOutput, err := io.ReadAll(stderrReader)
	require.NoError(t, err)

	return string(stdoutOutput), string(stderrOutput), runErr
}
