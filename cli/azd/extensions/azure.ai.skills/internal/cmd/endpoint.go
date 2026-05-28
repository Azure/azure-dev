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
	// projectsContextPath is the global-config path written by the sibling
	// `azure.ai.projects` extension's `azd ai project set` command. The
	// value at this path is the projectContextState object {Endpoint, SetAt};
	// the skills extension reads it but never writes it.
	projectsContextPath = "extensions.ai-projects.context"
	// skillsContextEndpointKey is the legacy global-config path read for
	// backward compatibility. Some hand-edited configurations and earlier
	// builds of this extension referenced this string-valued path.
	skillsContextEndpointKey = "extensions.ai-skills.project.context.endpoint"
	// agentsContextEndpointKey is the legacy global-config path written by
	// the removed `azd ai agent project set` command. Read as a fallback so
	// users who configured the endpoint there before the
	// `azure.ai.projects` extension existed still resolve. The
	// `azure.ai.projects` extension auto-migrates that key into
	// projectsContextPath the first time any `azd ai project` command runs;
	// the fallback exists for the window in between.
	agentsContextEndpointKey = "extensions.ai-agents.project.context.endpoint"
	azdEnvKey                = "AZURE_AI_PROJECT_ENDPOINT"
	foundryEnvKey            = "FOUNDRY_PROJECT_ENDPOINT"
)

// azdProjectSources holds the values read from azd-managed sources
// (levels 2 and 3 of resolveProjectEndpoint).
type azdProjectSources struct {
	// EnvValue is AZURE_AI_PROJECT_ENDPOINT from the active azd env, or "".
	EnvValue string
	// CfgEndpoint is the endpoint persisted at projectsContextPath in global
	// config, or "". Source of truth for `azd ai project set`.
	CfgEndpoint string
	// CfgFound is true when projectsContextPath was found and had a non-empty
	// endpoint.
	CfgFound bool
	// LegacySkillsEndpoint is the endpoint at skillsContextEndpointKey, or "".
	// Read as a fallback only.
	LegacySkillsEndpoint string
	// LegacySkillsFound is true when skillsContextEndpointKey was found and
	// had a non-empty endpoint.
	LegacySkillsFound bool
	// LegacyAgentsEndpoint is the endpoint at agentsContextEndpointKey, or "".
	// Read as a fallback only.
	LegacyAgentsEndpoint string
	// LegacyAgentsFound is true when agentsContextEndpointKey was found and
	// had a non-empty endpoint.
	LegacyAgentsFound bool
}

// readAzdProjectSourcesFunc is a package-level seam so tests can stub the
// daemon-backed lookup without spinning up a real azd gRPC server.
var readAzdProjectSourcesFunc = readAzdProjectSources

// readAzdProjectSources dials the azd daemon (if reachable) and reads the
// active env's AZURE_AI_PROJECT_ENDPOINT and the three candidate global-config
// keys in a single client lifetime. Errors talking to the daemon are silently
// ignored (treated as "no daemon"); the caller falls through to the
// FOUNDRY_PROJECT_ENDPOINT host env var.
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
			Key:     azdEnvKey,
		}); err == nil && valResp.Value != "" {
			out.EnvValue = valResp.Value
		}
	}

	// Level 3: global config. Read the new path written by
	// `azd ai project set` plus both legacy paths in one daemon round-trip
	// so resolveProjectEndpoint can pick the highest-priority hit.
	ch, cfgErr := azdext.NewConfigHelper(azdClient)
	if cfgErr == nil {
		var state struct {
			Endpoint string `json:"endpoint"`
		}
		if found, err := ch.GetUserJSON(ctx, projectsContextPath, &state); err == nil && found && state.Endpoint != "" {
			out.CfgEndpoint = state.Endpoint
			out.CfgFound = true
		}

		var legacySkills string
		if found, err := ch.GetUserJSON(ctx, skillsContextEndpointKey, &legacySkills); err == nil && found && legacySkills != "" {
			out.LegacySkillsEndpoint = legacySkills
			out.LegacySkillsFound = true
		}

		var legacyAgents string
		if found, err := ch.GetUserJSON(ctx, agentsContextEndpointKey, &legacyAgents); err == nil && found && legacyAgents != "" {
			out.LegacyAgentsEndpoint = legacyAgents
			out.LegacyAgentsFound = true
		}
	}

	return out, nil
}

// resolveProjectEndpoint implements the 5-level cascade from the design spec:
// flag, azd env, global config (ai-projects then legacy ai-skills then legacy
// ai-agents), host env, error.
func resolveProjectEndpoint(ctx context.Context, flagEndpoint string) (string, endpointSource, error) {
	if flagEndpoint != "" {
		if err := validateEndpoint(flagEndpoint); err != nil {
			return "", "", err
		}
		return flagEndpoint, sourceFlag, nil
	}

	sources, err := readAzdProjectSourcesFunc(ctx)
	if err != nil {
		return "", "", err
	}

	if sources.EnvValue != "" {
		if err := validateEndpoint(sources.EnvValue); err != nil {
			return "", "", err
		}
		return sources.EnvValue, sourceAzdEnv, nil
	}

	if sources.CfgFound && sources.CfgEndpoint != "" {
		if err := validateEndpoint(sources.CfgEndpoint); err != nil {
			return "", "", err
		}
		return sources.CfgEndpoint, sourceGlobalConfig, nil
	}

	if sources.LegacySkillsFound && sources.LegacySkillsEndpoint != "" {
		if err := validateEndpoint(sources.LegacySkillsEndpoint); err != nil {
			return "", "", err
		}
		log.Printf("resolveProjectEndpoint: using fallback global config key %q; "+
			"run `azd ai project set <endpoint>` to migrate", skillsContextEndpointKey)
		return sources.LegacySkillsEndpoint, sourceGlobalConfig, nil
	}

	if sources.LegacyAgentsFound && sources.LegacyAgentsEndpoint != "" {
		if err := validateEndpoint(sources.LegacyAgentsEndpoint); err != nil {
			return "", "", err
		}
		log.Printf("resolveProjectEndpoint: using fallback global config key %q; "+
			"run `azd ai project set <endpoint>` to migrate", agentsContextEndpointKey)
		return sources.LegacyAgentsEndpoint, sourceGlobalConfig, nil
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
			"persist a workspace default with `azd ai project set <endpoint>`, "+
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
