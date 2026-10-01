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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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
