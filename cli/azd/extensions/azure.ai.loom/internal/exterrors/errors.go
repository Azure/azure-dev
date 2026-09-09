// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// Package exterrors provides structured errors for the azure.ai.loom extension.
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

// Validation returns an input-validation error.
func Validation(code, message, suggestion string) error {
	return &azdext.LocalError{
		Message:    message,
		Code:       code,
		Category:   azdext.LocalErrorCategoryValidation,
		Suggestion: suggestion,
	}
}

// Dependency returns a missing-dependency error.
func Dependency(code, message, suggestion string) error {
	return &azdext.LocalError{
		Message:    message,
		Code:       code,
		Category:   azdext.LocalErrorCategoryDependency,
		Suggestion: suggestion,
	}
}

// Auth returns an authentication error.
func Auth(code, message, suggestion string) error {
	return &azdext.LocalError{
		Message:    message,
		Code:       code,
		Category:   azdext.LocalErrorCategoryAuth,
		Suggestion: suggestion,
	}
}

// Internal returns an unexpected local error.
func Internal(code, message string) error {
	return &azdext.LocalError{
		Message:  message,
		Code:     code,
		Category: azdext.LocalErrorCategoryInternal,
	}
}

// User returns an error caused by a user action.
func User(code, message string) error {
	return &azdext.LocalError{
		Message:  message,
		Code:     code,
		Category: azdext.LocalErrorCategoryUser,
	}
}

// Cancelled returns a user cancellation error.
func Cancelled(message string) error {
	return User(CodeCancelled, message)
}

// ServiceFromAzure converts an Azure response error into an extension service error.
func ServiceFromAzure(err error, operation string) error {
	if responseErr, ok := errors.AsType[*azcore.ResponseError](err); ok {
		serviceName := ""
		if responseErr.RawResponse != nil && responseErr.RawResponse.Request != nil {
			serviceName = responseErr.RawResponse.Request.Host
		}
		errorCode := responseErr.ErrorCode
		if errorCode == "" {
			errorCode = fmt.Sprintf("%d", responseErr.StatusCode)
		}
		return &azdext.ServiceError{
			Message:     fmt.Sprintf("%s: %s", operation, responseErr.Error()),
			ErrorCode:   fmt.Sprintf("%s.%s", operation, errorCode),
			StatusCode:  responseErr.StatusCode,
			ServiceName: serviceName,
		}
	}
	if IsCancellation(err) {
		return Cancelled(fmt.Sprintf("%s was cancelled", operation))
	}
	return Internal(operation, fmt.Sprintf("%s: %s", operation, err))
}

// IsCancellation reports whether err represents user cancellation.
func IsCancellation(err error) bool {
	if errors.Is(err, context.Canceled) {
		return true
	}
	if grpcStatus, ok := status.FromError(err); ok {
		return grpcStatus.Code() == codes.Canceled
	}
	return false
}
