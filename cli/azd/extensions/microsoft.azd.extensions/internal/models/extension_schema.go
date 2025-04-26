// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package models

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/extensions"
	"gopkg.in/yaml.v3"
)

type AdditionalMetadata map[string]string

type ExtensionSchema struct {
	AdditionalMetadata

	Id           string                           `yaml:"id"           json:"id"`
	Namespace    string                           `yaml:"namespace"    json:"namespace,omitempty"`
	Language     string                           `yaml:"language"     json:"language,omitempty"`
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
	Path         string                           `yaml:"-"            json:"-"`
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
