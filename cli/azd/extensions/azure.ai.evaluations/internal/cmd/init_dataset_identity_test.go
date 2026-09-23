// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"azureaieval/internal/exterrors"
	"azureaieval/internal/project"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/braydonk/yaml"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var initIdentityDeclarations = map[string]string{
	"local":      "  - name: seeds\n    file: ../original/seeds.jsonl\n    version: '7'\n    future_metadata: keep\n",
	"nested ref": "  - name: seeds\n    $ref: ./parts/dataset.yaml\n    version: '7'\n",
	"ref only":   "  - $ref: ./parts/dataset.yaml\n",
	"registered": "  - name: seeds\n    version: '7'\n",
}

func initIdentityFixture(t *testing.T, declaration string, prompts *seedCorrectionPromptServer) *initHarness {
	t.Helper()
	h := newInitHarness(t, nil, prompts)
	for _, dir := range []string{"original", "replacement", filepath.Join("evals", "parts", "inner"),
		filepath.Join(".azure", "dev")} {
		require.NoError(t, os.MkdirAll(dir, 0o700))
	}
	for path, body := range map[string]string{
		filepath.Join("original", "seeds.jsonl"):        `{"test_case_description":""}`,
		filepath.Join("replacement", "seeds.jsonl"):     `{"test_case_description":"help","desired_num_turns":20}`,
		filepath.Join("replacement", "corrected.jsonl"): `{"test_case_description":"help","desired_num_turns":20}`,
		filepath.Join("evals", project.EvalConfigBase): "# Keep the original\nfuture_metadata: keep\ndatasets:\n" +
			declaration + "  - name: unrelated\n    $ref: ./missing.yaml\n",
		filepath.Join("evals", "parts", "dataset.yaml"): "$ref: ./inner/dataset.yaml\n",
		filepath.Join("evals", "parts", "inner", "dataset.yaml"): "name: seeds\n" +
			"file: ../../../original/seeds.jsonl\nversion: '7'\nfuture_metadata: keep\n",
		filepath.Join(".azure", "dev", ".env"):        "KEEP=unchanged\n",
		filepath.Join(".azure", "dev", "config.json"): `{"keep":true}`,
	} {
		require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
	}
	return h
}

func TestInitDatasetFileCollisionPreservesState(t *testing.T) {
	for name, declaration := range initIdentityDeclarations {
		for _, mode := range []string{"simulation", "static", "turn"} {
			for _, output := range []string{"human", "json"} {
				t.Run(name+"/"+mode+"/"+output, func(t *testing.T) {
					h := initIdentityFixture(t, declaration, &seedCorrectionPromptServer{})
					before := initFileSnapshot(t, h.dir)
					args := simulationInitArgs("./replacement/seeds.jsonl")
					switch mode {
					case "static":
						args = []string{"--name", "quality", "--dataset", "./replacement/seeds.jsonl",
							"--judge-model", "judge", "--conversation-mode", "static"}
					case "turn":
						args = []string{"--name", "quality", "--dataset", "./replacement/seeds.jsonl",
							"--judge-model", "judge", "--target", "agent"}
					}
					if output == "human" {
						args = append(args, "--no-prompt")
					} else {
						args = append(args, "--output", "json")
					}
					text, err := executeConversationInit(t, args...)
					require.ErrorContains(t, err, "already declared")
					validation, ok := errors.AsType[*azdext.LocalError](err)
					require.True(t, ok)
					assert.Equal(t, exterrors.CodeConflictingArguments, validation.Code)
					assert.Contains(t, err.Error(), "seeds")
					assert.Contains(t, err.Error(), "./replacement/seeds.jsonl")
					assert.Empty(t, text, "no prompts or success-shaped document")
					assert.Zero(t, h.project.wiringAttempts())
					assert.Empty(t, h.usage.reported())
					assert.Equal(t, before, initFileSnapshot(t, h.dir))
				})
			}
		}
	}
}

func TestInitDatasetCollisionCorrection(t *testing.T) {
	for _, input := range []string{"explicit file", "declared dataset"} {
		for _, cancel := range []bool{false, true} {
			t.Run(input+"/"+map[bool]string{false: "distinct file", true: "cancel"}[cancel], func(t *testing.T) {
				t.Setenv("AZD_NO_PROMPT", "false")
				prompts := &seedCorrectionPromptServer{}
				h := initIdentityFixture(t, initIdentityDeclarations["nested ref"], prompts)
				args := simulationInitArgs("./replacement/seeds.jsonl")
				wantPrompts := 1
				if input == "declared dataset" {
					args = simulationInitArgs("seeds")
					prompts.datasets = append(prompts.datasets, "./replacement/seeds.jsonl")
					wantPrompts++
				}
				if !cancel {
					prompts.datasets = append(prompts.datasets, "./replacement/corrected.jsonl")
				}
				before := initFileSnapshot(t, h.dir)
				text, err := executeConversationInit(t, append(args, "--num-conversations", "5", "--max-turns", "20")...)
				assert.Contains(t, text, "already declared")
				assert.Contains(t, text, "different filename stem")
				if cancel {
					require.Error(t, err)
					assert.True(t, cancelled(err))
					prompts.mu.Lock()
					assert.Empty(t, prompts.messages)
					prompts.mu.Unlock()
					assert.Zero(t, h.project.wiringAttempts())
					assert.Empty(t, h.usage.reported())
					assert.Equal(t, before, initFileSnapshot(t, h.dir))
				} else {
					require.NoError(t, err)
					after := initFileSnapshot(t, h.dir)
					config := filepath.Join("evals", project.EvalConfigBase)
					for path, content := range before {
						if path == config {
							assert.Contains(t, after[path], content)
						} else {
							assert.Equal(t, content, after[path], path)
						}
					}
					var cfg project.EvalConfig
					require.NoError(t, yaml.Unmarshal([]byte(after[config]), &cfg))
					require.Len(t, cfg.Evals, 1)
					assert.Equal(t, "corrected", cfg.Evals[0].Dataset)
					assert.Equal(t, &project.Simulation{Model: "simulator", NumConversations: 5, MaxTurns: 20},
						cfg.Evals[0].Simulation)
					assert.Equal(t, "judge", cfg.Evals[0].Evaluators[0].InitializationParameters["model"])
					decl, err := project.ReadAuthoredDataset(filepath.Join(h.dir, "evals"), "corrected")
					require.NoError(t, err)
					require.NotNil(t, decl)
					assert.Equal(t, filepath.Join(h.dir, "replacement", "corrected.jsonl"), decl.File)
				}
				prompts.mu.Lock()
				defer prompts.mu.Unlock()
				assert.Len(t, prompts.selectCounts, wantPrompts)
				for _, count := range prompts.selectCounts {
					assert.Zero(t, count, "collision must be resolved before confirmation")
				}
				assert.Empty(t, prompts.models)
			})
		}
	}
}

func TestInitDatasetReusesEquivalentFiles(t *testing.T) {
	for _, declaration := range []string{"local", "nested ref", "ref only"} {
		for _, spelling := range []string{"relative", "canonical", "absolute", "hard link"} {
			t.Run(declaration+"/"+spelling, func(t *testing.T) {
				h := initIdentityFixture(t, initIdentityDeclarations[declaration], &seedCorrectionPromptServer{})
				path := "./original/seeds.jsonl"
				require.NoError(t, os.WriteFile(filepath.FromSlash(path),
					[]byte(`{"test_case_description":"help","desired_num_turns":21}`), 0o600))
				switch spelling {
				case "canonical":
					path = "./original/../original/seeds.jsonl"
				case "absolute":
					path = filepath.Join(h.dir, "original", "seeds.jsonl")
				case "hard link":
					require.NoError(t, os.Mkdir("linked", 0o700))
					path = filepath.Join("linked", "seeds.jsonl")
					require.NoError(t, os.Link(filepath.Join("original", "seeds.jsonl"), path))
				}
				before := initFileSnapshot(t, h.dir)
				text, err := executeConversationInit(t, append(simulationInitArgs(path), "--output", "json")...)
				require.NoError(t, err)
				assert.True(t, json.Valid([]byte(text)))
				after := initFileSnapshot(t, h.dir)
				config := filepath.Join("evals", project.EvalConfigBase)
				assert.Contains(t, after[config], before[config], "pins, unknown metadata and refs must be unchanged")
				var cfg project.EvalConfig
				require.NoError(t, yaml.Unmarshal([]byte(after[config]), &cfg))
				require.Len(t, cfg.Evals, 1)
				assert.Equal(t, "seeds", cfg.Evals[0].Dataset)
				assert.Len(t, cfg.Datasets, 2, "reuse must not duplicate a resolved ref-only declaration")
				if declaration == "local" {
					assert.Equal(t, "7", cfg.Datasets[0].Version)
				}
			})
		}
	}
}

func TestScaffoldDatasetFileIdentity(t *testing.T) {
	for _, collision := range []bool{false, true} {
		t.Run(map[bool]string{false: "same file", true: "different file"}[collision], func(t *testing.T) {
			t.Chdir(t.TempDir())
			require.NoError(t, os.Mkdir("original", 0o700))
			require.NoError(t, os.Mkdir("replacement", 0o700))
			for _, dir := range []string{"original", "replacement"} {
				require.NoError(t, os.WriteFile(filepath.Join(dir, "seeds.jsonl"), []byte(`{"query":"help"}`), 0o600))
			}
			cfg := &project.EvalConfig{Datasets: []project.DatasetDecl{
				{Name: "seeds", File: "../original/seeds.jsonl", Version: "7"},
			}}
			path := "./original/seeds.jsonl"
			if collision {
				path = "./replacement/seeds.jsonl"
			}
			_, err := planScaffold(scaffoldInput{
				evalName: "quality", target: "agent", dataset: path, evalDir: "evals", cfg: cfg,
			})
			if collision {
				require.ErrorContains(t, err, "already declared")
				assert.Empty(t, cfg.Evals)
			} else {
				require.NoError(t, err)
				require.Len(t, cfg.Evals, 1)
				assert.Equal(t, "seeds", cfg.Evals[0].Dataset)
			}
			assert.Equal(t, []project.DatasetDecl{{Name: "seeds", File: "../original/seeds.jsonl", Version: "7"}},
				cfg.Datasets)
		})
	}
}
