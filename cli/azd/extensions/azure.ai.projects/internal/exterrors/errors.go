// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// Package exterrors provides structured error helpers for the azure.ai.projects extension.
//
// This package mirrors a subset of azure.ai.agents/internal/exterrors so the
// two extensions can be consolidated into a shared package in a follow-up.
//
// Use plain Go errors until the current code can confidently choose a final
// category, code, and suggestion. At that point, create a structured error
// with one of the helpers in this package.
//
// Once an error is structured, return it unchanged. Avoid wrapping a structured
// error with [fmt.Errorf] and %w for extra context: azd serializes the
// structured error's own message and metadata, not the outer wrapper text.
package exterrors

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Validation returns a validation [azdext.LocalError] for user-input or
// configuration errors.
func Validation(code, message, suggestion string) error {
	return &azdext.LocalError{
		Message:    message,
		Code:       code,
		Category:   azdext.LocalErrorCategoryValidation,
		Suggestion: suggestion,
	}
}

// Dependency returns a dependency [azdext.LocalError] for missing resources or
// services.
func Dependency(code, message, suggestion string) error {
	return &azdext.LocalError{
		Message:    message,
		Code:       code,
		Category:   azdext.LocalErrorCategoryDependency,
		Suggestion: suggestion,
	}
}

// Auth returns an authentication or authorization error.
func Auth(code, message, suggestion string) error {
	return &azdext.LocalError{
		Message:    message,
		Code:       code,
		Category:   azdext.LocalErrorCategoryAuth,
		Suggestion: suggestion,
	}
}

// User returns a user-action error without a suggestion.
func User(code, message string) error {
	return &azdext.LocalError{
		Message:  message,
		Code:     code,
		Category: azdext.LocalErrorCategoryUser,
	}
}

// Internal returns an unexpected local failure.
func Internal(code, message string) error {
	return &azdext.LocalError{
		Message:  message,
		Code:     code,
		Category: azdext.LocalErrorCategoryInternal,
	}
}

// ServiceFromAzure classifies an Azure SDK response error.
func ServiceFromAzure(err error, operation string) error {
	if responseErr, ok := errors.AsType[*azcore.ResponseError](err); ok {
		serviceName := ""
		if responseErr.RawResponse != nil &&
			responseErr.RawResponse.Request != nil {
			serviceName = responseErr.RawResponse.Request.Host
		}
		code := responseErr.ErrorCode
		if code == "" {
			code = fmt.Sprintf("%d", responseErr.StatusCode)
		}
		return &azdext.ServiceError{
			Message: fmt.Sprintf(
				"%s: %s",
				operation,
				responseErr.Error(),
			),
			ErrorCode:   fmt.Sprintf("%s.%s", operation, code),
			StatusCode:  responseErr.StatusCode,
			ServiceName: serviceName,
		}
	}
	if IsCancellation(err) {
		return Cancelled(fmt.Sprintf("%s was cancelled", operation))
	}
	return Internal(
		operation,
		fmt.Sprintf("%s: %s", operation, err.Error()),
	)
}

// IsCancellation reports whether an operation was cancelled.
func IsCancellation(err error) bool {
	if errors.Is(err, context.Canceled) {
		return true
	}
	if grpcStatus, ok := status.FromError(err); ok {
		return grpcStatus.Code() == codes.Canceled
	}
	return false
}

// IsPromptRequired detects a no-prompt host failure.
func IsPromptRequired(err error) bool {
	if err == nil {
		return false
	}
	if grpcStatus, ok := status.FromError(err); ok {
		return strings.Contains(
			strings.ToLower(grpcStatus.Message()),
			"prompt required",
		)
	}
	return strings.Contains(
		strings.ToLower(err.Error()),
		"prompt required",
	)
}

// Cancelled returns a structured user cancellation.
func Cancelled(message string) error {
	return User(CodeCancelled, message)
}
