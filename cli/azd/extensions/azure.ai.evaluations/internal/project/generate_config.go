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

// Conventional locations. All are relative to the working directory and are
// used verbatim — never re-rooted under the agent or project directory.
const (
	DefaultEvalDir        = "evals"
	DefaultGenerateConfig = "evals/generate.yaml"
	DefaultDatasetsDir    = "datasets"
	DefaultEvaluatorsDir  = "evaluators"
)

// EvalConfigPath is where the body of the eval named by a service entry lives.
func EvalConfigPath(evalDir, evalName string) string {
	return filepath.Join(evalDir, evalName+".yaml")
}

// GenerateConfig says how the local dataset and evaluator artifacts referenced
// by an eval are produced. It is never deployed.
//
//	generationModel: gpt-5.6-luna
//	dataset:
//	  support-agent-smoke:
//	    sampleSize: 15
//	    outputDir: ./datasets
//	evaluator:
//	  support-quality:
//	    outputDir: ./evaluators
//	    deriveFrom: support-agent
//
// The maps are keyed by artifact name so `dataset generate <name>` and
// `evaluator generate <name>` each look up exactly the entry they were asked
// for, and generating one artifact never reads the other's settings.
type GenerateConfig struct {
	GenerationModel string                      `yaml:"generationModel,omitempty" json:"generationModel,omitempty"`
	Dataset         map[string]DatasetGenSpec   `yaml:"dataset,omitempty"         json:"dataset,omitempty"`
	Evaluator       map[string]EvaluatorGenSpec `yaml:"evaluator,omitempty"       json:"evaluator,omitempty"`
}

// DatasetGenSpec configures synthetic dataset generation for one dataset.
type DatasetGenSpec struct {
	SampleSize int    `yaml:"sampleSize,omitempty" json:"sampleSize,omitempty"`
	OutputDir  string `yaml:"outputDir,omitempty"  json:"outputDir,omitempty"`
	// DeriveFrom names the agent whose context seeds generation. Optional: the
	// eval's target supplies it, and --target overrides both.
	DeriveFrom string `yaml:"deriveFrom,omitempty" json:"deriveFrom,omitempty"`
	// Instructions points at a local file whose contents stand in for the
	// agent's published instructions for this generation only.
	Instructions string `yaml:"instructions,omitempty" json:"instructions,omitempty"`
	// TraceDays seeds generation from that many days of recent traces. Zero
	// disables it. Traces are a generation input only; they cannot be a run's
	// data source.
	TraceDays int `yaml:"traceDays,omitempty" json:"traceDays,omitempty"`
}

// EvaluatorGenSpec configures rubric generation for one evaluator.
type EvaluatorGenSpec struct {
	OutputDir string `yaml:"outputDir,omitempty" json:"outputDir,omitempty"`
	// DeriveFrom names the agent the rubric is written against.
	DeriveFrom string `yaml:"deriveFrom,omitempty" json:"deriveFrom,omitempty"`
	// Instructions points at a local file whose contents stand in for the
	// agent's published instructions for this generation only.
	Instructions string `yaml:"instructions,omitempty" json:"instructions,omitempty"`
	// TraceDays seeds generation from that many days of recent traces. Zero
	// disables it.
	TraceDays int `yaml:"traceDays,omitempty" json:"traceDays,omitempty"`
}

// ArtifactRef is the name/source pair a generation run produces, so the
// command can tell the developer how to reference it.
//
// Generation writes artifacts only and never edits azure.yaml or the eval
// config: `init` declares the paths and `generate` fills them in, which is what
// keeps a generation run a data-file-only diff.
type ArtifactRef struct {
	Name   string `json:"name"`
	Source string `json:"source"`
}

// Sample-count bounds enforced by the generation service.
const (
	MinSampleSize     = 15
	MaxSampleSize     = 1000
	DefaultSampleSize = 15
)

// LoadGenerateConfig reads a generation spec from disk.
//
// A missing file is not an error. Generation is optional — a developer with
// hand-authored data and evaluators never writes one — and every setting it
// carries can be given on the command line instead.
func LoadGenerateConfig(path string) (*GenerateConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &GenerateConfig{}, nil
		}
		return nil, fmt.Errorf("reading generation config %q: %w", path, err)
	}

	var cfg GenerateConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing generation config %q: %w", path, err)
	}
	return &cfg, nil
}

// DatasetSpec returns the settings for one dataset, and whether the config
// declared them.
func (c *GenerateConfig) DatasetSpec(name string) (DatasetGenSpec, bool) {
	spec, ok := c.Dataset[name]
	return spec, ok
}

// EvaluatorSpec returns the settings for one evaluator, and whether the config
// declared them.
func (c *GenerateConfig) EvaluatorSpec(name string) (EvaluatorGenSpec, bool) {
	spec, ok := c.Evaluator[name]
	return spec, ok
}

// ValidateSampleSize rejects a row count the service would reject, before a
// generation job is submitted and billed.
func ValidateSampleSize(n int) error {
	if n != 0 && (n < MinSampleSize || n > MaxSampleSize) {
		return fmt.Errorf(
			"sample size must be between %d and %d, got %d",
			MinSampleSize, MaxSampleSize, n)
	}
	return nil
}

// ArtifactPath resolves an outputDir value against baseDir. The value may be a
// directory, in which case the file name is derived from resourceName and ext,
// or an explicit file path, which is used as-is.
func ArtifactPath(baseDir, outputDir, resourceName, ext string) string {
	if outputDir == "" {
		return filepath.Join(baseDir, resourceName+ext)
	}
	candidate := outputDir
	if !filepath.IsAbs(candidate) {
		candidate = filepath.Join(baseDir, candidate)
	}
	if looksLikeFile(outputDir, ext) {
		return candidate
	}
	return filepath.Join(candidate, resourceName+ext)
}

// looksLikeFile treats a trailing recognized extension as an explicit file path.
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
