package project

import (
	"context"
	"fmt"
	"sort"

	"github.com/azure/azure-dev/cli/azd/pkg/account"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/ext"
	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/azcli"
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
	ProjectEventDeploy     ext.Event = "deploy"
	ProjectEventProvision  ext.Event = "provision"
	ServiceEventEnvUpdated ext.Event = "environment updated"
	ServiceEventRestore    ext.Event = "restore"
	ServiceEventPackage    ext.Event = "package"
	ServiceEventDeploy     ext.Event = "deploy"
)

var (
	ProjectEvents []ext.Event = []ext.Event{
		ProjectEventProvision,
		ProjectEventDeploy,
	}
	ServiceEvents []ext.Event = []ext.Event{
		ServiceEventEnvUpdated,
		ServiceEventRestore,
		ServiceEventPackage,
		ServiceEventDeploy,
	}
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
	accountManager account.Manager,
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
		service, err := serviceConfig.GetService(ctx, &project, env, azCli, accountManager, commandRunner, console)

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
}

// ParseProjectConfig will parse a project from a yaml string and return the project configuration
func ParseProjectConfig(yamlContent string) (*ProjectConfig, error) {
}

func (p *ProjectConfig) Initialize(
	ctx context.Context, env *environment.Environment, commandRunner exec.CommandRunner,
) error {
}

// LoadProjectConfig loads the azure.yaml configuring into an viewable structure
// This does not evaluate any tooling
func LoadProjectConfig(projectPath string) (*ProjectConfig, error) {
}
