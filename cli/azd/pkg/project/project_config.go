package project

import (
	"context"

	"github.com/azure/azure-dev/cli/azd/pkg/cloud"
	"github.com/azure/azure-dev/cli/azd/pkg/ext"
	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/azure/azure-dev/cli/azd/pkg/platform"
	"github.com/azure/azure-dev/cli/azd/pkg/state"
	"github.com/azure/azure-dev/cli/azd/pkg/workflow"
)

// ProjectConfig is the top level object serialized into an azure.yaml file.
// When changing project structure, make sure to update the JSON schema file for azure.yaml (<workspace
// root>/schemas/vN.M/azure.yaml.json).
type ProjectConfig struct {
	RequiredVersions  *RequiredVersions          `yaml:"requiredVersions,omitempty"`
	Name              string                     `yaml:"name"`
	ResourceGroupName osutil.ExpandableString    `yaml:"resourceGroup,omitempty"`
	Path              string                     `yaml:"-"`
	Metadata          *ProjectMetadata           `yaml:"metadata,omitempty"`
	Services          map[string]*ServiceConfig  `yaml:"services,omitempty"`
	Infra             provisioning.Options       `yaml:"infra,omitempty"`
	Pipeline          PipelineOptions            `yaml:"pipeline,omitempty"`
	Hooks             map[string]*ext.HookConfig `yaml:"hooks,omitempty"`
	State             *state.Config              `yaml:"state,omitempty"`
	Platform          *platform.Config           `yaml:"platform,omitempty"`
	Workflows         workflow.WorkflowMap       `yaml:"workflows,omitempty"`
	Cloud             *cloud.Config              `yaml:"cloud,omitempty"`

	*ext.EventDispatcher[ProjectLifecycleEventArgs] `yaml:"-"`
}

// RequiredVersions contains information about what versions of tools this project requires.
// If a value is nil, it is treated as if there is no constraint.
type RequiredVersions struct {
	// When non nil, a semver range (in the format expected by semver.ParseRange).
	Azd *string `yaml:"azd,omitempty"`
}

// options supported in azure.yaml
type PipelineOptions struct {
	Provider  string   `yaml:"provider"`
	Variables []string `yaml:"variables"`
	Secrets   []string `yaml:"secrets"`
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
