// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// Package exterrors provides structured error helpers for the azure.ai.toolboxes
// extension.
//
// Use plain Go errors until the current code can confidently choose a final
// category, code, and suggestion. At that point, create a structured error with
// one of the helpers in this package or with [ServiceFromAzure] for Azure SDK
// failures.
//
// Once an error is structured, usually return it unchanged. Avoid wrapping a
// structured error with [fmt.Errorf] and %w for extra context: azd serializes
// the structured error's own message and metadata, not the outer wrapper text.
package exterrors

import (
	"context"
	"errors"
	"fmt"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// ---------------------------------------------------------------------------
// Structured error factories
// ---------------------------------------------------------------------------

// Validation returns a validation [azdext.LocalError] for user input / flag errors.
func Validation(code, message, suggestion string) error {
	return &azdext.LocalError{
		Message:    message,
		Code:       code,
		Category:   azdext.LocalErrorCategoryValidation,
		Suggestion: suggestion,
	}
}

// Dependency returns a dependency [azdext.LocalError] for missing resources or services.
func Dependency(code, message, suggestion string) error {
	return &azdext.LocalError{
		Message:    message,
		Code:       code,
		Category:   azdext.LocalErrorCategoryDependency,
		Suggestion: suggestion,
	}
}

// Auth returns an auth [azdext.LocalError] for authentication/authorization failures.
func Auth(code, message, suggestion string) error {
	return &azdext.LocalError{
		Message:    message,
		Code:       code,
		Category:   azdext.LocalErrorCategoryAuth,
		Suggestion: suggestion,
	}
}

// User returns a user-action [azdext.LocalError] (e.g. cancellation). No suggestion.
func User(code, message string) error {
	return &azdext.LocalError{
		Message:  message,
		Code:     code,
		Category: azdext.LocalErrorCategoryUser,
	}
}

// Internal returns an internal [azdext.LocalError] for unexpected extension failures.
func Internal(code, message string) error {
	return &azdext.LocalError{
		Message:  message,
		Code:     code,
		Category: azdext.LocalErrorCategoryInternal,
	}
}

// Cancelled returns a user cancellation error.
func Cancelled(message string) error {
	return User(CodeCancelled, message)
}

// ---------------------------------------------------------------------------
// Azure error converters
// ---------------------------------------------------------------------------

// ServiceFromAzure wraps an [azcore.ResponseError] into an [azdext.ServiceError]
// with operation context. If the error is not an azcore.ResponseError, it
// returns a generic internal [azdext.LocalError].
func ServiceFromAzure(err error, operation string) error {
	var respErr *azcore.ResponseError
	if errors.As(err, &respErr) {
		serviceName := ""
		if respErr.RawResponse != nil && respErr.RawResponse.Request != nil {
			serviceName = respErr.RawResponse.Request.Host
		}
		code := respErr.ErrorCode
		if code == "" {
			code = fmt.Sprintf("%d", respErr.StatusCode)
		}
		return &azdext.ServiceError{
			Message:     fmt.Sprintf("%s: %s", operation, respErr.Error()),
			ErrorCode:   fmt.Sprintf("%s.%s", operation, code),
			StatusCode:  respErr.StatusCode,
			ServiceName: serviceName,
		}
	}
	if IsCancellation(err) {
		return Cancelled(fmt.Sprintf("%s was cancelled", operation))
	}
	return Internal(operation, fmt.Sprintf("%s: %s", operation, err.Error()))
}

// FromPrompt wraps a gRPC error from an azd host Prompt call into a structured
// error. Auth errors (Unauthenticated) are classified as Auth errors with a
// re-auth suggestion; cancellations as User cancellations; other errors are
// returned wrapped with the provided context message.
func FromPrompt(err error, contextMsg string) error {
	if err == nil {
		return nil
	}

	if IsCancellation(err) {
		return Cancelled(contextMsg)
	}

	st, ok := status.FromError(err)
	if ok && st.Code() == codes.Unauthenticated {
		return Auth(
			CodeAuthFailed,
			fmt.Sprintf("%s: %s", contextMsg, st.Message()),
			"run `azd auth login` to authenticate",
		)
	}

	return fmt.Errorf("%s: %w", contextMsg, err)
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// IsCancellation reports whether err represents user cancellation
// ([context.Canceled] or gRPC [codes.Canceled]).
func IsCancellation(err error) bool {
	if errors.Is(err, context.Canceled) {
		return true
	}
	if st, ok := status.FromError(err); ok && st.Code() == codes.Canceled {
		return true
	}
	return false
}
