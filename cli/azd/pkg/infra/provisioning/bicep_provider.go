package provisioning

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
	"github.com/azure/azure-dev/cli/azd/pkg/azure"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/infra"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/azcli"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/bicep"
	"github.com/drone/envsubst"
)

type BicepTemplate struct {
	//nolint:all
	Schema         string                          `json:"$schema`
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

// Previews the infrastructure provisioning
func (p *BicepProvider) Preview(ctx context.Context) *async.InteractiveTaskWithProgress[*PreviewResult, *PreviewProgress] {
	return async.RunInteractiveTaskWithProgress(
		func(asyncContext *async.InteractiveTaskContextWithProgress[*PreviewResult, *PreviewProgress]) {
			asyncContext.SetProgress(&PreviewProgress{Message: "Generating Bicep parameters file", Timestamp: time.Now()})
			bicepTemplate, err := p.createParametersFile()
			if err != nil {
				asyncContext.SetError(fmt.Errorf("creating parameters file: %w", err))
				return
			}

			modulePath := p.modulePath()
			asyncContext.SetProgress(&PreviewProgress{Message: "Compiling Bicep template", Timestamp: time.Now()})
			template, err := p.createPreview(ctx, modulePath)
			if err != nil {
				asyncContext.SetError(fmt.Errorf("creating template: %w", err))
				return
			}

			// Merge parameter values from template
			for key, param := range template.Parameters {
				if bicepParam, has := bicepTemplate.Parameters[key]; has {
					param.Value = bicepParam.Value
					template.Parameters[key] = param
				}
			}

			result := PreviewResult{
				Preview: *template,
			}

			asyncContext.SetResult(&result)
		})
}

func (p *BicepProvider) UpdatePlan(ctx context.Context, preview Preview) error {
	bicepFile := BicepTemplate{
		Schema:         "https://schema.management.azure.com/schemas/2019-04-01/deploymentParameters.json#",
		ContentVersion: "1.0.0.0",
	}

	parameters := make(map[string]BicepInputParameter)

	for key, param := range preview.Parameters {
		parameters[key] = BicepInputParameter(param)
	}

	bicepFile.Parameters = parameters

	bytes, err := json.MarshalIndent(bicepFile, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling parameters: %w", err)
	}

	parametersFilePath := p.parametersFilePath()
	err = os.WriteFile(parametersFilePath, bytes, 0644)
	if err != nil {
		return fmt.Errorf("writing parameters file: %w", err)
	}

	return nil
}

// Provisioning the infrastructure within the specified template
func (p *BicepProvider) Deploy(ctx context.Context, preview *Preview, scope Scope) *async.InteractiveTaskWithProgress[*DeployResult, *DeployProgress] {
	return async.RunInteractiveTaskWithProgress(
		func(asyncContext *async.InteractiveTaskContextWithProgress[*DeployResult, *DeployProgress]) {
			isDeploymentComplete := false

			err := asyncContext.Interact(func() error {
				deploymentSlug := azure.SubscriptionDeploymentRID(p.env.GetSubscriptionId(), p.env.GetEnvName())
				deploymentUrl := fmt.Sprintf("https://portal.azure.com/#blade/HubsExtension/DeploymentDetailsBlade/overview/id/%s\n\n", url.PathEscape(deploymentSlug))
				err := p.console.Message(ctx, fmt.Sprintf("Provisioning Azure resources can take some time.\n\nYou can view detailed progress in the Azure Portal:\n%s", deploymentUrl))

				if err != nil {
					return err
				}

				return nil
			})

			if err != nil {
				asyncContext.SetError(err)
				return
			}

			// Start the deployment
			go func() {
				defer func() {
					isDeploymentComplete = true
				}()

				modulePath := p.modulePath()
				parametersFilePath := p.parametersFilePath()
				deployResult, err := p.deployModule(ctx, scope, modulePath, parametersFilePath)
				var outputs map[string]PreviewOutputParameter

				if err != nil {
					asyncContext.SetError(err)
					return
				}

				if deployResult != nil {
					outputs = p.createOutputParameters(preview, deployResult.Properties.Outputs)
				}

				result := &DeployResult{
					Operations: nil,
					Outputs:    outputs,
				}

				asyncContext.SetResult(result)
			}()

			// Report incremental progress
			resourceManager := infra.NewAzureResourceManager(p.azCli)

			for range time.After(10 * time.Second) {
				if isDeploymentComplete {
					break
				}

				ops, err := resourceManager.GetDeploymentResourceOperations(ctx, p.env.GetSubscriptionId(), p.env.GetEnvName())
				if err != nil || len(ops) == 0 {
					continue
				}

				progressReport := DeployProgress{
					Timestamp:  time.Now(),
					Operations: ops,
				}

				asyncContext.SetProgress(&progressReport)
			}
		})
}

func (p *BicepProvider) Destroy(ctx context.Context, preview *Preview) *async.InteractiveTaskWithProgress[*DestroyResult, *DestroyProgress] {
	return async.RunInteractiveTaskWithProgress(
		func(asyncContext *async.InteractiveTaskContextWithProgress[*DestroyResult, *DestroyProgress]) {
			destroyResult := DestroyResult{}

			asyncContext.SetProgress(&DestroyProgress{Message: "Fetching resource groups", Timestamp: time.Now()})
			resourceManager := infra.NewAzureResourceManager(p.azCli)
			resourceGroups, err := resourceManager.GetResourceGroupsForDeployment(ctx, p.env.GetSubscriptionId(), p.env.GetEnvName())
			if err != nil {
				asyncContext.SetError(fmt.Errorf("discovering resource groups from deployment: %w", err))
			}

			var allResources []azcli.AzCliResource

			asyncContext.SetProgress(&DestroyProgress{Message: "Fetching resources", Timestamp: time.Now()})
			for _, resourceGroup := range resourceGroups {
				resources, err := p.azCli.ListResourceGroupResources(ctx, p.env.GetSubscriptionId(), resourceGroup)
				if err != nil {
					asyncContext.SetError(fmt.Errorf("listing resource group %s: %w", resourceGroup, err))
				}

				allResources = append(allResources, resources...)
			}

			err = asyncContext.Interact(func() error {
				confirmDestroy, err := p.console.Confirm(ctx, input.ConsoleOptions{
					Message:      fmt.Sprintf("This will delete %d resources, are you sure you want to continue?", len(allResources)),
					DefaultValue: false,
				})

				if err != nil {
					return err
				}

				if !confirmDestroy {
					return errors.New("user denied confirmation")
				}

				return nil
			})

			if err != nil {
				asyncContext.SetError(err)
				return
			}

			for _, resourceGroup := range resourceGroups {
				message := fmt.Sprintf("Deleting resource group '%s'", resourceGroup)
				asyncContext.SetProgress(&DestroyProgress{Message: message, Timestamp: time.Now()})

				if err := p.azCli.DeleteResourceGroup(ctx, p.env.GetSubscriptionId(), resourceGroup); err != nil {
					asyncContext.SetError(fmt.Errorf("deleting resource group %s: %w", resourceGroup, err))
				}
			}

			asyncContext.SetProgress(&DestroyProgress{Message: "Deleting deployment", Timestamp: time.Now()})
			if err := p.azCli.DeleteSubscriptionDeployment(ctx, p.env.GetSubscriptionId(), p.env.GetEnvName()); err != nil {
				asyncContext.SetError(fmt.Errorf("deleting subscription deployment: %w", err))
			}

			destroyResult.Resources = allResources
			asyncContext.SetResult(&destroyResult)
		})
}

func (p *BicepProvider) createOutputParameters(template *Preview, azureOutputParams map[string]azcli.AzCliDeploymentOutput) map[string]PreviewOutputParameter {
	canonicalOutputCasings := make(map[string]string, len(template.Outputs))

	for key := range template.Outputs {
		canonicalOutputCasings[strings.ToLower(key)] = key
	}

	outputParams := make(map[string]PreviewOutputParameter, len(azureOutputParams))

	for key, azureParam := range azureOutputParams {
		var paramName string
		canonicalCasing, found := canonicalOutputCasings[strings.ToLower(key)]
		if found {
			paramName = canonicalCasing
		} else {
			paramName = key
		}

		outputParams[paramName] = PreviewOutputParameter{
			Type:  azureParam.Type,
			Value: azureParam.Value,
		}
	}

	return outputParams
}

// Copies the Bicep parameters file from the project template into the .azure environment folder
func (p *BicepProvider) createParametersFile() (*BicepTemplate, error) {
	// Copy the parameter template file to the environment working directory and do substitutions.
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
		return nil, fmt.Errorf("substituting parameter file: %w", err)
	}

	parametersFilePath := p.parametersFilePath()
	writeDir := filepath.Dir(parametersFilePath)
	if err := os.MkdirAll(writeDir, 0755); err != nil {
		return nil, fmt.Errorf("creating directory structure: %w", err)
	}

	log.Printf("Writing parameters file to: %s", parametersFilePath)
	err = os.WriteFile(parametersFilePath, []byte(replaced), 0644)
	if err != nil {
		return nil, fmt.Errorf("writing parameter file: %w", err)
	}

	var bicepTemplate BicepTemplate
	if err := json.Unmarshal([]byte(replaced), &bicepTemplate); err != nil {
		return nil, fmt.Errorf("error unmarshalling Bicep template parameters: %w", err)
	}

	return &bicepTemplate, nil
}

// Creates the compiled template from the specified module path
func (p *BicepProvider) createPreview(ctx context.Context, modulePath string) (*Preview, error) {
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

	compiledTemplate, err := p.convertToPreview(bicepTemplate)
	if err != nil {
		return nil, fmt.Errorf("converting from bicep to compiled template: %w", err)
	}

	return compiledTemplate, nil
}

// Converts a Bicep parameters file to a generic provisioning template
func (p *BicepProvider) convertToPreview(bicepTemplate BicepTemplate) (*Preview, error) {
	template := Preview{}
	parameters := make(map[string]PreviewInputParameter)
	outputs := make(map[string]PreviewOutputParameter)

	for key, param := range bicepTemplate.Parameters {
		parameters[key] = PreviewInputParameter(param)
	}

	for key, param := range bicepTemplate.Outputs {
		outputs[key] = PreviewOutputParameter(param)
	}

	template.Parameters = parameters
	template.Outputs = outputs

	return &template, nil
}

// Deploys the specified Bicep module and parameters with the selected provisioning scope (subscription vs resource group)
func (p *BicepProvider) deployModule(ctx context.Context, scope Scope, bicepPath string, parametersPath string) (*azcli.AzCliDeployment, error) {
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

// Gets the path to the staging .azure parameters file path
func (p *BicepProvider) parametersFilePath() string {
	parametersFilename := fmt.Sprintf("%s.parameters.json", p.options.Module)
	return filepath.Join(p.projectPath, ".azure", p.env.GetEnvName(), p.options.Path, parametersFilename)
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

// NewBicepProvider creates a new instance of a Bicep Infra provider
func NewBicepProvider(env *environment.Environment, projectPath string, options Options, console input.Console, bicepArgs bicep.NewBicepCliArgs) Provider {
	bicepCli := bicep.NewBicepCli(bicepArgs)

	return &BicepProvider{
		env:         env,
		projectPath: projectPath,
		options:     options,
		console:     console,
		bicepCli:    bicepCli,
		azCli:       bicepArgs.AzCli,
	}
}
