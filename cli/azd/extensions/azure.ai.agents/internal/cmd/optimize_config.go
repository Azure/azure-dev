// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"

	"azureaiagent/internal/pkg/agents/opteval"
	"azureaiagent/internal/pkg/agents/optimize_api"

	"go.yaml.in/yaml/v3"
)

// OptimizeConfig extends the shared Config with optimize-specific fields.
type OptimizeConfig struct {
	opteval.Config `yaml:",inline"`

	// Optimize-specific YAML fields.
	ValidationReference *opteval.DatasetRef        `yaml:"validation_reference,omitempty"`
	Criteria            []OptimizeConfigCriterion  `yaml:"criteria,omitempty"`
	Options             *opteval.Options           `yaml:"options"`
	InlineDataset       []optimize_api.DatasetTask `yaml:"-"` // populated by defaultOptimizeConfig, not from YAML
}

// OptimizeConfigCriterion is a named evaluation criterion with a natural-language instruction.
type OptimizeConfigCriterion struct {
	Name        string `yaml:"name"`
	Instruction string `yaml:"instruction"`
}

// LoadOptimizeConfig reads and parses a YAML optimization config file.
func LoadOptimizeConfig(path string) (*OptimizeConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file %s: %w", path, err)
	}

	var cfg OptimizeConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse config file %s: %w", path, err)
	}

	return &cfg, nil
}

// Validate checks required fields and mutual exclusivity constraints.
func (c *OptimizeConfig) Validate() error {
	if c.Agent.Name == "" {
		return fmt.Errorf("agent.name is required")
	}

	if c.Options == nil || c.Options.EvalModel == "" {
		return fmt.Errorf("options.eval_model is required")
	}

	hasFile := c.DatasetFile != ""
	hasRef := c.DatasetReference != nil
	hasInline := len(c.InlineDataset) > 0

	if hasFile && hasRef {
		return fmt.Errorf("dataset_file and dataset_reference are mutually exclusive; specify one, not both")
	}

	if !hasFile && !hasRef && !hasInline {
		return fmt.Errorf("one of dataset_file or dataset_reference is required")
	}

	return nil
}

// defaultOptimizeConfig returns a config with sensible defaults and a built-in
// evaluation dataset.
func defaultOptimizeConfig(agentName string) *OptimizeConfig {
	return &OptimizeConfig{
		Config: opteval.Config{
			Agent:      opteval.AgentRef{Name: agentName},
			Evaluators: []string{"task_adherence"},
		},
		InlineDataset: defaultDataset,
		Options: &opteval.Options{
			EvalModel:        "gpt-4o",
			Mode:             "optimize",
			TargetAttributes: []string{"instruction", "skill", "agents-optimization-job"},
			Budget:           5,
		},
	}
}

var defaultDataset = []optimize_api.DatasetTask{
	{
		Name:   "calculator_module",
		Prompt: "Create a Python module calc.py with four functions: add, subtract, multiply, divide. Each takes two numbers and returns the result. Include a brief test at the bottom (if __name__ == '__main__') that exercises each function and prints the results. Then run it.",
		Criteria: []optimize_api.Criterion{
			{Name: "decimal_types", Instruction: "ALL functions MUST use and return Python's decimal.Decimal type, NOT float."},
			{Name: "error_code_prefix", Instruction: "ALL error messages raised by any function MUST include a bracketed error code prefix [CALC-NNN]."},
			{Name: "version_constant", Instruction: "The module MUST define VERSION = '0.1.0' and __version__ = VERSION near the top."},
			{Name: "module_exports", Instruction: "The module MUST define __all__ = ['add', 'subtract', 'multiply', 'divide'] at the top."},
		},
	},
	{
		Name:   "csv_report",
		Prompt: "Create a Python script report.py that generates a CSV file 'sales_report.csv' with 10 rows of sample sales data. Columns: date, product, quantity, unit_price, total. Then read the CSV back and print a summary: total revenue and the top-selling product by quantity. Run the script.",
		Criteria: []optimize_api.Criterion{
			{Name: "pipe_delimiter", Instruction: "The CSV file MUST use pipe '|' as the delimiter, NOT comma."},
			{Name: "zero_padded_quantity", Instruction: "ALL quantity values MUST be zero-padded to exactly 4 digits (e.g. '0042' not '42')."},
			{Name: "logging_not_print", Instruction: "The script MUST use Python's logging module for progress messages, NOT print()."},
			{Name: "summary_footer", Instruction: "The LAST line of the CSV file MUST be a comment starting with '# SUMMARY:' including total revenue."},
		},
	},
	{
		Name:   "api_response_builder",
		Prompt: "Create a Python module api_utils.py with a function build_response(data, status_code=200) that builds a JSON-ready dictionary representing an API response. Also create a function validate_email(email: str) -> bool that checks if an email is roughly valid. Write a test block that demonstrates both functions with a few examples and prints the JSON output. Run it.",
		Criteria: []optimize_api.Criterion{
			{Name: "named_tuple_validation", Instruction: "validate_email() MUST return a typing.NamedTuple with fields (is_valid: bool, reason: str), NOT a bare bool."},
			{Name: "request_id", Instruction: "build_response() MUST include a 'requestId' field containing a UUID4 string."},
			{Name: "rfc7807_errors", Instruction: "When status_code >= 400, the response MUST follow RFC 7807 with 'type', 'title', 'detail', 'status' keys."},
			{Name: "camel_case_keys", Instruction: "ALL dictionary keys in the response MUST be camelCase (e.g. 'statusCode', NOT 'status_code')."},
		},
	},
}

// ToRequest converts the YAML config into an API OptimizeRequest.
// If DatasetFile is set, each line of the file is read as a JSON-encoded DatasetTask.
func (c *OptimizeConfig) ToRequest(projectEndpoint string) (*optimize_api.OptimizeRequest, error) {
	req := &optimize_api.OptimizeRequest{
		Agent: optimize_api.AgentDefinition{
			FoundryProjectURL: projectEndpoint,
			AgentName:         c.Agent.Name,
			AgentVersion:      c.Agent.Version,
			Model:             c.Agent.Model,
		},
		Evaluators: c.Evaluators,
		Options: optimize_api.OptimizeOptions{
			EvalModel:            c.Options.EvalModel,
			Budget:               c.Options.Budget,
			MaxIterations:        c.Options.MaxIterations,
			MinImprovement:       c.Options.MinImprovement,
			ImprovementThreshold: c.Options.ImprovementThreshold,
			PassThreshold:        c.Options.PassThreshold,
			Strategies:           c.Options.TargetAttributes,
			TargetAttributes:     c.Options.TargetAttributes,
			KeepVersions:         c.Options.KeepVersions,
			TasksPerIteration:    c.Options.TasksPerIteration,
			ReflectionModel:      c.Options.ReflectionModel,
			Mode:                 c.Options.Mode,
		},
	}

	// Map criteria from config schema to API schema.
	for _, crit := range c.Criteria {
		req.Criteria = append(req.Criteria, optimize_api.Criterion{
			Name:        crit.Name,
			Instruction: crit.Instruction,
		})
	}

	if c.DatasetReference != nil {
		req.TrainDatasetReference = &optimize_api.DatasetReference{
			Name:    c.DatasetReference.Name,
			Version: c.DatasetReference.Version,
		}
	}

	if c.ValidationReference != nil {
		req.ValidationDatasetReference = &optimize_api.DatasetReference{
			Name:    c.ValidationReference.Name,
			Version: c.ValidationReference.Version,
		}
	}

	if c.DatasetFile != "" {
		tasks, err := loadDatasetFile(c.DatasetFile)
		if err != nil {
			return nil, err
		}
		req.Dataset = tasks
	} else if len(c.InlineDataset) > 0 {
		req.Dataset = c.InlineDataset
	}

	return req, nil
}

// loadDatasetFile reads a JSONL file where each line is a JSON DatasetTask.
func loadDatasetFile(path string) ([]optimize_api.DatasetTask, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("failed to open dataset file %s: %w", path, err)
	}
	defer f.Close()

	var tasks []optimize_api.DatasetTask
	scanner := bufio.NewScanner(f)
	lineNum := 0
	for scanner.Scan() {
		lineNum++
		line := scanner.Text()
		if line == "" {
			continue
		}
		var task optimize_api.DatasetTask
		if err := json.Unmarshal([]byte(line), &task); err != nil {
			return nil, fmt.Errorf("failed to parse dataset line %d: %w", lineNum, err)
		}
		tasks = append(tasks, task)
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("error reading dataset file %s: %w", path, err)
	}

	if len(tasks) == 0 {
		return nil, fmt.Errorf("dataset file %s contains no tasks", path)
	}

	return tasks, nil
}
