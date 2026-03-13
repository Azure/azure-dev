// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// Package exterrors provides structured error helpers for the azure.ai.agents extension.
//
// Use plain Go errors until the current code can confidently choose a final
// category, code, and suggestion. At that point, create a structured error with
// one of the helpers in this package or with one of the Azure/gRPC conversion
// helpers.
//
// Once an error is structured, usually return it unchanged. Avoid wrapping a
// structured error with [fmt.Errorf] and %w for extra context: azd serializes the
// structured error's own message and metadata, not the outer wrapper text.
package exterrors

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// ---------------------------------------------------------------------------
// Structured error factories
// ---------------------------------------------------------------------------

// Validation returns a validation [azdext.LocalError] for user-input or manifest errors.
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

// Compatibility returns a compatibility [azdext.LocalError] for version/feature mismatches.
func Compatibility(code, message, suggestion string) error {
	return &azdext.LocalError{
		Message:    message,
		Code:       code,
		Category:   azdext.LocalErrorCategoryCompatibility,
		Suggestion: suggestion,
	}
}

// Auth returns an auth [azdext.LocalError] for authentication or authorization failures.
func Auth(code, message, suggestion string) error {
	return &azdext.LocalError{
		Message:    message,
		Code:       code,
		Category:   azdext.LocalErrorCategoryAuth,
		Suggestion: suggestion,
	}
}

// Configuration returns a local/configuration [azdext.LocalError].
func Configuration(code, message, suggestion string) error {
	return &azdext.LocalError{
		Message:    message,
		Code:       code,
		Category:   azdext.LocalErrorCategoryLocal,
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

// Internal returns an internal [azdext.LocalError] for unexpected extension failures. No suggestion.
func Internal(code, message string) error {
	return &azdext.LocalError{
		Message:  message,
		Code:     code,
		Category: azdext.LocalErrorCategoryInternal,
	}
}

// ---------------------------------------------------------------------------
// Azure / gRPC error converters
// ---------------------------------------------------------------------------

// ServiceFromAzure wraps an [azcore.ResponseError] into an [azdext.ServiceError] with operation context.
// If the error is not an azcore.ResponseError, it returns a generic internal [azdext.LocalError].
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

// FromAiService wraps a gRPC error returned by an azd host AI service call
// into a structured [azdext.LocalError]. It detects auth errors ([codes.Unauthenticated])
// and classifies them as Auth errors. For other errors, it preserves the server's
// ErrorInfo reason code (from the azd.ai domain) when available,
// falling back to the provided code.
func FromAiService(err error, fallbackCode string) error {
	if err == nil {
		return nil
	}

	if IsCancellation(err) {
		return Cancelled(err.Error())
	}

	st, ok := status.FromError(err)
	if !ok {
		return Internal(fallbackCode, err.Error())
	}

	if st.Code() == codes.Unauthenticated {
		return authFromGrpcMessage(st.Message())
	}

	code := fallbackCode
	if reason := aiErrorReason(st); reason != "" {
		code = reason
	}

	return Internal(code, st.Message())
}

// FromPrompt wraps a gRPC error from an azd host Prompt call into a structured error.
// Auth errors ([codes.Unauthenticated]) are classified as Auth errors with a suggestion
// to re-authenticate. Other errors are returned with the provided context message.
func FromPrompt(err error, contextMsg string) error {
	if err == nil {
		return nil
	}

	if IsCancellation(err) {
		return Cancelled(contextMsg)
	}

	st, ok := status.FromError(err)
	if ok && st.Code() == codes.Unauthenticated {
		return authFromGrpcMessage(fmt.Sprintf("%s: %s", contextMsg, st.Message()))
	}

	return fmt.Errorf("%s: %w", contextMsg, err)
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// authFromGrpcMessage creates a structured Auth error from a gRPC Unauthenticated message.
// It classifies the error as not_logged_in, login_expired, or a generic auth_failed
// based on message content.
func authFromGrpcMessage(msg string) error {
	if strings.Contains(msg, "not logged in") {
		return Auth(CodeNotLoggedIn, msg, "run `azd auth login` to authenticate")
	}
	if strings.Contains(msg, "expired") {
		return Auth(CodeLoginExpired, msg, "run `azd auth login` to acquire a new token")
	}
	return Auth(CodeAuthFailed, msg, "run `azd auth login` to authenticate")
}

// IsCancellation checks if an error represents user cancellation
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

// Cancelled returns a user cancellation error.
func Cancelled(message string) error {
	return User(CodeCancelled, message)
}

// aiErrorReason extracts the ErrorInfo.Reason from a gRPC status
// when the domain matches [azdext.AiErrorDomain].
func aiErrorReason(st *status.Status) string {
	for _, detail := range st.Details() {
		info, ok := detail.(*errdetails.ErrorInfo)
		if ok && info.Domain == azdext.AiErrorDomain {
			return info.Reason
		}
	}
	return ""
}
