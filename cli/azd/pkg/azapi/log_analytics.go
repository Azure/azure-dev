// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azapi

import (
	"context"
	"fmt"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/operationalinsights/armoperationalinsights/v2"
	"github.com/azure/azure-dev/cli/azd/internal/agent/tools/common"
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
	workspacesDeletedClient, err := cli.createLogAnalyticsWorkspacesClient(ctx, subscriptionId)
	if err != nil {
		return err
	}

	deleteOpts := &armoperationalinsights.WorkspacesClientBeginDeleteOptions{
		Force: common.ToPtr(true),
	}

	poller, err := workspacesDeletedClient.BeginDelete(ctx, resourceGroupName, workspaceName, deleteOpts)
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
	credential, err := cli.credentialProvider.CredentialForSubscription(ctx, subscriptionId)
	if err != nil {
		return nil, err
	}

	client, err := armoperationalinsights.NewWorkspacesClient(subscriptionId, credential, cli.armClientOptions)
	if err != nil {
		return nil, fmt.Errorf("creating log analytics workspaces client: %w", err)
	}

	return client, nil
}
