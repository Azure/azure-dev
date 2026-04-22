// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azdext

import (
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/status"
)

// Auth error metadata constants used in gRPC ErrorInfo for auth-related host APIs.
const (
	AuthErrorDomain = "azd.auth"
)

// Auth error reason codes used in gRPC ErrorInfo.Reason.
//
// For AAD-originated failures, the Reason is the originating Entra error code formatted as
// "AADSTS<code>" (e.g. "AADSTS530084") so extensions can match on the AAD code directly without
// azd having to define a synthetic taxonomy. The constants below cover azd-local conditions
// that have no corresponding Entra code.
const (
	AuthErrorReasonNotLoggedIn   = "AUTH_NOT_LOGGED_IN"
	AuthErrorReasonLoginRequired = "AUTH_LOGIN_REQUIRED"
)

// AuthErrorReason extracts the ErrorInfo.Reason from a gRPC status when the domain
// matches [AuthErrorDomain].
func AuthErrorReason(st *status.Status) string {
	for _, detail := range st.Details() {
		info, ok := detail.(*errdetails.ErrorInfo)
		if ok && info.Domain == AuthErrorDomain {
			return info.Reason
		}
	}

	return ""
}
