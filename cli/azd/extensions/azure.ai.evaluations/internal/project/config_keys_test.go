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

// eval.yaml is the file a user writes, so its keys are the contract. They are
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
func TestEvalKeys(t *testing.T) {
	assert.ElementsMatch(t,
		[]string{
			"name", "id", "description", "dataset", "source",
			"evaluation_level", "max_samples", "evaluators", "target",
		},
		yamlKeys(t, Eval{}))
}

// Every entry in an eval's evaluators: list is a map keyed evaluator:, and the
// spec gives that map exactly five keys.
func TestEvaluatorRefKeys(t *testing.T) {
	assert.ElementsMatch(t,
		[]string{"evaluator", "name", "version", "initialization_parameters", "data_mapping"},
		yamlKeys(t, evalcore.EvaluatorRef{}),
		"the spec tabulates these five; a sixth is a promise it does not make")
}

// source: says where rows come from when they are not a dataset.
func TestSourceDeclKeys(t *testing.T) {
	assert.ElementsMatch(t,
		[]string{"type", "lookback_hours", "max_traces", "agent_name", "response_ids", "max_turns"},
		yamlKeys(t, SourceDecl{}))
}

// The catalogs are named, reusable assets: a name and where it comes from.
func TestCatalogKeys(t *testing.T) {
	assert.ElementsMatch(t, []string{"name", "source", "version"}, yamlKeys(t, DatasetDecl{}))
	assert.ElementsMatch(t, []string{"name", "source", "version"}, yamlKeys(t, EvaluatorDecl{}))
}

// The spec's casing table: eval.yaml uses the API's snake_case throughout, so
// a camelCase key would be the one place a reader has to remember an exception.
func TestEveryKeyIsSnakeCase(t *testing.T) {
	shapes := map[string]any{
		"EvalConfig":    EvalConfig{},
		"Eval":          Eval{},
		"SourceDecl":    SourceDecl{},
		"Target":        Target{},
		"DatasetDecl":   DatasetDecl{},
		"EvaluatorDecl": EvaluatorDecl{},
		"EvaluatorRef":  evalcore.EvaluatorRef{},
	}

	for name, shape := range shapes {
		for _, key := range yamlKeys(t, shape) {
			assert.Equalf(t, strings.ToLower(key), key,
				"%s.%s is not snake_case; eval.yaml uses the API's spelling throughout", name, key)
			assert.NotContainsf(t, key, "-",
				"%s.%s uses a dash; the API's convention is underscores", name, key)
		}
	}
}

// `target:` always means invoke and `source:` always means where rows come
// from. A trace-backed eval has no target, which is what agent_name under
// source: exists to say.
func TestTargetAndSourceAreDistinct(t *testing.T) {
	assert.ElementsMatch(t, []string{"type", "name"}, yamlKeys(t, Target{}),
		"the spec's target: is a type and a name; a version there would pin the "+
			"agent an eval invokes, which nothing asks for")

	assert.Contains(t, yamlKeys(t, SourceDecl{}), "agent_name",
		"a trace run filters by agent rather than invoking one")
	assert.NotContains(t, yamlKeys(t, Target{}), "agent_name",
		"the target already names what it invokes")
}
