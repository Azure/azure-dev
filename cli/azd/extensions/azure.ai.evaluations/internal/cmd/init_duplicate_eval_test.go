// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"azureaieval/internal/project"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writeEvalConfig puts a configuration on disk and returns its directory.
func writeEvalConfig(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(
		filepath.Join(dir, project.EvalConfigBase), []byte(body), 0o600))
	return dir
}

const oneTraceEval = `
evals:
  - name: first
    source:
      type: traces
      max_traces: 20
    target:
      type: agent
      name: support-agent
    evaluators:
      - evaluator: builtin.task_adherence
        initialization_parameters:
          model: gpt-4.1-nano
`

// An eval that would differ from one already declared only by name is refused
// before it is written.
//
// Deploying refuses the pair, and that refusal names the whole file -- so the
// entry `init` had written stayed behind, was offered by `run start`, and then
// reported itself as declared but never deployed.
func TestInitRefusesAnEvalItsOwnDeployWouldReject(t *testing.T) {
	dir := writeEvalConfig(t, oneTraceEval)

	cfg, err := project.OpenEvalConfig(dir)
	require.NoError(t, err)
	require.Len(t, cfg.Evals, 1)

	planned := cfg.Evals[0]
	planned.Name = "second"

	err = refuseDuplicateEval(dir, &planned)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "second")
	assert.Contains(t, err.Error(), "first", "the eval it clashes with is the useful half")
}

// Re-running init under the same name is a replacement, not a duplicate.
func TestInitDoesNotRefuseTheEvalItIsReplacing(t *testing.T) {
	dir := writeEvalConfig(t, oneTraceEval)

	cfg, err := project.OpenEvalConfig(dir)
	require.NoError(t, err)
	planned := cfg.Evals[0]

	require.NoError(t, refuseDuplicateEval(dir, &planned))
}

// An eval that differs in substance is written without complaint.
func TestInitAcceptsAnEvalThatDiffers(t *testing.T) {
	dir := writeEvalConfig(t, oneTraceEval)

	cfg, err := project.OpenEvalConfig(dir)
	require.NoError(t, err)
	planned := cfg.Evals[0]
	planned.Name = "second"
	planned.Target = &project.Target{Type: project.TargetTypeAgent, Name: "other-agent"}

	require.NoError(t, refuseDuplicateEval(dir, &planned))
}

// A configuration this check cannot read is left to the commands that resolve
// it: failing a scaffold over an unrelated broken include would be its own
// surprise.
func TestInitDoesNotFailOverAConfigItCannotRead(t *testing.T) {
	dir := writeEvalConfig(t, "evals:\n  - name: first\n    fiel: nonsense\n")

	planned := project.Eval{Name: "second"}
	require.NoError(t, refuseDuplicateEval(dir, &planned))
}
