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
	assert.True(t, pinsAgentTarget(target),
		"the schema has to pin the only target type a simulation can talk to, got %v", target)

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

// pinsAgentTarget reports whether the target constraint admits only the agent
// type, whether it says so directly or as one branch of a oneOf that also
// allows a $ref to a file.
func pinsAgentTarget(target map[string]any) bool {
	if props, ok := target["properties"].(map[string]any); ok {
		if typ, ok := props["type"].(map[string]any); ok && typ["const"] == "agent" {
			return true
		}
	}
	branches, ok := target["oneOf"].([]any)
	if !ok {
		return false
	}
	for _, branch := range branches {
		if b, ok := branch.(map[string]any); ok && pinsAgentTarget(b) {
			return true
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
