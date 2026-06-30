// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"encoding/json"
	"fmt"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/structpb"
)

// Default container settings constants
const (
	DefaultMemory = "1Gi"
	DefaultCpu    = "0.5"
)

// ResourceTier defines a preset CPU and memory allocation for container resources.
type ResourceTier struct {
	Cpu    string
	Memory string
}

// String returns a human-readable label for the resource tier.
func (t ResourceTier) String() string {
	coreUnit := "cores"
	if t.Cpu == "1" {
		coreUnit = "core"
	}
	return fmt.Sprintf("%s %s, %s memory", t.Cpu, coreUnit, t.Memory)
}

// ResourceTiers defines the available container resource allocation options.
var ResourceTiers = []ResourceTier{
	{Cpu: DefaultCpu, Memory: DefaultMemory},
	{Cpu: "1", Memory: "2Gi"},
	{Cpu: "2", Memory: "4Gi"},
}

// ServiceTargetAgentConfig provides custom configuration for the Azure AI Service target
type ServiceTargetAgentConfig struct {
	// Endpoint, when set on the azure.ai.project service, points at an existing
	// Foundry project. Its presence is the brownfield signal that makes provision
	// connect to that project instead of creating a new one.
	Endpoint        string             `json:"endpoint,omitempty"`
	Environment     map[string]string  `json:"env,omitempty"`
	Container       *ContainerSettings `json:"container,omitempty"`
	Deployments     []Deployment       `json:"deployments,omitempty"`
	Resources       []Resource         `json:"resources,omitempty"`
	ToolConnections []ToolConnection   `json:"toolConnections,omitempty"`
	Toolboxes       []Toolbox          `json:"toolboxes,omitempty"`
	Connections     []Connection       `json:"connections,omitempty"`
	MemoryStores    []MemoryStore      `json:"memoryStores,omitempty"`
	StartupCommand  string             `json:"startupCommand,omitempty"`
}

// ContainerSettings provides container configuration for the Azure AI Service target
type ContainerSettings struct {
	Resources *ResourceSettings `json:"resources,omitempty"`
}

// ResourceSettings provides resource configuration for the Azure AI Service target
type ResourceSettings struct {
	Memory string `json:"memory,omitempty"`
	Cpu    string `json:"cpu,omitempty"`
}

// Deployment represents a single model deployment
type Deployment struct {
	// Specify the name of model deployment.
	Name string `json:"name"`

	// Required. Properties of model deployment.
	Model DeploymentModel `json:"model"`

	// The resource model definition representing SKU.
	Sku DeploymentSku `json:"sku"`
}

// DeploymentModel represents the model configuration for a model deployment
type DeploymentModel struct {
	// Required. The name of model deployment.
	Name string `json:"name"`

	// Required. The format of model deployment.
	Format string `json:"format"`

	// Required. The version of model deployment.
	Version string `json:"version"`
}

// DeploymentSku represents the resource model definition representing SKU
type DeploymentSku struct {
	// Required. The name of the resource model definition representing SKU.
	Name string `json:"name"`

	// The capacity of the resource model definition representing SKU.
	Capacity int `json:"capacity"`
}

// Resource represents an external resource for agent execution
type Resource struct {
	Resource       string `json:"resource"`
	ConnectionName string `json:"connectionName"`
}

// Toolbox represents a reusable collection of tools deployed as a Foundry Toolset
type Toolbox struct {
	Name        string           `json:"name"`
	Description string           `json:"description,omitempty"`
	Tools       []map[string]any `json:"tools"`
}

// MemoryStore represents a Foundry memory store provisioned (create-if-not-exists)
// during deployment. It backs the agent's memory_search tool, letting the agent retain
// context across sessions. ChatModel and EmbeddingModel reference model deployment names
// available in the Foundry project.
type MemoryStore struct {
	Name           string              `json:"name"`
	Description    string              `json:"description,omitempty"`
	ChatModel      string              `json:"chatModel"`
	EmbeddingModel string              `json:"embeddingModel"`
	Options        *MemoryStoreOptions `json:"options,omitempty"`
}

// MemoryStoreOptions controls extraction behavior and retention defaults for a memory store.
type MemoryStoreOptions struct {
	ChatSummaryEnabled      *bool  `json:"chatSummaryEnabled,omitempty"`
	UserProfileEnabled      *bool  `json:"userProfileEnabled,omitempty"`
	ProceduralMemoryEnabled *bool  `json:"proceduralMemoryEnabled,omitempty"`
	DefaultTtlSeconds       *int   `json:"defaultTtlSeconds,omitempty"`
	UserProfileDetails      string `json:"userProfileDetails,omitempty"`
}

// Connection represents a project connection matching the Bicep ConnectionPropertiesV2 spec.
type Connection struct {
	Name                        string            `json:"name"`
	Category                    string            `json:"category"`
	Target                      string            `json:"target"`
	AuthType                    string            `json:"authType"`
	Credentials                 map[string]any    `json:"credentials,omitempty"`
	Metadata                    map[string]string `json:"metadata,omitempty"`
	ExpiryTime                  string            `json:"expiryTime,omitempty"`
	IsSharedToAll               *bool             `json:"isSharedToAll,omitempty"`
	SharedUserList              []string          `json:"sharedUserList,omitempty"`
	PeRequirement               string            `json:"peRequirement,omitempty"`
	PeStatus                    string            `json:"peStatus,omitempty"`
	UseWorkspaceManagedIdentity *bool             `json:"useWorkspaceManagedIdentity,omitempty"`
	Error                       string            `json:"error,omitempty"`
	AuthorizationUrl            string            `json:"authorizationUrl,omitempty"`
	TokenUrl                    string            `json:"tokenUrl,omitempty"`
	RefreshUrl                  string            `json:"refreshUrl,omitempty"`
	Scopes                      []string          `json:"scopes,omitempty"`
	Audience                    string            `json:"audience,omitempty"`
	ConnectorName               string            `json:"connectorName,omitempty"`
}

// ToolConnection represents a connection to an external service (MCP tool, A2A, custom API)
// that must be created in the Foundry project during provisioning via Bicep.
type ToolConnection struct {
	Name        string            `json:"name"`
	Category    string            `json:"category"`
	Target      string            `json:"target"`
	AuthType    string            `json:"authType"`
	Credentials map[string]any    `json:"credentials,omitempty"`
	Metadata    map[string]string `json:"metadata,omitempty"`
}

// UnmarshalStruct converts a structpb.Struct to a Go struct of type T
func UnmarshalStruct[T any](s *structpb.Struct, out *T) error {
	structBytes, err := protojson.Marshal(s)
	if err != nil {
		return err
	}

	return json.Unmarshal(structBytes, out)
}

// MarshalStruct converts a Go struct of type T to a structpb.Struct
func MarshalStruct[T any](in *T) (*structpb.Struct, error) {
	structBytes, err := json.Marshal(in)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal agent config: %w", err)
	}

	var structMap map[string]any
	if err := json.Unmarshal(structBytes, &structMap); err != nil {
		return nil, fmt.Errorf("failed to unmarshal agent config to map: %w", err)
	}

	out, err := structpb.NewStruct(structMap)
	if err != nil {
		return nil, fmt.Errorf("failed to create structpb from agent config: %w", err)
	}

	return out, nil
}
