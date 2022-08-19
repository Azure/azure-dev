package project

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/commands"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
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
		target = NewContainerAppTarget(sc, env, scope, azCli, docker.NewDocker(docker.DockerArgs{}))
	case string(AzureFunctionTarget):
		target = NewFunctionAppTarget(sc, env, scope, azCli)
	case string(StaticWebAppTarget):
		target = NewStaticWebAppTarget(sc, env, scope, azCli, swa.NewSwaCli())
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
		frameworkService = NewDockerProject(sc, env, docker.NewDocker(docker.DockerArgs{}), sourceFramework)
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
