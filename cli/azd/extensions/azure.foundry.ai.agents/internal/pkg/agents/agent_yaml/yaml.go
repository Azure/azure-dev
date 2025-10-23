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

// AgentDefinition represents The following is a specification for defining AI agents with structured metadata, inputs, outputs, tools, and templates.
// It provides a way to create reusable and composable AI agents that can be executed with specific configurations.
// The specification includes metadata about the agent, model configuration, input parameters, expected outputs,
// available tools, and template configurations for prompt rendering.
type AgentDefinition struct {
	Kind         AgentKind              `json:"kind"`                   // Kind represented by the document
	Name         string                 `json:"name"`                   // Human-readable name of the agent
	Description  string                 `json:"description,omitempty"`  // Description of the agent's capabilities and purpose
	Instructions string                 `json:"instructions,omitempty"` // Give your agent clear directions on what to do and how to do it
	Metadata     map[string]interface{} `json:"metadata,omitempty"`     // Additional metadata including authors, tags, and other arbitrary properties
	Model        Model                  `json:"model"`                  // Primary AI model configuration for the agent
	InputSchema  InputSchema            `json:"inputSchema,omitempty"`  // Input parameters that participate in template rendering
	OutputSchema OutputSchema           `json:"outputSchema,omitempty"` // Expected output format and structure from the agent
	Tools        []Tool                 `json:"tools,omitempty"`        // Tools available to the agent for extended functionality
}

// PromptAgent represents Prompt based agent definition. Used to create agents that can be executed directly.
// These agents can leverage tools, input parameters, and templates to generate responses.
// They are designed to be straightforward and easy to use for various applications.
type PromptAgent struct {
	AgentDefinition
	Kind                   AgentKind `json:"kind"`                             // Type of agent, e.g., 'prompt'
	Template               Template  `json:"template,omitempty"`               // Template configuration for prompt rendering
	Instructions           string    `json:"instructions,omitempty"`           // Give your agent clear directions on what to do and how to do it. Include specific tasks, their order, and any special instructions like tone or engagement style. (can use this for a pure yaml declaration or as content in the markdown format)
	AdditionalInstructions string    `json:"additionalInstructions,omitempty"` // Additional instructions or context for the agent, can be used to provide extra guidance (can use this for a pure yaml declaration)
}

// ContainerAgent represents The following represents a containerized agent that can be deployed and hosted.
// It includes details about the container image, registry information, and environment variables.
// This model allows for the definition of agents that can run in isolated environments,
// making them suitable for deployment in various cloud or on-premises scenarios.
//
// The containerized agent can communicate using specified protocols and can be scaled
// based on the provided configuration.
//
// This kind of agent represents the users intent to bring their own container specific
// app hosting platform that they manage.
type ContainerAgent struct {
	AgentDefinition
	Kind     AgentKind              `json:"kind"`              // Type of agent, e.g., 'container'
	Protocol string                 `json:"protocol"`          // Protocol used by the containerized agent
	Options  map[string]interface{} `json:"options,omitempty"` // Container definition including image, registry, and scaling information
}

// AgentManifest represents The following represents a manifest that can be used to create agents dynamically.
// It includes a list of models that the publisher of the manifest has tested and
// has confidence will work with an instantiated prompt agent.
// The manifest also includes parameters that can be used to configure the agent's behavior.
// These parameters include values that can be used as publisher parameters that can
// be used to describe additional variables that have been tested and are known to work.
//
// Variables described here are then used to project into a prompt agent that can be executed.
// Once parameters are provided, these can be referenced in the manifest using the following notation:
//
// `${param:MyParameter}`
//
// This allows for dynamic configuration of the agent based on the provided parameters.
// (This notation is used elsewhere, but only the `param` scope is supported here)
type AgentManifest struct {
	Agent AgentDefinition `json:"agent"` // The agent that this manifest is based on
	// Models     []Model         `json:"models"`     // Additional models that are known to work with this prompt
	Parameters []Parameter `json:"parameters"` // Parameters for configuring the agent's behavior and execution
}

// Binding represents Represents a binding between an input property and a tool parameter.
type Binding struct {
	Name  string `json:"name"`  // Name of the binding
	Input string `json:"input"` // The input property that will be bound to the tool parameter argument
}

// BingSearchConfiguration represents Configuration options for the Bing search tool.
type BingSearchConfiguration struct {
	Name      string `json:"name"`                // The name of the Bing search tool instance, used to identify the specific instance in the system
	Market    string `json:"market,omitempty"`    // The market where the results come from.
	SetLang   string `json:"setLang,omitempty"`   // The language to use for user interface strings when calling Bing API.
	Count     int64  `json:"count,omitempty"`     // The number of search results to return in the bing api response
	Freshness string `json:"freshness,omitempty"` // Filter search results by a specific time range. Accepted values: https://learn.microsoft.com/bing/search-apis/bing-web-search/reference/query-parameters
}

// Connection represents Connection configuration for AI agents.
// `provider`, `kind`, and `endpoint` are required properties here,
// but this section can accept additional via options.
type Connection struct {
	Kind             string `json:"kind"`                       // The Authentication kind for the AI service (e.g., 'key' for API key, 'oauth' for OAuth tokens)
	Authority        string `json:"authority"`                  // The authority level for the connection, indicating under whose authority the connection is made (e.g., 'user', 'agent', 'system')
	UsageDescription string `json:"usageDescription,omitempty"` // The usage description for the connection, providing context on how this connection will be used
}

// GenericConnection represents Generic connection configuration for AI services.
type GenericConnection struct {
	Connection
	Kind    string                 `json:"kind"`              // The Authentication kind for the AI service (e.g., 'key' for API key, 'oauth' for OAuth tokens)
	Options map[string]interface{} `json:"options,omitempty"` // Additional options for the connection
}

// ReferenceConnection represents Connection configuration for AI services using named connections.
type ReferenceConnection struct {
	Connection
	Kind string `json:"kind"` // The Authentication kind for the AI service (e.g., 'key' for API key, 'oauth' for OAuth tokens)
	Name string `json:"name"` // The name of the connection
}

// KeyConnection represents Connection configuration for AI services using API keys.
type KeyConnection struct {
	Connection
	Kind     string `json:"kind"`     // The Authentication kind for the AI service (e.g., 'key' for API key, 'oauth' for OAuth tokens)
	Endpoint string `json:"endpoint"` // The endpoint URL for the AI service
	Key      string `json:"key"`      // The API key for authenticating with the AI service
}

// OAuthConnection represents Connection configuration for AI services using OAuth authentication.
type OAuthConnection struct {
	Connection
	Kind         string        `json:"kind"`         // The Authentication kind for the AI service (e.g., 'key' for API key, 'oauth' for OAuth tokens)
	Endpoint     string        `json:"endpoint"`     // The endpoint URL for the AI service
	ClientId     string        `json:"clientId"`     // The OAuth client ID for authenticating with the AI service
	ClientSecret string        `json:"clientSecret"` // The OAuth client secret for authenticating with the AI service
	TokenUrl     string        `json:"tokenUrl"`     // The OAuth token URL for obtaining access tokens
	Scopes       []interface{} `json:"scopes"`       // The scopes required for the OAuth token
}

// Format represents Template format definition
type Format struct {
	Kind    string                 `json:"kind"`              // Template rendering engine used for slot filling prompts (e.g., mustache, jinja2)
	Strict  bool                   `json:"strict,omitempty"`  // Whether the template can emit structural text for parsing output
	Options map[string]interface{} `json:"options,omitempty"` // Options for the template engine
}

// HostedContainerDefinition represents Definition for a containerized AI agent hosted by the provider.
// This includes the container registry information and scaling configuration.
type HostedContainerDefinition struct {
	Scale   Scale       `json:"scale"`   // Instance scaling configuration
	Context interface{} `json:"context"` // Container context for building the container image
}

// Input represents Represents a single input property for a prompt.
// * This model defines the structure of input properties that can be used in prompts,
// including their type, description, whether they are required, and other attributes.
// * It allows for the definition of dynamic inputs that can be filled with data
// and processed to generate prompts for AI models.
type Input struct {
	Name        string      `json:"name"`                  // Name of the input property
	Kind        string      `json:"kind"`                  // The data type of the input property
	Description string      `json:"description,omitempty"` // A short description of the input property
	Required    bool        `json:"required,omitempty"`    // Whether the input property is required
	Strict      bool        `json:"strict,omitempty"`      // Whether the input property can emit structural text when parsing output
	Default     interface{} `json:"default,omitempty"`     // The default value of the input - this represents the default value if none is provided
	Sample      interface{} `json:"sample,omitempty"`      // A sample value of the input for examples and tooling
}

// ArrayInput represents Represents an array output property.
// This extends the base Output model to represent an array of items.
type ArrayInput struct {
	Input
	Kind  string `json:"kind"`
	Items Input  `json:"items"` // The type of items contained in the array
}

// ObjectInput represents Represents an object output property.
// This extends the base Output model to represent a structured object.
type ObjectInput struct {
	Input
	Kind       string        `json:"kind"`
	Properties []interface{} `json:"properties"` // The properties contained in the object
}

// InputSchema represents Definition for the input schema of a prompt.
// This includes the properties and example records.
type InputSchema struct {
	Examples   []interface{} `json:"examples,omitempty"` // Example records for the input schema
	Strict     bool          `json:"strict,omitempty"`   // Whether the input schema is strict - if true, only the defined properties are allowed
	Properties []Input       `json:"properties"`         // The input properties for the schema
}

// Model represents Model for defining the structure and behavior of AI agents.
// This model includes properties for specifying the model's provider, connection details, and various options.
// It allows for flexible configuration of AI models to suit different use cases and requirements.
type Model struct {
	Id         string       `json:"id"`                   // The unique identifier of the model - can be used as the single property shorthand
	Publisher  string       `json:"publisher,omitempty"`  // The publisher of the model (e.g., 'openai', 'azure', 'anthropic')
	Connection Connection   `json:"connection,omitempty"` // The connection configuration for the model
	Options    ModelOptions `json:"options,omitempty"`    // Additional options for the model
}

// ModelOptions represents Options for configuring the behavior of the AI model.
// `kind` is a required property here, but this section can accept additional via options.
type ModelOptions struct {
	Kind string `json:"kind"`
}

// Output represents Represents the output properties of an AI agent.
// Each output property can be a simple kind, an array, or an object.
type Output struct {
	Name        string `json:"name"`                  // Name of the output property
	Kind        string `json:"kind"`                  // The data kind of the output property
	Description string `json:"description,omitempty"` // A short description of the output property
	Required    bool   `json:"required,omitempty"`    // Whether the output property is required
}

// ArrayOutput represents Represents an array output property.
// This extends the base Output model to represent an array of items.
type ArrayOutput struct {
	Output
	Kind  string `json:"kind"`
	Items Output `json:"items"` // The type of items contained in the array
}

// ObjectOutput represents Represents an object output property.
// This extends the base Output model to represent a structured object.
type ObjectOutput struct {
	Output
	Kind       string        `json:"kind"`
	Properties []interface{} `json:"properties"` // The properties contained in the object
}

// OutputSchema represents Definition for the output schema of an AI agent.
// This includes the properties and example records.
type OutputSchema struct {
	Examples   []interface{} `json:"examples,omitempty"` // Example records for the output schema
	Properties []Output      `json:"properties"`         // The output properties for the schema
}

// Parameter represents Represents a parameter for a tool.
type Parameter struct {
	Name        string        `json:"name"`                  // Name of the parameter
	Kind        string        `json:"kind"`                  // The data type of the parameter
	Description string        `json:"description,omitempty"` // A short description of the property
	Required    bool          `json:"required,omitempty"`    // Whether the tool parameter is required
	Default     interface{}   `json:"default,omitempty"`     // The default value of the parameter - this represents the default value if none is provided
	Value       interface{}   `json:"value,omitempty"`       // Parameter value used for initializing manifest examples and tooling
	Enum        []interface{} `json:"enum,omitempty"`        // Allowed enumeration values for the parameter
}

// ObjectParameter represents Represents an object parameter for a tool.
type ObjectParameter struct {
	Parameter
	Kind       string      `json:"kind"`
	Properties []Parameter `json:"properties"` // The properties of the object parameter
}

// ArrayParameter represents Represents an array parameter for a tool.
type ArrayParameter struct {
	Parameter
	Kind  string      `json:"kind"`
	Items interface{} `json:"items"` // The kind of items contained in the array
}

// Parser represents Template parser definition
type Parser struct {
	Kind    string                 `json:"kind"`              // Parser used to process the rendered template into API-compatible format
	Options map[string]interface{} `json:"options,omitempty"` // Options for the parser
}

// Scale represents Configuration for scaling container instances.
type Scale struct {
	MinReplicas int32   `json:"minReplicas,omitempty"` // Minimum number of container instances to run
	MaxReplicas int32   `json:"maxReplicas,omitempty"` // Maximum number of container instances to run
	Cpu         float32 `json:"cpu"`                   // CPU allocation per instance (in cores)
	Memory      float32 `json:"memory"`                // Memory allocation per instance (in GB)
}

// Template represents Template model for defining prompt templates.
//
// This model specifies the rendering engine used for slot filling prompts,
// the parser used to process the rendered template into API-compatible format,
// and additional options for the template engine.
//
// It allows for the creation of reusable templates that can be filled with dynamic data
// and processed to generate prompts for AI models.
type Template struct {
	Format Format `json:"format"` // Template rendering engine used for slot filling prompts (e.g., mustache, jinja2)
	Parser Parser `json:"parser"` // Parser used to process the rendered template into API-compatible format
}

// Tool represents Represents a tool that can be used in prompts.
type Tool struct {
	Name        string    `json:"name"`                  // Name of the tool. If a function tool, this is the function name, otherwise it is the type
	Kind        string    `json:"kind"`                  // The kind identifier for the tool
	Description string    `json:"description,omitempty"` // A short description of the tool for metadata purposes
	Bindings    []Binding `json:"bindings,omitempty"`    // Tool argument bindings to input properties
}

// FunctionTool represents Represents a local function tool.
type FunctionTool struct {
	Tool
	Kind       string      `json:"kind"`       // The kind identifier for function tools
	Parameters []Parameter `json:"parameters"` // Parameters accepted by the function tool
}

// ServerTool represents Represents a generic server tool that runs on a server
// This tool kind is designed for operations that require server-side execution
// It may include features such as authentication, data storage, and long-running processes
// This tool kind is ideal for tasks that involve complex computations or access to secure resources
// Server tools can be used to offload heavy processing from client applications
type ServerTool struct {
	Tool
	Kind       string                 `json:"kind"`       // The kind identifier for server tools. This is a wildcard and can represent any server tool type not explicitly defined.
	Connection interface{}            `json:"connection"` // Connection configuration for the server tool
	Options    map[string]interface{} `json:"options"`    // Configuration options for the server tool
}

// BingSearchTool represents The Bing search tool.
type BingSearchTool struct {
	Tool
	Kind           string                    `json:"kind"`           // The kind identifier for Bing search tools
	Connection     interface{}               `json:"connection"`     // The connection configuration for the Bing search tool
	Configurations []BingSearchConfiguration `json:"configurations"` // The configuration options for the Bing search tool
}

// FileSearchTool represents A tool for searching files.
// This tool allows an AI agent to search for files based on a query.
type FileSearchTool struct {
	Tool
	Kind           string        `json:"kind"`                    // The kind identifier for file search tools
	Connection     interface{}   `json:"connection"`              // The connection configuration for the file search tool
	MaxNumResults  int32         `json:"maxNumResults,omitempty"` // The maximum number of search results to return.
	Ranker         string        `json:"ranker"`                  // File search ranker.
	ScoreThreshold float32       `json:"scoreThreshold"`          // Ranker search threshold.
	VectorStoreIds []interface{} `json:"vectorStoreIds"`          // The IDs of the vector stores to search within.
}

// McpTool represents The MCP Server tool.
type McpTool struct {
	Tool
	Kind       string        `json:"kind"`       // The kind identifier for MCP tools
	Connection interface{}   `json:"connection"` // The connection configuration for the MCP tool
	Name       string        `json:"name"`       // The name of the MCP tool
	Url        string        `json:"url"`        // The URL of the MCP server
	Allowed    []interface{} `json:"allowed"`    // List of allowed operations or resources for the MCP tool
}

// ModelTool represents The MCP Server tool.
type ModelTool struct {
	Tool
	Kind  string      `json:"kind"`  // The kind identifier for a model connection as a tool
	Model interface{} `json:"model"` // The connection configuration for the model tool
}

// OpenApiTool represents
type OpenApiTool struct {
	Tool
	Kind          string      `json:"kind"`          // The kind identifier for OpenAPI tools
	Connection    interface{} `json:"connection"`    // The connection configuration for the OpenAPI tool
	Specification string      `json:"specification"` // The URL or relative path to the OpenAPI specification document (JSON or YAML format)
}

// CodeInterpreterTool represents A tool for interpreting and executing code.
// This tool allows an AI agent to run code snippets and analyze data files.
type CodeInterpreterTool struct {
	Tool
	Kind    string        `json:"kind"`    // The kind identifier for code interpreter tools
	FileIds []interface{} `json:"fileIds"` // The IDs of the files to be used by the code interpreter tool.
}
