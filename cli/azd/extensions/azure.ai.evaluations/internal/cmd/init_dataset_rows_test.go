// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"azureaieval/internal/project"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writeLocalDataset drops a JSONL file in a temp dir and returns its path.
func writeLocalDataset(t *testing.T, name, contents string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	require.NoError(t, os.WriteFile(path, []byte(contents), 0o600))
	return path
}

// init exits 0 and writes dataset and eval declarations for local JSONL it
// cannot turn into evaluation rows. The file is in its hand and it makes no
// service call, so the failure was deferred to a deploy that had nothing to
// work with. ADO 5631311.
func TestInitScaffold_RefusesLocalDatasetFilesItCannotUse(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		contents string
		wantErr  string
	}{
		{
			name:     "a line that is not JSON",
			contents: "{\"query\":\"valid first row\"}\nnot-json\n",
			wantErr:  "line 2 is not valid JSON",
		},
		{
			name:     "an empty file",
			contents: "",
			wantErr:  "has no rows to evaluate",
		},
		{
			name:     "a file of only blank lines",
			contents: "\n   \n\n",
			wantErr:  "has no rows to evaluate",
		},
		{
			name:     "an array row instead of an object",
			contents: "[\"query\", \"response\"]\n",
			wantErr:  "line 1 is not valid JSON",
		},
		{
			name:     "an object row that carries nothing",
			contents: "{}\n",
			wantErr:  "line 1 is an empty object",
		},
		{
			name:     "a row that is a bare scalar",
			contents: "\"just a string\"\n",
			wantErr:  "line 1 is not valid JSON",
		},
		{
			name:     "a valid first row followed by an array",
			contents: "{\"query\":\"q\"}\n[\"not\",\"an\",\"object\"]\n",
			wantErr:  "line 2 is not valid JSON",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := validateJSONL(writeLocalDataset(t, "rows.jsonl", tt.contents))

			require.Error(t, err, "init must refuse this before writing a declaration")
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

// The files init has always accepted must keep being accepted. Refusing one of
// these would be a worse regression than the bug.
func TestInitScaffold_AcceptsUsableLocalDatasetFiles(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		contents string
	}{
		{
			name:     "one row",
			contents: "{\"query\":\"q\"}\n",
		},
		{
			name:     "several rows",
			contents: "{\"query\":\"a\"}\n{\"query\":\"b\"}\n{\"query\":\"c\"}\n",
		},
		{
			name:     "blank lines between rows are not rows",
			contents: "{\"query\":\"a\"}\n\n{\"query\":\"b\"}\n",
		},
		{
			name:     "no trailing newline",
			contents: "{\"query\":\"a\"}",
		},
		{
			name:     "conversation rows carrying nested messages",
			contents: "{\"messages\":[{\"role\":\"user\",\"content\":\"hi\"}]}\n",
		},
		{
			name:     "seed rows carrying no query at all",
			contents: "{\"test_case_description\":\"a delayed order\",\"desired_num_turns\":4}\n",
		},
		{
			name: "a byte order mark on the first row",
			// PowerShell redirection writes one, and deploy already tolerates it.
			contents: "\uFEFF{\"query\":\"a\"}\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assert.NoError(t, validateJSONL(writeLocalDataset(t, "rows.jsonl", tt.contents)))
		})
	}
}

// A path with no file behind it is reported as absent rather than as a row
// problem, so the reader is not sent looking inside a file that is not there.
func TestInitScaffold_UnreadableDatasetIsReportedAsAPathProblem(t *testing.T) {
	t.Parallel()

	err := validateJSONL(filepath.Join(t.TempDir(), "missing.jsonl"))

	require.Error(t, err)
	assert.NotContains(t, err.Error(), "line",
		"a missing file has no line to report")
}

func TestInitLocalJSONLRejectionPrecedesAllAuthoringWrites(t *testing.T) {
	for _, tc := range []struct {
		name, rows, want string
	}{
		{"malformed", "{\"query\":\"valid\"}\nnot-json\n", "line 2 is not valid JSON"},
		{"empty", "", "no rows"},
		{"array", "[\"query\",\"response\"]\n", "line 1 is not valid JSON"},
		{"scalar", "\"text\"\n", "line 1 is not valid JSON"},
		{"empty object", "{}\n", "empty object"},
	} {
		for _, mode := range []string{"turn", "static"} {
			for _, format := range []string{"default", "json"} {
				t.Run(tc.name+"/"+mode+"/"+format, func(t *testing.T) {
					h := newInitHarness(t, nil)
					require.NoError(t, os.WriteFile(h.seedRows, []byte(tc.rows), 0o600))
					before := initFileSnapshot(t, h.dir)
					args := []string{"init", "--name", "quality", "--source", "dataset",
						"--dataset", h.seedRows, "--judge-model", "judge", "--no-prompt", "--output", format}
					if mode == "static" {
						args = append(args, "--conversation-mode", "static")
					} else {
						args = append(args, "--target", "agent", "--evaluation-level", "turn")
					}
					root := NewRootCommand()
					root.SetContext(t.Context())
					root.SilenceErrors, root.SilenceUsage = true, true
					var out bytes.Buffer
					root.SetOut(&out)
					root.SetErr(&bytes.Buffer{})
					root.SetArgs(args)
					require.ErrorContains(t, root.Execute(), tc.want)
					if format == "json" {
						var doc jsonError
						require.NoError(t, json.Unmarshal(out.Bytes(), &doc))
						assert.Contains(t, doc.Error.Message, tc.want)
					} else {
						assert.Empty(t, out.String())
					}
					assert.Equal(t, before, initFileSnapshot(t, h.dir),
						"no lock, ignore file or artifact directory may be created for invalid rows")
					assert.Zero(t, h.project.wiringAttempts())
					assert.Empty(t, h.usage.reported())
				})
			}
		}
	}
}

func TestInitDeclaredLocalJSONLRejectionPreservesExistingConfig(t *testing.T) {
	for _, mode := range []string{"turn", "static"} {
		t.Run(mode, func(t *testing.T) {
			h := newInitHarness(t, nil)
			require.NoError(t, os.WriteFile(h.seedRows, []byte("{}\n"), 0o600))
			path := filepath.Join("team evals", "custom quality.yml")
			require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
			body := "# keep the declared dataset\ndatasets:\n  - name: golden\n    file: ../seed.jsonl\n"
			require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
			before := initFileSnapshot(t, h.dir)
			args := []string{"--path", path, "--name", "quality", "--source", "dataset",
				"--dataset", "golden", "--judge-model", "judge", "--no-prompt", "--output", "json"}
			if mode == "static" {
				args = append(args, "--conversation-mode", "static")
			} else {
				args = append(args, "--target", "agent")
			}
			text, err := executeConversationInit(t, args...)
			require.ErrorContains(t, err, "empty object")
			assert.Empty(t, text)
			assert.Equal(t, before, initFileSnapshot(t, h.dir))
			assert.Zero(t, h.project.wiringAttempts())
			assert.NoFileExists(t, filepath.Join("team evals", project.EvalConfigBase))
		})
	}
}

func TestInitLocalJSONLCorrectionPrecedesConfirmation(t *testing.T) {
	for _, mode := range []string{"turn", "static"} {
		for _, cancel := range []bool{false, true} {
			t.Run(mode+"/"+boolText(cancel), func(t *testing.T) {
				t.Setenv("AZD_NO_PROMPT", "false")
				prompts := &seedCorrectionPromptServer{}
				h := newInitHarness(t, nil, prompts)
				require.NoError(t, os.WriteFile(h.seedRows, []byte("{}\n"), 0o600))
				if !cancel {
					require.NoError(t, os.WriteFile("corrected.jsonl", []byte("{\"query\":\"valid\"}\n"), 0o600))
					prompts.datasets = []string{"./corrected.jsonl"}
				}
				before := initFileSnapshot(t, h.dir)
				args := []string{"--name", "quality", "--source", "dataset", "--dataset", h.seedRows,
					"--judge-model", "judge", "--evaluation-level", "turn"}
				if mode == "static" {
					args = []string{"--name", "quality", "--dataset", h.seedRows,
						"--judge-model", "judge", "--conversation-mode", "static"}
				} else {
					args = append(args, "--target", "agent")
				}
				text, err := executeConversationInit(t, args...)
				assert.Contains(t, text, "empty object")
				if cancel {
					require.Error(t, err)
					assert.Equal(t, before, initFileSnapshot(t, h.dir))
					assert.Zero(t, h.project.wiringAttempts())
				} else {
					require.NoError(t, err)
					cfg, err := project.OpenEvalConfig(filepath.Join(h.dir, project.DefaultEvalDir))
					require.NoError(t, err)
					require.Len(t, cfg.Evals, 1)
					assert.Equal(t, "corrected", cfg.Evals[0].Dataset)
				}
				prompts.mu.Lock()
				defer prompts.mu.Unlock()
				assert.Equal(t, []int{0}, prompts.selectCounts, "invalid rows are corrected before confirmation")
				if cancel {
					assert.Empty(t, prompts.messages)
				} else {
					assert.Len(t, prompts.messages, 1)
				}
			})
		}
	}
}
