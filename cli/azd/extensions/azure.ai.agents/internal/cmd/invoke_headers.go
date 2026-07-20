// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"fmt"
	"net/http"
	"strings"

	"azureaiagent/internal/exterrors"
)

// clientHeaderPrefix is the required prefix (case-insensitive) for headers
// passed via --client-header. The Responses protocol proxy forwards the x-client-*
// family to the agent container; other header names are rejected so users are
// not misled into thinking arbitrary headers reach the agent.
const clientHeaderPrefix = "x-client-"

// parseCustomHeaders converts raw "Name: Value" flag entries (curl -H style)
// into an http.Header with canonicalized keys. Repeating a name adds multiple
// values. Only x-client-* names are accepted; any other name (or a malformed
// entry) is rejected so the mistake surfaces before any request is sent.
func parseCustomHeaders(entries []string) (http.Header, error) {
	if len(entries) == 0 {
		return nil, nil
	}

	headers := http.Header{}
	for _, entry := range entries {
		name, value, ok := strings.Cut(entry, ":")
		if !ok {
			return nil, exterrors.Validation(
				exterrors.CodeInvalidParameter,
				fmt.Sprintf("invalid --client-header value %q", entry),
				`use the "Name: Value" format, for example --client-header "x-client-request-id: abc123"`,
			)
		}

		name = strings.TrimSpace(name)
		if name == "" {
			return nil, exterrors.Validation(
				exterrors.CodeInvalidParameter,
				fmt.Sprintf("invalid --client-header value %q: header name is empty", entry),
				`use the "Name: Value" format, for example --client-header "x-client-request-id: abc123"`,
			)
		}
		if !isValidHeaderName(name) {
			return nil, exterrors.Validation(
				exterrors.CodeInvalidParameter,
				fmt.Sprintf("invalid --client-header name %q", name),
				"header names may only contain letters, digits, and the characters !#$%&'*+-.^_`|~",
			)
		}
		if !strings.HasPrefix(strings.ToLower(name), clientHeaderPrefix) {
			return nil, exterrors.Validation(
				exterrors.CodeInvalidParameter,
				fmt.Sprintf("unsupported --client-header name %q", name),
				fmt.Sprintf(
					"only %s* headers can be sent with --client-header; "+
						"use --user-identity or --call-id for identity headers",
					clientHeaderPrefix,
				),
			)
		}

		headers.Add(name, strings.TrimSpace(value))
	}

	return headers, nil
}

// isValidHeaderName reports whether name is a valid HTTP field name per the
// RFC 7230 token grammar. This guards against sending malformed headers (for
// example, names containing spaces) that would otherwise fail deeper in the
// stack with a less actionable error.
func isValidHeaderName(name string) bool {
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z',
			r >= 'A' && r <= 'Z',
			r >= '0' && r <= '9':
			continue
		}
		if !strings.ContainsRune("!#$%&'*+-.^_`|~", r) {
			return false
		}
	}
	return true
}

// applyCustomHeaders adds the parsed custom headers to req. It is called before
// the request builders set their managed headers (Content-Type, Authorization,
// user identity, and so on) so those always take precedence and a custom header
// can never accidentally clobber authentication.
func applyCustomHeaders(req *http.Request, headers http.Header) {
	for name, values := range headers {
		for _, value := range values {
			req.Header.Add(name, value)
		}
	}
}
