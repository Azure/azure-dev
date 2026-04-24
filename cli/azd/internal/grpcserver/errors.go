// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package grpcserver

import (
	"errors"
	"fmt"

	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/auth"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
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
	if !hasSuggestion && !isAuthErr {
		return err
	}

	code := codes.Unknown
	if st, ok := azdext.GRPCStatusFromError(err); ok {
		code = st.Code()
	}
	if isAuthErr {
		code = codes.Unauthenticated
	}

	st := status.New(code, statusMessage(err, suggestionErr))
	if isAuthErr {
		st = withAuthErrorInfo(st, err)
	}
	if hasSuggestion {
		st = withActionableErrorDetail(st, suggestionErr)
	}

	return st.Err()
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
