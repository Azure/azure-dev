// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"fmt"
	"net/url"
	"strings"

	"azureaiagent/internal/exterrors"
)

const foundryHostSuffix = ".services.ai.azure.com"
const projectEndpointPathPrefix = "/api/projects/"

// IsFoundryHost reports whether hostname belongs to the supported Foundry service.
func IsFoundryHost(hostname string) bool {
	return strings.HasSuffix(strings.ToLower(hostname), foundryHostSuffix)
}

// ValidateProjectEndpoint validates and normalizes a Foundry project endpoint,
// dropping URL credentials, queries and fragments. The second result indicates
// a path that does not have the usual /api/projects/<project> shape.
func ValidateProjectEndpoint(raw string) (string, bool, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", false, exterrors.Validation(exterrors.CodeInvalidParameter,
			"project endpoint must not be empty",
			"provide a Foundry project endpoint URL "+
				"(e.g. https://<account>.services.ai.azure.com/api/projects/<project>)")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", false, exterrors.Validation(exterrors.CodeInvalidParameter,
			"invalid project endpoint URL", "provide a valid https:// Foundry project endpoint URL")
	}
	if !strings.EqualFold(u.Scheme, "https") {
		return "", false, exterrors.Validation(exterrors.CodeInvalidParameter,
			"project endpoint must use https", "provide an https:// URL")
	}
	host := u.Hostname()
	if host == "" {
		return "", false, exterrors.Validation(exterrors.CodeInvalidParameter,
			"project endpoint host must not be empty", "provide a URL with a hostname")
	}
	if !IsFoundryHost(host) {
		return "", false, exterrors.Validation(exterrors.CodeInvalidParameter,
			fmt.Sprintf("project endpoint host %q is not a recognized Foundry host (*%s)", host, foundryHostSuffix),
			"the host must end with "+foundryHostSuffix)
	}
	if u.Port() != "" {
		return "", false, exterrors.Validation(exterrors.CodeInvalidParameter,
			fmt.Sprintf("project endpoint host %q must not include a port", u.Host),
			"remove the explicit port from the URL")
	}
	path := strings.TrimRight(u.EscapedPath(), "/")
	normalized := fmt.Sprintf("https://%s%s", strings.ToLower(host), path)
	warning := !strings.HasPrefix(path, projectEndpointPathPrefix) ||
		strings.TrimPrefix(path, projectEndpointPathPrefix) == ""
	return normalized, warning, nil
}
