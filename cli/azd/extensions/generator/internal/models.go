// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package internal

import (
	"github.com/azure/azure-dev/cli/azd/pkg/extensions"
)

// ExtensionMetadata represents the structure of extension.yaml
type ExtensionSchema struct {
	Id           string                           `yaml:"id" json:"id"`
	Namespace    string                           `yaml:"namespace" json:"namespace,omitempty"`
	EntryPoint   string                           `yaml:"entryPoint" json:"entryPoint,omitempty"`
	Version      string                           `yaml:"version" json:"version"`
	DisplayName  string                           `yaml:"displayName" json:"displayName"`
	Description  string                           `yaml:"description" json:"description"`
	Usage        string                           `yaml:"usage" json:"usage"`
	Examples     []extensions.ExtensionExample    `yaml:"examples" json:"examples"`
	Tags         []string                         `yaml:"tags" json:"tags,omitempty"`
	Dependencies []extensions.ExtensionDependency `yaml:"dependencies" json:"dependencies,omitempty"`
	Platforms    map[string]map[string]any        `yaml:"platforms" json:"platforms,omitempty"`
}
