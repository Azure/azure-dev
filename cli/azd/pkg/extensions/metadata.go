// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package extensions

import (
	"github.com/invopop/jsonschema"
)

// ExtensionCommandMetadata represents the complete metadata for an extension including commands and configuration
type ExtensionCommandMetadata struct {
	// SchemaVersion is the version of the metadata schema (e.g., "1.0")
	SchemaVersion string `json:"schemaVersion"`
	// ID is the extension identifier matching extension.yaml
	ID string `json:"id"`
	// Commands is the list of root-level commands provided by the extension
	Commands []Command `json:"commands"`
	// Configuration describes extension configuration options (Phase 2)
	Configuration *ConfigurationMetadata `json:"configuration,omitempty"`
}

// Command represents a command or subcommand in the extension's command tree
type Command struct {
	// Name is the command path as an array of strings (e.g., ["demo", "context"])
	Name []string `json:"name"`
	// Short is a brief one-line description of the command
	Short string `json:"short"`
	// Long is an optional detailed multi-line description (markdown supported)
	Long string `json:"long,omitempty"`
	// Usage is an optional usage template string
	Usage string `json:"usage,omitempty"`
	// Examples contains example usages of the command
	Examples []CommandExample `json:"examples,omitempty"`
	// Args defines the positional arguments accepted by the command
	Args []Argument `json:"args,omitempty"`
	// Flags defines the flags/options accepted by the command
	Flags []Flag `json:"flags,omitempty"`
	// Subcommands contains nested subcommands
	Subcommands []Command `json:"subcommands,omitempty"`
	// Hidden indicates if the command should be hidden from help output
	Hidden bool `json:"hidden,omitempty"`
	// Aliases contains alternative names for the command
	Aliases []string `json:"aliases,omitempty"`
	// Deprecated contains a deprecation notice if the command is deprecated
	Deprecated string `json:"deprecated,omitempty"`
}

// CommandExample represents an example usage of a command
type CommandExample struct {
	// Description explains what the example demonstrates
	Description string `json:"description"`
	// Command is the example command string
	Command string `json:"command"`
}

// Argument represents a positional argument for a command
type Argument struct {
	// Name is the argument name
	Name string `json:"name"`
	// Description explains the purpose of the argument
	Description string `json:"description"`
	// Required indicates if the argument is required
	Required bool `json:"required"`
	// Variadic indicates if the argument accepts multiple values
	Variadic bool `json:"variadic,omitempty"`
	// ValidValues contains the allowed values for the argument
	ValidValues []string `json:"validValues,omitempty"`
}

// Flag represents a command-line flag/option
type Flag struct {
	// Name is the flag name without dashes
	Name string `json:"name"`
	// Shorthand is the optional single character shorthand (without dash)
	Shorthand string `json:"shorthand,omitempty"`
	// Description explains the purpose of the flag
	Description string `json:"description"`
	// Type is the data type: "string", "bool", "int", "stringArray", "intArray"
	Type string `json:"type"`
	// Default is the default value when the flag is not provided
	Default interface{} `json:"default,omitempty"`
	// Required indicates if the flag is required
	Required bool `json:"required,omitempty"`
	// ValidValues contains the allowed values for the flag
	ValidValues []string `json:"validValues,omitempty"`
	// Hidden indicates if the flag should be hidden from help output
	Hidden bool `json:"hidden,omitempty"`
	// Deprecated contains a deprecation notice if the flag is deprecated
	Deprecated string `json:"deprecated,omitempty"`
}

// EnvironmentVariable represents an environment variable used or recognized by the extension
type EnvironmentVariable struct {
	// Name is the environment variable name (e.g., "EXTENSION_API_KEY")
	Name string `json:"name"`
	// Description explains when and why to use this environment variable
	Description string `json:"description"`
	// Default is the default value used if the environment variable is not set
	Default string `json:"default,omitempty"`
	// Example provides an example value for documentation purposes
	Example string `json:"example,omitempty"`
}

// ConfigurationMetadata describes extension configuration options (Phase 2).
// Each field contains a JSON Schema (github.com/invopop/jsonschema) that defines
// the structure and validation rules for extension configuration at different scopes.
//
// RECOMMENDED: Extension developers should define configuration using Go types
// with json tags and generate schemas via reflection:
//
//	type CustomGlobalConfig struct {
//	    APIKey  string `json:"apiKey" jsonschema:"required,description=API key,minLength=10"`
//	    Timeout int    `json:"timeout,omitempty" jsonschema:"minimum=1,maximum=300,default=60"`
//	}
//
//	config := &extensions.ConfigurationMetadata{
//	    Global: jsonschema.Reflect(&CustomGlobalConfig{}),
//	}
//
// Advanced: Schemas can also be built programmatically:
//
//	props := jsonschema.NewProperties()
//	props.Set("apiKey", &jsonschema.Schema{
//	    Type: "string",
//	    Description: "API key for authentication",
//	})
//	config := &extensions.ConfigurationMetadata{
//	    Global: &jsonschema.Schema{
//	        Type: "object",
//	        Properties: props,
//	        Required: []string{"apiKey"},
//	    },
//	}
type ConfigurationMetadata struct {
	// Global contains JSON Schema for global-level configuration options
	Global *jsonschema.Schema `json:"global,omitempty"`
	// Project contains JSON Schema for project-level configuration options
	Project *jsonschema.Schema `json:"project,omitempty"`
	// Service contains JSON Schema for service-level configuration options
	Service *jsonschema.Schema `json:"service,omitempty"`
	// EnvironmentVariables describes environment variables used by the extension
	EnvironmentVariables []EnvironmentVariable `json:"environmentVariables,omitempty"`
}
