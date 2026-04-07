// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package utils

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

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
	Resources            *ResourceDefinition         `yaml:"resources"`
	InstanceCount        int                         `yaml:"instance_count"`
	ProcessPerNode       int                         `yaml:"process_per_node"`
	EnvironmentVariables map[string]string           `yaml:"environment_variables"`
	Timeout              string                      `yaml:"timeout"`
	Tags                 map[string]string           `yaml:"tags"`
}

// ResourceDefinition represents the compute resource configuration in a YAML job definition.
type ResourceDefinition struct {
	InstanceCount int            `yaml:"instance_count"`
	InstanceType  string         `yaml:"instance_type"`
	ShmSize       string         `yaml:"shm_size"`
	DockerArgs    string         `yaml:"docker_args"`
	Properties    map[string]any `yaml:"properties"`
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

// ResolveRelativePaths converts relative paths in the job definition to absolute paths
// based on the YAML file's directory. Remote URIs (azureml://, https://, http://) are left as-is.
func (j *JobDefinition) ResolveRelativePaths(yamlDir string) {
	if j.Code != "" && !filepath.IsAbs(j.Code) && !IsRemoteURI(j.Code) {
		j.Code = filepath.Join(yamlDir, j.Code)
	}
	for name, input := range j.Inputs {
		if input.Path != "" && !filepath.IsAbs(input.Path) && !IsRemoteURI(input.Path) {
			input.Path = filepath.Join(yamlDir, input.Path)
			j.Inputs[name] = input
		}
	}
}

// IsRemoteURI checks if a string is a remote URI (not a local path).
func IsRemoteURI(s string) bool {
	lower := strings.ToLower(s)
	return strings.HasPrefix(lower, "azureml://") ||
		strings.HasPrefix(lower, "https://") ||
		strings.HasPrefix(lower, "http://") ||
		strings.HasPrefix(lower, "git://") ||
		strings.HasPrefix(lower, "git+")
}

// ValidateJobDefinition checks that required fields are present.
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
