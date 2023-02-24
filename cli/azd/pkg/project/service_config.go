package project

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/account"
	"github.com/azure/azure-dev/cli/azd/pkg/azureutil"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/ext"
	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/azcli"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/docker"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/swa"
)

type ServiceConfig struct {
	// Reference to the parent project configuration
	Project *ProjectConfig `yaml:"projectConfig,omitempty"`
	// The friendly name/key of the project from the azure.yaml file
	Name string `yaml:",omitempty"`
	// The name used to override the default azure resource name
	ResourceName ExpandableString `yaml:"resourceName,omitempty"`
	// The relative path to the project folder from the project root
	RelativePath string `yaml:"project"`
	// The azure hosting model to use, ex) appservice, function, containerapp
	Host string `yaml:"host"`
	// The programming language of the project
	Language string `yaml:"language"`
	// The output path for build artifacts
	OutputPath string `yaml:"dist,omitempty"`
	// The infrastructure module path relative to the root infra folder to use for this project
	Module string `yaml:"module,omitempty"`
	// The optional docker options
	Docker DockerProjectOptions `yaml:"docker,omitempty"`
	// The infrastructure provisioning configuration
	Infra provisioning.Options `yaml:"infra,omitempty"`
	// Hook configuration for service
	Hooks map[string]*ext.HookConfig `yaml:"hooks,omitempty"`

	*ext.EventDispatcher[ServiceLifecycleEventArgs] `yaml:",omitempty"`
}

type ServiceLifecycleEventArgs struct {
	Project *ProjectConfig
	Service *ServiceConfig
	Args    map[string]any
}

// Path returns the fully qualified path to the project
func (sc *ServiceConfig) Path() string {
	return filepath.Join(sc.Project.Path, sc.RelativePath)
}

// GetService constructs a parsed Service object from the Service configuration
func (sc *ServiceConfig) GetService(
	ctx context.Context,
	project *Project,
	env *environment.Environment,
	azCli azcli.AzCli,
	accountManager account.Manager,
	commandRunner exec.CommandRunner,
	console input.Console,
) (*Service, error) {
	framework, err := sc.GetFrameworkService(ctx, env, commandRunner)
	if err != nil {
		return nil, fmt.Errorf("creating framework service: %w", err)
	}

	azureResource, err := sc.resolveServiceResource(ctx, project.ResourceGroupName, env, azCli, "provision")
	if err != nil {
		return nil, err
	}

	targetResource := environment.NewTargetResource(
		env.GetSubscriptionId(),
		project.ResourceGroupName,
		azureResource.Name,
		azureResource.Type,
	)

	serviceTarget, err := sc.GetServiceTarget(ctx, env, targetResource, azCli, commandRunner, console, accountManager)
	if err != nil {
		return nil, fmt.Errorf("creating service target: %w", err)
	}

	return &Service{
		Project:        project,
		Config:         sc,
		Environment:    env,
		Framework:      framework,
		Target:         serviceTarget,
		TargetResource: targetResource,
	}, nil
}

// GetServiceTarget constructs a ServiceTarget from the underlying service configuration
func (sc *ServiceConfig) GetServiceTarget(
	ctx context.Context,
	env *environment.Environment,
	resource *environment.TargetResource,
	azCli azcli.AzCli,
	commandRunner exec.CommandRunner,
	console input.Console,
	accountManager account.Manager,
) (ServiceTarget, error) {
	var target ServiceTarget
	var err error

	switch sc.Host {
	case "", string(AppServiceTarget):
		target, err = NewAppServiceTarget(sc, env, resource, azCli)
	case string(ContainerAppTarget):
		target, err = NewContainerAppTarget(
			sc, env, resource, azCli, docker.NewDocker(commandRunner), console, commandRunner, accountManager,
		)
	case string(AzureFunctionTarget):
		target, err = NewFunctionAppTarget(sc, env, resource, azCli)
	case string(StaticWebAppTarget):
		target, err = NewStaticWebAppTarget(sc, env, resource, azCli, swa.NewSwaCli(commandRunner))
	default:
		return nil, fmt.Errorf("unsupported host '%s' for service '%s'", sc.Host, sc.Name)
	}

	if err != nil {
		return nil, fmt.Errorf("failed validation for host '%s': %w", sc.Host, err)
	}

	return target, nil
}

// GetFrameworkService constructs a framework service from the underlying service configuration
func (sc *ServiceConfig) GetFrameworkService(
	ctx context.Context, env *environment.Environment, commandRunner exec.CommandRunner) (FrameworkService, error) {
	var frameworkService FrameworkService

	switch sc.Language {
	case "", "dotnet", "csharp", "fsharp":
		frameworkService = NewDotNetProject(commandRunner, sc, env)
	case "py", "python":
		frameworkService = NewPythonProject(commandRunner, sc, env)
	case "js", "ts":
		frameworkService = NewNpmProject(commandRunner, sc, env)
	case "java":
		frameworkService = NewMavenProject(commandRunner, sc, env)
	default:
		return nil, fmt.Errorf("unsupported language '%s' for service '%s'", sc.Language, sc.Name)
	}

	// For containerized applications we use a nested framework service
	if sc.Host == string(ContainerAppTarget) {
		sourceFramework := frameworkService
		frameworkService = NewDockerProject(sc, env, docker.NewDocker(commandRunner), sourceFramework)
	}

	return frameworkService, nil
}

const (
	defaultServiceTag = "azd-service-name"
)

// resolveServiceResource resolves the service resource during service construction
func (sc *ServiceConfig) resolveServiceResource(
	ctx context.Context,
	resourceGroupName string,
	env *environment.Environment,
	azCli azcli.AzCli,
	rerunCommand string,
) (azcli.AzCliResource, error) {
	azureResource, err := sc.GetServiceResource(ctx, resourceGroupName, env, azCli, rerunCommand)

	// If the service target supports delayed provisioning, the resource isn't expected to be found yet.
	// Return the empty resource
	var resourceNotFoundError *azureutil.ResourceNotFoundError
	if err != nil &&
		errors.As(err, &resourceNotFoundError) &&
		ServiceTargetKind(sc.Host).SupportsDelayedProvisioning() {
		return azureResource, nil
	}

	if err != nil {
		return azcli.AzCliResource{}, err
	}

	return azureResource, nil
}

// GetServiceResources gets the specific azure service resource targeted by the service.
//
// rerunCommand specifies the command that users should rerun in case of misconfiguration.
// This is included in the error message if applicable
func (sc *ServiceConfig) GetServiceResource(
	ctx context.Context,
	resourceGroupName string,
	env *environment.Environment,
	azCli azcli.AzCli,
	rerunCommand string,
) (azcli.AzCliResource, error) {

	expandedResourceName, err := sc.ResourceName.Envsubst(env.Getenv)
	if err != nil {
		return azcli.AzCliResource{}, fmt.Errorf("expanding name: %w", err)
	}

	resources, err := sc.GetServiceResources(ctx, resourceGroupName, env, azCli)
	if err != nil {
		return azcli.AzCliResource{}, fmt.Errorf("getting service resource: %w", err)
	}

	if expandedResourceName == "" { // A tag search was performed
		if len(resources) == 0 {
			err := fmt.Errorf(
				//nolint:lll
				"unable to find a resource tagged with '%s: %s'. Ensure the service resource is correctly tagged in your bicep files, and rerun %s",
				defaultServiceTag,
				sc.Name,
				rerunCommand,
			)
			return azcli.AzCliResource{}, azureutil.ResourceNotFound(err)
		}

		if len(resources) != 1 {
			return azcli.AzCliResource{}, fmt.Errorf(
				//nolint:lll
				"expecting only '1' resource tagged with '%s: %s', but found '%d'. Ensure a unique service resource is correctly tagged in your bicep files, and rerun %s",
				defaultServiceTag,
				sc.Name,
				len(resources),
				rerunCommand,
			)
		}
	} else { // Name based search
		if len(resources) == 0 {
			err := fmt.Errorf(
				"unable to find a resource with name '%s'. Ensure that resourceName in azure.yaml is valid, and rerun %s",
				expandedResourceName,
				rerunCommand)
			return azcli.AzCliResource{}, azureutil.ResourceNotFound(err)
		}

		// This can happen if multiple resources with different resource types are given the same name.
		if len(resources) != 1 {
			return azcli.AzCliResource{},
				fmt.Errorf(
					//nolint:lll
					"expecting only '1' resource named '%s', but found '%d'. Use a unique name for the service resource in the resource group '%s'",
					expandedResourceName,
					len(resources),
					resourceGroupName)
		}
	}

	return resources[0], nil
}

// GetServiceResources finds azure service resources targeted by the service.
//
// If an explicit `ResourceName` is specified in `azure.yaml`, a resource with that name is searched for.
// Otherwise, searches for resources with 'azd-service-name' tag set to the service key.
func (sc *ServiceConfig) GetServiceResources(
	ctx context.Context,
	resourceGroupName string,
	env *environment.Environment,
	azCli azcli.AzCli,
) ([]azcli.AzCliResource, error) {
	filter := fmt.Sprintf("tagName eq '%s' and tagValue eq '%s'", defaultServiceTag, sc.Name)

	subst, err := sc.ResourceName.Envsubst(env.Getenv)
	if err != nil {
		return nil, err
	}

	if strings.TrimSpace(subst) != "" {
		filter = fmt.Sprintf("name eq '%s'", subst)
	}

	return azCli.ListResourceGroupResources(
		ctx,
		env.GetSubscriptionId(),
		resourceGroupName,
		&azcli.ListResourceGroupResourcesOptions{
			Filter: &filter,
		},
	)
}

// Gets a list of required tools for the current service
func (sc *ServiceConfig) GetRequiredTools(
	ctx context.Context,
	env *environment.Environment,
	commandRunner exec.CommandRunner,
) ([]tools.ExternalTool, error) {
	frameworkService, err := sc.GetFrameworkService(ctx, env, commandRunner)
	if err != nil {
		return nil, fmt.Errorf("getting framework services: %w", err)
	}

	return frameworkService.RequiredExternalTools(), nil
}

// Restores dependencies for the current service
func (sc *ServiceConfig) Restore(ctx context.Context, env *environment.Environment, commandRunner exec.CommandRunner) error {
	eventArgs := ServiceLifecycleEventArgs{
		Project: sc.Project,
		Service: sc,
	}

	return sc.Invoke(ctx, ServiceEventRestore, eventArgs, func() error {
		frameworkService, err := sc.GetFrameworkService(ctx, env, commandRunner)
		if err != nil {
			return fmt.Errorf("getting framework services: %w", err)
		}

		if err := frameworkService.InstallDependencies(ctx); err != nil {
			return fmt.Errorf("failed installing dependencies, %w", err)
		}

		return nil
	})
}
