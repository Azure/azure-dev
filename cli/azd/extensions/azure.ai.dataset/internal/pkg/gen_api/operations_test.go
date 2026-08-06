// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package gen_api

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// call is what the client actually put on the wire, which is the part of these
// operations that can be wrong without anything failing to compile.
type call struct {
	method string
	path   string
	query  url.Values
	body   string
}

// recorder answers every request with status and body, remembering the last one.
func recorder(t *testing.T, status int, body string) (*Client, *call) {
	t.Helper()
	var last call
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		last = call{method: r.Method, path: r.URL.Path, query: r.URL.Query(), body: string(raw)}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		if body != "" {
			_, _ = w.Write([]byte(body))
		}
	}))
	t.Cleanup(server.Close)

	// MaxRetries -1 disables the SDK's retry policy. Without it a test that
	// answers 5xx on purpose spends ten seconds being retried.
	client := NewClientFromPipeline(server.URL, runtime.NewPipeline(
		"test", "v1.0.0", runtime.PipelineOptions{},
		&policy.ClientOptions{Retry: policy.RetryOptions{MaxRetries: -1}}))
	return client, &last
}

// The cancel route takes a colon, not a path segment. `{id}/cancel` is a 404
// while `{id}:cancel` reaches the action, and nothing but the URL says so.
func TestCancelDataGenerationJob_UsesTheColonForm(t *testing.T) {
	client, last := recorder(t, http.StatusOK, `{"id":"dgj_1","status":"cancelled"}`)

	_, err := client.CancelDataGenerationJob(context.Background(), "dgj_1", "v1")

	require.NoError(t, err)
	assert.Equal(t, http.MethodPost, last.method)
	assert.Equal(t, "/data_generation_jobs/dgj_1:cancel", last.path)
	assert.Equal(t, "{}", last.body,
		"the empty object is what carries a content type; without it the route answers 415")
}

// A delete that removed the record answers 204 with no body. Treating that as a
// failure would report every successful delete as an error.
func TestDeleteDataGenerationJob_AcceptsNoContent(t *testing.T) {
	client, last := recorder(t, http.StatusNoContent, "")

	require.NoError(t, client.DeleteDataGenerationJob(context.Background(), "dgj_1", "v1"))

	assert.Equal(t, http.MethodDelete, last.method)
	assert.Equal(t, "/data_generation_jobs/dgj_1", last.path)
}

// The job routes answer with `data`, not the `value` the dataset routes use.
// Reading the wrong key returns an empty list from a full response.
func TestListDataGenerationJobs_ReadsTheDataEnvelope(t *testing.T) {
	client, last := recorder(t, http.StatusOK,
		`{"data":[{"id":"dgj_1","status":"completed"},{"id":"dgj_2","status":"running"}]}`)

	list, err := client.ListDataGenerationJobs(context.Background(), "v1")

	require.NoError(t, err)
	assert.Equal(t, "/data_generation_jobs", last.path)
	require.Len(t, list.Data, 2)
	assert.Equal(t, "dgj_1", list.Data[0].ID)
}

// The request body is what the service validates, so what reaches the wire is
// pinned rather than left to whatever the builder happened to set.
func TestCreateDataGenerationJob_SendsTheBuiltRequest(t *testing.T) {
	client, last := recorder(t, http.StatusOK, `{"id":"dgj_1","status":"running"}`)

	req := NewDataGenerationJobRequest("support-regression", "gpt-4o", 15,
		[]GenerationSource{{Type: "prompt", Prompt: "be helpful"}})
	_, err := client.CreateDataGenerationJob(context.Background(), req, "v1")

	require.NoError(t, err)
	assert.Equal(t, http.MethodPost, last.method)
	assert.Equal(t, "/data_generation_jobs", last.path)
	assert.Contains(t, last.body, `"name":"support-regression"`)
	assert.Contains(t, last.body, `"max_samples":15`)
	assert.Contains(t, last.body, `"scenario":"evaluation"`)
}

// An id goes into the path, so one containing a separator has to be escaped or
// it silently addresses a different route.
func TestOperations_EscapeIdsInThePath(t *testing.T) {
	client, last := recorder(t, http.StatusOK, `{"id":"x"}`)

	_, err := client.GetDataGenerationJob(context.Background(), "dgj/../evil", "v1")

	require.NoError(t, err)
	assert.NotContains(t, last.path, "/../",
		"an unescaped id would let a name climb out of its route")
}

func TestOperations_SendTheApiVersion(t *testing.T) {
	client, last := recorder(t, http.StatusOK, `{"id":"x"}`)

	_, err := client.GetDataGenerationJob(context.Background(), "dgj_1", "2025-11-15-preview")

	require.NoError(t, err)
	assert.Equal(t, "2025-11-15-preview", last.query.Get("api-version"))
}

// Only the newest agent version seeds generation: the point is to describe what
// the agent does now.
func TestGetAgent_ReadsTheCatalogEntry(t *testing.T) {
	client, last := recorder(t, http.StatusOK,
		`{"name":"support","versions":{"latest":{"version":"3",`+
			`"definition":{"instructions":"Be helpful."}}}}`)

	agent, err := client.GetAgent(context.Background(), "support", "v1")

	require.NoError(t, err)
	assert.Equal(t, "/agents/support", last.path)
	assert.Equal(t, "Be helpful.", agent.Instructions())
}

// A 404 has to arrive as one, because jobLookupError branches on it to point at
// the evaluator group rather than reporting a transport failure.
func TestOperations_NotFoundIsRecognizable(t *testing.T) {
	client, _ := recorder(t, http.StatusNotFound, `{"error":{"code":"NotFound"}}`)

	_, err := client.GetDataGenerationJob(context.Background(), "dgj_missing", "v1")

	require.Error(t, err)
	assert.True(t, IsNotFound(err))
}

// A server fault is worth retrying and has to be recognizable as such, or the
// poller gives up on a job the service is still working on.
func TestOperations_ServerFaultIsTransient(t *testing.T) {
	client, _ := recorder(t, http.StatusBadGateway, `{"error":{"code":"BadGateway"}}`)

	_, err := client.GetDataGenerationJob(context.Background(), "dgj_1", "v1")

	require.Error(t, err)
	assert.True(t, IsTransientError(err))
	assert.False(t, IsNotFound(err))
}

// An empty body on a success is not a parse failure: a 204 carries none, and
// the typed helper has to hand back a zero value rather than an error.
func TestOperations_EmptyBodyIsNotAnError(t *testing.T) {
	client, _ := recorder(t, http.StatusOK, "")

	job, err := client.GetDataGenerationJob(context.Background(), "dgj_1", "v1")

	require.NoError(t, err)
	require.NotNil(t, job)
	assert.Empty(t, job.ID)
}
