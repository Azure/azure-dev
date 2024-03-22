package ai

import (
	"fmt"

	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/wbreza/azure-sdk-for-go/sdk/resourcemanager/machinelearning/armmachinelearning/v3"
	"gopkg.in/yaml.v3"
)

type ConnectionConfig struct {
	ComponentConfig `yaml:",inline"`
	Category        armmachinelearning.ConnectionCategory `yaml:"category,omitempty"`
	AuthType        armmachinelearning.ConnectionAuthType `yaml:"authType,omitempty"`
	Target          osutil.ExpandableString               `yaml:"target,omitempty"`
	ApiKey          osutil.ExpandableString               `yaml:"apiKey,omitempty"`
	Metadata        map[string]string                     `yaml:"metadata,omitempty"`
}

type ComponentConfig struct {
	Name      osutil.ExpandableString            `yaml:"name,omitempty"`
	Path      string                             `yaml:"path,omitempty"`
	Workspace osutil.ExpandableString            `yaml:"workspace,omitempty"`
	Overrides map[string]osutil.ExpandableString `yaml:"overrides,omitempty"`
}

type EndpointConfig struct {
	ComponentConfig `yaml:",inline"`
	Environment     osutil.ExpandableString `yaml:"environment,omitempty"`
	Model           osutil.ExpandableString `yaml:"model,omitempty"`
	Deployment      *ComponentConfig        `yaml:"deployment,omitempty"`
}

// ParseConfig parses a config from a generic interface.
func ParseComponentConfig(config any) (*ComponentConfig, error) {
	yamlBytes, err := yaml.Marshal(config)
	if err != nil {
		return nil, fmt.Errorf("failed marshalling config: %w", err)
	}

	var parsedConfig ComponentConfig
	if err := yaml.Unmarshal(yamlBytes, &parsedConfig); err != nil {
		return nil, fmt.Errorf("failed unmarshalling config: %w", err)
	}

	return &parsedConfig, nil
}

func ParseEndpointConfig(config any) (*EndpointConfig, error) {
	yamlBytes, err := yaml.Marshal(config)
	if err != nil {
		return nil, fmt.Errorf("failed marshalling config: %w", err)
	}

	var parsedConfig EndpointConfig
	if err := yaml.Unmarshal(yamlBytes, &parsedConfig); err != nil {
		return nil, fmt.Errorf("failed unmarshalling config: %w", err)
	}

	return &parsedConfig, nil
}
