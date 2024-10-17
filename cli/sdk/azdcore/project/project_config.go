package project

import (
	"context"

	"github.com/azure/azure-dev/cli/sdk/azdcore/azure"
	"github.com/azure/azure-dev/cli/sdk/azdcore/common"
	"github.com/azure/azure-dev/cli/sdk/azdcore/contracts"
)

// ProjectConfig is the top level object serialized into an azure.yaml file.
// When changing project structure, make sure to update the JSON schema file for azure.yaml (<workspace
// root>/schemas/vN.M/azure.yaml.json).
type ProjectConfig struct {
	RequiredVersions  *RequiredVersions             `yaml:"requiredVersions,omitempty"`
	Name              string                        `yaml:"name"`
	ResourceGroupName common.ExpandableString       `yaml:"resourceGroup,omitempty"`
	Path              string                        `yaml:"-"`
	Metadata          *ProjectMetadata              `yaml:"metadata,omitempty"`
	Services          map[string]*ServiceConfig     `yaml:"services,omitempty"`
	Infra             contracts.ProvisioningOptions `yaml:"infra,omitempty"`
	Pipeline          contracts.PipelineOptions     `yaml:"pipeline,omitempty"`
	Hooks             contracts.HooksConfig         `yaml:"hooks,omitempty"`
	State             *contracts.StateConfig        `yaml:"state,omitempty"`
	Platform          *contracts.PlatformConfig     `yaml:"platform,omitempty"`
	Workflows         contracts.WorkflowMap         `yaml:"workflows,omitempty"`
	Cloud             azure.CloudConfig             `yaml:"cloud,omitempty"`

	*EventDispatcher[ProjectLifecycleEventArgs] `yaml:"-"`
}

// RequiredVersions contains information about what versions of tools this project requires.
// If a value is nil, it is treated as if there is no constraint.
type RequiredVersions struct {
	// When non nil, a semver range (in the format expected by semver.ParseRange).
	Azd *string `yaml:"azd,omitempty"`
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
