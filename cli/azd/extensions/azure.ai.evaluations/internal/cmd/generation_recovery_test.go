// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"azureaieval/internal/pkg/eval_api"
	"azureaieval/internal/project"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const recoveryEvalConfig = `evals:
  - name: existing-traces
    source:
      type: traces
      agent_name: support-agent
    evaluators:
      - evaluator: builtin.task_completion
`

func generationRecoveryFixture(
	t *testing.T, jobs ...*eval_api.GenerationJob,
) (*evalContext, []generationPlan, string, *[]string) {
	t.Helper()
	return generationRecoveryFixtureWithDatasetStatus(t, 0, jobs...)
}

func generationRecoveryFixtureWithDatasetStatus(
	t *testing.T, datasetStatus int, jobs ...*eval_api.GenerationJob,
) (*evalContext, []generationPlan, string, *[]string) {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "azure.eval.yaml"), []byte(recoveryEvalConfig), 0o600))
	priorBudget := generatePollBudget
	generatePollBudget = eval_api.PollerOptions{Interval: time.Millisecond, MaxAttempts: 2}
	t.Cleanup(func() { generatePollBudget = priorBudget })
	var mu sync.Mutex
	var requests []string
	completed := recoveryRubricJob()
	if len(jobs) > 0 {
		completed = jobs[0]
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		requests = append(requests, r.Method+" "+r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		dataset := strings.Contains(r.URL.Path, "data_generation_jobs")
		if r.Method == http.MethodPost {
			id := "evaluator-job"
			if dataset {
				id = "dataset-job"
			}
			assert.NoError(t, json.NewEncoder(w).Encode(map[string]string{"id": id, "status": "running"}))
		} else if dataset {
			if datasetStatus != 0 {
				w.WriteHeader(datasetStatus)
				_, _ = w.Write([]byte(`{"error":{"code":"GenerationReadRefused"}}`))
				return
			}
			if len(jobs) > 1 {
				assert.NoError(t, json.NewEncoder(w).Encode(jobs[1]))
				return
			}
			_, _ = w.Write([]byte(`{"id":"dataset-job","status":"failed","error":{"message":"dataset service failure"}}`))
		} else {
			assert.NoError(t, json.NewEncoder(w).Encode(completed))
		}
	}))
	t.Cleanup(server.Close)
	pipeline := runtime.NewPipeline("test", "v1", runtime.PipelineOptions{},
		&policy.ClientOptions{Retry: policy.RetryOptions{MaxRetries: -1}})
	ec := &evalContext{evalClient: eval_api.NewEvalClientFromPipeline(server.URL, pipeline)}
	plans := []generationPlan{
		{
			Kind: generateKindDataset, Name: "turn-tests", Model: "test-model",
			Instruction: "Test instruction", From: []string{"prompt"}, SampleSize: 15,
			BaseDir: dir, OutputDir: "datasets",
		},
		{
			Kind: generateKindEvaluator, Name: "quality", Model: "test-model",
			Instruction: "Test instruction", From: []string{"prompt"},
			BaseDir: dir, OutputDir: "evaluators",
		},
	}
	return ec, plans, dir, &requests
}

func recoveryRubricJob() *eval_api.GenerationJob {
	return &eval_api.GenerationJob{
		ID: "evaluator-job", Status: "succeeded",
		Result: json.RawMessage(`{"name":"quality","version":"1","display_name":"Support quality",` +
			`"categories":["quality","agents"],"supported_evaluation_levels":["turn","conversation"],"definition":` +
			`{"type":"rubric","dimensions":[{"id":"helpfulness","description":"Helpful"}]}}`),
	}
}

func TestGenerationPartialOutcomePreservesSuccessfulArtifacts(t *testing.T) {
	for _, format := range []string{"", "json"} {
		t.Run(format, func(t *testing.T) {
			ec, plans, dir, requests := generationRecoveryFixture(t)
			cmd := jsonCmd(t, format)
			cmd.SetContext(t.Context())
			var out, stderr bytes.Buffer
			cmd.SetOut(&out)
			cmd.SetErr(&stderr)
			cmd.RunE = func(*cobra.Command, []string) error {
				return ec.runGenerations(cmd, plans, generateFlags{path: dir})
			}
			priorExit := exitProcess
			exitCode := 0
			exitProcess = func(code int) { exitCode = code }
			t.Cleanup(func() { exitProcess = priorExit })
			reportFailuresAsJSON(cmd)
			err := cmd.RunE(cmd, nil)
			require.ErrorContains(t, err, "dataset service failure")
			require.FileExists(t, filepath.Join(dir, "evaluators", "quality.json"))
			cfg, err := project.OpenEvalConfig(dir)
			require.NoError(t, err)
			require.Len(t, cfg.Evaluators, 1)
			assert.Equal(t, "quality", cfg.Evaluators[0].Name)
			require.Len(t, cfg.Evals, 1)
			assert.Equal(t, "existing-traces", cfg.Evals[0].Name)
			assert.Empty(t, cfg.Evals[0].Dataset)
			assert.Equal(t, "builtin.task_completion", cfg.Evals[0].Evaluators[0].Evaluator)
			assert.Empty(t, cfg.Datasets)
			postCount := 0
			for _, request := range *requests {
				if strings.HasPrefix(request, "POST ") {
					postCount++
				}
				assert.NotContains(t, request, "DELETE")
			}
			assert.Equal(t, 2, postCount, "one job per artifact; no regeneration or rollback")
			if format == "json" {
				assert.Equal(t, 1, exitCode)
				decoder := json.NewDecoder(&out)
				var doc map[string]generationResult
				require.NoError(t, decoder.Decode(&doc))
				assert.Equal(t, io.EOF, decoder.Decode(new(any)), "stdout must contain exactly one JSON document")
				assert.Equal(t, "succeeded", doc["evaluator"].Status)
				assert.Equal(t, "1", doc["evaluator"].Version)
				assert.Equal(t, "evaluator-job", doc["evaluator"].JobID)
				assert.Equal(t, "failed", doc["dataset"].Status)
				assert.Contains(t, doc["dataset"].Error, "dataset service failure")
				assert.Contains(t, doc["dataset"].Recovery, "job show dataset-job --dataset")
				assert.Contains(t, doc["dataset"].RetryGuidance, "--dataset only")
				assert.Contains(t, doc["evaluator"].Guidance, "no existing eval references")
			} else {
				text := out.String()
				assert.Contains(t, text, "Generation did not fully complete")
				assert.Contains(t, text, "Kept evaluator")
				assert.Contains(t, text, "downloaded and declared")
				assert.Contains(t, text, "job show dataset-job --dataset")
				assert.NotContains(t, text, "Generation completed")
				assert.NotContains(t, text, "Next: azd ai eval init")
			}
		})
	}
}

func TestGenerationPartialJSONPreservesRemediation(t *testing.T) {
	for _, status := range []int{http.StatusForbidden, http.StatusServiceUnavailable} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			ec, plans, dir, requests := generationRecoveryFixtureWithDatasetStatus(t, status)
			cmd := jsonCmd(t, "json")
			cmd.SetContext(t.Context())
			var out, stderr bytes.Buffer
			cmd.SetOut(&out)
			cmd.SetErr(&stderr)
			cmd.RunE = func(*cobra.Command, []string) error {
				return ec.runGenerations(cmd, plans, generateFlags{path: dir})
			}
			priorExit := exitProcess
			exitCode := 0
			exitProcess = func(code int) { exitCode = code }
			t.Cleanup(func() { exitProcess = priorExit })
			reportFailuresAsJSON(cmd)
			err := cmd.RunE(cmd, nil)
			require.Error(t, err)
			assert.Equal(t, 1, exitCode)
			wantSuggestion := azdext.ErrorSuggestion(err)
			if status == http.StatusForbidden {
				require.NotEmpty(t, wantSuggestion)
			} else {
				require.Empty(t, wantSuggestion)
			}
			decoder := json.NewDecoder(bytes.NewReader(out.Bytes()))
			var doc map[string]map[string]any
			require.NoError(t, decoder.Decode(&doc))
			require.ErrorIs(t, decoder.Decode(new(any)), io.EOF)
			require.Contains(t, doc, "dataset")
			require.Contains(t, doc, "evaluator")
			failed, kept := doc["dataset"], doc["evaluator"]
			assert.Equal(t, "failed", failed["status"])
			assert.Equal(t, "dataset-job", failed["job_id"])
			message, ok := failed["error"].(string)
			require.True(t, ok, "the existing generation error string is a compatibility contract")
			if status == http.StatusForbidden {
				assert.Contains(t, message, "GenerationReadRefused")
			} else {
				assert.Contains(t, message, "did not complete within 2 attempts")
			}
			if wantSuggestion == "" {
				assert.NotContains(t, failed, "suggestion")
			} else {
				assert.Equal(t, wantSuggestion, failed["suggestion"])
			}
			assert.Contains(t, failed["recovery_command"], "job show dataset-job --dataset")
			assert.Contains(t, failed["retry_guidance"], "--dataset only")
			assert.Equal(t, "succeeded", kept["status"])
			assert.Equal(t, "quality", kept["name"])
			assert.Equal(t, "1", kept["version"])
			assert.Equal(t, "evaluator-job", kept["job_id"])
			assert.NotContains(t, kept, "error")
			assert.NotContains(t, kept, "suggestion")
			require.FileExists(t, filepath.Join(dir, "evaluators", "quality.json"))
			cfg, err := project.OpenEvalConfig(dir)
			require.NoError(t, err)
			require.Len(t, cfg.Evaluators, 1)
			assert.Empty(t, cfg.Datasets)
			posts := 0
			for _, request := range *requests {
				if strings.HasPrefix(request, "POST ") {
					posts++
				}
				assert.NotContains(t, request, "DELETE ")
			}
			assert.Equal(t, 2, posts, "one job per artifact, no regeneration or rollback")
			assert.NotEmpty(t, stderr.String())
		})
	}
}

func TestGenerationPartialJSONRedactsJobFailureMessage(t *testing.T) {
	for _, tc := range []struct{ name, message, safeMessage string }{
		{
			"userinfo query fragment",
			"Download https://fixture-user:fixture-password@example.test/rows.jsonl" +
				"?sig=fixture-signature#fixture-fragment failed.",
			"Download https://example.test/rows.jsonl failed.",
		},
		{
			"whitespace query",
			"Download https://example.test/rows.jsonl?sig= \tfixture-signature failed.",
			"Download <redacted-url> failed.",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			failedJob := &eval_api.GenerationJob{
				ID: "dataset-job", Status: "failed", Error: &eval_api.JobError{Message: tc.message},
			}
			ec, plans, dir, requests := generationRecoveryFixture(t, recoveryRubricJob(), failedJob)
			cmd := jsonCmd(t, "json")
			cmd.SetContext(t.Context())
			var out, stderr bytes.Buffer
			cmd.SetOut(&out)
			cmd.SetErr(&stderr)
			cmd.RunE = func(*cobra.Command, []string) error {
				return ec.runGenerations(cmd, plans, generateFlags{path: dir})
			}
			priorExit := exitProcess
			exitCode := 0
			exitProcess = func(code int) { exitCode = code }
			t.Cleanup(func() { exitProcess = priorExit })
			reportFailuresAsJSON(cmd)
			err := cmd.RunE(cmd, nil)
			require.ErrorContains(t, err, tc.message, "the returned error remains unmodified")
			assert.Equal(t, tc.message, failedJob.Error.Message)
			assert.Equal(t, 1, exitCode)

			var result map[string]generationResult
			decoder := json.NewDecoder(bytes.NewReader(out.Bytes()))
			require.NoError(t, decoder.Decode(&result))
			require.ErrorIs(t, decoder.Decode(new(any)), io.EOF)
			assert.Equal(t, "failed", result["dataset"].Status)
			assert.Equal(t, "dataset-job", result["dataset"].JobID)
			assert.Nil(t, result["dataset"].ArtifactRef)
			assert.Contains(t, result["dataset"].Error, tc.safeMessage)
			assert.Contains(t, result["dataset"].Recovery, "job show dataset-job --dataset")
			assert.Contains(t, result["dataset"].RetryGuidance, "--dataset only")
			assert.Equal(t, "succeeded", result["evaluator"].Status)
			require.NotNil(t, result["evaluator"].ArtifactRef)
			assert.Equal(t, "quality", result["evaluator"].Name)
			assert.Equal(t, "1", result["evaluator"].Version)
			require.FileExists(t, filepath.Join(dir, "evaluators", "quality.json"))
			cfg, err := project.OpenEvalConfig(dir)
			require.NoError(t, err)
			require.Len(t, cfg.Evaluators, 1)
			assert.Equal(t, "quality", cfg.Evaluators[0].Name)
			assert.Empty(t, cfg.Datasets)
			assert.Contains(t, stderr.String(), tc.safeMessage)
			for _, sensitive := range []string{
				"fixture-user", "fixture-password", "fixture-signature", "fixture-fragment", "sig=",
			} {
				assert.NotContains(t, out.String(), sensitive)
				assert.NotContains(t, stderr.String(), sensitive)
			}
			assert.Len(t, *requests, 4, "one submit and one completed-job read per artifact")
			for _, request := range *requests {
				assert.NotContains(t, request, "DELETE")
			}
		})
	}
}

func TestGenerationCatalogFailureCanRecoverWithoutRegeneration(t *testing.T) {
	ec, plans, dir, requests := generationRecoveryFixture(t)
	// This also models a concurrent config edit made while generation polls.
	path := filepath.Join(dir, "azure.eval.yaml")
	require.NoError(t, os.WriteFile(path, []byte("evals: ["), 0o600))
	cmd := jsonCmd(t, "json")
	cmd.SetContext(t.Context())
	var out bytes.Buffer
	cmd.SetOut(&out)
	err := ec.runGenerations(cmd, plans[1:], generateFlags{path: dir})
	require.Error(t, err)
	var doc map[string]generationResult
	require.NoError(t, json.Unmarshal(out.Bytes(), &doc))
	assert.Equal(t, "catalog_failed", doc["evaluator"].Status)
	assert.Equal(t, "quality", doc["evaluator"].Name)
	assert.NotEmpty(t, doc["evaluator"].Error)
	assert.Contains(t, doc["evaluator"].Recovery, "job show evaluator-job --evaluator")
	require.FileExists(t, filepath.Join(dir, "evaluators", "quality.json"))

	artifactPath := filepath.Join(dir, "evaluators", "quality.json")
	edited := []byte(`{"type":"rubric","dimensions":[{"id":"helpfulness","description":"Locally edited"}]}`)
	require.NoError(t, os.WriteFile(artifactPath, edited, 0o600))
	require.NoError(t, os.WriteFile(path, []byte(recoveryEvalConfig), 0o600))
	action := &jobShowAction{cmd: cmd, flags: &jobFlags{path: dir}}
	for range 2 {
		_, err = action.collect(t.Context(), ec, evaluatorJobs, recoveryRubricJob(), io.Discard)
		require.NoError(t, err)
		cfg, err := project.OpenEvalConfig(dir)
		require.NoError(t, err)
		require.Len(t, cfg.Evaluators, 1)
		assert.Equal(t, "Support quality", cfg.Evaluators[0].DisplayName)
		assert.Equal(t, []string{"quality", "agents"}, cfg.Evaluators[0].Categories)
		assert.Equal(t, []string{"turn", "conversation"}, cfg.Evaluators[0].SupportedEvaluationLevels)
		actual, err := os.ReadFile(artifactPath)
		require.NoError(t, err)
		assert.Equal(t, edited, actual, "recovering catalog metadata must preserve local rubric edits")
	}
	cfg, err := project.OpenEvalConfig(dir)
	require.NoError(t, err)
	require.Len(t, cfg.Evaluators, 1)
	require.Len(t, cfg.Evals, 1)
	assert.Len(t, *requests, 2, "one submission and poll; collection must not submit another job")
}

func TestGenerationRecoveryCommandPreservesScopeWithoutCredentials(t *testing.T) {
	outcome := generationOutcome{
		plan:   generationPlan{Kind: generateKindDataset, OutputDir: "custom output"},
		report: generationReport{jobID: "job-42"}, err: errors.New("interrupted"),
	}
	command, err := generationRecoveryCommand(outcome, generateFlags{path: "config with spaces"},
		"https://username:password@example.test/api/projects/project?sig=secret#fragment", "test env")
	require.NoError(t, err)
	assert.Contains(t, command, "job show job-42 --dataset")
	assert.Contains(t, command, `"config with spaces"`)
	assert.Contains(t, command, `"custom output"`)
	assert.Contains(t, command, `"test env"`)
	for _, secret := range []string{"username", "password", "sig=", "secret", "fragment", "--force"} {
		assert.NotContains(t, command, secret)
	}
	assert.NotContains(t, command, " generate ", "an interrupted job must be recovered, not resubmitted")

	outcome.report.jobID = ""
	command, err = generationRecoveryCommand(outcome, generateFlags{}, "", "")
	require.NoError(t, err)
	assert.Equal(t, "azd ai eval job list --dataset", command, "a lost submit response is not proof no job exists")
}

func TestGenerationExplainsAnUnreferencedDatasetWithoutChangingTraceEval(t *testing.T) {
	_, _, dir, _ := generationRecoveryFixture(t)
	path := filepath.Join(dir, "azure.eval.yaml")
	before, err := os.ReadFile(path)
	require.NoError(t, err)
	guidance := generationCatalogGuidance(dir, generateKindDataset, "turn-tests")
	assert.Contains(t, guidance, "no existing eval references")
	assert.Contains(t, guidance, "trace-backed eval cannot consume a dataset")
	after, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, before, after)
	assert.Empty(t, generationCatalogGuidance(dir, generateKindEvaluator, "builtin.task_completion"))
}

func TestEvaluatorRecollectionPreservesAuthoredCatalogMetadata(t *testing.T) {
	for _, existing := range []string{"authored values", "explicit empty", "missing fields"} {
		t.Run(existing, func(t *testing.T) {
			ec, _, dir, requests := generationRecoveryFixture(t)
			cmd := jsonCmd(t, "json")
			cmd.SetContext(t.Context())
			cmd.SetOut(io.Discard)
			action := &jobShowAction{cmd: cmd, flags: &jobFlags{path: dir}}
			_, err := action.collect(t.Context(), ec, evaluatorJobs, recoveryRubricJob(), io.Discard)
			require.NoError(t, err)
			path := filepath.Join(dir, "azure.eval.yaml")
			catalog := "evaluators:\n  - name: quality\n    source: ./evaluators/quality.json\n" +
				"    display_name: Authored name # keep this comment\n"
			switch existing {
			case "authored values":
				catalog += "    categories: [safety]\n    supported_evaluation_levels: [conversation]\n"
			case "explicit empty":
				catalog += "    categories: []\n    supported_evaluation_levels: []\n"
			}
			require.NoError(t, os.WriteFile(path, []byte(catalog), 0o600))
			artifact := filepath.Join(dir, "evaluators", "quality.json")
			edited := []byte(`{"type":"rubric","dimensions":[{"id":"local-edit","weight":2}]}`)
			require.NoError(t, os.WriteFile(artifact, edited, 0o600))
			for range 2 {
				_, err = action.collect(t.Context(), ec, evaluatorJobs, recoveryRubricJob(), io.Discard)
				require.NoError(t, err)
			}
			cfg, err := project.OpenEvalConfig(dir)
			require.NoError(t, err)
			require.Len(t, cfg.Evaluators, 1)
			assert.Equal(t, "Authored name", cfg.Evaluators[0].DisplayName)
			switch existing {
			case "authored values":
				assert.Equal(t, []string{"safety"}, cfg.Evaluators[0].Categories)
				assert.Equal(t, []string{"conversation"}, cfg.Evaluators[0].SupportedEvaluationLevels)
			case "explicit empty":
				assert.Empty(t, cfg.Evaluators[0].Categories)
				assert.Empty(t, cfg.Evaluators[0].SupportedEvaluationLevels)
			case "missing fields":
				assert.Equal(t, []string{"quality", "agents"}, cfg.Evaluators[0].Categories)
				assert.Equal(t, []string{"turn", "conversation"}, cfg.Evaluators[0].SupportedEvaluationLevels)
			}
			content, err := os.ReadFile(artifact)
			require.NoError(t, err)
			assert.Equal(t, edited, content)
			written, err := os.ReadFile(path)
			require.NoError(t, err)
			assert.Contains(t, string(written), "# keep this comment")
			if existing != "missing fields" {
				assert.Equal(t, catalog, string(written))
			}
			action.flags.force = true
			_, err = action.collect(t.Context(), ec, evaluatorJobs, recoveryRubricJob(), io.Discard)
			require.NoError(t, err)
			cfg, err = project.OpenEvalConfig(dir)
			require.NoError(t, err)
			assert.Equal(t, "Support quality", cfg.Evaluators[0].DisplayName)
			assert.Equal(t, []string{"quality", "agents"}, cfg.Evaluators[0].Categories)
			assert.Equal(t, []string{"turn", "conversation"}, cfg.Evaluators[0].SupportedEvaluationLevels)
			assert.Empty(t, *requests, "recollection must not submit a generation or publication request")
		})
	}
}

func TestEvaluatorRecollectionRefreshesExplicitEmptyListsOnlyWhenForced(t *testing.T) {
	for _, force := range []bool{false, true} {
		t.Run(fmt.Sprintf("force=%t", force), func(t *testing.T) {
			ec, _, dir, requests := generationRecoveryFixture(t)
			cmd := jsonCmd(t, "json")
			cmd.SetContext(t.Context())
			cmd.SetOut(io.Discard)
			action := &jobShowAction{cmd: cmd, flags: &jobFlags{path: dir}}
			_, err := action.collect(t.Context(), ec, evaluatorJobs, recoveryRubricJob(), io.Discard)
			require.NoError(t, err)
			job := recoveryRubricJob()
			job.Result = json.RawMessage(strings.ReplaceAll(strings.ReplaceAll(string(job.Result),
				`"categories":["quality","agents"]`, `"categories":[]`),
				`"supported_evaluation_levels":["turn","conversation"]`, `"supported_evaluation_levels":[]`))
			action.flags.force = force
			for range 2 {
				_, err = action.collect(t.Context(), ec, evaluatorJobs, job, io.Discard)
				require.NoError(t, err)
				cfg, err := project.OpenEvalConfig(dir)
				require.NoError(t, err)
				require.Len(t, cfg.Evaluators, 1)
				if force {
					assert.Equal(t, []string{}, cfg.Evaluators[0].Categories)
					assert.Equal(t, []string{}, cfg.Evaluators[0].SupportedEvaluationLevels)
				} else {
					assert.Equal(t, []string{"quality", "agents"}, cfg.Evaluators[0].Categories)
					assert.Equal(t, []string{"turn", "conversation"}, cfg.Evaluators[0].SupportedEvaluationLevels)
				}
			}
			assert.Empty(t, *requests, "recollection must not create a new generation or version")
		})
	}
}
