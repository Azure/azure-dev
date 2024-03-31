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
	"math"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/cognitiveservices/armcognitiveservices"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"github.com/azure/azure-dev/cli/azd/pkg/account"
	"github.com/azure/azure-dev/cli/azd/pkg/alpha"
	"github.com/azure/azure-dev/cli/azd/pkg/azapi"
	"github.com/azure/azure-dev/cli/azd/pkg/azure"
	"github.com/azure/azure-dev/cli/azd/pkg/cmdsubst"
	"github.com/azure/azure-dev/cli/azd/pkg/convert"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/infra"
	. "github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/keyvault"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/output/ux"
	"github.com/azure/azure-dev/cli/azd/pkg/password"
	"github.com/azure/azure-dev/cli/azd/pkg/prompt"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/azcli"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/bicep"
	"github.com/benbjohnson/clock"
	"github.com/drone/envsubst"
	"golang.org/x/exp/maps"
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
	options               Options
	console               input.Console
	bicepCli              bicep.BicepCli
	azCli                 azcli.AzCli
	deploymentsService    azapi.Deployments
	deploymentOperations  azapi.DeploymentOperations
	prompters             prompt.Prompter
	curPrincipal          CurrentPrincipalIdProvider
	alphaFeatureManager   *alpha.FeatureManager
	clock                 clock.Clock
	ignoreDeploymentState bool
	// compileBicepResult is cached to avoid recompiling the same bicep file multiple times in the same azd run.
	compileBicepMemoryCache *compileBicepResult
	// prevent resolving parameters multiple times in the same azd run.
	ensureParamsInMemoryCache azure.ArmParameters
	keyvaultService           keyvault.KeyVaultService

	portalUrlBase string
}

var ErrResourceGroupScopeNotSupported = fmt.Errorf(
	"resource group scoped deployments are currently under alpha support and need to be explicitly enabled."+
		" Run `%s` to enable this feature.", alpha.GetEnableCommand(ResourceGroupDeploymentFeature),
)

// Name gets the name of the infra provider
func (p *BicepProvider) Name() string {
	return "Bicep"
}

func (p *BicepProvider) RequiredExternalTools() []tools.ExternalTool {
	return []tools.ExternalTool{}
}

func (p *BicepProvider) Initialize(ctx context.Context, projectPath string, options Options) error {
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

// EnsureEnv ensures that the environment is in a provision-ready state with required values set, prompting the user if
// values are unset.
//
// An environment is considered to be in a provision-ready state if it contains both an AZURE_SUBSCRIPTION_ID and
// AZURE_LOCATION value. Additionally, for resource group scoped deployments, an AZURE_RESOURCE_GROUP value is required.
func (p *BicepProvider) EnsureEnv(ctx context.Context) error {
	modulePath := p.modulePath()
	compileResult, compileErr := p.compileBicep(ctx, modulePath)
	if compileErr != nil {
		log.Printf("Unable to compile bicep module for initializing environment. error: %v", compileErr)
		log.Printf("Initializing environment w/o arm template info.")
	}

	if err := EnsureSubscriptionAndLocation(ctx, p.envManager, p.env, p.prompters, func(loc account.Location) bool {
		// compileResult can be nil if the infra folder is missing and azd couldn't get a template information.
		// A template information can be used to apply filters to the initial values (like location).
		// But if there's not template, azd will continue with azd env init.
		if compileResult == nil {
			return true
		}
		if locationParam, defined := compileResult.Template.Parameters["location"]; defined {
			if locationParam.AllowedValues != nil {
				return slices.IndexFunc(*locationParam.AllowedValues, func(allowedValue any) bool {
					allowedValueString, goodCast := allowedValue.(string)
					return goodCast && loc.Name == allowedValueString
				}) != -1
			}
		}
		return true
	}); err != nil {
		return err
	}

	// If there's not template, just behave as if we are in a subscription scope (and don't ask about
	// AZURE_RESOURCE_GROUP). Future operations which try to use the infrastructure may fail, but that's ok. These
	// failures will have reasonable error messages.
	//
	// We want to handle the case where the provider can `Initialize` even without a template, because we do this
	// in a few of our end to end telemetry tests to speed things up.
	if compileErr != nil {
		return nil
	}

	// prompt parameters during initialization and ignore any errors.
	// This strategy takes advantage of the bicep compilation from initialization and allows prompting for required inputs
	_, _ = p.ensureParameters(ctx, compileResult.Template)

	scope, err := compileResult.Template.TargetScope()
	if err != nil {
		return err
	}

	if scope == azure.DeploymentScopeResourceGroup {
		if !p.alphaFeatureManager.IsEnabled(ResourceGroupDeploymentFeature) {
			return ErrResourceGroupScopeNotSupported
		}

		p.console.WarnForFeature(ctx, ResourceGroupDeploymentFeature)

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

func (p *BicepProvider) LastDeployment(ctx context.Context) (*armresources.DeploymentExtended, error) {
	modulePath := p.modulePath()
	compileResult, err := p.compileBicep(ctx, modulePath)
	if err != nil {
		return nil, fmt.Errorf("compiling bicep template: %w", err)
	}

	scope, err := p.scopeForTemplate(ctx, compileResult.Template)
	if err != nil {
		return nil, fmt.Errorf("computing deployment scope: %w", err)
	}

	return p.latestDeploymentResult(ctx, scope)
}

func (p *BicepProvider) State(ctx context.Context, options *StateOptions) (*StateResult, error) {
	if options == nil {
		options = &StateOptions{}
	}

	var err error
	spinnerMessage := "Loading Bicep template"
	// TODO: Report progress, "Loading Bicep template"
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

		scope, err = p.scopeForTemplate(ctx, compileResult.Template)
		if err != nil {
			return nil, fmt.Errorf("computing deployment scope: %w", err)
		}

		outputs = compileResult.Template.Outputs
	} else if errors.Is(err, os.ErrNotExist) {
		// To support BYOI (bring your own infrastructure)
		// We need to support the case where there template does not contain an `infra` folder.
		scope, scopeErr = p.inferScopeFromEnv(ctx)
		if scopeErr != nil {
			return nil, fmt.Errorf("computing deployment scope: %w", err)
		}

		outputs = azure.ArmTemplateOutputs{}
	}

	// TODO: Report progress, "Retrieving Azure deployment"
	spinnerMessage = "Retrieving Azure deployment"
	p.console.ShowSpinner(ctx, spinnerMessage, input.Step)

	var deployment *armresources.DeploymentExtended

	deployments, err := p.findCompletedDeployments(ctx, p.env.Name(), scope, options.Hint())
	p.console.StopSpinner(ctx, "", input.StepDone)

	if err != nil {
		p.console.StopSpinner(ctx, spinnerMessage, input.StepFailed)
		return nil, fmt.Errorf("retrieving deployment: %w", err)
	} else {
		p.console.StopSpinner(ctx, "", input.StepDone)
	}

	if len(deployments) > 1 {
		deploymentOptions := getDeploymentOptions(ctx, deployments)

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

	azdDeployment, err := p.createDeploymentFromArmDeployment(scope, *deployment.Name)
	if err != nil {
		return nil, err
	}

	p.console.MessageUxItem(ctx, &ux.DoneMessage{
		Message: fmt.Sprintf("Retrieving Azure deployment (%s)", output.WithHighLightFormat(*deployment.Name)),
	})

	state := State{}
	state.Resources = make([]Resource, len(deployment.Properties.OutputResources))

	for idx, res := range deployment.Properties.OutputResources {
		state.Resources[idx] = Resource{
			Id: *res.ID,
		}
	}

	state.Outputs = p.createOutputParameters(
		outputs,
		azapi.CreateDeploymentOutput(deployment.Properties.Outputs),
	)

	p.console.MessageUxItem(ctx, &ux.DoneMessage{
		Message: fmt.Sprintf("Updated %d environment variables", len(state.Outputs)),
	})

	p.console.Message(ctx, fmt.Sprintf(
		"\nPopulated environment from Azure infrastructure deployment: %s",
		output.WithHyperlink(azdDeployment.OutputsUrl(), *deployment.Name),
	))

	return &StateResult{
		State: &state,
	}, nil
}

func (p *BicepProvider) createDeploymentFromArmDeployment(
	scope infra.Scope,
	deploymentName string,
) (infra.Deployment, error) {
	switch scope.(type) {
	case *infra.ResourceGroupScope:
		return infra.NewResourceGroupDeployment(
			p.deploymentsService,
			p.deploymentOperations,
			p.env.GetSubscriptionId(),
			p.env.Getenv(environment.ResourceGroupEnvVarName),
			deploymentName,
			p.portalUrlBase,
		), nil
	case *infra.SubscriptionScope:
		return infra.NewSubscriptionDeployment(
			p.deploymentsService,
			p.deploymentOperations,
			p.env.GetLocation(),
			p.env.GetSubscriptionId(),
			deploymentName,
			p.portalUrlBase,
		), nil
	default:
		return nil, errors.New("unsupported deployment scope")
	}
}

var ResourceGroupDeploymentFeature = alpha.MustFeatureKey("resourceGroupDeployments")

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
	// TODO: Report progress, "Compiling Bicep template"
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

	target, err := p.deploymentScope(deploymentScope)
	if err != nil {
		return nil, err
	}

	return &deploymentDetails{
		CompiledBicep: compileResult,
		Target:        target,
	}, nil
}

func (p *BicepProvider) deploymentScope(deploymentScope azure.DeploymentScope) (infra.Deployment, error) {
	if deploymentScope == azure.DeploymentScopeSubscription {
		return infra.NewSubscriptionDeployment(
			p.deploymentsService,
			p.deploymentOperations,
			p.env.GetLocation(),
			p.env.GetSubscriptionId(),
			deploymentNameForEnv(p.env.Name(), p.clock),
			p.portalUrlBase,
		), nil
	} else if deploymentScope == azure.DeploymentScopeResourceGroup {
		return infra.NewResourceGroupDeployment(
			p.deploymentsService,
			p.deploymentOperations,
			p.env.GetSubscriptionId(),
			p.env.Getenv(environment.ResourceGroupEnvVarName),
			deploymentNameForEnv(p.env.Name(), p.clock),
			p.portalUrlBase,
		), nil
	}
	return nil, fmt.Errorf("unsupported scope: %s", deploymentScope)
}

// cArmDeploymentNameLengthMax is the maximum length of the name of a deployment in ARM.
const cArmDeploymentNameLengthMax = 64

// deploymentNameForEnv creates a name to use for the deployment object for a given environment. It appends the current
// unix time to the environment name (separated by a hyphen) to provide a unique name for each deployment. If the resulting
// name is longer than the ARM limit, the longest suffix of the name under the limit is returned.
func deploymentNameForEnv(envName string, clock clock.Clock) string {
	name := fmt.Sprintf("%s-%d", envName, clock.Now().Unix())
	if len(name) <= cArmDeploymentNameLengthMax {
		return name
	}

	return name[len(name)-cArmDeploymentNameLengthMax:]
}

// deploymentState returns the latests deployment if it is the same as the deployment within deploymentData or an error
// otherwise.
func (p *BicepProvider) deploymentState(
	ctx context.Context,
	deploymentData *deploymentDetails,
	currentParamsHash string) (*armresources.DeploymentExtended, error) {

	p.console.ShowSpinner(ctx, "Comparing deployment state", input.Step)
	prevDeploymentResult, err := p.latestDeploymentResult(ctx, deploymentData.Target)
	if err != nil {
		return nil, fmt.Errorf("deployment state error: %w", err)
	}

	// State is invalid if the last deployment was not succeeded
	// This is currently safe because we rely on latestDeploymentResult which
	// relies on findCompletedDeployments which filters to only Failed and Succeeded
	if *prevDeploymentResult.Properties.ProvisioningState != armresources.ProvisioningStateSucceeded {
		return nil, fmt.Errorf("last deployment failed.")
	}

	var templateHash string
	createHashResult, err := p.deploymentsService.CalculateTemplateHash(
		ctx, p.env.GetSubscriptionId(), deploymentData.CompiledBicep.RawArmTemplate)
	if err != nil {
		return nil, fmt.Errorf("can't get hash from current template: %w", err)
	}

	if createHashResult.TemplateHash != nil {
		templateHash = *createHashResult.TemplateHash
	}

	if !prevDeploymentEqualToCurrent(ctx, prevDeploymentResult, templateHash, currentParamsHash) {
		return nil, fmt.Errorf("deployment state has changed")
	}

	return prevDeploymentResult, nil
}

// latestDeploymentResult looks and finds a previous deployment for the current azd project.
func (p *BicepProvider) latestDeploymentResult(
	ctx context.Context,
	scope infra.Scope,
) (*armresources.DeploymentExtended, error) {
	deployments, err := p.findCompletedDeployments(ctx, p.env.Name(), scope, "")
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
func prevDeploymentEqualToCurrent(
	ctx context.Context, prev *armresources.DeploymentExtended, templateHash, paramsHash string) bool {
	if prev == nil {
		logDS("No previous deployment.")
		return false
	}

	if prev.Tags == nil {
		logDS("No previous deployment params tags")
		return false
	}

	prevTemplateHash := convert.ToValueWithDefault(prev.Properties.TemplateHash, "")
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
func (p *BicepProvider) Deploy(ctx context.Context) (*DeployResult, error) {

	if p.ignoreDeploymentState {
		logDS("Azure Deployment State is disabled by --ignore-ads arg.")
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
		logDS(parametersHashErr.Error())
	}

	if !p.ignoreDeploymentState && parametersHashErr == nil {
		deploymentState, err := p.deploymentState(ctx, bicepDeploymentData, currentParamsHash)
		if err == nil {
			deployment.Outputs = p.createOutputParameters(
				bicepDeploymentData.CompiledBicep.Template.Outputs,
				azapi.CreateDeploymentOutput(deploymentState.Properties.Outputs),
			)

			return &DeployResult{
				Deployment:    deployment,
				SkippedReason: DeploymentStateSkipped,
			}, nil
		}
		logDS(err.Error())
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
		resourceManager := infra.NewAzureResourceManager(p.azCli, p.deploymentOperations)
		progressDisplay := NewProvisioningProgressDisplay(resourceManager, p.console, bicepDeploymentData.Target)
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
	deployResult, err := p.deployModule(
		ctx,
		bicepDeploymentData.Target,
		bicepDeploymentData.CompiledBicep.RawArmTemplate,
		bicepDeploymentData.CompiledBicep.Parameters,
		deploymentTags,
	)
	if err != nil {
		return nil, err
	}

	deployment.Outputs = p.createOutputParameters(
		bicepDeploymentData.CompiledBicep.Template.Outputs,
		azapi.CreateDeploymentOutput(deployResult.Properties.Outputs),
	)

	return &DeployResult{
		Deployment: deployment,
	}, nil
}

// Preview runs deploy using the what-if argument
func (p *BicepProvider) Preview(ctx context.Context) (*DeployPreviewResult, error) {
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

	var changes []*DeploymentPreviewChange
	for _, change := range deployPreviewResult.Properties.Changes {
		resourceAfter := change.After.(map[string]interface{})

		changes = append(changes, &DeploymentPreviewChange{
			ChangeType: ChangeType(*change.ChangeType),
			ResourceId: Resource{
				Id: *change.ResourceID,
			},
			ResourceType: resourceAfter["type"].(string),
			Name:         resourceAfter["name"].(string),
		})
	}

	return &DeployPreviewResult{
		Preview: &DeploymentPreview{
			Status: *deployPreviewResult.Status,
			Properties: &DeploymentPreviewProperties{
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

func (p *BicepProvider) scopeForTemplate(ctx context.Context, t azure.ArmTemplate) (infra.Scope, error) {
	deploymentScope, err := t.TargetScope()
	if err != nil {
		return nil, err
	}

	if deploymentScope == azure.DeploymentScopeSubscription {
		return infra.NewSubscriptionScope(
			p.deploymentsService, p.deploymentOperations, p.env.GetSubscriptionId()), nil
	} else if deploymentScope == azure.DeploymentScopeResourceGroup {
		return infra.NewResourceGroupScope(
			p.deploymentsService,
			p.deploymentOperations,
			p.env.GetSubscriptionId(),
			p.env.Getenv(environment.ResourceGroupEnvVarName),
		), nil
	} else {
		return nil, fmt.Errorf("unsupported deployment scope: %s", deploymentScope)
	}
}

func (p *BicepProvider) inferScopeFromEnv(ctx context.Context) (infra.Scope, error) {
	if resourceGroup, has := p.env.LookupEnv(environment.ResourceGroupEnvVarName); has {
		return infra.NewResourceGroupScope(
			p.deploymentsService,
			p.deploymentOperations,
			p.env.GetSubscriptionId(),
			resourceGroup,
		), nil
	} else {
		return infra.NewSubscriptionScope(
			p.deploymentsService,
			p.deploymentOperations,
			p.env.GetSubscriptionId(),
		), nil
	}
}

const cEmptySubDeployTemplate = `{
	"$schema": "https://schema.management.azure.com/schemas/2018-05-01/subscriptionDeploymentTemplate.json#",
	"contentVersion": "1.0.0.0",
	"parameters": {},
	"variables": {},
	"resources": [],
	"outputs": {}
  }`

const cEmptyResourceGroupDeployTemplate = `{
	"$schema": "https://schema.management.azure.com/schemas/2019-04-01/deploymentTemplate.json#",
	"contentVersion": "1.0.0.0",
	"parameters": {},
	"variables": {},
	"resources": [],
	"outputs": {}
  }`

// Destroys the specified deployment by deleting all azure resources, resource groups & deployments that are referenced.
func (p *BicepProvider) Destroy(ctx context.Context, options DestroyOptions) (*DestroyResult, error) {
	modulePath := p.modulePath()
	// TODO: Report progress, "Compiling Bicep template"
	compileResult, err := p.compileBicep(ctx, modulePath)
	if err != nil {
		return nil, fmt.Errorf("creating template: %w", err)
	}

	scope, err := p.scopeForTemplate(ctx, compileResult.Template)
	if err != nil {
		return nil, fmt.Errorf("computing deployment scope: %w", err)
	}

	targetScope, err := compileResult.Template.TargetScope()
	if err != nil {
		return nil, err
	}
	deployScope, err := p.deploymentScope(targetScope)
	if err != nil {
		return nil, err
	}

	// TODO: Report progress, "Fetching resource groups"
	deployments, err := p.findCompletedDeployments(ctx, p.env.Name(), scope, "")
	if err != nil {
		return nil, err
	}

	rgsFromDeployment := resourceGroupsToDelete(deployments[0])

	// TODO: Report progress, "Fetching resources"
	groupedResources, err := p.getAllResourcesToDelete(ctx, rgsFromDeployment)
	if err != nil {
		return nil, fmt.Errorf("getting resources to delete: %w", err)
	}

	allResources := []azcli.AzCliResource{}
	for _, groupResources := range groupedResources {
		allResources = append(allResources, groupResources...)
	}

	// TODO: Report progress, "Getting Key Vaults to purge"
	keyVaults, err := p.getKeyVaultsToPurge(ctx, groupedResources)
	if err != nil {
		return nil, fmt.Errorf("getting key vaults to purge: %w", err)
	}

	// TODO: Report progress, "Getting Managed HSMs to purge"
	managedHSMs, err := p.getManagedHSMsToPurge(ctx, groupedResources)
	if err != nil {
		return nil, fmt.Errorf("getting managed hsms to purge: %w", err)
	}

	// TODO: Report progress, "Getting App Configurations to purge"
	appConfigs, err := p.getAppConfigsToPurge(ctx, groupedResources)
	if err != nil {
		return nil, fmt.Errorf("getting app configurations to purge: %w", err)
	}

	// TODO: Report progress, "Getting API Management Services to purge"
	apiManagements, err := p.getApiManagementsToPurge(ctx, groupedResources)
	if err != nil {
		return nil, fmt.Errorf("getting API managements to purge: %w", err)
	}

	// TODO: Report progress, "Getting Cognitive Accounts to purge"
	cognitiveAccounts, err := p.getCognitiveAccountsToPurge(ctx, groupedResources)
	if err != nil {
		return nil, fmt.Errorf("getting cognitive accounts to purge: %w", err)
	}

	if err := p.destroyResourceGroups(ctx, options, groupedResources, len(allResources)); err != nil {
		return nil, fmt.Errorf("deleting resource groups: %w", err)
	}

	keyVaultsPurge := itemToPurge{
		resourceType: "Key Vault",
		count:        len(keyVaults),
		purge: func(skipPurge bool, self *itemToPurge) error {
			return p.purgeKeyVaults(ctx, keyVaults, options, skipPurge)
		},
	}
	managedHSMsPurge := itemToPurge{
		resourceType: "Managed HSM",
		count:        len(managedHSMs),
		purge: func(skipPurge bool, self *itemToPurge) error {
			return p.purgeManagedHSMs(ctx, managedHSMs, options, skipPurge)
		},
	}
	appConfigsPurge := itemToPurge{
		resourceType: "App Configuration",
		count:        len(appConfigs),
		purge: func(skipPurge bool, self *itemToPurge) error {
			return p.purgeAppConfigs(ctx, appConfigs, options, skipPurge)
		},
	}
	aPIManagement := itemToPurge{
		resourceType: "API Management",
		count:        len(apiManagements),
		purge: func(skipPurge bool, self *itemToPurge) error {
			return p.purgeAPIManagement(ctx, apiManagements, options, skipPurge)
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
				return p.purgeCognitiveAccounts(ctx, self.cognitiveAccounts, options, skipPurge)
			},
			cognitiveAccounts: groupByKind[name],
		}
		purgeItem = append(purgeItem, addPurgeItem)
	}

	if err := p.purgeItems(ctx, purgeItem, options); err != nil {
		return nil, fmt.Errorf("purging resources: %w", err)
	}

	destroyResult := &DestroyResult{
		InvalidatedEnvKeys: maps.Keys(p.createOutputParameters(
			compileResult.Template.Outputs,
			azapi.CreateDeploymentOutput(deployments[0].Properties.Outputs),
		)),
	}

	// Since we have deleted the resource group, add AZURE_RESOURCE_GROUP to the list of invalidated env vars
	// so it will be removed from the .env file.
	if _, ok := scope.(*infra.ResourceGroupScope); ok {
		destroyResult.InvalidatedEnvKeys = append(
			destroyResult.InvalidatedEnvKeys, environment.ResourceGroupEnvVarName,
		)
	}

	var emptyTemplate json.RawMessage
	if targetScope == azure.DeploymentScopeSubscription {
		emptyTemplate = []byte(cEmptySubDeployTemplate)
	} else {
		emptyTemplate = []byte(cEmptyResourceGroupDeployTemplate)
	}

	// create empty deployment to void provision state
	// We want to keep the deployment history, that's why it's not just deleted
	if _, err := p.deployModule(ctx,
		deployScope,
		emptyTemplate,
		azure.ArmParameters{},
		map[string]*string{
			azure.TagKeyAzdEnvName: to.Ptr(p.env.Name()),
			"azd-deploy-reason":    to.Ptr("down"),
		}); err != nil {
		log.Println("failed creating new empty deployment after destroy")
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

// findCompletedDeployments finds the most recent deployment the given environment in the provided scope,
// considering only deployments which have completed (either successfully or unsuccessfully).
func (p *BicepProvider) findCompletedDeployments(
	ctx context.Context, envName string, scope infra.Scope, hint string,
) ([]*armresources.DeploymentExtended, error) {

	deployments, err := scope.ListDeployments(ctx)
	if err != nil {
		return nil, err
	}

	slices.SortFunc(deployments, func(x, y *armresources.DeploymentExtended) int {
		return y.Properties.Timestamp.Compare(*x.Properties.Timestamp)
	})

	// If hint is not provided, use the environment name as the hint
	if hint == "" {
		hint = envName
	}

	// Environment matching strategy
	// 1. Deployment with azd tagged env name
	// 2. Exact match on environment name to deployment name (old azd strategy)
	// 3. Multiple matching names based on specified hint (show user prompt)
	matchingDeployments := []*armresources.DeploymentExtended{}

	for _, deployment := range deployments {
		// We only want to consider deployments that are in a terminal state, not any which may be ongoing.
		if *deployment.Properties.ProvisioningState != armresources.ProvisioningStateSucceeded &&
			*deployment.Properties.ProvisioningState != armresources.ProvisioningStateFailed {
			continue
		}

		// Match on current azd strategy (tags) or old azd strategy (deployment name)
		if v, has := deployment.Tags[azure.TagKeyAzdEnvName]; has && *v == envName || *deployment.Name == envName {
			return []*armresources.DeploymentExtended{deployment}, nil
		}

		// Fallback: Match on hint
		if hint != "" && strings.Contains(*deployment.Name, hint) {
			matchingDeployments = append(matchingDeployments, deployment)
		}
	}

	if len(matchingDeployments) == 0 {
		return nil, fmt.Errorf("'%s': %w", envName, ErrDeploymentsNotFound)
	}

	return matchingDeployments, nil
}

func getDeploymentOptions(ctx context.Context, deployments []*armresources.DeploymentExtended) []string {
	promptValues := []string{}
	for index, deployment := range deployments {
		optionTitle := fmt.Sprintf("%d. %s (%s)",
			index+1,
			*deployment.Name,
			deployment.Properties.Timestamp.Local().Format("1/2/2006, 3:04 PM"),
		)
		promptValues = append(promptValues, optionTitle)
	}

	return promptValues
}

// resourceGroupsToDelete collects the resource groups from an existing deployment which should be removed as part of a
// destroy operation.
func resourceGroupsToDelete(deployment *armresources.DeploymentExtended) []string {
	// NOTE: it's possible for a deployment to list a resource group more than once. We're only interested in the
	// unique set.
	resourceGroups := map[string]struct{}{}

	if *deployment.Properties.ProvisioningState == armresources.ProvisioningStateSucceeded {
		// For a successful deployment, we can use the output resources property to see the resource groups that were
		// provisioned from this.
		for _, resourceId := range deployment.Properties.OutputResources {
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
		for _, dependency := range deployment.Properties.Dependencies {
			if *dependency.ResourceType == string(infra.AzureResourceTypeDeployment) {
				for _, dependent := range dependency.DependsOn {
					if *dependent.ResourceType == arm.ResourceGroupResourceType.String() {
						resourceGroups[*dependent.ResourceName] = struct{}{}
					}
				}
			}

		}
	}

	return maps.Keys(resourceGroups)
}

func (p *BicepProvider) getAllResourcesToDelete(
	ctx context.Context,
	resourceGroups []string,
) (map[string][]azcli.AzCliResource, error) {
	allResources := map[string][]azcli.AzCliResource{}

	for _, resourceGroup := range resourceGroups {
		groupResources, err := p.azCli.ListResourceGroupResources(ctx, p.env.GetSubscriptionId(), resourceGroup, nil)
		var errDetails *azcore.ResponseError
		if errors.As(err, &errDetails) && errDetails.StatusCode == 404 {
			// Resource group not found and already deleted, skip grouping for deletion
			continue
		}

		if err != nil {
			return allResources, err
		}

		allResources[resourceGroup] = groupResources
	}

	return allResources, nil
}

func (p *BicepProvider) generateResourceGroupsToDelete(groupedResources map[string][]azcli.AzCliResource) []string {
	lines := []string{"Resource group(s) to be deleted:", ""}

	for rg := range groupedResources {
		lines = append(lines, fmt.Sprintf(
			"  • %s: %s",
			rg,
			output.WithLinkFormat("%s/#@/resource/subscriptions/%s/resourceGroups/%s/overview",
				p.portalUrlBase,
				p.env.GetSubscriptionId(),
				rg,
			),
		))
	}
	return append(lines, "")
}

// Deletes the azure resources within the deployment
func (p *BicepProvider) destroyResourceGroups(
	ctx context.Context,
	options DestroyOptions,
	groupedResources map[string][]azcli.AzCliResource,
	resourceCount int,
) error {
	if !options.Force() {
		p.console.MessageUxItem(ctx, &ux.MultilineMessage{
			Lines: p.generateResourceGroupsToDelete(groupedResources)},
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

	for resourceGroup := range groupedResources {
		message := fmt.Sprintf("Deleting resource group: %s",
			output.WithHighLightFormat(resourceGroup),
		)
		p.console.ShowSpinner(ctx, message, input.Step)
		err := p.azCli.DeleteResourceGroup(ctx, p.env.GetSubscriptionId(), resourceGroup)

		p.console.StopSpinner(ctx, message, input.GetStepResultFormat(err))
		if err != nil {
			return err
		}
	}
	// empty line at the end of all resource group deletion
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
	options DestroyOptions,
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

		if err != nil {
			return err
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
	groupedResources map[string][]azcli.AzCliResource,
) ([]*keyvault.KeyVault, error) {
	vaults := []*keyvault.KeyVault{}

	for resourceGroup, groupResources := range groupedResources {
		for _, resource := range groupResources {
			if resource.Type == string(infra.AzureResourceTypeKeyVault) {
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
	groupedResources map[string][]azcli.AzCliResource,
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
	groupedResources map[string][]azcli.AzCliResource,
) ([]*azcli.AzCliManagedHSM, error) {
	managedHSMs := []*azcli.AzCliManagedHSM{}

	for resourceGroup, groupResources := range groupedResources {
		for _, resource := range groupResources {
			if resource.Type == string(infra.AzureResourceTypeManagedHSM) {
				managedHSM, err := p.azCli.GetManagedHSM(
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
	groupedResources map[string][]azcli.AzCliResource,
) ([]*azcli.AzCliManagedHSM, error) {
	managedHSMs, err := p.getManagedHSMs(ctx, groupedResources)
	if err != nil {
		return nil, err
	}

	managedHSMsToPurge := []*azcli.AzCliManagedHSM{}
	for _, v := range managedHSMs {
		if v.Properties.EnableSoftDelete && !v.Properties.EnablePurgeProtection {
			managedHSMsToPurge = append(managedHSMsToPurge, v)
		}
	}

	return managedHSMsToPurge, nil
}

func (p *BicepProvider) getCognitiveAccountsToPurge(
	ctx context.Context,
	groupedResources map[string][]azcli.AzCliResource,
) (map[string][]armcognitiveservices.Account, error) {
	result := make(map[string][]armcognitiveservices.Account)

	for resourceGroup, groupResources := range groupedResources {
		cognitiveAccounts := []armcognitiveservices.Account{}
		for _, resource := range groupResources {
			if resource.Type == string(infra.AzureResourceTypeCognitiveServiceAccount) {
				account, err := p.azCli.GetCognitiveAccount(
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
	options DestroyOptions,
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
	managedHSMs []*azcli.AzCliManagedHSM,
	options DestroyOptions,
	skip bool,
) error {
	for _, managedHSM := range managedHSMs {
		err := p.runPurgeAsStep(ctx, "Managed HSM", managedHSM.Name, func() error {
			return p.azCli.PurgeManagedHSM(
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
	options DestroyOptions,
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
			return p.azCli.PurgeCognitiveAccount(
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
	groupedResources map[string][]azcli.AzCliResource,
) ([]*azcli.AzCliAppConfig, error) {
	configs := []*azcli.AzCliAppConfig{}

	for resourceGroup, groupResources := range groupedResources {
		for _, resource := range groupResources {
			if resource.Type == string(infra.AzureResourceTypeAppConfig) {
				config, err := p.azCli.GetAppConfig(
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
	groupedResources map[string][]azcli.AzCliResource,
) ([]*azcli.AzCliApim, error) {
	apims := []*azcli.AzCliApim{}

	for resourceGroup, groupResources := range groupedResources {
		for _, resource := range groupResources {
			if resource.Type == string(infra.AzureResourceTypeApim) {
				apim, err := p.azCli.GetApim(ctx, azure.SubscriptionFromRID(resource.Id), resourceGroup, resource.Name)
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
	appConfigs []*azcli.AzCliAppConfig,
	options DestroyOptions,
	skip bool,
) error {
	for _, appConfig := range appConfigs {
		err := p.runPurgeAsStep(ctx, "app config", appConfig.Name, func() error {
			return p.azCli.PurgeAppConfig(
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
	apims []*azcli.AzCliApim,
	options DestroyOptions,
	skip bool,
) error {
	for _, apim := range apims {
		err := p.runPurgeAsStep(ctx, "apim", apim.Name, func() error {
			return p.azCli.PurgeApim(ctx, azure.SubscriptionFromRID(apim.Id), apim.Name, apim.Location)
		}, skip)
		if err != nil {
			return fmt.Errorf("purging api management service %s: %w", apim.Name, err)
		}
	}

	return nil
}

func (p *BicepProvider) mapBicepTypeToInterfaceType(s string) ParameterType {
	switch s {
	case "String", "string", "secureString", "securestring":
		return ParameterTypeString
	case "Bool", "bool":
		return ParameterTypeBoolean
	case "Int", "int":
		return ParameterTypeNumber
	case "Object", "object", "secureObject", "secureobject":
		return ParameterTypeObject
	case "Array", "array":
		return ParameterTypeArray
	default:
		panic(fmt.Sprintf("unexpected bicep type: '%s'", s))
	}
}

// Creates a normalized view of the azure output parameters and resolves inconsistencies in the output parameter name
// casings.
func (p *BicepProvider) createOutputParameters(
	templateOutputs azure.ArmTemplateOutputs,
	azureOutputParams map[string]azapi.AzCliDeploymentOutput,
) map[string]OutputParameter {
	canonicalOutputCasings := make(map[string]string, len(templateOutputs))

	for key := range templateOutputs {
		canonicalOutputCasings[strings.ToLower(key)] = key
	}

	outputParams := make(map[string]OutputParameter, len(azureOutputParams))

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

		outputParams[paramName] = OutputParameter{
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
func (p *BicepProvider) convertToDeployment(bicepTemplate azure.ArmTemplate) (*Deployment, error) {
	template := Deployment{}
	parameters := make(map[string]InputParameter)
	outputs := make(map[string]OutputParameter)

	for key, param := range bicepTemplate.Parameters {
		parameters[key] = InputParameter{
			Type:         string(p.mapBicepTypeToInterfaceType(param.Type)),
			DefaultValue: param.DefaultValue,
		}
	}

	for key, param := range bicepTemplate.Outputs {
		outputs[key] = OutputParameter{
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
) (*armresources.DeploymentExtended, error) {
	return target.Deploy(ctx, armTemplate, armParameters, tags)
}

// Gets the folder path to the specified module
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
	if p.ensureParamsInMemoryCache != nil {
		return maps.Clone(p.ensureParamsInMemoryCache), nil
	}

	parameters, err := p.loadParameters(ctx)
	if err != nil {
		return nil, fmt.Errorf("resolving bicep parameters file: %w", err)
	}

	if len(template.Parameters) == 0 {
		return azure.ArmParameters{}, nil
	}
	configuredParameters := make(azure.ArmParameters, len(template.Parameters))

	sortedKeys := maps.Keys(template.Parameters)
	slices.Sort(sortedKeys)

	configModified := false

	var parameterPrompts []struct {
		key   string
		param azure.ArmTemplateParameterDefinition
	}

	for _, key := range sortedKeys {
		param := template.Parameters[key]

		// If a value is explicitly configured via a parameters file, use it.
		// unless the parameter value inference is nil/empty
		if v, has := parameters[key]; has {
			paramValue := armParameterFileValue(p.mapBicepTypeToInterfaceType(param.Type), v.Value, param.DefaultValue)
			if paramValue != nil {
				configuredParameters[key] = azure.ArmParameterValue{
					Value: paramValue,
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
			if isValueAssignableToParameterType(p.mapBicepTypeToInterfaceType(param.Type), v) {
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
				configKey := fmt.Sprintf("infra.parameters.%s", prompt.key)
				value := values[prompt.key]

				if err := p.env.Config.Set(configKey, value); err == nil {
					configModified = true
				} else {
					// errors from config.Set are panics, so we can't recover from them
					// For example, the value is not serializable to JSON
					log.Panicf(fmt.Sprintf("warning: failed to set value: %v", err))
				}

				configuredParameters[prompt.key] = azure.ArmParameterValue{
					Value: value,
				}
			}
		} else {
			for _, prompt := range parameterPrompts {
				configKey := fmt.Sprintf("infra.parameters.%s", prompt.key)

				// Otherwise, prompt for the value.
				value, err := p.promptForParameter(ctx, prompt.key, prompt.param)
				if err != nil {
					return nil, fmt.Errorf("prompting for value: %w", err)
				}

				if err := p.env.Config.Set(configKey, value); err == nil {
					configModified = true
				} else {
					// errors from config.Set are panics, so we can't recover from them
					// For example, the value is not serializable to JSON
					log.Panicf(fmt.Sprintf("warning: failed to set value: %v", err))
				}

				configuredParameters[prompt.key] = azure.ArmParameterValue{
					Value: value,
				}
			}
		}
	}

	if configModified {
		if err := p.envManager.Save(ctx, p.env); err != nil {
			p.console.Message(ctx, fmt.Sprintf("warning: failed to save configured values: %v", err))
		}
	}
	p.ensureParamsInMemoryCache = maps.Clone(configuredParameters)
	return configuredParameters, nil
}

// Convert the ARM parameters file value into a value suitable for deployment
func armParameterFileValue(paramType ParameterType, value any, defaultValue any) any {
	// Quick return if the value being converted is not a string
	if value == nil || reflect.TypeOf(value).Kind() != reflect.String {
		return value
	}

	// Relax the handling of bool and number types to accept convertible strings
	switch paramType {
	case ParameterTypeBoolean:
		if val, ok := value.(string); ok {
			if boolVal, err := strconv.ParseBool(val); err == nil {
				return boolVal
			}
		}
	case ParameterTypeNumber:
		if val, ok := value.(string); ok {
			if intVal, err := strconv.ParseInt(val, 10, 64); err == nil {
				return intVal
			}
		}
	case ParameterTypeString:
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

func isValueAssignableToParameterType(paramType ParameterType, value any) bool {
	switch paramType {
	case ParameterTypeArray:
		_, ok := value.([]any)
		return ok
	case ParameterTypeBoolean:
		_, ok := value.(bool)
		return ok
	case ParameterTypeNumber:
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
	case ParameterTypeObject:
		_, ok := value.(map[string]any)
		return ok
	case ParameterTypeString:
		_, ok := value.(string)
		return ok
	default:
		panic(fmt.Sprintf("unexpected type: %v", paramType))
	}
}

// NewBicepProvider creates a new instance of a Bicep Infra provider
func NewBicepProvider(
	bicepCli bicep.BicepCli,
	azCli azcli.AzCli,
	deploymentsService azapi.Deployments,
	deploymentOperations azapi.DeploymentOperations,
	envManager environment.Manager,
	env *environment.Environment,
	console input.Console,
	prompters prompt.Prompter,
	curPrincipal CurrentPrincipalIdProvider,
	alphaFeatureManager *alpha.FeatureManager,
	clock clock.Clock,
	keyvaultService keyvault.KeyVaultService,
	portalUrlBase string,
) Provider {
	return &BicepProvider{
		envManager:           envManager,
		env:                  env,
		console:              console,
		bicepCli:             bicepCli,
		azCli:                azCli,
		deploymentsService:   deploymentsService,
		deploymentOperations: deploymentOperations,
		prompters:            prompters,
		curPrincipal:         curPrincipal,
		alphaFeatureManager:  alphaFeatureManager,
		clock:                clock,
		keyvaultService:      keyvaultService,
		portalUrlBase:        portalUrlBase,
	}
}
