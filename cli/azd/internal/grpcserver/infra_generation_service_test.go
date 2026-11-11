// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package grpcserver

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	v1beta "github.com/azure/azure-dev/cli/azd/pkg/azdext/contracts/v1beta"
	"github.com/azure/azure-dev/cli/azd/pkg/extensions"
	"github.com/azure/azure-dev/cli/azd/pkg/ioc"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/structpb"
)

func generationExtension() *extensions.Extension {
	return &extensions.Extension{
		Id: "test.generation", Version: "1.0.0",
		Capabilities: []extensions.CapabilityType{extensions.InfraGenerationProviderCapability},
		Providers:    []extensions.Provider{{Name: "resources", Type: extensions.InfraGenerationProviderType}},
	}
}

func TestInfraGenerationServiceRegistersAndCleansUp(t *testing.T) {
	extension := generationExtension()
	service := NewInfraGenerationService(ioc.NewNestedContainer(nil),
		newStreamTestExtensionManager(t, extension)).(*InfraGenerationService)
	stream := newRegisterStream(extensionClaimsContext(t.Context(), extension.Id), &v1beta.InfraGenerationMessage{
		RequestId: "register",
		MessageType: &v1beta.InfraGenerationMessage_RegisterRequest{
			RegisterRequest: &v1beta.RegisterInfraGenerationProviderRequest{Name: "resources"},
		},
	})
	require.NoError(t, service.Stream(stream))
	require.NotNil(t, stream.response.GetRegisterResponse())
	require.Equal(t, "register", stream.response.RequestId)
	require.Empty(t, service.providers.owners)
}

func TestInfraGenerationServiceRequiresCapability(t *testing.T) {
	extension := generationExtension()
	extension.Capabilities = []extensions.CapabilityType{extensions.ProvisioningProviderCapability}
	service := NewInfraGenerationService(ioc.NewNestedContainer(nil), newStreamTestExtensionManager(t, extension))
	stream := newRegisterStream(extensionClaimsContext(t.Context(), extension.Id), &v1beta.InfraGenerationMessage{})
	err := service.Stream(stream)
	require.Equal(t, codes.PermissionDenied, status.Code(err))
}

func TestInfraGenerationServiceRejectsUndeclaredProvider(t *testing.T) {
	extension := generationExtension()
	service := NewInfraGenerationService(ioc.NewNestedContainer(nil), newStreamTestExtensionManager(t, extension))
	stream := newRegisterStream(extensionClaimsContext(t.Context(), extension.Id), &v1beta.InfraGenerationMessage{
		MessageType: &v1beta.InfraGenerationMessage_RegisterRequest{
			RegisterRequest: &v1beta.RegisterInfraGenerationProviderRequest{Name: "another-extension"},
		},
	})
	require.NoError(t, service.Stream(stream))
	err := (&azdext.InfraGenerationEnvelope{}).GetError(stream.response)
	require.ErrorContains(t, err, "does not declare")
}

func TestInfraGenerationServiceCleansUpReceiveFailure(t *testing.T) {
	extension := generationExtension()
	service := NewInfraGenerationService(ioc.NewNestedContainer(nil),
		newStreamTestExtensionManager(t, extension)).(*InfraGenerationService)
	failure := errors.New("stream failed")
	stream := &generationRegistrationFailureStream{
		registerStream: newRegisterStream(extensionClaimsContext(t.Context(), extension.Id),
			&v1beta.InfraGenerationMessage{
				MessageType: &v1beta.InfraGenerationMessage_RegisterRequest{
					RegisterRequest: &v1beta.RegisterInfraGenerationProviderRequest{Name: "resources"},
				},
			}),
		failure: failure,
	}
	err := service.Stream(stream)
	require.ErrorIs(t, err, failure)
	require.NotNil(t, stream.response.GetRegisterResponse())
	require.Empty(t, service.providers.owners)
}

type generationRegistrationFailureStream struct {
	*registerStream[v1beta.InfraGenerationMessage]
	failure error
}

func (s *generationRegistrationFailureStream) Recv() (*v1beta.InfraGenerationMessage, error) {
	if !s.received {
		return s.registerStream.Recv()
	}
	<-s.responseSent
	return nil, s.failure
}

func TestInfraGenerationServiceRejectsEmptyProviderName(t *testing.T) {
	extension := generationExtension()
	service := NewInfraGenerationService(ioc.NewNestedContainer(nil),
		newStreamTestExtensionManager(t, extension)).(*InfraGenerationService)
	stream := newRegisterStream(extensionClaimsContext(t.Context(), extension.Id), &v1beta.InfraGenerationMessage{
		MessageType: &v1beta.InfraGenerationMessage_RegisterRequest{
			RegisterRequest: &v1beta.RegisterInfraGenerationProviderRequest{Name: " "},
		},
	})

	require.NoError(t, service.Stream(stream))
	err := (&azdext.InfraGenerationEnvelope{}).GetError(stream.response)
	require.ErrorContains(t, err, "infrastructure generator name is required")
	require.Empty(t, service.providers.owners)
}

type generationReadyService struct {
	azdext.UnimplementedExtensionServiceServer
	ready chan struct{}
}

func (s *generationReadyService) Ready(context.Context, *azdext.ReadyRequest) (*azdext.ReadyResponse, error) {
	close(s.ready)
	return &azdext.ReadyResponse{}, nil
}

type recordingInfrastructureGenerator struct {
	requests chan *v1beta.InfraGenerateRequest
}

func (g *recordingInfrastructureGenerator) Generate(
	_ context.Context, request *v1beta.InfraGenerateRequest,
) (*v1beta.InfraGenerateResponse, error) {
	g.requests <- proto.Clone(request).(*v1beta.InfraGenerateRequest)
	return &v1beta.InfraGenerateResponse{Entry: &v1beta.ProvisioningOptions{
		Provider: "terraform", Path: "infra/data", Config: request.Entry.Config,
	}}, nil
}

func TestInfraGenerationServiceRoutesThroughBetaSDK(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	extension := generationExtension()
	container := ioc.NewNestedContainer(nil)
	ready := &generationReadyService{ready: make(chan struct{})}
	server := newTestServer(azdext.UnimplementedContainerServiceServer{}, ready)
	server.infraGenerationService = NewInfraGenerationService(container, newStreamTestExtensionManager(t, extension))
	info, err := server.Start()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, server.Stop()) })
	services := server.grpcServer.GetServiceInfo()
	require.Contains(t, services, "azd.extensions.v1beta.InfraGenerationService")
	require.NotContains(t, services, "azd.extensions.v1.InfraGenerationService")
	token, err := GenerateExtensionToken(extension, info)
	require.NoError(t, err)
	client, err := azdext.NewAzdClient(azdext.WithAddress(info.Address))
	require.NoError(t, err)
	t.Cleanup(client.Close)
	provider := &recordingInfrastructureGenerator{
		requests: make(chan *v1beta.InfraGenerateRequest, 1),
	}
	host := azdext.NewExtensionHost(client).WithInfraGenerationProvider("resources", provider)
	hostCtx, stopHost := context.WithCancel(azdext.WithAccessToken(ctx, token))
	done := make(chan error, 1)
	go func() { done <- host.Run(hostCtx) }()
	t.Cleanup(func() {
		stopHost()
		select {
		case err := <-done:
			require.NoError(t, err)
		case <-time.After(5 * time.Second):
			t.Error("generation host did not stop")
		}
	})
	select {
	case <-ready.ready:
	case <-ctx.Done():
		t.Fatal("generation host did not become ready")
	}
	var generator project.InfrastructureGenerator
	require.NoError(t, container.ResolveNamed(project.InfraGenerationProviderKey("resources"), &generator))
	request := &v1beta.InfraGenerateRequest{
		ProjectPath: "/project", OutputPath: "infra/data",
		Entry: &v1beta.ProvisioningOptions{Provider: "resources", Name: "data", Config: &structpb.Struct{
			Fields: map[string]*structpb.Value{"backend": structpb.NewStringValue("custom")},
		}},
		Options: &v1beta.InfraGenerateOptions{Force: true},
	}
	result, err := generator.Generate(ctx, request)
	require.NoError(t, err)
	require.Equal(t, "terraform", result.Entry.Provider)
	require.Equal(t, "infra/data", result.Entry.Path)
	require.True(t, proto.Equal(request.Entry.Config, result.Entry.Config))
	require.True(t, proto.Equal(request, <-provider.requests))

	request.Options = nil
	_, err = generator.Generate(ctx, request)
	require.ErrorContains(t, err, "non-nil options")
	require.Empty(t, provider.requests, "missing options must not reach the provider")
}
