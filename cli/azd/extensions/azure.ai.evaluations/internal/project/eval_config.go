// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// Package project models the eval configuration carried by the
// `host: azure.ai.eval` service entry in azure.yaml.
package project

import (
	"strings"

	"azureaieval/internal/messages"
	"azureaieval/internal/pkg/evalcore"
)

// EvalConfig is one evaluation configuration: the catalogs of reusable assets,
// and every eval defined over them.
//
// It is the body of a single `azure.ai.eval` service entry, pulled in with
// $ref. One file rather than one per eval, because the catalogs are shared:
// two evals over the same dataset should name it once.
//
// How it is stored lives in eval_config_store.go.
type EvalConfig struct {
	Datasets   []DatasetDecl   `yaml:"datasets,omitempty"   json:"datasets,omitempty"`
	Evaluators []EvaluatorDecl `yaml:"evaluators,omitempty" json:"evaluators,omitempty"`
	Evals      []Eval          `yaml:"evals,omitempty"      json:"evals,omitempty"`
}

// DatasetDecl is a catalog entry. A local File is uploaded on deploy; without
// one the name must already resolve to a registered dataset.
//
// Deliberately not a `$ref`: that directive replaces a definition with one
// loaded from a YAML or JSON file, and these rows are an artifact to publish.
// A `.jsonl` is neither, so there would be nothing to splice.
type DatasetDecl struct {
	Name    string `yaml:"name"              json:"name"`
	File    string `yaml:"file,omitempty"    json:"file,omitempty"`
	Version string `yaml:"version,omitempty" json:"version,omitempty"`
}

// EvaluatorDecl is a catalog entry for a custom evaluator. Built-ins are
// referenced straight from an eval and never declared here.
//
// Source names a `.json` file holding a rubric: a list of weighted scoring
// dimensions.
//
// Ref carries the `$ref` an author wrote. Commands that use the configuration
// never see it -- resolution has already replaced the entry with the file's
// content by then -- but the commands that read, modify and save the file do,
// and modelling it is what lets the include survive being written back. Name is
// omitempty for the same reason: an entry that is only a `$ref` has no name of
// its own until the file it names supplies one.
//
// Definition is the rubric written out in place of naming a file, which is what
// lets a `$ref` name a rubric: resolution splices the file's keys in here, and
// they have to land on a field to survive strict decoding. It is deliberately
// one named key rather than a catch-all: a catch-all would swallow every
// misspelling in the entry and publish it to the service as rubric content.
type EvaluatorDecl struct {
	Ref        string         `yaml:"$ref,omitempty"       json:"$ref,omitempty"`
	Name       string         `yaml:"name,omitempty"       json:"name,omitempty"`
	Source     string         `yaml:"source,omitempty"     json:"source,omitempty"`
	Version    string         `yaml:"version,omitempty"    json:"version,omitempty"`
	Definition map[string]any `yaml:"definition,omitempty" json:"definition,omitempty"`
}

// Eval is one evaluation defined over the catalogs.
//
// Dataset and Source are alternatives: rows come from a catalog dataset, or
// from a source such as production traces. Target is what gets invoked, and is
// a separate axis — an eval can read traces and invoke nothing.
type Eval struct {
	Name            string                 `yaml:"name"                        json:"name"`
	ID              string                 `yaml:"id,omitempty"                json:"id,omitempty"`
	Description     string                 `yaml:"description,omitempty"       json:"description,omitempty"`
	Dataset         string                 `yaml:"dataset,omitempty"           json:"dataset,omitempty"`
	Source          *SourceDecl            `yaml:"source,omitempty"            json:"source,omitempty"`
	EvaluationLevel string                 `yaml:"evaluation_level,omitempty"  json:"evaluation_level,omitempty"`
	MaxSamples      int                    `yaml:"max_samples,omitempty"       json:"max_samples,omitempty"`
	Evaluators      evalcore.EvaluatorList `yaml:"evaluators,omitempty"        json:"evaluators,omitempty"`
	Target          *Target                `yaml:"target,omitempty"            json:"target,omitempty"`
}

// SourceDecl says where an eval's rows come from when they are not a dataset.
type SourceDecl struct {
	Type          string   `yaml:"type"                      json:"type"`
	LookbackHours int      `yaml:"lookback_hours,omitempty"  json:"lookback_hours,omitempty"`
	MaxTraces     int      `yaml:"max_traces,omitempty"      json:"max_traces,omitempty"`
	AgentName     string   `yaml:"agent_name,omitempty"      json:"agent_name,omitempty"`
	ResponseIDs   []string `yaml:"response_ids,omitempty"    json:"response_ids,omitempty"`
	MaxTurns      int      `yaml:"max_turns,omitempty"       json:"max_turns,omitempty"`
	// AgentVersion pins which deployment's spans are read. Without it the
	// service chooses, and a redeployed agent is evaluated against whichever
	// version it picked.
	AgentVersion string `yaml:"agent_version,omitempty"   json:"agent_version,omitempty"`
	// StartTime and EndTime bound the window explicitly. LookbackHours stays
	// supported, read as a start bound measured back from EndTime, or from now
	// when nothing closes the window.
	StartTime string `yaml:"start_time,omitempty"      json:"start_time,omitempty"`
	EndTime   string `yaml:"end_time,omitempty"        json:"end_time,omitempty"`
}

// Source types an eval can read rows from.
const (
	SourceTypeTraces    = "traces"
	SourceTypeResponses = "responses"
)

// DefaultScaffoldMaxTraces is the cap init writes on a trace-backed eval, so a
// first run is bounded rather than taking the service's own default of 1000.
// Deleting max_traces from the file restores that default.
const DefaultScaffoldMaxTraces = 20

// Target names what the run invokes.
type Target struct {
	Type string `yaml:"type" json:"type"`
	Name string `yaml:"name" json:"name"`
}

// Target types the extension can invoke. Absent means nothing is invoked and
// the dataset already carries the answers.
const (
	TargetTypeAgent = "agent"
	TargetTypeModel = "model"
)

// Evaluation levels accepted by the service. The service default is turn.
const (
	EvaluationLevelTurn         = "turn"
	EvaluationLevelConversation = "conversation"
)

// EvalNames lists the declared evals in declaration order.
func (c *EvalConfig) EvalNames() []string {
	names := make([]string, 0, len(c.Evals))
	for _, e := range c.Evals {
		names = append(names, e.Name)
	}
	return names
}

// Eval returns the named eval.
//
// An empty name is only answered when the file declares exactly one, because
// guessing which eval a command meant is the kind of mistake that is noticed
// only after it has run.
func (c *EvalConfig) Eval(name string) (*Eval, error) {
	if name == "" {
		switch len(c.Evals) {
		case 0:
			return nil, messages.NoEvalsDeclared()
		case 1:
			return &c.Evals[0], nil
		default:
			return nil, messages.SeveralEvalsDeclared(len(c.Evals), c.EvalNames())
		}
	}

	for i := range c.Evals {
		if c.Evals[i].Name == name {
			return &c.Evals[i], nil
		}
	}
	return nil, messages.EvalNotDeclared(name, c.EvalNames())
}

// HasEval reports whether the named eval is declared. Unlike Eval it never
// falls back to "the only one", so callers checking for a collision cannot
// match a differently named entry.
func (c *EvalConfig) HasEval(name string) bool {
	for i := range c.Evals {
		if c.Evals[i].Name == name {
			return true
		}
	}
	return false
}

// RemoveEval drops the named eval, reporting whether it was there.
func (c *EvalConfig) RemoveEval(name string) bool {
	for i := range c.Evals {
		if c.Evals[i].Name == name {
			c.Evals = append(c.Evals[:i], c.Evals[i+1:]...)
			return true
		}
	}
	return false
}

// DatasetDeclaration returns the catalog entry an eval's `dataset:` names.
func (c *EvalConfig) DatasetDeclaration(name string) (*DatasetDecl, bool) {
	for i := range c.Datasets {
		if c.Datasets[i].Name == name {
			return &c.Datasets[i], true
		}
	}
	return nil, false
}

// EvaluatorDeclaration returns the catalog entry an evaluator reference names.
func (c *EvalConfig) EvaluatorDeclaration(name string) (*EvaluatorDecl, bool) {
	for i := range c.Evaluators {
		if c.Evaluators[i].Name == name {
			return &c.Evaluators[i], true
		}
	}
	return nil, false
}

// CustomEvaluators are the catalog entries this configuration owns -- the ones
// carrying a rubric, either as a local source or written out under
// `definition`, published before the evals that name them.
func (c *EvalConfig) CustomEvaluators() []EvaluatorDecl {
	var owned []EvaluatorDecl
	for _, decl := range c.Evaluators {
		if decl.Source == "" && decl.Definition == nil {
			continue
		}
		owned = append(owned, decl)
	}
	return owned
}

// LocalDatasets are the catalog entries carrying a file to upload.
func (c *EvalConfig) LocalDatasets() []DatasetDecl {
	var owned []DatasetDecl
	for _, decl := range c.Datasets {
		if decl.File == "" {
			continue
		}
		owned = append(owned, decl)
	}
	return owned
}

// Validate checks the invariants the provider relies on before it calls the
// service, so failures surface as config errors rather than opaque 4xx.
func (c *EvalConfig) Validate() error {
	return c.validate(true)
}

// ValidateForLookup checks what resolving a declaration by name depends on: a
// readable set of catalogs, and a name that is present and not shared.
//
// What an eval says about itself is left to deploying it, and to the run door,
// which applies the same rules to the entry the run is actually about. Checking
// it here stranded commands that had already been told which eval they meant --
// `run list --eval <name>` refused to list anything because a different entry
// was malformed, and the way out was to hand-edit a file the error did not
// mention.
//
// The catalogs stay, because they are the file's shared half: a duplicate
// dataset name makes the lookup this method exists to serve ambiguous, and no
// declaration can be read against a catalog that does not parse into one.
func (c *EvalConfig) ValidateForLookup() error {
	return c.validate(false)
}

func (c *EvalConfig) validate(deploying bool) error {
	if err := c.validateCatalogs(); err != nil {
		return err
	}
	// A catalog with no eval is what `generate` leaves behind, and it stays
	// that way until `init` wires one. Refusing it on the way to a lookup
	// stranded `run --eval <id>` in a project where `generate` ran first, over
	// the absence of a declaration the id did not need. `Eval` answers for the
	// case that does need one.
	if deploying && len(c.Evals) == 0 {
		return messages.AtLeastOneEvalRequired()
	}

	seen := map[string]bool{}
	substance := map[string]string{}
	for i, eval := range c.Evals {
		if eval.Name == "" {
			return messages.EvalNameRequired(i)
		}
		if seen[eval.Name] {
			return messages.DuplicateEvalName(i, eval.Name)
		}
		seen[eval.Name] = true

		if !deploying {
			// Only what resolving a declaration by name depends on, which is a
			// name that is present and not shared. Everything an eval says about
			// itself is checked on the way to deploying it, and again at the run
			// door on the entry the run is actually about. Enforcing it here
			// stranded commands that had already been told which eval they
			// meant: one malformed entry stopped `run list --eval <another>`
			// listing anything, and the way out was to hand-edit a file the
			// error did not mention.
			continue
		}

		if err := c.validateEval(i, eval); err != nil {
			return err
		}

		// Two evals that differ only by name are indistinguishable once
		// deployed: the environment records an id against each eval's substance
		// so a renamed declaration can find what it already deployed, and a
		// shared substance makes that lookup ambiguous.
		digest, err := FingerprintGroup(eval)
		if err != nil {
			return err
		}
		if first, clash := substance[digest]; clash {
			return messages.EvalsIdenticalApartFromName(i, eval.Name, first)
		}
		substance[digest] = eval.Name
	}
	return nil
}

func (c *EvalConfig) validateCatalogs() error {
	datasets := map[string]bool{}
	for i, d := range c.Datasets {
		if d.Name == "" {
			return messages.DatasetNameRequired(i)
		}
		if datasets[d.Name] {
			return messages.DuplicateDatasetName(i, d.Name)
		}
		datasets[d.Name] = true
	}

	evaluators := map[string]bool{}
	for i, e := range c.Evaluators {
		if e.Name == "" {
			return messages.EvaluatorNameRequired(i)
		}
		if evaluators[e.Name] {
			return messages.DuplicateEvaluatorName(i, e.Name)
		}
		evaluators[e.Name] = true

		if strings.HasPrefix(e.Name, evalcore.BuiltinPrefix) {
			return messages.BuiltinNeedsNoCatalogEntry(i, e.Name)
		}
		// The service assigns an evaluator's version on publish, so a declared
		// one cannot be honoured alongside a source: the upload lands on
		// whatever comes next and the eval binds that, leaving the pin
		// describing a version nothing uses.
		if e.Source != "" && e.Version != "" {
			return messages.EvaluatorVersionWithSource(i, e.Name)
		}
		if e.Definition != nil {
			if e.Source != "" {
				return messages.EvaluatorRubricDeclaredTwice(i, e.Name)
			}
			if e.Version != "" {
				return messages.EvaluatorVersionWithDefinition(i, e.Name)
			}
		}
	}
	return nil
}

func (c *EvalConfig) validateEval(i int, eval Eval) error {
	if err := ValidateRunnable(&eval); err != nil {
		return messages.InEvalAt(i, eval.Name, err)
	}
	if eval.Dataset != "" {
		if _, ok := c.DatasetDeclaration(eval.Dataset); !ok {
			return messages.DatasetNotInDatasetsCatalog(i, eval.Name, eval.Dataset)
		}
	}

	if len(eval.Evaluators) == 0 {
		return messages.AtLeastOneEvaluatorRequired(i, eval.Name)
	}
	criteria := map[string]bool{}
	for j, ref := range eval.Evaluators {
		if ref.Evaluator == "" {
			return messages.EvaluatorFieldRequired(i, j)
		}
		// The criterion name is what identifies a result row, so two rows that
		// cannot be told apart are refused here rather than in the results.
		criterion := ref.CriterionName()
		if criteria[criterion] {
			return messages.DuplicateCriterion(i, j, criterion)
		}
		criteria[criterion] = true

		if ref.IsBuiltin() {
			continue
		}
		if _, ok := c.EvaluatorDeclaration(ref.Evaluator); !ok {
			return messages.EvaluatorNotInCatalog(i, j, ref.Evaluator)
		}
	}

	return nil
}
