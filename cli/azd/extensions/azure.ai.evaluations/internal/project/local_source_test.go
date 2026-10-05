// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExplicitLocalSourceConfiguration(t *testing.T) {
	for _, tc := range []struct {
		name   string
		source map[string]any
		cap    int
		valid  bool
	}{
		{"local", map[string]any{"type": "local", "file": "./rows.jsonl"}, 0, true},
		{"capped", map[string]any{"type": "local", "file": "./rows.jsonl"}, 10, true},
		{"negative cap", map[string]any{"type": "local", "file": "./rows.jsonl"}, -1, false},
		{"missing file", map[string]any{"type": "local"}, 0, false},
		{"blank file", map[string]any{"type": "local", "file": " "}, 0, false},
		// #nosec G101 -- synthetic credential-bearing URL is rejected before any file or network access.
		{"URL", map[string]any{"type": "local", "file": "https://user:secret@example.test/data?sig=secret"}, 0, false},
		{"trace field zero", map[string]any{"type": "local", "file": "rows", "max_traces": 0}, 0, false},
		{"responses empty", map[string]any{"type": "local", "file": "rows", "response_ids": []string{}}, 0, false},
		{"version pin", map[string]any{"type": "local", "file": "rows", "version": "7"}, 0, false},
		{"trace file", map[string]any{"type": "traces", "agent_name": "agent", "file": ""}, 0, false},
		{"response file", map[string]any{"type": "responses", "response_ids": []string{"one"}, "file": "rows"}, 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body, err := json.Marshal(map[string]any{"evals": []any{map[string]any{
				"name": "quality", "source": tc.source, "max_samples": tc.cap,
				"evaluators": []any{map[string]any{"evaluator": "builtin.relevance"}},
			}}})
			require.NoError(t, err)
			cfg, err := DecodeEvalConfig(body, "test")
			if err == nil {
				err = cfg.Validate()
			}
			if tc.valid {
				require.NoError(t, err)
				assert.True(t, cfg.Evals[0].IsLocalSource())
				assert.Empty(t, cfg.Datasets)
			} else {
				require.Error(t, err)
				assert.NotContains(t, err.Error(), "secret")
			}
		})
	}
}

func TestExplicitLocalSourceNestedRefsShareCLIAndDeployBase(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "evals", "sources", "data files"), 0o700))
	path := filepath.Join(root, "evals", "azure.eval.yaml")
	require.NoError(t, os.WriteFile(path, []byte(`evals:
  - name: quality
    source:
      $ref: ./sources/local.yaml
    max_samples: 2
    evaluators:
      - evaluator: builtin.relevance
`), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(root, "evals", "sources", "local.yaml"),
		[]byte("type: local\nfile: ./data files/rows.jsonl\n"), 0o600))
	want := filepath.Join(root, "evals", "sources", "data files", "rows.jsonl")
	require.NoError(t, os.WriteFile(want, []byte("{\"query\":\"local\"}\n"), 0o600))
	cfg, err := LoadEvalConfig(path)
	require.NoError(t, err)
	require.NoError(t, cfg.Validate())
	assert.Equal(t, want, cfg.Evals[0].LocalSourcePath(filepath.Dir(path)))
	service := &azdext.ServiceConfig{Name: "evals", AdditionalProperties: propsFrom(t, map[string]any{
		"$ref": "./evals/azure.eval.yaml",
	})}
	deployed, err := EvalConfigFromService(service, root)
	require.NoError(t, err)
	require.NoError(t, deployed.Validate())
	assert.Equal(t, want, deployed.Evals[0].LocalSourcePath(root))
	assert.Empty(t, deployed.Datasets)
}
