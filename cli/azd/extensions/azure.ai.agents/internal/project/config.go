// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"encoding/json"
	"fmt"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/structpb"
)

// ServiceTargetAgentConfig provides custom configuration for the Azure AI Service target
type ServiceTargetAgentConfig struct {
	Environment map[string]string `json:"env,omitempty"`
	Scale       *ScaleSettings    `json:"scale,omitempty"`
	Deployments []Deployment      `json:"deployments,omitempty"`
	Resources   []Resource        `json:"resources,omitempty"`
}

// ScaleSettings provides scaling configuration for the Azure AI Service target
type ScaleSettings struct {
	MinReplicas int    `json:"minReplicas,omitempty"`
	MaxReplicas int    `json:"maxReplicas,omitempty"`
	Memory      string `json:"memory,omitempty"`
	Cpu         string `json:"cpu,omitempty"`
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
