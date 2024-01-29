// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azapi

import (
	"context"
	"errors"
	"fmt"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	azdinternal "github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/account"
	"github.com/azure/azure-dev/cli/azd/pkg/azsdk"
	azdCloud "github.com/azure/azure-dev/cli/azd/pkg/cloud"
	"github.com/azure/azure-dev/cli/azd/pkg/httputil"
)

type DeploymentOperations interface {
	ListSubscriptionDeploymentOperations(
		ctx context.Context,
		subscriptionId string,
		deploymentName string,
	) ([]*armresources.DeploymentOperation, error)
	ListResourceGroupDeploymentOperations(
		ctx context.Context,
		subscriptionId string,
		resourceGroupName string,
		deploymentName string,
	) ([]*armresources.DeploymentOperation, error)
}

func NewDeploymentOperations(
	credentialProvider account.SubscriptionCredentialProvider,
	httpClient httputil.HttpClient,
	cloud *azdCloud.Cloud,
) DeploymentOperations {
	return &deploymentOperations{
		credentialProvider: credentialProvider,
		httpClient:         httpClient,
		userAgent:          azdinternal.UserAgent(),
		cloud:              cloud,
	}
}

type deploymentOperations struct {
	credentialProvider account.SubscriptionCredentialProvider
	httpClient         httputil.HttpClient
	userAgent          string
	cloud              *azdCloud.Cloud
}

func (dp *deploymentOperations) createDeploymentsOperationsClient(
	ctx context.Context,
	subscriptionId string,
) (*armresources.DeploymentOperationsClient, error) {
	credential, err := dp.credentialProvider.CredentialForSubscription(ctx, subscriptionId)
	if err != nil {
		return nil, err
	}

	options := dp.clientOptionsBuilder(ctx).BuildArmClientOptions()
	client, err := armresources.NewDeploymentOperationsClient(subscriptionId, credential, options)
	if err != nil {
		return nil, fmt.Errorf("creating deployments client: %w", err)
	}

	return client, nil
}

func (dp *deploymentOperations) ListSubscriptionDeploymentOperations(
	ctx context.Context,
	subscriptionId string,
	deploymentName string,
) ([]*armresources.DeploymentOperation, error) {
	result := []*armresources.DeploymentOperation{}
	deploymentOperationsClient, err := dp.createDeploymentsOperationsClient(ctx, subscriptionId)
	if err != nil {
		return nil, fmt.Errorf("creating deployments client: %w", err)
	}

	// Get all without any filter
	getDeploymentsPager := deploymentOperationsClient.NewListAtSubscriptionScopePager(deploymentName, nil)

	for getDeploymentsPager.More() {
		page, err := getDeploymentsPager.NextPage(ctx)
		var errDetails *azcore.ResponseError
		if errors.As(err, &errDetails) && errDetails.StatusCode == 404 {
			return nil, ErrDeploymentNotFound
		}
		if err != nil {
			return nil, fmt.Errorf("failed getting list of deployment operations: %w", err)
		}
		result = append(result, page.Value...)
	}

	return result, nil
}

func (dp *deploymentOperations) ListResourceGroupDeploymentOperations(
	ctx context.Context,
	subscriptionId string,
	resourceGroupName string,
	deploymentName string,
) ([]*armresources.DeploymentOperation, error) {
	result := []*armresources.DeploymentOperation{}
	deploymentOperationsClient, err := dp.createDeploymentsOperationsClient(ctx, subscriptionId)
	if err != nil {
		return nil, fmt.Errorf("creating deployments client: %w", err)
	}

	// Get all without any filter
	getDeploymentsPager := deploymentOperationsClient.NewListPager(resourceGroupName, deploymentName, nil)

	for getDeploymentsPager.More() {
		page, err := getDeploymentsPager.NextPage(ctx)
		var errDetails *azcore.ResponseError
		if errors.As(err, &errDetails) && errDetails.StatusCode == 404 {
			return nil, ErrDeploymentNotFound
		}
		if err != nil {
			return nil, fmt.Errorf("failed getting list of deployment operations from resource group: %w", err)
		}
		result = append(result, page.Value...)
	}

	return result, nil
}

func (dp *deploymentOperations) clientOptionsBuilder(ctx context.Context) *azsdk.ClientOptionsBuilder {
	return azsdk.NewClientOptionsBuilder().
		WithTransport(dp.httpClient).
		WithPerCallPolicy(azsdk.NewUserAgentPolicy(dp.userAgent)).
		WithPerCallPolicy(azsdk.NewMsCorrelationPolicy(ctx)).
		WithCloud(*dp.cloud.Configuration)
}
