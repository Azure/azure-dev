// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"fmt"
	"net/url"
	"os"
	"strings"

	"azureaiagent/internal/exterrors"
)

// EndpointSource identifies where the resolved project endpoint came from.
type EndpointSource string

const (
	// SourceFlag means the endpoint came from the -p / --project-endpoint flag.
	SourceFlag EndpointSource = "flag"
	// SourceAzdEnv means the endpoint came from the active azd environment's
	// FOUNDRY_PROJECT_ENDPOINT value.
	SourceAzdEnv EndpointSource = "azdEnv"
	// SourceGlobalConfig means the endpoint came from ~/.azd/config.json
	// (extensions.ai-agents.context.endpoint).
	SourceGlobalConfig EndpointSource = "globalConfig"
	// SourceFoundryEnv means the endpoint came from the FOUNDRY_PROJECT_ENDPOINT
	// host environment variable.
	SourceFoundryEnv EndpointSource = "foundryEnv"
)

// foundryHostSuffixes is the authoritative list of accepted Foundry host suffixes.
// isFoundryHost checks this list; both validateProjectEndpoint and parseAgentEndpoint
// (agent_endpoint.go) call isFoundryHost, so all validators stay in sync automatically.
var foundryHostSuffixes = []string{
	".services.ai.azure.com",
}

// projectEndpointPathPrefix is the expected path prefix for Foundry project endpoints.
const projectEndpointPathPrefix = "/api/projects/"

// FoundryEndpointOverrideEnvVar is the environment variable that, when set,
// causes the project-endpoint validator to skip the Foundry host suffix check
// and accept http:// (in addition to https://). It exists so developers can
// point the extension at a locally running Foundry backend (e.g. the vienna
// "managed-harness" service on http://localhost:5000) for end-to-end testing.
//
// IMPORTANT: This bypass is for development/testing only. Never document it
// in user-facing help; it is intentionally undocumented and may change or be
// removed at any time.
const FoundryEndpointOverrideEnvVar = "AZD_FOUNDRY_ENDPOINT_OVERRIDE"

// foundryEndpointValidationBypassed reports whether the
// AZD_FOUNDRY_ENDPOINT_OVERRIDE environment variable is set to any non-empty
// value. When true, validateProjectEndpoint relaxes its scheme and host checks.
func foundryEndpointValidationBypassed() bool {
	return strings.TrimSpace(os.Getenv(FoundryEndpointOverrideEnvVar)) != ""
}

// isFoundryHost reports whether the hostname ends with one of the recognized
// Foundry host suffixes.
func isFoundryHost(hostname string) bool {
	h := strings.ToLower(hostname)
	for _, suffix := range foundryHostSuffixes {
		if strings.HasSuffix(h, suffix) {
			return true
		}
	}
	return false
}

// validateProjectEndpoint validates and normalizes a Foundry project endpoint URL.
//
// The URL must be an absolute https:// URL whose host ends with a recognized
// Foundry suffix (see [foundryHostSuffixes]). Whitespace is trimmed, trailing
// slashes are stripped, and the result is returned in normalized form.
//
// The second return value is true when the path does not look like
// /api/projects/<proj> — callers may use this as a non-fatal warning.
func validateProjectEndpoint(raw string) (normalized string, pathWarning bool, err error) {
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

	// When the override env var is set we accept http:// in addition to
	// https:// so developers can target a locally running Foundry backend.
	bypass := foundryEndpointValidationBypassed()

	if !strings.EqualFold(u.Scheme, "https") &&
		!(bypass && strings.EqualFold(u.Scheme, "http")) {
		return "", false, exterrors.Validation(
			exterrors.CodeInvalidParameter,
			"project endpoint must use https",
			"provide an https:// URL",
		)
	}

	host := u.Hostname()
	if host == "" {
		return "", false, exterrors.Validation(
			exterrors.CodeInvalidParameter,
			"project endpoint host must not be empty",
			"provide a URL with a hostname",
		)
	}
	if !bypass && !isFoundryHost(host) {
		return "", false, exterrors.Validation(
			exterrors.CodeInvalidParameter,
			fmt.Sprintf(
				"project endpoint host %q is not a recognized Foundry host (*%s)",
				host, foundryHostSuffixes[0],
			),
			"the host must end with "+foundryHostSuffixes[0],
		)
	}

	if !bypass && u.Port() != "" {
		return "", false, exterrors.Validation(
			exterrors.CodeInvalidParameter,
			fmt.Sprintf("project endpoint host %q must not include a port", u.Host),
			"remove the explicit port from the URL",
		)
	}

	// Normalize: lowercase host, strip trailing slash. Preserve the scheme as
	// originally supplied so the override path can keep http:// for localhost.
	scheme := strings.ToLower(u.Scheme)
	path := strings.TrimRight(u.EscapedPath(), "/")
	hostPart := strings.ToLower(host)
	if u.Port() != "" {
		hostPart = fmt.Sprintf("%s:%s", hostPart, u.Port())
	}
	normalized = fmt.Sprintf("%s://%s%s", scheme, hostPart, path)

	// Warn when the path does not look like /api/projects/<proj>. The override
	// path skips this warning entirely — local backends often expose a simpler
	// path layout.
	if !bypass && (!strings.HasPrefix(path, projectEndpointPathPrefix) ||
		strings.TrimPrefix(path, projectEndpointPathPrefix) == "") {
		pathWarning = true
	}

	return normalized, pathWarning, nil
}

// noProjectEndpointError returns the structured dependency error used when no
// project endpoint could be resolved from any source. The suggestion list is
// generic (no --project-endpoint bullet); callers that expose that flag prepend
// their own line.
func noProjectEndpointError() error {
	return exterrors.Dependency(
		exterrors.CodeMissingProjectEndpoint,
		"no Foundry project endpoint resolved",
		"persist a workspace default with `azd ai agent project set <endpoint>`, "+
			"or set FOUNDRY_PROJECT_ENDPOINT in the active azd environment, "+
			"or export FOUNDRY_PROJECT_ENDPOINT in your shell",
	)
}
