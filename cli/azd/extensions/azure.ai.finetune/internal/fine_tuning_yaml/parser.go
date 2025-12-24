// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package fine_tuning_yaml

import (
	"fmt"
	"os"

	"github.com/braydonk/yaml"
)

// ParseFineTuningConfig reads and parses a YAML fine-tuning configuration file
func ParseFineTuningConfig(filePath string) (*FineTuningConfig, error) {
	// Read the YAML file
	yamlFile, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file %s: %w", filePath, err)
	}

	// Parse YAML into config struct
	var config FineTuningConfig
	if err := yaml.Unmarshal(yamlFile, &config); err != nil {
		return nil, fmt.Errorf("failed to parse YAML config: %w", err)
	}

	// Validate the configuration
	if err := config.Validate(); err != nil {
		return nil, fmt.Errorf("invalid configuration: %w", err)
	}

	return &config, nil
}

// Validate checks if the configuration is valid
func (c *FineTuningConfig) Validate() error {
	// Validate required fields
	if c.Model == "" {
		return fmt.Errorf("model is required")
	}

	if c.TrainingFile == "" {
		return fmt.Errorf("training_file is required")
	}

	// Validate method if provided
	if c.Method.Type != "" {
		if c.Method.Type != string(Supervised) && c.Method.Type != string(DPO) && c.Method.Type != string(Reinforcement) {
			return fmt.Errorf("invalid method type: %s (must be 'supervised', 'dpo', or 'reinforcement')", c.Method.Type)
		}

		// Validate method-specific configuration
		switch c.Method.Type {
		case string(Supervised):
			if c.Method.Supervised == nil {
				return fmt.Errorf("supervised method requires 'supervised' configuration block")
			}
		case string(DPO):
			if c.Method.DPO == nil {
				return fmt.Errorf("dpo method requires 'dpo' configuration block")
			}
		case string(Reinforcement):
			if c.Method.Reinforcement == nil {
				return fmt.Errorf("reinforcement method requires 'reinforcement' configuration block")
			}
			// Validate reinforcement-specific configuration
			if err := c.Method.Reinforcement.Validate(); err != nil {
				return err
			}
		}
	}

	// Validate suffix length if provided
	if c.Suffix != nil && len(*c.Suffix) > 64 {
		return fmt.Errorf("suffix exceeds maximum length of 64 characters: %d", len(*c.Suffix))
	}

	// Validate metadata constraints
	if c.Metadata != nil {
		if len(c.Metadata) > 16 {
			return fmt.Errorf("metadata exceeds maximum of 16 key-value pairs: %d", len(c.Metadata))
		}
		for k, v := range c.Metadata {
			if len(k) > 64 {
				return fmt.Errorf("metadata key exceeds maximum length of 64 characters: %s", k)
			}
			if len(v) > 512 {
				return fmt.Errorf("metadata value exceeds maximum length of 512 characters for key: %s", k)
			}
		}
	}

	return nil
}

// Validate checks if reinforcement configuration is valid
func (r *ReinforcementConfig) Validate() error {
	if r == nil {
		return nil
	}

	// Validate grader configuration
	if r.Grader.Type != "" {
		if err := r.Grader.Validate(); err != nil {
			return err
		}
	}

	return nil
}

// Validate checks if grader configuration is valid
func (g *GraderConfig) Validate() error {
	if g.Type == "" {
		return nil // grader is optional
	}

	validGraderTypes := map[string]bool{
		"string_check":    true,
		"text_similarity": true,
		"python":          true,
		"score_model":     true,
		"multi":           true,
	}

	if !validGraderTypes[g.Type] {
		return fmt.Errorf("invalid grader type: %s (must be 'string_check', 'text_similarity', 'python', 'score_model', or 'multi')", g.Type)
	}

	switch g.Type {
	case "string_check":
		if g.StringCheck == nil {
			return fmt.Errorf("string_check grader type requires 'string_check' configuration block")
		}
		if err := g.StringCheck.Validate(); err != nil {
			return err
		}

	case "text_similarity":
		if g.TextSimilarity == nil {
			return fmt.Errorf("text_similarity grader type requires 'text_similarity' configuration block")
		}
		if err := g.TextSimilarity.Validate(); err != nil {
			return err
		}

	case "python":
		if g.Python == nil {
			return fmt.Errorf("python grader type requires 'python' configuration block")
		}
		if err := g.Python.Validate(); err != nil {
			return err
		}

	case "score_model":
		if g.ScoreModel == nil {
			return fmt.Errorf("score_model grader type requires 'score_model' configuration block")
		}
		if err := g.ScoreModel.Validate(); err != nil {
			return err
		}

	case "multi":
		if g.Multi == nil {
			return fmt.Errorf("multi grader type requires 'multi' configuration block")
		}
		if err := g.Multi.Validate(); err != nil {
			return err
		}
	}

	return nil
}

// Validate checks if string check grader configuration is valid
func (s *StringCheckGraderConfig) Validate() error {
	if s.Type == "" {
		s.Type = "string_check" // set default
	}

	if s.Type != "string_check" {
		return fmt.Errorf("string_check grader type must be 'string_check', got: %s", s.Type)
	}

	if s.Input == "" {
		return fmt.Errorf("string_check grader requires 'input' field")
	}

	if s.Name == "" {
		return fmt.Errorf("string_check grader requires 'name' field")
	}

	if s.Operation == "" {
		return fmt.Errorf("string_check grader requires 'operation' field")
	}

	validOperations := map[string]bool{"eq": true, "contains": true, "regex": true}
	if !validOperations[s.Operation] {
		return fmt.Errorf("invalid string_check operation: %s (must be 'eq', 'contains', or 'regex')", s.Operation)
	}

	if s.Reference == "" {
		return fmt.Errorf("string_check grader requires 'reference' field")
	}

	return nil
}

// Validate checks if text similarity grader configuration is valid
func (t *TextSimilarityGraderConfig) Validate() error {
	if t.Type == "" {
		t.Type = "text_similarity" // set default
	}

	if t.Type != "text_similarity" {
		return fmt.Errorf("text_similarity grader type must be 'text_similarity', got: %s", t.Type)
	}

	if t.Name == "" {
		return fmt.Errorf("text_similarity grader requires 'name' field")
	}

	if t.Input == "" {
		return fmt.Errorf("text_similarity grader requires 'input' field")
	}

	if t.Reference == "" {
		return fmt.Errorf("text_similarity grader requires 'reference' field")
	}

	if t.EvaluationMetric == "" {
		return fmt.Errorf("text_similarity grader requires 'evaluation_metric' field")
	}

	validMetrics := map[string]bool{
		"cosine":      true,
		"fuzzy_match": true,
		"bleu":        true,
		"gleu":        true,
		"meteor":      true,
		"rouge_1":     true,
		"rouge_2":     true,
		"rouge_3":     true,
		"rouge_4":     true,
		"rouge_5":     true,
		"rouge_l":     true,
	}
	if !validMetrics[t.EvaluationMetric] {
		return fmt.Errorf("invalid evaluation_metric: %s", t.EvaluationMetric)
	}

	return nil
}

// Validate checks if python grader configuration is valid
func (p *PythonGraderConfig) Validate() error {
	if p.Type == "" {
		p.Type = "python" // set default
	}

	if p.Type != "python" {
		return fmt.Errorf("python grader type must be 'python', got: %s", p.Type)
	}

	if p.Name == "" {
		return fmt.Errorf("python grader requires 'name' field")
	}

	if p.Source == "" {
		return fmt.Errorf("python grader requires 'source' field")
	}

	return nil
}

// Validate checks if score model grader configuration is valid
func (s *ScoreModelGraderConfig) Validate() error {
	if s.Type == "" {
		s.Type = "score_model" // set default
	}

	if s.Type != "score_model" {
		return fmt.Errorf("score_model grader type must be 'score_model', got: %s", s.Type)
	}

	if s.Name == "" {
		return fmt.Errorf("score_model grader requires 'name' field")
	}

	if s.Model == "" {
		return fmt.Errorf("score_model grader requires 'model' field")
	}

	if len(s.Input) == 0 {
		return fmt.Errorf("score_model grader requires 'input' field with at least one message")
	}

	// Validate each message input
	for i, msgInput := range s.Input {
		if msgInput.Role == "" {
			return fmt.Errorf("score_model grader input[%d] requires 'role' field", i)
		}

		validRoles := map[string]bool{"user": true, "assistant": true, "system": true, "developer": true}
		if !validRoles[msgInput.Role] {
			return fmt.Errorf("score_model grader input[%d] has invalid role: %s (must be 'user', 'assistant', 'system', or 'developer')", i, msgInput.Role)
		}

		if len(msgInput.Content) == 0 {
			return fmt.Errorf("score_model grader input[%d] requires at least one content item", i)
		}

		// Validate each content item
		for j, content := range msgInput.Content {
			if content.Type == "" {
				return fmt.Errorf("score_model grader input[%d].content[%d] requires 'type' field", i, j)
			}

			validContentTypes := map[string]bool{"text": true, "image": true, "audio": true}
			if !validContentTypes[content.Type] {
				return fmt.Errorf("score_model grader input[%d].content[%d] has invalid type: %s (must be 'text', 'image', or 'audio')", i, j, content.Type)
			}
		}
	}

	// Validate sampling parameters if provided
	if s.SamplingParams != nil {
		if s.SamplingParams.ReasoningEffort != "" {
			validEfforts := map[string]bool{
				"none":    true,
				"minimal": true,
				"low":     true,
				"medium":  true,
				"high":    true,
				"xhigh":   true,
			}
			if !validEfforts[s.SamplingParams.ReasoningEffort] {
				return fmt.Errorf("invalid reasoning_effort: %s", s.SamplingParams.ReasoningEffort)
			}
		}
	}

	return nil
}

// Validate checks if multi grader configuration is valid
func (m *MultiGraderConfig) Validate() error {
	if m.Type == "" {
		m.Type = "multi" // set default
	}

	if m.Type != "multi" {
		return fmt.Errorf("multi grader type must be 'multi', got: %s", m.Type)
	}

	if len(m.Graders) == 0 {
		return fmt.Errorf("multi grader requires at least one grader in 'graders' field")
	}

	if m.Aggregation == "" {
		return fmt.Errorf("multi grader requires 'aggregation' field")
	}

	validAggregations := map[string]bool{"average": true, "weighted": true, "min": true, "max": true}
	if !validAggregations[m.Aggregation] {
		return fmt.Errorf("invalid aggregation method: %s (must be 'average', 'weighted', 'min', or 'max')", m.Aggregation)
	}

	// Validate weights if weighted aggregation
	if m.Aggregation == "weighted" {
		if len(m.Weights) == 0 {
			return fmt.Errorf("weighted aggregation requires 'weights' field")
		}
		if len(m.Weights) != len(m.Graders) {
			return fmt.Errorf("number of weights (%d) must match number of graders (%d)", len(m.Weights), len(m.Graders))
		}
	}

	return nil
}

// GetMethodType returns the method type as MethodType constant
func (c *FineTuningConfig) GetMethodType() MethodType {
	switch c.Method.Type {
	case string(Supervised):
		return Supervised
	case string(DPO):
		return DPO
	case string(Reinforcement):
		return Reinforcement
	default:
		return Supervised // default to supervised
	}
}

// Example YAML structure:
/*
# Minimal configuration
model: gpt-4o-mini
training_file: "local:/path/to/training.jsonl"

---

# Supervised fine-tuning
model: gpt-4o-mini
training_file: "local:/path/to/training.jsonl"
validation_file: "local:/path/to/validation.jsonl"

suffix: "supervised-model"
seed: 42

method:
  type: supervised
  supervised:
    hyperparameters:
      epochs: 3
      batch_size: 8
      learning_rate_multiplier: 1.0

metadata:
  project: "my-project"
  team: "data-science"

---

# DPO (Direct Preference Optimization)
model: gpt-4o-mini
training_file: "local:/path/to/training.jsonl"

method:
  type: dpo
  dpo:
    hyperparameters:
      epochs: 2
      batch_size: 16
      learning_rate_multiplier: 0.5
      beta: 0.1

---

# Reinforcement learning with string check grader
model: gpt-4o-mini
training_file: "local:/path/to/training.jsonl"

method:
  type: reinforcement
  reinforcement:
    grader:
      type: string_check
      string_check:
        type: string_check
        input: "{{ item.output }}"
        name: "exact_match_grader"
        operation: "eq"
        reference: "{{ item.expected }}"
    hyperparameters:
      epochs: 3
      batch_size: 8
      eval_interval: 10
      eval_samples: 5

---

# Reinforcement learning with text similarity grader
model: gpt-4o-mini
training_file: "local:/path/to/training.jsonl"

method:
  type: reinforcement
  reinforcement:
    grader:
      type: text_similarity
      text_similarity:
        type: text_similarity
        name: "similarity_grader"
        input: "{{ item.output }}"
        reference: "{{ item.reference }}"
        evaluation_metric: "rouge_l"
    hyperparameters:
      epochs: 2
      compute_multiplier: auto
      reasoning_effort: "medium"

---

# Reinforcement learning with python grader
model: gpt-4o-mini
training_file: "local:/path/to/training.jsonl"

method:
  type: reinforcement
  reinforcement:
    grader:
      type: python
      python:
        type: python
        name: "custom_evaluator"
        source: |
          def evaluate(output, expected):
              return 1.0 if output == expected else 0.0
        image_tag: "python:3.11"
    hyperparameters:
      epochs: 3
      batch_size: 8

---

# Reinforcement learning with score model grader
model: gpt-4o-mini
training_file: "local:/path/to/training.jsonl"

method:
  type: reinforcement
  reinforcement:
    grader:
      type: score_model
      score_model:
        type: score_model
        name: "gpt_evaluator"
        model: "gpt-4o"
        input:
          - role: "user"
            type: "message"
            content:
              - type: "text"
                text: "Rate this response: {{ item.output }}"
          - role: "assistant"
            type: "message"
            content:
              - type: "text"
                text: "Expected: {{ item.expected }}"
        range: [0, 10]
        sampling_params:
          max_completions_tokens: 50
          reasoning_effort: "medium"
    hyperparameters:
      epochs: 2
      eval_interval: 5

---

# Reinforcement learning with multi grader (combining multiple evaluators)
model: gpt-4o-mini
training_file: "local:/path/to/training.jsonl"

method:
  type: reinforcement
  reinforcement:
    grader:
      type: multi
      multi:
        type: multi
        graders:
          - type: string_check
            input: "{{ item.output }}"
            name: "exact_match"
            operation: "eq"
            reference: "{{ item.expected }}"
          - type: text_similarity
            name: "semantic_similarity"
            input: "{{ item.output }}"
            reference: "{{ item.expected }}"
            evaluation_metric: "rouge_l"
        aggregation: "weighted"
        weights: [0.4, 0.6]
    hyperparameters:
      epochs: 3
      batch_size: 8
      compute_multiplier: auto
*/
