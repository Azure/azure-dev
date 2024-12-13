package project

import (
	"context"
	"fmt"

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
	// Metadata that specifies the schema version.
	//
	// This is currently only used during [Save] to write the file schema annotation for intellisense.
	// This should include the "v" prefix used in official version numbers.
	MetaSchemaVersion string `yaml:"-"`

	RequiredVersions  *RequiredVersions          `yaml:"requiredVersions,omitempty"`
	Name              string                     `yaml:"name"`
	ResourceGroupName osutil.ExpandableString    `yaml:"resourceGroup,omitempty"`
	Path              string                     `yaml:"-"`
	Metadata          *ProjectMetadata           `yaml:"metadata,omitempty"`
	Services          map[string]*ServiceConfig  `yaml:"services,omitempty"`
	Infra             provisioning.Options       `yaml:"infra,omitempty"`
	Pipeline          PipelineOptions            `yaml:"pipeline,omitempty"`
	Hooks             HooksConfig                `yaml:"hooks,omitempty"`
	State             *state.Config              `yaml:"state,omitempty"`
	Platform          *platform.Config           `yaml:"platform,omitempty"`
	Workflows         workflow.WorkflowMap       `yaml:"workflows,omitempty"`
	Cloud             *cloud.Config              `yaml:"cloud,omitempty"`
	Resources         map[string]*ResourceConfig `yaml:"resources,omitempty"`

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

// HooksConfig is an alias for map of hook names to slice of hook configurations
// This custom alias type is used to help support YAML unmarshalling of legacy single hook configurations
// and new multiple hook configurations
type HooksConfig map[string][]*ext.HookConfig

// UnmarshalYAML converts the hooks configuration from YAML supporting both legacy single hook configurations
// and new multiple hook configurations
func (ch *HooksConfig) UnmarshalYAML(unmarshal func(interface{}) error) error {
	var legacyConfig map[string]*ext.HookConfig

	// Attempt to unmarshal the legacy single hook configuration
	if err := unmarshal(&legacyConfig); err == nil {
		newConfig := HooksConfig{}

		for key, value := range legacyConfig {
			newConfig[key] = []*ext.HookConfig{value}
		}

		*ch = newConfig
	} else { // Unmarshal the new multiple hook configuration
		var newConfig map[string][]*ext.HookConfig
		if err := unmarshal(&newConfig); err != nil {
			return fmt.Errorf("failed to unmarshal hooks configuration: %w", err)
		}

		*ch = newConfig
	}

	return nil
}

// MarshalYAML marshals the hooks configuration to YAML supporting both legacy single hook configurations
func (ch HooksConfig) MarshalYAML() (interface{}, error) {
	if len(ch) == 0 {
		return nil, nil
	}

	result := map[string]any{}
	for key, hooks := range ch {
		if len(hooks) == 1 {
			result[key] = hooks[0]
		} else {
			result[key] = hooks
		}
	}

	return result, nil
}
