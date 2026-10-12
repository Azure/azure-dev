// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"reflect"
	"strings"
	"testing"

	"azureaieval/internal/pkg/evalcore"

	"github.com/stretchr/testify/assert"
)

// azure.eval.yaml is the file a user writes, so its keys are the contract. They are
// pinned whole rather than exercised through fixtures: a fixture that stops
// parsing says a test broke, not that a published key was renamed under
// everyone who already wrote one.
//
// The spec's configuration model is the source for every list here. Changing
// one means changing both.

// yamlKeys reads the yaml tag names off a struct, in declaration order.
func yamlKeys(t *testing.T, v any) []string {
	t.Helper()
	typ := reflect.TypeOf(v)
	for typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}

	var keys []string
	for field := range typ.Fields() {
		tag := field.Tag.Get("yaml")
		if tag == "" || tag == "-" {
			continue
		}
		name := strings.Split(tag, ",")[0]
		if name == "" {
			continue
		}
		keys = append(keys, name)
	}
	return keys
}

// The top level: catalogs first, then the evals defined over them.
func TestEvalConfigKeys(t *testing.T) {
	assert.Equal(t, []string{"datasets", "evaluators", "evals"},
		yamlKeys(t, EvalConfig{}),
		"the top-level shape is the spec's configuration model")
}

// An eval names what it evaluates, what it reads, and how to grade it.
//
// No `$ref`: the directive is resolved before this decodes, and the commands
// that read the file as written answer it from the document instead.
func TestEvalKeys(t *testing.T) {
	assert.ElementsMatch(t,
		[]string{
			"name", "id", "description", "dataset", "source",
			"evaluationLevel", "maxSamples", "evaluators", "target", "simulation",
		},
		yamlKeys(t, Eval{}))
}

// The simulation block is what selects the simulation run type, so its surface
// is pinned the same way. Its presence is the signal; there is no mode field.
func TestSimulationKeys(t *testing.T) {
	assert.ElementsMatch(t,
		[]string{"model", "numConversations", "maxTurns"},
		yamlKeys(t, Simulation{}))
}

// Every entry in an eval's evaluators: list is a map keyed evaluator:.
func TestEvaluatorRefKeys(t *testing.T) {
	assert.ElementsMatch(t,
		[]string{"evaluator", "name", "version", "initializationParameters", "dataMapping"},
		yamlKeys(t, evalcore.EvaluatorRef{}),
		"the spec tabulates these five")
}

// source: says where rows come from when they are not a dataset.
func TestSourceDeclKeys(t *testing.T) {
	assert.ElementsMatch(t,
		[]string{
			"type", "lookbackHours", "maxTraces", "agentName", "responseIds", "maxTurns",
			"agentVersion", "startTime", "endTime",
		},
		yamlKeys(t, SourceDecl{}))
}

// The catalogs are named, reusable assets. A dataset says where its rows come
// from. An evaluator says where its rubric is -- named as a file, or written
// out under `definition`, which a `$ref` may fill from a file of its own -- and
// carries the catalog metadata a republish would otherwise lose.
func TestCatalogKeys(t *testing.T) {
	assert.ElementsMatch(t,
		[]string{"name", "file", "version", "tags"}, yamlKeys(t, DatasetDecl{}))
	assert.ElementsMatch(t,
		[]string{
			"name", "source", "version", "definition",
			"displayName", "categories", "supportedEvaluationLevels",
		},
		yamlKeys(t, EvaluatorDecl{}))
}

// The azure.yaml convention is camelCase, so the file's own keys are all one
// style: a reader never has to remember an exception. The service's vocabulary
// that a key holds (an evaluator's initialization parameters, a rubric) keeps
// its own spelling, which is why only the keys of these shapes are checked.
func TestEveryKeyIsCamelCase(t *testing.T) {
	shapes := map[string]any{
		"EvalConfig":    EvalConfig{},
		"Eval":          Eval{},
		"SourceDecl":    SourceDecl{},
		"Simulation":    Simulation{},
		"Target":        Target{},
		"DatasetDecl":   DatasetDecl{},
		"EvaluatorDecl": EvaluatorDecl{},
		"EvaluatorRef":  evalcore.EvaluatorRef{},
	}

	for name, shape := range shapes {
		for _, key := range yamlKeys(t, shape) {
			assert.Truef(t, key[0] >= 'a' && key[0] <= 'z',
				"%s.%s does not start in lower case; keys are camelCase", name, key)
			assert.NotContainsf(t, key, "_",
				"%s.%s uses an underscore; keys are camelCase", name, key)
			assert.NotContainsf(t, key, "-",
				"%s.%s uses a dash; keys are camelCase", name, key)
		}
	}
}

// The json tags on these shapes are not the file's keys: they are the encoding
// an eval's change-detection fingerprint is computed from, and that fingerprint
// is recorded in the azd environment to decide whether an immutable eval has to
// be recreated. Renaming one would make every eval already published look
// changed, so the names are pinned as they were.
func TestJSONKeysStayAsFingerprinted(t *testing.T) {
	jsonKeys := func(v any) []string {
		typ := reflect.TypeOf(v)
		var keys []string
		for field := range typ.Fields() {
			if name := strings.Split(field.Tag.Get("json"), ",")[0]; name != "" && name != "-" {
				keys = append(keys, name)
			}
		}
		return keys
	}
	assert.ElementsMatch(t, []string{
		"name", "id", "description", "dataset", "source",
		"evaluation_level", "max_samples", "evaluators", "target", "simulation",
	}, jsonKeys(Eval{}))
	assert.ElementsMatch(t, []string{
		"type", "lookback_hours", "max_traces", "agent_name", "response_ids", "max_turns",
		"agent_version", "start_time", "end_time",
	}, jsonKeys(SourceDecl{}))
	assert.ElementsMatch(t, []string{"model", "num_conversations", "max_turns"}, jsonKeys(Simulation{}))
	assert.ElementsMatch(t, []string{
		"evaluator", "name", "version", "initialization_parameters", "data_mapping",
	}, jsonKeys(evalcore.EvaluatorRef{}))
}

// `target:` always means invoke and `source:` always means where rows come
// from. A trace-backed eval has no target, which is what agentName under
// source: exists to say.
func TestTargetAndSourceAreDistinct(t *testing.T) {
	assert.ElementsMatch(t, []string{"type", "name"}, yamlKeys(t, Target{}),
		"the spec's target: is a type and a name; a version there would pin the "+
			"agent an eval invokes, which nothing asks for")

	assert.Contains(t, yamlKeys(t, SourceDecl{}), "agentName",
		"a trace run filters by agent rather than invoking one")
	assert.NotContains(t, yamlKeys(t, Target{}), "agentName",
		"the target already names what it invokes")
}
