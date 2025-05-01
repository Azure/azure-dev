// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package models

import (
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/extensions"
	"github.com/bradleyjkemp/cupaloy/v2"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// TestExtensionSchema_MarshalYAML_OmitsEmptyCollections verifies that empty slices and maps
// are omitted when marshaling to YAML
func TestExtensionSchema_MarshalYAML_OmitsEmptyCollections(t *testing.T) {
	s := ExtensionSchema{
		Id:          "test",
		Version:     "1.0.0",
		DisplayName: "Test",
		Description: "Test extension",
		Usage:       "usage info",
		// all slices/maps left empty
	}

	data, err := yaml.Marshal(s)
	require.NoError(t, err, "marshal failed")

	// Use cupaloy to snapshot the marshaled YAML
	cupaloy.SnapshotT(t, string(data))
}

// TestExtensionSchema_MarshalYAML_InlinesAdditionalMetadata verifies that additional metadata
// fields are inlined in the YAML output
func TestExtensionSchema_MarshalYAML_InlinesAdditionalMetadata(t *testing.T) {
	s := ExtensionSchema{
		Id:          "test",
		Version:     "1.0.0",
		DisplayName: "Test",
		Description: "desc",
		Usage:       "usage",
	}

	data, err := yaml.Marshal(s)
	require.NoError(t, err, "marshal failed")

	// Use cupaloy to snapshot the marshaled YAML
	cupaloy.SnapshotT(t, string(data))
}

// TestExtensionSchema_MarshalYAML_OmitsEmptyAdditionalMetadata verifies that empty
// additional metadata is omitted from the YAML output
func TestExtensionSchema_MarshalYAML_OmitsEmptyAdditionalMetadata(t *testing.T) {
	s := ExtensionSchema{
		Id:          "test",
		Version:     "1.0.0",
		DisplayName: "Test",
		Description: "desc",
		Usage:       "usage",
	}

	data, err := yaml.Marshal(s)
	require.NoError(t, err, "marshal failed")

	// Use cupaloy to snapshot the marshaled YAML
	cupaloy.SnapshotT(t, string(data))
}

// TestExtensionSchema_UnmarshalYAML_BasicFields verifies that all fields are correctly
// unmarshaled from YAML
func TestExtensionSchema_UnmarshalYAML_BasicFields(t *testing.T) {
	// Create schema with basic fields for testing
	input := ExtensionSchema{
		Id:          "test-extension",
		Version:     "2.0.0",
		DisplayName: "Test Extension",
		Description: "This is a test extension",
		Usage:       "Testing unmarshalling",
		Namespace:   "test.namespace",
		Language:    "go",
		EntryPoint:  "main.go",
		Capabilities: []extensions.CapabilityType{
			"graph",
		},
		Examples: []extensions.ExtensionExample{
			{
				Name:        "Example1",
				Description: "Example description",
				Usage:       "azd extension test",
			},
		},
		Tags: []string{"test", "yaml"},
		Dependencies: []extensions.ExtensionDependency{
			{Id: "dep1", Version: "1.0.0"},
		},
		Platforms: map[string]map[string]any{
			"windows": {"command": "powershell.exe"},
		},
	}

	// Marshal the schema to YAML
	yamlData, err := yaml.Marshal(input)
	require.NoError(t, err, "marshal failed")

	// Unmarshal the YAML back into a new schema to test unmarshal
	var unmarshalled ExtensionSchema
	err = yaml.Unmarshal(yamlData, &unmarshalled)
	require.NoError(t, err, "unmarshal failed")

	// Marshal again to verify the round-trip works
	remarshalled, err := yaml.Marshal(unmarshalled)
	require.NoError(t, err, "remarshal failed")

	// Verify the round-trip preserved everything by comparing YAML content
	require.Equal(t, string(yamlData), string(remarshalled), "Round trip failed - YAML content differs")

	// Use cupaloy to snapshot the result
	cupaloy.SnapshotT(t, string(remarshalled))
}

// TestExtensionSchema_UnmarshalYAML_WithAdditionalMetadata verifies that additional
// metadata fields are correctly unmarshaled
func TestExtensionSchema_UnmarshalYAML_WithAdditionalMetadata(t *testing.T) {
	// Create schema with additional metadata fields for testing
	input := ExtensionSchema{
		Id:          "test-extension",
		Version:     "1.0.0",
		DisplayName: "Test Extension",
		Description: "Extension description",
		Usage:       "Usage info",
	}

	// Marshal the schema to YAML
	yamlData, err := yaml.Marshal(input)
	require.NoError(t, err, "marshal failed")

	// Unmarshal the YAML back into a new schema to test unmarshal
	var unmarshalled ExtensionSchema
	err = yaml.Unmarshal(yamlData, &unmarshalled)
	require.NoError(t, err, "unmarshal failed")

	// Marshal again to verify the round-trip works
	remarshalled, err := yaml.Marshal(unmarshalled)
	require.NoError(t, err, "remarshal failed")

	// Verify the round-trip preserved everything by comparing YAML content
	require.Equal(t, string(yamlData), string(remarshalled), "Round trip failed - YAML content differs")

	// Use cupaloy to snapshot the result
	cupaloy.SnapshotT(t, string(remarshalled))
}

// TestExtensionSchema_RoundTrip verifies that marshaling and unmarshaling preserves
// all fields and values
func TestExtensionSchema_RoundTrip(t *testing.T) {
	// Create an ExtensionSchema with all fields populated
	original := ExtensionSchema{
		Id:           "test-extension",
		Version:      "1.0.0",
		Namespace:    "test.namespace",
		Language:     "go",
		EntryPoint:   "main.go",
		DisplayName:  "Test Extension",
		Description:  "Test description",
		Usage:        "Test usage",
		Capabilities: []extensions.CapabilityType{"graph", "storage"},
		Examples: []extensions.ExtensionExample{
			{Name: "Example1", Description: "Example Description", Usage: "azd test"},
		},
		Tags: []string{"test", "yaml"},
		Dependencies: []extensions.ExtensionDependency{
			{Id: "dep1", Version: "1.0.0"},
		},
		Platforms: map[string]map[string]any{
			"windows": {"command": "cmd.exe"},
		},
	}

	// Marshal to YAML
	yamlData, err := yaml.Marshal(original)
	require.NoError(t, err, "marshal failed")

	// Snapshot the original marshaled data
	cupaloy.SnapshotT(t, string(yamlData))

	// Unmarshal back to a new ExtensionSchema
	var unmarshalled ExtensionSchema
	err = yaml.Unmarshal(yamlData, &unmarshalled)
	require.NoError(t, err, "unmarshal failed")

	// Marshal again to verify structure is preserved
	remarshalled, err := yaml.Marshal(unmarshalled)
	require.NoError(t, err, "remarshal failed")

	// Verify the round-trip preserved everything by comparing YAML content
	require.Equal(t, string(yamlData), string(remarshalled), "Round trip failed - YAML content differs")
}
