// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// optimize_config.go defines OptimizeConfig (the YAML config structure for
// optimization jobs), provides loading/validation, and converts configs into
// API requests. It also handles reading skills from disk and parsing YAML
// frontmatter in skill files.

package cmd

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"

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

	// Runtime-only: resolved skill directory and tools file (not serialized to YAML).
	SkillDir  string `yaml:"-"`
	ToolsFile string `yaml:"-"`
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
			Evaluators: opteval.EvaluatorList{{Name: "builtin.task_adherence"}},
		},
		InlineDataset: defaultDataset,
		Options: &opteval.Options{
			EvalModel:        "gpt-4o",
			Mode:             "optimize",
			TargetAttributes: []string{"instruction", "skill"},
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
			SystemPrompt:      c.Agent.ResolvedSystemPrompt(),
		},
		Evaluators: c.Evaluators.Names(),
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

	// Map target_config from YAML to API format.
	if c.Options.TargetConfig != nil {
		req.Options.TargetConfig = &optimize_api.TargetConfig{
			Model: c.Options.TargetConfig.Model,
		}
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
		tasks, err := loadJSONLFile[optimize_api.DatasetTask](c.DatasetFile)
		if err != nil {
			return nil, err
		}
		req.Dataset = tasks
	} else if len(c.InlineDataset) > 0 {
		req.Dataset = c.InlineDataset
	}

	// Load skills from skill_dir if specified.
	if c.SkillDir != "" {
		skills, err := loadSkillsFromDir(c.SkillDir)
		if err != nil {
			return nil, fmt.Errorf("loading skills from %s: %w", c.SkillDir, err)
		}
		req.Agent.Skills = skills
	}

	// Load tool definitions if a tools file is specified.
	// TODO: re-enable when tools optimization is supported in the service.
	// if c.ToolsFile != "" {
	// 	tools, err := loadToolDefinitions(c.ToolsFile)
	// 	if err != nil {
	// 		return nil, fmt.Errorf("loading tool definitions from %s: %w", c.ToolsFile, err)
	// 	}
	// 	req.Agent.ToolDefinitions = tools
	// }

	return req, nil
}

// loadSkillsFromDir reads skill files from a directory and returns SkillDefinitions.
// For markdown files (.md), YAML frontmatter is parsed to extract name and description;
// the content after the frontmatter becomes the skill body.
// For other files, the filename (without extension) is used as the name and the full
// content as the body.
// Subdirectories are recursed into — each file within is also loaded as a skill.
func loadSkillsFromDir(dir string) ([]optimize_api.SkillDefinition, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("reading skill directory: %w", err)
	}

	var skills []optimize_api.SkillDefinition
	for _, entry := range entries {
		entryPath := filepath.Join(dir, entry.Name())

		if entry.IsDir() {
			subSkills, err := loadSkillsFromDir(entryPath)
			if err != nil {
				return nil, err
			}
			skills = append(skills, subSkills...)
			continue
		}

		data, err := os.ReadFile(entryPath)
		if err != nil {
			return nil, fmt.Errorf("reading skill file %s: %w", entry.Name(), err)
		}

		skill := parseSkillFile(entry.Name(), string(data))
		skills = append(skills, skill)
	}

	return skills, nil
}

// skillFrontmatter represents the YAML frontmatter in a skill markdown file.
type skillFrontmatter struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
}

// parseSkillFile parses a skill file. For .md files it attempts to extract
// YAML frontmatter (delimited by "---") for name and description; the body
// is the content after the frontmatter. For other files, the filename (sans
// extension) is the name and the full content is the body.
func parseSkillFile(filename, content string) optimize_api.SkillDefinition {
	ext := filepath.Ext(filename)
	baseName := strings.TrimSuffix(filename, ext)

	if !strings.EqualFold(ext, ".md") {
		return optimize_api.SkillDefinition{
			Name: baseName,
			Body: content,
		}
	}

	// Try to parse YAML frontmatter from markdown.
	fm, body := splitFrontmatter(content)
	skill := optimize_api.SkillDefinition{
		Name: baseName,
		Body: body,
	}

	if fm != "" {
		var meta skillFrontmatter
		if err := yaml.Unmarshal([]byte(fm), &meta); err == nil {
			if meta.Name != "" {
				skill.Name = meta.Name
			}
			skill.Description = meta.Description
		}
	}

	return skill
}

// splitFrontmatter splits YAML frontmatter (between "---" delimiters) from
// the rest of the content. Returns (frontmatter, body). If no frontmatter is
// found, returns ("", original content).
func splitFrontmatter(content string) (string, string) {
	const delimiter = "---"

	scanner := bufio.NewScanner(strings.NewReader(content))
	if !scanner.Scan() {
		return "", content
	}
	if strings.TrimSpace(scanner.Text()) != delimiter {
		return "", content
	}

	var fmLines []string
	for scanner.Scan() {
		line := scanner.Text()
		if strings.TrimSpace(line) == delimiter {
			// Found closing delimiter — rest is the body.
			var bodyLines []string
			for scanner.Scan() {
				bodyLines = append(bodyLines, scanner.Text())
			}
			body := strings.Join(bodyLines, "\n")
			return strings.Join(fmLines, "\n"), strings.TrimSpace(body)
		}
		fmLines = append(fmLines, line)
	}

	// No closing delimiter found — treat entire content as body.
	return "", content
}
