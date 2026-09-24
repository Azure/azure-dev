// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"azureaieval/internal/exterrors"
	"azureaieval/internal/messages"
	"azureaieval/internal/project"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func executeConversationInit(t *testing.T, args ...string) (string, error) {
	t.Helper()
	cmd := newInitCommand()
	cmd.Flags().Bool("no-prompt", false, "")
	cmd.Flags().StringP("output", "o", "", "")
	cmd.SetContext(t.Context())
	cmd.SilenceErrors, cmd.SilenceUsage = true, true
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return out.String(), err
}

func TestInitConversationModesWriteRunnableConfig(t *testing.T) {
	cases := []struct {
		name   string
		args   []string
		mode   string
		count  int
		turns  int
		target bool
		trace  bool
	}{
		{"static", []string{"--conversation-mode", "static"}, conversationModeStatic, 0, 0, false, false},
		{"static default", []string{"--source", "dataset", "--evaluation-level", "conversation"},
			conversationModeStatic, 0, 0, false, false},
		{"simulation defaults", []string{"--conversation-mode", "simulation", "--target", "agent",
			"--simulation-model", "connection/simulator"}, conversationModeSimulation, 1, 0, true, false},
		{"simulation minimum", []string{"--conversation-mode", "simulation", "--target", "agent",
			"--simulation-model", "connection/simulator", "--num-conversations", "1", "--max-turns", "1"},
			conversationModeSimulation, 1, 1, true, false},
		{"simulation maximum", []string{"--conversation-mode", "simulation", "--target", "agent",
			"--simulation-model", "connection/simulator", "--num-conversations", "5", "--max-turns", "20"},
			conversationModeSimulation, 5, 20, true, false},
		{"turn unchanged", []string{"--source", "dataset", "--target", "agent"}, "", 0, 0, true, false},
		{"trace conversation unchanged", []string{"--source", "traces", "--target", "agent",
			"--evaluation-level", "conversation"}, "", 0, 0, false, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newInitHarness(t, nil)
			args := append([]string{"--name", "quality", "--judge-model", "judge", "--output", "json"}, tc.args...)
			if !tc.trace {
				args = append(args, "--dataset", "registered-data")
			}
			text, err := executeConversationInit(t, args...)
			require.NoError(t, err)
			var doc struct {
				ConversationMode string              `json:"conversationMode"`
				EvaluationLevel  string              `json:"evaluationLevel"`
				Target           string              `json:"target"`
				Simulation       *project.Simulation `json:"simulation"`
			}
			require.NoError(t, json.Unmarshal([]byte(text), &doc), "stdout must be exactly one JSON document")
			assert.Equal(t, tc.mode, doc.ConversationMode)

			cfg, err := project.OpenEvalConfig(filepath.Join(h.dir, "evals"))
			require.NoError(t, err)
			require.Len(t, cfg.Evals, 1)
			eval := cfg.Evals[0]
			assert.Equal(t, eval.EvaluationLevel, doc.EvaluationLevel)
			assert.NotEmpty(t, doc.EvaluationLevel, "the selected level must be present in machine-readable output")
			require.NoError(t, project.ValidateRunnable(&eval))
			assert.Equal(t, "judge", eval.Evaluators[0].InitializationParameters["model"])
			assert.Equal(t, "builtin.task_completion", eval.Evaluators[0].Evaluator)
			assert.Equal(t, 1, h.project.wiringAttempts())
			if tc.target {
				require.NotNil(t, eval.Target)
				assert.Equal(t, project.TargetTypeAgent, eval.Target.Type)
				assert.Equal(t, "agent", eval.Target.Name)
			} else {
				assert.Nil(t, eval.Target)
			}
			if tc.trace {
				require.NotNil(t, eval.Source)
				assert.Equal(t, "agent", eval.Source.AgentName)
			} else {
				assert.Nil(t, eval.Source)
			}
			if tc.mode == conversationModeSimulation {
				require.NotNil(t, eval.Simulation)
				assert.Equal(t, "connection/simulator", eval.Simulation.Model)
				assert.Equal(t, tc.count, eval.Simulation.NumConversations)
				assert.Equal(t, tc.turns, eval.Simulation.MaxTurns)
				assert.Equal(t, eval.Simulation, doc.Simulation)
				raw, err := os.ReadFile(filepath.Join(h.dir, "evals", "azure.eval.yaml"))
				require.NoError(t, err)
				if tc.turns == 0 {
					assert.NotContains(t, string(raw), "max_turns:")
				} else {
					assert.Contains(t, string(raw), fmt.Sprintf("max_turns: %d", tc.turns))
				}
			} else {
				assert.Nil(t, eval.Simulation)
				assert.Nil(t, doc.Simulation)
			}
			if tc.mode == conversationModeStatic {
				assert.Empty(t, doc.Target)
			}
		})
	}
}

func TestInitConversationRejectsIgnoredFlagsAndNumericBounds(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want string
		code string
	}{
		{"unknown mode", []string{"--conversation-mode", "other"}, "static or simulation", exterrors.CodeInvalidParameter},
		{"empty mode", []string{"--conversation-mode="}, "static or simulation", exterrors.CodeInvalidParameter},
		{"empty simulator", []string{"--conversation-mode", "simulation", "--simulation-model="},
			"--simulation-model", exterrors.CodeInvalidParameter},
		{"simulation turn", []string{"--conversation-mode", "simulation", "--evaluation-level", "turn"},
			"--evaluation-level conversation", exterrors.CodeConflictingArguments},
		{"simulation traces", []string{"--conversation-mode", "simulation", "--source", "traces"},
			"--source dataset", exterrors.CodeConflictingArguments},
		{"static traces", []string{"--conversation-mode", "static", "--source", "traces"},
			"--source dataset", exterrors.CodeConflictingArguments},
		{"static target", []string{"--conversation-mode", "static", "--target", "agent"},
			"--target", exterrors.CodeConflictingArguments},
		{"static empty target", []string{"--conversation-mode", "static", "--target="},
			"--target", exterrors.CodeConflictingArguments},
		{"default static target", []string{"--evaluation-level", "conversation", "--source", "dataset", "--target", "agent"},
			"--target", exterrors.CodeConflictingArguments},
		{"trace empty dataset", []string{"--source", "traces", "--dataset="},
			"--dataset", exterrors.CodeConflictingArguments},
	}
	for _, mode := range []string{"static", ""} {
		for _, flag := range []string{"simulation-model", "num-conversations", "max-turns"} {
			args := []string{"--" + flag + "=0"}
			if mode != "" {
				args = append(args, "--conversation-mode", mode)
			}
			cases = append(cases, struct {
				name string
				args []string
				want string
				code string
			}{mode + flag, args, "--" + flag, exterrors.CodeConflictingArguments})
		}
	}
	for _, bound := range []struct {
		flag string
		max  int
	}{{"num-conversations", 5}, {"max-turns", 20}} {
		for _, value := range []int{-1, 0, bound.max + 1} {
			cases = append(cases, struct {
				name string
				args []string
				want string
				code string
			}{
				fmt.Sprintf("%s %d", bound.flag, value),
				[]string{"--conversation-mode", "simulation", fmt.Sprintf("--%s=%d", bound.flag, value)},
				"--" + bound.flag, exterrors.CodeInvalidParameter,
			})
		}
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newInitHarness(t, nil)
			text, err := executeConversationInit(t, append(tc.args, "--no-prompt")...)
			require.ErrorContains(t, err, tc.want)
			local, ok := errors.AsType[*azdext.LocalError](err)
			require.True(t, ok, "flag errors must be structured: %v", err)
			assert.Equal(t, tc.code, local.Code)
			assert.Empty(t, text)
			assert.Zero(t, h.project.wiringAttempts())
			assert.False(t, scaffoldedAnything(t, filepath.Join(h.dir, "evals")))
		})
	}
}

func TestInitConversationAggregatesNoninteractiveRequiredInputs(t *testing.T) {
	for _, unattended := range [][]string{{"--no-prompt"}, {"--output", "json"}} {
		t.Run(strings.Join(unattended, " "), func(t *testing.T) {
			h := newInitHarness(t, nil)
			args := append([]string{"--conversation-mode", "simulation"}, unattended...)
			text, err := executeConversationInit(t, args...)
			require.Error(t, err)
			for _, flag := range []string{"--target", "--dataset", "--simulation-model", "--judge-model"} {
				assert.Contains(t, err.Error(), flag)
			}
			assert.Empty(t, text)
			assert.Zero(t, h.project.wiringAttempts())
			assert.False(t, scaffoldedAnything(t, filepath.Join(h.dir, "evals")))
		})
	}
}

type conversationPromptServer struct {
	azdext.UnimplementedPromptServiceServer
	mu        sync.Mutex
	mode      int32
	decision  int32
	messages  []string
	models    []*azdext.PromptOptions
	onConfirm func() error
}

func (s *conversationPromptServer) Select(
	_ context.Context, req *azdext.SelectRequest,
) (*azdext.SelectResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.messages = append(s.messages, req.GetOptions().GetMessage())
	if req.GetOptions().GetMessage() == messages.SelectConversationModePrompt() {
		return &azdext.SelectResponse{Value: new(s.mode)}, nil
	}
	if s.onConfirm != nil {
		if err := s.onConfirm(); err != nil {
			return nil, err
		}
	}
	return &azdext.SelectResponse{Value: new(s.decision)}, nil
}

func (s *conversationPromptServer) Prompt(
	_ context.Context, req *azdext.PromptRequest,
) (*azdext.PromptResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.models = append(s.models, req.GetOptions())
	if req.GetOptions().GetMessage() == messages.JudgeModelPrompt() {
		return &azdext.PromptResponse{Value: "judge"}, nil
	}
	return &azdext.PromptResponse{Value: "connection/simulator"}, nil
}

func (s *conversationPromptServer) MultiSelect(
	_ context.Context, req *azdext.MultiSelectRequest,
) (*azdext.MultiSelectResponse, error) {
	var chosen []*azdext.MultiSelectChoice
	for _, choice := range req.GetOptions().GetChoices() {
		if choice.GetSelected() {
			chosen = append(chosen, choice)
		}
	}
	return &azdext.MultiSelectResponse{Values: chosen}, nil
}

func TestInitConversationInteractivePickerAndModelPrompt(t *testing.T) {
	for _, mode := range []int32{0, 1} {
		t.Run(fmt.Sprint(mode), func(t *testing.T) {
			t.Setenv("AZD_NO_PROMPT", "false")
			prompts := &conversationPromptServer{mode: mode}
			h := newInitHarness(t, nil, prompts)
			args := []string{"--name", "quality", "--source", "dataset", "--evaluation-level", "conversation",
				"--dataset", "seeds", "--judge-model", "judge"}
			if mode == 1 {
				args = append(args, "--target", "agent")
			}
			text, err := executeConversationInit(t, args...)
			require.NoError(t, err)
			cfg, err := project.OpenEvalConfig(filepath.Join(h.dir, "evals"))
			require.NoError(t, err)
			require.Len(t, cfg.Evals, 1)
			prompts.mu.Lock()
			defer prompts.mu.Unlock()
			assert.Contains(t, prompts.messages, messages.SelectConversationModePrompt())
			if mode == 1 {
				require.Len(t, prompts.models, 1)
				assert.Equal(t, messages.SimulationModelPrompt(), prompts.models[0].Message)
				assert.Empty(t, prompts.models[0].DefaultValue, "never guess the simulator from the judge")
				require.NotNil(t, cfg.Evals[0].Simulation)
				assert.Equal(t, "connection/simulator", cfg.Evals[0].Simulation.Model)
				for _, want := range []string{"Simulation model", "simulator", "Judge model", "judge",
					"Conversations per seed", "Maximum turns", "service default"} {
					assert.Contains(t, text, want)
				}
			} else {
				assert.Empty(t, prompts.models)
				assert.Nil(t, cfg.Evals[0].Target)
				assert.Nil(t, cfg.Evals[0].Simulation)
				assert.Contains(t, text, "none (score completed messages)")
				assert.NotContains(t, text, "Agent:")
			}
		})
	}
}

func TestInitSimulationPreservesExistingConfigAndUnknownFields(t *testing.T) {
	h := newInitHarness(t, nil)
	dir := filepath.Join(h.dir, "evals")
	require.NoError(t, os.MkdirAll(dir, 0o700))
	configPath := filepath.Join(dir, "azure.eval.yaml")
	existing := "# keep this comment\nfuture_setting: keep-me\ndatasets:\n" +
		"  - name: previous-data\n    future_dataset_field: keep-too\n" +
		"evaluators:\n  - name: custom\n    future_evaluator_field: keep-also\n" +
		"evals:\n  - name: previous-eval\n    dataset: previous-data\n" +
		"    future_eval_field: keep-final\n    evaluators:\n      - evaluator: custom\n"
	require.NoError(t, os.WriteFile(configPath, []byte(existing), 0o600))
	require.NoError(t, h.runInit(t, "--name", "simulation", "--conversation-mode", "simulation",
		"--target", "agent", "--dataset", "seeds", "--simulation-model", "connection/simulator", "--judge-model", "judge"))
	raw, err := os.ReadFile(configPath)
	require.NoError(t, err)
	for _, want := range []string{"# keep this comment", "future_setting: keep-me", "future_dataset_field: keep-too",
		"future_evaluator_field: keep-also", "future_eval_field: keep-final", "name: previous-eval", "name: simulation"} {
		assert.Contains(t, string(raw), want)
	}
	before := string(raw)
	err = h.runInit(t, "--name", "simulation", "--conversation-mode", "simulation",
		"--target", "agent", "--dataset", "replacement", "--simulation-model", "connection/replacement",
		"--judge-model", "replacement")
	require.Error(t, err)
	after, err := os.ReadFile(configPath)
	require.NoError(t, err)
	assert.Equal(t, before, string(after))
}

func TestGeneratedConversationHandoffRunsThroughInit(t *testing.T) {
	h := newInitHarness(t, nil)
	outcomes := bothGenerated()[:1]
	outcomes[0].plan.EvaluationLevel = project.EvaluationLevelConversation
	outcomes[0].plan.Model = "generation-only"
	command := initHandoff(outcomes, "quality")
	assert.Contains(t, command, "--conversation-mode simulation")
	assert.NotContains(t, command, "generation-only")
	assert.NotContains(t, command, "--simulation-model")
	args := strings.Fields(strings.TrimPrefix(command, "azd ai eval init "))
	args = append(args, "--simulation-model", "connection/simulator", "--judge-model", "judge")
	require.NoError(t, h.runInit(t, args...))
	cfg, err := project.OpenEvalConfig(filepath.Join(h.dir, "quality"))
	require.NoError(t, err)
	require.Len(t, cfg.Evals, 1)
	require.NotNil(t, cfg.Evals[0].Simulation)
	assert.Equal(t, "connection/simulator", cfg.Evals[0].Simulation.Model)
	assert.Equal(t, "judge", cfg.Evals[0].Evaluators[0].InitializationParameters["model"])
	assert.Equal(t, "hero-agent", cfg.Evals[0].Target.Name)
}

func TestInitConversationHelpExplainsModeAndBounds(t *testing.T) {
	cmd := newInitCommand()
	require.NoError(t, cmd.Flags().Set("conversation-mode", "simulation"))
	assert.Equal(t, "1", cmd.Flags().Lookup("num-conversations").DefValue)
	for _, flag := range []string{"conversation-mode", "simulation-model", "num-conversations", "max-turns"} {
		assert.NotNil(t, cmd.Flags().Lookup(flag))
	}
	assert.Contains(t, cmd.Flags().Lookup("num-conversations").Usage, "1-5")
	assert.Contains(t, cmd.Flags().Lookup("max-turns").Usage, "1-20")
	assert.Contains(t, cmd.Flags().Lookup("max-turns").Usage, "service default")
	assert.Contains(t, cmd.Flags().Lookup("simulation-model").Usage, "Connection-name/model-deployment")
	assert.Contains(t, cmd.Long, "--output json")
	assert.Contains(t, cmd.Long, "best-effort")
}

func TestGeneratedConversationHandoffPromptsForIndependentModels(t *testing.T) {
	t.Setenv("AZD_NO_PROMPT", "false")
	prompts := &conversationPromptServer{}
	h := newInitHarness(t, nil, prompts)
	outcomes := bothGenerated()[:1]
	outcomes[0].plan.EvaluationLevel = project.EvaluationLevelConversation
	outcomes[0].plan.Model = "generation-only"
	command := initHandoff(outcomes, "")
	args := strings.Fields(strings.TrimPrefix(command, "azd ai eval init "))
	args = append(args, "--name", "quality")
	_, err := executeConversationInit(t, args...)
	require.NoError(t, err)
	cfg, err := project.OpenEvalConfig(filepath.Join(h.dir, "evals"))
	require.NoError(t, err)
	assert.Equal(t, "connection/simulator", cfg.Evals[0].Simulation.Model)
	assert.Equal(t, "judge", cfg.Evals[0].Evaluators[0].InitializationParameters["model"])
	prompts.mu.Lock()
	defer prompts.mu.Unlock()
	require.Len(t, prompts.models, 2)
	assert.Equal(t, messages.SimulationModelPrompt(), prompts.models[0].Message)
	assert.Equal(t, messages.JudgeModelPrompt(), prompts.models[1].Message)
	for _, prompt := range prompts.models {
		assert.Empty(t, prompt.DefaultValue)
	}
}

func TestInitSimulationCancelledConfirmationWritesNothing(t *testing.T) {
	t.Setenv("AZD_NO_PROMPT", "false")
	prompts := &conversationPromptServer{decision: scaffoldCancel}
	h := newInitHarness(t, nil, prompts)
	text, err := executeConversationInit(t, "--name", "quality", "--conversation-mode", "simulation",
		"--dataset", "seeds", "--target", "agent", "--simulation-model", "connection/simulator", "--judge-model", "judge",
		"--num-conversations", "5", "--max-turns", "20")
	require.NoError(t, err)
	assert.Regexp(t, `Maximum turns:\s+20`, text)
	assert.Contains(t, text, "Conversations per seed: 5")
	assert.Zero(t, h.project.wiringAttempts())
	assert.False(t, scaffoldedAnything(t, filepath.Join(h.dir, "evals")))
}

func TestInitHandoffPreservesCustomConfigFile(t *testing.T) {
	path := filepath.Join("team evals", "custom.yaml")
	command := initHandoff(bothGenerated(), path)
	assert.Contains(t, command, "--path "+quoteForShell(path))
	assert.NotContains(t, command, "custom.yaml/azure.eval.yaml")
}

func TestInitSimulationRefusesKnownIncompatibleEvaluatorBeforeWriting(t *testing.T) {
	for _, level := range []string{"turn", "Turn", "TURN", "tUrN"} {
		t.Run(level, func(t *testing.T) {
			h := newInitHarness(t, nil)
			path := filepath.Join(h.dir, "evals", "azure.eval.yaml")
			require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
			body := "evaluators:\n  - name: turn-only\n    supported_evaluation_levels: [" + level + "]\n"
			require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
			before := initFileSnapshot(t, h.dir)
			text, err := executeConversationInit(t, append(simulationInitArgs("seeds"),
				"--evaluator", "turn-only", "--output", "json")...)
			require.ErrorContains(t, err, "--evaluator turn-only")
			require.ErrorContains(t, err, "--evaluation-level conversation")
			assert.Empty(t, text)
			assert.Equal(t, before, initFileSnapshot(t, h.dir))
			assert.Zero(t, h.project.wiringAttempts())
		})
	}
}

func TestInitSimulationRejectsUnqualifiedModelBeforeWrites(t *testing.T) {
	for _, model := range []string{
		"simulator", "/simulator", "connection/", "connection/model/extra", "connection/my model",
	} {
		t.Run(model, func(t *testing.T) {
			h := newInitHarness(t, nil)
			before := initFileSnapshot(t, h.dir)
			text, err := executeConversationInit(t, "--name", "quality", "--conversation-mode", "simulation",
				"--target", "agent", "--dataset", "seeds", "--simulation-model", model,
				"--judge-model", "judge", "--output", "json")
			require.ErrorContains(t, err, "--simulation-model")
			require.ErrorContains(t, err, "connection-name/model-deployment")
			assert.Empty(t, text)
			assert.Zero(t, h.project.wiringAttempts())
			assert.Equal(t, before, initFileSnapshot(t, h.dir))
		})
	}

}

func TestInitRejectsUnsupportedOutputBeforeWrites(t *testing.T) {
	for _, format := range []string{"yaml", "xml", "table", "none"} {
		for _, flag := range []string{"--output", "-o"} {
			t.Run(format+"/"+flag, func(t *testing.T) {
				h := newInitHarness(t, nil)
				before := initFileSnapshot(t, h.dir)
				text, err := executeConversationInit(t, "--name", "quality", "--conversation-mode", "static",
					"--dataset", h.seedRows, "--judge-model", "judge", "--no-prompt", flag, format)
				require.ErrorContains(t, err, "--output")
				require.ErrorContains(t, err, format)
				local, ok := errors.AsType[*azdext.LocalError](err)
				require.True(t, ok)
				assert.Equal(t, exterrors.CodeInvalidParameter, local.Code)
				assert.Contains(t, local.Suggestion, "json")
				assert.Empty(t, text)
				assert.Zero(t, h.project.wiringAttempts())
				assert.Empty(t, h.usage.reported())
				assert.Equal(t, before, initFileSnapshot(t, h.dir))
			})
		}
	}
}

func TestInitSupportedOutputFormats(t *testing.T) {
	for _, format := range []string{"", "default", "DEFAULT", "json", "JSON"} {
		t.Run(format, func(t *testing.T) {
			h := newInitHarness(t, nil)
			text, err := executeConversationInit(t, "--name", "quality", "--conversation-mode", "static",
				"--dataset", h.seedRows, "--judge-model", "judge", "--no-prompt", "--output", format)
			require.NoError(t, err)
			assert.Equal(t, 1, h.project.wiringAttempts())
			assert.NotEmpty(t, text)
			assert.Equal(t, strings.EqualFold(format, "json"), json.Valid([]byte(text)))
		})
	}
}

func TestInitRootOutputValidationAndHelp(t *testing.T) {
	h := newInitHarness(t, nil)
	before := initFileSnapshot(t, h.dir)
	root := NewRootCommand()
	root.SetContext(t.Context())
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"init", "--name", "quality", "--conversation-mode", "static",
		"--dataset", h.seedRows, "--judge-model", "judge", "--no-prompt", "-o", "yaml"})
	require.ErrorContains(t, root.Execute(), "--output")
	assert.Empty(t, out.String())
	assert.Equal(t, before, initFileSnapshot(t, h.dir))
	root.SetArgs([]string{"init", "--help"})
	require.NoError(t, root.Execute())
	assert.Contains(t, out.String(), "Output format: default (human-readable) or json")
}
