// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package grpcserver

import (
	"errors"
	"fmt"
	"log"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/auth"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	statuspb "google.golang.org/genproto/googleapis/rpc/status"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

// mapHostError serializes a host-originated Go error into a gRPC status error for transport
// to extensions.
//
// This is the gRPC serialization boundary: the original error chain (e.g. *auth.AuthFailedError)
// is intentionally not preserved across the wire — only the structured details defined here.
//
// Auth-related errors are reported with codes.Unauthenticated and an azd.auth ErrorInfo detail
// (preserving AADSTS<code> reasons from Entra). ErrorWithSuggestion errors carry an
// ActionableErrorDetail so consumers receive suggestion + links structurally.
//
// status.Message is always err.Error() (or ErrorWithSuggestion.Message when set). Suggestion
// text is never concatenated into status.Message; consumers must read ActionableErrorDetail
// for remediation guidance.
func mapHostError(err error) error {
	if err == nil {
		return nil
	}

	suggestionErr, hasSuggestion := errors.AsType[*internal.ErrorWithSuggestion](err)
	isAuthErr := isAuthError(err)
	responseErr, hasResponseError := errors.AsType[*azcore.ResponseError](err)
	relayedErr := relayedExtensionError(err)
	existingStatus, hasExistingStatus := azdext.GRPCStatusFromError(err)
	if !hasSuggestion && !isAuthErr && !hasResponseError && relayedErr == nil {
		return err
	}

	code := codes.Unknown
	if hasExistingStatus {
		code = existingStatus.Code()
	}
	if isAuthErr {
		code = codes.Unauthenticated
	}

	st := hostErrorStatus(existingStatus, hasExistingStatus, code, statusMessage(err, suggestionErr))
	if isAuthErr {
		st = withAuthErrorInfo(st, err)
	}
	if hasSuggestion {
		st = withActionableErrorDetail(st, suggestionErr)
	}
	if relayedErr != nil {
		st = withRelayedExtensionErrorDetail(st, relayedErr)
	} else if hasResponseError && !hasServiceErrorDetail(st) {
		st = withServiceErrorDetail(st, responseErr)
	}

	return st.Err()
}

func relayedExtensionError(err error) *azdext.ExtensionError {
	if _, ok := errors.AsType[*azdext.ServiceError](err); ok {
		return azdext.WrapError(err)
	}
	if _, ok := errors.AsType[*azdext.LocalError](err); ok {
		return azdext.WrapError(err)
	}
	if _, ok := errors.AsType[*azdext.ToolError](err); ok {
		return azdext.WrapError(err)
	}
	return nil
}

func hostErrorStatus(
	existingStatus *status.Status,
	hasExistingStatus bool,
	code codes.Code,
	message string,
) *status.Status {
	if !hasExistingStatus {
		return status.New(code, message)
	}

	statusProto := proto.Clone(existingStatus.Proto()).(*statuspb.Status)
	statusProto.Code = int32(code) //nolint:gosec // gRPC status codes use the defined int32 range
	statusProto.Message = message
	return status.FromProto(statusProto)
}

func hasServiceErrorDetail(st *status.Status) bool {
	return azdext.ServiceErrorDetailFromStatus(st) != nil
}

func hasRelayedExtensionErrorDetail(st *status.Status) bool {
	return azdext.ExtensionErrorFromStatus(st) != nil
}

func withRelayedExtensionErrorDetail(st *status.Status, relayedErr *azdext.ExtensionError) *status.Status {
	if st == nil || relayedErr == nil || hasRelayedExtensionErrorDetail(st) {
		return st
	}

	withDetails, detailErr := st.WithDetails(relayedErr)
	if detailErr != nil {
		log.Printf("failed to attach relayed extension error detail to gRPC status: %v", detailErr)
		return st
	}

	return withDetails
}

func withServiceErrorDetail(st *status.Status, responseErr *azcore.ResponseError) *status.Status {
	if st == nil || responseErr == nil {
		return st
	}

	detail := &azdext.ServiceErrorDetail{
		ErrorCode:   responseErr.ErrorCode,
		StatusCode:  int32(responseErr.StatusCode), //nolint:gosec // HTTP status codes fit in int32
		ServiceName: responseErrorServiceName(responseErr),
	}
	withDetails, detailErr := st.WithDetails(detail)
	if detailErr != nil {
		log.Printf("failed to attach service error detail to gRPC status: %v", detailErr)
		return st
	}

	return withDetails
}

func responseErrorServiceName(responseErr *azcore.ResponseError) string {
	if responseErr == nil || responseErr.RawResponse == nil || responseErr.RawResponse.Request == nil {
		return ""
	}

	request := responseErr.RawResponse.Request
	if request.Host != "" {
		return request.Host
	}
	if request.URL != nil {
		return request.URL.Hostname()
	}

	return ""
}

// statusMessage returns the user-facing message that should populate status.Message.
// When the source is an ErrorWithSuggestion with an explicit Message, that wins. Otherwise,
// if err is already a gRPC status error, use its Message (avoids nesting "rpc error: ..."
// prefixes in the new status). Falls back to err.Error(). Suggestion text is never appended.
func statusMessage(err error, suggestionErr *internal.ErrorWithSuggestion) string {
	if suggestionErr != nil && suggestionErr.Message != "" {
		return suggestionErr.Message
	}
	if st, ok := azdext.GRPCStatusFromError(err); ok {
		return st.Message()
	}
	return err.Error()
}

// isAuthError reports whether err's chain contains a known auth-failure type that should be
// surfaced over gRPC as codes.Unauthenticated.
func isAuthError(err error) bool {
	if errors.Is(err, auth.ErrNoCurrentUser) {
		return true
	}
	if _, ok := errors.AsType[*auth.ReLoginRequiredError](err); ok {
		return true
	}
	if _, ok := errors.AsType[*auth.AuthFailedError](err); ok {
		return true
	}
	return false
}

func withAuthErrorInfo(st *status.Status, err error) *status.Status {
	reason := grpcAuthReason(err)
	if reason == "" {
		return st
	}

	withDetails, detailErr := st.WithDetails(&errdetails.ErrorInfo{
		Reason: reason,
		Domain: azdext.AuthErrorDomain,
	})
	if detailErr != nil {
		log.Printf("failed to attach auth ErrorInfo to gRPC status: %v", detailErr)
		return st
	}

	return withDetails
}

func withActionableErrorDetail(st *status.Status, err *internal.ErrorWithSuggestion) *status.Status {
	if err.Suggestion == "" && len(err.Links) == 0 {
		return st
	}

	withDetails, detailErr := st.WithDetails(&azdext.ActionableErrorDetail{
		Suggestion: err.Suggestion,
		Links:      azdext.WrapErrorLinks(err.Links),
	})
	if detailErr != nil {
		log.Printf("failed to attach ActionableErrorDetail to gRPC status: %v", detailErr)
		return st
	}

	return withDetails
}

func grpcAuthReason(err error) string {
	if errors.Is(err, auth.ErrNoCurrentUser) {
		return azdext.AuthErrorReasonNotLoggedIn
	}

	// Pass through the originating AAD error code (e.g., "AADSTS530084") when available.
	// This preserves Entra's own semantics rather than redefining them on azd's side.
	if authFailed, ok := errors.AsType[*auth.AuthFailedError](err); ok {
		if authFailed.Parsed != nil && len(authFailed.Parsed.ErrorCodes) > 0 {
			return fmt.Sprintf("AADSTS%d", authFailed.Parsed.ErrorCodes[0])
		}
	}

	if _, ok := errors.AsType[*auth.ReLoginRequiredError](err); ok {
		return azdext.AuthErrorReasonLoginRequired
	}

	return ""
}
