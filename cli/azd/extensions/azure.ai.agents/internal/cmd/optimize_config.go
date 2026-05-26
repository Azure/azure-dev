// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// optimize_config.go defines OptimizeConfig (the YAML config structure for
// optimization jobs), provides loading/validation, and converts configs into
// API requests. It also handles reading skills from disk and parsing YAML
// preamble in skill files.

package cmd

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"azureaiagent/internal/pkg/agents/opt_eval"
	"azureaiagent/internal/pkg/agents/optimize_api"

	"go.yaml.in/yaml/v3"
)

// OptimizeConfig extends the shared Config with optimize-specific fields.
type OptimizeConfig struct {
	opt_eval.Config `yaml:",inline"`

	// Optimize-specific YAML fields.
	ValidationReference *opt_eval.DatasetRef `yaml:"validation_reference,omitempty"`
	Options             *opt_eval.Options    `yaml:"options"`

	// Runtime-only: resolved skill directory and tools file (not serialized to YAML).
	SkillDir  string `yaml:"-"`
	ToolsFile string `yaml:"-"`
}

// LoadOptimizeConfig reads and parses a YAML optimization config file.
func LoadOptimizeConfig(path string) (*OptimizeConfig, error) {
	data, err := os.ReadFile(path) //nolint:gosec // path is provided by user for local config
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

	if hasFile && hasRef {
		return fmt.Errorf("dataset_file and dataset_reference are mutually exclusive; specify one, not both")
	}

	if !hasFile && !hasRef {
		return fmt.Errorf(
			"a dataset is required: provide dataset_file or dataset_reference in your config, " +
				"or run 'azd ai agent eval init' to generate one")
	}

	return nil
}

// defaultOptimizeConfig returns a minimal config skeleton with sensible defaults.
// Dataset, eval model, and other values are resolved interactively or via flags.
func defaultOptimizeConfig(agentName string) *OptimizeConfig {
	return &OptimizeConfig{
		Config: opt_eval.Config{
			Agent:      opt_eval.AgentRef{Name: agentName},
			Evaluators: opt_eval.EvaluatorList{{Name: "builtin.task_adherence"}},
		},
		Options: &opt_eval.Options{
			MaxIterations: new(5),
		},
	}
}

// ToRequest converts the YAML config into an API OptimizeRequest.
// If DatasetFile is set, each line is passed through as raw JSON.
// Returns the request, any non-fatal warnings, and an error.
func (c *OptimizeConfig) ToRequest() (*optimize_api.OptimizeRequest, []string, error) {
	req := &optimize_api.OptimizeRequest{
		Agent: optimize_api.AgentIdentifier{
			AgentName:    c.Agent.Name,
			AgentVersion: c.Agent.Version,
		},
		Evaluators: c.Evaluators.Names(),
		Options: optimize_api.OptimizeOptions{
			EvalModel:         c.Options.EvalModel,
			MaxIterations:     c.Options.MaxIterations,
			OptimizationModel: c.Options.OptimizationModel,
			EvaluationLevel:   c.Options.EvaluationLevel,
		},
	}

	// Map optimization_config from YAML to API format.
	if c.Options.OptimizationConfig != nil {
		req.Options.OptimizationConfig = c.Options.OptimizationConfig
	}

	// Put baselineModel into optimizationConfig.
	if c.Agent.Model != "" {
		if req.Options.OptimizationConfig == nil {
			req.Options.OptimizationConfig = make(map[string]json.RawMessage)
		}
		raw, _ := json.Marshal(c.Agent.Model)
		req.Options.OptimizationConfig["baselineModel"] = raw
	}

	var warnings []string

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
		lines, err := loadJSONLRawFile(c.DatasetFile)
		if err != nil {
			return nil, nil, err
		}
		req.Dataset = lines
	}

	// Populate optimization_config with systemPrompt, skills, tools.
	ensureOptConfig := func() {
		if req.Options.OptimizationConfig == nil {
			req.Options.OptimizationConfig = make(map[string]json.RawMessage)
		}
	}

	if prompt := c.Agent.ResolvedSystemPrompt(); prompt != "" {
		ensureOptConfig()
		raw, _ := json.Marshal(prompt)
		req.Options.OptimizationConfig["systemPrompt"] = raw
	}

	// Load skills from skill_dir if specified.
	if c.SkillDir != "" {
		skills, err := loadSkillsFromDir(c.SkillDir)
		if err != nil {
			return nil, nil, fmt.Errorf("loading skills from %s: %w", c.SkillDir, err)
		}
		ensureOptConfig()
		raw, _ := json.Marshal(skills)
		req.Options.OptimizationConfig["skills"] = raw
	}

	// Load tool definitions if a tools file is specified.
	if c.ToolsFile != "" {
		tools, toolWarns, err := loadToolDefinitions(c.ToolsFile)
		if err != nil {
			return nil, nil, fmt.Errorf("loading tool definitions from %s: %w", c.ToolsFile, err)
		}
		warnings = append(warnings, toolWarns...)
		ensureOptConfig()
		raw, _ := json.Marshal(tools)
		req.Options.OptimizationConfig["tools"] = raw
	}

	return req, warnings, nil
}

// loadToolDefinitions reads an OpenAI-format tools JSON file, deserializes
// into typed ToolDefinition structs, and warns about non-function tool types.
func loadToolDefinitions(path string) ([]optimize_api.ToolDefinition, []string, error) {
	data, err := os.ReadFile(path) //nolint:gosec // path derived from project tools file
	if err != nil {
		return nil, nil, fmt.Errorf("reading tools file: %w", err)
	}

	var tools []optimize_api.ToolDefinition
	if err := json.Unmarshal(data, &tools); err != nil {
		return nil, nil, fmt.Errorf("parsing tools file: %w", err)
	}

	var warnings []string
	for _, t := range tools {
		if t.Type != "function" {
			warnings = append(warnings, fmt.Sprintf(
				"tool %q has type %q (expected \"function\"); it will be sent but may not be recognized",
				t.Function.Name, t.Type))
		}
	}

	if len(tools) == 0 {
		warnings = append(warnings, fmt.Sprintf("tools file %s contains no tool definitions", path))
	}

	return tools, warnings, nil
}

// loadJSONLRawFile reads a JSONL file and returns each non-empty line as
// a json.RawMessage, preserving unknown fields. Uses a streaming scanner
// to avoid loading the entire file into memory at once.
func loadJSONLRawFile(path string) ([]json.RawMessage, error) {
	f, err := os.Open(path) //nolint:gosec // path is provided by user for local config
	if err != nil {
		return nil, fmt.Errorf("failed to read JSONL file %s: %w", path, err)
	}
	defer f.Close()

	var result []json.RawMessage
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		raw := make([]byte, len(line))
		copy(raw, line)
		if !json.Valid(raw) {
			return nil, fmt.Errorf("invalid JSON line in %s: %s", path, line)
		}
		result = append(result, json.RawMessage(raw))
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("reading JSONL file %s: %w", path, err)
	}

	if len(result) == 0 {
		return nil, fmt.Errorf("JSONL file %s is empty", path)
	}

	return result, nil
}

// loadSkillsFromDir reads skill files from a directory and returns SkillDefinitions.
// For markdown files (.md), YAML preamble is parsed to extract name and description;
// the content after the preamble becomes the skill body.
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

		data, err := os.ReadFile(entryPath) //nolint:gosec // path derived from project skill directory
		if err != nil {
			return nil, fmt.Errorf("reading skill file %s: %w", entry.Name(), err)
		}

		skill := parseSkillFile(entry.Name(), string(data))
		skills = append(skills, skill)
	}

	return skills, nil
}

// skillPreamble represents the YAML preamble in a skill markdown file.
type skillPreamble struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
}

// parseSkillFile parses a skill file. For .md files it attempts to extract
// YAML preamble (delimited by "---") for name and description; the body
// is the content after the preamble. For other files, the filename (sans
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

	// Try to parse YAML preamble from markdown.
	fm, body := splitPreamble(content)
	skill := optimize_api.SkillDefinition{
		Name: baseName,
		Body: body,
	}

	if fm != "" {
		var meta skillPreamble
		if err := yaml.Unmarshal([]byte(fm), &meta); err == nil {
			if meta.Name != "" {
				skill.Name = meta.Name
			}
			skill.Description = meta.Description
		}
	}

	return skill
}

// splitPreamble splits YAML preamble (between "---" delimiters) from
// the rest of the content. Returns (preamble, body). If no preamble is
// found, returns ("", original content).
func splitPreamble(content string) (string, string) {
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
