package provisioning

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"log"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/infra"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/drone/envsubst"
)

type BicepTemplate struct {
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

// BicepInfraProvider exposes infrastructure provisioning using Azure Bicep templates
type BicepInfraProvider struct {
	env         *environment.Environment
	projectPath string
	options     InfrastructureOptions
	bicepCli    tools.BicepCli
	azCli       tools.AzCli
}

// Name gets the name of the infra provider
func (p *BicepInfraProvider) Name() string {
	return "Bicep"
}

func (p *BicepInfraProvider) RequiredExternalTools() []tools.ExternalTool {
	return []tools.ExternalTool{p.bicepCli, p.azCli}
}

// Plans the infrastructure provisioning
func (p *BicepInfraProvider) Plan(ctx context.Context) (*ProvisioningPlan, error) {
	bicepTemplate, err := p.createParametersFile()
	if err != nil {
		return nil, fmt.Errorf("creating parameters file: %w", err)
	}

	modulePath := p.modulePath()
	template, err := p.createPlan(ctx, modulePath)
	if err != nil {
		return nil, fmt.Errorf("creating template: %w", err)
	}

	// Merge parameter values from template
	for key, param := range template.Parameters {
		if bicepParam, has := bicepTemplate.Parameters[key]; has {
			param.Value = bicepParam.Value
			template.Parameters[key] = param
		}
	}

	return template, nil
}

func (p *BicepInfraProvider) SaveTemplate(ctx context.Context, template ProvisioningPlan) error {
	bicepFile := BicepTemplate{
		Schema:         "https://schema.management.azure.com/schemas/2019-04-01/deploymentParameters.json#",
		ContentVersion: "1.0.0.0",
	}

	parameters := make(map[string]BicepInputParameter)

	for key, param := range template.Parameters {
		parameters[key] = BicepInputParameter{
			Type:         param.Type,
			DefaultValue: param.DefaultValue,
			Value:        param.Value,
		}
	}

	bicepFile.Parameters = parameters

	bytes, err := json.MarshalIndent(bicepFile, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling parameters: %w", err)
	}

	parametersFilePath := p.parametersFilePath()
	err = ioutil.WriteFile(parametersFilePath, bytes, 0644)
	if err != nil {
		return fmt.Errorf("writing parameters file: %w", err)
	}

	return nil
}

// Provisioning the infrastructure within the specified template
func (p *BicepInfraProvider) Apply(ctx context.Context, plan *ProvisioningPlan, scope ProvisioningScope) (<-chan *ProvisionApplyResult, <-chan *ProvisionApplyProgress) {
	resultChannel := make(chan *ProvisionApplyResult, 1)
	progressChannel := make(chan *ProvisionApplyProgress)

	go func() {
		defer close(resultChannel)
		defer close(progressChannel)

		isDeploymentComplete := false

		// Start the deployment
		go func() {
			modulePath := p.modulePath()
			parametersFilePath := p.parametersFilePath()
			deployResult, err := p.applyModule(ctx, scope, modulePath, parametersFilePath)
			var outputs map[string]ProvisioningPlanOutputParameter

			if deployResult != nil {
				outputs = p.createOutputParameters(plan, deployResult.Properties.Outputs)
			}

			resultChannel <- &ProvisionApplyResult{
				Error:      err,
				Operations: nil,
				Outputs:    outputs,
			}

			isDeploymentComplete = true
		}()

		// Report incremental progress
		resourceManager := infra.NewAzureResourceManager(p.azCli)
		for {
			if isDeploymentComplete {
				break
			}

			select {
			case <-time.After(10 * time.Second):
				ops, err := resourceManager.GetDeploymentResourceOperations(ctx, p.env.GetSubscriptionId(), p.env.GetEnvName())
				if err != nil || len(*ops) == 0 {
					continue
				}

				progressReport := ProvisionApplyProgress{
					Timestamp:  time.Now(),
					Operations: *ops,
				}

				// If deployment is already completed don't report status since the channel has been closed
				if !isDeploymentComplete {
					progressChannel <- &progressReport
				}
			}
		}
	}()

	return resultChannel, progressChannel
}

func (p *BicepInfraProvider) Destroy(ctx context.Context, plan *ProvisioningPlan) (<-chan *ProvisionDestroyResult, <-chan *ProvisionDestroyProgress) {
	resultChannel := make(chan *ProvisionDestroyResult, 1)
	progressChannel := make(chan *ProvisionDestroyProgress)

	go func() {
		defer close(resultChannel)
		defer close(progressChannel)

		destroyResult := ProvisionDestroyResult{}

		progressChannel <- &ProvisionDestroyProgress{Message: "Fetching resource groups", Timestamp: time.Now()}
		resourceManager := infra.NewAzureResourceManager(p.azCli)
		resourceGroups, err := resourceManager.GetResourceGroupsForDeployment(ctx, p.env.GetSubscriptionId(), p.env.GetEnvName())
		if err != nil {
			destroyResult.Error = fmt.Errorf("discovering resource groups from deployment: %w", err)
		}

		var allResources []tools.AzCliResource

		progressChannel <- &ProvisionDestroyProgress{Message: "Fetching resources", Timestamp: time.Now()}
		for _, resourceGroup := range resourceGroups {
			resources, err := p.azCli.ListResourceGroupResources(ctx, p.env.GetSubscriptionId(), resourceGroup)
			if err != nil {
				destroyResult.Error = fmt.Errorf("listing resource group %s: %w", resourceGroup, err)
			}

			allResources = append(allResources, resources...)
		}

		for _, resourceGroup := range resourceGroups {
			message := fmt.Sprintf("Deleting resource group '%s'", resourceGroup)
			progressChannel <- &ProvisionDestroyProgress{Message: message, Timestamp: time.Now()}

			if err := p.azCli.DeleteResourceGroup(ctx, p.env.GetSubscriptionId(), resourceGroup); err != nil {
				destroyResult.Error = fmt.Errorf("deleting resource group %s: %w", resourceGroup, err)
			}
		}

		progressChannel <- &ProvisionDestroyProgress{Message: "Deleting deployment", Timestamp: time.Now()}
		if err := p.azCli.DeleteSubscriptionDeployment(ctx, p.env.GetSubscriptionId(), p.env.GetEnvName()); err != nil {
			destroyResult.Error = fmt.Errorf("deleting subscription deployment: %w", err)
		}

		destroyResult.Resources = allResources

		resultChannel <- &destroyResult
	}()

	return resultChannel, progressChannel
}

func (p *BicepInfraProvider) createOutputParameters(template *ProvisioningPlan, azureOutputParams map[string]tools.AzCliDeploymentOutput) map[string]ProvisioningPlanOutputParameter {
	canonicalOutputCasings := make(map[string]string, len(template.Outputs))

	for key := range template.Outputs {
		canonicalOutputCasings[strings.ToLower(key)] = key
	}

	outputParams := make(map[string]ProvisioningPlanOutputParameter, len(azureOutputParams))

	for key, azureParam := range azureOutputParams {
		var paramName string
		canonicalCasing, found := canonicalOutputCasings[strings.ToLower(key)]
		if found {
			paramName = canonicalCasing
		} else {
			paramName = key
		}

		outputParams[paramName] = ProvisioningPlanOutputParameter{
			Type:  azureParam.Type,
			Value: azureParam.Value,
		}
	}

	return outputParams
}

// Copies the Bicep parameters file from the project template into the .azure environment folder
func (p *BicepInfraProvider) createParametersFile() (*BicepTemplate, error) {
	// Copy the parameter template file to the environment working directory and do substitutions.
	parametersTemplateFilePath := p.parametersTemplateFilePath()
	log.Printf("Reading parameters template file from: %s", parametersTemplateFilePath)
	parametersBytes, err := ioutil.ReadFile(parametersTemplateFilePath)
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
	err = ioutil.WriteFile(parametersFilePath, []byte(replaced), 0644)
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
func (p *BicepInfraProvider) createPlan(ctx context.Context, modulePath string) (*ProvisioningPlan, error) {
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

	compiledTemplate, err := p.convertToPlan(bicepTemplate)
	if err != nil {
		return nil, fmt.Errorf("converting from bicep to compiled template: %w", err)
	}

	return compiledTemplate, nil
}

// Converts a Bicep parameters file to a generic provisioning template
func (p *BicepInfraProvider) convertToPlan(bicepTemplate BicepTemplate) (*ProvisioningPlan, error) {
	template := ProvisioningPlan{}
	parameters := make(map[string]ProvisioningPlanInputParameter)
	outputs := make(map[string]ProvisioningPlanOutputParameter)

	for key, param := range bicepTemplate.Parameters {
		parameters[key] = ProvisioningPlanInputParameter{
			Type:         param.Type,
			Value:        param.Value,
			DefaultValue: param.DefaultValue,
		}
	}

	for key, param := range bicepTemplate.Outputs {
		outputs[key] = ProvisioningPlanOutputParameter{
			Type:  param.Type,
			Value: param.Value,
		}
	}

	template.Parameters = parameters
	template.Outputs = outputs

	return &template, nil
}

// Deploys the specified Bicep module and parameters with the selected provisioning scope (subscription vs resource group)
func (p *BicepInfraProvider) applyModule(ctx context.Context, scope ProvisioningScope, bicepPath string, parametersPath string) (*tools.AzCliDeployment, error) {
	// We've seen issues where `Deploy` completes but for a short while after, fetching the deployment fails with a `DeploymentNotFound` error.
	// Since other commands of ours use the deployment, let's try to fetch it here and if we fail with `DeploymentNotFound`,
	// ignore this error, wait a short while and retry.
	if err := scope.Deploy(ctx, bicepPath, parametersPath); err != nil {
		return nil, fmt.Errorf("failed deploying: %w", err)
	}

	var deployment tools.AzCliDeployment
	var err error

	for i := 0; i < 10; i++ {
		time.Sleep(time.Duration(math.Min(float64(i), 3)*10) * time.Second)
		deployment, err = scope.GetDeployment(ctx)
		if errors.Is(err, tools.ErrDeploymentNotFound) {
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
func (p *BicepInfraProvider) parametersTemplateFilePath() string {
	infraPath := p.options.Path
	if strings.TrimSpace(infraPath) == "" {
		infraPath = "infra"
	}

	parametersFilename := fmt.Sprintf("%s.parameters.json", p.options.Module)
	return filepath.Join(p.projectPath, infraPath, parametersFilename)
}

// Gets the path to the staging .azure parameters file path
func (p *BicepInfraProvider) parametersFilePath() string {
	parametersFilename := fmt.Sprintf("%s.parameters.json", p.options.Module)
	return filepath.Join(p.projectPath, ".azure", p.env.GetEnvName(), p.options.Path, parametersFilename)
}

// Gets the folder path to the specified module
func (p *BicepInfraProvider) modulePath() string {
	infraPath := p.options.Path
	if strings.TrimSpace(infraPath) == "" {
		infraPath = "infra"
	}

	moduleFilename := fmt.Sprintf("%s.bicep", p.options.Module)
	return filepath.Join(p.projectPath, infraPath, moduleFilename)
}

// NewBicepInfraProvider creates a new instance of a Bicep Infra provider
func NewBicepInfraProvider(env *environment.Environment, projectPath string, options InfrastructureOptions, azCli tools.AzCli) InfraProvider {
	bicepCli := tools.NewBicepCli(azCli)

	return &BicepInfraProvider{
		env:         env,
		projectPath: projectPath,
		options:     options,
		bicepCli:    bicepCli,
		azCli:       azCli,
	}
}
