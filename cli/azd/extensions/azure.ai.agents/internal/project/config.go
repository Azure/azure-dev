package project

import (
	"encoding/json"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/structpb"
)

// FoundryAgentConfig provides custom configuration for the Foundry AI Service target
type FoundryAgentConfig struct {
	FoundryProjectEndpoint string            `json:"projectEndpoint,omitempty"`
	Environment            map[string]string `json:"env,omitempty"`
	Scale                  *ScaleSettings    `json:"scale,omitempty"`
}

// ScaleSettings provides scaling configuration for the Foundry AI Service target
type ScaleSettings struct {
	MinReplicas int    `json:"minReplicas,omitempty"`
	MaxReplicas int    `json:"maxReplicas,omitempty"`
	Memory      string `json:"memory,omitempty"`
	Cpu         string `json:"cpu,omitempty"`
}

// UnmarshalStruct converts a structpb.Struct to a Go struct of type T
func UnmarshalStruct[T any](s *structpb.Struct, out *T) error {
	structBytes, err := protojson.Marshal(s)
	if err != nil {
		return err
	}

	return json.Unmarshal(structBytes, out)
}
