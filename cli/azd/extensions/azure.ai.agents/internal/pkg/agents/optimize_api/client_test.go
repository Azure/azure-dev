// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package optimize_api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newTestClient builds an OptimizeClient that talks to the given httptest server
// with no auth (bare pipeline).
func newTestClient(serverURL string) *OptimizeClient {
	pipeline := runtime.NewPipeline(
		"test",
		"v0.0.0",
		runtime.PipelineOptions{
			PerCall: []policy.Policy{foundryFeaturesPolicy{}},
		},
		&policy.ClientOptions{},
	)
	return &OptimizeClient{
		endpoint: serverURL,
		pipeline: pipeline,
	}
}

// stubCredential satisfies azcore.TokenCredential for constructor tests.
type stubCredential struct{}

func (stubCredential) GetToken(_ context.Context, _ policy.TokenRequestOptions) (azcore.AccessToken, error) {
	return azcore.AccessToken{Token: "stub"}, nil
}

func TestNewOptimizeClient(t *testing.T) {
	t.Parallel()
	client := NewOptimizeClient("https://example.com", stubCredential{})
	require.NotNil(t, client)
	assert.Equal(t, "https://example.com", client.endpoint)
}

func TestStartOptimize(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.True(t, strings.HasSuffix(r.URL.Path, "/agent_optimization_jobs"))
		assert.Contains(t, r.URL.RawQuery, "api-version="+APIVersion)

		w.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(w).Encode(OptimizeResponse{
			OperationID: "op-abc",
			Status:      StatusQueued,
		})
	}))
	defer server.Close()

	client := newTestClient(server.URL)
	resp, err := client.StartOptimize(context.Background(), &OptimizeRequest{
		Agent: AgentIdentifier{
			AgentName: "agent-1",
		},
		Options: OptimizeOptions{EvalModel: "gpt-4o-mini"},
	})

	require.NoError(t, err)
	assert.Equal(t, "op-abc", resp.OperationID)
	assert.Equal(t, StatusQueued, resp.Status)
}

func TestGetOptimizeStatus(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodGet, r.Method)
		assert.Contains(t, r.URL.Path, "/agent_optimization_jobs/op-123")
		assert.Contains(t, r.URL.RawQuery, "api-version="+APIVersion)
		assert.Equal(t, "AgentsOptimization=V2Preview", r.Header.Get("Foundry-Features"))

		_ = json.NewEncoder(w).Encode(OptimizeJobStatus{
			ID:        "op-123",
			Status:    StatusCompleted,
			CreatedAt: 1781036157,
			UpdatedAt: 1781037526,
			Result: &OptimizeResult{
				Best:     "cand-1",
				Baseline: "cand-0",
				Candidates: []CandidateResult{
					{Name: "candidate-1", CandidateID: "cand-1", AvgScore: 0.92},
					{Name: "baseline", CandidateID: "cand-0", AvgScore: 0.6},
				},
			},
		})
	}))
	defer server.Close()

	client := newTestClient(server.URL)
	status, err := client.GetOptimizeStatus(context.Background(), "op-123")

	require.NoError(t, err)
	assert.Equal(t, "op-123", status.ID)
	assert.Equal(t, StatusCompleted, status.Status)
	require.NotNil(t, status.BestCandidate())
	assert.InDelta(t, 0.92, status.BestCandidate().AvgScore, 0.001)
	require.NotNil(t, status.BaselineCandidate())
	assert.InDelta(t, 0.6, status.BaselineCandidate().AvgScore, 0.001)
}

func TestListOptimizeJobs(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodGet, r.Method)
		assert.Contains(t, r.URL.RawQuery, "limit=10")
		assert.Contains(t, r.URL.RawQuery, "status=running")
		assert.Contains(t, r.URL.RawQuery, "api-version="+APIVersion)

		_ = json.NewEncoder(w).Encode(OptimizeListResponse{
			Data: []OptimizeJobStatus{
				{ID: "op-1", Status: StatusRunning},
				{ID: "op-2", Status: StatusRunning},
			},
			FirstID: "op-1",
			LastID:  "op-2",
			HasMore: false,
		})
	}))
	defer server.Close()

	client := newTestClient(server.URL)
	resp, err := client.ListOptimizeJobs(context.Background(), 10, "running")

	require.NoError(t, err)
	assert.Len(t, resp.Data, 2)
	assert.Equal(t, "op-1", resp.FirstID)
	assert.Equal(t, "op-2", resp.LastID)
	assert.False(t, resp.HasMore)
}

func TestCancelOptimize(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Contains(t, r.URL.Path, "/agent_optimization_jobs/op-xyz:cancel")
		assert.Contains(t, r.URL.RawQuery, "api-version="+APIVersion)

		_ = json.NewEncoder(w).Encode(OptimizeCancelResponse{
			OperationID: "op-xyz",
			Status:      StatusCancelled,
		})
	}))
	defer server.Close()

	client := newTestClient(server.URL)
	resp, err := client.CancelOptimize(context.Background(), "op-xyz")

	require.NoError(t, err)
	assert.Equal(t, "op-xyz", resp.OperationID)
	assert.Equal(t, StatusCancelled, resp.Status)
}

func TestStartOptimize_HTTPError(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error": {"code": "BadRequest", "message": "invalid payload"}}`))
	}))
	defer server.Close()

	client := newTestClient(server.URL)
	resp, err := client.StartOptimize(context.Background(), &OptimizeRequest{})

	assert.Nil(t, resp)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "400")
}

func TestGetOptimizeStatus_HTTPError(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error": {"code": "NotFound", "message": "job not found"}}`))
	}))
	defer server.Close()

	client := newTestClient(server.URL)
	resp, err := client.GetOptimizeStatus(context.Background(), "nonexistent")

	assert.Nil(t, resp)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "404")
}

func TestListOptimizeJobs_NoStatusFilter(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.NotContains(t, r.URL.RawQuery, "status=")
		_ = json.NewEncoder(w).Encode(OptimizeListResponse{
			Data: []OptimizeJobStatus{},
		})
	}))
	defer server.Close()

	client := newTestClient(server.URL)
	resp, err := client.ListOptimizeJobs(context.Background(), 20, "")

	require.NoError(t, err)
	assert.Empty(t, resp.Data)
}

func TestReportDeployment(t *testing.T) {
	t.Parallel()

	var capturedBody map[string]string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Contains(t, r.URL.Path, "/agent_optimization_jobs/opt-1/candidates/cand-42:promote")
		assert.Contains(t, r.URL.RawQuery, "api-version="+APIVersion)

		err := json.NewDecoder(r.Body).Decode(&capturedBody)
		assert.NoError(t, err)

		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := newTestClient(server.URL)
	err := client.ReportDeployment(t.Context(), "opt-1", &DeploymentReport{
		CandidateID:  "cand-42",
		AgentName:    "my-agent",
		AgentVersion: "3",
	})

	require.NoError(t, err)
	assert.Equal(t, "my-agent", capturedBody["agent_name"])
	assert.Equal(t, "3", capturedBody["agent_version"])
	// CandidateID should not appear in the body (json:"-")
	assert.Empty(t, capturedBody["candidate_id"])
}

func TestReportDeployment_HTTPError(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"code":"BadRequest","message":"invalid candidate"}}`))
	}))
	defer server.Close()

	client := newTestClient(server.URL)
	err := client.ReportDeployment(t.Context(), "opt-1", &DeploymentReport{
		CandidateID:  "bad-id",
		AgentName:    "agent",
		AgentVersion: "1",
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "400")
}

// ---------------------------------------------------------------------------
// Candidate endpoints — nested under agent_optimization_jobs/{jobId}
// ---------------------------------------------------------------------------

func TestGetCandidateConfig(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodGet, r.Method)
		assert.Contains(t, r.URL.Path, "/agent_optimization_jobs/opt-1/candidates/cand-9/config")
		assert.Contains(t, r.URL.RawQuery, "api-version="+APIVersion)

		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"system_prompt":"hello","model":"gpt-4o"}`))
	}))
	defer server.Close()

	client := newTestClient(server.URL)
	cfg, err := client.GetCandidateConfig(context.Background(), "opt-1", "cand-9")

	require.NoError(t, err)
	assert.JSONEq(t, `{"system_prompt":"hello","model":"gpt-4o"}`, string(cfg))
}

func TestGetCandidateConfig_InvalidJSON(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`not-json`))
	}))
	defer server.Close()

	client := newTestClient(server.URL)
	_, err := client.GetCandidateConfig(context.Background(), "opt-1", "cand-9")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "not valid JSON")
}

func TestGetCandidate(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodGet, r.Method)
		assert.Contains(t, r.URL.Path, "/agent_optimization_jobs/opt-1/candidates/cand-9")
		assert.Contains(t, r.URL.RawQuery, "api-version="+APIVersion)

		_ = json.NewEncoder(w).Encode(CandidateManifest{
			Files: []CandidateFile{
				{Path: "skills/foo/SKILL.md", Type: "skill"},
				{Path: "tools.json", Type: "tools"},
			},
		})
	}))
	defer server.Close()

	client := newTestClient(server.URL)
	manifest, err := client.GetCandidate(context.Background(), "opt-1", "cand-9")

	require.NoError(t, err)
	require.Len(t, manifest.Files, 2)
	assert.Equal(t, "skills/foo/SKILL.md", manifest.Files[0].Path)
	assert.Equal(t, "skill", manifest.Files[0].Type)
}

func TestGetCandidateFile(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodGet, r.Method)
		assert.Contains(t, r.URL.Path, "/agent_optimization_jobs/opt-1/candidates/cand-9/files")
		assert.Equal(t, "skills/foo/SKILL.md", r.URL.Query().Get("path"))
		assert.Contains(t, r.URL.RawQuery, "api-version="+APIVersion)

		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("# Skill content"))
	}))
	defer server.Close()

	client := newTestClient(server.URL)
	content, err := client.GetCandidateFile(context.Background(), "opt-1", "cand-9", "skills/foo/SKILL.md")

	require.NoError(t, err)
	assert.Equal(t, "# Skill content", content)
}
