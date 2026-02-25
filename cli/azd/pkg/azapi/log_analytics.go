// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azapi

import (
	"context"
	"fmt"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/operationalinsights/armoperationalinsights/v2"
)

type AzCliLogAnalyticsWorkspace struct {
	Id   string `json:"id"`
	Name string `json:"name"`
}

func (cli *AzureClient) GetLogAnalyticsWorkspace(
	ctx context.Context,
	subscriptionId string,
	resourceGroupName string,
	workspaceName string,
) (*AzCliLogAnalyticsWorkspace, error) {
	workspacesClient, err := cli.createLogAnalyticsWorkspacesClient(ctx, subscriptionId)
	if err != nil {
		return nil, err
	}

	workspace, err := workspacesClient.Get(ctx, resourceGroupName, workspaceName, nil)
	if err != nil {
		return nil, fmt.Errorf("failed getting log analytics workspace: %w", err)
	}

	return &AzCliLogAnalyticsWorkspace{
		Id:   *workspace.ID,
		Name: *workspace.Name,
	}, nil
}

func (cli *AzureClient) PurgeLogAnalyticsWorkspace(
	ctx context.Context,
	subscriptionId string,
	resourceGroupName string,
	workspaceName string,
) error {
	workspacesClient, err := cli.createLogAnalyticsWorkspacesClient(ctx, subscriptionId)
	if err != nil {
		return err
	}

	deleteOpts := &armoperationalinsights.WorkspacesClientBeginDeleteOptions{
		Force: to.Ptr(true),
	}

	// https://learn.microsoft.com/rest/api/loganalytics/workspaces/delete?view=rest-loganalytics-2025-07-01&tabs=HTTP
	// Purging a workspace is done by setting the Force parameter to true when deleting the workspace
	poller, err := workspacesClient.BeginDelete(ctx, resourceGroupName, workspaceName, deleteOpts)
	if err != nil {
		return fmt.Errorf("starting purging log analytics workspace: %w", err)
	}

	_, err = poller.PollUntilDone(ctx, nil)
	if err != nil {
		return fmt.Errorf("purging log analytics workspace: %w", err)
	}

	return nil
}

func (cli *AzureClient) createLogAnalyticsWorkspacesClient(
	ctx context.Context,
	subscriptionId string,
) (*armoperationalinsights.WorkspacesClient, error) {
	return cli.logAnalyticsWorkspacesCache.GetOrCreate(
		subscriptionId,
		func() (*armoperationalinsights.WorkspacesClient, error) {
			credential, err := cli.credentialProvider.CredentialForSubscription(ctx, subscriptionId)
			if err != nil {
				return nil, err
			}

			client, err := armoperationalinsights.NewWorkspacesClient(subscriptionId, credential, cli.armClientOptions)
			if err != nil {
				return nil, fmt.Errorf("creating log analytics workspaces client: %w", err)
			}

			return client, nil
		},
	)
}
