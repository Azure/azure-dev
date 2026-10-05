// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"

	"azureaieval/internal/pkg/eval_api"
	"azureaieval/internal/project"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEvaluatorArtifactsRejectNonObjectResults(t *testing.T) {
	for _, raw := range []string{`null`, `[]`, `"unexpected"`, `42`, `true`} {
		for _, existing := range []bool{false, true} {
			state := "new"
			if existing {
				state = "existing"
			}
			for _, caller := range []string{"download", "write", "collection", "job show", "generate"} {
				t.Run(raw+"/"+state+"/"+caller, func(t *testing.T) {
					job := &eval_api.GenerationJob{ID: "evaluator-job", Status: "succeeded", Result: json.RawMessage(raw)}
					dir := t.TempDir()
					var ec *evalContext
					var plans []generationPlan
					if caller == "generate" {
						ec, plans, dir, _ = generationRecoveryFixture(t, job)
					}
					configPath := filepath.Join(dir, "azure.eval.yaml")
					config := []byte(recoveryEvalConfig)
					require.NoError(t, os.WriteFile(configPath, config, 0o600))
					path := filepath.Join(dir, "evaluators", "quality.json")
					original := []byte(`{"type":"rubric","dimensions":[{"id":"authored"}]}`)
					if existing {
						require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
						require.NoError(t, os.WriteFile(path, original, 0o600))
					}
					cmd := jsonCmd(t, "json")
					cmd.SetContext(t.Context())
					var out bytes.Buffer
					cmd.SetOut(&out)
					var err error
					switch caller {
					case "download":
						action := &evaluatorDownloadAction{
							cmd: cmd, name: "quality", version: "3", outFile: path, force: existing,
						}
						err = action.download(t.Context(), evaluatorServing(t, []string{"3"}, raw))
					case "write":
						err = writeRubric(path, job.Result)
					case "collection":
						ref, collectErr := (&evalContext{}).collectRubric(
							job, "quality", dir, "evaluators", &out, false)
						err = collectErr
						assert.Nil(t, ref)
					case "job show":
						action := &jobShowAction{cmd: cmd, flags: &jobFlags{path: dir, force: existing}}
						ref, collectErr := action.collect(t.Context(), &evalContext{}, evaluatorJobs, job, &out)
						err = collectErr
						assert.Nil(t, ref)
					case "generate":
						plans[1].ReplaceApproved = existing
						cmd.RunE = func(*cobra.Command, []string) error {
							return ec.runGenerations(cmd, plans[1:], generateFlags{path: dir})
						}
						priorExit := exitProcess
						exitCode := 0
						exitProcess = func(code int) { exitCode = code }
						t.Cleanup(func() { exitProcess = priorExit })
						reportFailuresAsJSON(cmd)
						err = cmd.RunE(cmd, nil)
						assert.Equal(t, 1, exitCode)
						decoder := json.NewDecoder(&out)
						var result map[string]generationResult
						require.NoError(t, decoder.Decode(&result))
						require.ErrorIs(t, decoder.Decode(new(any)), io.EOF)
						assert.Equal(t, "failed", result["evaluator"].Status)
						assert.Equal(t, "evaluator-job", result["evaluator"].JobID)
						assert.Nil(t, result["evaluator"].ArtifactRef)
						assert.NotEmpty(t, result["evaluator"].Error)
					}
					require.Error(t, err)
					if caller != "generate" {
						assert.Empty(t, out.String())
					}
					if existing {
						after, readErr := os.ReadFile(path)
						require.NoError(t, readErr)
						assert.Equal(t, original, after)
					} else {
						assert.NoDirExists(t, filepath.Dir(path), "reject before creating the artifact directory")
					}
					after, readErr := os.ReadFile(configPath)
					require.NoError(t, readErr)
					assert.Equal(t, config, after, "do not catalog malformed artifacts")
				})
			}
		}
	}
}

func TestEvaluatorArtifactsPreserveUnknownObjects(t *testing.T) {
	for _, raw := range []string{`{}`, `{"future_shape":{"value":9007199254740993}}`} {
		for _, caller := range []string{"download", "generate"} {
			t.Run(caller+"/"+raw, func(t *testing.T) {
				dir := t.TempDir()
				cmd := evaluatorDownloadCmd(t)
				cmd.SetOut(io.Discard)
				path := filepath.Join(dir, "evaluators", "quality.json")
				switch caller {
				case "download":
					action := &evaluatorDownloadAction{cmd: cmd, name: "quality", version: "3", outFile: path}
					require.NoError(t, action.download(t.Context(), evaluatorServing(t, []string{"3"}, raw)))
				case "generate":
					job := &eval_api.GenerationJob{ID: "evaluator-job", Status: "succeeded", Result: json.RawMessage(raw)}
					ec, plans, generatedDir, _ := generationRecoveryFixture(t, job)
					require.NoError(t, ec.runGenerations(cmd, plans[1:], generateFlags{path: generatedDir}))
					path = filepath.Join(generatedDir, "evaluators", "quality.json")
					cfg, err := project.OpenEvalConfig(generatedDir)
					require.NoError(t, err)
					require.Len(t, cfg.Evaluators, 1)
					assert.Equal(t, "quality", cfg.Evaluators[0].Name)
				}
				body, err := os.ReadFile(path)
				require.NoError(t, err)
				require.JSONEq(t, raw, string(body))
				if caller == "generate" {
					assert.Equal(t, raw, string(body), "unknown generation documents retain their authored bytes")
				}
			})
		}
	}
}

func TestMalformedKnownRubricDoesNotReplaceArtifactOrCatalog(t *testing.T) {
	for _, definition := range []string{
		`{"type":"rubric","dimensions":null}`,
		`{"type":"rubric","dimensions":[null]}`,
		`{"type":"rubric","dimensions":[{"id":"a","description":42}]}`,
		`{"type":"rubric","dimensions":[{"id":"a","always_applicable":"yes"}]}`,
		`{"type":"rubric","dimensions":[{"id":"a","weight":"bad"}]}`,
		`{"type":"rubric","dimensions":[],"pass_threshold":"bad"}`,
	} {
		for _, caller := range []string{"download", "generation", "job show", "job show preserved"} {
			t.Run(caller+"/"+definition, func(t *testing.T) {
				dir := t.TempDir()
				path := filepath.Join(dir, "evaluators", "quality.json")
				require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
				before := []byte(`{"type":"rubric","dimensions":[{"id":"authored"}]}`)
				require.NoError(t, os.WriteFile(path, before, 0o600))
				configPath := filepath.Join(dir, "azure.eval.yaml")
				config := []byte("evaluators:\n  - name: quality\n    source: ./evaluators/quality.json\n")
				require.NoError(t, os.WriteFile(configPath, config, 0o600))
				raw := json.RawMessage(`{"name":"quality","version":"3","metadata":{"service":"only"},"definition":` +
					definition + `}`)
				var output bytes.Buffer
				var err error
				switch caller {
				case "download":
					ec := evaluatorServing(t, []string{"3"}, string(raw))
					action := &evaluatorDownloadAction{
						cmd: evaluatorDownloadCmd(t), name: "quality", version: "3", outFile: path, force: true,
					}
					action.cmd.SetOut(&output)
					err = action.download(t.Context(), ec)
				case "generation":
					err = writeRubric(path, raw)
				case "job show", "job show preserved":
					cmd := jsonCmd(t, "json")
					cmd.SetOut(&output)
					action := &jobShowAction{cmd: cmd, flags: &jobFlags{path: dir, force: caller == "job show"}}
					_, err = action.collect(t.Context(), &evalContext{}, evaluatorJobs,
						&eval_api.GenerationJob{ID: "job", Status: "succeeded", Result: raw}, &output)
				}
				require.Error(t, err)
				assert.Contains(t, err.Error(), "rubric")
				assert.Empty(t, output.String(), "malformed rubric must not report a successful write or collection")
				after, err := os.ReadFile(path)
				require.NoError(t, err)
				assert.Equal(t, before, after)
				afterConfig, err := os.ReadFile(configPath)
				require.NoError(t, err)
				assert.Equal(t, config, afterConfig)
			})
		}
	}

}

func TestMalformedKnownRubricDoesNotCreateDestination(t *testing.T) {
	for _, caller := range []string{"download", "generation", "job show"} {
		t.Run(caller, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "evaluators", "quality.json")
			raw := json.RawMessage(`{"name":"quality","version":"3","definition":{"type":"rubric","dimensions":null}}`)
			cmd := evaluatorDownloadCmd(t)
			cmd.SetOut(io.Discard)
			var err error
			switch caller {
			case "download":
				action := &evaluatorDownloadAction{cmd: cmd, name: "quality", version: "3", outFile: path}
				err = action.download(t.Context(), evaluatorServing(t, []string{"3"}, string(raw)))
			case "generation":
				err = writeRubric(path, raw)
			case "job show":
				action := &jobShowAction{cmd: cmd, flags: &jobFlags{path: dir}}
				_, err = action.collect(t.Context(), &evalContext{}, evaluatorJobs,
					&eval_api.GenerationJob{ID: "job", Status: "succeeded", Result: raw}, io.Discard)
			}
			require.Error(t, err)
			entries, err := os.ReadDir(dir)
			require.NoError(t, err)
			assert.Empty(t, entries)
		})
	}
}
