// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"go.yaml.in/yaml/v3"
)

// Conventional locations. Both are relative to the working directory and are
// used verbatim — never re-rooted under the agent or project directory.
const (
	DefaultEvalDir        = "evals"
	DefaultGenerateConfig = "evals/eval_generate.yaml"
	DefaultDeployConfig   = "evals/azure.yaml"
	DefaultDatasetsDir    = "datasets"
	DefaultEvaluatorsDir  = "evaluators"
)

// GenerateConfig is the generation spec — input to `azd ai eval generate`. It
// is never deployed.
type GenerateConfig struct {
	Agent    AgentSpec    `yaml:"agent"    json:"agent"`
	Generate GenerateSpec `yaml:"generate" json:"generate"`
}

// AgentSpec identifies the agent and the context the generator reads.
type AgentSpec struct {
	Name    string       `yaml:"name"              json:"name"`
	Context AgentContext `yaml:"context,omitempty" json:"context,omitempty"`
}

// AgentContext points at the material used to synthesize a rubric and dataset.
type AgentContext struct {
	Instructions string     `yaml:"instructions,omitempty" json:"instructions,omitempty"`
	Tools        string     `yaml:"tools,omitempty"        json:"tools,omitempty"`
	Traces       *TraceSpec `yaml:"traces,omitempty"       json:"traces,omitempty"`
}

// TraceSpec seeds rubric generation from recent traces. Traces are a generation
// input only; they cannot be a run's data source.
type TraceSpec struct {
	Source string `yaml:"source,omitempty" json:"source,omitempty"`
	Window string `yaml:"window,omitempty" json:"window,omitempty"`
	Sample int    `yaml:"sample,omitempty" json:"sample,omitempty"`
}

// GenerateSpec configures what gets produced.
type GenerateSpec struct {
	Rubric  *RubricSpec  `yaml:"rubric,omitempty"  json:"rubric,omitempty"`
	Dataset *DatasetSpec `yaml:"dataset,omitempty" json:"dataset,omitempty"`
}

// RubricSpec configures rubric (LLM-graded evaluator) generation.
type RubricSpec struct {
	Name     string `yaml:"name"                json:"name"`
	Model    string `yaml:"model,omitempty"     json:"model,omitempty"`
	LocalDir string `yaml:"local_dir,omitempty" json:"local_dir,omitempty"`
}

// DatasetSpec configures synthetic dataset generation.
type DatasetSpec struct {
	Name       string `yaml:"name"                 json:"name"`
	Strategy   string `yaml:"strategy,omitempty"   json:"strategy,omitempty"`
	SampleSize int    `yaml:"sampleSize,omitempty" json:"sampleSize,omitempty"`
	LocalDir   string `yaml:"local_dir,omitempty"  json:"local_dir,omitempty"`
}

// Generation strategies.
const (
	StrategySynthetic  = "synthetic"
	StrategyFromTraces = "from-traces"
)

// Sample-count bounds enforced by the generation service.
const (
	MinSampleSize     = 15
	MaxSampleSize     = 1000
	DefaultSampleSize = 15
)

// LoadGenerateConfig reads a generation spec from disk.
func LoadGenerateConfig(path string) (*GenerateConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading generation config %q: %w", path, err)
	}

	var cfg GenerateConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing generation config %q: %w", path, err)
	}
	return &cfg, nil
}

// Validate reports configuration errors before any generation job is submitted.
func (c *GenerateConfig) Validate() error {
	if c.Agent.Name == "" {
		return fmt.Errorf("agent.name is required")
	}
	if c.Generate.Rubric == nil && c.Generate.Dataset == nil {
		return fmt.Errorf("generate must declare a rubric, a dataset, or both")
	}
	if r := c.Generate.Rubric; r != nil && r.Name == "" {
		return fmt.Errorf("generate.rubric.name is required")
	}
	if d := c.Generate.Dataset; d != nil {
		if d.Name == "" {
			return fmt.Errorf("generate.dataset.name is required")
		}
		switch d.Strategy {
		case "", StrategySynthetic, StrategyFromTraces:
		default:
			return fmt.Errorf(
				"generate.dataset.strategy %q is invalid; expected %q or %q",
				d.Strategy, StrategySynthetic, StrategyFromTraces)
		}
		if d.SampleSize != 0 && (d.SampleSize < MinSampleSize || d.SampleSize > MaxSampleSize) {
			return fmt.Errorf(
				"generate.dataset.sampleSize must be between %d and %d, got %d",
				MinSampleSize, MaxSampleSize, d.SampleSize)
		}
	}
	return nil
}

// ArtifactPath resolves a local_dir value against baseDir. The value may be a
// directory, in which case the file name is derived from resourceName and ext,
// or an explicit file path, which is used as-is.
func ArtifactPath(baseDir, localDir, resourceName, ext string) string {
	if localDir == "" {
		return filepath.Join(baseDir, resourceName+ext)
	}
	candidate := localDir
	if !filepath.IsAbs(candidate) {
		candidate = filepath.Join(baseDir, candidate)
	}
	if looksLikeFile(localDir, ext) {
		return candidate
	}
	return filepath.Join(candidate, resourceName+ext)
}

// looksLikeFile treats a trailing recognised extension as an explicit file path.
func looksLikeFile(p, ext string) bool {
	got := strings.ToLower(filepath.Ext(p))
	if got == "" {
		return false
	}
	if got == strings.ToLower(ext) {
		return true
	}
	switch got {
	case ".json", ".jsonl", ".yaml", ".yml":
		return true
	}
	return false
}
