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
	"github.com/azure/azure-dev/cli/azd/pkg/extensions"
	"github.com/azure/azure-dev/cli/azd/pkg/grpcbroker"
	"github.com/azure/azure-dev/cli/azd/pkg/ioc"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// ImporterGrpcService implements azdext.ImporterServiceServer.
type ImporterGrpcService struct {
	azdext.UnimplementedImporterServiceServer
	container        *ioc.NestedContainer
	extensionManager *extensions.Manager
	providerMap      map[string]*grpcbroker.MessageBroker[azdext.ImporterMessage]
	providerMapMu    sync.Mutex
}

// NewImporterGrpcService creates a new ImporterGrpcService instance.
func NewImporterGrpcService(
	container *ioc.NestedContainer,
	extensionManager *extensions.Manager,
) azdext.ImporterServiceServer {
	return &ImporterGrpcService{
		container:        container,
		extensionManager: extensionManager,
		providerMap:      make(map[string]*grpcbroker.MessageBroker[azdext.ImporterMessage]),
	}
}

// Stream handles the bi-directional streaming for importer operations.
func (s *ImporterGrpcService) Stream(stream azdext.ImporterService_StreamServer) error {
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

	if !extension.HasCapability(extensions.ImporterProviderCapability) {
		return status.Errorf(codes.PermissionDenied, "extension does not support importer-provider capability")
	}

	// Create message broker for this stream
	ops := azdext.NewImporterEnvelope()
	broker := grpcbroker.NewMessageBroker(stream, ops, extension.Id, log.Default())

	// Track the importer name for cleanup when stream closes
	var registeredImporterName string

	// Register handler for RegisterImporterRequest
	err = broker.On(func(
		ctx context.Context,
		req *azdext.RegisterImporterRequest,
	) (*azdext.ImporterMessage, error) {
		return s.onRegisterRequest(ctx, req, extension, broker, &registeredImporterName)
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
	delete(s.providerMap, registeredImporterName)
	s.providerMapMu.Unlock()

	return nil
}

// onRegisterRequest handles the registration of an importer provider
func (s *ImporterGrpcService) onRegisterRequest(
	ctx context.Context,
	req *azdext.RegisterImporterRequest,
	extension *extensions.Extension,
	broker *grpcbroker.MessageBroker[azdext.ImporterMessage],
	registeredImporterName *string,
) (*azdext.ImporterMessage, error) {
	importerName := req.GetName()
	s.providerMapMu.Lock()
	defer s.providerMapMu.Unlock()

	if _, has := s.providerMap[importerName]; has {
		return nil, status.Errorf(codes.AlreadyExists, "provider %s already registered", importerName)
	}

	// Register external importer with DI container, passing the broker
	err := s.container.RegisterNamedSingleton(importerName, func() project.Importer {
		return project.NewExternalImporter(
			importerName,
			extension,
			broker,
		)
	})

	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to register importer: %s", err.Error())
	}

	s.providerMap[importerName] = broker
	*registeredImporterName = importerName
	log.Printf("Registered importer: %s", importerName)

	// Return response envelope
	return &azdext.ImporterMessage{
		MessageType: &azdext.ImporterMessage_RegisterImporterResponse{
			RegisterImporterResponse: &azdext.RegisterImporterResponse{},
		},
	}, nil
}
