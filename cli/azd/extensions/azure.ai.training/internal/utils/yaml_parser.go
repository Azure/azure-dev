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
	Schema               string                       `yaml:"$schema"`
	Type                 string                       `yaml:"type"`
	Name                 string                       `yaml:"name"`
	DisplayName          string                       `yaml:"display_name"`
	ExperimentName       string                       `yaml:"experiment_name"`
	Description          string                       `yaml:"description"`
	Command              string                       `yaml:"command" validate:"required"`
	Environment          string                       `yaml:"environment" validate:"required"`
	Compute              string                       `yaml:"compute" validate:"required"`
	Code                 string                       `yaml:"code"`
	Inputs               map[string]InputDefinition   `yaml:"inputs"`
	Outputs              map[string]OutputDefinition  `yaml:"outputs"`
	Distribution         *DistributionDefinition      `yaml:"distribution"`
	Resources            *ResourceDefinition          `yaml:"resources"`
	InstanceCount        int                          `yaml:"instance_count"`
	GPUCount             int                          `yaml:"gpu_count"`
	EnvironmentVariables map[string]string            `yaml:"environment_variables"`
	Identity             string                       `yaml:"identity"`
	Timeout              string                       `yaml:"timeout"`
	Tags                 map[string]string            `yaml:"tags"`
	Services             map[string]ServiceDefinition `yaml:"services"`
}

// DistributionDefinition represents the distributed training launcher config in YAML.
// Mirrors the AML CLI v2 distribution schemas: pytorch, tensorflow, mpi, ray.
// Per-type fields are flat on this struct; only the fields relevant to the
// chosen type are honored, the rest are ignored and not sent to the backend.
type DistributionDefinition struct {
	Type string `yaml:"type"` // "pytorch" | "tensorflow" | "mpi" | "ray"

	// PyTorch + Mpi
	ProcessCountPerInstance int `yaml:"process_count_per_instance"`

	// TensorFlow
	ParameterServerCount int `yaml:"parameter_server_count"`
	WorkerCount          int `yaml:"worker_count"`

	// Ray. Pointer types let an unset field stay nil so we don't send zero/false
	// when the user didn't ask for it (the backend treats absent vs. explicit-0
	// differently for some of these).
	Port                     *int   `yaml:"port"`
	Address                  string `yaml:"address"`
	IncludeDashboard         *bool  `yaml:"include_dashboard"`
	DashboardPort            *int   `yaml:"dashboard_port"`
	HeadNodeAdditionalArgs   string `yaml:"head_node_additional_args"`
	WorkerNodeAdditionalArgs string `yaml:"worker_node_additional_args"`
}

// ServiceDefinition represents a job service declared in YAML.
// Mirrors the AML CLI v2 job service schemas: ssh, jupyter_lab, tensor_board,
// vs_code, custom. Per-type required fields are flat at the top of this struct
// (e.g. ssh_public_keys for ssh, log_dir for tensor_board); free-form per-type
// settings can also be supplied via `properties`.
type ServiceDefinition struct {
	Type          string         `yaml:"type"`            // "ssh" | "jupyter_lab" | "tensor_board" | "vs_code" | "custom"
	SshPublicKeys string         `yaml:"ssh_public_keys"` // ssh only: a single SSH public key string (required for ssh)
	LogDir        string         `yaml:"log_dir"`         // tensor_board only: log directory path
	Nodes         string         `yaml:"nodes"`           // "all" to run on all nodes; empty/unset → leader node only (index 0)
	Port          int            `yaml:"port"`
	Properties    map[string]any `yaml:"properties"`
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
	// #nosec G304 -- path is a user-supplied YAML job definition; reading is the command's purpose
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read job file: %w", err)
	}

	var job JobDefinition
	if err := yaml.Unmarshal(data, &job); err != nil {
		return nil, fmt.Errorf("failed to parse job YAML: %w", err)
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
