// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package exterrors

import (
	"errors"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

// Validation returns a validation error for user-input or flag errors.
func Validation(code, message, suggestion string) error {
	return &azdext.LocalError{
		Message:    message,
		Code:       code,
		Category:   azdext.LocalErrorCategoryValidation,
		Suggestion: suggestion,
	}
}

// Dependency returns a dependency error for missing resources or services.
func Dependency(code, message, suggestion string) error {
	return &azdext.LocalError{
		Message:    message,
		Code:       code,
		Category:   azdext.LocalErrorCategoryDependency,
		Suggestion: suggestion,
	}
}

// Auth returns an auth error for authentication or authorization failures.
func Auth(code, message, suggestion string) error {
	return &azdext.LocalError{
		Message:    message,
		Code:       code,
		Category:   azdext.LocalErrorCategoryAuth,
		Suggestion: suggestion,
	}
}

// ServiceFromAzure converts an Azure SDK error into a structured service error.
func ServiceFromAzure(err error, operation string) error {
	if respErr, ok := errors.AsType[*azcore.ResponseError](err); ok {
		return &azdext.ServiceError{
			Message:     respErr.Error(),
			ErrorCode:   respErr.ErrorCode,
			StatusCode:  respErr.StatusCode,
			ServiceName: operation,
		}
	}
	return err
}
