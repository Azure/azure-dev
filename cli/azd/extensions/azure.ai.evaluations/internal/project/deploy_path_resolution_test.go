// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The deploy path resolves a declared file exactly once.
//
// Two rebases are in play and only one may apply. Core rebases `file` and
// `source` onto the root it was given -- the project root here -- so a path
// written beside evals/azure.eval.yaml arrives as `evals/datasets/...`.
// Joining that against the service's own directory as well produced
// `<root>/evals/evals/datasets/...`, and `azd up` reported every generated
// dataset as missing.
func TestDeployResolvesADeclaredFileOnlyOnce(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "evals", "datasets"), 0o750))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "evals", "evaluators"), 0o750))

	require.NoError(t, os.WriteFile(
		filepath.Join(root, "evals", "azure.eval.yaml"),
		[]byte(`datasets:
  - name: golden
    file: ./datasets/rows.jsonl

evaluators:
  - name: quality
    source: ./evaluators/quality.json

evals:
  - name: nightly
    dataset: golden
    evaluators:
      - evaluator: quality
`), 0o600))

	rows := filepath.Join(root, "evals", "datasets", "rows.jsonl")
	require.NoError(t, os.WriteFile(rows, []byte("{}\n"), 0o600))
	rubric := filepath.Join(root, "evals", "evaluators", "quality.json")
	require.NoError(t, os.WriteFile(rubric, []byte("{}\n"), 0o600))

	svc := &azdext.ServiceConfig{
		Name: "support-agent-evals",
		AdditionalProperties: propsFrom(t, map[string]any{
			"$ref": "./evals/azure.eval.yaml",
		}),
	}

	cfg, err := EvalConfigFromService(svc, root)
	require.NoError(t, err)
	require.Len(t, cfg.Datasets, 1)
	require.Len(t, cfg.Evaluators, 1)

	// Exactly what Deploy does with the decoded declarations.
	baseDir := root

	assert.Equal(t, rows, ResolveSource(baseDir, cfg.Datasets[0].File))
	assert.FileExists(t, ResolveSource(baseDir, cfg.Datasets[0].File),
		"a dataset the project scaffolded has to be found where it was written")

	assert.Equal(t, rubric, ResolveSource(baseDir, cfg.Evaluators[0].Source))
	assert.FileExists(t, ResolveSource(baseDir, cfg.Evaluators[0].Source),
		"and so does a rubric the generator wrote beside it")
}
