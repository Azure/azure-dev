// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package bicep

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/cognitiveservices/armcognitiveservices"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"github.com/azure/azure-dev/cli/azd/pkg/alpha"
	"github.com/azure/azure-dev/cli/azd/pkg/azure"
	"github.com/azure/azure-dev/cli/azd/pkg/cmdsubst"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/infra"
	. "github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/output/ux"
	"github.com/azure/azure-dev/cli/azd/pkg/prompt"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/azcli"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/bicep"
	"github.com/benbjohnson/clock"
	"github.com/drone/envsubst"
	"golang.org/x/exp/maps"
	"golang.org/x/exp/slices"
)

const DefaultModule = "main"

type BicepDeploymentDetails struct {
	// Template is the template to deploy during the deployment operation.
	Template azure.RawArmTemplate
	// Parameters are the values to provide to the template during the deployment operation.
	Parameters azure.ArmParameters
	// TemplateOutputs are the outputs as specified by the template.
	TemplateOutputs azure.ArmTemplateOutputs
	// Target is the unique resource in azure that represents the deployment that will happen. A target can be scoped to
	// either subscriptions, or resource groups.
	Target infra.Deployment
}

// BicepProvider exposes infrastructure provisioning using Azure Bicep templates
type BicepProvider struct {
	env                 *environment.Environment
	projectPath         string
	options             Options
	console             input.Console
	bicepCli            bicep.BicepCli
	azCli               azcli.AzCli
	prompters           prompt.Prompter
	curPrincipal        CurrentPrincipalIdProvider
	alphaFeatureManager *alpha.FeatureManager
	clock               clock.Clock
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
	if strings.TrimSpace(options.Module) == "" {
		options.Module = DefaultModule
	}

	p.projectPath = projectPath
	p.options = options

	requiredTools := p.RequiredExternalTools()
	if err := tools.EnsureInstalled(ctx, requiredTools...); err != nil {
		return err
	}

	return p.prompters.EnsureEnv(ctx)
}

func (p *BicepProvider) State(ctx context.Context) (*StateResult, error) {
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

	modulePath := p.modulePath()
	_, template, err := p.compileBicep(ctx, modulePath)
	if err != nil {
		return nil, fmt.Errorf("compiling bicep template: %w", err)
	}

	scope, err := p.scopeForTemplate(ctx, template)
	if err != nil {
		return nil, fmt.Errorf("computing deployment scope: %w", err)
	}

	// TODO: Report progress, "Retrieving Azure deployment"
	spinnerMessage = "Retrieving Azure deployment"
	p.console.ShowSpinner(ctx, spinnerMessage, input.Step)

	armDeployment, err := latestCompletedDeployment(ctx, p.env.GetEnvName(), scope)
	if err != nil {
		return nil, fmt.Errorf("retrieving deployment: %w", err)
	}

	state := State{}
	state.Resources = make([]Resource, len(armDeployment.Properties.OutputResources))

	for idx, res := range armDeployment.Properties.OutputResources {
		state.Resources[idx] = Resource{
			Id: *res.ID,
		}
	}

	// TODO: Report progress, "Normalizing output parameters"
	spinnerMessage = "Normalizing output parameters"
	p.console.ShowSpinner(ctx, spinnerMessage, input.Step)

	state.Outputs = p.createOutputParameters(
		template.Outputs,
		azcli.CreateDeploymentOutput(armDeployment.Properties.Outputs),
	)

	return &StateResult{
		State: &state,
	}, nil
}

var ResourceGroupDeploymentFeature = alpha.MustFeatureKey("resourceGroupDeployments")

// Plans the infrastructure provisioning
func (p *BicepProvider) Plan(ctx context.Context) (*DeploymentPlan, error) {
	p.console.ShowSpinner(ctx, "Creating a deployment plan", input.Step)
	// TODO: Report progress, "Generating Bicep parameters file"

	parameters, err := p.loadParameters(ctx)
	if err != nil {
		return nil, fmt.Errorf("creating parameters file: %w", err)
	}

	modulePath := p.modulePath()
	// TODO: Report progress, "Compiling Bicep template"
	rawTemplate, template, err := p.compileBicep(ctx, modulePath)
	if err != nil {
		return nil, fmt.Errorf("creating template: %w", err)
	}

	configuredParameters, err := p.ensureParameters(ctx, template, parameters)
	if err != nil {
		return nil, err
	}

	deployment, err := p.convertToDeployment(template)
	if err != nil {
		return nil, err
	}

	deploymentScope, err := template.TargetScope()
	if err != nil {
		return nil, err
	}

	var target infra.Deployment

	if deploymentScope == azure.DeploymentScopeSubscription {
		target = infra.NewSubscriptionDeployment(
			p.azCli,
			p.env.GetLocation(),
			p.env.GetSubscriptionId(),
			deploymentNameForEnv(p.env.GetEnvName(), p.clock),
		)
	} else if deploymentScope == azure.DeploymentScopeResourceGroup {
		if !p.alphaFeatureManager.IsEnabled(ResourceGroupDeploymentFeature) {
			return nil, ErrResourceGroupScopeNotSupported
		}

		p.console.WarnForFeature(ctx, ResourceGroupDeploymentFeature)

		if p.env.Getenv(environment.ResourceGroupEnvVarName) == "" {
			rgName, err := p.prompters.PromptResourceGroup(ctx)
			if err != nil {
				return nil, err
			}

			p.env.DotenvSet(environment.ResourceGroupEnvVarName, rgName)
			if err := p.env.Save(); err != nil {
				return nil, fmt.Errorf("saving environment: %w", err)
			}
		}

		target = infra.NewResourceGroupDeployment(
			p.azCli,
			p.env.GetSubscriptionId(),
			p.env.Getenv(environment.ResourceGroupEnvVarName),
			deploymentNameForEnv(p.env.GetEnvName(), p.clock),
		)
	} else {
		return nil, fmt.Errorf("unsupported scope: %s", deploymentScope)
	}

	return &DeploymentPlan{
		Deployment: *deployment,
		Details: BicepDeploymentDetails{
			Template:        rawTemplate,
			TemplateOutputs: template.Outputs,
			Parameters:      configuredParameters,
			Target:          target,
		},
	}, nil
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

// Provisioning the infrastructure within the specified template
func (p *BicepProvider) Deploy(ctx context.Context, pd *DeploymentPlan) (*DeployResult, error) {
	bicepDeploymentData := pd.Details.(BicepDeploymentDetails)

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
		resourceManager := infra.NewAzureResourceManager(p.azCli)
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

	deployResult, err := p.deployModule(
		ctx,
		bicepDeploymentData.Target,
		bicepDeploymentData.Template,
		bicepDeploymentData.Parameters,
		map[string]*string{
			azure.TagKeyAzdEnvName: to.Ptr(p.env.GetEnvName()),
		},
	)
	if err != nil {
		return nil, err
	}

	deployment := pd.Deployment
	deployment.Outputs = p.createOutputParameters(
		bicepDeploymentData.TemplateOutputs,
		azcli.CreateDeploymentOutput(deployResult.Properties.Outputs),
	)

	return &DeployResult{
		Deployment: &deployment,
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
		return infra.NewSubscriptionScope(p.azCli, p.env.GetSubscriptionId()), nil
	} else if deploymentScope == azure.DeploymentScopeResourceGroup {
		if !p.alphaFeatureManager.IsEnabled(ResourceGroupDeploymentFeature) {
			return nil, ErrResourceGroupScopeNotSupported
		}

		p.console.WarnForFeature(ctx, ResourceGroupDeploymentFeature)

		if p.env.Getenv(environment.ResourceGroupEnvVarName) == "" {
			rgName, err := p.prompters.PromptResourceGroup(ctx)
			if err != nil {
				return nil, err
			}

			p.env.DotenvSet(environment.ResourceGroupEnvVarName, rgName)
			if err := p.env.Save(); err != nil {
				return nil, fmt.Errorf("saving resource group name: %w", err)
			}
		}

		return infra.NewResourceGroupScope(
			p.azCli,
			p.env.GetSubscriptionId(),
			p.env.Getenv(environment.ResourceGroupEnvVarName),
		), nil

	} else {
		return nil, fmt.Errorf("unsupported deployment scope: %s", deploymentScope)
	}

}

// Destroys the specified deployment by deleting all azure resources, resource groups & deployments that are referenced.
func (p *BicepProvider) Destroy(ctx context.Context, options DestroyOptions) (*DestroyResult, error) {
	modulePath := p.modulePath()
	// TODO: Report progress, "Compiling Bicep template"
	_, template, err := p.compileBicep(ctx, modulePath)
	if err != nil {
		return nil, fmt.Errorf("creating template: %w", err)
	}

	scope, err := p.scopeForTemplate(ctx, template)
	if err != nil {
		return nil, fmt.Errorf("computing deployment scope: %w", err)
	}

	// TODO: Report progress, "Fetching resource groups"
	deployment, err := latestCompletedDeployment(ctx, p.env.GetEnvName(), scope)
	if err != nil {
		return nil, err
	}

	rgsFromDeployment := resourceGroupsFromDeployment(deployment)

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
			template.Outputs,
			azcli.CreateDeploymentOutput(deployment.Properties.Outputs),
		)),
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

// latestCompletedDeployment finds the most recent deployment the given environment in the provided scope,
// considering only deployments which have completed (either successfully or unsuccessfully).
func latestCompletedDeployment(
	ctx context.Context, envName string, scope infra.Scope,
) (*armresources.DeploymentExtended, error) {

	deployments, err := scope.ListDeployments(ctx)
	if err != nil {
		return nil, err
	}

	slices.SortFunc(deployments, func(x, y *armresources.DeploymentExtended) bool {
		return x.Properties.Timestamp.After(*y.Properties.Timestamp)
	})

	// Earlier versions of `azd` did not use unique deployment names per deployment and also did not tag the deployment
	// with an `azd` specific tag. Instead, the name of the deployment simply matched the environment name.
	//
	// As we walk the list of deployments, we note if we find a deployment matching this older strategy and will return
	// it if we can't find a deployment that matches the newer one.
	var matchingBareDeployment *armresources.DeploymentExtended

	for _, deployment := range deployments {

		// We only want to consider deployments that are in a terminal state, not any which may be ongoing.
		if *deployment.Properties.ProvisioningState != armresources.ProvisioningStateSucceeded &&
			*deployment.Properties.ProvisioningState != armresources.ProvisioningStateFailed {
			continue
		}

		if v, has := deployment.Tags[azure.TagKeyAzdEnvName]; has && *v == envName {
			return deployment, nil
		}

		if *deployment.Name == envName {
			matchingBareDeployment = deployment
		}
	}

	if matchingBareDeployment != nil {
		return matchingBareDeployment, nil
	}

	return nil, fmt.Errorf("no deployments found for environment %s", envName)
}

// resourceGroupsFromDeployment returns the names of all the unique set of resource group name names resource groups from
//
//	the OutputResources section of a ARM deployment.
func resourceGroupsFromDeployment(deployment *armresources.DeploymentExtended) []string {

	// NOTE: it's possible for a deployment to list a resource group more than once. We're only interested in the
	// unique set.
	resourceGroups := map[string]struct{}{}

	for _, resourceId := range deployment.Properties.OutputResources {
		if resourceId != nil && resourceId.ID != nil {
			resId, err := arm.ParseResourceID(*resourceId.ID)
			if err == nil && resId.ResourceGroupName != "" {
				resourceGroups[resId.ResourceGroupName] = struct{}{}
			}
		}
	}

	var resourceGroupNames []string

	for k := range resourceGroups {
		resourceGroupNames = append(resourceGroupNames, k)
	}

	return resourceGroupNames
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

func generateResourceGroupsToDelete(groupedResources map[string][]azcli.AzCliResource, subId string) []string {
	lines := []string{"Resource group(s) to be deleted:", ""}

	for rg := range groupedResources {
		lines = append(lines, fmt.Sprintf(
			"  • %s: %s",
			rg,
			output.WithLinkFormat("https://portal.azure.com/#@/resource/subscriptions/%s/resourceGroups/%s/overview",
				subId,
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
			Lines: generateResourceGroupsToDelete(groupedResources, p.env.GetSubscriptionId())},
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
				"after deletion. During this period, their names may not be reused. In the future, you cant use "+
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
) ([]*azcli.AzCliKeyVault, error) {
	vaults := []*azcli.AzCliKeyVault{}

	for resourceGroup, groupResources := range groupedResources {
		for _, resource := range groupResources {
			if resource.Type == string(infra.AzureResourceTypeKeyVault) {
				vault, err := p.azCli.GetKeyVault(ctx, azure.SubscriptionFromRID(resource.Id), resourceGroup, resource.Name)
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
) ([]*azcli.AzCliKeyVault, error) {
	vaults, err := p.getKeyVaults(ctx, groupedResources)
	if err != nil {
		return nil, err
	}

	vaultsToPurge := []*azcli.AzCliKeyVault{}
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
				managedHSM, err := p.azCli.GetManagedHSM(ctx, azure.SubscriptionFromRID(resource.Id), resourceGroup, resource.Name)
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
	keyVaults []*azcli.AzCliKeyVault,
	options DestroyOptions,
	skip bool,
) error {
	for _, keyVault := range keyVaults {
		err := p.runPurgeAsStep(ctx, "Key Vault", keyVault.Name, func() error {
			return p.azCli.PurgeKeyVault(
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
// See https://learn.microsoft.com/en-us/azure/azure-app-configuration/concept-soft-delete for more information
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
	azureOutputParams map[string]azcli.AzCliDeploymentOutput,
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
			paramName = key
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
	parametersTemplateFilePath := p.parametersTemplateFilePath()
	log.Printf("Reading parameters template file from: %s", parametersTemplateFilePath)
	parametersBytes, err := os.ReadFile(parametersTemplateFilePath)
	if err != nil {
		return nil, fmt.Errorf("reading parameter file template: %w", err)
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
		cmdExecutor := cmdsubst.NewSecretOrRandomPasswordExecutor(p.azCli, p.env.GetSubscriptionId())
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

func (p *BicepProvider) compileBicep(
	ctx context.Context, modulePath string,
) (azure.RawArmTemplate, azure.ArmTemplate, error) {

	compiled, err := p.bicepCli.Build(ctx, modulePath)
	if err != nil {
		return nil, azure.ArmTemplate{}, fmt.Errorf("failed to compile bicep template: %w", err)
	}

	rawTemplate := azure.RawArmTemplate(compiled)

	var template azure.ArmTemplate
	if err := json.Unmarshal(rawTemplate, &template); err != nil {
		log.Printf("failed unmarshalling compiled arm template to JSON (err: %v), template contents:\n%s", err, compiled)
		return nil, azure.ArmTemplate{}, fmt.Errorf("failed unmarshalling arm template from json: %w", err)
	}

	return rawTemplate, template, nil
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

// Gets the path to the project parameters file path
func (p *BicepProvider) parametersTemplateFilePath() string {
	infraPath := p.options.Path
	parametersFilename := fmt.Sprintf("%s.parameters.json", p.options.Module)
	return filepath.Join(p.projectPath, infraPath, parametersFilename)
}

// Gets the folder path to the specified module
func (p *BicepProvider) modulePath() string {
	infraPath := p.options.Path
	moduleFilename := fmt.Sprintf("%s.bicep", p.options.Module)
	return filepath.Join(p.projectPath, infraPath, moduleFilename)
}

// Ensures the provisioning parameters are valid and prompts the user for input as needed
func (p *BicepProvider) ensureParameters(
	ctx context.Context,
	template azure.ArmTemplate,
	parameters azure.ArmParameters,
) (azure.ArmParameters, error) {
	if len(template.Parameters) == 0 {
		return azure.ArmParameters{}, nil
	}

	configuredParameters := make(azure.ArmParameters, len(template.Parameters))

	sortedKeys := maps.Keys(template.Parameters)
	slices.Sort(sortedKeys)

	configModified := false

	for _, key := range sortedKeys {
		param := template.Parameters[key]

		// If a value is explicitly configured via a parameters file, use it.
		if v, has := parameters[key]; has {
			configuredParameters[key] = azure.ArmParameterValue{
				Value: armParameterFileValue(p.mapBicepTypeToInterfaceType(param.Type), v.Value),
			}
			continue
		}

		// If this parameter has a default, then there is no need for us to configure it.
		if param.DefaultValue != nil {
			continue
		}

		// This required parameter was not in parameters file - see if we stored a value in config from an earlier
		// prompt and if so use it.
		configKey := fmt.Sprintf("infra.parameters.%s", key)

		if v, has := p.env.Config.Get(configKey); has {

			if !isValueAssignableToParameterType(p.mapBicepTypeToInterfaceType(param.Type), v) {
				// The saved value is no longer valid (perhaps the user edited their template to change the type of a)
				// parameter and then re-ran `azd provision`. Forget the saved value (if we can) and prompt for a new one.
				_ = p.env.Config.Unset("infra.parameters.%s")
			}

			configuredParameters[key] = azure.ArmParameterValue{
				Value: v,
			}
			continue
		}

		// Otherwise, prompt for the value.
		value, err := p.promptForParameter(ctx, key, param)
		if err != nil {
			return nil, fmt.Errorf("prompting for value: %w", err)
		}

		if !param.Secure() {
			saveParameter, err := p.console.Confirm(ctx, input.ConsoleOptions{
				Message: "Save the value in the environment for future use",
			})

			if err != nil {
				return nil, fmt.Errorf("prompting to save deployment parameter: %w", err)
			}

			if saveParameter {
				if err := p.env.Config.Set(configKey, value); err == nil {
					configModified = true
				} else {
					p.console.Message(ctx, fmt.Sprintf("warning: failed to set value: %v", err))
				}
			}
		}

		configuredParameters[key] = azure.ArmParameterValue{
			Value: value,
		}
	}

	if configModified {
		if err := p.env.Save(); err != nil {
			p.console.Message(ctx, fmt.Sprintf("warning: failed to save configured values: %v", err))
		}
	}

	return configuredParameters, nil
}

// Convert the ARM parameters file value into a value suitable for deployment
func armParameterFileValue(paramType ParameterType, value any) any {
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
	}

	return value
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
	env *environment.Environment,
	console input.Console,
	prompters prompt.Prompter,
	curPrincipal CurrentPrincipalIdProvider,
	alphaFeatureManager *alpha.FeatureManager,
	clock clock.Clock,
) Provider {
	return &BicepProvider{
		env:                 env,
		console:             console,
		bicepCli:            bicepCli,
		azCli:               azCli,
		prompters:           prompters,
		curPrincipal:        curPrincipal,
		alphaFeatureManager: alphaFeatureManager,
		clock:               clock,
	}
}
