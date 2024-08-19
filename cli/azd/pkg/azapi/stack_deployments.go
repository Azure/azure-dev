package azapi

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armdeploymentstacks"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"github.com/azure/azure-dev/cli/azd/pkg/account"
	"github.com/azure/azure-dev/cli/azd/pkg/alpha"
	"github.com/azure/azure-dev/cli/azd/pkg/async"
	"github.com/azure/azure-dev/cli/azd/pkg/azure"
	"github.com/azure/azure-dev/cli/azd/pkg/cloud"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/benbjohnson/clock"
	"github.com/sethvargo/go-retry"
)

var FeatureDeploymentStacks = alpha.MustFeatureKey("deployment.stacks")

const (
	cStacksPortalUrlFragment = "#@microsoft.onmicrosoft.com/resource"
)

type StackDeployments struct {
	credentialProvider  account.SubscriptionCredentialProvider
	armClientOptions    *arm.ClientOptions
	standardDeployments *StandardDeployments
	cloud               *cloud.Cloud
}

func NewStackDeployments(
	credentialProvider account.SubscriptionCredentialProvider,
	armClientOptions *arm.ClientOptions,
	standardDeployments *StandardDeployments,
	cloud *cloud.Cloud,
	clock clock.Clock,
) *StackDeployments {
	return &StackDeployments{
		credentialProvider:  credentialProvider,
		armClientOptions:    armClientOptions,
		standardDeployments: standardDeployments,
		cloud:               cloud,
	}
}

// GenerateDeploymentName creates a name to use for the deployment object for a given environment. It appends the current
// unix time to the environment name (separated by a hyphen) to provide a unique name for each deployment. If the resulting
// name is longer than the ARM limit, the longest suffix of the name under the limit is returned.
func (d *StackDeployments) GenerateDeploymentName(baseName string) string {
	return fmt.Sprintf("azd-stack-%s", baseName)
}

func (d *StackDeployments) ListSubscriptionDeployments(
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

func (d *StackDeployments) GetSubscriptionDeployment(
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
		// If a deployment stack is not found with the given name, fallback to check for standard deployments
		if errors.Is(err, ErrDeploymentNotFound) {
			return d.standardDeployments.GetSubscriptionDeployment(ctx, subscriptionId, deploymentName)
		}

		return nil, err
	}

	return d.convertFromStackDeployment(deploymentStack), nil
}

func (d *StackDeployments) ListResourceGroupDeployments(
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

func (d *StackDeployments) GetResourceGroupDeployment(
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
		// If a deployment stack is not found with the given name, fallback to check for standard deployments
		if errors.Is(err, ErrDeploymentNotFound) {
			return d.standardDeployments.GetResourceGroupDeployment(ctx, subscriptionId, resourceGroupName, deploymentName)
		}

		return nil, err
	}

	return d.convertFromStackDeployment(deploymentStack), nil
}

func (d *StackDeployments) DeployToSubscription(
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

	templateHash, err := d.CalculateTemplateHash(ctx, subscriptionId, armTemplate)
	if err != nil {
		return nil, fmt.Errorf("failed to calculate template hash: %w", err)
	}

	tags[azure.TagKeyAzdDeploymentTemplateHashName] = &templateHash

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
				Mode: to.Ptr(armdeploymentstacks.DenySettingsModeNone),
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

func (d *StackDeployments) DeployToResourceGroup(
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
				Mode: to.Ptr(armdeploymentstacks.DenySettingsModeNone),
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

func (d *StackDeployments) ListSubscriptionDeploymentOperations(
	ctx context.Context,
	subscriptionId string,
	deploymentName string,
) ([]*armresources.DeploymentOperation, error) {
	deployment, err := d.GetSubscriptionDeployment(ctx, subscriptionId, deploymentName)
	if err != nil && !errors.Is(err, ErrDeploymentNotFound) {
		return nil, err
	}

	if deployment != nil && deployment.DeploymentId != "" {
		deploymentName = filepath.Base(deployment.DeploymentId)
	}

	return d.standardDeployments.ListSubscriptionDeploymentOperations(ctx, subscriptionId, deploymentName)
}

func (d *StackDeployments) ListResourceGroupDeploymentOperations(
	ctx context.Context,
	subscriptionId string,
	resourceGroupName string,
	deploymentName string,
) ([]*armresources.DeploymentOperation, error) {
	// The requested deployment name may be an inner deployment which will not be found in the deployment stacks.
	// If this is the case continue on checking if there is a stack deployment
	// If a deployment stack is found then use the deployment id of the stack
	deployment, err := d.GetResourceGroupDeployment(ctx, subscriptionId, resourceGroupName, deploymentName)
	if err != nil && !errors.Is(err, ErrDeploymentNotFound) {
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

func (d *StackDeployments) WhatIfDeployToSubscription(
	ctx context.Context,
	subscriptionId string,
	location string,
	deploymentName string,
	armTemplate azure.RawArmTemplate,
	parameters azure.ArmParameters,
) (*armresources.WhatIfOperationResult, error) {
	deployment, err := d.GetSubscriptionDeployment(ctx, subscriptionId, deploymentName)
	if err != nil && !errors.Is(err, ErrDeploymentNotFound) {
		return nil, err
	}

	if deployment != nil && deployment.DeploymentId != "" {
		deploymentName = filepath.Base(deployment.DeploymentId)
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

func (d *StackDeployments) WhatIfDeployToResourceGroup(
	ctx context.Context,
	subscriptionId string,
	resourceGroup string,
	deploymentName string,
	armTemplate azure.RawArmTemplate,
	parameters azure.ArmParameters,
) (*armresources.WhatIfOperationResult, error) {
	deployment, err := d.GetResourceGroupDeployment(ctx, subscriptionId, resourceGroup, deploymentName)
	if err != nil && !errors.Is(err, ErrDeploymentNotFound) {
		return nil, err
	}

	if deployment != nil && deployment.DeploymentId != "" {
		deploymentName = filepath.Base(deployment.DeploymentId)
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

func (d *StackDeployments) ListSubscriptionDeploymentResources(
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
func (d *StackDeployments) ListResourceGroupDeploymentResources(
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

func (d *StackDeployments) DeleteSubscriptionDeployment(
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
		UnmanageActionManagementGroups: to.Ptr(armdeploymentstacks.UnmanageActionManagementGroupModeDelete),
		UnmanageActionResourceGroups:   to.Ptr(armdeploymentstacks.UnmanageActionResourceGroupModeDelete),
		UnmanageActionResources:        to.Ptr(armdeploymentstacks.UnmanageActionResourceModeDelete),
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

func (d *StackDeployments) DeleteResourceGroupDeployment(
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

	// Delete all resource groups & resources within the deployment stack
	options := armdeploymentstacks.ClientBeginDeleteAtResourceGroupOptions{
		UnmanageActionManagementGroups: to.Ptr(armdeploymentstacks.UnmanageActionManagementGroupModeDelete),
		UnmanageActionResourceGroups:   to.Ptr(armdeploymentstacks.UnmanageActionResourceGroupModeDelete),
		UnmanageActionResources:        to.Ptr(armdeploymentstacks.UnmanageActionResourceModeDelete),
	}

	progress.SetProgress(DeleteDeploymentProgress{
		Name:    deploymentName,
		Message: fmt.Sprintf("Deleting resource group deployment stack %s", output.WithHighLightFormat(deploymentName)),
		State:   DeleteResourceStateInProgress,
	})

	poller, err := client.BeginDeleteAtResourceGroup(ctx, resourceGroupName, deploymentName, &options)
	if err != nil {
		progress.SetProgress(DeleteDeploymentProgress{
			Name: deploymentName,
			Message: fmt.Sprintf(
				"Failed deleting resource group deployment stack %s",
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
				"Failed deleting resource group deployment stack %s",
				output.WithHighLightFormat(deploymentName),
			),
			State: DeleteResourceStateFailed,
		})

		return err
	}

	progress.SetProgress(DeleteDeploymentProgress{
		Name:    deploymentName,
		Message: fmt.Sprintf("Deleted resource group deployment stack %s", output.WithHighLightFormat(deploymentName)),
		State:   DeleteResourceStateSucceeded,
	})

	return nil
}

func (d *StackDeployments) CalculateTemplateHash(
	ctx context.Context,
	subscriptionId string,
	template azure.RawArmTemplate,
) (string, error) {
	return d.standardDeployments.CalculateTemplateHash(ctx, subscriptionId, template)
}

func (d *StackDeployments) createClient(ctx context.Context, subscriptionId string) (*armdeploymentstacks.Client, error) {
	credential, err := d.credentialProvider.CredentialForSubscription(ctx, subscriptionId)
	if err != nil {
		return nil, err
	}

	return armdeploymentstacks.NewClient(subscriptionId, credential, d.armClientOptions)
}

// Converts from an ARM Extended Deployment to Azd Generic deployment
func (d *StackDeployments) convertFromStackDeployment(deployment *armdeploymentstacks.DeploymentStack) *ResourceDeployment {
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
		ProvisioningState: convertFromStacksProvisioningState(*deployment.Properties.ProvisioningState),
		Timestamp:         *deployment.SystemData.LastModifiedAt,
		TemplateHash:      deployment.Tags[azure.TagKeyAzdDeploymentTemplateHashName],
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

func convertFromStacksProvisioningState(
	state armdeploymentstacks.DeploymentStackProvisioningState,
) DeploymentProvisioningState {
	switch state {
	case armdeploymentstacks.DeploymentStackProvisioningStateCanceled:
		return DeploymentProvisioningStateCanceled
	case armdeploymentstacks.DeploymentStackProvisioningStateCanceling:
		return DeploymentProvisioningStateCanceling
	case armdeploymentstacks.DeploymentStackProvisioningStateCreating:
		return DeploymentProvisioningStateCreating
	case armdeploymentstacks.DeploymentStackProvisioningStateDeleting:
		return DeploymentProvisioningStateDeleting
	case armdeploymentstacks.DeploymentStackProvisioningStateDeletingResources:
		return DeploymentProvisioningStateDeletingResources
	case armdeploymentstacks.DeploymentStackProvisioningStateDeploying:
		return DeploymentProvisioningStateDeploying
	case armdeploymentstacks.DeploymentStackProvisioningStateFailed:
		return DeploymentProvisioningStateFailed
	case armdeploymentstacks.DeploymentStackProvisioningStateSucceeded:
		return DeploymentProvisioningStateSucceeded
	case armdeploymentstacks.DeploymentStackProvisioningStateUpdatingDenyAssignments:
		return DeploymentProvisioningStateUpdatingDenyAssignments
	case armdeploymentstacks.DeploymentStackProvisioningStateValidating:
		return DeploymentProvisioningStateValidating
	case armdeploymentstacks.DeploymentStackProvisioningStateWaiting:
		return DeploymentProvisioningStateWaiting
	}

	return DeploymentProvisioningState("")
}
