package project

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/azureutil"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/azcli"
	"github.com/drone/envsubst"
	"gopkg.in/yaml.v3"
)

// ProjectConfig is the top level object serialized into an azure.yaml file.
// When changing project structure, make sure to update the JSON schema file for azure.yaml (<workspace root>/schemas/vN.M/azure.yaml.json).
type ProjectConfig struct {
	Name              string                    `yaml:"name"`
	ResourceGroupName string                    `yaml:"resourceGroup,omitempty"`
	Path              string                    `yaml:",omitempty"`
	Metadata          *ProjectMetadata          `yaml:"metadata,omitempty"`
	Services          map[string]*ServiceConfig `yaml:",omitempty"`
	Infra             provisioning.Options      `yaml:"infra"`

	handlers map[Event][]ProjectLifecycleEventHandlerFn
}

// Project lifecycle events
type Event string

const (
	// Raised before project is initialized
	Initializing Event = "initializing"
	// Raised after project is initialized
	Initialized Event = "initialized"
	// Raised before project is provisioned
	Provisioning Event = "provisioning"
	// Raised after project is provisioned
	Provisioned Event = "provisioned"
	// Raised before project is deployed
	Deploying Event = "deploying"
	// Raised after project is deployed
	Deployed Event = "deployed"
	// Raised before project is destroyed
	Destroying Event = "destroying"
	// Raised after project is destroyed
	Destroyed Event = "destroyed"
)

// Project lifecycle event arguments
type ProjectLifecycleEventArgs struct {
	Project *ProjectConfig
	Args    map[string]any
}

// Function definition for project events
type ProjectLifecycleEventHandlerFn func(ctx context.Context, args ProjectLifecycleEventArgs) error

type ProjectMetadata struct {
	// Template is a slug that identifies the template and a version. This attribute should be
	// in every template that we ship.
	// ex: todo-python-mongo@version
	Template string
}

// HasService checks if the project contains a service with a given name.
func (p *ProjectConfig) HasService(name string) bool {
	for key, svc := range p.Services {
		if key == name && svc != nil {
			return true
		}
	}

	return false
}

// GetProject constructs a Project from the project configuration
// This also performs project validation
func (pc *ProjectConfig) GetProject(ctx context.Context, env *environment.Environment) (*Project, error) {
	serviceMap := map[string]*Service{}

	project := Project{
		Name:     pc.Name,
		Metadata: pc.Metadata,
		Config:   pc,
		Services: make([]*Service, 0),
	}

	// This sets the current template within the go context
	// The context is then used when the AzCli is instantiated to set the correct user agent
	if project.Metadata != nil && strings.TrimSpace(project.Metadata.Template) != "" {
		ctx = azcli.WithTemplateName(ctx, project.Metadata.Template)
	}

	if pc.ResourceGroupName == "" {
		// We won't have a ResourceGroupName yet if it hasn't been set in either azure.yaml or AZURE_RESOURCE_GROUP env var
		// Let's try to find the right resource group for this environment
		resourceGroupName, err := azureutil.FindResourceGroupForEnvironment(ctx, env)
		if err != nil {
			return nil, err
		}
		pc.ResourceGroupName = resourceGroupName
	}

	for key, serviceConfig := range pc.Services {
		// If the 'resourceName' was not overridden in the project yaml
		// Retrieve the resource name from the provisioned resources if available
		if strings.TrimSpace(serviceConfig.ResourceName) == "" {
			resolvedResourceName, err := GetServiceResourceName(ctx, pc.ResourceGroupName, serviceConfig.Name, env)
			if err != nil {
				return nil, fmt.Errorf("getting resource name: %w", err)
			}

			serviceConfig.ResourceName = resolvedResourceName
		}

		deploymentScope := environment.NewDeploymentScope(env.GetSubscriptionId(), pc.ResourceGroupName, serviceConfig.ResourceName)
		service, err := serviceConfig.GetService(ctx, &project, env, deploymentScope)

		if err != nil {
			return nil, fmt.Errorf("creating service %s: %w", key, err)
		}

		serviceMap[key] = service
	}

	// Sort services by friendly name an then collect them into a list. This provides a stable ordering of services.
	serviceKeys := make([]string, 0, len(serviceMap))
	for k := range serviceMap {
		serviceKeys = append(serviceKeys, k)
	}
	sort.Strings(serviceKeys)

	for _, key := range serviceKeys {
		project.Services = append(project.Services, serviceMap[key])
	}

	return &project, nil
}

// Adds an event handler for the specified event name
func (pc *ProjectConfig) AddHandler(name Event, handler ProjectLifecycleEventHandlerFn) error {
	newHandler := fmt.Sprintf("%v", handler)
	events := pc.handlers[name]

	for _, ref := range events {
		existingHandler := fmt.Sprintf("%v", ref)

		if newHandler == existingHandler {
			return fmt.Errorf("event handler has already been registered for %s event", name)
		}
	}

	events = append(events, handler)
	pc.handlers[name] = events

	return nil
}

// Removes the event handler for the specified event name
func (pc *ProjectConfig) RemoveHandler(name Event, handler ProjectLifecycleEventHandlerFn) error {
	newHandler := fmt.Sprintf("%v", handler)
	events := pc.handlers[name]
	for i, ref := range events {
		existingHandler := fmt.Sprintf("%v", ref)

		if newHandler == existingHandler {
			pc.handlers[name] = append(events[:i], events[i+1:]...)
			return nil
		}
	}

	return fmt.Errorf("specified handler was not found in %s event registrations", name)
}

// Raises the specified event and calls any registered event handlers
func (pc *ProjectConfig) RaiseEvent(ctx context.Context, name Event, args map[string]any) error {
	handlerErrors := []error{}

	if args == nil {
		args = make(map[string]any)
	}

	eventArgs := ProjectLifecycleEventArgs{
		Args:    args,
		Project: pc,
	}

	handlers := pc.handlers[name]

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

// ParseProjectConfig will parse a project from a yaml string and return the project configuration
func ParseProjectConfig(yamlContent string, env *environment.Environment) (*ProjectConfig, error) {
	log.Printf("Parsing file contents, %s\n", yamlContent)
	rawFile, err := envsubst.Parse(yamlContent)
	if err != nil {
		return nil, fmt.Errorf("parsing environment references in project file: %w", err)
	}

	file, err := rawFile.Execute(func(name string) string {
		if val, has := env.Values[name]; has {
			return val
		}
		return os.Getenv(name)
	})

	if err != nil {
		return nil, fmt.Errorf("replacing environment references: %w", err)
	}

	var projectFile ProjectConfig

	if err = yaml.Unmarshal([]byte(file), &projectFile); err != nil {
		return nil, fmt.Errorf("unable to parse azure.yaml file. Please check the format of the file, and also verify you have the latest version of the CLI: %w", err)
	}

	projectFile.handlers = make(map[Event][]ProjectLifecycleEventHandlerFn)

	// If ResourceGroupName not set in azure.yaml, then look for it in the AZURE_RESOURCE_GROUP env var
	if strings.TrimSpace(projectFile.ResourceGroupName) == "" {
		projectFile.ResourceGroupName = environment.GetResourceGroupNameFromEnvVar(env)
	}

	for key, svc := range projectFile.Services {
		svc.handlers = make(map[Event][]ServiceLifecycleEventHandlerFn)
		svc.Name = key
		svc.Project = &projectFile

		// By convention, the name of the infrastructure module to use when doing an IaC based deployment is the friendly
		// name of the service. This may be overridden by the `module` property of `azure.yaml`
		if svc.Module == "" {
			svc.Module = key
		}

		if svc.Language == "" || svc.Language == "csharp" || svc.Language == "fsharp" {
			svc.Language = "dotnet"
		}
	}

	return &projectFile, nil
}

func (p *ProjectConfig) Initialize(ctx context.Context, env *environment.Environment) error {
	var allTools []tools.ExternalTool
	for _, svc := range p.Services {
		frameworkService, err := svc.GetFrameworkService(ctx, env)
		if err != nil {
			return fmt.Errorf("getting framework services: %w", err)
		}
		if err := (*frameworkService).Initialize(ctx); err != nil {
			return err
		}

		requiredTools := (*frameworkService).RequiredExternalTools()
		allTools = append(allTools, requiredTools...)
	}

	if err := tools.EnsureInstalled(ctx, tools.Unique(allTools)...); err != nil {
		return err
	}

	return nil
}

// LoadProjectConfig loads the azure.yaml configuring into an viewable structure
// This does not evaluate any tooling
func LoadProjectConfig(projectPath string, env *environment.Environment) (*ProjectConfig, error) {
	log.Printf("Reading project from file '%s'\n", projectPath)
	bytes, err := os.ReadFile(projectPath)
	if err != nil {
		return nil, fmt.Errorf("reading project file: %w", err)
	}

	yaml := string(bytes)

	projectConfig, err := ParseProjectConfig(yaml, env)
	if err != nil {
		return nil, fmt.Errorf("parsing project file: %w", err)
	}

	projectConfig.Path = filepath.Dir(projectPath)
	return projectConfig, nil
}
