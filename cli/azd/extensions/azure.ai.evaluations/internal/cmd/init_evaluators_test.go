// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"azureaieval/internal/pkg/eval_api"
	"azureaieval/internal/pkg/evalcore"
	"azureaieval/internal/project"

	"bytes"
	"context"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"net"
	"os"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"

	// What an eval grades on is a SET, so there is no "the only one" to detect the
	// way there is for the target and the judge model. Which criteria define
	// quality is the substantive decision in the configuration, so init asks.
	//
	// Init only proposes built-ins, never a rubric that has not been generated.
	"google.golang.org/grpc"
)

func TestDefaultEvaluatorsProposeOnlyWhatAlreadyResolves(t *testing.T) {
	assert.Equal(t,
		[]string{evalcore.BuiltinPrefix + "output_quality", evalcore.BuiltinPrefix + "tool_use_quality"},
		defaultEvaluators())
}

func TestInitEvaluatorChoicesRespectKnownLocalCompatibility(t *testing.T) {
	cfg := &project.EvalConfig{Evaluators: []project.EvaluatorDecl{
		{Name: "turn-only", SupportedEvaluationLevels: []string{"turn"}},
		{Name: "conversation-only", SupportedEvaluationLevels: []string{"conversation"}},
		{Name: "turn-mixed-case", SupportedEvaluationLevels: []string{"TuRn"}},
		{Name: "conversation-mixed-case", SupportedEvaluationLevels: []string{"Conversation"}},
		{Name: "unknown"},
		{Name: "future", SupportedEvaluationLevels: []string{"future-level"}},
		{Name: "mixed-future", SupportedEvaluationLevels: []string{"TURN", "Future-Level"}},
	}}
	for _, level := range []string{"turn", "conversation", "Turn", "CONVERSATION"} {
		t.Run(level, func(t *testing.T) {
			choices := evaluatorChoices(cfg, level)
			assert.Contains(t, choices, strings.ToLower(level)+"-only")
			assert.Contains(t, choices, strings.ToLower(level)+"-mixed-case")
			assert.Contains(t, choices, "unknown")
			assert.NotContains(t, choices, "future")
			assert.Equal(t, strings.EqualFold(level, "turn"), slices.Contains(choices, "mixed-future"))
			require.ErrorContains(t, validateInitEvaluatorLevels(cfg, []string{"future"}, level),
				"--evaluation-level "+level)
			for _, other := range evaluationLevels {
				if !strings.EqualFold(other, level) {
					assert.NotContains(t, choices, other+"-only")
					assert.NotContains(t, choices, other+"-mixed-case")
					require.ErrorContains(t, validateInitEvaluatorLevels(cfg, []string{other + "-only"}, level),
						"--evaluation-level "+level)
					require.ErrorContains(t, validateInitEvaluatorLevels(cfg, []string{other + "-mixed-case"}, level),
						"--evaluation-level "+level)
				}
			}
			assert.NoError(t, validateInitEvaluatorLevels(cfg,
				[]string{"unknown", strings.ToLower(level) + "-mixed-case"}, level))
		})
	}
}

func TestInitEvaluatorLevelsMatchReconciliation(t *testing.T) {
	for _, tc := range []struct {
		name   string
		levels []string
		want   bool
	}{
		{"absent metadata", nil, true},
		{"empty metadata", []string{}, true},
		{"exact match", []string{"turn"}, true},
		{"case-insensitive match", []string{"TuRn"}, true},
		{"both known levels", []string{"conversation", "turn"}, true},
		{"unknown only", []string{"future-level"}, false},
		{"wrong known level", []string{"conversation"}, false},
		{"wrong known and unknown", []string{"conversation", "future-level"}, false},
		{"unknown and wrong known", []string{"future-level", "conversation"}, false},
		{"exact match and unknown", []string{"turn", "future-level"}, true},
		{"unknown and exact match", []string{"future-level", "TuRn"}, true},
		{"blank only", []string{""}, false},
		{"non-exact whitespace", []string{" turn "}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			decl := project.EvaluatorDecl{Name: "custom", SupportedEvaluationLevels: tc.levels}
			schema := eval_api.EvaluatorSummary{SupportedEvaluationLevels: tc.levels}
			level := project.EvaluationLevelTurn
			assert.Equal(t, tc.want, schema.SupportsLevel(level))
			assert.Equal(t, tc.want, initEvaluatorSupportsLevel(&decl, level))
			eval := &project.Eval{
				EvaluationLevel: level,
				Evaluators:      evalcore.EvaluatorList{{Evaluator: decl.Name}},
			}
			reconcileErr := checkEvaluatorRequirements(eval, map[string]*eval_api.EvaluatorSummary{decl.Name: &schema})
			cfg := &project.EvalConfig{Evaluators: []project.EvaluatorDecl{decl}}
			choices := evaluatorChoices(cfg, level)
			err := validateInitEvaluatorLevels(cfg, []string{decl.Name}, level)
			if tc.want {
				assert.Contains(t, choices, decl.Name)
				require.NoError(t, err)
				require.NoError(t, reconcileErr)
			} else {
				assert.NotContains(t, choices, decl.Name)
				require.ErrorContains(t, err, "--evaluator custom")
				assert.ErrorContains(t, err, "--evaluation-level turn")
				require.Error(t, reconcileErr)
			}
		})
	}
}

func TestEvaluatorChoicesOfferTheCompositeShortlist(t *testing.T) {
	assert.Equal(t, []string{
		evalcore.BuiltinPrefix + "output_quality",
		evalcore.BuiltinPrefix + "tool_use_quality",
	}, evaluatorChoices(nil, project.EvaluationLevelTurn))
}

// The prompt offers what is knowable without a service call -- the picker makes
// none -- which is the composite shortlist plus whatever the catalog already
// declares. A declaration is offered because its file already exists; nothing
// that would have to be generated first appears here.
func TestEvaluatorChoicesOfferTheCatalogToo(t *testing.T) {
	cfg := &project.EvalConfig{Evaluators: []project.EvaluatorDecl{
		{Name: "support-agent-quality"},
		{Name: "tone-check"},
	}}

	got := evaluatorChoices(cfg, project.EvaluationLevelTurn)

	assert.Equal(t, []string{
		evalcore.BuiltinPrefix + "output_quality",
		evalcore.BuiltinPrefix + "tool_use_quality",
		"support-agent-quality",
		"tone-check",
	}, got)
}

func TestInitDistinguishesOmittedAndEmptyEvaluators(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"omitted", nil},
		{"empty argv", []string{"--evaluator", ""}},
		{"empty assignment", []string{"--evaluator="}},
		{"empty CSV entries", []string{"--evaluator", ","}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newInitHarness(t, nil)
			before := initFileSnapshot(t, h.dir)
			args := append([]string{"--source", "traces", "--name", "quality", "--target", "agent",
				"--judge-model", "judge", "--no-prompt", "--output", "json"}, tc.args...)
			text, err := executeConversationInit(t, args...)
			if tc.args != nil {
				require.ErrorContains(t, err, "--evaluator")
				assert.Empty(t, text)
				assert.Zero(t, h.project.wiringAttempts())
				assert.Equal(t, before, initFileSnapshot(t, h.dir))
				return
			}
			require.NoError(t, err)
			cfg, err := project.OpenEvalConfig(filepath.Join(h.dir, project.DefaultEvalDir))
			require.NoError(t, err)
			require.Len(t, cfg.Evals, 1)
			require.Len(t, cfg.Evals[0].Evaluators, 2)
			assert.Equal(t, "builtin.output_quality", cfg.Evals[0].Evaluators[0].Evaluator)
			assert.Equal(t, "builtin.tool_use_quality", cfg.Evals[0].Evaluators[1].Evaluator)
		})
	}
}

// What an eval grades on is a SET, so there is no "the only one" to detect the
// way there is for the target and the judge model. Which criteria define
// quality is the substantive decision in the configuration, so init asks.
//
// The defaults are the two production composites. Their component evaluators
// must not also be selected, or the same dimension is scored twice.
func TestDefaultEvaluatorsUseProductionCompositesWithoutConstituents(t *testing.T) {
	assert.Equal(t, []string{
		evalcore.BuiltinPrefix + "output_quality",
		evalcore.BuiltinPrefix + "tool_use_quality",
	}, defaultEvaluators())
}

// The recommendations are the production composite set, not their standalone
// constituents.
func TestEvaluatorChoicesOfferProductionComposites(t *testing.T) {
	assert.Equal(t, []string{
		evalcore.BuiltinPrefix + "output_quality",
		evalcore.BuiltinPrefix + "tool_use_quality",
	}, evaluatorChoices(nil, project.EvaluationLevelTurn))
}

func TestResolveEvaluatorsNoPromptUsesAvailableCompositeDefaults(t *testing.T) {
	cmd := &cobra.Command{}
	cmd.Flags().Bool("no-prompt", true, "")

	got, chosen, err := resolveEvaluators(cmd, nil, project.EvaluationLevelTurn, defaultEvaluators())

	require.NoError(t, err)
	assert.False(t, chosen)
	assert.Equal(t, defaultEvaluators(), got)
}

func TestResolveEvaluatorsRefusesUnavailableCompositeDefaults(t *testing.T) {
	cmd := &cobra.Command{}
	cmd.Flags().Bool("no-prompt", true, "")

	_, _, err := resolveEvaluators(cmd, nil, project.EvaluationLevelTurn,
		[]string{evalcore.BuiltinPrefix + "output_quality"})

	require.Error(t, err)
	assert.Contains(t, err.Error(), evalcore.BuiltinPrefix+"tool_use_quality")
}

type evaluatorPickerServer struct {
	azdext.UnimplementedPromptServiceServer
	response *azdext.MultiSelectResponse
	requests chan *azdext.MultiSelectRequest
}

func (s *evaluatorPickerServer) MultiSelect(
	_ context.Context, req *azdext.MultiSelectRequest,
) (*azdext.MultiSelectResponse, error) {
	s.requests <- req
	return s.response, nil
}

func serveEvaluatorPicker(t *testing.T, selected ...string) *evaluatorPickerServer {
	t.Helper()
	values := make([]*azdext.MultiSelectChoice, 0, len(selected))
	for _, value := range selected {
		values = append(values, &azdext.MultiSelectChoice{Value: value})
	}
	picker := &evaluatorPickerServer{
		response: &azdext.MultiSelectResponse{Values: values},
		requests: make(chan *azdext.MultiSelectRequest, 1),
	}
	server := grpc.NewServer()
	azdext.RegisterPromptServiceServer(server, picker)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)
	t.Setenv("AZD_SERVER", listener.Addr().String())
	t.Setenv("AZD_NO_PROMPT", "false")
	return picker
}

func TestResolveEvaluatorsInteractivePreselectsCompositesAndPreservesSelection(t *testing.T) {
	selected := []string{evalcore.BuiltinPrefix + "tool_use_quality"}
	picker := serveEvaluatorPicker(t, selected...)
	cmd := &cobra.Command{}
	cmd.Flags().Bool("no-prompt", false, "")
	cmd.Flags().String("output", "", "")
	cmd.SetContext(t.Context())

	got, chosen, err := resolveEvaluators(cmd, nil, project.EvaluationLevelTurn, defaultEvaluators())

	require.NoError(t, err)
	assert.True(t, chosen)
	assert.Equal(t, selected, got, "the response replaces rather than merges with the preselection")
	require.Len(t, picker.requests, 1)
	req := <-picker.requests
	require.Len(t, req.Options.Choices, 2)
	for _, choice := range req.Options.Choices {
		assert.True(t, choice.Selected, "%s should be recommended", choice.Value)
	}
}

type finalEvaluatorSelectionServer struct {
	conversationPromptServer
	selected []string
	requests int
	choices  []string
}

func (s *finalEvaluatorSelectionServer) MultiSelect(
	_ context.Context, req *azdext.MultiSelectRequest,
) (*azdext.MultiSelectResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.requests++
	for _, choice := range req.Options.Choices {
		s.choices = append(s.choices, choice.Value)
	}
	var selected []*azdext.MultiSelectChoice
	for _, value := range s.selected {
		selected = append(selected, &azdext.MultiSelectChoice{Value: value})
	}
	return &azdext.MultiSelectResponse{Values: selected}, nil
}

func TestInitValidatesFinalSelectionNotProvisionalDefaults(t *testing.T) {
	for _, tc := range []struct {
		name       string
		selected   string
		known      []string
		unattended bool
		wantError  bool
	}{
		{"available builtin alternative", "builtin.output_quality", []string{"output_quality"}, false, false},
		{"declared custom alternative", "custom", []string{"output_quality"}, false, false},
		{"custom without available builtins", "custom", []string{}, false, false},
		{"unavailable final selection", "builtin.tool_use_quality", []string{"output_quality"}, false, true},
		{"unattended defaults stay strict", "", []string{"output_quality"}, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("AZD_NO_PROMPT", "false")
			prompts := &finalEvaluatorSelectionServer{selected: []string{tc.selected}}
			h := newInitHarness(t, nil, prompts)
			dir := filepath.Join(h.dir, project.DefaultEvalDir)
			require.NoError(t, os.MkdirAll(dir, 0o700))
			require.NoError(t, os.WriteFile(filepath.Join(dir, "azure.eval.yaml"),
				[]byte("evaluators:\n  - name: custom\n    supported_evaluation_levels: [turn]\n"), 0o600))
			before := initFileSnapshot(t, h.dir)
			cmd := &cobra.Command{}
			cmd.Flags().Bool("no-prompt", tc.unattended, "")
			cmd.Flags().String("output", "", "")
			cmd.SetContext(t.Context())
			var output bytes.Buffer
			cmd.SetOut(&output)
			cmd.SetErr(&output)
			action := &initAction{
				cmd: cmd,
				flags: &initFlags{evalName: "quality", target: "agent", source: initSourceTraces,
					evaluationLevel: project.EvaluationLevelTurn, judgeModel: "judge",
					maxTraces: project.DefaultScaffoldMaxTraces},
				knownBuiltins: func(context.Context) []string { return tc.known },
			}
			err := action.Run()
			prompts.mu.Lock()
			defer prompts.mu.Unlock()
			if tc.unattended {
				assert.Zero(t, prompts.requests)
			} else {
				assert.Equal(t, 1, prompts.requests, "a missing default must not prevent the picker")
				assert.NotContains(t, prompts.choices, "builtin.tool_use_quality")
				assert.Contains(t, prompts.choices, "custom")
			}
			if tc.wantError {
				require.ErrorContains(t, err, "builtin.tool_use_quality")
				assert.Zero(t, h.project.wiringAttempts())
				assert.Equal(t, before, initFileSnapshot(t, h.dir))
				return
			}
			require.NoError(t, err)
			cfg, err := project.OpenEvalConfig(dir)
			require.NoError(t, err)
			require.Len(t, cfg.Evals, 1)
			require.Len(t, cfg.Evals[0].Evaluators, 1)
			assert.Equal(t, tc.selected, cfg.Evals[0].Evaluators[0].Evaluator)
		})
	}
}
