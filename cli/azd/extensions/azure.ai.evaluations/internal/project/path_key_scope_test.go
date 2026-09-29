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

// `source` means two different things in this configuration, and only one is a
// path.
//
// On an evaluator it names a rubric file. On an eval it is a mapping that says
// where rows come from -- `{type: traces, agent_name: ...}`. WithPathKeys
// declares `source` a path key, so this pins that the mapping is left alone:
// rebasing only applies to a relative string, and an eval's source carries no
// string to rebase.
func TestAnEvalSourceMappingSurvivesPathKeyRebasing(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "evals", "parts"), 0o750))

	// Inside a $ref, which is the only place rebasing happens at all.
	require.NoError(t, os.WriteFile(filepath.Join(root, "evals", "parts", "trace.yaml"),
		[]byte(`name: from-traces
source:
  type: traces
  agent_name: support-agent
  lookback_hours: 24
evaluators:
  - evaluator: builtin.task_adherence
`), 0o600))

	configPath := filepath.Join(root, "evals", "azure.eval.yaml")
	require.NoError(t, os.WriteFile(configPath, []byte(`evals:
  - $ref: ./parts/trace.yaml
`), 0o600))

	for _, route := range []struct {
		name string
		load func() (*EvalConfig, error)
	}{
		{"the CLI reading it off disk", func() (*EvalConfig, error) {
			return LoadEvalConfig(configPath)
		}},
		{"the deploy reading it off the service", func() (*EvalConfig, error) {
			svc := &azdext.ServiceConfig{
				Name:                 "evals",
				Host:                 EvalHost,
				AdditionalProperties: propsFrom(t, map[string]any{"$ref": "./evals/azure.eval.yaml"}),
			}
			return EvalConfigFromService(svc, root)
		}},
	} {
		t.Run(route.name, func(t *testing.T) {
			cfg, err := route.load()
			require.NoError(t, err)
			require.Len(t, cfg.Evals, 1)

			src := cfg.Evals[0].Source
			require.NotNil(t, src, "the mapping has to survive as a mapping")
			assert.Equal(t, "traces", src.Type)
			assert.Equal(t, "support-agent", src.AgentName,
				"an agent name is not a path and must not be rebased into one")
		})
	}
}

// A dataset's `file` and an evaluator's `source` are the path keys, and a
// declaration that names neither carries nothing to rebase.
func TestADeclarationWithNoLocalFileIsUntouched(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "evals", "parts"), 0o750))

	require.NoError(t, os.WriteFile(filepath.Join(root, "evals", "parts", "registered.yaml"),
		[]byte("name: already-published\nversion: \"3\"\n"), 0o600))

	configPath := filepath.Join(root, "evals", "azure.eval.yaml")
	require.NoError(t, os.WriteFile(configPath, []byte(`datasets:
  - $ref: ./parts/registered.yaml

evals:
  - name: nightly
    dataset: already-published
`), 0o600))

	cfg, err := LoadEvalConfig(configPath)
	require.NoError(t, err)
	require.Len(t, cfg.Datasets, 1)

	assert.Equal(t, "already-published", cfg.Datasets[0].Name)
	assert.Empty(t, cfg.Datasets[0].File,
		"a registered dataset names no local file, and none may be invented for it")
	assert.Equal(t, "3", cfg.Datasets[0].Version)
}
