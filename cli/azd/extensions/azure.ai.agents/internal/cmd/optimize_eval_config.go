// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"azureaiagent/internal/exterrors"
	"azureaiagent/internal/pkg/agents/optconfig"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/foundry"
	"github.com/spf13/cobra"
	"go.yaml.in/yaml/v3"
)

type optimizeEvalConfig struct {
	Datasets []struct {
		Name    string            `yaml:"name"`
		File    string            `yaml:"file"`
		Version string            `yaml:"version"`
		Tags    map[string]string `yaml:"tags"`
	} `yaml:"datasets"`
	Evaluators []struct {
		Name                      string         `yaml:"name"`
		Source                    string         `yaml:"source"`
		Version                   string         `yaml:"version"`
		DisplayName               string         `yaml:"displayName"`
		Categories                []string       `yaml:"categories"`
		SupportedEvaluationLevels []string       `yaml:"supportedEvaluationLevels"`
		Definition                map[string]any `yaml:"definition"`
	} `yaml:"evaluators"`
	Evals []optimizeEval `yaml:"evals"`
}

func validateOptimizeCommandFlags(cmd *cobra.Command, flags *optimizeFlags, args []string) error {
	for _, name := range []string{
		"config", "eval-config", "eval", "agent", "dataset", "eval-model",
		"optimize-model", "endpoint", "project-endpoint",
	} {
		if cmd.Flags().Changed(name) {
			value, err := cmd.Flags().GetString(name)
			if err != nil {
				return err
			}
			if strings.TrimSpace(value) == "" {
				return exterrors.Validation(exterrors.CodeConflictingArguments,
					fmt.Sprintf("--%s must not be empty", name), "supply a non-empty value or omit the flag")
			}
		}
	}
	if cmd.Flags().Changed("config") &&
		(cmd.Flags().Changed("eval") || cmd.Flags().Changed("eval-config")) {
		return exterrors.Validation(exterrors.CodeConflictingArguments,
			"--config cannot be combined with --eval or --eval-config",
			"use an optimization request file, or select an evaluation configuration")
	}
	if len(args) > 0 && cmd.Flags().Changed("agent") {
		return exterrors.Validation(exterrors.CodeConflictingArguments,
			"the agent argument cannot be combined with --agent", "supply the agent once")
	}
	if cmd.Flags().Changed("endpoint") && cmd.Flags().Changed("project-endpoint") {
		return exterrors.Validation(exterrors.CodeConflictingArguments,
			"--endpoint cannot be combined with --project-endpoint", "supply a single endpoint")
	}
	if cmd.Flags().Changed("evaluator") {
		for _, evaluator := range flags.evaluators {
			if strings.TrimSpace(evaluator) == "" {
				return fmt.Errorf("--evaluator must not be empty")
			}
		}
	}
	if cmd.Flags().Changed("max-candidates") && flags.maxCandidates < 1 {
		return fmt.Errorf("--max-candidates must be >= 1")
	}
	if flags.pollInterval < 1 {
		return fmt.Errorf("--poll-interval must be >= 1")
	}
	if flags.configFile != "" {
		cfg, err := LoadOptimizeConfig(flags.configFile)
		if err != nil {
			return err
		}
		if cfg.Request.OptimizationConfiguration.Type == "prompt_optimization" {
			for _, name := range []string{"dataset", "eval-model", "evaluator", "max-candidates"} {
				if cmd.Flags().Changed(name) {
					return exterrors.Validation(exterrors.CodeConflictingArguments,
						fmt.Sprintf("--%s cannot be used with a prompt_optimization --config", name),
						"use an agent_optimization request for evaluation-driven optimization")
				}
			}
		}
	}
	if flags.evalConfigFile != "" {
		if _, err := readOptimizeEvalConfig(flags.evalConfigFile); err != nil {
			return err
		}
	}
	return nil
}

type optimizeEval struct {
	Name            string `yaml:"name"`
	ID              string `yaml:"id"`
	Description     string `yaml:"description"`
	Dataset         string `yaml:"dataset"`
	EvaluationLevel string `yaml:"evaluationLevel"`
	MaxSamples      int    `yaml:"maxSamples"`
	Target          *struct {
		Type string `yaml:"type"`
		Name string `yaml:"name"`
	} `yaml:"target"`
	Evaluators []struct {
		Evaluator                string            `yaml:"evaluator"`
		Name                     string            `yaml:"name"`
		Version                  string            `yaml:"version"`
		InitializationParameters map[string]any    `yaml:"initializationParameters"`
		DataMapping              map[string]string `yaml:"dataMapping"`
	} `yaml:"evaluators"`
	Source     map[string]any `yaml:"source"`
	Simulation map[string]any `yaml:"simulation"`
}

func readOptimizeEvalConfig(path string) (*optimizeEvalConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading evaluation configuration: %w", err)
	}
	var values map[string]any
	if err := yaml.Unmarshal(data, &values); err != nil {
		return nil, fmt.Errorf("parsing evaluation configuration: %w", err)
	}
	return decodeOptimizeEvalConfig(values, filepath.Dir(path))
}

func decodeOptimizeEvalConfig(values map[string]any, baseDir string) (*optimizeEvalConfig, error) {
	baseDir, err := filepath.Abs(baseDir)
	if err != nil {
		return nil, fmt.Errorf("resolving evaluation base directory: %w", err)
	}
	resolved, err := foundry.ResolveFileRefs(values, baseDir, foundry.WithPathKeys("file", "source"))
	if err != nil {
		return nil, fmt.Errorf("resolving evaluation configuration: %w", err)
	}
	data, err := yaml.Marshal(resolved)
	if err != nil {
		return nil, fmt.Errorf("encoding evaluation configuration: %w", err)
	}
	var cfg optimizeEvalConfig
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("invalid evaluation configuration: %w; use the configuration from 'azd ai eval init'", err)
	}
	// The resolver rebases included paths relative to baseDir; make them absolute once.
	for i := range cfg.Datasets {
		file := cfg.Datasets[i].File
		if file != "" && !filepath.IsAbs(file) {
			cfg.Datasets[i].File = filepath.Join(baseDir, file)
		}
	}
	return &cfg, nil
}

type optimizeEvalChoice struct {
	name   string
	config *optimizeEvalConfig
	eval   optimizeEval
}

func optimizeEvalChoices(
	cfg *optimizeEvalConfig,
	source, selector, agentName, serviceName string,
) ([]optimizeEvalChoice, error) {
	var choices []optimizeEvalChoice
	names := make(map[string]bool)
	for _, eval := range cfg.Evals {
		if eval.Name == "" || names[eval.Name] {
			return nil, fmt.Errorf("evaluation names in %s must be non-empty and unique", source)
		}
		names[eval.Name] = true
		if selector != "" && eval.Name != selector {
			continue
		}
		if eval.Target == nil || eval.Target.Type != "agent" ||
			(eval.Target.Name != agentName && eval.Target.Name != serviceName) {
			continue
		}
		choices = append(choices, optimizeEvalChoice{
			name: source + "/" + eval.Name, config: cfg, eval: eval,
		})
	}
	return choices, nil
}

func projectOptimizeEvalChoices(
	project *azdext.ProjectConfig,
	selector, agentName, serviceName string,
) ([]optimizeEvalChoice, error) {
	var choices []optimizeEvalChoice
	for _, name := range slices.Sorted(maps.Keys(project.Services)) {
		service := project.Services[name]
		if service.Host != "azure.ai.eval" {
			continue
		}
		properties := service.GetAdditionalProperties()
		if properties == nil {
			return nil, fmt.Errorf("evaluation service %q has no configuration", name)
		}
		cfg, err := decodeOptimizeEvalConfig(properties.AsMap(), project.Path)
		if err != nil {
			return nil, fmt.Errorf("evaluation service %q: %w", name, err)
		}
		candidates, err := optimizeEvalChoices(cfg, name, selector, agentName, serviceName)
		if err != nil {
			return nil, err
		}
		choices = append(choices, candidates...)
	}
	return choices, nil
}

func optimizeConfigFromEval(choice optimizeEvalChoice, agentName, version, judgeModel string) (*OptimizeConfig, error) {
	eval := choice.eval
	if eval.Source != nil || eval.Simulation != nil || eval.EvaluationLevel == "conversation" {
		return nil, fmt.Errorf("eval %q: optimizer eval reuse currently requires a turn dataset and an agent target; "+
			"provide an optimization request with --config for conversation simulation", eval.Name)
	}
	if eval.EvaluationLevel != "" && eval.EvaluationLevel != "turn" {
		return nil, fmt.Errorf("eval %q has unsupported evaluationLevel %q", eval.Name, eval.EvaluationLevel)
	}
	cfg := defaultOptimizeConfig(agentName)
	cfg.Agent.Version = version
	cfg.Name = eval.Name
	cfg.MaxSamples = eval.MaxSamples
	cfg.Options.EvalModel = judgeModel
	evaluatorNames := make(map[string]bool)
	for _, evaluator := range choice.config.Evaluators {
		if evaluator.Name == "" || evaluatorNames[evaluator.Name] {
			return nil, fmt.Errorf("evaluator catalog names must be non-empty and unique")
		}
		evaluatorNames[evaluator.Name] = true
	}
	datasetNames := make(map[string]bool)
	for _, dataset := range choice.config.Datasets {
		if dataset.Name == "" || datasetNames[dataset.Name] {
			return nil, fmt.Errorf("dataset names must be non-empty and unique")
		}
		datasetNames[dataset.Name] = true
		if dataset.Name != eval.Dataset {
			continue
		}
		if dataset.File != "" {
			cfg.Dataset = &optconfig.DatasetRef{LocalURI: dataset.File}
		} else {
			cfg.Dataset = &optconfig.DatasetRef{Name: dataset.Name, Version: dataset.Version}
		}
	}
	if cfg.Dataset == nil {
		return nil, fmt.Errorf("eval %q: dataset %q has no catalog declaration", eval.Name, eval.Dataset)
	}
	for _, evaluator := range eval.Evaluators {
		if evaluator.Evaluator == "" {
			return nil, fmt.Errorf("eval %q: evaluator references require evaluator", eval.Name)
		}
		if len(evaluator.DataMapping) != 0 {
			return nil, fmt.Errorf("eval %q: evaluator %q has dataMapping, which optimization cannot represent",
				eval.Name, evaluator.Evaluator)
		}
		if evaluator.Name != "" && evaluator.Name != strings.TrimPrefix(evaluator.Evaluator, "builtin.") {
			return nil, fmt.Errorf("eval %q: custom criterion names cannot be applied to optimization", eval.Name)
		}
		version := evaluator.Version
		for _, declaration := range choice.config.Evaluators {
			if declaration.Name == evaluator.Evaluator && version == "" {
				version = declaration.Version
			}
		}
		params := evaluator.InitializationParameters
		for _, key := range []string{"model", "deployment_name"} {
			model, ok := params[key]
			if !ok {
				continue
			}
			name, valid := model.(string)
			if !valid || name == "" {
				return nil, fmt.Errorf("evaluator %q: initializationParameters.%s must be non-empty",
					evaluator.Evaluator, key)
			}
			if cfg.Options.EvalModel == "" {
				cfg.Options.EvalModel = name
			} else if judgeModel == "" && cfg.Options.EvalModel != name {
				return nil, fmt.Errorf("eval %q uses multiple judge models; "+
					"specify --eval-model for optimization", eval.Name)
			}
		}
		params = maps.Clone(params)
		if judgeModel != "" {
			if _, exists := params["model"]; exists {
				params["model"] = judgeModel
			}
			if _, exists := params["deployment_name"]; exists {
				params["deployment_name"] = judgeModel
			}
		}
		cfg.Evaluators = append(cfg.Evaluators, optconfig.EvaluatorRef{
			Name: evaluator.Evaluator, Version: version, InitializationParameters: params,
		})
	}
	return cfg, nil
}
