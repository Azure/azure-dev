// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package foundry

import "github.com/azure/azure-dev/cli/azd/pkg/azdext"

// CodeInvalidFileRef is the structured error code emitted when a $ref include cannot be
// resolved or written. Foundry extensions surface it through azd's structured error channel.
const CodeInvalidFileRef = "invalid_file_ref"

// fileRefValidation returns a validation error for a malformed or unreadable $ref include.
// It builds the same azdext.LocalError shape the Foundry extensions use, so callers can
// branch on Code/Category regardless of which extension owns the resource.
func fileRefValidation(message, suggestion string) error {
	return &azdext.LocalError{
		Message:    message,
		Code:       CodeInvalidFileRef,
		Category:   azdext.LocalErrorCategoryValidation,
		Suggestion: suggestion,
	}
}

// fileRefInternal returns an internal error for an unexpected $ref resolution failure.
func fileRefInternal(message string) error {
	return &azdext.LocalError{
		Message:  message,
		Code:     CodeInvalidFileRef,
		Category: azdext.LocalErrorCategoryInternal,
	}
}
