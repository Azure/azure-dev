// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package grpcserver

import (
	context "context"
	"fmt"
	"log"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/extensions"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// ExtensionService implements azdext.ExtensionServiceServer.
type ExtensionService struct {
	azdext.UnimplementedExtensionServiceServer
	extensionManager *extensions.Manager
	// Add any dependencies or state here as needed
}

// NewExtensionService creates a new ExtensionService instance.
func NewExtensionService(extensionManager *extensions.Manager) azdext.ExtensionServiceServer {
	return &ExtensionService{
		extensionManager: extensionManager,
	}
}

// Ready signals that the extension is done registering all capabilities and blocks until shutdown.
// This is a blocking call that keeps the extension alive until the server signals shutdown.
func (s *ExtensionService) Ready(ctx context.Context, req *azdext.ReadyRequest) (*azdext.ReadyResponse, error) {
	extensionClaims, err := GetExtensionClaims(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get extension claims: %w", err)
	}

	options := extensions.LookupOptions{
		Id: extensionClaims.Subject,
	}

	extension, err := s.extensionManager.GetInstalled(options)
	if err != nil {
		return nil, status.Errorf(codes.FailedPrecondition, "failed to get extension: %s", err.Error())
	}

	extension.Initialize()
	log.Printf("Extension %s is ready", extensionClaims.Subject)

	// Block until context is cancelled (server shutdown signal)
	<-ctx.Done()
	log.Printf("Extension %s shutting down", extensionClaims.Subject)

	return &azdext.ReadyResponse{}, nil
}
