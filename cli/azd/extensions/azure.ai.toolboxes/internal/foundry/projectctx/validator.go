// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package projectctx

import (
	"fmt"
	"net/url"
	"strings"

	"azure.ai.toolboxes/internal/exterrors"
)

// foundryHostSuffixes is the authoritative list of accepted Foundry host suffixes.
var foundryHostSuffixes = []string{
	".services.ai.azure.com",
}

// projectEndpointPathPrefix is the expected path prefix for Foundry project endpoints.
const projectEndpointPathPrefix = "/api/projects/"

// isFoundryHost reports whether the hostname ends with a recognized Foundry suffix.
func isFoundryHost(hostname string) bool {
	h := strings.ToLower(hostname)
	for _, suffix := range foundryHostSuffixes {
		if strings.HasSuffix(h, suffix) {
			return true
		}
	}
	return false
}

// Validate validates and normalizes a Foundry project endpoint URL.
//
// The URL must be an absolute https:// URL whose host ends with a recognized
// Foundry suffix. Whitespace is trimmed, trailing slashes are stripped, and
// the result is returned in normalized form.
//
// The second return value is true when the path does not look like
// /api/projects/<proj> — callers may use this as a non-fatal warning.
func Validate(raw string) (normalized string, pathWarning bool, err error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", false, exterrors.Validation(
			exterrors.CodeInvalidParameter,
			"project endpoint must not be empty",
			"provide a Foundry project endpoint URL "+
				"(e.g. https://<account>.services.ai.azure.com/api/projects/<project>)",
		)
	}

	u, parseErr := url.Parse(raw)
	if parseErr != nil {
		return "", false, exterrors.Validation(
			exterrors.CodeInvalidParameter,
			fmt.Sprintf("invalid project endpoint URL: %v", parseErr),
			"provide a valid https:// Foundry project endpoint URL",
		)
	}

	if !strings.EqualFold(u.Scheme, "https") {
		return "", false, exterrors.Validation(
			exterrors.CodeInvalidParameter,
			"project endpoint must use https",
			"provide an https:// URL",
		)
	}

	host := u.Hostname()
	if host == "" || !isFoundryHost(host) {
		return "", false, exterrors.Validation(
			exterrors.CodeInvalidParameter,
			fmt.Sprintf(
				"project endpoint host %q is not a recognized Foundry host (*%s)",
				host, foundryHostSuffixes[0],
			),
			"the host must end with "+foundryHostSuffixes[0],
		)
	}

	if u.Port() != "" {
		return "", false, exterrors.Validation(
			exterrors.CodeInvalidParameter,
			fmt.Sprintf("project endpoint host %q must not include a port", u.Host),
			"remove the explicit port from the URL",
		)
	}

	// Normalize: lowercase host, strip trailing slash.
	path := strings.TrimRight(u.EscapedPath(), "/")
	normalized = fmt.Sprintf("https://%s%s", strings.ToLower(host), path)

	// Warn when the path does not look like /api/projects/<proj>.
	if !strings.HasPrefix(path, projectEndpointPathPrefix) ||
		strings.TrimPrefix(path, projectEndpointPathPrefix) == "" {
		pathWarning = true
	}

	return normalized, pathWarning, nil
}

// NoEndpointError returns the structured dependency error used when no project
// endpoint could be resolved from any source.
func NoEndpointError() error {
	return exterrors.Dependency(
		exterrors.CodeMissingProjectEndpoint,
		"no Foundry project endpoint resolved",
		"persist a workspace default with `azd ai agent project set <endpoint>`, "+
			"or set AZURE_AI_PROJECT_ENDPOINT in the active azd environment, "+
			"or export FOUNDRY_PROJECT_ENDPOINT in your shell",
	)
}
