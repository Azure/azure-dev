// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
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

func generationRecoveryFixture(t *testing.T) (*evalContext, []generationPlan, string, *[]string) {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "azure.eval.yaml"), []byte(recoveryEvalConfig), 0o600))
	priorBudget := generatePollBudget
	generatePollBudget = eval_api.PollerOptions{Interval: time.Millisecond, MaxAttempts: 2}
	t.Cleanup(func() { generatePollBudget = priorBudget })
	var mu sync.Mutex
	var requests []string
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
			_, _ = w.Write([]byte(`{"id":"dataset-job","status":"failed","error":{"message":"dataset service failure"}}`))
		} else {
			assert.NoError(t, json.NewEncoder(w).Encode(recoveryRubricJob()))
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
		Result: json.RawMessage(`{"name":"quality","version":"1","definition":` +
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

	require.NoError(t, os.WriteFile(path, []byte(recoveryEvalConfig), 0o600))
	action := &jobShowAction{cmd: cmd, flags: &jobFlags{path: dir}}
	for range 2 {
		_, err = action.collect(t.Context(), ec, evaluatorJobs, recoveryRubricJob(), io.Discard)
		require.NoError(t, err)
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
