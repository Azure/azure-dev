// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package exterrors

import (
	"context"
	"errors"
	"fmt"

	"azure.ai.content-understanding/internal/cuclient"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func HostUnavailable() error {
	return &azdext.LocalError{
		Code:       CodeHostUnavailable,
		Category:   azdext.LocalErrorCategoryDependency,
		Message:    "The azd extension host is unavailable.",
		Suggestion: "Run this extension through `azd ai cu`, not by executing the extension binary directly.",
	}
}

func MissingTenant() error {
	return &azdext.LocalError{
		Code:       CodeMissingTenant,
		Category:   azdext.LocalErrorCategoryDependency,
		Message:    "No Azure tenant is configured for this operation.",
		Suggestion: "Set AZURE_TENANT_ID, or use an azd environment with a configured tenant.",
	}
}

func FromError(err error) (result error) {
	defer sanitizeError(&result)
	if err == nil {
		return nil
	}
	if _, ok := errors.AsType[*azdext.LocalError](err); ok {
		return err
	}
	if _, ok := errors.AsType[*azdext.ServiceError](err); ok {
		return err
	}
	if _, ok := errors.AsType[*azdext.ToolError](err); ok {
		return err
	}
	if azdext.ActionableErrorDetailFromError(err) != nil {
		return err
	}
	if domain, ok := errors.AsType[*cuclient.Error](err); ok {
		return fromDomainError(domain)
	}
	if errors.Is(err, context.Canceled) || status.Code(err) == codes.Canceled {
		return &azdext.LocalError{
			Code: CodeCancelled, Category: azdext.LocalErrorCategoryUser,
			Message: "The operation was cancelled.",
		}
	}
	if errors.Is(err, context.DeadlineExceeded) || status.Code(err) == codes.DeadlineExceeded {
		return &azdext.LocalError{
			Code: CodeDeadlineExceeded, Category: azdext.LocalErrorCategoryInternal,
			Message: "The operation timed out.", Suggestion: "Check connectivity and retry the operation.",
		}
	}
	if status.Code(err) == codes.Unauthenticated || status.Code(err) == codes.PermissionDenied {
		return &azdext.LocalError{
			Code: CodeAuthenticationFailed, Category: azdext.LocalErrorCategoryAuth,
			Message:    "The azd host could not authorize this operation.",
			Suggestion: "Run `azd auth login` with the intended account, then retry through `azd ai cu`.",
		}
	}
	if status.Code(err) == codes.Unavailable {
		return HostUnavailable()
	}
	if response, ok := errors.AsType[*azcore.ResponseError](err); ok {
		return &azdext.ServiceError{
			Message:    fmt.Sprintf("The Content Understanding request failed (HTTP %d).", response.StatusCode),
			ErrorCode:  response.ErrorCode,
			StatusCode: response.StatusCode,
		}
	}
	return &azdext.LocalError{
		Code: CodeUnexpected, Category: azdext.LocalErrorCategoryInternal,
		Message:    "The Content Understanding extension could not complete the operation.",
		Suggestion: "Retry with --debug. If the failure persists, report the operation and this error code.",
	}
}

func fromDomainError(domain *cuclient.Error) error {
	local := &azdext.LocalError{Code: domain.Code, Message: domain.Message, Suggestion: domain.Suggestion}
	switch domain.Kind {
	case cuclient.ErrorValidation:
		local.Category = azdext.LocalErrorCategoryValidation
	case cuclient.ErrorAuthentication:
		local.Category = azdext.LocalErrorCategoryAuth
	case cuclient.ErrorCancelled:
		local.Category = azdext.LocalErrorCategoryUser
		local.Code = CodeCancelled
	case cuclient.ErrorTimeout:
		local.Category = azdext.LocalErrorCategoryInternal
		local.Code = CodeDeadlineExceeded
	case cuclient.ErrorNotFound, cuclient.ErrorConflict, cuclient.ErrorThrottled, cuclient.ErrorLimit, cuclient.ErrorService:
		return &azdext.ServiceError{
			Message: domain.Message, ErrorCode: domain.Code,
			StatusCode: domain.StatusCode, Suggestion: domain.Suggestion,
		}
	default:
		local.Category = azdext.LocalErrorCategoryInternal
		local.Code = CodeUnexpected
	}
	return local
}
