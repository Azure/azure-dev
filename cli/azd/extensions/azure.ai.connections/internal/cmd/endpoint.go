// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"
	"log"
	"net/url"
	"strings"
)

// parseEndpointComponents extracts account and project names from the endpoint URL.
// Expected format: https://{account}.services.ai.azure.com/api/projects/{project}
//
// projectctx.Validate already ensures the URL is an https:// Foundry host without
// a port; this helper only handles the connection-specific account/project split.
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

// resolveARMContext resolves the ARM subscription + resource group for the
// project.
//
// It prefers projectID - the Foundry project's ARM resource ID, read from the
// azd environment's AZURE_AI_PROJECT_ID - when it is present and matches the
// resolved endpoint's account/project. That path works even when the project
// has no connections yet, which is required to create the first connection via
// `azd up` or `azd ai connection create`. When projectID is missing, malformed,
// or points at a different project, it falls back to discovering the context
// from an existing connection's ARM resource ID.
func resolveARMContext(
	ctx context.Context,
	projectID, account, project string,
	dpClient *dataClient,
) (*armContext, error) {
	if projectID != "" {
		armCtx, err := parseARMResourceID(projectID)
		switch {
		case err != nil:
			log.Printf("connections: could not parse AZURE_AI_PROJECT_ID %q; "+
				"falling back to connection discovery: %v", projectID, err)
		case !strings.EqualFold(armCtx.AccountName, account) ||
			!strings.EqualFold(armCtx.ProjectName, project):
			log.Printf("connections: AZURE_AI_PROJECT_ID %q does not match resolved "+
				"endpoint %s/%s; falling back to connection discovery", projectID, account, project)
		default:
			return armCtx, nil
		}
	}

	return discoverARMContext(ctx, dpClient)
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
				"Create a connection via the Foundry portal first, " +
				"or pass the project endpoint that already has connections",
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
