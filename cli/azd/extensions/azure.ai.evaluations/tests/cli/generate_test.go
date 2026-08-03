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
		args: []string{"dataset", "generate", "d", "--target", "a",
			"--agent-instruction", "inline", "--agent-instruction-file", instruction},
		want: "agent-instruction-file",
	}, {
		name: "below the minimum sample size",
		args: []string{"dataset", "generate", "d", "--target", "a", "--max-samples", "14"},
		want: "between 15 and 1000",
	}, {
		name: "above the maximum sample size",
		args: []string{"dataset", "generate", "d", "--target", "a", "--max-samples", "1001"},
		want: "between 15 and 1000",
	}, {
		name: "a missing instruction file names the flag",
		args: []string{"dataset", "generate", "d", "--target", "a",
			"--agent-instruction-file", filepath.Join(dir, "absent.md")},
		want: "--agent-instruction-file",
	}, {
		name: "generating a dataset needs a model deployment",
		args: []string{"dataset", "generate", "d", "--target", "a", "--agent-instruction", "inline"},
		want: "--generation-model",
	}, {
		name: "generating an evaluator needs a model deployment",
		args: []string{"evaluator", "generate", "e", "--target", "a", "--agent-instruction", "inline"},
		want: "--generation-model",
	}}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := requireFailure(t, runIn(t, dir, tc.args...))
			require.Contains(t, r.Combined(), tc.want)
		})
	}
}

// TestCLIGenerateNamesTheArtifact pins the positional argument. Without it the
// name would come from the spec, and two runs would quietly overwrite the same
// artifact.
func TestCLIGenerateNamesTheArtifact(t *testing.T) {
	for _, group := range []string{"dataset", "evaluator"} {
		t.Run(group, func(t *testing.T) {
			r := requireFailure(t, runIn(t, t.TempDir(), group, "generate"))
			require.Contains(t, r.Combined(), "accepts 1 arg")
		})
	}
}

// TestCLIGenerateNoPromptNamesWhatIsMissing is the CI case: with no target and
// nothing to prompt with, the process has to end saying which flag to pass.
func TestCLIGenerateNoPromptNamesWhatIsMissing(t *testing.T) {
	r := requireFailure(t, runIn(t, t.TempDir(), "dataset", "generate", "d", "--no-prompt"))
	require.Contains(t, r.Combined(), "--target is required")
	require.Contains(t, r.Combined(), "--no-prompt",
		"the message must say why it could not be resolved")
}

// TestCLIGenerateReadsTheSpec proves the config file is loaded and validated
// rather than only the flags.
//
// The strategy is the clearest evidence: `from-traces` is a value the spec
// accepts syntactically and the generation API cannot honour, so the refusal
// can only come from having parsed the file.
func TestCLIGenerateReadsTheSpec(t *testing.T) {
	dir := t.TempDir()
	spec := filepath.Join(dir, "gen.yaml")
	require.NoError(t, os.WriteFile(spec, []byte(`
agent:
  name: from-spec
generate:
  dataset:
    name: spec-dataset
    strategy: from-traces
`), 0o600))

	r := requireFailure(t, runIn(t, dir, "dataset", "generate", "d", "--config", spec))
	require.Contains(t, r.Combined(), "from-traces")
	require.Contains(t, r.Combined(), "agent.context.traces.window",
		"the refusal must point at the field that does seed generation from traces")
}

// TestCLIGenerateFlagsAreScopedToTheirArtifact asserts the two commands do not
// share settings that only one of them can honour. A sample count means nothing
// to a rubric, and a trace window means nothing to a synthetic dataset; either
// would be accepted and dropped.
func TestCLIGenerateFlagsAreScopedToTheirArtifact(t *testing.T) {
	dir := t.TempDir()

	r := requireFailure(t, runIn(t, dir, "evaluator", "generate", "e",
		"--target", "a", "--max-samples", "20"))
	require.Contains(t, r.Combined(), "max-samples",
		"--max-samples belongs to dataset generate")

	r = requireFailure(t, runIn(t, dir, "dataset", "generate", "d",
		"--target", "a", "--trace-days", "7"))
	require.Contains(t, r.Combined(), "trace-days",
		"--trace-days belongs to evaluator generate")
}
