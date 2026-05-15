// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package opteval

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"

	"azureaiagent/internal/pkg/agents/agent_yaml"

	"go.yaml.in/yaml/v3"
)

// Config is the shared YAML configuration for eval and optimize commands.
//
// Contains fields common to both commands. Optimize-specific fields
// (Criteria, ValidationReference, etc) live in
// the OptimizeConfig wrapper in the cmd package.
//
// Runtime state (operation IDs, eval IDs, status) is stored in
// the azd environment rather than in this config file.
type Config struct {
	Name             string      `yaml:"name,omitempty"`
	Agent            AgentRef    `yaml:"agent"`
	DatasetFile      string      `yaml:"dataset_file,omitempty"`
	DatasetReference *DatasetRef `yaml:"dataset_reference,omitempty"`
	Evaluators       []string    `yaml:"evaluators,omitempty"`
}

// AgentRef references the agent under evaluation/optimization.
type AgentRef struct {
	Name    string               `yaml:"name"`
	Kind    agent_yaml.AgentKind `yaml:"kind,omitempty"`
	Version string               `yaml:"version,omitempty"`
	Model   string               `yaml:"model,omitempty"`
}

// DatasetRef references a named/versioned dataset.
type DatasetRef struct {
	Name    string `yaml:"name"`
	Version string `yaml:"version,omitempty"`
}

// Options holds run-time options for eval and optimize.
// Eval only uses EvalModel; optimize uses all fields.
type Options struct {
	EvalModel            string   `yaml:"eval_model,omitempty"`
	Mode                 string   `yaml:"mode,omitempty"`
	TargetAttributes     []string `yaml:"target_attributes,omitempty"`
	Budget               int      `yaml:"budget,omitempty"`
	MaxIterations        int      `yaml:"max_iterations,omitempty"`
	MinImprovement       float64  `yaml:"min_improvement,omitempty"`
	ImprovementThreshold float64  `yaml:"improvement_threshold,omitempty"`
	PassThreshold        float64  `yaml:"pass_threshold,omitempty"`
	KeepVersions         bool     `yaml:"keep_versions,omitempty"`
	TasksPerIteration    int      `yaml:"tasks_per_iteration,omitempty"`
	ReflectionModel      string   `yaml:"reflection_model,omitempty"`
}

// DefaultTargetAttributes are the default optimization target attributes.
var DefaultTargetAttributes = []string{"instruction", "skill", "agents-optimization-job"}

// Deprecated: DefaultStrategies is an alias for backward compatibility.
var DefaultStrategies = DefaultTargetAttributes

// UnmarshalYAML populates default target attributes when the field is absent in YAML.
// For backward compatibility, the legacy "strategies" key is also accepted.
func (o *Options) UnmarshalYAML(value *yaml.Node) error {
	// Alias avoids infinite recursion.
	type raw Options
	if err := value.Decode((*raw)(o)); err != nil {
		return err
	}

	// Backward compatibility: if "strategies" is present and target_attributes is not,
	// migrate the value.
	if len(o.TargetAttributes) == 0 {
		var legacy struct {
			Strategies []string `yaml:"strategies"`
		}
		_ = value.Decode(&legacy)
		if len(legacy.Strategies) > 0 {
			o.TargetAttributes = legacy.Strategies
		}
	}

	if len(o.TargetAttributes) == 0 {
		o.TargetAttributes = slices.Clone(DefaultTargetAttributes)
		// o.MaxIterations = 5
		// o.Budget = 100
	}
	return nil
}

// Read reads a YAML config file (eval or optimize format).
func Read(path string) (*Config, error) {
	data, err := os.ReadFile(path) //nolint:gosec // path is provided by user for local config
	if err != nil {
		return nil, fmt.Errorf("failed to read config %q: %w", path, err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse config %q: %w", path, err)
	}

	return &cfg, nil
}

// Write writes a YAML config file.
func Write(path string, cfg *Config) error {
	if err := os.MkdirAll(filepath.Dir(path), 0750); err != nil {
		return fmt.Errorf("creating config directory: %w", err)
	}

	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}

	return os.WriteFile(path, data, 0600)
}
