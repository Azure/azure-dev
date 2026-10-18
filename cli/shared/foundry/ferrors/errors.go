// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// Package ferrors defines module-owned typed errors for the Foundry shared module.
// Callers (typically Foundry extensions) translate these into azdext.LocalError or
// extension-local structured errors at the extension boundary. This package must not
// import github.com/azure/azure-dev/cli/azd.
package ferrors

// Category classifies a shared-module error for extension-side adapters.
type Category string

const (
	CategoryValidation Category = "validation"
	CategoryInternal   Category = "internal"
)

// Stable error codes emitted by the Foundry shared module.
const (
	// CodeInvalidFileRef is emitted when a $ref include cannot be resolved or written.
	CodeInvalidFileRef = "invalid_file_ref"
)

// Error is a typed shared-module error with a stable code, category, message, and
// optional suggestion. Extensions should map Code/Category into their local error type.
type Error struct {
	Message    string
	Code       string
	Category   Category
	Suggestion string
}

func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	return e.Message
}

// Validation returns a validation Error with the given code, message, and optional suggestion.
func Validation(code, message, suggestion string) *Error {
	return &Error{
		Message:    message,
		Code:       code,
		Category:   CategoryValidation,
		Suggestion: suggestion,
	}
}

// Internal returns an internal Error with the given code and message.
func Internal(code, message string) *Error {
	return &Error{
		Message:  message,
		Code:     code,
		Category: CategoryInternal,
	}
}

// InvalidFileRefValidation is a validation error for a malformed or unreadable $ref include.
func InvalidFileRefValidation(message, suggestion string) *Error {
	return Validation(CodeInvalidFileRef, message, suggestion)
}

// InvalidFileRefInternal is an internal error for an unexpected $ref resolution failure.
func InvalidFileRefInternal(message string) *Error {
	return Internal(CodeInvalidFileRef, message)
}
