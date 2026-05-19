// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"strings"

	"azure.ai.routines/internal/exterrors"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

// EndpointSource identifies where the resolved project endpoint came from.
type EndpointSource string

const (
	// SourceFlag means the endpoint came from the -p / --project-endpoint flag.
	SourceFlag EndpointSource = "flag"
	// SourceAzdEnv means the endpoint came from the active azd environment's AZURE_AI_PROJECT_ENDPOINT.
	SourceAzdEnv EndpointSource = "azdEnv"
	// SourceGlobalConfig means the endpoint came from ~/.azd/config.json.
	SourceGlobalConfig EndpointSource = "globalConfig"
	// SourceFoundryEnv means the endpoint came from the FOUNDRY_PROJECT_ENDPOINT env var.
	SourceFoundryEnv EndpointSource = "foundryEnv"
)

// foundryHostSuffixes lists the accepted Foundry host suffixes.
var foundryHostSuffixes = []string{
	".services.ai.azure.com",
}

// projectContextConfigPath is the global config path for the persisted project context.
// Matches the azure.ai.agents extension for cross-extension compatibility.
const projectContextConfigPath = "extensions.ai-agents.project.context"

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

// validateProjectEndpoint validates and normalizes a Foundry project endpoint URL.
func validateProjectEndpoint(raw string) (normalized string, err error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", exterrors.Validation(
			exterrors.CodeInvalidParameter,
			"project endpoint must not be empty",
			"provide a Foundry project endpoint URL "+
				"(e.g. https://<account>.services.ai.azure.com/api/projects/<project>)",
		)
	}

	u, parseErr := url.Parse(raw)
	if parseErr != nil {
		return "", exterrors.Validation(
			exterrors.CodeInvalidParameter,
			fmt.Sprintf("invalid project endpoint URL: %v", parseErr),
			"provide a valid https:// Foundry project endpoint URL",
		)
	}

	if !strings.EqualFold(u.Scheme, "https") {
		return "", exterrors.Validation(
			exterrors.CodeInvalidParameter,
			"project endpoint must use https",
			"provide an https:// URL",
		)
	}

	host := u.Hostname()
	if host == "" || !isFoundryHost(host) {
		return "", exterrors.Validation(
			exterrors.CodeInvalidParameter,
			fmt.Sprintf("project endpoint host %q is not a recognized Foundry host (*%s)",
				host, foundryHostSuffixes[0]),
			"the host must end with "+foundryHostSuffixes[0],
		)
	}

	// Normalize: lowercase host, strip trailing slash.
	path := strings.TrimRight(u.EscapedPath(), "/")
	normalized = fmt.Sprintf("https://%s%s", strings.ToLower(host), path)
	return normalized, nil
}

// resolvedEndpoint holds the result of resolveProjectEndpoint.
type resolvedEndpoint struct {
	Endpoint string
	Source   EndpointSource
}

// azdProjectSources holds the values read from azd-managed sources (levels 2 and 3).
type azdProjectSources struct {
	// EnvValue is the AZURE_AI_PROJECT_ENDPOINT from the active azd env, or "".
	EnvValue string
	// EnvName is the active azd env name. Only meaningful when EnvValue != "".
	EnvName string
	// CfgEndpoint is the endpoint persisted in global config, or "".
	CfgEndpoint string
	// CfgFound is true when the global config path was found and had a non-empty endpoint.
	CfgFound bool
}

// readAzdProjectSourcesFunc is a package-level seam so tests can stub the
// daemon-backed lookup without spinning up a real azd gRPC server.
var readAzdProjectSourcesFunc = readAzdProjectSources

// readAzdProjectSources dials the azd daemon (if reachable) and reads the
// active env's AZURE_AI_PROJECT_ENDPOINT and the global-config project
// endpoint in a single client lifetime. Errors talking to the daemon are
// silently ignored (treated as "no daemon"); the caller falls through to
// the FOUNDRY_PROJECT_ENDPOINT host env var.
func readAzdProjectSources(ctx context.Context) (azdProjectSources, error) {
	var out azdProjectSources

	azdClient, err := azdext.NewAzdClient()
	if err != nil {
		return out, nil
	}
	defer azdClient.Close()

	// Level 2: active azd env → AZURE_AI_PROJECT_ENDPOINT.
	if envResp, err := azdClient.Environment().GetCurrent(ctx, &azdext.EmptyRequest{}); err == nil {
		if valResp, err := azdClient.Environment().GetValue(ctx, &azdext.GetEnvRequest{
			EnvName: envResp.Environment.Name,
			Key:     "AZURE_AI_PROJECT_ENDPOINT",
		}); err == nil && valResp.Value != "" {
			out.EnvValue = valResp.Value
			out.EnvName = envResp.Environment.Name
		}
	}

	// Level 3: global config → extensions.ai-agents.project.context.endpoint.
	ch, cfgErr := azdext.NewConfigHelper(azdClient)
	if cfgErr == nil {
		var state struct {
			Endpoint string `json:"endpoint"`
		}
		if found, err := ch.GetUserJSON(ctx, projectContextConfigPath, &state); err == nil && found && state.Endpoint != "" {
			out.CfgEndpoint = state.Endpoint
			out.CfgFound = true
		}
	}

	return out, nil
}

// resolveProjectEndpoint implements the 5-level cascade:
//
//  1. -p / --project-endpoint flag
//  2. Active azd env → AZURE_AI_PROJECT_ENDPOINT
//  3. Global config → extensions.ai-agents.project.context.endpoint
//  4. FOUNDRY_PROJECT_ENDPOINT environment variable
//  5. Structured dependency error
func resolveProjectEndpoint(ctx context.Context, flagValue string) (*resolvedEndpoint, error) {
	// Level 1: explicit flag.
	if flagValue != "" {
		normalized, err := validateProjectEndpoint(flagValue)
		if err != nil {
			return nil, err
		}
		return &resolvedEndpoint{Endpoint: normalized, Source: SourceFlag}, nil
	}

	// Levels 2 & 3: azd daemon sources (replaceable seam for testing).
	sources, err := readAzdProjectSourcesFunc(ctx)
	if err != nil {
		return nil, err
	}

	if sources.EnvValue != "" {
		normalized, err := validateProjectEndpoint(sources.EnvValue)
		if err != nil {
			return nil, err
		}
		return &resolvedEndpoint{Endpoint: normalized, Source: SourceAzdEnv}, nil
	}

	if sources.CfgFound && sources.CfgEndpoint != "" {
		normalized, err := validateProjectEndpoint(sources.CfgEndpoint)
		if err != nil {
			return nil, err
		}
		return &resolvedEndpoint{Endpoint: normalized, Source: SourceGlobalConfig}, nil
	}

	// Level 4: FOUNDRY_PROJECT_ENDPOINT env var.
	if ep := os.Getenv("FOUNDRY_PROJECT_ENDPOINT"); ep != "" {
		normalized, err := validateProjectEndpoint(ep)
		if err != nil {
			return nil, err
		}
		return &resolvedEndpoint{Endpoint: normalized, Source: SourceFoundryEnv}, nil
	}

	// Level 5: structured error.
	return nil, exterrors.Dependency(
		exterrors.CodeMissingProjectEndpoint,
		"no Foundry project endpoint resolved",
		"pass -p / --project-endpoint, run 'azd ai agent project set <endpoint>', "+
			"set AZURE_AI_PROJECT_ENDPOINT in the active azd environment, "+
			"or export FOUNDRY_PROJECT_ENDPOINT in your shell",
	)
}
