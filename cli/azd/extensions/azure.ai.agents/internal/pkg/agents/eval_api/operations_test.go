// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package eval_api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// test helpers
// ---------------------------------------------------------------------------

// fakeCredential satisfies azcore.TokenCredential for tests without real auth.
type fakeCredential struct{}

func (f *fakeCredential) GetToken(
	_ context.Context,
	_ policy.TokenRequestOptions,
) (azcore.AccessToken, error) {
	return azcore.AccessToken{Token: "fake-token"}, nil
}

// newTestClient creates an EvalClient pointed at a test HTTP server.
func newTestClient(t *testing.T, handler http.Handler) (*EvalClient, *httptest.Server) {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	pipeline := runtime.NewPipeline(
		"test",
		"v0.0.0",
		runtime.PipelineOptions{},
		&policy.ClientOptions{},
	)
	client := NewEvalClientFromPipeline(server.URL, pipeline)
	return client, server
}

// ---------------------------------------------------------------------------
// NewEvalClient
// ---------------------------------------------------------------------------

func TestNewEvalClient(t *testing.T) {
	t.Parallel()

	client := NewEvalClient("https://example.ai.azure.com", &fakeCredential{})
	require.NotNil(t, client)
	assert.Equal(t, "https://example.ai.azure.com", client.endpoint)
}

// ---------------------------------------------------------------------------
// CreateDataGenerationJob
// ---------------------------------------------------------------------------

func TestCreateDataGenerationJob_Success(t *testing.T) {
	t.Parallel()

	var capturedPath, capturedAPIVersion string
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedPath = r.URL.Path
		capturedAPIVersion = r.URL.Query().Get("api-version")

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		resp := map[string]any{"id": "op-123", "status": "running"}
		data, _ := json.Marshal(resp)
		_, _ = w.Write(data)
	})

	client, _ := newTestClient(t, handler)
	result, err := client.CreateDataGenerationJob(t.Context(), &DataGenerationJobRequest{
		Inputs: DataGenerationInputs{
			Name:     "test",
			Scenario: "evaluation",
		},
	}, "v1")

	require.NoError(t, err)
	assert.Equal(t, "/data_generation_jobs", capturedPath)
	assert.Equal(t, "v1", capturedAPIVersion)
	assert.Equal(t, "op-123", result.ID)
	assert.Equal(t, "running", result.Status)
}

// ---------------------------------------------------------------------------
// GetDataGenerationJob
// ---------------------------------------------------------------------------

func TestGetDataGenerationJob_Success(t *testing.T) {
	t.Parallel()

	var capturedPath string
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedPath = r.URL.Path

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		resp := map[string]any{
			"id":     "op-123",
			"status": "completed",
			"result": map[string]any{"name": "test-ds", "version": "v1"},
		}
		data, _ := json.Marshal(resp)
		_, _ = w.Write(data)
	})

	client, _ := newTestClient(t, handler)
	result, err := client.GetDataGenerationJob(t.Context(), "op-123", "v1")

	require.NoError(t, err)
	assert.Equal(t, "/data_generation_jobs/op-123", capturedPath)
	assert.Equal(t, "completed", result.Status)
	name, _ := result.ResolvedNameVersion()
	assert.Equal(t, "test-ds", name)
}

// ---------------------------------------------------------------------------
// CreateEvaluatorGenerationJob
// ---------------------------------------------------------------------------

func TestCreateEvaluatorGenerationJob_Success(t *testing.T) {
	t.Parallel()

	var capturedPath string
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedPath = r.URL.Path

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		resp := map[string]any{"id": "eval-op-456", "status": "running"}
		data, _ := json.Marshal(resp)
		_, _ = w.Write(data)
	})

	client, _ := newTestClient(t, handler)
	result, err := client.CreateEvaluatorGenerationJob(
		t.Context(), &EvaluatorGenerationJobRequest{Inputs: EvaluatorGenerationInputs{Name: "my-eval"}}, "v1",
	)

	require.NoError(t, err)
	assert.Equal(t, "/evaluator_generation_jobs", capturedPath)
	assert.Equal(t, "eval-op-456", result.ID)
}

// ---------------------------------------------------------------------------
// GetEvaluatorGenerationJob
// ---------------------------------------------------------------------------

func TestGetEvaluatorGenerationJob_Success(t *testing.T) {
	t.Parallel()

	var capturedPath string
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedPath = r.URL.Path

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		resp := map[string]any{
			"id":     "eval-op-456",
			"status": "completed",
			"result": map[string]any{"name": "quality"},
		}
		data, _ := json.Marshal(resp)
		_, _ = w.Write(data)
	})

	client, _ := newTestClient(t, handler)
	result, err := client.GetEvaluatorGenerationJob(t.Context(), "eval-op-456", "v1")

	require.NoError(t, err)
	assert.Equal(t, "/evaluator_generation_jobs/eval-op-456", capturedPath)
	assert.Equal(t, "completed", result.Status)
	name, _ := result.ResolvedNameVersion()
	assert.Equal(t, "quality", name)
}

// ---------------------------------------------------------------------------
// CreateOpenAIEval
// ---------------------------------------------------------------------------

func TestCreateOpenAIEval_Success(t *testing.T) {
	t.Parallel()

	var capturedPath string
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedPath = r.URL.Path

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		resp := map[string]any{"id": "eval-001", "name": "smoke-core"}
		data, _ := json.Marshal(resp)
		_, _ = w.Write(data)
	})

	client, _ := newTestClient(t, handler)
	result, err := client.CreateOpenAIEval(
		t.Context(), &CreateOpenAIEvalRequest{Name: "smoke-core"},
	)

	require.NoError(t, err)
	assert.Equal(t, "/openai/v1/evals", capturedPath)
	assert.Equal(t, "eval-001", result.ID)
}

// ---------------------------------------------------------------------------
// ListOpenAIEvals
// ---------------------------------------------------------------------------

func TestListOpenAIEvals_Success(t *testing.T) {
	t.Parallel()

	var capturedLimit string
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedLimit = r.URL.Query().Get("limit")

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		resp := map[string]any{
			"data": []any{
				map[string]any{"id": "eval-1"},
				map[string]any{"id": "eval-2"},
			},
		}
		data, _ := json.Marshal(resp)
		_, _ = w.Write(data)
	})

	client, _ := newTestClient(t, handler)
	result, err := client.ListOpenAIEvals(t.Context(), 10)

	require.NoError(t, err)
	assert.Equal(t, "10", capturedLimit)
	assert.Len(t, result.Data, 2)
}

func TestListOpenAIEvals_ZeroLimit(t *testing.T) {
	t.Parallel()

	var hasLimitParam bool
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hasLimitParam = r.URL.Query().Has("limit")

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"data":[]}`))
	})

	client, _ := newTestClient(t, handler)
	_, err := client.ListOpenAIEvals(t.Context(), 0)

	require.NoError(t, err)
	assert.False(t, hasLimitParam, "limit should not be set when 0")
}

// ---------------------------------------------------------------------------
// GetOpenAIEval
// ---------------------------------------------------------------------------

func TestGetOpenAIEval_Success(t *testing.T) {
	t.Parallel()

	var capturedPath string
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedPath = r.URL.Path

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		resp := map[string]any{"id": "eval-001", "name": "smoke-core", "metadata": map[string]string{"azd_agent": "agent-1"}}
		data, _ := json.Marshal(resp)
		_, _ = w.Write(data)
	})

	client, _ := newTestClient(t, handler)
	result, err := client.GetOpenAIEval(t.Context(), "eval-001")

	require.NoError(t, err)
	assert.Equal(t, "/openai/v1/evals/eval-001", capturedPath)
	assert.Equal(t, "smoke-core", result.Name)
}

// ---------------------------------------------------------------------------
// CreateOpenAIEvalRun
// ---------------------------------------------------------------------------

func TestCreateOpenAIEvalRun_Success(t *testing.T) {
	t.Parallel()

	var capturedPath string
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedPath = r.URL.Path

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		resp := map[string]any{"id": "run-001", "status": "running"}
		data, _ := json.Marshal(resp)
		_, _ = w.Write(data)
	})

	client, _ := newTestClient(t, handler)
	result, err := client.CreateOpenAIEvalRun(
		t.Context(), "eval-001", &CreateOpenAIEvalRunRequest{
			Metadata: map[string]string{"agent": "a"},
		},
	)

	require.NoError(t, err)
	assert.Equal(t, "/openai/v1/evals/eval-001/runs", capturedPath)
	assert.Equal(t, "run-001", result.ID)
}

// ---------------------------------------------------------------------------
// ListOpenAIEvalRuns
// ---------------------------------------------------------------------------

func TestListOpenAIEvalRuns_Success(t *testing.T) {
	t.Parallel()

	var capturedPath, capturedLimit string
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedPath = r.URL.Path
		capturedLimit = r.URL.Query().Get("limit")

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		resp := map[string]any{"data": []any{map[string]any{"id": "run-1"}}}
		data, _ := json.Marshal(resp)
		_, _ = w.Write(data)
	})

	client, _ := newTestClient(t, handler)
	result, err := client.ListOpenAIEvalRuns(t.Context(), "eval-001", 5)

	require.NoError(t, err)
	assert.Equal(t, "/openai/v1/evals/eval-001/runs", capturedPath)
	assert.Equal(t, "5", capturedLimit)
	assert.Len(t, result.Data, 1)
}

// ---------------------------------------------------------------------------
// GetOpenAIEvalRun
// ---------------------------------------------------------------------------

func TestGetOpenAIEvalRun_Success(t *testing.T) {
	t.Parallel()

	var capturedPath string
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedPath = r.URL.Path

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		resp := map[string]any{"id": "run-001", "status": "completed", "score": 0.92}
		data, _ := json.Marshal(resp)
		_, _ = w.Write(data)
	})

	client, _ := newTestClient(t, handler)
	result, err := client.GetOpenAIEvalRun(t.Context(), "eval-001", "run-001")

	require.NoError(t, err)
	assert.Equal(t, "/openai/v1/evals/eval-001/runs/run-001", capturedPath)
	assert.Equal(t, "completed", result.Status)
}

// ---------------------------------------------------------------------------
// Error handling
// ---------------------------------------------------------------------------

func TestDoRequest_ServerError(t *testing.T) {
	t.Parallel()

	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})

	client, _ := newTestClient(t, handler)
	_, err := client.CreateOpenAIEval(t.Context(), &CreateOpenAIEvalRequest{})
	assert.Error(t, err)
}

func TestDoRequest_EmptyBody(t *testing.T) {
	t.Parallel()

	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	client, _ := newTestClient(t, handler)
	result, err := client.ListOpenAIEvals(t.Context(), 0)
	require.NoError(t, err)
	assert.Empty(t, result.Data)
}

func TestDoRequest_NoAPIVersionInOpenAIQuery(t *testing.T) {
	t.Parallel()

	var capturedAPIVersion string
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedAPIVersion = r.URL.Query().Get("api-version")

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	})

	client, _ := newTestClient(t, handler)
	_, err := client.GetOpenAIEval(t.Context(), "eval-1")
	require.NoError(t, err)
	assert.Equal(t, "", capturedAPIVersion)
}

func TestDoRequest_RequestBodySent(t *testing.T) {
	t.Parallel()

	var capturedBody map[string]any
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		_ = json.NewDecoder(r.Body).Decode(&capturedBody)

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"ok"}`))
	})

	client, _ := newTestClient(t, handler)
	req := &DataGenerationJobRequest{
		Inputs: DataGenerationInputs{
			Name:     "test-eval",
			Scenario: "evaluation",
		},
	}
	_, err := client.CreateDataGenerationJob(t.Context(), req, "v1")

	require.NoError(t, err)
	require.NotNil(t, capturedBody)
	inputs, ok := capturedBody["inputs"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "test-eval", inputs["name"])
	assert.Equal(t, "evaluation", inputs["scenario"])
}
