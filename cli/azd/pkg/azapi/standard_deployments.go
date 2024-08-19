package azapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"github.com/azure/azure-dev/cli/azd/pkg/account"
	"github.com/azure/azure-dev/cli/azd/pkg/async"
	"github.com/azure/azure-dev/cli/azd/pkg/azure"
	"github.com/azure/azure-dev/cli/azd/pkg/cloud"
	"github.com/azure/azure-dev/cli/azd/pkg/convert"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/benbjohnson/clock"
	"golang.org/x/exp/maps"
)

// cArmDeploymentNameLengthMax is the maximum length of the name of a deployment in ARM.
const (
	cArmDeploymentNameLengthMax = 64
	cPortalUrlFragment          = "#view/HubsExtension/DeploymentDetailsBlade/~/overview/id"
	cOutputsUrlFragment         = "#view/HubsExtension/DeploymentDetailsBlade/~/outputs/id"
)

type StandardDeployments struct {
	credentialProvider account.SubscriptionCredentialProvider
	armClientOptions   *arm.ClientOptions
	resourceService    *ResourceService
	cloud              *cloud.Cloud
	clock              clock.Clock
}

func NewStandardDeployments(
	credentialProvider account.SubscriptionCredentialProvider,
	armClientOptions *arm.ClientOptions,
	resourceService *ResourceService,
	cloud *cloud.Cloud,
	clock clock.Clock,
) *StandardDeployments {
	return &StandardDeployments{
		credentialProvider: credentialProvider,
		armClientOptions:   armClientOptions,
		resourceService:    resourceService,
		cloud:              cloud,
		clock:              clock,
	}
}

// GenerateDeploymentName creates a name to use for the deployment object for a given environment. It appends the current
// unix time to the environment name (separated by a hyphen) to provide a unique name for each deployment. If the resulting
// name is longer than the ARM limit, the longest suffix of the name under the limit is returned.
func (ds *StandardDeployments) GenerateDeploymentName(baseName string) string {
	name := fmt.Sprintf("%s-%d", baseName, ds.clock.Now().Unix())
	if len(name) <= cArmDeploymentNameLengthMax {
		return name
	}

	return name[len(name)-cArmDeploymentNameLengthMax:]
}

func (ds *StandardDeployments) CalculateTemplateHash(
	ctx context.Context,
	subscriptionId string,
	template azure.RawArmTemplate,
) (string, error) {
	deploymentClient, err := ds.createDeploymentsClient(ctx, subscriptionId)
	if err != nil {
		return "", fmt.Errorf("creating deployments client: %w", err)
	}

	response, err := deploymentClient.CalculateTemplateHash(ctx, template, nil)
	if err != nil {
		return "", fmt.Errorf("calculating template hash: %w", err)
	}

	return *response.TemplateHashResult.TemplateHash, nil
}

func (ds *StandardDeployments) ListSubscriptionDeployments(
	ctx context.Context,
	subscriptionId string,
) ([]*ResourceDeployment, error) {
	deploymentClient, err := ds.createDeploymentsClient(ctx, subscriptionId)
	if err != nil {
		return nil, fmt.Errorf("creating deployments client: %w", err)
	}

	results := []*ResourceDeployment{}

	pager := deploymentClient.NewListAtSubscriptionScopePager(nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, err
		}

		for _, deployment := range page.Value {
			results = append(results, ds.convertFromArmDeployment(deployment))
		}
	}

	return results, nil
}

func (ds *StandardDeployments) GetSubscriptionDeployment(
	ctx context.Context,
	subscriptionId string,
	deploymentName string,
) (*ResourceDeployment, error) {
	deploymentClient, err := ds.createDeploymentsClient(ctx, subscriptionId)
	if err != nil {
		return nil, fmt.Errorf("creating deployments client: %w", err)
	}

	deployment, err := deploymentClient.GetAtSubscriptionScope(ctx, deploymentName, nil)
	if err != nil {
		var errDetails *azcore.ResponseError
		if errors.As(err, &errDetails) && errDetails.StatusCode == 404 {
			return nil, ErrDeploymentNotFound
		}
		return nil, fmt.Errorf("getting deployment from subscription: %w", err)
	}

	return ds.convertFromArmDeployment(&deployment.DeploymentExtended), nil
}

func (ds *StandardDeployments) ListResourceGroupDeployments(
	ctx context.Context,
	subscriptionId string,
	resourceGroupName string,
) ([]*ResourceDeployment, error) {
	deploymentClient, err := ds.createDeploymentsClient(ctx, subscriptionId)
	if err != nil {
		return nil, fmt.Errorf("creating deployments client: %w", err)
	}

	results := []*ResourceDeployment{}

	pager := deploymentClient.NewListByResourceGroupPager(resourceGroupName, nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, err
		}

		for _, deployment := range page.Value {
			results = append(results, ds.convertFromArmDeployment(deployment))
		}
	}

	return results, nil
}

func (ds *StandardDeployments) GetResourceGroupDeployment(
	ctx context.Context,
	subscriptionId string,
	resourceGroupName string,
	deploymentName string,
) (*ResourceDeployment, error) {
	deploymentClient, err := ds.createDeploymentsClient(ctx, subscriptionId)
	if err != nil {
		return nil, fmt.Errorf("creating deployments client: %w", err)
	}

	deployment, err := deploymentClient.Get(ctx, resourceGroupName, deploymentName, nil)
	if err != nil {
		var errDetails *azcore.ResponseError
		if errors.As(err, &errDetails) && errDetails.StatusCode == 404 {
			return nil, ErrDeploymentNotFound
		}
		return nil, fmt.Errorf("getting deployment from resource group: %w", err)
	}

	return ds.convertFromArmDeployment(&deployment.DeploymentExtended), nil
}

func (ds *StandardDeployments) createDeploymentsClient(
	ctx context.Context,
	subscriptionId string,
) (*armresources.DeploymentsClient, error) {
	credential, err := ds.credentialProvider.CredentialForSubscription(ctx, subscriptionId)
	if err != nil {
		return nil, err
	}

	client, err := armresources.NewDeploymentsClient(subscriptionId, credential, ds.armClientOptions)
	if err != nil {
		return nil, fmt.Errorf("creating deployments client: %w", err)
	}

	return client, nil
}

func (ds *StandardDeployments) DeployToSubscription(
	ctx context.Context,
	subscriptionId string,
	location string,
	deploymentName string,
	armTemplate azure.RawArmTemplate,
	parameters azure.ArmParameters,
	tags map[string]*string,
) (*ResourceDeployment, error) {
	deploymentClient, err := ds.createDeploymentsClient(ctx, subscriptionId)
	if err != nil {
		return nil, fmt.Errorf("creating deployments client: %w", err)
	}

	createFromTemplateOperation, err := deploymentClient.BeginCreateOrUpdateAtSubscriptionScope(
		ctx, deploymentName,
		armresources.Deployment{
			Properties: &armresources.DeploymentProperties{
				Template:   armTemplate,
				Parameters: parameters,
				Mode:       to.Ptr(armresources.DeploymentModeIncremental),
			},
			Location: to.Ptr(location),
			Tags:     tags,
		}, nil)
	if err != nil {
		return nil, fmt.Errorf("starting deployment to subscription: %w", err)
	}

	// wait for deployment creation
	deployResult, err := createFromTemplateOperation.PollUntilDone(ctx, nil)
	if err != nil {
		deploymentError := createDeploymentError(err)
		return nil, fmt.Errorf(
			"deploying to subscription:\n\nDeployment Error Details:\n%w",
			deploymentError,
		)
	}

	return ds.convertFromArmDeployment(&deployResult.DeploymentExtended), nil
}

func (ds *StandardDeployments) DeployToResourceGroup(
	ctx context.Context,
	subscriptionId, resourceGroup, deploymentName string,
	armTemplate azure.RawArmTemplate,
	parameters azure.ArmParameters,
	tags map[string]*string,
) (*ResourceDeployment, error) {
	deploymentClient, err := ds.createDeploymentsClient(ctx, subscriptionId)
	if err != nil {
		return nil, fmt.Errorf("creating deployments client: %w", err)
	}

	createFromTemplateOperation, err := deploymentClient.BeginCreateOrUpdate(
		ctx, resourceGroup, deploymentName,
		armresources.Deployment{
			Properties: &armresources.DeploymentProperties{
				Template:   armTemplate,
				Parameters: parameters,
				Mode:       to.Ptr(armresources.DeploymentModeIncremental),
			},
			Tags: tags,
		}, nil)
	if err != nil {
		return nil, fmt.Errorf("starting deployment to resource group: %w", err)
	}

	// wait for deployment creation
	deployResult, err := createFromTemplateOperation.PollUntilDone(ctx, nil)
	if err != nil {
		deploymentError := createDeploymentError(err)
		return nil, fmt.Errorf(
			"deploying to resource group:\n\nDeployment Error Details:\n%w",
			deploymentError,
		)
	}

	return ds.convertFromArmDeployment(&deployResult.DeploymentExtended), nil
}

func (ds *StandardDeployments) ListSubscriptionDeploymentOperations(
	ctx context.Context,
	subscriptionId string,
	deploymentName string,
) ([]*armresources.DeploymentOperation, error) {
	result := []*armresources.DeploymentOperation{}
	deploymentOperationsClient, err := ds.createDeploymentsOperationsClient(ctx, subscriptionId)
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
			return nil, fmt.Errorf("failed getting list of deployment operations from subscription: %w", err)
		}
		result = append(result, page.Value...)
	}

	return result, nil
}

func (ds *StandardDeployments) ListResourceGroupDeploymentOperations(
	ctx context.Context,
	subscriptionId string,
	resourceGroupName string,
	deploymentName string,
) ([]*armresources.DeploymentOperation, error) {
	result := []*armresources.DeploymentOperation{}
	deploymentOperationsClient, err := ds.createDeploymentsOperationsClient(ctx, subscriptionId)
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

func (ds *StandardDeployments) ListSubscriptionDeploymentResources(
	ctx context.Context,
	subscriptionId string,
	deploymentName string,
) ([]*armresources.ResourceReference, error) {
	client, err := ds.createDeploymentsClient(ctx, subscriptionId)
	if err != nil {
		return nil, fmt.Errorf("creating deployments client: %w", err)
	}

	response, err := client.GetAtSubscriptionScope(ctx, deploymentName, nil)
	if err != nil {
		return nil, fmt.Errorf("getting deployment from subscription: %w", err)
	}

	return ds.getResourcesFromDeployment(ctx, &response.DeploymentExtended, subscriptionId)
}

func (ds *StandardDeployments) ListResourceGroupDeploymentResources(
	ctx context.Context,
	subscriptionId string,
	resourceGroupName string,
	deploymentName string,
) ([]*armresources.ResourceReference, error) {
	client, err := ds.createDeploymentsClient(ctx, subscriptionId)
	if err != nil {
		return nil, fmt.Errorf("creating deployments client: %w", err)
	}

	response, err := client.Get(ctx, resourceGroupName, deploymentName, nil)
	if err != nil {
		return nil, fmt.Errorf("getting deployment from subscription: %w", err)
	}

	return ds.getResourcesFromDeployment(ctx, &response.DeploymentExtended, subscriptionId)
}

func (ds *StandardDeployments) DeleteSubscriptionDeployment(
	ctx context.Context,
	subscriptionId string,
	deploymentName string,
	progress *async.Progress[DeleteDeploymentProgress],
) error {
	resources, err := ds.ListSubscriptionDeploymentResources(ctx, subscriptionId, deploymentName)
	if err != nil {
		return err
	}

	resourceGroups := map[string]struct{}{}
	for _, resource := range resources {
		resourceId, err := arm.ParseResourceID(*resource.ID)
		if err != nil {
			return fmt.Errorf("parsing resource ID: %w", err)
		}

		resourceGroups[resourceId.ResourceGroupName] = struct{}{}
	}

	for resourceGroup := range resourceGroups {
		progress.SetProgress(DeleteDeploymentProgress{
			Name:    resourceGroup,
			Message: fmt.Sprintf("Deleting resource group %s", output.WithHighLightFormat(resourceGroup)),
			State:   DeleteResourceStateInProgress,
		})

		if err := ds.resourceService.DeleteResourceGroup(ctx, subscriptionId, resourceGroup); err != nil {
			progress.SetProgress(DeleteDeploymentProgress{
				Name:    resourceGroup,
				Message: fmt.Sprintf("Failed deleting resource group %s", output.WithHighLightFormat(resourceGroup)),
				State:   DeleteResourceStateFailed,
			})

			return err
		}

		progress.SetProgress(DeleteDeploymentProgress{
			Name:    resourceGroup,
			Message: fmt.Sprintf("Deleted resource group %s", output.WithHighLightFormat(resourceGroup)),
			State:   DeleteResourceStateSucceeded,
		})
	}

	// Deploy empty template to void provision state and keep deployment history instead of deleting previous deployments
	// Get deployment metadata
	deployment, err := ds.GetSubscriptionDeployment(ctx, subscriptionId, deploymentName)
	if err != nil {
		return fmt.Errorf("subscription deployment '%s' not found: %w", deploymentName, err)
	}

	envName, has := deployment.Tags[azure.TagKeyAzdEnvName]
	if has {
		var emptyTemplate json.RawMessage = []byte(emptySubscriptionArmTemplate)
		emptyDeploymentName := ds.GenerateDeploymentName(*envName)
		tags := map[string]*string{
			azure.TagKeyAzdEnvName: envName,
			"azd-deploy-reason":    to.Ptr("down"),
		}

		_, err = ds.DeployToSubscription(
			ctx,
			subscriptionId,
			deployment.Location,
			emptyDeploymentName,
			emptyTemplate,
			azure.ArmParameters{},
			tags,
		)

		if err != nil {
			return fmt.Errorf("deploying empty template to subscription: %w", err)
		}
	}

	return nil
}

func (ds *StandardDeployments) DeleteResourceGroupDeployment(
	ctx context.Context,
	subscriptionId,
	resourceGroupName string,
	deploymentName string,
	progress *async.Progress[DeleteDeploymentProgress],
) error {
	progress.SetProgress(DeleteDeploymentProgress{
		Name:    resourceGroupName,
		Message: fmt.Sprintf("Deleting resource group %s", output.WithHighLightFormat(resourceGroupName)),
		State:   DeleteResourceStateInProgress,
	})

	if err := ds.resourceService.DeleteResourceGroup(ctx, subscriptionId, resourceGroupName); err != nil {
		progress.SetProgress(DeleteDeploymentProgress{
			Name:    resourceGroupName,
			Message: fmt.Sprintf("Failed resource group %s", output.WithHighLightFormat(resourceGroupName)),
			State:   DeleteResourceStateInProgress,
		})

		return err
	}

	progress.SetProgress(DeleteDeploymentProgress{
		Name:    resourceGroupName,
		Message: fmt.Sprintf("Deleted resource group %s", output.WithHighLightFormat(resourceGroupName)),
		State:   DeleteResourceStateInProgress,
	})

	// Deploy empty template to void provision state and keep deployment history instead of deleting previous deployments
	// Get deployment metadata
	deployment, err := ds.GetResourceGroupDeployment(ctx, subscriptionId, resourceGroupName, deploymentName)
	if err != nil {
		return fmt.Errorf("resource group deployment '%s' not found: %w", deploymentName, err)
	}

	envName, has := deployment.Tags[azure.TagKeyAzdEnvName]
	if has {
		var emptyTemplate json.RawMessage = []byte(emptySubscriptionArmTemplate)
		emptyDeploymentName := ds.GenerateDeploymentName(*envName)
		tags := map[string]*string{
			azure.TagKeyAzdEnvName: envName,
			"azd-deploy-reason":    to.Ptr("down"),
		}

		_, err = ds.DeployToResourceGroup(
			ctx,
			subscriptionId,
			resourceGroupName,
			emptyDeploymentName,
			emptyTemplate,
			azure.ArmParameters{},
			tags,
		)

		if err != nil {
			return fmt.Errorf("deploying empty template to resource group: %w", err)
		}
	}

	return nil
}

func (ds *StandardDeployments) getResourcesFromDeployment(
	ctx context.Context,
	deployment *armresources.DeploymentExtended,
	subscriptionId string,
) ([]*armresources.ResourceReference, error) {
	allResources := map[string]*armresources.ResourceReference{}
	for _, outputResource := range deployment.Properties.OutputResources {
		allResources[*outputResource.ID] = outputResource
	}

	for _, dependency := range deployment.Properties.Dependencies {
		if *dependency.ResourceType == string(AzureResourceTypeDeployment) {
			for _, dependentResource := range dependency.DependsOn {
				if *dependentResource.ResourceType == string(AzureResourceTypeResourceGroup) {
					parsedResource, err := arm.ParseResourceID(*dependency.ID)
					if err != nil {
						return nil, fmt.Errorf("parsing resource ID: %w", err)
					}

					deploymentResources, err := ds.ListResourceGroupDeploymentResources(
						ctx,
						subscriptionId,
						parsedResource.ResourceGroupName,
						*dependency.ResourceName,
					)
					if err != nil {
						return nil, fmt.Errorf("getting deployment resources: %w", err)
					}

					for _, deploymentResource := range deploymentResources {
						allResources[*deploymentResource.ID] = deploymentResource
					}
				}
			}
		}
	}

	return maps.Values(allResources), nil
}

func (ds *StandardDeployments) WhatIfDeployToSubscription(
	ctx context.Context,
	subscriptionId string,
	location string,
	deploymentName string,
	armTemplate azure.RawArmTemplate,
	parameters azure.ArmParameters,
) (*armresources.WhatIfOperationResult, error) {
	deploymentClient, err := ds.createDeploymentsClient(ctx, subscriptionId)
	if err != nil {
		return nil, fmt.Errorf("creating deployments client: %w", err)
	}

	createFromTemplateOperation, err := deploymentClient.BeginWhatIfAtSubscriptionScope(
		ctx, deploymentName,
		armresources.DeploymentWhatIf{
			Properties: &armresources.DeploymentWhatIfProperties{
				Template:       armTemplate,
				Parameters:     parameters,
				Mode:           to.Ptr(armresources.DeploymentModeIncremental),
				WhatIfSettings: &armresources.DeploymentWhatIfSettings{},
			},
			Location: to.Ptr(location),
		}, nil)
	if err != nil {
		return nil, fmt.Errorf("starting deployment to subscription: %w", err)
	}

	// wait for deployment creation
	deployResult, err := createFromTemplateOperation.PollUntilDone(ctx, nil)
	if err != nil {
		deploymentError := createDeploymentError(err)
		return nil, fmt.Errorf(
			"deploying to subscription:\n\nDeployment Error Details:\n%w",
			deploymentError,
		)
	}

	return &deployResult.WhatIfOperationResult, nil
}

func (ds *StandardDeployments) WhatIfDeployToResourceGroup(
	ctx context.Context,
	subscriptionId, resourceGroup, deploymentName string,
	armTemplate azure.RawArmTemplate,
	parameters azure.ArmParameters,
) (*armresources.WhatIfOperationResult, error) {
	deploymentClient, err := ds.createDeploymentsClient(ctx, subscriptionId)
	if err != nil {
		return nil, fmt.Errorf("creating deployments client: %w", err)
	}

	createFromTemplateOperation, err := deploymentClient.BeginWhatIf(
		ctx, resourceGroup, deploymentName,
		armresources.DeploymentWhatIf{
			Properties: &armresources.DeploymentWhatIfProperties{
				Template:   armTemplate,
				Parameters: parameters,
				Mode:       to.Ptr(armresources.DeploymentModeIncremental),
			},
		}, nil)
	if err != nil {
		return nil, fmt.Errorf("starting deployment to resource group: %w", err)
	}

	// wait for deployment creation
	deployResult, err := createFromTemplateOperation.PollUntilDone(ctx, nil)
	if err != nil {
		deploymentError := createDeploymentError(err)
		return nil, fmt.Errorf(
			"deploying to resource group:\n\nDeployment Error Details:\n%w",
			deploymentError,
		)
	}

	return &deployResult.WhatIfOperationResult, nil
}

func (ds *StandardDeployments) createDeploymentsOperationsClient(
	ctx context.Context,
	subscriptionId string,
) (*armresources.DeploymentOperationsClient, error) {
	credential, err := ds.credentialProvider.CredentialForSubscription(ctx, subscriptionId)
	if err != nil {
		return nil, err
	}

	client, err := armresources.NewDeploymentOperationsClient(subscriptionId, credential, ds.armClientOptions)
	if err != nil {
		return nil, fmt.Errorf("creating deployments client: %w", err)
	}

	return client, nil
}

// Converts from an ARM Extended Deployment to Azd Generic deployment
func (ds *StandardDeployments) convertFromArmDeployment(deployment *armresources.DeploymentExtended) *ResourceDeployment {
	return &ResourceDeployment{
		Id:                *deployment.ID,
		Location:          convert.ToValueWithDefault(deployment.Location, ""),
		DeploymentId:      *deployment.ID,
		Name:              *deployment.Name,
		Type:              *deployment.Type,
		Tags:              deployment.Tags,
		ProvisioningState: convertFromStandardProvisioningState(*deployment.Properties.ProvisioningState),
		Timestamp:         *deployment.Properties.Timestamp,
		TemplateHash:      deployment.Properties.TemplateHash,
		Outputs:           deployment.Properties.Outputs,
		Resources:         deployment.Properties.OutputResources,
		Dependencies:      deployment.Properties.Dependencies,

		PortalUrl: fmt.Sprintf("%s/%s/%s",
			ds.cloud.PortalUrlBase,
			cPortalUrlFragment,
			url.PathEscape(*deployment.ID),
		),

		OutputsUrl: fmt.Sprintf("%s/%s/%s",
			ds.cloud.PortalUrlBase,
			cOutputsUrlFragment,
			url.PathEscape(*deployment.ID),
		),

		DeploymentUrl: fmt.Sprintf("%s/%s/%s",
			ds.cloud.PortalUrlBase,
			cPortalUrlFragment,
			url.PathEscape(*deployment.ID),
		),
	}
}

func convertFromStandardProvisioningState(state armresources.ProvisioningState) DeploymentProvisioningState {
	switch state {
	case armresources.ProvisioningStateAccepted:
		return DeploymentProvisioningStateAccepted
	case armresources.ProvisioningStateCanceled:
		return DeploymentProvisioningStateCanceled
	case armresources.ProvisioningStateCreating:
		return DeploymentProvisioningStateCreating
	case armresources.ProvisioningStateDeleted:
		return DeploymentProvisioningStateDeleted
	case armresources.ProvisioningStateDeleting:
		return DeploymentProvisioningStateDeleting
	case armresources.ProvisioningStateFailed:
		return DeploymentProvisioningStateFailed
	case armresources.ProvisioningStateNotSpecified:
		return DeploymentProvisioningStateNotSpecified
	case armresources.ProvisioningStateReady:
		return DeploymentProvisioningStateReady
	case armresources.ProvisioningStateRunning:
		return DeploymentProvisioningStateRunning
	case armresources.ProvisioningStateSucceeded:
		return DeploymentProvisioningStateSucceeded
	case armresources.ProvisioningStateUpdating:
		return DeploymentProvisioningStateUpdating
	}

	return DeploymentProvisioningState("")
}
