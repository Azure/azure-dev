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
	Kind         AgentKind               `json:"kind"`
	Name         string                  `json:"name"`
	DisplayName  *string                 `json:"displayName,omitempty"`
	Description  *string                 `json:"description,omitempty"`
	Metadata     *map[string]interface{} `json:"metadata,omitempty"`
	InputSchema  *PropertySchema         `json:"inputSchema,omitempty"`
	OutputSchema *PropertySchema         `json:"outputSchema,omitempty"`
	Tools        *[]Tool                 `json:"tools,omitempty"`
}

// PromptAgent is a prompt based agent definition used to create agents that can be executed directly.
// These agents can leverage tools, input parameters, and templates to generate responses.
// They are designed to be straightforward and easy to use for various applications.
type PromptAgent struct {
	AgentDefinition                  // Embedded parent struct
	Kind                   AgentKind `json:"kind"`
	Model                  Model     `json:"model"`
	Template               *Template `json:"template,omitempty"`
	Instructions           *string   `json:"instructions,omitempty"`
	AdditionalInstructions *string   `json:"additionalInstructions,omitempty"`
}

// HostedContainerAgent represents a container based agent hosted by the provider/publisher.
// The intent is to represent a container application that the user wants to run
// in a hosted environment that the provider manages.
type HostedContainerAgent struct {
	AgentDefinition                           // Embedded parent struct
	Kind            AgentKind                 `json:"kind"`
	Protocols       []ProtocolVersionRecord   `json:"protocols"`
	Models          []Model                   `json:"models"`
	Container       HostedContainerDefinition `json:"container"`
}

// ContainerAgent represents a containerized agent that can be deployed and hosted.
// It includes details about the container image, registry information, and environment variables.
// This model allows for the definition of agents that can run in isolated environments,
// making them suitable for deployment in various cloud or on-premises scenarios.
// The containerized agent can communicate using specified protocols and can be scaled
// based on the provided configuration. This kind of agent represents the users intent
// to bring their own container specific app hosting platform that they manage.
type ContainerAgent struct {
	AgentDefinition                         // Embedded parent struct
	Kind            AgentKind               `json:"kind"`
	Protocols       []ProtocolVersionRecord `json:"protocols"`
	Models          []Model                 `json:"models"`
	Resource        string                  `json:"resource"`
	IngressSuffix   string                  `json:"ingressSuffix"`
	Options         *map[string]interface{} `json:"options,omitempty"`
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
	AgentDefinition                         // Embedded parent struct
	Kind            AgentKind               `json:"kind"`
	Trigger         *map[string]interface{} `json:"trigger,omitempty"`
}

// AgentManifest represents a manifest that can be used to create agents dynamically.
// It includes parameters that can be used to configure the agent's behavior.
// These parameters include values that can be used as publisher parameters that can
// be used to describe additional variables that have been tested and are known to work.
// Variables described here are then used to project into a prompt agent that can be executed.
// Once parameters are provided, these can be referenced in the manifest using the following notation:
// `{{myParameter}}` This allows for dynamic configuration of the agent based on the provided parameters.
type AgentManifest struct {
	Name        string                  `json:"name"`
	DisplayName string                  `json:"displayName"`
	Description *string                 `json:"description,omitempty"`
	Metadata    *map[string]interface{} `json:"metadata,omitempty"`
	Template    any                     `json:"template"` // can be PromptAgent, HostedContainerAgent, ContainerAgent, or WorkflowAgent
	Parameters  []Parameter             `json:"parameters"`
}

// Binding represents a binding between an input property and a tool parameter.
type Binding struct {
	Name  string `json:"name"`
	Input string `json:"input"`
}

// BingSearchOption provides configuration options for the Bing search tool.
type BingSearchOption struct {
	Name      string  `json:"name"`
	Market    *string `json:"market,omitempty"`
	SetLang   *string `json:"setLang,omitempty"`
	Count     *int    `json:"count,omitempty"`
	Freshness *string `json:"freshness,omitempty"`
}

// Connection configuration for AI agents.
// `provider`, `kind`, and `endpoint` are required properties here,
// but this section can accept additional via options.
type Connection struct {
	Kind             string  `json:"kind"`
	Authority        string  `json:"authority"`
	UsageDescription *string `json:"usageDescription,omitempty"`
}

// ReferenceConnection provides connection configuration for AI services using named connections.
type ReferenceConnection struct {
	Connection        // Embedded parent struct
	Kind       string `json:"kind"`
	Name       string `json:"name"`
}

// TokenCredentialConnection provides connection configuration for AI services using token credentials.
type TokenCredentialConnection struct {
	Connection        // Embedded parent struct
	Kind       string `json:"kind"`
	Endpoint   string `json:"endpoint"`
}

// ApiKeyConnection provides connection configuration for AI services using API keys.
type ApiKeyConnection struct {
	Connection        // Embedded parent struct
	Kind       string `json:"kind"`
	Endpoint   string `json:"endpoint"`
	ApiKey     string `json:"apiKey"`
}

// EnvironmentVariable represents an environment variable configuration.
type EnvironmentVariable struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// Format represents the Format from _Format.py
type Format struct {
	Kind    string                  `json:"kind"`
	Strict  *bool                   `json:"strict,omitempty"`
	Options *map[string]interface{} `json:"options,omitempty"`
}

// HostedContainerDefinition represents the HostedContainerDefinition from _HostedContainerDefinition.py
type HostedContainerDefinition struct {
	Scale                Scale                  `json:"scale"`
	Image                *string                `json:"image,omitempty"`
	Context              map[string]interface{} `json:"context"`
	EnvironmentVariables *[]EnvironmentVariable `json:"environmentVariables,omitempty"`
}

// McpServerApprovalMode represents the McpServerApprovalMode from _McpServerApprovalMode.py
type McpServerApprovalMode struct {
	Mode                       string   `json:"mode"`
	AlwaysRequireApprovalTools []string `json:"alwaysRequireApprovalTools"`
	NeverRequireApprovalTools  []string `json:"neverRequireApprovalTools"`
}

// Model defines the structure and behavior of AI agents.
// This model includes properties for specifying the model's provider, connection details, and various options.
// It allows for flexible configuration of AI models to suit different use cases and requirements.
type Model struct {
	Id         string        `json:"id"`
	Provider   *string       `json:"provider,omitempty"`
	ApiType    string        `json:"apiType"`
	Deployment *string       `json:"deployment,omitempty"`
	Version    *string       `json:"version,omitempty"`
	Connection *Connection   `json:"connection,omitempty"`
	Options    *ModelOptions `json:"options,omitempty"`
}

// ModelOptions represents the ModelOptions from _ModelOptions.py
type ModelOptions struct {
	FrequencyPenalty       *float64                `json:"frequencyPenalty,omitempty"`
	MaxOutputTokens        *int                    `json:"maxOutputTokens,omitempty"`
	PresencePenalty        *float64                `json:"presencePenalty,omitempty"`
	Seed                   *int                    `json:"seed,omitempty"`
	Temperature            *float64                `json:"temperature,omitempty"`
	TopK                   *int                    `json:"topK,omitempty"`
	TopP                   *float64                `json:"topP,omitempty"`
	StopSequences          *[]string               `json:"stopSequences,omitempty"`
	AllowMultipleToolCalls *bool                   `json:"allowMultipleToolCalls,omitempty"`
	AdditionalProperties   *map[string]interface{} `json:"additionalProperties,omitempty"`
}

// Parameter represents the Parameter from _Parameter.py
type Parameter struct {
	Name        string          `json:"name"`
	Description *string         `json:"description,omitempty"`
	Required    *bool           `json:"required,omitempty"`
	Schema      ParameterSchema `json:"schema"`
}

// ParameterSchema represents the ParameterSchema from _ParameterSchema.py
type ParameterSchema struct {
	Type       string                  `json:"type"`
	Default    *interface{}            `json:"default,omitempty"`
	Enum       *[]interface{}          `json:"enum,omitempty"`
	Extensions *map[string]interface{} `json:"extensions,omitempty"`
}

// StringParameterSchema represents a string parameter schema.
type StringParameterSchema struct {
	ParameterSchema         // Embedded parent struct
	Type            string  `json:"type"`
	MinLength       *int    `json:"minLength,omitempty"`
	MaxLength       *int    `json:"maxLength,omitempty"`
	Pattern         *string `json:"pattern,omitempty"`
}

// DigitParameterSchema represents a digit parameter schema.
type DigitParameterSchema struct {
	ParameterSchema           // Embedded parent struct
	Type             string   `json:"type"`
	Minimum          *int     `json:"minimum,omitempty"`
	Maximum          *int     `json:"maximum,omitempty"`
	ExclusiveMinimum *bool    `json:"exclusiveMinimum,omitempty"`
	ExclusiveMaximum *bool    `json:"exclusiveMaximum,omitempty"`
	MultipleOf       *float64 `json:"multipleOf,omitempty"`
}

// Parser represents the Parser from _Parser.py
type Parser struct {
	Kind    string                  `json:"kind"`
	Options *map[string]interface{} `json:"options,omitempty"`
}

// Property represents the Property from _Property.py
type Property struct {
	Name        string         `json:"name"`
	Kind        string         `json:"kind"`
	Description *string        `json:"description,omitempty"`
	Required    *bool          `json:"required,omitempty"`
	Strict      *bool          `json:"strict,omitempty"`
	Default     *interface{}   `json:"default,omitempty"`
	Example     *interface{}   `json:"example,omitempty"`
	EnumValues  *[]interface{} `json:"enumValues,omitempty"`
}

// ArrayProperty represents an array property.
// This extends the base Property model to represent an array of items.
type ArrayProperty struct {
	Property          // Embedded parent struct
	Kind     string   `json:"kind"`
	Items    Property `json:"items"`
}

// ObjectProperty represents an object property.
// This extends the base Property model to represent a structured object.
type ObjectProperty struct {
	Property              // Embedded parent struct
	Kind       string     `json:"kind"`
	Properties []Property `json:"properties"`
}

// PropertySchema defines the property schema of a model.
// This includes the properties and example records.
type PropertySchema struct {
	Examples   *[]map[string]interface{} `json:"examples,omitempty"`
	Strict     *bool                     `json:"strict,omitempty"`
	Properties []Property                `json:"properties"`
}

// ProtocolVersionRecord represents the ProtocolVersionRecord from _ProtocolVersionRecord.py
type ProtocolVersionRecord struct {
	Protocol string `json:"protocol"`
	Version  string `json:"version"`
}

// Scale represents the Scale from _Scale.py
type Scale struct {
	MinReplicas *int    `json:"minReplicas,omitempty"`
	MaxReplicas *int    `json:"maxReplicas,omitempty"`
	Cpu         float64 `json:"cpu"`
	Memory      float64 `json:"memory"`
}

// Template represents the Template from _Template.py
type Template struct {
	Format Format `json:"format"`
	Parser Parser `json:"parser"`
}

// Tool represents a tool that can be used in prompts.
type Tool struct {
	Name        string     `json:"name"`
	Kind        string     `json:"kind"`
	Description *string    `json:"description,omitempty"`
	Bindings    *[]Binding `json:"bindings,omitempty"`
}

// FunctionTool represents a local function tool.
type FunctionTool struct {
	Tool                      // Embedded parent struct
	Kind       string         `json:"kind"`
	Parameters PropertySchema `json:"parameters"`
	Strict     *bool          `json:"strict,omitempty"`
}

// ServerTool represents a generic server tool that runs on a server.
// This tool kind is designed for operations that require server-side execution.
// It may include features such as authentication, data storage, and long-running processes.
// This tool kind is ideal for tasks that involve complex computations or access to secure resources.
// Server tools can be used to offload heavy processing from client applications.
type ServerTool struct {
	Tool                              // Embedded parent struct
	Kind       string                 `json:"kind"`
	Connection Connection             `json:"connection"`
	Options    map[string]interface{} `json:"options"`
}

// BingSearchTool represents the Bing search tool.
type BingSearchTool struct {
	Tool                          // Embedded parent struct
	Kind       string             `json:"kind"`
	Connection Connection         `json:"connection"`
	Options    []BingSearchOption `json:"options"`
}

// FileSearchTool is a tool for searching files.
// This tool allows an AI agent to search for files based on a query.
type FileSearchTool struct {
	Tool                                   // Embedded parent struct
	Kind           string                  `json:"kind"`
	Connection     Connection              `json:"connection"`
	VectorStoreIds []string                `json:"vectorStoreIds"`
	MaxNumResults  *int                    `json:"maxNumResults,omitempty"`
	Ranker         string                  `json:"ranker"`
	ScoreThreshold float64                 `json:"scoreThreshold"`
	Filters        *map[string]interface{} `json:"filters,omitempty"`
}

// McpTool represents the MCP Server tool.
type McpTool struct {
	Tool                               // Embedded parent struct
	Kind         string                `json:"kind"`
	Connection   Connection            `json:"connection"`
	Name         string                `json:"name"`
	Url          string                `json:"url"`
	ApprovalMode McpServerApprovalMode `json:"approvalMode"`
	AllowedTools []string              `json:"allowedTools"`
}

// OpenApiTool represents an OpenAPI tool.
type OpenApiTool struct {
	Tool                     // Embedded parent struct
	Kind          string     `json:"kind"`
	Connection    Connection `json:"connection"`
	Specification string     `json:"specification"`
}

// CodeInterpreterTool is a tool for interpreting and executing code.
// This tool allows an AI agent to run code snippets and analyze data files.
type CodeInterpreterTool struct {
	Tool             // Embedded parent struct
	Kind    string   `json:"kind"`
	FileIds []string `json:"fileIds"`
}
