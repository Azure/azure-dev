package ai

import (
	"fmt"

	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"gopkg.in/yaml.v3"
)

type ComponentConfig struct {
	Name      osutil.ExpandableString            `yaml:"name,omitempty"`
	Path      string                             `yaml:"path,omitempty"`
	Overrides map[string]osutil.ExpandableString `yaml:"overrides,omitempty"`
}

type EndpointDeploymentConfig struct {
	Workspace   osutil.ExpandableString `yaml:"workspace,omitempty"`
	Environment *ComponentConfig        `yaml:"environment,omitempty"`
	Model       *ComponentConfig        `yaml:"model,omitempty"`
	Flow        *ComponentConfig        `yaml:"flow,omitempty"`
	Deployment  *ComponentConfig        `yaml:"deployment,omitempty"`
}

type Flow struct {
	Name        string            `json:"name"`
	Description string            `json:"description"`
	Type        string            `json:"type"`
	Path        string            `json:"path"`
	DisplayName string            `json:"display_name"`
	Tags        map[string]string `json:"tags"`
}

type Scope struct {
	subscriptionId string
	resourceGroup  string
	workspace      string
}

func NewScope(subscriptionId string, resourceGroup string, workspace string) *Scope {
	return &Scope{
		subscriptionId: subscriptionId,
		resourceGroup:  resourceGroup,
		workspace:      workspace,
	}
}

func (s *Scope) SubscriptionId() string {
	return s.subscriptionId
}

func (s *Scope) ResourceGroup() string {
	return s.resourceGroup
}

func (s *Scope) Workspace() string {
	return s.workspace
}

// ParseConfig parses a config from a generic interface.
func ParseConfig[T comparable](config any) (*T, error) {
	yamlBytes, err := yaml.Marshal(config)
	if err != nil {
		return nil, fmt.Errorf("failed marshalling config: %w", err)
	}

	var parsedConfig T
	if err := yaml.Unmarshal(yamlBytes, &parsedConfig); err != nil {
		return nil, fmt.Errorf("failed unmarshalling config: %w", err)
	}

	return &parsedConfig, nil
}
