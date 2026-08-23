// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Every shape core splices can also be opened by the commands that edit it.
//
// Core replaces any object holding a `$ref`, not just the ones the catalogs
// declare. An eval's `source:`, its `target:` and the items of its
// `evaluators:` are objects too, so all three deployed and then refused the
// same file with `unknown key "$ref"` the moment `generate` or `init` read it.
//
// The same asymmetry the catalog entries had, one level down: a configuration
// that works until you run the command that edits it.
func TestNestedIncludesAreEditableToo(t *testing.T) {
	shapes := map[string]string{
		"eval source": `
evals:
  - name: nightly
    source:
      $ref: ./parts/source.yaml
    evaluators:
      - evaluator: builtin.task_adherence
`,
		"eval target": `
datasets:
  - name: golden
    file: ./datasets/golden.jsonl
evals:
  - name: nightly
    dataset: golden
    target:
      $ref: ./parts/target.yaml
    evaluators:
      - evaluator: builtin.task_adherence
`,
		"evaluator reference": `
datasets:
  - name: golden
    file: ./datasets/golden.jsonl
evals:
  - name: nightly
    dataset: golden
    evaluators:
      - $ref: ./parts/evaluator.yaml
`,
	}

	for name, body := range shapes {
		t.Run(name, func(t *testing.T) {
			dir := writeNestedRefFixture(t, body)

			_, deployed := OpenEvalConfig(dir)
			require.NoError(t, deployed, "core splices this shape, so the resolving read accepts it")

			_, edited := OpenEvalConfigForEdit(dir)
			require.NoError(t, edited,
				"the editing read has to survive what deploy accepts, or generate and init "+
					"refuse a file that deploys")
		})
	}
}

// The directive survives being written back, so an edit does not replace the
// include with whatever it resolved to.
func TestANestedIncludeSurvivesAnEdit(t *testing.T) {
	dir := writeNestedRefFixture(t, `
datasets:
  - name: golden
    file: ./datasets/golden.jsonl
evals:
  - name: nightly
    dataset: golden
    target:
      $ref: ./parts/target.yaml
    evaluators:
      - evaluator: builtin.task_adherence
`)

	cfg, err := OpenEvalConfigForEdit(dir)
	require.NoError(t, err)
	require.Len(t, cfg.Evals, 1)
	require.NotNil(t, cfg.Evals[0].Target)
	assert.Equal(t, "./parts/target.yaml", cfg.Evals[0].Target.Ref)
	assert.Empty(t, cfg.Evals[0].Target.Type, "the referenced file supplies this, not the entry")

	// Through the editing path an author actually reaches.
	require.NoError(t, ApplyScaffold(dir, ScaffoldWrite{
		Datasets: []DatasetDecl{{Name: "extra", File: "./datasets/extra.jsonl"}},
	}))

	written, err := os.ReadFile(filepath.Join(dir, EvalConfigBase))
	require.NoError(t, err)
	assert.Contains(t, string(written), "$ref: ./parts/target.yaml")
	assert.NotContains(t, string(written), `type: ""`,
		"a key the referenced file supplies is not written back as empty")
}

// The resolved reading is the same whichever route reaches it.
func TestANestedIncludeResolvesToTheSameThingOnBothRoutes(t *testing.T) {
	dir := writeNestedRefFixture(t, `
datasets:
  - name: golden
    file: ./datasets/golden.jsonl
evals:
  - name: nightly
    dataset: golden
    target:
      $ref: ./parts/target.yaml
    evaluators:
      - evaluator: builtin.task_adherence
`)

	cfg, err := OpenEvalConfig(dir)
	require.NoError(t, err)
	require.Len(t, cfg.Evals, 1)
	require.NotNil(t, cfg.Evals[0].Target)
	assert.Equal(t, TargetTypeAgent, cfg.Evals[0].Target.Type)
	assert.Equal(t, "support-agent", cfg.Evals[0].Target.Name)
	assert.Empty(t, cfg.Evals[0].Target.Ref, "resolution consumes the directive")
}

func writeNestedRefFixture(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "parts"), 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "parts", "source.yaml"),
		[]byte("type: traces\nlookback_hours: 24\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "parts", "target.yaml"),
		[]byte("type: agent\nname: support-agent\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "parts", "evaluator.yaml"),
		[]byte("evaluator: builtin.task_adherence\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, EvalConfigBase), []byte(body), 0o600))
	return dir
}
