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

// --from and --max-samples are documented "Dataset only", and --evaluator
// generates no dataset. They were accepted and ignored, so the run produced a
// rubric and said nothing about the sample count that was asked for.
func TestDatasetOnlyFlagsAreRefusedForEvaluatorOnlyGeneration(t *testing.T) {
	for flag, args := range map[string][]string{
		"--from":        {"--evaluator", "--evaluator-name", "ev", "--from", "prompt"},
		"--max-samples": {"--evaluator", "--evaluator-name", "ev", "--max-samples", "50"},
	} {
		err := runGenerate(t, args...)

		require.Errorf(t, err, "%s cannot apply to an evaluator-only generation", flag)
		assert.Contains(t, err.Error(), flag)
		assert.Contains(t, err.Error(), "--evaluator",
			"the refusal has to name the flag that made it inapplicable")
	}
}

// The same flags with a dataset selected are the ordinary case, so the refusal
// above has to be about the combination rather than about the flags.
func TestDatasetOnlyFlagsAreStillAcceptedForADataset(t *testing.T) {
	err := runGenerate(t,
		"--dataset", "--dataset-name", "ds", "--from", "prompt", "--max-samples", "50")

	if err != nil {
		assert.NotContains(t, err.Error(), "only affects the dataset",
			"a dataset generation must not be refused its own flags")
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
