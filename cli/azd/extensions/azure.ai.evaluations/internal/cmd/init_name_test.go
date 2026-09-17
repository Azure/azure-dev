// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"testing"

	"azureaieval/internal/project"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func configWithEvals(names ...string) *project.EvalConfig {
	cfg := &project.EvalConfig{}
	for _, n := range names {
		cfg.Evals = append(cfg.Evals, project.Eval{Name: n})
	}
	return cfg
}

// The spec's default: init proposes a name, and proposes one the file can
// still accept rather than failing on its own suggestion.
func TestEvalName_TakesTheSuggestionWhenNoneWasGiven(t *testing.T) {
	cfg := configWithEvals("support-agent-trace-eval")
	suggested := uniqueEvalName(cfg, defaultEvalName("support-agent", initSourceTraces))
	require.Equal(t, "support-agent-trace-eval-2", suggested)

	got, err := resolveEvalName(
		noPromptCmd(t, true), cfg, "evals/azure.eval.yaml", "", suggested)

	require.NoError(t, err)
	assert.Equal(t, "support-agent-trace-eval-2", got)
}

// A name the caller gave and the file has room for is the answer, and asking
// about it would read their own flag back to them.
func TestEvalName_ExplicitAndFreeIsTakenAsGiven(t *testing.T) {
	got, err := resolveEvalName(
		noPromptCmd(t, true), configWithEvals("other"), "evals/azure.eval.yaml",
		"nightly", "support-agent-trace-eval")

	require.NoError(t, err)
	assert.Equal(t, "nightly", got)
}

// Nobody is there to be asked again, so a taken name ends the command -- which
// is the spec's `--no-prompt` rule, and now the only path that hard-fails.
func TestEvalName_DuplicateUnderNoPromptIsRejected(t *testing.T) {
	_, err := resolveEvalName(
		noPromptCmd(t, true), configWithEvals("nightly"), "evals/azure.eval.yaml",
		"nightly", "support-agent-trace-eval")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "nightly")
	assert.Contains(t, err.Error(), "already exists")
	assert.NotContains(t, err.Error(), "--force",
		"init no longer has a flag that replaces an eval, so it must not suggest one")
}

// The service's character set is applied locally, so a name it would answer
// with a wrapped 400 is refused before anything is written.
func TestEvalName_RefusesWhatTheServiceWouldRefuse(t *testing.T) {
	for _, name := range []string{"my eval", "a/b", "caf\u00e9-eval", "../escape"} {
		t.Run(name, func(t *testing.T) {
			_, err := resolveEvalName(
				noPromptCmd(t, true), configWithEvals(), "evals/azure.eval.yaml",
				name, "support-agent-trace-eval")

			require.Errorf(t, err, "%q must not reach the file", name)
		})
	}
}

// An empty --name is not a name, so the suggestion stands.
func TestEvalName_EmptyExplicitFallsBackToTheSuggestion(t *testing.T) {
	got, err := resolveEvalName(
		noPromptCmd(t, true), configWithEvals(), "evals/azure.eval.yaml",
		"", "support-agent-dataset-eval")

	require.NoError(t, err)
	assert.Equal(t, "support-agent-dataset-eval", got)
}

// What the re-ask offers has to be usable, or the reader is handed back the
// name they were just refused.
func TestEvalName_TheRetrySuggestionIsFree(t *testing.T) {
	cfg := configWithEvals("nightly", "nightly-2")

	next := uniqueEvalName(cfg, trimEvalName("nightly", 0))

	assert.Equal(t, "nightly-3", next)
	assert.NoError(t, evalNameUsable(cfg, "evals/azure.eval.yaml", next))
}

// evalNameUsable is what both the prompt and the no-prompt path decide on, so
// the two cannot drift into disagreeing about the same name.
func TestEvalNameUsable_SeparatesTakenFromMalformed(t *testing.T) {
	cfg := configWithEvals("nightly")

	require.NoError(t, evalNameUsable(cfg, "c.yaml", "fresh"))

	taken := evalNameUsable(cfg, "c.yaml", "nightly")
	require.Error(t, taken)
	assert.Contains(t, taken.Error(), "already exists")

	malformed := evalNameUsable(cfg, "c.yaml", "not a name")
	require.Error(t, malformed)
	assert.Contains(t, malformed.Error(), "not a usable eval name")
}
