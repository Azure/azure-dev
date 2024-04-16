package devcenter

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"github.com/azure/azure-dev/cli/azd/pkg/azapi"
	"github.com/azure/azure-dev/cli/azd/pkg/devcentersdk"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/infra"
	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"golang.org/x/exp/maps"
)

const (
	ProvisionParametersConfigPath string                    = "provision.parameters"
	ProvisionKindDevCenter        provisioning.ProviderKind = "devcenter"

	// ADE environment ARM deployment tags
	DeploymentTagDevCenterName    = "AdeDevCenterName"
	DeploymentTagDevCenterProject = "AdeProjectName"
	DeploymentTagEnvironmentType  = "AdeEnvironmentTypeName"
	DeploymentTagEnvironmentName  = "AdeEnvironmentName"
)

// ProvisionProvider is a devcenter provider for provisioning ADE environments
type ProvisionProvider struct {
	console         input.Console
	env             *environment.Environment
	envManager      environment.Manager
	config          *Config
	devCenterClient devcentersdk.DevCenterClient
	resourceManager *infra.AzureResourceManager
	manager         Manager
	prompter        *Prompter
	options         provisioning.Options
}

// NewProvisionProvider creates a new devcenter provider
func NewProvisionProvider(
	console input.Console,
	env *environment.Environment,
	envManager environment.Manager,
	config *Config,
	devCenterClient devcentersdk.DevCenterClient,
	resourceManager *infra.AzureResourceManager,
	manager Manager,
	prompter *Prompter,
) provisioning.Provider {
	return &ProvisionProvider{
		console:         console,
		env:             env,
		envManager:      envManager,
		config:          config,
		devCenterClient: devCenterClient,
		resourceManager: resourceManager,
		manager:         manager,
		prompter:        prompter,
	}
}

// Name returns the name of the provider
func (p *ProvisionProvider) Name() string {
	return "Dev Center"
}

// Initialize initializes the provider
func (p *ProvisionProvider) Initialize(ctx context.Context, projectPath string, options provisioning.Options) error {
	p.options = options

	return p.EnsureEnv(ctx)
}

// State returns the state of the environment from the most recent ARM deployment
func (p *ProvisionProvider) State(
	ctx context.Context,
	options *provisioning.StateOptions,
) (*provisioning.StateResult, error) {
	if err := p.config.EnsureValid(); err != nil {
		return nil, fmt.Errorf("invalid devcenter configuration, %w", err)
	}

	envName := p.env.Name()
	environment, err := p.devCenterClient.
		DevCenterByName(p.config.Name).
		ProjectByName(p.config.Project).
		EnvironmentsByUser(p.config.User).
		EnvironmentByName(envName).
		Get(ctx)

	if err != nil {
		return nil, fmt.Errorf("failed getting environment: %w", err)
	}

	outputs, err := p.manager.Outputs(ctx, environment)
	if err != nil {
		return nil, fmt.Errorf("failed getting environment outputs: %w", err)
	}

	return &provisioning.StateResult{
		State: &provisioning.State{
			Outputs: outputs,
		},
	}, nil
}

// Deploy deploys the environment from the configured environment definition
func (p *ProvisionProvider) Deploy(ctx context.Context) (*provisioning.DeployResult, error) {
	if err := p.config.EnsureValid(); err != nil {
		return nil, fmt.Errorf("invalid devcenter configuration, %w", err)
	}

	if hasInfraTemplates(p.options.Path) {
		//nolint:lll
		warningMsg := fmt.Sprintf(
			"WARNING: IaC templates were found at '%s'. IaC templates are not supported for Dev Center environments and will be ignored.\n",
			p.options.Path,
		)

		p.console.Message(
			ctx,
			output.WithWarningFormat(warningMsg),
		)
	}

	envDef, err := p.devCenterClient.
		DevCenterByName(p.config.Name).
		ProjectByName(p.config.Project).
		CatalogByName(p.config.Catalog).
		EnvironmentDefinitionByName(p.config.EnvironmentDefinition).
		Get(ctx)

	if err != nil {
		return nil, fmt.Errorf("failed getting environment definition: %w", err)
	}

	paramValues, err := p.prompter.PromptParameters(ctx, p.env, envDef)
	if err != nil {
		return nil, fmt.Errorf("failed prompting for parameters: %w", err)
	}

	for key, value := range paramValues {
		path := fmt.Sprintf("%s.%s", ProvisionParametersConfigPath, key)
		if err := p.env.Config.Set(path, value); err != nil {
			return nil, fmt.Errorf("failed setting config value %s: %w", path, err)
		}
	}

	if err := p.envManager.Save(ctx, p.env); err != nil {
		return nil, fmt.Errorf("failed saving environment: %w", err)
	}

	envName := p.env.Name()

	// Check to see if an existing devcenter environment already exists
	existingEnv, _ := p.devCenterClient.
		DevCenterByName(p.config.Name).
		ProjectByName(p.config.Project).
		EnvironmentsByUser(p.config.User).
		EnvironmentByName(envName).
		Get(ctx)

	var spinnerMessage string
	if existingEnv == nil {
		spinnerMessage = fmt.Sprintf("Creating devcenter environment %s", output.WithHighLightFormat(envName))
	} else {
		spinnerMessage = fmt.Sprintf("Updating devcenter environment %s", output.WithHighLightFormat(envName))
	}

	envSpec := devcentersdk.EnvironmentSpec{
		CatalogName:               p.config.Catalog,
		EnvironmentType:           p.config.EnvironmentType,
		EnvironmentDefinitionName: p.config.EnvironmentDefinition,
		Parameters:                paramValues,
	}

	p.console.ShowSpinner(ctx, spinnerMessage, input.Step)

	poller, err := p.devCenterClient.
		DevCenterByName(p.config.Name).
		ProjectByName(p.config.Project).
		EnvironmentsByUser(p.config.User).
		EnvironmentByName(envName).
		BeginPut(ctx, envSpec)

	if err != nil {
		p.console.StopSpinner(ctx, spinnerMessage, input.StepFailed)
		return nil, fmt.Errorf("failed creating environment: %w", err)
	}

	p.console.StopSpinner(ctx, spinnerMessage, input.StepDone)

	pollingContext, cancel := context.WithCancel(ctx)
	defer cancel()

	spinnerMessage = "Deploying dev center environment"
	p.console.ShowSpinner(ctx, spinnerMessage, input.Step)

	go p.pollForEnvironment(pollingContext, envName)

	_, _ = poller.PollUntilDone(ctx, nil)
	if err != nil {
		p.console.StopSpinner(ctx, spinnerMessage, input.StepFailed)
		return nil, fmt.Errorf("failed creating environment: %w", err)
	}

	environment, err := p.devCenterClient.
		DevCenterByName(p.config.Name).
		ProjectByName(p.config.Project).
		EnvironmentsByUser(p.config.User).
		EnvironmentByName(envName).
		Get(ctx)

	if err != nil {
		p.console.StopSpinner(ctx, spinnerMessage, input.StepFailed)
		return nil, fmt.Errorf("failed getting environment: %w", err)
	}

	p.console.StopSpinner(ctx, spinnerMessage, input.StepDone)

	outputs, err := p.manager.Outputs(ctx, environment)
	if err != nil {
		return nil, fmt.Errorf("failed getting environment outputs: %w", err)
	}

	result := &provisioning.DeployResult{
		Deployment: &provisioning.Deployment{
			Parameters: createInputParameters(envDef, paramValues),
			Outputs:    outputs,
		},
	}

	return result, nil
}

// Preview previews the deployment of the environment from the configured environment definition
func (p *ProvisionProvider) Preview(ctx context.Context) (*provisioning.DeployPreviewResult, error) {
	return nil, fmt.Errorf("preview is not supported for devcenter")
}

// Destroy destroys the environment by deleting the ADE environment
func (p *ProvisionProvider) Destroy(
	ctx context.Context,
	options provisioning.DestroyOptions,
) (*provisioning.DestroyResult, error) {
	if err := p.config.EnsureValid(); err != nil {
		return nil, fmt.Errorf("invalid devcenter configuration, %w", err)
	}

	envName := p.env.Name()
	spinnerMessage := fmt.Sprintf("Deleting devcenter environment %s", output.WithHighLightFormat(envName))

	if !options.Force() {
		warningMessage := output.WithWarningFormat(
			"WARNING: This will delete the following Dev Center environment and all of its resources:\n",
		)
		p.console.Message(ctx, warningMessage)

		p.console.Message(ctx, fmt.Sprintf("Dev Center: %s", output.WithHighLightFormat(p.config.Name)))
		p.console.Message(ctx, fmt.Sprintf("Project: %s", output.WithHighLightFormat(p.config.Project)))
		p.console.Message(ctx, fmt.Sprintf("Environment Type: %s", output.WithHighLightFormat(p.config.EnvironmentType)))
		p.console.Message(ctx,
			fmt.Sprintf("Environment Definition: %s", output.WithHighLightFormat(p.config.EnvironmentDefinition)),
		)
		p.console.Message(ctx, fmt.Sprintf("Environment: %s\n", output.WithHighLightFormat(envName)))

		confirm, err := p.console.Confirm(ctx, input.ConsoleOptions{
			Message:      "Are you sure you want to continue?",
			DefaultValue: false,
		})

		if err != nil {
			p.console.Message(ctx, "")
			p.console.ShowSpinner(ctx, spinnerMessage, input.Step)
			p.console.StopSpinner(ctx, spinnerMessage, input.StepFailed)
			return nil, fmt.Errorf("destroy operation interrupted: %w", err)
		}

		p.console.Message(ctx, "\n")

		if !confirm {
			p.console.ShowSpinner(ctx, spinnerMessage, input.Step)
			p.console.StopSpinner(ctx, spinnerMessage, input.StepSkipped)
			return nil, fmt.Errorf("destroy operation cancelled")
		}
	}

	devCenterEnv, err := p.devCenterClient.
		DevCenterByName(p.config.Name).
		ProjectByName(p.config.Project).
		EnvironmentsByUser(p.config.User).
		EnvironmentByName(envName).
		Get(ctx)

	if err != nil {
		return nil, fmt.Errorf("failed getting devcenter environment: %w", err)
	}

	// Get environment outputs to invalidate them after destroy
	outputs, err := p.manager.Outputs(ctx, devCenterEnv)
	if err != nil {
		return nil, fmt.Errorf("failed getting environment outputs: %w", err)
	}

	p.console.ShowSpinner(ctx, spinnerMessage, input.Step)

	poller, err := p.devCenterClient.
		DevCenterByName(p.config.Name).
		ProjectByName(p.config.Project).
		EnvironmentsByUser(p.config.User).
		EnvironmentByName(envName).
		BeginDelete(ctx)

	if err != nil {
		p.console.StopSpinner(ctx, spinnerMessage, input.StepFailed)
		return nil, fmt.Errorf("failed deleting environment: %w", err)
	}

	_, err = poller.PollUntilDone(ctx, nil)
	if err != nil {
		p.console.StopSpinner(ctx, spinnerMessage, input.StepFailed)
		return nil, fmt.Errorf("failed deleting environment: %w", err)
	}

	p.console.StopSpinner(ctx, spinnerMessage, input.StepDone)

	result := &provisioning.DestroyResult{
		InvalidatedEnvKeys: maps.Keys(outputs),
	}

	return result, nil
}

// EnsureEnv ensures that the environment is configured for the Dev Center provider.
// Require selection for devcenter, project, catalog, environment type, and environment definition
func (p *ProvisionProvider) EnsureEnv(ctx context.Context) error {
	// Cache config values prior to prompting user
	currentConfig := *p.config
	updatedConfig, err := p.prompter.PromptForConfig(ctx)
	if err != nil {
		return err
	}

	if updatedConfig.EnvironmentType == "" {
		envType, err := p.prompter.PromptEnvironmentType(ctx, updatedConfig.Name, updatedConfig.Project)
		if err != nil {
			return err
		}
		updatedConfig.EnvironmentType = envType.Name
	}

	if updatedConfig.User == "" {
		updatedConfig.User = "me"
	}

	// Set any missing config values in environment configuration for future use
	if currentConfig.Name == "" {
		if err := p.env.Config.Set(DevCenterNamePath, updatedConfig.Name); err != nil {
			return err
		}
	}

	if currentConfig.Project == "" {
		if err := p.env.Config.Set(DevCenterProjectPath, updatedConfig.Project); err != nil {
			return err
		}
	}

	if currentConfig.Catalog == "" {
		if err := p.env.Config.Set(DevCenterCatalogPath, updatedConfig.Catalog); err != nil {
			return err
		}
	}

	if currentConfig.EnvironmentType == "" {
		if err := p.env.Config.Set(DevCenterEnvTypePath, updatedConfig.EnvironmentType); err != nil {
			return err
		}
	}

	if currentConfig.EnvironmentDefinition == "" {
		if err := p.env.Config.Set(DevCenterEnvDefinitionPath, updatedConfig.EnvironmentDefinition); err != nil {
			return err
		}
	}

	if currentConfig.User == "" {
		if err := p.env.Config.Set(DevCenterUserPath, updatedConfig.User); err != nil {
			return err
		}
	}

	if err := p.envManager.Save(ctx, p.env); err != nil {
		return fmt.Errorf("failed saving environment: %w", err)
	}

	p.config = updatedConfig

	return nil
}

// Polls for the ADE environment and ARM deployment to be created
func (p *ProvisionProvider) pollForEnvironment(ctx context.Context, envName string) {
	// Disable reporting progress if needed
	if use, err := strconv.ParseBool(os.Getenv("AZD_DEBUG_PROVISION_PROGRESS_DISABLE")); err == nil && use {
		log.Println("Disabling progress reporting since AZD_DEBUG_PROVISION_PROGRESS_DISABLE was set")
		return
	}

	initialDelay := 3 * time.Second
	regularDelay := 5 * time.Second
	timer := time.NewTimer(initialDelay)
	pollStartTime := time.Now()

	for {
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
			environment, err := p.devCenterClient.
				DevCenterByName(p.config.Name).
				ProjectByName(p.config.Project).
				EnvironmentsByUser(p.config.User).
				EnvironmentByName(envName).
				Get(ctx)

			// We need to wait until the ADE environment has created the resource group
			if err != nil ||
				environment == nil ||
				environment.ProvisioningState == devcentersdk.ProvisioningStateCreating ||
				environment.ResourceGroupId == "" {
				timer.Reset(regularDelay)
				continue
			}

			// After the resource group has been created
			// We can start polling for a new deployment that started after we started polling
			deployment, err := p.manager.Deployment(ctx, environment, func(d *armresources.DeploymentExtended) bool {
				return *d.Properties.ProvisioningState == "Running" && d.Properties.Timestamp.After(pollStartTime)
			})

			if err != nil || deployment == nil {
				timer.Reset(regularDelay)
				continue
			}

			timer.Stop()

			// Finally polling for provisioning progress
			go p.pollForProgress(ctx, deployment)
		}
	}
}

// Polls the ARM deployment triggered by ADE and start reporting incremental provisioning progress
func (p *ProvisionProvider) pollForProgress(ctx context.Context, deployment infra.Deployment) {
	// Disable reporting progress if needed
	if use, err := strconv.ParseBool(os.Getenv("AZD_DEBUG_PROVISION_PROGRESS_DISABLE")); err == nil && use {
		log.Println("Disabling progress reporting since AZD_DEBUG_PROVISION_PROGRESS_DISABLE was set")
		return
	}

	// Report incremental progress
	progressDisplay := provisioning.NewProvisioningProgressDisplay(p.resourceManager, p.console, deployment)

	initialDelay := 3 * time.Second
	regularDelay := 10 * time.Second
	timer := time.NewTimer(initialDelay)
	queryStartTime := time.Now()

	for {
		select {
		case <-ctx.Done():
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
}

func mapBicepTypeToInterfaceType(s string) provisioning.ParameterType {
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
func createOutputParameters(
	deploymentOutputs map[string]azapi.AzCliDeploymentOutput,
) map[string]provisioning.OutputParameter {
	outputParams := map[string]provisioning.OutputParameter{}

	for key, azureParam := range deploymentOutputs {
		// To support BYOI (bring your own infrastructure) scenarios we will default to UPPER when canonical casing
		// is not found in the parameters file to workaround strange azure behavior with OUTPUT values that look
		// like `azurE_RESOURCE_GROUP`
		paramName := strings.ToUpper(key)

		outputParams[paramName] = provisioning.OutputParameter{
			Type:  mapBicepTypeToInterfaceType(azureParam.Type),
			Value: azureParam.Value,
		}
	}

	return outputParams
}

func createInputParameters(
	environmentDefinition *devcentersdk.EnvironmentDefinition,
	parameterValues map[string]any,
) map[string]provisioning.InputParameter {
	inputParams := map[string]provisioning.InputParameter{}

	for _, param := range environmentDefinition.Parameters {
		inputParams[param.Id] = provisioning.InputParameter{
			Type:         string(param.Type),
			DefaultValue: param.Default,
			Value:        parameterValues[param.Id],
		}
	}

	return inputParams
}

// hasInfraTemplates returns true if the specified path contains any infrastructure templates
func hasInfraTemplates(path string) bool {
	if _, err := os.Stat(path); err != nil && errors.Is(err, os.ErrNotExist) {
		return false
	}

	entries, err := os.ReadDir(path)
	if errors.Is(err, os.ErrNotExist) {
		return false
	}

	return len(entries) > 0
}
