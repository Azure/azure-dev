// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"azureaieval/internal/project"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A dataset inside the eval directory is reached without climbing out of it.
//
// The location is the directory before anything is written and the
// configuration file once it exists, which is what a second `init` resolves to.
// Rebasing against the file put a `..` in front of every path, so
// `./evals/datasets/rows.jsonl` was written as `../datasets/rows.jsonl` and the
// deploy looked for it beside the project rather than beside the config.
func TestADatasetInsideTheEvalDirNeedsNoDotDot(t *testing.T) {
	for _, tc := range []struct {
		name         string
		location     string
		configOnDisk bool
	}{
		{"first init, the location is the directory", "evals", false},
		{"second init, the location is the file", filepath.Join("evals", "azure.eval.yaml"), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			t.Chdir(root)
			require.NoError(t, os.MkdirAll(filepath.Join("evals", "datasets"), 0o750))
			require.NoError(t, os.WriteFile(
				filepath.Join("evals", "datasets", "rows.jsonl"), []byte("{\"query\":\"q\"}\n"), 0o600))
			if tc.configOnDisk {
				require.NoError(t, os.WriteFile(
					filepath.Join("evals", "azure.eval.yaml"), []byte("evals: []\n"), 0o600))
			}

			_, cfg := scaffoldFor(t, scaffoldInput{
				evalName: "smoke",
				target:   "a",
				dataset:  filepath.Join("evals", "datasets", "rows.jsonl"),
				evalDir:  tc.location,
			})

			decl, ok := cfg.DatasetDeclaration("rows")
			require.True(t, ok)
			assert.Equal(t, "./datasets/rows.jsonl", decl.File,
				"the rows sit beside the configuration")
		})
	}
}

func TestInitConfigurationFileOrDirectoryLocation(t *testing.T) {
	for _, location := range []struct {
		name     string
		basename string
		file     bool
		exists   bool
	}{
		{"new yaml file", "custom.yaml", true, false},
		{"new yml file", "custom.yml", true, false},
		{"new uppercase file", "custom.YAML", true, false},
		{"existing yaml file", "custom.yaml", true, true},
		{"existing yml file", "custom.yml", true, true},
		{"existing other file", "custom.config", true, true},
		{"new directory", "quality", false, false},
		{"existing directory", "quality", false, true},
		{"existing yaml-named directory", "custom.yaml", false, true},
	} {
		for _, absolute := range []bool{false, true} {
			form := "relative"
			if absolute {
				form = "absolute"
			}
			t.Run(location.name+"/"+form, func(t *testing.T) {
				h := newInitHarness(t, nil)
				path := filepath.Join("custom config", location.basename)
				if absolute {
					path = filepath.Join(h.dir, path)
				}
				wantDir, wantConfig := path, filepath.Join(path, project.EvalConfigBase)
				if location.file {
					wantDir, wantConfig = filepath.Dir(path), path
				}
				if location.exists {
					require.NoError(t, os.MkdirAll(wantDir, 0o700))
					if location.file {
						require.NoError(t, os.WriteFile(path, []byte("# Keep existing config\nevals: []\n"), 0o600))
					}
				}
				text, err := executeConversationInit(t, "--path", path, "--name", "quality",
					"--conversation-mode", "static", "--dataset", h.seedRows, "--judge-model", "judge", "--output", "json")
				require.NoError(t, err)
				var result map[string]any
				require.NoError(t, json.Unmarshal([]byte(text), &result))
				assert.Equal(t, wantConfig, result["evalConfig"])
				assert.Equal(t, filepath.Join(wantDir, project.DefaultDatasetsDir), result["datasetsDir"])
				assert.Equal(t, filepath.Join(wantDir, project.DefaultEvaluatorsDir), result["evaluatorsDir"])
				info, err := os.Stat(wantConfig)
				require.NoError(t, err)
				assert.False(t, info.IsDir(), "the requested configuration must be a file, not a parent directory")
				if location.file && location.exists {
					body, err := os.ReadFile(path)
					require.NoError(t, err)
					assert.Contains(t, string(body), "# Keep existing config")
				}
			})
		}
	}
}
