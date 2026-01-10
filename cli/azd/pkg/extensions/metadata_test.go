// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package extensions

import (
	"encoding/json"
	"testing"

	"github.com/invopop/jsonschema"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExtensionCommandMetadata_Marshaling(t *testing.T) {
	metadata := &ExtensionCommandMetadata{
		SchemaVersion: "1.0",
		ID:            "microsoft.azd.demo",
		Version:       "1.0.0",
		Commands: []Command{
			{
				Name:  []string{"demo", "greet"},
				Short: "Greet the user",
				Long:  "This command greets the user with a friendly message.",
				Usage: "greet [name]",
				Examples: []CommandExample{
					{
						Description: "Greet with default name",
						Command:     "azd x demo greet",
					},
					{
						Description: "Greet with custom name",
						Command:     "azd x demo greet Alice",
					},
				},
				Args: []Argument{
					{
						Name:        "name",
						Description: "The name to greet",
						Required:    false,
					},
				},
				Flags: []Flag{
					{
						Name:        "format",
						Shorthand:   "f",
						Description: "Output format",
						Type:        "string",
						Default:     "text",
						ValidValues: []string{"text", "json"},
					},
					{
						Name:        "verbose",
						Shorthand:   "v",
						Description: "Enable verbose output",
						Type:        "bool",
						Default:     false,
					},
				},
			},
		},
	}

	data, err := json.Marshal(metadata)
	require.NoError(t, err)
	assert.Contains(t, string(data), `"schemaVersion":"1.0"`)
	assert.Contains(t, string(data), `"id":"microsoft.azd.demo"`)
	assert.Contains(t, string(data), `"version":"1.0.0"`)
	assert.Contains(t, string(data), `"name":["demo","greet"]`)
}

func TestExtensionMetadata_UnmarshalJSON(t *testing.T) {
	jsonData := `{
		"schemaVersion": "1.0",
		"id": "microsoft.azd.demo",
		"version": "1.0.0",
		"commands": [
			{
				"name": ["demo", "greet"],
				"short": "Greet the user",
				"long": "This command greets the user with a friendly message.",
				"usage": "greet [name]",
				"examples": [
					{
						"description": "Greet with default name",
						"command": "azd x demo greet"
					}
				],
				"args": [
					{
						"name": "name",
						"description": "The name to greet",
						"required": false
					}
				],
				"flags": [
					{
						"name": "format",
						"shorthand": "f",
						"description": "Output format",
						"type": "string",
						"default": "text",
						"validValues": ["text", "json"]
					}
				]
			}
		]
	}`

	var metadata ExtensionCommandMetadata
	err := json.Unmarshal([]byte(jsonData), &metadata)
	require.NoError(t, err)

	assert.Equal(t, "1.0", metadata.SchemaVersion)
	assert.Equal(t, "microsoft.azd.demo", metadata.ID)
	assert.Equal(t, "1.0.0", metadata.Version)
	assert.Len(t, metadata.Commands, 1)

	cmd := metadata.Commands[0]
	assert.Equal(t, []string{"demo", "greet"}, cmd.Name)
	assert.Equal(t, "Greet the user", cmd.Short)
	assert.Equal(t, "This command greets the user with a friendly message.", cmd.Long)
	assert.Len(t, cmd.Examples, 1)
	assert.Len(t, cmd.Args, 1)
	assert.Len(t, cmd.Flags, 1)

	assert.Equal(t, "name", cmd.Args[0].Name)
	assert.False(t, cmd.Args[0].Required)

	assert.Equal(t, "format", cmd.Flags[0].Name)
	assert.Equal(t, "f", cmd.Flags[0].Shorthand)
	assert.Equal(t, "string", cmd.Flags[0].Type)
	assert.Equal(t, "text", cmd.Flags[0].Default)
	assert.Equal(t, []string{"text", "json"}, cmd.Flags[0].ValidValues)
}

func TestCommand_NestedSubcommands(t *testing.T) {
	metadata := ExtensionCommandMetadata{
		SchemaVersion: "1.0",
		ID:            "microsoft.azd.test",
		Version:       "1.0.0",
		Commands: []Command{
			{
				Name:  []string{"test"},
				Short: "Test commands",
				Subcommands: []Command{
					{
						Name:  []string{"test", "unit"},
						Short: "Run unit tests",
					},
					{
						Name:  []string{"test", "integration"},
						Short: "Run integration tests",
					},
				},
			},
		},
	}

	data, err := json.Marshal(metadata)
	require.NoError(t, err)

	var unmarshaled ExtensionCommandMetadata
	err = json.Unmarshal(data, &unmarshaled)
	require.NoError(t, err)

	assert.Len(t, unmarshaled.Commands, 1)
	assert.Len(t, unmarshaled.Commands[0].Subcommands, 2)
	assert.Equal(t, []string{"test", "unit"}, unmarshaled.Commands[0].Subcommands[0].Name)
	assert.Equal(t, []string{"test", "integration"}, unmarshaled.Commands[0].Subcommands[1].Name)
}

func TestFlag_AllTypes(t *testing.T) {
	flags := []Flag{
		{
			Name:        "string-flag",
			Type:        "string",
			Description: "A string flag",
			Default:     "default",
		},
		{
			Name:        "bool-flag",
			Type:        "bool",
			Description: "A boolean flag",
			Default:     true,
		},
		{
			Name:        "int-flag",
			Type:        "int",
			Description: "An integer flag",
			Default:     42,
		},
		{
			Name:        "string-array-flag",
			Type:        "stringArray",
			Description: "A string array flag",
			Default:     []string{"value1", "value2"},
		},
		{
			Name:        "int-array-flag",
			Type:        "intArray",
			Description: "An integer array flag",
			Default:     []int{1, 2, 3},
		},
	}

	data, err := json.Marshal(flags)
	require.NoError(t, err)

	var unmarshaled []Flag
	err = json.Unmarshal(data, &unmarshaled)
	require.NoError(t, err)

	assert.Len(t, unmarshaled, 5)
	assert.Equal(t, "string", unmarshaled[0].Type)
	assert.Equal(t, "bool", unmarshaled[1].Type)
	assert.Equal(t, "int", unmarshaled[2].Type)
	assert.Equal(t, "stringArray", unmarshaled[3].Type)
	assert.Equal(t, "intArray", unmarshaled[4].Type)
}

func TestCommand_OptionalFields(t *testing.T) {
	cmd := Command{
		Name:       []string{"test"},
		Short:      "Test command",
		Hidden:     true,
		Aliases:    []string{"t", "tst"},
		Deprecated: "Use 'new-test' instead",
	}

	data, err := json.Marshal(cmd)
	require.NoError(t, err)

	var unmarshaled Command
	err = json.Unmarshal(data, &unmarshaled)
	require.NoError(t, err)

	assert.Equal(t, []string{"test"}, unmarshaled.Name)
	assert.True(t, unmarshaled.Hidden)
	assert.Equal(t, []string{"t", "tst"}, unmarshaled.Aliases)
	assert.Equal(t, "Use 'new-test' instead", unmarshaled.Deprecated)
}

func TestArgument_Variadic(t *testing.T) {
	arg := Argument{
		Name:        "files",
		Description: "Files to process",
		Required:    true,
		Variadic:    true,
		ValidValues: []string{".txt", ".md"},
	}

	data, err := json.Marshal(arg)
	require.NoError(t, err)

	var unmarshaled Argument
	err = json.Unmarshal(data, &unmarshaled)
	require.NoError(t, err)

	assert.Equal(t, "files", unmarshaled.Name)
	assert.True(t, unmarshaled.Required)
	assert.True(t, unmarshaled.Variadic)
	assert.Equal(t, []string{".txt", ".md"}, unmarshaled.ValidValues)
}

func TestConfigurationMetadata_Optional(t *testing.T) {
	// Without configuration
	metadata := ExtensionCommandMetadata{
		SchemaVersion: "1.0",
		ID:            "test",
		Version:       "1.0.0",
		Commands:      []Command{},
	}

	data1, err := json.Marshal(metadata)
	require.NoError(t, err)
	assert.NotContains(t, string(data1), "configuration")

	// With configuration
	globalProps := jsonschema.NewProperties()
	globalProps.Set("apiKey", &jsonschema.Schema{
		Type:        "string",
		Description: "API key for the service",
	})

	metadata2 := ExtensionCommandMetadata{
		SchemaVersion: "1.0",
		ID:            "test",
		Version:       "1.0.0",
		Commands:      []Command{},
		Configuration: &ConfigurationMetadata{
			Global: &jsonschema.Schema{
				Type:       "object",
				Properties: globalProps,
			},
		},
	}

	data2, err := json.Marshal(metadata2)
	require.NoError(t, err)
	assert.Contains(t, string(data2), "configuration")
	assert.Contains(t, string(data2), "global")
	assert.Contains(t, string(data2), "apiKey")
}

func TestExtensionMetadata_FutureSchemaVersion(t *testing.T) {
	// Simulate future schema version with unknown fields
	jsonData := `{
		"schemaVersion": "2.0",
		"id": "test",
		"version": "1.0.0",
		"commands": [],
		"newFeature": "some value",
		"anotherNewField": 123
	}`

	var metadata ExtensionCommandMetadata
	err := json.Unmarshal([]byte(jsonData), &metadata)
	require.NoError(t, err)

	// Should parse known fields successfully
	assert.Equal(t, "2.0", metadata.SchemaVersion)
	assert.Equal(t, "test", metadata.ID)
	assert.Equal(t, "1.0.0", metadata.Version)
	assert.Empty(t, metadata.Commands)
}
