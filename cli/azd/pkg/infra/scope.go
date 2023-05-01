// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package infra

import (
	"context"
	"fmt"
	"net/url"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"github.com/azure/azure-dev/cli/azd/pkg/azure"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/azcli"
)

type Deployment interface {
	// SubscriptionId is the id of the subscription which this deployment targets.
	SubscriptionId() string
	// Name is the name of this deployment.
	Name() string
	// PortalUrl is the URL that may be used to view this deployment in Azure Portal.
	PortalUrl() string
	// Deploy a given template with a set of parameters.
	Deploy(
		ctx context.Context,
		template azure.RawArmTemplate,
		parameters azure.ArmParameters,
	) (*armresources.DeploymentExtended, error)
	// Deployment fetches information about this deployment.
	Deployment(ctx context.Context) (*armresources.DeploymentExtended, error)
	// Operations returns all the operations for this deployment.
	Operations(ctx context.Context) ([]*armresources.DeploymentOperation, error)
}

type ResourceGroupDeployment struct {
	azCli             azcli.AzCli
	subscriptionId    string
	resourceGroupName string
	name              string
}

func (s *ResourceGroupDeployment) Name() string {
	return s.name
}

// Gets the Azure subscription id
func (s *ResourceGroupDeployment) SubscriptionId() string {
	return s.subscriptionId
}

// Gets the resource group name
func (s *ResourceGroupDeployment) ResourceGroupName() string {
	return s.resourceGroupName
}

func (s *ResourceGroupDeployment) Deploy(
	ctx context.Context, template azure.RawArmTemplate, parameters azure.ArmParameters,
) (*armresources.DeploymentExtended, error) {
	return s.azCli.DeployToResourceGroup(ctx, s.subscriptionId, s.resourceGroupName, s.name, template, parameters)
}

// GetDeployment fetches the result of the most recent deployment.
func (s *ResourceGroupDeployment) Deployment(ctx context.Context) (*armresources.DeploymentExtended, error) {
	return s.azCli.GetResourceGroupDeployment(ctx, s.subscriptionId, s.resourceGroupName, s.name)
}

// Gets the resource deployment operations for the current scope
func (s *ResourceGroupDeployment) Operations(ctx context.Context) ([]*armresources.DeploymentOperation, error) {
	return s.azCli.ListResourceGroupDeploymentOperations(ctx, s.subscriptionId, s.resourceGroupName, s.name)
}

// Gets the url to check deployment progress
func (s *ResourceGroupDeployment) PortalUrl() string {
	return fmt.Sprintf("%s/%s",
		cPortalUrlPrefix,
		url.PathEscape(azure.ResourceGroupDeploymentRID(s.subscriptionId, s.resourceGroupName, s.name)))
}

func NewResourceGroupDeployment(
	azCli azcli.AzCli, subscriptionId string, resourceGroupName string, deploymentName string,
) Deployment {
	return &ResourceGroupDeployment{
		azCli:             azCli,
		subscriptionId:    subscriptionId,
		resourceGroupName: resourceGroupName,
		name:              deploymentName,
	}
}

// cPortalUrlPrefix is the prefix which can be combined with the RID of a deployment to produce a URL into the Azure Portal
// that shows information about the deployment.
const cPortalUrlPrefix = "https://portal.azure.com/#blade/HubsExtension/DeploymentDetailsBlade/overview/id"

type SubscriptionDeployment struct {
	azCli          azcli.AzCli
	subscriptionId string
	name           string
	location       string
}

func (s *SubscriptionDeployment) Name() string {
	return s.name
}

// Gets the Azure subscription id
func (s *SubscriptionDeployment) SubscriptionId() string {
	return s.subscriptionId
}

// Gets the url to check deployment progress
func (s *SubscriptionDeployment) PortalUrl() string {
	return fmt.Sprintf("%s/%s",
		cPortalUrlPrefix,
		url.PathEscape(azure.SubscriptionDeploymentRID(s.subscriptionId, s.name)))
}

// Gets the Azure location for the subscription deployment
func (s *SubscriptionDeployment) Location() string {
	return s.location
}

// Deploy a given template with a set of parameters.
func (s *SubscriptionDeployment) Deploy(
	ctx context.Context, template azure.RawArmTemplate, parameters azure.ArmParameters,
) (*armresources.DeploymentExtended, error) {
	return s.azCli.DeployToSubscription(ctx, s.subscriptionId, s.location, s.name, template, parameters)
}

// GetDeployment fetches the result of the most recent deployment.
func (s *SubscriptionDeployment) Deployment(ctx context.Context) (*armresources.DeploymentExtended, error) {
	return s.azCli.GetSubscriptionDeployment(ctx, s.subscriptionId, s.name)
}

// Gets the resource deployment operations for the current scope
func (s *SubscriptionDeployment) Operations(ctx context.Context) ([]*armresources.DeploymentOperation, error) {
	return s.azCli.ListSubscriptionDeploymentOperations(ctx, s.subscriptionId, s.name)
}

func NewSubscriptionDeployment(
	azCli azcli.AzCli, location string, subscriptionId string, deploymentName string,
) *SubscriptionDeployment {
	return &SubscriptionDeployment{
		azCli:          azCli,
		subscriptionId: subscriptionId,
		name:           deploymentName,
		location:       location,
	}
}
