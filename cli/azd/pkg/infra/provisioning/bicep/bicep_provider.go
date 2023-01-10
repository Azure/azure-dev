// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// Package bicep contains an implementation of provider.Provider for Bicep. This
// provider is registered for use when this package is imported, and can be imported for
// side effects only to register the provider, e.g.:
//
// require(
//
//	_ "github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning/bicep"
//
// )
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

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"github.com/azure/azure-dev/cli/azd/pkg/async"
	"github.com/azure/azure-dev/cli/azd/pkg/azure"
	"github.com/azure/azure-dev/cli/azd/pkg/cmdsubst"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/infra"
	. "github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/azcli"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/bicep"
	"github.com/drone/envsubst"
	"github.com/sethvargo/go-retry"
	"golang.org/x/exp/maps"
	"golang.org/x/exp/slices"
)

type BicepDeploymentDetails struct {
	// Template is the template to deploy during the deployment operation.
	Template azure.RawArmTemplate
	// Parameters are the values to provide to the template during the deployment operation.
	Parameters azure.ArmParameters
	// TemplateOutputs are the outputs as specified by the template.
	TemplateOutputs azure.ArmTemplateOutputs
}

// BicepProvider exposes infrastructure provisioning using Azure Bicep templates
type BicepProvider struct {
	env         *environment.Environment
	projectPath string
	options     Options
	console     input.Console
	bicepCli    bicep.BicepCli
	azCli       azcli.AzCli
}

// Name gets the name of the infra provider
func (p *BicepProvider) Name() string {
	return "Bicep"
}

func (p *BicepProvider) RequiredExternalTools() []tools.ExternalTool {
	return []tools.ExternalTool{}
}

func (p *BicepProvider) State(
	ctx context.Context,
	scope infra.Scope,
) *async.InteractiveTaskWithProgress[*StateResult, *StateProgress] {
	return async.RunInteractiveTaskWithProgress(
		func(asyncContext *async.InteractiveTaskContextWithProgress[*StateResult, *StateProgress]) {
			asyncContext.SetProgress(&StateProgress{Message: "Loading Bicep template", Timestamp: time.Now()})
			modulePath := p.modulePath()
			_, template, err := p.compileBicep(ctx, modulePath)
			if err != nil {
				asyncContext.SetError(fmt.Errorf("compiling bicep template: %w", err))
				return
			}

			asyncContext.SetProgress(&StateProgress{Message: "Retrieving Azure deployment", Timestamp: time.Now()})
			armDeployment, err := scope.GetDeployment(ctx)
			if err != nil {
				asyncContext.SetError(fmt.Errorf("retrieving deployment: %w", err))
				return
			}

			state := State{}
			state.Resources = make([]Resource, len(armDeployment.Properties.OutputResources))

			for idx, res := range armDeployment.Properties.OutputResources {
				state.Resources[idx] = Resource{
					Id: *res.ID,
				}
			}

			asyncContext.SetProgress(&StateProgress{Message: "Normalizing output parameters", Timestamp: time.Now()})
			state.Outputs = p.createOutputParameters(
				template.Outputs,
				azcli.CreateDeploymentOutput(armDeployment.Properties.Outputs),
			)

			result := StateResult{
				State: &state,
			}

			asyncContext.SetResult(&result)
		})
}

// Plans the infrastructure provisioning
func (p *BicepProvider) Plan(
	ctx context.Context,
) *async.InteractiveTaskWithProgress[*DeploymentPlan, *DeploymentPlanningProgress] {
	return async.RunInteractiveTaskWithProgress(
		func(asyncContext *async.InteractiveTaskContextWithProgress[*DeploymentPlan, *DeploymentPlanningProgress]) {
			p.console.ShowSpinner(ctx, "Creating a deployment plan", input.Step)
			asyncContext.SetProgress(
				&DeploymentPlanningProgress{Message: "Generating Bicep parameters file", Timestamp: time.Now()},
			)

			parameters, err := p.loadParameters(ctx, asyncContext)
			if err != nil {
				asyncContext.SetError(fmt.Errorf("creating parameters file: %w", err))
				return
			}

			modulePath := p.modulePath()
			asyncContext.SetProgress(&DeploymentPlanningProgress{Message: "Compiling Bicep template", Timestamp: time.Now()})
			rawTemplate, template, err := p.compileBicep(ctx, modulePath)
			if err != nil {
				asyncContext.SetError(fmt.Errorf("creating template: %w", err))
				return
			}

			configuredParameters, err := p.ensureParameters(ctx, asyncContext, template, parameters)
			if err != nil {
				asyncContext.SetError(err)
				return
			}

			deployment, err := p.convertToDeployment(template)
			if err != nil {
				asyncContext.SetError(err)
				return
			}

			result := DeploymentPlan{
				Deployment: *deployment,
				Details: BicepDeploymentDetails{
					Template:        rawTemplate,
					TemplateOutputs: template.Outputs,
					Parameters:      configuredParameters,
				},
			}
			// remove the spinner with no message as no message is expected
			p.console.StopSpinner(ctx, "", input.StepDone)
			asyncContext.SetResult(&result)
		})
}

// Provisioning the infrastructure within the specified template
func (p *BicepProvider) Deploy(
	ctx context.Context,
	pd *DeploymentPlan,
	scope infra.Scope,
) *async.InteractiveTaskWithProgress[*DeployResult, *DeployProgress] {
	return async.RunInteractiveTaskWithProgress(
		func(asyncContext *async.InteractiveTaskContextWithProgress[*DeployResult, *DeployProgress]) {
			done := make(chan bool)

			// Ensure the done marker channel is sent in all conditions
			defer func() {
				done <- true
			}()

			// Report incremental progress
			go func() {
				resourceManager := infra.NewAzureResourceManager(p.azCli)
				progressDisplay := NewProvisioningProgressDisplay(resourceManager, p.console, scope)
				// Make initial delay shorter to be more responsive in displaying initial progress
				initialDelay := 3 * time.Second
				regularDelay := 10 * time.Second
				timer := time.NewTimer(initialDelay)
				queryStartTime := time.Now()

				for {
					select {
					case <-done:
						timer.Stop()
						return
					case <-timer.C:
						progressReport, err := progressDisplay.ReportProgress(ctx, &queryStartTime)
						if err != nil {
							// We don't want to fail the whole deployment if a progress reporting error occurs
							log.Printf("error while reporting progress: %s", err.Error())
							continue
						}

						asyncContext.SetProgress(progressReport)

						timer.Reset(regularDelay)
					}
				}
			}()

			// Start the deployment
			p.console.ShowSpinner(ctx, "Creating/Updating resources", input.Step)
			bicepDeploymentData := pd.Details.(BicepDeploymentDetails)

			deployResult, err := p.deployModule(ctx, scope, bicepDeploymentData.Template, bicepDeploymentData.Parameters)
			if err != nil {
				asyncContext.SetError(err)
				return
			}

			deployment := pd.Deployment
			deployment.Outputs = p.createOutputParameters(
				bicepDeploymentData.TemplateOutputs,
				azcli.CreateDeploymentOutput(deployResult.Properties.Outputs),
			)

			result := &DeployResult{
				Deployment: &deployment,
			}

			asyncContext.SetResult(result)
		})
}

type itemToPurge struct {
	resourceType string
	count        int
	purge        func() error
}

// Destroys the specified deployment by deleting all azure resources, resource groups & deployments that are referenced.
func (p *BicepProvider) Destroy(
	ctx context.Context,
	deployment *Deployment,
	options DestroyOptions,
) *async.InteractiveTaskWithProgress[*DestroyResult, *DestroyProgress] {
	return async.RunInteractiveTaskWithProgress(
		func(asyncContext *async.InteractiveTaskContextWithProgress[*DestroyResult, *DestroyProgress]) {
			asyncContext.SetProgress(&DestroyProgress{Message: "Fetching resource groups", Timestamp: time.Now()})
			resourceGroups, err := p.getResourceGroups(ctx)
			if err != nil {
				asyncContext.SetError(err)
				return
			}

			asyncContext.SetProgress(&DestroyProgress{Message: "Fetching resources", Timestamp: time.Now()})
			groupedResources, err := p.getAllResources(ctx, resourceGroups)
			if err != nil {
				asyncContext.SetError(fmt.Errorf("getting resources to delete: %w", err))
				return
			}

			allResources := []azcli.AzCliResource{}
			for _, groupResources := range groupedResources {
				allResources = append(allResources, groupResources...)
			}

			asyncContext.SetProgress(&DestroyProgress{Message: "Getting Key Vaults to purge", Timestamp: time.Now()})
			keyVaults, err := p.getKeyVaultsToPurge(ctx, groupedResources)
			if err != nil {
				asyncContext.SetError(fmt.Errorf("getting key vaults to purge: %w", err))
				return
			}

			asyncContext.SetProgress(&DestroyProgress{Message: "Getting App Configurations to purge", Timestamp: time.Now()})
			appConfigs, err := p.getAppConfigsToPurge(ctx, groupedResources)
			if err != nil {
				asyncContext.SetError(fmt.Errorf("getting app configurations to purge: %w", err))
				return
			}

			asyncContext.SetProgress(&DestroyProgress{Message: "Getting API Management Services to purge", Timestamp: time.Now()})
			apiManagements, err := p.getApiManagementsToPurge(ctx, groupedResources)
			if err != nil {
				asyncContext.SetError(fmt.Errorf("getting API managements to purge: %w", err))
				return
			}

			if err := p.destroyResourceGroups(ctx, asyncContext, options, groupedResources, len(allResources)); err != nil {
				asyncContext.SetError(fmt.Errorf("destroying resource groups: %w", err))
				return
			}

			keyVaultsPurge := itemToPurge{
				resourceType: "Key Vaults",
				count:        len(keyVaults),
				purge: func() error {
					return p.purgeKeyVaults(ctx, asyncContext, keyVaults, options)
				},
			}
			appConfigsPurge := itemToPurge{
				resourceType: "App Configurations",
				count:        len(appConfigs),
				purge: func() error {
					return p.purgeAppConfigs(ctx, asyncContext, appConfigs, options)
				},
			}
			aPIManagement := itemToPurge{
				resourceType: "API Managements",
				count:        len(apiManagements),
				purge: func() error {
					return p.purgeAPIManagement(ctx, asyncContext, apiManagements, options)
				},
			}
			purgeItem := []itemToPurge{keyVaultsPurge, appConfigsPurge, aPIManagement}

			if err := p.purgeItems(ctx, asyncContext, purgeItem, options); err != nil {
				asyncContext.SetError(fmt.Errorf("purging resources: %w", err))
				return
			}

			if err := p.deleteDeployment(ctx, asyncContext); err != nil {
				asyncContext.SetError(fmt.Errorf("deleting subscription deployment: %w", err))
				return
			}

			destroyResult := DestroyResult{
				Resources: allResources,
				Outputs:   deployment.Outputs,
			}

			asyncContext.SetResult(&destroyResult)
		})
}

func (p *BicepProvider) getResourceGroups(ctx context.Context) ([]string, error) {
	resourceManager := infra.NewAzureResourceManager(p.azCli)
	resourceGroups, err := resourceManager.GetResourceGroupsForDeployment(ctx, p.env.GetSubscriptionId(), p.env.GetEnvName())
	if err != nil {
		return []string{}, err
	}

	return resourceGroups, nil
}

func (p *BicepProvider) getAllResources(
	ctx context.Context,
	resourceGroups []string,
) (map[string][]azcli.AzCliResource, error) {
	allResources := map[string][]azcli.AzCliResource{}

	for _, resourceGroup := range resourceGroups {
		groupResources, err := p.azCli.ListResourceGroupResources(ctx, p.env.GetSubscriptionId(), resourceGroup, nil)
		if err != nil {
			return allResources, err
		}

		allResources[resourceGroup] = groupResources
	}

	return allResources, nil
}

// Deletes the azure resources within the deployment
func (p *BicepProvider) destroyResourceGroups(
	ctx context.Context,
	asyncContext *async.InteractiveTaskContextWithProgress[*DestroyResult, *DestroyProgress],
	options DestroyOptions,
	groupedResources map[string][]azcli.AzCliResource,
	resourceCount int,
) error {
	if !options.Force() {
		err := asyncContext.Interact(func() error {
			confirmDestroy, err := p.console.Confirm(ctx, input.ConsoleOptions{
				Message: fmt.Sprintf(
					"This will delete %d resources, are you sure you want to continue?",
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
		})

		if err != nil {
			return err
		}
	}

	for resourceGroup := range groupedResources {
		message := fmt.Sprintf(
			"%s resource group %s",
			output.WithErrorFormat("Deleting"),
			output.WithHighLightFormat(resourceGroup),
		)
		asyncContext.SetProgress(&DestroyProgress{Message: message, Timestamp: time.Now()})

		if err := p.azCli.DeleteResourceGroup(ctx, p.env.GetSubscriptionId(), resourceGroup); err != nil {
			return err
		}

		p.console.Message(
			ctx,
			fmt.Sprintf(
				"%s resource group %s",
				output.WithErrorFormat("Deleted"),
				output.WithHighLightFormat(resourceGroup),
			),
		)
	}

	return nil
}

func (p *BicepProvider) purgeItems(
	ctx context.Context,
	asyncContext *async.InteractiveTaskContextWithProgress[*DestroyResult, *DestroyProgress],
	items []itemToPurge,
	options DestroyOptions,
) error {
	if len(items) > 0 && !options.Purge() {
		var itemString string
		var itemsWarning string
		for _, v := range items {
			if v.count > 0 {
				if itemString != "" {
					itemString = itemString + "/" + v.resourceType
				} else {
					itemString = v.resourceType
				}
				itemsWarning = itemsWarning + fmt.Sprintf("\n				%d %s", v.count, v.resourceType)
			}
		}

		if len(itemsWarning) < 1 {
			return nil
		}

		itemsWarning = "\n\nThis operation will delete:" + itemsWarning + fmt.Sprintf("\nThese %s have soft delete enabled "+
			"allowing them to be recovered for a period "+
			"of time after deletion. During this period, their names may not be reused.\n"+
			"You can use argument --purge to skip this confirmation.\n\n", itemString)

		p.console.Message(ctx, output.WithWarningFormat(itemsWarning))

		err := asyncContext.Interact(func() error {
			purgeItems, err := p.console.Confirm(ctx, input.ConsoleOptions{
				Message: fmt.Sprintf(
					"Would you like to %s delete these %s instead, allowing their names to be reused?",
					output.WithErrorFormat("permanently"),
					itemString,
				),
				DefaultValue: false,
			})

			if err != nil {
				return fmt.Errorf("prompting for %s confirmation: %w", itemString, err)
			}

			if !purgeItems {
				return fmt.Errorf("user denied %s confirmation", itemString)
			}

			return nil
		})

		if err != nil {
			return err
		}
	}
	for _, item := range items {
		if err := item.purge(); err != nil {
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
				vault, err := p.azCli.GetKeyVault(ctx, p.env.GetSubscriptionId(), resourceGroup, resource.Name)
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

// Azure KeyVaults have a "soft delete" functionality (now enabled by default) where a vault may be marked
// such that when it is deleted it can be recovered for a period of time. During that time, the name may
// not be reused.
//
// This means that running `az dev provision`, then `az dev infra delete` and finally `az dev provision`
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
	asyncContext *async.InteractiveTaskContextWithProgress[*DestroyResult, *DestroyProgress],
	keyVaults []*azcli.AzCliKeyVault,
	options DestroyOptions,
) error {
	for _, keyVault := range keyVaults {
		progressReport := DestroyProgress{
			Timestamp: time.Now(),
			Message: fmt.Sprintf(
				"%s key vault %s",
				output.WithErrorFormat("Purging"),
				output.WithHighLightFormat(keyVault.Name),
			),
		}

		asyncContext.SetProgress(&progressReport)

		err := p.azCli.PurgeKeyVault(ctx, p.env.GetSubscriptionId(), keyVault.Name, keyVault.Location)
		if err != nil {
			return fmt.Errorf("purging key vault %s: %w", keyVault.Name, err)
		}

		p.console.Message(
			ctx,
			fmt.Sprintf("%s key vault %s", output.WithErrorFormat("Purged"), output.WithHighLightFormat(keyVault.Name)),
		)
	}

	return nil
}

func (p *BicepProvider) getAppConfigsToPurge(
	ctx context.Context,
	groupedResources map[string][]azcli.AzCliResource,
) ([]*azcli.AzCliAppConfig, error) {
	configs := []*azcli.AzCliAppConfig{}

	for resourceGroup, groupResources := range groupedResources {
		for _, resource := range groupResources {
			if resource.Type == string(infra.AzureResourceTypeAppConfig) {
				config, err := p.azCli.GetAppConfig(ctx, p.env.GetSubscriptionId(), resourceGroup, resource.Name)
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
				apim, err := p.azCli.GetApim(ctx, p.env.GetSubscriptionId(), resourceGroup, resource.Name)
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
// This means that running `az dev provision`, then `az dev infra delete` and finally `az dev provision`
// again would lead to a deployment error since the configuration name is in use.
//
// Since that's behavior we'd like to support, we run a purge operation for each AppConfiguration after it has been deleted.
//
// See https://learn.microsoft.com/en-us/azure/azure-app-configuration/concept-soft-delete for more information
// on this feature.
func (p *BicepProvider) purgeAppConfigs(
	ctx context.Context,
	asyncContext *async.InteractiveTaskContextWithProgress[*DestroyResult, *DestroyProgress],
	appConfigs []*azcli.AzCliAppConfig,
	options DestroyOptions,
) error {
	for _, appConfig := range appConfigs {
		progressReport := DestroyProgress{
			Timestamp: time.Now(),
			Message: fmt.Sprintf(
				"%s app configuration %s",
				output.WithErrorFormat("Purging"),
				output.WithHighLightFormat(appConfig.Name),
			),
		}

		asyncContext.SetProgress(&progressReport)

		err := p.azCli.PurgeAppConfig(ctx, p.env.GetSubscriptionId(), appConfig.Name, appConfig.Location)
		if err != nil {
			return fmt.Errorf("purging app configuration %s: %w", appConfig.Name, err)
		}

		p.console.Message(
			ctx,
			fmt.Sprintf(
				"%s app configuration %s",
				output.WithErrorFormat("Purged"),
				output.WithHighLightFormat(appConfig.Name),
			),
		)
	}

	return nil
}

func (p *BicepProvider) purgeAPIManagement(
	ctx context.Context,
	asyncContext *async.InteractiveTaskContextWithProgress[*DestroyResult, *DestroyProgress],
	apims []*azcli.AzCliApim,
	options DestroyOptions,
) error {
	for _, apim := range apims {
		progressReport := DestroyProgress{
			Timestamp: time.Now(),
			Message: fmt.Sprintf(
				"%s api management service %s",
				output.WithErrorFormat("Purging"),
				output.WithHighLightFormat(apim.Name),
			),
		}

		asyncContext.SetProgress(&progressReport)

		err := p.azCli.PurgeApim(ctx, p.env.GetSubscriptionId(), apim.Name, apim.Location)
		if err != nil {
			return fmt.Errorf("purging api management service %s: %w", apim.Name, err)
		}

		p.console.Message(
			ctx,
			fmt.Sprintf(
				"%s api management service %s",
				output.WithErrorFormat("Purged"),
				output.WithHighLightFormat(apim.Name),
			),
		)
	}

	return nil
}

// Deletes the azure deployment
func (p *BicepProvider) deleteDeployment(
	ctx context.Context,
	asyncContext *async.InteractiveTaskContextWithProgress[*DestroyResult, *DestroyProgress],
) error {
	asyncContext.SetProgress(&DestroyProgress{Message: "Deleting deployment", Timestamp: time.Now()})

	deploymentName := p.env.GetEnvName()

	if err := p.azCli.DeleteSubscriptionDeployment(ctx, p.env.GetSubscriptionId(), deploymentName); err != nil {
		return err
	}

	p.console.Message(
		ctx,
		fmt.Sprintf("%s deployment %s", output.WithErrorFormat("Deleted"), output.WithHighLightFormat(deploymentName)),
	)

	return nil
}

func (p *BicepProvider) mapBicepTypeToInterfaceType(s string) ParameterType {
	switch s {
	case "String", "string", "secureString":
		return ParameterTypeString
	case "Bool", "bool":
		return ParameterTypeBoolean
	case "Int", "int":
		return ParameterTypeNumber
	case "Object", "object", "secureObject":
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
func (p *BicepProvider) loadParameters(
	ctx context.Context,
	asyncContext *async.InteractiveTaskContextWithProgress[*DeploymentPlan, *DeploymentPlanningProgress],
) (map[string]azure.ArmParameterValue, error) {
	parametersTemplateFilePath := p.parametersTemplateFilePath()
	log.Printf("Reading parameters template file from: %s", parametersTemplateFilePath)
	parametersBytes, err := os.ReadFile(parametersTemplateFilePath)
	if err != nil {
		return nil, fmt.Errorf("reading parameter file template: %w", err)
	}

	replaced, err := envsubst.Eval(string(parametersBytes), func(name string) string {
		if val, has := p.env.Values[name]; has {
			return val
		}
		return os.Getenv(name)
	})
	if err != nil {
		return nil, fmt.Errorf("substituting environment variables inside parameter file: %w", err)
	}

	if cmdsubst.ContainsCommandInvocation(replaced, cmdsubst.SecretOrRandomPasswordCommandName) {
		cmdExecutor := cmdsubst.NewSecretOrRandomPasswordExecutor(p.azCli)
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
	scope infra.Scope,
	armTemplate azure.RawArmTemplate,
	armParameters azure.ArmParameters,
) (*armresources.DeploymentExtended, error) {

	if err := scope.Deploy(ctx, armTemplate, armParameters); err != nil {
		return nil, fmt.Errorf("failed deploying: %w", err)
	}

	// We've seen issues where `Deploy` completes but for a short while after, fetching the deployment fails with a
	// `DeploymentNotFound` error.
	// Since other commands of ours use the deployment, let's try to fetch it here and if we fail with `DeploymentNotFound`,
	// ignore this error, wait a short while and retry.

	var deployment *armresources.DeploymentExtended
	if err := retry.Do(ctx, retry.WithMaxRetries(10, retry.NewExponential(1*time.Second)), func(ctx context.Context) error {
		deploymentResult, err := scope.GetDeployment(ctx)
		if errors.Is(err, azcli.ErrDeploymentNotFound) {
			return retry.RetryableError(err)
		} else if err != nil {
			return fmt.Errorf("failed waiting for deployment: %w", err)
		}

		deployment = deploymentResult
		return nil
	}); err != nil {
		return nil, fmt.Errorf("timed out waiting for deployment: %w", err)
	}

	return deployment, nil
}

// Gets the path to the project parameters file path
func (p *BicepProvider) parametersTemplateFilePath() string {
	infraPath := p.options.Path
	if strings.TrimSpace(infraPath) == "" {
		infraPath = "infra"
	}

	parametersFilename := fmt.Sprintf("%s.parameters.json", p.options.Module)
	return filepath.Join(p.projectPath, infraPath, parametersFilename)
}

// Gets the folder path to the specified module
func (p *BicepProvider) modulePath() string {
	infraPath := p.options.Path
	if strings.TrimSpace(infraPath) == "" {
		infraPath = "infra"
	}

	moduleFilename := fmt.Sprintf("%s.bicep", p.options.Module)
	return filepath.Join(p.projectPath, infraPath, moduleFilename)
}

// Ensures the provisioning parameters are valid and prompts the user for input as needed
func (p *BicepProvider) ensureParameters(
	ctx context.Context,
	asyncContext *async.InteractiveTaskContextWithProgress[*DeploymentPlan, *DeploymentPlanningProgress],
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
	ctx context.Context,
	azCli azcli.AzCli,
	env *environment.Environment,
	projectPath string,
	infraOptions Options,
	commandRunner exec.CommandRunner,
	console input.Console,
) (*BicepProvider, error) {
	bicepCli, err := bicep.NewBicepCli(ctx, console, commandRunner)
	if err != nil {
		return nil, err
	}

	// Default to a module named "main" if not specified.
	if strings.TrimSpace(infraOptions.Module) == "" {
		infraOptions.Module = "main"
	}

	return &BicepProvider{
		env:         env,
		projectPath: projectPath,
		options:     infraOptions,
		console:     console,
		bicepCli:    bicepCli,
		azCli:       azCli,
	}, nil
}

func init() {
	err := RegisterProvider(
		Bicep,
		func(
			ctx context.Context,
			env *environment.Environment,
			projectPath string,
			options Options,
			console input.Console,
			azCli azcli.AzCli,
			commandRunner exec.CommandRunner,
		) (Provider, error) {
			return NewBicepProvider(ctx, azCli, env, projectPath, options, commandRunner, console)
		},
	)

	if err != nil {
		panic(err)
	}
}
