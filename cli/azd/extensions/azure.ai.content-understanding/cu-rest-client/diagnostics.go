// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package curestclient

import (
	"net/http"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

const MaxDiagnosticBytes = 2048

var (
	diagnosticAuthorization = regexp.MustCompile(`(?im)\b(?:proxy-)?authorization\s*[:=]\s*[^\r\n]*`)
	diagnosticSecret        = regexp.MustCompile(
		`(?im)\b(?:[a-z_]*token|api[-_]?key|ocp-apim-subscription-key|account[-_]?key|client_secret|` +
			`password|secret|sas|sig|schema|content|text|payload|body|path)\s*[:=]\s*[^\r\n]*`)
	diagnosticURL         = regexp.MustCompile(`(?i)\b[a-z][a-z0-9+.-]*://[^\s"'<>]+`)
	diagnosticWindowsPath = regexp.MustCompile(`(?i)(?:[a-z]:[\\/]|\\\\)[^\s"'<>]+`)
	diagnosticPOSIXPath   = regexp.MustCompile(`(^|[\s"'=(])(?:/|~/|\.\.?/)[^\s"'<>]+`)
	diagnosticQuotedValue = regexp.MustCompile(`(?m)"[^"\r\n]*(?:"|$)|(^|[\s:=(])'[^'\r\n]*(?:'|$)`)
	diagnosticFieldPath   = regexp.MustCompile(`\b(fieldSchema\.fields|fields|properties)\.[^\s:]+`)
	diagnosticModelName   = regexp.MustCompile(
		`(?i)\b(model(?: name)?\s+)(?:'([a-z0-9][a-z0-9._-]{0,127})'|"([a-z0-9][a-z0-9._-]{0,127})")`)
	diagnosticEndpointExample = regexp.MustCompile(
		`https://<your-resource>\.services\.ai\.azure\.com(\.(?:\s|$)|\s|$)`)
)

// RedactDiagnostic preserves diagnostic prose and model identifiers while removing
// credentials, URLs, paths, quoted customer values, and labeled document data.
// It must not be used to alter successful service results.
func RedactDiagnostic(message string) string {
	if len(message) > 64*1024 || !utf8.ValidString(message) {
		return "Diagnostic omitted because it exceeds the safe text limit."
	}

	const example = "https://<your-resource>.services.ai.azure.com"
	const placeholder = "CU_ENDPOINT_EXAMPLE_PLACEHOLDER"
	// Protect only the literal help example, not real URLs or URL suffixes.
	message = diagnosticEndpointExample.ReplaceAllString(message, placeholder+"${1}")
	if start := strings.IndexByte(message, '{'); start >= 0 {
		message = message[:start] + "[redacted payload]"
	}
	message = diagnosticAuthorization.ReplaceAllString(message, "Authorization: [redacted]")
	message = diagnosticSecret.ReplaceAllString(message, "[redacted value]")
	message = diagnosticURL.ReplaceAllString(message, "[redacted URL]")
	message = diagnosticWindowsPath.ReplaceAllString(message, "[redacted path]")
	message = diagnosticPOSIXPath.ReplaceAllString(message, "${1}[redacted path]")
	// Remove quotes only around explicitly identified, syntactically safe model names.
	message = diagnosticModelName.ReplaceAllString(message, "${1}${2}${3}")
	message = diagnosticQuotedValue.ReplaceAllStringFunc(message, func(value string) string {
		if value[0] == '"' {
			return "[redacted value]"
		}
		return value[:strings.IndexByte(value, '\'')] + "[redacted value]"
	})
	message = diagnosticFieldPath.ReplaceAllString(message, "${1}.[redacted]")
	message = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) && r != '\n' && r != '\t' || unicode.Is(unicode.Bidi_Control, r) {
			return -1
		}
		return r
	}, message)
	message = strings.TrimSpace(strings.ReplaceAll(message, placeholder, example))
	if len(message) > MaxDiagnosticBytes {
		return "Diagnostic omitted because it exceeds the safe text limit."
	}
	return message
}

func redactResponseDiagnostic(message string, response *http.Response) string {
	if response != nil && response.Request != nil {
		for _, name := range []string{"Authorization", "Proxy-Authorization", "Ocp-Apim-Subscription-Key"} {
			value := response.Request.Header.Get(name)
			if value == "" {
				continue
			}
			message = strings.ReplaceAll(message, value, "[redacted credential]")
			if name != "Ocp-Apim-Subscription-Key" {
				_, credential, found := strings.Cut(value, " ")
				if found && strings.TrimSpace(credential) != "" {
					message = strings.ReplaceAll(message, strings.TrimSpace(credential), "[redacted credential]")
				}
			}
		}
	}
	return RedactDiagnostic(message)
}
