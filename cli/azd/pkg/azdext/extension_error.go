// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azdext

import (
	"errors"
	"fmt"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
)

// ExtensionResponseError represents an HTTP response error returned from an extension over gRPC.
// It mirrors azcore.ResponseError and preserves structured error information for telemetry purposes.
type ExtensionResponseError struct {
	// Message is the human-readable error message
	Message string
	// Details contains additional error details
	Details string
	// ErrorCode is the error code from the service (e.g., "Conflict", "NotFound")
	ErrorCode string
	// StatusCode is the HTTP status code (e.g., 409, 404, 500)
	StatusCode int
	// ServiceName is the service name for telemetry (e.g., "ai.azure.com")
	ServiceName string
}

// Error implements the error interface
func (e *ExtensionResponseError) Error() string {
	if e.Details != "" {
		return fmt.Sprintf("%s: %s", e.Message, e.Details)
	}
	return e.Message
}

// HasServiceInfo returns true if the error contains service information for telemetry
func (e *ExtensionResponseError) HasServiceInfo() bool {
	return e.StatusCode > 0 && e.ServiceName != ""
}

// errorMessage defines the common interface for protobuf error messages
// This allows us to write generic unwrap logic for any generated proto message
type errorMessage interface {
	comparable
	GetMessage() string
	GetDetails() string
	GetErrorCode() string
	GetStatusCode() int32
	GetServiceName() string
}

// errorInfo is a helper struct to hold extracted error information
// before converting to a specific protobuf message type
type errorInfo struct {
	message    string
	details    string
	errorCode  string
	statusCode int32
	service    string
}

// captureErrorInfo extracts structured error information from a Go error.
// It handles nil errors, ExtensionResponseError, and azcore.ResponseError.
func captureErrorInfo(err error) errorInfo {
	if err == nil {
		return errorInfo{}
	}

	// Default to the error string
	info := errorInfo{message: err.Error()}

	// If it's already an ExtensionResponseError, preserve all fields including Details
	var extErr *ExtensionResponseError
	if errors.As(err, &extErr) {
		info.message = extErr.Message
		info.details = extErr.Details
		info.errorCode = extErr.ErrorCode
		info.statusCode = int32(extErr.StatusCode)
		info.service = extErr.ServiceName
		return info
	}

	// Try to extract structured error information from Azure SDK errors
	var respErr *azcore.ResponseError
	if errors.As(err, &respErr) {
		info.errorCode = respErr.ErrorCode
		info.statusCode = int32(respErr.StatusCode)
		if respErr.RawResponse != nil && respErr.RawResponse.Request != nil {
			info.service = respErr.RawResponse.Request.Host
		}
	}

	return info
}

// WrapErrorForServiceTarget wraps a Go error into a ServiceTargetErrorMessage for transmission over gRPC.
func WrapErrorForServiceTarget(err error) *ServiceTargetErrorMessage {
	info := captureErrorInfo(err)
	if info.message == "" {
		return nil
	}

	return &ServiceTargetErrorMessage{
		Message:     info.message,
		Details:     info.details,
		ErrorCode:   info.errorCode,
		StatusCode:  info.statusCode,
		ServiceName: info.service,
	}
}

// WrapErrorForFrameworkService wraps a Go error into a FrameworkServiceErrorMessage for transmission over gRPC.
func WrapErrorForFrameworkService(err error) *FrameworkServiceErrorMessage {
	info := captureErrorInfo(err)
	if info.message == "" {
		return nil
	}

	return &FrameworkServiceErrorMessage{
		Message:     info.message,
		Details:     info.details,
		ErrorCode:   info.errorCode,
		StatusCode:  info.statusCode,
		ServiceName: info.service,
	}
}

// unwrapError is a generic helper to convert protobuf error messages back to Go errors
func unwrapError[T errorMessage](msg T) error {
	var zero T
	if msg == zero || msg.GetMessage() == "" {
		return nil
	}

	return &ExtensionResponseError{
		Message:     msg.GetMessage(),
		Details:     msg.GetDetails(),
		ErrorCode:   msg.GetErrorCode(),
		StatusCode:  int(msg.GetStatusCode()),
		ServiceName: msg.GetServiceName(),
	}
}

// UnwrapErrorFromServiceTarget converts a ServiceTargetErrorMessage back to a Go error.
func UnwrapErrorFromServiceTarget(msg *ServiceTargetErrorMessage) error {
	return unwrapError(msg)
}

// UnwrapErrorFromFrameworkService converts a FrameworkServiceErrorMessage back to a Go error.
func UnwrapErrorFromFrameworkService(msg *FrameworkServiceErrorMessage) error {
	return unwrapError(msg)
}
