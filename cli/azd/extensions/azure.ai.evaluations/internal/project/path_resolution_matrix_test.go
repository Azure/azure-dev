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

// Path resolution has to answer the same thing on both routes.
//
// The configuration is read two ways -- off disk by the CLI commands, and out
// of the service entry by `azd up` -- and each anchors `$ref` resolution
// somewhere different: the CLI at the configuration's own directory, the deploy
// at the project root. Core rebases `file` and `source` onto whichever root it
// was given, so the join that follows has to use that same root. Using the
// service's directory there applied the rebase twice and `azd up` reported
// every scaffolded dataset as missing, while every CLI command found it.
//
// So the property under test is agreement, not a literal string: the same
// declaration must name the same file on disk whichever route opened it.

// writeFixture lays out a project and returns its root.
func writeFixture(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for rel, content := range files {
		full := filepath.Join(root, filepath.FromSlash(rel))
		require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o750))
		require.NoError(t, os.WriteFile(full, []byte(content), 0o600))
	}
	return root
}

// resolveBothWays returns the dataset file and evaluator source as the CLI
// resolves them and as the deploy does.
func resolveBothWays(t *testing.T, root, configRel string) (cliFile, deployFile, cliSource, deploySource string) {
	t.Helper()

	configPath := filepath.Join(root, filepath.FromSlash(configRel))

	cliCfg, err := LoadEvalConfig(configPath)
	require.NoError(t, err, "the CLI has to be able to open it")
	cliBase := filepath.Dir(configPath)

	svc := &azdext.ServiceConfig{
		Name:                 "evals",
		Host:                 EvalHost,
		AdditionalProperties: propsFrom(t, map[string]any{"$ref": "./" + configRel}),
	}
	deployCfg, err := EvalConfigFromService(svc, root)
	require.NoError(t, err, "`azd up` has to be able to read the same file")

	if len(cliCfg.Datasets) > 0 {
		cliFile = ResolveSource(cliBase, cliCfg.Datasets[0].File)
		deployFile = ResolveSource(root, deployCfg.Datasets[0].File)
	}
	if len(cliCfg.Evaluators) > 0 {
		cliSource = ResolveSource(cliBase, cliCfg.Evaluators[0].Source)
		deploySource = ResolveSource(root, deployCfg.Evaluators[0].Source)
	}
	return cliFile, deployFile, cliSource, deploySource
}

const nightlyEval = `
evals:
  - name: nightly
    dataset: golden
    evaluators:
      - evaluator: quality
`

func TestBothRoutesResolveTheSameFile(t *testing.T) {
	for _, tc := range []struct {
		name  string
		files map[string]string
		// Where the rows and the rubric really are, relative to the root.
		wantRows   string
		wantRubric string
	}{
		{
			name: "written inline in the configuration",
			files: map[string]string{
				"evals/azure.eval.yaml": `datasets:
  - name: golden
    file: ./datasets/rows.jsonl
evaluators:
  - name: quality
    source: ./evaluators/quality.json
` + nightlyEval,
				"evals/datasets/rows.jsonl":     "{}\n",
				"evals/evaluators/quality.json": "{}\n",
			},
			wantRows:   "evals/datasets/rows.jsonl",
			wantRubric: "evals/evaluators/quality.json",
		},
		{
			name: "each entry pulled in by its own $ref",
			files: map[string]string{
				"evals/azure.eval.yaml": `datasets:
  - $ref: ./parts/golden.yaml
evaluators:
  - $ref: ./parts/quality.yaml
` + nightlyEval,
				// Written beside parts/, so they mean parts/... and not evals/...
				"evals/parts/golden.yaml":             "name: golden\nfile: ./datasets/rows.jsonl\n",
				"evals/parts/quality.yaml":            "name: quality\nsource: ./evaluators/quality.json\n",
				"evals/parts/datasets/rows.jsonl":     "{}\n",
				"evals/parts/evaluators/quality.json": "{}\n",
			},
			wantRows:   "evals/parts/datasets/rows.jsonl",
			wantRubric: "evals/parts/evaluators/quality.json",
		},
		{
			name: "a $ref reached through another $ref",
			files: map[string]string{
				"evals/azure.eval.yaml": `datasets:
  - $ref: ./parts/golden.yaml
evaluators:
  - $ref: ./parts/quality.yaml
` + nightlyEval,
				"evals/parts/golden.yaml":  "$ref: ./inner/golden.yaml\n",
				"evals/parts/quality.yaml": "$ref: ./inner/quality.yaml\n",
				// Relative to inner/, which is the directory that must win.
				"evals/parts/inner/golden.yaml":             "name: golden\nfile: ./datasets/rows.jsonl\n",
				"evals/parts/inner/quality.yaml":            "name: quality\nsource: ./evaluators/quality.json\n",
				"evals/parts/inner/datasets/rows.jsonl":     "{}\n",
				"evals/parts/inner/evaluators/quality.json": "{}\n",
			},
			wantRows:   "evals/parts/inner/datasets/rows.jsonl",
			wantRubric: "evals/parts/inner/evaluators/quality.json",
		},
		{
			name: "a $ref that climbs out of the configuration's directory",
			files: map[string]string{
				"evals/azure.eval.yaml": `datasets:
  - $ref: ../shared/golden.yaml
evaluators:
  - $ref: ../shared/quality.yaml
` + nightlyEval,
				"shared/golden.yaml":             "name: golden\nfile: ./datasets/rows.jsonl\n",
				"shared/quality.yaml":            "name: quality\nsource: ./evaluators/quality.json\n",
				"shared/datasets/rows.jsonl":     "{}\n",
				"shared/evaluators/quality.json": "{}\n",
			},
			wantRows:   "shared/datasets/rows.jsonl",
			wantRubric: "shared/evaluators/quality.json",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := writeFixture(t, tc.files)

			cliFile, deployFile, cliSource, deploySource :=
				resolveBothWays(t, root, "evals/azure.eval.yaml")

			wantRows := filepath.Join(root, filepath.FromSlash(tc.wantRows))
			wantRubric := filepath.Join(root, filepath.FromSlash(tc.wantRubric))

			assert.Equal(t, wantRows, cliFile, "the CLI has to find the rows where they were written")
			assert.Equal(t, wantRows, deployFile, "and so does the deploy")
			assert.FileExists(t, cliFile)
			assert.FileExists(t, deployFile)

			assert.Equal(t, wantRubric, cliSource, "the CLI has to find the rubric where it was written")
			assert.Equal(t, wantRubric, deploySource, "and so does the deploy")
			assert.FileExists(t, cliSource)
			assert.FileExists(t, deploySource)
		})
	}
}

// An absolute path is the author naming a file outright, and neither route may
// re-root it.
func TestNeitherRouteRerootsAnAbsolutePath(t *testing.T) {
	outside := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(outside, "data"), 0o750))
	rows := filepath.Join(outside, "data", "rows.jsonl")
	require.NoError(t, os.WriteFile(rows, []byte("{}\n"), 0o600))

	root := writeFixture(t, map[string]string{
		"evals/azure.eval.yaml": "datasets:\n  - name: golden\n    file: " +
			filepath.ToSlash(rows) + "\n" + nightlyEval,
	})

	cliFile, deployFile, _, _ := resolveBothWays(t, root, "evals/azure.eval.yaml")

	// Compared cleaned: the value is passed through exactly as authored, and it
	// had to be authored with forward slashes to survive YAML on Windows.
	assert.Equal(t, rows, filepath.Clean(cliFile))
	assert.Equal(t, rows, filepath.Clean(deployFile))
	assert.FileExists(t, cliFile)
	assert.FileExists(t, deployFile)
}

// The whole configuration kept outside the project still finds its own files.
//
// Core expresses an out-of-tree include relative to the root it was given, so
// the decoded path carries `..` segments and joining from the root lands back
// outside. This is the shape a shared evaluation suite takes.
func TestAnOutOfTreeConfigurationFindsItsOwnFiles(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()

	require.NoError(t, os.MkdirAll(filepath.Join(outside, "datasets"), 0o750))
	rows := filepath.Join(outside, "datasets", "rows.jsonl")
	require.NoError(t, os.WriteFile(rows, []byte("{}\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(outside, "azure.eval.yaml"),
		[]byte("datasets:\n  - name: golden\n    file: ./datasets/rows.jsonl\n"+nightlyEval), 0o600))

	svc := &azdext.ServiceConfig{
		Name: "evals",
		Host: EvalHost,
		AdditionalProperties: propsFrom(t, map[string]any{
			"$ref": filepath.ToSlash(filepath.Join(outside, "azure.eval.yaml")),
		}),
	}

	cfg, err := EvalConfigFromService(svc, root)
	require.NoError(t, err)
	require.Len(t, cfg.Datasets, 1)

	resolved := ResolveSource(root, cfg.Datasets[0].File)
	assert.Equal(t, rows, resolved)
	assert.FileExists(t, resolved)
}

// A service that carries its configuration inline is authored in azure.yaml, so
// core leaves those paths exactly as written and they resolve from the root.
func TestAnInlineServiceResolvesFromTheProjectRoot(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "datasets"), 0o750))
	rows := filepath.Join(root, "datasets", "rows.jsonl")
	require.NoError(t, os.WriteFile(rows, []byte("{}\n"), 0o600))

	svc := &azdext.ServiceConfig{
		Name: "evals",
		Host: EvalHost,
		AdditionalProperties: propsFrom(t, map[string]any{
			"datasets": []any{
				map[string]any{"name": "golden", "file": "./datasets/rows.jsonl"},
			},
			"evals": []any{
				map[string]any{"name": "nightly", "dataset": "golden"},
			},
		}),
	}

	cfg, err := EvalConfigFromService(svc, root)
	require.NoError(t, err)
	require.Len(t, cfg.Datasets, 1)

	resolved := ResolveSource(root, cfg.Datasets[0].File)
	assert.Equal(t, rows, resolved)
	assert.FileExists(t, resolved)
}
