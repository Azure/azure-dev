// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azapi

import (
	"context"
	"errors"
	"fmt"
	"log"
	"maps"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armdeploymentstacks"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/account"
	"github.com/azure/azure-dev/cli/azd/pkg/alpha"
	"github.com/azure/azure-dev/cli/azd/pkg/async"
	"github.com/azure/azure-dev/cli/azd/pkg/azure"
	"github.com/azure/azure-dev/cli/azd/pkg/cloud"
	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/convert"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/benbjohnson/clock"
	"github.com/sethvargo/go-retry"
)

var FeatureDeploymentStacks = alpha.MustFeatureKey("deployment.stacks")

const (
	deploymentStacksConfigKey      = "DeploymentStacks"
	stacksPortalUrlFragment        = "#@microsoft.onmicrosoft.com/resource"
	bypassOutOfSyncErrorEnvVarName = "DEPLOYMENT_STACKS_BYPASS_STACK_OUT_OF_SYNC_ERROR"
)

var defaultDeploymentStackOptions = &deploymentStackOptions{
	BypassStackOutOfSyncError: to.Ptr(false),
	ActionOnUnmanage: &armdeploymentstacks.ActionOnUnmanage{
		ManagementGroups: to.Ptr(armdeploymentstacks.DeploymentStacksDeleteDetachEnumDelete),
		ResourceGroups:   to.Ptr(armdeploymentstacks.DeploymentStacksDeleteDetachEnumDelete),
		Resources:        to.Ptr(armdeploymentstacks.DeploymentStacksDeleteDetachEnumDelete),
	},
	DenySettings: &armdeploymentstacks.DenySettings{
		Mode: to.Ptr(armdeploymentstacks.DenySettingsModeNone),
	},
}

type StackDeployments struct {
	credentialProvider  account.SubscriptionCredentialProvider
	armClientOptions    *arm.ClientOptions
	standardDeployments *StandardDeployments
	cloud               *cloud.Cloud
}

type deploymentStackOptions struct {
	BypassStackOutOfSyncError *bool                                 `yaml:"bypassStackOutOfSyncError,omitempty"`
	ActionOnUnmanage          *armdeploymentstacks.ActionOnUnmanage `yaml:"actionOnUnmanage,omitempty"`
	DenySettings              *armdeploymentstacks.DenySettings     `yaml:"denySettings,omitempty"`
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

// GenerateDeploymentName creates a name to use for the deployment stack from the base name.
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
		retry.WithMaxDuration(
			10*time.Minute,
			retry.WithCappedDuration(10*time.Second, retry.NewExponential(1*time.Second)),
		),
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
		retry.WithMaxDuration(
			10*time.Minute,
			retry.WithCappedDuration(10*time.Second, retry.NewExponential(1*time.Second)),
		),
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
	options map[string]any,
) (*ResourceDeployment, error) {
	client, err := d.createClient(ctx, subscriptionId)
	if err != nil {
		return nil, err
	}

	stack, err := d.stackFromArmForSubscription(ctx, subscriptionId, location, armTemplate, parameters, tags, options)
	if err != nil {
		return nil, err
	}

	poller, err := client.BeginCreateOrUpdateAtSubscription(ctx, deploymentName, stack, nil)
	if err != nil {
		return nil, err
	}

	_, err = poller.PollUntilDone(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("deploying to subscription: %w", createDeploymentError(err, DeploymentOperationDeploy))
	}

	return d.GetSubscriptionDeployment(ctx, subscriptionId, deploymentName)
}

func (d *StackDeployments) stackFromArmForResourceGroup(
	ctx context.Context,
	subscriptionId string,
	armTemplate azure.RawArmTemplate,
	parameters azure.ArmParameters,
	tags map[string]*string,
	options map[string]any,
) (armdeploymentstacks.DeploymentStack, error) {
	if tags == nil {
		tags = map[string]*string{}
	}

	templateHash, err := d.CalculateTemplateHash(ctx, subscriptionId, armTemplate)
	if err != nil {
		return armdeploymentstacks.DeploymentStack{}, fmt.Errorf("failed to calculate template hash: %w", err)
	}

	clonedTags := maps.Clone(tags)
	clonedTags[azure.TagKeyAzdDeploymentTemplateHashName] = &templateHash

	stackParams := convertToStackParams(parameters)

	deploymentStackOptions, err := parseDeploymentStackOptions(options)
	if err != nil {
		return armdeploymentstacks.DeploymentStack{}, err
	}

	stack := armdeploymentstacks.DeploymentStack{
		Tags: clonedTags,
		Properties: &armdeploymentstacks.DeploymentStackProperties{
			BypassStackOutOfSyncError: deploymentStackOptions.BypassStackOutOfSyncError,
			ActionOnUnmanage:          deploymentStackOptions.ActionOnUnmanage,
			DenySettings:              deploymentStackOptions.DenySettings,
			Parameters:                stackParams,
			Template:                  armTemplate,
		},
	}

	return stack, nil
}

func (d *StackDeployments) DeployToResourceGroup(
	ctx context.Context,
	subscriptionId string,
	resourceGroup string,
	deploymentName string,
	armTemplate azure.RawArmTemplate,
	parameters azure.ArmParameters,
	tags map[string]*string,
	options map[string]any,
) (*ResourceDeployment, error) {
	client, err := d.createClient(ctx, subscriptionId)
	if err != nil {
		return nil, err
	}

	stack, err := d.stackFromArmForResourceGroup(ctx, subscriptionId, armTemplate, parameters, tags, options)
	if err != nil {
		return nil, err
	}

	poller, err := client.BeginCreateOrUpdateAtResourceGroup(ctx, resourceGroup, deploymentName, stack, nil)
	if err != nil {
		return nil, err
	}

	_, err = poller.PollUntilDone(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("deploying to resource group: %w", createDeploymentError(err, DeploymentOperationDeploy))
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
	return nil, ErrPreviewNotSupported
}

func (d *StackDeployments) WhatIfDeployToResourceGroup(
	ctx context.Context,
	subscriptionId string,
	resourceGroup string,
	deploymentName string,
	armTemplate azure.RawArmTemplate,
	parameters azure.ArmParameters,
) (*armresources.WhatIfOperationResult, error) {
	return nil, ErrPreviewNotSupported
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
	options map[string]any,
	progress *async.Progress[DeleteDeploymentProgress],
) error {
	client, err := d.createClient(ctx, subscriptionId)
	if err != nil {
		return err
	}

	deploymentStackOptions, err := parseDeploymentStackOptions(options)
	if err != nil {
		return err
	}

	deleteOptions := &armdeploymentstacks.ClientBeginDeleteAtSubscriptionOptions{
		BypassStackOutOfSyncError: deploymentStackOptions.BypassStackOutOfSyncError,
		UnmanageActionManagementGroups: (*armdeploymentstacks.UnmanageActionManagementGroupMode)(
			deploymentStackOptions.ActionOnUnmanage.ManagementGroups,
		),
		UnmanageActionResourceGroups: (*armdeploymentstacks.UnmanageActionResourceGroupMode)(
			deploymentStackOptions.ActionOnUnmanage.ResourceGroups,
		),
		UnmanageActionResources: (*armdeploymentstacks.UnmanageActionResourceMode)(
			deploymentStackOptions.ActionOnUnmanage.Resources,
		),
	}

	progress.SetProgress(DeleteDeploymentProgress{
		Name:    deploymentName,
		Message: fmt.Sprintf("Deleting subscription deployment stack %s", output.WithHighLightFormat(deploymentName)),
		State:   DeleteResourceStateInProgress,
	})

	poller, err := client.BeginDeleteAtSubscription(ctx, deploymentName, deleteOptions)
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

// parseDeploymentStackOptions parses the deployment stack options from the given options map.
// If the options map is nil, the default deployment stack options are returned.
// Default deployment stack options are:
// - BypassStackOutOfSyncError: false
// - ActionOnUnmanage: Delete for all
// - DenySettings: nil
func parseDeploymentStackOptions(options map[string]any) (*deploymentStackOptions, error) {
	bypassStackOutOfSyncErrorVal, hasBypassStackOutOfSyncError := os.LookupEnv(bypassOutOfSyncErrorEnvVarName)

	if options == nil && !hasBypassStackOutOfSyncError {
		return defaultDeploymentStackOptions, nil
	}

	optionsConfig := config.NewConfig(options)

	var deploymentStackOptions *deploymentStackOptions
	hasDeploymentStacksConfig, err := optionsConfig.GetSection(deploymentStacksConfigKey, &deploymentStackOptions)
	if err != nil {
		suggestion := &internal.ErrorWithSuggestion{
			Err:        fmt.Errorf("failed parsing deployment stack options: %w", err),
			Suggestion: "Review the 'infra.deploymentStacks' configuration section in the 'azure.yaml' file.",
		}

		return nil, suggestion
	}

	if !hasBypassStackOutOfSyncError && (!hasDeploymentStacksConfig || deploymentStackOptions == nil) {
		return defaultDeploymentStackOptions, nil
	}

	if deploymentStackOptions == nil {
		deploymentStackOptions = defaultDeploymentStackOptions
	}

	// The BypassStackOutOfSyncError will NOT be exposed in the `azure.yaml` for configuration
	// since this option will typically only be used on a per call basis.
	// The value will be read from the environment variable `DEPLOYMENT_STACKS_BYPASS_STACK_OUT_OF_SYNC_ERROR`
	// If the value is a truthy value, the value will be set to true, otherwise it will be set to false (default)
	if hasBypassStackOutOfSyncError {
		byPassOutOfSyncError, err := strconv.ParseBool(bypassStackOutOfSyncErrorVal)
		if err != nil {
			log.Printf(
				"Failed to parse environment variable '%s' value '%s' as a boolean. Defaulting to false.",
				bypassOutOfSyncErrorEnvVarName,
				bypassStackOutOfSyncErrorVal,
			)
		} else {
			deploymentStackOptions.BypassStackOutOfSyncError = &byPassOutOfSyncError
		}
	}

	if deploymentStackOptions.BypassStackOutOfSyncError == nil {
		deploymentStackOptions.BypassStackOutOfSyncError = defaultDeploymentStackOptions.BypassStackOutOfSyncError
	}

	if deploymentStackOptions.ActionOnUnmanage == nil {
		deploymentStackOptions.ActionOnUnmanage = defaultDeploymentStackOptions.ActionOnUnmanage
	}

	if deploymentStackOptions.DenySettings == nil {
		deploymentStackOptions.DenySettings = defaultDeploymentStackOptions.DenySettings
	}

	return deploymentStackOptions, nil
}

func (d *StackDeployments) DeleteResourceGroupDeployment(
	ctx context.Context,
	subscriptionId,
	resourceGroupName string,
	deploymentName string,
	options map[string]any,
	progress *async.Progress[DeleteDeploymentProgress],
) error {
	client, err := d.createClient(ctx, subscriptionId)
	if err != nil {
		return err
	}

	deploymentStackOptions, err := parseDeploymentStackOptions(options)
	if err != nil {
		return err
	}

	deleteOptions := &armdeploymentstacks.ClientBeginDeleteAtResourceGroupOptions{
		BypassStackOutOfSyncError: deploymentStackOptions.BypassStackOutOfSyncError,
		UnmanageActionManagementGroups: (*armdeploymentstacks.UnmanageActionManagementGroupMode)(
			deploymentStackOptions.ActionOnUnmanage.ManagementGroups,
		),
		UnmanageActionResourceGroups: (*armdeploymentstacks.UnmanageActionResourceGroupMode)(
			deploymentStackOptions.ActionOnUnmanage.ResourceGroups,
		),
		UnmanageActionResources: (*armdeploymentstacks.UnmanageActionResourceMode)(
			deploymentStackOptions.ActionOnUnmanage.Resources,
		),
	}

	progress.SetProgress(DeleteDeploymentProgress{
		Name:    deploymentName,
		Message: fmt.Sprintf("Deleting resource group deployment stack %s", output.WithHighLightFormat(deploymentName)),
		State:   DeleteResourceStateInProgress,
	})

	poller, err := client.BeginDeleteAtResourceGroup(ctx, resourceGroupName, deploymentName, deleteOptions)
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

	deploymentId := convert.ToValueWithDefault(deployment.Properties.DeploymentID, "")

	return &ResourceDeployment{
		Id:                *deployment.ID,
		Location:          convert.ToValueWithDefault(deployment.Location, ""),
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
			stacksPortalUrlFragment,
			*deployment.ID,
		),

		OutputsUrl: fmt.Sprintf("%s/%s/%s/outputs",
			d.cloud.PortalUrlBase,
			stacksPortalUrlFragment,
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

// convertToStackParams converts the given ARM parameters to deployment stack parameters
func convertToStackParams(parameters azure.ArmParameters) map[string]*armdeploymentstacks.DeploymentParameter {
	stackParams := map[string]*armdeploymentstacks.DeploymentParameter{}
	for k, v := range parameters {
		if v.KeyVaultReference != nil {
			stackParams[k] = &armdeploymentstacks.DeploymentParameter{
				Reference: &armdeploymentstacks.KeyVaultParameterReference{
					KeyVault: &armdeploymentstacks.KeyVaultReference{
						ID: &v.KeyVaultReference.KeyVault.ID,
					},
					SecretName:    &v.KeyVaultReference.SecretName,
					SecretVersion: &v.KeyVaultReference.SecretVersion,
				},
			}
		} else {
			stackParams[k] = &armdeploymentstacks.DeploymentParameter{
				Value: v.Value,
			}
		}
	}
	return stackParams
}

// Preflight API validates whether the specified template is syntactically correct
// and will be accepted by Azure Resource Manager.
func (d *StackDeployments) ValidatePreflightToResourceGroup(
	ctx context.Context,
	subscriptionId string,
	resourceGroup string,
	deploymentName string,
	armTemplate azure.RawArmTemplate,
	parameters azure.ArmParameters,
	tags map[string]*string,
	options map[string]any,
) error {
	client, err := d.createClient(ctx, subscriptionId)
	if err != nil {
		return err
	}

	stack, err := d.stackFromArmForResourceGroup(ctx, subscriptionId, armTemplate, parameters, tags, options)
	if err != nil {
		return err
	}

	validateResult, err := client.BeginValidateStackAtResourceGroup(ctx, resourceGroup, deploymentName, stack, nil)
	if err != nil {
		return fmt.Errorf(
			"validating deployment to resource group: %w",
			createDeploymentError(err, DeploymentOperationValidate),
		)
	}
	_, err = validateResult.PollUntilDone(ctx, nil)
	if err != nil {
		return fmt.Errorf(
			"validating deployment to resource group: %w",
			createDeploymentError(err, DeploymentOperationValidate),
		)
	}

	return nil
}

func (d *StackDeployments) stackFromArmForSubscription(
	ctx context.Context,
	subscriptionId string,
	location string,
	armTemplate azure.RawArmTemplate,
	parameters azure.ArmParameters,
	tags map[string]*string,
	options map[string]any,
) (armdeploymentstacks.DeploymentStack, error) {
	if tags == nil {
		tags = map[string]*string{}
	}

	templateHash, err := d.CalculateTemplateHash(ctx, subscriptionId, armTemplate)
	if err != nil {
		return armdeploymentstacks.DeploymentStack{}, fmt.Errorf("failed to calculate template hash: %w", err)
	}

	clonedTags := maps.Clone(tags)
	clonedTags[azure.TagKeyAzdDeploymentTemplateHashName] = &templateHash

	stackParams := convertToStackParams(parameters)

	deploymentStackOptions, err := parseDeploymentStackOptions(options)
	if err != nil {
		return armdeploymentstacks.DeploymentStack{}, err
	}

	stack := armdeploymentstacks.DeploymentStack{
		Location: &location,
		Tags:     clonedTags,
		Properties: &armdeploymentstacks.DeploymentStackProperties{
			BypassStackOutOfSyncError: deploymentStackOptions.BypassStackOutOfSyncError,
			ActionOnUnmanage:          deploymentStackOptions.ActionOnUnmanage,
			DenySettings:              deploymentStackOptions.DenySettings,
			Parameters:                stackParams,
			Template:                  armTemplate,
		},
	}
	return stack, nil
}

// Preflight API validates whether the specified template is syntactically correct
// and will be accepted by Azure Resource Manager.
func (d *StackDeployments) ValidatePreflightToSubscription(
	ctx context.Context,
	subscriptionId string,
	location string,
	deploymentName string,
	armTemplate azure.RawArmTemplate,
	parameters azure.ArmParameters,
	tags map[string]*string,
	options map[string]any,
) error {
	client, err := d.createClient(ctx, subscriptionId)
	if err != nil {
		return err
	}

	stack, err := d.stackFromArmForSubscription(ctx, subscriptionId, location, armTemplate, parameters, tags, options)
	if err != nil {
		return err
	}

	validateResult, err := client.BeginValidateStackAtSubscription(ctx, deploymentName, stack, nil)
	if err != nil {
		return fmt.Errorf(
			"validating deployment to subscription: %w",
			createDeploymentError(err, DeploymentOperationValidate),
		)
	}
	_, err = validateResult.PollUntilDone(ctx, nil)
	if err != nil {
		return fmt.Errorf(
			"validating deployment to subscription: %w",
			createDeploymentError(err, DeploymentOperationValidate),
		)
	}

	return nil
}
