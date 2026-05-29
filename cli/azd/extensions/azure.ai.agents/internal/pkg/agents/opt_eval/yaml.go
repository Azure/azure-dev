// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package opt_eval

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"azureaiagent/internal/pkg/agents/agent_yaml"

	"go.yaml.in/yaml/v3"
)

// SafePath validates that joining baseDir with an untrusted relative path
// does not escape baseDir (zip-slip prevention). Returns the cleaned
// absolute path or an error.
func SafePath(baseDir, untrusted string) (string, error) {
	p := filepath.Join(baseDir, filepath.FromSlash(untrusted))
	p = filepath.Clean(p)

	rel, err := filepath.Rel(baseDir, p)
	if err != nil {
		return "", fmt.Errorf("path %q escapes base directory", untrusted)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path %q escapes base directory", untrusted)
	}
	return p, nil
}

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

// Agent config directory structure
//
// Each agent configuration version (baseline or optimized candidate) is stored
// under AgentConfigsDir as a self-contained directory with a fixed layout:
//
//	.agent_configs/
//	├── baseline/                  # original agent config captured by eval init or optimize
//	│   ├── metadata.yaml          # MetadataFile  — model, file pointers
//	│   ├── instructions.md        # InstructionFile — system prompt
//	│   ├── skills/                # SkillsDir — skill definitions (optional)
//	│   └── tools.json             # ToolsFile — tool definitions (optional)
//	└── <candidate-id>/            # optimized candidate written by optimize apply
//	    ├── metadata.yaml
//	    ├── instructions.md
//	    ├── skills/
//	    └── tools.json
//
// Both eval and optimize commands share these constants and layout conventions.
// Eval init writes the baseline directory; optimize apply writes candidate
// directories and reads the baseline for diff display.
const (
	// AgentConfigsDir is the top-level folder that holds agent configuration
	// versions (baseline and optimized candidates).
	AgentConfigsDir = ".agent_configs"

	// BaselineDir is the subdirectory name for the original agent configuration.
	BaselineDir = "baseline"

	// MetadataFile is the YAML file in each config directory that describes
	// the agent model, instruction file path, skill directory, and tools file.
	MetadataFile = "metadata.yaml"

	// InstructionFile is the Markdown file containing the agent's system prompt.
	InstructionFile = "instructions.md"

	// SkillsDir is the subdirectory containing skill definition files.
	SkillsDir = "skills"

	// ToolsFile is the JSON file containing tool definitions.
	ToolsFile = "tools.json"
)

// BaselineConfigRelPath returns the project-relative path to the baseline
// metadata file: ".agent_configs/baseline/metadata.yaml".
func BaselineConfigRelPath() string {
	return filepath.Join(AgentConfigsDir, BaselineDir, MetadataFile)
}

// AgentConfig holds resolved agent configuration from metadata.yaml.
// Unlike AgentRef (the YAML-serializable reference), AgentConfig contains
// fully resolved absolute paths and values for use during command execution.
type AgentConfig struct {
	ConfigFile      string // project-relative path to metadata.yaml
	Model           string // resolved model name
	InstructionFile string // absolute path to instruction file
	SkillDir        string // absolute path to skills directory
	ToolsFile       string // absolute path to tools definition file
}

// ResolvedInstruction reads and returns the instruction file content.
// Returns empty string if no instruction file is set or the file cannot be read.
func (c *AgentConfig) ResolvedInstruction() string {
	if c.InstructionFile == "" {
		return ""
	}
	data, err := os.ReadFile(c.InstructionFile) //nolint:gosec // path from project config
	if err != nil {
		return ""
	}
	return string(data)
}

// AgentRef references the agent under evaluation/optimization.
// Optimize-specific fields (skill_dir, tools_file) are stored in
// OptimizeConfig, not here, so eval.yaml stays target-agnostic.
type AgentRef struct {
	Name       string               `yaml:"name"`
	Kind       agent_yaml.AgentKind `yaml:"kind,omitempty"`
	Version    string               `yaml:"version,omitempty"`
	ConfigFile string               `yaml:"config,omitempty"`
	Model      string               `yaml:"model,omitempty"`
	// Not serialized to YAML — populated at runtime from config or flags.
	Instruction InstructionRef `yaml:"-"`
}

// ResolveConfig loads the metadata.yaml pointed to by ConfigFile and returns
// a resolved AgentConfig without mutating the AgentRef. Relative paths inside
// metadata.yaml are resolved against the directory containing the config file.
// Returns nil if ConfigFile is not set.
func (a *AgentRef) ResolveConfig(projectDir string) *AgentConfig {
	if a.ConfigFile == "" {
		return nil
	}

	configPath := a.ConfigFile
	if !filepath.IsAbs(configPath) {
		configPath = filepath.Join(projectDir, configPath)
	}
	configDir := filepath.Dir(configPath)

	cfg := &AgentConfig{ConfigFile: a.ConfigFile}

	data, err := os.ReadFile(configPath) //nolint:gosec // path from project config
	if err != nil {
		return cfg
	}

	var meta struct {
		Model           string `yaml:"model"`
		InstructionFile string `yaml:"instruction_file"`
		SkillDir        string `yaml:"skill_dir"`
		ToolsFile       string `yaml:"tools_file"`
	}
	if err := yaml.Unmarshal(data, &meta); err != nil {
		return cfg
	}

	cfg.Model = meta.Model
	if meta.InstructionFile != "" {
		instrPath := meta.InstructionFile
		if !filepath.IsAbs(instrPath) {
			instrPath = filepath.Join(configDir, instrPath)
		}
		cfg.InstructionFile = instrPath
	}
	if meta.SkillDir != "" {
		skillDir := meta.SkillDir
		if !filepath.IsAbs(skillDir) {
			skillDir = filepath.Join(configDir, skillDir)
		}
		cfg.SkillDir = skillDir
	}
	if meta.ToolsFile != "" {
		toolsFile := meta.ToolsFile
		if !filepath.IsAbs(toolsFile) {
			toolsFile = filepath.Join(configDir, toolsFile)
		}
		cfg.ToolsFile = toolsFile
	}

	return cfg
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

// OptimizationConfig is a per-target-attribute map of configuration overrides.
// Each key is a target attribute name (e.g. "model") and the value is the
// JSON-encoded configuration for that attribute.
//
// Implements yaml.Unmarshaler so YAML native types (strings, lists, maps) are
// automatically converted to json.RawMessage, allowing users to write:
//
//	optimization_config:
//	  model: ["gpt-4o", "gpt-5"]
//	  baselineModel: gpt-4o
type OptimizationConfig map[string]json.RawMessage

// UnmarshalYAML decodes each value as a YAML native type and re-encodes it as
// JSON, so users don't need to quote JSON strings in YAML.
func (oc *OptimizationConfig) UnmarshalYAML(value *yaml.Node) error {
	var raw map[string]any
	if err := value.Decode(&raw); err != nil {
		return err
	}
	result := make(OptimizationConfig, len(raw))
	for k, v := range raw {
		// If the YAML value is already a valid JSON string (e.g. '["gpt-4o"]'),
		// store it directly to avoid double-encoding.
		if s, ok := v.(string); ok {
			trimmed := strings.TrimSpace(s)
			if json.Valid([]byte(trimmed)) {
				result[k] = json.RawMessage(trimmed)
				continue
			}
		}
		data, err := json.Marshal(v)
		if err != nil {
			return fmt.Errorf("marshaling optimization_config[%q]: %w", k, err)
		}
		result[k] = data
	}
	*oc = result
	return nil
}

// Options holds run-time options for eval and optimize.
// Eval only uses EvalModel; optimize uses all fields.
type Options struct {
	EvalModel          string             `yaml:"eval_model,omitempty"`
	OptimizationConfig OptimizationConfig `yaml:"optimization_config,omitempty"`
	MaxIterations      *int               `yaml:"max_iterations,omitempty"`
	OptimizationModel  string             `yaml:"optimization_model,omitempty"`
	EvaluationLevel    string             `yaml:"evaluation_level,omitempty"`
}

// UnmarshalYAML decodes Options from a YAML node.
func (o *Options) UnmarshalYAML(value *yaml.Node) error {
	// Alias avoids infinite recursion.
	type raw Options
	if err := value.Decode((*raw)(o)); err != nil {
		return err
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
