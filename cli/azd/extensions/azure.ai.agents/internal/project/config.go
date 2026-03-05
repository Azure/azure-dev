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
	DefaultMemory      = "0.5Gi"
	DefaultCpu         = "0.25"
	DefaultMinReplicas = 0
	DefaultMaxReplicas = 1
)

// ContainerPreset defines a named configuration for container resources.
type ContainerPreset struct {
	Label       string
	Cpu         string
	Memory      string
	MinReplicas int
	MaxReplicas int
}

// ContainerPresets defines the available container configuration tiers.
var ContainerPresets = []ContainerPreset{
	{Label: "Cost Effective — 0.25 CPU, 0.5 GB RAM, 0–1 replicas", Cpu: "0.25", Memory: "0.5Gi", MinReplicas: 0, MaxReplicas: 1},
	{Label: "Balanced — 0.5 CPU, 1.0 GB RAM, 0–3 replicas", Cpu: "0.5", Memory: "1.0Gi", MinReplicas: 0, MaxReplicas: 3},
	{Label: "Performance — 1.0 CPU, 2.0 GB RAM, 1–5 replicas", Cpu: "1.0", Memory: "2.0Gi", MinReplicas: 1, MaxReplicas: 5},
}

// ServiceTargetAgentConfig provides custom configuration for the Azure AI Service target
type ServiceTargetAgentConfig struct {
	Environment map[string]string  `json:"env,omitempty"`
	Container   *ContainerSettings `json:"container,omitempty"`
	Deployments []Deployment       `json:"deployments,omitempty"`
	Resources   []Resource         `json:"resources,omitempty"`
}

// ContainerSettings provides container configuration for the Azure AI Service target
type ContainerSettings struct {
	Resources *ResourceSettings `json:"resources,omitempty"`
	Scale     *ScaleSettings    `json:"scale,omitempty"`
}

// ResourceSettings provides resource configuration for the Azure AI Service target
type ResourceSettings struct {
	Memory string `json:"memory,omitempty"`
	Cpu    string `json:"cpu,omitempty"`
}

// ScaleSettings provides scaling configuration for the Azure AI Service target
type ScaleSettings struct {
	MinReplicas int `json:"minReplicas,omitempty"`
	MaxReplicas int `json:"maxReplicas,omitempty"`
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

	var structMap map[string]interface{}
	if err := json.Unmarshal(structBytes, &structMap); err != nil {
		return nil, fmt.Errorf("failed to unmarshal agent config to map: %w", err)
	}

	out, err := structpb.NewStruct(structMap)
	if err != nil {
		return nil, fmt.Errorf("failed to create structpb from agent config: %w", err)
	}

	return out, nil
}
