// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"azureaieval/internal/exterrors"
	"azureaieval/internal/project"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
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
// work with.
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

// A malformed or empty JSONL row used to reach -o json with a
// message and no code at all.
func TestInitScaffold_LocalDatasetValidationCarriesAStableCode(t *testing.T) {
	for _, tt := range []struct{ name, contents, wantErr string }{
		{"a line that is not JSON", "not-json\n", "is not valid JSON"},
		{"an object row that carries nothing", "{}\n", "is an empty object"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			err := validateJSONL(writeLocalDataset(t, "rows.jsonl", tt.contents))

			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
			local, ok := errors.AsType[*azdext.LocalError](err)
			require.True(t, ok, "a local dataset validation failure must carry a structured code")
			assert.Equal(t, exterrors.CodeInvalidParameter, local.Code)
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
		{"malformed", "{\"query\":\"valid\",\"messages\":[]}\nnot-json\n", "line 2 is not valid JSON"},
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
					rows := "{\"query\":\"valid\"}\n"
					if mode == "static" {
						rows = "{\"messages\":[]}\n"
					}
					require.NoError(t, os.WriteFile("corrected.jsonl", []byte(rows), 0o600))
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

func TestValidateInitStaticConversationRequiresMessagesEveryRow(t *testing.T) {
	for _, tc := range []struct {
		name, rows, want string
	}{
		{"turn row", `{"query":"q","response":"a"}`, `row 1 has no "messages"`},
		{"seed row", `{"test_case_description":"help"}`, `row 1 has no "messages"`},
		{"mixed later turn", "{\"messages\":[]}\n{\"query\":\"q\",\"response\":\"a\"}", `row 2 has no "messages"`},
		{"mixed first turn", "{\"query\":\"q\",\"response\":\"a\"}\n{\"messages\":[]}", `row 1 has no "messages"`},
		{"blank line before turn", "{\"messages\":[]}\n\n{\"query\":\"q\"}", `row 2 has no "messages"`},
		{"empty messages", `{"messages":[]}`, ""},
		{"completed messages", `{"messages":[{"role":"user","content":"hi"}]}`, ""},
		{"mixed optional fields", "{\"messages\":[],\"category\":\"a\"}\n{\"messages\":[]}", ""},
		{"BOM and blank lines", "\uFEFF{\"messages\":[]}\n\n{\"messages\":[]}", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := writeLocalDataset(t, "completed.jsonl", tc.rows)
			err := validateInitDataset(t.Context(), filepath.Join(filepath.Dir(path), project.EvalConfigBase),
				initAnswers{source: initSourceDataset, datasetRef: path,
					evaluationLevel: project.EvaluationLevelConversation, conversationMode: conversationModeStatic},
				&project.EvalConfig{})
			if tc.want == "" {
				require.NoError(t, err)
			} else {
				require.ErrorContains(t, err, tc.want)
				local, ok := errors.AsType[*azdext.LocalError](err)
				require.True(t, ok)
				assert.Equal(t, exterrors.CodeInvalidParameter, local.Code)
				assert.Contains(t, local.Suggestion, "--evaluation-level turn")
			}
		})
	}
}

func TestInitStaticConversationMissingMessagesPrecedesWrites(t *testing.T) {
	for _, rows := range []string{
		`{"query":"q","response":"a"}`,
		"{\"messages\":[]}\n{\"query\":\"q\",\"response\":\"a\"}",
	} {
		for _, datasetKind := range []string{"path", "named", "ref-only"} {
			for _, format := range []string{"default", "json"} {
				t.Run(rows+"/"+datasetKind+"/"+format, func(t *testing.T) {
					h := newInitHarness(t, nil)
					require.NoError(t, os.WriteFile(h.seedRows, []byte(rows), 0o600))
					dataset := h.seedRows
					config := filepath.Join(h.dir, project.DefaultEvalDir, project.EvalConfigBase)
					if datasetKind != "path" {
						require.NoError(t, os.MkdirAll(filepath.Dir(config), 0o700))
						body := "# preserve authored content\ndatasets:\n  - name: completed\n    file: ../seed.jsonl\n"
						if datasetKind == "ref-only" {
							require.NoError(t, os.WriteFile(filepath.Join(filepath.Dir(config), "completed.yaml"),
								[]byte("name: completed\nfile: ../seed.jsonl\n"), 0o600))
							body = "# preserve authored content\ndatasets:\n  - $ref: completed.yaml\n"
						}
						require.NoError(t, os.WriteFile(config, []byte(body), 0o600))
						dataset = "completed"
					}
					before := initFileSnapshot(t, h.dir)
					root := NewRootCommand()
					root.SetContext(t.Context())
					root.SilenceErrors, root.SilenceUsage = true, true
					var out bytes.Buffer
					root.SetOut(&out)
					root.SetErr(&bytes.Buffer{})
					// An omitted mode must also validate the implicit static choice.
					root.SetArgs([]string{"init", "--name", "quality", "--source", "dataset",
						"--evaluation-level", "conversation", "--dataset", dataset,
						"--judge-model", "judge", "--no-prompt", "--output", format})
					require.ErrorContains(t, root.Execute(), `"messages"`)
					if format == "json" {
						var doc jsonError
						require.NoError(t, json.Unmarshal(out.Bytes(), &doc))
						assert.Contains(t, doc.Error.Message, `"messages"`)
						assert.Equal(t, exterrors.CodeInvalidParameter, doc.Error.Code)
					} else {
						assert.Empty(t, out.String())
					}
					assert.Equal(t, before, initFileSnapshot(t, h.dir))
					assert.Zero(t, h.project.wiringAttempts())
					assert.Empty(t, h.usage.reported())
				})
			}
		}
	}
}

func TestInitStaticDatasetValidationPreservesOtherSources(t *testing.T) {
	for _, tc := range []struct {
		name, dataset, rows, level string
		simulation                 *project.Simulation
	}{
		{"turn", "", `{"query":"q","response":"a"}`, project.EvaluationLevelTurn, nil},
		{"simulation", "", `{"test_case_description":"help"}`, project.EvaluationLevelConversation,
			&project.Simulation{Model: "connection/model"}},
		{"registered static", "published-conversations", "", project.EvaluationLevelConversation, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			location := filepath.Join(t.TempDir(), project.EvalConfigBase)
			dataset := tc.dataset
			if dataset == "" {
				dataset = writeLocalDataset(t, "rows.jsonl", tc.rows)
			}
			require.NoError(t, validateInitDataset(t.Context(), location, initAnswers{
				source: initSourceDataset, datasetRef: dataset, evaluationLevel: tc.level, simulation: tc.simulation,
			}, &project.EvalConfig{}))
		})
	}
}

func TestInitStaticConversationRevalidatesBeforeScaffold(t *testing.T) {
	t.Setenv("AZD_NO_PROMPT", "false")
	prompts := &conversationPromptServer{}
	h := newInitHarness(t, nil, prompts)
	require.NoError(t, os.WriteFile(h.seedRows, []byte("{\"messages\":[]}\n"), 0o600))
	config := filepath.Join(h.dir, project.DefaultEvalDir, project.EvalConfigBase)
	unlock, err := project.LockEvalConfig(t.Context(), config)
	require.NoError(t, err)
	unlock()
	want := initFileSnapshot(t, h.dir)
	changed := "{\"query\":\"q\",\"response\":\"a\"}\n"
	relative, err := filepath.Rel(h.dir, h.seedRows)
	require.NoError(t, err)
	want[relative] = changed
	prompts.onConfirm = func() error {
		return os.WriteFile(h.seedRows, []byte(changed), 0o600)
	}
	_, err = executeConversationInit(t, "--name", "quality", "--conversation-mode", "static",
		"--dataset", h.seedRows, "--judge-model", "judge", "--evaluator", "builtin.task_completion")
	require.ErrorContains(t, err, `row 1 has no "messages"`)
	assert.Equal(t, want, initFileSnapshot(t, h.dir), "only the concurrent dataset edit survives")
	assert.Zero(t, h.project.wiringAttempts())
	assert.Empty(t, h.usage.reported())
	prompts.mu.Lock()
	defer prompts.mu.Unlock()
	require.Len(t, prompts.messages, 1, "the dataset changes at the actual confirmation prompt")
}
