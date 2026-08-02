// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// Package project models the eval configuration carried by the
// `host: azure.ai.eval` service entry in azure.yaml.
package project

import (
	"fmt"
	"os"
	"strings"

	"azureaieval/internal/pkg/evalcore"

	"go.yaml.in/yaml/v3"
)

// EvalConfig is the deployment spec — the body of the azure.ai.eval service
// entry, normally kept in evals/azure.yaml and pulled in with $ref.
type EvalConfig struct {
	Evaluators []EvaluatorDecl `yaml:"evaluators,omitempty" json:"evaluators,omitempty"`
	Datasets   []DatasetDecl   `yaml:"datasets,omitempty"   json:"datasets,omitempty"`
	Evals      []Eval          `yaml:"evals,omitempty" json:"evals,omitempty"`
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
// Source decides which kind of evaluator this is, by extension: a `.py` file is
// a single self-contained Python script and publishes a code evaluator, a
// `.json` file holds a rubric. A code evaluator cannot name a folder — it runs
// as a python grader, which is handed one script's source and cannot import a
// helper module beside it.
type EvaluatorDecl struct {
	Name    string `yaml:"name"              json:"name"`
	Source  string `yaml:"source,omitempty"  json:"source,omitempty"`
	Version string `yaml:"version,omitempty" json:"version,omitempty"`

	// Code evaluators only. The three schema fields name JSON files beside the
	// script, resolved like Source, because they are edited as files rather
	// than written inline in YAML.
	ImageTag       string `yaml:"image_tag,omitempty"       json:"image_tag,omitempty"`
	Metrics        string `yaml:"metrics,omitempty"         json:"metrics,omitempty"`
	DataSchema     string `yaml:"data_schema,omitempty"     json:"data_schema,omitempty"`
	InitParameters string `yaml:"init_parameters,omitempty" json:"init_parameters,omitempty"`
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

// TargetTypeModel evaluates a model deployment directly, with no agent in
// front of it. A model answers as plain text, so a group targeting one binds
// its response differently from a group targeting an agent.
const TargetTypeModel = "model"

// Options are run settings carried on the group.
type Options struct {
	EvalModel       string `yaml:"eval_model,omitempty"       json:"eval_model,omitempty"`
	MaxSamples      int    `yaml:"max_samples,omitempty"      json:"max_samples,omitempty"`
	EvaluationLevel string `yaml:"evaluation_level,omitempty" json:"evaluation_level,omitempty"`
}

// Evaluation levels accepted by the service. The service default is turn.
const (
	EvaluationLevelTurn         = "turn"
	EvaluationLevelConversation = "conversation"
)

// LoadEvalConfig reads a deployment spec from disk. The path is used verbatim,
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

// Validate checks the invariants the provider relies on before it calls the
// service, so failures surface as config errors rather than opaque 4xx.
func (c *EvalConfig) Validate() error {
	datasets := map[string]bool{}
	for i, d := range c.Datasets {
		if d.Name == "" {
			return fmt.Errorf("datasets[%d]: 'name' is required", i)
		}
		if datasets[d.Name] {
			return fmt.Errorf("datasets[%d]: duplicate dataset name %q", i, d.Name)
		}
		datasets[d.Name] = true
	}

	evaluators := map[string]bool{}
	for i, e := range c.Evaluators {
		if e.Name == "" {
			return fmt.Errorf("evaluators[%d]: 'name' is required", i)
		}
		if strings.HasPrefix(e.Name, evalcore.BuiltinPrefix) {
			return fmt.Errorf(
				"evaluators[%d]: built-in evaluator %q must not be declared; "+
					"reference it directly from an eval", i, e.Name)
		}
		if evaluators[e.Name] {
			return fmt.Errorf("evaluators[%d]: duplicate evaluator name %q", i, e.Name)
		}
		// The service assigns an evaluator's version on publish, so a declared
		// one cannot be honoured alongside a source: the upload lands on
		// whatever comes next and the group binds that, leaving the pin
		// describing a version nothing uses.
		if e.Source != "" && e.Version != "" {
			return fmt.Errorf(
				"evaluators[%d] (%s): `version` cannot be set with `source`, because the "+
					"service assigns the version when it publishes. Drop `version` to "+
					"publish this file, or drop `source` to reference a version already "+
					"on the project", i, e.Name)
		}
		evaluators[e.Name] = true
	}

	groups := map[string]bool{}
	for i, g := range c.Evals {
		if g.Name == "" {
			return fmt.Errorf("evals[%d]: 'name' is required", i)
		}
		if groups[g.Name] {
			return fmt.Errorf("evals[%d]: duplicate eval name %q", i, g.Name)
		}
		groups[g.Name] = true

		if g.Dataset != "" && !datasets[g.Dataset] {
			return fmt.Errorf(
				"evals[%d] (%s): dataset %q is not declared in datasets",
				i, g.Name, g.Dataset)
		}
		if len(g.Evaluators) == 0 {
			return fmt.Errorf("evals[%d] (%s): at least one evaluator is required", i, g.Name)
		}
		for _, ref := range g.Evaluators {
			if ref.IsBuiltin() {
				continue
			}
			if !evaluators[ref.Name] {
				return fmt.Errorf(
					"evals[%d] (%s): evaluator %q is not declared in evaluators "+
						"(built-ins need the %q prefix)",
					i, g.Name, ref.Name, evalcore.BuiltinPrefix)
			}
		}
		if g.Target != nil && g.Target.Type != "" &&
			g.Target.Type != TargetTypeAgent && g.Target.Type != TargetTypeModel {
			return fmt.Errorf(
				"evals[%d] (%s): target.type %q is not supported; use %q or %q",
				i, g.Name, g.Target.Type, TargetTypeAgent, TargetTypeModel)
		}
		if g.Options != nil {
			switch g.Options.EvaluationLevel {
			case "", EvaluationLevelTurn, EvaluationLevelConversation:
			default:
				return fmt.Errorf(
					"evals[%d] (%s): evaluation_level %q is invalid; expected %q or %q",
					i, g.Name, g.Options.EvaluationLevel,
					EvaluationLevelTurn, EvaluationLevelConversation)
			}
		}
	}

	return nil
}

// Dataset returns the declaration with the given name.
func (c *EvalConfig) Dataset(name string) (*DatasetDecl, bool) {
	for i := range c.Datasets {
		if c.Datasets[i].Name == name {
			return &c.Datasets[i], true
		}
	}
	return nil, false
}

// Evaluator returns the declaration with the given name.
func (c *EvalConfig) Evaluator(name string) (*EvaluatorDecl, bool) {
	for i := range c.Evaluators {
		if c.Evaluators[i].Name == name {
			return &c.Evaluators[i], true
		}
	}
	return nil, false
}

// Group returns the eval with the given name.
func (c *EvalConfig) Group(name string) (*Eval, bool) {
	for i := range c.Evals {
		if c.Evals[i].Name == name {
			return &c.Evals[i], true
		}
	}
	return nil, false
}

// ResolveGroup picks the group to act on: the named one, or the only one when
// the config declares exactly one.
func (c *EvalConfig) ResolveGroup(name string) (*Eval, error) {
	if name != "" {
		g, ok := c.Group(name)
		if !ok {
			return nil, fmt.Errorf("eval %q is not declared in the config", name)
		}
		return g, nil
	}
	switch len(c.Evals) {
	case 0:
		return nil, fmt.Errorf("no evals are declared in the config")
	case 1:
		return &c.Evals[0], nil
	default:
		names := make([]string, 0, len(c.Evals))
		for _, g := range c.Evals {
			names = append(names, g.Name)
		}
		return nil, fmt.Errorf(
			"the config declares %d evals (%s); choose one with --eval",
			len(c.Evals), strings.Join(names, ", "))
	}
}
