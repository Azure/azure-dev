package project

import (
	"context"
	"sort"

	"github.com/azure/azure-dev/cli/azd/pkg/ext"
	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
	"github.com/azure/azure-dev/cli/azd/pkg/state"
)

// ProjectConfig is the top level object serialized into an azure.yaml file.
// When changing project structure, make sure to update the JSON schema file for azure.yaml (<workspace
// root>/schemas/vN.M/azure.yaml.json).
type ProjectConfig struct {
	RequiredVersions  *RequiredVersions          `yaml:"requiredVersions,omitempty"`
	Name              string                     `yaml:"name"`
	ResourceGroupName ExpandableString           `yaml:"resourceGroup,omitempty"`
	Path              string                     `yaml:"-"`
	Metadata          *ProjectMetadata           `yaml:"metadata,omitempty"`
	Services          map[string]*ServiceConfig  `yaml:"services,omitempty"`
	Infra             provisioning.Options       `yaml:"infra,omitempty"`
	Pipeline          PipelineOptions            `yaml:"pipeline,omitempty"`
	Hooks             map[string]*ext.HookConfig `yaml:"hooks,omitempty"`
	State             *state.Config              `yaml:"state,omitempty"`
	Platform          *PlatformConfig            `yaml:"platform,omitempty"`

	*ext.EventDispatcher[ProjectLifecycleEventArgs] `yaml:"-"`
}

type PlatformKind string

type PlatformConfig struct {
	Type   PlatformKind   `yaml:"type"`
	Config map[string]any `yaml:"config"`
}

// RequiredVersions contains information about what versions of tools this project requires.
// If a value is nil, it is treated as if there is no constraint.
type RequiredVersions struct {
	// When non nil, a semver range (in the format expected by semver.ParseRange).
	Azd *string `yaml:"azd,omitempty"`
}

// options supported in azure.yaml
type PipelineOptions struct {
	Provider string `yaml:"provider"`
}

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

// Retrieves the list of services in the project, in a stable ordering that is deterministic.
func (p *ProjectConfig) GetServicesStable() []*ServiceConfig {
	// Sort services by friendly name an then collect them into a list. This provides a stable ordering of services.
	serviceKeys := make([]string, 0, len(p.Services))
	for k := range p.Services {
		serviceKeys = append(serviceKeys, k)
	}
	sort.Strings(serviceKeys)

	services := make([]*ServiceConfig, 0, len(p.Services))
	for _, key := range serviceKeys {
		services = append(services, p.Services[key])
	}
	return services
}
