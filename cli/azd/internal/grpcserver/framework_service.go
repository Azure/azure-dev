// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package grpcserver

import (
	"errors"
	"fmt"
	"io"
	"log"
	"sync"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
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
	providerMap      map[string]azdext.FrameworkService_StreamServer
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
		providerMap:      make(map[string]azdext.FrameworkService_StreamServer),
	}
}

// Stream handles the bi-directional streaming for framework service operations.
func (s *FrameworkService) Stream(
	stream azdext.FrameworkService_StreamServer,
) error {
	ctx := stream.Context()
	extensionClaims, err := GetExtensionClaims(ctx)
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

	msg, err := stream.Recv()
	if errors.Is(err, io.EOF) {
		return nil
	}
	if err != nil {
		return err
	}

	regRequest := msg.GetRegisterFrameworkServiceRequest()
	if regRequest == nil {
		return status.Errorf(
			codes.FailedPrecondition,
			"expected RegisterFrameworkServiceRequest, got %T",
			msg.GetMessageType(),
		)
	}

	language := regRequest.GetLanguage()
	s.providerMapMu.Lock()
	if _, has := s.providerMap[language]; has {
		s.providerMapMu.Unlock()
		return status.Errorf(codes.AlreadyExists, "provider %s already registered", language)
	}

	// Register external framework service with DI container
	err = s.container.RegisterNamedSingleton(language, func(
		console input.Console,
	) project.FrameworkService {
		return project.NewExternalFrameworkService(
			language,
			project.ServiceLanguageKind(language),
			extension,
			stream,
			console,
		)
	})

	if err != nil {
		s.providerMapMu.Unlock()
		return status.Errorf(codes.Internal, "failed to register framework service: %s", err.Error())
	}

	s.providerMap[language] = stream

	// Send registration response
	response := &azdext.FrameworkServiceMessage{
		RequestId: msg.RequestId,
		MessageType: &azdext.FrameworkServiceMessage_RegisterFrameworkServiceResponse{
			RegisterFrameworkServiceResponse: &azdext.RegisterFrameworkServiceResponse{},
		},
	}

	if err := stream.Send(response); err != nil {
		s.providerMapMu.Unlock()
		return err
	}
	s.providerMapMu.Unlock()

	// The stream is now handled by the ExternalFrameworkService, so we don't consume messages here.
	// We need to wait for the stream to close without consuming messages.
	select {
	case <-ctx.Done():
		log.Printf("Framework service stream for language '%s' cancelled", language)
	case <-stream.Context().Done():
		log.Printf("Framework service stream for language '%s' closed by client", language)
	}

	// Clean up when stream closes
	s.providerMapMu.Lock()
	delete(s.providerMap, language)
	s.providerMapMu.Unlock()

	return nil
}
