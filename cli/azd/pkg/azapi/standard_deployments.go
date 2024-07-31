package azapi

import (
	"context"
	"errors"
	"fmt"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"github.com/azure/azure-dev/cli/azd/pkg/account"
	"github.com/azure/azure-dev/cli/azd/pkg/azure"
	"github.com/benbjohnson/clock"
)

// cArmDeploymentNameLengthMax is the maximum length of the name of a deployment in ARM.
const cArmDeploymentNameLengthMax = 64

type deployments struct {
	credentialProvider account.SubscriptionCredentialProvider
	armClientOptions   *arm.ClientOptions
	clock              clock.Clock
}

func NewDeployments(
	credentialProvider account.SubscriptionCredentialProvider,
	armClientOptions *arm.ClientOptions,
	clock clock.Clock,
) Deployments {
	return &deployments{
		credentialProvider: credentialProvider,
		armClientOptions:   armClientOptions,
		clock:              clock,
	}
}

// GenerateDeploymentName creates a name to use for the deployment object for a given environment. It appends the current
// unix time to the environment name (separated by a hyphen) to provide a unique name for each deployment. If the resulting
// name is longer than the ARM limit, the longest suffix of the name under the limit is returned.
func (ds *deployments) GenerateDeploymentName(baseName string) string {
	name := fmt.Sprintf("%s-%d", baseName, ds.clock.Now().Unix())
	if len(name) <= cArmDeploymentNameLengthMax {
		return name
	}

	return name[len(name)-cArmDeploymentNameLengthMax:]
}

func (ds *deployments) CalculateTemplateHash(
	ctx context.Context,
	subscriptionId string,
	template azure.RawArmTemplate) (result armresources.DeploymentsClientCalculateTemplateHashResponse, err error) {
	deploymentClient, err := ds.createDeploymentsClient(ctx, subscriptionId)
	if err != nil {
		return result, fmt.Errorf("creating deployments client: %w", err)
	}

	return deploymentClient.CalculateTemplateHash(ctx, template, nil)
}

func (ds *deployments) ListSubscriptionDeployments(
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
			results = append(results, convertFromArmDeployment(deployment))
		}
	}

	return results, nil
}

func (ds *deployments) GetSubscriptionDeployment(
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

	return convertFromArmDeployment(&deployment.DeploymentExtended), nil
}

func (ds *deployments) ListResourceGroupDeployments(
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
			results = append(results, convertFromArmDeployment(deployment))
		}
	}

	return results, nil
}

func (ds *deployments) GetResourceGroupDeployment(
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

	return convertFromArmDeployment(&deployment.DeploymentExtended), nil
}

func (ds *deployments) createDeploymentsClient(
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

func (ds *deployments) DeployToSubscription(
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

	return convertFromArmDeployment(&deployResult.DeploymentExtended), nil
}

func (ds *deployments) DeployToResourceGroup(
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

	return convertFromArmDeployment(&deployResult.DeploymentExtended), nil
}

func (ds *deployments) WhatIfDeployToSubscription(
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

func (ds *deployments) WhatIfDeployToResourceGroup(
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

// Converts from an ARM Extended Deployment to Azd Generic deployment
func convertFromArmDeployment(deployment *armresources.DeploymentExtended) *ResourceDeployment {
	return &ResourceDeployment{
		Id:                *deployment.ID,
		DeploymentId:      *deployment.ID,
		Name:              *deployment.Name,
		Type:              *deployment.Type,
		Tags:              deployment.Tags,
		ProvisioningState: DeploymentProvisioningState(*deployment.Properties.ProvisioningState),
		Timestamp:         *deployment.Properties.Timestamp,
		TemplateHash:      deployment.Properties.TemplateHash,
		Outputs:           deployment.Properties.Outputs,
		Resources:         deployment.Properties.OutputResources,
		Dependencies:      []*armresources.Dependency{}, // deployment.Properties.Dependencies,
	}
}
