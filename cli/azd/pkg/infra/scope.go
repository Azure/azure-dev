// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package infra

import (
	"context"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"github.com/azure/azure-dev/pkg/async"
	"github.com/azure/azure-dev/pkg/azapi"
	"github.com/azure/azure-dev/pkg/azure"
)

type Scope interface {
	// SubscriptionId is the id of the subscription which this deployment targets.
	SubscriptionId() string
	// ListDeployments returns all the deployments at this scope.
	ListDeployments(ctx context.Context) ([]*azapi.ResourceDeployment, error)
	Deployment(deploymentName string) Deployment
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
	// Validate a given template on preflight API
	ValidatePreflight(
		ctx context.Context,
		template azure.RawArmTemplate,
		parameters azure.ArmParameters,
		tags map[string]*string,
		options map[string]any,
	) error
	// Deploy a given template with a set of parameters.
	Deploy(
		ctx context.Context,
		template azure.RawArmTemplate,
		parameters azure.ArmParameters,
		tags map[string]*string,
		options map[string]any,
	) (*azapi.ResourceDeployment, error)
	Delete(ctx context.Context,
		options map[string]any,
		progress *async.Progress[azapi.DeleteDeploymentProgress],
	) error
	// Deploy a given template with a set of parameters.
	DeployPreview(
		ctx context.Context,
		template azure.RawArmTemplate,
		parameters azure.ArmParameters,
	) (*armresources.WhatIfOperationResult, error)
	// Deployment fetches information about this deployment.
	Get(ctx context.Context) (*azapi.ResourceDeployment, error)
	// Operations returns all the operations for this deployment.
	Operations(ctx context.Context) ([]*armresources.DeploymentOperation, error)
	Resources(ctx context.Context) ([]*armresources.ResourceReference, error)
}

type ResourceGroupDeployment struct {
	*ResourceGroupScope
	name       string
	deployment *azapi.ResourceDeployment
}

func (s *ResourceGroupDeployment) Name() string {
	return s.name
}

func (s *ResourceGroupDeployment) ValidatePreflight(
	ctx context.Context,
	template azure.RawArmTemplate,
	parameters azure.ArmParameters,
	tags map[string]*string,
	options map[string]any,
) error {
	return s.deploymentService.ValidatePreflightToResourceGroup(
		ctx, s.subscriptionId, s.resourceGroupName, s.name, template, parameters, tags, options)
}

func (s *ResourceGroupDeployment) Deploy(
	ctx context.Context,
	template azure.RawArmTemplate,
	parameters azure.ArmParameters,
	tags map[string]*string,
	options map[string]any,
) (*azapi.ResourceDeployment, error) {
	return s.deploymentService.DeployToResourceGroup(
		ctx, s.subscriptionId, s.resourceGroupName, s.name, template, parameters, tags, options)
}

func (s *ResourceGroupDeployment) Delete(
	ctx context.Context,
	options map[string]any,
	progress *async.Progress[azapi.DeleteDeploymentProgress],
) error {
	return s.deploymentService.DeleteResourceGroupDeployment(
		ctx,
		s.subscriptionId,
		s.resourceGroupName,
		s.name,
		options,
		progress,
	)
}

func (s *ResourceGroupDeployment) DeployPreview(
	ctx context.Context,
	template azure.RawArmTemplate,
	parameters azure.ArmParameters) (*armresources.WhatIfOperationResult, error) {
	return s.deploymentService.WhatIfDeployToResourceGroup(
		ctx, s.subscriptionId, s.resourceGroupName, s.name, template, parameters)
}

// GetDeployment fetches the result of the most recent deployment.
func (s *ResourceGroupDeployment) Get(ctx context.Context) (*azapi.ResourceDeployment, error) {
	return s.deploymentService.GetResourceGroupDeployment(ctx, s.subscriptionId, s.resourceGroupName, s.name)
}

// Gets the resource deployment operations for the current scope
func (s *ResourceGroupDeployment) Operations(ctx context.Context) ([]*armresources.DeploymentOperation, error) {
	return s.deploymentService.
		ListResourceGroupDeploymentOperations(
			ctx,
			s.subscriptionId,
			s.resourceGroupName,
			s.name,
		)
}

func (s *ResourceGroupDeployment) Resources(ctx context.Context) ([]*armresources.ResourceReference, error) {
	return s.deploymentService.ListResourceGroupDeploymentResources(ctx, s.subscriptionId, s.resourceGroupName, s.name)
}

// Gets the url to check deployment resource
func (s *ResourceGroupDeployment) PortalUrl(ctx context.Context) (string, error) {
	if s.deployment == nil {
		deployment, err := s.Get(ctx)
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
		deployment, err := s.Get(ctx)
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
		deployment, err := s.Get(ctx)
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
	deploymentService azapi.DeploymentService
	subscriptionId    string
	resourceGroupName string
}

func newResourceGroupScope(
	deploymentsService azapi.DeploymentService,
	subscriptionId string,
	resourceGroupName string,
) *ResourceGroupScope {
	return &ResourceGroupScope{
		deploymentService: deploymentsService,
		subscriptionId:    subscriptionId,
		resourceGroupName: resourceGroupName,
	}
}

func (s *ResourceGroupScope) SubscriptionId() string {
	return s.subscriptionId
}

func (s *ResourceGroupScope) ResourceGroupName() string {
	return s.resourceGroupName
}

// ListDeployments returns all the deployments in this resource group.
func (s *ResourceGroupScope) ListDeployments(ctx context.Context) ([]*azapi.ResourceDeployment, error) {
	return s.deploymentService.ListResourceGroupDeployments(ctx, s.subscriptionId, s.resourceGroupName)
}

// Deployment gets the deployment with the specified name.
func (s *ResourceGroupScope) Deployment(deploymentName string) Deployment {
	return NewResourceGroupDeployment(s, deploymentName)
}

type SubscriptionDeployment struct {
	*SubscriptionScope
	name       string
	deployment *azapi.ResourceDeployment
}

func (s *SubscriptionDeployment) Name() string {
	return s.name
}

// Gets the url to check deployment resource
func (s *SubscriptionDeployment) PortalUrl(ctx context.Context) (string, error) {
	if s.deployment == nil {
		deployment, err := s.Get(ctx)
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
		deployment, err := s.Get(ctx)
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
		deployment, err := s.Get(ctx)
		if err != nil {
			return "", err
		}

		s.deployment = deployment
	}

	return s.deployment.DeploymentUrl, nil
}

func (s *SubscriptionDeployment) ValidatePreflight(
	ctx context.Context,
	template azure.RawArmTemplate,
	parameters azure.ArmParameters,
	tags map[string]*string,
	options map[string]any,
) error {
	return s.deploymentService.ValidatePreflightToSubscription(ctx, s.subscriptionId, s.location,
		s.name, template, parameters, tags, options)
}

// Deploy a given template with a set of parameters.
func (s *SubscriptionDeployment) Deploy(
	ctx context.Context,
	template azure.RawArmTemplate,
	parameters azure.ArmParameters,
	tags map[string]*string,
	options map[string]any,
) (*azapi.ResourceDeployment, error) {
	return s.deploymentService.DeployToSubscription(
		ctx,
		s.subscriptionId,
		s.location,
		s.name,
		template,
		parameters,
		tags,
		options,
	)
}

func (s *SubscriptionDeployment) Delete(
	ctx context.Context,
	options map[string]any,
	progress *async.Progress[azapi.DeleteDeploymentProgress],
) error {
	return s.deploymentService.DeleteSubscriptionDeployment(ctx, s.subscriptionId, s.name, options, progress)
}

// Deploy a given template with a set of parameters.
func (s *SubscriptionDeployment) DeployPreview(
	ctx context.Context,
	template azure.RawArmTemplate,
	parameters azure.ArmParameters) (*armresources.WhatIfOperationResult, error) {
	return s.deploymentService.WhatIfDeployToSubscription(
		ctx, s.subscriptionId, s.location, s.name, template, parameters)
}

// GetDeployment fetches the result of the most recent deployment.
func (s *SubscriptionDeployment) Get(ctx context.Context) (*azapi.ResourceDeployment, error) {
	return s.deploymentService.GetSubscriptionDeployment(ctx, s.subscriptionId, s.name)
}

// Gets the resource deployment operations for the current scope
func (s *SubscriptionDeployment) Operations(ctx context.Context) ([]*armresources.DeploymentOperation, error) {
	return s.deploymentService.ListSubscriptionDeploymentOperations(ctx, s.subscriptionId, s.name)
}

func (s *SubscriptionDeployment) Resources(ctx context.Context) ([]*armresources.ResourceReference, error) {
	return s.deploymentService.ListSubscriptionDeploymentResources(ctx, s.subscriptionId, s.name)
}

func NewSubscriptionDeployment(
	scope *SubscriptionScope,
	deploymentName string,
) *SubscriptionDeployment {
	return &SubscriptionDeployment{
		SubscriptionScope: scope,
		name:              deploymentName,
	}
}

type SubscriptionScope struct {
	deploymentService azapi.DeploymentService
	subscriptionId    string
	location          string
}

// Gets the Azure subscription id
func (s *SubscriptionScope) SubscriptionId() string {
	return s.subscriptionId
}

func (s *SubscriptionScope) Location() string {
	return s.location
}

func (s *SubscriptionScope) Deployment(deploymentName string) Deployment {
	return NewSubscriptionDeployment(s, deploymentName)
}

// ListDeployments returns all the deployments at subscription scope.
func (s *SubscriptionScope) ListDeployments(ctx context.Context) ([]*azapi.ResourceDeployment, error) {
	return s.deploymentService.ListSubscriptionDeployments(ctx, s.subscriptionId)
}

func newSubscriptionScope(
	deploymentsService azapi.DeploymentService,
	subscriptionId string,
	location string,
) *SubscriptionScope {
	return &SubscriptionScope{
		deploymentService: deploymentsService,
		subscriptionId:    subscriptionId,
		location:          location,
	}
}
