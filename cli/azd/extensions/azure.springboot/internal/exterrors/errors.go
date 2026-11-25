// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package exterrors

import "github.com/azure/azure-dev/cli/azd/pkg/azdext"

// Validation creates a user-facing local validation error with actionable guidance.
func Validation(code, message, suggestion string) error {
	return &azdext.LocalError{
		Message:    message,
		Code:       code,
		Category:   azdext.LocalErrorCategoryUser,
		Suggestion: suggestion,
	}
}

// Configuration creates a non-disclosing Spring configuration validation error.
func Configuration(message string) error {
	return Validation(
		"spring_configuration_unresolved",
		message,
		"provide unambiguous supported Spring configuration; configuration values are not included in diagnostics",
	)
}
