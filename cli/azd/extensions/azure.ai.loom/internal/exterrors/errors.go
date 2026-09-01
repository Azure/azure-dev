// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// Package exterrors provides structured errors for the azure.ai.loom extension.
package exterrors

import (
	"errors"
	"fmt"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
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
	return Internal(operation, fmt.Sprintf("%s: %s", operation, err))
}
