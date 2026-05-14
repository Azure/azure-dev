// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package eval_api

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"azureaiagent/internal/pkg/agents/opteval"

	"go.yaml.in/yaml/v3"
)

// EvalConfig extends the shared Config with eval-specific fields and helpers.
type EvalConfig struct {
	opteval.Config `yaml:",inline"`

	// Options holds run-time options (eval_model, etc.).
	Options *opteval.Options `yaml:"options,omitempty"`

	// GenerationInstruction is the prompt used to generate adaptive evaluators
	// and synthetic eval datasets.
	GenerationInstruction string `yaml:"generation_instruction,omitempty"`

	// MaxSamples is the maximum number of data samples to generate.
	MaxSamples int `yaml:"max_samples,omitempty"`

	// TraceDays is the number of days of agent traces to include (0 = none).
	TraceDays int `yaml:"trace_days,omitempty"`
}

// LoadEvalConfig reads and parses a YAML eval config file.
func LoadEvalConfig(path string) (*EvalConfig, error) {
	data, err := os.ReadFile(path) //nolint:gosec // path is provided by user for local config
	if err != nil {
		return nil, fmt.Errorf("failed to read eval config %q: %w", path, err)
	}

	var cfg EvalConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse eval config %q: %w", path, err)
	}

	return &cfg, nil
}

// WriteEvalConfig writes the eval config to a YAML file.
func WriteEvalConfig(path string, cfg *EvalConfig) error {
	if err := os.MkdirAll(filepath.Dir(path), 0750); err != nil {
		return fmt.Errorf("creating config directory: %w", err)
	}

	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("failed to marshal eval config: %w", err)
	}

	if err := os.WriteFile(path, data, 0600); err != nil {
		return fmt.Errorf("failed to write eval config %q: %w", path, err)
	}

	return nil
}

// Validate checks required fields for the eval command.
func (c *EvalConfig) Validate() error {
	if c.Agent.Name == "" {
		return fmt.Errorf("agent.name is required")
	}

	hasFile := c.DatasetFile != ""
	hasRef := c.DatasetReference != nil

	if hasFile && hasRef {
		return fmt.Errorf("dataset_file and dataset_reference are mutually exclusive; specify one, not both")
	}

	if !hasFile && !hasRef {
		return fmt.Errorf("one of dataset_file or dataset_reference is required")
	}

	return nil
}

// ToAgentTargetAdaptableEvalGroupRequest builds the request body for creating an OpenAI eval
// with agent target completions and adaptable evaluator schema.
func (c *EvalConfig) ToAgentTargetAdaptableEvalGroupRequest() *CreateOpenAIEvalRequest {
	request := &CreateOpenAIEvalRequest{
		Name: c.Name,
		Metadata: map[string]string{
			"azd_agent":         c.Agent.Name,
			"azd_agent_version": c.Agent.Version,
		},
		DataSourceConfig: &DataSourceConfig{
			Type:                "custom",
			IncludeSampleSchema: true,
			ItemSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"query": map[string]any{"type": "string"},
				},
			},
		},
	}

	// Build testing_criteria from evaluators.
	evalModel := ""
	if c.Options != nil {
		evalModel = c.Options.EvalModel
	}
	for _, evaluator := range c.Evaluators {
		apiName := strings.TrimPrefix(evaluator, "builtin.")
		criterion := TestingCriterion{
			Type:          "azure_ai_evaluator",
			Name:          apiName,
			EvaluatorName: evaluator,
			DataMapping: map[string]string{
				//"messages": "{{item.messages}}",
				"query":            "{{item.query}}",
				"response":         "{{sample.output_items}}",
				"tool_calls":       "{{sample.tool_calls}}",
				"tool_definitions": "{{sample.tool_definitions}}",
			},
		}
		if evalModel != "" {
			criterion.InitializationParameters = map[string]any{
				"model":           evalModel,
				"deployment_name": evalModel,
			}
		}
		request.TestingCriteria = append(request.TestingCriteria, criterion)
	}

	return request
}
