package azapi

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armdeploymentstacks"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"github.com/azure/azure-dev/cli/azd/pkg/account"
	"github.com/azure/azure-dev/cli/azd/pkg/alpha"
	"github.com/azure/azure-dev/cli/azd/pkg/async"
	"github.com/azure/azure-dev/cli/azd/pkg/azure"
	"github.com/azure/azure-dev/cli/azd/pkg/cloud"
	"github.com/azure/azure-dev/cli/azd/pkg/convert"
	"github.com/azure/azure-dev/cli/azd/pkg/ioc"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/benbjohnson/clock"
	"github.com/sethvargo/go-retry"
)

var FeatureDeploymentStacks = alpha.MustFeatureKey("deployment.stacks")

const (
	cStacksPortalUrlFragment = "#@microsoft.onmicrosoft.com/resource"
)

type deploymentStacks struct {
	serviceLocator      ioc.ServiceLocator
	credentialProvider  account.SubscriptionCredentialProvider
	armClientOptions    *arm.ClientOptions
	standardDeployments DeploymentService
	cloud               *cloud.Cloud
}

func NewDeploymentStacks(
	serviceLocator ioc.ServiceLocator,
	credentialProvider account.SubscriptionCredentialProvider,
	armClientOptions *arm.ClientOptions,
	cloud *cloud.Cloud,
	clock clock.Clock,
) DeploymentService {
	return &deploymentStacks{
		serviceLocator:     serviceLocator,
		credentialProvider: credentialProvider,
		armClientOptions:   armClientOptions,
		cloud:              cloud,
	}
}

func (d *deploymentStacks) init(ctx context.Context) error {
	if d.standardDeployments == nil {
		err := d.serviceLocator.ResolveNamed(string(DeploymentTypeStandard), &d.standardDeployments)
		if err != nil {
			return err
		}
	}

	return nil
}

// GenerateDeploymentName creates a name to use for the deployment object for a given environment. It appends the current
// unix time to the environment name (separated by a hyphen) to provide a unique name for each deployment. If the resulting
// name is longer than the ARM limit, the longest suffix of the name under the limit is returned.
func (d *deploymentStacks) GenerateDeploymentName(baseName string) string {
	return fmt.Sprintf("azd-stack-%s", baseName)
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
			results = append(results, d.convertFromStackDeployment(deployment))
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

	var deploymentStack *armdeploymentstacks.DeploymentStack

	err = retry.Do(
		ctx,
		retry.WithMaxDuration(10*time.Minute, retry.NewConstant(5*time.Second)),
		func(ctx context.Context) error {
			response, err := client.GetAtSubscription(ctx, deploymentName, nil)
			if err != nil {
				return fmt.Errorf(
					"%w: '%s' in subscription '%s', Error: %w",
					ErrDeploymentNotFound,
					subscriptionId,
					deploymentName,
					err,
				)
			}

			if response.DeploymentStack.Properties.DeploymentID == nil {
				return retry.RetryableError(errors.New("deployment stack is missing ARM deployment id"))
			}

			deploymentStack = &response.DeploymentStack

			return nil
		})

	if err != nil {
		return nil, err
	}

	return d.convertFromStackDeployment(deploymentStack), nil
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
			results = append(results, d.convertFromStackDeployment(deployment))
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

	var deploymentStack *armdeploymentstacks.DeploymentStack

	err = retry.Do(
		ctx,
		retry.WithMaxDuration(10*time.Minute, retry.NewConstant(5*time.Second)),
		func(ctx context.Context) error {
			response, err := client.GetAtResourceGroup(ctx, resourceGroupName, deploymentName, nil)
			if err != nil {
				return fmt.Errorf(
					"%w: '%s' in resource group '%s', Error: %w",
					ErrDeploymentNotFound,
					resourceGroupName,
					deploymentName,
					err,
				)
			}

			if response.DeploymentStack.Properties.DeploymentID == nil {
				return retry.RetryableError(errors.New("deployment stack is missing ARM deployment id"))
			}

			deploymentStack = &response.DeploymentStack

			return nil
		})

	if err != nil {
		return nil, err
	}

	return d.convertFromStackDeployment(deploymentStack), nil
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

	_, err = poller.PollUntilDone(ctx, nil)
	if err != nil {
		return nil, err
	}

	return d.GetSubscriptionDeployment(ctx, subscriptionId, deploymentName)
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

	_, err = poller.PollUntilDone(ctx, nil)
	if err != nil {
		return nil, err
	}

	return d.GetResourceGroupDeployment(ctx, subscriptionId, resourceGroup, deploymentName)
}

func (d *deploymentStacks) ListSubscriptionDeploymentOperations(
	ctx context.Context,
	subscriptionId string,
	deploymentName string,
) ([]*armresources.DeploymentOperation, error) {
	if err := d.init(ctx); err != nil {
		return nil, err
	}

	deployment, err := d.GetSubscriptionDeployment(ctx, subscriptionId, deploymentName)
	if err != nil {
		return nil, err
	}

	deploymentName = filepath.Base(deployment.DeploymentId)

	return d.standardDeployments.ListSubscriptionDeploymentOperations(ctx, subscriptionId, deploymentName)
}

func (d *deploymentStacks) ListResourceGroupDeploymentOperations(
	ctx context.Context,
	subscriptionId string,
	resourceGroupName string,
	deploymentName string,
) ([]*armresources.DeploymentOperation, error) {
	if err := d.init(ctx); err != nil {
		return nil, err
	}

	// The requested deployment name may be an inner deployment which will not be found in the deployment stacks.
	// If this is the case continue on checking if there is a stack deployment
	// If a deployment stack is found then use the deployment id of the stack
	deployment, err := d.GetResourceGroupDeployment(ctx, subscriptionId, resourceGroupName, deploymentName)
	if !errors.Is(err, ErrDeploymentNotFound) {
		return nil, err
	}

	if deployment != nil && deployment.DeploymentId != "" {
		deploymentName = filepath.Base(deployment.DeploymentId)
	}

	return d.standardDeployments.ListResourceGroupDeploymentOperations(
		ctx,
		subscriptionId,
		resourceGroupName,
		deploymentName,
	)
}

func (d *deploymentStacks) WhatIfDeployToSubscription(
	ctx context.Context,
	subscriptionId string,
	location string,
	deploymentName string,
	armTemplate azure.RawArmTemplate,
	parameters azure.ArmParameters,
) (*armresources.WhatIfOperationResult, error) {
	if err := d.init(ctx); err != nil {
		return nil, err
	}

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
	if err := d.init(ctx); err != nil {
		return nil, err
	}

	return d.standardDeployments.WhatIfDeployToResourceGroup(
		ctx,
		subscriptionId,
		resourceGroup,
		deploymentName,
		armTemplate,
		parameters,
	)
}

func (d *deploymentStacks) ListSubscriptionDeploymentResources(
	ctx context.Context,
	subscriptionId string,
	deploymentName string,
) ([]*armresources.ResourceReference, error) {
	deployment, err := d.GetSubscriptionDeployment(ctx, subscriptionId, deploymentName)
	if err != nil {
		return nil, err
	}

	return deployment.Resources, nil
}
func (d *deploymentStacks) ListResourceGroupDeploymentResources(
	ctx context.Context,
	subscriptionId string,
	resourceGroupName string,
	deploymentName string,
) ([]*armresources.ResourceReference, error) {
	deployment, err := d.GetResourceGroupDeployment(ctx, subscriptionId, resourceGroupName, deploymentName)
	if err != nil {
		return nil, err
	}

	return deployment.Resources, nil
}

func (d *deploymentStacks) DeleteSubscriptionDeployment(
	ctx context.Context,
	subscriptionId string,
	deploymentName string,
	progress *async.Progress[DeleteDeploymentProgress],
) error {
	client, err := d.createClient(ctx, subscriptionId)
	if err != nil {
		return err
	}

	// Delete all resource groups & resources within the deployment stack
	options := armdeploymentstacks.ClientBeginDeleteAtSubscriptionOptions{
		UnmanageActionManagementGroups: convert.RefOf(armdeploymentstacks.UnmanageActionManagementGroupModeDelete),
		UnmanageActionResourceGroups:   convert.RefOf(armdeploymentstacks.UnmanageActionResourceGroupModeDelete),
		UnmanageActionResources:        convert.RefOf(armdeploymentstacks.UnmanageActionResourceModeDelete),
	}

	progress.SetProgress(DeleteDeploymentProgress{
		Name:    deploymentName,
		Message: fmt.Sprintf("Deleting subscription deployment stack %s", output.WithHighLightFormat(deploymentName)),
		State:   DeleteResourceStateInProgress,
	})

	poller, err := client.BeginDeleteAtSubscription(ctx, deploymentName, &options)
	if err != nil {
		progress.SetProgress(DeleteDeploymentProgress{
			Name: deploymentName,
			Message: fmt.Sprintf(
				"Failed deleting subscription deployment stack %s",
				output.WithHighLightFormat(deploymentName),
			),
			State: DeleteResourceStateFailed,
		})

		return err
	}

	_, err = poller.PollUntilDone(ctx, nil)
	if err != nil {
		progress.SetProgress(DeleteDeploymentProgress{
			Name: deploymentName,
			Message: fmt.Sprintf(
				"Failed deleting subscription deployment stack %s",
				output.WithHighLightFormat(deploymentName),
			),
			State: DeleteResourceStateFailed,
		})

		return err
	}

	progress.SetProgress(DeleteDeploymentProgress{
		Name:    deploymentName,
		Message: fmt.Sprintf("Deleted subscription deployment stack %s", output.WithHighLightFormat(deploymentName)),
		State:   DeleteResourceStateSucceeded,
	})

	return nil
}

func (d *deploymentStacks) DeleteResourceGroupDeployment(
	ctx context.Context,
	subscriptionId,
	resourceGroupName string,
	deploymentName string,
	progress *async.Progress[DeleteDeploymentProgress],
) error {
	client, err := d.createClient(ctx, subscriptionId)
	if err != nil {
		return err
	}

	poller, err := client.BeginDeleteAtResourceGroup(ctx, resourceGroupName, deploymentName, nil)
	if err != nil {
		return err
	}

	_, err = poller.PollUntilDone(ctx, nil)
	if err != nil {
		return err
	}

	return nil
}

func (d *deploymentStacks) CalculateTemplateHash(
	ctx context.Context,
	subscriptionId string,
	template azure.RawArmTemplate,
) (string, error) {
	if err := d.init(ctx); err != nil {
		return "", err
	}

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
func (d *deploymentStacks) convertFromStackDeployment(deployment *armdeploymentstacks.DeploymentStack) *ResourceDeployment {
	resources := []*armresources.ResourceReference{}
	for _, resource := range deployment.Properties.Resources {
		resources = append(resources, &armresources.ResourceReference{ID: resource.ID})
	}

	// When the deployment stack is initially created it may have not been have a deployment id set
	deploymentId := ""
	if deployment.Properties.DeploymentID != nil {
		deploymentId = *deployment.Properties.DeploymentID
	}

	return &ResourceDeployment{
		Id:                *deployment.ID,
		DeploymentId:      deploymentId,
		Name:              *deployment.Name,
		Type:              *deployment.Type,
		Tags:              deployment.Tags,
		ProvisioningState: DeploymentProvisioningState(*deployment.Properties.ProvisioningState),
		Timestamp:         *deployment.SystemData.LastModifiedAt,
		TemplateHash:      nil,
		Outputs:           deployment.Properties.Outputs,
		Resources:         resources,
		Dependencies:      []*armresources.Dependency{},

		PortalUrl: fmt.Sprintf("%s/%s/%s",
			d.cloud.PortalUrlBase,
			cStacksPortalUrlFragment,
			*deployment.ID,
		),

		OutputsUrl: fmt.Sprintf("%s/%s/%s/outputs",
			d.cloud.PortalUrlBase,
			cStacksPortalUrlFragment,
			*deployment.ID,
		),

		DeploymentUrl: fmt.Sprintf("%s/%s/%s",
			d.cloud.PortalUrlBase,
			cPortalUrlFragment,
			url.PathEscape(deploymentId),
		),
	}
}
