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
	"azureaiagent/internal/pkg/agents/opt_eval"

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

	assert.Contains(t, names, "generate")
	assert.Contains(t, names, "run")
	assert.Contains(t, names, "list")
	assert.Contains(t, names, "show")
	// "init" remains registered as a hidden deprecated alias for "generate".
	assert.Contains(t, names, "init")
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

func TestGenerationJob_ResolvedNameVersion(t *testing.T) {
	t.Parallel()

	// Empty job returns empty name and empty version.
	name, version := (&eval_api.GenerationJob{}).ResolvedNameVersion()
	assert.Equal(t, "", name)
	assert.Equal(t, "", version)

	// Extracts name and version from the result JSON.
	job := &eval_api.GenerationJob{
		Result: json.RawMessage(`{"name":"generated-ds","version":"v2"}`),
	}
	name, version = job.ResolvedNameVersion()
	assert.Equal(t, "generated-ds", name)
	assert.Equal(t, "v2", version)

	// Extracts from result.outputs[0] (nested API response format).
	jobNested := &eval_api.GenerationJob{
		Result: json.RawMessage(`{"outputs":[{"type":"dataset","name":"nested-ds","version":"36735"}]}`),
	}
	name, version = jobNested.ResolvedNameVersion()
	assert.Equal(t, "nested-ds", name)
	assert.Equal(t, "36735", version)

	// Defaults version to "latest" when missing.
	jobNoVer := &eval_api.GenerationJob{
		Result: json.RawMessage(`{"name":"smoke-core"}`),
	}
	name, version = jobNoVer.ResolvedNameVersion()
	assert.Equal(t, "smoke-core", name)
	assert.Equal(t, "latest", version)
}

func TestOpenAIEval_ResolvedID(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "eval-1", (&eval_api.OpenAIEval{ID: "eval-1", Name: "n"}).ResolvedID())
	assert.Equal(t, "n", (&eval_api.OpenAIEval{Name: "n"}).ResolvedID())
	assert.Equal(t, "", (&eval_api.OpenAIEval{}).ResolvedID())
}

// ---------------------------------------------------------------------------
// eval_api.FormatTimestamp
// ---------------------------------------------------------------------------

func TestFormatTimestamp(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "2024-01-15 10:30 UTC", eval_api.FormatTimestamp("2024-01-15 10:30 UTC"))
	assert.Contains(t, eval_api.FormatTimestamp(float64(1705312200)), "2024-01-15")
	assert.Contains(t, eval_api.FormatTimestamp(int64(1705312200)), "2024-01-15")
	assert.Equal(t, "", eval_api.FormatTimestamp(nil))
	assert.Equal(t, "", eval_api.FormatTimestamp(true))
}

// ---------------------------------------------------------------------------
// eval_api.ResolveRelPath
// ---------------------------------------------------------------------------

func TestResolveRelPath(t *testing.T) {
	t.Parallel()

	t.Run("absolute path returned as-is", func(t *testing.T) {
		t.Parallel()
		abs := filepath.Join(os.TempDir(), "eval.yaml")
		assert.Equal(t, abs, eval_api.ResolveRelPath(abs, "/project"))
	})

	t.Run("relative path joined with agent project", func(t *testing.T) {
		t.Parallel()
		result := eval_api.ResolveRelPath("eval.yaml", "/project/agent")
		assert.Equal(t, filepath.Join("/project/agent", "eval.yaml"), result)
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
// EvalState — stored in azd environment (integration-tested via eval generate/run)
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// writeEvalReviewArtifacts
// ---------------------------------------------------------------------------

func TestWriteEvalReviewArtifacts(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	cfg := &evalConfig{}
	cfg.DatasetReference = &evalDatasetRef{Name: "test-data", Version: "v1"}
	cfg.Evaluators = opt_eval.EvaluatorList{{Name: "quality"}}

	err := eval_api.WriteEvalReviewArtifacts(dir, cfg)
	require.NoError(t, err)

	evPath := filepath.Join(dir, "evaluators", "quality", "quality.yaml")
	assert.FileExists(t, evPath)
}

func TestWriteEvalReviewArtifacts_NilDataset(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	cfg := &evalConfig{}
	// No dataset reference — should not panic.
	err := eval_api.WriteEvalReviewArtifacts(dir, cfg)
	require.NoError(t, err)
}

// ---------------------------------------------------------------------------
// saveEvaluatorResult
// ---------------------------------------------------------------------------

func TestSaveEvaluatorResult(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	result := json.RawMessage(`{"name":"smoke-core","definition":{"type":"rubric","dimensions":[{"id":"quality","weight":10}]}}`)
	require.NoError(t, eval_api.SaveEvaluatorResult(dir, "smoke-core", result))

	path := filepath.Join(dir, "evaluators", "smoke-core", "rubric_dimensions.json")
	assert.FileExists(t, path)
	data, err := os.ReadFile(path) //nolint:gosec // test file path
	require.NoError(t, err)
	// Only the dimensions array is saved, not the outer fields.
	assert.Contains(t, string(data), `"id": "quality"`)
	assert.Contains(t, string(data), `"weight": 10`)
	assert.NotContains(t, string(data), `"name": "smoke-core"`)
}

func TestSaveEvaluatorResult_WithVersion(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	result := json.RawMessage(`{"name":"custom","definition":{"type":"rubric","dimensions":[{"id":"d1","weight":5}]}}`)
	require.NoError(t, eval_api.SaveEvaluatorResult(dir, "custom", result))

	path := filepath.Join(dir, "evaluators", "custom", "rubric_dimensions.json")
	assert.FileExists(t, path)
}

func TestSaveEvaluatorResult_NilResult(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	require.NoError(t, eval_api.SaveEvaluatorResult(dir, "test", nil))
	path := filepath.Join(dir, "evaluators", "test", "rubric_dimensions.json")
	assert.NoFileExists(t, path)
}

func TestSaveEvaluatorResult_EmptyName(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	require.NoError(t, eval_api.SaveEvaluatorResult(dir, "", json.RawMessage(`{"name":"x"}`)))
	// Should not create any file.
	matches, _ := filepath.Glob(filepath.Join(dir, "evaluators", "*.json"))
	assert.Empty(t, matches)
}

func TestWriteEvalReviewArtifacts_SkipsWhenResultExists(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	// Pre-save a result file.
	require.NoError(t, eval_api.SaveEvaluatorResult(dir, "quality", json.RawMessage(`{"name":"quality","definition":{"type":"rubric","dimensions":[{"id":"q","weight":1}]}}`)))

	cfg := &evalConfig{}
	cfg.Evaluators = opt_eval.EvaluatorList{{Name: "quality"}}
	err := eval_api.WriteEvalReviewArtifacts(dir, cfg)
	require.NoError(t, err)

	// Should NOT create a .yaml stub since .json result already exists.
	yamlPath := filepath.Join(dir, "evaluators", "quality", "quality.yaml")
	assert.NoFileExists(t, yamlPath)
}

// ---------------------------------------------------------------------------
// downloadDatasetArtifact
// ---------------------------------------------------------------------------

func TestDownloadDatasetArtifact_NilDataset(t *testing.T) {
	t.Parallel()
	_, err := eval_api.DownloadDatasetArtifact(t.Context(), nil, t.TempDir(), nil, "v1")
	require.NoError(t, err)
}

func TestDownloadDatasetArtifact_WritesBlob(t *testing.T) {
	t.Parallel()

	// The Azure SDK bearer token policy rejects non-TLS test servers, so the
	// credential call will fail. downloadDatasetArtifact now returns the error.
	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"sas_uri":"http://blob.example/data?sig=abc"}`))
	}))
	t.Cleanup(apiServer.Close)

	client := dataset_api.NewDatasetClient(apiServer.URL, &fakeTokenCredential{})
	dir := t.TempDir()

	ref := &evalDatasetRef{Name: "test-ds", Version: "v1"}
	_, err := eval_api.DownloadDatasetArtifact(t.Context(), client, dir, ref, "v1")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "getting dataset credential")

	// No file written when credential fetch fails.
	dest := eval_api.DatasetArtifactPath(dir, ref)
	assert.NoDirExists(t, dest)
}

// ---------------------------------------------------------------------------
// datasetArtifactPath
// ---------------------------------------------------------------------------

func TestDatasetArtifactPath(t *testing.T) {
	t.Parallel()
	ref := &evalDatasetRef{Name: "golden", Version: "v2"}
	result := eval_api.DatasetArtifactPath("/project", ref)
	assert.Equal(t, filepath.Join("/project", "datasets", "golden"), result)

	// No version — same path, version not included.
	refNoVer := &evalDatasetRef{Name: "golden", Version: ""}
	resultNoVer := eval_api.DatasetArtifactPath("/project", refNoVer)
	assert.Equal(t, filepath.Join("/project", "datasets", "golden"), resultNoVer)
}

// ---------------------------------------------------------------------------
// writeJSONFile
// ---------------------------------------------------------------------------

func TestWriteJSONFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "result.json")

	err := eval_api.WriteJSONFile(path, map[string]string{"hello": "world"})
	require.NoError(t, err)

	data, err := os.ReadFile(path) //nolint:gosec // test file path
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
// eval_api.WriteEvalConfig / eval_api.LoadEvalConfig round-trip
// ---------------------------------------------------------------------------

func TestEvalConfigRoundTrip(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "eval.yaml")

	original := &evalConfig{
		Config: opt_eval.Config{
			Name: "smoke-core",
			Agent: evalAgentRef{
				Name:    "my-agent",
				Kind:    agent_yaml.AgentKindHosted,
				Version: "v1",
			},
			DatasetReference: &evalDatasetRef{Name: "ds", Version: "v1"},
			Evaluators:       opt_eval.EvaluatorList{{Name: "builtin.task_adherence"}},
		},
		Options: &opt_eval.Options{
			EvalModel: "gpt-4o",
		},
		MaxSamples: 50,
	}

	err := eval_api.WriteEvalConfig(path, original)
	require.NoError(t, err)

	loaded, err := eval_api.LoadEvalConfig(path)
	require.NoError(t, err)

	assert.Equal(t, original.Name, loaded.Name)
	assert.Equal(t, original.Agent.Name, loaded.Agent.Name)
	assert.Equal(t, original.Agent.Kind, loaded.Agent.Kind)
	assert.Equal(t, original.Agent.Version, loaded.Agent.Version)
	assert.Equal(t, "gpt-4o", loaded.Options.EvalModel)
	assert.Equal(t, original.MaxSamples, loaded.MaxSamples)
	require.NotNil(t, loaded.DatasetReference)
	assert.Equal(t, "ds", loaded.DatasetReference.Name)
	require.Len(t, loaded.Evaluators, 1)
	assert.Equal(t, "builtin.task_adherence", loaded.Evaluators[0].Name)
}

func TestReadEvalConfig_MissingFile(t *testing.T) {
	t.Parallel()
	_, err := eval_api.LoadEvalConfig("/nonexistent/path/eval.yaml")
	assert.Error(t, err)
}

// ---------------------------------------------------------------------------
// endpointFromProjectID — project ID to endpoint conversion
// ---------------------------------------------------------------------------

func TestEndpointFromProjectID(t *testing.T) {
	t.Parallel()
	t.Run("valid project ID", func(t *testing.T) {
		t.Parallel()
		projectID := "/subscriptions/sub123/resourceGroups/rg/providers/Microsoft.CognitiveServices/accounts/myaccount/projects/myproject"
		endpoint, err := endpointFromProjectID(projectID)
		require.NoError(t, err)
		assert.Contains(t, endpoint, "myaccount")
		assert.Contains(t, endpoint, "myproject")
	})

	t.Run("invalid project ID", func(t *testing.T) {
		t.Parallel()
		_, err := endpointFromProjectID("not-a-valid-id")
		assert.Error(t, err)
	})

	t.Run("empty project ID", func(t *testing.T) {
		t.Parallel()
		_, err := endpointFromProjectID("")
		assert.Error(t, err)
	})
}
