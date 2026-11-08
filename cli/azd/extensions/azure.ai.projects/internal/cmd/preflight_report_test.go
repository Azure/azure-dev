// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	v1beta "github.com/azure/azure-dev/cli/azd/pkg/azdext/contracts/v1beta"
	"github.com/azure/azure-dev/cli/azd/pkg/grpcbroker"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type preflightListenerProvider struct {
	azdext.ProvisioningProvider
	report func(string)
	text   string
}

func (*preflightListenerProvider) Initialize(context.Context, string, *azdext.ProvisioningOptions) error {
	return nil
}

func (p *preflightListenerProvider) Deploy(
	context.Context, grpcbroker.ProgressFunc,
) (*azdext.ProvisioningDeployResult, error) {
	if p.text != "" {
		p.report(p.text)
	}
	return &azdext.ProvisioningDeployResult{}, nil
}

type preflightListenerServer struct {
	v1beta.UnimplementedEventServiceServer
	v1beta.UnimplementedCommandResultServiceServer
	azdext.UnimplementedProvisioningServiceServer
	azdext.UnimplementedExtensionServiceServer
	mode        string
	ready       chan struct{}
	deployed    chan struct{}
	completed   chan *v1beta.EventMessage
	followUps   chan *v1beta.SetFollowUpRequest
	eventClosed chan struct{}
}

func (s *preflightListenerServer) Ready(context.Context, *azdext.ReadyRequest) (*azdext.ReadyResponse, error) {
	close(s.ready)
	return &azdext.ReadyResponse{}, nil
}

func (s *preflightListenerServer) SetFollowUp(
	_ context.Context, request *v1beta.SetFollowUpRequest,
) (*v1beta.SetFollowUpResponse, error) {
	s.followUps <- request
	if s.mode == "publication failure" {
		return nil, status.Error(codes.PermissionDenied, "follow-up rejected")
	}
	return &v1beta.SetFollowUpResponse{}, nil
}

func (s *preflightListenerServer) EventStream(
	stream grpc.BidiStreamingServer[v1beta.EventMessage, v1beta.EventMessage],
) error {
	defer close(s.eventClosed)
	request, err := stream.Recv()
	if err != nil {
		return err
	}
	if subscription := request.GetSubscribeProjectEvent(); subscription == nil ||
		len(subscription.GetEventNames()) != 1 || subscription.GetEventNames()[0] != "postprovision" {
		return status.Error(codes.InvalidArgument, "unexpected report subscription")
	}
	response := &v1beta.EventMessage{
		RequestId: request.GetRequestId(),
		MessageType: &v1beta.EventMessage_SubscribeProjectEventResponse{
			SubscribeProjectEventResponse: &v1beta.SubscribeProjectEventResponse{},
		},
	}
	if s.mode == "subscription failure" {
		response.Error = &v1beta.ExtensionError{Message: "subscription denied"}
	}
	if s.mode == "invalid acknowledgement" {
		response.MessageType = &v1beta.EventMessage_ProjectHandlerStatus{
			ProjectHandlerStatus: &v1beta.ProjectHandlerStatus{Status: "completed"},
		}
	}
	if err := stream.Send(response); err != nil {
		return err
	}
	if s.mode == "subscription failure" {
		<-stream.Context().Done()
		return stream.Context().Err()
	}
	if s.mode == "stream failure" {
		select {
		case <-s.ready:
			return status.Error(codes.Unavailable, "report stream disconnected")
		case <-stream.Context().Done():
			return stream.Context().Err()
		}
	}
	select {
	case <-s.deployed:
	case <-stream.Context().Done():
		return stream.Context().Err()
	}
	for index := range 2 {
		if err := stream.Send(&v1beta.EventMessage{
			RequestId: fmt.Sprintf("report-%d", index),
			MessageType: &v1beta.EventMessage_InvokeProjectHandler{
				InvokeProjectHandler: &v1beta.InvokeProjectHandler{
					EventName: "postprovision", InvocationId: fmt.Sprintf("invocation-%d", index),
				},
			},
		}); err != nil {
			return err
		}
		result, err := stream.Recv()
		if err != nil {
			return err
		}
		s.completed <- result
	}
	<-stream.Context().Done()
	return stream.Context().Err()
}

func (s *preflightListenerServer) Stream(
	stream grpc.BidiStreamingServer[azdext.ProvisioningMessage, azdext.ProvisioningMessage],
) error {
	request, err := stream.Recv()
	if err != nil {
		return err
	}
	if registration := request.GetRegisterProvisioningProviderRequest(); registration == nil ||
		registration.GetName() != "offline" {
		return status.Error(codes.InvalidArgument, "unexpected provider registration")
	}
	response := &azdext.ProvisioningMessage{
		RequestId: request.GetRequestId(),
		MessageType: &azdext.ProvisioningMessage_RegisterProvisioningProviderResponse{
			RegisterProvisioningProviderResponse: &azdext.RegisterProvisioningProviderResponse{},
		},
	}
	if s.mode == "registration failure" {
		(&azdext.ProvisioningEnvelope{}).SetError(
			response, status.Error(codes.PermissionDenied, "provider registration denied"),
		)
	}
	if err := stream.Send(response); err != nil {
		return err
	}
	if s.mode == "registration failure" {
		<-stream.Context().Done()
		return stream.Context().Err()
	}
	select {
	case <-s.ready:
	case <-stream.Context().Done():
		return stream.Context().Err()
	}
	if err := stream.Send(&azdext.ProvisioningMessage{
		RequestId: "initialize",
		MessageType: &azdext.ProvisioningMessage_InitializeRequest{
			InitializeRequest: &azdext.ProvisioningInitializeRequest{
				Options: &azdext.ProvisioningOptions{Provider: "offline"},
			},
		},
	}); err != nil {
		return err
	}
	initialized, err := stream.Recv()
	if err != nil {
		return err
	}
	if initialized.GetInitializeResponse() == nil {
		return status.Error(codes.Internal, "provider initialization failed")
	}
	if err := stream.Send(&azdext.ProvisioningMessage{
		RequestId: "deploy",
		MessageType: &azdext.ProvisioningMessage_DeployRequest{
			DeployRequest: &azdext.ProvisioningDeployRequest{ProviderName: "offline"},
		},
	}); err != nil {
		return err
	}
	deployed, err := stream.Recv()
	if err != nil {
		return err
	}
	if deployed.GetDeployResponse() == nil {
		return status.Error(codes.Internal, "provider deployment failed")
	}
	close(s.deployed)
	<-stream.Context().Done()
	return stream.Context().Err()
}

func TestPreflightReportListener(t *testing.T) {
	for _, test := range []struct {
		name, text, wantError string
	}{
		{name: "persistent report", text: "agent-pool: PASS; capacity warning\nrole-assignment-keys: SKIPPED"},
		{name: "empty report"},
		{name: "subscription failure", wantError: "subscription denied"},
		{name: "invalid acknowledgement", wantError: "invalid postprovision report subscription response"},
		{name: "stream failure", wantError: "report stream disconnected"},
		{name: "registration failure", wantError: "provider registration denied"},
		{name: "publication failure", text: "capacity warning"},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			services := &preflightListenerServer{
				mode: test.name, ready: make(chan struct{}), deployed: make(chan struct{}),
				completed: make(chan *v1beta.EventMessage, 2),
				followUps: make(chan *v1beta.SetFollowUpRequest, 2), eventClosed: make(chan struct{}),
			}
			server := grpc.NewServer()
			v1beta.RegisterEventServiceServer(server, services)
			v1beta.RegisterCommandResultServiceServer(server, services)
			azdext.RegisterProvisioningServiceServer(server, services)
			azdext.RegisterExtensionServiceServer(server, services)
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			require.NoError(t, err)
			served := make(chan error, 1)
			go func() { served <- server.Serve(listener) }()
			t.Cleanup(func() {
				server.Stop()
				require.NoError(t, <-served)
			})
			t.Setenv("AZD_SERVER", listener.Addr().String())
			t.Setenv("AZD_EXTENSION_ID", "azure.ai.projects")
			command := newListenCommand(func(host *azdext.ExtensionHost, report func(string)) {
				host.WithProvisioningProvider("offline", func() azdext.ProvisioningProvider {
					return &preflightListenerProvider{report: report, text: test.text}
				})
			})
			command.SilenceErrors, command.SilenceUsage = true, true
			var commandError error
			finished := make(chan struct{})
			go func() {
				commandError = command.ExecuteContext(ctx)
				close(finished)
			}()
			t.Cleanup(func() {
				cancel()
				select {
				case <-finished:
				case <-time.After(10 * time.Second):
					t.Error("listener did not shut down")
				}
			})
			if test.wantError == "" {
				for index := range 2 {
					select {
					case result := <-services.completed:
						require.Equal(t, fmt.Sprintf("report-%d", index), result.GetRequestId())
						handler := result.GetProjectHandlerStatus()
						require.NotNil(t, handler)
						if test.name == "publication failure" && index == 0 {
							require.Equal(t, "failed", handler.GetStatus())
							require.Contains(t, handler.GetMessage(), "follow-up rejected")
							require.NotNil(t, handler.GetError())
						} else {
							require.Equal(t, "completed", handler.GetStatus())
						}
					case <-finished:
						t.Fatalf("listener exited before publishing reports: %v", commandError)
					case <-ctx.Done():
						t.Fatal("listener did not deliver reports")
					}
				}
				if test.text == "" {
					require.Empty(t, services.followUps)
				} else {
					require.Len(t, services.followUps, 1, "the next invocation must not repeat the report")
					followUp := <-services.followUps
					require.Equal(t, "invocation-0", followUp.GetInvocationId())
					require.Equal(t, "BYO-AKS preflight report:\n"+test.text, followUp.GetText())
				}
				cancel()
			}
			select {
			case <-finished:
			case <-time.After(5 * time.Second):
				t.Fatal("listener did not finish")
			}
			if test.wantError != "" {
				require.ErrorContains(t, commandError, test.wantError)
			} else {
				require.NoError(t, commandError)
			}
			select {
			case <-services.eventClosed:
			case <-time.After(5 * time.Second):
				t.Fatal("report event stream remained open after listener shutdown")
			}
		})
	}
}
