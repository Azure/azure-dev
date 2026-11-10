// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strings"

	"azure.ai.connections/internal/exterrors"
	"azure.ai.connections/internal/foundry/projectctx"
	"azure.ai.connections/internal/pkg/connections"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/cognitiveservices/armcognitiveservices/v2"
)

// parseEndpointProject reads the project name, not the ARM account.
// The endpoint's DNS subdomain may differ from the ARM account name.
func parseEndpointProject(endpoint string) (string, error) {
	u, err := url.Parse(endpoint)
	if err != nil {
		return "", errors.New("invalid project endpoint URL")
	}
	if u.Hostname() != "" {
		parts := strings.Split(strings.Trim(u.Path, "/"), "/")
		for i, part := range parts {
			if part == "projects" && i+1 < len(parts) && parts[i+1] != "" {
				return parts[i+1], nil
			}
		}
	}

	return "", errors.New("could not parse project from endpoint")
}

// armContext holds the ARM components needed for SDK calls.
type armContext struct {
	SubscriptionID string
	ResourceGroup  string
	AccountName    string
	ProjectName    string
}

type connectionLister interface {
	ListConnections(context.Context) ([]connections.Connection, error)
}

var errNoConnectionsForARMDiscovery = errors.New("no connections available for ARM discovery")

// resolveARMContext verifies the environment's project ARM ID using
// its advertised endpoint. Existing connections on the resolved
// data-plane endpoint supply the fallback ARM identity.
func resolveARMContext(
	ctx context.Context,
	projectID, endpoint string,
	dpClient connectionLister,
	credential azcore.TokenCredential,
	clientOptions *arm.ClientOptions,
) (*armContext, error) {
	project, err := parseEndpointProject(endpoint)
	if err != nil {
		return nil, err
	}

	projectIDConflict := false
	if projectID != "" {
		armCtx, err := parseARMResourceID(projectID)
		switch {
		case err != nil:
			log.Printf("connections: invalid AZURE_AI_PROJECT_ID; falling back to connection discovery")
		case !strings.EqualFold(armCtx.ProjectName, project):
			projectIDConflict = true
			log.Printf("connections: AZURE_AI_PROJECT_ID names a different project; " +
				"falling back to connection discovery")
		default:
			matches, err := matchesARMProjectEndpoint(ctx, armCtx, endpoint, credential, clientOptions)
			if err != nil {
				return nil, err
			}
			if matches {
				return armCtx, nil
			}
			projectIDConflict = true
			log.Printf("connections: AZURE_AI_PROJECT_ID does not advertise the resolved endpoint; " +
				"falling back to connection discovery")
		}
	}

	armCtx, err := discoverARMContext(ctx, dpClient)
	if errors.Is(err, errNoConnectionsForARMDiscovery) {
		if projectIDConflict {
			return nil, exterrors.Validation(
				exterrors.CodeConflictingArguments,
				"AZURE_AI_PROJECT_ID could not be matched to the resolved project endpoint, "+
					"and the project has no connections for ARM discovery.",
				"Use --project-endpoint and AZURE_AI_PROJECT_ID for the same Foundry project. "+
					"Persist the matching project ARM resource ID in the selected azd environment.",
			)
		}
		return nil, exterrors.Validation(
			exterrors.CodeInvalidParameter,
			"AZURE_AI_PROJECT_ID is required when the project has no connections; "+
				"the endpoint alone cannot provide the ARM subscription and resource group.",
			"Persist AZURE_AI_PROJECT_ID as the full ARM resource ID of the project matching the endpoint "+
				"in the selected azd environment (not only in the shell): "+
				"run 'azd env set AZURE_AI_PROJECT_ID \"<project-resource-id>\" --environment \"<environment>\"', "+
				"or 'azd ai project add --project-id \"<project-resource-id>\" --environment \"<environment>\"'. "+
				"Then retry the Connection command or deployment that produced this error.",
		)
	}
	if err != nil {
		return nil, err
	}
	if !strings.EqualFold(armCtx.ProjectName, project) {
		return nil, exterrors.Validation(
			exterrors.CodeConflictingArguments,
			"The discovered ARM project does not match the resolved project endpoint.",
			"Verify --project-endpoint and AZURE_AI_PROJECT_ID in the selected azd environment.",
		)
	}
	return armCtx, nil
}

func matchesARMProjectEndpoint(
	ctx context.Context,
	armCtx *armContext,
	endpoint string,
	credential azcore.TokenCredential,
	clientOptions *arm.ClientOptions,
) (bool, error) {
	const opGetProject = "get_project"
	client, err := armcognitiveservices.NewProjectsClient(armCtx.SubscriptionID, credential, clientOptions)
	if err != nil {
		return false, fmt.Errorf("failed to create ARM projects client: %w", err)
	}
	response, err := client.Get(ctx, armCtx.ResourceGroup, armCtx.AccountName, armCtx.ProjectName, nil)
	if err != nil {
		if responseErr, ok := errors.AsType[*azcore.ResponseError](err); ok &&
			responseErr.StatusCode == http.StatusNotFound {
			return false, nil
		}
		return false, exterrors.ServiceFromAzure(err, opGetProject)
	}
	if response.Properties == nil {
		return false, nil
	}
	advertised, _, err := projectctx.Validate(deref(response.Properties.Endpoints["AI Foundry API"]))
	return err == nil && strings.EqualFold(advertised, endpoint), nil
}

// discoverARMContext makes a data-plane list call to discover subscription and
// resource group from the ARM resource IDs embedded in connection responses.
func discoverARMContext(
	ctx context.Context,
	dpClient connectionLister,
) (*armContext, error) {
	conns, err := dpClient.ListConnections(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list connections for ARM discovery: %w", err)
	}

	if len(conns) == 0 {
		return nil, errNoConnectionsForARMDiscovery
	}

	return parseARMResourceID(conns[0].ID)
}

// parseARMResourceID extracts ARM components from a full resource ID string.
func parseARMResourceID(resourceID string) (*armContext, error) {
	id, err := arm.ParseResourceID(strings.TrimSpace(resourceID))
	if err != nil {
		return nil, errors.New("invalid Foundry project or connection ARM resource ID")
	}
	if strings.EqualFold(id.ResourceType.String(), "Microsoft.CognitiveServices/accounts/projects/connections") {
		id = id.Parent
	}
	if !strings.EqualFold(id.ResourceType.String(), "Microsoft.CognitiveServices/accounts/projects") ||
		id.SubscriptionID == "" || id.ResourceGroupName == "" || id.Parent == nil ||
		id.Name == "" || id.Parent.Name == "" {
		return nil, errors.New("a Microsoft.CognitiveServices project or connection ARM resource ID is required")
	}
	return &armContext{
		SubscriptionID: id.SubscriptionID,
		ResourceGroup:  id.ResourceGroupName,
		AccountName:    id.Parent.Name,
		ProjectName:    id.Name,
	}, nil
}
