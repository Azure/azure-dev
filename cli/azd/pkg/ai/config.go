package ai

import (
	"fmt"

	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/braydonk/yaml"
)

// ComponentConfig is a base configuration structure used by multiple AI components
type ComponentConfig struct {
	Name      osutil.ExpandableString            `yaml:"name,omitempty"`
	Path      string                             `yaml:"path,omitempty"`
	Overrides map[string]osutil.ExpandableString `yaml:"overrides,omitempty"`
}

type DeploymentConfig struct {
	ComponentConfig `yaml:",inline"`
	// A map of environment variables to set for the deployment
	Environment map[string]osutil.ExpandableString `yaml:"environment,omitempty"`
}

// EndpointDeploymentConfig is a configuration structure for an ML online endpoint deployment
type EndpointDeploymentConfig struct {
	Workspace   osutil.ExpandableString `yaml:"workspace,omitempty"`
	Environment *ComponentConfig        `yaml:"environment,omitempty"`
	Model       *ComponentConfig        `yaml:"model,omitempty"`
	Flow        *ComponentConfig        `yaml:"flow,omitempty"`
	Deployment  *DeploymentConfig       `yaml:"deployment,omitempty"`
}

// Flow is a configuration to defined a Prompt flow component
type Flow struct {
	Name        string            `json:"name"`
	Description string            `json:"description"`
	Type        string            `json:"type"`
	Path        string            `json:"path"`
	DisplayName string            `json:"display_name"`
	Tags        map[string]string `json:"tags"`
}

// Scope is a context based structure to define the Azure scope of a AI component
type Scope struct {
	subscriptionId string
	resourceGroup  string
	workspace      string
}

// NewScope creates a new Scope instance
func NewScope(subscriptionId string, resourceGroup string, workspace string) *Scope {
	return &Scope{
		subscriptionId: subscriptionId,
		resourceGroup:  resourceGroup,
		workspace:      workspace,
	}
}

// SubscriptionId returns the subscription ID from the scope
func (s *Scope) SubscriptionId() string {
	return s.subscriptionId
}

// ResourceGroup returns the resource group from the scope
func (s *Scope) ResourceGroup() string {
	return s.resourceGroup
}

// Workspace returns the workspace from the scope
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
