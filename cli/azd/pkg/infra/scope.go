// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package infra

import (
	"context"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"github.com/azure/azure-dev/cli/azd/pkg/azure"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/azcli"
)

type Scope interface {
	// Gets the Azure subscription id
	SubscriptionId() string
	// Gets the deployment name
	Name() string
	// Gets the url to check deployment progress
	DeploymentUrl() string
	// Deploy a given template with a set of parameters.
	Deploy(ctx context.Context, template *azure.ArmTemplate, parametersPath string) error
	// GetDeployment fetches the result of the most recent deployment.
	GetDeployment(ctx context.Context) (*armresources.DeploymentExtended, error)
	// Gets the resource deployment operations for the current scope
	GetResourceOperations(ctx context.Context) ([]*armresources.DeploymentOperation, error)
}

type ResourceGroupScope struct {
	azCli          azcli.AzCli
	name           string
	subscriptionId string
	resourceGroup  string
}

// Gets the Azure subscription id
func (s *ResourceGroupScope) SubscriptionId() string {
	return s.subscriptionId
}

// Gets the deployment name
func (s *ResourceGroupScope) Name() string {
	return s.name
}

// Gets the resource group name
func (s *ResourceGroupScope) ResourceGroup() string {
	return s.resourceGroup
}

func (s *ResourceGroupScope) Deploy(ctx context.Context, template *azure.ArmTemplate, parametersPath string) error {
	_, err := s.azCli.DeployToResourceGroup(ctx, s.subscriptionId, s.resourceGroup, s.name, template, parametersPath)
	return err
}

// GetDeployment fetches the result of the most recent deployment.
func (s *ResourceGroupScope) GetDeployment(ctx context.Context) (*armresources.DeploymentExtended, error) {
	return s.azCli.GetResourceGroupDeployment(ctx, s.subscriptionId, s.resourceGroup, s.name)
}

// Gets the resource deployment operations for the current scope
func (s *ResourceGroupScope) GetResourceOperations(ctx context.Context) ([]*armresources.DeploymentOperation, error) {
	return s.azCli.ListResourceGroupDeploymentOperations(ctx, s.subscriptionId, s.resourceGroup, s.name)
}

// Gets the url to check deployment progress
func (s *ResourceGroupScope) DeploymentUrl() string {
	return azure.ResourceGroupDeploymentRID(s.subscriptionId, s.resourceGroup, s.name)
}

func NewResourceGroupScope(
	ctx context.Context, subscriptionId string, resourceGroup string, deploymentName string) Scope {
	return &ResourceGroupScope{
		azCli:          azcli.GetAzCli(ctx),
		name:           deploymentName,
		subscriptionId: subscriptionId,
		resourceGroup:  resourceGroup,
	}
}

type SubscriptionScope struct {
	azCli          azcli.AzCli
	name           string
	subscriptionId string
	location       string
}

// Gets the deployment name
func (s *SubscriptionScope) Name() string {
	return s.name
}

// Gets the Azure subscription id
func (s *SubscriptionScope) SubscriptionId() string {
	return s.subscriptionId
}

// Gets the url to check deployment progress
func (s *SubscriptionScope) DeploymentUrl() string {
	return azure.SubscriptionDeploymentRID(s.subscriptionId, s.name)
}

// Gets the Azure location for the subscription deployment
func (s *SubscriptionScope) Location() string {
	return s.location
}

// Deploy a given template with a set of parameters.
func (s *SubscriptionScope) Deploy(ctx context.Context, template *azure.ArmTemplate, parametersPath string) error {
	_, err := s.azCli.DeployToSubscription(ctx, s.subscriptionId, s.name, template, parametersPath, s.location)
	return err
}

// GetDeployment fetches the result of the most recent deployment.
func (s *SubscriptionScope) GetDeployment(ctx context.Context) (*armresources.DeploymentExtended, error) {
	return s.azCli.GetSubscriptionDeployment(ctx, s.subscriptionId, s.name)
}

// Gets the resource deployment operations for the current scope
func (s *SubscriptionScope) GetResourceOperations(ctx context.Context) ([]*armresources.DeploymentOperation, error) {
	return s.azCli.ListSubscriptionDeploymentOperations(ctx, s.subscriptionId, s.name)
}

func NewSubscriptionScope(ctx context.Context, location string, subscriptionId string, deploymentName string) Scope {
	return &SubscriptionScope{
		azCli:          azcli.GetAzCli(ctx),
		name:           deploymentName,
		subscriptionId: subscriptionId,
		location:       location,
	}
}
