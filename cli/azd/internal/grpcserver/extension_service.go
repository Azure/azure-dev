// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package grpcserver

import (
	context "context"
	"fmt"
	"log"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	v1beta "github.com/azure/azure-dev/cli/azd/pkg/azdext/contracts/v1beta"
	"github.com/azure/azure-dev/cli/azd/pkg/errorchain"
	"github.com/azure/azure-dev/cli/azd/pkg/errorhandler"
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
	if err := s.storeReportedError(ctx, azdext.UnwrapError(req.GetError())); err != nil {
		return nil, err
	}

	return &azdext.ReportErrorResponse{}, nil
}

func (s *ExtensionService) storeReportedError(ctx context.Context, reported error) error {
	extensionClaims, err := extensions.GetClaimsFromContext(ctx)
	if err != nil {
		return fmt.Errorf("failed to get extension claims: %w", err)
	}

	extension, err := s.extensionManager.GetInstalled(extensions.FilterOptions{
		Id: extensionClaims.Subject,
	})
	if err != nil {
		return status.Errorf(codes.FailedPrecondition, "failed to get extension: %s", err.Error())
	}

	if reported != nil {
		extension.SetReportedError(reported)
	}

	return nil
}

type betaExtensionServiceOverride struct {
	service  *ExtensionService
	override any
}

func (s *betaExtensionServiceOverride) Ready(
	ctx context.Context,
	req *v1beta.ReadyRequest,
) (*v1beta.ReadyResponse, error) {
	if override, ok := s.override.(BetaExtensionServiceReadyOverride); ok {
		return override.Ready(ctx, req)
	}

	return adaptBetaUnary(
		ctx,
		req,
		new(azdext.ReadyRequest),
		s.service.Ready,
		new(v1beta.ReadyResponse),
		"ExtensionService.Ready",
	)
}

func (s *betaExtensionServiceOverride) ReportError(
	ctx context.Context,
	req *v1beta.ReportErrorRequest,
) (*v1beta.ReportErrorResponse, error) {
	if override, ok := s.override.(BetaExtensionServiceReportErrorOverride); ok {
		return override.ReportError(ctx, req)
	}

	if err := s.service.storeReportedError(ctx, unwrapBetaExtensionError(req.GetError())); err != nil {
		return nil, err
	}

	return &v1beta.ReportErrorResponse{}, nil
}

func unwrapBetaExtensionError(msg *v1beta.ExtensionError) error {
	if msg == nil {
		return nil
	}

	links := unwrapBetaErrorLinks(msg.GetLinks())
	if serviceErr := msg.GetServiceError(); serviceErr != nil {
		return &azdext.ServiceError{
			Message:     msg.GetMessage(),
			ErrorCode:   serviceErr.GetErrorCode(),
			StatusCode:  int(serviceErr.GetStatusCode()),
			ServiceName: serviceErr.GetServiceName(),
			Suggestion:  msg.GetSuggestion(),
			Links:       links,
		}
	}
	if localErr := msg.GetLocalError(); localErr != nil {
		return &azdext.LocalError{
			Message:    msg.GetMessage(),
			Code:       localErr.GetCode(),
			Category:   azdext.ParseLocalErrorCategory(localErr.GetCategory()),
			CauseTypes: errorchain.NormalizeCauseTypes(localErr.GetCauseTypes()),
			Suggestion: msg.GetSuggestion(),
			Links:      links,
		}
	}
	if toolErr := msg.GetToolError(); toolErr != nil {
		var exitCode *int
		if toolErr.ExitCode != nil {
			exitCode = new(int(toolErr.GetExitCode()))
		}
		kind := azdext.ToolErrorKindFailed
		if toolErr.GetFailureKind() == string(azdext.ToolErrorKindMissing) {
			kind = azdext.ToolErrorKindMissing
		}
		return &azdext.ToolError{
			Message:    msg.GetMessage(),
			ToolName:   toolErr.GetToolName(),
			Kind:       kind,
			ExitCode:   exitCode,
			Suggestion: msg.GetSuggestion(),
			Links:      links,
		}
	}

	switch msg.GetOrigin() {
	case v1beta.ErrorOrigin_ERROR_ORIGIN_SERVICE:
		return &azdext.ServiceError{Message: msg.GetMessage(), Suggestion: msg.GetSuggestion(), Links: links}
	case v1beta.ErrorOrigin_ERROR_ORIGIN_TOOL:
		return &azdext.ToolError{
			Message: msg.GetMessage(), Kind: azdext.ToolErrorKindFailed, Suggestion: msg.GetSuggestion(), Links: links,
		}
	default:
		return &azdext.LocalError{
			Message: msg.GetMessage(), Category: azdext.LocalErrorCategoryLocal,
			Suggestion: msg.GetSuggestion(), Links: links,
		}
	}
}

func unwrapBetaErrorLinks(links []*v1beta.ErrorLink) []errorhandler.ErrorLink {
	if len(links) == 0 {
		return nil
	}

	result := make([]errorhandler.ErrorLink, len(links))
	for index, link := range links {
		if link != nil {
			result[index] = errorhandler.ErrorLink{URL: link.GetUrl(), Title: link.GetTitle()}
		}
	}
	return result
}
