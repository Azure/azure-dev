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
	"github.com/azure/azure-dev/cli/azd/pkg/account"
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
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/bicep"
	"github.com/drone/envsubst"
)

const (
	defaultModule = "main"
	defaultPath   = "infra"
)

type deploymentDetails struct {
	CompiledBicep *compileBicepResult
	// Target is the unique resource in azure that represents the deployment that will happen. A target can be scoped to
	// either subscriptions, or resource groups.
	Target infra.Deployment
}

// BicepProvider exposes infrastructure provisioning using Azure Bicep templates
type BicepProvider struct {
	env                   *environment.Environment
	envManager            environment.Manager
	projectPath           string
	options               provisioning.Options
	console               input.Console
	bicepCli              *bicep.Cli
	azapi                 *azapi.AzureClient
	resourceService       *azapi.ResourceService
	deploymentManager     *infra.DeploymentManager
	prompters             prompt.Prompter
	curPrincipal          provisioning.CurrentPrincipalIdProvider
	ignoreDeploymentState bool
	// compileBicepResult is cached to avoid recompiling the same bicep file multiple times in the same azd run.
	compileBicepMemoryCache *compileBicepResult
	keyvaultService         keyvault.KeyVaultService
	portalUrlBase           string
}

// Name gets the name of the infra provider
func (p *BicepProvider) Name() string {
	return "Bicep"
}

func (p *BicepProvider) RequiredExternalTools() []tools.ExternalTool {
	return []tools.ExternalTool{}
}

// Initialize initializes provider state from the options.
// It also calls EnsureEnv, which ensures the client-side state is ready for provisioning.
func (p *BicepProvider) Initialize(ctx context.Context, projectPath string, options provisioning.Options) error {
	p.projectPath = projectPath
	p.options = options
	if p.options.Module == "" {
		p.options.Module = defaultModule
	}
	if p.options.Path == "" {
		p.options.Path = defaultPath
	}

	requiredTools := p.RequiredExternalTools()
	if err := tools.EnsureInstalled(ctx, requiredTools...); err != nil {
		return err
	}
	p.ignoreDeploymentState = options.IgnoreDeploymentState

	p.console.ShowSpinner(ctx, "Initialize bicep provider", input.Step)
	err := p.EnsureEnv(ctx)
	p.console.StopSpinner(ctx, "", input.Step)
	return err
}

var ErrEnsureEnvPreReqBicepCompileFailed = errors.New("")

// EnsureEnv ensures that the environment is in a provision-ready state with required values set, prompting the user if
// values are unset. This also requires that the Bicep module can be compiled.
func (p *BicepProvider) EnsureEnv(ctx context.Context) error {
	modulePath := p.modulePath()

	// for .bicepparam, we first prompt for environment values before calling compiling bicepparam file
	// which can reference these values
	if isBicepParamFile(modulePath) {
		if err := provisioning.EnsureSubscriptionAndLocation(ctx, p.envManager, p.env, p.prompters, nil); err != nil {
			return err
		}
	}

	compileResult, compileErr := p.compileBicep(ctx, modulePath)
	if compileErr != nil {
		return fmt.Errorf("%w%w", ErrEnsureEnvPreReqBicepCompileFailed, compileErr)
	}

	// for .bicep, azd must load a parameters.json file and create the ArmParameters
	if isBicepFile(modulePath) {
		var filterLocation = func(loc account.Location) bool {
			if locationParam, defined := compileResult.Template.Parameters["location"]; defined {
				if locationParam.AllowedValues != nil {
					return slices.IndexFunc(*locationParam.AllowedValues, func(allowedValue any) bool {
						allowedValueString, goodCast := allowedValue.(string)
						return goodCast && loc.Name == allowedValueString
					}) != -1
				}
			}
			return true
		}

		err := provisioning.EnsureSubscriptionAndLocation(ctx, p.envManager, p.env, p.prompters, filterLocation)
		if err != nil {
			return err
		}

		if _, err := p.ensureParameters(ctx, compileResult.Template); err != nil {
			return err
		}
	}

	scope, err := compileResult.Template.TargetScope()
	if err != nil {
		return err
	}

	if scope == azure.DeploymentScopeResourceGroup {
		if p.env.Getenv(environment.ResourceGroupEnvVarName) == "" {
			rgName, err := p.prompters.PromptResourceGroup(ctx)
			if err != nil {
				return err
			}

			p.env.DotenvSet(environment.ResourceGroupEnvVarName, rgName)
			if err := p.envManager.Save(ctx, p.env); err != nil {
				return fmt.Errorf("saving resource group name: %w", err)
			}
		}
	}

	return nil
}

func (p *BicepProvider) LastDeployment(ctx context.Context) (*azapi.ResourceDeployment, error) {
	modulePath := p.modulePath()
	compileResult, err := p.compileBicep(ctx, modulePath)
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

	modulePath := p.modulePath()
	if _, err := os.Stat(modulePath); err == nil {
		compileResult, err := p.compileBicep(ctx, modulePath)
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

	deployments, err := p.deploymentManager.CompletedDeployments(ctx, scope, p.env.Name(), options.Hint())
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

	state.Outputs = p.createOutputParameters(
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

const bicepFileExtension = ".bicep"
const bicepparamFileExtension = ".bicepparam"

func isBicepFile(modulePath string) bool {
	return filepath.Ext(modulePath) == bicepFileExtension
}

func isBicepParamFile(modulePath string) bool {
	return filepath.Ext(modulePath) == bicepparamFileExtension
}

// Plans the infrastructure provisioning
func (p *BicepProvider) plan(ctx context.Context) (*deploymentDetails, error) {
	p.console.ShowSpinner(ctx, "Creating a deployment plan", input.Step)

	modulePath := p.modulePath()
	compileResult, err := p.compileBicep(ctx, modulePath)
	if err != nil {
		return nil, fmt.Errorf("creating template: %w", err)
	}

	// for .bicep, azd must load a parameters.json file and create the ArmParameters
	if isBicepFile(modulePath) {
		configuredParameters, err := p.ensureParameters(ctx, compileResult.Template)
		if err != nil {
			return nil, err
		}
		compileResult.Parameters = configuredParameters
	}

	deploymentScope, err := compileResult.Template.TargetScope()
	if err != nil {
		return nil, err
	}

	target, err := p.deploymentFromScopeType(deploymentScope)
	if err != nil {
		return nil, err
	}

	return &deploymentDetails{
		CompiledBicep: compileResult,
		Target:        target,
	}, nil
}

func (p *BicepProvider) deploymentFromScopeType(deploymentScopeType azure.DeploymentScope) (infra.Deployment, error) {
	deploymentName := p.deploymentManager.GenerateDeploymentName(p.env.Name())

	if deploymentScopeType == azure.DeploymentScopeSubscription {
		scope := p.deploymentManager.SubscriptionScope(p.env.GetSubscriptionId(), p.env.GetLocation())
		return infra.NewSubscriptionDeployment(
			scope,
			deploymentName,
		), nil
	} else if deploymentScopeType == azure.DeploymentScopeResourceGroup {
		scope := p.deploymentManager.ResourceGroupScope(
			p.env.GetSubscriptionId(),
			p.env.Getenv(environment.ResourceGroupEnvVarName),
		)
		return infra.NewResourceGroupDeployment(scope, deploymentName), nil
	}
	return nil, fmt.Errorf("unsupported scope: %s", deploymentScopeType)
}

// deploymentState returns the latests deployment if it is the same as the deployment within deploymentData or an error
// otherwise.
func (p *BicepProvider) deploymentState(
	ctx context.Context,
	deploymentData *deploymentDetails,
	currentParamsHash string,
) (*azapi.ResourceDeployment, error) {

	p.console.ShowSpinner(ctx, "Comparing deployment state", input.Step)
	prevDeploymentResult, err := p.latestDeploymentResult(ctx, deploymentData.Target)
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
		deploymentData.CompiledBicep.RawArmTemplate,
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
	deployments, err := p.deploymentManager.CompletedDeployments(ctx, scope, p.env.Name(), "")
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

	bicepDeploymentData, err := p.plan(ctx)
	if err != nil {
		return nil, err
	}

	deployment, err := p.convertToDeployment(bicepDeploymentData.CompiledBicep.Template)
	if err != nil {
		return nil, err
	}

	// parameters hash is required for doing deployment state validation check but also to set the hash
	// after a successful deployment.
	currentParamsHash, parametersHashErr := parametersHash(
		bicepDeploymentData.CompiledBicep.Template.Parameters, bicepDeploymentData.CompiledBicep.Parameters)
	if parametersHashErr != nil {
		// fail to hash parameters won't stop the operation. It only disables deployment state and recording parameters hash
		logDS("%s", parametersHashErr.Error())
	}

	if !p.ignoreDeploymentState && parametersHashErr == nil {
		deploymentState, err := p.deploymentState(ctx, bicepDeploymentData, currentParamsHash)
		if err == nil {
			deployment.Outputs = p.createOutputParameters(
				bicepDeploymentData.CompiledBicep.Template.Outputs,
				azapi.CreateDeploymentOutput(deploymentState.Outputs),
			)

			return &provisioning.DeployResult{
				Deployment:    deployment,
				SkippedReason: provisioning.DeploymentStateSkipped,
			}, nil
		}
		logDS("%s", err.Error())
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
		progressDisplay := p.deploymentManager.ProgressDisplay(bicepDeploymentData.Target)
		// Make initial delay shorter to be more responsive in displaying initial progress
		initialDelay := 3 * time.Second
		regularDelay := 10 * time.Second
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
					log.Printf("error while reporting progress: %s", err.Error())
				}

				timer.Reset(regularDelay)
			}
		}
	}()

	// Start the deployment
	p.console.ShowSpinner(ctx, "Creating/Updating resources", input.Step)

	deploymentTags := map[string]*string{
		azure.TagKeyAzdEnvName: to.Ptr(p.env.Name()),
	}
	if parametersHashErr == nil {
		deploymentTags[azure.TagKeyAzdDeploymentStateParamHashName] = to.Ptr(currentParamsHash)
	}

	optionsMap, err := convert.ToMap(p.options)
	if err != nil {
		return nil, err
	}

	deployResult, err := p.deployModule(
		ctx,
		bicepDeploymentData.Target,
		bicepDeploymentData.CompiledBicep.RawArmTemplate,
		bicepDeploymentData.CompiledBicep.Parameters,
		deploymentTags,
		optionsMap,
	)
	if err != nil {
		return nil, err
	}

	deployment.Outputs = p.createOutputParameters(
		bicepDeploymentData.CompiledBicep.Template.Outputs,
		azapi.CreateDeploymentOutput(deployResult.Outputs),
	)

	return &provisioning.DeployResult{
		Deployment: deployment,
	}, nil
}

// Preview runs deploy using the what-if argument
func (p *BicepProvider) Preview(ctx context.Context) (*provisioning.DeployPreviewResult, error) {
	bicepDeploymentData, err := p.plan(ctx)
	if err != nil {
		return nil, err
	}

	p.console.ShowSpinner(ctx, "Generating infrastructure preview", input.Step)

	targetScope := bicepDeploymentData.Target
	deployPreviewResult, err := targetScope.DeployPreview(
		ctx,
		bicepDeploymentData.CompiledBicep.RawArmTemplate,
		bicepDeploymentData.CompiledBicep.Parameters,
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
		resourceAfter := change.After.(map[string]interface{})

		changes = append(changes, &provisioning.DeploymentPreviewChange{
			ChangeType: provisioning.ChangeType(*change.ChangeType),
			ResourceId: provisioning.Resource{
				Id: *change.ResourceID,
			},
			ResourceType: resourceAfter["type"].(string),
			Name:         resourceAfter["name"].(string),
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
	modulePath := p.modulePath()
	p.console.ShowSpinner(ctx, "Discovering resources to delete...", input.Step)
	defer p.console.StopSpinner(ctx, "", input.StepDone)
	compileResult, err := p.compileBicep(ctx, modulePath)
	if err != nil {
		return nil, fmt.Errorf("creating template: %w", err)
	}

	scope, err := p.scopeForTemplate(compileResult.Template)
	if err != nil {
		return nil, fmt.Errorf("computing deployment scope: %w", err)
	}

	completedDeployments, err := p.deploymentManager.CompletedDeployments(ctx, scope, p.env.Name(), "")
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

	if len(groupedResources) == 0 {
		return nil, fmt.Errorf("%w, '%s'", infra.ErrDeploymentResourcesNotFound, deploymentToDelete.Name())
	}

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

	p.console.StopSpinner(ctx, "", input.StepDone)
	if err := p.destroyDeploymentWithConfirmation(
		ctx,
		options,
		deploymentToDelete,
		groupedResources,
		len(resourcesToDelete),
	); err != nil {
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

	destroyResult := &provisioning.DestroyResult{
		InvalidatedEnvKeys: slices.Collect(maps.Keys(p.createOutputParameters(
			compileResult.Template.Outputs,
			azapi.CreateDeploymentOutput(mostRecentDeployment.Outputs),
		))),
	}

	// Since we have deleted the resource group, add AZURE_RESOURCE_GROUP to the list of invalidated env vars
	// so it will be removed from the .env file.
	if _, ok := scope.(*infra.ResourceGroupScope); ok {
		destroyResult.InvalidatedEnvKeys = append(
			destroyResult.InvalidatedEnvKeys, environment.ResourceGroupEnvVarName,
		)
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

// resourceGroupsToDelete collects the resource groups from an existing deployment which should be removed as part of a
// destroy operation.
func resourceGroupsToDelete(deployment *azapi.ResourceDeployment) []string {
	// NOTE: it's possible for a deployment to list a resource group more than once. We're only interested in the
	// unique set.
	resourceGroups := map[string]struct{}{}

	if deployment.ProvisioningState == azapi.DeploymentProvisioningStateSucceeded {
		// For a successful deployment, we can use the output resources property to see the resource groups that were
		// provisioned from this.
		for _, resourceId := range deployment.Resources {
			if resourceId != nil && resourceId.ID != nil {
				resId, err := arm.ParseResourceID(*resourceId.ID)
				if err == nil && resId.ResourceGroupName != "" {
					resourceGroups[resId.ResourceGroupName] = struct{}{}
				}
			}
		}
	} else {
		// For a failed deployment, the `outputResources` field is not populated. Instead, we assume that any resource
		// groups which this deployment itself deployed into should be deleted. This matches what a deployment likes
		// for the common pattern of having a subscription level deployment which allocates a set of resource groups
		// and then does nested deployments into them.
		for _, dependency := range deployment.Dependencies {
			if *dependency.ResourceType == string(azapi.AzureResourceTypeDeployment) {
				for _, dependent := range dependency.DependsOn {
					if *dependent.ResourceType == arm.ResourceGroupResourceType.String() {
						resourceGroups[*dependent.ResourceName] = struct{}{}
					}
				}
			}

		}
	}

	return slices.Collect(maps.Keys(resourceGroups))
}

func (p *BicepProvider) generateResourcesToDelete(groupedResources map[string][]*azapi.Resource) []string {
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
			resourceTypeName := azapi.GetResourceTypeDisplayName(azapi.AzureResourceType(resource.Type))
			if resourceTypeName == "" {
				continue
			}

			lines = append(lines, fmt.Sprintf("  • %s: %s", resourceTypeName, resource.Name))
		}
	}

	return append(lines, "\n")
}

// Deletes the azure resources within the deployment
func (p *BicepProvider) destroyDeploymentWithConfirmation(
	ctx context.Context,
	options provisioning.DestroyOptions,
	deployment infra.Deployment,
	groupedResources map[string][]*azapi.Resource,
	resourceCount int,
) error {
	if !options.Force() {
		p.console.MessageUxItem(ctx, &ux.MultilineMessage{
			Lines: p.generateResourcesToDelete(groupedResources)},
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
	}

	p.console.Message(ctx, output.WithGrayFormat("Deleting your resources can take some time.\n"))

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

func (p *BicepProvider) mapBicepTypeToInterfaceType(s string) provisioning.ParameterType {
	switch s {
	case "String", "string", "secureString", "securestring":
		return provisioning.ParameterTypeString
	case "Bool", "bool":
		return provisioning.ParameterTypeBoolean
	case "Int", "int":
		return provisioning.ParameterTypeNumber
	case "Object", "object", "secureObject", "secureobject":
		return provisioning.ParameterTypeObject
	case "Array", "array":
		return provisioning.ParameterTypeArray
	default:
		panic(fmt.Sprintf("unexpected bicep type: '%s'", s))
	}
}

// Creates a normalized view of the azure output parameters and resolves inconsistencies in the output parameter name
// casings.
func (p *BicepProvider) createOutputParameters(
	templateOutputs azure.ArmTemplateOutputs,
	azureOutputParams map[string]azapi.AzCliDeploymentOutput,
) map[string]provisioning.OutputParameter {
	canonicalOutputCasings := make(map[string]string, len(templateOutputs))

	for key := range templateOutputs {
		canonicalOutputCasings[strings.ToLower(key)] = key
	}

	outputParams := make(map[string]provisioning.OutputParameter, len(azureOutputParams))

	for key, azureParam := range azureOutputParams {
		var paramName string
		canonicalCasing, found := canonicalOutputCasings[strings.ToLower(key)]
		if found {
			paramName = canonicalCasing
		} else {
			// To support BYOI (bring your own infrastructure) scenarios we will default to UPPER when canonical casing
			// is not found in the parameters file to workaround strange azure behavior with OUTPUT values that look
			// like `azurE_RESOURCE_GROUP`
			paramName = strings.ToUpper(key)
		}

		outputParams[paramName] = provisioning.OutputParameter{
			Type:  p.mapBicepTypeToInterfaceType(azureParam.Type),
			Value: azureParam.Value,
		}
	}

	return outputParams
}

// loadParameters reads the parameters file template for environment/module specified by Options,
// doing environment and command substitutions, and returns the values.
func (p *BicepProvider) loadParameters(ctx context.Context) (map[string]azure.ArmParameterValue, error) {
	parametersFilename := fmt.Sprintf("%s.parameters.json", p.options.Module)
	parametersRoot := p.options.Path

	if !filepath.IsAbs(parametersRoot) {
		parametersRoot = filepath.Join(p.projectPath, parametersRoot)
	}

	paramFilePath := filepath.Join(parametersRoot, parametersFilename)
	parametersBytes, err := os.ReadFile(paramFilePath)
	if err != nil {
		return nil, fmt.Errorf("reading parameters.json: %w", err)
	}

	principalId, err := p.curPrincipal.CurrentPrincipalId(ctx)
	if err != nil {
		return nil, fmt.Errorf("fetching current principal id: %w", err)
	}

	replaced, err := envsubst.Eval(string(parametersBytes), func(name string) string {
		if name == environment.PrincipalIdEnvVarName {
			return principalId
		}

		return p.env.Getenv(name)
	})
	if err != nil {
		return nil, fmt.Errorf("substituting environment variables inside parameter file: %w", err)
	}

	if cmdsubst.ContainsCommandInvocation(replaced, cmdsubst.SecretOrRandomPasswordCommandName) {
		cmdExecutor := cmdsubst.NewSecretOrRandomPasswordExecutor(p.keyvaultService, p.env.GetSubscriptionId())
		replaced, err = cmdsubst.Eval(ctx, replaced, cmdExecutor)
		if err != nil {
			return nil, fmt.Errorf("substituting command output inside parameter file: %w", err)
		}
	}

	var armParameters azure.ArmParameterFile
	if err := json.Unmarshal([]byte(replaced), &armParameters); err != nil {
		return nil, fmt.Errorf("error unmarshalling Bicep template parameters: %w", err)
	}

	return armParameters.Parameters, nil
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
func (p *BicepProvider) compileBicep(
	ctx context.Context, modulePath string,
) (*compileBicepResult, error) {
	if p.compileBicepMemoryCache != nil {
		return p.compileBicepMemoryCache, nil
	}

	var compiled string
	var parameters azure.ArmParameters

	if isBicepParamFile(modulePath) {
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
		compiledResult, err := p.bicepCli.BuildBicepParam(ctx, modulePath, azdEnv)
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
		res, err := p.bicepCli.Build(ctx, modulePath)
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

// Converts a Bicep parameters file to a generic provisioning template
func (p *BicepProvider) convertToDeployment(bicepTemplate azure.ArmTemplate) (*provisioning.Deployment, error) {
	template := provisioning.Deployment{}
	parameters := make(map[string]provisioning.InputParameter)
	outputs := make(map[string]provisioning.OutputParameter)

	for key, param := range bicepTemplate.Parameters {
		parameters[key] = provisioning.InputParameter{
			Type:         string(p.mapBicepTypeToInterfaceType(param.Type)),
			DefaultValue: param.DefaultValue,
		}
	}

	for key, param := range bicepTemplate.Outputs {
		outputs[key] = provisioning.OutputParameter{
			Type:  p.mapBicepTypeToInterfaceType(param.Type),
			Value: param.Value,
		}
	}

	template.Parameters = parameters
	template.Outputs = outputs

	return &template, nil
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

// Returns either the bicep or bicepparam module file located in the infrastructure root.
// The bicepparam file is preferred over bicep file.
func (p *BicepProvider) modulePath() string {
	infraRoot := p.options.Path
	moduleName := p.options.Module

	if !filepath.IsAbs(infraRoot) {
		infraRoot = filepath.Join(p.projectPath, infraRoot)
	}

	// Check if there's a <moduleName>.bicepparam first. It will be preferred over a <moduleName>.bicep
	moduleFilename := moduleName + bicepparamFileExtension
	moduleFilePath := filepath.Join(infraRoot, moduleFilename)
	if _, err := os.Stat(moduleFilePath); err == nil {
		return moduleFilePath
	}

	// fallback to .bicep
	moduleFilename = moduleName + bicepFileExtension
	return filepath.Join(infraRoot, moduleFilename)
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
	inputsParameter azure.ArmParameterValue, inputsUpdated bool, err error) {
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

	return azure.ArmParameterValue{
		Value: existingInputs,
	}, wroteNewInput, nil
}

// Ensures the provisioning parameters are valid and prompts the user for input as needed
func (p *BicepProvider) ensureParameters(
	ctx context.Context,
	template azure.ArmTemplate,
) (azure.ArmParameters, error) {
	parameters, err := p.loadParameters(ctx)
	if err != nil {
		return nil, fmt.Errorf("resolving bicep parameters file: %w", err)
	}

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

	for _, key := range sortedKeys {
		param := template.Parameters[key]
		parameterType := p.mapBicepTypeToInterfaceType(param.Type)
		azdMetadata, hasMetadata := param.AzdMetadata()

		// If a value is explicitly configured via a parameters file, use it.
		// unless the parameter value inference is nil/empty
		if v, has := parameters[key]; has {
			paramValue := armParameterFileValue(parameterType, v.Value, param.DefaultValue)

			if paramValue != nil {
				needForDeployParameter := hasMetadata &&
					azdMetadata.Type != nil &&
					*azdMetadata.Type == azure.AzdMetadataTypeNeedForDeploy
				if needForDeployParameter && paramValue == "" && param.DefaultValue != nil {
					// Parameters with needForDeploy metadata don't support overriding with empty values when a default
					// value is present. If the value is empty, we'll use the default value instead.
					defValue, castOk := param.DefaultValue.(string)
					if castOk {
						paramValue = defValue
					}
				}
				configuredParameters[key] = azure.ArmParameterValue{
					Value: paramValue,
				}
				if needForDeployParameter {
					mustSetParamAsConfig(key, paramValue, p.env.Config, param.Secure())
					configModified = true
				}
				continue
			}
		}

		// If this parameter has a default, then there is no need for us to configure it.
		if param.DefaultValue != nil {
			continue
		}

		// This required parameter was not in parameters file - see if we stored a value in config from an earlier
		// prompt and if so use it.
		configKey := fmt.Sprintf("infra.parameters.%s", key)

		if v, has := p.env.Config.Get(configKey); has {
			if isValueAssignableToParameterType(parameterType, v) {
				configuredParameters[key] = azure.ArmParameterValue{
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
			configuredParameters[key] = azure.ArmParameterValue{
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
				configuredParameters[key] = azure.ArmParameterValue{
					Value: value,
				}
			}
		} else {
			for _, prompt := range parameterPrompts {
				key := prompt.key

				// Otherwise, prompt for the value.
				value, err := p.promptForParameter(ctx, key, prompt.param)
				if err != nil {
					return nil, fmt.Errorf("prompting for value: %w", err)
				}

				mustSetParamAsConfig(key, value, p.env.Config, prompt.param.Secure())
				configModified = true
				configuredParameters[key] = azure.ArmParameterValue{
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
	deploymentManager *infra.DeploymentManager,
	envManager environment.Manager,
	env *environment.Environment,
	console input.Console,
	prompters prompt.Prompter,
	curPrincipal provisioning.CurrentPrincipalIdProvider,
	keyvaultService keyvault.KeyVaultService,
	cloud *cloud.Cloud,
) provisioning.Provider {
	return &BicepProvider{
		envManager:        envManager,
		env:               env,
		console:           console,
		azapi:             azapi,
		bicepCli:          bicepCli,
		resourceService:   resourceService,
		deploymentManager: deploymentManager,
		prompters:         prompters,
		curPrincipal:      curPrincipal,
		keyvaultService:   keyvaultService,
		portalUrlBase:     cloud.PortalUrlBase,
	}
}
