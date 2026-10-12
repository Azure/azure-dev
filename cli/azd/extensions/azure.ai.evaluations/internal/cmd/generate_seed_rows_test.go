// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"azureaieval/internal/pkg/eval_api"
	"azureaieval/internal/project"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNormalizeGeneratedSeedRows(t *testing.T) {
	for _, tc := range []struct {
		name, input, wantError string
		changed                bool
	}{
		{name: "canonical unchanged", input: " {\"simulation_configuration\":{\"desired_num_turns\":4}}\r\n"},
		{name: "description only unchanged", input: "{\"test_case_description\":\"A fixture.\"}\n"},
		{name: "flat target", input: `{"id":9007199254740993,"desired_num_turns":4}`, changed: true},
		{name: "merge other settings", input: `{"desired_num_turns":4,"simulation_configuration":{"max_num_turns":6}}`,
			changed: true},
		{name: "both locations", input: `{"desired_num_turns":4,"simulation_configuration":{"desired_num_turns":3}}`,
			wantError: "both inside and outside"},
		{name: "invalid setting object", input: `{"desired_num_turns":4,"simulation_configuration":null}`,
			wantError: "must be an object"},
		{name: "invalid JSON", input: "{", wantError: "generated seed row 1"},
		{name: "fractional target", input: `{"desired_num_turns":2.5}`, wantError: "positive whole number"},
		{name: "zero target", input: `{"desired_num_turns":0}`, wantError: "positive whole number"},
		{name: "null target", input: `{"desired_num_turns":null}`, wantError: "positive whole number"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			content, changed, err := normalizeGeneratedSeedRows([]byte(tc.input))
			if tc.wantError != "" {
				require.ErrorContains(t, err, tc.wantError)
				assert.Nil(t, content)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.changed, changed)
			if !changed {
				assert.Equal(t, tc.input, string(content), "canonical content must remain byte-identical")
				return
			}
			var row map[string]json.RawMessage
			require.NoError(t, json.Unmarshal(content, &row))
			assert.NotContains(t, row, seedTurnsField)
			var settings map[string]json.RawMessage
			require.NoError(t, json.Unmarshal(row[seedConfigField], &settings))
			assert.JSONEq(t, "4", string(settings[seedTurnsField]))
			if tc.name == "flat target" {
				assert.Equal(t, "9007199254740993", string(row["id"]), "do not round unrelated identifiers")
			} else {
				assert.JSONEq(t, "6", string(settings["max_num_turns"]))
			}
		})
	}
}

func TestGeneratedSeedsPublishCanonicalRowsBeforeSimulation(t *testing.T) {
	for _, tc := range []struct {
		name, outputDir, registryTag string
		force                        bool
	}{
		{name: "default path"},
		{name: "custom directory", outputDir: "custom seeds"},
		{name: "custom file", outputDir: "custom seeds/selected.jsonl"},
		{name: "replace existing file", outputDir: "custom seeds/selected.jsonl", force: true},
		{name: "service tags without job inputs", registryTag: tagDataGenerationType},
		{name: "portal tags without job inputs", registryTag: tagScenario},
	} {
		t.Run(tc.name, func(t *testing.T) {
			generatedSeedsPublishCanonicalRowsBeforeSimulation(t, tc.outputDir, tc.force, tc.registryTag)
		})
	}
}

func generatedSeedsPublishCanonicalRowsBeforeSimulation(
	t *testing.T, outputDir string, force bool, registryTag string,
) {
	t.Helper()

	const generated = `{"id":9007199254740993,"test_case_description":"A delayed order.","desired_num_turns":4}` + "\n"
	var uploaded []byte
	var submitted eval_api.CreateOpenAIEvalRunRequest
	var generatedDownloads atomic.Int64
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/datasets/golden/versions":
			_, _ = io.WriteString(w, `{"value":[{"version":"1"}]}`)
		case "/datasets/golden/versions/1":
			tags := seedDatasetTags("conversation")
			if registryTag != "" {
				tags = map[string]string{registryTag: "conversation_simulation"}
			}
			assert.NoError(t, json.NewEncoder(w).Encode(map[string]any{
				"name": "golden", "version": "1", "id": "generated-id", "tags": tags,
			}))
		case "/datasets/golden/versions/1/credentials":
			generatedDownloads.Add(1)
			assert.NoError(t, json.NewEncoder(w).Encode(map[string]string{"sas_uri": server.URL + "/generated.jsonl"}))
		case "/generated.jsonl":
			generatedDownloads.Add(1)
			_, _ = io.WriteString(w, generated)
		case "/datasets/golden/versions/2.0/startPendingUpload":
			assert.NoError(t, json.NewEncoder(w).Encode(map[string]any{"blobReference": map[string]any{
				"blobUri":    server.URL + "/upload",
				"credential": map[string]string{"sasUri": server.URL + "/upload"},
			}}))
		case "/upload/golden.jsonl":
			if r.Method == http.MethodPut {
				var err error
				uploaded, err = io.ReadAll(r.Body)
				assert.NoError(t, err)
				w.WriteHeader(http.StatusCreated)
			} else {
				_, _ = w.Write(uploaded)
			}
		case "/datasets/golden/versions/2.0":
			_, _ = io.WriteString(w, `{"name":"golden","version":"2.0","id":"canonical-issued-id"}`)
		case "/datasets/golden/versions/2.0/credentials":
			assert.NoError(t, json.NewEncoder(w).Encode(map[string]string{
				"sas_uri": server.URL + "/upload/golden.jsonl",
			}))
		case "/openai/v1/evals/eval_1/runs":
			assert.NoError(t, json.NewDecoder(r.Body).Decode(&submitted))
			_, _ = io.WriteString(w, `{"id":"evalrun_1","status":"queued"}`)
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	ec := evalContextFor(server)
	env := &testEnvServer{state: map[string]string{
		project.FingerprintKey("dataset", "golden"): "previously-published-file",
		versionKey("dataset", "golden"):             "old-version",
	}}
	ec.azdClient, ec.envName = newTestAzdClient(t, env), "test"
	dir := t.TempDir()
	expectedPath := project.ArtifactPath(dir, firstNonEmpty(outputDir, project.DefaultDatasetsDir), "golden", ".jsonl")
	if force {
		require.NoError(t, os.MkdirAll(filepath.Dir(expectedPath), 0o750))
		require.NoError(t, os.WriteFile(expectedPath, []byte("existing edited content\n"), 0o600))
	}
	var output bytes.Buffer
	action := &jobShowAction{
		cmd: catalogCommand(t, &output), flags: &jobFlags{path: dir, outputDir: outputDir, force: force},
	}
	job := datasetJobResult("golden", "1")
	if registryTag == "" {
		job.Inputs = &eval_api.DataGenerationInputs{Options: eval_api.DataGenerationOptions{Type: "simulation_seed"}}
	}
	ref, err := action.collect(t.Context(), ec, datasetJobs, job, &output)
	require.NoError(t, err)
	assert.Equal(t, "1", ref.Version, "retain the generation job's version as provenance")
	assert.Empty(t, env.stored(t, project.FingerprintKey("dataset", "golden")))
	assert.Empty(t, env.stored(t, versionKey("dataset", "golden")))
	assert.Empty(t, uploaded, "collection must not silently publish a replacement version")
	assert.Contains(t, output.String(), "Publish the local dataset")
	cfg, err := project.OpenEvalConfig(dir)
	require.NoError(t, err)
	decl, ok := cfg.DatasetDeclaration("golden")
	require.True(t, ok)
	path := filepath.Join(dir, filepath.FromSlash(ref.Source))
	assert.Equal(t, filepath.Clean(expectedPath), filepath.Clean(path))
	assert.Equal(t, ref.Source, decl.File, "the catalog must point to the requested output")
	assert.Empty(t, decl.Version, "the generation version must not pin normalized content")
	content, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Contains(t, string(content), `"simulation_configuration":{"desired_num_turns":4}`)
	assert.Contains(t, string(content), `"id":9007199254740993`)
	action.flags.force = false
	downloads := generatedDownloads.Load()
	again, err := action.collect(t.Context(), ec, datasetJobs, job, &output)
	require.NoError(t, err)
	assert.Equal(t, ref, again, "reattaching preserves the same artifact provenance")
	assert.Empty(t, env.stored(t, project.FingerprintKey("dataset", "golden")))
	assert.Equal(t, downloads, generatedDownloads.Load(), "reattaching must not download the old generated version")

	version, changed, err := (&evalReconciler{ec: ec}).EnsureDataset(t.Context(), *decl, path)
	require.NoError(t, err)
	require.True(t, changed)
	require.Equal(t, "2.0", version)
	assert.Equal(t, content, uploaded)
	group := runnableSimulation()
	group.Dataset = "golden"
	source, resolved, err := ec.buildRunDataSource(t.Context(), group, filepath.Join(dir, project.EvalConfigBase), 0)
	require.NoError(t, err)
	assert.Equal(t, version, resolved)
	_, err = ec.evalClient.CreateOpenAIEvalRun(t.Context(), "eval_1", &eval_api.CreateOpenAIEvalRunRequest{
		Name: "simulation", DataSource: source, EvaluationLevel: "conversation",
		Metadata: map[string]string{metaDatasetVersion: resolved},
	})
	require.NoError(t, err)
	require.NotNil(t, submitted.DataSource)
	assert.Equal(t, "canonical-issued-id", submitted.DataSource.Source.ID)
	assert.Empty(t, submitted.DataSource.Source.Content)
	assert.Equal(t, "2.0", submitted.Metadata[metaDatasetVersion])
	assert.Equal(t, seedConfigField, submitted.DataSource.DataMapping[seedConfigField])
	assert.Equal(t, group.Simulation.Model, submitted.DataSource.ModelConfiguration.Model)

	publishedDigest, err := project.Fingerprint(path)
	require.NoError(t, err)
	for _, local := range [][]byte{content, []byte("{\"test_case_description\":\"A locally edited scenario.\"}\r\n")} {
		require.NoError(t, os.WriteFile(path, local, 0o600))
		collected, err := action.collect(t.Context(), ec, datasetJobs, job, &output)
		require.NoError(t, err)
		assert.Equal(t, ref, collected, "the original generation provenance must remain stable after publication")
		assert.Equal(t, downloads, generatedDownloads.Load(), "do not fetch the old version over local content")
		preserved, err := os.ReadFile(path)
		require.NoError(t, err)
		assert.Equal(t, local, preserved)
		assert.Equal(t, "2.0", env.stored(t, versionKey("dataset", "golden")))
		assert.Equal(t, publishedDigest, env.stored(t, project.FingerprintKey("dataset", "golden")))
		current, err := project.OpenEvalConfig(dir)
		require.NoError(t, err)
		catalog, ok := current.DatasetDeclaration("golden")
		require.True(t, ok)
		assert.Equal(t, "conversation", catalog.Tags[tagEvaluationLevel])
		assert.Empty(t, catalog.Version)
	}
}

func TestSeedNormalizationFailurePreservesExistingFile(t *testing.T) {
	server := generationServer(t, `{"desired_num_turns":4,"simulation_configuration":{"desired_num_turns":3}}`)
	dir := t.TempDir()
	path := filepath.Join(dir, "golden.jsonl")
	const original = "the existing local bytes\n"
	require.NoError(t, os.WriteFile(path, []byte(original), 0o600))
	ref, err := evalContextFor(server).collectDataset(
		t.Context(), datasetJobResult("golden", "3"), "", dir, "", "conversation", io.Discard, true)
	require.ErrorContains(t, err, "both inside and outside")
	assert.Nil(t, ref)
	content, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, original, string(content))
	assert.False(t, strings.Contains(string(content), "desired_num_turns"))
}

func TestTurnGenerationPreservesFlatFields(t *testing.T) {
	const rows = "{\"query\":\"fixture\",\"desired_num_turns\":4}\n"
	server := generationServer(t, rows)
	dir := t.TempDir()
	ec := evalContextFor(server)
	env := &testEnvServer{}
	ec.azdClient, ec.envName = newTestAzdClient(t, env), "test"
	ref, err := ec.collectDataset(t.Context(), datasetJobResult("golden", "3"),
		"", dir, "", "turn", io.Discard, false)
	require.NoError(t, err)
	require.NotNil(t, ref)
	path := filepath.Join(dir, "golden.jsonl")
	content, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, rows, string(content))
	digest, err := project.Fingerprint(path)
	require.NoError(t, err)
	assert.Equal(t, digest, env.stored(t, project.FingerprintKey("dataset", "golden")))
	assert.Equal(t, "3", env.stored(t, versionKey("dataset", "golden")))
}
