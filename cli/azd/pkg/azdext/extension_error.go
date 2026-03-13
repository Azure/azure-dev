// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azdext

import (
	"errors"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// ServiceError represents an HTTP/gRPC service error from an extension.
// It preserves structured error information for telemetry and error handling.
//
// ServiceError supports Go error wrapping via the optional Cause field.
// When Cause is set, [errors.Unwrap] returns it, enabling the standard
// errors.Is / errors.As traversal through the cause chain while still
// carrying the structured service metadata for telemetry.
type ServiceError struct {
	// Message is the human-readable error message
	Message string
	// ErrorCode is the error code from the service (e.g., "Conflict", "NotFound")
	ErrorCode string
	// StatusCode is the HTTP status code (e.g., 409, 404, 500)
	StatusCode int
	// ServiceName is the service host/name for telemetry (e.g., "ai.azure.com")
	ServiceName string
	// Suggestion contains optional user-facing remediation guidance.
	Suggestion string
	// Cause is the optional underlying error that triggered this service error.
	// It is not transmitted over gRPC but enables local errors.Is / errors.As checks.
	Cause error
}

// LocalError represents non-service extension errors, such as validation/config failures.
//
// LocalError supports Go error wrapping via the optional Cause field.
// When Cause is set, [errors.Unwrap] returns it, enabling the standard
// errors.Is / errors.As traversal through the cause chain while still
// carrying the structured metadata (Code, Category, Suggestion) for
// telemetry and UX display.
type LocalError struct {
	// Message is the human-readable error message
	Message string
	// Code is an extension-defined machine-readable error code (lowercase snake_case, e.g. "missing_subscription_id").
	// It appears in telemetry as the last segment of ext.<category>.<code>.
	Code string
	// Category classifies the local error (for example: user, validation, dependency)
	Category LocalErrorCategory
	// Suggestion contains optional user-facing remediation guidance.
	Suggestion string
	// Cause is the optional underlying error that triggered this local error.
	// It is not transmitted over gRPC but enables local errors.Is / errors.As checks.
	Cause error
}

// Error implements the error interface.
func (e *LocalError) Error() string {
	return e.Message
}

// Unwrap returns the underlying cause, enabling errors.Is and errors.As
// to traverse through the LocalError to the original error.
func (e *LocalError) Unwrap() error {
	return e.Cause
}

// Error implements the error interface.
func (e *ServiceError) Error() string {
	return e.Message
}

// Unwrap returns the underlying cause, enabling errors.Is and errors.As
// to traverse through the ServiceError to the original error.
func (e *ServiceError) Unwrap() error {
	return e.Cause
}

// WrapError converts a Go error into an ExtensionError proto for transmission to the azd host.
// It is called from extension processes (via [ReportError] and envelope SetError methods)
// to serialize errors before sending them over gRPC.
//
// The function applies detection in priority order:
//  1. [ServiceError] / [LocalError] — already structured by extension code (highest specificity)
//  2. [azcore.ResponseError] — Azure SDK HTTP errors
//  3. gRPC Unauthenticated — auto-classified as auth category (safety net)
//  4. Fallback — unclassified error with original message
//
// The counterpart [UnwrapError] is called from the azd host to deserialize
// the proto back into typed Go errors for telemetry classification.
func WrapError(err error) *ExtensionError {
	if err == nil {
		return nil
	}

	extErr := &ExtensionError{
		Message: err.Error(),
		Origin:  ErrorOrigin_ERROR_ORIGIN_UNSPECIFIED,
	}

	// Check for extension error types (already structured)
	var extServiceErr *ServiceError
	if errors.As(err, &extServiceErr) {
		extErr.Message = extServiceErr.Message
		extErr.Suggestion = extServiceErr.Suggestion
		extErr.Origin = ErrorOrigin_ERROR_ORIGIN_SERVICE
		extErr.Source = &ExtensionError_ServiceError{
			ServiceError: &ServiceErrorDetail{
				ErrorCode: extServiceErr.ErrorCode,
				//nolint:gosec // G115: HTTP status codes are well within int32 range
				StatusCode:  int32(extServiceErr.StatusCode),
				ServiceName: extServiceErr.ServiceName,
			},
		}
		return extErr
	}

	var extLocalErr *LocalError
	if errors.As(err, &extLocalErr) {
		normalizedCategory := NormalizeLocalErrorCategory(extLocalErr.Category)
		extErr.Message = extLocalErr.Message
		extErr.Suggestion = extLocalErr.Suggestion
		extErr.Origin = ErrorOrigin_ERROR_ORIGIN_LOCAL
		extErr.Source = &ExtensionError_LocalError{
			LocalError: &LocalErrorDetail{
				Code:     extLocalErr.Code,
				Category: string(normalizedCategory),
			},
		}
		return extErr
	}

	// Try to detect Azure SDK errors
	var respErr *azcore.ResponseError
	if errors.As(err, &respErr) {
		extErr.Origin = ErrorOrigin_ERROR_ORIGIN_SERVICE
		serviceName := ""
		if respErr.RawResponse != nil && respErr.RawResponse.Request != nil {
			serviceName = respErr.RawResponse.Request.Host
		}
		extErr.Source = &ExtensionError_ServiceError{
			ServiceError: &ServiceErrorDetail{
				ErrorCode: respErr.ErrorCode,
				//nolint:gosec // G115: HTTP status codes are well within int32 range
				StatusCode:  int32(respErr.StatusCode),
				ServiceName: serviceName,
			},
		}
		return extErr
	}

	// Detect gRPC Unauthenticated errors as an auth safety net.
	// If the extension didn't already classify the error, this ensures auth failures
	// from azd host calls are reported with the correct category in telemetry.
	// Use errors.As to detect gRPC status errors even when wrapped by fmt.Errorf.
	var grpcErr interface{ GRPCStatus() *status.Status }
	if errors.As(err, &grpcErr) {
		if st := grpcErr.GRPCStatus(); st.Code() == codes.Unauthenticated {
			extErr.Origin = ErrorOrigin_ERROR_ORIGIN_LOCAL
			extErr.Message = st.Message()
			extErr.Source = &ExtensionError_LocalError{
				LocalError: &LocalErrorDetail{
					Code:     "auth_failed",
					Category: string(LocalErrorCategoryAuth),
				},
			}
			return extErr
		}
	}

	return extErr
}

// UnwrapError converts an ExtensionError proto back to a typed Go error.
// It is called from the azd host (via [ExtensionService.ReportError] handler
// and envelope GetError methods) to deserialize errors received from extensions
// for telemetry classification and error handling.
// It returns the appropriate error type based on the origin field.
func UnwrapError(msg *ExtensionError) error {
	if msg == nil || msg.GetMessage() == "" {
		return nil
	}

	// Check for service error details
	if svcErr := msg.GetServiceError(); svcErr != nil {
		return &ServiceError{
			Message:     msg.GetMessage(),
			ErrorCode:   svcErr.GetErrorCode(),
			StatusCode:  int(svcErr.GetStatusCode()),
			ServiceName: svcErr.GetServiceName(),
			Suggestion:  msg.GetSuggestion(),
		}
	}

	if localErr := msg.GetLocalError(); localErr != nil {
		normalizedCategory := ParseLocalErrorCategory(localErr.GetCategory())
		return &LocalError{
			Message:    msg.GetMessage(),
			Code:       localErr.GetCode(),
			Category:   normalizedCategory,
			Suggestion: msg.GetSuggestion(),
		}
	}

	if msg.GetOrigin() == ErrorOrigin_ERROR_ORIGIN_LOCAL {
		return &LocalError{
			Message:    msg.GetMessage(),
			Category:   LocalErrorCategoryLocal,
			Suggestion: msg.GetSuggestion(),
		}
	}

	if msg.GetOrigin() == ErrorOrigin_ERROR_ORIGIN_SERVICE {
		return &ServiceError{
			Message:    msg.GetMessage(),
			Suggestion: msg.GetSuggestion(),
		}
	}

	return &LocalError{
		Message:    msg.GetMessage(),
		Category:   LocalErrorCategoryLocal,
		Suggestion: msg.GetSuggestion(),
	}
}
