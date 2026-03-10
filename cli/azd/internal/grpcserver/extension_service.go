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

// Ready signals that the extension is done registering all capabilities.
// The extension will remain alive as long as its streams are active and context is not cancelled.
func (s *ExtensionService) Ready(ctx context.Context, req *azdext.ReadyRequest) (*azdext.ReadyResponse, error) {
	extensionClaims, err := extensions.GetClaimsFromContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get extension claims: %w", err)
	}

	options := extensions.FilterOptions{
		Id: extensionClaims.Subject,
	}

	extension, err := s.extensionManager.GetInstalled(options)
	if err != nil {
		return nil, status.Errorf(codes.FailedPrecondition, "failed to get extension: %s", err.Error())
	}

	extension.Initialize()
	log.Printf("Extension %s is ready", extensionClaims.Subject)

	return &azdext.ReadyResponse{}, nil
}

// ReportError receives a structured error from the extension and stores it
// so the host can retrieve it after the extension process exits.
func (s *ExtensionService) ReportError(
	ctx context.Context, req *azdext.ReportErrorRequest,
) (*azdext.ReportErrorResponse, error) {
	extensionClaims, err := extensions.GetClaimsFromContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get extension claims: %w", err)
	}

	extension, err := s.extensionManager.GetInstalled(extensions.FilterOptions{
		Id: extensionClaims.Subject,
	})
	if err != nil {
		return nil, status.Errorf(codes.FailedPrecondition, "failed to get extension: %s", err.Error())
	}

	if req.GetError() != nil {
		extension.SetReportedError(azdext.UnwrapError(req.GetError()))
	}

	return &azdext.ReportErrorResponse{}, nil
}
