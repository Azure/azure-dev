// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package eval_api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// call is what the client actually put on the wire, which is the part of these
// operations that can be wrong without anything failing to compile.
type call struct {
	method string
	path   string
	// rawPath is the path as it went over the wire, where escaping is still
	// visible. path has been decoded and cannot tell %2F from a separator.
	rawPath string
	query   url.Values
	body    string
}

// recorder answers every request with status and body, remembering the last one.
func recorder(t *testing.T, status int, body string) (*EvalClient, *call) {
	t.Helper()
	var last call
	client := newRecordingClient(t, func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		last = call{
			method:  r.Method,
			path:    r.URL.Path,
			rawPath: r.URL.EscapedPath(),
			query:   r.URL.Query(),
			body:    string(raw),
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		if body != "" {
			_, _ = w.Write([]byte(body))
		}
	})
	return client, &last
}

// The cancel route takes a colon, not a path segment. `{id}/cancel` is a 404
// while `{id}:cancel` reaches the action, and nothing but the URL says so.
func TestCancelGenerationJob_UsesTheColonForm(t *testing.T) {
	tests := []struct {
		name   string
		cancel func(*EvalClient) error
		want   string
	}{
		{
			name: "dataset",
			cancel: func(c *EvalClient) error {
				_, err := c.CancelDataGenerationJob(context.Background(), "dgj_1", "v1")
				return err
			},
			want: "/data_generation_jobs/dgj_1:cancel",
		},
		{
			name: "evaluator",
			cancel: func(c *EvalClient) error {
				_, err := c.CancelEvaluatorGenerationJob(context.Background(), "egj_1", "v1")
				return err
			},
			want: "/evaluator_generation_jobs/egj_1:cancel",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, last := recorder(t, http.StatusOK, `{"id":"j_1","status":"cancelled"}`)

			require.NoError(t, tt.cancel(client))

			assert.Equal(t, http.MethodPost, last.method)
			assert.Equal(t, tt.want, last.path)
			assert.Equal(t, "{}", last.body,
				"the empty object is what carries a content type; without it the route answers 415")
		})
	}
}

// A delete that removed the record answers 204 with no body. Treating that as a
// failure would report every successful delete as an error.
func TestDeleteGenerationJob_AcceptsNoContent(t *testing.T) {
	client, last := recorder(t, http.StatusNoContent, "")

	require.NoError(t, client.DeleteDataGenerationJob(context.Background(), "dgj_1", "v1"))

	assert.Equal(t, http.MethodDelete, last.method)
	assert.Equal(t, "/data_generation_jobs/dgj_1", last.path)
}

// The job routes answer with `data`, not the `value` the dataset and evaluator
// routes use. Reading the wrong key returns an empty list from a full response.
func TestListGenerationJobs_ReadsTheDataEnvelope(t *testing.T) {
	client, _ := recorder(t, http.StatusOK,
		`{"data":[{"id":"dgj_1","status":"completed"},{"id":"dgj_2","status":"running"}]}`)

	list, err := client.ListDataGenerationJobs(context.Background(), "v1")

	require.NoError(t, err)
	require.Len(t, list.Data, 2)
	assert.Equal(t, "dgj_1", list.Data[0].ID)
}

// An id goes into the path, so one containing a separator has to be escaped or
// it silently addresses a different route.
//
// The assertion is on the wire form: the decoded path shows the separators
// again, so it cannot tell a correctly escaped id from an unescaped one.
func TestOperations_EscapeIdsInThePath(t *testing.T) {
	client, last := recorder(t, http.StatusOK, `{"id":"x"}`)

	_, err := client.GetDataGenerationJob(context.Background(), "dgj/../evil", "v1")

	require.NoError(t, err)
	assert.Equal(t, "/data_generation_jobs/dgj%2F..%2Fevil", last.rawPath,
		"the id stays one segment; escaping it twice would send %252F and address a differently named job")
}

// The api-version is what selects the contract; sending the wrong one, or none,
// is answered by a different shape than the client parses.
func TestOperations_SendTheApiVersion(t *testing.T) {
	client, last := recorder(t, http.StatusOK, `{"id":"x"}`)

	_, err := client.GetDataGenerationJob(context.Background(), "dgj_1", "2025-11-15-preview")

	require.NoError(t, err)
	assert.Equal(t, "2025-11-15-preview", last.query.Get("api-version"))
}

// The OpenAI-compatible eval routes send no api-version at all, so adding one
// would be as wrong as omitting it elsewhere.
func TestOpenAIEvalRoutes_SendNoApiVersion(t *testing.T) {
	client, last := recorder(t, http.StatusOK, `{"id":"eval_1"}`)

	_, err := client.GetOpenAIEval(context.Background(), "eval_1")

	require.NoError(t, err)
	assert.Equal(t, "/openai/v1/evals/eval_1", last.path)
	assert.Empty(t, last.query.Get("api-version"))
}

// A rename is pushed in place so the eval keeps its id and its run history.
// The route is a POST to the eval itself, not a PATCH and not a new eval.
func TestUpdateOpenAIEval_PostsToTheEval(t *testing.T) {
	client, last := recorder(t, http.StatusOK, `{"id":"eval_1","name":"renamed"}`)

	_, err := client.UpdateOpenAIEval(context.Background(), "eval_1",
		&UpdateOpenAIEvalRequest{Name: "renamed"})

	require.NoError(t, err)
	assert.Equal(t, http.MethodPost, last.method)
	assert.Equal(t, "/openai/v1/evals/eval_1", last.path)

	var sent map[string]any
	require.NoError(t, json.Unmarshal([]byte(last.body), &sent))
	assert.Equal(t, "renamed", sent["name"])
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

// A 404 has to arrive as one, because the commands branch on it to tell "no
// such thing" apart from "the call failed".
func TestOperations_NotFoundIsRecognizable(t *testing.T) {
	client, _ := recorder(t, http.StatusNotFound, `{"error":{"code":"NotFound"}}`)

	_, err := client.GetOpenAIEval(context.Background(), "eval_missing")

	require.Error(t, err)
	assert.True(t, IsNotFound(err), "the commands branch on this to name the thing that is missing")
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
