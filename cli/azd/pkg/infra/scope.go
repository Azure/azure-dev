// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package infra

import (
	"context"
	"fmt"
	"net/url"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"github.com/azure/azure-dev/cli/azd/pkg/azure"
	"github.com/azure/azure-dev/cli/azd/pkg/azureapis"
)

type Scope interface {
	// SubscriptionId is the id of the subscription which this deployment targets.
	SubscriptionId() string
	// ListDeployments returns all the deployments at this scope.
	ListDeployments(ctx context.Context) ([]*armresources.DeploymentExtended, error)
}

type Deployment interface {
	Scope
	// Name is the name of this deployment.
	Name() string
	// PortalUrl is the URL that may be used to view this deployment in Azure Portal.
	PortalUrl() string
	// Deploy a given template with a set of parameters.
	Deploy(
		ctx context.Context,
		template azure.RawArmTemplate,
		parameters azure.ArmParameters,
		tags map[string]*string,
	) (*armresources.DeploymentExtended, error)
	// Deployment fetches information about this deployment.
	Deployment(ctx context.Context) (*armresources.DeploymentExtended, error)
	// Operations returns all the operations for this deployment.
	Operations(ctx context.Context) ([]*armresources.DeploymentOperation, error)
}

type ResourceGroupDeployment struct {
	*ResourceGroupScope
	name string
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
	ctx context.Context, template azure.RawArmTemplate, parameters azure.ArmParameters, tags map[string]*string,
) (*armresources.DeploymentExtended, error) {
	return s.deploymentsService.DeployToResourceGroup(
		ctx, s.subscriptionId, s.resourceGroupName, s.name, template, parameters, tags)
}

// GetDeployment fetches the result of the most recent deployment.
func (s *ResourceGroupDeployment) Deployment(ctx context.Context) (*armresources.DeploymentExtended, error) {
	return s.deploymentsService.GetResourceGroupDeployment(ctx, s.subscriptionId, s.resourceGroupName, s.name)
}

// Gets the resource deployment operations for the current scope
func (s *ResourceGroupDeployment) Operations(ctx context.Context) ([]*armresources.DeploymentOperation, error) {
	return s.deploymentOperationsService.ListResourceGroupDeploymentOperations(
		ctx, s.subscriptionId, s.resourceGroupName, s.name)
}

// Gets the url to check deployment progress
func (s *ResourceGroupDeployment) PortalUrl() string {
	return fmt.Sprintf("%s/%s",
		cPortalUrlPrefix,
		url.PathEscape(azure.ResourceGroupDeploymentRID(s.subscriptionId, s.resourceGroupName, s.name)))
}

func NewResourceGroupDeployment(
	deploymentsService azureapis.Deployments,
	deploymentOperationsService azureapis.DeploymentOperations,
	subscriptionId string, resourceGroupName string, deploymentName string,
) Deployment {
	return &ResourceGroupDeployment{
		ResourceGroupScope: NewResourceGroupScope(
			deploymentsService,
			deploymentOperationsService,
			subscriptionId, resourceGroupName),
		name: deploymentName,
	}
}

type ResourceGroupScope struct {
	deploymentsService          azureapis.Deployments
	deploymentOperationsService azureapis.DeploymentOperations
	subscriptionId              string
	resourceGroupName           string
}

func NewResourceGroupScope(
	deploymentsService azureapis.Deployments,
	deploymentOperationsService azureapis.DeploymentOperations,
	subscriptionId string, resourceGroupName string) *ResourceGroupScope {
	return &ResourceGroupScope{
		deploymentsService:          deploymentsService,
		deploymentOperationsService: deploymentOperationsService,
		subscriptionId:              subscriptionId,
		resourceGroupName:           resourceGroupName,
	}
}

func (s *ResourceGroupScope) SubscriptionId() string {
	return s.subscriptionId
}

func (s *ResourceGroupScope) ResourceGroupName() string {
	return s.resourceGroupName
}

// ListDeployments returns all the deployments in this resource group.
func (s *ResourceGroupScope) ListDeployments(ctx context.Context) ([]*armresources.DeploymentExtended, error) {
	return s.deploymentsService.ListResourceGroupDeployments(ctx, s.subscriptionId, s.resourceGroupName)
}

// cPortalUrlPrefix is the prefix which can be combined with the RID of a deployment to produce a URL into the Azure Portal
// that shows information about the deployment.
const cPortalUrlPrefix = "https://portal.azure.com/#blade/HubsExtension/DeploymentDetailsBlade/overview/id"

type SubscriptionDeployment struct {
	*SubscriptionScope
	name     string
	location string
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
	ctx context.Context, template azure.RawArmTemplate, parameters azure.ArmParameters, tags map[string]*string,
) (*armresources.DeploymentExtended, error) {
	return s.deploymentsService.DeployToSubscription(ctx, s.subscriptionId, s.location, s.name, template, parameters, tags)
}

// GetDeployment fetches the result of the most recent deployment.
func (s *SubscriptionDeployment) Deployment(ctx context.Context) (*armresources.DeploymentExtended, error) {
	return s.deploymentsService.GetSubscriptionDeployment(ctx, s.subscriptionId, s.name)
}

// Gets the resource deployment operations for the current scope
func (s *SubscriptionDeployment) Operations(ctx context.Context) ([]*armresources.DeploymentOperation, error) {
	return s.deploymentOperationsService.ListSubscriptionDeploymentOperations(ctx, s.subscriptionId, s.name)
}

func NewSubscriptionDeployment(
	deploymentsService azureapis.Deployments,
	deploymentOperationsService azureapis.DeploymentOperations,
	location string, subscriptionId string, deploymentName string,
) *SubscriptionDeployment {
	return &SubscriptionDeployment{
		SubscriptionScope: NewSubscriptionScope(
			deploymentsService,
			deploymentOperationsService,
			subscriptionId),
		name:     deploymentName,
		location: location,
	}
}

type SubscriptionScope struct {
	deploymentsService          azureapis.Deployments
	deploymentOperationsService azureapis.DeploymentOperations
	subscriptionId              string
}

// Gets the Azure subscription id
func (s *SubscriptionScope) SubscriptionId() string {
	return s.subscriptionId
}

// ListDeployments returns all the deployments at subscription scope.
func (s *SubscriptionScope) ListDeployments(ctx context.Context) ([]*armresources.DeploymentExtended, error) {
	return s.deploymentsService.ListSubscriptionDeployments(ctx, s.subscriptionId)
}

func NewSubscriptionScope(
	deploymentsService azureapis.Deployments,
	deploymentOperationsService azureapis.DeploymentOperations,
	subscriptionId string) *SubscriptionScope {
	return &SubscriptionScope{
		deploymentsService:          deploymentsService,
		deploymentOperationsService: deploymentOperationsService,
		subscriptionId:              subscriptionId,
	}
}
