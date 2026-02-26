// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package exterrors

import (
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/status"
)

// FromAzdHost wraps a gRPC error returned by an azd host service call
// into a structured Internal LocalError. It preserves the server's
// ErrorInfo reason code (from the azd.ai domain) when available,
// falling back to the provided code.
func FromAzdHost(err error, fallbackCode string) error {
	if err == nil {
		return nil
	}

	if IsCancellation(err) {
		return Cancelled(err.Error())
	}

	st, ok := status.FromError(err)
	if !ok {
		return Internal(fallbackCode, err.Error())
	}

	code := fallbackCode
	if reason := aiErrorReason(st); reason != "" {
		code = reason
	}

	return Internal(code, st.Message())
}

// aiErrorReason extracts the ErrorInfo.Reason from a gRPC status
// when the domain matches azdext.AiErrorDomain.
func aiErrorReason(st *status.Status) string {
	for _, detail := range st.Details() {
		info, ok := detail.(*errdetails.ErrorInfo)
		if ok && info.Domain == azdext.AiErrorDomain {
			return info.Reason
		}
	}
	return ""
}
