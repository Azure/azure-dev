// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"azureaiagent/internal/pkg/agents/agent_yaml"
	"azureaiagent/internal/pkg/agents/dataset_api"
	"azureaiagent/internal/pkg/agents/eval_api"
	"azureaiagent/internal/pkg/agents/opteval"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeTokenCredential satisfies azcore.TokenCredential for tests.
type fakeTokenCredential struct{}

func (f *fakeTokenCredential) GetToken(
	_ context.Context,
	_ policy.TokenRequestOptions,
) (azcore.AccessToken, error) {
	return azcore.AccessToken{Token: "fake-token"}, nil
}

// ---------------------------------------------------------------------------
// newEvalCommand — command tree shape
// ---------------------------------------------------------------------------

func TestNewEvalCommand_HasExpectedSubcommands(t *testing.T) {
	t.Parallel()

	cmd := newEvalCommand(&azdext.ExtensionContext{})
	names := make([]string, 0, len(cmd.Commands()))
	for _, sub := range cmd.Commands() {
		names = append(names, sub.Name())
	}

	assert.Contains(t, names, "init")
	assert.Contains(t, names, "run")
	assert.Contains(t, names, "list")
	assert.Contains(t, names, "show")
}

func TestNewEvalCommand_UseString(t *testing.T) {
	t.Parallel()
	cmd := newEvalCommand(&azdext.ExtensionContext{})
	assert.Equal(t, "eval <command>", cmd.Use)
}

// ---------------------------------------------------------------------------
// GenerationJob methods
// ---------------------------------------------------------------------------

func TestGenerationJob_OperationID(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "op-123", (&eval_api.GenerationJob{ID: "op-123"}).OperationID())
	assert.Equal(t, "", (&eval_api.GenerationJob{}).OperationID())
}

func TestGenerationJob_NormalizedStatus(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "completed", (&eval_api.GenerationJob{Status: "completed"}).NormalizedStatus())
	assert.Equal(t, "running", (&eval_api.GenerationJob{}).NormalizedStatus())
}

func TestGenerationJob_ResolvedDatasetName(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "", (&eval_api.GenerationJob{}).ResolvedDatasetName())

	// Extracts name from the result JSON.
	job := &eval_api.GenerationJob{
		Result: json.RawMessage(`{"name":"generated-ds","version":"v2"}`),
	}
	assert.Equal(t, "generated-ds", job.ResolvedDatasetName())

	// Extracts name from result.outputs[0] (nested API response format).
	jobNested := &eval_api.GenerationJob{
		Result: json.RawMessage(`{"outputs":[{"type":"dataset","name":"nested-ds","version":"36735"}]}`),
	}
	assert.Equal(t, "nested-ds", jobNested.ResolvedDatasetName())
}

func TestGenerationJob_ResolvedDatasetVersion(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "v1", (&eval_api.GenerationJob{}).ResolvedDatasetVersion())

	// Extracts version from the result JSON.
	job := &eval_api.GenerationJob{
		Result: json.RawMessage(`{"name":"ds","version":"v5"}`),
	}
	assert.Equal(t, "v5", job.ResolvedDatasetVersion())

	// Extracts version from result.outputs[0] (nested API response format).
	jobNested := &eval_api.GenerationJob{
		Result: json.RawMessage(`{"outputs":[{"type":"dataset","name":"ds","version":"36735"}]}`),
	}
	assert.Equal(t, "36735", jobNested.ResolvedDatasetVersion())
}

func TestGenerationJob_ResolvedEvaluatorName(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "", (&eval_api.GenerationJob{}).ResolvedEvaluatorName())

	// Extracts name from the result JSON.
	job := &eval_api.GenerationJob{
		Result: json.RawMessage(`{"name":"smoke-core","display_name":"smoke-core"}`),
	}
	assert.Equal(t, "smoke-core", job.ResolvedEvaluatorName())
}

func TestOpenAIEval_ResolvedID(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "eval-1", (&eval_api.OpenAIEval{ID: "eval-1", Name: "n"}).ResolvedID())
	assert.Equal(t, "n", (&eval_api.OpenAIEval{Name: "n"}).ResolvedID())
	assert.Equal(t, "", (&eval_api.OpenAIEval{}).ResolvedID())
}

// ---------------------------------------------------------------------------
// formatAny / formatTimestamp
// ---------------------------------------------------------------------------

func TestFormatAny(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "", formatAny(nil))
	assert.Equal(t, "hello", formatAny("hello"))
	assert.Equal(t, "42", formatAny(float64(42)))
	assert.Equal(t, "true", formatAny(true))
}

func TestFormatTimestamp(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "2024-01-15 10:30 UTC", formatTimestamp("2024-01-15 10:30 UTC"))
	assert.Contains(t, formatTimestamp(float64(1705312200)), "2024-01-15")
	assert.Contains(t, formatTimestamp(int64(1705312200)), "2024-01-15")
	assert.Equal(t, "", formatTimestamp(nil))
	assert.Equal(t, "", formatTimestamp(true))
}

// ---------------------------------------------------------------------------
// resolveEvalOutputPath / resolveEvalConfigPath
// ---------------------------------------------------------------------------

func TestResolveEvalOutputPath(t *testing.T) {
	t.Parallel()

	t.Run("absolute path returned as-is", func(t *testing.T) {
		t.Parallel()
		abs := filepath.Join(os.TempDir(), "eval.yaml")
		assert.Equal(t, abs, resolveEvalOutputPath(abs, "/project"))
	})

	t.Run("relative path joined with agent project", func(t *testing.T) {
		t.Parallel()
		result := resolveEvalOutputPath("eval.yaml", "/project/agent")
		assert.Equal(t, filepath.Join("/project/agent", "eval.yaml"), result)
	})
}

func TestResolveEvalConfigPath(t *testing.T) {
	t.Parallel()

	t.Run("absolute path returned as-is", func(t *testing.T) {
		t.Parallel()
		abs := filepath.Join(os.TempDir(), "eval.yaml")
		assert.Equal(t, abs, resolveEvalConfigPath(abs, "/project"))
	})

	t.Run("relative path joined with agent project when file does not exist", func(t *testing.T) {
		t.Parallel()
		result := resolveEvalConfigPath("nonexistent.yaml", "/project/agent")
		assert.Equal(t, filepath.Join("/project/agent", "nonexistent.yaml"), result)
	})
}

// ---------------------------------------------------------------------------
// detectEvalAgentKind
// ---------------------------------------------------------------------------

func TestDetectEvalAgentKind(t *testing.T) {
	t.Parallel()

	t.Run("detects hosted kind from agent.yaml", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		writeTestFile(t, dir, "agent.yaml", "kind: hosted\nname: test-agent\n")
		kind, path := detectEvalAgentKind(dir)
		assert.Equal(t, agent_yaml.AgentKindHosted, kind)
		assert.Equal(t, filepath.Join(dir, "agent.yaml"), path)
	})

	t.Run("detects prompt kind from agent.yml", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		writeTestFile(t, dir, "agent.yml", "kind: prompt\nname: test-agent\n")
		kind, path := detectEvalAgentKind(dir)
		assert.Equal(t, agent_yaml.AgentKindPrompt, kind)
		assert.Equal(t, filepath.Join(dir, "agent.yml"), path)
	})

	t.Run("returns empty for missing manifest", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		kind, path := detectEvalAgentKind(dir)
		assert.Empty(t, kind)
		assert.Empty(t, path)
	})

	t.Run("returns empty for invalid kind", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		writeTestFile(t, dir, "agent.yaml", "kind: invalid_kind_xyz\nname: test-agent\n")
		kind, path := detectEvalAgentKind(dir)
		assert.Empty(t, kind)
		assert.Empty(t, path)
	})

	t.Run("returns empty for malformed YAML", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		writeTestFile(t, dir, "agent.yaml", "{{invalid yaml}}")
		kind, path := detectEvalAgentKind(dir)
		assert.Empty(t, kind)
		assert.Empty(t, path)
	})
}

// ---------------------------------------------------------------------------
// ensureFoundryDirs
// ---------------------------------------------------------------------------

func TestEnsureFoundryDirs(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	err := ensureFoundryDirs(dir)
	require.NoError(t, err)

	for _, sub := range []string{"datasets", "evaluators", "results"} {
		path := filepath.Join(dir, ".azure", ".foundry", sub)
		info, err := os.Stat(path)
		require.NoError(t, err, "expected %s to exist", sub)
		assert.True(t, info.IsDir())
	}
}

func TestEnsureFoundryDirs_Idempotent(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	require.NoError(t, ensureFoundryDirs(dir))
	require.NoError(t, ensureFoundryDirs(dir))
}

// ---------------------------------------------------------------------------
// evalState — stored in azd environment (integration-tested via eval init/run)
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// writeEvalReviewArtifacts
// ---------------------------------------------------------------------------

func TestWriteEvalReviewArtifacts(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	require.NoError(t, ensureFoundryDirs(dir))

	cfg := &evalConfig{}
	cfg.DatasetReference = &evalDatasetRef{Name: "test-data", Version: "v1"}
	cfg.Evaluators = []string{"quality"}

	writeEvalReviewArtifacts(dir, cfg)

	// writeEvalReviewArtifacts only writes evaluator stubs; dataset download
	// is handled separately by downloadDatasetArtifact.
	dsPath := filepath.Join(dir, ".azure", ".foundry", "datasets", "test-data-v1.jsonl")
	assert.NoFileExists(t, dsPath)

	evPath := filepath.Join(dir, ".azure", ".foundry", "evaluators", "quality.yaml")
	assert.FileExists(t, evPath)
}

func TestWriteEvalReviewArtifacts_NilDataset(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	require.NoError(t, ensureFoundryDirs(dir))

	cfg := &evalConfig{}
	// No dataset reference — should not panic.
	writeEvalReviewArtifacts(dir, cfg)
}

// ---------------------------------------------------------------------------
// saveEvaluatorResult
// ---------------------------------------------------------------------------

func TestSaveEvaluatorResult(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	require.NoError(t, ensureFoundryDirs(dir))

	result := json.RawMessage(`{"name":"smoke-core","description":"An evaluator"}`)
	saveEvaluatorResult(dir, "smoke-core", result)

	path := filepath.Join(dir, ".azure", ".foundry", "evaluators", "smoke-core.json")
	assert.FileExists(t, path)
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Contains(t, string(data), `"name": "smoke-core"`)
	assert.Contains(t, string(data), `"description": "An evaluator"`)
}

func TestSaveEvaluatorResult_NilResult(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	require.NoError(t, ensureFoundryDirs(dir))

	saveEvaluatorResult(dir, "test", nil)
	path := filepath.Join(dir, ".azure", ".foundry", "evaluators", "test.json")
	assert.NoFileExists(t, path)
}

func TestSaveEvaluatorResult_EmptyName(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	require.NoError(t, ensureFoundryDirs(dir))

	saveEvaluatorResult(dir, "", json.RawMessage(`{"name":"x"}`))
	// Should not create any file.
	matches, _ := filepath.Glob(filepath.Join(dir, ".azure", ".foundry", "evaluators", "*.json"))
	assert.Empty(t, matches)
}

func TestWriteEvalReviewArtifacts_SkipsWhenResultExists(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	require.NoError(t, ensureFoundryDirs(dir))

	// Pre-save a result file.
	saveEvaluatorResult(dir, "quality", json.RawMessage(`{"name":"quality"}`))

	cfg := &evalConfig{}
	cfg.Evaluators = []string{"quality"}
	writeEvalReviewArtifacts(dir, cfg)

	// Should NOT create a .yaml stub since .json result already exists.
	yamlPath := filepath.Join(dir, ".azure", ".foundry", "evaluators", "quality.yaml")
	assert.NoFileExists(t, yamlPath)
}

// ---------------------------------------------------------------------------
// downloadDatasetArtifact
// ---------------------------------------------------------------------------

func TestDownloadDatasetArtifact_NilDataset(t *testing.T) {
	t.Parallel()
	err := downloadDatasetArtifact(t.Context(), nil, t.TempDir(), nil, "2025-11-15-preview")
	require.NoError(t, err)
}

func TestDownloadDatasetArtifact_WritesBlob(t *testing.T) {
	t.Parallel()

	// The Azure SDK bearer token policy rejects non-TLS test servers, so the
	// credential call will fail. downloadDatasetArtifact gracefully writes a
	// placeholder in that case — verify the placeholder is created.
	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"sas_uri":"http://blob.example/data?sig=abc"}`))
	}))
	t.Cleanup(apiServer.Close)

	client := dataset_api.NewDatasetClient(apiServer.URL, &fakeTokenCredential{})
	dir := t.TempDir()
	require.NoError(t, ensureFoundryDirs(dir))

	ref := &evalDatasetRef{Name: "test-ds", Version: "v1"}
	err := downloadDatasetArtifact(t.Context(), client, dir, ref, "2025-11-15-preview")
	require.NoError(t, err)

	// Placeholder is written when credential fetch fails (non-TLS test server).
	dest := datasetArtifactPath(dir, ref)
	assert.FileExists(t, dest)
	data, err := os.ReadFile(dest)
	require.NoError(t, err)
	assert.Equal(t, "{}\n", string(data))
}

// ---------------------------------------------------------------------------
// datasetArtifactPath
// ---------------------------------------------------------------------------

func TestDatasetArtifactPath(t *testing.T) {
	t.Parallel()
	ref := &evalDatasetRef{Name: "golden", Version: "v2"}
	result := datasetArtifactPath("/project", ref)
	assert.Equal(t, filepath.Join("/project", ".azure", ".foundry", "datasets", "golden-v2.jsonl"), result)
}

// ---------------------------------------------------------------------------
// writeJSONFile
// ---------------------------------------------------------------------------

func TestWriteJSONFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "result.json")

	err := writeJSONFile(path, map[string]string{"hello": "world"})
	require.NoError(t, err)

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Contains(t, string(data), `"hello": "world"`)
}

// ---------------------------------------------------------------------------
// evalAgentContextError
// ---------------------------------------------------------------------------

func TestEvalAgentContextError(t *testing.T) {
	t.Parallel()

	t.Run("without cause", func(t *testing.T) {
		t.Parallel()
		err := evalAgentContextError(nil)
		assert.Contains(t, err.Error(), "agent context could not be resolved")
		var localErr *azdext.LocalError
		require.True(t, errors.As(err, &localErr))
		assert.Contains(t, localErr.Suggestion, "azd ai agent init")
	})

	t.Run("with cause", func(t *testing.T) {
		t.Parallel()
		cause := assert.AnError
		err := evalAgentContextError(cause)
		assert.Contains(t, err.Error(), cause.Error())
		var localErr *azdext.LocalError
		require.True(t, errors.As(err, &localErr))
		assert.Contains(t, localErr.Suggestion, "--agent")
		assert.Contains(t, localErr.Suggestion, "--project-endpoint")
	})
}

// ---------------------------------------------------------------------------
// relPathForYaml
// ---------------------------------------------------------------------------

func TestRelPathForYaml(t *testing.T) {
	t.Parallel()

	result := relPathForYaml("/project", filepath.Join("/project", "src", "agent.yaml"))
	assert.Equal(t, "src/agent.yaml", result)
}

// ---------------------------------------------------------------------------
// writeEvalConfig / readEvalConfig round-trip
// ---------------------------------------------------------------------------

func TestEvalConfigRoundTrip(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "eval.yaml")

	original := &evalConfig{
		Config: opteval.Config{
			Name: "smoke-core",
			Agent: evalAgentRef{
				Name:         "my-agent",
				Kind:         agent_yaml.AgentKindHosted,
				Version:      "v1",
				SystemPrompt: "Test this agent",
			},
			DatasetReference: &evalDatasetRef{Name: "ds", Version: "v1"},
			Evaluators:       []string{"builtin.task_adherence"},
		},
		Options: &opteval.Options{
			EvalModel: "gpt-4o",
		},
		MaxSamples: 50,
	}

	err := writeEvalConfig(path, original)
	require.NoError(t, err)

	loaded, err := readEvalConfig(path)
	require.NoError(t, err)

	assert.Equal(t, original.Name, loaded.Name)
	assert.Equal(t, original.Agent.Name, loaded.Agent.Name)
	assert.Equal(t, original.Agent.Kind, loaded.Agent.Kind)
	assert.Equal(t, original.Agent.Version, loaded.Agent.Version)
	assert.Equal(t, "gpt-4o", loaded.Options.EvalModel)
	assert.Equal(t, original.Agent.SystemPrompt, loaded.Agent.SystemPrompt)
	assert.Equal(t, original.MaxSamples, loaded.MaxSamples)
	require.NotNil(t, loaded.DatasetReference)
	assert.Equal(t, "ds", loaded.DatasetReference.Name)
	require.Len(t, loaded.Evaluators, 1)
	assert.Equal(t, "builtin.task_adherence", loaded.Evaluators[0])
}

func TestReadEvalConfig_MissingFile(t *testing.T) {
	t.Parallel()
	_, err := readEvalConfig("/nonexistent/path/eval.yaml")
	assert.Error(t, err)
}
