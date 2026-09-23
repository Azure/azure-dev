// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"azureaieval/internal/messages"
	"azureaieval/internal/project"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func simulationInitArgs(dataset string) []string {
	return []string{"--name", "simulation", "--conversation-mode", "simulation",
		"--target", "agent", "--dataset", dataset, "--simulation-model", "simulator", "--judge-model", "judge"}
}

func initFileSnapshot(t *testing.T, dir string) map[string]string {
	t.Helper()
	root, err := os.OpenRoot(dir)
	require.NoError(t, err)
	defer root.Close()
	files := map[string]string{}
	require.NoError(t, filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		if entry.IsDir() {
			files[relative] = "<directory>"
			return nil
		}
		body, err := root.ReadFile(relative)
		if err != nil {
			return err
		}
		files[relative] = string(body)
		return nil
	}))
	return files
}

func TestInitSimulationRefusesLocalRowsBeforeAnyWrites(t *testing.T) {
	for _, tc := range []struct {
		name string
		rows string
		want string
	}{
		{"blank and zero", `{"test_case_description":"","desired_num_turns":0}`, "empty or non-text"},
		{"missing description", `{"desired_num_turns":1}`, `no "test_case_description"`},
		{"whitespace", `{"test_case_description":" \t\r\n "}`, "empty or non-text"},
		{"null description", `{"test_case_description":null}`, "empty or non-text"},
		{"number description", `{"test_case_description":42}`, "empty or non-text"},
		{"boolean description", `{"test_case_description":true}`, "empty or non-text"},
		{"zero turns", `{"test_case_description":"help","desired_num_turns":0}`, "positive whole number"},
		{"negative turns", `{"test_case_description":"help","desired_num_turns":-1}`, "positive whole number"},
		{"fractional turns", `{"test_case_description":"help","desired_num_turns":1.5}`, "positive whole number"},
		{"string turns", `{"test_case_description":"help","desired_num_turns":"1"}`, "positive whole number"},
		{"null turns", `{"test_case_description":"help","desired_num_turns":null}`, "positive whole number"},
		{"boolean turns", `{"test_case_description":"help","desired_num_turns":true}`, "positive whole number"},
		{"over explicit cap", `{"test_case_description":"help","desired_num_turns":21}`, "max_turns is 20"},
		{"completed messages", `{"test_case_description":"help","messages":[]}`, `carries "messages"`},
		{"query field", `{"test_case_description":"help","query":"hello"}`, `carries "query"`},
		{"empty query", `{"test_case_description":"help","query":""}`, `carries "query"`},
		{"null query", `{"test_case_description":"help","query":null}`, `carries "query"`},
		{"response field", `{"test_case_description":"help","response":"hi"}`, `carries "response"`},
		{"empty response", `{"test_case_description":"help","response":""}`, `carries "response"`},
		{"null response", `{"test_case_description":"help","response":null}`, `carries "response"`},
		{"late bad row", "\n{\"test_case_description\":\"help\"}\n\n{\"test_case_description\":\"\"}\n", "row 2"},
		{"mixed shapes", "{\"test_case_description\":\"help\"}\n{\"messages\":[]}\n", `row 2 carries "messages"`},
		{"mixed turn rows", "{\"test_case_description\":\"help\"}\n{\"query\":\"hi\"}\n", `row 2 carries "query"`},
		{"malformed", `{"test_case_description":`, "not valid JSON"},
		{"empty", "\n \n", "no rows"},
		{"empty object", `{}`, "empty object"},
	} {
		for _, output := range []string{"human", "json"} {
			t.Run(tc.name+"/"+output, func(t *testing.T) {
				h := newInitHarness(t, nil)
				require.NoError(t, os.WriteFile(h.seedRows, []byte(tc.rows), 0o600))
				private := filepath.Join(h.dir, ".azure", "dev")
				require.NoError(t, os.MkdirAll(private, 0o700))
				require.NoError(t, os.WriteFile(filepath.Join(private, ".env"), []byte("KEEP=unchanged\n"), 0o600))
				require.NoError(t, os.WriteFile(filepath.Join(private, "config.json"), []byte(`{"keep":true}`), 0o600))
				before := initFileSnapshot(t, h.dir)
				args := append(simulationInitArgs("./seed.jsonl"), "--max-turns", "20", "--no-prompt")
				if output == "json" {
					args = append(args, "--output", "json")
				}
				text, err := executeConversationInit(t, args...)
				require.ErrorContains(t, err, tc.want)
				assert.Empty(t, text, "no success-shaped output, prompts or JSON contamination")
				assert.Zero(t, h.project.wiringAttempts())
				assert.Empty(t, h.usage.reported())
				assert.Equal(t, before, initFileSnapshot(t, h.dir),
					"refusal must precede config locks, directories, scaffolds and azure.yaml wiring")
			})
		}
	}
}

func TestInitSimulationChecksDeclaredLocalFilesAndNestedRefs(t *testing.T) {
	for _, declaration := range []string{
		"    file: ./parts/rows.jsonl\n",
		"    $ref: ./parts/dataset.yaml\n",
	} {
		for _, row := range []string{
			`{"test_case_description":""}`,
			`{"test_case_description":"help","desired_num_turns":0}`,
		} {
			t.Run(declaration+row, func(t *testing.T) {
				h := newInitHarness(t, nil)
				dir := filepath.Join(h.dir, "nested", "quality")
				parts := filepath.Join(dir, "parts")
				require.NoError(t, os.MkdirAll(filepath.Join(parts, "inner"), 0o700))
				require.NoError(t, os.WriteFile(filepath.Join(parts, "rows.jsonl"), []byte(row), 0o600))
				require.NoError(t, os.WriteFile(filepath.Join(parts, "dataset.yaml"),
					[]byte("$ref: ./inner/dataset.yaml\n"), 0o600))
				require.NoError(t, os.WriteFile(filepath.Join(parts, "inner", "dataset.yaml"),
					[]byte("file: ../rows.jsonl\nfuture_metadata: keep\n"), 0o600))
				body := "# Preserve my file\nfuture_setting: keep\ndatasets:\n  - name: seeds\n" +
					declaration + "  - name: unrelated\n    $ref: ./does-not-exist.yaml\n" +
					"evaluators:\n  - name: custom\n    $ref: ./also-missing.yaml\n"
				require.NoError(t, os.WriteFile(filepath.Join(dir, project.EvalConfigBase), []byte(body), 0o600))
				before := initFileSnapshot(t, h.dir)
				args := append(simulationInitArgs("seeds"), "--path", filepath.Join("nested", "quality"),
					"--output", "json")
				text, err := executeConversationInit(t, args...)
				require.ErrorContains(t, err, "row 1")
				assert.Empty(t, text)
				assert.Zero(t, h.project.wiringAttempts())
				assert.Equal(t, before, initFileSnapshot(t, h.dir))
			})
		}
	}
}

func TestInitSimulationLocalRowsPreserveValidBounds(t *testing.T) {
	for _, tc := range []struct {
		name string
		rows string
		args []string
		max  int
	}{
		{"omitted turns", `{"test_case_description":"help"}`, nil, 0},
		{"minimum", `{"test_case_description":"help","desired_num_turns":1}`, []string{"--max-turns", "1"}, 1},
		{"maximum", `{"test_case_description":"help","desired_num_turns":20}`, []string{"--max-turns", "20"}, 20},
		{"no invented ceiling", `{"test_case_description":"help","desired_num_turns":21}`, nil, 0},
		{"BOM and blanks", "\xef\xbb\xbf\n\n{\"test_case_description\":\"help\"}\n \n", nil, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newInitHarness(t, nil)
			require.NoError(t, os.WriteFile(h.seedRows, []byte(tc.rows), 0o600))
			args := append(simulationInitArgs(h.seedRows), tc.args...)
			text, err := executeConversationInit(t, append(args, "--output", "json")...)
			require.NoError(t, err)
			assert.True(t, json.Valid([]byte(text)), "stdout must be one valid JSON document")
			cfg, err := project.OpenEvalConfig(filepath.Join(h.dir, "evals"))
			require.NoError(t, err)
			require.Len(t, cfg.Evals, 1)
			require.NotNil(t, cfg.Evals[0].Simulation)
			assert.Equal(t, tc.max, cfg.Evals[0].Simulation.MaxTurns)
			assert.Equal(t, "simulator", cfg.Evals[0].Simulation.Model)
			assert.Equal(t, "judge", cfg.Evals[0].Evaluators[0].InitializationParameters["model"])
		})
	}
}

func TestInitSimulationValidatesAfterInteractiveModeAndModel(t *testing.T) {
	t.Setenv("AZD_NO_PROMPT", "false")
	prompts := &seedCorrectionPromptServer{conversationPromptServer: conversationPromptServer{mode: 1}}
	h := newInitHarness(t, nil, prompts)
	require.NoError(t, os.WriteFile(h.seedRows, []byte(`{"test_case_description":""}`), 0o600))
	before := initFileSnapshot(t, h.dir)
	text, err := executeConversationInit(t, "--name", "simulation", "--source", "dataset",
		"--evaluation-level", "conversation", "--target", "agent", "--dataset", h.seedRows, "--judge-model", "judge")
	require.Error(t, err)
	assert.True(t, cancelled(err))
	assert.Contains(t, text, "empty or non-text")
	prompts.mu.Lock()
	defer prompts.mu.Unlock()
	require.Len(t, prompts.models, 1, "the simulator was chosen before validating the seed rows")
	assert.Len(t, prompts.messages, 1, "the mode picker ran, but no Add confirmation")
	assert.Len(t, prompts.selectCounts, 1, "invalid rows offered correction before cancellation")
	assert.Equal(t, before, initFileSnapshot(t, h.dir))
}

type seedCorrectionPromptServer struct {
	conversationPromptServer
	datasets     []string
	selectCounts []int
}

func (s *seedCorrectionPromptServer) Prompt(
	ctx context.Context, req *azdext.PromptRequest,
) (*azdext.PromptResponse, error) {
	if req.GetOptions().GetMessage() != messages.EnterDatasetPrompt() {
		return s.conversationPromptServer.Prompt(ctx, req)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.selectCounts = append(s.selectCounts, len(s.messages))
	if len(s.datasets) == 0 {
		return nil, status.Error(codes.Canceled, "cancelled by reader")
	}
	answer := s.datasets[0]
	s.datasets = s.datasets[1:]
	return &azdext.PromptResponse{Value: answer}, nil
}

func TestInitSimulationCorrectsInvalidDatasetBeforeConfirmation(t *testing.T) {
	for _, input := range []string{"explicit path", "prompted path", "declared alias", "default declaration"} {
		t.Run(input, func(t *testing.T) {
			t.Setenv("AZD_NO_PROMPT", "false")
			prompts := &seedCorrectionPromptServer{}
			h := newInitHarness(t, nil, prompts)
			require.NoError(t, os.WriteFile(h.seedRows, []byte(`{"test_case_description":" "}`), 0o600))
			require.NoError(t, os.WriteFile(filepath.Join(h.dir, "zero.jsonl"),
				[]byte(`{"test_case_description":"help","desired_num_turns":0}`), 0o600))
			require.NoError(t, os.WriteFile(filepath.Join(h.dir, "mixed.jsonl"),
				[]byte(`{"test_case_description":"help","query":null}`), 0o600))
			require.NoError(t, os.WriteFile(filepath.Join(h.dir, "corrected.jsonl"),
				[]byte(`{"test_case_description":"help","desired_num_turns":1}`), 0o600))

			dir := filepath.Join(h.dir, "evals")
			require.NoError(t, os.MkdirAll(dir, 0o700))
			body := "# Keep my catalogue\nevaluators: []\n"
			dataset := "./seed.jsonl"
			if input == "declared alias" || input == "default declaration" {
				dataset = "seeds"
				require.NoError(t, os.MkdirAll(filepath.Join(dir, "parts"), 0o700))
				require.NoError(t, os.WriteFile(filepath.Join(dir, "parts", "dataset.yaml"),
					[]byte("file: ../../seed.jsonl\n"), 0o600))
				body += "datasets:\n  - name: seeds\n    $ref: ./parts/dataset.yaml\n"
			}
			path := filepath.Join(dir, project.EvalConfigBase)
			require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
			prompts.datasets = []string{"./zero.jsonl", "./mixed.jsonl", "./corrected.jsonl"}
			args := []string{"--name", "simulation", "--conversation-mode", "simulation",
				"--target", "agent", "--simulation-model", "simulator", "--judge-model", "judge"}
			if input == "prompted path" || input == "default declaration" {
				if input == "prompted path" {
					prompts.datasets = append([]string{"./seed.jsonl"}, prompts.datasets...)
				}
			} else {
				args = append(args, "--dataset", dataset)
			}
			args = append(args, "--num-conversations", "3", "--max-turns", "5")
			text, err := executeConversationInit(t, args...)
			require.NoError(t, err)
			assert.Contains(t, text, "empty or non-text")
			assert.Contains(t, text, "positive whole number")
			assert.Contains(t, text, `carries "query"`)
			assert.Contains(t, text, "Press Ctrl+C to cancel")
			cfg, err := project.OpenEvalConfig(dir)
			require.NoError(t, err)
			require.Len(t, cfg.Evals, 1)
			group := cfg.Evals[0]
			assert.Equal(t, "simulation", group.Name)
			assert.Equal(t, "corrected", group.Dataset)
			require.NotNil(t, group.Simulation)
			assert.Equal(t, &project.Simulation{Model: "simulator", NumConversations: 3, MaxTurns: 5},
				group.Simulation)
			assert.Equal(t, "judge", group.Evaluators[0].InitializationParameters["model"])
			after, err := os.ReadFile(path)
			require.NoError(t, err)
			assert.Contains(t, string(after), body, "correction must not replace the original declaration")
			prompts.mu.Lock()
			defer prompts.mu.Unlock()
			assert.Empty(t, prompts.models, "correction must retain independent model choices")
			assert.Empty(t, prompts.datasets)
			for _, count := range prompts.selectCounts {
				assert.Zero(t, count, "no confirmation before every correction was validated")
			}
			assert.Len(t, prompts.messages, 1, "only the final valid scaffold reaches confirmation")
		})
	}
}

func TestInitSimulationCorrectionCancellationPreservesFiles(t *testing.T) {
	for _, cancelAt := range []string{"correction", "confirmation"} {
		t.Run(cancelAt, func(t *testing.T) {
			t.Setenv("AZD_NO_PROMPT", "false")
			prompts := &seedCorrectionPromptServer{conversationPromptServer: conversationPromptServer{decision: 2}}
			h := newInitHarness(t, nil, prompts)
			require.NoError(t, os.WriteFile(h.seedRows, []byte(`{"test_case_description":" "}`), 0o600))
			private := filepath.Join(h.dir, ".azure", "dev")
			require.NoError(t, os.MkdirAll(private, 0o700))
			require.NoError(t, os.WriteFile(filepath.Join(private, ".env"), []byte("KEEP=unchanged\n"), 0o600))
			require.NoError(t, os.WriteFile(filepath.Join(private, "config.json"), []byte(`{"keep":true}`), 0o600))
			if cancelAt == "confirmation" {
				require.NoError(t, os.WriteFile(filepath.Join(h.dir, "valid.jsonl"),
					[]byte(`{"test_case_description":"help"}`), 0o600))
				prompts.datasets = []string{"./valid.jsonl"}
			}
			before := initFileSnapshot(t, h.dir)
			text, err := executeConversationInit(t, simulationInitArgs("./seed.jsonl")...)
			prompts.mu.Lock()
			defer prompts.mu.Unlock()
			if cancelAt == "correction" {
				require.Error(t, err)
				assert.True(t, cancelled(err))
				assert.Empty(t, prompts.messages)
			} else {
				require.NoError(t, err)
				assert.Contains(t, text, messages.ScaffoldCancelled())
				assert.Len(t, prompts.messages, 1)
			}
			assert.Contains(t, text, "empty or non-text")
			assert.Zero(t, h.project.wiringAttempts())
			assert.Empty(t, h.usage.reported())
			assert.Equal(t, before, initFileSnapshot(t, h.dir))
		})
	}
}

func TestInitSimulationCorrectionRetriesAreBounded(t *testing.T) {
	t.Setenv("AZD_NO_PROMPT", "false")
	prompts := &seedCorrectionPromptServer{}
	h := newInitHarness(t, nil, prompts)
	require.NoError(t, os.WriteFile(h.seedRows, []byte(`{"test_case_description":"help","desired_num_turns":0}`), 0o600))
	for range 8 {
		prompts.datasets = append(prompts.datasets, "./seed.jsonl")
	}
	before := initFileSnapshot(t, h.dir)
	_, err := executeConversationInit(t, simulationInitArgs("./seed.jsonl")...)
	require.ErrorContains(t, err, "positive whole number")
	prompts.mu.Lock()
	defer prompts.mu.Unlock()
	assert.Len(t, prompts.selectCounts, 8)
	assert.Empty(t, prompts.messages, "never confirm invalid rows")
	assert.Zero(t, h.project.wiringAttempts())
	assert.Equal(t, before, initFileSnapshot(t, h.dir))
}

func TestInitSimulationKeepsDeclaredDatasetRefs(t *testing.T) {
	for _, named := range []bool{true, false} {
		t.Run(map[bool]string{true: "named", false: "ref only"}[named], func(t *testing.T) {
			h := newInitHarness(t, nil)
			dir := filepath.Join(h.dir, "nested", "quality")
			require.NoError(t, os.MkdirAll(filepath.Join(dir, "parts"), 0o700))
			require.NoError(t, os.WriteFile(filepath.Join(dir, "parts", "rows.jsonl"),
				[]byte(`{"test_case_description":"help"}`), 0o600))
			included := "name: seeds\nfile: ./rows.jsonl\n"
			require.NoError(t, os.WriteFile(filepath.Join(dir, "parts", "dataset.yaml"), []byte(included), 0o600))
			declaration := "  - $ref: ./parts/dataset.yaml\n"
			if named {
				declaration = "  - name: seeds\n    $ref: ./parts/dataset.yaml\n"
			}
			body := "# Keep my references\nfuture_metadata: keep\ndatasets:\n" + declaration
			path := filepath.Join(dir, project.EvalConfigBase)
			require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
			args := append(simulationInitArgs("seeds"), "--path", dir, "--output", "json")
			text, err := executeConversationInit(t, args...)
			require.NoError(t, err)
			assert.True(t, json.Valid([]byte(text)))
			after, err := os.ReadFile(path)
			require.NoError(t, err)
			assert.Contains(t, string(after), body, "existing comments, unknown metadata and refs are preserved")
			authored, err := project.ReadAuthoredConfig(path)
			require.NoError(t, err)
			if named {
				assert.Equal(t, []string{"seeds"}, authored.Names(project.SectionDatasets))
			} else {
				assert.Empty(t, authored.Names(project.SectionDatasets), "do not redeclare a ref-only dataset")
				assert.True(t, authored.HasUnnamedRef(project.SectionDatasets))
			}
			assert.Contains(t, authored.Names(project.SectionEvals), "simulation")
			refAfter, err := os.ReadFile(filepath.Join(dir, "parts", "dataset.yaml"))
			require.NoError(t, err)
			assert.Equal(t, included, string(refAfter))
		})
	}
}

func TestInitLocalSeedValidationLeavesOtherModesAndRegisteredNamesAlone(t *testing.T) {
	for _, mode := range []string{"static", "turn", "registered", "declared registered"} {
		t.Run(mode, func(t *testing.T) {
			h := newInitHarness(t, nil)
			require.NoError(t, os.WriteFile(h.seedRows, []byte(`{"messages":[]}`), 0o600))
			args := []string{"--name", "quality", "--dataset", h.seedRows, "--judge-model", "judge", "--output", "json"}
			switch mode {
			case "static":
				args = append(args, "--conversation-mode", "static")
			case "turn":
				args = append(args, "--target", "agent", "--evaluation-level", "turn")
			default:
				args = append(simulationInitArgs("registered-seeds"), "--output", "json")
				if mode == "declared registered" {
					dir := filepath.Join(h.dir, "evals")
					require.NoError(t, os.MkdirAll(dir, 0o700))
					require.NoError(t, os.WriteFile(filepath.Join(dir, project.EvalConfigBase),
						[]byte("datasets:\n  - name: registered-seeds\n    version: '1.0'\n"), 0o600))
				}
			}
			text, err := executeConversationInit(t, args...)
			require.NoError(t, err)
			assert.True(t, json.Valid([]byte(text)))
			assertOneInitCompleted(t, h, "dataset")
		})
	}
}
