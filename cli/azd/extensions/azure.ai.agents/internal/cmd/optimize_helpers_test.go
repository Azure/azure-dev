// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"azureaiagent/internal/pkg/agents/optimize_api"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
)

func TestOptimizeConnectionFlags_Resolve_AllEmpty(t *testing.T) {
	t.Setenv("AZURE_AI_OPTIMIZE_ENDPOINT", "")

	f := &optimizeConnectionFlags{}
	_, err := f.resolve(context.Background())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "endpoint")
}

func TestOptimizeConnectionFlags_Resolve_FromEnv(t *testing.T) {
	t.Setenv("AZURE_AI_OPTIMIZE_ENDPOINT", "https://example.com")

	f := &optimizeConnectionFlags{}
	endpoint, err := f.resolve(context.Background())
	assert.NoError(t, err)
	assert.Equal(t, "https://example.com", endpoint)
}

func TestOptimizeConnectionFlags_Resolve_FlagsOverrideEnv(t *testing.T) {
	t.Setenv("AZURE_AI_OPTIMIZE_ENDPOINT", "https://from-env.com")

	f := &optimizeConnectionFlags{
		endpoint: "https://from-flag.com",
	}
	endpoint, err := f.resolve(context.Background())
	assert.NoError(t, err)
	assert.Equal(t, "https://from-flag.com", endpoint)
}

func TestOptimizeConnectionFlags_Resolve_TrimsTrailingSlash(t *testing.T) {
	t.Setenv("AZURE_AI_OPTIMIZE_ENDPOINT", "https://example.com/")

	f := &optimizeConnectionFlags{}
	endpoint, err := f.resolve(context.Background())
	assert.NoError(t, err)
	assert.Equal(t, "https://example.com", endpoint)
}

func TestOptimizeConnectionFlags_Resolve_ProjectEndpointFlag(t *testing.T) {
	f := &optimizeConnectionFlags{
		projectEndpoint: "https://my-project.services.ai.azure.com/",
	}
	endpoint, err := f.resolve(context.Background())
	assert.NoError(t, err)
	assert.Equal(t, "https://my-project.services.ai.azure.com", endpoint)
}

// newOptimizeTestAzdClient creates a test AzdClient backed by a gRPC server
// with the given environment service implementation.
func newOptimizeTestAzdClient(
	t *testing.T,
	envServer azdext.EnvironmentServiceServer,
) *azdext.AzdClient {
	t.Helper()

	grpcServer := grpc.NewServer()
	azdext.RegisterEnvironmentServiceServer(grpcServer, envServer)

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	go func() { _ = grpcServer.Serve(listener) }()

	t.Cleanup(func() {
		grpcServer.Stop()
		_ = listener.Close()
	})

	azdClient, err := azdext.NewAzdClient(azdext.WithAddress(listener.Addr().String()))
	require.NoError(t, err)
	t.Cleanup(func() { azdClient.Close() })

	return azdClient
}

// newTestOptimizeClient creates an OptimizeClient that talks to the given
// httptest server, using a bare pipeline (no auth).
func newTestOptimizeClient(endpoint string) *optimize_api.OptimizeClient {
	pl := runtime.NewPipeline("test", "v0.0.0", runtime.PipelineOptions{}, &policy.ClientOptions{})
	return optimize_api.NewOptimizeClientFromPipeline(endpoint, pl)
}

func TestReportOptimizationDeployments_NoAgents(t *testing.T) {
	t.Parallel()

	envServer := &testEnvironmentServiceServer{
		values: map[string]map[string]string{
			"dev": {},
		},
	}
	azdClient := newOptimizeTestAzdClient(t, envServer)

	// Should complete without calling any API.
	reportOptimizationDeployments(
		t.Context(), azdClient, nil, "dev", "https://unused.example.com",
		newTestOptimizeClient,
	)
}

func TestReportOptimizationDeployments_Success_ClearsCandidate(t *testing.T) {
	t.Parallel()

	envServer := &testEnvironmentServiceServer{
		values: map[string]map[string]string{
			"dev": {
				"AGENT_MY_AGENT_OPTIMIZATION_CANDIDATE_ID": "cand-123",
				"AGENT_MY_AGENT_VERSION":                   "v2",
			},
		},
	}
	azdClient := newOptimizeTestAzdClient(t, envServer)

	var gotURL string
	var gotBody optimize_api.DeploymentReport
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotURL = r.URL.String()
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &gotBody)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	agents := []*azdext.ServiceConfig{{Name: "my-agent"}}

	reportOptimizationDeployments(
		t.Context(), azdClient, agents, "dev", srv.URL,
		newTestOptimizeClient,
	)

	assert.Contains(t, gotURL, "/optimize/candidates/cand-123:promote")
	assert.Equal(t, "my-agent", gotBody.AgentName)
	assert.Equal(t, "v2", gotBody.AgentVersion)
	// CandidateID is json:"-", so it should not appear in the body.
	assert.Empty(t, gotBody.CandidateID)

	// The candidate key should be cleared after successful reporting.
	assert.Equal(t, "", envServer.values["dev"]["AGENT_MY_AGENT_OPTIMIZATION_CANDIDATE_ID"])
}

func TestReportOptimizationDeployments_MissingCandidateID_Skips(t *testing.T) {
	t.Parallel()

	envServer := &testEnvironmentServiceServer{
		values: map[string]map[string]string{
			"dev": {
				// No AGENT_SVC_OPTIMIZATION_CANDIDATE_ID at all.
				"AGENT_SVC_VERSION": "v1",
			},
		},
	}
	azdClient := newOptimizeTestAzdClient(t, envServer)

	apiCalled := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		apiCalled = true
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	agents := []*azdext.ServiceConfig{{Name: "svc"}}
	reportOptimizationDeployments(
		t.Context(), azdClient, agents, "dev", srv.URL,
		newTestOptimizeClient,
	)

	assert.False(t, apiCalled, "API should not be called when candidate ID is missing")
}

func TestReportOptimizationDeployments_MissingVersion_Skips(t *testing.T) {
	t.Parallel()

	envServer := &testEnvironmentServiceServer{
		values: map[string]map[string]string{
			"dev": {
				"AGENT_SVC_OPTIMIZATION_CANDIDATE_ID": "cand-456",
				// No AGENT_SVC_VERSION.
			},
		},
	}
	azdClient := newOptimizeTestAzdClient(t, envServer)

	apiCalled := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		apiCalled = true
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	agents := []*azdext.ServiceConfig{{Name: "svc"}}
	reportOptimizationDeployments(
		t.Context(), azdClient, agents, "dev", srv.URL,
		newTestOptimizeClient,
	)

	assert.False(t, apiCalled, "API should not be called when version is missing")
}

func TestReportOptimizationDeployments_APIFailure_DoesNotClearCandidate(t *testing.T) {
	t.Parallel()

	envServer := &testEnvironmentServiceServer{
		values: map[string]map[string]string{
			"dev": {
				"AGENT_SVC_OPTIMIZATION_CANDIDATE_ID": "cand-789",
				"AGENT_SVC_VERSION":                   "v3",
			},
		},
	}
	azdClient := newOptimizeTestAzdClient(t, envServer)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	agents := []*azdext.ServiceConfig{{Name: "svc"}}
	reportOptimizationDeployments(
		t.Context(), azdClient, agents, "dev", srv.URL,
		newTestOptimizeClient,
	)

	// Candidate key should NOT be cleared when the API returns an error.
	assert.Equal(t, "cand-789", envServer.values["dev"]["AGENT_SVC_OPTIMIZATION_CANDIDATE_ID"])
}

func TestReportOptimizationDeployments_MultipleAgents(t *testing.T) {
	t.Parallel()

	envServer := &testEnvironmentServiceServer{
		values: map[string]map[string]string{
			"dev": {
				"AGENT_ALPHA_OPTIMIZATION_CANDIDATE_ID": "c-a",
				"AGENT_ALPHA_VERSION":                   "v1",
				// beta has no candidate — should be skipped.
				"AGENT_BETA_VERSION": "v2",
				// gamma has candidate but API will fail for it.
				"AGENT_GAMMA_OPTIMIZATION_CANDIDATE_ID": "c-g",
				"AGENT_GAMMA_VERSION":                   "v3",
			},
		},
	}
	azdClient := newOptimizeTestAzdClient(t, envServer)

	promoted := map[string]bool{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/optimize/candidates/c-g:promote" {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		promoted[r.URL.Path] = true
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	agents := []*azdext.ServiceConfig{
		{Name: "alpha"},
		{Name: "beta"},
		{Name: "gamma"},
	}

	reportOptimizationDeployments(
		t.Context(), azdClient, agents, "dev", srv.URL,
		newTestOptimizeClient,
	)

	// Alpha: promoted and cleared.
	assert.True(t, promoted["/optimize/candidates/c-a:promote"])
	assert.Equal(t, "", envServer.values["dev"]["AGENT_ALPHA_OPTIMIZATION_CANDIDATE_ID"])

	// Beta: skipped (no candidate ID), no API call.
	assert.False(t, promoted["/optimize/candidates/:promote"]) // shouldn't appear

	// Gamma: API failed, so candidate key should remain.
	assert.Equal(t, "c-g", envServer.values["dev"]["AGENT_GAMMA_OPTIMIZATION_CANDIDATE_ID"])
}

func TestReportOptimizationDeployments_ServiceNameWithDashes(t *testing.T) {
	t.Parallel()

	envServer := &testEnvironmentServiceServer{
		values: map[string]map[string]string{
			"dev": {
				"AGENT_MY_COOL_AGENT_OPTIMIZATION_CANDIDATE_ID": "cand-dash",
				"AGENT_MY_COOL_AGENT_VERSION":                   "v5",
			},
		},
	}
	azdClient := newOptimizeTestAzdClient(t, envServer)

	var gotURL string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotURL = r.URL.String()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	agents := []*azdext.ServiceConfig{{Name: "my-cool-agent"}}
	reportOptimizationDeployments(
		t.Context(), azdClient, agents, "dev", srv.URL,
		newTestOptimizeClient,
	)

	assert.Contains(t, gotURL, "/optimize/candidates/cand-dash:promote")
	assert.Equal(t, "", envServer.values["dev"]["AGENT_MY_COOL_AGENT_OPTIMIZATION_CANDIDATE_ID"])
}

func TestReportOptimizationDeployments_PanicRecovery(t *testing.T) {
	t.Parallel()

	envServer := &testEnvironmentServiceServer{
		values: map[string]map[string]string{
			"dev": {
				"AGENT_SVC_OPTIMIZATION_CANDIDATE_ID": "cand-panic",
				"AGENT_SVC_VERSION":                   "v1",
			},
		},
	}
	azdClient := newOptimizeTestAzdClient(t, envServer)

	agents := []*azdext.ServiceConfig{{Name: "svc"}}

	// Pass a newClient factory that panics. The recover guard should
	// prevent this from crashing the caller.
	assert.NotPanics(t, func() {
		reportOptimizationDeployments(
			t.Context(), azdClient, agents, "dev", "https://unused",
			func(_ string) *optimize_api.OptimizeClient {
				panic("boom")
			},
		)
	})

	// Candidate key should remain since the promote never succeeded.
	assert.Equal(t, "cand-panic", envServer.values["dev"]["AGENT_SVC_OPTIMIZATION_CANDIDATE_ID"])
}
