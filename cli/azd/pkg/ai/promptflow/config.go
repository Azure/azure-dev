package promptflow

import (
	"fmt"

	"github.com/azure/azure-dev/cli/azd/pkg/ai"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"gopkg.in/yaml.v3"
)

type Flow struct {
	Name        string            `json:"name"`
	Description string            `json:"description"`
	Type        FlowType          `json:"type"`
	Path        string            `json:"path"`
	DisplayName string            `json:"display_name"`
	Tags        map[string]string `json:"tags"`
}

type FlowType string

const (
	FlowTypeChat FlowType = "chat"
)

type Config struct {
	Name         osutil.ExpandableString            `yaml:"name,omitempty"`
	Workspace    osutil.ExpandableString            `yaml:"workspace,omitempty"`
	Connections  []*ai.ConnectionConfig             `yaml:"connections,omitempty"`
	Environments []*ai.ComponentConfig              `yaml:"environments,omitempty"`
	Models       []*ai.ComponentConfig              `yaml:"models,omitempty"`
	Endpoints    []*ai.EndpointConfig               `yaml:"endpoints,omitempty"`
	Overrides    map[string]osutil.ExpandableString `yaml:"overrides,omitempty"`
}

// ParseConfig parses a config from a generic interface.
func ParseConfig(config any) (*Config, error) {
	yamlBytes, err := yaml.Marshal(config)
	if err != nil {
		return nil, fmt.Errorf("failed marshalling config: %w", err)
	}

	var parsedConfig Config
	if err := yaml.Unmarshal(yamlBytes, &parsedConfig); err != nil {
		return nil, fmt.Errorf("failed unmarshalling config: %w", err)
	}

	return &parsedConfig, nil
}
