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
	t.Setenv("FOUNDRY_PROJECT_ENDPOINT", "")
	t.Setenv("AZURE_AI_PROJECT_ENDPOINT", "")
	t.Setenv("AZD_SERVER", "")
	f := &optimizeConnectionFlags{}
	_, err := f.resolve(t.Context(), "")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "endpoint")
}

func TestOptimizeConnectionFlags_Resolve_FlagEndpoint(t *testing.T) {
	f := &optimizeConnectionFlags{
		endpoint: "https://from-flag.com",
	}
	endpoint, err := f.resolve(context.Background(), "")
	assert.NoError(t, err)
	assert.Equal(t, "https://from-flag.com", endpoint)
}

func TestOptimizeConnectionFlags_Resolve_TrimsTrailingSlash(t *testing.T) {
	f := &optimizeConnectionFlags{
		endpoint: "https://example.com/",
	}
	endpoint, err := f.resolve(context.Background(), "")
	assert.NoError(t, err)
	assert.Equal(t, "https://example.com", endpoint)
}

func TestOptimizeConnectionFlags_Resolve_ProjectEndpointFlag(t *testing.T) {
	f := &optimizeConnectionFlags{
		projectEndpoint: "https://my-project.services.ai.azure.com/",
	}
	endpoint, err := f.resolve(context.Background(), "")
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

// optimizeJobIDKeyForAgent mirrors the AGENT_{KEY}_OPTIMIZATION_CANDIDATE_ID
// naming and applies the same service-key normalization (dashes -> underscores,
// uppercased).
func TestOptimizeJobIDKeyForAgent(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		agent string
		want  string
	}{
		{"simple", "echo", "AGENT_ECHO_OPTIMIZATION_JOB_ID"},
		{"dashes", "my-cool-agent", "AGENT_MY_COOL_AGENT_OPTIMIZATION_JOB_ID"},
		{"already upper", "BOT", "AGENT_BOT_OPTIMIZATION_JOB_ID"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, optimizeJobIDKeyForAgent(tt.agent))
		})
	}
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
				"OPTIMIZE_LAST_OPERATION_ID":               "opt-1",
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

	assert.Contains(t, gotURL, "/agent_optimization_jobs/opt-1/candidates/cand-123:promote")
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
				"OPTIMIZE_LAST_OPERATION_ID":          "opt-1",
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
				"OPTIMIZE_LAST_OPERATION_ID":            "opt-1",
			},
		},
	}
	azdClient := newOptimizeTestAzdClient(t, envServer)

	promoted := map[string]bool{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/agent_optimization_jobs/opt-1/candidates/c-g:promote" {
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
	assert.True(t, promoted["/agent_optimization_jobs/opt-1/candidates/c-a:promote"])
	assert.Equal(t, "", envServer.values["dev"]["AGENT_ALPHA_OPTIMIZATION_CANDIDATE_ID"])

	// Beta: skipped (no candidate ID), no API call.
	assert.False(t, promoted["/agent_optimization_jobs/opt-1/candidates/:promote"]) // shouldn't appear

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
				"OPTIMIZE_LAST_OPERATION_ID":                    "opt-1",
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

	assert.Contains(t, gotURL, "/agent_optimization_jobs/opt-1/candidates/cand-dash:promote")
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

func TestOptimizeConnectionFlags_Resolve_FoundryEnvVar(t *testing.T) {
	t.Setenv("AZD_SERVER", "")
	t.Setenv("FOUNDRY_PROJECT_ENDPOINT", "https://foundry.example.com/")
	f := &optimizeConnectionFlags{}
	endpoint, err := f.resolve(t.Context(), "")
	assert.NoError(t, err)
	assert.Equal(t, "https://foundry.example.com", endpoint)
}

func TestOptimizeConnectionFlags_Resolve_AzureAIEnvVar(t *testing.T) {
	t.Setenv("AZD_SERVER", "")
	t.Setenv("FOUNDRY_PROJECT_ENDPOINT", "")
	t.Setenv("AZURE_AI_PROJECT_ENDPOINT", "https://azure-ai.example.com/")
	f := &optimizeConnectionFlags{}
	endpoint, err := f.resolve(t.Context(), "")
	assert.NoError(t, err)
	assert.Equal(t, "https://azure-ai.example.com", endpoint)
}

func TestOptimizeConnectionFlags_Resolve_FoundryTakesPriorityOverAzureAI(t *testing.T) {
	t.Setenv("AZD_SERVER", "")
	t.Setenv("FOUNDRY_PROJECT_ENDPOINT", "https://foundry.example.com")
	t.Setenv("AZURE_AI_PROJECT_ENDPOINT", "https://azure-ai.example.com")
	f := &optimizeConnectionFlags{}
	endpoint, err := f.resolve(t.Context(), "")
	assert.NoError(t, err)
	assert.Equal(t, "https://foundry.example.com", endpoint)
}

func TestResolveProjectEndpointForDeploy_FoundryEnvVar(t *testing.T) {
	t.Setenv("AZD_SERVER", "")
	t.Setenv("FOUNDRY_PROJECT_ENDPOINT", "https://foundry-deploy.example.com/")
	ep, err := resolveProjectEndpointForDeploy(t.Context(), &optimizeConnectionFlags{}, "")
	assert.NoError(t, err)
	assert.Equal(t, "https://foundry-deploy.example.com", ep)
}

func TestResolveProjectEndpointForDeploy_AzureAIEnvVar(t *testing.T) {
	t.Setenv("AZD_SERVER", "")
	t.Setenv("FOUNDRY_PROJECT_ENDPOINT", "")
	t.Setenv("AZURE_AI_PROJECT_ENDPOINT", "https://azure-ai-deploy.example.com/")
	ep, err := resolveProjectEndpointForDeploy(t.Context(), &optimizeConnectionFlags{}, "")
	assert.NoError(t, err)
	assert.Equal(t, "https://azure-ai-deploy.example.com", ep)
}

func TestResolveProjectEndpointForDeploy_FoundryTakesPriorityOverAzureAI(t *testing.T) {
	t.Setenv("AZD_SERVER", "")
	t.Setenv("FOUNDRY_PROJECT_ENDPOINT", "https://foundry-deploy.example.com")
	t.Setenv("AZURE_AI_PROJECT_ENDPOINT", "https://azure-ai-deploy.example.com")
	ep, err := resolveProjectEndpointForDeploy(t.Context(), &optimizeConnectionFlags{}, "")
	assert.NoError(t, err)
	assert.Equal(t, "https://foundry-deploy.example.com", ep)
}

// ---------------------------------------------------------------------------
// getExistingEnvironment
// ---------------------------------------------------------------------------

func TestGetExistingEnvironment_EmptyName_UsesCurrent(t *testing.T) {
	t.Parallel()
	envServer := &testEnvironmentServiceServer{
		current: &azdext.Environment{Name: "dev"},
	}
	azdClient := newOptimizeTestAzdClient(t, envServer)
	env := getExistingEnvironment(t.Context(), "", azdClient)
	require.NotNil(t, env)
	assert.Equal(t, "dev", env.Name)
}

func TestGetExistingEnvironment_ExplicitName_UsesGet(t *testing.T) {
	t.Parallel()
	envServer := &testEnvironmentServiceServer{
		current: &azdext.Environment{Name: "dev"},
		environments: map[string]*azdext.Environment{
			"staging": {Name: "staging"},
		},
	}
	azdClient := newOptimizeTestAzdClient(t, envServer)
	env := getExistingEnvironment(t.Context(), "staging", azdClient)
	require.NotNil(t, env)
	assert.Equal(t, "staging", env.Name)
}

func TestGetExistingEnvironment_NotFound_ReturnsNil(t *testing.T) {
	t.Parallel()
	envServer := &testEnvironmentServiceServer{
		environments: map[string]*azdext.Environment{},
	}
	azdClient := newOptimizeTestAzdClient(t, envServer)
	env := getExistingEnvironment(t.Context(), "nonexistent", azdClient)
	assert.Nil(t, env)
}

// ---------------------------------------------------------------------------
// candidateDisplayName
// ---------------------------------------------------------------------------

func TestCandidateDisplayName(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "baseline", candidateDisplayName("baseline", false))
	assert.Equal(t, "candidate_1 ★", candidateDisplayName("candidate_1", true))
}

// ---------------------------------------------------------------------------
// candidateTableHeader
// ---------------------------------------------------------------------------

func TestCandidateTableHeader(t *testing.T) {
	t.Parallel()

	t.Run("both columns", func(t *testing.T) {
		t.Parallel()
		header, sep := candidateTableHeader(true, true)
		assert.Contains(t, header, "Candidate")
		assert.Contains(t, header, "Score")
		assert.Contains(t, header, "Eval")
		assert.Contains(t, header, "Strategy")
		assert.Contains(t, sep, "─")
	})

	t.Run("no eval no strategy", func(t *testing.T) {
		t.Parallel()
		header, _ := candidateTableHeader(false, false)
		assert.NotContains(t, header, "Eval")
		assert.NotContains(t, header, "Strategy")
	})
}

// ---------------------------------------------------------------------------
// formatCandidateRow — dash placeholders for empty cells
// ---------------------------------------------------------------------------

func TestFormatCandidateRow_EmptyEvalShowsDash(t *testing.T) {
	t.Parallel()
	row := formatCandidateRow("candidate_1", 0.95, "", nil, true, false)
	assert.Contains(t, row, "-")
	assert.Contains(t, row, "0.950")
}

func TestFormatCandidateRow_NonEmptyEvalPreserved(t *testing.T) {
	t.Parallel()
	row := formatCandidateRow("candidate_1", 0.95, "View", nil, true, false)
	assert.Contains(t, row, "View")
}

func TestFormatCandidateRow_EmptyStrategyShowsDash(t *testing.T) {
	t.Parallel()
	row := formatCandidateRow("candidate_1", 0.90, "", nil, false, true)
	assert.Contains(t, row, "-")
}

func TestFormatCandidateRow_NonEmptyStrategyPreserved(t *testing.T) {
	t.Parallel()
	row := formatCandidateRow("candidate_1", 0.90, "", []string{"skills", "system_prompt"}, false, true)
	assert.Contains(t, row, "skills")
	assert.Contains(t, row, "system_prompt")
}

func TestFormatCandidateRow_BothColumnsEmpty(t *testing.T) {
	t.Parallel()
	row := formatCandidateRow("baseline", 0.80, "", nil, true, true)
	// Both eval and strategy should show dashes.
	assert.Contains(t, row, "baseline")
	assert.Contains(t, row, "0.800")
	// There should be at least two "-" characters (one for each empty column).
	count := 0
	for _, c := range row {
		if c == '-' {
			count++
		}
	}
	assert.GreaterOrEqual(t, count, 2, "both empty columns should show dash placeholders")
}

func TestFormatCandidateRow_HiddenColumnsOmitted(t *testing.T) {
	t.Parallel()
	row := formatCandidateRow("candidate_1", 0.95, "View", []string{"skills"}, false, false)
	// Neither eval nor strategy should appear when both are hidden.
	assert.NotContains(t, row, "View")
	assert.NotContains(t, row, "skills")
}
