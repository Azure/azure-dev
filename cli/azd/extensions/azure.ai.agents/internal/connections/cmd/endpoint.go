// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"strings"

	"azureaiagent/internal/connections/exterrors"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

// TODO: Unify endpoint resolution with the project set/unset commands being added
// to avoid duplicating the resolution cascade logic.

// resolveProjectEndpoint implements the 5-level resolution cascade from the spec.
//
//  1. -p / --project-endpoint flag (passed as flagEndpoint)
//  2. Active azd env → AZURE_AI_PROJECT_ENDPOINT
//  3. Global config → extensions.ai-agents.context.endpoint
//  4. FOUNDRY_PROJECT_ENDPOINT environment variable
//  5. Structured error
func resolveProjectEndpoint(ctx context.Context, flagEndpoint string) (string, error) {
	// 1. Flag
	if flagEndpoint != "" {
		return flagEndpoint, nil
	}

	// 2 & 3. Try azd host (env value + global config) — best-effort
	azdClient, err := azdext.NewAzdClient()
	if err == nil {
		defer azdClient.Close()

		// 2. Active azd env → AZURE_AI_PROJECT_ENDPOINT
		if envResp, err := azdClient.Environment().GetCurrent(ctx, &azdext.EmptyRequest{}); err == nil {
			if valResp, err := azdClient.Environment().GetValue(ctx, &azdext.GetEnvRequest{
				EnvName: envResp.Environment.Name,
				Key:     "AZURE_AI_PROJECT_ENDPOINT",
			}); err == nil && valResp.Value != "" {
				return valResp.Value, nil
			}
		}

		// 3. Global config → extensions.ai-agents.context.endpoint
		ch, cfgErr := azdext.NewConfigHelper(azdClient)
		if cfgErr == nil {
			var endpoint string
			if found, err := ch.GetUserJSON(
				ctx, "extensions.ai-agents.context.endpoint", &endpoint,
			); err == nil && found && endpoint != "" {
				return endpoint, nil
			}
		}
	}

	// 4. FOUNDRY_PROJECT_ENDPOINT environment variable
	// TODO: Document FOUNDRY_PROJECT_ENDPOINT in cli/azd/docs/environment-variables.md
	if ep := os.Getenv("FOUNDRY_PROJECT_ENDPOINT"); ep != "" {
		return ep, nil
	}

	// 5. Structured error
	return "", exterrors.Dependency(
		exterrors.CodeMissingProjectEndpoint,
		"No Foundry project endpoint resolved.",
		"Pass '--project-endpoint', set FOUNDRY_PROJECT_ENDPOINT env var, or run 'azd ai agent init' in an azd project.",
	)
}

// parseEndpointComponents extracts account and project names from the endpoint URL.
// Expected format: https://{account}.services.ai.azure.com/api/projects/{project}
func parseEndpointComponents(endpoint string) (account, project string, err error) {
	u, err := url.Parse(endpoint)
	if err != nil {
		return "", "", fmt.Errorf("invalid endpoint URL: %w", err)
	}

	account, _, _ = strings.Cut(u.Hostname(), ".")

	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	for i, p := range parts {
		if p == "projects" && i+1 < len(parts) {
			project = parts[i+1]
			break
		}
	}

	if account == "" || project == "" {
		return "", "", fmt.Errorf("could not parse account/project from endpoint %q", endpoint)
	}

	return account, project, nil
}

// armContext holds the ARM components needed for SDK calls.
type armContext struct {
	SubscriptionID string
	ResourceGroup  string
	AccountName    string
	ProjectName    string
}

// discoverARMContext makes a data-plane list call to discover subscription and
// resource group from the ARM resource IDs embedded in connection responses.
func discoverARMContext(
	ctx context.Context,
	dpClient *dataClient,
) (*armContext, error) {
	conns, err := dpClient.ListConnections(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list connections for ARM discovery: %w", err)
	}

	if len(conns) == 0 {
		return nil, fmt.Errorf(
			"no connections found in project; cannot discover ARM context. " +
				"Create a connection via the Foundry portal first, or pass the project endpoint that already has connections",
		)
	}

	return parseARMResourceID(conns[0].ID)
}

// parseARMResourceID extracts ARM components from a full resource ID string.
func parseARMResourceID(resourceID string) (*armContext, error) {
	parts := strings.Split(resourceID, "/")
	result := &armContext{}

	for i, part := range parts {
		switch {
		case part == "subscriptions" && i+1 < len(parts):
			result.SubscriptionID = parts[i+1]
		case part == "resourceGroups" && i+1 < len(parts):
			result.ResourceGroup = parts[i+1]
		case part == "accounts" && i+1 < len(parts):
			result.AccountName = parts[i+1]
		case part == "projects" && i+1 < len(parts):
			result.ProjectName = parts[i+1]
		}
	}

	if result.SubscriptionID == "" || result.ResourceGroup == "" ||
		result.AccountName == "" || result.ProjectName == "" {
		return nil, fmt.Errorf("could not extract ARM context from resource ID: %s", resourceID)
	}

	return result, nil
}
