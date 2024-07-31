package azapi

import (
	"context"
	"fmt"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armdeploymentstacks"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"github.com/azure/azure-dev/cli/azd/pkg/account"
	"github.com/azure/azure-dev/cli/azd/pkg/alpha"
	"github.com/azure/azure-dev/cli/azd/pkg/azure"
	"github.com/azure/azure-dev/cli/azd/pkg/convert"
	"github.com/benbjohnson/clock"
)

var FeatureDeploymentStacks = alpha.MustFeatureKey("deployment.stacks")

type deploymentStacks struct {
	credentialProvider  account.SubscriptionCredentialProvider
	armClientOptions    *arm.ClientOptions
	standardDeployments Deployments
}

func NewDeploymentStacks(
	credentialProvider account.SubscriptionCredentialProvider,
	armClientOptions *arm.ClientOptions,
	clock clock.Clock,
) Deployments {
	return &deploymentStacks{
		credentialProvider:  credentialProvider,
		armClientOptions:    armClientOptions,
		standardDeployments: NewDeployments(credentialProvider, armClientOptions, clock),
	}
}

// GenerateDeploymentName creates a name to use for the deployment object for a given environment. It appends the current
// unix time to the environment name (separated by a hyphen) to provide a unique name for each deployment. If the resulting
// name is longer than the ARM limit, the longest suffix of the name under the limit is returned.
func (d *deploymentStacks) GenerateDeploymentName(baseName string) string {
	return fmt.Sprintf("azd-stack-%s", baseName)
}

func (d *deploymentStacks) PortalUrl(deployment *ResourceDeployment) string {
	return ""
}

func (d *deploymentStacks) DeploymentUrl(deployment *ResourceDeployment) string {
	return ""
}

func (d *deploymentStacks) OutputsUrl(deployment *ResourceDeployment) string {
	return ""
}

func (d *deploymentStacks) ListSubscriptionDeployments(
	ctx context.Context,
	subscriptionId string,
) ([]*ResourceDeployment, error) {
	client, err := d.createClient(ctx, subscriptionId)
	if err != nil {
		return nil, err
	}

	results := []*ResourceDeployment{}

	pager := client.NewListAtSubscriptionPager(nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, err
		}

		for _, deployment := range page.Value {
			results = append(results, convertFromStackDeployment(deployment))
		}
	}

	return results, nil
}

func (d *deploymentStacks) GetSubscriptionDeployment(
	ctx context.Context,
	subscriptionId string,
	deploymentName string,
) (*ResourceDeployment, error) {
	client, err := d.createClient(ctx, subscriptionId)
	if err != nil {
		return nil, err
	}

	response, err := client.GetAtSubscription(ctx, deploymentName, nil)
	if err != nil {
		return nil, fmt.Errorf(
			"%w: '%s' in subscription '%s', Error: %w",
			ErrDeploymentNotFound,
			subscriptionId,
			deploymentName,
			err,
		)
	}

	return convertFromStackDeployment(&response.DeploymentStack), nil
}

func (d *deploymentStacks) ListResourceGroupDeployments(
	ctx context.Context,
	subscriptionId string,
	resourceGroupName string,
) ([]*ResourceDeployment, error) {
	client, err := d.createClient(ctx, subscriptionId)
	if err != nil {
		return nil, err
	}

	results := []*ResourceDeployment{}

	pager := client.NewListAtResourceGroupPager(resourceGroupName, nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, err
		}

		for _, deployment := range page.Value {
			results = append(results, convertFromStackDeployment(deployment))
		}
	}

	return results, nil
}

func (d *deploymentStacks) GetResourceGroupDeployment(
	ctx context.Context,
	subscriptionId string,
	resourceGroupName string,
	deploymentName string,
) (*ResourceDeployment, error) {
	client, err := d.createClient(ctx, subscriptionId)
	if err != nil {
		return nil, err
	}

	response, err := client.GetAtResourceGroup(ctx, resourceGroupName, deploymentName, nil)
	if err != nil {
		return nil, fmt.Errorf(
			"%w: '%s' in resource group '%s', Error: %w",
			ErrDeploymentNotFound,
			resourceGroupName,
			deploymentName,
			err,
		)
	}

	return convertFromStackDeployment(&response.DeploymentStack), err
}

func (d *deploymentStacks) DeployToSubscription(
	ctx context.Context,
	subscriptionId string,
	location string,
	deploymentName string,
	armTemplate azure.RawArmTemplate,
	parameters azure.ArmParameters,
	tags map[string]*string,
) (*ResourceDeployment, error) {
	client, err := d.createClient(ctx, subscriptionId)
	if err != nil {
		return nil, err
	}

	stackParams := map[string]*armdeploymentstacks.DeploymentParameter{}
	for k, v := range parameters {
		stackParams[k] = &armdeploymentstacks.DeploymentParameter{
			Value: v.Value,
		}
	}

	deleteBehavior := armdeploymentstacks.DeploymentStacksDeleteDetachEnumDelete

	stack := armdeploymentstacks.DeploymentStack{
		Location: &location,
		Tags:     tags,
		Properties: &armdeploymentstacks.DeploymentStackProperties{
			ActionOnUnmanage: &armdeploymentstacks.ActionOnUnmanage{
				Resources:        &deleteBehavior,
				ManagementGroups: &deleteBehavior,
				ResourceGroups:   &deleteBehavior,
			},
			DenySettings: &armdeploymentstacks.DenySettings{
				Mode: convert.RefOf(armdeploymentstacks.DenySettingsModeNone),
			},
			Parameters: stackParams,
			Template:   armTemplate,
		},
	}
	poller, err := client.BeginCreateOrUpdateAtSubscription(ctx, deploymentName, stack, nil)
	if err != nil {
		return nil, err
	}

	result, err := poller.PollUntilDone(ctx, nil)
	if err != nil {
		return nil, err
	}

	return convertFromStackDeployment(&result.DeploymentStack), nil
}

func (d *deploymentStacks) DeployToResourceGroup(
	ctx context.Context,
	subscriptionId string,
	resourceGroup string,
	deploymentName string,
	armTemplate azure.RawArmTemplate,
	parameters azure.ArmParameters,
	tags map[string]*string,
) (*ResourceDeployment, error) {
	client, err := d.createClient(ctx, subscriptionId)
	if err != nil {
		return nil, err
	}

	stackParams := map[string]*armdeploymentstacks.DeploymentParameter{}
	for k, v := range parameters {
		stackParams[k] = &armdeploymentstacks.DeploymentParameter{
			Value: v.Value,
		}
	}

	deleteBehavior := armdeploymentstacks.DeploymentStacksDeleteDetachEnumDelete

	stack := armdeploymentstacks.DeploymentStack{
		Tags: tags,
		Properties: &armdeploymentstacks.DeploymentStackProperties{
			ActionOnUnmanage: &armdeploymentstacks.ActionOnUnmanage{
				Resources:        &deleteBehavior,
				ManagementGroups: &deleteBehavior,
				ResourceGroups:   &deleteBehavior,
			},
			DenySettings: &armdeploymentstacks.DenySettings{
				Mode: convert.RefOf(armdeploymentstacks.DenySettingsModeNone),
			},
			Parameters: stackParams,
			Template:   armTemplate,
		},
	}
	poller, err := client.BeginCreateOrUpdateAtResourceGroup(ctx, resourceGroup, deploymentName, stack, nil)
	if err != nil {
		return nil, err
	}

	result, err := poller.PollUntilDone(ctx, nil)
	if err != nil {
		return nil, err
	}

	return convertFromStackDeployment(&result.DeploymentStack), nil
}

func (d *deploymentStacks) WhatIfDeployToSubscription(
	ctx context.Context,
	subscriptionId string,
	location string,
	deploymentName string,
	armTemplate azure.RawArmTemplate,
	parameters azure.ArmParameters,
) (*armresources.WhatIfOperationResult, error) {
	return d.standardDeployments.WhatIfDeployToSubscription(
		ctx,
		subscriptionId,
		location,
		deploymentName,
		armTemplate,
		parameters,
	)
}

func (d *deploymentStacks) WhatIfDeployToResourceGroup(
	ctx context.Context,
	subscriptionId string,
	resourceGroup string,
	deploymentName string,
	armTemplate azure.RawArmTemplate,
	parameters azure.ArmParameters,
) (*armresources.WhatIfOperationResult, error) {
	return d.standardDeployments.WhatIfDeployToResourceGroup(
		ctx,
		subscriptionId,
		resourceGroup,
		deploymentName,
		armTemplate,
		parameters,
	)
}

func (d *deploymentStacks) CalculateTemplateHash(
	ctx context.Context,
	subscriptionId string,
	template azure.RawArmTemplate,
) (armresources.DeploymentsClientCalculateTemplateHashResponse, error) {
	return d.standardDeployments.CalculateTemplateHash(ctx, subscriptionId, template)
}

func (d *deploymentStacks) createClient(ctx context.Context, subscriptionId string) (*armdeploymentstacks.Client, error) {
	credential, err := d.credentialProvider.CredentialForSubscription(ctx, subscriptionId)
	if err != nil {
		return nil, err
	}

	return armdeploymentstacks.NewClient(subscriptionId, credential, d.armClientOptions)
}

// Converts from an ARM Extended Deployment to Azd Generic deployment
func convertFromStackDeployment(deployment *armdeploymentstacks.DeploymentStack) *ResourceDeployment {
	resources := []*armresources.ResourceReference{}
	for _, resource := range deployment.Properties.Resources {
		resources = append(resources, &armresources.ResourceReference{ID: resource.ID})
	}

	return &ResourceDeployment{
		Id:                *deployment.ID,
		DeploymentId:      convert.ToStringWithDefault(deployment.Properties.DeploymentID, ""),
		Name:              *deployment.Name,
		Type:              *deployment.Type,
		Tags:              deployment.Tags,
		ProvisioningState: DeploymentProvisioningState(*deployment.Properties.ProvisioningState),
		Timestamp:         *deployment.SystemData.LastModifiedAt,
		TemplateHash:      nil,
		Outputs:           deployment.Properties.Outputs,
		Resources:         resources,
		Dependencies:      []*armresources.Dependency{},
	}
}
