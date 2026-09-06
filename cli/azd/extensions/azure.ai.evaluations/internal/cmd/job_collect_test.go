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
	"testing"

	"azureaieval/internal/pkg/dataset_api"
	"azureaieval/internal/pkg/eval_api"
	"azureaieval/internal/project"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// generationServer answers the three calls collecting a dataset makes: the
// version read, the blob URI, and the rows themselves.
func generationServer(t *testing.T, rows string) *httptest.Server {
	t.Helper()
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/rows.jsonl"):
			_, _ = w.Write([]byte(rows))
		case strings.HasSuffix(r.URL.Path, "/credentials"):
			w.Header().Set("Content-Type", "application/json")
			// assert, not require: this runs on the server's goroutine.
			assert.NoError(t, json.NewEncoder(w).Encode(map[string]any{
				"blobReferenceForConsumption": map[string]any{
					"blobUri":    srv.URL + "/rows.jsonl",
					"credential": map[string]any{"sasUri": srv.URL + "/rows.jsonl"},
				},
			}))
		default:
			w.Header().Set("Content-Type", "application/json")
			assert.NoError(t, json.NewEncoder(w).Encode(map[string]any{
				"name": "golden", "version": "3",
			}))
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// datasetJobResult is a finished data job as the service reports one: the name
// and version live in the result, not on the job itself.
func datasetJobResult(name, version string) *eval_api.GenerationJob {
	return &eval_api.GenerationJob{
		ID:     "job_1",
		Status: "completed",
		Result: json.RawMessage(`{"name":"` + name + `","version":"` + version + `"}`),
	}
}

// `generate --no-wait` returns before the job has produced anything, and the
// CLI directs the caller to `job show` to collect the artifact -- including
// from the error refusing `--output-dir --no-wait`. It only printed status, so
// the rows the job was billed for were never downloaded and the catalog entry
// was never written.
func TestCollectingASucceededDatasetJobWritesTheArtifact(t *testing.T) {
	const rows = "{\"query\":\"hi\"}\n"
	srv := generationServer(t, rows)

	pipeline := runtime.NewPipeline("test", "v1", runtime.PipelineOptions{},
		&policy.ClientOptions{Retry: policy.RetryOptions{MaxRetries: -1}})
	ec := &evalContext{
		evalClient:    eval_api.NewEvalClientFromPipeline(srv.URL, pipeline),
		datasetClient: dataset_api.NewDatasetClientFromPipeline(srv.URL, pipeline),
	}

	evalDir := t.TempDir()
	var out bytes.Buffer
	ref, err := ec.collectDataset(
		t.Context(),
		datasetJobResult("golden", "3"),
		"", evalDir, "datasets", &out,
	)

	require.NoError(t, err)
	require.NotNil(t, ref, "the caller needs a catalog entry to write")
	assert.Equal(t, "golden", ref.Name, "the service's own name stands in when reattaching")
	assert.Equal(t, "3", ref.Version)

	written, err := os.ReadFile(filepath.Join(evalDir, "datasets", "golden.jsonl"))
	require.NoError(t, err, "the rows the job was billed for have to reach the disk")
	assert.Equal(t, rows, string(written))
	assert.Contains(t, out.String(), "golden.jsonl", "and the caller is told where they landed")
}

// A rubric is the same contract on the other collection.
func TestCollectingASucceededEvaluatorJobWritesTheRubric(t *testing.T) {
	ec := &evalContext{}
	evalDir := t.TempDir()

	var out bytes.Buffer
	ref, err := ec.collectRubric(
		&eval_api.GenerationJob{
			ID:     "job_1",
			Status: "succeeded",
			Result: json.RawMessage(`{"name":"quality","version":"2","definition":{"dimensions":[]}}`),
		},
		"", evalDir, "evaluators", &out,
	)

	require.NoError(t, err)
	require.NotNil(t, ref)

	written, err := os.ReadFile(filepath.Join(evalDir, "evaluators", ref.Name+".json"))
	require.NoError(t, err)
	assert.Contains(t, string(written), "dimensions")
}

// Collecting twice is what a caller polling `job show` does, and it must not
// leave a second copy or a second catalog entry behind.
func TestCollectingTwiceIsTheSameAsCollectingOnce(t *testing.T) {
	const rows = "{\"query\":\"hi\"}\n"
	srv := generationServer(t, rows)

	pipeline := runtime.NewPipeline("test", "v1", runtime.PipelineOptions{},
		&policy.ClientOptions{Retry: policy.RetryOptions{MaxRetries: -1}})
	ec := &evalContext{
		evalClient:    eval_api.NewEvalClientFromPipeline(srv.URL, pipeline),
		datasetClient: dataset_api.NewDatasetClientFromPipeline(srv.URL, pipeline),
	}

	evalDir := t.TempDir()
	job := datasetJobResult("golden", "3")

	var out bytes.Buffer
	first, err := ec.collectDataset(t.Context(), job, "", evalDir, "datasets", &out)
	require.NoError(t, err)
	second, err := ec.collectDataset(t.Context(), job, "", evalDir, "datasets", &out)
	require.NoError(t, err)

	assert.Equal(t, first, second, "the same job collects to the same place")

	entries, err := os.ReadDir(filepath.Join(evalDir, "datasets"))
	require.NoError(t, err)
	assert.Len(t, entries, 1, "collecting again must not leave a second file")
}

// `job show` is the command a caller polls while waiting, so it is called far
// more often on a job with nothing to collect than on one with something. The
// guard, not the caller, has to hold that line.
//
// The context is deliberately nil: reaching the collection at all would
// dereference it, so this fails loudly if the guard is ever dropped rather than
// quietly downloading against a half-built job.
func TestAJobThatHasNotSucceededCollectsNothing(t *testing.T) {
	for _, status := range []string{"queued", "in_progress", "running", "failed", "cancelled"} {
		t.Run(status, func(t *testing.T) {
			a := &jobShowAction{cmd: &cobra.Command{}, flags: &jobFlags{}}

			ref, err := a.collect(
				t.Context(), nil, datasetJobs,
				&eval_api.GenerationJob{ID: "job_1", Status: status},
				io.Discard,
			)

			require.NoError(t, err, "a job still running is reported, not an error")
			assert.Nil(t, ref, "nothing was produced, so there is nothing to record")
		})
	}
}

// evalContextFor talks to the fake generation service over a pipeline with no
// credential, which is all collection needs.
func evalContextFor(srv *httptest.Server) *evalContext {
	pipeline := runtime.NewPipeline("test", "v1", runtime.PipelineOptions{},
		&policy.ClientOptions{Retry: policy.RetryOptions{MaxRetries: -1}})
	return &evalContext{
		evalClient:    eval_api.NewEvalClientFromPipeline(srv.URL, pipeline),
		datasetClient: dataset_api.NewDatasetClientFromPipeline(srv.URL, pipeline),
	}
}

// The whole of what `generate --no-wait` left undone, through the seam `job
// show` actually calls. Covering only the download would have missed the
// catalog write, which is the half that makes the artifact usable rather than
// merely present on disk.
func TestShowCollectsASucceededJobToDiskAndCatalog(t *testing.T) {
	srv := generationServer(t, "{\"query\":\"hi\"}\n")
	evalDir := t.TempDir()

	var buf bytes.Buffer
	a := &jobShowAction{cmd: catalogCommand(t, &buf), flags: &jobFlags{path: evalDir}}

	ref, err := a.collect(
		t.Context(), evalContextFor(srv), datasetJobs,
		datasetJobResult("golden", "3"), &buf,
	)

	require.NoError(t, err)
	require.NotNil(t, ref)

	_, err = os.Stat(filepath.Join(evalDir, "datasets", "golden.jsonl"))
	require.NoError(t, err, "the rows the job was billed for have to reach the disk")

	cfg, err := os.ReadFile(filepath.Join(evalDir, project.EvalConfigBase))
	require.NoError(t, err, "and the catalog has to name them, or nothing can use them")
	assert.Contains(t, string(cfg), "golden")
}

// `generate` refuses --output-dir alongside --no-wait and points the caller at
// `job show`, so `job show` honouring it is what makes that redirection true.
func TestShowWritesWhereTheCallerAsked(t *testing.T) {
	srv := generationServer(t, "{\"query\":\"hi\"}\n")
	evalDir := t.TempDir()

	var buf bytes.Buffer
	a := &jobShowAction{
		cmd:   catalogCommand(t, &buf),
		flags: &jobFlags{path: evalDir, outputDir: "elsewhere"},
	}

	_, err := a.collect(
		t.Context(), evalContextFor(srv), datasetJobs,
		datasetJobResult("golden", "3"), &buf,
	)
	require.NoError(t, err)

	_, err = os.Stat(filepath.Join(evalDir, "elsewhere", "golden.jsonl"))
	require.NoError(t, err, "the directory the caller named is where it goes")

	_, err = os.Stat(filepath.Join(evalDir, "datasets", "golden.jsonl"))
	assert.ErrorIs(t, err, os.ErrNotExist, "and not also in the default one")
}
