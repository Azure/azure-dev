// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azdext

import (
	"errors"
	"fmt"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
)

// ServiceError represents an HTTP/gRPC service error from an extension.
// It preserves structured error information for telemetry and error handling.
type ServiceError struct {
	// Message is the human-readable error message
	Message string
	// Details contains additional error details
	Details string
	// ErrorCode is the error code from the service (e.g., "Conflict", "NotFound")
	ErrorCode string
	// StatusCode is the HTTP status code (e.g., 409, 404, 500)
	StatusCode int
	// ServiceName is the service host/name for telemetry (e.g., "ai.azure.com")
	ServiceName string
}

// Error implements the error interface
func (e *ServiceError) Error() string {
	if e.Details != "" {
		return fmt.Sprintf("%s: %s", e.Message, e.Details)
	}
	return e.Message
}

// WrapError wraps a Go error into an ExtensionError for transmission over gRPC.
// It detects the error type and populates the appropriate source details.
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
		extErr.Details = extServiceErr.Details
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
	}

	return extErr
}

// UnwrapError converts an ExtensionError back to a typed Go error.
// It returns the appropriate error type based on the origin field.
func UnwrapError(msg *ExtensionError) error {
	if msg == nil || msg.GetMessage() == "" {
		return nil
	}

	// Check for service error details
	if svcErr := msg.GetServiceError(); svcErr != nil {
		return &ServiceError{
			Message:     msg.GetMessage(),
			Details:     msg.GetDetails(),
			ErrorCode:   svcErr.GetErrorCode(),
			StatusCode:  int(svcErr.GetStatusCode()),
			ServiceName: svcErr.GetServiceName(),
		}
	}

	// Return a generic service error with just the message/details
	return &ServiceError{
		Message: msg.GetMessage(),
		Details: msg.GetDetails(),
	}
}
