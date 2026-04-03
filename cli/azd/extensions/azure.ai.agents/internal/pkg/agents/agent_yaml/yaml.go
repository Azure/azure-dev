// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package agent_yaml

import (
	"fmt"
	"slices"

	"go.yaml.in/yaml/v3"
)

// AgentKind represents the type of agent
type AgentKind string

const (
	AgentKindPrompt   AgentKind = "prompt"
	AgentKindHosted   AgentKind = "hosted"
	AgentKindWorkflow AgentKind = "workflow"
)

// IsValidAgentKind checks if the provided AgentKind is valid
func IsValidAgentKind(kind AgentKind) bool {
	return slices.Contains(ValidAgentKinds(), kind)
}

// ValidAgentKinds returns a slice of all valid AgentKind values
func ValidAgentKinds() []AgentKind {
	return []AgentKind{
		AgentKindPrompt,
		AgentKindHosted,
		AgentKindWorkflow,
	}
}

type ResourceKind string

const (
	ResourceKindModel   ResourceKind = "model"
	ResourceKindTool    ResourceKind = "tool"
	ResourceKindToolbox ResourceKind = "toolbox"
)

type ToolKind string

const (
	ToolKindFunction        ToolKind = "function"
	ToolKindCustom          ToolKind = "custom"
	ToolKindWebSearch       ToolKind = "webSearch"
	ToolKindBingGrounding   ToolKind = "bingGrounding"
	ToolKindFileSearch      ToolKind = "fileSearch"
	ToolKindMcp             ToolKind = "mcp"
	ToolKindOpenApi         ToolKind = "openApi"
	ToolKindCodeInterpreter ToolKind = "codeInterpreter"
)

type ConnectionKind string

const (
	ConnectionKindReference ConnectionKind = "reference"
	ConnectionKindRemote    ConnectionKind = "remote"
	ConnectionKindApiKey    ConnectionKind = "apiKey"
	ConnectionKindAnonymous ConnectionKind = "anonymous"
)

// AgentDefinition The following is a specification for defining AI agents with structured metadata, inputs, outputs, tools,
// and templates.
// It provides a way to create reusable and composable AI agents that can be executed with specific configurations.
// The specification includes metadata about the agent, model configuration, input parameters, expected outputs,
// available tools, and template configurations for prompt rendering.
type AgentDefinition struct {
	Kind         AgentKind       `json:"kind" yaml:"kind"`
	Name         string          `json:"name" yaml:"name"`
	DisplayName  *string         `json:"displayName,omitempty" yaml:"displayName,omitempty"`
	Description  *string         `json:"description,omitempty" yaml:"description,omitempty"`
	Metadata     *map[string]any `json:"metadata,omitempty" yaml:"metadata,omitempty"`
	InputSchema  *PropertySchema `json:"inputSchema,omitempty" yaml:"inputSchema,omitempty"`
	OutputSchema *PropertySchema `json:"outputSchema,omitempty" yaml:"outputSchema,omitempty"`
}

// PromptAgent Prompt based agent definition. Used to create agents that can be executed directly.
// These agents can leverage tools, input parameters, and templates to generate responses.
// They are designed to be straightforward and easy to use for various applications.
type PromptAgent struct {
	AgentDefinition        `json:",inline" yaml:",inline"`
	Model                  Model     `json:"model" yaml:"model"`
	Tools                  *[]any    `json:"tools,omitempty" yaml:"tools,omitempty"` // Will be a type of Tool
	Template               *Template `json:"template,omitempty" yaml:"template,omitempty"`
	Instructions           *string   `json:"instructions,omitempty" yaml:"instructions,omitempty"`
	AdditionalInstructions *string   `json:"additionalInstructions,omitempty" yaml:"additionalInstructions,omitempty"`
}

// Workflow A workflow agent that can orchestrate multiple steps and actions.
// This agent type is designed to handle complex workflows that may involve
// multiple tools, models, and decision points.
// The workflow agent can be configured with a series of steps that define
// the flow of execution, including conditional logic and parallel processing.
// This allows for the creation of sophisticated AI-driven processes that can
// adapt to various scenarios and requirements.
// Note: The detailed structure of the workflow steps and actions is not defined here
// and would need to be implemented based on specific use cases and requirements.
type Workflow struct {
	AgentDefinition `json:",inline" yaml:",inline"`
	Trigger         *map[string]any `json:"trigger,omitempty" yaml:"trigger,omitempty"`
}

// ContainerAgent This represents a container based agent hosted by the provider/publisher.
// The intent is to represent a container application that the user wants to run
// in a hosted environment that the provider manages.
type ContainerAgent struct {
	AgentDefinition      `json:",inline" yaml:",inline"`
	Protocols            []ProtocolVersionRecord `json:"protocols" yaml:"protocols"`
	EnvironmentVariables *[]EnvironmentVariable  `json:"environmentVariables,omitempty" yaml:"environment_variables,omitempty"`
}

// AgentManifest The following represents a manifest that can be used to create agents dynamically.
// It includes parameters that can be used to configure the agent's behavior.
// These parameters include values that can be used as publisher parameters that can
// be used to describe additional variables that have been tested and are known to work.
// Variables described here are then used to project into a prompt agent that can be executed.
// Once parameters are provided, these can be referenced in the manifest using the following notation:
// `{{myParameter}}`
// This allows for dynamic configuration of the agent based on the provided parameters.
// (This notation is used elsewhere, but only the `param` scope is supported here)
type AgentManifest struct {
	Name        string          `json:"name" yaml:"name"`
	DisplayName string          `json:"displayName" yaml:"displayName"`
	Description *string         `json:"description,omitempty" yaml:"description,omitempty"`
	Metadata    *map[string]any `json:"metadata,omitempty" yaml:"metadata,omitempty"`
	Template    any             `json:"template" yaml:"template"`
	Parameters  PropertySchema  `json:"parameters" yaml:"parameters"`
	Resources   []any           `json:"resources" yaml:"resources"` // Will be a type of Resource
}

// Binding Represents a binding between an input property and a tool parameter.
type Binding struct {
	Name  string `json:"name" yaml:"name"`
	Input string `json:"input" yaml:"input"`
}

// Connection Connection configuration for AI agents.
// `provider`, `kind`, and `endpoint` are required properties here,
// but this section can accept additional via options.
type Connection struct {
	Kind               ConnectionKind `json:"kind" yaml:"kind"`
	AuthenticationMode string         `json:"authenticationMode" yaml:"authenticationMode"`
	UsageDescription   *string        `json:"usageDescription,omitempty" yaml:"usageDescription,omitempty"`
}

// ReferenceConnection Connection configuration for AI services using named connections.
type ReferenceConnection struct {
	Connection `json:",inline" yaml:",inline"`
	Name       string  `json:"name" yaml:"name"`
	Target     *string `json:"target,omitempty" yaml:"target,omitempty"`
}

// RemoteConnection Connection configuration for AI services using named connections.
type RemoteConnection struct {
	Connection `json:",inline" yaml:",inline"`
	Name       string `json:"name" yaml:"name"`
	Endpoint   string `json:"endpoint" yaml:"endpoint"`
}

// ApiKeyConnection Connection configuration for AI services using API keys.
type ApiKeyConnection struct {
	Connection `json:",inline" yaml:",inline"`
	Endpoint   string `json:"endpoint" yaml:"endpoint"`
	//nolint:gosec // schema field name for manifest serialization, not embedded credential
	ApiKey string `json:"apiKey" yaml:"apiKey"`
}

// AnonymousConnection represents a anonymousconnection.
type AnonymousConnection struct {
	Connection `json:",inline" yaml:",inline"`
	Endpoint   string `json:"endpoint" yaml:"endpoint"`
}

// EnvironmentVariable Definition for an environment variable used in containerized agents.
type EnvironmentVariable struct {
	Name  string `json:"name" yaml:"name"`
	Value string `json:"value" yaml:"value"`
}

// Format Template format definition
type Format struct {
	Kind    string          `json:"kind" yaml:"kind"`
	Strict  *bool           `json:"strict,omitempty" yaml:"strict,omitempty"`
	Options *map[string]any `json:"options,omitempty" yaml:"options,omitempty"`
}

// McpServerApprovalMode The approval mode for MCP server tools.
type McpServerApprovalMode struct {
	Kind string `json:"kind" yaml:"kind"`
}

// McpServerToolAlwaysRequireApprovalMode represents a mcpservertoolalwaysrequireapprovalmode.
type McpServerToolAlwaysRequireApprovalMode struct {
	McpServerApprovalMode `json:",inline" yaml:",inline"`
	Kind                  string `json:"kind" yaml:"kind"`
}

// McpServerToolNeverRequireApprovalMode represents a mcpservertoolneverrequireapprovalmode.
type McpServerToolNeverRequireApprovalMode struct {
	McpServerApprovalMode `json:",inline" yaml:",inline"`
	Kind                  string `json:"kind" yaml:"kind"`
}

// McpServerToolSpecifyApprovalMode represents a mcpservertoolspecifyapprovalmode.
type McpServerToolSpecifyApprovalMode struct {
	McpServerApprovalMode      `json:",inline" yaml:",inline"`
	Kind                       string   `json:"kind" yaml:"kind"`
	AlwaysRequireApprovalTools []string `json:"alwaysRequireApprovalTools" yaml:"alwaysRequireApprovalTools"`
	NeverRequireApprovalTools  []string `json:"neverRequireApprovalTools" yaml:"neverRequireApprovalTools"`
}

// Model Model for defining the structure and behavior of AI agents.
// This model includes properties for specifying the model's provider, connection details, and various options.
// It allows for flexible configuration of AI models to suit different use cases and requirements.
type Model struct {
	Id         string        `json:"id" yaml:"id"`
	Provider   *string       `json:"provider,omitempty" yaml:"provider,omitempty"`
	ApiType    *string       `json:"apiType,omitempty" yaml:"apiType,omitempty"`
	Connection *any          `json:"connection,omitempty" yaml:"connection,omitempty"` // Must be a type of Connection
	Options    *ModelOptions `json:"options,omitempty" yaml:"options,omitempty"`
}

// ModelOptions Options for configuring the behavior of the AI model.
// `kind` is a required property here, but this section can accept additional via options.
type ModelOptions struct {
	FrequencyPenalty       *float64        `json:"frequencyPenalty,omitempty" yaml:"frequencyPenalty,omitempty"`
	MaxOutputTokens        *int            `json:"maxOutputTokens,omitempty" yaml:"maxOutputTokens,omitempty"`
	PresencePenalty        *float64        `json:"presencePenalty,omitempty" yaml:"presencePenalty,omitempty"`
	Seed                   *int            `json:"seed,omitempty" yaml:"seed,omitempty"`
	Temperature            *float64        `json:"temperature,omitempty" yaml:"temperature,omitempty"`
	TopK                   *int            `json:"topK,omitempty" yaml:"topK,omitempty"`
	TopP                   *float64        `json:"topP,omitempty" yaml:"topP,omitempty"`
	StopSequences          *[]string       `json:"stopSequences,omitempty" yaml:"stopSequences,omitempty"`
	AllowMultipleToolCalls *bool           `json:"allowMultipleToolCalls,omitempty" yaml:"allowMultipleToolCalls,omitempty"`
	AdditionalProperties   *map[string]any `json:"additionalProperties,omitempty" yaml:"additionalProperties,omitempty"`
}

// Parser Template parser definition
type Parser struct {
	Kind    string          `json:"kind" yaml:"kind"`
	Options *map[string]any `json:"options,omitempty" yaml:"options,omitempty"`
}

// Property Represents a single property.
// This model defines the structure of properties that can be used in prompts,
// including their type, description, whether they are required, and other attributes.
// It allows for the definition of dynamic inputs that can be filled with data
// and processed to generate prompts for AI models.
type Property struct {
	Name        string  `json:"name" yaml:"name"`
	Kind        string  `json:"kind" yaml:"kind"`
	Description *string `json:"description,omitempty" yaml:"description,omitempty"`
	Required    *bool   `json:"required,omitempty" yaml:"required,omitempty"`
	Default     *any    `json:"default,omitempty" yaml:"default,omitempty"`
	Example     *any    `json:"example,omitempty" yaml:"example,omitempty"`
	EnumValues  *[]any  `json:"enumValues,omitempty" yaml:"enumValues,omitempty"`
	Secret      *bool   `json:"secret,omitempty" yaml:"secret,omitempty"`
}

// ArrayProperty Represents an array property.
// This extends the base Property model to represent an array of items.
type ArrayProperty struct {
	Property `json:",inline" yaml:",inline"`
	Kind     string   `json:"kind" yaml:"kind"`
	Items    Property `json:"items" yaml:"items"`
}

// ObjectProperty Represents an object property.
// This extends the base Property model to represent a structured object.
type ObjectProperty struct {
	Property   `json:",inline" yaml:",inline"`
	Kind       string     `json:"kind" yaml:"kind"`
	Properties []Property `json:"properties" yaml:"properties"`
}

// PropertySchema Definition for the property schema of a model.
// This includes the properties and example records.
//
// The schema supports two YAML layouts for Properties:
//
// Array format (explicit):
//
//	properties:
//	  - name: foo
//	    kind: string
//
// Record/map format (canonical agent manifest shorthand):
//
//	parameters:
//	  foo:
//	    schema: { type: string }
//	    description: a foo param
//	    required: true
//
// UnmarshalYAML detects which layout is present and normalises to []Property.
type PropertySchema struct {
	Examples   *[]map[string]any `json:"examples,omitempty" yaml:"-"`
	Strict     *bool             `json:"strict,omitempty" yaml:"-"`
	Properties []Property        `json:"properties" yaml:"-"`
}

// UnmarshalYAML supports both the array format (properties: []) and the
// record/map format where parameter names are direct YAML keys.
func (ps *PropertySchema) UnmarshalYAML(value *yaml.Node) error {
	// The node should be a mapping.
	if value.Kind != yaml.MappingNode {
		return fmt.Errorf("PropertySchema: expected mapping node, got %d", value.Kind)
	}

	// First pass: look for known struct keys (examples, strict, properties).
	// Anything else is treated as a record-format parameter name.
	var (
		propertiesNode *yaml.Node
		extraKeys      []string
		extraValues    []*yaml.Node
	)

	for i := 0; i < len(value.Content)-1; i += 2 {
		key := value.Content[i].Value
		val := value.Content[i+1]

		switch key {
		case "examples":
			var examples []map[string]any
			if err := val.Decode(&examples); err != nil {
				return fmt.Errorf("PropertySchema.examples: %w", err)
			}
			ps.Examples = &examples
		case "strict":
			var strict bool
			if err := val.Decode(&strict); err != nil {
				return fmt.Errorf("PropertySchema.strict: %w", err)
			}
			ps.Strict = &strict
		case "properties":
			propertiesNode = val
		default:
			extraKeys = append(extraKeys, key)
			extraValues = append(extraValues, val)
		}
	}

	// If an explicit "properties" key was found, decode it (array or map).
	if propertiesNode != nil {
		props, err := decodePropertiesNode(propertiesNode)
		if err != nil {
			return fmt.Errorf("PropertySchema.properties: %w", err)
		}
		ps.Properties = props
		return nil
	}

	// No explicit "properties" key — treat extra keys as record-format params.
	if len(extraKeys) > 0 {
		for i, name := range extraKeys {
			prop, err := decodeRecordProperty(name, extraValues[i])
			if err != nil {
				return fmt.Errorf("PropertySchema parameter %q: %w", name, err)
			}
			ps.Properties = append(ps.Properties, prop)
		}
	}

	return nil
}

// decodePropertiesNode handles "properties:" as either an array or a map.
func decodePropertiesNode(node *yaml.Node) ([]Property, error) {
	switch node.Kind {
	case yaml.SequenceNode:
		var props []Property
		if err := node.Decode(&props); err != nil {
			return nil, err
		}
		return props, nil
	case yaml.MappingNode:
		var props []Property
		for i := 0; i < len(node.Content)-1; i += 2 {
			name := node.Content[i].Value
			prop, err := decodeRecordProperty(name, node.Content[i+1])
			if err != nil {
				return nil, fmt.Errorf("property %q: %w", name, err)
			}
			props = append(props, prop)
		}
		return props, nil
	default:
		return nil, fmt.Errorf("expected sequence or mapping, got %d", node.Kind)
	}
}

// recordEntry is the intermediate structure for parsing a single record-format
// parameter entry like:
//
//	param_name:
//	  schema: { type: string, enum: [...], default: ... }
//	  description: some text
//	  required: true
type recordEntry struct {
	Schema      *recordSchema `yaml:"schema"`
	Description string        `yaml:"description"`
	Required    bool          `yaml:"required"`
	Default     *any          `yaml:"default"`
	Example     *any          `yaml:"example"`
	EnumValues  *[]any        `yaml:"enumValues"`
}

type recordSchema struct {
	Type    string `yaml:"type"`
	Enum    []any  `yaml:"enum"`
	Default *any   `yaml:"default"`
	Secret  bool   `yaml:"secret"`
}

// decodeRecordProperty converts a record-format parameter entry into a Property.
func decodeRecordProperty(name string, node *yaml.Node) (Property, error) {
	var entry recordEntry
	if err := node.Decode(&entry); err != nil {
		return Property{}, err
	}

	prop := Property{Name: name}
	if entry.Description != "" {
		prop.Description = &entry.Description
	}
	if entry.Required {
		prop.Required = &entry.Required
	}
	if entry.Default != nil {
		prop.Default = entry.Default
	}
	if entry.Example != nil {
		prop.Example = entry.Example
	}
	if entry.EnumValues != nil {
		prop.EnumValues = entry.EnumValues
	}

	// Extract kind/default/enum/secret from nested schema if present
	if entry.Schema != nil {
		prop.Kind = entry.Schema.Type
		if entry.Schema.Default != nil && prop.Default == nil {
			prop.Default = entry.Schema.Default
		}
		if len(entry.Schema.Enum) > 0 && prop.EnumValues == nil {
			prop.EnumValues = &entry.Schema.Enum
		}
		if entry.Schema.Secret {
			prop.Secret = new(true)
		}
	}

	return prop, nil
}

// MarshalYAML writes PropertySchema back as the record/map format so that
// {{param}} placeholders elsewhere in the document survive a marshal→unmarshal
// round-trip through InjectParameterValuesIntoManifest.
func (ps PropertySchema) MarshalYAML() (any, error) {
	out := make(map[string]any)

	if ps.Examples != nil {
		out["examples"] = *ps.Examples
	}
	if ps.Strict != nil {
		out["strict"] = *ps.Strict
	}

	// Emit each property as a record-format entry.
	props := make(map[string]any, len(ps.Properties))
	for _, p := range ps.Properties {
		entry := map[string]any{}
		schema := map[string]any{}

		if p.Kind != "" {
			schema["type"] = p.Kind
		}
		if p.Default != nil {
			schema["default"] = *p.Default
		}
		if p.EnumValues != nil {
			schema["enum"] = *p.EnumValues
		}
		if p.Secret != nil && *p.Secret {
			schema["secret"] = true
		}
		if len(schema) > 0 {
			entry["schema"] = schema
		}

		if p.Description != nil {
			entry["description"] = *p.Description
		}
		if p.Required != nil {
			entry["required"] = *p.Required
		}
		if p.Example != nil {
			entry["example"] = *p.Example
		}
		props[p.Name] = entry
	}

	if len(props) > 0 {
		// Merge property keys at the top level (record format)
		for k, v := range props {
			out[k] = v
		}
	}

	return out, nil
}

// ProtocolVersionRecord represents a protocolversionrecord.
type ProtocolVersionRecord struct {
	Protocol string `json:"protocol" yaml:"protocol"`
	Version  string `json:"version" yaml:"version"`
}

// Resource Represents a resource required by the agent.
// Resources can include databases, APIs, or other external systems
// that the agent needs to interact with to perform its tasks
type Resource struct {
	Name string       `json:"name" yaml:"name"`
	Kind ResourceKind `json:"kind" yaml:"kind"`
}

// ModelResource Represents a model resource required by the agent
type ModelResource struct {
	Resource `json:",inline" yaml:",inline"`
	Id       string `json:"id" yaml:"id"`
}

// ToolResource Represents a tool resource required by the agent
type ToolResource struct {
	Resource `json:",inline" yaml:",inline"`
	Id       string         `json:"id" yaml:"id"`
	Options  map[string]any `json:"options" yaml:"options"`
}

// ToolboxToolDefinition describes a tool within a toolbox resource.
// Tools with a target/authType represent external connections (MCP, OpenAPI)
// that must be created in the Foundry project. Built-in tools (bing_grounding,
// azure_ai_search, etc.) only need an id.
type ToolboxToolDefinition struct {
	Id       string         `json:"id" yaml:"id"`
	Name     string         `json:"name,omitempty" yaml:"name,omitempty"`
	Target   string         `json:"target,omitempty" yaml:"target,omitempty"`
	AuthType string         `json:"authType,omitempty" yaml:"authType,omitempty"`
	Options  map[string]any `json:"options,omitempty" yaml:"options,omitempty"`
}

// ToolboxResource Represents a toolbox resource required by the agent.
// A toolbox is a reusable collection of tools that can be deployed as a Foundry Toolset.
type ToolboxResource struct {
	Resource    `json:",inline" yaml:",inline"`
	Id          string                  `json:"id,omitempty" yaml:"id,omitempty"`
	Description string                  `json:"description,omitempty" yaml:"description,omitempty"`
	Tools       []ToolboxToolDefinition `json:"tools,omitempty" yaml:"tools,omitempty"`
	Options     map[string]any          `json:"options,omitempty" yaml:"options,omitempty"`
}

// Template Template model for defining prompt templates.
// This model specifies the rendering engine used for slot filling prompts,
// the parser used to process the rendered template into API-compatible format,
// and additional options for the template engine.
// It allows for the creation of reusable templates that can be filled with dynamic data
// and processed to generate prompts for AI models.
type Template struct {
	Format Format `json:"format" yaml:"format"`
	Parser Parser `json:"parser" yaml:"parser"`
}

// Tool Represents a tool that can be used in prompts.
type Tool struct {
	Name        string     `json:"name" yaml:"name"`
	Kind        ToolKind   `json:"kind" yaml:"kind"`
	Description *string    `json:"description,omitempty" yaml:"description,omitempty"`
	Bindings    *[]Binding `json:"bindings,omitempty" yaml:"bindings,omitempty"`
}

// FunctionTool Represents a local function tool.
// FunctionTool A tool that calls a custom function.
// This tool allows an AI agent to call external functions and APIs.
type FunctionTool struct {
	Tool       `json:",inline" yaml:",inline"`
	Parameters PropertySchema `json:"parameters" yaml:"parameters"`
	Strict     *bool          `json:"strict,omitempty" yaml:"strict,omitempty"`
}

// CustomTool Represents a generic server tool that runs on a server.
// This tool kind is designed for operations that require server-side execution.
// It may include features such as authentication, data storage, and long-running processes.
// This tool kind is ideal for tasks that involve complex computations or access to secure resources.
// Server tools can be used to offload heavy processing from client applications.
type CustomTool struct {
	Tool       `json:",inline" yaml:",inline"`
	Connection any            `json:"connection" yaml:"connection"` // Must be a type of Connection
	Options    map[string]any `json:"options" yaml:"options"`
}

// WebSearchTool The Bing search tool.
type WebSearchTool struct {
	Tool    `json:",inline" yaml:",inline"`
	Options map[string]any `json:"options,omitempty" yaml:"options,omitempty"`
}

// BingGroundingTool The Bing search tool.
type BingGroundingTool struct {
	Tool       `json:",inline" yaml:",inline"`
	Connection any            `json:"connection" yaml:"connection"` // Must be a type of Connection
	Options    map[string]any `json:"options,omitempty" yaml:"options,omitempty"`
}

// FileSearchTool A tool for searching files.
// This tool allows an AI agent to search for files based on a query.
type FileSearchTool struct {
	Tool               `json:",inline" yaml:",inline"`
	Connection         any             `json:"connection" yaml:"connection"` // Must be a type of Connection
	VectorStoreIds     []string        `json:"vectorStoreIds" yaml:"vectorStoreIds"`
	MaximumResultCount *int            `json:"maximumResultCount,omitempty" yaml:"maximumResultCount,omitempty"`
	Ranker             *string         `json:"ranker,omitempty" yaml:"ranker,omitempty"`
	ScoreThreshold     *float64        `json:"scoreThreshold,omitempty" yaml:"scoreThreshold,omitempty"`
	Filters            *map[string]any `json:"filters,omitempty" yaml:"filters,omitempty"`
	Options            map[string]any  `json:"options" yaml:"options"`
}

// McpTool The MCP Server tool.
type McpTool struct {
	Tool              `json:",inline" yaml:",inline"`
	Connection        any                   `json:"connection" yaml:"connection"` // Must be a type of Connection
	ServerName        string                `json:"serverName" yaml:"serverName"`
	ServerDescription *string               `json:"serverDescription,omitempty" yaml:"serverDescription,omitempty"`
	ApprovalMode      McpServerApprovalMode `json:"approvalMode" yaml:"approvalMode"`
	AllowedTools      *[]string             `json:"allowedTools,omitempty" yaml:"allowedTools,omitempty"`
	Options           map[string]any        `json:"options" yaml:"options"`
}

// OpenApiTool represents a openapitool.
type OpenApiTool struct {
	Tool          `json:",inline" yaml:",inline"`
	Connection    any            `json:"connection" yaml:"connection"` // Must be a type of Connection
	Specification string         `json:"specification" yaml:"specification"`
	Options       map[string]any `json:"options" yaml:"options"`
}

// CodeInterpreterTool A tool for running code.
// This tool allows an AI agent to run and execute code snippets.
type CodeInterpreterTool struct {
	Tool    `json:",inline" yaml:",inline"`
	FileIds []string       `json:"fileIds" yaml:"fileIds"`
	Options map[string]any `json:"options" yaml:"options"`
}
