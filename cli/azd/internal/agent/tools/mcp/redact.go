// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package mcp

import "regexp"

// redactSensitiveData removes sensitive information from strings that may be logged.
// It handles: Authorization headers, bearer tokens, API tokens, and raw token strings.
func redactSensitiveData(s string) string {
	// Redact Authorization header values
	s = authHeaderRedactor.ReplaceAllString(s, "${1}[REDACTED]")
	// Redact bearer tokens
	s = bearerTokenRedactor.ReplaceAllString(s, "Bearer [REDACTED]")
	// Redact token= query parameters
	s = tokenParamRedactor.ReplaceAllString(s, "token=[REDACTED]")
	return s
}

var (
	authHeaderRedactor  = regexp.MustCompile(`(?i)(Authorization:\s*)(.+)`)
	bearerTokenRedactor = regexp.MustCompile(`(?i)Bearer\s+[A-Za-z0-9\-._~+/]+=*`)
	tokenParamRedactor  = regexp.MustCompile(`(?i)token=[A-Za-z0-9\-._~+/]+=*`)
)
