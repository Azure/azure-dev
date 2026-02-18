// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package bicep

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"maps"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/cognitiveservices/armcognitiveservices"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"github.com/azure/azure-dev/cli/azd/pkg/account"
	"github.com/azure/azure-dev/cli/azd/pkg/ai"
	"github.com/azure/azure-dev/cli/azd/pkg/async"
	"github.com/azure/azure-dev/cli/azd/pkg/azapi"
	"github.com/azure/azure-dev/cli/azd/pkg/azure"
	"github.com/azure/azure-dev/cli/azd/pkg/cloud"
	"github.com/azure/azure-dev/cli/azd/pkg/cmdsubst"
	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/convert"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/infra"
	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/keyvault"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/output/ux"
	"github.com/azure/azure-dev/cli/azd/pkg/password"
	"github.com/azure/azure-dev/cli/azd/pkg/prompt"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/bicep"
	"github.com/drone/envsubst"
)

type bicepFileMode int

const (
	bicepMode bicepFileMode = iota
	bicepparamMode
)

const (
	// apiVersion for checking if a resource group exists
	apiVersionResourceGroupExistence = "2025-03-01"
)

// BicepProvider exposes infrastructure provisioning using Azure Bicep templates
type BicepProvider struct {
	// Options that are available after Initialize()
	options               provisioning.Options
	projectPath           string
	path                  string
	layer                 string
	mode                  bicepFileMode
	ignoreDeploymentState bool

	// Dependencies
	envManager          environment.Manager
	env                 *environment.Environment
	console             input.Console
	bicepCli            *bicep.Cli
	azapi               *azapi.AzureClient
	resourceService     *azapi.ResourceService
	resourceManager     infra.ResourceManager
	deploymentManager   *infra.DeploymentManager
	prompters           prompt.Prompter
	curPrincipal        provisioning.CurrentPrincipalIdProvider
	portalUrlBase       string
	keyvaultService     keyvault.KeyVaultService
	subscriptionManager *account.SubscriptionsManager
	aiModelService      *ai.AiModelService

	// Internal state
	// compileBicepResult is cached to avoid recompiling the same bicep file multiple times in the same azd run.
	compileBicepMemoryCache *compileBicepResult
}

// Name gets the name of the infra provider
func (p *BicepProvider) Name() string {
	return "Bicep"
}

// Initialize initializes provider state from the options.
// It also calls EnsureEnv, which ensures the client-side state is ready for provisioning.
func (p *BicepProvider) Initialize(ctx context.Context, projectPath string, opt provisioning.Options) error {
	infraOptions, err := opt.GetWithDefaults()
	if err != nil {
		return err
	}

	if !filepath.IsAbs(infraOptions.Path) {
		infraOptions.Path = filepath.Join(projectPath, infraOptions.Path)
	}

	bicepparam := infraOptions.Module + ".bicepparam"
	bicepFile := infraOptions.Module + ".bicep"

	// Check if there's a <moduleName>.bicepparam first. It will be preferred over a <moduleName>.bicep
	if _, err := os.Stat(filepath.Join(infraOptions.Path, bicepparam)); err == nil {
		p.path = filepath.Join(infraOptions.Path, bicepparam)
		p.mode = bicepparamMode
	} else {
		p.path = filepath.Join(infraOptions.Path, bicepFile)
		p.mode = bicepMode
	}

	p.projectPath = projectPath
	p.layer = infraOptions.Name
	p.options = infraOptions
	p.ignoreDeploymentState = infraOptions.IgnoreDeploymentState

	if opt.Mode == provisioning.ModeDeploy {
		// For regular deployments, ensure the environment is in a provision-ready state
		p.console.ShowSpinner(ctx, "Initialize bicep provider", input.Step)
		err = p.EnsureEnv(ctx)
		p.console.StopSpinner(ctx, "", input.Step)
	}

	return err
}

var ErrEnsureEnvPreReqBicepCompileFailed = errors.New("")

// EnsureEnv ensures that the environment is in a provision-ready state with required values set, prompting the user if
// values are unset. This also requires that the Bicep module can be compiled.
func (p *BicepProvider) EnsureEnv(ctx context.Context) error {
	// for .bicepparam, we first prompt for environment values before calling compiling bicepparam file
	// which can reference these values
	if p.mode == bicepparamMode {
		if err := provisioning.EnsureSubscriptionAndLocation(
			ctx, p.envManager, p.env, p.prompters, provisioning.EnsureSubscriptionAndLocationOptions{}); err != nil {
			return err
		}
	}

	compileResult, compileErr := p.compileBicep(ctx)
	if compileErr != nil {
		return fmt.Errorf("%w%w", ErrEnsureEnvPreReqBicepCompileFailed, compileErr)
	}

	// for .bicep, azd must load a parameters.json file and create the ArmParameters so we know if the are filters
	// to apply for location (using the allowedValues or the location azd metadata)
	if p.mode == bicepMode {
		err := provisioning.EnsureSubscription(
			ctx, p.envManager, p.env, p.prompters)
		if err != nil {
			return err
		}

		_, err = p.ensureParameters(ctx, compileResult.Template)
		if err != nil {
			return err
		}

	}

	scope, err := compileResult.Template.TargetScope()
	if err != nil {
		return err
	}

	if scope == azure.DeploymentScopeResourceGroup {
		if err := p.ensureResourceGroup(ctx, p.env); err != nil {
			return err
		}
	}

	return nil
}

// ensureResourceGroup ensures that the resource group with AZURE_RESOURCE_GROUP key exists in the environment,
// prompting the user to create a resource group if it is unset or does not exist.
func (p *BicepProvider) ensureResourceGroup(ctx context.Context, env *environment.Environment) error {
	promptAndSave := func(opt prompt.PromptResourceOptions) error {
		rgName, err := p.prompters.PromptResourceGroup(ctx, opt)
		if err != nil {
			return err
		}

		p.env.DotenvSet(environment.ResourceGroupEnvVarName, rgName)
		if err := p.envManager.Save(ctx, p.env); err != nil {
			return fmt.Errorf("saving resource group name: %w", err)
		}

		return nil
	}

	resourceGroup := env.Getenv(environment.ResourceGroupEnvVarName)
	if resourceGroup == "" {
		return promptAndSave(prompt.PromptResourceOptions{})
	}

	resourceId := fmt.Sprintf(
		"/subscriptions/%s/resourceGroups/%s",
		p.env.GetSubscriptionId(),
		resourceGroup)

	resId, err := arm.ParseResourceID(resourceId)
	if err != nil {
		return fmt.Errorf("invalid '%s': %w", environment.ResourceGroupEnvVarName, err)
	}

	exists, err := p.resourceService.CheckExistenceByID(ctx, *resId, apiVersionResourceGroupExistence)
	if err != nil {
		return fmt.Errorf("checking if resource group exists: %w", err)
	}

	if !exists {
		// Resource group no longer exists, prompt the user to create a new one.
		// This handles the case where a resource group was deleted.
		return promptAndSave(prompt.PromptResourceOptions{DefaultName: resourceGroup})
	}

	return nil
}

func locationParameterFilterImpl(allowedLocations []string, location account.Location) bool {
	if allowedLocations == nil {
		return true
	}
	return slices.Contains(allowedLocations, location.Name)
}

// defaultPromptValue resolves if there is an intention from a location parameter to use a default location.
//
// If the parameter has AzdMetadataTypeLocation, with a default location set, the default location is returned.
// If the parameter has AllowedValues, the first option value is returned.
// Otherwise, nil is returned to indicate no user-provided default value.
func defaultPromptValue(locationParam azure.ArmTemplateParameterDefinition) *string {
	azdMetadata, has := locationParam.AzdMetadata()
	if has &&
		azdMetadata.Type != nil && *azdMetadata.Type == azure.AzdMetadataTypeLocation &&
		azdMetadata.Default != nil {
		// Metadata using location type and a default location. This is the highest priority.
		defaultStr := fmt.Sprintf("%v", azdMetadata.Default)
		return &defaultStr
	}

	if locationParam.AllowedValues != nil {
		firstOption, castOk := (*locationParam.AllowedValues)[0].(string)
		// if cast doesn't work, we don't have a default location
		if castOk {
			return &firstOption
		}
	}
	return nil
}

func (p *BicepProvider) LastDeployment(ctx context.Context) (*azapi.ResourceDeployment, error) {
	compileResult, err := p.compileBicep(ctx)
	if err != nil {
		return nil, fmt.Errorf("compiling bicep template: %w", err)
	}

	scope, err := p.scopeForTemplate(compileResult.Template)
	if err != nil {
		return nil, fmt.Errorf("computing deployment scope: %w", err)
	}

	return p.latestDeploymentResult(ctx, scope)
}

func (p *BicepProvider) State(ctx context.Context, options *provisioning.StateOptions) (*provisioning.StateResult, error) {
	if options == nil {
		options = &provisioning.StateOptions{}
	}

	var err error
	spinnerMessage := "Loading Bicep template"
	p.console.ShowSpinner(ctx, spinnerMessage, input.Step)
	defer func() {
		// Make sure we stop the spinner if an error occurs with the last message.
		if err != nil {
			p.console.StopSpinner(ctx, spinnerMessage, input.StepFailed)
		}
	}()

	var scope infra.Scope
	var outputs azure.ArmTemplateOutputs
	var scopeErr error

	if _, err := os.Stat(p.path); err == nil {
		compileResult, err := p.compileBicep(ctx)
		if err != nil {
			return nil, fmt.Errorf("compiling bicep template: %w", err)
		}

		scope, err = p.scopeForTemplate(compileResult.Template)
		if err != nil {
			return nil, fmt.Errorf("computing deployment scope: %w", err)
		}

		outputs = compileResult.Template.Outputs
	} else if errors.Is(err, os.ErrNotExist) {
		// To support BYOI (bring your own infrastructure)
		// We need to support the case where there template does not contain an `infra` folder.
		scope, scopeErr = p.inferScopeFromEnv()
		if scopeErr != nil {
			return nil, fmt.Errorf("computing deployment scope: %w", err)
		}

		outputs = azure.ArmTemplateOutputs{}
	}

	spinnerMessage = "Retrieving Azure deployment"
	p.console.ShowSpinner(ctx, spinnerMessage, input.Step)

	var deployment *azapi.ResourceDeployment

	deployments, err := p.deploymentManager.CompletedDeployments(ctx, scope, p.env.Name(), p.layer, options.Hint())
	p.console.StopSpinner(ctx, "", input.StepDone)

	if err != nil {
		p.console.StopSpinner(ctx, spinnerMessage, input.StepFailed)
		return nil, fmt.Errorf("retrieving deployment: %w", err)
	} else {
		p.console.StopSpinner(ctx, "", input.StepDone)
	}

	if len(deployments) > 1 {
		deploymentOptions := getDeploymentOptions(deployments)

		p.console.Message(ctx, output.WithWarningFormat("WARNING: Multiple matching deployments were found\n"))

		promptConfig := input.ConsoleOptions{
			Message: "Select a deployment to continue:",
			Options: deploymentOptions,
		}

		selectedDeployment, err := p.console.Select(ctx, promptConfig)
		if err != nil {
			return nil, err
		}

		deployment = deployments[selectedDeployment]
		p.console.Message(ctx, "")
	} else {
		deployment = deployments[0]
	}

	azdDeployment, err := p.createDeploymentFromArmDeployment(scope, deployment.Name)
	if err != nil {
		return nil, err
	}

	p.console.MessageUxItem(ctx, &ux.DoneMessage{
		Message: fmt.Sprintf("Retrieving Azure deployment (%s)", output.WithHighLightFormat(deployment.Name)),
	})

	state := provisioning.State{}
	state.Resources = make([]provisioning.Resource, len(deployment.Resources))

	for idx, res := range deployment.Resources {
		state.Resources[idx] = provisioning.Resource{
			Id: *res.ID,
		}
	}

	state.Outputs = provisioning.OutputParametersFromArmOutputs(
		outputs,
		azapi.CreateDeploymentOutput(deployment.Outputs),
	)

	p.console.MessageUxItem(ctx, &ux.DoneMessage{
		Message: fmt.Sprintf("Updated %d environment variables", len(state.Outputs)),
	})

	outputsUrl, err := azdDeployment.OutputsUrl(ctx)
	if err != nil {
		return nil, err
	}

	p.console.Message(ctx, fmt.Sprintf(
		"\nPopulated environment from Azure infrastructure deployment: %s",
		output.WithHyperlink(outputsUrl, deployment.Name),
	))

	return &provisioning.StateResult{
		State: &state,
	}, nil
}

func (p *BicepProvider) createDeploymentFromArmDeployment(
	scope infra.Scope,
	deploymentName string,
) (infra.Deployment, error) {
	resourceGroupScope, ok := scope.(*infra.ResourceGroupScope)
	if ok {
		return p.deploymentManager.ResourceGroupDeployment(resourceGroupScope, deploymentName), nil
	}

	subscriptionScope, ok := scope.(*infra.SubscriptionScope)
	if ok {
		return p.deploymentManager.SubscriptionDeployment(subscriptionScope, deploymentName), nil
	}

	return nil, errors.New("unsupported deployment scope")
}

// plan creates an execution plan that can be executed for previewing, or deploying these changes.
//
// It ensures that all parameters are filled in and is ready for execution.
func (p *BicepProvider) plan(ctx context.Context) (*compileBicepResult, error) {
	p.console.ShowSpinner(ctx, "Creating a deployment plan", input.Step)

	switch p.mode {
	case bicepMode:
		compileResult, err := p.compileBicep(ctx)
		if err != nil {
			return nil, fmt.Errorf("compiling bicep template: %w", err)
		}

		// prompt for any missing parameters
		configuredParameters, err := p.ensureParameters(ctx, compileResult.Template)
		if err != nil {
			return nil, err
		}

		compileResult.Parameters = configuredParameters
		return compileResult, nil

	case bicepparamMode:
		// To ensure any compile-time bicepparam parameters such as `readEnvironmentVariable()`
		// are resolved correctly right before deployment occurs, we clear the cache
		// and trigger full compilation here.
		p.compileBicepMemoryCache = nil

		compileResult, err := p.compileBicep(ctx)
		if err != nil {
			return nil, fmt.Errorf("compiling bicep template: %w", err)
		}

		return compileResult, nil
	}

	return nil, errors.New("unsupported bicep mode")
}

// generateDeploymentObject generates an [infra.Deployment] object from the given plan with a unique name.
func (p *BicepProvider) generateDeploymentObject(plan *compileBicepResult) (infra.Deployment, error) {
	baseName := p.env.Name()
	if p.layer != "" {
		baseName += "-" + p.layer
	}

	uniqueName := p.deploymentManager.GenerateDeploymentName(baseName)
	scope, err := plan.Template.TargetScope()
	if err != nil {
		return nil, err
	}

	switch scope {
	case azure.DeploymentScopeSubscription:
		scope := p.deploymentManager.SubscriptionScope(p.env.GetSubscriptionId(), p.env.GetLocation())
		return infra.NewSubscriptionDeployment(
			scope,
			uniqueName,
		), nil

	case azure.DeploymentScopeResourceGroup:
		scope := p.deploymentManager.ResourceGroupScope(
			p.env.GetSubscriptionId(),
			p.env.Getenv(environment.ResourceGroupEnvVarName),
		)
		return infra.NewResourceGroupDeployment(scope, uniqueName), nil

	default:
		return nil, fmt.Errorf("unsupported scope: %s", scope)
	}
}

// deploymentState returns the latests deployment if it is the same as the deployment within deploymentData or an error
// otherwise.
func (p *BicepProvider) deploymentState(
	ctx context.Context,
	planned *compileBicepResult,
	scope infra.Scope,
	currentParamsHash string,
) (*azapi.ResourceDeployment, error) {
	p.console.ShowSpinner(ctx, "Comparing deployment state", input.Step)
	prevDeploymentResult, err := p.latestDeploymentResult(ctx, scope)
	if err != nil {
		return nil, fmt.Errorf("deployment state error: %w", err)
	}

	// State is invalid if the last deployment was not succeeded
	// This is currently safe because we rely on latestDeploymentResult which
	// relies on findCompletedDeployments which filters to only Failed and Succeeded
	if prevDeploymentResult.ProvisioningState != azapi.DeploymentProvisioningStateSucceeded {
		return nil, fmt.Errorf("last deployment failed.")
	}

	templateHash, err := p.deploymentManager.CalculateTemplateHash(
		ctx, p.env.GetSubscriptionId(),
		planned.RawArmTemplate,
	)

	if err != nil {
		return nil, fmt.Errorf("can't get hash from current template: %w", err)
	}

	if !prevDeploymentEqualToCurrent(prevDeploymentResult, templateHash, currentParamsHash) {
		return nil, fmt.Errorf("deployment state has changed")
	}

	return prevDeploymentResult, nil
}

// latestDeploymentResult looks and finds a previous deployment for the current azd project.
func (p *BicepProvider) latestDeploymentResult(
	ctx context.Context,
	scope infra.Scope,
) (*azapi.ResourceDeployment, error) {
	deployments, err := p.deploymentManager.CompletedDeployments(ctx, scope, p.env.Name(), p.layer, "")
	// findCompletedDeployments returns error if no deployments are found
	// No need to check for empty list
	if err != nil {
		return nil, err
	}

	if len(deployments) > 1 {
		// If more than one deployment found, ignore the prev-deployment
		return nil, fmt.Errorf("more than one previous deployment match.")
	}

	return deployments[0], nil
}

// parametersHash generates a hash from its name and final value.
// The final value is either the parameter default value or the value from the params input.
func parametersHash(templateParameters azure.ArmTemplateParameterDefinitions, params azure.ArmParameters) (string, error) {
	hash256 := sha256.New()

	// Get the parameter name and its final value.
	// Any other change on the parameter definition would break the template-hash
	nameAndValueParams := make(map[string]any, len(templateParameters))

	for paramName, paramDefinition := range templateParameters {
		pValue := paramDefinition.DefaultValue
		if param, exists := params[paramName]; exists {
			pValue = param.Value
		}
		nameAndValueParams[paramName] = pValue
	}
	nameAndValueParamsBytes, err := json.Marshal(nameAndValueParams)
	if err != nil {
		return "", err
	}
	if _, err := hash256.Write(nameAndValueParamsBytes); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", hash256.Sum(nil)), nil
}

// prevDeploymentEqualToCurrent compares the template hash from a previous deployment against a current template.
func prevDeploymentEqualToCurrent(prev *azapi.ResourceDeployment, templateHash, paramsHash string) bool {
	if prev == nil {
		logDS("No previous deployment.")
		return false
	}

	if prev.Tags == nil {
		logDS("No previous deployment params tags")
		return false
	}

	prevTemplateHash := convert.ToValueWithDefault(prev.TemplateHash, "")
	if prevTemplateHash != templateHash {
		logDS("template hash is different from previous deployment")
		return false
	}

	prevParamHash, hasTag := prev.Tags[azure.TagKeyAzdDeploymentStateParamHashName]
	if !hasTag {
		logDS("no param hash tag on last deployment.")
		return false
	}

	if *prevParamHash != paramsHash {
		logDS("template parameters are different from previous deployment")
		return false
	}

	logDS("Previous deployment state is equal to current deployment. Deployment can be skipped.")
	return true
}

func logDS(msg string, v ...any) {
	log.Printf("%s : %s", "deployment-state: ", fmt.Sprintf(msg, v...))
}

// Provisioning the infrastructure within the specified template
func (p *BicepProvider) Deploy(ctx context.Context) (*provisioning.DeployResult, error) {
	if p.ignoreDeploymentState {
		logDS("Azure Deployment State is disabled by --no-state arg.")
	}

	planned, err := p.plan(ctx)
	if err != nil {
		return nil, err
	}

	deployment, err := p.generateDeploymentObject(planned)
	if err != nil {
		return nil, err
	}

	result := p.convertToDeployment(planned.Template)

	// parameters hash is required for doing deployment state validation check but also to set the hash
	// after a successful deployment.
	currentParamsHash, parametersHashErr := parametersHash(planned.Template.Parameters, planned.Parameters)
	if parametersHashErr != nil {
		// fail to hash parameters won't stop the operation. It only disables deployment state and recording parameters hash
		logDS("%s", parametersHashErr.Error())
	}

	if !p.ignoreDeploymentState && parametersHashErr == nil {
		deploymentState, stateErr := p.deploymentState(ctx, planned, deployment, currentParamsHash)
		if stateErr == nil {
			// As a heuristic, we also check the existence of all resource groups
			// created by the deployment to validate the deployment state.
			// This handles the scenario of resource group(s) being deleted outside of azd,
			// which is quite common.
			// This check adds ~100ms per resource group to the deployment time.
			for _, res := range deploymentState.Resources {
				if res != nil && res.ID != nil {
					resId, err := arm.ParseResourceID(*res.ID)
					if err == nil && resId.ResourceType.Type == arm.ResourceGroupResourceType.Type {
						exists, err := p.resourceService.CheckExistenceByID(ctx, *resId, apiVersionResourceGroupExistence)
						if err == nil && !exists {
							stateErr = fmt.Errorf(
								"resource group %s no longer exists, invalidating deployment state", resId.ResourceGroupName)
							break
						}
					}
				}
			}
		}

		if stateErr == nil {
			result.Outputs = provisioning.OutputParametersFromArmOutputs(
				planned.Template.Outputs,
				azapi.CreateDeploymentOutput(deploymentState.Outputs),
			)

			return &provisioning.DeployResult{
				Deployment:    &result,
				SkippedReason: provisioning.DeploymentStateSkipped,
			}, nil
		}
		logDS("%s", stateErr.Error())
	}

	deploymentTags := map[string]*string{
		azure.TagKeyAzdEnvName:   to.Ptr(p.env.Name()),
		azure.TagKeyAzdLayerName: &p.layer,
	}
	if parametersHashErr == nil {
		deploymentTags[azure.TagKeyAzdDeploymentStateParamHashName] = to.Ptr(currentParamsHash)
	}

	optionsMap, err := convert.ToMap(p.options)
	if err != nil {
		return nil, err
	}

	err = p.validatePreflight(
		ctx,
		deployment,
		planned.RawArmTemplate,
		planned.Parameters,
		deploymentTags,
		optionsMap,
	)
	if err != nil {
		return nil, err
	}

	cancelProgress := make(chan bool)
	defer func() { cancelProgress <- true }()
	go func() {
		// Disable reporting progress if needed
		if use, err := strconv.ParseBool(os.Getenv("AZD_DEBUG_PROVISION_PROGRESS_DISABLE")); err == nil && use {
			log.Println("Disabling progress reporting since AZD_DEBUG_PROVISION_PROGRESS_DISABLE was set")
			<-cancelProgress
			return
		}

		// Report incremental progress
		progressDisplay := p.deploymentManager.ProgressDisplay(deployment)
		// Make initial delay shorter to be more responsive in displaying initial progress
		initialDelay := 3 * time.Second
		regularDelay := 3 * time.Second
		timer := time.NewTimer(initialDelay)
		queryStartTime := time.Now()

		for {
			select {
			case <-cancelProgress:
				timer.Stop()
				return
			case <-timer.C:
				if err := progressDisplay.ReportProgress(ctx, &queryStartTime); err != nil {
					// We don't want to fail the whole deployment if a progress reporting error occurs
					log.Printf("error while reporting progress: %v", err)
				}

				timer.Reset(regularDelay)
			}
		}
	}()

	// Start the deployment
	p.console.ShowSpinner(ctx, "Creating/Updating resources", input.Step)

	deployResult, err := p.deployModule(
		ctx,
		deployment,
		planned.RawArmTemplate,
		planned.Parameters,
		deploymentTags,
		optionsMap,
	)
	if err != nil {
		return nil, err
	}

	result.Outputs = provisioning.OutputParametersFromArmOutputs(
		planned.Template.Outputs,
		azapi.CreateDeploymentOutput(deployResult.Outputs),
	)

	return &provisioning.DeployResult{
		Deployment: &result,
	}, nil
}

// Preview runs deploy using the what-if argument
func (p *BicepProvider) Preview(ctx context.Context) (*provisioning.DeployPreviewResult, error) {
	planned, err := p.plan(ctx)
	if err != nil {
		return nil, err
	}

	p.console.ShowSpinner(ctx, "Generating infrastructure preview", input.Step)

	deployment, err := p.generateDeploymentObject(planned)
	if err != nil {
		return nil, err
	}

	deployPreviewResult, err := deployment.DeployPreview(
		ctx,
		planned.RawArmTemplate,
		planned.Parameters,
	)
	if err != nil {
		return nil, err
	}

	if deployPreviewResult.Error != nil {
		deploymentErr := *deployPreviewResult.Error
		errDetailsList := make([]string, len(deploymentErr.Details))
		for index, errDetail := range deploymentErr.Details {
			errDetailsList[index] = fmt.Sprintf(
				"code: %s, message: %s",
				convert.ToValueWithDefault(errDetail.Code, ""),
				convert.ToValueWithDefault(errDetail.Message, ""),
			)
		}

		var errDetails string
		if len(errDetailsList) > 0 {
			errDetails = fmt.Sprintf(" Details: %s", strings.Join(errDetailsList, "\n"))
		}
		return nil, fmt.Errorf(
			"generating preview: error code: %s, message: %s.%s",
			convert.ToValueWithDefault(deploymentErr.Code, ""),
			convert.ToValueWithDefault(deploymentErr.Message, ""),
			errDetails,
		)
	}

	var changes []*provisioning.DeploymentPreviewChange
	for _, change := range deployPreviewResult.Properties.Changes {
		// Use After state if available (e.g., Create, Modify), otherwise use Before state (e.g., Delete).
		// ARM returns nil for After when a resource is being deleted and nil for Before when created.
		var resourceState map[string]interface{}
		if change.After != nil {
			resourceState, _ = change.After.(map[string]interface{})
		}
		if resourceState == nil && change.Before != nil {
			resourceState, _ = change.Before.(map[string]interface{})
		}
		if resourceState == nil {
			// Skip changes with no resource state information
			continue
		}

		resourceType, _ := resourceState["type"].(string)
		resourceName, _ := resourceState["name"].(string)

		// Convert Delta (property-level changes) from Azure SDK format to our format
		var delta []provisioning.DeploymentPreviewPropertyChange
		if change.Delta != nil {
			delta = convertPropertyChanges(change.Delta)
		}

		changes = append(changes, &provisioning.DeploymentPreviewChange{
			ChangeType: provisioning.ChangeType(*change.ChangeType),
			ResourceId: provisioning.Resource{
				Id: *change.ResourceID,
			},
			ResourceType: resourceType,
			Name:         resourceName,
			Before:       change.Before,
			After:        change.After,
			Delta:        delta,
		})
	}

	return &provisioning.DeployPreviewResult{
		Preview: &provisioning.DeploymentPreview{
			Status: *deployPreviewResult.Status,
			Properties: &provisioning.DeploymentPreviewProperties{
				Changes: changes,
			},
		},
	}, nil
}

// convertPropertyChanges converts Azure SDK's WhatIfPropertyChange to our DeploymentPreviewPropertyChange
func convertPropertyChanges(changes []*armresources.WhatIfPropertyChange) []provisioning.DeploymentPreviewPropertyChange {
	if changes == nil {
		return nil
	}

	result := make([]provisioning.DeploymentPreviewPropertyChange, 0, len(changes))
	for _, change := range changes {
		if change == nil {
			continue
		}

		propertyChange := provisioning.DeploymentPreviewPropertyChange{
			Path:   convert.ToValueWithDefault(change.Path, ""),
			Before: change.Before,
			After:  change.After,
		}

		// Convert PropertyChangeType
		if change.PropertyChangeType != nil {
			propertyChange.ChangeType = provisioning.PropertyChangeType(*change.PropertyChangeType)
		}

		// Recursively convert children if present
		if change.Children != nil {
			propertyChange.Children = convertPropertyChanges(change.Children)
		}

		result = append(result, propertyChange)
	}

	return result
}

type itemToPurge struct {
	resourceType      string
	count             int
	purge             func(skipPurge bool, self *itemToPurge) error
	cognitiveAccounts []cognitiveAccount
}

func (p *BicepProvider) scopeForTemplate(t azure.ArmTemplate) (infra.Scope, error) {
	deploymentScope, err := t.TargetScope()
	if err != nil {
		return nil, err
	}

	if deploymentScope == azure.DeploymentScopeSubscription {
		return p.deploymentManager.SubscriptionScope(p.env.GetSubscriptionId(), p.env.GetLocation()), nil
	} else if deploymentScope == azure.DeploymentScopeResourceGroup {
		return p.deploymentManager.ResourceGroupScope(
			p.env.GetSubscriptionId(),
			p.env.Getenv(environment.ResourceGroupEnvVarName),
		), nil
	} else {
		return nil, fmt.Errorf("unsupported deployment scope: %s", deploymentScope)
	}
}

func (p *BicepProvider) inferScopeFromEnv() (infra.Scope, error) {
	if resourceGroup, has := p.env.LookupEnv(environment.ResourceGroupEnvVarName); has {
		return p.deploymentManager.ResourceGroupScope(p.env.GetSubscriptionId(), resourceGroup), nil
	} else {
		return p.deploymentManager.SubscriptionScope(p.env.GetSubscriptionId(), p.env.GetLocation()), nil
	}
}

// Destroys the specified deployment by deleting all azure resources, resource groups & deployments that are referenced.
func (p *BicepProvider) Destroy(
	ctx context.Context,
	options provisioning.DestroyOptions,
) (*provisioning.DestroyResult, error) {
	p.console.ShowSpinner(ctx, "Discovering resources to delete...", input.Step)
	defer p.console.StopSpinner(ctx, "", input.StepDone)
	compileResult, err := p.compileBicep(ctx)
	if err != nil {
		return nil, fmt.Errorf("creating template: %w", err)
	}

	targetScope, err := compileResult.Template.TargetScope()
	if err != nil {
		return nil, fmt.Errorf("computing deployment scope: %w", err)
	}

	switch targetScope {
	case azure.DeploymentScopeResourceGroup:
		if p.env.Getenv(environment.ResourceGroupEnvVarName) == "" {
			return nil, azapi.ErrDeploymentNotFound
		}
	case azure.DeploymentScopeSubscription:
		if p.env.Getenv(environment.SubscriptionIdEnvVarName) == "" || p.env.Getenv(environment.LocationEnvVarName) == "" {
			return nil, azapi.ErrDeploymentNotFound
		}
	}

	scope, err := p.scopeForTemplate(compileResult.Template)
	if err != nil {
		return nil, fmt.Errorf("computing deployment scope: %w", err)
	}

	completedDeployments, err := p.deploymentManager.CompletedDeployments(ctx, scope, p.env.Name(), p.layer, "")
	if err != nil {
		return nil, fmt.Errorf("finding completed deployments: %w", err)
	}

	if len(completedDeployments) == 0 {
		return nil, fmt.Errorf("no deployments found for environment, '%s'", p.env.Name())
	}

	mostRecentDeployment := completedDeployments[0]
	deploymentToDelete := scope.Deployment(mostRecentDeployment.Name)

	resourcesToDelete, err := deploymentToDelete.Resources(ctx)
	if err != nil {
		return nil, fmt.Errorf("getting resources to delete: %w", err)
	}

	groupedResources, err := azapi.GroupByResourceGroup(resourcesToDelete)
	if err != nil {
		return nil, fmt.Errorf("mapping resources to resource groups: %w", err)
	}

	// If no resources found, we still need to void the deployment state.
	// This can happen when resources have been manually deleted before running azd down.
	// Voiding the state ensures that subsequent azd provision commands work correctly
	// by creating a new empty deployment that becomes the last successful deployment.
	if len(groupedResources) == 0 {
		p.console.StopSpinner(ctx, "", input.StepDone)
		// Call deployment.Delete to void the state even though there are no resources to delete
		if err := p.destroyDeployment(ctx, deploymentToDelete); err != nil {
			return nil, fmt.Errorf("voiding deployment state: %w", err)
		}
	} else {
		keyVaults, err := p.getKeyVaultsToPurge(ctx, groupedResources)
		if err != nil {
			return nil, fmt.Errorf("getting key vaults to purge: %w", err)
		}

		managedHSMs, err := p.getManagedHSMsToPurge(ctx, groupedResources)
		if err != nil {
			return nil, fmt.Errorf("getting managed hsms to purge: %w", err)
		}

		appConfigs, err := p.getAppConfigsToPurge(ctx, groupedResources)
		if err != nil {
			return nil, fmt.Errorf("getting app configurations to purge: %w", err)
		}

		apiManagements, err := p.getApiManagementsToPurge(ctx, groupedResources)
		if err != nil {
			return nil, fmt.Errorf("getting API managements to purge: %w", err)
		}

		cognitiveAccounts, err := p.getCognitiveAccountsToPurge(ctx, groupedResources)
		if err != nil {
			return nil, fmt.Errorf("getting cognitive accounts to purge: %w", err)
		}

		logAnalyticsWorkspaces, err := p.getLogAnalyticsWorkspacesToPurge(ctx, groupedResources)
		if err != nil {
			return nil, fmt.Errorf("getting log analytics workspaces to purge: %w", err)
		}

		p.console.StopSpinner(ctx, "", input.StepDone)

		// Prompt for confirmation before deleting resources
		if err := p.promptDeletion(ctx, options, groupedResources, len(resourcesToDelete)); err != nil {
			return nil, err
		}

		p.console.Message(ctx, output.WithGrayFormat("Deleting your resources can take some time.\n"))

		// Force delete Log Analytics Workspaces first if purge is enabled
		// This must happen before deleting resource groups since force delete requires the workspace to exist
		if options.Purge() && len(logAnalyticsWorkspaces) > 0 {
			if err := p.forceDeleteLogAnalyticsWorkspaces(ctx, logAnalyticsWorkspaces); err != nil {
				return nil, fmt.Errorf("force deleting log analytics workspaces: %w", err)
			}
		}

		if err := p.destroyDeployment(ctx, deploymentToDelete); err != nil {
			return nil, fmt.Errorf("deleting resource groups: %w", err)
		}

		keyVaultsPurge := itemToPurge{
			resourceType: "Key Vault",
			count:        len(keyVaults),
			purge: func(skipPurge bool, self *itemToPurge) error {
				return p.purgeKeyVaults(ctx, keyVaults, skipPurge)
			},
		}
		managedHSMsPurge := itemToPurge{
			resourceType: "Managed HSM",
			count:        len(managedHSMs),
			purge: func(skipPurge bool, self *itemToPurge) error {
				return p.purgeManagedHSMs(ctx, managedHSMs, skipPurge)
			},
		}
		appConfigsPurge := itemToPurge{
			resourceType: "App Configuration",
			count:        len(appConfigs),
			purge: func(skipPurge bool, self *itemToPurge) error {
				return p.purgeAppConfigs(ctx, appConfigs, skipPurge)
			},
		}
		aPIManagement := itemToPurge{
			resourceType: "API Management",
			count:        len(apiManagements),
			purge: func(skipPurge bool, self *itemToPurge) error {
				return p.purgeAPIManagement(ctx, apiManagements, skipPurge)
			},
		}

		var purgeItem []itemToPurge
		for _, item := range []itemToPurge{keyVaultsPurge, managedHSMsPurge, appConfigsPurge, aPIManagement} {
			if item.count > 0 {
				purgeItem = append(purgeItem, item)
			}
		}

		// cognitive services are grouped by resource group because the name of the resource group is required to purge
		groupByKind := cognitiveAccountsByKind(cognitiveAccounts)
		for name, cogAccounts := range groupByKind {
			addPurgeItem := itemToPurge{
				resourceType: name,
				count:        len(cogAccounts),
				purge: func(skipPurge bool, self *itemToPurge) error {
					return p.purgeCognitiveAccounts(ctx, self.cognitiveAccounts, skipPurge)
				},
				cognitiveAccounts: groupByKind[name],
			}
			purgeItem = append(purgeItem, addPurgeItem)
		}

		if err := p.purgeItems(ctx, purgeItem, options); err != nil {
			return nil, fmt.Errorf("purging resources: %w", err)
		}
	}

	destroyResult := &provisioning.DestroyResult{
		InvalidatedEnvKeys: slices.Collect(maps.Keys(provisioning.OutputParametersFromArmOutputs(
			compileResult.Template.Outputs,
			azapi.CreateDeploymentOutput(mostRecentDeployment.Outputs),
		))),
	}

	return destroyResult, nil
}

// A local type for adding the resource group to a cognitive account as it is required for purging
type cognitiveAccount struct {
	account       armcognitiveservices.Account
	resourceGroup string
}

// transform a map of resourceGroup and accounts to group by kind in all resource groups but keeping the resource group
// on each account
func cognitiveAccountsByKind(
	accountsByResourceGroup map[string][]armcognitiveservices.Account) map[string][]cognitiveAccount {
	result := make(map[string][]cognitiveAccount)
	for resourceGroup, cogAccounts := range accountsByResourceGroup {
		for _, cogAccount := range cogAccounts {
			kindName := *cogAccount.Kind
			// Replace "FormRecognizer" with "DocumentIntelligence"
			if kindName == "FormRecognizer" {
				kindName = "Document Intelligence"
			}
			_, exists := result[kindName]
			if exists {
				result[kindName] = append(result[kindName], cognitiveAccount{
					account:       cogAccount,
					resourceGroup: resourceGroup,
				})
			} else {
				result[kindName] = []cognitiveAccount{{
					account:       cogAccount,
					resourceGroup: resourceGroup,
				}}
			}
		}
	}
	return result
}

func getDeploymentOptions(deployments []*azapi.ResourceDeployment) []string {
	promptValues := []string{}
	for index, deployment := range deployments {
		optionTitle := fmt.Sprintf("%d. %s (%s)",
			index+1,
			deployment.Name,
			deployment.Timestamp.Local().Format("1/2/2006, 3:04 PM"),
		)
		promptValues = append(promptValues, optionTitle)
	}

	return promptValues
}

func (p *BicepProvider) generateResourcesToDelete(
	ctx context.Context,
	groupedResources map[string][]*azapi.Resource,
) []string {
	lines := []string{"Resource(s) to be deleted:"}

	for resourceGroupName, resources := range groupedResources {
		lines = append(lines, "")

		// Resource Group
		resourceGroupLink := fmt.Sprintf("%s/#@/resource/subscriptions/%s/resourceGroups/%s/overview",
			p.portalUrlBase,
			p.env.GetSubscriptionId(),
			resourceGroupName,
		)

		lines = append(lines,
			fmt.Sprintf("%s %s",
				output.WithHighLightFormat("Resource Group:"),
				output.WithHyperlink(resourceGroupLink, resourceGroupName),
			),
		)

		// Resources in each group
		for _, resource := range resources {
			resourceTypeName, err := p.resourceManager.GetResourceTypeDisplayName(
				ctx,
				p.env.GetSubscriptionId(),
				resource.Id,
				azapi.AzureResourceType(resource.Type),
			)
			if err != nil {
				// Fall back to static lookup if dynamic lookup fails
				resourceTypeName = azapi.GetResourceTypeDisplayName(azapi.AzureResourceType(resource.Type))
			}
			if resourceTypeName == "" {
				continue
			}

			lines = append(lines, fmt.Sprintf("  • %s: %s", resourceTypeName, resource.Name))
		}
	}

	return append(lines, "\n")
}

// promptDeletion prompts the user for confirmation before deleting resources.
// Returns nil if the user confirms, or an error if they deny or an error occurs.
func (p *BicepProvider) promptDeletion(
	ctx context.Context,
	options provisioning.DestroyOptions,
	groupedResources map[string][]*azapi.Resource,
	resourceCount int,
) error {
	if options.Force() {
		return nil
	}

	p.console.MessageUxItem(ctx, &ux.MultilineMessage{
		Lines: p.generateResourcesToDelete(ctx, groupedResources)},
	)
	confirmDestroy, err := p.console.Confirm(ctx, input.ConsoleOptions{
		Message: fmt.Sprintf(
			"Total resources to %s: %d, are you sure you want to continue?",
			output.WithErrorFormat("delete"),
			resourceCount,
		),
		DefaultValue: false,
	})

	if err != nil {
		return fmt.Errorf("prompting for delete confirmation: %w", err)
	}

	if !confirmDestroy {
		return errors.New("user denied delete confirmation")
	}

	return nil
}

// destroyDeployment deletes the azure resources within the deployment and voids the deployment state.
func (p *BicepProvider) destroyDeployment(
	ctx context.Context,
	deployment infra.Deployment,
) error {
	err := async.RunWithProgressE(func(progressMessage azapi.DeleteDeploymentProgress) {
		switch progressMessage.State {
		case azapi.DeleteResourceStateInProgress:
			p.console.ShowSpinner(ctx, progressMessage.Message, input.Step)
		case azapi.DeleteResourceStateSucceeded:
			p.console.StopSpinner(ctx, progressMessage.Message, input.StepDone)
		case azapi.DeleteResourceStateFailed:
			p.console.StopSpinner(ctx, progressMessage.Message, input.StepFailed)
		}
	}, func(progress *async.Progress[azapi.DeleteDeploymentProgress]) error {
		optionsMap, err := convert.ToMap(p.options)
		if err != nil {
			return err
		}

		return deployment.Delete(ctx, optionsMap, progress)
	})

	if err != nil {
		return err
	}

	p.console.Message(ctx, "")

	return nil
}

func itemsCountAsText(items []itemToPurge) string {
	count := len(items)
	if count < 1 {
		log.Panic("calling itemsCountAsText() with empty list.")
	}

	var tokens []string
	for _, item := range items {
		if item.count > 0 {
			tokens = append(tokens, fmt.Sprintf("%d %s", item.count, item.resourceType))
		}
	}

	return ux.ListAsText(tokens)
}

func (p *BicepProvider) purgeItems(
	ctx context.Context,
	items []itemToPurge,
	options provisioning.DestroyOptions,
) error {
	if len(items) == 0 {
		// nothing to purge
		return nil
	}

	skipPurge := false
	if !options.Purge() {

		p.console.MessageUxItem(ctx, &ux.WarningMessage{
			Description: fmt.Sprintf(
				"The following operation will delete %s.",
				itemsCountAsText(items),
			),
		})
		p.console.Message(ctx, fmt.Sprintf(
			"These resources have soft delete enabled allowing them to be recovered for a period or time "+
				"after deletion. During this period, their names may not be reused. In the future, you can use "+
				"the argument %s to skip this confirmation.\n", output.WithHighLightFormat("--purge")))

		purgeItems, err := p.console.Confirm(ctx, input.ConsoleOptions{
			Message: fmt.Sprintf(
				"Would you like to %s these resources instead, allowing their names to be reused?",
				output.WithErrorFormat("permanently delete"),
			),
			DefaultValue: false,
		})
		p.console.Message(ctx, "")

		if err != nil {
			return fmt.Errorf("prompting for confirmation: %w", err)
		}

		if !purgeItems {
			skipPurge = true
		}
	}
	for index, item := range items {
		if err := item.purge(skipPurge, &items[index]); err != nil {
			return fmt.Errorf("failed to purge %s: %w", item.resourceType, err)
		}
	}

	return nil
}

func (p *BicepProvider) getKeyVaults(
	ctx context.Context,
	groupedResources map[string][]*azapi.Resource,
) ([]*keyvault.KeyVault, error) {
	vaults := []*keyvault.KeyVault{}

	for resourceGroup, groupResources := range groupedResources {
		for _, resource := range groupResources {
			if resource.Type == string(azapi.AzureResourceTypeKeyVault) {
				vault, err := p.keyvaultService.GetKeyVault(
					ctx, azure.SubscriptionFromRID(resource.Id), resourceGroup, resource.Name)
				if err != nil {
					return nil, fmt.Errorf("listing key vault %s properties: %w", resource.Name, err)
				}
				vaults = append(vaults, vault)
			}
		}
	}

	return vaults, nil
}

func (p *BicepProvider) getKeyVaultsToPurge(
	ctx context.Context,
	groupedResources map[string][]*azapi.Resource,
) ([]*keyvault.KeyVault, error) {
	vaults, err := p.getKeyVaults(ctx, groupedResources)
	if err != nil {
		return nil, err
	}

	vaultsToPurge := []*keyvault.KeyVault{}
	for _, v := range vaults {
		if v.Properties.EnableSoftDelete && !v.Properties.EnablePurgeProtection {
			vaultsToPurge = append(vaultsToPurge, v)
		}
	}

	return vaultsToPurge, nil
}

func (p *BicepProvider) getManagedHSMs(
	ctx context.Context,
	groupedResources map[string][]*azapi.Resource,
) ([]*azapi.AzCliManagedHSM, error) {
	managedHSMs := []*azapi.AzCliManagedHSM{}

	for resourceGroup, groupResources := range groupedResources {
		for _, resource := range groupResources {
			if resource.Type == string(azapi.AzureResourceTypeManagedHSM) {
				managedHSM, err := p.azapi.GetManagedHSM(
					ctx,
					azure.SubscriptionFromRID(resource.Id),
					resourceGroup,
					resource.Name,
				)
				if err != nil {
					return nil, fmt.Errorf("listing managed hsm %s properties: %w", resource.Name, err)
				}
				managedHSMs = append(managedHSMs, managedHSM)
			}
		}
	}

	return managedHSMs, nil
}

func (p *BicepProvider) getManagedHSMsToPurge(
	ctx context.Context,
	groupedResources map[string][]*azapi.Resource,
) ([]*azapi.AzCliManagedHSM, error) {
	managedHSMs, err := p.getManagedHSMs(ctx, groupedResources)
	if err != nil {
		return nil, err
	}

	managedHSMsToPurge := []*azapi.AzCliManagedHSM{}
	for _, v := range managedHSMs {
		if v.Properties.EnableSoftDelete && !v.Properties.EnablePurgeProtection {
			managedHSMsToPurge = append(managedHSMsToPurge, v)
		}
	}

	return managedHSMsToPurge, nil
}

func (p *BicepProvider) getCognitiveAccountsToPurge(
	ctx context.Context,
	groupedResources map[string][]*azapi.Resource,
) (map[string][]armcognitiveservices.Account, error) {
	result := make(map[string][]armcognitiveservices.Account)

	for resourceGroup, groupResources := range groupedResources {
		cognitiveAccounts := []armcognitiveservices.Account{}
		for _, resource := range groupResources {
			if resource.Type == string(azapi.AzureResourceTypeCognitiveServiceAccount) {
				account, err := p.azapi.GetCognitiveAccount(
					ctx, azure.SubscriptionFromRID(resource.Id), resourceGroup, resource.Name)
				if err != nil {
					return nil, fmt.Errorf("getting cognitive account %s: %w", resource.Name, err)
				}
				cognitiveAccounts = append(cognitiveAccounts, account)
			}
			if len(cognitiveAccounts) > 0 {
				result[resourceGroup] = cognitiveAccounts
			}
		}
	}

	return result, nil
}

// Azure KeyVaults have a "soft delete" functionality (now enabled by default) where a vault may be marked
// such that when it is deleted it can be recovered for a period of time. During that time, the name may
// not be reused.
//
// This means that running `azd provision`, then `azd down` and finally `azd provision`
// again would lead to a deployment error since the vault name is in use.
//
// Since that's behavior we'd like to support, we run a purge operation for each KeyVault after
// it has been deleted.
//
// See
// https://docs.microsoft.com/azure/key-vault/general/key-vault-recovery?tabs=azure-portal#what-are-soft-delete-and-purge-protection
// for more information on this feature.
//
//nolint:lll
func (p *BicepProvider) purgeKeyVaults(
	ctx context.Context,
	keyVaults []*keyvault.KeyVault,
	skip bool,
) error {
	for _, keyVault := range keyVaults {
		err := p.runPurgeAsStep(ctx, "Key Vault", keyVault.Name, func() error {
			return p.keyvaultService.PurgeKeyVault(
				ctx, azure.SubscriptionFromRID(keyVault.Id), keyVault.Name, keyVault.Location)
		}, skip)
		if err != nil {
			return fmt.Errorf("purging key vault %s: %w", keyVault.Name, err)
		}
	}
	return nil
}

func (p *BicepProvider) purgeManagedHSMs(
	ctx context.Context,
	managedHSMs []*azapi.AzCliManagedHSM,
	skip bool,
) error {
	for _, managedHSM := range managedHSMs {
		err := p.runPurgeAsStep(ctx, "Managed HSM", managedHSM.Name, func() error {
			return p.azapi.PurgeManagedHSM(
				ctx, azure.SubscriptionFromRID(managedHSM.Id), managedHSM.Name, managedHSM.Location)
		}, skip)
		if err != nil {
			return fmt.Errorf("purging managed hsm %s: %w", managedHSM.Name, err)
		}
	}
	return nil
}

func (p *BicepProvider) purgeCognitiveAccounts(
	ctx context.Context,
	cognitiveAccounts []cognitiveAccount,
	skip bool,
) error {
	for _, cogAccount := range cognitiveAccounts {
		accountName := cogAccount.account.Name
		if accountName == nil {
			return fmt.Errorf("Cognitive account without a name")
		}
		accountId := cogAccount.account.ID
		if accountId == nil {
			return fmt.Errorf("Cognitive account without an id")
		}
		location := cogAccount.account.Location
		if location == nil {
			return fmt.Errorf("Cognitive account without a location")
		}

		err := p.runPurgeAsStep(ctx, "Cognitive Account", *accountName, func() error {
			return p.azapi.PurgeCognitiveAccount(
				ctx, azure.SubscriptionFromRID(*accountId), *location, cogAccount.resourceGroup, *accountName)
		}, skip)
		if err != nil {
			return fmt.Errorf("purging cognitive account %s: %w", *accountName, err)
		}
	}
	return nil
}

func (p *BicepProvider) runPurgeAsStep(
	ctx context.Context, purgeType, name string, step func() error, skipped bool) error {

	message := fmt.Sprintf("Purging %s: %s", purgeType, output.WithHighLightFormat(name))
	p.console.ShowSpinner(ctx, message, input.Step)
	if skipped {
		p.console.StopSpinner(ctx, message, input.StepSkipped)
		return nil
	}

	err := step()
	p.console.StopSpinner(ctx, message, input.GetStepResultFormat(err))

	return err
}

func (p *BicepProvider) getAppConfigsToPurge(
	ctx context.Context,
	groupedResources map[string][]*azapi.Resource,
) ([]*azapi.AzCliAppConfig, error) {
	configs := []*azapi.AzCliAppConfig{}

	for resourceGroup, groupResources := range groupedResources {
		for _, resource := range groupResources {
			if resource.Type == string(azapi.AzureResourceTypeAppConfig) {
				config, err := p.azapi.GetAppConfig(
					ctx,
					azure.SubscriptionFromRID(resource.Id),
					resourceGroup,
					resource.Name,
				)
				if err != nil {
					return nil, fmt.Errorf("listing app configuration %s properties: %w", resource.Name, err)
				}

				if !config.Properties.EnablePurgeProtection {
					configs = append(configs, config)
				}
			}
		}
	}

	return configs, nil
}

func (p *BicepProvider) getApiManagementsToPurge(
	ctx context.Context,
	groupedResources map[string][]*azapi.Resource,
) ([]*azapi.AzCliApim, error) {
	apims := []*azapi.AzCliApim{}

	for resourceGroup, groupResources := range groupedResources {
		for _, resource := range groupResources {
			if resource.Type == string(azapi.AzureResourceTypeApim) {
				apim, err := p.azapi.GetApim(ctx, azure.SubscriptionFromRID(resource.Id), resourceGroup, resource.Name)
				if err != nil {
					return nil, fmt.Errorf("listing api management service %s properties: %w", resource.Name, err)
				}

				//No filtering needed like it does in key vaults or app configuration
				//as soft-delete happens for all Api Management resources
				apims = append(apims, apim)
			}
		}
	}

	return apims, nil
}

func (p *BicepProvider) getLogAnalyticsWorkspacesToPurge(
	ctx context.Context,
	groupedResources map[string][]*azapi.Resource,
) ([]*azapi.AzCliLogAnalyticsWorkspace, error) {
	workspaces := []*azapi.AzCliLogAnalyticsWorkspace{}

	for resourceGroup, groupResources := range groupedResources {
		for _, resource := range groupResources {
			if resource.Type == string(azapi.AzureResourceTypeLogAnalyticsWorkspace) {
				workspace, err := p.azapi.GetLogAnalyticsWorkspace(
					ctx,
					azure.SubscriptionFromRID(resource.Id),
					resourceGroup,
					resource.Name,
				)
				if err != nil {
					return nil, fmt.Errorf("listing log analytics workspace %s properties: %w", resource.Name, err)
				}

				workspaces = append(workspaces, workspace)
			}
		}
	}

	return workspaces, nil
}

// Azure AppConfigurations have a "soft delete" functionality (now enabled by default) where a configuration store
// may be marked such that when it is deleted it can be recovered for a period of time. During that time,
// the name may not be reused.
//
// This means that running `azd provision`, then `azd down` and finally `azd provision`
// again would lead to a deployment error since the configuration name is in use.
//
// Since that's behavior we'd like to support, we run a purge operation for each AppConfiguration after it has been deleted.
//
// See https://learn.microsoft.com/azure/azure-app-configuration/concept-soft-delete for more information
// on this feature.
func (p *BicepProvider) purgeAppConfigs(
	ctx context.Context,
	appConfigs []*azapi.AzCliAppConfig,
	skip bool,
) error {
	for _, appConfig := range appConfigs {
		err := p.runPurgeAsStep(ctx, "app config", appConfig.Name, func() error {
			return p.azapi.PurgeAppConfig(
				ctx, azure.SubscriptionFromRID(appConfig.Id), appConfig.Name, appConfig.Location)
		}, skip)
		if err != nil {
			return fmt.Errorf("purging app configuration %s: %w", appConfig.Name, err)
		}
	}

	return nil
}

func (p *BicepProvider) purgeAPIManagement(
	ctx context.Context,
	apims []*azapi.AzCliApim,
	skip bool,
) error {
	for _, apim := range apims {
		err := p.runPurgeAsStep(ctx, "apim", apim.Name, func() error {
			return p.azapi.PurgeApim(ctx, azure.SubscriptionFromRID(apim.Id), apim.Name, apim.Location)
		}, skip)
		if err != nil {
			return fmt.Errorf("purging api management service %s: %w", apim.Name, err)
		}
	}

	return nil
}

// Handle Log Analytics Workspaces separately with Force option when purge is enabled.
// Unlike many other resources, Log Analytics Workspaces are not able to be purged after soft-delete
// because purge function only support for purge tables not a whole workspace and force delete must happen
// when their resource group is not deleted, so we must purge them explicitly before deleting the resource
func (p *BicepProvider) forceDeleteLogAnalyticsWorkspaces(
	ctx context.Context,
	workspaces []*azapi.AzCliLogAnalyticsWorkspace,
) error {
	for _, workspace := range workspaces {
		message := fmt.Sprintf("Purging Log Analytics Workspace: %s", output.WithHighLightFormat(workspace.Name))
		p.console.ShowSpinner(ctx, message, input.Step)

		err := p.azapi.PurgeLogAnalyticsWorkspace(
			ctx,
			azure.SubscriptionFromRID(workspace.Id),
			*azure.GetResourceGroupName(workspace.Id),
			workspace.Name,
		)

		p.console.StopSpinner(ctx, message, input.GetStepResultFormat(err))
		if err != nil {
			return fmt.Errorf("purging log analytics workspace %s: %w", workspace.Name, err)
		}
	}
	return nil
}

type loadParametersResult struct {
	parameters     map[string]azure.ArmParameter
	locationParams []string
	// envMapping is a map of parameter name to environment variable names
	// holds information about which parameters are mapped to which env vars for
	// cases like "param": "${env:AZURE_FOO}-${env:AZURE_BAR}", envMapping will
	// contain {"param": ["AZURE_FOO", "AZURE_BAR"]}
	// This information is useful for setting a CI/CD automatically. Each env var
	// will be set to the value of the parameter as variable or secret.
	envMapping map[string][]string
}

// envSubstResult contains the results of environment variable substitution
type envSubstResult struct {
	hasUnsetEnvVar                  bool
	mappedEnvVars                   []string
	parametersMappedToAzureLocation []string
}

// evalParamEnvSubst evaluates environment variable substitution on a single parameter string value.
// It returns the substituted string, env var mapping information, and any error.
func evalParamEnvSubst(
	value string,
	principalId string,
	principalType string,
	paramName string,
	env *environment.Environment,
) (string, envSubstResult, error) {
	result := envSubstResult{}

	replaced, err := envsubst.Eval(value, func(name string) string {
		if name == environment.PrincipalIdEnvVarName {
			return principalId
		}
		if name == environment.PrincipalTypeEnvVarName {
			return principalType
		}
		if name == environment.LocationEnvVarName {
			result.parametersMappedToAzureLocation = append(result.parametersMappedToAzureLocation, paramName)
		}
		// principalId and locations are intentionally excluded from the mapped env vars as
		// they are global env vars
		result.mappedEnvVars = append(result.mappedEnvVars, name)
		if _, isDefined := env.LookupEnv(name); !isDefined {
			result.hasUnsetEnvVar = true
		}
		return env.Getenv(name)
	})
	return replaced, result, err
}

// evalCommandSubstitution evaluates command substitutions (like secretOrRandomPassword) in the given string.
func (p *BicepProvider) evalCommandSubstitution(ctx context.Context, value string) (string, error) {
	if !cmdsubst.ContainsCommandInvocation(value, cmdsubst.SecretOrRandomPasswordCommandName) {
		return value, nil
	}

	cmdExecutor := cmdsubst.NewSecretOrRandomPasswordExecutor(p.keyvaultService, p.env.GetSubscriptionId())
	replaced, err := cmdsubst.Eval(ctx, value, cmdExecutor)
	if err != nil {
		return "", fmt.Errorf("substituting command output: %w", err)
	}
	return replaced, nil
}

// loadParameters reads the parameters file template for environment/module specified by Options,
// doing environment and command substitutions, and returns the values.
func (p *BicepProvider) loadParameters(ctx context.Context, template *azure.ArmTemplate) (loadParametersResult, error) {
	parametersFilename := fmt.Sprintf("%s.parameters.json", p.options.Module)
	paramFilePath := filepath.Join(filepath.Dir(p.path), parametersFilename)
	parametersBytes, err := os.ReadFile(paramFilePath)
	// if the file does not exist, we return an empty parameters map
	// This makes AZD to support deploying bicep modules without parameters file, assuming AZD prompts for all required
	// parameters.
	if os.IsNotExist(err) {
		log.Printf("parameters file %s does not exist, using empty parameters", paramFilePath)
		return loadParametersResult{}, nil
	}
	if err != nil {
		return loadParametersResult{}, fmt.Errorf("reading parameters.json: %w", err)
	}

	principalId, err := p.curPrincipal.CurrentPrincipalId(ctx)
	if err != nil {
		return loadParametersResult{}, fmt.Errorf("fetching current principal id: %w", err)
	}

	principalType, err := p.curPrincipal.CurrentPrincipalType(ctx)
	if err != nil {
		return loadParametersResult{}, fmt.Errorf("fetching current principal type: %w", err)
	}

	var decodedParamsFile azure.ArmParameterFile
	if err := json.Unmarshal(parametersBytes, &decodedParamsFile); err != nil {
		return loadParametersResult{}, fmt.Errorf("error unmarshalling Bicep template parameters: %w", err)
	}

	parametersMappedToAzureLocation := []string{}
	resolvedParams := map[string]azure.ArmParameter{}
	envMapping := map[string][]string{}

	// resolving each parameter to keep track of the name during the resolution.
	// We used to resolve all the file before, supporting env var substitution at any part of the file.
	// We want to support substitution only for the parameter value.
	// We also need to identify which parameters are mapped to AZURE_LOCATION (if any).
	// We also want to exclude parameters mapped to env vars which env var is not set (instead of using empty string).
	for paramName, param := range decodedParamsFile.Parameters {
		paramDef, hasDef := template.Parameters[paramName]
		var paramType provisioning.ParameterType
		if hasDef {
			paramType = provisioning.ParameterTypeFromArmType(paramDef.Type)
		}

		// Path A: Handle complex types (array, object) with string values (like "${MY_ARRAY}")
		if hasDef &&
			(paramType == provisioning.ParameterTypeObject || paramType == provisioning.ParameterTypeArray) &&
			param.KeyVaultReference == nil {
			if stringVal, ok := param.Value.(string); ok {
				// Run envsubst on just the string value, not the full parameter JSON
				// This allows us to validate the substituted value as JSON,
				// and avoid issues with string quoting in the parameters file.
				replaced, substResult, err := evalParamEnvSubst(
					stringVal,
					principalId,
					string(principalType),
					paramName,
					p.env,
				)
				if err != nil {
					return loadParametersResult{}, err
				}

				envMapping[paramName] = substResult.mappedEnvVars
				parametersMappedToAzureLocation = append(
					parametersMappedToAzureLocation, substResult.parametersMappedToAzureLocation...)

				// Omit unset parameters
				if replaced == "" && substResult.hasUnsetEnvVar {
					continue
				}

				// Parse the substituted value as JSON (array/object)
				var jsonValue any
				if err := json.Unmarshal([]byte(replaced), &jsonValue); err != nil {
					return loadParametersResult{}, fmt.Errorf(
						"substituting parameter '%s' (%s): %w: value '%s' is not valid JSON",
						paramName, paramDef.Type, err, replaced)
				}

				// Check for command substitution (like secretOrRandomPassword)
				cmdSubstStr, err := p.evalCommandSubstitution(ctx, replaced)
				if err != nil {
					return loadParametersResult{}, err
				}

				// Re-parse after command substitution if it was applied
				if cmdSubstStr != replaced {
					if err := json.Unmarshal([]byte(cmdSubstStr), &jsonValue); err != nil {
						return loadParametersResult{}, fmt.Errorf(
							"command-substituting parameter '%s' (%s): %w: value '%s' is not valid JSON",
							paramName, paramDef.Type, err, cmdSubstStr)
					}
				}

				resolvedParams[paramName] = azure.ArmParameter{Value: jsonValue}
				continue
			}
		}

		// Path B: Handle all other cases: non-complex types, complex types with non-string values, keyvault refs
		paramBytes, err := json.Marshal(param)
		if err != nil {
			return loadParametersResult{}, fmt.Errorf("error decoding deployment parameter %s: %w", paramName, err)
		}

		replaced, substResult, err := evalParamEnvSubst(
			string(paramBytes),
			principalId,
			string(principalType),
			paramName,
			p.env,
		)
		if err != nil {
			return loadParametersResult{}, fmt.Errorf("substituting environment variables for %s: %w", paramName, err)
		}

		envMapping[paramName] = substResult.mappedEnvVars
		parametersMappedToAzureLocation = append(
			parametersMappedToAzureLocation, substResult.parametersMappedToAzureLocation...)

		// resolve command substitutions like `secretOrRandomPassword`
		replaced, err = p.evalCommandSubstitution(ctx, replaced)
		if err != nil {
			return loadParametersResult{}, err
		}

		var resolvedParam azure.ArmParameter
		if err := json.Unmarshal([]byte(replaced), &resolvedParam); err != nil {
			return loadParametersResult{}, fmt.Errorf("error unmarshalling Bicep template parameters: %w", err)
		}
		if resolvedParam.Value == nil && resolvedParam.KeyVaultReference == nil {
			// ignore parameters that are not set
			continue
		}
		if resolvedParam.Value != nil && resolvedParam.KeyVaultReference != nil {
			return loadParametersResult{}, fmt.Errorf(
				"parameter %s has both a value and a keyvault reference: %w", paramName, err)
		}
		if resolvedParam.KeyVaultReference != nil {
			// parameter defined using a key vault reference. AZD does not validate the key vault reference
			// if there is an issue with it, the deployment will fail.
			resolvedParams[paramName] = resolvedParam
			continue
		}

		// Ignore string parameters which are empty b/c they are mapped to an undefined env var
		if stringValue, isString := resolvedParam.Value.(string); isString {
			// After previous checks, we know resolvedParam.Value is not nil
			if stringValue == "" && substResult.hasUnsetEnvVar {
				// parameter is empty and has an unset env var
				continue
			}
		}

		// all other cases here represent a valid resolved parameter
		resolvedParams[paramName] = resolvedParam
	}

	return loadParametersResult{
		parameters:     resolvedParams,
		locationParams: parametersMappedToAzureLocation,
		envMapping:     envMapping,
	}, nil
}

type compiledBicepParamResult struct {
	TemplateJson   string `json:"templateJson"`
	ParametersJson string `json:"parametersJson"`
}

type compileBicepResult struct {
	RawArmTemplate azure.RawArmTemplate
	Template       azure.ArmTemplate
	// Parameters are populated either by compiling a .bicepparam (automatically) or by azd after compiling a .bicep file.
	Parameters azure.ArmParameters
}

// compileBicep compiles the bicep module at the given path and returns the compiled ARM template and parameters.
// The results of the compilation are cached in memory.
func (p *BicepProvider) compileBicep(ctx context.Context) (*compileBicepResult, error) {
	if p.compileBicepMemoryCache != nil {
		return p.compileBicepMemoryCache, nil
	}

	var compiled string
	var parameters azure.ArmParameters

	if p.mode == bicepparamMode {
		azdEnv := p.env.Environ()
		// append principalID (not stored to .env by default). For non-bicepparam, principalId is resolved
		// without looking at .env
		if _, exists := p.env.LookupEnv(environment.PrincipalIdEnvVarName); !exists {
			currentPrincipalId, err := p.curPrincipal.CurrentPrincipalId(ctx)
			if err != nil {
				return nil, fmt.Errorf("fetching current principal id for bicepparam compilation: %w", err)
			}
			azdEnv = append(azdEnv, fmt.Sprintf("%s=%s", environment.PrincipalIdEnvVarName, currentPrincipalId))
		}
		compiledResult, err := p.bicepCli.BuildBicepParam(ctx, p.path, azdEnv)
		if err != nil {
			return nil, fmt.Errorf("failed to compile bicepparam template: %w", err)
		}
		compiled = compiledResult.Compiled

		var bicepParamOutput compiledBicepParamResult
		if err := json.Unmarshal([]byte(compiled), &bicepParamOutput); err != nil {
			log.Printf("failed unmarshalling compiled bicepparam (err: %v), template contents:\n%s", err, compiled)
			return nil, fmt.Errorf("failed unmarshalling arm template from json: %w", err)
		}
		compiled = bicepParamOutput.TemplateJson
		var params azure.ArmParameterFile
		if err := json.Unmarshal([]byte(bicepParamOutput.ParametersJson), &params); err != nil {
			log.Printf("failed unmarshalling compiled bicepparam parameters(err: %v), template contents:\n%s", err, compiled)
			return nil, fmt.Errorf("failed unmarshalling arm parameters template from json: %w", err)
		}
		parameters = params.Parameters
	} else {
		res, err := p.bicepCli.Build(ctx, p.path)
		if err != nil {
			return nil, fmt.Errorf("failed to compile bicep template: %w", err)
		}
		compiled = res.Compiled
	}

	rawTemplate := azure.RawArmTemplate(compiled)

	var template azure.ArmTemplate
	if err := json.Unmarshal(rawTemplate, &template); err != nil {
		log.Printf("failed unmarshalling compiled arm template to JSON (err: %v), template contents:\n%s", err, compiled)
		return nil, fmt.Errorf("failed unmarshalling arm template from json: %w", err)
	}

	// update user-defined parameters
	for paramKey, param := range template.Parameters {
		paramRef := param.Ref
		isUserDefinedType := paramRef != ""
		if isUserDefinedType {
			definitionKeyName, err := definitionName(paramRef)
			if err != nil {
				return nil, err
			}
			paramDefinition, findDefinition := template.Definitions[definitionKeyName]
			if !findDefinition {
				return nil, fmt.Errorf("did not find definition for parameter type: %s", definitionKeyName)
			}
			template.Parameters[paramKey] = azure.ArmTemplateParameterDefinition{
				// Take this values from the parameter definition
				Type:                 paramDefinition.Type,
				AllowedValues:        paramDefinition.AllowedValues,
				Properties:           paramDefinition.Properties,
				AdditionalProperties: paramDefinition.AdditionalProperties,
				// Azd combines Metadata from type definition and original parameter
				// This allows to definitions to use azd-metadata on user-defined types and then add more properties
				// to metadata or override something just for one parameter
				Metadata: combineMetadata(paramDefinition.Metadata, param.Metadata),
				// Keep this values from the original parameter
				DefaultValue: param.DefaultValue,
				// Note: Min/MaxLength and Min/MaxValue can't be used on user-defined types. No need to handle it here.
			}
		}
	}

	// outputs resolves just the type. Value and Metadata should persist
	for outputKey, output := range template.Outputs {
		paramRef := output.Ref
		isUserDefinedType := paramRef != ""
		if isUserDefinedType {
			definitionKeyName, err := definitionName(paramRef)
			if err != nil {
				return nil, err
			}
			paramDefinition, findDefinition := template.Definitions[definitionKeyName]
			if !findDefinition {
				return nil, fmt.Errorf("did not find definition for parameter type: %s", definitionKeyName)
			}
			template.Outputs[outputKey] = azure.ArmTemplateOutput{
				Type:     paramDefinition.Type,
				Value:    output.Value,
				Metadata: output.Metadata,
			}
		}
	}
	p.compileBicepMemoryCache = &compileBicepResult{
		RawArmTemplate: rawTemplate,
		Template:       template,
		Parameters:     parameters,
	}

	return p.compileBicepMemoryCache, nil
}

func combineMetadata(base map[string]json.RawMessage, override map[string]json.RawMessage) map[string]json.RawMessage {
	if base == nil && override == nil {
		return nil
	}

	if override == nil {
		return base
	}

	// final map is expected to be at least the same size as the base
	finalMetadata := make(map[string]json.RawMessage, len(base))

	for key, data := range base {
		finalMetadata[key] = data
	}

	for key, data := range override {
		finalMetadata[key] = data
	}

	return finalMetadata
}

func definitionName(typeDefinitionRef string) (string, error) {
	// We typically expect `#/definitions/<name>` or `/definitions/<name>`, but loosely, we simply take
	// `<name>` as the value of the last separated element.
	definitionKeyNameTokens := strings.Split(typeDefinitionRef, "/")
	definitionKeyNameTokensLen := len(definitionKeyNameTokens)
	if definitionKeyNameTokensLen < 1 {
		return "", fmt.Errorf("failed resolving user defined parameter type: %s", typeDefinitionRef)
	}
	return definitionKeyNameTokens[definitionKeyNameTokensLen-1], nil
}

// Converts a Bicep parameters file to a generic [provisioning.Deployment].
func (p *BicepProvider) convertToDeployment(bicepTemplate azure.ArmTemplate) provisioning.Deployment {
	result := provisioning.Deployment{}
	parameters := make(map[string]provisioning.InputParameter)
	outputs := make(map[string]provisioning.OutputParameter)

	for key, param := range bicepTemplate.Parameters {
		parameters[key] = provisioning.InputParameter{
			Type:         string(provisioning.ParameterTypeFromArmType(param.Type)),
			DefaultValue: param.DefaultValue,
		}
	}

	for key, param := range bicepTemplate.Outputs {
		outputs[key] = provisioning.OutputParameter{
			Type:  provisioning.ParameterTypeFromArmType(param.Type),
			Value: param.Value,
		}
	}

	result.Parameters = parameters
	result.Outputs = outputs

	return result
}

func (p *BicepProvider) validatePreflight(
	ctx context.Context,
	target infra.Deployment,
	armTemplate azure.RawArmTemplate,
	armParameters azure.ArmParameters,
	tags map[string]*string,
	options map[string]any,
) error {
	return target.ValidatePreflight(ctx, armTemplate, armParameters, tags, options)
}

// Deploys the specified Bicep module and parameters with the selected provisioning scope (subscription vs resource group)
func (p *BicepProvider) deployModule(
	ctx context.Context,
	target infra.Deployment,
	armTemplate azure.RawArmTemplate,
	armParameters azure.ArmParameters,
	tags map[string]*string,
	options map[string]any,
) (*azapi.ResourceDeployment, error) {
	return target.Deploy(ctx, armTemplate, armParameters, tags, options)
}

// inputsParameter generates and updates input parameters for the Azure Resource Manager (ARM) template.
// It takes an existingInputs map that contains the current input values for each resource, and an autoGenParameters map
// that contains information about the input parameters to be generated.
// The method iterates over the autoGenParameters map and checks if each input parameter already exists in the existingInputs
// map.
// If an input parameter does not exist, a new value is generated and added to the existingInputs map.
// The method returns an azure.ArmParameterValue struct that contains the updated existingInputs map, a boolean indicating
// whether new inputs were written, and an error if any occurred during the generation of input values.
func inputsParameter(
	existingInputs map[string]map[string]any, autoGenParameters map[string]map[string]azure.AutoGenInput) (
	inputsParameter azure.ArmParameter, inputsUpdated bool, err error) {
	wroteNewInput := false

	for inputResource, inputResourceInfo := range autoGenParameters {
		existingRecordsForResource := make(map[string]any)
		if current, exists := existingInputs[inputResource]; exists {
			existingRecordsForResource = current
		}
		for inputName, inputInfo := range inputResourceInfo {
			if _, has := existingRecordsForResource[inputName]; !has {
				val, err := password.Generate(password.GenerateConfig{
					Length:     inputInfo.Length,
					NoLower:    inputInfo.NoLower,
					NoUpper:    inputInfo.NoUpper,
					NoNumeric:  inputInfo.NoNumeric,
					NoSpecial:  inputInfo.NoSpecial,
					MinLower:   inputInfo.MinLower,
					MinUpper:   inputInfo.MinUpper,
					MinNumeric: inputInfo.MinNumeric,
					MinSpecial: inputInfo.MinSpecial,
				},
				)
				if err != nil {
					return inputsParameter, inputsUpdated, fmt.Errorf("generating value for input %s: %w", inputName, err)
				}
				existingRecordsForResource[inputName] = val
				wroteNewInput = true
			}
		}
		existingInputs[inputResource] = existingRecordsForResource
	}

	return azure.ArmParameter{
		Value: existingInputs,
	}, wroteNewInput, nil
}

// ensureParameters validates that all parameters from the template are defined.
// Parameters values can be defined in the parameters file (main.parameters.json). This file supports mapping values to
// environment variables.
// If a parameter is not defined in the parameters file AND there is not a default value defined in the template for it,
// AZD will prompt the user for a value.
// Parameters mapped to env var ${AZURE_LOCATION} are identified as location parameters for AZD during prompting. AZD will
// prompt just for one location and save the value in AZD's .env file as AZURE_LOCATION and the value is used for all
// parameters mapped to that env var.
// AZD supports resolving env vars with a default value defined in the parameters file using the syntax
// ${AZURE_LOCATION=defaultValue}. If the env var is not set, the default value will be used.
func (p *BicepProvider) ensureParameters(
	ctx context.Context,
	template azure.ArmTemplate,
) (azure.ArmParameters, error) {
	//snapshot the AZURE_LOCATIOn in azd env if it is set in System env
	locationSystemEnv, hasLocation := os.LookupEnv(environment.LocationEnvVarName)
	_, hasAzdLocation := p.env.Dotenv()[environment.LocationEnvVarName]
	if hasLocation && !hasAzdLocation && locationSystemEnv != "" {
		p.env.SetLocation(locationSystemEnv)
		if err := p.envManager.Save(ctx, p.env); err != nil {
			return nil, fmt.Errorf("saving location to .env: %w", err)
		}
	}
	// using loadParameters to resolve the parameters file (usually main.parameters.json)
	// parameters with a mapping to env vars are resolved.
	// Parameters mapped to env vars that are not set in the environment are removed from the parameters file
	parametersResult, err := p.loadParameters(ctx, &template)
	if err != nil {
		return nil, fmt.Errorf("resolving bicep parameters file: %w", err)
	}
	parameters := parametersResult.parameters
	locationParameters := parametersResult.locationParams

	if len(template.Parameters) == 0 {
		return azure.ArmParameters{}, nil
	}
	configuredParameters := make(azure.ArmParameters, len(template.Parameters))

	sortedKeys := slices.Sorted(maps.Keys(template.Parameters))

	configModified := false

	var parameterPrompts []struct {
		key   string
		param azure.ArmTemplateParameterDefinition
	}

	// make all parameters mapped to AZURE_LOCATION env var to be location parameters
	for _, key := range sortedKeys {
		param := template.Parameters[key]
		if slices.Contains(locationParameters, key) {
			azdMetadata, hasAzdMetadata := param.AzdMetadata()
			if !hasAzdMetadata {
				azdMetadata = azure.AzdMetadata{
					Type: to.Ptr(azure.AzdMetadataTypeLocation),
				}
			}
			if azdMetadata.Type == nil {
				azdMetadata.Type = to.Ptr(azure.AzdMetadataTypeLocation)
			}
			if azdMetadata.Type != nil && *azdMetadata.Type != azure.AzdMetadataTypeLocation {
				return nil, fmt.Errorf(
					"parameter %s is mapped to AZURE_LOCATION but has a different azd metadata type: %s."+
						"Parameters mapped to AZURE_LOCATION can only be typed as location",
					key,
					*azdMetadata.Type)
			}
			mdBytes, err := json.Marshal(azdMetadata)
			if err != nil {
				return nil, fmt.Errorf("marshalling azd metadata: %w", err)
			}
			if param.Metadata == nil {
				param.Metadata = map[string]json.RawMessage{"azd": mdBytes}
			} else {
				param.Metadata["azd"] = mdBytes
			}
			template.Parameters[key] = param
		}
	}

	for _, key := range sortedKeys {
		param := template.Parameters[key]
		parameterType := provisioning.ParameterTypeFromArmType(param.Type)
		azdMetadata, hasMetadata := param.AzdMetadata()

		// If a value is explicitly configured via a parameters file, use it.
		if v, has := parameters[key]; has {
			// Directly pass through Key Vault references without prompting.
			if v.KeyVaultReference != nil {
				configuredParameters[key] = azure.ArmParameter{
					KeyVaultReference: v.KeyVaultReference,
				}
				continue
			}

			paramValue := armParameterFileValue(parameterType, v.Value, param.DefaultValue)
			if paramValue != nil {

				if stringValue, isString := paramValue.(string); isString && param.Secure() {
					// For secure parameters using a string value, azd checks if the string is an Azure Key Vault Secret
					// and if yes, it fetches the secret value from the Key Vault.
					if keyvault.IsAzureKeyVaultSecret(stringValue) {
						paramValue, err = p.keyvaultService.SecretFromAkvs(ctx, stringValue)
						if err != nil {
							return nil, err
						}
					}
				}

				configuredParameters[key] = azure.ArmParameter{
					Value: paramValue,
				}
				continue
			}
		}

		// If this parameter has a default, then there is no need for us to configure it.
		if param.DefaultValue != nil {
			continue
		}

		if param.Nullable != nil && *param.Nullable {
			// If the parameter is nullable, we can skip prompting for it.
			continue
		}

		// This required parameter was not in parameters file - see if we stored a value in config from an earlier
		// prompt and if so use it.
		configKey := fmt.Sprintf("infra.parameters.%s", key)

		if v, has := p.env.Config.Get(configKey); has {
			if isValueAssignableToParameterType(parameterType, v) {
				configuredParameters[key] = azure.ArmParameter{
					Value: v,
				}
				continue
			} else {
				// The saved value is no longer valid (perhaps the user edited their template to change the type of a)
				// parameter and then re-ran `azd provision`. Forget the saved value (if we can) and prompt for a new one.
				_ = p.env.Config.Unset("infra.parameters.%s")
			}
		}

		// If the parameter is tagged with {type: "generate"}, skip prompting.
		// We generate it once, then save to config for next attempts.`.
		if hasMetadata && parameterType == provisioning.ParameterTypeString && azdMetadata.Type != nil &&
			*azdMetadata.Type == azure.AzdMetadataTypeGenerate {

			// - generate once
			genValue, err := autoGenerate(key, azdMetadata)
			if err != nil {
				return nil, err
			}
			configuredParameters[key] = azure.ArmParameter{
				Value: genValue,
			}
			mustSetParamAsConfig(key, genValue, p.env.Config, param.Secure())
			configModified = true
			continue
		}

		// No saved value for this required parameter, we'll need to prompt for it.
		parameterPrompts = append(parameterPrompts, struct {
			key   string
			param azure.ArmTemplateParameterDefinition
		}{key: key, param: param})
	}

	if len(parameterPrompts) > 0 {
		if p.console.SupportsPromptDialog() {

			dialog := input.PromptDialog{
				Title: "Configure required deployment parameters",
				Description: "The following parameters are required for deployment. " +
					"Provide values for each parameter. They will be saved for future deployments.",
			}

			for _, prompt := range parameterPrompts {
				dialog.Prompts = append(dialog.Prompts, p.promptDialogItemForParameter(prompt.key, prompt.param))
			}

			values, err := p.console.PromptDialog(ctx, dialog)
			if err != nil {
				return nil, fmt.Errorf("prompting for values: %w", err)
			}

			for _, prompt := range parameterPrompts {
				key := prompt.key
				value := values[prompt.key]
				mustSetParamAsConfig(key, value, p.env.Config, prompt.param.Secure())
				configModified = true
				configuredParameters[key] = azure.ArmParameter{
					Value: value,
				}
			}
		} else {
			for _, prompt := range parameterPrompts {
				key := prompt.key

				// Otherwise, prompt for the value.
				value, err := p.promptForParameter(ctx, key, prompt.param, locationParameters)
				if err != nil {
					return nil, fmt.Errorf("prompting for value: %w", err)
				}

				if key != "location" {
					// location param is special.
					// It is not persisted in config, it is set in the .env directly
					mustSetParamAsConfig(key, value, p.env.Config, prompt.param.Secure())
				}
				configModified = true
				configuredParameters[key] = azure.ArmParameter{
					Value: value,
				}
			}
		}
	}

	if configModified {
		if err := p.envManager.Save(ctx, p.env); err != nil {
			return nil, fmt.Errorf("saving prompt values: %w", err)
		}
	}
	return configuredParameters, nil
}

var configInfraParametersKey = "infra.parameters."

// mustSetParamAsConfig sets the specified key-value pair in the given config.Config object.
// If the isSecured flag is set to true, the value is set as a secret using config.SetSecret,
// otherwise it is set using config.Set.
// If an error occurs while setting the value, the function panics with a warning message.
func mustSetParamAsConfig(key string, value any, config config.Config, isSecured bool) {
	configKey := configInfraParametersKey + key

	if !isSecured {
		if err := config.Set(configKey, value); err != nil {
			log.Panicf("failed setting config value: %v", err)
		}
		return
	}

	secretString, castOk := value.(string)
	if !castOk {
		log.Panic("tried to set a non-string as secret. This is not supported.")
	}
	if err := config.SetSecret(configKey, secretString); err != nil {
		log.Panicf("failed setting a secret in config: %v", err)
	}
}

// Convert the ARM parameters file value into a value suitable for deployment
func armParameterFileValue(paramType provisioning.ParameterType, value any, defaultValue any) any {
	// Quick return if the value being converted is not a string
	if value == nil || reflect.TypeOf(value).Kind() != reflect.String {
		return value
	}

	// Relax the handling of bool and number types to accept convertible strings
	switch paramType {
	case provisioning.ParameterTypeBoolean:
		if val, ok := value.(string); ok {
			if boolVal, err := strconv.ParseBool(val); err == nil {
				return boolVal
			}
		}
	case provisioning.ParameterTypeNumber:
		if val, ok := value.(string); ok {
			if intVal, err := strconv.ParseInt(val, 10, 64); err == nil {
				return intVal
			}
		}
	case provisioning.ParameterTypeString:
		// Use Cases
		// 1. Non-empty input value, return input value (no prompt)
		// 2. Empty input value and no default - return nil (prompt user)
		// 3. Empty input value and non-empty default - return empty input string (no prompt)
		paramVal, paramValid := value.(string)
		if paramValid && paramVal != "" {
			return paramVal
		}

		defaultVal, hasDefault := defaultValue.(string)
		if hasDefault && paramValid && paramVal != defaultVal {
			return paramVal
		}
	default:
		return value
	}

	return nil
}

func isValueAssignableToParameterType(paramType provisioning.ParameterType, value any) bool {
	switch paramType {
	case provisioning.ParameterTypeArray:
		_, ok := value.([]any)
		return ok
	case provisioning.ParameterTypeBoolean:
		_, ok := value.(bool)
		return ok
	case provisioning.ParameterTypeNumber:
		switch t := value.(type) {
		case int, int8, int16, int32, int64:
			return true
		case uint, uint8, uint16, uint32, uint64:
			return true
		case float32:
			return float64(t) == math.Trunc(float64(t))
		case float64:
			return t == math.Trunc(t)
		case json.Number:
			_, err := t.Int64()
			return err == nil
		default:
			return false
		}
	case provisioning.ParameterTypeObject:
		_, ok := value.(map[string]any)
		return ok
	case provisioning.ParameterTypeString:
		_, ok := value.(string)
		return ok
	default:
		panic(fmt.Sprintf("unexpected type: %v", paramType))
	}
}

// NewBicepProvider creates a new instance of a Bicep Infra provider
func NewBicepProvider(
	azapi *azapi.AzureClient,
	bicepCli *bicep.Cli,
	resourceService *azapi.ResourceService,
	resourceManager infra.ResourceManager,
	deploymentManager *infra.DeploymentManager,
	envManager environment.Manager,
	env *environment.Environment,
	console input.Console,
	prompters prompt.Prompter,
	curPrincipal provisioning.CurrentPrincipalIdProvider,
	keyvaultService keyvault.KeyVaultService,
	cloud *cloud.Cloud,
	subscriptionManager *account.SubscriptionsManager,
	aiModelService *ai.AiModelService,
) provisioning.Provider {
	return &BicepProvider{
		envManager:          envManager,
		env:                 env,
		console:             console,
		azapi:               azapi,
		bicepCli:            bicepCli,
		resourceService:     resourceService,
		resourceManager:     resourceManager,
		deploymentManager:   deploymentManager,
		prompters:           prompters,
		curPrincipal:        curPrincipal,
		keyvaultService:     keyvaultService,
		portalUrlBase:       cloud.PortalUrlBase,
		subscriptionManager: subscriptionManager,
		aiModelService:      aiModelService,
	}
}

func (p *BicepProvider) Parameters(ctx context.Context) ([]provisioning.Parameter, error) {
	compileResult, err := p.compileBicep(ctx)
	if err != nil {
		return nil, fmt.Errorf("creating template: %w", err)
	}
	// templateParameters are the parameters defined in the bicep template. We know when a parameter is secured,
	// its type and its default value from this definition.
	templateParameters := compileResult.Template.Parameters

	// parametersInfo contains the env vars mappings (from a parameters file). bicepparam is not supported yet.
	parametersInfo, err := p.loadParameters(ctx, &compileResult.Template)
	if err != nil {
		return nil, fmt.Errorf("loading parameters: %w", err)
	}

	// resolved parameters contains the final value for the parameters after evaluating. The final value can be
	// from env var, from default value or from user input (prompt).
	resolvedParams, err := p.ensureParameters(ctx, compileResult.Template)
	if err != nil {
		return nil, fmt.Errorf("resolving parameters: %w", err)
	}

	provisionParameters := []provisioning.Parameter{}
	for key, param := range templateParameters {
		if _, usingParam := resolvedParams[key]; !usingParam {
			// No resolved param for this parameter definition.
			continue
		}
		_, isPrompt := p.env.Config.Get(fmt.Sprintf("infra.parameters.%s", key))
		singleMapping := len(parametersInfo.envMapping[key]) == 1
		usingEnvVarMapping := false
		if singleMapping {
			envValue, defined := p.env.LookupEnv(parametersInfo.envMapping[key][0])
			usingEnvVarMapping = singleMapping && defined && envValue == fmt.Sprintf("%v", resolvedParams[key].Value)
		}
		provisionParameters = append(provisionParameters, provisioning.Parameter{
			Name:          key,
			Secret:        param.Secure(),
			Value:         resolvedParams[key].Value,
			EnvVarMapping: parametersInfo.envMapping[key],
			// No env var mapping and param is persisted in env config infra.parameters means local prompt only
			// If user set an env var mapping after a local prompt, the env var overrides the value persisted in config
			// which turns local prompt false
			LocalPrompt:        isPrompt && !usingEnvVarMapping,
			UsingEnvVarMapping: usingEnvVarMapping,
		})
	}

	return provisionParameters, nil
}
