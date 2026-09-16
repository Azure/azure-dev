// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

//go:build live

package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// Generation submits a job that costs model time and takes minutes, so what is
// exercised here is everything up to that point: the flag combinations each
// command refuses and the spec it parses. No test here submits a job.
//
// There is one command per artifact, so nothing suppresses anything: a caller
// who already has a dataset simply does not run `dataset generate`.

// TestCLIGenerateRefusesBadFlagCombinations covers the mistakes that must cost
// nothing to make. Each is decided locally, so a user finds out before a job is
// billed.
func TestCLIGenerateRefusesBadFlagCombinations(t *testing.T) {
	dir := t.TempDir()
	instruction := filepath.Join(dir, "instruction.md")
	require.NoError(t, os.WriteFile(instruction, []byte("test refunds"), 0o600))

	cases := []struct {
		name string
		args []string
		want string
	}{{
		name: "the two instruction sources are mutually exclusive",
		args: []string{"generate", "--dataset", "--dataset-name", "d", "--target", "a",
			"--agent-instruction", "inline", "--agent-instruction-file", instruction},
		want: "agent-instruction-file",
	}, {
		name: "below the minimum sample size",
		args: []string{"generate", "--dataset", "--dataset-name", "d", "--target", "a",
			"--max-samples", "14"},
		want: "between 15 and 1000",
	}, {
		name: "above the maximum sample size",
		args: []string{"generate", "--dataset", "--dataset-name", "d", "--target", "a",
			"--max-samples", "1001"},
		want: "between 15 and 1000",
	}, {
		name: "a missing instruction file names the flag",
		args: []string{"generate", "--dataset", "--dataset-name", "d", "--target", "a",
			"--agent-instruction-file", filepath.Join(dir, "absent.md")},
		want: "--agent-instruction-file",
	}, {
		name: "generating a dataset needs a model deployment",
		args: []string{"generate", "--dataset", "--dataset-name", "d",
			"--agent-instruction", "inline"},
		want: "--generation-model",
	}, {
		name: "generating an evaluator needs a model deployment",
		args: []string{"generate", "--evaluator", "--evaluator-name", "e",
			"--agent-instruction", "inline"},
		want: "--generation-model",
	}}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := requireFailure(t, runIn(t, dir, tc.args...))
			require.Contains(t, r.Combined(), tc.want)
		})
	}
}

// TestCLIGenerateNamesTheArtifact pins where the name comes from. The composite
// takes no positional, so with neither a name nor a target there is nothing to
// call the artifact, and the refusal has to name both ways out.
func TestCLIGenerateNamesTheArtifact(t *testing.T) {
	r := requireFailure(t, runIn(t, t.TempDir(), "generate", "--dataset"))

	require.Contains(t, r.Combined(), "--dataset-name")
	require.Contains(t, r.Combined(), "--target")
}

// TestCLIGenerateNoPromptNamesWhatIsMissing is the CI case: with nothing to
// prompt with, the process has to end saying which flag to pass.
//
// The target is no longer among them — it is read from the eval's declaration —
// but the generation model has no other source, so it is the one input a bare
// directory cannot supply.
//
// The target names an agent that does not exist, and that is load-bearing: a
// real one supplies a deployment, and then nothing is missing to report.
func TestCLIGenerateNoPromptNamesWhatIsMissing(t *testing.T) {
	r := requireFailure(t, runIn(t, t.TempDir(),
		"generate", "--dataset", "--dataset-name", "d", "--target", "a", "--no-prompt"))
	require.Contains(t, r.Combined(), "--generation-model")
}

// TestCLIGenerateDatasetFlagsDoNotApplyToTheEvaluator asserts the scoping the
// help promises is real.
//
// One command now carries both artifacts' settings, so cobra can no longer
// refuse --max-samples on a rubric. What must still hold is that it is not
// *validated* against a generation that does not use it: narrowing to the
// evaluator has to get past the sample-size check, and fail on the model it
// genuinely lacks instead.
func TestCLIGenerateDatasetFlagsDoNotApplyToTheEvaluator(t *testing.T) {
	r := requireFailure(t, runIn(t, t.TempDir(), "generate", "--evaluator",
		"--evaluator-name", "e", "--target", "a", "--max-samples", "20"))

	require.NotContains(t, r.Combined(), "between 15 and 1000",
		"--max-samples shapes the dataset, so it must not be validated for a rubric")
	require.Contains(t, r.Combined(), "--generation-model",
		"it should get as far as the input it actually lacks")
}
