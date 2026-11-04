// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// Package optconfig holds resolved optimizer inputs and agent configuration files.
package optconfig

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
// does not escape baseDir.
func SafePath(baseDir, untrusted string) (string, error) {
	p := filepath.Clean(filepath.Join(baseDir, filepath.FromSlash(untrusted)))
	rel, err := filepath.Rel(baseDir, p)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path %q escapes base directory", untrusted)
	}
	return p, nil
}

// Config holds resolved inputs, not the user-facing GA request schema.
type Config struct {
	Name        string
	Agent       AgentRef
	DatasetFile string
	Dataset     *DatasetRef
	Evaluators  EvaluatorList
}

// LocalDatasetPath returns the local training dataset path, if any.
func (c *Config) LocalDatasetPath() string {
	if c.DatasetFile != "" {
		return c.DatasetFile
	}
	if c.Dataset.IsLocal() {
		return c.Dataset.LocalURI
	}
	return ""
}

// RemoteDatasetReference returns a registered dataset reference, if any.
func (c *Config) RemoteDatasetReference() *DatasetRef {
	if c.Dataset.IsRemote() {
		return c.Dataset
	}
	return nil
}

// EvaluatorRef identifies an evaluator used by optimization.
type EvaluatorRef struct {
	Name                     string
	Version                  string
	LocalURI                 string
	InitializationParameters map[string]any
}

// EvaluatorList contains resolved evaluators.
type EvaluatorList []EvaluatorRef

// Agent configuration directory layout constants.
const (
	AgentConfigsDir = ".agent_configs"
	BaselineDir     = "baseline"
	MetadataFile    = "metadata.yaml"
	InstructionFile = "instructions.md"
	SkillsDir       = "skills"
	ToolsFile       = "tools.json"
)

// BaselineConfigRelPath returns the project-relative baseline metadata path.
func BaselineConfigRelPath() string {
	return filepath.Join(AgentConfigsDir, BaselineDir, MetadataFile)
}

// AgentConfig holds resolved agent configuration from metadata.yaml.
type AgentConfig struct {
	ConfigFile      string
	Model           string
	InstructionFile string
	SkillDir        string
	ToolsFile       string
}

// AgentRef references the agent being optimized and its local baseline.
type AgentRef struct {
	Name        string
	Kind        agent_yaml.AgentKind
	Version     string
	ConfigFile  string
	Model       string
	Instruction InstructionRef
}

// ResolveConfig loads the baseline metadata pointed to by ConfigFile without
// mutating the reference. Metadata paths resolve relative to the metadata file.
// The tool_file key takes precedence over the older tools_file key.
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
		ToolsFile       string `yaml:"tool_file"`
		LegacyToolsFile string `yaml:"tools_file"`
	}
	if err := yaml.Unmarshal(data, &meta); err != nil {
		return cfg
	}
	if meta.ToolsFile == "" {
		meta.ToolsFile = meta.LegacyToolsFile
	}
	cfg.Model = meta.Model
	if meta.InstructionFile != "" {
		cfg.InstructionFile = meta.InstructionFile
		if !filepath.IsAbs(cfg.InstructionFile) {
			cfg.InstructionFile = filepath.Join(configDir, cfg.InstructionFile)
		}
	}
	if meta.SkillDir != "" {
		cfg.SkillDir = meta.SkillDir
		if !filepath.IsAbs(cfg.SkillDir) {
			cfg.SkillDir = filepath.Join(configDir, cfg.SkillDir)
		}
	}
	if meta.ToolsFile != "" {
		cfg.ToolsFile = meta.ToolsFile
		if !filepath.IsAbs(cfg.ToolsFile) {
			cfg.ToolsFile = filepath.Join(configDir, cfg.ToolsFile)
		}
	}
	return cfg
}

// ResolvedSystemPrompt returns the resolved instruction text.
func (a *AgentRef) ResolvedSystemPrompt() string {
	return a.Instruction.Resolve()
}

// InstructionRef contains inline instructions or an instruction file path.
type InstructionRef struct {
	Value string
	File  string
}

// Resolve reads the instruction file, or returns the inline value.
func (r *InstructionRef) Resolve() string {
	if r.File != "" {
		data, err := os.ReadFile(r.File)
		if err == nil {
			return string(data)
		}
	}
	return r.Value
}

// IsEmpty reports whether no instruction has been resolved.
func (r *InstructionRef) IsEmpty() bool {
	return r.Value == "" && r.File == ""
}

// DatasetRef identifies a local file or a versioned registered dataset.
type DatasetRef struct {
	Name     string
	Version  string
	LocalURI string
}

// IsLocal reports whether the reference points to a local dataset file.
func (d *DatasetRef) IsLocal() bool {
	return d != nil && d.Name == "" && d.LocalURI != ""
}

// IsRemote reports whether the reference points to a registered dataset.
func (d *DatasetRef) IsRemote() bool {
	return d != nil && d.Name != ""
}

// OptimizationConfig holds resolved per-attribute optimization settings.
type OptimizationConfig map[string]json.RawMessage

// Options holds resolved optimizer options.
type Options struct {
	EvalModel              string
	OptimizationConfig     OptimizationConfig
	MaxCandidates          *int
	OptimizationModel      string
	EvaluationLevel        string
	MaxStalls              *int
	MaxConcurrentAgentRuns *int
}
