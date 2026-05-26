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
		runtime.PipelineOptions{},
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
		assert.True(t, strings.HasSuffix(r.URL.Path, "/optimize"))
		assert.Contains(t, r.URL.RawQuery, "api-version=v1")

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
		assert.Contains(t, r.URL.Path, "/optimize/op-123")
		assert.Contains(t, r.URL.RawQuery, "api-version=v1")

		_ = json.NewEncoder(w).Encode(OptimizeJobStatus{
			OperationID: "op-123",
			Status:      StatusCompleted,
			CreatedAt:   "2024-01-01T00:00:00Z",
			UpdatedAt:   "2024-01-01T01:00:00Z",
			Best: &CandidateResult{
				Name:     "candidate-1",
				AvgScore: 0.92,
				PassRate: 0.95,
			},
			Baseline: &CandidateResult{
				Name:     "baseline",
				AvgScore: 0.6,
			},
		})
	}))
	defer server.Close()

	client := newTestClient(server.URL)
	status, err := client.GetOptimizeStatus(context.Background(), "op-123")

	require.NoError(t, err)
	assert.Equal(t, "op-123", status.OperationID)
	assert.Equal(t, StatusCompleted, status.Status)
	require.NotNil(t, status.Best)
	assert.InDelta(t, 0.92, status.Best.AvgScore, 0.001)
	require.NotNil(t, status.Baseline)
	assert.InDelta(t, 0.6, status.Baseline.AvgScore, 0.001)
}

func TestListOptimizeJobs(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodGet, r.Method)
		assert.Contains(t, r.URL.RawQuery, "limit=10")
		assert.Contains(t, r.URL.RawQuery, "status=running")
		assert.Contains(t, r.URL.RawQuery, "api-version=v1")

		_ = json.NewEncoder(w).Encode(OptimizeListResponse{
			Data: []OptimizeJobStatus{
				{OperationID: "op-1", Status: StatusRunning},
				{OperationID: "op-2", Status: StatusRunning},
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
		assert.Contains(t, r.URL.Path, "/optimize/op-xyz/cancel")
		assert.Contains(t, r.URL.RawQuery, "api-version=v1")

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
		assert.Contains(t, r.URL.Path, "/optimize/candidates/cand-42:promote")
		assert.Contains(t, r.URL.RawQuery, "api-version=v1")

		err := json.NewDecoder(r.Body).Decode(&capturedBody)
		assert.NoError(t, err)

		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := newTestClient(server.URL)
	err := client.ReportDeployment(t.Context(), &DeploymentReport{
		CandidateID:  "cand-42",
		AgentName:    "my-agent",
		AgentVersion: "3",
	})

	require.NoError(t, err)
	assert.Equal(t, "my-agent", capturedBody["agentName"])
	assert.Equal(t, "3", capturedBody["agentVersion"])
	// CandidateID should not appear in the body (json:"-")
	assert.Empty(t, capturedBody["candidateId"])
}

func TestReportDeployment_HTTPError(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"code":"BadRequest","message":"invalid candidate"}}`))
	}))
	defer server.Close()

	client := newTestClient(server.URL)
	err := client.ReportDeployment(t.Context(), &DeploymentReport{
		CandidateID:  "bad-id",
		AgentName:    "agent",
		AgentVersion: "1",
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "400")
}
