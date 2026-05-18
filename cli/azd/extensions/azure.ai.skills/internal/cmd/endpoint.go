// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"
	"log"
	"net/url"
	"os"
	"strings"

	"azureaiskills/internal/exterrors"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

type endpointSource string

const (
	sourceFlag         endpointSource = "flag"
	sourceAzdEnv       endpointSource = "azdEnv"
	sourceGlobalConfig endpointSource = "globalConfig"
	sourceFoundryEnv   endpointSource = "foundryEnv"
)

const (
	skillsContextEndpointKey = "extensions.ai-skills.project.context.endpoint"
	// Read-only fallback to the ai-agents key so users who configured the
	// endpoint via that extension don't have to re-run `set`.
	agentsContextEndpointKey = "extensions.ai-agents.project.context.endpoint"
	azdEnvKey                = "AZURE_AI_PROJECT_ENDPOINT"
	foundryEnvKey            = "FOUNDRY_PROJECT_ENDPOINT"
)

// resolveProjectEndpoint implements the 5-level cascade from the design spec:
// flag, azd env, global config (skills then agents), host env, error.
func resolveProjectEndpoint(ctx context.Context, flagEndpoint string) (string, endpointSource, error) {
	if flagEndpoint != "" {
		if err := validateEndpoint(flagEndpoint); err != nil {
			return "", "", err
		}
		return flagEndpoint, sourceFlag, nil
	}

	if azdClient, err := azdext.NewAzdClient(); err == nil {
		defer azdClient.Close()

		if envResp, envErr := azdClient.Environment().GetCurrent(ctx, &azdext.EmptyRequest{}); envErr == nil {
			if valResp, valErr := azdClient.Environment().GetValue(ctx, &azdext.GetEnvRequest{
				EnvName: envResp.Environment.Name,
				Key:     azdEnvKey,
			}); valErr == nil && valResp.Value != "" {
				if err := validateEndpoint(valResp.Value); err != nil {
					return "", "", err
				}
				return valResp.Value, sourceAzdEnv, nil
			}
		}

		if ch, chErr := azdext.NewConfigHelper(azdClient); chErr == nil {
			for _, key := range []string{skillsContextEndpointKey, agentsContextEndpointKey} {
				var endpoint string
				if found, getErr := ch.GetUserJSON(ctx, key, &endpoint); getErr == nil && found && endpoint != "" {
					if key == agentsContextEndpointKey {
						log.Printf("resolveProjectEndpoint: using fallback global config key %q", agentsContextEndpointKey)
					}
					if err := validateEndpoint(endpoint); err != nil {
						return "", "", err
					}
					return endpoint, sourceGlobalConfig, nil
				}
			}
		}
	}

	if ep := os.Getenv(foundryEnvKey); ep != "" {
		if err := validateEndpoint(ep); err != nil {
			return "", "", err
		}
		return ep, sourceFoundryEnv, nil
	}

	return "", "", exterrors.Dependency(
		exterrors.CodeMissingProjectEndpoint,
		"no Foundry project endpoint resolved",
		"pass --project-endpoint, set "+azdEnvKey+" in the active azd environment, "+
			"persist a workspace default with `azd ai agent project set <endpoint>`, "+
			"or export "+foundryEnvKey+" in your shell",
	)
}

func validateEndpoint(endpoint string) error {
	u, err := url.Parse(endpoint)
	if err != nil {
		return fmt.Errorf("invalid project endpoint %q: %w", endpoint, err)
	}
	if !strings.EqualFold(u.Scheme, "https") {
		return fmt.Errorf("invalid project endpoint %q: must use https scheme", endpoint)
	}
	if u.Host == "" {
		return fmt.Errorf("invalid project endpoint %q: missing host", endpoint)
	}
	return nil
}
