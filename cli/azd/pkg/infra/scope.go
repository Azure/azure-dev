// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package infra

import (
	"context"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"github.com/azure/azure-dev/cli/azd/pkg/azapi"
	"github.com/azure/azure-dev/cli/azd/pkg/azure"
)

type ScopeType string

const (
	ScopeTypeSubscription  ScopeType = "subscription"
	ScopeTypeResourceGroup ScopeType = "resourcegroup"
)

type Scope interface {
	Type() ScopeType
	// SubscriptionId is the id of the subscription which this deployment targets.
	SubscriptionId() string
	// ListDeployments returns all the deployments at this scope.
	ListDeployments(ctx context.Context) ([]*azapi.ResourceDeployment, error)
}

type Deployment interface {
	Scope
	// Name is the name of this deployment.
	Name() string
	// PortalUrl is the URL that may be used to view this deployment resource in the Azure Portal.
	PortalUrl(ctx context.Context) (string, error)
	// OutputsUrl is the URL that may be used to view this deployment outputs the in Azure Portal.
	OutputsUrl(ctx context.Context) (string, error)
	// DeploymentUrl is the URL that may be used to view this deployment progress in the Azure Portal.
	DeploymentUrl(ctx context.Context) (string, error)
	// Deploy a given template with a set of parameters.
	Deploy(
		ctx context.Context,
		template azure.RawArmTemplate,
		parameters azure.ArmParameters,
		tags map[string]*string,
	) (*azapi.ResourceDeployment, error)
	// Deploy a given template with a set of parameters.
	DeployPreview(
		ctx context.Context,
		template azure.RawArmTemplate,
		parameters azure.ArmParameters,
	) (*armresources.WhatIfOperationResult, error)
	// Deployment fetches information about this deployment.
	Deployment(ctx context.Context) (*azapi.ResourceDeployment, error)
	// Operations returns all the operations for this deployment.
	Operations(ctx context.Context) ([]*armresources.DeploymentOperation, error)
}

type ResourceGroupDeployment struct {
	*ResourceGroupScope
	name       string
	deployment *azapi.ResourceDeployment
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
) (*azapi.ResourceDeployment, error) {
	return s.deployments.DeployToResourceGroup(
		ctx, s.subscriptionId, s.resourceGroupName, s.name, template, parameters, tags)
}

func (s *ResourceGroupDeployment) DeployPreview(
	ctx context.Context,
	template azure.RawArmTemplate,
	parameters azure.ArmParameters) (*armresources.WhatIfOperationResult, error) {
	return s.deployments.WhatIfDeployToResourceGroup(
		ctx, s.subscriptionId, s.resourceGroupName, s.name, template, parameters)
}

// GetDeployment fetches the result of the most recent deployment.
func (s *ResourceGroupDeployment) Deployment(ctx context.Context) (*azapi.ResourceDeployment, error) {
	return s.deployments.GetResourceGroupDeployment(ctx, s.subscriptionId, s.resourceGroupName, s.name)
}

// Gets the resource deployment operations for the current scope
func (s *ResourceGroupDeployment) Operations(ctx context.Context) ([]*armresources.DeploymentOperation, error) {
	return s.deploymentOperations.ListResourceGroupDeploymentOperations(
		ctx, s.subscriptionId, s.resourceGroupName, s.name)
}

// Gets the url to check deployment resource
func (s *ResourceGroupDeployment) PortalUrl(ctx context.Context) (string, error) {
	if s.deployment == nil {
		deployment, err := s.Deployment(ctx)
		if err != nil {
			return "", err
		}

		s.deployment = deployment
	}

	return s.deployment.PortalUrl, nil
}

// Gets the url to view deployment outputs
func (s *ResourceGroupDeployment) OutputsUrl(ctx context.Context) (string, error) {
	if s.deployment == nil {
		deployment, err := s.Deployment(ctx)
		if err != nil {
			return "", err
		}

		s.deployment = deployment
	}

	return s.deployment.OutputsUrl, nil
}

// Gets the url to view deployment
func (s *ResourceGroupDeployment) DeploymentUrl(ctx context.Context) (string, error) {
	if s.deployment == nil {
		deployment, err := s.Deployment(ctx)
		if err != nil {
			return "", err
		}

		s.deployment = deployment
	}

	return s.deployment.DeploymentUrl, nil
}

func NewResourceGroupDeployment(scope *ResourceGroupScope, deploymentName string) *ResourceGroupDeployment {
	return &ResourceGroupDeployment{
		ResourceGroupScope: scope,
		name:               deploymentName,
	}
}

type ResourceGroupScope struct {
	deployments          azapi.Deployments
	deploymentOperations azapi.DeploymentOperations
	subscriptionId       string
	resourceGroupName    string
}

func newResourceGroupScope(
	deploymentsService azapi.Deployments,
	deploymentOperations azapi.DeploymentOperations,
	subscriptionId string, resourceGroupName string) *ResourceGroupScope {
	return &ResourceGroupScope{
		deployments:          deploymentsService,
		deploymentOperations: deploymentOperations,
		subscriptionId:       subscriptionId,
		resourceGroupName:    resourceGroupName,
	}
}

func (s *ResourceGroupScope) Type() ScopeType {
	return ScopeTypeResourceGroup
}

func (s *ResourceGroupScope) SubscriptionId() string {
	return s.subscriptionId
}

func (s *ResourceGroupScope) ResourceGroupName() string {
	return s.resourceGroupName
}

// ListDeployments returns all the deployments in this resource group.
func (s *ResourceGroupScope) ListDeployments(ctx context.Context) ([]*azapi.ResourceDeployment, error) {
	return s.deployments.ListResourceGroupDeployments(ctx, s.subscriptionId, s.resourceGroupName)
}

const cPortalUrlFragment = "#view/HubsExtension/DeploymentDetailsBlade/~/overview/id"
const cOutputsUrlFragment = "#view/HubsExtension/DeploymentDetailsBlade/~/outputs/id"

type SubscriptionDeployment struct {
	*SubscriptionScope
	name       string
	location   string
	deployment *azapi.ResourceDeployment
}

func (s *SubscriptionDeployment) Name() string {
	return s.name
}

// Gets the Azure subscription id
func (s *SubscriptionDeployment) SubscriptionId() string {
	return s.subscriptionId
}

// Gets the url to check deployment resource
func (s *SubscriptionDeployment) PortalUrl(ctx context.Context) (string, error) {
	if s.deployment == nil {
		deployment, err := s.Deployment(ctx)
		if err != nil {
			return "", err
		}

		s.deployment = deployment
	}

	return s.deployment.PortalUrl, nil
}

// Gets the url to view deployment outputs
func (s *SubscriptionDeployment) OutputsUrl(ctx context.Context) (string, error) {
	if s.deployment == nil {
		deployment, err := s.Deployment(ctx)
		if err != nil {
			return "", err
		}

		s.deployment = deployment
	}

	return s.deployment.OutputsUrl, nil
}

// Gets the url to view deployment
func (s *SubscriptionDeployment) DeploymentUrl(ctx context.Context) (string, error) {
	if s.deployment == nil {
		deployment, err := s.Deployment(ctx)
		if err != nil {
			return "", err
		}

		s.deployment = deployment
	}

	return s.deployment.DeploymentUrl, nil
}

// Gets the Azure location for the subscription deployment
func (s *SubscriptionDeployment) Location() string {
	return s.location
}

// Deploy a given template with a set of parameters.
func (s *SubscriptionDeployment) Deploy(
	ctx context.Context,
	template azure.RawArmTemplate,
	parameters azure.ArmParameters,
	tags map[string]*string,
) (*azapi.ResourceDeployment, error) {
	return s.deploymentsService.DeployToSubscription(ctx, s.subscriptionId, s.location, s.name, template, parameters, tags)
}

// Deploy a given template with a set of parameters.
func (s *SubscriptionDeployment) DeployPreview(
	ctx context.Context,
	template azure.RawArmTemplate,
	parameters azure.ArmParameters) (*armresources.WhatIfOperationResult, error) {
	return s.deploymentsService.WhatIfDeployToSubscription(
		ctx, s.subscriptionId, s.location, s.name, template, parameters)
}

// GetDeployment fetches the result of the most recent deployment.
func (s *SubscriptionDeployment) Deployment(ctx context.Context) (*azapi.ResourceDeployment, error) {
	return s.deploymentsService.GetSubscriptionDeployment(ctx, s.subscriptionId, s.name)
}

// Gets the resource deployment operations for the current scope
func (s *SubscriptionDeployment) Operations(ctx context.Context) ([]*armresources.DeploymentOperation, error) {
	return s.deploymentOperations.ListSubscriptionDeploymentOperations(ctx, s.subscriptionId, s.name)
}

func NewSubscriptionDeployment(
	scope *SubscriptionScope,
	location string,
	deploymentName string,
) *SubscriptionDeployment {
	return &SubscriptionDeployment{
		SubscriptionScope: scope,
		name:              deploymentName,
		location:          location,
	}
}

type SubscriptionScope struct {
	deploymentsService   azapi.Deployments
	deploymentOperations azapi.DeploymentOperations
	subscriptionId       string
}

func (s *SubscriptionScope) Type() ScopeType {
	return ScopeTypeSubscription
}

// Gets the Azure subscription id
func (s *SubscriptionScope) SubscriptionId() string {
	return s.subscriptionId
}

// ListDeployments returns all the deployments at subscription scope.
func (s *SubscriptionScope) ListDeployments(ctx context.Context) ([]*azapi.ResourceDeployment, error) {
	return s.deploymentsService.ListSubscriptionDeployments(ctx, s.subscriptionId)
}

func newSubscriptionScope(
	deploymentsService azapi.Deployments,
	deploymentOperations azapi.DeploymentOperations,
	subscriptionId string,
) *SubscriptionScope {
	return &SubscriptionScope{
		deploymentsService:   deploymentsService,
		deploymentOperations: deploymentOperations,
		subscriptionId:       subscriptionId,
	}
}
