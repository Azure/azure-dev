// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package models

// ErrorDetail represents a standardized error response across vendors
type ErrorDetail struct {
	Code        string // Standard error code (e.g., "INVALID_REQUEST", "RATE_LIMITED")
	Message     string // User-friendly error message
	Retryable   bool   // Whether the operation can be retried
	VendorError error  // Original vendor-specific error (for debugging)
	VendorCode  string // Vendor-specific error code
}

// Common error codes
const (
	ErrorCodeInvalidRequest     = "INVALID_REQUEST"
	ErrorCodeNotFound           = "NOT_FOUND"
	ErrorCodeUnauthorized       = "UNAUTHORIZED"
	ErrorCodeForbidden          = "FORBIDDEN"
	ErrorCodeRateLimited        = "RATE_LIMITED"
	ErrorCodeServiceUnavailable = "SERVICE_UNAVAILABLE"
	ErrorCodeInternalError      = "INTERNAL_ERROR"
	ErrorCodeInvalidModel       = "INVALID_MODEL"
	ErrorCodeInvalidFileSize    = "INVALID_FILE_SIZE"
	ErrorCodeOperationFailed    = "OPERATION_FAILED"
)

// Error implements the error interface
func (e *ErrorDetail) Error() string {
	return e.Message
}
