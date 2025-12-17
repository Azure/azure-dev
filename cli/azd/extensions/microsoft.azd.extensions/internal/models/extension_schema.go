// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package models

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/azure/azure-dev/cli/azd/extensions/microsoft.azd.extensions/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/extensions"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"go.yaml.in/yaml/v3"
)

type ExtensionSchema struct {
	Id           string                           `yaml:"id"           json:"id"`
	Namespace    string                           `yaml:"namespace"    json:"namespace,omitempty"`
	Language     string                           `yaml:"language"     json:"language,omitempty"`
	EntryPoint   string                           `yaml:"entryPoint"   json:"entryPoint,omitempty"`
	Version      string                           `yaml:"version"      json:"version"`
	Capabilities []extensions.CapabilityType      `yaml:"capabilities" json:"capabilities"`
	Providers    []extensions.Provider            `yaml:"providers"    json:"providers,omitempty"`
	DisplayName  string                           `yaml:"displayName"  json:"displayName"`
	Description  string                           `yaml:"description"  json:"description"`
	Usage        string                           `yaml:"usage"        json:"usage"`
	Examples     []extensions.ExtensionExample    `yaml:"examples"     json:"examples"`
	Tags         []string                         `yaml:"tags"         json:"tags,omitempty"`
	Dependencies []extensions.ExtensionDependency `yaml:"dependencies" json:"dependencies,omitempty"`
	Platforms    map[string]map[string]any        `yaml:"platforms"    json:"platforms,omitempty"`
	Path         string                           `yaml:"-"            json:"-"`
}

type schemaAlias ExtensionSchema

func (e ExtensionSchema) MarshalYAML() (interface{}, error) {
	// Create a new map to build our output
	base := make(map[string]interface{})

	// Add required fields
	base["id"] = e.Id
	base["version"] = e.Version
	base["displayName"] = e.DisplayName
	base["description"] = e.Description
	base["usage"] = e.Usage

	// Add optional fields only if not empty
	if e.Namespace != "" {
		base["namespace"] = e.Namespace
	}
	if e.Language != "" {
		base["language"] = e.Language
	}
	if e.EntryPoint != "" {
		base["entryPoint"] = e.EntryPoint
	}
	if len(e.Capabilities) > 0 {
		base["capabilities"] = e.Capabilities
	}
	if len(e.Examples) > 0 {
		base["examples"] = e.Examples
	}
	if len(e.Tags) > 0 {
		base["tags"] = e.Tags
	}
	if len(e.Dependencies) > 0 {
		base["dependencies"] = e.Dependencies
	}
	if len(e.Providers) > 0 {
		base["providers"] = e.Providers
	}
	if len(e.Platforms) > 0 {
		base["platforms"] = e.Platforms
	}

	return base, nil
}

func (e *ExtensionSchema) UnmarshalYAML(value *yaml.Node) error {
	// Create a temporary map to hold all YAML fields
	var fields map[string]interface{}
	if err := value.Decode(&fields); err != nil {
		return err
	}

	// Create an alias to avoid recursion when unmarshaling known fields
	// and decode into it
	var alias schemaAlias
	if err := value.Decode(&alias); err != nil {
		return err
	}

	// Copy known fields from the alias to the original struct
	*e = ExtensionSchema(alias)

	return nil
}

// SafeDashId replaces all '.' in the extension ID with '-'.
// This is useful for creating a safe ID for use in URLs or other contexts
func (e *ExtensionSchema) SafeDashId() string {
	return strings.ReplaceAll(e.Id, ".", "-")
}

func LoadExtension(extensionPath string) (*ExtensionSchema, error) {
	// Load metadata
	metadataPath := filepath.Join(extensionPath, "extension.yaml")
	metadataBytes, err := os.ReadFile(metadataPath)
	if err != nil {
		if os.IsNotExist(err) {
			extensionYaml := output.WithHighLightFormat("extension.yaml")

			return nil, internal.NewUserFriendlyErrorf(
				"Extension manifest file not found",
				`Ensure that the %s file exists in the current directory.
Alternatively, you can specify the path to the %s file using the --cwd flag.

Example: %s`,
				extensionYaml,
				extensionYaml,
				output.WithHighLightFormat("azd x <command> --cwd <path-to-extension>"),
			)
		}
		return nil, fmt.Errorf("failed to read metadata: %w", err)
	}

	var extensionMetadata ExtensionSchema
	if err := yaml.Unmarshal(metadataBytes, &extensionMetadata); err != nil {
		return nil, fmt.Errorf("failed to parse metadata: %w", err)
	}

	if extensionMetadata.Id == "" {
		return nil, fmt.Errorf("id is required in the metadata")
	}

	if extensionMetadata.Version == "" {
		return nil, fmt.Errorf("version is required in the metadata")
	}

	absExtensionPath, err := filepath.Abs(extensionPath)
	if err != nil {
		return nil, fmt.Errorf("failed to get absolute path for extension directory: %w", err)
	}

	extensionMetadata.Path = absExtensionPath

	return &extensionMetadata, nil
}

func LoadRegistry(registryPath string) (*extensions.Registry, error) {
	registryBytes, err := os.ReadFile(registryPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read registry file: %w", err)
	}

	var registry extensions.Registry
	if err := json.Unmarshal(registryBytes, &registry); err != nil {
		return nil, fmt.Errorf("failed to parse registry file: %w", err)
	}

	return &registry, nil
}
