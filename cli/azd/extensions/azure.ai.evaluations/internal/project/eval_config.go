// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// Package project models the eval configuration carried by the
// `host: azure.ai.eval` service entry in azure.yaml.
package project

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"azureaieval/internal/pkg/evalcore"

	"go.yaml.in/yaml/v3"
)

// EvalConfig is one eval — the body of a single `azure.ai.eval` service entry,
// kept in evals/<eval-name>.yaml and pulled in with $ref.
//
// The eval's name is the service key in azure.yaml and is not repeated here.
// One service per eval is what lets the azd dependency graph order an eval
// after the agent it evaluates.
type EvalConfig struct {
	Description string                 `yaml:"description,omitempty" json:"description,omitempty"`
	Dataset     *DatasetDecl           `yaml:"dataset,omitempty"     json:"dataset,omitempty"`
	Evaluators  evalcore.EvaluatorList `yaml:"evaluators,omitempty"  json:"evaluators,omitempty"`
	Target      *Target                `yaml:"target,omitempty"      json:"target,omitempty"`
	Options     *Options               `yaml:"options,omitempty"     json:"options,omitempty"`
}

// DatasetDecl declares a dataset. A local Source is uploaded on deploy; without
// one the name must already resolve to a registered dataset.
type DatasetDecl struct {
	Name    string `yaml:"name"              json:"name"`
	Source  string `yaml:"source,omitempty"  json:"source,omitempty"`
	Version string `yaml:"version,omitempty" json:"version,omitempty"`
}

// EvaluatorDecl declares a custom evaluator. Built-ins are referenced directly
// from an eval and never declared here.
//
// Source names a `.json` file holding a rubric: a list of weighted scoring
// dimensions.
type EvaluatorDecl struct {
	Name    string `yaml:"name"              json:"name"`
	Source  string `yaml:"source,omitempty"  json:"source,omitempty"`
	Version string `yaml:"version,omitempty" json:"version,omitempty"`
}

// Eval is a run definition: evaluators plus options, bound to a dataset.
type Eval struct {
	Name        string                 `yaml:"name"                  json:"name"`
	ID          string                 `yaml:"id,omitempty"          json:"id,omitempty"`
	Description string                 `yaml:"description,omitempty" json:"description,omitempty"`
	Dataset     string                 `yaml:"dataset,omitempty"     json:"dataset,omitempty"`
	Evaluators  evalcore.EvaluatorList `yaml:"evaluators,omitempty"  json:"evaluators,omitempty"`
	Target      *Target                `yaml:"target,omitempty"      json:"target,omitempty"`
	Options     *Options               `yaml:"options,omitempty"     json:"options,omitempty"`
}

// Target names what the run invokes. Only type "agent" is supported today.
type Target struct {
	Type string `yaml:"type" json:"type"`
	Name string `yaml:"name" json:"name"`
}

const TargetTypeAgent = "agent"

// Options are run settings carried on the eval.
//
// There is deliberately no judge-model option. A judge deployment is a testing
// criterion's `initialization_parameters.deployment_name`, which differs per
// evaluator, so it is declared on the evaluator reference instead.
type Options struct {
	MaxSamples      int    `yaml:"max_samples,omitempty"      json:"max_samples,omitempty"`
	EvaluationLevel string `yaml:"evaluation_level,omitempty" json:"evaluation_level,omitempty"`
}

// Evaluation levels accepted by the service. The service default is turn.
const (
	EvaluationLevelTurn         = "turn"
	EvaluationLevelConversation = "conversation"
)

// LoadEvalConfig reads an eval body from disk. The path is used verbatim,
// relative to the process working directory — never re-rooted.
func LoadEvalConfig(path string) (*EvalConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading eval config %q: %w", path, err)
	}

	var cfg EvalConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing eval config %q: %w", path, err)
	}
	return &cfg, nil
}

// EvalNamesIn lists the evals declared under evalDir, in sorted order.
//
// One file is one eval, named after it. The generation spec shares the
// directory and is not one, so it is excluded by name.
func EvalNamesIn(evalDir string) ([]string, error) {
	entries, err := os.ReadDir(evalDir)
	if err != nil {
		return nil, err
	}

	var names []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		ext := strings.ToLower(filepath.Ext(e.Name()))
		if ext != ".yaml" && ext != ".yml" {
			continue
		}
		name := strings.TrimSuffix(e.Name(), filepath.Ext(e.Name()))
		if name == generateConfigBase {
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)
	return names, nil
}

// generateConfigBase is the reserved file name in the evals directory.
const generateConfigBase = "generate"

// ResolveEvalConfigPath finds the config file holding one eval's body.
//
// A named eval is evals/<name>.yaml. With no name the directory must hold
// exactly one eval, and anything else names the candidates rather than
// picking one, because guessing which eval a command meant is the kind of
// mistake that is only noticed after it has run.
func ResolveEvalConfigPath(evalDir, evalName string) (string, error) {
	if evalName != "" {
		path := EvalConfigPath(evalDir, evalName)
		if _, err := os.Stat(path); err != nil {
			return "", fmt.Errorf("eval %q is not declared in %s", evalName, evalDir)
		}
		return path, nil
	}

	names, err := EvalNamesIn(evalDir)
	if err != nil {
		return "", fmt.Errorf("reading %s: %w", evalDir, err)
	}
	switch len(names) {
	case 0:
		return "", fmt.Errorf("no evals are declared in %s", evalDir)
	case 1:
		return EvalConfigPath(evalDir, names[0]), nil
	default:
		return "", fmt.Errorf(
			"%s declares %d evals (%s); choose one with --eval",
			evalDir, len(names), strings.Join(names, ", "))
	}
}

// Validate checks the invariants the provider relies on before it calls the
// service, so failures surface as config errors rather than opaque 4xx.
func (c *EvalConfig) Validate() error {
	if c.Dataset != nil && c.Dataset.Name == "" {
		return fmt.Errorf("dataset: 'name' is required")
	}
	if len(c.Evaluators) == 0 {
		return fmt.Errorf("at least one evaluator is required")
	}

	evaluators := map[string]bool{}
	for i, e := range c.Evaluators {
		if e.Name == "" {
			return fmt.Errorf("evaluators[%d]: 'name' is required", i)
		}
		if evaluators[e.Name] {
			return fmt.Errorf("evaluators[%d]: duplicate evaluator name %q", i, e.Name)
		}
		evaluators[e.Name] = true

		if e.IsBuiltin() {
			if e.Source != "" {
				return fmt.Errorf(
					"evaluators[%d] (%s): a built-in evaluator has no source to publish",
					i, e.Name)
			}
			continue
		}
		// The service assigns an evaluator's version on publish, so a declared
		// one cannot be honoured alongside a source: the upload lands on
		// whatever comes next and the eval binds that, leaving the pin
		// describing a version nothing uses.
		if e.Source != "" && e.Version != "" {
			return fmt.Errorf(
				"evaluators[%d] (%s): `version` cannot be set with `source`, because the "+
					"service assigns the version when it publishes. Drop `version` to "+
					"publish this file, or drop `source` to reference a version already "+
					"on the project", i, e.Name)
		}
	}

	if c.Target != nil && c.Target.Type != "" && c.Target.Type != TargetTypeAgent {
		return fmt.Errorf(
			"target.type %q is not supported; use %q", c.Target.Type, TargetTypeAgent)
	}
	if c.Options != nil {
		switch c.Options.EvaluationLevel {
		case "", EvaluationLevelTurn, EvaluationLevelConversation:
		default:
			return fmt.Errorf(
				"options.evaluation_level %q is invalid; expected %q or %q",
				c.Options.EvaluationLevel, EvaluationLevelTurn, EvaluationLevelConversation)
		}
	}

	return nil
}

// Eval resolves the config into the eval the reconciler publishes, taking its
// name from the service entry that pulled the file in.
func (c *EvalConfig) Eval(name string) Eval {
	resolved := Eval{
		Name:        name,
		Description: c.Description,
		Evaluators:  c.Evaluators,
		Target:      c.Target,
		Options:     c.Options,
	}
	if c.Dataset != nil {
		resolved.Dataset = c.Dataset.Name
	}
	return resolved
}

// CustomEvaluators are the evaluators this config owns — the referenced ones
// carrying a local source, which are published before the eval that names them.
// A built-in needs nothing, and one without a source is already registered.
func (c *EvalConfig) CustomEvaluators() []EvaluatorDecl {
	var owned []EvaluatorDecl
	for _, ref := range c.Evaluators {
		if ref.IsBuiltin() || ref.Source == "" {
			continue
		}
		owned = append(owned, EvaluatorDecl{
			Name:    ref.Name,
			Source:  ref.Source,
			Version: ref.Version,
		})
	}
	return owned
}
