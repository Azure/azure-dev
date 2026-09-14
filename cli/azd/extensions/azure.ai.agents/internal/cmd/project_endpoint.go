// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"azureaiagent/internal/exterrors"
	"azureaiagent/internal/project"
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

// isFoundryHost reports whether the hostname ends with one of the recognized
// Foundry host suffixes.
func isFoundryHost(hostname string) bool {
	return project.IsFoundryHost(hostname)
}

// validateProjectEndpoint validates and normalizes a Foundry project endpoint URL.
//
// The URL must be an absolute https:// URL whose host ends with a recognized
// Foundry suffix. Whitespace is trimmed, trailing
// slashes are stripped, and the result is returned in normalized form.
//
// The second return value is true when the path does not look like
// /api/projects/<proj> — callers may use this as a non-fatal warning.
func validateProjectEndpoint(raw string) (normalized string, pathWarning bool, err error) {
	return project.ValidateProjectEndpoint(raw)
}

// noProjectEndpointError returns the structured dependency error used when no
// project endpoint could be resolved from any source. The suggestion list is
// generic (no --project-endpoint bullet); callers that expose that flag prepend
// their own line.
func noProjectEndpointError() error {
	return exterrors.Dependency(
		exterrors.CodeMissingProjectEndpoint,
		"no Foundry project endpoint resolved",
		"persist a workspace default with `azd ai project set <endpoint>`, "+
			"or set FOUNDRY_PROJECT_ENDPOINT in the active azd environment, "+
			"or export FOUNDRY_PROJECT_ENDPOINT in your shell",
	)
}
