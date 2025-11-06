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
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/extensions"
	"github.com/azure/azure-dev/cli/azd/pkg/grpcbroker"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/ioc"
	"github.com/azure/azure-dev/cli/azd/pkg/lazy"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/azure/azure-dev/cli/azd/pkg/prompt"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// ServiceTargetService implements azdext.ServiceTargetServiceServer.
type ServiceTargetService struct {
	azdext.UnimplementedServiceTargetServiceServer
	container        *ioc.NestedContainer
	extensionManager *extensions.Manager
	lazyEnv          *lazy.Lazy[*environment.Environment]
	providerMap      map[string]*grpcbroker.MessageBroker[azdext.ServiceTargetMessage]
	providerMapMu    sync.Mutex
}

// NewServiceTargetService creates a new ServiceTargetService instance.
func NewServiceTargetService(
	container *ioc.NestedContainer,
	extensionManager *extensions.Manager,
	lazyEnv *lazy.Lazy[*environment.Environment],
) azdext.ServiceTargetServiceServer {
	return &ServiceTargetService{
		container:        container,
		extensionManager: extensionManager,
		lazyEnv:          lazyEnv,
		providerMap:      make(map[string]*grpcbroker.MessageBroker[azdext.ServiceTargetMessage]),
	}
}

// Stream handles the bi-directional streaming for service target operations.
func (s *ServiceTargetService) Stream(stream azdext.ServiceTargetService_StreamServer) error {
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

	if !extension.HasCapability(extensions.ServiceTargetProviderCapability) {
		return status.Errorf(codes.PermissionDenied, "extension does not support service-target-provider capability")
	}

	// Create message broker for this stream
	ops := azdext.NewServiceTargetEnvelope()
	broker := grpcbroker.NewMessageBroker(stream, ops)

	// Track the hostType for cleanup when stream closes
	var registeredHostType string

	// Register handler for RegisterServiceTargetRequest
	err = broker.On(func(
		ctx context.Context,
		req *azdext.RegisterServiceTargetRequest,
	) (*azdext.ServiceTargetMessage, error) {
		return s.onRegisterRequest(ctx, req, extension, broker, &registeredHostType)
	})

	if err != nil {
		return fmt.Errorf("failed to register handler: %w", err)
	}

	// Run the broker dispatcher (blocking)
	// This will return when the stream closes or encounters an error
	if err := broker.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		log.Printf("Broker error for provider %s: %v", registeredHostType, err)
		return fmt.Errorf("broker error: %w", err)
	}

	log.Printf("Stream closed for provider: %s", registeredHostType)

	s.providerMapMu.Lock()
	delete(s.providerMap, registeredHostType)
	s.providerMapMu.Unlock()

	return nil
}

// onRegisterRequest handles the registration of a service target provider
func (s *ServiceTargetService) onRegisterRequest(
	ctx context.Context,
	req *azdext.RegisterServiceTargetRequest,
	extension *extensions.Extension,
	broker *grpcbroker.MessageBroker[azdext.ServiceTargetMessage],
	registeredHostType *string,
) (*azdext.ServiceTargetMessage, error) {
	hostType := req.GetHost()
	s.providerMapMu.Lock()
	defer s.providerMapMu.Unlock()

	if _, has := s.providerMap[hostType]; has {
		return nil, status.Errorf(codes.AlreadyExists, "provider %s already registered", hostType)
	}

	// Register external service target with DI container, passing the broker
	err := s.container.RegisterNamedSingleton(hostType, func(
		console input.Console,
		prompter prompt.Prompter,
	) project.ServiceTarget {
		env, _ := s.lazyEnv.GetValue()
		return project.NewExternalServiceTarget(
			hostType,
			project.ServiceTargetKind(hostType),
			extension,
			broker,
			console,
			prompter,
			env,
		)
	})

	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to register service target: %s", err.Error())
	}

	s.providerMap[hostType] = broker
	*registeredHostType = hostType
	log.Printf("Registered service target: %s", hostType)

	// Return response envelope
	return &azdext.ServiceTargetMessage{
		MessageType: &azdext.ServiceTargetMessage_RegisterServiceTargetResponse{
			RegisterServiceTargetResponse: &azdext.RegisterServiceTargetResponse{},
		},
	}, nil
}
