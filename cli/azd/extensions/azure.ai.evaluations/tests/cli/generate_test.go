// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

//go:build live

package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// `generate` submits two jobs that cost model time and take minutes, so what
// is exercised here is everything up to that point: the flag combinations it
// refuses, the spec it parses, and the two flags that mean "I already have
// this one, do not make another". None of these tests submits a job — the last
// one reaches the service and deliberately generates nothing.

// TestCLIGenerateRefusesBadFlagCombinations covers the mistakes that must cost
// nothing to make. Each of these is decided locally, so a user finds out
// before a job is billed.
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
		args: []string{"--target", "a", "--agent-instruction", "inline",
			"--agent-instruction-file", instruction},
		want: "agent-instruction-file",
	}, {
		name: "below the minimum sample size",
		args: []string{"--target", "a", "--max-samples", "14"},
		want: "between 15 and 1000",
	}, {
		name: "above the maximum sample size",
		args: []string{"--target", "a", "--max-samples", "1001"},
		want: "between 15 and 1000",
	}, {
		name: "a missing instruction file names the flag",
		args: []string{"--target", "a", "--agent-instruction-file",
			filepath.Join(dir, "absent.md")},
		want: "--agent-instruction-file",
	}, {
		name: "generating needs a model deployment",
		args: []string{"--target", "a", "--agent-instruction", "inline"},
		want: "--generation-model",
	}}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := requireFailure(t, runIn(t, dir, append([]string{"generate"}, tc.args...)...))
			require.Contains(t, r.Combined(), tc.want)
		})
	}
}

// TestCLIGenerateNoPromptNamesWhatIsMissing is the CI case: with no target and
// nothing to prompt with, the process has to end saying which flag to pass.
func TestCLIGenerateNoPromptNamesWhatIsMissing(t *testing.T) {
	r := requireFailure(t, runIn(t, t.TempDir(), "generate", "--no-prompt"))
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

	r := requireFailure(t, runIn(t, dir, "generate", "--config", spec))
	require.Contains(t, r.Combined(), "from-traces")
	require.Contains(t, r.Combined(), "agent.context.traces.window",
		"the refusal must point at the field that does seed generation from traces")
}

// TestCLIGenerateSkipsWhatWasSupplied is the one generate test that reaches the
// service, and it is here because the skip decision is made in the command
// body rather than in the config resolver.
//
// With both artifacts supplied there is nothing left to generate, so the whole
// command runs without submitting a job — which is what makes it affordable to
// assert on. A regression that stopped honouring either flag would show up as
// a generation job starting instead of this returning.
func TestCLIGenerateSkipsWhatWasSupplied(t *testing.T) {
	dir := t.TempDir()

	r := requireSuccess(t, runIn(t, dir, "generate",
		"--target", "azd-eval-probe-agent",
		"--agent-instruction", "answer questions about orders",
		"--evaluator", "already-published",
		"--dataset", "already-registered"))

	require.Contains(t, r.Stdout, "skipping rubric generation")
	require.Contains(t, r.Stdout, "skipping data generation")
	require.Contains(t, r.Stdout, "Nothing was generated.")

	// The deployment spec is only rewritten when something was produced, and
	// writing an empty reference into it would be worse than not writing.
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Empty(t, entries, "a generate that produced nothing must write nothing")
}

// TestCLIGenerateSuppressionIsPerArtifact pins the two flags apart: neither
// may suppress the artifact it does not name.
//
// Each case supplies one artifact and leaves the other to be generated, and is
// stopped at the model check that precedes submission. Reaching that error is
// the proof: it is only raised when something is still going to be generated,
// so it says the unsupplied artifact survived the other flag.
func TestCLIGenerateSuppressionIsPerArtifact(t *testing.T) {
	cases := []struct {
		name     string
		supplied []string
		survives string
	}{
		{"a supplied evaluator leaves the dataset", []string{"--evaluator", "already-published"}, "dataset"},
		{"a supplied dataset leaves the rubric", []string{"--dataset", "already-registered"}, "rubric"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			args := append([]string{"generate",
				"--target", "azd-eval-probe-agent",
				"--agent-instruction", "answer questions about orders"}, tc.supplied...)

			r := requireFailure(t, runIn(t, t.TempDir(), args...))
			require.Contains(t, r.Combined(), "--generation-model",
				"the %s was suppressed by a flag that does not name it", tc.survives)
			require.NotContains(t, strings.ToLower(r.Combined()), "generating ",
				"the run must stop at the model check, before any job is submitted")
		})
	}
}
