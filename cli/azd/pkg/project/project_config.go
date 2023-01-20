package project

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"

	"github.com/azure/azure-dev/cli/azd/internal/telemetry"
	"github.com/azure/azure-dev/cli/azd/internal/telemetry/fields"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/ext"
	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/ioc"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/azcli"
	"gopkg.in/yaml.v3"
)

// ProjectConfig is the top level object serialized into an azure.yaml file.
// When changing project structure, make sure to update the JSON schema file for azure.yaml (<workspace
// root>/schemas/vN.M/azure.yaml.json).
type ProjectConfig struct {
	Name              string                     `yaml:"name"`
	ResourceGroupName ExpandableString           `yaml:"resourceGroup,omitempty"`
	Path              string                     `yaml:",omitempty"`
	Metadata          *ProjectMetadata           `yaml:"metadata,omitempty"`
	Services          map[string]*ServiceConfig  `yaml:",omitempty"`
	Infra             provisioning.Options       `yaml:"infra"`
	Pipeline          PipelineOptions            `yaml:"pipeline"`
	Hooks             map[string]*ext.HookConfig `yaml:"hooks,omitempty"`

	*ext.EventDispatcher[ProjectLifecycleEventArgs] `yaml:",omitempty"`
}

// options supported in azure.yaml
type PipelineOptions struct {
	Provider string `yaml:"provider"`
}

const (
	// Raised before project is initialized
	Initializing ext.Event = "initializing"
	// Raised after project is initialized
	Initialized ext.Event = "initialized"
	// Raised before project is provisioned
	Provisioning ext.Event = "provisioning"
	// Raised after project is provisioned
	Provisioned ext.Event = "provisioned"
	// Raised before project is deployed
	Deploying ext.Event = "deploying"
	// Raised after project is deployed
	Deployed ext.Event = "deployed"
	// Raised before project is destroyed
	Destroying ext.Event = "destroying"
	// Raised after project is destroyed
	Destroyed ext.Event = "destroyed"
	// Raised after environment is updated
	EnvironmentUpdated  ext.Event = "environment updated"
	ServiceEventRestore ext.Event = "restore"
	ServiceEventPackage ext.Event = "package"
	ServiceEventDeploy  ext.Event = "deploy"
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
func (pc *ProjectConfig) GetProject(
	ctx context.Context,
	env *environment.Environment,
	console input.Console,
	azCli azcli.AzCli,
	commandRunner exec.CommandRunner,
) (*Project, error) {
	serviceMap := map[string]*Service{}

	project := Project{
		Name:     pc.Name,
		Metadata: pc.Metadata,
		Config:   pc,
		Services: make([]*Service, 0),
	}

	resourceGroupName, err := GetResourceGroupName(ctx, azCli, pc, env)
	if err != nil {
		return nil, err
	}
	project.ResourceGroupName = resourceGroupName

	for key, serviceConfig := range pc.Services {
		service, err := serviceConfig.GetService(ctx, &project, env, azCli, commandRunner, console)

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

// Saves the current instance back to the azure.yaml file
func (p *ProjectConfig) Save(projectPath string) error {
	projectBytes, err := yaml.Marshal(p)
	if err != nil {
		return fmt.Errorf("marshalling project yaml: %w", err)
	}

	err = os.WriteFile(projectPath, projectBytes, osutil.PermissionFile)
	if err != nil {
		return fmt.Errorf("saving project file: %w", err)
	}

	p.Path = projectPath

	return nil
}

// ParseProjectConfig will parse a project from a yaml string and return the project configuration
func ParseProjectConfig(yamlContent string) (*ProjectConfig, error) {
	var projectFile ProjectConfig

	if err := yaml.Unmarshal([]byte(yamlContent), &projectFile); err != nil {
		return nil, fmt.Errorf(
			"unable to parse azure.yaml file. Please check the format of the file, "+
				"and also verify you have the latest version of the CLI: %w",
			err,
		)
	}

	projectFile.EventDispatcher = ext.NewEventDispatcher[ProjectLifecycleEventArgs]()

	for key, svc := range projectFile.Services {
		svc.Name = key
		svc.Project = &projectFile
		svc.EventDispatcher = ext.NewEventDispatcher[ServiceLifecycleEventArgs]()

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

func (p *ProjectConfig) Initialize(
	ctx context.Context, env *environment.Environment, commandRunner exec.CommandRunner,
) error {
	var allTools []tools.ExternalTool
	for _, svc := range p.Services {
		frameworkService, err := svc.GetFrameworkService(ctx, env, commandRunner)
		if err != nil {
			return fmt.Errorf("getting framework services: %w", err)
		}
		if err := frameworkService.Initialize(ctx); err != nil {
			return err
		}

		requiredTools := frameworkService.RequiredExternalTools()
		allTools = append(allTools, requiredTools...)
	}

	if err := tools.EnsureInstalled(ctx, tools.Unique(allTools)...); err != nil {
		return err
	}

	return nil
}

// LoadProjectConfig loads the azure.yaml configuring into an viewable structure
// This does not evaluate any tooling
func LoadProjectConfig(projectPath string) (*ProjectConfig, error) {
	log.Printf("Reading project from file '%s'\n", projectPath)
	bytes, err := os.ReadFile(projectPath)
	if err != nil {
		return nil, fmt.Errorf("reading project file: %w", err)
	}

	yaml := string(bytes)

	projectConfig, err := ParseProjectConfig(yaml)
	if err != nil {
		return nil, fmt.Errorf("parsing project file: %w", err)
	}

	if projectConfig.Metadata != nil {
		telemetry.SetUsageAttributes(fields.StringHashed(fields.TemplateIdKey, projectConfig.Metadata.Template))
	}

	projectConfig.Path = filepath.Dir(projectPath)
	return projectConfig, nil
}

func GetCurrent() (*ProjectConfig, error) {
	var projectConfig *ProjectConfig
	if err := ioc.Global.Resolve(&projectConfig); err != nil {
		return nil, err
	}

	return projectConfig, nil
}
