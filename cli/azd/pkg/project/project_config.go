package project

import (
	"context"

	"github.com/azure/azure-dev/cli/azd/pkg/ext"
	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
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
