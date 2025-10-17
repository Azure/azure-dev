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
	AgentContainerStatusStarting  AgentContainerStatus = "Starting"
	AgentContainerStatusRunning   AgentContainerStatus = "Running"
	AgentContainerStatusStopping  AgentContainerStatus = "Stopping"
	AgentContainerStatusStopped   AgentContainerStatus = "Stopped"
	AgentContainerStatusFailed    AgentContainerStatus = "Failed"
	AgentContainerStatusDeleting  AgentContainerStatus = "Deleting"
	AgentContainerStatusDeleted   AgentContainerStatus = "Deleted"
	AgentContainerStatusUpdating  AgentContainerStatus = "Updating"
)

// AgentContainerOperationStatus represents the status of container operations
type AgentContainerOperationStatus string

const (
	AgentContainerOperationStatusNotStarted  AgentContainerOperationStatus = "NotStarted"
	AgentContainerOperationStatusInProgress  AgentContainerOperationStatus = "InProgress"
	AgentContainerOperationStatusSucceeded   AgentContainerOperationStatus = "Succeeded"
	AgentContainerOperationStatusFailed      AgentContainerOperationStatus = "Failed"
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

// Tool represents an OpenAI tool
type Tool struct {
	// Implementation depends on OpenAI package structure
	// This is a placeholder for the actual OpenAI tool structure
	Type     string      `json:"type"`
	Function interface{} `json:"function,omitempty"`
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
	Description           *string               `json:"description,omitempty"`
	DefaultValue          interface{}           `json:"default_value,omitempty"`
	ToolArgumentBindings  []ToolArgumentBinding `json:"tool_argument_bindings,omitempty"`
	Schema                interface{}           `json:"schema,omitempty"`
	Required              *bool                 `json:"required,omitempty"`
}

// PromptAgentDefinition represents a prompt-based agent
type PromptAgentDefinition struct {
	AgentDefinition
	Model            string                              `json:"model"`
	Instructions     *string                             `json:"instructions,omitempty"`
	Temperature      *float32                            `json:"temperature,omitempty"`
	TopP             *float32                            `json:"top_p,omitempty"`
	Reasoning        *Reasoning                          `json:"reasoning,omitempty"`
	Tools            []Tool                              `json:"tools,omitempty"`
	Text             *ResponseTextFormatConfiguration    `json:"text,omitempty"`
	StructuredInputs map[string]StructuredInputDefinition `json:"structured_inputs,omitempty"`
}

// CreateAgentVersionRequest represents a request to create an agent version
type CreateAgentVersionRequest struct {
	Description *string                `json:"description,omitempty"`
	Metadata    map[string]string      `json:"metadata,omitempty"`
	Definition  interface{}            `json:"definition"` // Can be any of the agent definition types
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
	Object      string                 `json:"object"`
	ID          string                 `json:"id"`
	Name        string                 `json:"name"`
	Version     string                 `json:"version"`
	Description *string                `json:"description,omitempty"`
	Metadata    map[string]string      `json:"metadata,omitempty"`
	CreatedAt   int64                  `json:"created_at"`
	Definition  interface{}            `json:"definition"` // Can be any of the agent definition types
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
	EvalID         string `json:"eval_id"`
	MaxHourlyRuns  *int32 `json:"max_hourly_runs,omitempty"`
}

// AgentEventHandlerRequest represents a request to create an event handler
type AgentEventHandlerRequest struct {
	Name        string                        `json:"name"`
	Metadata    map[string]string             `json:"metadata,omitempty"`
	EventTypes  []AgentEventType              `json:"event_types"`
	Filter      *AgentEventHandlerFilter      `json:"filter,omitempty"`
	Destination AgentEventHandlerDestination  `json:"destination"`
}

// AgentEventHandlerObject represents an event handler
type AgentEventHandlerObject struct {
	Object      string                        `json:"object"`
	ID          string                        `json:"id"`
	Name        string                        `json:"name"`
	Metadata    map[string]string             `json:"metadata,omitempty"`
	CreatedAt   int64                         `json:"created_at"`
	EventTypes  []AgentEventType              `json:"event_types"`
	Filter      *AgentEventHandlerFilter      `json:"filter,omitempty"`
	Destination AgentEventHandlerDestination  `json:"destination"`
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

// AgentContainerObject represents the details of an agent container
type AgentContainerObject struct {
	Object       string                `json:"object"`
	Status       AgentContainerStatus  `json:"status"`
	MaxReplicas  *int32                `json:"max_replicas,omitempty"`
	MinReplicas  *int32                `json:"min_replicas,omitempty"`
	ErrorMessage *string               `json:"error_message,omitempty"`
	CreatedAt    string                `json:"created_at"`
	UpdatedAt    string                `json:"updated_at"`
}

// AgentContainerOperationObject represents a container operation
type AgentContainerOperationObject struct {
	ID             string                         `json:"id"`
	AgentID        string                         `json:"agent_id"`
	AgentVersionID string                         `json:"agent_version_id"`
	Status         AgentContainerOperationStatus `json:"status"`
	Error          *AgentContainerOperationError `json:"error,omitempty"`
	Container      *AgentContainerObject         `json:"container,omitempty"`
}

// AcceptedAgentContainerOperation represents an accepted container operation response
type AcceptedAgentContainerOperation struct {
	Location string                         `json:"location"` // From Operation-Location header
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
