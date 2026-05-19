// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// Package exterrors provides structured error helpers for the azure.ai.routines extension.
package exterrors

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

// Validation returns a validation LocalError for user-input errors.
func Validation(code, message, suggestion string) error {
	return &azdext.LocalError{
		Message:    message,
		Code:       code,
		Category:   azdext.LocalErrorCategoryValidation,
		Suggestion: suggestion,
	}
}

// Dependency returns a dependency LocalError for missing resources or services.
func Dependency(code, message, suggestion string) error {
	return &azdext.LocalError{
		Message:    message,
		Code:       code,
		Category:   azdext.LocalErrorCategoryDependency,
		Suggestion: suggestion,
	}
}

// Auth returns an auth LocalError for authentication/authorization failures.
func Auth(code, message, suggestion string) error {
	return &azdext.LocalError{
		Message:    message,
		Code:       code,
		Category:   azdext.LocalErrorCategoryAuth,
		Suggestion: suggestion,
	}
}

// Internal returns an internal LocalError for unexpected failures.
func Internal(code, message string) error {
	return &azdext.LocalError{
		Message:  message,
		Code:     code,
		Category: azdext.LocalErrorCategoryInternal,
	}
}

// User returns a user-action LocalError (e.g. cancellation).
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

// ServiceFromAzure wraps an azcore.ResponseError into an azdext.ServiceError.
// If the error is not an azcore.ResponseError, it returns a generic internal error.
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

// ServiceFromStatus returns a ServiceFromAzure-style error for a raw HTTP status code.
func ServiceFromStatus(statusCode int, operation, message string) error {
	return &azdext.ServiceError{
		Message:    fmt.Sprintf("%s: %s", operation, message),
		ErrorCode:  fmt.Sprintf("%s.%d", operation, statusCode),
		StatusCode: statusCode,
	}
}

// IsNotFound returns true if the error represents an HTTP 404.
func IsNotFound(err error) bool {
	var respErr *azcore.ResponseError
	if errors.As(err, &respErr) {
		return respErr.StatusCode == http.StatusNotFound
	}
	var svcErr *azdext.ServiceError
	if errors.As(err, &svcErr) {
		return svcErr.StatusCode == http.StatusNotFound
	}
	return false
}

// IsConflict returns true if the error represents an HTTP 409.
func IsConflict(err error) bool {
	var respErr *azcore.ResponseError
	if errors.As(err, &respErr) {
		return respErr.StatusCode == http.StatusConflict
	}
	var svcErr *azdext.ServiceError
	if errors.As(err, &svcErr) {
		return svcErr.StatusCode == http.StatusConflict
	}
	return false
}

// IsCancellation checks if an error represents user cancellation.
func IsCancellation(err error) bool {
	return errors.Is(err, context.Canceled)
}

// authFromMessage creates an Auth error from an HTTP response message.
func authFromMessage(msg string) error {
	if strings.Contains(msg, "not logged in") {
		return Auth(CodeNotLoggedIn, msg, "run `azd auth login` to authenticate")
	}
	if strings.Contains(msg, "expired") {
		return Auth(CodeLoginExpired, msg, "run `azd auth login` to acquire a new token")
	}
	return Auth(CodeAuthFailed, msg, "run `azd auth login` to authenticate")
}

// WrapAuthError wraps a 401 error as an Auth error.
func WrapAuthError(err error, operation string) error {
	var respErr *azcore.ResponseError
	if errors.As(err, &respErr) && respErr.StatusCode == http.StatusUnauthorized {
		return authFromMessage(respErr.Error())
	}
	return ServiceFromAzure(err, operation)
}
