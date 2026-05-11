// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azdext

import (
	"errors"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/azure/azure-dev/cli/azd/pkg/errorhandler"
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
	// Links contains optional reference links rendered alongside the suggestion.
	Links []errorhandler.ErrorLink
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
	// Links contains optional reference links rendered alongside the suggestion.
	Links []errorhandler.ErrorLink
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
//  3. gRPC status — host-originated errors carrying ActionableErrorDetail and/or auth ErrorInfo
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
		extErr.Links = WrapErrorLinks(extServiceErr.Links)
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
		extErr.Links = WrapErrorLinks(extLocalErr.Links)
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

	if st, ok := GRPCStatusFromError(err); ok {
		populateExtensionErrorFromStatus(extErr, st)
	}

	return extErr
}

// populateExtensionErrorFromStatus shapes extErr from a host-originated gRPC status.
// It computes (message, suggestion, links, origin, source) in a single pass without
// the string-equality fallbacks that earlier versions used.
func populateExtensionErrorFromStatus(extErr *ExtensionError, st *status.Status) {
	actionable := ActionableErrorDetailFromStatus(st)
	isAuth := st.Code() == codes.Unauthenticated

	if actionable == nil && !isAuth {
		// Plain gRPC error with no host metadata; leave extErr as-is so the caller
		// surfaces the original message via the unspecified origin path.
		return
	}

	// status.Message is the canonical user-facing payload for host errors.
	extErr.Message = st.Message()
	if actionable != nil {
		extErr.Suggestion = actionable.GetSuggestion()
		extErr.Links = actionable.GetLinks()
	}

	switch {
	case isAuth:
		// Auth safety net: classify as auth even when the extension didn't pre-classify.
		// Preserves the AAD-originated reason (or AUTH_* azd-local reason) via authLocalErrorCode.
		extErr.Origin = ErrorOrigin_ERROR_ORIGIN_LOCAL
		extErr.Source = &ExtensionError_LocalError{
			LocalError: &LocalErrorDetail{
				Code:     authLocalErrorCode(st),
				Category: string(LocalErrorCategoryAuth),
			},
		}
	default:
		// Non-auth host-originated actionable error.
		extErr.Origin = ErrorOrigin_ERROR_ORIGIN_LOCAL
		extErr.Source = &ExtensionError_LocalError{
			LocalError: &LocalErrorDetail{
				Category: string(LocalErrorCategoryLocal),
			},
		}
	}
}

// GRPCStatusFromError extracts a *status.Status from err's chain when one is present.
// Returns (nil, false) if err does not carry a gRPC status.
func GRPCStatusFromError(err error) (*status.Status, bool) {
	grpcErr, ok := errors.AsType[interface {
		error
		GRPCStatus() *status.Status
	}](err)
	if !ok {
		return nil, false
	}

	st := grpcErr.GRPCStatus()
	return st, st != nil
}

// ActionableErrorDetailFromError extracts host-originated actionable guidance from a gRPC status error.
func ActionableErrorDetailFromError(err error) *ActionableErrorDetail {
	st, ok := GRPCStatusFromError(err)
	if !ok {
		return nil
	}

	return ActionableErrorDetailFromStatus(st)
}

// ActionableErrorDetailFromStatus extracts host-originated actionable guidance from a gRPC status.
func ActionableErrorDetailFromStatus(st *status.Status) *ActionableErrorDetail {
	if st == nil {
		return nil
	}

	for _, detail := range st.Details() {
		if actionable, ok := detail.(*ActionableErrorDetail); ok {
			return actionable
		}
	}

	return nil
}

func authLocalErrorCode(st *status.Status) string {
	switch AuthErrorReason(st) {
	case AuthErrorReasonNotLoggedIn:
		return "not_logged_in"
	case AuthErrorReasonLoginRequired:
		return "login_required"
	}

	// All other reasons (including AAD-originated codes like "AADSTS530084") collapse to a
	// generic "auth_failed" label. Extensions that need per-AAD-code granularity can read the
	// raw reason directly from the gRPC ErrorInfo via AuthErrorReason.
	return "auth_failed"
}

// UnwrapError converts an ExtensionError proto back to a typed Go error.
// It is called from the azd host (via [ExtensionService.ReportError] handler
// and envelope GetError methods) to deserialize errors received from extensions
// for telemetry classification and error handling.
// It returns the appropriate error type based on the origin field.
func UnwrapError(msg *ExtensionError) error {
	if msg == nil {
		return nil
	}

	links := UnwrapErrorLinks(msg.GetLinks())

	// Check for service error details
	if svcErr := msg.GetServiceError(); svcErr != nil {
		return &ServiceError{
			Message:     msg.GetMessage(),
			ErrorCode:   svcErr.GetErrorCode(),
			StatusCode:  int(svcErr.GetStatusCode()),
			ServiceName: svcErr.GetServiceName(),
			Suggestion:  msg.GetSuggestion(),
			Links:       links,
		}
	}

	if localErr := msg.GetLocalError(); localErr != nil {
		normalizedCategory := ParseLocalErrorCategory(localErr.GetCategory())
		return &LocalError{
			Message:    msg.GetMessage(),
			Code:       localErr.GetCode(),
			Category:   normalizedCategory,
			Suggestion: msg.GetSuggestion(),
			Links:      links,
		}
	}

	if msg.GetOrigin() == ErrorOrigin_ERROR_ORIGIN_LOCAL {
		return &LocalError{
			Message:    msg.GetMessage(),
			Category:   LocalErrorCategoryLocal,
			Suggestion: msg.GetSuggestion(),
			Links:      links,
		}
	}

	if msg.GetOrigin() == ErrorOrigin_ERROR_ORIGIN_SERVICE {
		return &ServiceError{
			Message:    msg.GetMessage(),
			Suggestion: msg.GetSuggestion(),
			Links:      links,
		}
	}

	return &LocalError{
		Message:    msg.GetMessage(),
		Category:   LocalErrorCategoryLocal,
		Suggestion: msg.GetSuggestion(),
		Links:      links,
	}
}

// WrapErrorLinks converts errorhandler.ErrorLink values into proto ErrorLink messages.
func WrapErrorLinks(links []errorhandler.ErrorLink) []*ErrorLink {
	if len(links) == 0 {
		return nil
	}

	protoLinks := make([]*ErrorLink, len(links))
	for i, link := range links {
		protoLinks[i] = &ErrorLink{
			Url:   link.URL,
			Title: link.Title,
		}
	}

	return protoLinks
}

// UnwrapErrorLinks converts proto ErrorLink messages back into errorhandler.ErrorLink values.
func UnwrapErrorLinks(links []*ErrorLink) []errorhandler.ErrorLink {
	if len(links) == 0 {
		return nil
	}

	unwrapped := make([]errorhandler.ErrorLink, len(links))
	for i, link := range links {
		if link == nil {
			continue
		}

		unwrapped[i] = errorhandler.ErrorLink{
			URL:   link.GetUrl(),
			Title: link.GetTitle(),
		}
	}

	return unwrapped
}
