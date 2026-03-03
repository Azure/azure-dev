// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package agent_api

// AgentProtocol represents the protocol types supported by agents
type AgentProtocol string

const (
	AgentProtocolActivityProtocol AgentProtocol = "activity_protocol"
	AgentProtocolResponses        AgentProtocol = "responses"
)

// AgentKind represents the different types of agents
type AgentKind string

const (
	AgentKindPrompt       AgentKind = "prompt"
	AgentKindHosted       AgentKind = "hosted"
	AgentKindContainerApp AgentKind = "container_app"
	AgentKindWorkflow     AgentKind = "workflow"
)

// AgentEventType represents the types of events that can be handled
type AgentEventType string

const (
	AgentEventTypeResponseCompleted AgentEventType = "response.completed"
)

// AgentEventHandlerDestinationType represents the destination types for event handlers
type AgentEventHandlerDestinationType string

const (
	AgentEventHandlerDestinationTypeEvals AgentEventHandlerDestinationType = "evals"
)

// AgentContainerStatus represents the status of an agent container
type AgentContainerStatus string

const (
	AgentContainerStatusStarting AgentContainerStatus = "Starting"
	AgentContainerStatusRunning  AgentContainerStatus = "Running"
	AgentContainerStatusStopping AgentContainerStatus = "Stopping"
	AgentContainerStatusStopped  AgentContainerStatus = "Stopped"
	AgentContainerStatusFailed   AgentContainerStatus = "Failed"
	AgentContainerStatusDeleting AgentContainerStatus = "Deleting"
	AgentContainerStatusDeleted  AgentContainerStatus = "Deleted"
	AgentContainerStatusUpdating AgentContainerStatus = "Updating"
)

// AgentContainerOperationStatus represents the status of container operations
type AgentContainerOperationStatus string

const (
	AgentContainerOperationStatusNotStarted AgentContainerOperationStatus = "NotStarted"
	AgentContainerOperationStatusInProgress AgentContainerOperationStatus = "InProgress"
	AgentContainerOperationStatusSucceeded  AgentContainerOperationStatus = "Succeeded"
	AgentContainerOperationStatusFailed     AgentContainerOperationStatus = "Failed"
)

// RaiConfig represents configuration for Responsible AI content filtering
type RaiConfig struct {
	RaiPolicyName string `json:"rai_policy_name"`
}

// AgentDefinition is the base definition for all agent types
type AgentDefinition struct {
	Kind      AgentKind  `json:"kind"`
	RaiConfig *RaiConfig `json:"rai_config,omitempty"`
}

// ProtocolVersionRecord represents a mapping for protocol and version
type ProtocolVersionRecord struct {
	Protocol AgentProtocol `json:"protocol"`
	Version  string        `json:"version"`
}

// WorkflowDefinition represents a workflow agent
type WorkflowDefinition struct {
	AgentDefinition
	Trigger map[string]interface{} `json:"trigger,omitempty"`
}

// HostedAgentDefinition represents a hosted agent
type HostedAgentDefinition struct {
	AgentDefinition
	ContainerProtocolVersions []ProtocolVersionRecord `json:"container_protocol_versions"`
	CPU                       string                  `json:"cpu"`
	Memory                    string                  `json:"memory"`
	EnvironmentVariables      map[string]string       `json:"environment_variables,omitempty"`
}

// ImageBasedHostedAgentDefinition represents an image-based hosted agent
type ImageBasedHostedAgentDefinition struct {
	HostedAgentDefinition
	Image string `json:"image"`
}

// ContainerAppAgentDefinition represents a container app agent
type ContainerAppAgentDefinition struct {
	AgentDefinition
	ContainerProtocolVersions []ProtocolVersionRecord `json:"container_protocol_versions"`
	ContainerAppResourceID    string                  `json:"container_app_resource_id"`
	IngressSubdomainSuffix    string                  `json:"ingress_subdomain_suffix"`
}

// ResponseTextFormatConfiguration represents text format configuration
type ResponseTextFormatConfiguration struct {
	// Implementation depends on OpenAI package structure
	// This is a placeholder for the actual OpenAI response format configuration
	Type string `json:"type,omitempty"`
}

// Reasoning represents OpenAI reasoning configuration
type Reasoning struct {
	// Implementation depends on OpenAI package structure
	// This is a placeholder for the actual OpenAI reasoning structure
	Effort string `json:"effort,omitempty"`
}

// ToolArgumentBinding represents binding configuration for tool arguments
type ToolArgumentBinding struct {
	ToolName     *string `json:"tool_name,omitempty"`
	ArgumentName string  `json:"argument_name"`
}

// StructuredInputDefinition represents a structured input definition
type StructuredInputDefinition struct {
	Description          *string               `json:"description,omitempty"`
	DefaultValue         interface{}           `json:"default_value,omitempty"`
	ToolArgumentBindings []ToolArgumentBinding `json:"tool_argument_bindings,omitempty"`
	Schema               interface{}           `json:"schema,omitempty"`
	Required             *bool                 `json:"required,omitempty"`
}

// PromptAgentDefinition represents a prompt-based agent
type PromptAgentDefinition struct {
	AgentDefinition
	Model            string                               `json:"model"`
	Instructions     *string                              `json:"instructions,omitempty"`
	Temperature      *float32                             `json:"temperature,omitempty"`
	TopP             *float32                             `json:"top_p,omitempty"`
	Reasoning        *Reasoning                           `json:"reasoning,omitempty"`
	Tools            []any                                `json:"tools,omitempty"` // Must be a type of Tool
	Text             *ResponseTextFormatConfiguration     `json:"text,omitempty"`
	StructuredInputs map[string]StructuredInputDefinition `json:"structured_inputs,omitempty"`
}

// CreateAgentVersionRequest represents a request to create an agent version
type CreateAgentVersionRequest struct {
	Description *string           `json:"description,omitempty"`
	Metadata    map[string]string `json:"metadata,omitempty"`
	Definition  interface{}       `json:"definition"` // Can be any of the agent definition types
}

// CreateAgentRequest represents a request to create an agent
type CreateAgentRequest struct {
	Name string `json:"name"`
	CreateAgentVersionRequest
}

// UpdateAgentRequest represents a request to update an agent
type UpdateAgentRequest struct {
	CreateAgentVersionRequest
}

// AgentVersionObject represents an agent version
type AgentVersionObject struct {
	Object      string            `json:"object"`
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Version     string            `json:"version"`
	Description *string           `json:"description,omitempty"`
	Metadata    map[string]string `json:"metadata,omitempty"`
	CreatedAt   int64             `json:"created_at"`
	Definition  interface{}       `json:"definition"` // Can be any of the agent definition types
}

// AgentObject represents an agent
type AgentObject struct {
	Object   string `json:"object"`
	ID       string `json:"id"`
	Name     string `json:"name"`
	Versions struct {
		Latest AgentVersionObject `json:"latest"`
	} `json:"versions"`
}

// CommonListObjectProperties represents common properties for list responses
type CommonListObjectProperties struct {
	Object  string `json:"object"`
	FirstID string `json:"first_id,omitempty"`
	LastID  string `json:"last_id,omitempty"`
	HasMore bool   `json:"has_more"`
}

// AgentList represents a list of agents
type AgentList struct {
	Data []AgentObject `json:"data"`
	CommonListObjectProperties
}

// AgentVersionList represents a list of agent versions
type AgentVersionList struct {
	Data []AgentVersionObject `json:"data"`
	CommonListObjectProperties
}

// DeleteAgentResponse represents the response when deleting an agent
type DeleteAgentResponse struct {
	Object  string `json:"object"`
	Name    string `json:"name"`
	Deleted bool   `json:"deleted"`
}

// DeleteAgentVersionResponse represents the response when deleting an agent version
type DeleteAgentVersionResponse struct {
	Object  string `json:"object"`
	Name    string `json:"name"`
	Version string `json:"version"`
	Deleted bool   `json:"deleted"`
}

// AgentEventHandlerFilter represents filter conditions for event handlers
type AgentEventHandlerFilter struct {
	AgentVersions []string `json:"agent_versions"`
}

// AgentEventHandlerDestination is the base for event handler destinations
type AgentEventHandlerDestination struct {
	Type AgentEventHandlerDestinationType `json:"type"`
}

// EvalsDestination represents an evals destination for event handlers
type EvalsDestination struct {
	AgentEventHandlerDestination
	EvalID        string `json:"eval_id"`
	MaxHourlyRuns *int32 `json:"max_hourly_runs,omitempty"`
}

// AgentEventHandlerRequest represents a request to create an event handler
type AgentEventHandlerRequest struct {
	Name        string                       `json:"name"`
	Metadata    map[string]string            `json:"metadata,omitempty"`
	EventTypes  []AgentEventType             `json:"event_types"`
	Filter      *AgentEventHandlerFilter     `json:"filter,omitempty"`
	Destination AgentEventHandlerDestination `json:"destination"`
}

// AgentEventHandlerObject represents an event handler
type AgentEventHandlerObject struct {
	Object      string                       `json:"object"`
	ID          string                       `json:"id"`
	Name        string                       `json:"name"`
	Metadata    map[string]string            `json:"metadata,omitempty"`
	CreatedAt   int64                        `json:"created_at"`
	EventTypes  []AgentEventType             `json:"event_types"`
	Filter      *AgentEventHandlerFilter     `json:"filter,omitempty"`
	Destination AgentEventHandlerDestination `json:"destination"`
}

// DeleteAgentEventHandlerResponse represents the response when deleting an event handler
type DeleteAgentEventHandlerResponse struct {
	Object  string `json:"object"`
	Name    string `json:"name"`
	Deleted bool   `json:"deleted"`
}

// AgentContainerOperationError represents error details for container operations
type AgentContainerOperationError struct {
	Code    string `json:"code"`
	Type    string `json:"type"`
	Message string `json:"message"`
}

// AgentContainerReplicaState represents the state of a single container replica
type AgentContainerReplicaState struct {
	Name           string `json:"name"`
	State          string `json:"state"`
	ContainerState string `json:"container_state,omitempty"`
}

// AgentContainerDetails represents the nested container runtime details
type AgentContainerDetails struct {
	HealthState       string                       `json:"health_state,omitempty"`
	ProvisioningState string                       `json:"provisioning_state,omitempty"`
	State             string                       `json:"state,omitempty"`
	UpdatedOn         string                       `json:"updated_on,omitempty"`
	Replicas          []AgentContainerReplicaState  `json:"replicas,omitempty"`
}

// AgentContainerObject represents the details of an agent container
type AgentContainerObject struct {
	Object       string                 `json:"object"`
	ID           string                 `json:"id,omitempty"`
	Status       AgentContainerStatus   `json:"status"`
	MaxReplicas  *int32                 `json:"max_replicas,omitempty"`
	MinReplicas  *int32                 `json:"min_replicas,omitempty"`
	ErrorMessage *string                `json:"error_message,omitempty"`
	CreatedAt    string                 `json:"created_at"`
	UpdatedAt    string                 `json:"updated_at"`
	Container    *AgentContainerDetails `json:"container,omitempty"`
}

// AgentContainerOperationObject represents a container operation
type AgentContainerOperationObject struct {
	ID             string                        `json:"id"`
	AgentID        string                        `json:"agent_id"`
	AgentVersionID string                        `json:"agent_version_id"`
	Status         AgentContainerOperationStatus `json:"status"`
	Error          *AgentContainerOperationError `json:"error,omitempty"`
	Container      *AgentContainerObject         `json:"container,omitempty"`
}

// AcceptedAgentContainerOperation represents an accepted container operation response
type AcceptedAgentContainerOperation struct {
	Location string                        `json:"location"` // From Operation-Location header
	Body     AgentContainerOperationObject `json:"body"`
}

// ListAgentQueryParameters represents query parameters for listing agents
type ListAgentQueryParameters struct {
	Kind   *AgentKind `json:"kind,omitempty"`
	Limit  *int32     `json:"limit,omitempty"`
	After  *string    `json:"after,omitempty"`
	Before *string    `json:"before,omitempty"`
	Order  *string    `json:"order,omitempty"`
}

// ToolType represents the type of tool
type ToolType string

const (
	ToolTypeFunction                   ToolType = "function"
	ToolTypeFileSearch                 ToolType = "file_search"
	ToolTypeComputerUsePreview         ToolType = "computer_use_preview"
	ToolTypeWebSearchPreview           ToolType = "web_search_preview"
	ToolTypeMCP                        ToolType = "mcp"
	ToolTypeCodeInterpreter            ToolType = "code_interpreter"
	ToolTypeImageGeneration            ToolType = "image_generation"
	ToolTypeLocalShell                 ToolType = "local_shell"
	ToolTypeBingGrounding              ToolType = "bing_grounding"
	ToolTypeBrowserAutomationPreview   ToolType = "browser_automation_preview"
	ToolTypeFabricDataagentPreview     ToolType = "fabric_dataagent_preview"
	ToolTypeSharepointGroundingPreview ToolType = "sharepoint_grounding_preview"
	ToolTypeAzureAISearch              ToolType = "azure_ai_search"
	ToolTypeOpenAPI                    ToolType = "openapi"
	ToolTypeBingCustomSearchPreview    ToolType = "bing_custom_search_preview"
	ToolTypeAzureFunction              ToolType = "azure_function"
	ToolTypeCaptureStructuredOutputs   ToolType = "capture_structured_outputs"
	ToolTypeA2APreview                 ToolType = "a2a_preview"
	ToolTypeMemorySearch               ToolType = "memory_search"
)

// Tool represents an OpenAI tool
type Tool struct {
	Type ToolType `json:"type"`
}

// FunctionTool defines a function in your own code the model can choose to call
type FunctionTool struct {
	Tool
	Name        string      `json:"name"`
	Description *string     `json:"description,omitempty"`
	Parameters  interface{} `json:"parameters"`
	Strict      *bool       `json:"strict"`
}

// FileSearchTool enables searching for information across vector stores
type FileSearchTool struct {
	Tool
	VectorStoreIds []string        `json:"vector_store_ids"`
	MaxNumResults  *int32          `json:"max_num_results,omitempty"`
	RankingOptions *RankingOptions `json:"ranking_options,omitempty"`
	Filters        interface{}     `json:"filters,omitempty"` // Can be ComparisonFilter or CompoundFilter
}

// CodeInterpreterTool runs Python code to help generate a response
type CodeInterpreterTool struct {
	Tool
	Container interface{} `json:"container"` // Can be string (container ID) or CodeInterpreterToolAuto object
}

// ImageGenTool generates images using a model like gpt-image-1
type ImageGenTool struct {
	Tool
	Model             *string     `json:"model,omitempty"`              // Default: "gpt-image-1"
	Quality           *string     `json:"quality,omitempty"`            // low, medium, high, auto
	Size              *string     `json:"size,omitempty"`               // 1024x1024, 1024x1536, 1536x1024, auto
	OutputFormat      *string     `json:"output_format,omitempty"`      // png, webp, jpeg
	OutputCompression *int32      `json:"output_compression,omitempty"` // 0-100
	Moderation        *string     `json:"moderation,omitempty"`         // auto, low
	Background        *string     `json:"background,omitempty"`         // transparent, opaque, auto
	InputImageMask    interface{} `json:"input_image_mask,omitempty"`   // Object with image_url and/or file_id
	PartialImages     *int32      `json:"partial_images,omitempty"`     // 0-3
}

// WebSearchPreviewTool performs web searches (preview feature)
type WebSearchPreviewTool struct {
	Tool
	UserLocation      *Location `json:"user_location,omitempty"`       // OpenAI.Location object
	SearchContextSize *string   `json:"search_context_size,omitempty"` // low, medium, high
}

// LocalShellTool allows the model to execute shell commands in a local environment
type LocalShellTool struct {
	Tool
}

// MCPTool for Model Context Protocol tools
type MCPTool struct {
	Tool
	ServerLabel         string            `json:"server_label"`
	ServerURL           string            `json:"server_url"`
	Headers             map[string]string `json:"headers,omitempty"`
	AllowedTools        interface{}       `json:"allowed_tools,omitempty"`    // Can be []string or object with tool_names
	RequireApproval     interface{}       `json:"require_approval,omitempty"` // Can be string ("always"/"never") or object with always/never properties
	ProjectConnectionID *string           `json:"project_connection_id,omitempty"`
}

// ComputerUsePreviewTool for computer use capabilities (preview feature)
type ComputerUsePreviewTool struct {
	Tool
	Environment   string `json:"environment"`    // windows, mac, linux, ubuntu, browser
	DisplayWidth  int32  `json:"display_width"`  // Required
	DisplayHeight int32  `json:"display_height"` // Required
}

// BingGroundingAgentTool for Bing grounding search functionality
type BingGroundingAgentTool struct {
	Tool
	BingGrounding BingGroundingSearchToolParameters `json:"bing_grounding"`
}

// AzureAISearchAgentTool for Azure AI Search functionality
type AzureAISearchAgentTool struct {
	Tool
	AzureAISearch AzureAISearchToolResource `json:"azure_ai_search"`
}

// SharepointAgentTool for SharePoint grounding functionality
type SharepointAgentTool struct {
	Tool
	SharepointGroundingPreview SharepointGroundingToolParameters `json:"sharepoint_grounding_preview"`
}

// MicrosoftFabricAgentTool for Microsoft Fabric data agent functionality
type MicrosoftFabricAgentTool struct {
	Tool
	FabricDataagentPreview FabricDataAgentToolParameters `json:"fabric_dataagent_preview"`
}

// OpenApiAgentTool for OpenAPI-based tools
type OpenApiAgentTool struct {
	Tool
	OpenAPI OpenApiFunctionDefinition `json:"openapi"`
}

// BingCustomSearchAgentTool for Bing custom search functionality
type BingCustomSearchAgentTool struct {
	Tool
	BingCustomSearch BingCustomSearchToolParameters `json:"bing_custom_search_preview"`
}

// BrowserAutomationAgentTool for browser automation functionality
type BrowserAutomationAgentTool struct {
	Tool
	BrowserAutomation BrowserAutomationToolParameters `json:"browser_automation_preview"`
}

// AzureFunctionAgentTool for Azure Function integration
type AzureFunctionAgentTool struct {
	Tool
	AzureFunction AzureFunctionDefinition `json:"azure_function"`
}

// CaptureStructuredOutputsTool for capturing structured outputs
type CaptureStructuredOutputsTool struct {
	Tool
	Outputs StructuredOutputDefinition `json:"outputs"`
}

// A2ATool for agent-to-agent communication (preview feature)
type A2ATool struct {
	Tool
	BaseURL             *string `json:"base_url,omitempty"`
	AgentCardPath       *string `json:"agent_card_path,omitempty"`
	ProjectConnectionID *string `json:"project_connection_id,omitempty"`
}

// MemorySearchTool for searching memory/knowledge bases
type MemorySearchTool struct {
	Tool
	MemoryStoreName string               `json:"memory_store_name"`
	Scope           string               `json:"scope"`
	SearchOptions   *MemorySearchOptions `json:"search_options,omitempty"`
	UpdateDelay     *string              `json:"update_delay,omitempty"` // duration format
}

// RankingOptions represents ranking options for file search
type RankingOptions struct {
	Ranker         *string  `json:"ranker,omitempty"`          // auto, default-2024-11-15
	ScoreThreshold *float32 `json:"score_threshold,omitempty"` // number between 0 and 1
}

// ComparisonFilter represents a filter for comparing an attribute key to a value
type ComparisonFilter struct {
	Type  string      `json:"type"` // eq, ne, gt, gte, lt, lte
	Key   string      `json:"key"`
	Value interface{} `json:"value"` // string, number, or boolean
}

// CompoundFilter represents a filter that combines multiple filters
type CompoundFilter struct {
	Type    string        `json:"type"`    // and, or
	Filters []interface{} `json:"filters"` // Array of ComparisonFilter or CompoundFilter
}

// Location represents a user location for web search
type Location struct {
	Type string `json:"type"` // Currently only "approximate" is supported
}

// ApproximateLocation represents an approximate user location
type ApproximateLocation struct {
	Location
	Country *string `json:"country,omitempty"`
	City    *string `json:"city,omitempty"`
}

// MemorySearchOptions represents options for memory search
type MemorySearchOptions struct {
	MaxMemories *int32 `json:"max_memories,omitempty"`
}

// ToolProjectConnection represents a project connection for tools
type ToolProjectConnection struct {
	ID string `json:"id"`
}

// ToolProjectConnectionList represents a list of project connections
type ToolProjectConnectionList struct {
	ProjectConnections []ToolProjectConnection `json:"project_connections"`
}

// BingGroundingSearchConfiguration represents Bing search configuration
type BingGroundingSearchConfiguration struct {
	ProjectConnectionID string  `json:"project_connection_id"`
	Market              *string `json:"market,omitempty"`
	SetLang             *string `json:"set_lang,omitempty"`
	Count               *int32  `json:"count,omitempty"`
}

// BingGroundingSearchToolParameters represents parameters for Bing grounding search
type BingGroundingSearchToolParameters struct {
	ProjectConnections   ToolProjectConnectionList          `json:"project_connections"`
	SearchConfigurations []BingGroundingSearchConfiguration `json:"search_configurations"`
}

// AzureAISearchQueryType represents query types for Azure AI Search
type AzureAISearchQueryType string

const (
	AzureAISearchQueryTypeSimple   AzureAISearchQueryType = "simple"
	AzureAISearchQueryTypeSemantic AzureAISearchQueryType = "semantic"
	AzureAISearchQueryTypeVector   AzureAISearchQueryType = "vector"
	AzureAISearchQueryTypeHybrid   AzureAISearchQueryType = "hybrid"
)

// AISearchIndexResource represents an AI Search index resource
type AISearchIndexResource struct {
	ProjectConnectionID string                  `json:"project_connection_id"`
	IndexName           *string                 `json:"indexName,omitempty"`
	QueryType           *AzureAISearchQueryType `json:"queryType,omitempty"`
	TopK                *int32                  `json:"topK,omitempty"`
	Filter              *string                 `json:"filter,omitempty"`
	IndexAssetID        *string                 `json:"indexAssetId,omitempty"`
}

// AzureAISearchToolResource represents Azure AI Search tool configuration
type AzureAISearchToolResource struct {
	IndexList []AISearchIndexResource `json:"indexList,omitempty"`
}

// SharepointGroundingToolParameters represents parameters for SharePoint grounding
type SharepointGroundingToolParameters struct {
	ProjectConnections []ToolProjectConnection `json:"project_connections,omitempty"`
}

// FabricDataAgentToolParameters represents parameters for Microsoft Fabric data agent
type FabricDataAgentToolParameters struct {
	ProjectConnections []ToolProjectConnection `json:"project_connections,omitempty"`
}

// OpenApiAuthType represents authentication types for OpenAPI
type OpenApiAuthType string

const (
	OpenApiAuthTypeAnonymous         OpenApiAuthType = "anonymous"
	OpenApiAuthTypeProjectConnection OpenApiAuthType = "project_connection"
	OpenApiAuthTypeManagedIdentity   OpenApiAuthType = "managed_identity"
)

// OpenApiAuthDetails represents authentication details for OpenAPI
type OpenApiAuthDetails struct {
	Type OpenApiAuthType `json:"type"`
}

// OpenApiFunctionDefinition represents an OpenAPI function definition
type OpenApiFunctionDefinition struct {
	Name          string             `json:"name"`
	Description   *string            `json:"description,omitempty"`
	Spec          interface{}        `json:"spec"` // JSON Schema object
	Auth          OpenApiAuthDetails `json:"auth"`
	DefaultParams []string           `json:"default_params,omitempty"`
	Functions     []OpenApiFunction  `json:"functions,omitempty"`
}

// OpenApiFunction represents a function in OpenAPI definition
type OpenApiFunction struct {
	Name        string      `json:"name"`
	Description *string     `json:"description,omitempty"`
	Parameters  interface{} `json:"parameters"` // JSON Schema object
}

// BingCustomSearchConfiguration represents Bing custom search configuration
type BingCustomSearchConfiguration struct {
	ProjectConnectionID string  `json:"project_connection_id"`
	InstanceName        string  `json:"instance_name"`
	Market              *string `json:"market,omitempty"`
	SetLang             *string `json:"set_lang,omitempty"`
	Count               *int64  `json:"count,omitempty"`
	Freshness           *string `json:"freshness,omitempty"`
}

// BingCustomSearchToolParameters represents parameters for Bing custom search
type BingCustomSearchToolParameters struct {
	SearchConfigurations []BingCustomSearchConfiguration `json:"search_configurations"`
}

// BrowserAutomationToolConnectionParameters represents connection parameters for browser automation
type BrowserAutomationToolConnectionParameters struct {
	ID string `json:"id"`
}

// BrowserAutomationToolParameters represents parameters for browser automation
type BrowserAutomationToolParameters struct {
	ProjectConnection BrowserAutomationToolConnectionParameters `json:"project_connection"`
}

// AzureFunctionStorageQueue represents an Azure function storage queue
type AzureFunctionStorageQueue struct {
	QueueServiceEndpoint string `json:"queue_service_endpoint"`
	QueueName            string `json:"queue_name"`
}

// AzureFunctionBinding represents binding for Azure functions
type AzureFunctionBinding struct {
	Type         string                    `json:"type"` // storage_queue
	StorageQueue AzureFunctionStorageQueue `json:"storage_queue"`
}

// AzureFunction represents an Azure function definition
type AzureFunction struct {
	Name        string      `json:"name"`
	Description *string     `json:"description,omitempty"`
	Parameters  interface{} `json:"parameters"` // JSON Schema object
}

// AzureFunctionDefinition represents the complete Azure function definition
type AzureFunctionDefinition struct {
	Function      AzureFunction        `json:"function"`
	InputBinding  AzureFunctionBinding `json:"input_binding"`
	OutputBinding AzureFunctionBinding `json:"output_binding"`
}

// StructuredOutputDefinition represents a structured output definition
type StructuredOutputDefinition struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	Schema      map[string]interface{} `json:"schema"`
	Strict      *bool                  `json:"strict"`
}
