// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package agent_yaml

// AgentKind represents the type of agent
type AgentKind string

const (
	AgentKindPrompt       AgentKind = "prompt"
	AgentKindHosted       AgentKind = "hosted"
	AgentKindContainerApp AgentKind = "container_app"
	// Same as AgentKindContainerApp but this is the expected way to refer to container based agents in yaml files
	AgentKindYamlContainerApp AgentKind = "container"
	AgentKindWorkflow         AgentKind = "workflow"
)

// IsValidAgentKind checks if the provided AgentKind is valid
func IsValidAgentKind(kind AgentKind) bool {
	switch kind {
	case AgentKindPrompt, AgentKindHosted, AgentKindContainerApp, AgentKindWorkflow, AgentKindYamlContainerApp:
		return true
	default:
		return false
	}
}

// ValidAgentKinds returns a slice of all valid AgentKind values
func ValidAgentKinds() []AgentKind {
	return []AgentKind{
		AgentKindPrompt,
		AgentKindHosted,
		AgentKindContainerApp,
		AgentKindWorkflow,
	}
}

// AgentDefinition is a specification for defining AI agents with structured metadata, inputs, outputs, tools, and templates.
// It provides a way to create reusable and composable AI agents that can be executed with specific configurations.
// The specification includes metadata about the agent, model configuration, input parameters, expected outputs,
// available tools, and template configurations for prompt rendering.
type AgentDefinition struct {
	Kind         AgentKind               `json:"kind" yaml:"kind"`
	Name         string                  `json:"name" yaml:"name"`
	DisplayName  *string                 `json:"displayName,omitempty" yaml:"displayName,omitempty"`
	Description  *string                 `json:"description,omitempty" yaml:"description,omitempty"`
	Metadata     *map[string]interface{} `json:"metadata,omitempty" yaml:"metadata,omitempty"`
	InputSchema  *PropertySchema         `json:"inputSchema,omitempty" yaml:"inputSchema,omitempty"`
	OutputSchema *PropertySchema         `json:"outputSchema,omitempty" yaml:"outputSchema,omitempty"`
	Tools        *[]Tool                 `json:"tools,omitempty" yaml:"tools,omitempty"`
}

// PromptAgent is a prompt based agent definition used to create agents that can be executed directly.
// These agents can leverage tools, input parameters, and templates to generate responses.
// They are designed to be straightforward and easy to use for various applications.
type PromptAgent struct {
	AgentDefinition        `json:",inline" yaml:",inline"`
	Model                  Model     `json:"model" yaml:"model"`
	Template               *Template `json:"template,omitempty" yaml:"template,omitempty"`
	Instructions           *string   `json:"instructions,omitempty" yaml:"instructions,omitempty"`
	AdditionalInstructions *string   `json:"additionalInstructions,omitempty" yaml:"additionalInstructions,omitempty"`
}

// HostedContainerAgent represents a container based agent hosted by the provider/publisher.
// The intent is to represent a container application that the user wants to run
// in a hosted environment that the provider manages.
type HostedContainerAgent struct {
	AgentDefinition `json:",inline" yaml:",inline"`
	Protocols       []ProtocolVersionRecord   `json:"protocols" yaml:"protocols"`
	Models          []Model                   `json:"models" yaml:"models"`
	Container       HostedContainerDefinition `json:"container" yaml:"container"`
}

// ContainerAgent represents a containerized agent that can be deployed and hosted.
// It includes details about the container image, registry information, and environment variables.
// This model allows for the definition of agents that can run in isolated environments,
// making them suitable for deployment in various cloud or on-premises scenarios.
// The containerized agent can communicate using specified protocols and can be scaled
// based on the provided configuration. This kind of agent represents the users intent
// to bring their own container specific app hosting platform that they manage.
type ContainerAgent struct {
	AgentDefinition `json:",inline" yaml:",inline"`
	Protocols       []ProtocolVersionRecord `json:"protocols" yaml:"protocols"`
	Models          []Model                 `json:"models" yaml:"models"`
	Resource        string                  `json:"resource" yaml:"resource"`
	IngressSuffix   string                  `json:"ingressSuffix" yaml:"ingressSuffix"`
	Options         *map[string]interface{} `json:"options,omitempty" yaml:"options,omitempty"`
}

// WorkflowAgent is a workflow agent that can orchestrate multiple steps and actions.
// This agent type is designed to handle complex workflows that may involve
// multiple tools, models, and decision points. The workflow agent can be configured
// with a series of steps that define the flow of execution, including conditional
// logic and parallel processing. This allows for the creation of sophisticated
// AI-driven processes that can adapt to various scenarios and requirements.
// Note: The detailed structure of the workflow steps and actions is not defined here
// and would need to be implemented based on specific use cases and requirements.
type WorkflowAgent struct {
	AgentDefinition `json:",inline" yaml:",inline"`
	Trigger         *map[string]interface{} `json:"trigger,omitempty" yaml:"trigger,omitempty"`
}

// AgentManifest represents a manifest that can be used to create agents dynamically.
// It includes parameters that can be used to configure the agent's behavior.
// These parameters include values that can be used as publisher parameters that can
// be used to describe additional variables that have been tested and are known to work.
// Variables described here are then used to project into a prompt agent that can be executed.
// Once parameters are provided, these can be referenced in the manifest using the following notation:
// `{{myParameter}}` This allows for dynamic configuration of the agent based on the provided parameters.
type AgentManifest struct {
	Name        string                  `json:"name" yaml:"name"`
	DisplayName string                  `json:"displayName" yaml:"displayName"`
	Description *string                 `json:"description,omitempty" yaml:"description,omitempty"`
	Metadata    *map[string]interface{} `json:"metadata,omitempty" yaml:"metadata,omitempty"`
	Template    any                     `json:"template" yaml:"template"` // can be PromptAgent, HostedContainerAgent, ContainerAgent, or WorkflowAgent
	Parameters  *map[string]Parameter   `json:"parameters,omitempty" yaml:"parameters,omitempty"`
}

// Binding represents a binding between an input property and a tool parameter.
type Binding struct {
	Name  string `json:"name" yaml:"name"`
	Input string `json:"input" yaml:"input"`
}

// BingSearchOption provides configuration options for the Bing search tool.
type BingSearchOption struct {
	Name      string  `json:"name" yaml:"name"`
	Market    *string `json:"market,omitempty" yaml:"market,omitempty"`
	SetLang   *string `json:"setLang,omitempty" yaml:"setLang,omitempty"`
	Count     *int    `json:"count,omitempty" yaml:"count,omitempty"`
	Freshness *string `json:"freshness,omitempty" yaml:"freshness,omitempty"`
}

// Connection configuration for AI agents.
// `provider`, `kind`, and `endpoint` are required properties here,
// but this section can accept additional via options.
type Connection struct {
	Kind             string  `json:"kind" yaml:"kind"`
	Authority        string  `json:"authority" yaml:"authority"`
	UsageDescription *string `json:"usageDescription,omitempty" yaml:"usageDescription,omitempty"`
}

// ReferenceConnection provides connection configuration for AI services using named connections.
type ReferenceConnection struct {
	Connection `yaml:",inline"` // Embedded parent struct
	Kind       string           `json:"kind" yaml:"kind"`
	Name       string           `json:"name" yaml:"name"`
}

// TokenCredentialConnection provides connection configuration for AI services using token credentials.
type TokenCredentialConnection struct {
	Connection `yaml:",inline"` // Embedded parent struct
	Kind       string           `json:"kind" yaml:"kind"`
	Endpoint   string           `json:"endpoint" yaml:"endpoint"`
}

// ApiKeyConnection provides connection configuration for AI services using API keys.
type ApiKeyConnection struct {
	Connection `yaml:",inline"` // Embedded parent struct
	Kind       string           `json:"kind" yaml:"kind"`
	Endpoint   string           `json:"endpoint" yaml:"endpoint"`
	ApiKey     string           `json:"apiKey" yaml:"apiKey"`
}

// EnvironmentVariable represents an environment variable configuration.
type EnvironmentVariable struct {
	Name  string `json:"name" yaml:"name"`
	Value string `json:"value" yaml:"value"`
}

// Format represents the Format from _Format.py
type Format struct {
	Kind    string                  `json:"kind" yaml:"kind"`
	Strict  *bool                   `json:"strict,omitempty" yaml:"strict,omitempty"`
	Options *map[string]interface{} `json:"options,omitempty" yaml:"options,omitempty"`
}

// HostedContainerDefinition represents the HostedContainerDefinition from _HostedContainerDefinition.py
type HostedContainerDefinition struct {
	Scale                Scale                  `json:"scale" yaml:"scale"`
	Image                *string                `json:"image,omitempty" yaml:"image,omitempty"`
	Context              map[string]interface{} `json:"context" yaml:"context"`
	EnvironmentVariables *[]EnvironmentVariable `json:"environmentVariables,omitempty" yaml:"environmentVariables,omitempty"`
}

// McpServerApprovalMode represents the McpServerApprovalMode from _McpServerApprovalMode.py
type McpServerApprovalMode struct {
	Mode                       string   `json:"mode" yaml:"mode"`
	AlwaysRequireApprovalTools []string `json:"alwaysRequireApprovalTools" yaml:"alwaysRequireApprovalTools"`
	NeverRequireApprovalTools  []string `json:"neverRequireApprovalTools" yaml:"neverRequireApprovalTools"`
}

// Model defines the structure and behavior of AI agents.
// This model includes properties for specifying the model's provider, connection details, and various options.
// It allows for flexible configuration of AI models to suit different use cases and requirements.
type Model struct {
	Id         string        `json:"id" yaml:"id"`
	Provider   *string       `json:"provider,omitempty" yaml:"provider,omitempty"`
	ApiType    string        `json:"apiType" yaml:"apiType"`
	Deployment *string       `json:"deployment,omitempty" yaml:"deployment,omitempty"`
	Version    *string       `json:"version,omitempty" yaml:"version,omitempty"`
	Connection *Connection   `json:"connection,omitempty" yaml:"connection,omitempty"`
	Options    *ModelOptions `json:"options,omitempty" yaml:"options,omitempty"`
}

// ModelOptions represents the ModelOptions from _ModelOptions.py
type ModelOptions struct {
	FrequencyPenalty       *float64                `json:"frequencyPenalty,omitempty" yaml:"frequencyPenalty,omitempty"`
	MaxOutputTokens        *int                    `json:"maxOutputTokens,omitempty" yaml:"maxOutputTokens,omitempty"`
	PresencePenalty        *float64                `json:"presencePenalty,omitempty" yaml:"presencePenalty,omitempty"`
	Seed                   *int                    `json:"seed,omitempty" yaml:"seed,omitempty"`
	Temperature            *float64                `json:"temperature,omitempty" yaml:"temperature,omitempty"`
	TopK                   *int                    `json:"topK,omitempty" yaml:"topK,omitempty"`
	TopP                   *float64                `json:"topP,omitempty" yaml:"topP,omitempty"`
	StopSequences          *[]string               `json:"stopSequences,omitempty" yaml:"stopSequences,omitempty"`
	AllowMultipleToolCalls *bool                   `json:"allowMultipleToolCalls,omitempty" yaml:"allowMultipleToolCalls,omitempty"`
	AdditionalProperties   *map[string]interface{} `json:"additionalProperties,omitempty" yaml:"additionalProperties,omitempty"`
}

// Parameter represents the Parameter from _Parameter.py
type Parameter struct {
	Name        string          `json:"name" yaml:"name"`
	Description *string         `json:"description,omitempty" yaml:"description,omitempty"`
	Required    *bool           `json:"required,omitempty" yaml:"required,omitempty"`
	Schema      ParameterSchema `json:"schema" yaml:"schema"`
}

// ParameterSchema represents the ParameterSchema from _ParameterSchema.py
type ParameterSchema struct {
	Type       string                  `json:"type" yaml:"type"`
	Default    *interface{}            `json:"default,omitempty" yaml:"default,omitempty"`
	Enum       *[]interface{}          `json:"enum,omitempty" yaml:"enum,omitempty"`
	Extensions *map[string]interface{} `json:"extensions,omitempty" yaml:"extensions,omitempty"`
}

// StringParameterSchema represents a string parameter schema.
type StringParameterSchema struct {
	ParameterSchema `yaml:",inline"` // Embedded parent struct
	Type            string           `json:"type" yaml:"type"`
	MinLength       *int             `json:"minLength,omitempty" yaml:"minLength,omitempty"`
	MaxLength       *int             `json:"maxLength,omitempty" yaml:"maxLength,omitempty"`
	Pattern         *string          `json:"pattern,omitempty" yaml:"pattern,omitempty"`
}

// DigitParameterSchema represents a digit parameter schema.
type DigitParameterSchema struct {
	ParameterSchema  `yaml:",inline"` // Embedded parent struct
	Type             string           `json:"type" yaml:"type"`
	Minimum          *int             `json:"minimum,omitempty" yaml:"minimum,omitempty"`
	Maximum          *int             `json:"maximum,omitempty" yaml:"maximum,omitempty"`
	ExclusiveMinimum *bool            `json:"exclusiveMinimum,omitempty" yaml:"exclusiveMinimum,omitempty"`
	ExclusiveMaximum *bool            `json:"exclusiveMaximum,omitempty" yaml:"exclusiveMaximum,omitempty"`
	MultipleOf       *float64         `json:"multipleOf,omitempty" yaml:"multipleOf,omitempty"`
}

// Parser represents the Parser from _Parser.py
type Parser struct {
	Kind    string                  `json:"kind" yaml:"kind"`
	Options *map[string]interface{} `json:"options,omitempty" yaml:"options,omitempty"`
}

// Property represents the Property from _Property.py
type Property struct {
	Name        string         `json:"name" yaml:"name"`
	Kind        string         `json:"kind" yaml:"kind"`
	Description *string        `json:"description,omitempty" yaml:"description,omitempty"`
	Required    *bool          `json:"required,omitempty" yaml:"required,omitempty"`
	Strict      *bool          `json:"strict,omitempty" yaml:"strict,omitempty"`
	Default     *interface{}   `json:"default,omitempty" yaml:"default,omitempty"`
	Example     *interface{}   `json:"example,omitempty" yaml:"example,omitempty"`
	EnumValues  *[]interface{} `json:"enumValues,omitempty" yaml:"enumValues,omitempty"`
}

// ArrayProperty represents an array property.
// This extends the base Property model to represent an array of items.
type ArrayProperty struct {
	Property `yaml:",inline"` // Embedded parent struct
	Kind     string           `json:"kind" yaml:"kind"`
	Items    Property         `json:"items" yaml:"items"`
}

// ObjectProperty represents an object property.
// This extends the base Property model to represent a structured object.
type ObjectProperty struct {
	Property   `yaml:",inline"` // Embedded parent struct
	Kind       string           `json:"kind" yaml:"kind"`
	Properties []Property       `json:"properties" yaml:"properties"`
}

// PropertySchema defines the property schema of a model.
// This includes the properties and example records.
type PropertySchema struct {
	Examples   *[]map[string]interface{} `json:"examples,omitempty" yaml:"examples,omitempty"`
	Strict     *bool                     `json:"strict,omitempty" yaml:"strict,omitempty"`
	Properties []Property                `json:"properties" yaml:"properties"`
}

// ProtocolVersionRecord represents the ProtocolVersionRecord from _ProtocolVersionRecord.py
type ProtocolVersionRecord struct {
	Protocol string `json:"protocol" yaml:"protocol"`
	Version  string `json:"version" yaml:"version"`
}

// Scale represents the Scale from _Scale.py
type Scale struct {
	MinReplicas *int    `json:"minReplicas,omitempty" yaml:"minReplicas,omitempty"`
	MaxReplicas *int    `json:"maxReplicas,omitempty" yaml:"maxReplicas,omitempty"`
	Cpu         float64 `json:"cpu" yaml:"cpu"`
	Memory      float64 `json:"memory" yaml:"memory"`
}

// Template represents the Template from _Template.py
type Template struct {
	Format Format `json:"format" yaml:"format"`
	Parser Parser `json:"parser" yaml:"parser"`
}

// Tool represents a tool that can be used in prompts.
type Tool struct {
	Name        string     `json:"name" yaml:"name"`
	Kind        string     `json:"kind" yaml:"kind"`
	Description *string    `json:"description,omitempty" yaml:"description,omitempty"`
	Bindings    *[]Binding `json:"bindings,omitempty" yaml:"bindings,omitempty"`
}

// FunctionTool represents a local function tool.
type FunctionTool struct {
	Tool       `yaml:",inline"` // Embedded parent struct
	Kind       string           `json:"kind" yaml:"kind"`
	Parameters PropertySchema   `json:"parameters" yaml:"parameters"`
	Strict     *bool            `json:"strict,omitempty" yaml:"strict,omitempty"`
}

// ServerTool represents a generic server tool that runs on a server.
// This tool kind is designed for operations that require server-side execution.
// It may include features such as authentication, data storage, and long-running processes.
// This tool kind is ideal for tasks that involve complex computations or access to secure resources.
// Server tools can be used to offload heavy processing from client applications.
type ServerTool struct {
	Tool       `yaml:",inline"`       // Embedded parent struct
	Kind       string                 `json:"kind" yaml:"kind"`
	Connection Connection             `json:"connection" yaml:"connection"`
	Options    map[string]interface{} `json:"options" yaml:"options"`
}

// BingSearchTool represents the Bing search tool.
type BingSearchTool struct {
	Tool       `yaml:",inline"`   // Embedded parent struct
	Kind       string             `json:"kind" yaml:"kind"`
	Connection Connection         `json:"connection" yaml:"connection"`
	Options    []BingSearchOption `json:"options" yaml:"options"`
}

// FileSearchTool is a tool for searching files.
// This tool allows an AI agent to search for files based on a query.
type FileSearchTool struct {
	Tool           `yaml:",inline"`        // Embedded parent struct
	Kind           string                  `json:"kind" yaml:"kind"`
	Connection     Connection              `json:"connection" yaml:"connection"`
	VectorStoreIds []string                `json:"vectorStoreIds" yaml:"vectorStoreIds"`
	MaxNumResults  *int                    `json:"maxNumResults,omitempty" yaml:"maxNumResults,omitempty"`
	Ranker         string                  `json:"ranker" yaml:"ranker"`
	ScoreThreshold float64                 `json:"scoreThreshold" yaml:"scoreThreshold"`
	Filters        *map[string]interface{} `json:"filters,omitempty" yaml:"filters,omitempty"`
}

// McpTool represents the MCP Server tool.
type McpTool struct {
	Tool         `yaml:",inline"`      // Embedded parent struct
	Kind         string                `json:"kind" yaml:"kind"`
	Connection   Connection            `json:"connection" yaml:"connection"`
	Name         string                `json:"name" yaml:"name"`
	Url          string                `json:"url" yaml:"url"`
	ApprovalMode McpServerApprovalMode `json:"approvalMode" yaml:"approvalMode"`
	AllowedTools []string              `json:"allowedTools" yaml:"allowedTools"`
}

// OpenApiTool represents an OpenAPI tool.
type OpenApiTool struct {
	Tool          `yaml:",inline"` // Embedded parent struct
	Kind          string           `json:"kind" yaml:"kind"`
	Connection    Connection       `json:"connection" yaml:"connection"`
	Specification string           `json:"specification" yaml:"specification"`
}

// CodeInterpreterTool is a tool for interpreting and executing code.
// This tool allows an AI agent to run code snippets and analyze data files.
type CodeInterpreterTool struct {
	Tool    `yaml:",inline"` // Embedded parent struct
	Kind    string           `json:"kind" yaml:"kind"`
	FileIds []string         `json:"fileIds" yaml:"fileIds"`
}
