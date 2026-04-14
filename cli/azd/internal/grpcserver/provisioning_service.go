// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package grpcserver

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/extensions"
	"github.com/azure/azure-dev/cli/azd/pkg/grpcbroker"
	"github.com/azure/azure-dev/cli/azd/pkg/ioc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// ProvisioningService implements azdext.ProvisioningServiceServer.
type ProvisioningService struct {
	azdext.UnimplementedProvisioningServiceServer
	container        *ioc.NestedContainer
	extensionManager *extensions.Manager
	providerMap      map[string]*grpcbroker.MessageBroker[azdext.ProvisioningMessage]
	providerMapMu    sync.Mutex
}

// NewProvisioningService creates a new ProvisioningService instance.
func NewProvisioningService(
	container *ioc.NestedContainer,
	extensionManager *extensions.Manager,
) azdext.ProvisioningServiceServer {
	return &ProvisioningService{
		container:        container,
		extensionManager: extensionManager,
		providerMap:      make(map[string]*grpcbroker.MessageBroker[azdext.ProvisioningMessage]),
	}
}

// Stream handles the bi-directional streaming for provisioning operations.
func (s *ProvisioningService) Stream(
	stream azdext.ProvisioningService_StreamServer,
) error {
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
		return status.Errorf(
			codes.FailedPrecondition, "failed to get extension: %s", err.Error(),
		)
	}

	if !extension.HasCapability(extensions.ProvisioningProviderCapability) {
		return status.Errorf(
			codes.PermissionDenied,
			"extension does not support provisioning-provider capability",
		)
	}

	// Create message broker for this stream
	ops := azdext.NewProvisioningEnvelope()
	broker := grpcbroker.NewMessageBroker(stream, ops, extension.Id, log.Default())

	// Track the provider name for cleanup when stream closes
	var registeredProviderName string

	// Register handler for RegisterProvisioningProviderRequest
	err = broker.On(func(
		ctx context.Context,
		req *azdext.RegisterProvisioningProviderRequest,
	) (*azdext.ProvisioningMessage, error) {
		return s.onRegisterRequest(
			ctx, req, extension, broker, &registeredProviderName,
		)
	})

	if err != nil {
		return fmt.Errorf("failed to register handler: %w", err)
	}

	// Run the broker dispatcher (blocking)
	if err := broker.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		log.Printf("Broker error for provider %s: %v", registeredProviderName, err)
		return fmt.Errorf("broker error: %w", err)
	}

	s.providerMapMu.Lock()
	delete(s.providerMap, registeredProviderName)
	s.providerMapMu.Unlock()

	return nil
}

// onRegisterRequest handles the registration of a provisioning provider
func (s *ProvisioningService) onRegisterRequest(
	ctx context.Context,
	req *azdext.RegisterProvisioningProviderRequest,
	extension *extensions.Extension,
	broker *grpcbroker.MessageBroker[azdext.ProvisioningMessage],
	registeredProviderName *string,
) (*azdext.ProvisioningMessage, error) {
	providerName := req.GetName()
	if strings.TrimSpace(providerName) == "" {
		return nil, status.Errorf(
			codes.InvalidArgument, "provider name cannot be empty",
		)
	}

	s.providerMapMu.Lock()
	defer s.providerMapMu.Unlock()

	if _, has := s.providerMap[providerName]; has {
		return nil, status.Errorf(
			codes.AlreadyExists, "provider %s already registered", providerName,
		)
	}

	// Register external provisioning provider with DI container
	err := s.container.RegisterNamedTransient(
		providerName,
		NewExternalProvisioningProviderFactory(
			providerName, extension, broker,
		),
	)

	if err != nil {
		return nil, status.Errorf(
			codes.Internal,
			"failed to register provisioning provider: %s",
			err.Error(),
		)
	}

	s.providerMap[providerName] = broker
	*registeredProviderName = providerName
	log.Printf("Registered provisioning provider: %s", providerName)

	// Return response envelope
	return &azdext.ProvisioningMessage{
		MessageType: &azdext.ProvisioningMessage_RegisterProvisioningProviderResponse{
			RegisterProvisioningProviderResponse: &azdext.RegisterProvisioningProviderResponse{},
		},
	}, nil
}
