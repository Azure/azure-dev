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
const (
	AuthErrorReasonNotLoggedIn            = "AUTH_NOT_LOGGED_IN"
	AuthErrorReasonLoginRequired          = "AUTH_LOGIN_REQUIRED"
	AuthErrorReasonTokenProtectionBlocked = "AUTH_TOKEN_PROTECTION_BLOCKED"
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
