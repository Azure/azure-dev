// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package exterrors

import (
	"errors"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"google.golang.org/grpc/status"
)

const MaxDiagnosticBytes = 2048

var (
	authorization = regexp.MustCompile(`(?im)\b(?:proxy-)?authorization\s*[:=]\s*[^\r\n]*`)
	secretValue   = regexp.MustCompile(
		`(?im)\b(?:[a-z_]*token|api[-_]?key|ocp-apim-subscription-key|accountkey|client_secret|` +
			`password|secret|sas|sig|schema|content|text|payload|body|path)\s*[:=]\s*[^\r\n]*`)
	diagnosticURL  = regexp.MustCompile(`(?i)\b[a-z][a-z0-9+.-]*://[^\s"'<>]+`)
	windowsPath    = regexp.MustCompile(`(?i)(?:[a-z]:[\\/]|\\\\)[^\s"'<>]+`)
	posixPath      = regexp.MustCompile(`(^|[\s"'=(])(?:/|~/|\.\.?/)[^\s"'<>]+`)
	quotedValue    = regexp.MustCompile(`"[^"\r\n]*"|'[^'\r\n]*'`)
	fieldPath      = regexp.MustCompile(`\b(fieldSchema\.fields|fields|properties)\.[^\s:]+`)
	diagnosticCode = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{0,127}$`)
)

// Redact is for diagnostics only, never command results or service payloads.
// Unknown unstructured errors must still use FromError's fixed fallback message.
func Redact(message string) string {
	if len(message) > 64*1024 || !utf8.ValidString(message) {
		return "Diagnostic omitted because it exceeds the safe text limit."
	}
	if start := strings.IndexByte(message, '{'); start >= 0 {
		message = message[:start] + "[redacted payload]"
	}
	message = authorization.ReplaceAllString(message, "Authorization: [redacted]")
	message = secretValue.ReplaceAllString(message, "[redacted value]")
	message = diagnosticURL.ReplaceAllString(message, "[redacted URL]")
	message = windowsPath.ReplaceAllString(message, "[redacted path]")
	message = posixPath.ReplaceAllString(message, "${1}[redacted path]")
	message = quotedValue.ReplaceAllString(message, "[redacted value]")
	message = fieldPath.ReplaceAllString(message, "${1}.[redacted]")
	message = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) && r != '\n' && r != '\t' {
			return -1
		}
		return r
	}, message)
	message = strings.TrimSpace(message)
	if len(message) > MaxDiagnosticBytes {
		return "Diagnostic omitted because it exceeds the safe text limit."
	}
	return message
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
