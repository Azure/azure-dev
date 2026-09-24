// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"azureaieval/internal/pkg/eval_api"
	"azureaieval/internal/project"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// bothGenerated is a finished run that produced a dataset and an evaluator.
func bothGenerated() []generationOutcome {
	return []generationOutcome{
		{
			plan: generationPlan{
				Kind: generateKindDataset, Agent: "hero-agent", EvaluationLevel: "turn",
			},
			ref:    &project.ArtifactRef{Name: "hero-agent-turn-tests"},
			report: generationReport{jobID: "datagen-1"},
		},
		{
			plan:   generationPlan{Kind: generateKindEvaluator, Agent: "hero-agent"},
			ref:    &project.ArtifactRef{Name: "hero-agent-evaluator"},
			report: generationReport{jobID: "evaluatorgen-1"},
		},
	}
}

// Generation used to end on its last download line. The job ids -- the only
// handle on a billed job once the command exits -- scrolled past unlabelled,
// and the caller was left to work out that `init` came next and to retype every
// name the command had just chosen for them.
func TestGenerationClosesWithItsJobsAndTheNextCommand(t *testing.T) {
	var out bytes.Buffer

	writeGenerationCompleted(&out, bothGenerated(), "")

	text := out.String()
	assert.Contains(t, text, "Generation completed")
	assert.Contains(t, text, "dataset job: datagen-1")
	assert.Contains(t, text, "evaluator job: evaluatorgen-1")
	assert.Contains(t, text, "Next: azd ai eval init")
}

// Known resource choices are retained; model deployments remain independent inputs.
func TestTheInitHandoffCarriesKnownArtifactAndTargetInputs(t *testing.T) {
	got := initHandoff(bothGenerated(), "")

	assert.Contains(t, got, "--target hero-agent",
		"init can detect this, but the printed line has to run as printed")
	assert.Contains(t, got, "--source dataset")
	assert.Contains(t, got, "--dataset hero-agent-turn-tests")
	assert.Contains(t, got, "--evaluation-level turn")
	assert.Contains(t, got, "--evaluator builtin.task_completion")
	assert.Contains(t, got, "--evaluator hero-agent-evaluator")
	assert.NotContains(t, got, "<", "a line with a placeholder in it is not a command")
}

// Only what was generated is named. Pointing at a dataset that was never
// produced prints a command that fails on its first flag.
func TestTheHandoffNamesOnlyWhatWasGenerated(t *testing.T) {
	dataset := bothGenerated()[:1]
	evaluator := bothGenerated()[1:]

	datasetOnly := initHandoff(dataset, "")
	assert.Contains(t, datasetOnly, "--dataset hero-agent-turn-tests")
	assert.NotContains(t, datasetOnly, "--evaluator")

	evaluatorOnly := initHandoff(evaluator, "")
	assert.Contains(t, evaluatorOnly, "--evaluator hero-agent-evaluator")
	assert.NotContains(t, evaluatorOnly, "--dataset")
	assert.NotContains(t, evaluatorOnly, "--source")
}

// Nothing produced is nothing to hand off. A --no-wait run has job ids and no
// artifacts, and `init` cannot be pointed at either of them yet.
func TestNothingProducedPrintsNoHandoff(t *testing.T) {
	outcomes := []generationOutcome{{
		plan:   generationPlan{Kind: generateKindDataset},
		report: generationReport{jobID: "datagen-1"},
	}}

	require.Empty(t, initHandoff(outcomes, ""))

	var out bytes.Buffer
	writeGenerationCompleted(&out, outcomes, "")
	assert.Contains(t, out.String(), "datagen-1", "the job id is still worth having")
	assert.NotContains(t, out.String(), "Next:")
	assert.NotContains(t, out.String(), "Run this init command")
}

func TestGenerationHandoffExplainsInteractiveAndUnattendedModels(t *testing.T) {
	for _, tc := range []struct {
		name       string
		dataset    bool
		evaluator  bool
		level      string
		simulation bool
	}{
		{"turn dataset and rubric", true, true, project.EvaluationLevelTurn, false},
		{"turn dataset only", true, false, project.EvaluationLevelTurn, false},
		{"rubric only", false, true, "", false},
		{"conversation dataset and rubric", true, true, project.EvaluationLevelConversation, true},
		{"conversation dataset only", true, false, project.EvaluationLevelConversation, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			outcomes := bothGenerated()
			outcomes[0].plan.EvaluationLevel = tc.level
			if !tc.dataset {
				outcomes = outcomes[1:]
			} else if !tc.evaluator {
				outcomes = outcomes[:1]
			}
			for i := range outcomes {
				outcomes[i].plan.Model = "generation-only"
			}
			command := initHandoff(outcomes, "team evals")
			var out bytes.Buffer
			writeGenerationCompleted(&out, outcomes, "team evals")
			text := out.String()
			assert.Contains(t, text, "Next: "+command+"\n", "guidance must not alter the actual command")
			assert.Contains(t, text, "Run this init command interactively to resolve missing inputs.")
			assert.Contains(t, text, "For unattended use, add --no-prompt --judge-model <judge-deployment>")
			assert.Contains(t, text, "independently of --generation-model")
			assert.NotContains(t, text, "generation-only", "never infer a judge or simulator from the generation model")
			assert.NotContains(t, command, "<", "placeholders belong in guidance, not the copyable command")
			if tc.simulation {
				assert.Contains(t, text, "--simulation-model <connection-name/model-deployment>")
			} else {
				assert.NotContains(t, text, "--simulation-model")
			}
		})
	}
}

func TestGenerationHandoffGuidanceOnlyReachesCompletedHumanOutput(t *testing.T) {
	for _, tc := range []struct {
		name   string
		format string
		noWait bool
	}{
		{"human completed", "", false},
		{"JSON completed", "json", false},
		{"human submitted", "", true},
		{"JSON submitted", "json", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				assert.NoError(t, json.NewEncoder(w).Encode(map[string]any{
					"id": "job-rubric", "status": "completed",
					"result": map[string]any{
						"name": "quality", "version": "1", "definition": map[string]any{"dimensions": []any{}},
					},
				}))
			}))
			t.Cleanup(server.Close)
			pipeline := runtime.NewPipeline("test", "v1", runtime.PipelineOptions{},
				&policy.ClientOptions{Retry: policy.RetryOptions{MaxRetries: -1}})
			ec := &evalContext{evalClient: eval_api.NewEvalClientFromPipeline(server.URL, pipeline)}
			dir := t.TempDir()
			cmd := &cobra.Command{}
			cmd.SetContext(t.Context())
			cmd.Flags().String("output", tc.format, "")
			var out bytes.Buffer
			cmd.SetOut(&out)
			require.NoError(t, ec.runGenerations(cmd, []generationPlan{{
				Name: "quality", Kind: generateKindEvaluator, Model: "generation-only",
				From: []string{project.GenerateFromPrompt}, Instruction: "Generate a rubric.",
				BaseDir: dir, OutputDir: "evaluators",
			}}, generateFlags{path: dir, noWait: tc.noWait}))
			if tc.format == "json" {
				var document map[string]any
				require.NoError(t, json.Unmarshal(out.Bytes(), &document), "stdout must be one JSON document")
				assert.Contains(t, document, "evaluator")
			}
			if tc.format == "json" || tc.noWait {
				assert.NotContains(t, out.String(), "Run this init command")
				assert.NotContains(t, out.String(), "--judge-model")
			} else {
				assert.Contains(t, out.String(), "Run this init command interactively")
				assert.Contains(t, out.String(), "--judge-model <judge-deployment>")
			}
		})
	}
}

func TestUnattendedHandoffGuidanceCompletesMissingResources(t *testing.T) {
	for _, kind := range []string{"rubric only", "instruction-only conversation"} {
		t.Run(kind, func(t *testing.T) {
			h := newInitHarness(t, nil)
			outcomes := bothGenerated()[1:]
			if kind == "instruction-only conversation" {
				outcomes = bothGenerated()[:1]
				outcomes[0].plan.Agent = ""
				outcomes[0].plan.EvaluationLevel = project.EvaluationLevelConversation
			}
			var out bytes.Buffer
			if kind == "rubric only" {
				catalogCommand := &cobra.Command{}
				catalogCommand.SetContext(t.Context())
				catalogCommand.SetOut(&out)
				require.NoError(t, addEvaluatorToCatalog(
					catalogCommand, filepath.Join(h.dir, project.DefaultEvalDir), outcomes[0].ref))
				out.Reset()
			}
			writeGenerationCompleted(&out, outcomes, "")
			_, guidance, found := strings.Cut(out.String(), "For unattended use, add ")
			require.True(t, found)
			flags, _, found := strings.Cut(guidance, ". Choose these deployments")
			require.True(t, found)
			flags = strings.NewReplacer(
				"<judge-deployment>", "judge",
				"<connection-name/model-deployment>", "connection/simulator",
				"<agent-name>", "existing-agent",
				"<dataset-name-or-jsonl-path>", "registered-data",
			).Replace(flags)
			command := strings.TrimPrefix(initHandoff(outcomes, ""), "azd ai eval init ")
			_, err := executeConversationInit(t, strings.Fields(command+" "+flags)...)
			require.NoError(t, err, "the completed guidance must work without configured local agents or datasets")
			cfg, err := project.OpenEvalConfig(filepath.Join(h.dir, "evals"))
			require.NoError(t, err)
			require.Len(t, cfg.Evals, 1)
			require.NotNil(t, cfg.Evals[0].Target)
			if kind == "instruction-only conversation" {
				assert.Equal(t, "existing-agent", cfg.Evals[0].Target.Name)
				require.NotNil(t, cfg.Evals[0].Simulation)
				assert.Equal(t, "connection/simulator", cfg.Evals[0].Simulation.Model)
				assert.Contains(t, out.String(), "Before running init, add --target")
			} else {
				assert.Equal(t, "registered-data", cfg.Evals[0].Dataset)
				assert.Contains(t, out.String(), "No dataset was generated")
			}
		})
	}
}

func TestConversationHandoffOnlyIncludesCompatibleGeneratedEvaluators(t *testing.T) {
	for _, tc := range []struct {
		name   string
		levels []string
		keep   bool
	}{
		{"conversation", []string{"conversation"}, true},
		{"both", []string{"turn", "conversation"}, true},
		{"unknown", nil, true},
		{"future metadata", []string{"future"}, true},
		{"turn only", []string{"turn"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			outcomes := bothGenerated()
			outcomes[0].plan.EvaluationLevel = project.EvaluationLevelConversation
			outcomes[1].ref.SupportedEvaluationLevels = tc.levels
			command := initHandoff(outcomes, "")
			assert.Contains(t, command, "--conversation-mode simulation")
			var out bytes.Buffer
			writeGenerationCompleted(&out, outcomes, "")
			if tc.keep {
				assert.Contains(t, command, "--evaluator hero-agent-evaluator")
				assert.NotContains(t, out.String(), "does not support")
			} else {
				assert.NotContains(t, command, "--evaluator hero-agent-evaluator")
				assert.Contains(t, out.String(), "does not support")
				assert.Contains(t, out.String(), "remains in the catalogue")
			}
		})
	}
}
