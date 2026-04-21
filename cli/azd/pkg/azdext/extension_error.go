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
}

// LocalError represents non-service extension errors, such as validation/config failures.
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
}

// Error implements the error interface.
func (e *LocalError) Error() string {
	return e.Message
}

// Error implements the error interface.
func (e *ServiceError) Error() string {
	return e.Message
}

// WrapError converts a Go error into an ExtensionError proto for transmission to the azd host.
// It is called from extension processes (via [ReportError] and envelope SetError methods)
// to serialize errors before sending them over gRPC.
//
// The function applies detection in priority order:
//  1. [ServiceError] / [LocalError] — already structured by extension code (highest specificity)
//  2. [azcore.ResponseError] — Azure SDK HTTP errors
//  3. gRPC Unauthenticated — auto-classified as auth category, preserving auth subtypes from ErrorInfo when present
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
	if extServiceErr, ok := errors.AsType[*ServiceError](err); ok {
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

	if extLocalErr, ok := errors.AsType[*LocalError](err); ok {
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
	if respErr, ok := errors.AsType[*azcore.ResponseError](err); ok {
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
	// from azd host calls are reported with the correct category in telemetry,
	// and preserves specific auth subtypes when the host attached auth ErrorInfo details.
	// Use errors.AsType to detect gRPC status errors even when wrapped by fmt.Errorf.
	if grpcErr, ok := errors.AsType[interface {
		error
		GRPCStatus() *status.Status
	}](err); ok {
		if st := grpcErr.GRPCStatus(); st.Code() == codes.Unauthenticated {
			extErr.Origin = ErrorOrigin_ERROR_ORIGIN_LOCAL
			extErr.Message = st.Message()
			extErr.Source = &ExtensionError_LocalError{
				LocalError: &LocalErrorDetail{
					Code:     authLocalErrorCode(st),
					Category: string(LocalErrorCategoryAuth),
				},
			}
			return extErr
		}
	}

	return extErr
}

func authLocalErrorCode(st *status.Status) string {
	switch AuthErrorReason(st) {
	case AuthErrorReasonNotLoggedIn:
		return "not_logged_in"
	case AuthErrorReasonLoginRequired:
		return "login_required"
	case AuthErrorReasonTokenProtectionBlocked:
		return "token_protection_blocked"
	default:
		return "auth_failed"
	}
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
