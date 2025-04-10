package models

import (
	"github.com/azure/azure-dev/cli/azd/pkg/extensions"
)

type ExtensionSchema struct {
	Id           string                           `yaml:"id"           json:"id"`
	Namespace    string                           `yaml:"namespace"    json:"namespace,omitempty"`
	EntryPoint   string                           `yaml:"entryPoint"   json:"entryPoint,omitempty"`
	Version      string                           `yaml:"version"      json:"version"`
	Capabilities []extensions.CapabilityType      `yaml:"capabilities" json:"capabilities"`
	DisplayName  string                           `yaml:"displayName"  json:"displayName"`
	Description  string                           `yaml:"description"  json:"description"`
	Usage        string                           `yaml:"usage"        json:"usage"`
	Examples     []extensions.ExtensionExample    `yaml:"examples"     json:"examples"`
	Tags         []string                         `yaml:"tags"         json:"tags,omitempty"`
	Dependencies []extensions.ExtensionDependency `yaml:"dependencies" json:"dependencies,omitempty"`
	Platforms    map[string]map[string]any        `yaml:"platforms"    json:"platforms,omitempty"`
}
