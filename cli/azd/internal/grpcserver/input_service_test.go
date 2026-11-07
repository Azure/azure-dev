// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package grpcserver

import (
	"context"
	"errors"
	"io"
	"log"
	"slices"
	"testing"
	"time"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	v1beta "github.com/azure/azure-dev/cli/azd/pkg/azdext/contracts/v1beta"
	"github.com/azure/azure-dev/cli/azd/pkg/extensions"
	"github.com/azure/azure-dev/cli/azd/pkg/grpcbroker"
	"github.com/azure/azure-dev/cli/azd/pkg/inputs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

type inputReadyService struct {
	azdext.UnimplementedExtensionServiceServer
	ready chan struct{}
}

func (s *inputReadyService) Ready(context.Context, *azdext.ReadyRequest) (*azdext.ReadyResponse, error) {
	close(s.ready)
	return &azdext.ReadyResponse{}, nil
}

func startInputTestHost(t *testing.T, handler azdext.BetaInputHandler) *InputService {
	t.Helper()
	extension := &extensions.Extension{
		Id: "test.inputs", Capabilities: []extensions.CapabilityType{extensions.InputProviderCapability},
	}
	service := NewInputService(newStreamTestExtensionManager(t, extension))
	ready := &inputReadyService{ready: make(chan struct{})}
	server := NewServer(
		azdext.UnimplementedProjectServiceServer{}, azdext.UnimplementedEnvironmentServiceServer{},
		azdext.UnimplementedPromptServiceServer{}, azdext.UnimplementedUserConfigServiceServer{},
		azdext.UnimplementedDeploymentServiceServer{}, azdext.UnimplementedEventServiceServer{},
		v1beta.UnimplementedComposeServiceServer{}, azdext.UnimplementedWorkflowServiceServer{},
		ready, azdext.UnimplementedServiceTargetServiceServer{}, azdext.UnimplementedFrameworkServiceServer{},
		azdext.UnimplementedContainerServiceServer{}, azdext.UnimplementedAccountServiceServer{},
		azdext.UnimplementedAiModelServiceServer{}, v1beta.UnimplementedCopilotServiceServer{},
		azdext.UnimplementedProvisioningServiceServer{}, azdext.UnimplementedValidationServiceServer{},
		azdext.UnimplementedTelemetryServiceServer{}, v1beta.UnimplementedCommandResultServiceServer{}, service,
	)
	info, err := server.Start()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, server.Stop()) })
	require.Contains(t, server.grpcServer.GetServiceInfo(), "azd.extensions.v1beta.InputService")
	require.NotContains(t, server.grpcServer.GetServiceInfo(), "azd.extensions.v1.InputService")
	require.NotContains(t, server.grpcServer.GetServiceInfo(), "azdext.InputService")

	connection, err := grpc.NewClient(info.Address, grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	defer connection.Close()
	err = connection.Invoke(t.Context(), "/azd.extensions.v1.InputService/Resolve",
		&v1beta.ResolveInputRequest{}, &v1beta.ResolveInputResponse{})
	require.Equal(t, codes.Unimplemented, status.Code(err))

	client, err := azdext.NewAzdClient(azdext.WithAddress(info.Address))
	require.NoError(t, err)
	t.Cleanup(client.Close)
	token, err := GenerateExtensionToken(extension, info)
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(azdext.WithAccessToken(t.Context(), token))
	done := make(chan error, 1)
	host := azdext.NewExtensionHost(client).
		WithBetaInputProvider([]string{inputs.EnvironmentNameKind, inputs.AzureLocationKind}, handler)
	go func() { done <- host.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			require.NoError(t, err)
		case <-time.After(5 * time.Second):
			t.Error("input extension host did not stop")
		}
		require.Eventually(t, func() bool {
			service.mu.RLock()
			defer service.mu.RUnlock()
			return !slices.ContainsFunc(service.providers, func(entry inputProviderEntry) bool {
				return entry.err == nil
			})
		}, 5*time.Second, 5*time.Millisecond)
	})
	select {
	case <-ready.ready:
	case err := <-done:
		done <- err
		t.Fatalf("input host failed before readiness: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("input host did not become ready")
	}
	return service
}

func TestBetaInputProviderResolvesAndDeclines(t *testing.T) {
	service := startInputTestHost(t, func(
		_ context.Context, definition *v1beta.InputDefinition,
	) (*v1beta.ResolveInputResponse, error) {
		assert.Equal(t, "AZURE_ENV_NAME", definition.EnvironmentVariable)
		return &v1beta.ResolveInputResponse{Options: []*v1beta.InputOption{{Value: "demo", Recommended: true}}}, nil
	})
	value, err := service.ResolveInput(t.Context(), &v1beta.InputDefinition{
		Id: "environment.name", Kind: inputs.EnvironmentNameKind, EnvironmentVariable: "AZURE_ENV_NAME",
	})
	require.NoError(t, err)
	require.Equal(t, []inputs.Option{{
		Value: "demo", Recommended: true, Sources: []string{"test.inputs"},
	}}, value)
	value, err = service.ResolveInput(t.Context(), &v1beta.InputDefinition{Id: "other", Kind: "other/v1"})
	require.NoError(t, err)
	require.Nil(t, value)
}

func TestBetaInputProviderOutcomes(t *testing.T) {
	for _, tt := range []struct {
		name     string
		response *v1beta.ResolveInputResponse
		err      error
	}{
		{name: "decline"},
		{name: "explicit empty", response: &v1beta.ResolveInputResponse{
			Options: []*v1beta.InputOption{{Value: ""}},
		}},
		{name: "failure", err: errors.New("resolution failed")},
	} {
		t.Run(tt.name, func(t *testing.T) {
			service := startInputTestHost(t, func(
				context.Context, *v1beta.InputDefinition,
			) (*v1beta.ResolveInputResponse, error) {
				return tt.response, tt.err
			})
			value, err := service.ResolveInput(t.Context(), &v1beta.InputDefinition{
				Id: "location", Kind: inputs.AzureLocationKind,
			})
			if tt.err != nil {
				require.ErrorContains(t, err, tt.err.Error())
			} else {
				require.NoError(t, err)
				if tt.response == nil {
					require.Empty(t, value)
				} else {
					require.Len(t, value, 1)
					require.Empty(t, value[0].Value)
				}
			}
		})
	}
}

func TestBetaInputProviderCancellation(t *testing.T) {
	started := make(chan struct{})
	service := startInputTestHost(t, func(
		ctx context.Context, _ *v1beta.InputDefinition,
	) (*v1beta.ResolveInputResponse, error) {
		close(started)
		<-ctx.Done()
		return nil, ctx.Err()
	})
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() {
		_, err := service.ResolveInput(ctx, &v1beta.InputDefinition{Id: "location", Kind: inputs.AzureLocationKind})
		done <- err
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("input handler did not start")
	}
	cancel()
	select {
	case err := <-done:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(5 * time.Second):
		t.Fatal("input request cancellation was not propagated")
	}
}

func TestBetaInputProviderMultipleAnswers(t *testing.T) {
	for _, required := range []bool{false, true} {
		t.Run(map[bool]string{false: "different recommendations", true: "conflicting required sets"}[required],
			func(t *testing.T) {
				calls := 0
				service := startInputTestHost(t, func(
					context.Context, *v1beta.InputDefinition,
				) (*v1beta.ResolveInputResponse, error) {
					calls++
					value := "first-sensitive-value"
					if calls > 1 {
						value = "second-sensitive-value"
					}
					return &v1beta.ResolveInputResponse{
						Required: required,
						Options: []*v1beta.InputOption{{
							Value: value, Recommended: true, Rationale: "Provider preference.",
						}},
					}, nil
				})
				service.mu.Lock()
				second := service.providers[0]
				second.extension = "test.second"
				service.providers = append(service.providers, second)
				service.mu.Unlock()
				value, err := service.ResolveInput(t.Context(), &v1beta.InputDefinition{
					Id: "environment.name", Kind: inputs.EnvironmentNameKind,
				})
				require.Equal(t, 2, calls)
				if required {
					require.ErrorContains(t, err, "no compatible options")
					require.ErrorContains(t, err, "test.inputs")
					require.ErrorContains(t, err, "test.second")
					require.NotContains(t, err.Error(), "sensitive-value")
					require.Nil(t, value)
				} else {
					require.NoError(t, err)
					require.Len(t, value, 2)
					require.Equal(t, "first-sensitive-value", value[0].Value)
					require.True(t, value[0].Recommended)
					require.Equal(t, []string{"Provider preference."}, value[0].Rationales)
				}
			})
	}
}

func TestBetaInputProviderAllowedValues(t *testing.T) {
	service := startInputTestHost(t, func(
		context.Context, *v1beta.InputDefinition,
	) (*v1beta.ResolveInputResponse, error) {
		return &v1beta.ResolveInputResponse{Options: []*v1beta.InputOption{
			{Value: "eastus2", Recommended: true}, {Value: "westus3"},
		}}, nil
	})
	value, err := service.ResolveInput(t.Context(), &v1beta.InputDefinition{
		Id: "location", Kind: inputs.AzureLocationKind, AllowedValues: []string{"westus3"},
	})
	require.NoError(t, err)
	require.Equal(t, []inputs.Option{{Value: "westus3", Sources: []string{"test.inputs"}}}, value)
}

func TestBetaInputProviderStreamAuthorizationAndCleanup(t *testing.T) {
	for _, authorized := range []bool{false, true} {
		t.Run(map[bool]string{false: "unauthorized", true: "authorized"}[authorized], func(t *testing.T) {
			extension := &extensions.Extension{Id: "test.inputs"}
			if authorized {
				extension.Capabilities = []extensions.CapabilityType{extensions.InputProviderCapability}
			}

			service := NewInputService(newStreamTestExtensionManager(t, extension))
			stream := newRegisterStream(extensionClaimsContext(t.Context(), extension.Id), &v1beta.InputMessage{
				RequestId: "registration",
				MessageType: &v1beta.InputMessage_RegisterInputProviderRequest{
					RegisterInputProviderRequest: &v1beta.RegisterInputProviderRequest{
						Kinds: []string{inputs.AzureLocationKind},
					},
				},
			})
			err := service.Stream(stream)
			if authorized {
				require.NoError(t, err)
				require.NotNil(t, stream.response.GetRegisterInputProviderResponse())
			} else {
				require.Equal(t, codes.PermissionDenied, status.Code(err))
			}
			if authorized {
				require.Len(t, service.providers, 1)
				_, err := service.ResolveInput(t.Context(), &v1beta.InputDefinition{
					Id: "location", Kind: inputs.AzureLocationKind,
				})
				require.ErrorContains(t, err, "input provider disconnected")
				other, err := service.ResolveInput(t.Context(), &v1beta.InputDefinition{Id: "other", Kind: "other/v1"})
				require.NoError(t, err)
				require.Empty(t, other)

				stream := newRegisterStream(extensionClaimsContext(t.Context(), extension.Id), stream.request)
				require.NoError(t, service.Stream(stream))
				require.NotNil(t, stream.response.GetRegisterInputProviderResponse(), "reconnection must register again")
				require.Len(t, service.providers, 1)
			} else {
				require.Empty(t, service.providers)
			}
		})
	}
}

func TestBetaInputProviderCannotRegisterClosedStream(t *testing.T) {
	service := NewInputService(nil)
	broker := grpcbroker.NewMessageBroker(
		newFailingStream[v1beta.InputMessage](t.Context(), io.EOF),
		azdext.NewBetaInputEnvelope(), "test.inputs", log.Default())
	broker.Close()
	response, err := service.registerProvider("test.inputs", broker, &v1beta.RegisterInputProviderRequest{
		Kinds: []string{inputs.AzureLocationKind},
	})
	require.Nil(t, response)
	require.Equal(t, codes.Unavailable, status.Code(err))
	require.Empty(t, service.providers)
}
