// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package exterrors

import (
	"errors"
	"regexp"

	"azure.ai.content-understanding/internal/cuclient"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"google.golang.org/grpc/status"
)

const MaxDiagnosticBytes = 2048

var diagnosticCode = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{0,127}$`)

// Redact is for diagnostics only, never command results or service payloads.
// Unknown unstructured errors must still use FromError's fixed fallback message.
func Redact(message string) string {
	return cuclient.RedactDiagnostic(message)
}

func safeCode(value string) string {
	if diagnosticCode.MatchString(value) {
		return value
	}
	return CodeUnexpected
}

func sanitizeError(result *error) {
	if *result == nil {
		return
	}
	wrapped := *result
	if local, ok := errors.AsType[*azdext.LocalError](wrapped); ok {
		copy := *local
		copy.Code, copy.Message, copy.Suggestion = safeCode(local.Code), Redact(local.Message), Redact(local.Suggestion)
		if copy.Code != local.Code || copy.Message != local.Message || copy.Suggestion != local.Suggestion ||
			Redact(wrapped.Error()) != wrapped.Error() || len(local.Links) != 0 || len(local.CauseTypes) != 0 {
			copy.Links, copy.CauseTypes = nil, nil
			*result = &copy
		}
	} else if service, ok := errors.AsType[*azdext.ServiceError](wrapped); ok {
		copy := *service
		copy.ErrorCode = safeCode(service.ErrorCode)
		copy.Message, copy.Suggestion = Redact(service.Message), Redact(service.Suggestion)
		if copy.ServiceName != "ContentUnderstanding" {
			copy.ServiceName = ""
		}
		if copy.ErrorCode != service.ErrorCode || copy.Message != service.Message || copy.Suggestion != service.Suggestion ||
			Redact(wrapped.Error()) != wrapped.Error() || len(service.Links) != 0 ||
			copy.ServiceName != service.ServiceName {
			copy.Links = nil
			*result = &copy
		}
	} else if tool, ok := errors.AsType[*azdext.ToolError](wrapped); ok {
		copy := *tool
		copy.ToolName, copy.Message, copy.Suggestion = safeCode(tool.ToolName), Redact(tool.Message), Redact(tool.Suggestion)
		if copy.ToolName != tool.ToolName || copy.Message != tool.Message || copy.Suggestion != tool.Suggestion ||
			Redact(wrapped.Error()) != wrapped.Error() || len(tool.Links) != 0 {
			copy.Links = nil
			*result = &copy
		}
	} else if detail := azdext.ActionableErrorDetailFromError(wrapped); detail != nil {
		st := status.Convert(wrapped)
		message, suggestion := Redact(st.Message()), Redact(detail.Suggestion)
		if message != st.Message() || suggestion != detail.Suggestion || len(detail.Links) != 0 {
			safe, err := status.New(st.Code(), message).WithDetails(&azdext.ActionableErrorDetail{Suggestion: suggestion})
			if err != nil {
				*result = status.Error(st.Code(), message)
			} else {
				*result = safe.Err()
			}
		}
	}
}
