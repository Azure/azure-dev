// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"azureaieval/internal/pkg/eval_api"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Agent-seeded generation fails server-side for every agent, and the waiting
// path answers that with a prompt-only retry. Under --no-wait the client is
// gone before the failure arrives, so submitting the agent-seeded shape billed
// a job that could not succeed and left `job show` reporting it.
func TestNoWaitSubmitsTheShapeThatCanSucceed(t *testing.T) {
	var submitted []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			body := new(bytes.Buffer)
			_, _ = body.ReadFrom(r.Body)
			submitted = body.Bytes()
		}
		w.Header().Set("Content-Type", "application/json")
		assert.NoError(t, json.NewEncoder(w).Encode(map[string]any{
			"id": "job_1", "status": "running",
		}))
	}))
	t.Cleanup(srv.Close)

	pipeline := runtime.NewPipeline("test", "v1", runtime.PipelineOptions{},
		&policy.ClientOptions{Retry: policy.RetryOptions{MaxRetries: -1}})
	ec := &evalContext{evalClient: eval_api.NewEvalClientFromPipeline(srv.URL, pipeline)}

	var out bytes.Buffer
	var report generationReport
	_, err := ec.generateDataset(t.Context(), generationPlan{
		Name:        "golden",
		Model:       "gpt-4.1-nano",
		Agent:       "support-agent",
		Instruction: "score the answers",
		From:        []string{"agent", "prompt"},
		SampleSize:  10,
	}, &out, true, &report, refuseRetry)

	require.NoError(t, err)
	require.NotEmpty(t, submitted, "the job has to reach the service")

	assert.NotContains(t, string(submitted), "support-agent",
		"the agent source is what fails, and nothing is left to retry it here")
	assert.Contains(t, out.String(), "--no-wait",
		"and the caller is told why the agent was dropped")
	assert.Equal(t, "job_1", report.jobID, "the submitted job is still what they reattach to")
}

// The waiting path keeps its retry: it can see the failure, so it submits the
// agent-seeded shape first and only drops the agent when the service rejects it.
func TestWaitingStillSubmitsTheAgentSourceFirst(t *testing.T) {
	var first []byte
	var posts int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			posts++
			if posts == 1 {
				body := new(bytes.Buffer)
				_, _ = body.ReadFrom(r.Body)
				first = body.Bytes()
			}
		}
		w.Header().Set("Content-Type", "application/json")
		assert.NoError(t, json.NewEncoder(w).Encode(map[string]any{
			"id": "job_1", "status": "failed",
			"error": map[string]any{"message": "boom"},
		}))
	}))
	t.Cleanup(srv.Close)

	pipeline := runtime.NewPipeline("test", "v1", runtime.PipelineOptions{},
		&policy.ClientOptions{Retry: policy.RetryOptions{MaxRetries: -1}})
	ec := &evalContext{evalClient: eval_api.NewEvalClientFromPipeline(srv.URL, pipeline)}

	var out bytes.Buffer
	var report generationReport
	_, _ = ec.generateDataset(t.Context(), generationPlan{
		Name:        "golden",
		Model:       "gpt-4.1-nano",
		Agent:       "support-agent",
		Instruction: "score the answers",
		From:        []string{"agent", "prompt"},
		SampleSize:  10,
	}, &out, false, &report, allowRetry)

	require.NotEmpty(t, first)
	assert.True(t, strings.Contains(string(first), "support-agent"),
		"the waiting path can see the failure, so it still tries the agent first")
}

// GENERATE spec 10: "Retrying changes the requested sources and submits another
// billable job, so it requires consent."
//
// It used to be automatic. The command had been confirmed once, for one job,
// against the sources the caller asked for; the fallback silently bought a
// second one against different sources.
func TestTheFallbackJobIsNotSubmittedWithoutConsent(t *testing.T) {
	posts := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			posts++
		}
		w.Header().Set("Content-Type", "application/json")
		assert.NoError(t, json.NewEncoder(w).Encode(map[string]any{
			"id": "job_1", "status": "failed",
			"error": map[string]any{"message": "DataGenerationJobSystemError"},
		}))
	}))
	t.Cleanup(srv.Close)

	pipeline := runtime.NewPipeline("test", "v1", runtime.PipelineOptions{},
		&policy.ClientOptions{Retry: policy.RetryOptions{MaxRetries: -1}})
	ec := &evalContext{evalClient: eval_api.NewEvalClientFromPipeline(srv.URL, pipeline)}

	var out bytes.Buffer
	var report generationReport
	_, err := ec.generateDataset(t.Context(), generationPlan{
		Name:        "golden",
		Model:       "gpt-4.1-nano",
		Agent:       "support-agent",
		Instruction: "score the answers",
		From:        []string{"agent", "prompt"},
		SampleSize:  10,
	}, &out, false, &report, refuseRetry)

	require.Error(t, err)
	assert.Equal(t, 1, posts, "declining must not buy a second job")
	assert.Contains(t, err.Error(), "--from prompt",
		"and the refusal names the flag that asks for the surviving source")
}

func refuseRetry(string, string, error) (bool, error) { return false, nil }
func allowRetry(string, string, error) (bool, error)  { return true, nil }
