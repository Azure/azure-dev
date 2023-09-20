package devcenter

import (
	"context"
	"fmt"

	"github.com/azure/azure-dev/cli/azd/pkg/devcentersdk"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	. "github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
)

const (
	DevCenterEnvName              = "AZURE_DEVCENTER_NAME"
	DevCenterCatalogEnvName       = "AZURE_DEVCENTER_CATALOG_NAME"
	DevCenterProjectEnvName       = "AZURE_DEVCENTER_PROJECT_NAME"
	DevCenterEnvTypeEnvName       = "AZURE_DEVCENTER_ENV_TYPE_NAME"
	DevCenterEnvDefinitionEnvName = "AZURE_DEVCENTER_ENV_DEFINITION_NAME"
)

type DevCenterConfig struct {
	DevCenterName             string
	CatalogName               string
	ProjectName               string
	EnvironmentType           string
	EnvironmentDefinitionName string
}

type DevCenterProvider struct {
	console         input.Console
	env             *environment.Environment
	envManager      environment.Manager
	devCenterClient devcentersdk.DevCenterClient
	prompter        *Prompter
}

func NewDevCenterProvider(
	console input.Console,
	env *environment.Environment,
	envManager environment.Manager,
	devCenterClient devcentersdk.DevCenterClient,
	prompter *Prompter,
) Provider {
	return &DevCenterProvider{
		console:         console,
		env:             env,
		devCenterClient: devCenterClient,
		prompter:        prompter,
	}
}

func (p *DevCenterProvider) Name() string {
	return "Dev Center"
}

func (p *DevCenterProvider) Initialize(ctx context.Context, projectPath string, options Options) error {
	return p.EnsureEnv(ctx)
}

func (p *DevCenterProvider) State(ctx context.Context, options *StateOptions) (*StateResult, error) {
	result := &StateResult{
		State: &State{},
	}

	return result, nil
}

func (p *DevCenterProvider) Deploy(ctx context.Context) (*DeployResult, error) {
	devCenterConfig, err := p.getDevCenterConfig()
	if err != nil {
		return nil, err
	}

	envDef, err := p.devCenterClient.
		DevCenterByName(devCenterConfig.DevCenterName).
		ProjectByName(devCenterConfig.ProjectName).
		CatalogByName(devCenterConfig.CatalogName).
		EnvironmentDefinitionByName(devCenterConfig.EnvironmentDefinitionName).
		Get(ctx)

	if err != nil {
		return nil, fmt.Errorf("failed getting environment definition: %w", err)
	}

	paramValues, err := p.prompter.PromptParameters(ctx, p.env, envDef)
	if err != nil {
		return nil, fmt.Errorf("failed prompting for parameters: %w", err)
	}

	for key, value := range paramValues {
		path := fmt.Sprintf("provision.%s", key)
		if err := p.env.Config.Set(path, value); err != nil {
			return nil, fmt.Errorf("failed setting config value %s: %w", path, err)
		}
	}

	if err := p.envManager.Save(ctx, p.env); err != nil {
		return nil, fmt.Errorf("failed saving environment: %w", err)
	}

	envName := p.env.GetEnvName()

	envSpec := devcentersdk.EnvironmentSpec{
		CatalogName:               devCenterConfig.CatalogName,
		EnvironmentType:           devCenterConfig.EnvironmentType,
		EnvironmentDefinitionName: devCenterConfig.EnvironmentDefinitionName,
		Parameters:                paramValues,
	}

	spinnerMessage := fmt.Sprintf("Creating devcenter environment %s", envName)
	p.console.ShowSpinner(ctx, spinnerMessage, input.Step)

	poller, err := p.devCenterClient.
		DevCenterByName(devCenterConfig.DevCenterName).
		ProjectByName(devCenterConfig.ProjectName).
		EnvironmentByName(envName).
		BeginPut(ctx, envSpec)

	if err != nil {
		p.console.StopSpinner(ctx, spinnerMessage, input.StepFailed)
		return nil, fmt.Errorf("failed creating environment: %w", err)
	}

	_, err = poller.PollUntilDone(ctx, nil)
	if err != nil {
		p.console.StopSpinner(ctx, spinnerMessage, input.StepFailed)
		return nil, fmt.Errorf("failed creating environment: %w", err)
	}

	p.console.StopSpinner(ctx, spinnerMessage, input.StepDone)

	result := &DeployResult{
		Deployment: &Deployment{},
	}

	return result, nil
}

func (p *DevCenterProvider) Preview(ctx context.Context) (*DeployPreviewResult, error) {
	result := &DeployPreviewResult{
		Preview: &DeploymentPreview{},
	}

	return result, nil
}

func (p *DevCenterProvider) Destroy(ctx context.Context, options DestroyOptions) (*DestroyResult, error) {
	result := &DestroyResult{}

	return result, nil
}

// EnsureEnv ensures that the environment is configured for the Dev Center provider.
// Require selection for devcenter, project, catalog, environment type, and environment definition
func (p *DevCenterProvider) EnsureEnv(ctx context.Context) error {
	devCenterName := p.env.Getenv(DevCenterEnvName)
	var err error

	if devCenterName == "" {
		devCenterName, err = p.prompter.PromptDevCenter(ctx)
		if err != nil {
			return err
		}
		p.env.DotenvSet(DevCenterEnvName, devCenterName)
	}

	projectName := p.env.Getenv(DevCenterProjectEnvName)
	if projectName == "" {
		projectName, err = p.prompter.PromptProject(ctx, devCenterName)
		if err != nil {
			return err
		}
		p.env.DotenvSet(DevCenterProjectEnvName, projectName)
	}

	catalogName := p.env.Getenv(DevCenterCatalogEnvName)
	if catalogName == "" {
		catalogName, err = p.prompter.PromptCatalog(ctx, devCenterName, projectName)
		if err != nil {
			return err
		}
		p.env.DotenvSet(DevCenterCatalogEnvName, catalogName)
	}

	envTypeName := p.env.Getenv(DevCenterEnvTypeEnvName)
	if envTypeName == "" {
		envTypeName, err = p.prompter.PromptEnvironmentType(ctx, devCenterName, projectName)
		if err != nil {
			return err
		}
		p.env.DotenvSet(DevCenterEnvTypeEnvName, envTypeName)
	}

	envDefinitionName := p.env.Getenv(DevCenterEnvDefinitionEnvName)
	if envDefinitionName == "" {
		envDefinitionName, err = p.prompter.PromptEnvironmentDefinition(ctx, devCenterName, projectName)
		if err != nil {
			return err
		}
		p.env.DotenvSet(DevCenterEnvDefinitionEnvName, envDefinitionName)
	}

	if err := p.envManager.Save(ctx, p.env); err != nil {
		return fmt.Errorf("failed saving environment: %w", err)
	}

	return nil
}

func (p *DevCenterProvider) getDevCenterConfig() (*DevCenterConfig, error) {
	devCenterName := p.env.Getenv(DevCenterEnvName)
	if devCenterName == "" {
		return nil, fmt.Errorf("missing environment variable %s", DevCenterEnvName)
	}

	projectName := p.env.Getenv(DevCenterProjectEnvName)
	if projectName == "" {
		return nil, fmt.Errorf("missing environment variable %s", DevCenterProjectEnvName)
	}

	catalogName := p.env.Getenv(DevCenterCatalogEnvName)
	if catalogName == "" {
		return nil, fmt.Errorf("missing environment variable %s", DevCenterCatalogEnvName)
	}

	envTypeName := p.env.Getenv(DevCenterEnvTypeEnvName)
	if envTypeName == "" {
		return nil, fmt.Errorf("missing environment variable %s", DevCenterEnvTypeEnvName)
	}

	envDefinitionName := p.env.Getenv(DevCenterEnvDefinitionEnvName)
	if envDefinitionName == "" {
		return nil, fmt.Errorf("missing environment variable %s", DevCenterEnvDefinitionEnvName)
	}

	return &DevCenterConfig{
		DevCenterName:             devCenterName,
		ProjectName:               projectName,
		CatalogName:               catalogName,
		EnvironmentType:           envTypeName,
		EnvironmentDefinitionName: envDefinitionName,
	}, nil
}
