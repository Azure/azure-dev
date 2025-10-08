// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package extensions

import "encoding/json"

type ExtensionExample struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Usage       string `json:"usage"`
}

// Provider represents a provider registered by an extension
type Provider struct {
	// Name is the unique identifier for this provider within the extension
	Name string `json:"name"`
	// Type is the type of provider
	Type ProviderType `json:"type"`
	// Description is the description of what this provider does
	Description string `json:"description"`
}

// Registry represents the registry.json structure
type Registry struct {
	// Extensions is a list of extensions in the registry
	Extensions []*ExtensionMetadata `json:"extensions"`
}

type CapabilityType string

const (
	// Custom commands expose new command groups & comments to AZD
	CustomCommandCapability CapabilityType = "custom-commands"
	// Lifecycle events enable extensions to subscribe to AZD project & service lifecycle events
	LifecycleEventsCapability CapabilityType = "lifecycle-events"
	// McpServerCapability enables extensions to start an MCP server
	McpServerCapability CapabilityType = "mcp-server"
	// Service target providers enable extensions to package, publish, and deploy to custom service targets
	ServiceTargetProviderCapability CapabilityType = "service-target-provider"
	// Framework service providers enable extensions to provide custom language frameworks and build systems
	FrameworkServiceProviderCapability CapabilityType = "framework-service-provider"
)

type ProviderType string

const (
	// Service target provider type for custom deployment targets
	ServiceTargetProviderType ProviderType = "service-target"
)

// Extension represents an extension in the registry
type ExtensionMetadata struct {
	// Id is a unique identifier for the extension
	Id string `json:"id"`
	// Namespace is used to expose extension commands within a named group
	Namespace string `json:"namespace,omitempty"`
	// DisplayName is the name of the extension
	DisplayName string `json:"displayName"`
	// Description is a brief description of the extension
	Description string `json:"description"`
	// Versions is a list of versions of the extension that are released over time.
	Versions []ExtensionVersion `json:"versions"`
	// Source is used to store the extension source from where the extension is fetched
	Source string `json:"source,omitempty"`
	// Tags is a list of tags that can be used to filter extensions
	Tags []string `json:"tags,omitempty"`
	// Platforms is a map of platform specific metadata required for extensions
	Platforms map[string]map[string]any `json:"platforms,omitempty"`
}

// ExtensionDependency represents a dependency of an extension
type ExtensionDependency struct {
	// Id is the unique identifier of the dependent extension
	Id string `json:"id"`
	// Version is the version of the dependent extension and supports semantic versioning expressions.
	Version string `json:"version,omitempty"`
}

// McpConfig represents the MCP server configuration for an extension
type McpConfig struct {
	// Server contains configuration for starting the extension's MCP server
	Server McpServerConfig `json:"server"`
}

// McpServerConfig represents the configuration for starting an extension's MCP server
type McpServerConfig struct {
	// Args are the command-line arguments to pass when starting the MCP server
	Args []string `json:"args"`
	// Env are additional environment variables to set when starting the MCP server
	Env []string `json:"env,omitempty"`
}

// ExtensionVersion represents a version of an extension
type ExtensionVersion struct {
	// Version is the version of the extension
	Version string `json:"version"`
	// Capabilities is a list of capabilities that the extension provides
	Capabilities []CapabilityType `json:"capabilities,omitempty"`
	// Providers is a list of providers that this extension version registers
	Providers []Provider `json:"providers,omitempty"`
	// Usage is show how to use the extension
	Usage string `json:"usage"`
	// Examples is a list of examples for the extension
	Examples []ExtensionExample `json:"examples"`
	// Artifacts is a map of artifacts for the extension key on platform (os & architecture)
	Artifacts map[string]ExtensionArtifact `json:"artifacts,omitempty"`
	// Dependencies is a list of dependencies for the extension
	// An extension with dependencies and no artifacts is considered an extension pack.
	// The dependencies are resolved and installed when the extension pack is installed.
	Dependencies []ExtensionDependency `json:"dependencies,omitempty"`
	// Entry point is the entry point for the extension
	// This will typically be the name of the executable or script to run
	EntryPoint string `json:"entryPoint,omitempty"`
	// McpConfig is the MCP server configuration for this extension version
	McpConfig *McpConfig `json:"mcp,omitempty"`
}

// ExtensionArtifact represents the artifact information of an extension
// An artifact can be a URL to a single binary file or a zip archive.
type ExtensionArtifact struct {
	// URL is the location of the artifact
	URL string `json:"url"`
	// Checksum is the checksum of the artifact
	Checksum ExtensionChecksum `json:"checksum"`
	// AdditionalMetadata is a map of additional metadata for the artifact
	AdditionalMetadata map[string]any `json:"-"`
}

// ExtensionChecksum represents the checksum of an extension artifact used to validate the integrity of the artifact.
type ExtensionChecksum struct {
	// Algorithm is the algorithm used to calculate the checksum
	// Examples: sha256, sha512
	Algorithm string `json:"algorithm"`
	// Value is the checksum value to match during the integrity check.
	Value string `json:"value"`
}

func (c ExtensionArtifact) MarshalJSON() ([]byte, error) {
	type Alias ExtensionArtifact

	baseMap := map[string]any{}
	data, err := json.Marshal(Alias(c))
	if err != nil {
		return nil, err
	}

	if err := json.Unmarshal(data, &baseMap); err != nil {
		return nil, err
	}

	for k, v := range c.AdditionalMetadata {
		baseMap[k] = v
	}

	return json.Marshal(baseMap)
}

func (c *ExtensionArtifact) UnmarshalJSON(data []byte) error {
	// Create an alias type to avoid recursion
	type Alias ExtensionArtifact

	// Deserialize the known fields into the alias
	alias := Alias{}
	if err := json.Unmarshal(data, &alias); err != nil {
		return err
	}

	// Copy the fields from the alias back into the struct
	*c = ExtensionArtifact(alias)

	// Deserialize the remaining fields into a map
	temp := make(map[string]interface{})
	if err := json.Unmarshal(data, &temp); err != nil {
		return err
	}

	// Remove known fields from the temp map
	delete(temp, "url")
	delete(temp, "checksum")

	// Convert the remaining fields to Extras
	c.AdditionalMetadata = map[string]any{}
	for k, v := range temp {
		if strValue, ok := v.(string); ok {
			c.AdditionalMetadata[k] = strValue
		}
	}

	return nil
}
