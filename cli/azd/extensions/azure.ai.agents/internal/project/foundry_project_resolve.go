// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"azureaiagent/internal/exterrors"
	"azureaiagent/internal/pkg/azure"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

// foundryProjectResourceType is the ARM resource type for a Foundry project.
const foundryProjectResourceType = "Microsoft.CognitiveServices/accounts/projects"

// foundryEndpointHostSuffix is the host suffix of a Foundry project endpoint.
const foundryEndpointHostSuffix = ".services.ai.azure.com"

// validateFoundryEndpoint enforces the transport rules every Foundry data-plane
// caller relies on: a non-empty https URL on a recognized Foundry host with no
// explicit port. Rejecting http, foreign hosts, and ports up front avoids
// sending credentials to an unexpected endpoint and catches a partially
// expanded ${VAR} that would otherwise leave an invalid host. It returns the
// parsed URL so callers can extract additional structure without re-parsing.
func validateFoundryEndpoint(endpoint string) (*url.URL, error) {
	trimmed := strings.TrimSpace(endpoint)
	if trimmed == "" {
		return nil, fmt.Errorf("endpoint is empty")
	}

	parsed, err := url.Parse(trimmed)
	if err != nil {
		return nil, fmt.Errorf("invalid endpoint %q: %w", endpoint, err)
	}
	if !strings.EqualFold(parsed.Scheme, "https") {
		return nil, fmt.Errorf("endpoint %q must use https", endpoint)
	}

	host := parsed.Hostname()
	if !strings.HasSuffix(strings.ToLower(host), foundryEndpointHostSuffix) {
		return nil, fmt.Errorf("endpoint host %q is not a Foundry project endpoint", host)
	}
	if parsed.Port() != "" {
		return nil, fmt.Errorf("endpoint %q must not include a port", endpoint)
	}

	return parsed, nil
}

// parseFoundryEndpoint extracts the account and project names from a Foundry
// project endpoint of the form
// https://<account>.services.ai.azure.com/api/projects/<project>.
func parseFoundryEndpoint(endpoint string) (account string, project string, err error) {
	parsed, err := validateFoundryEndpoint(endpoint)
	if err != nil {
		return "", "", err
	}

	host := parsed.Hostname()
	account = host[:len(host)-len(foundryEndpointHostSuffix)]
	if account == "" {
		return "", "", fmt.Errorf("endpoint %q is missing the account name", endpoint)
	}

	// Path is /api/projects/<project>; take the segment after "projects".
	segments := strings.Split(strings.Trim(parsed.Path, "/"), "/")
	for i := 0; i+1 < len(segments); i++ {
		if segments[i] == "projects" && segments[i+1] != "" {
			project = segments[i+1]
			break
		}
	}
	if project == "" {
		return "", "", fmt.Errorf("endpoint %q is missing the project name", endpoint)
	}

	return account, project, nil
}

// resolveFoundryProjectIDFromEndpoint resolves the ARM resource ID of a Foundry
// project from its data-plane endpoint by listing the Foundry projects in the
// subscription and matching the account and project names. It enables the
// `endpoint:` path (design spec #8590 §1.4): connect to an existing project
// without provisioning, so `azd deploy` can run without AZURE_AI_PROJECT_ID.
func resolveFoundryProjectIDFromEndpoint(
	ctx context.Context,
	credential azcore.TokenCredential,
	subscriptionID string,
	endpoint string,
) (string, error) {
	account, project, err := parseFoundryEndpoint(endpoint)
	if err != nil {
		return "", err
	}

	client, err := armresources.NewClient(subscriptionID, credential, azure.NewArmClientOptions())
	if err != nil {
		return "", fmt.Errorf("failed to create resources client: %w", err)
	}

	pager := client.NewListPager(&armresources.ClientListOptions{
		Filter: new(fmt.Sprintf("resourceType eq '%s'", foundryProjectResourceType)),
	})
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return "", fmt.Errorf("failed to list Foundry projects: %w", err)
		}
		for _, resource := range page.Value {
			if resource == nil || resource.ID == nil {
				continue
			}
			parsed, err := arm.ParseResourceID(*resource.ID)
			if err != nil || parsed.Parent == nil {
				continue
			}
			if strings.EqualFold(parsed.Parent.Name, account) && strings.EqualFold(parsed.Name, project) {
				return *resource.ID, nil
			}
		}
	}

	return "", fmt.Errorf(
		"no Foundry project matching endpoint %q was found in subscription %s", endpoint, subscriptionID)
}

// resolveProjectFromEndpoint connects to an existing Foundry project when the
// service sets `endpoint:` but no project was provisioned. It resolves the
// project's ARM resource ID from the endpoint and persists it as
// AZURE_AI_PROJECT_ID, so the shared deploy machinery (GetTargetResource,
// finalizeDeploy) works without `azd provision` (design spec #8590 §1.4).
func (p *FoundryServiceTargetProvider) resolveProjectFromEndpoint(ctx context.Context) error {
	// Already provisioned or previously resolved: nothing to do.
	existing, err := p.azdClient.Environment().GetValue(ctx, &azdext.GetEnvRequest{
		EnvName: p.agent.env.Name,
		Key:     "AZURE_AI_PROJECT_ID",
	})
	if err == nil && existing.Value != "" {
		return nil
	}

	endpoint := p.resolveEndpoint(ctx)
	if endpoint == "" {
		// No endpoint and no project ID: leave resolution to provision. The
		// deploy path surfaces an actionable error if neither is present.
		return nil
	}

	subscriptionID, err := p.subscriptionID(ctx)
	if err != nil {
		return err
	}

	projectID, err := resolveFoundryProjectIDFromEndpoint(ctx, p.agent.credential, subscriptionID, endpoint)
	if err != nil {
		return exterrors.Dependency(
			exterrors.CodeMissingAiProjectId,
			fmt.Sprintf("failed to resolve the Foundry project from endpoint %q: %s", endpoint, err),
			"verify the 'endpoint:' on the microsoft.foundry service points at an existing project "+
				"you can access, or run 'azd provision'",
		)
	}

	if _, err := p.azdClient.Environment().SetValue(ctx, &azdext.SetEnvRequest{
		EnvName: p.agent.env.Name,
		Key:     "AZURE_AI_PROJECT_ID",
		Value:   projectID,
	}); err != nil {
		return fmt.Errorf("failed to persist AZURE_AI_PROJECT_ID: %w", err)
	}

	return nil
}

// resolveEndpoint returns the Foundry project endpoint to connect to, preferring
// the service `endpoint:` field (with ${VAR} expansion, since core does not
// expand AdditionalProperties) and falling back to the FOUNDRY_PROJECT_ENDPOINT
// azd environment value.
func (p *FoundryServiceTargetProvider) resolveEndpoint(ctx context.Context) string {
	if p.config != nil && p.config.Endpoint != "" {
		azdEnv, _ := p.environmentValues(ctx)
		expanded, err := ExpandEnv(p.config.Endpoint, func(name string) string { return azdEnv[name] })
		if err == nil && strings.TrimSpace(expanded) != "" {
			return expanded
		}
		return p.config.Endpoint
	}

	resp, err := p.azdClient.Environment().GetValue(ctx, &azdext.GetEnvRequest{
		EnvName: p.agent.env.Name,
		Key:     "FOUNDRY_PROJECT_ENDPOINT",
	})
	if err != nil {
		return ""
	}
	return resp.Value
}

// subscriptionID reads AZURE_SUBSCRIPTION_ID from the active azd environment.
func (p *FoundryServiceTargetProvider) subscriptionID(ctx context.Context) (string, error) {
	resp, err := p.azdClient.Environment().GetValue(ctx, &azdext.GetEnvRequest{
		EnvName: p.agent.env.Name,
		Key:     "AZURE_SUBSCRIPTION_ID",
	})
	if err != nil || resp.Value == "" {
		return "", exterrors.Dependency(
			exterrors.CodeMissingAzureSubscription,
			"AZURE_SUBSCRIPTION_ID is required to resolve the Foundry project from 'endpoint:'",
			"run 'azd env set AZURE_SUBSCRIPTION_ID <id>' or 'azd provision'",
		)
	}
	return resp.Value, nil
}
