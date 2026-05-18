// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"

	"azureaiagent/internal/connections/exterrors"
	"azureaiagent/internal/connections/pkg/connections"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/cognitiveservices/armcognitiveservices/v2"
)

// dataClient is a type alias for the data-plane client (used in endpoint.go).
type dataClient = connections.DataClient

// connectionContext holds the resolved clients and project info for connection operations.
type connectionContext struct {
	armClient *armcognitiveservices.ProjectConnectionsClient
	dpClient  *connections.DataClient
	rg        string
	account   string
	project   string
}

// resolveConnectionContext resolves the project endpoint, discovers ARM context,
// and creates both clients needed for connection operations.
func resolveConnectionContext(
	ctx context.Context,
	flagEndpoint string,
) (*connectionContext, error) {
	endpoint, err := resolveProjectEndpoint(ctx, flagEndpoint)
	if err != nil {
		return nil, err
	}

	account, project, err := parseEndpointComponents(endpoint)
	if err != nil {
		return nil, err
	}

	cred, err := newCredential()
	if err != nil {
		return nil, err
	}

	// Data-plane client (for list, get-with-credentials, and ARM discovery)
	dpClient := connections.NewDataClient(endpoint, cred)

	// Discover subscription + resource group from data-plane response
	armCtx, err := discoverARMContext(ctx, dpClient)
	if err != nil {
		return nil, err
	}

	// ARM SDK client for CRUD
	armClient, err := armcognitiveservices.NewProjectConnectionsClient(
		armCtx.SubscriptionID, cred, nil,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create ARM connections client: %w", err)
	}

	return &connectionContext{
		armClient: armClient,
		dpClient:  dpClient,
		rg:        armCtx.ResourceGroup,
		account:   account,
		project:   project,
	}, nil
}

// newCredential creates an Azure credential for API calls.
func newCredential() (azcore.TokenCredential, error) {
	cred, err := azidentity.NewAzureDeveloperCLICredential(
		&azidentity.AzureDeveloperCLICredentialOptions{},
	)
	if err != nil {
		return nil, exterrors.Auth(
			exterrors.CodeCredentialCreationFailed,
			fmt.Sprintf("Failed to create Azure credential: %s", err),
			"Run 'azd auth login' to authenticate.",
		)
	}

	return cred, nil
}
