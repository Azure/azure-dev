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
	CategoryAuth       Category = "auth"
	CategoryService    Category = "service"
	CategoryInternal   Category = "internal"
)

// Stable error codes emitted by the Foundry shared module.
const (
	// CodeInvalidFileRef is emitted when a $ref include cannot be resolved or written.
	CodeInvalidFileRef = "invalid_file_ref"
	// CodeAuthFailed is emitted when Azure authentication fails or is required.
	CodeAuthFailed = "auth_failed"
)

// Error is a typed shared-module error with a stable code, category, message,
// optional suggestion, and optional service HTTP status or underlying cause.
// Extensions map Code/Category into their local structured error type.
type Error struct {
	Message     string
	Code        string
	Category    Category
	Suggestion  string
	StatusCode  int
	ServiceName string
	Cause       error
}

// Error returns the user-facing message without serializing Cause, since raw SDK
// errors can contain credential-bearing URLs or response payloads.
func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	return e.Message
}

// Unwrap returns the underlying failure for errors.Is / errors.AsType inspection.
func (e *Error) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
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

// Auth returns an authentication Error with the given code, message, and optional suggestion.
func Auth(code, message, suggestion string) *Error {
	return &Error{
		Message:    message,
		Code:       code,
		Category:   CategoryAuth,
		Suggestion: suggestion,
	}
}

// Service returns a remote service Error with the given code, HTTP status, message, and suggestion.
func Service(code string, statusCode int, serviceName, message, suggestion string, cause error) *Error {
	return &Error{
		Message:     message,
		Code:        code,
		Category:    CategoryService,
		StatusCode:  statusCode,
		ServiceName: serviceName,
		Suggestion:  suggestion,
		Cause:       cause,
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
