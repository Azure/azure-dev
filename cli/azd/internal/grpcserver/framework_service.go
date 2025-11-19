// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package grpcserver

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sync"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext/grpcbroker"
	"github.com/azure/azure-dev/cli/azd/pkg/extensions"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/ioc"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// FrameworkService implements azdext.FrameworkServiceServer.
type FrameworkService struct {
	azdext.UnimplementedFrameworkServiceServer
	container        *ioc.NestedContainer
	extensionManager *extensions.Manager
	providerMap      map[string]*grpcbroker.MessageBroker[azdext.FrameworkServiceMessage]
	providerMapMu    sync.Mutex
}

// NewFrameworkService creates a new FrameworkService instance.
func NewFrameworkService(
	container *ioc.NestedContainer,
	extensionManager *extensions.Manager,
) azdext.FrameworkServiceServer {
	return &FrameworkService{
		container:        container,
		extensionManager: extensionManager,
		providerMap:      make(map[string]*grpcbroker.MessageBroker[azdext.FrameworkServiceMessage]),
	}
}

// Stream handles the bi-directional streaming for framework service operations.
func (s *FrameworkService) Stream(stream azdext.FrameworkService_StreamServer) error {
	ctx := stream.Context()
	extensionClaims, err := extensions.GetClaimsFromContext(ctx)
	if err != nil {
		return fmt.Errorf("failed to get extension claims: %w", err)
	}

	options := extensions.FilterOptions{
		Id: extensionClaims.Subject,
	}

	extension, err := s.extensionManager.GetInstalled(options)
	if err != nil {
		return status.Errorf(codes.FailedPrecondition, "failed to get extension: %s", err.Error())
	}

	// For framework services, we'll create a custom capability check similar to service targets
	// Extensions providing framework services should declare this capability
	if !extension.HasCapability("framework-service-provider") {
		return status.Errorf(codes.PermissionDenied, "extension does not support framework-service-provider capability")
	}

	// Create message broker for this stream
	ops := azdext.NewFrameworkServiceEnvelope()
	broker := grpcbroker.NewMessageBroker(stream, ops, extension.Id)

	// Track the language for cleanup when stream closes
	var registeredLanguage string

	// Register handler for RegisterFrameworkServiceRequest
	err = broker.On(func(
		ctx context.Context,
		req *azdext.RegisterFrameworkServiceRequest,
	) (*azdext.FrameworkServiceMessage, error) {
		return s.onRegisterRequest(ctx, req, extension, broker, &registeredLanguage)
	})

	if err != nil {
		return fmt.Errorf("failed to register handler: %w", err)
	}

	// Run the broker dispatcher (blocking)
	// This will return when the stream closes or encounters an error
	if err := broker.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		return fmt.Errorf("broker error: %w", err)
	}

	s.providerMapMu.Lock()
	delete(s.providerMap, registeredLanguage)
	s.providerMapMu.Unlock()

	return nil
}

// onRegisterRequest handles the registration of a framework service provider
func (s *FrameworkService) onRegisterRequest(
	ctx context.Context,
	req *azdext.RegisterFrameworkServiceRequest,
	extension *extensions.Extension,
	broker *grpcbroker.MessageBroker[azdext.FrameworkServiceMessage],
	registeredLanguage *string,
) (*azdext.FrameworkServiceMessage, error) {
	language := req.GetLanguage()
	s.providerMapMu.Lock()
	defer s.providerMapMu.Unlock()

	if _, has := s.providerMap[language]; has {
		return nil, status.Errorf(codes.AlreadyExists, "provider %s already registered", language)
	}

	// Register external framework service with DI container, passing the broker
	err := s.container.RegisterNamedSingleton(language, func(
		console input.Console,
	) project.FrameworkService {
		return project.NewExternalFrameworkService(
			language,
			project.ServiceLanguageKind(language),
			extension,
			broker,
			console,
		)
	})

	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to register framework service: %s", err.Error())
	}

	s.providerMap[language] = broker
	*registeredLanguage = language
	log.Printf("Registered framework service: %s", language)

	// Return response envelope
	return &azdext.FrameworkServiceMessage{
		MessageType: &azdext.FrameworkServiceMessage_RegisterFrameworkServiceResponse{
			RegisterFrameworkServiceResponse: &azdext.RegisterFrameworkServiceResponse{},
		},
	}, nil
}
