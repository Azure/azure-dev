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
	ValidationDataset *opt_eval.DatasetRef `yaml:"validation_dataset,omitempty"`
	// LegacyValidationReference reads the deprecated `validation_reference`
	// YAML key. Use ValidationDataset instead; this is consulted only for
	// backward compatibility and is merged into ValidationDataset by
	// normalizeValidationDataset at load time.
	LegacyValidationReference *opt_eval.DatasetRef `yaml:"validation_reference,omitempty"`
	Options                   *opt_eval.Options    `yaml:"options"`

	// Runtime-only: resolved skill directory and tools file (not serialized to YAML).
	SkillDir  string `yaml:"-"`
	ToolsFile string `yaml:"-"`
}

// normalizeValidationDataset merges the deprecated `validation_reference` key
// into ValidationDataset when it is unset, then clears the legacy field so it
// is not re-written.
func (c *OptimizeConfig) normalizeValidationDataset() {
	if c.ValidationDataset == nil && c.LegacyValidationReference != nil {
		c.ValidationDataset = c.LegacyValidationReference
	}
	c.LegacyValidationReference = nil
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
	cfg.NormalizeDataset()
	cfg.normalizeValidationDataset()

	return &cfg, nil
}

// Validate checks required fields and dataset constraints.
func (c *OptimizeConfig) Validate() error {
	if c.Agent.Name == "" {
		return fmt.Errorf("agent.name is required")
	}

	if c.Options == nil || c.Options.EvalModel == "" {
		return fmt.Errorf("options.eval_model is required")
	}

	if c.Options.OptimizationModel == "" {
		return fmt.Errorf(
			"options.optimization_model is required: pass --optimize-model <name>, " +
				"or add 'optimization_model' under 'options:' in your config")
	}

	if len(c.Evaluators) == 0 {
		return fmt.Errorf(
			"at least one evaluator is required: pass --evaluator <name> (repeatable), " +
				"add an 'evaluators:' section to your config, or run 'azd ai agent eval generate' to generate one")
	}

	hasLocal := c.LocalDatasetPath() != ""
	hasRemote := c.RemoteDatasetReference() != nil

	if !hasLocal && !hasRemote {
		return fmt.Errorf(
			"a dataset is required: provide a local or registered dataset in your config, " +
				"or run 'azd ai agent eval generate' to generate one")
	}

	return nil
}

// defaultOptimizeConfig returns a minimal config skeleton with sensible defaults.
// Dataset, eval model, evaluators, and other values are resolved interactively or via flags.
func defaultOptimizeConfig(agentName string) *OptimizeConfig {
	return &OptimizeConfig{
		Config: opt_eval.Config{
			Agent: opt_eval.AgentRef{Name: agentName},
		},
		Options: &opt_eval.Options{
			MaxCandidates: new(5),
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
		Evaluators: evaluatorRefs(c.Evaluators),
		Options: optimize_api.OptimizeOptions{
			EvalModel:         c.Options.EvalModel,
			MaxCandidates:     c.Options.MaxCandidates,
			OptimizationModel: c.Options.OptimizationModel,
			EvaluationLevel:   c.Options.EvaluationLevel,
		},
	}

	// Map optimization_config from YAML to API format.
	if c.Options.OptimizationConfig != nil {
		req.Options.OptimizationConfig = c.Options.OptimizationConfig
	}

	// Put the baseline model into optimization_config as "model".
	if c.Agent.Model != "" {
		if req.Options.OptimizationConfig == nil {
			req.Options.OptimizationConfig = make(map[string]json.RawMessage)
		}
		raw, _ := json.Marshal(c.Agent.Model)
		req.Options.OptimizationConfig["model"] = raw
	}

	var warnings []string

	if ref := c.RemoteDatasetReference(); ref != nil {
		req.TrainDataset = &optimize_api.Dataset{
			Type:    optimize_api.DatasetTypeReference,
			Name:    ref.Name,
			Version: ref.Version,
		}
	}

	if c.ValidationDataset != nil {
		req.ValidationDataset = &optimize_api.Dataset{
			Type:    optimize_api.DatasetTypeReference,
			Name:    c.ValidationDataset.Name,
			Version: c.ValidationDataset.Version,
		}
	}

	if localPath := c.LocalDatasetPath(); localPath != "" {
		lines, err := loadJSONLRawFile(localPath)
		if err != nil {
			return nil, nil, err
		}
		req.TrainDataset = &optimize_api.Dataset{
			Type:  optimize_api.DatasetTypeInline,
			Items: lines,
		}
	}

	// Populate optimization_config with system_prompt, skills, tools.
	ensureOptConfig := func() {
		if req.Options.OptimizationConfig == nil {
			req.Options.OptimizationConfig = make(map[string]json.RawMessage)
		}
	}

	if prompt := c.Agent.ResolvedSystemPrompt(); prompt != "" {
		ensureOptConfig()
		raw, _ := json.Marshal(prompt)
		req.Options.OptimizationConfig["system_prompt"] = raw
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

// evaluatorRefs converts a YAML evaluator list into API evaluator references,
// preserving each evaluator's name and optional version.
func evaluatorRefs(list opt_eval.EvaluatorList) []optimize_api.EvaluatorRef {
	if len(list) == 0 {
		return nil
	}
	refs := make([]optimize_api.EvaluatorRef, 0, len(list))
	for _, e := range list {
		refs = append(refs, optimize_api.EvaluatorRef{Name: e.Name, Version: e.Version})
	}
	return refs
}

// mergeEvaluators appends add to base, skipping entries whose name already
// exists in base (case-sensitive). Order is preserved: base first, then any
// new entries from add. Used to layer --evaluator flags on top of config
// evaluators without dropping the config entries.
func mergeEvaluators(base, add opt_eval.EvaluatorList) opt_eval.EvaluatorList {
	seen := make(map[string]struct{}, len(base))
	for _, e := range base {
		seen[e.Name] = struct{}{}
	}
	merged := base
	for _, e := range add {
		if _, ok := seen[e.Name]; ok {
			continue
		}
		seen[e.Name] = struct{}{}
		merged = append(merged, e)
	}
	return merged
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
		if skill.Body == "" && skill.Description == "" {
			continue
		}
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
