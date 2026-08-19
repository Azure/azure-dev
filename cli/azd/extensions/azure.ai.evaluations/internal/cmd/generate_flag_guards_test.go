// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func runGenerate(t *testing.T, args ...string) error {
	t.Helper()
	root := NewRootCommand()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs(append([]string{"generate"}, args...))
	return root.ExecuteContext(context.Background())
}

// Each of these is read while building one kind of artifact and ignored while
// building the other, so given for the wrong one they were accepted and
// dropped -- `--evaluator --max-samples 50` produced a rubric and said nothing
// about the 50, and `--dataset --trace-days 7` a dataset and nothing about the
// seven days.
func TestFlagsThatCannotApplyAreRefused(t *testing.T) {
	cases := []struct {
		flag     string
		narrowed string
		args     []string
	}{
		{"--from", "--evaluator",
			[]string{"--evaluator", "--evaluator-name", "ev", "--from", "prompt"}},
		{"--max-samples", "--evaluator",
			[]string{"--evaluator", "--evaluator-name", "ev", "--max-samples", "50"}},
		{"--dataset-name", "--evaluator",
			[]string{"--evaluator", "--evaluator-name", "ev", "--dataset-name", "ds"}},
		{"--trace-days", "--dataset",
			[]string{"--dataset", "--dataset-name", "ds", "--trace-days", "7"}},
		{"--evaluator-name", "--dataset",
			[]string{"--dataset", "--dataset-name", "ds", "--evaluator-name", "ev"}},
	}

	for _, c := range cases {
		err := runGenerate(t, c.args...)

		require.Errorf(t, err, "%s cannot apply under %s", c.flag, c.narrowed)
		assert.Contains(t, err.Error(), c.flag)
		assert.Containsf(t, err.Error(), c.narrowed,
			"the refusal has to name the flag that made %s inapplicable", c.flag)
	}
}

// Generating both is the default, and every one of those flags applies then.
// Without this the guard above could be satisfied by refusing them always.
func TestNoFlagIsRefusedWhenBothArtifactsAreGenerated(t *testing.T) {
	err := runGenerate(t,
		"--dataset-name", "ds", "--evaluator-name", "ev",
		"--from", "prompt", "--max-samples", "50", "--trace-days", "7")

	if err != nil {
		assert.NotContains(t, err.Error(), "has no effect on what",
			"generating both artifacts makes every one of these flags applicable")
	}
}

// And each flag is still accepted for the artifact it does affect.
func TestEachFlagIsAcceptedForItsOwnArtifact(t *testing.T) {
	forDataset := runGenerate(t,
		"--dataset", "--dataset-name", "ds", "--from", "prompt", "--max-samples", "50")
	if forDataset != nil {
		assert.NotContains(t, forDataset.Error(), "has no effect on what")
	}

	forEvaluator := runGenerate(t,
		"--evaluator", "--evaluator-name", "ev", "--trace-days", "7")
	if forEvaluator != nil {
		assert.NotContains(t, forEvaluator.Error(), "has no effect on what")
	}
}

// Zero already means "seed the rubric from no traces", so a negative window has
// nothing left to mean. It was accepted and read as zero, quietly producing a
// rubric with none of the trace seeding that was asked for.
func TestNegativeTraceDaysIsRefused(t *testing.T) {
	err := runGenerate(t, "--evaluator", "--evaluator-name", "ev", "--trace-days", "-5")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "--trace-days")
	assert.Contains(t, err.Error(), "-5", "the value that was rejected")
}

// Zero is a real answer and has to keep working.
func TestZeroTraceDaysIsAccepted(t *testing.T) {
	err := runGenerate(t, "--evaluator", "--evaluator-name", "ev", "--trace-days", "0")

	if err != nil {
		assert.NotContains(t, err.Error(), "--trace-days",
			"0 is how a caller says to read no traces")
	}
}

// --no-wait returns as soon as the job is submitted, so there is no artifact to
// place. Accepting both left the caller waiting for a file never coming.
func TestOutputDirWithNoWaitIsRefused(t *testing.T) {
	err := runGenerate(t,
		"--dataset", "--dataset-name", "ds", "--no-wait", "--output-dir", t.TempDir())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "--output-dir")
	assert.Contains(t, err.Error(), "--no-wait")
	assert.Contains(t, err.Error(), "job show",
		"the refusal has to name how to collect the artifact later")
}

// An output directory without --no-wait is what the flag is for.
func TestOutputDirAloneIsStillAccepted(t *testing.T) {
	err := runGenerate(t, "--dataset", "--dataset-name", "ds", "--output-dir", t.TempDir())

	if err != nil {
		assert.NotContains(t, err.Error(), "nothing to write to",
			"an output directory without --no-wait must not be refused")
	}
}

// --wait says the default out loud, so it has to be accepted; asking for both
// at once says two things and has to be refused, as it is on `run start`.
func TestWaitAndNoWaitContradictEachOther(t *testing.T) {
	err := runGenerate(t, "--dataset", "--dataset-name", "ds", "--wait", "--no-wait")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "no-wait")
	assert.Contains(t, err.Error(), "wait")
}

// And --wait on its own is not a refusal, whatever else the run goes on to do.
func TestWaitAloneIsAccepted(t *testing.T) {
	err := runGenerate(t, "--dataset", "--dataset-name", "ds", "--wait")

	if err != nil {
		assert.NotContains(t, err.Error(), "--wait",
			"--wait names the default, so it must not be refused")
	}
}
