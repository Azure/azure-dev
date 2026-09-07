// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// evalsForTargets builds a configuration declaring one eval per target given.
func evalsForTargets(targets ...string) string {
	var body strings.Builder
	body.WriteString("evals:\n")
	for i, target := range targets {
		body.WriteString("  - name: eval" + string(rune('a'+i)) + "\n")
		body.WriteString("    target:\n")
		body.WriteString("      type: agent\n")
		body.WriteString("      name: " + target + "\n")
	}
	return body.String()
}

// A configuration can declare evals for more than one agent. Taking the first
// one sent a billed generation the wrong agent's instructions and model, and
// said nothing about having chosen.
func TestGenerateRefusesToGuessBetweenDeclaredTargets(t *testing.T) {
	dir := writeEvalConfig(t, evalsForTargets("checkout-agent", "support-agent"))

	target, err := declaredTarget(dir)

	require.Error(t, err, "two agents is not something to pick from silently")
	assert.Empty(t, target)
	assert.Contains(t, err.Error(), "checkout-agent")
	assert.Contains(t, err.Error(), "support-agent",
		"the refusal has to name both, or the caller cannot act on it")
}

// One agent across several evals is still one agent, so repetition is not
// ambiguity.
func TestGenerateInfersASingleTargetEvenWhenRepeated(t *testing.T) {
	dir := writeEvalConfig(t, evalsForTargets("checkout-agent", "checkout-agent"))

	target, err := declaredTarget(dir)

	require.NoError(t, err)
	assert.Equal(t, "checkout-agent", target)
}

// A bare directory has nothing to read, and generation from an instruction
// alone is a supported path, so absence is not an error.
func TestGenerateTreatsNoConfigurationAsNoTarget(t *testing.T) {
	target, err := declaredTarget(t.TempDir())

	require.NoError(t, err)
	assert.Empty(t, target)
}

// evalForModelTarget declares an eval whose target is a model deployment.
func evalForModelTarget(name string) string {
	return "evals:\n" +
		"  - name: model-eval\n" +
		"    target:\n" +
		"      type: model\n" +
		"      name: " + name + "\n"
}

// A model target names a deployment, not an agent. Inferring one handed
// GetAgent the deployment name, and the miss came back as a missing
// --generation-model -- an error about a flag the caller had no reason to pass.
func TestGenerateDoesNotInferAModelTargetAsAnAgent(t *testing.T) {
	dir := writeEvalConfig(t, evalForModelTarget("gpt-5.6-luna"))

	target, err := declaredTarget(dir)

	require.NoError(t, err)
	assert.Empty(t, target, "a deployment name is not an agent to generate from")
}

// And a file carrying one of each is not ambiguous: there is exactly one agent
// in it. Counting the model as a second target refused a configuration that
// named its agent perfectly well.
func TestGenerateInfersTheAgentBesideAModelTarget(t *testing.T) {
	dir := writeEvalConfig(t,
		evalsForTargets("checkout-agent")+
			"  - name: model-eval\n    target:\n      type: model\n      name: gpt-5.6-luna\n")

	target, err := declaredTarget(dir)

	require.NoError(t, err)
	assert.Equal(t, "checkout-agent", target)
}

// An absent type is an agent, which is what the schema defaults to and what
// every configuration written before the field existed relies on.
func TestGenerateInfersATargetWithNoDeclaredType(t *testing.T) {
	dir := writeEvalConfig(t, "evals:\n  - name: eval\n    target:\n      name: checkout-agent\n")

	target, err := declaredTarget(dir)

	require.NoError(t, err)
	assert.Equal(t, "checkout-agent", target)
}

// --target is the caller saying which one they meant, so an ambiguous
// configuration must not refuse them.
func TestAnExplicitTargetIsNotRefusedByAnAmbiguousConfiguration(t *testing.T) {
	dir := writeEvalConfig(t, evalsForTargets("checkout-agent", "support-agent"))

	plan, err := resolvePlan(&generateFlags{
		target:      "support-agent",
		instruction: "score the answers",
		path:        dir,
	}, "ds", "datasets")

	require.NoError(t, err, "the caller already said which agent this is for")
	assert.Equal(t, "support-agent", plan.Agent)
}
