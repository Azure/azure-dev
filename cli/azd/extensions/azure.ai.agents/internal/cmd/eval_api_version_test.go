// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"azureaiagent/internal/pkg/agents/agent_api"
	"azureaiagent/internal/pkg/agents/agent_yaml"
	"azureaiagent/internal/pkg/agents/dataset_api"
	"azureaiagent/internal/pkg/agents/eval_api"
	"azureaiagent/internal/pkg/agents/opt_eval"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// API version constant values
// ---------------------------------------------------------------------------

func TestProjectEndpointAPIVersion_Value(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "2025-11-15-preview", ProjectEndpointAPIVersion)
}

func TestDataGenerationAPIVersion_Value(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "v1", DataGenerationAPIVersion)
}

func TestDefaultAgentAPIVersion_Value(t *testing.T) {
	t.Parallel()
	assert.Equal(t, agent_api.AgentEndpointAPIVersion, DefaultAgentAPIVersion)
}

// ---------------------------------------------------------------------------
// helpers — test pipeline with no auth
// ---------------------------------------------------------------------------

func newTestPipeline() runtime.Pipeline {
	return runtime.NewPipeline("test", "v0.0.0", runtime.PipelineOptions{}, &policy.ClientOptions{})
}

// ---------------------------------------------------------------------------
// submitDatasetGeneration — passes DataGenerationAPIVersion
// ---------------------------------------------------------------------------

func TestSubmitDatasetGeneration_APIVersion(t *testing.T) {
	t.Parallel()

	var capturedAPIVersion string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedAPIVersion = r.URL.Query().Get("api-version")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"op-1","status":"running"}`))
	}))
	t.Cleanup(server.Close)

	client := eval_api.NewEvalClientFromPipeline(server.URL, newTestPipeline())
	resolved := &evalResolvedContext{
		evalClient: client,
		agentName:  "test-agent",
		agentKind:  agent_yaml.AgentKindHosted,
		version:    "v1",
	}
	flags := &evalGenerateFlags{
		evalModel:  "gpt-4o",
		maxSamples: 10,
	}

	job, err := submitDatasetGeneration(t.Context(), resolved, flags)
	require.NoError(t, err)
	assert.Equal(t, "op-1", job.ID)
	assert.Equal(t, DataGenerationAPIVersion, capturedAPIVersion,
		"submitDatasetGeneration should pass DataGenerationAPIVersion to the API")
}

// ---------------------------------------------------------------------------
// submitEvaluatorGeneration — passes ProjectEndpointAPIVersion
// ---------------------------------------------------------------------------

func TestSubmitEvaluatorGeneration_APIVersion(t *testing.T) {
	t.Parallel()

	var capturedAPIVersion string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedAPIVersion = r.URL.Query().Get("api-version")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"eval-op-1","status":"running"}`))
	}))
	t.Cleanup(server.Close)

	client := eval_api.NewEvalClientFromPipeline(server.URL, newTestPipeline())
	resolved := &evalResolvedContext{
		evalClient: client,
		agentName:  "test-agent",
		agentKind:  agent_yaml.AgentKindHosted,
		version:    "v1",
	}
	flags := &evalGenerateFlags{
		evalModel: "gpt-4o",
	}

	job, err := submitEvaluatorGeneration(t.Context(), resolved, flags)
	require.NoError(t, err)
	assert.Equal(t, "eval-op-1", job.ID)
	assert.Equal(t, ProjectEndpointAPIVersion, capturedAPIVersion,
		"submitEvaluatorGeneration should pass ProjectEndpointAPIVersion to the API")
}

// ---------------------------------------------------------------------------
// updateDataset — passes ProjectEndpointAPIVersion to UploadNewVersion
// ---------------------------------------------------------------------------

func TestUpdateDataset_APIVersion(t *testing.T) {
	t.Parallel()

	// Track api-version from every request the dataset client makes.
	var capturedVersions []string
	requestCount := 0
	var serverURL string
	mux := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedVersions = append(capturedVersions, r.URL.Query().Get("api-version"))
		requestCount++
		w.Header().Set("Content-Type", "application/json")

		switch {
		case requestCount == 1:
			// Step 1: startPendingUpload — return SAS URI for blob upload.
			w.WriteHeader(http.StatusOK)
			resp := `{"blobReference":{"blobUri":"` + serverURL +
				`/container","credential":{"sasUri":"` + serverURL + `/container?sig=fake"}}}`
			_, _ = w.Write([]byte(resp))
		case requestCount == 2:
			// Step 2: blob upload (PUT to SAS URI) — accept it.
			w.WriteHeader(http.StatusCreated)
		case requestCount == 3:
			// Step 3: finalize dataset version.
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"name":"test-ds","version":"v2"}`))
		}
	})
	server := httptest.NewServer(mux)
	serverURL = server.URL
	t.Cleanup(server.Close)

	client := dataset_api.NewDatasetClientFromPipeline(server.URL, newTestPipeline())

	// Create a temp config dir with a JSONL file for the dataset.
	dir := t.TempDir()
	dataDir := filepath.Join(dir, "data")
	require.NoError(t, os.MkdirAll(dataDir, 0750))
	require.NoError(t, os.WriteFile(
		filepath.Join(dataDir, "golden.jsonl"),
		[]byte(`{"query":"hello"}`+"\n"),
		0600,
	))

	configPath := filepath.Join(dir, "eval.yaml")
	cfg := &evalConfig{
		Config: opt_eval.Config{
			DatasetReference: &evalDatasetRef{
				Name:     "test-ds",
				Version:  "v1",
				LocalURI: dataDir,
			},
		},
	}

	updated, err := updateDataset(t.Context(), client, cfg, configPath)
	require.NoError(t, err)
	assert.Equal(t, 1, updated)

	// startPendingUpload and finalize both carry the API version parameter.
	require.GreaterOrEqual(t, len(capturedVersions), 2)
	assert.Equal(t, ProjectEndpointAPIVersion, capturedVersions[0],
		"startPendingUpload should use ProjectEndpointAPIVersion")
}

// ---------------------------------------------------------------------------
// updateEvaluators — passes ProjectEndpointAPIVersion to GetEvaluatorRaw
// and CreateEvaluatorVersion
// ---------------------------------------------------------------------------

func TestUpdateEvaluators_APIVersion(t *testing.T) {
	t.Parallel()

	var capturedVersions []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedVersions = append(capturedVersions, r.URL.Query().Get("api-version"))
		w.Header().Set("Content-Type", "application/json")

		switch r.Method {
		case http.MethodGet:
			// GetEvaluatorRaw response
			resp := map[string]any{
				"name": "quality-eval",
				"definition": map[string]any{
					"type": "rubric",
				},
			}
			data, _ := json.Marshal(resp)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(data)
		case http.MethodPost:
			// CreateEvaluatorVersion response
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"name":"quality-eval","version":"v2"}`))
		}
	}))
	t.Cleanup(server.Close)

	client := eval_api.NewEvalClientFromPipeline(server.URL, newTestPipeline())

	// Create a temp dir with a valid dimensions JSON file.
	dir := t.TempDir()
	dimFile := filepath.Join(dir, "dimensions.json")
	require.NoError(t, os.WriteFile(dimFile, []byte(`[{"name":"helpfulness","weight":1}]`), 0600))

	configPath := filepath.Join(dir, "eval.yaml")
	cfg := &evalConfig{
		Config: opt_eval.Config{
			Evaluators: opt_eval.EvaluatorList{
				{Name: "quality-eval", Version: "v1", LocalURI: "dimensions.json"},
			},
		},
	}

	updated, err := updateEvaluators(t.Context(), client, cfg, configPath)
	require.NoError(t, err)
	assert.Equal(t, 1, updated)

	// Both GetEvaluatorRaw and CreateEvaluatorVersion should use ProjectEndpointAPIVersion.
	require.Len(t, capturedVersions, 2)
	assert.Equal(t, ProjectEndpointAPIVersion, capturedVersions[0],
		"GetEvaluatorRaw should use ProjectEndpointAPIVersion")
	assert.Equal(t, ProjectEndpointAPIVersion, capturedVersions[1],
		"CreateEvaluatorVersion should use ProjectEndpointAPIVersion")
}
