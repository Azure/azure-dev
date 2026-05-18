// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"log"
	"os"

	"azureaiskills/internal/exterrors"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

// endpointSource identifies where the resolved Foundry project endpoint came
// from. Used for telemetry and debug logging only — never echoed to the user.
type endpointSource string

const (
	sourceFlag         endpointSource = "flag"
	sourceAzdEnv       endpointSource = "azdEnv"
	sourceGlobalConfig endpointSource = "globalConfig"
	sourceFoundryEnv   endpointSource = "foundryEnv"
)

const (
	// skillsContextEndpointKey is the global-config path this extension owns.
	skillsContextEndpointKey = "extensions.ai-skills.project.context.endpoint"
	// agentsContextEndpointKey is the global-config path owned by
	// `azure.ai.agents`. We read it as a fallback so users who configured the
	// endpoint via the agents extension do not have to re-run `set`.
	agentsContextEndpointKey = "extensions.ai-agents.project.context.endpoint"
	// azdEnvKey is the active azd environment value that supplies the
	// Foundry project endpoint.
	azdEnvKey = "AZURE_AI_PROJECT_ENDPOINT"
	// foundryEnvKey is the host environment variable that supplies the
	// Foundry project endpoint as a last-resort fallback.
	foundryEnvKey = "FOUNDRY_PROJECT_ENDPOINT"
)

// resolveProjectEndpoint implements the 5-level cascade from the design spec.
//
//  1. flagEndpoint (from -p / --project-endpoint).
//  2. Active azd env value AZURE_AI_PROJECT_ENDPOINT.
//  3. Global config extensions.ai-skills.project.context.endpoint, falling
//     back to extensions.ai-agents.project.context.endpoint.
//  4. Host env var FOUNDRY_PROJECT_ENDPOINT.
//  5. Structured error.
//
// The endpoint string is returned verbatim; URL validation is left to the
// caller (the existing agents extension validates against a Foundry-host
// suffix list, but the skills surface accepts any reachable HTTPS endpoint
// against the data plane, so we defer that check to the actual HTTP call).
func resolveProjectEndpoint(ctx context.Context, flagEndpoint string) (string, endpointSource, error) {
	if flagEndpoint != "" {
		return flagEndpoint, sourceFlag, nil
	}

	// Levels 2 & 3 require the azd client. If azd is not running this
	// extension as a child process (unlikely in practice), skip both and fall
	// through to the host env var.
	if azdClient, err := azdext.NewAzdClient(); err == nil {
		defer azdClient.Close()

		// 2. Active azd env value.
		if envResp, envErr := azdClient.Environment().GetCurrent(ctx, &azdext.EmptyRequest{}); envErr == nil {
			if valResp, valErr := azdClient.Environment().GetValue(ctx, &azdext.GetEnvRequest{
				EnvName: envResp.Environment.Name,
				Key:     azdEnvKey,
			}); valErr == nil && valResp.Value != "" {
				return valResp.Value, sourceAzdEnv, nil
			}
		}

		// 3. Global config: skills key first, then agents fallback.
		if ch, chErr := azdext.NewConfigHelper(azdClient); chErr == nil {
			for _, key := range []string{skillsContextEndpointKey, agentsContextEndpointKey} {
				var endpoint string
				if found, getErr := ch.GetUserJSON(ctx, key, &endpoint); getErr == nil && found && endpoint != "" {
					if key == agentsContextEndpointKey {
						log.Printf("resolveProjectEndpoint: using fallback global config key %q", agentsContextEndpointKey)
					}
					return endpoint, sourceGlobalConfig, nil
				}
			}
		}
	}

	// 4. Host env var.
	if ep := os.Getenv(foundryEnvKey); ep != "" {
		return ep, sourceFoundryEnv, nil
	}

	// 5. Structured error.
	return "", "", exterrors.Dependency(
		exterrors.CodeMissingProjectEndpoint,
		"no Foundry project endpoint resolved",
		"pass --project-endpoint, set "+azdEnvKey+" in the active azd environment, "+
			"persist a workspace default with `azd ai agent project set <endpoint>`, "+
			"or export "+foundryEnvKey+" in your shell",
	)
}
