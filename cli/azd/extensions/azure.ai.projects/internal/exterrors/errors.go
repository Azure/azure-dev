// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// Package exterrors provides structured error helpers for the azure.ai.projects extension.
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
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
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
