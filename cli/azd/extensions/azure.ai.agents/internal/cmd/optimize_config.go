// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// optimize_config.go defines OptimizeConfig (the YAML config structure for
// optimization jobs), provides loading/validation, and converts configs into
// API requests. It also handles reading skills from disk and parsing YAML
// preamble in skill files.

package cmd

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"azureaiagent/internal/pkg/agents/optconfig"
	"azureaiagent/internal/pkg/agents/optimize_api"

	"go.yaml.in/yaml/v3"
)

// OptimizeConfig holds resolved optimizer inputs and an optional GA request.
type OptimizeConfig struct {
	optconfig.Config `yaml:",inline"`

	// Resolved dataset and optimizer options.
	ValidationDataset *optconfig.DatasetRef `yaml:"validation_dataset,omitempty"`
	Options           *optconfig.Options    `yaml:"options"`

	// Runtime-only: resolved skill directory and tools file (not serialized to YAML).
	SkillDir      string                        `yaml:"-"`
	ToolsFile     string                        `yaml:"-"`
	Request       *optimize_api.OptimizeRequest `yaml:"-"`
	TrainingSet   *optimize_api.SetInput        `yaml:"-"`
	ValidationSet *optimize_api.SetInput        `yaml:"-"`
	MaxSamples    int                           `yaml:"-"`
}

// LoadOptimizeConfig reads a GA v1 optimization request from YAML or JSON.
// Omitted optional settings retain service defaults rather than command defaults.
func LoadOptimizeConfig(path string) (*OptimizeConfig, error) {
	data, err := os.ReadFile(path) //nolint:gosec // path is provided by user for local config
	if err != nil {
		return nil, fmt.Errorf("failed to read config file %s: %w", path, err)
	}

	var values map[string]any
	if err := yaml.Unmarshal(data, &values); err != nil {
		return nil, fmt.Errorf("failed to parse config file %s: %w", path, err)
	}
	if _, legacy := values["agent"]; legacy {
		return nil, fmt.Errorf("legacy optimization/eval configuration is not supported; " +
			"use an optimization request file or select an eval configured with 'azd ai eval init'")
	}
	encoded, err := json.Marshal(values)
	if err != nil {
		return nil, fmt.Errorf("encoding config file %s: %w", path, err)
	}
	var request optimize_api.OptimizeRequest
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		return nil, fmt.Errorf("invalid optimization config file %s: %w", path, err)
	}
	if request.OptimizationConfiguration.Type != "agent_optimization" &&
		request.OptimizationConfiguration.Type != "prompt_optimization" {
		return nil, fmt.Errorf("optimization_configuration.type must be agent_optimization or prompt_optimization")
	}
	if request.TargetConfiguration == nil || request.TargetConfiguration.Type != "foundry_agent" ||
		request.TargetConfiguration.Name == "" {
		return nil, fmt.Errorf("target_configuration must name a foundry_agent")
	}
	return optimizeConfigFromRequest(&request), nil
}

func optimizeConfigFromRequest(request *optimize_api.OptimizeRequest) *OptimizeConfig {
	cfg := defaultOptimizeConfig(request.TargetConfiguration.Name)
	cfg.Agent.Version = request.TargetConfiguration.Version
	cfg.Request = request
	cfg.Options.OptimizationModel = request.OptimizationModelConfiguration.Model
	config := &request.OptimizationConfiguration
	if evaluation := config.EvaluationConfiguration; evaluation != nil {
		cfg.TrainingSet = &evaluation.TrainingSet
		cfg.ValidationSet = evaluation.ValidationSet
		cfg.Options.EvalModel = evaluation.EvaluationModel.Model
		cfg.Options.MaxConcurrentAgentRuns = evaluation.MaxConcurrentAgentRuns
		for _, evaluator := range evaluation.Evaluators {
			cfg.Evaluators = append(cfg.Evaluators, optconfig.EvaluatorRef{
				Name: evaluator.Name, Version: evaluator.Version,
				InitializationParameters: evaluator.InitializationParameters,
			})
		}
	}
	if search := config.CandidateSearchConfiguration; search != nil {
		cfg.Options.MaxCandidates = search.MaxCandidates
		cfg.Options.MaxStalls = search.MaxConsecutiveFailedCandidateScreens
	}
	if baseline := config.BaselineAgentConfiguration; baseline != nil {
		cfg.Agent.Model = baseline.CurrentModel
		cfg.Agent.Instruction.Value = baseline.SystemPrompt
	}
	return cfg
}

// Validate checks required fields and dataset constraints.
func (c *OptimizeConfig) Validate() error {
	if c.Agent.Name == "" {
		return fmt.Errorf("agent.name is required")
	}
	if c.Request != nil && c.Request.OptimizationConfiguration.Type == "prompt_optimization" {
		return nil
	}

	if c.Options == nil || c.Options.EvalModel == "" {
		return fmt.Errorf("eval_model is required: set --eval-model or evaluation_configuration.evaluation_model.model")
	}

	if c.Options.OptimizationModel == "" {
		return fmt.Errorf(
			"optimization_model is required: pass --optimize-model <name>, " +
				"or set optimization_model_configuration.model in your --config file")
	}

	if len(c.Evaluators) == 0 {
		return fmt.Errorf(
			"at least one evaluator is required: pass --evaluator <name> (repeatable), " +
				"configure evaluation_configuration.evaluators, or run 'azd ai eval generate'")
	}

	hasLocal := c.LocalDatasetPath() != ""
	hasRemote := c.RemoteDatasetReference() != nil

	if !hasLocal && !hasRemote && c.TrainingSet == nil {
		return fmt.Errorf(
			"a dataset is required: provide a local or registered dataset in your config, " +
				"or run 'azd ai eval generate' to generate one")
	}

	if c.Options.MaxStalls != nil && *c.Options.MaxStalls < 1 {
		return fmt.Errorf(
			"max_stalls must be >= 1 (got %d): set candidate_search_configuration.max_consecutive_failed_candidate_screens",
			*c.Options.MaxStalls)
	}

	if c.Options.MaxConcurrentAgentRuns != nil && *c.Options.MaxConcurrentAgentRuns < 1 {
		return fmt.Errorf(
			"max_concurrent_agent_runs must be >= 1 (got %d): "+
				"set evaluation_configuration.max_concurrent_agent_runs in your --config file",
			*c.Options.MaxConcurrentAgentRuns)
	}

	return nil
}

// defaultOptimizeConfig returns a minimal config skeleton without optional optimization defaults.
// Dataset, eval model, evaluators, and other values are resolved interactively or via flags.
func defaultOptimizeConfig(agentName string) *OptimizeConfig {
	return &OptimizeConfig{
		Config: optconfig.Config{
			Agent: optconfig.AgentRef{Name: agentName},
		},
		Options: &optconfig.Options{},
	}
}

// ToRequest converts resolved inputs into a GA v1 OptimizeRequest.
// Local JSONL rows are preserved as raw JSON in test_cases.
// Returns the request, any non-fatal warnings, and an error.
func (c *OptimizeConfig) ToRequest() (*optimize_api.OptimizeRequest, []string, error) {
	req := optimize_api.OptimizeRequest{}
	if c.Request != nil {
		req = *c.Request
	} else {
		req.DisplayName = c.Name
	}
	req.TargetConfiguration = &optimize_api.AgentIdentifier{
		Type: "foundry_agent", Name: c.Agent.Name, Version: c.Agent.Version,
	}
	options := c.Options
	if options == nil {
		options = &optconfig.Options{}
	}
	req.OptimizationModelConfiguration.Model = options.OptimizationModel
	config := &req.OptimizationConfiguration
	if config.Type == "" {
		config.Type = "agent_optimization"
	}
	if config.Type == "prompt_optimization" {
		return &req, nil, nil
	}
	evaluation := optimize_api.EvaluationConfiguration{}
	if config.EvaluationConfiguration != nil {
		evaluation = *config.EvaluationConfiguration
	}
	evaluation.Evaluators = evaluatorRefs(c.Evaluators)
	evaluation.EvaluationModel.Model = options.EvalModel
	evaluation.MaxConcurrentAgentRuns = options.MaxConcurrentAgentRuns
	setType := "target_completion"
	if options.EvaluationLevel == "conversation" {
		setType = "user_conversation_simulation"
	}
	if c.TrainingSet != nil {
		evaluation.TrainingSet = *c.TrainingSet
	}
	trainRef := c.Dataset
	if c.DatasetFile != "" {
		trainRef = &optconfig.DatasetRef{LocalURI: c.DatasetFile}
	}
	if trainRef != nil {
		ds, err := datasetRefToAPI(trainRef)
		if err != nil {
			return nil, nil, fmt.Errorf("train dataset: %w", err)
		}
		evaluation.TrainingSet = optimize_api.SetInput{Type: setType, Source: *ds}
	}
	if c.ValidationSet != nil {
		evaluation.ValidationSet = c.ValidationSet
	}
	if c.ValidationDataset != nil {
		ds, err := datasetRefToAPI(c.ValidationDataset)
		if err != nil {
			return nil, nil, fmt.Errorf("validation dataset: %w", err)
		}
		evaluation.ValidationSet = &optimize_api.SetInput{Type: setType, Source: *ds}
	}
	config.EvaluationConfiguration = &evaluation
	if c.MaxSamples < 0 {
		return nil, nil, fmt.Errorf("maxSamples must be >= 0")
	}
	if c.MaxSamples > 0 {
		if evaluation.TrainingSet.Source.Type != optimize_api.DatasetTypeInline {
			return nil, nil, fmt.Errorf("maxSamples requires a local dataset for optimization")
		}
		cases := evaluation.TrainingSet.Source.TestCases
		evaluation.TrainingSet.Source.TestCases = cases[:min(len(cases), c.MaxSamples)]
	}
	if config.CandidateSearchConfiguration != nil || options.MaxCandidates != nil || options.MaxStalls != nil {
		search := optimize_api.CandidateSearchConfiguration{}
		if config.CandidateSearchConfiguration != nil {
			search = *config.CandidateSearchConfiguration
		}
		search.MaxCandidates = options.MaxCandidates
		search.MaxConsecutiveFailedCandidateScreens = options.MaxStalls
		config.CandidateSearchConfiguration = &search
	}
	baseline := optimize_api.BaselineAgentConfiguration{}
	if config.BaselineAgentConfiguration != nil {
		baseline = *config.BaselineAgentConfiguration
	}
	space := optimize_api.OptimizationSpace{}
	if config.AgentOptimizationSpace != nil {
		space = *config.AgentOptimizationSpace
	}
	for key, value := range options.OptimizationConfig {
		var target any
		switch key {
		case "model":
			target = &baseline.CurrentModel
		case "system_prompt":
			target = &baseline.SystemPrompt
		case "skills":
			target = &baseline.Skills
		case "tools":
			target = &baseline.Tools
		case "model_search_space":
			target = &space.ModelSearchSpace
		case "target_attributes":
			target = &space.TargetAttributes
		default:
			return nil, nil, fmt.Errorf("unsupported optimization setting %q", key)
		}
		if err := json.Unmarshal(value, target); err != nil {
			return nil, nil, fmt.Errorf("invalid optimization setting %q: %w", key, err)
		}
	}
	if c.Agent.Model != "" {
		baseline.CurrentModel = c.Agent.Model
	}
	if c.Agent.Instruction.File != "" {
		prompt, err := os.ReadFile(c.Agent.Instruction.File)
		if err != nil {
			return nil, nil, fmt.Errorf("reading baseline instructions: %w", err)
		}
		baseline.SystemPrompt = string(prompt)
	} else if c.Agent.Instruction.Value != "" {
		baseline.SystemPrompt = c.Agent.Instruction.Value
	}
	var warnings []string
	if c.SkillDir != "" {
		skills, err := loadSkillsFromDir(c.SkillDir)
		if err != nil {
			return nil, nil, fmt.Errorf("loading skills from %s: %w", c.SkillDir, err)
		}
		baseline.Skills = skills
	}

	// Load tool definitions if a tools file is specified.
	if c.ToolsFile != "" {
		tools, toolWarns, err := loadToolDefinitions(c.ToolsFile)
		if err != nil {
			return nil, nil, fmt.Errorf("loading tool definitions from %s: %w", c.ToolsFile, err)
		}
		warnings = append(warnings, toolWarns...)
		baseline.Tools = tools
	}
	if baseline.CurrentModel != "" || baseline.SystemPrompt != "" || baseline.Skills != nil || baseline.Tools != nil {
		config.BaselineAgentConfiguration = &baseline
	}
	if config.AgentOptimizationSpace != nil ||
		space.TargetAttributes != nil || space.ModelSearchSpace != nil {
		config.AgentOptimizationSpace = &space
	}
	return &req, warnings, nil
}

// datasetRefToAPI converts a DatasetRef into the API Dataset payload. A local
// dataset (local_uri, no name) is sent inline with its JSONL items; a
// registered dataset (name/version) is sent as a reference.
func datasetRefToAPI(ref *optconfig.DatasetRef) (*optimize_api.Dataset, error) {
	if ref.IsLocal() {
		lines, err := loadJSONLRawFile(ref.LocalURI)
		if err != nil {
			return nil, err
		}
		return &optimize_api.Dataset{Type: optimize_api.DatasetTypeInline, TestCases: lines}, nil
	}
	if ref.Name == "" || ref.Version == "" {
		return nil, fmt.Errorf("registered datasets require both name and version; " +
			"select a versioned eval dataset or provide a local JSONL file")
	}
	return &optimize_api.Dataset{
		Type:    optimize_api.DatasetTypeReference,
		Name:    ref.Name,
		Version: ref.Version,
	}, nil
}

// evaluatorRefs converts a YAML evaluator list into API evaluator references,
// preserving each evaluator's name, optional version, and initialization_parameters.
func evaluatorRefs(list optconfig.EvaluatorList) []optimize_api.EvaluatorRef {
	if len(list) == 0 {
		return nil
	}
	refs := make([]optimize_api.EvaluatorRef, 0, len(list))
	for _, e := range list {
		refs = append(refs, optimize_api.EvaluatorRef{
			Name:                     e.Name,
			Version:                  e.Version,
			InitializationParameters: e.InitializationParameters,
		})
	}
	return refs
}

// exists in base (case-sensitive). Order is preserved: base first, then any
// new entries from add. Used to layer --evaluator flags on top of config
// evaluators without dropping the config entries.
func mergeEvaluators(base, add optconfig.EvaluatorList) optconfig.EvaluatorList {
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
	Description string `yaml:"description,omitempty"`
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
