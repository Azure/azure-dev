// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// The README's configuration example has to be one the CLI can load.
//
// It documented `options.eval_model`, which is not a key this decoder has ever
// had: following the README produced `unknown key "options"`. Nothing caught it
// because the example was prose, so correcting the keys alone would only have
// reset the clock on the same drift.
func TestTheREADMEExampleLoads(t *testing.T) {
	readme, err := os.ReadFile(filepath.Join("..", "..", "README.md"))
	require.NoError(t, err)

	body := evalConfigExample(t, string(readme))

	dir := t.TempDir()
	root, err := os.OpenRoot(dir)
	require.NoError(t, err)
	defer func() { _ = root.Close() }()
	require.NoError(t, root.WriteFile(EvalConfigBase, []byte(body), 0o600))

	_, err = LoadEvalConfig(filepath.Join(dir, EvalConfigBase))
	require.NoError(t, err, "the README example has to survive the decoder it documents")
}

// evalConfigExample returns the fenced yaml block the README labels as the eval
// configuration, identified by the file name comment on its first line.
func evalConfigExample(t *testing.T, readme string) string {
	t.Helper()

	const marker = "```yaml\n# evals/" + EvalConfigBase + "\n"

	readme = strings.ReplaceAll(readme, "\r\n", "\n")

	start := strings.Index(readme, marker)
	require.NotEqual(t, -1, start,
		"the README no longer opens the example with `# evals/%s`; retarget this test "+
			"rather than deleting it", EvalConfigBase)

	rest := readme[start+len(marker):]
	end := strings.Index(rest, "```")
	require.NotEqual(t, -1, end, "the example's fence is unterminated")

	return rest[:end]
}

// The simulation example is the one a reader copies to set up a multi-turn run,
// and every rule it states in prose is one the CLI enforces. Loading it is not
// enough: a block that decodes but is refused at deploy documents a
// configuration that does not work.
func TestTheREADMESimulationExampleIsRunnable(t *testing.T) {
	readme, err := os.ReadFile(filepath.Join("..", "..", "README.md"))
	require.NoError(t, err)

	body := yamlBlockAfter(t, string(readme), "### Simulating multi-turn conversations")

	dir := t.TempDir()
	root, err := os.OpenRoot(dir)
	require.NoError(t, err)
	defer func() { _ = root.Close() }()
	require.NoError(t, root.WriteFile(EvalConfigBase, []byte(body), 0o600))

	cfg, err := LoadEvalConfig(filepath.Join(dir, EvalConfigBase))
	require.NoError(t, err, "the README example has to survive the decoder it documents")
	require.Len(t, cfg.Evals, 1)

	eval := cfg.Evals[0]
	require.NotNil(t, eval.Simulation, "the example is there to document simulation:")
	require.NoError(t, validateSimulation(&eval),
		"the README documents a simulation the CLI would refuse")

	// The prose states each of these; a change that drops one should fail here
	// rather than in a reader's terminal.
	require.Equal(t, EvaluationLevelConversation, eval.EvaluationLevel)
	require.NotNil(t, eval.Target)
	require.Equal(t, TargetTypeAgent, eval.Target.Type)
	require.Nil(t, eval.Source, "simulation: and source: describe different runs")
	require.Zero(t, eval.MaxSamples, "a simulation cannot be sampled")

	// The bounds the comments quote have to be the bounds that are enforced.
	require.GreaterOrEqual(t, eval.Simulation.NumConversations, MinNumConversations)
	require.LessOrEqual(t, eval.Simulation.NumConversations, MaxNumConversations)
	require.GreaterOrEqual(t, eval.Simulation.MaxTurns, MinSimulationTurns)
	require.LessOrEqual(t, eval.Simulation.MaxTurns, MaxSimulationTurns)
}

// yamlBlockAfter returns the first fenced yaml block following a heading.
func yamlBlockAfter(t *testing.T, readme, heading string) string {
	t.Helper()

	readme = strings.ReplaceAll(readme, "\r\n", "\n")

	at := strings.Index(readme, heading)
	require.NotEqual(t, -1, at,
		"the README no longer has the %q section; retarget this test rather than deleting it",
		heading)

	rest := readme[at+len(heading):]
	start := strings.Index(rest, "```yaml\n")
	require.NotEqual(t, -1, start, "no yaml block follows %q", heading)

	rest = rest[start+len("```yaml\n"):]
	end := strings.Index(rest, "```")
	require.NotEqual(t, -1, end, "the example's fence is unterminated")

	return rest[:end]
}
