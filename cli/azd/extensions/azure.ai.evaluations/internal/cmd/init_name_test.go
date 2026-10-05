// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"azureaieval/internal/messages"
	"azureaieval/internal/project"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func configWithEvals(names ...string) *project.EvalConfig {
	cfg := &project.EvalConfig{}
	for _, n := range names {
		cfg.Evals = append(cfg.Evals, project.Eval{Name: n})
	}
	return cfg
}

// The spec's default: init proposes a name, and proposes one the file can
// still accept rather than failing on its own suggestion.
func TestEvalName_TakesTheSuggestionWhenNoneWasGiven(t *testing.T) {
	cfg := configWithEvals("support-agent-trace-turn-eval")
	suggested := uniqueEvalName(cfg, defaultEvalName("support-agent", initSourceTraces, "turn", ""))
	require.Equal(t, "support-agent-trace-turn-eval-2", suggested)

	got, err := resolveEvalName(
		noPromptCmd(t, true), cfg, "evals/azure.eval.yaml", "", suggested)

	require.NoError(t, err)
	assert.Equal(t, "support-agent-trace-turn-eval-2", got)
}

// A name the caller gave and the file has room for is the answer, and asking
// about it would read their own flag back to them.
func TestEvalName_ExplicitAndFreeIsTakenAsGiven(t *testing.T) {
	got, err := resolveEvalName(
		noPromptCmd(t, true), configWithEvals("other"), "evals/azure.eval.yaml",
		"nightly", "support-agent-trace-eval")

	require.NoError(t, err)
	assert.Equal(t, "nightly", got)
}

// Nobody is there to be asked again, so a taken name ends the command -- which
// is the spec's `--no-prompt` rule, and now the only path that hard-fails.
func TestEvalName_DuplicateUnderNoPromptIsRejected(t *testing.T) {
	_, err := resolveEvalName(
		noPromptCmd(t, true), configWithEvals("nightly"), "evals/azure.eval.yaml",
		"nightly", "support-agent-trace-eval")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "nightly")
	assert.Contains(t, err.Error(), "already exists")
	assert.NotContains(t, err.Error(), "--force",
		"init no longer has a flag that replaces an eval, so it must not suggest one")
}

// The service's character set is applied locally, so a name it would answer
// with a wrapped 400 is refused before anything is written.
func TestEvalName_RefusesWhatTheServiceWouldRefuse(t *testing.T) {
	for _, name := range []string{"my eval", "a/b", "caf\u00e9-eval", "../escape"} {
		t.Run(name, func(t *testing.T) {
			_, err := resolveEvalName(
				noPromptCmd(t, true), configWithEvals(), "evals/azure.eval.yaml",
				name, "support-agent-trace-eval")

			require.Errorf(t, err, "%q must not reach the file", name)
		})
	}
}

// An empty --name is not a name, so the suggestion stands.
func TestEvalName_EmptyExplicitFallsBackToTheSuggestion(t *testing.T) {
	got, err := resolveEvalName(
		noPromptCmd(t, true), configWithEvals(), "evals/azure.eval.yaml",
		"", "support-agent-dataset-eval")

	require.NoError(t, err)
	assert.Equal(t, "support-agent-dataset-eval", got)
}

// What the re-ask offers has to be usable, or the reader is handed back the
// name they were just refused.
func TestEvalName_TheRetrySuggestionIsFree(t *testing.T) {
	cfg := configWithEvals("nightly", "nightly-2")

	next := uniqueEvalName(cfg, trimEvalName("nightly", 0))

	assert.Equal(t, "nightly-3", next)
	assert.NoError(t, evalNameUsable(cfg, "evals/azure.eval.yaml", next))
}

// evalNameUsable is what both the prompt and the no-prompt path decide on, so
// the two cannot drift into disagreeing about the same name.
func TestEvalNameUsable_SeparatesTakenFromMalformed(t *testing.T) {
	cfg := configWithEvals("nightly")

	require.NoError(t, evalNameUsable(cfg, "c.yaml", "fresh"))

	taken := evalNameUsable(cfg, "c.yaml", "nightly")
	require.Error(t, taken)
	assert.Contains(t, taken.Error(), "already exists")

	malformed := evalNameUsable(cfg, "c.yaml", "not a name")
	require.Error(t, malformed)
	assert.Contains(t, malformed.Error(), "not a usable eval name")
}

type defaultNamePromptServer struct {
	conversationPromptServer
}

func (s *defaultNamePromptServer) Prompt(
	ctx context.Context, req *azdext.PromptRequest,
) (*azdext.PromptResponse, error) {
	if req.GetOptions().GetMessage() == messages.EvalNamePrompt() {
		return &azdext.PromptResponse{Value: req.GetOptions().GetDefaultValue()}, nil
	}
	return s.conversationPromptServer.Prompt(ctx, req)
}

func TestInitDefaultNamesIdentifySourceModeAndLevel(t *testing.T) {
	for _, format := range []string{"interactive", "no-prompt", "json"} {
		t.Run(format, func(t *testing.T) {
			t.Setenv("AZD_NO_PROMPT", "false")
			h := newInitHarness(t, nil, &defaultNamePromptServer{})
			path := filepath.Join("team evals", "custom quality.yml")
			require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
			original := "# Preserve older names\nx-owner: team\nevals:\n  - name: agent-dataset-eval\n"
			require.NoError(t, os.WriteFile(path, []byte(original), 0o600))
			cases := []struct {
				name string
				args []string
			}{
				{"agent-dataset-turn-eval", []string{"--source", "dataset", "--evaluation-level", "turn",
					"--target", "agent", "--dataset", "turn-data"}},
				{"agent-trace-turn-eval", []string{"--source", "traces", "--evaluation-level", "turn", "--target", "agent"}},
				{"agent-trace-conversation-eval", []string{"--source", "traces",
					"--evaluation-level", "conversation", "--target", "agent"}},
				{"static-conversation-eval", []string{"--conversation-mode", "static", "--dataset", "completed"}},
				{"agent-simulation-conversation-eval", []string{"--conversation-mode", "simulation",
					"--target", "agent", "--dataset", "seeds", "--simulation-model", "connection/simulator"}},
				{"agent-dataset-turn-eval-2", []string{"--source", "dataset", "--evaluation-level", "turn",
					"--target", "agent", "--dataset", "different-turn-data"}},
			}
			for _, tc := range cases {
				args := append([]string{"--path", path, "--judge-model", "judge"}, tc.args...)
				switch format {
				case "no-prompt":
					args = append(args, "--no-prompt")
				case "json":
					args = append(args, "--output", "json")
				}
				text, err := executeConversationInit(t, args...)
				require.NoError(t, err)
				if format == "json" {
					var doc map[string]any
					require.NoError(t, json.Unmarshal([]byte(text), &doc))
					assert.Equal(t, tc.name, doc["eval"])
				} else {
					assert.Contains(t, text, "Next: azd ai eval create "+tc.name)
					assert.Contains(t, text, `--path "team evals/custom quality.yml"`)
				}
				authored, err := project.ReadAuthoredConfig(path)
				require.NoError(t, err)
				assert.Contains(t, authored.Names(project.SectionEvals), tc.name)
			}
			body, err := os.ReadFile(path)
			require.NoError(t, err)
			assert.Contains(t, string(body), "# Preserve older names")
			assert.Contains(t, string(body), "x-owner: team")
			assert.Contains(t, string(body), "name: agent-dataset-eval\n", "existing names must not be migrated")
			assert.NoFileExists(t, filepath.Join(h.dir, "evals", project.EvalConfigBase))
		})
	}
}

func TestInitStaticNamingUsesOnlyUnambiguousLocalAgentHint(t *testing.T) {
	for _, tc := range []struct {
		name   string
		agents []string
		want   string
	}{
		{"known local agent", []string{"travel-planner"}, "travel-planner-static-conversation-eval"},
		{"no agent", nil, "static-conversation-eval"},
		{"ambiguous agents", []string{"one", "two"}, "static-conversation-eval"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			command := newInitCommand()
			command.SetContext(t.Context())
			command.Flags().Bool("no-prompt", true, "")
			action := &initAction{cmd: command, flags: &initFlags{
				conversationMode: conversationModeStatic, dataset: "completed", judgeModel: "judge",
			}}
			proj := projectWith(tc.agents...)
			for _, service := range proj.Services {
				service.Host = project.AgentHost
			}
			answers, err := action.ask(initContext{
				cfg: &project.EvalConfig{}, azdProject: proj,
				configPath: filepath.Join(t.TempDir(), "quality.yml"),
			})
			require.NoError(t, err)
			assert.Equal(t, tc.want, answers.evalName)
			assert.Empty(t, answers.target, "the naming hint must not turn static scoring into target invocation")
		})
	}
}
