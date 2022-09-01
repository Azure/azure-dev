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
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/azure/azure-dev/cli/azd/pkg/async"
	"github.com/azure/azure-dev/cli/azd/pkg/cmdsubst"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/infra"
	. "github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/azcli"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/bicep"
	"github.com/drone/envsubst"
)

type BicepTemplate struct {
	Schema         string                          `json:"$schema"`
	ContentVersion string                          `json:"contentVersion"`
	Parameters     map[string]BicepInputParameter  `json:"parameters"`
	Outputs        map[string]BicepOutputParameter `json:"outputs"`
}

type BicepInputParameter struct {
	Type         string      `json:"type"`
	DefaultValue interface{} `json:"defaultValue"`
	Value        interface{} `json:"value"`
}

type BicepOutputParameter struct {
	Type  string      `json:"type"`
	Value interface{} `json:"value"`
}

type BicepDeploymentDetails struct {
	ParameterFilePath string
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
	return []tools.ExternalTool{p.bicepCli, p.azCli}
}

// Gets the latest deployment details for the specified scope
func (p *BicepProvider) GetDeployment(ctx context.Context, scope infra.Scope) *async.InteractiveTaskWithProgress[*DeployResult, *DeployProgress] {
	return async.RunInteractiveTaskWithProgress(
		func(asyncContext *async.InteractiveTaskContextWithProgress[*DeployResult, *DeployProgress]) {
			asyncContext.SetProgress(&DeployProgress{Message: "Loading Bicep template", Timestamp: time.Now()})
			modulePath := p.modulePath()
			deployment, err := p.createDeployment(ctx, modulePath)
			if err != nil {
				asyncContext.SetError(fmt.Errorf("compiling bicep template: %w", err))
				return
			}

			asyncContext.SetProgress(&DeployProgress{Message: "Retrieving Azure deployment", Timestamp: time.Now()})
			armDeployment, err := scope.GetDeployment(ctx)
			if err != nil {
				asyncContext.SetError(fmt.Errorf("retrieving deployment: %w", err))
				return
			}

			asyncContext.SetProgress(&DeployProgress{Message: "Normalizing output parameters", Timestamp: time.Now()})
			deployment.Outputs = p.createOutputParameters(deployment, armDeployment.Properties.Outputs)

			result := DeployResult{
				Deployment: deployment,
				Operations: []azcli.AzCliResourceOperation{},
			}

			asyncContext.SetResult(&result)
		})
}

// Plans the infrastructure provisioning
func (p *BicepProvider) Plan(ctx context.Context) *async.InteractiveTaskWithProgress[*DeploymentPlan, *DeploymentPlanningProgress] {
	return async.RunInteractiveTaskWithProgress(
		func(asyncContext *async.InteractiveTaskContextWithProgress[*DeploymentPlan, *DeploymentPlanningProgress]) {
			asyncContext.SetProgress(&DeploymentPlanningProgress{Message: "Generating Bicep parameters file", Timestamp: time.Now()})
			bicepTemplate, parameterFilePath, err := p.createParametersFile(ctx, asyncContext)
			if err != nil {
				asyncContext.SetError(fmt.Errorf("creating parameters file: %w", err))
				return
			}

			modulePath := p.modulePath()
			asyncContext.SetProgress(&DeploymentPlanningProgress{Message: "Compiling Bicep template", Timestamp: time.Now()})
			deployment, err := p.createDeployment(ctx, modulePath)
			if err != nil {
				asyncContext.SetError(fmt.Errorf("creating template: %w", err))
				return
			}

			// Merge parameter values from template
			for key, param := range deployment.Parameters {
				if bicepParam, has := bicepTemplate.Parameters[key]; has {
					param.Value = bicepParam.Value
					deployment.Parameters[key] = param
				}
			}

			updated, err := p.ensureParameters(ctx, deployment)
			if err != nil {
				asyncContext.SetError(err)
				return
			}

			if updated {
				if err := p.updateParametersFile(ctx, deployment, parameterFilePath); err != nil {
					asyncContext.SetError(fmt.Errorf("updating deployment parameters: %w", err))
					return
				}
			}

			result := DeploymentPlan{
				Deployment: *deployment,
				Details: BicepDeploymentDetails{
					ParameterFilePath: parameterFilePath,
				},
			}

			asyncContext.SetResult(&result)
		})
}

// Provisioning the infrastructure within the specified template
func (p *BicepProvider) Deploy(ctx context.Context, pd *DeploymentPlan, scope infra.Scope) *async.InteractiveTaskWithProgress[*DeployResult, *DeployProgress] {
	return async.RunInteractiveTaskWithProgress(
		func(asyncContext *async.InteractiveTaskContextWithProgress[*DeployResult, *DeployProgress]) {
			done := make(chan bool)
			var operations []azcli.AzCliResourceOperation

			deploymentUrl := fmt.Sprintf(output.WithLinkFormat("https://portal.azure.com/#blade/HubsExtension/DeploymentDetailsBlade/overview/id/%s\n"), url.PathEscape(scope.DeploymentUrl()))
			p.console.Message(ctx, fmt.Sprintf("Provisioning Azure resources can take some time.\n\nYou can view detailed progress in the Azure Portal:\n%s", deploymentUrl))

			// Ensure the done marker channel is sent in all conditions
			defer func() {
				done <- true
			}()

			// Report incremental progress
			go func() {
				resourceManager := infra.NewAzureResourceManager(ctx)
				progressDisplay := NewProvisioningProgressDisplay(resourceManager, p.console, scope)

				for {
					select {
					case <-done:
						return
					case <-time.After(10 * time.Second):
						progressReport, err := progressDisplay.ReportProgress(ctx)
						if err != nil {
							// We don't want to fail the whole deployment if a progress reporting error occurs
							log.Printf("error while reporting progress: %s", err.Error())
							continue
						}

						operations = progressReport.Operations
						asyncContext.SetProgress(progressReport)
					}
				}
			}()

			// Start the deployment
			modulePath := p.modulePath()
			bicepDeploymentData := pd.Details.(BicepDeploymentDetails)
			deployResult, err := p.deployModule(ctx, scope, modulePath, bicepDeploymentData.ParameterFilePath)

			if err != nil {
				asyncContext.SetError(err)
				return
			}

			deployment := pd.Deployment
			if deployResult != nil {
				deployment.Outputs = p.createOutputParameters(&pd.Deployment, deployResult.Properties.Outputs)
			}

			result := &DeployResult{
				Operations: operations,
				Deployment: &deployment,
			}

			asyncContext.SetResult(result)
		})
}

// Destroys the specified deployment by deleting all azure resources, resource groups & deployments that are referenced.
func (p *BicepProvider) Destroy(ctx context.Context, deployment *Deployment, options DestroyOptions) *async.InteractiveTaskWithProgress[*DestroyResult, *DestroyProgress] {
	return async.RunInteractiveTaskWithProgress(
		func(asyncContext *async.InteractiveTaskContextWithProgress[*DestroyResult, *DestroyProgress]) {
			asyncContext.SetProgress(&DestroyProgress{Message: "Fetching resource groups", Timestamp: time.Now()})
			resourceGroups, err := p.getResourceGroups(ctx)
			if err != nil {
				asyncContext.SetError(err)
				return
			}

			asyncContext.SetProgress(&DestroyProgress{Message: "Fetching resources", Timestamp: time.Now()})
			allResources, err := p.getAllResources(ctx, resourceGroups)
			if err != nil {
				asyncContext.SetError(fmt.Errorf("getting resources to delete: %w", err))
				return
			}

			asyncContext.SetProgress(&DestroyProgress{Message: "Getting KeyVaults to purge", Timestamp: time.Now()})
			keyVaults, err := p.getKeyVaultsToPurge(ctx, allResources)
			if err != nil {
				asyncContext.SetError(fmt.Errorf("getting key vaults to purge: %w", err))
				return
			}

			if err := p.destroyResourceGroups(ctx, asyncContext, options, resourceGroups, allResources); err != nil {
				asyncContext.SetError(fmt.Errorf("destroying resource groups: %w", err))
				return
			}

			if err := p.purgeKeyVaults(ctx, asyncContext, keyVaults, options); err != nil {
				asyncContext.SetError(fmt.Errorf("purging key vaults: %w", err))
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
	resourceManager := infra.NewAzureResourceManager(ctx)
	resourceGroups, err := resourceManager.GetResourceGroupsForDeployment(ctx, p.env.GetSubscriptionId(), p.env.GetEnvName())
	if err != nil {
		return []string{}, err
	}

	return resourceGroups, nil
}

func (p *BicepProvider) getAllResources(ctx context.Context, resourceGroups []string) ([]azcli.AzCliResource, error) {
	allResources := []azcli.AzCliResource{}

	for _, resourceGroup := range resourceGroups {
		resources, err := p.azCli.ListResourceGroupResources(ctx, p.env.GetSubscriptionId(), resourceGroup)
		if err != nil {
			return []azcli.AzCliResource{}, nil
		}

		allResources = append(allResources, resources...)
	}

	return allResources, nil
}

// Deletes the azure resources within the deployment
func (p *BicepProvider) destroyResourceGroups(ctx context.Context, asyncContext *async.InteractiveTaskContextWithProgress[*DestroyResult, *DestroyProgress], options DestroyOptions, resourceGroups []string, allResources []azcli.AzCliResource) error {
	if !options.Force() {
		err := asyncContext.Interact(func() error {
			confirmDestroy, err := p.console.Confirm(ctx, input.ConsoleOptions{
				Message:      fmt.Sprintf("This will delete %d resources, are you sure you want to continue?", len(allResources)),
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

	for _, resourceGroup := range resourceGroups {
		message := fmt.Sprintf("%s resource group %s", output.WithErrorFormat("Deleting"), output.WithHighLightFormat(resourceGroup))
		asyncContext.SetProgress(&DestroyProgress{Message: message, Timestamp: time.Now()})

		if err := p.azCli.DeleteResourceGroup(ctx, p.env.GetSubscriptionId(), resourceGroup); err != nil {
			return err
		}

		p.console.Message(ctx, fmt.Sprintf("%s resource group %s", output.WithErrorFormat("Deleted"), output.WithHighLightFormat(resourceGroup)))
	}

	return nil
}

func (p *BicepProvider) getKeyVaults(ctx context.Context, resources []azcli.AzCliResource) ([]azcli.AzCliKeyVault, error) {
	vaults := []azcli.AzCliKeyVault{}

	for _, resource := range resources {
		if resource.Type == string(infra.AzureResourceTypeKeyVault) {
			vault, err := p.azCli.GetKeyVault(ctx, p.env.GetSubscriptionId(), resource.Name)
			if err != nil {
				return []azcli.AzCliKeyVault{}, fmt.Errorf("listing key vault %s properties: %w", resource.Name, err)
			}
			vaults = append(vaults, vault)
		}
	}

	return vaults, nil
}

func (p *BicepProvider) getKeyVaultsToPurge(ctx context.Context, resources []azcli.AzCliResource) ([]azcli.AzCliKeyVault, error) {
	vaults, err := p.getKeyVaults(ctx, resources)
	if err != nil {
		return []azcli.AzCliKeyVault{}, err
	}

	vaultsToPurge := []azcli.AzCliKeyVault{}
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
// See https://docs.microsoft.com/azure/key-vault/general/key-vault-recovery?tabs=azure-portal#what-are-soft-delete-and-purge-protection
// for more information on this feature.
func (p *BicepProvider) purgeKeyVaults(ctx context.Context, asyncContext *async.InteractiveTaskContextWithProgress[*DestroyResult, *DestroyProgress], keyVaults []azcli.AzCliKeyVault, options DestroyOptions) error {
	if len(keyVaults) > 0 && !options.Purge() {
		keyVaultWarning := fmt.Sprintf(""+
			"\nThis operation will delete and purge %d Key Vaults. These Key Vaults have soft delete enabled allowing them to be recovered for a period \n"+
			"of time after deletion. During this period, their names may not be reused.\n"+
			"You can use argument --purge to skip this confirmation.\n\n",
			len(keyVaults))

		p.console.Message(ctx, output.WithWarningFormat(keyVaultWarning))

		err := asyncContext.Interact(func() error {
			purgeKeyVaults, err := p.console.Confirm(ctx, input.ConsoleOptions{
				Message:      fmt.Sprintf("Would you like to %s delete these Key Vaults instead, allowing their names to be reused?", output.WithErrorFormat("permanently")),
				DefaultValue: false,
			})

			if err != nil {
				return fmt.Errorf("prompting for purge confirmation: %w", err)
			}

			if !purgeKeyVaults {
				return errors.New("user denied purge confirmation")
			}

			return nil
		})

		if err != nil {
			return err
		}
	}

	for _, keyVault := range keyVaults {
		progressReport := DestroyProgress{
			Timestamp: time.Now(),
			Message:   fmt.Sprintf("%s key vault %s", output.WithErrorFormat("Purging"), output.WithHighLightFormat(keyVault.Name)),
		}

		asyncContext.SetProgress(&progressReport)

		err := p.azCli.PurgeKeyVault(ctx, p.env.GetSubscriptionId(), keyVault.Name)
		if err != nil {
			return fmt.Errorf("purging key vault %s: %w", keyVault.Name, err)
		}

		p.console.Message(ctx, fmt.Sprintf("%s key vault %s", output.WithErrorFormat("Purged"), output.WithHighLightFormat(keyVault.Name)))
	}

	return nil
}

// Deletes the azure deployment
func (p *BicepProvider) deleteDeployment(ctx context.Context, asyncContext *async.InteractiveTaskContextWithProgress[*DestroyResult, *DestroyProgress]) error {
	asyncContext.SetProgress(&DestroyProgress{Message: "Deleting deployment", Timestamp: time.Now()})

	deploymentName := p.env.GetEnvName()

	if err := p.azCli.DeleteSubscriptionDeployment(ctx, p.env.GetSubscriptionId(), deploymentName); err != nil {
		return err
	}

	p.console.Message(ctx, fmt.Sprintf("%s deployment %s", output.WithErrorFormat("Deleted"), output.WithHighLightFormat(deploymentName)))

	return nil
}

// Converts the specified deployment to a bicep template parameters file and writes the file to disk.
func (p *BicepProvider) updateParametersFile(ctx context.Context, deployment *Deployment, parameterFilePath string) error {
	bicepFile := BicepTemplate{
		Schema:         "https://schema.management.azure.com/schemas/2019-04-01/deploymentParameters.json#",
		ContentVersion: "1.0.0.0",
	}

	parameters := make(map[string]BicepInputParameter)

	for key, param := range deployment.Parameters {
		parameters[key] = BicepInputParameter(param)
	}

	bicepFile.Parameters = parameters

	bytes, err := json.MarshalIndent(bicepFile, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling parameters: %w", err)
	}

	err = os.WriteFile(parameterFilePath, bytes, osutil.PermissionFile)
	if err != nil {
		return fmt.Errorf("writing parameters file: %w", err)
	}

	return nil
}

// Creates a normalized view of the azure output parameters and resolves inconsistencies in the output parameter name casings.
func (p *BicepProvider) createOutputParameters(template *Deployment, azureOutputParams map[string]azcli.AzCliDeploymentOutput) map[string]OutputParameter {
	canonicalOutputCasings := make(map[string]string, len(template.Outputs))

	for key := range template.Outputs {
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
			Type:  azureParam.Type,
			Value: azureParam.Value,
		}
	}

	return outputParams
}

// createParametersFile will read the parameters file template for environment/module specified by Options,
// do environment and command substitutions, and write out the result into a temporary file.
//
// The caller of the method is responsible for deleting the file when it is no longer necessary.
func (p *BicepProvider) createParametersFile(ctx context.Context, asyncContext *async.InteractiveTaskContextWithProgress[*DeploymentPlan, *DeploymentPlanningProgress]) (*BicepTemplate, string, error) {
	parametersTemplateFilePath := p.parametersTemplateFilePath()
	log.Printf("Reading parameters template file from: %s", parametersTemplateFilePath)
	parametersBytes, err := os.ReadFile(parametersTemplateFilePath)
	if err != nil {
		return nil, "", fmt.Errorf("reading parameter file template: %w", err)
	}

	replaced, err := envsubst.Eval(string(parametersBytes), func(name string) string {
		if val, has := p.env.Values[name]; has {
			return val
		}
		return os.Getenv(name)
	})
	if err != nil {
		return nil, "", fmt.Errorf("substituting environment variables inside parameter file: %w", err)
	}

	if cmdsubst.ContainsCommandInvocation(replaced, cmdsubst.SecretOrRandomPasswordCommandName) {
		cmdExecutor := cmdsubst.NewSecretOrRandomPasswordExecutor(ctx, p.azCli, p.env.GetSubscriptionId())
		replaced, err = cmdsubst.Eval(replaced, cmdExecutor)
		if err != nil {
			return nil, "", fmt.Errorf("substituting command output inside parameter file: %w", err)
		}
	}

	var bicepTemplate BicepTemplate
	if err := json.Unmarshal([]byte(replaced), &bicepTemplate); err != nil {
		return nil, "", fmt.Errorf("error unmarshalling Bicep template parameters: %w", err)
	}

	file, err := os.CreateTemp("", "deploymentParameters")
	if err != nil {
		return nil, "", err
	}

	_, err = file.Write([]byte(replaced))
	file.Close() // Errors OK to ignore (see the docs) and we need to close the file whether Write() succeeded or not.
	if err != nil {
		os.Remove(file.Name()) // Error OK to ignore as well.
		return nil, "", err
	}

	return &bicepTemplate, file.Name(), nil
}

// Creates the compiled template from the specified module path
func (p *BicepProvider) createDeployment(ctx context.Context, modulePath string) (*Deployment, error) {
	// Compile the bicep file into an ARM template we can create.
	compiled, err := p.bicepCli.Build(ctx, modulePath)
	if err != nil {
		return nil, fmt.Errorf("failed to compile bicep template: %w", err)
	}

	// Fetch the parameters from the template and ensure we have a value for each one, otherwise
	// prompt.
	var bicepTemplate BicepTemplate
	if err := json.Unmarshal([]byte(compiled), &bicepTemplate); err != nil {
		log.Printf("failed un-marshaling compiled arm template to JSON (err: %v), template contents:\n%s", err, compiled)
		return nil, fmt.Errorf("error un-marshaling arm template from json: %w", err)
	}

	compiledTemplate, err := p.convertToDeployment(bicepTemplate)
	if err != nil {
		return nil, fmt.Errorf("converting from bicep to compiled template: %w", err)
	}

	return compiledTemplate, nil
}

// Converts a Bicep parameters file to a generic provisioning template
func (p *BicepProvider) convertToDeployment(bicepTemplate BicepTemplate) (*Deployment, error) {
	template := Deployment{}
	parameters := make(map[string]InputParameter)
	outputs := make(map[string]OutputParameter)

	for key, param := range bicepTemplate.Parameters {
		parameters[key] = InputParameter(param)
	}

	for key, param := range bicepTemplate.Outputs {
		outputs[key] = OutputParameter(param)
	}

	template.Parameters = parameters
	template.Outputs = outputs

	return &template, nil
}

// Deploys the specified Bicep module and parameters with the selected provisioning scope (subscription vs resource group)
func (p *BicepProvider) deployModule(ctx context.Context, scope infra.Scope, bicepPath string, parametersPath string) (*azcli.AzCliDeployment, error) {
	// We've seen issues where `Deploy` completes but for a short while after, fetching the deployment fails with a `DeploymentNotFound` error.
	// Since other commands of ours use the deployment, let's try to fetch it here and if we fail with `DeploymentNotFound`,
	// ignore this error, wait a short while and retry.
	if err := scope.Deploy(ctx, bicepPath, parametersPath); err != nil {
		return nil, fmt.Errorf("failed deploying: %w", err)
	}

	var deployment azcli.AzCliDeployment
	var err error

	for i := 0; i < 10; i++ {
		time.Sleep(time.Duration(math.Min(float64(i), 3)*10) * time.Second)
		deployment, err = scope.GetDeployment(ctx)
		if errors.Is(err, azcli.ErrDeploymentNotFound) {
			continue
		} else if err != nil {
			return nil, fmt.Errorf("failed waiting for deployment: %w", err)
		} else {
			return &deployment, nil
		}
	}

	return nil, fmt.Errorf("timed out waiting for deployment: %w", err)
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
func (p *BicepProvider) ensureParameters(ctx context.Context, deployment *Deployment) (bool, error) {
	if len(deployment.Parameters) == 0 {
		return false, nil
	}

	updatedParameters := false
	for key, param := range deployment.Parameters {
		// If this parameter has a default, then there is no need for us to configure it
		if param.HasDefaultValue() {
			continue
		}
		if !param.HasValue() {
			userValue, err := p.console.Prompt(ctx, input.ConsoleOptions{
				Message: fmt.Sprintf("Please enter a value for the '%s' deployment parameter:", key),
			})

			if err != nil {
				return false, fmt.Errorf("prompting for deployment parameter: %w", err)
			}

			param.Value = userValue

			saveParameter, err := p.console.Confirm(ctx, input.ConsoleOptions{
				Message: "Save the value in the environment for future use",
			})

			if err != nil {
				return false, fmt.Errorf("prompting to save deployment parameter: %w", err)
			}

			if saveParameter {
				p.env.Values[key] = userValue
			}

			updatedParameters = true
		}
	}

	return updatedParameters, nil
}

// NewBicepProvider creates a new instance of a Bicep Infra provider
func NewBicepProvider(ctx context.Context, env *environment.Environment, projectPath string, infraOptions Options) *BicepProvider {
	azCli := azcli.GetAzCli(ctx)
	bicepCli := bicep.GetBicepCli(ctx)
	console := input.GetConsole(ctx)

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
	}
}

// Registers the Bicep provider with the provisioning module
func Register() {
	err := RegisterProvider(Bicep, func(ctx context.Context, env *environment.Environment, projectPath string, options Options) (Provider, error) {
		return NewBicepProvider(ctx, env, projectPath, options), nil
	})

	if err != nil {
		panic(err)
	}
}
