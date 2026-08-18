// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package eval_api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The generation-job listings answer with the same has_more/last_id cursor the
// OpenAI listings use, but GenerationJobList did not carry those fields, so
// both read one page and stopped. Against the shared bug bash project that meant
// `job list` reported the first twenty jobs of many, with nothing to say so.
func TestGenerationJobListingsFollowTheCursor(t *testing.T) {
	for _, tc := range []struct {
		name string
		path string
		list func(*EvalClient) (*GenerationJobList, error)
	}{
		{
			name: "dataset jobs",
			path: "/data_generation_jobs",
			list: func(c *EvalClient) (*GenerationJobList, error) {
				return c.ListDataGenerationJobs(t.Context(), "2025-11-15-preview")
			},
		},
		{
			name: "evaluator jobs",
			path: "/evaluator_generation_jobs",
			list: func(c *EvalClient) (*GenerationJobList, error) {
				return c.ListEvaluatorGenerationJobs(t.Context(), "2025-11-15-preview")
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var afters []string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, tc.path, r.URL.Path)
				after := r.URL.Query().Get("after")
				afters = append(afters, after)

				w.WriteHeader(http.StatusOK)
				switch after {
				case "":
					_, _ = w.Write([]byte(
						`{"data":[{"id":"j1"},{"id":"j2"}],"has_more":true,"last_id":"j2"}`))
				case "j2":
					_, _ = w.Write([]byte(
						`{"data":[{"id":"j3"}],"has_more":false,"last_id":"j3"}`))
				default:
					t.Errorf("unexpected cursor %q", after)
				}
			}))
			t.Cleanup(srv.Close)

			client := NewEvalClientFromPipeline(
				srv.URL, runtime.NewPipeline("test", "v1", runtime.PipelineOptions{}, nil))

			list, err := tc.list(client)
			require.NoError(t, err)
			require.Len(t, list.Data, 3, "every page should be gathered, not just the first")
			assert.Equal(t, []string{"", "j2"}, afters,
				"the second page should be asked for with the cursor the first returned")
		})
	}
}

// A listing that fits in one page must not ask for a second.
func TestGenerationJobListingStopsWithoutACursor(t *testing.T) {
	var requests int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"data":[{"id":"j1"}],"has_more":false,"last_id":"j1"}`))
	}))
	t.Cleanup(srv.Close)

	client := NewEvalClientFromPipeline(
		srv.URL, runtime.NewPipeline("test", "v1", runtime.PipelineOptions{}, nil))

	list, err := client.ListDataGenerationJobs(t.Context(), "2025-11-15-preview")
	require.NoError(t, err)
	assert.Len(t, list.Data, 1)
	assert.Equal(t, 1, requests)
}
