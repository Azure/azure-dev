// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package optimize_api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSkillCandidateConfigCannotBeAppliedOrDeployed(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/agent_optimization_jobs/job/candidates/candidate", r.URL.Path)
		_, _ = w.Write([]byte(`{"candidate_id":"candidate","output":{"type":"skill_optimization"},
			"files":[{"path":"config.json","type":"config"}]}`))
	}))
	defer server.Close()
	config, err := newTestClient(server.URL).GetCandidateConfig(t.Context(), "job", "candidate")
	require.ErrorContains(t, err, "skill optimization publishes skill versions")
	assert.Nil(t, config)
}

func TestSkillCandidateDetailsHydratedOnlyForTerminalJobs(t *testing.T) {
	t.Parallel()
	for _, status := range []string{StatusQueued, StatusInProgress, StatusSucceeded, StatusFailed, StatusCancelled, StatusSkipped} {
		t.Run(status, func(t *testing.T) {
			t.Parallel()
			var details atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, "/candidate") {
					details.Add(1)
					_, _ = w.Write([]byte(`{"candidate_id":"candidate","rationale":"Exact detail",
						"output":{"type":"skill_optimization","skill_results":[]}}`))
					return
				}
				_, _ = w.Write([]byte(`{"data":[{"candidate_id":"candidate","output":{"type":"skill_optimization"}}]}`))
			}))
			defer server.Close()
			job := &OptimizeJobStatus{ID: "job", Status: status,
				OptimizationConfiguration: OptimizationConfiguration{Type: SkillOptimizationType}}
			require.NoError(t, newTestClient(server.URL).LoadCandidates(t.Context(), job))
			require.Len(t, job.Candidates(), 1)
			if IsTerminal(status) {
				assert.EqualValues(t, 1, details.Load())
				assert.Equal(t, "Exact detail", job.Candidates()[0].Rationale)
				assert.NotNil(t, job.Candidates()[0].Output.SkillResults)
			} else {
				assert.Zero(t, details.Load())
			}
		})
	}
}

func TestSkillCandidateHydrationFailureIsVisible(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/candidate") {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		_, _ = w.Write([]byte(`{"data":[{"candidate_id":"candidate"}]}`))
	}))
	defer server.Close()
	job := &OptimizeJobStatus{ID: "job", Status: StatusSucceeded,
		OptimizationConfiguration: OptimizationConfiguration{Type: SkillOptimizationType}}
	err := newTestClient(server.URL).LoadCandidates(t.Context(), job)
	require.ErrorContains(t, err, `loading skill candidate "candidate"`)
	assert.Empty(t, job.CandidateResults)
}

func TestSkillOptionalArrayPresence(t *testing.T) {
	t.Parallel()
	for _, body := range []string{
		`{"type":"skill_optimization"}`,
		`{"type":"skill_optimization","skill_results":[]}`,
	} {
		var output CandidateOutput
		require.NoError(t, json.Unmarshal([]byte(body), &output))
		actual, err := json.Marshal(output)
		require.NoError(t, err)
		assert.JSONEq(t, body, string(actual))
	}
}
