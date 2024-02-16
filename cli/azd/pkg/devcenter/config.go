package devcenter

import (
	"fmt"

	"github.com/azure/azure-dev/cli/azd/pkg/platform"
)

const (
	// Environment variable names
	DevCenterNameEnvName          = "AZURE_DEVCENTER_NAME"
	DevCenterCatalogEnvName       = "AZURE_DEVCENTER_CATALOG"
	DevCenterProjectEnvName       = "AZURE_DEVCENTER_PROJECT"
	DevCenterEnvTypeEnvName       = "AZURE_DEVCENTER_ENVIRONMENT_TYPE"
	DevCenterEnvDefinitionEnvName = "AZURE_DEVCENTER_ENVIRONMENT_DEFINITION"
	DevCenterEnvUser              = "AZURE_DEVCENTER_ENVIRONMENT_USER"

	// Environment configuration paths
	DevCenterNamePath          = ConfigPath + ".name"
	DevCenterCatalogPath       = ConfigPath + ".catalog"
	DevCenterProjectPath       = ConfigPath + ".project"
	DevCenterEnvTypePath       = ConfigPath + ".environmentType"
	DevCenterEnvDefinitionPath = ConfigPath + ".environmentDefinition"
	DevCenterUserPath          = ConfigPath + ".user"

	PlatformKindDevCenter platform.PlatformKind = "devcenter"
)

// Config provides the Azure DevCenter configuration used for devcenter enabled projects
type Config struct {
	Name                  string `json:"name,omitempty"                  yaml:"name,omitempty"`
	Catalog               string `json:"catalog,omitempty"               yaml:"catalog,omitempty"`
	Project               string `json:"project,omitempty"               yaml:"project,omitempty"`
	EnvironmentType       string `json:"environmentType,omitempty"       yaml:"environmentType,omitempty"`
	EnvironmentDefinition string `json:"environmentDefinition,omitempty" yaml:"environmentDefinition,omitempty"`
	User                  string `json:"user,omitempty"                  yaml:"user,omitempty"`
}

// EnsureValid ensures the devcenter configuration is valid to continue with provisioning
func (c *Config) EnsureValid() error {
	if c.Name == "" {
		return fmt.Errorf("devcenter name is required")
	}

	if c.Project == "" {
		return fmt.Errorf("devcenter project is required")
	}

	if c.EnvironmentDefinition == "" {
		return fmt.Errorf("devcenter environment definition is required")
	}

	return nil
}
