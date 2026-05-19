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
	Name             string        `yaml:"name,omitempty"`
	Agent            AgentRef      `yaml:"agent"`
	DatasetFile      string        `yaml:"dataset_file,omitempty"`
	DatasetReference *DatasetRef   `yaml:"dataset_reference,omitempty"`
	Evaluators       EvaluatorList `yaml:"evaluators,omitempty"`
}

// EvaluatorRef describes an evaluator. It can be a simple string name or a
// structured entry with name, version, and local_uri.
type EvaluatorRef struct {
	Name     string `yaml:"name" json:"name"`
	Version  string `yaml:"version,omitempty" json:"version,omitempty"`
	LocalURI string `yaml:"local_uri,omitempty" json:"local_uri,omitempty"`
}

// EvaluatorList is a list of evaluators that supports mixed YAML:
//
//	evaluators:
//	  - builtin.task_adherence
//	  - name: custom-quality
//	    version: "2"
//	    local_uri: evaluators/custom-quality_2.json
type EvaluatorList []EvaluatorRef

// UnmarshalYAML handles both plain string and mapping entries.
func (el *EvaluatorList) UnmarshalYAML(value *yaml.Node) error {
	if value.Kind != yaml.SequenceNode {
		return fmt.Errorf("evaluators must be a sequence, got %v", value.Kind)
	}

	result := make([]EvaluatorRef, 0, len(value.Content))
	for _, item := range value.Content {
		switch item.Kind {
		case yaml.ScalarNode:
			// Plain string entry: "builtin.task_adherence"
			result = append(result, EvaluatorRef{Name: item.Value})
		case yaml.MappingNode:
			// Structured entry: {name: ..., version: ..., local_uri: ...}
			var ref EvaluatorRef
			if err := item.Decode(&ref); err != nil {
				return fmt.Errorf("parsing evaluator entry: %w", err)
			}
			result = append(result, ref)
		default:
			return fmt.Errorf("unexpected evaluator entry type: %v", item.Kind)
		}
	}
	*el = result
	return nil
}

// MarshalYAML emits plain strings for simple evaluators and mappings for
// structured ones (those with version or local_uri).
func (el EvaluatorList) MarshalYAML() (any, error) {
	nodes := make([]*yaml.Node, 0, len(el))
	for _, ref := range el {
		if ref.Version == "" && ref.LocalURI == "" {
			// Emit as a plain string.
			nodes = append(nodes, &yaml.Node{
				Kind:  yaml.ScalarNode,
				Tag:   "!!str",
				Value: ref.Name,
			})
		} else {
			// Emit as a mapping.
			var n yaml.Node
			if err := n.Encode(ref); err != nil {
				return nil, err
			}
			nodes = append(nodes, &n)
		}
	}
	return &yaml.Node{Kind: yaml.SequenceNode, Content: nodes}, nil
}

// Names returns the evaluator names as a plain string slice.
func (el EvaluatorList) Names() []string {
	names := make([]string, len(el))
	for i, ref := range el {
		names[i] = ref.Name
	}
	return names
}

// FindByLocalURI returns all evaluators that have a local_uri set.
func (el EvaluatorList) FindByLocalURI() []EvaluatorRef {
	var refs []EvaluatorRef
	for _, ref := range el {
		if ref.LocalURI != "" {
			refs = append(refs, ref)
		}
	}
	return refs
}

// SetVersion updates the version of a named evaluator in the list.
func (el EvaluatorList) SetVersion(name, version string) {
	for i := range el {
		if el[i].Name == name {
			el[i].Version = version
			return
		}
	}
}

// SetLocalURI updates the local_uri of a named evaluator in the list.
func (el EvaluatorList) SetLocalURI(name, uri string) {
	for i := range el {
		if el[i].Name == name {
			el[i].LocalURI = uri
			return
		}
	}
}

// AgentRef references the agent under evaluation/optimization.
type AgentRef struct {
	Name        string               `yaml:"name"`
	Kind        agent_yaml.AgentKind `yaml:"kind,omitempty"`
	Version     string               `yaml:"version,omitempty"`
	Model       string               `yaml:"model,omitempty"`
	Instruction InstructionRef       `yaml:"instruction,omitempty"`
	SkillDir    string               `yaml:"skill_dir,omitempty"`
}

// ResolvedSystemPrompt returns the resolved instruction text.
// If the instruction references a file, its contents are read; otherwise the
// inline value is returned.
func (a *AgentRef) ResolvedSystemPrompt() string {
	return a.Instruction.Resolve()
}

// InstructionRef holds an instruction that can be either an inline string or a
// file reference. In YAML it supports two forms:
//
//	instruction: "inline text"
//	instruction:
//	  file: ./path/to/file.md
type InstructionRef struct {
	Value string `yaml:"-"` // inline text
	File  string `yaml:"-"` // file reference
}

// Resolve returns the instruction text. If File is set, the file is read;
// otherwise Value is returned directly.
func (r *InstructionRef) Resolve() string {
	if r.File != "" {
		data, err := os.ReadFile(r.File)
		if err != nil {
			return r.Value
		}
		return string(data)
	}
	return r.Value
}

// IsEmpty returns true if neither inline value nor file is set.
func (r *InstructionRef) IsEmpty() bool {
	return r.Value == "" && r.File == ""
}

// UnmarshalYAML allows InstructionRef to be either a plain string or a mapping
// with a "file" key.
func (r *InstructionRef) UnmarshalYAML(value *yaml.Node) error {
	if value.Kind == yaml.ScalarNode {
		r.Value = value.Value
		return nil
	}
	if value.Kind == yaml.MappingNode {
		var m struct {
			File string `yaml:"file"`
		}
		if err := value.Decode(&m); err != nil {
			return err
		}
		r.File = m.File
		return nil
	}
	return fmt.Errorf("instruction must be a string or a mapping with 'file' key")
}

// MarshalYAML writes InstructionRef as a plain string when inline, or as a
// mapping with "file" when referencing a file.
func (r InstructionRef) MarshalYAML() (any, error) {
	if r.File != "" {
		return map[string]string{"file": r.File}, nil
	}
	return r.Value, nil
}

// DatasetRef references a named/versioned dataset.
type DatasetRef struct {
	Name     string `yaml:"name"`
	Version  string `yaml:"version,omitempty"`
	LocalURI string `yaml:"local_uri,omitempty"`
}

// TargetConfig specifies model candidates and other target-specific configuration.
type TargetConfig struct {
	Model []string `yaml:"model,omitempty"`
}

// Options holds run-time options for eval and optimize.
// Eval only uses EvalModel; optimize uses all fields.
type Options struct {
	EvalModel            string        `yaml:"eval_model,omitempty"`
	Mode                 string        `yaml:"mode,omitempty"`
	TargetAttributes     []string      `yaml:"target_attributes,omitempty"`
	TargetConfig         *TargetConfig `yaml:"target_config,omitempty"`
	Budget               int           `yaml:"budget,omitempty"`
	MaxIterations        int           `yaml:"max_iterations,omitempty"`
	MinImprovement       float64       `yaml:"min_improvement,omitempty"`
	ImprovementThreshold float64       `yaml:"improvement_threshold,omitempty"`
	PassThreshold        float64       `yaml:"pass_threshold,omitempty"`
	KeepVersions         bool          `yaml:"keep_versions,omitempty"`
	TasksPerIteration    int           `yaml:"tasks_per_iteration,omitempty"`
	ReflectionModel      string        `yaml:"reflection_model,omitempty"`
}

// DefaultTargetAttributes are the default optimization target attributes.
var DefaultTargetAttributes = []string{"agents-optimization-job"}

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
	}

	o.MaxIterations = 4
	o.Budget = 100
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
