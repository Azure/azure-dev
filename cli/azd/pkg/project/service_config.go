package project

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/azure/azure-dev/cli/azd/pkg/commands"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
)

type ServiceConfig struct {
	// Reference to the parent project configuration
	Project *ProjectConfig `yaml:"omitempty"`
	// The friendly name/key of the project from the azure.yaml file
	Name string
	// The name used to override the default azure resource name
	ResourceName string `yaml:"resourceName"`
	// The relative path to the project folder from the project root
	RelativePath string `yaml:"project"`
	// The azure hosting model to use, ex) appservice, function, containerapp
	Host string `yaml:"host"`
	// The programming language of the project
	Language string `yaml:"language"`
	// The output path for build artifacts
	OutputPath string `yaml:"dist"`
	// The infrastructure module name to use for this project
	ModuleName string `yaml:"moduleName"`
	// The custom service type options
	Options map[string]interface{} `yaml:"options"`
}

// Path returns the fully qualified path to the project
func (sc *ServiceConfig) Path() string {
	return filepath.Join(sc.Project.Path, sc.RelativePath)
}

// GetService constructs a parsed Service object from the Service configuration
func (sc *ServiceConfig) GetService(ctx context.Context, project *Project, env *environment.Environment, scope *environment.DeploymentScope) (*Service, error) {
	framework, err := sc.GetFrameworkService(ctx, env)
	if err != nil {
		return nil, fmt.Errorf("creating framework service: %w", err)
	}

	serviceTarget, err := sc.GetServiceTarget(ctx, env, scope)
	if err != nil {
		return nil, fmt.Errorf("creating service target: %w", err)
	}

	return &Service{
		Project:   project,
		Config:    sc,
		Framework: *framework,
		Target:    *serviceTarget,
		Scope:     scope,
	}, nil
}

// GetServiceTarget constructs a ServiceTarget from the underlying service configuration
func (sc *ServiceConfig) GetServiceTarget(ctx context.Context, env *environment.Environment, scope *environment.DeploymentScope) (*ServiceTarget, error) {
	var target ServiceTarget

	azCli := commands.GetAzCliFromContext(ctx)

	switch sc.Host {
	case "", string(AppServiceTarget):
		target = NewAppServiceTarget(sc, env, scope, azCli)
	case string(ContainerAppTarget):
		target = NewContainerAppTarget(sc, env, scope, azCli, tools.NewDocker())
	case string(AzureFunctionTarget):
		target = NewFunctionAppTarget(sc, env, scope, azCli)
	case string(StaticWebAppTarget):
		target = NewStaticWebAppTarget(sc, env, scope, azCli, tools.NewSwaCli())
	default:
		return nil, fmt.Errorf("unsupported host '%s' for service '%s'", sc.Host, sc.Name)
	}

	return &target, nil
}

// GetFrameworkService constructs a framework service from the underlying service configuration
func (sc *ServiceConfig) GetFrameworkService(ctx context.Context, env *environment.Environment) (*FrameworkService, error) {
	var frameworkService FrameworkService

	switch sc.Language {
	case "", "dotnet", "csharp", "fsharp":
		frameworkService = NewDotNetProject(sc, env)
	case "py", "python":
		frameworkService = NewPythonProject(sc, env)
	case "js", "ts":
		frameworkService = NewNpmProject(sc, env)
	default:
		return nil, fmt.Errorf("unsupported language '%s' for service '%s'", sc.Language, sc.Name)
	}

	// For containerized applications we use a nested framework service
	if sc.Host == string(ContainerAppTarget) {
		sourceFramework := frameworkService
		frameworkService = NewDockerProject(sc, env, tools.NewDocker(), sourceFramework)
	}

	return &frameworkService, nil
}
