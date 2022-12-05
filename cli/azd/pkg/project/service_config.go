package project

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/azcli"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/docker"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/swa"
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
	// The infrastructure module path relative to the root infra folder to use for this project
	Module string `yaml:"module"`
	// The optional docker options
	Docker DockerProjectOptions `yaml:"docker"`
	// The infrastructure provisioning configuration
	Infra provisioning.Options `yaml:"infra"`

	handlers map[Event][]ServiceLifecycleEventHandlerFn
}

type ServiceLifecycleEventArgs struct {
	Project *ProjectConfig
	Service *ServiceConfig
	Args    map[string]any
}

// Function definition for project events
type ServiceLifecycleEventHandlerFn func(ctx context.Context, args ServiceLifecycleEventArgs) error

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
	commandRunner exec.CommandRunner,
	console input.Console,
) (*Service, error) {
	framework, err := sc.GetFrameworkService(ctx, env, commandRunner)
	if err != nil {
		return nil, fmt.Errorf("creating framework service: %w", err)
	}

	azureResource, err := sc.GetServiceResource(ctx, project.ResourceGroupName, env, azCli)
	if err != nil {
		return nil, err
	}

	targetResource := environment.NewTargetResource(
		env.GetSubscriptionId(),
		project.ResourceGroupName,
		azureResource.Name,
		azureResource.Type,
	)

	serviceTarget, err := sc.GetServiceTarget(ctx, env, targetResource, azCli, commandRunner, console)
	if err != nil {
		return nil, fmt.Errorf("creating service target: %w", err)
	}

	return &Service{
		Project:        project,
		Config:         sc,
		Framework:      *framework,
		Target:         *serviceTarget,
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
) (*ServiceTarget, error) {
	var target ServiceTarget
	var err error

	switch sc.Host {
	case "", string(AppServiceTarget):
		target, err = NewAppServiceTarget(sc, env, resource, azCli)
	case string(ContainerAppTarget):
		target, err = NewContainerAppTarget(
			sc, env, resource, azCli, docker.NewDocker(commandRunner), console, commandRunner,
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

	return &target, nil
}

// GetFrameworkService constructs a framework service from the underlying service configuration
func (sc *ServiceConfig) GetFrameworkService(
	ctx context.Context, env *environment.Environment, commandRunner exec.CommandRunner) (*FrameworkService, error) {
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

	return &frameworkService, nil
}

// Adds an event handler for the specified event name
func (sc *ServiceConfig) AddHandler(name Event, handler ServiceLifecycleEventHandlerFn) error {
	newHandler := fmt.Sprintf("%v", handler)
	events := sc.handlers[name]

	for _, ref := range events {
		existingHandler := fmt.Sprintf("%v", ref)

		if newHandler == existingHandler {
			return fmt.Errorf("event handler has already been registered for %s event", name)
		}
	}

	events = append(events, handler)
	sc.handlers[name] = events

	return nil
}

// Removes the event handler for the specified event name
func (sc *ServiceConfig) RemoveHandler(name Event, handler ServiceLifecycleEventHandlerFn) error {
	newHandler := fmt.Sprintf("%v", handler)
	events := sc.handlers[name]
	for i, ref := range events {
		existingHandler := fmt.Sprintf("%v", ref)

		if newHandler == existingHandler {
			sc.handlers[name] = append(events[:i], events[i+1:]...)
			return nil
		}
	}

	return fmt.Errorf("specified handler was not found in %s event registrations", name)
}

// Raises the specified event and calls any registered event handlers
func (sc *ServiceConfig) RaiseEvent(ctx context.Context, name Event, args map[string]any) error {
	handlerErrors := []error{}

	if args == nil {
		args = make(map[string]any)
	}

	eventArgs := ServiceLifecycleEventArgs{
		Project: sc.Project,
		Service: sc,
		Args:    args,
	}

	handlers := sc.handlers[name]

	// TODO: Opportunity to dispatch these event handlers in parallel if needed
	for _, handler := range handlers {
		err := handler(ctx, eventArgs)
		if err != nil {
			handlerErrors = append(handlerErrors, err)
		}
	}

	// Build final error string if their are any failures
	if len(handlerErrors) > 0 {
		lines := make([]string, len(handlerErrors))
		for i, err := range handlerErrors {
			lines[i] = err.Error()
		}

		return errors.New(strings.Join(lines, ","))
	}

	return nil
}

const (
	defaultServiceTag = "azd-service-name"
)

// GetServiceResources gets the specific azure service resource targeted by the service.
func (sc *ServiceConfig) GetServiceResource(
	ctx context.Context,
	resourceGroupName string,
	env *environment.Environment,
	azCli azcli.AzCli,
) (azcli.AzCliResource, error) {
	resources, err := sc.GetServiceResources(ctx, resourceGroupName, env, azCli)
	if err != nil {
		return azcli.AzCliResource{}, fmt.Errorf("getting service resource: %w", err)
	}

	if strings.TrimSpace(sc.ResourceName) == "" { // A tag search was performed
		if len(resources) == 0 {
			return azcli.AzCliResource{}, fmt.Errorf(
				//nolint:lll
				"unable to find a provisioned resource tagged with '%s: %s'. Ensure the service resource is correctly tagged in your bicep files, and rerun provision",
				defaultServiceTag,
				sc.Name,
			)
		}

		if len(resources) != 1 {
			return azcli.AzCliResource{}, fmt.Errorf(
				//nolint:lll
				"expecting only '1' resource tagged with '%s: %s', but found '%d'. Ensure a unique service resource is correctly tagged in your bicep files, and rerun provision",
				defaultServiceTag,
				sc.Name,
				len(resources),
			)
		}
	} else { // Name based search
		if len(resources) == 0 {
			return azcli.AzCliResource{},
				fmt.Errorf(
					"unable to find a provisioned resource with name '%s'. Ensure that a previous provision was successful",
					sc.ResourceName)
		}

		// This can happen if multiple resources with different resource types are given the same name.
		if len(resources) != 1 {
			return azcli.AzCliResource{},
				fmt.Errorf(
					//nolint:lll
					"expecting only '1' resource named '%s', but found '%d'. Use a unique name for the service resource in the resource group '%s'",
					sc.ResourceName,
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

	if strings.TrimSpace(sc.ResourceName) != "" {
		filter = fmt.Sprintf("name eq '%s'", sc.ResourceName)
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
