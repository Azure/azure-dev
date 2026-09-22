// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"encoding/json"
	"io"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The schema is what an editor checks a configuration against before the CLI
// ever sees it, so a rule the CLI enforces and the schema does not is a rule a
// reader first learns about from a failed deploy.
//
// validateSimulation refuses a simulation that is not at conversation level,
// that targets a model, that also declares source:, or that is sampled. The
// conditional below has to refuse the same four, and it accepted the first two
// until it was pinned.
func TestTheSchemaRefusesWhatValidateSimulationRefuses(t *testing.T) {
	t.Parallel()

	branch := simulationConditional(t)

	then, ok := branch["then"].(map[string]any)
	require.True(t, ok, "the simulation conditional has no `then`")

	properties, ok := then["properties"].(map[string]any)
	require.True(t, ok, "the simulation conditional constrains no properties")

	// A simulation has no turn to score before the conversation exists.
	assert.Equal(t, map[string]any{"const": "conversation"}, properties["evaluation_level"],
		"the schema has to pin the only level a simulation can run at")

	// And nothing to hold the conversation with if the target is a model.
	target, ok := properties["target"].(map[string]any)
	require.True(t, ok, "the simulation conditional leaves target: unconstrained")
	assert.True(t, refusesAModelTarget(target),
		"the schema has to refuse the only target type a simulation cannot talk to, got %v", target)

	// It refuses without requiring. An omitted type already means agent -- the
	// base Target schema says so and validateSimulation accepts it -- and a
	// $ref target carries no type at all, so demanding one here would reject
	// two shapes the CLI runs happily.
	assert.NotContains(t, target, "required",
		"narrowing the type must not start requiring it")
	assert.NotContains(t, target, "oneOf",
		"a $ref target satisfies the unconstrained branch too, so oneOf is ambiguous here")

	// A simulation creates its conversations; a source collects ones that
	// already happened. Declaring both runs one and ignores the other.
	assert.Equal(t, false, properties["source"], "source: has to be refused alongside simulation:")

	// The run references the registered seed dataset by id, so a cap cannot be
	// applied to it.
	assert.Equal(t, false, properties["max_samples"], "max_samples: has to be refused alongside simulation:")

	required, ok := then["required"].([]any)
	require.True(t, ok, "the simulation conditional requires nothing")
	for _, key := range []string{"dataset", "target", "evaluation_level"} {
		assert.Contains(t, required, key, "a simulation cannot run without %s:", key)
	}
}

// refusesAModelTarget reports whether the target constraint pins the type to
// agent when a type is stated, whether it says so directly or inside a
// combinator.
func refusesAModelTarget(target map[string]any) bool {
	if props, ok := target["properties"].(map[string]any); ok {
		if typ, ok := props["type"].(map[string]any); ok && typ["const"] == "agent" {
			return true
		}
	}
	for _, key := range []string{"anyOf", "oneOf", "allOf"} {
		branches, ok := target[key].([]any)
		if !ok {
			continue
		}
		for _, branch := range branches {
			if b, ok := branch.(map[string]any); ok && refusesAModelTarget(b) {
				return true
			}
		}
	}
	return false
}

// simulationConditional returns the `allOf` branch whose `if` fires on a
// declared simulation.
func simulationConditional(t *testing.T) map[string]any {
	t.Helper()

	root, err := os.OpenRoot("../..")
	require.NoError(t, err)
	defer func() { _ = root.Close() }()

	f, err := root.Open("schemas/azure.ai.eval.json")
	require.NoError(t, err)
	body, err := io.ReadAll(f)
	_ = f.Close()
	require.NoError(t, err)

	var schema map[string]any
	require.NoError(t, json.Unmarshal(body, &schema))

	definitions, ok := schema["definitions"].(map[string]any)
	require.True(t, ok, "the schema has no definitions")
	eval, ok := definitions["Eval"].(map[string]any)
	require.True(t, ok, "the schema no longer defines Eval; retarget this test rather than deleting it")

	branches, ok := eval["allOf"].([]any)
	require.True(t, ok, "Eval declares no conditionals")

	for _, raw := range branches {
		branch, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		cond, ok := branch["if"].(map[string]any)
		if !ok {
			continue
		}
		required, ok := cond["required"].([]any)
		if !ok || len(required) != 1 || required[0] != "simulation" {
			continue
		}
		return branch
	}

	t.Fatal("no conditional fires on a declared simulation")
	return nil
}
