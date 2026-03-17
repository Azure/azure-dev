// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package utils

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// JobDefinition represents a job defined in a YAML file (AML-compatible format).
type JobDefinition struct {
	Schema               string                      `yaml:"$schema"`
	Type                 string                      `yaml:"type"`
	Name                 string                      `yaml:"name"`
	DisplayName          string                      `yaml:"display_name"`
	Description          string                      `yaml:"description"`
	Command              string                      `yaml:"command"`
	Environment          string                      `yaml:"environment"`
	Compute              string                      `yaml:"compute"`
	Code                 string                      `yaml:"code"`
	Inputs               map[string]InputDefinition  `yaml:"inputs"`
	Outputs              map[string]OutputDefinition `yaml:"outputs"`
	Distribution         string                      `yaml:"distribution"`
	InstanceCount        int                         `yaml:"instance_count"`
	ProcessPerNode       int                         `yaml:"process_per_node"`
	EnvironmentVariables map[string]string           `yaml:"environment_variables"`
	Timeout              string                      `yaml:"timeout"`
	Tags                 map[string]string           `yaml:"tags"`
}

// InputDefinition represents an input in the YAML job definition.
type InputDefinition struct {
	Type  string `yaml:"type"`
	Path  string `yaml:"path"`
	Mode  string `yaml:"mode"`
	Value string `yaml:"value"`
}

// OutputDefinition represents an output in the YAML job definition.
type OutputDefinition struct {
	Type string `yaml:"type"`
	Path string `yaml:"path"`
	Mode string `yaml:"mode"`
}

// ParseJobFile reads and parses a YAML job definition file.
func ParseJobFile(path string) (*JobDefinition, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read job file: %w", err)
	}

	var job JobDefinition
	if err := yaml.Unmarshal(data, &job); err != nil {
		return nil, fmt.Errorf("failed to parse job YAML: %w", err)
	}

	if err := ValidateJobDefinition(&job); err != nil {
		return nil, err
	}

	return &job, nil
}

// ValidateJobDefinition checks that required fields are present.
// Note: 'name' is optional — if omitted, the CLI will auto-generate a UUID.
func ValidateJobDefinition(job *JobDefinition) error {
	if job.Command == "" {
		return fmt.Errorf("job YAML validation: 'command' is required")
	}
	if job.Environment == "" {
		return fmt.Errorf("job YAML validation: 'environment' is required")
	}
	if job.Compute == "" {
		return fmt.Errorf("job YAML validation: 'compute' is required")
	}
	return nil
}
