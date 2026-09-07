// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"testing"

	"azureaieval/internal/project"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// noPromptCmd is a command with the flag the real tree inherits from the root.
func noPromptCmd(t *testing.T, set bool) *cobra.Command {
	t.Helper()
	cmd := &cobra.Command{}
	cmd.Flags().Bool("no-prompt", set, "")
	return cmd
}

// An explicit --dataset is the answer, and nothing is asked about it.
func TestResolveDataset_FlagWins(t *testing.T) {
	cfg := &project.EvalConfig{Datasets: []project.DatasetDecl{
		{Name: "a"}, {Name: "b"},
	}}
	got, err := resolveDataset(noPromptCmd(t, true), cfg, "prod-golden")
	require.NoError(t, err)
	assert.Equal(t, "prod-golden", got,
		"a flag the caller passed is not something to ask about, even with two declared")
}

// One declaration is not a guess, so it is used without a question -- and
// without a flag, which is what keeps `azd ai eval init` a bare command in the
// project it was scaffolded for.
func TestResolveDataset_TheOnlyDeclaredOne(t *testing.T) {
	cfg := &project.EvalConfig{Datasets: []project.DatasetDecl{{Name: "golden"}}}

	got, err := resolveDataset(noPromptCmd(t, true), cfg, "")
	require.NoError(t, err)
	assert.Equal(t, "golden", got, "settled the same way with nobody to ask")

	got, err = resolveDataset(noPromptCmd(t, false), cfg, "")
	require.NoError(t, err)
	assert.Equal(t, "golden", got, "and the same way with someone to ask")
}

// With nobody to ask, the two gaps are named rather than filled.
//
// They used to be filled by declaring a dataset that generation would produce
// later, which wrote an eval nothing satisfied: the deploy shipped the rest of
// the project and then failed on rows nothing had generated.
func TestResolveDataset_UnderNoPromptNamesTheFlag(t *testing.T) {
	none, err := resolveDataset(noPromptCmd(t, true), &project.EvalConfig{}, "")
	require.Error(t, err)
	assert.Empty(t, none)
	assert.Contains(t, err.Error(), "--dataset",
		"a refusal in a pipeline has to say what would have fixed it")

	several := &project.EvalConfig{Datasets: []project.DatasetDecl{
		{Name: "nightly"}, {Name: "prod"},
	}}
	_, err = resolveDataset(noPromptCmd(t, true), several, "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "nightly")
	assert.Contains(t, err.Error(), "prod",
		"and has to name the candidates it could not choose between")
}
