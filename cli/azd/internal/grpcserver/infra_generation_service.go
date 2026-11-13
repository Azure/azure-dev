// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package grpcserver

import (
	"context"
	"errors"
	"fmt"
	"log"
	"slices"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	v1beta "github.com/azure/azure-dev/cli/azd/pkg/azdext/contracts/v1beta"
	"github.com/azure/azure-dev/cli/azd/pkg/extensions"
	"github.com/azure/azure-dev/cli/azd/pkg/grpcbroker"
	"github.com/azure/azure-dev/cli/azd/pkg/ioc"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// InfraGenerationService connects infrastructure generators to authenticated extension streams.
type InfraGenerationService struct {
	v1beta.UnimplementedInfraGenerationServiceServer
	container *ioc.NestedContainer
	manager   *extensions.Manager
	providers *providerStreamRegistry[*grpcbroker.MessageBroker[v1beta.InfraGenerationMessage]]
}

// NewInfraGenerationService creates a service using the given container and extension manager.
func NewInfraGenerationService(
	container *ioc.NestedContainer,
	manager *extensions.Manager,
) v1beta.InfraGenerationServiceServer {
	return &InfraGenerationService{
		container: container,
		manager:   manager,
		providers: newProviderStreamRegistry[*grpcbroker.MessageBroker[v1beta.InfraGenerationMessage]](
			"infrastructure generator",
		),
	}
}

// Stream registers the authenticated extension's infrastructure generators.
func (s *InfraGenerationService) Stream(stream v1beta.InfraGenerationService_StreamServer) error {
	ctx := stream.Context()
	extension, err := s.authorizedExtension(ctx)
	if err != nil {
		return err
	}

	broker := grpcbroker.NewMessageBroker(stream, &azdext.InfraGenerationEnvelope{}, extension.Id, log.Default())
	defer func() {
		broker.Close()
		s.providers.Release(broker)
	}()

	if err := s.registerStreamHandlers(extension, broker); err != nil {
		return err
	}

	if err := broker.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		return fmt.Errorf("infrastructure generation stream: %w", err)
	}

	return nil
}

func (s *InfraGenerationService) authorizedExtension(ctx context.Context) (*extensions.Extension, error) {
	claims, err := extensions.GetClaimsFromContext(ctx)
	if err != nil {
		return nil, err
	}

	extension, err := s.manager.GetInstalled(extensions.FilterOptions{Id: claims.Subject})
	if err != nil {
		return nil, status.Errorf(codes.FailedPrecondition, "loading generator extension: %v", err)
	}

	if !extension.HasCapability(extensions.InfraGenerationProviderCapability) {
		return nil, status.Error(codes.PermissionDenied, "extension lacks infra-generation-provider capability")
	}

	return extension, nil
}

func (s *InfraGenerationService) registerStreamHandlers(
	extension *extensions.Extension,
	broker *grpcbroker.MessageBroker[v1beta.InfraGenerationMessage],
) error {
	return broker.On(func(
		_ context.Context, request *v1beta.RegisterInfraGenerationProviderRequest,
	) (*v1beta.InfraGenerationMessage, error) {
		if err := s.registerProvider(extension, broker, request.GetName()); err != nil {
			return nil, err
		}

		return &v1beta.InfraGenerationMessage{
			MessageType: &v1beta.InfraGenerationMessage_RegisterResponse{
				RegisterResponse: &v1beta.RegisterInfraGenerationProviderResponse{},
			},
		}, nil
	})
}

func (s *InfraGenerationService) registerProvider(
	extension *extensions.Extension,
	broker *grpcbroker.MessageBroker[v1beta.InfraGenerationMessage],
	name string,
) error {
	if strings.TrimSpace(name) == "" {
		return status.Error(codes.InvalidArgument, "infrastructure generator name is required")
	}

	if !slices.ContainsFunc(extension.Providers, func(provider extensions.Provider) bool {
		return provider.Name == name && provider.Type == extensions.InfraGenerationProviderType
	}) {
		return status.Errorf(codes.PermissionDenied, "extension does not declare infrastructure generator %q", name)
	}

	return s.providers.Register(name, broker, func() error {
		return s.container.RegisterNamedTransient(
			project.InfraGenerationProviderKey(name),
			func() project.InfrastructureGenerator {
				return &externalInfrastructureGenerator{
					extension: extension,
					broker:    broker,
				}
			},
		)
	})
}

// externalInfrastructureGenerator forwards generation requests to its extension.
type externalInfrastructureGenerator struct {
	extension *extensions.Extension
	broker    *grpcbroker.MessageBroker[v1beta.InfraGenerationMessage]
}

func (g *externalInfrastructureGenerator) Generate(
	ctx context.Context, request *v1beta.InfraGenerateRequest,
) (*v1beta.InfraGenerateResponse, error) {
	response, err := g.broker.SendAndWait(ctx, &v1beta.InfraGenerationMessage{
		RequestId:   uuid.NewString(),
		MessageType: &v1beta.InfraGenerationMessage_GenerateRequest{GenerateRequest: request},
	})
	if err != nil {
		return nil, extensions.WrapInvocationError(err, g.extension.Id, g.extension.Version, "")
	}

	if response == nil || response.GetGenerateResponse() == nil {
		return nil, fmt.Errorf("expected infrastructure generation response")
	}

	return response.GetGenerateResponse(), nil
}
