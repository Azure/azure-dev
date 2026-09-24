// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"azureaieval/internal/project"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/braydonk/yaml"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInitRootSaveFailureRestoresConfigAndAllowsExactRetry(t *testing.T) {
	for _, filename := range []string{project.EvalConfigBase, project.LegacyEvalConfigBase, "custom quality.yml"} {
		for _, existing := range []bool{false, true} {
			for _, format := range []string{"default", "json"} {
				name := filename + "/" + format
				if existing {
					name += "/existing"
				}
				t.Run(name, func(t *testing.T) {
					h := newInitHarness(t, nil)
					evalDir := filepath.Join(h.dir, "quality files")
					configPath := filepath.Join(evalDir, filename)
					require.NoError(t, os.MkdirAll(evalDir, 0o700))
					ignorePath := filepath.Join(evalDir, ".gitignore")
					require.NoError(t, os.WriteFile(ignorePath, []byte("# user rules\r\nprivate/\r\n"), 0o600))
					original := []byte("# keep exact formatting\r\nx-metadata: {owner: team}\r\n" +
						"datasets:\r\n  - {name: golden, file: ./golden.jsonl}\r\n" +
						"evals:\r\n  - {name: existing, dataset: golden}\r\n")
					if existing {
						require.NoError(t, os.WriteFile(configPath, original, 0o600))
					}
					rootPath := filepath.Join(h.dir, "azure.yaml")
					rootBefore, err := os.ReadFile(rootPath)
					require.NoError(t, err)
					rows := []byte("{\"messages\":[{\"role\":\"user\",\"content\":\"hello\"}," +
						"{\"role\":\"assistant\",\"content\":\"world\"}]}\n")
					require.NoError(t, os.WriteFile(h.seedRows, rows, 0o600))

					var denySave atomic.Bool
					denySave.Store(true)
					var sawScaffold atomic.Bool
					h.project.setAddServiceHandler(func(request *azdext.AddServiceRequest) error {
						body, err := os.ReadFile(configPath)
						if err != nil {
							return err
						}
						sawScaffold.Store(strings.Contains(string(body), "name: retry-quality"))
						if denySave.Load() {
							return &os.PathError{Op: "save", Path: rootPath, Err: os.ErrPermission}
						}
						var root map[string]any
						if err := yaml.Unmarshal(rootBefore, &root); err != nil {
							return err
						}
						services := map[string]any{
							"agent": map[string]any{"host": "containerapp", "project": "./agent"},
							request.Service.Name: map[string]any{
								"host": request.Service.Host,
								"$ref": request.Service.AdditionalProperties.AsMap()["$ref"],
							},
						}
						root["services"] = services
						body, err = yaml.Marshal(root)
						if err != nil {
							return err
						}
						return os.WriteFile(rootPath, body, 0o600)
					})
					args := []string{"--path", configPath, "--name", "retry-quality", "--conversation-mode", "static",
						"--dataset", h.seedRows, "--judge-model", "judge", "--no-prompt", "--output", format}
					text, err := executeConversationInit(t, args...)
					require.ErrorContains(t, err, "permission denied")
					assert.ErrorContains(t, err, "was rolled back")
					assert.True(t, sawScaffold.Load(), "the injected failure must follow the scaffold write")
					assert.Empty(t, text)
					assert.Empty(t, h.usage.reported())
					rootAfter, err := os.ReadFile(rootPath)
					require.NoError(t, err)
					assert.Equal(t, rootBefore, rootAfter)
					if existing {
						body, err := os.ReadFile(configPath)
						require.NoError(t, err)
						assert.Equal(t, original, body)
					} else {
						assert.NoFileExists(t, configPath)
					}
					ignore, err := os.ReadFile(ignorePath)
					require.NoError(t, err)
					assert.Equal(t, "# user rules\r\nprivate/\r\n", string(ignore))
					actualRows, err := os.ReadFile(h.seedRows)
					require.NoError(t, err)
					assert.Equal(t, rows, actualRows)

					denySave.Store(false)
					text, err = executeConversationInit(t, args...)
					require.NoError(t, err, "the identical command must recover after root writeability is restored")
					assert.Equal(t, format == "json", json.Valid([]byte(text)))
					authored, err := project.ReadAuthoredConfig(configPath)
					require.NoError(t, err)
					cfg := declaredSoFar(authored)
					wantEvals := 1
					if existing {
						wantEvals++
					}
					require.Len(t, cfg.Evals, wantEvals)
					assert.Equal(t, "retry-quality", cfg.Evals[len(cfg.Evals)-1].Name)
					assert.Equal(t, 2, h.project.wiringAttempts())
					assertOneInitCompleted(t, h, "dataset")
					body, err := os.ReadFile(rootPath)
					require.NoError(t, err)
					assert.Contains(t, string(body), "conversation-evals:")
					assert.Contains(t, string(body), "$ref: ./"+filepath.ToSlash(filepath.Join("quality files", filename)))
				})
			}
		}
	}
}

func TestInitRootSaveFailurePreservesConcurrentChanges(t *testing.T) {
	for _, change := range []string{"eval", "root", "eval replaced by directory", "root removed", "cancelled"} {
		t.Run(change, func(t *testing.T) {
			h := newInitHarness(t, nil)
			configPath := filepath.Join(h.dir, "quality.yml")
			rootPath := filepath.Join(h.dir, "azure.yaml")
			var mu sync.Mutex
			var changedBody []byte
			h.project.setAddServiceHandler(func(_ *azdext.AddServiceRequest) error {
				mu.Lock()
				defer mu.Unlock()
				switch change {
				case "eval", "root":
					path := configPath
					if change == "root" {
						path = rootPath
					}
					body, err := os.ReadFile(path)
					if err != nil {
						return err
					}
					changedBody = append(body, []byte("# concurrent edit\n")...)
					if err := os.WriteFile(path, changedBody, 0o600); err != nil {
						return err
					}
				case "eval replaced by directory":
					if err := os.Remove(configPath); err != nil {
						return err
					}
					if err := os.Mkdir(configPath, 0o700); err != nil {
						return err
					}
				case "root removed":
					if err := os.Remove(rootPath); err != nil {
						return err
					}
				case "cancelled":
					return context.Canceled
				}
				return os.ErrPermission
			})
			text, err := executeConversationInit(t, "--path", configPath, "--name", "quality",
				"--source", "traces", "--target", "agent", "--judge-model", "judge", "--no-prompt", "--output", "json")
			require.Error(t, err)
			assert.Empty(t, text)
			assert.Empty(t, h.usage.reported())
			if change == "cancelled" {
				assert.ErrorContains(t, err, "canceled")
				assert.ErrorContains(t, err, "was rolled back")
				assert.NoFileExists(t, configPath)
				return
			}
			assert.ErrorContains(t, err, "permission denied")
			assert.ErrorContains(t, err, "could not safely roll back")
			assert.ErrorContains(t, err, "Inspect")
			switch change {
			case "eval", "root":
				path := configPath
				if change == "root" {
					path = rootPath
				}
				body, err := os.ReadFile(path)
				require.NoError(t, err)
				mu.Lock()
				assert.Equal(t, changedBody, body)
				mu.Unlock()
				assert.FileExists(t, configPath)
			case "eval replaced by directory":
				assert.DirExists(t, configPath)
			case "root removed":
				assert.NoFileExists(t, rootPath)
				assert.FileExists(t, configPath)
			}
		})
	}
}
