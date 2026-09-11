// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"testing"

	"github.com/spf13/pflag"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// `--evaluator a,b` used to be accepted whole, writing one evaluator literally
// named "a,b" into the config. init exited 0 and the failure surfaced two
// commands later at create, naming a value passed to a different command.
func TestEvaluatorFlagSplitsOnCommas(t *testing.T) {
	cmd := newInitCommand()
	require.NoError(t, cmd.Flags().Parse([]string{
		"--evaluator", "builtin.task_adherence,builtin.relevance",
	}))

	got, err := cmd.Flags().GetStringSlice("evaluator")
	require.NoError(t, err)
	assert.Equal(t, []string{"builtin.task_adherence", "builtin.relevance"}, got,
		"a comma separates references; it is never part of an evaluator name")
}

// The sibling repeatable flag already split on commas, and two flags documented
// the same way behaving differently is visible only to someone who knows pflag.
func TestRepeatableFlagsAgreeOnCommas(t *testing.T) {
	typeOf := func(cmd *pflag.FlagSet, name string) string {
		f := cmd.Lookup(name)
		require.NotNilf(t, f, "%s is not registered", name)
		return f.Value.Type()
	}

	assert.Equal(t, "stringSlice", typeOf(newInitCommand().Flags(), "evaluator"))
	assert.Equal(t, typeOf(newGenerateCommand().Flags(), "from"),
		typeOf(newInitCommand().Flags(), "evaluator"),
		"both are repeatable reference lists, so they must split alike")
}

// Repeating the flag still works, because a comma list is an addition rather
// than a replacement.
func TestEvaluatorFlagStillRepeats(t *testing.T) {
	cmd := newInitCommand()
	require.NoError(t, cmd.Flags().Parse([]string{
		"--evaluator", "builtin.task_adherence", "--evaluator", "builtin.relevance",
	}))

	got, err := cmd.Flags().GetStringSlice("evaluator")
	require.NoError(t, err)
	assert.Equal(t, []string{"builtin.task_adherence", "builtin.relevance"}, got)
}

// A stray comma leaves an empty reference, which would otherwise be written to
// the config and looked up as "".
func TestEvaluatorRefsRejectWhatCannotNameAnEvaluator(t *testing.T) {
	require.NoError(t, validateEvaluatorRefs([]string{"builtin.relevance", "my-rubric"}))

	err := validateEvaluatorRefs([]string{"builtin.relevance", ""})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--evaluator")

	err = validateEvaluatorRefs([]string{"two words"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "two words")
}
