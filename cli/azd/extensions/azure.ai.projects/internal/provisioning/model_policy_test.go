// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// cspell:ignore nextgen noncompliant

package provisioning

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"azure.ai.projects/internal/exterrors"
	"azure.ai.projects/synthesis"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testPolicyProjectID = "/subscriptions/00000000-0000-0000-0000-000000000001/resourceGroups/rg-foundry/" +
	"providers/Microsoft.CognitiveServices/accounts/my-account/projects/my-project"

type evaluateDeploymentPoliciesRequest struct {
	Deployments []struct {
		Name       string `json:"name"`
		Properties struct {
			Model struct {
				Format  string `json:"format"`
				Name    string `json:"name"`
				Version string `json:"version"`
			} `json:"model"`
		} `json:"properties"`
	} `json:"deployments"`
}

func TestEvaluateProjectModelDeploymentsPolicy_DenyBlocksAndAuditAllows(t *testing.T) {
	const (
		denyAssignmentID = "/subscriptions/00000000-0000-0000-0000-000000000001/providers/" +
			"Microsoft.Authorization/policyAssignments/approved-models-only"
		auditAssignmentID = "/subscriptions/00000000-0000-0000-0000-000000000001/providers/" +
			"Microsoft.Authorization/policyAssignments/audit-models-only"
	)

	var postBody evaluateDeploymentPoliciesRequest
	var postCount int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&postCount, 1)
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(
			t,
			"/subscriptions/00000000-0000-0000-0000-000000000001/resourceGroups/rg-foundry/"+
				"providers/Microsoft.CognitiveServices/accounts/my-account/evaluateDeploymentPolicies",
			r.URL.Path,
		)
		assert.Equal(t, evaluateDeploymentPoliciesAPIVersion, r.URL.Query().Get("api-version"))
		require.NoError(t, json.NewDecoder(r.Body).Decode(&postBody))

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"results": map[string]any{
				"chat-denied": map[string]any{
					"evaluationOutcome": "NonCompliant",
					"errorMessage":      "Disallowed by policy with secret=do-not-leak",
					"nonCompliantAssignments": []map[string]any{
						{
							"assignmentId": denyAssignmentID,
							"effect":       "Deny",
						},
					},
				},
				"chat-audited": map[string]any{
					"evaluationOutcome": "NonCompliant",
					"nonCompliantAssignments": []map[string]any{
						{
							"assignmentId": auditAssignmentID,
							"effect":       "Audit",
						},
					},
				},
				"chat-compliant": map[string]any{
					"evaluationOutcome":       "Compliant",
					"nonCompliantAssignments": []map[string]any{},
				},
			},
		})
	}))
	defer server.Close()

	prevEndpoint := modelPolicyARMEndpoint
	prevPipeline := newModelPolicyPipeline
	modelPolicyARMEndpoint = server.URL
	newModelPolicyPipeline = func(_ azcore.TokenCredential) runtime.Pipeline {
		return runtime.NewPipeline(
			"test",
			"v1",
			runtime.PipelineOptions{},
			&policy.ClientOptions{Transport: server.Client()},
		)
	}
	t.Cleanup(func() {
		modelPolicyARMEndpoint = prevEndpoint
		newModelPolicyPipeline = prevPipeline
	})

	deployments := []synthesis.Deployment{
		{
			Name:  "chat-denied",
			Model: synthesis.DeploymentModel{Format: "OpenAI", Name: "gpt-4o", Version: "2024-08-06"},
		},
		{
			Name:  "CHAT-DENIED", // duplicate case-insensitive name should be deduplicated
			Model: synthesis.DeploymentModel{Format: "OpenAI", Name: "gpt-4o", Version: "2024-08-06"},
		},
		{
			Name:  "chat-audited",
			Model: synthesis.DeploymentModel{Format: "OpenAI", Name: "gpt-4.1-mini", Version: "2025-04-14"},
		},
		{
			Name:  "chat-compliant",
			Model: synthesis.DeploymentModel{Format: "OpenAI", Name: "gpt-4.1", Version: "2025-04-14"},
		},
	}

	violations, err := EvaluateProjectModelDeploymentsPolicy(
		t.Context(),
		monitoringTestCredential{},
		testPolicyProjectID,
		deployments,
	)
	require.NoError(t, err)
	assert.Equal(t, int32(1), atomic.LoadInt32(&postCount))
	require.Len(t, postBody.Deployments, 3)

	require.Len(t, violations, 1)
	assert.Equal(t, "chat-denied", violations[0].DeploymentName)
	assert.Equal(t, "approved-models-only", violations[0].DisplayName)
	assert.Equal(
		t,
		`The "approved-models-only" policy doesn't allow model "gpt-4o (version 2024-08-06)" for deployment "chat-denied" in this project.`,
		violations[0].Message,
	)
	assert.NotContains(t, violations[0].Message, "do-not-leak")
	assert.Contains(t, violations[0].PortalURL, "/manage/govern/assignments/")

	checkErr := CheckProjectModelDeploymentsPolicy(
		t.Context(),
		monitoringTestCredential{},
		testPolicyProjectID,
		deployments,
		nil,
	)
	require.Error(t, checkErr)
	localErr, ok := errors.AsType[*azdext.LocalError](checkErr)
	require.True(t, ok)
	assert.Equal(t, exterrors.CodeModelBlockedByPolicy, localErr.Code)
	assert.Contains(t, localErr.Message, violations[0].Message)
	assert.Contains(t, localErr.Suggestion, "View policy (approved-models-only):")
}

func TestEvaluateProjectModelDeploymentsPolicy_ResolvesMissingMetadataViaGetDeployment(t *testing.T) {
	var getCalled, postCalled bool
	var postBody evaluateDeploymentPoliciesRequest

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet &&
			strings.HasSuffix(r.URL.Path, "/accounts/my-account/deployments/existing-chat"):
			getCalled = true
			assert.Equal(t, cognitiveServicesDeploymentAPIVersion, r.URL.Query().Get("api-version"))
			_ = json.NewEncoder(w).Encode(map[string]any{
				"properties": map[string]any{
					"model": map[string]any{
						"format":  "OpenAI",
						"name":    "gpt-4o",
						"version": "2024-11-20",
					},
				},
			})
		case r.Method == http.MethodGet &&
			strings.HasSuffix(r.URL.Path, "/accounts/my-account/deployments/mismatched-chat"):
			_ = json.NewEncoder(w).Encode(map[string]any{
				"properties": map[string]any{
					"model": map[string]any{
						"format":  "OpenAI",
						"name":    "gpt-4o",
						"version": "2024-08-06",
					},
				},
			})
		case r.Method == http.MethodPost &&
			strings.HasSuffix(r.URL.Path, "/accounts/my-account/evaluateDeploymentPolicies"):
			postCalled = true
			require.NoError(t, json.NewDecoder(r.Body).Decode(&postBody))
			_ = json.NewEncoder(w).Encode(map[string]any{
				"results": map[string]any{
					"existing-chat": map[string]any{
						"evaluationOutcome":       "Compliant",
						"nonCompliantAssignments": []map[string]any{},
					},
				},
			})
		default:
			t.Fatalf("unexpected HTTP request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	prevEndpoint := modelPolicyARMEndpoint
	prevPipeline := newModelPolicyPipeline
	modelPolicyARMEndpoint = server.URL
	newModelPolicyPipeline = func(_ azcore.TokenCredential) runtime.Pipeline {
		return runtime.NewPipeline(
			"test",
			"v1",
			runtime.PipelineOptions{},
			&policy.ClientOptions{Transport: server.Client()},
		)
	}
	t.Cleanup(func() {
		modelPolicyARMEndpoint = prevEndpoint
		newModelPolicyPipeline = prevPipeline
	})

	violations, err := EvaluateProjectModelDeploymentsPolicy(
		t.Context(),
		monitoringTestCredential{},
		testPolicyProjectID,
		[]synthesis.Deployment{
			{
				Name:  "existing-chat",
				Model: synthesis.DeploymentModel{Name: "gpt-4o"}, // missing Format and Version
			},
			{
				Name:  "mismatched-chat",
				Model: synthesis.DeploymentModel{Name: "gpt-4.1"}, // different model name than live deployment
			},
		},
	)
	require.NoError(t, err)
	assert.Empty(t, violations)
	assert.True(t, getCalled)
	assert.True(t, postCalled)
	require.Len(t, postBody.Deployments, 1)
	assert.Equal(t, "existing-chat", postBody.Deployments[0].Name)
	assert.Equal(t, "OpenAI", postBody.Deployments[0].Properties.Model.Format)
	assert.Equal(t, "gpt-4o", postBody.Deployments[0].Properties.Model.Name)
	assert.Equal(t, "2024-11-20", postBody.Deployments[0].Properties.Model.Version)
}

func TestParseFoundryProjectResourceIDAndPortalURL(t *testing.T) {
	var httpCalls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&httpCalls, 1)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	prevEndpoint := modelPolicyARMEndpoint
	prevPipeline := newModelPolicyPipeline
	modelPolicyARMEndpoint = server.URL
	newModelPolicyPipeline = func(_ azcore.TokenCredential) runtime.Pipeline {
		return runtime.NewPipeline(
			"test",
			"v1",
			runtime.PipelineOptions{},
			&policy.ClientOptions{Transport: server.Client()},
		)
	}
	t.Cleanup(func() {
		modelPolicyARMEndpoint = prevEndpoint
		newModelPolicyPipeline = prevPipeline
	})

	for _, invalidID := range []string{
		"@attacker.example/subscriptions/00000000-0000-0000-0000-000000000001/resourceGroups/rg/" +
			"providers/Microsoft.CognitiveServices/accounts/acc/projects/proj",
		"https://attacker.example/subscriptions/00000000-0000-0000-0000-000000000001/resourceGroups/rg/" +
			"providers/Microsoft.CognitiveServices/accounts/acc/projects/proj",
		"/subscriptions/not-a-uuid/resourceGroups/rg/providers/Microsoft.CognitiveServices/accounts/acc/projects/proj",
		"/subscriptions/00000000-0000-0000-0000-000000000001/resourceGroups/rg/" +
			"providers/Microsoft.Other/accounts/acc/projects/proj",
		"/subscriptions/00000000-0000-0000-0000-000000000001/resourceGroups/rg/" +
			"providers/Microsoft.CognitiveServices/accounts/acc/projects/proj/extra",
		testPolicyProjectID + "//",
	} {
		_, _, _, _, ok := ParseFoundryProjectResourceID(invalidID)
		assert.False(t, ok, "expected invalid project ID: %s", invalidID)

		_, err := EvaluateProjectModelDeploymentsPolicy(
			t.Context(),
			monitoringTestCredential{},
			invalidID,
			[]synthesis.Deployment{
				{
					Name:  "chat",
					Model: synthesis.DeploymentModel{Format: "OpenAI", Name: "gpt-4.1", Version: "2025-04-14"},
				},
			},
		)
		require.Error(t, err)
	}
	assert.Zero(t, atomic.LoadInt32(&httpCalls), "invalid project IDs must make 0 HTTP calls")

	scopes := []struct {
		name         string
		assignmentID string
	}{
		{
			name: "subscription scope",
			assignmentID: "/subscriptions/00000000-0000-0000-0000-000000000001/" +
				"providers/Microsoft.Authorization/policyAssignments/sub-policy",
		},
		{
			name: "resource group scope",
			assignmentID: "/subscriptions/00000000-0000-0000-0000-000000000001/resourceGroups/rg-foundry/" +
				"providers/Microsoft.Authorization/policyAssignments/rg-policy",
		},
		{
			name: "account scope",
			assignmentID: "/subscriptions/00000000-0000-0000-0000-000000000001/resourceGroups/rg-foundry/" +
				"providers/Microsoft.CognitiveServices/accounts/my-account/" +
				"providers/Microsoft.Authorization/policyAssignments/account-policy",
		},
		{
			name: "project scope",
			assignmentID: testPolicyProjectID +
				"/providers/Microsoft.Authorization/policyAssignments/project-policy",
		},
		{
			name: "management group scope",
			assignmentID: "/providers/Microsoft.Management/managementGroups/corp-mg/" +
				"providers/Microsoft.Authorization/policyAssignments/mg-policy",
		},
	}

	for _, tc := range scopes {
		t.Run(tc.name, func(t *testing.T) {
			portalURL := BuildModelPolicyAssignmentPortalURL(testPolicyProjectID, tc.assignmentID)
			assert.Contains(
				t,
				portalURL,
				"https://ai.azure.com/nextgen/r/AAAAAAAAAAAAAAAAAAAAAQ%2Crg-foundry%2C%2Cmy-account%2Cmy-project/manage/govern/assignments/",
			)
			assert.Contains(t, portalURL, "flight=govern_ignite")
			assert.Contains(t, portalURL, "subscriptionId=00000000-0000-0000-0000-000000000001")
		})
	}

	assert.Equal(
		t,
		defaultFoundryGovernPortalURL,
		BuildModelPolicyAssignmentPortalURL(testPolicyProjectID, "invalid-assignment-id"),
	)

	invalidAssignments := []struct {
		name         string
		assignmentID string
	}{
		{
			name:         "non-UUID subscription ID",
			assignmentID: "/subscriptions/not-a-uuid/providers/Microsoft.Authorization/policyAssignments/deny",
		},
		{
			name: "malformed intermediate scope",
			assignmentID: "/subscriptions/1756abc0-3554-4341-8d6a-46674962ea19/badSegment/foo/" +
				"providers/Microsoft.Authorization/policyAssignments/deny",
		},
		{
			name: "malformed segment between resourceGroups and providers",
			assignmentID: "/subscriptions/1756abc0-3554-4341-8d6a-46674962ea19/resourceGroups/rg/badSegment/foo/" +
				"providers/Microsoft.CognitiveServices/accounts/account/" +
				"providers/Microsoft.Authorization/policyAssignments/deny",
		},
		{
			name: "empty path segment // in assignment ID",
			assignmentID: "/subscriptions/1756abc0-3554-4341-8d6a-46674962ea19/resourceGroups/rg//" +
				"providers/Microsoft.CognitiveServices/accounts/account/" +
				"providers/Microsoft.Authorization/policyAssignments/deny",
		},
		{
			name: "forbidden @ character",
			assignmentID: "/subscriptions/1756abc0-3554-4341-8d6a-46674962ea19/providers/" +
				"Microsoft.Authorization/policyAssignments/deny@evil",
		},
		{
			name: "forbidden ? query character",
			assignmentID: "/subscriptions/1756abc0-3554-4341-8d6a-46674962ea19/providers/" +
				"Microsoft.Authorization/policyAssignments/deny?foo=bar",
		},
		{
			name: "forbidden # fragment character",
			assignmentID: "/subscriptions/1756abc0-3554-4341-8d6a-46674962ea19/providers/" +
				"Microsoft.Authorization/policyAssignments/deny#frag",
		},
		{
			name: "forbidden whitespace character",
			assignmentID: "/subscriptions/1756abc0-3554-4341-8d6a-46674962ea19/providers/" +
				"Microsoft.Authorization/policyAssignments/deny policy",
		},
		{
			name: "trailing double slash // in assignment ID",
			assignmentID: "/subscriptions/1756abc0-3554-4341-8d6a-46674962ea19/providers/" +
				"Microsoft.Authorization/policyAssignments/deny//",
		},
	}

	for _, tc := range invalidAssignments {
		t.Run(tc.name, func(t *testing.T) {
			assert.False(t, isValidModelPolicyAssignmentID(tc.assignmentID))
			assert.Equal(
				t,
				defaultFoundryGovernPortalURL,
				BuildModelPolicyAssignmentPortalURL(testPolicyProjectID, tc.assignmentID),
			)
		})
	}
}

func TestEvaluateModelDeploymentsWithPipeline_InvalidAssignmentIDFallsBackToGeneric(t *testing.T) {
	invalidAssignmentIDs := []string{
		"/subscriptions/not-a-uuid/providers/Microsoft.Authorization/policyAssignments/deny",
		"/subscriptions/1756abc0-3554-4341-8d6a-46674962ea19/badSegment/foo/" +
			"providers/Microsoft.Authorization/policyAssignments/deny",
		"/subscriptions/1756abc0-3554-4341-8d6a-46674962ea19/providers/" +
			"Microsoft.Authorization/policyAssignments/deny?evil=1",
	}

	for _, badID := range invalidAssignmentIDs {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"results": map[string]any{
					"chat": map[string]any{
						"evaluationOutcome": "NonCompliant",
						"nonCompliantAssignments": []map[string]any{
							{
								"assignmentId": badID,
								"effect":       "Deny",
							},
						},
					},
				},
			})
		}))

		pipeline := runtime.NewPipeline(
			"test",
			"v1",
			runtime.PipelineOptions{},
			&policy.ClientOptions{Transport: server.Client()},
		)
		violations, err := evaluateModelDeploymentsWithPipeline(
			t.Context(),
			pipeline,
			server.URL,
			testPolicyProjectID,
			"00000000-0000-0000-0000-000000000001",
			"rg-foundry",
			"my-account",
			[]synthesis.Deployment{
				{
					Name:  "chat",
					Model: synthesis.DeploymentModel{Format: "OpenAI", Name: "gpt-4o", Version: "2024-08-06"},
				},
			},
		)
		server.Close()

		require.NoError(t, err)
		require.Len(t, violations, 1)
		assert.Equal(t, "Azure Policy", violations[0].DisplayName)
		assert.Empty(t, violations[0].AssignmentID)
		assert.Equal(t, defaultFoundryGovernPortalURL, violations[0].PortalURL)
		assert.Contains(
			t,
			violations[0].Message,
			`The "Azure Policy" policy doesn't allow model "gpt-4o (version 2024-08-06)" for deployment "chat" in this project.`,
		)
	}
}

func TestEvaluateModelDeploymentsWithPipeline_PreservesConfirmedDenyAcrossPartialBatchFailure(t *testing.T) {
	const denyAssignmentID = "/subscriptions/00000000-0000-0000-0000-000000000001/providers/" +
		"Microsoft.Authorization/policyAssignments/approved-models-only"

	var batchCalls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		call := atomic.AddInt32(&batchCalls, 1)
		if call == 1 {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"results": map[string]any{
					"dep-0": map[string]any{
						"evaluationOutcome": "NonCompliant",
						"nonCompliantAssignments": []map[string]any{
							{
								"assignmentId": denyAssignmentID,
								"effect":       "Deny",
							},
						},
					},
				},
			})
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	prevEndpoint := modelPolicyARMEndpoint
	prevPipeline := newModelPolicyPipeline
	modelPolicyARMEndpoint = server.URL
	newModelPolicyPipeline = func(_ azcore.TokenCredential) runtime.Pipeline {
		return runtime.NewPipeline(
			"test",
			"v1",
			runtime.PipelineOptions{},
			&policy.ClientOptions{
				Transport: server.Client(),
				Retry:     policy.RetryOptions{MaxRetries: -1},
			},
		)
	}
	t.Cleanup(func() {
		modelPolicyARMEndpoint = prevEndpoint
		newModelPolicyPipeline = prevPipeline
	})

	deployments := make([]synthesis.Deployment, 0, 101)
	for i := range 101 {
		deployments = append(deployments, synthesis.Deployment{
			Name:  fmt.Sprintf("dep-%d", i),
			Model: synthesis.DeploymentModel{Format: "OpenAI", Name: "gpt-4o", Version: "2024-08-06"},
		})
	}

	violations, err := EvaluateProjectModelDeploymentsPolicy(
		t.Context(),
		monitoringTestCredential{},
		testPolicyProjectID,
		deployments,
	)
	require.Error(t, err)
	assert.Equal(t, int32(2), atomic.LoadInt32(&batchCalls))
	require.Len(t, violations, 1)
	assert.Equal(t, "dep-0", violations[0].DeploymentName)
	assert.Equal(t, "approved-models-only", violations[0].DisplayName)

	atomic.StoreInt32(&batchCalls, 0)
	checkErr := CheckProjectModelDeploymentsPolicy(
		t.Context(),
		monitoringTestCredential{},
		testPolicyProjectID,
		deployments,
		nil,
	)
	require.Error(t, checkErr)
	assert.Equal(t, int32(2), atomic.LoadInt32(&batchCalls))
	localErr, ok := errors.AsType[*azdext.LocalError](checkErr)
	require.True(t, ok)
	assert.Equal(t, exterrors.CodeModelBlockedByPolicy, localErr.Code)
}

func TestShouldSkipModelPolicyChecks_Precedence(t *testing.T) {
	t.Setenv(SkipModelPolicyChecksEnvKey, "true")
	t.Setenv(LegacySkipModelPolicyChecksEnvKey, "true")

	assert.False(t, ShouldSkipModelPolicyChecks(map[string]string{
		SkipModelPolicyChecksEnvKey: "false",
	}), "active azd env must take precedence over os.Getenv")

	assert.True(t, ShouldSkipModelPolicyChecks(map[string]string{
		SkipModelPolicyChecksEnvKey: "1",
	}))

	assert.True(t, ShouldSkipModelPolicyChecks(nil))

	t.Setenv(SkipModelPolicyChecksEnvKey, "")
	assert.True(t, ShouldSkipModelPolicyChecks(nil), "falls back to legacy env var when primary is unset")

	t.Setenv(LegacySkipModelPolicyChecksEnvKey, "false")
	assert.False(t, ShouldSkipModelPolicyChecks(nil))
}

func TestFoundryProvisioningProviderDeploy_ModelPolicyCheck(t *testing.T) {
	deployments := []synthesis.Deployment{
		{
			Name:  "chat",
			Model: synthesis.DeploymentModel{Format: "OpenAI", Name: "gpt-4o", Version: "2024-08-06"},
			Sku:   synthesis.DeploymentSku{Name: "GlobalStandard", Capacity: 10},
		},
	}

	t.Run("blocks before ARM deployment submission when policy denies model", func(t *testing.T) {
		fixture := monitoringCloudFixture{}
		p := newMonitoringCloudFixture(t, &fixture)
		p.existingProjectID = testPolicyProjectID
		p.synthResult = &synthesis.Result{
			Parameters: map[string]any{
				"deployments": deployments,
			},
		}
		p.onDiskSource = monitoringTestSource("none")
		p.onDiskSource.mode = templateModeBicep
		p.onDiskSource.parameters["projectResourceId"] = map[string]any{"value": testPolicyProjectID}
		p.onDiskSource.parameters["deployments"] = map[string]any{"value": deployments}

		var policyChecked bool
		p.modelPolicyCheck = func(
			_ context.Context,
			_ azcore.TokenCredential,
			projectID string,
			gotDeployments []synthesis.Deployment,
			_ map[string]string,
		) error {
			policyChecked = true
			assert.Equal(t, testPolicyProjectID, projectID)
			require.Len(t, gotDeployments, 1)
			assert.Equal(t, "chat", gotDeployments[0].Name)
			return ModelPolicyViolationsToError([]ModelPolicyViolation{
				{
					DeploymentName: "chat",
					ModelFormat:    "OpenAI",
					ModelName:      "gpt-4o",
					ModelVersion:   "2024-08-06",
					DisplayName:    "approved-models",
					PortalURL:      defaultFoundryGovernPortalURL,
				},
			})
		}

		_, err := p.Deploy(t.Context(), func(string) {})
		require.Error(t, err)
		assert.True(t, policyChecked)
		localErr, ok := errors.AsType[*azdext.LocalError](err)
		require.True(t, ok)
		assert.Equal(t, exterrors.CodeModelBlockedByPolicy, localErr.Code)

		fixture.mu.Lock()
		defer fixture.mu.Unlock()
		assert.Empty(t, fixture.writes, "blocked deployment must not submit ARM deployment")
	})

	t.Run("proceeds on fail-open when policy evaluator errors out", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		}))
		defer server.Close()

		prevEndpoint := modelPolicyARMEndpoint
		prevPipeline := newModelPolicyPipeline
		modelPolicyARMEndpoint = server.URL
		newModelPolicyPipeline = func(_ azcore.TokenCredential) runtime.Pipeline {
			return runtime.NewPipeline(
				"test",
				"v1",
				runtime.PipelineOptions{},
				&policy.ClientOptions{Transport: server.Client()},
			)
		}
		t.Cleanup(func() {
			modelPolicyARMEndpoint = prevEndpoint
			newModelPolicyPipeline = prevPipeline
		})

		fixture := monitoringCloudFixture{}
		p := newMonitoringCloudFixture(t, &fixture)
		p.existingProjectID = testPolicyProjectID
		p.synthResult = &synthesis.Result{
			Parameters: map[string]any{
				"deployments": deployments,
			},
		}
		p.onDiskSource = monitoringTestSource("none")
		p.onDiskSource.mode = templateModeBicep
		p.onDiskSource.parameters["projectResourceId"] = map[string]any{"value": testPolicyProjectID}
		p.onDiskSource.parameters["deployments"] = map[string]any{"value": deployments}
		p.modelPolicyCheck = CheckProjectModelDeploymentsPolicy

		_, err := p.Deploy(t.Context(), func(string) {})
		require.NoError(t, err)

		fixture.mu.Lock()
		defer fixture.mu.Unlock()
		require.Len(t, fixture.writes, 1, "fail-open check must allow ARM deployment submission to proceed")
	})

	t.Run("extracts JSON-decoded []any deployments override from src.parameters", func(t *testing.T) {
		fixture := monitoringCloudFixture{}
		p := newMonitoringCloudFixture(t, &fixture)
		p.existingProjectID = testPolicyProjectID
		p.synthResult = &synthesis.Result{
			Parameters: map[string]any{
				"deployments": deployments, // stale YAML model: gpt-4o
			},
		}
		p.onDiskSource = monitoringTestSource("none")
		p.onDiskSource.mode = templateModeBicep
		p.onDiskSource.parameters["projectResourceId"] = map[string]any{"value": testPolicyProjectID}
		p.onDiskSource.parameters["deployments"] = map[string]any{
			"value": []any{
				map[string]any{
					"name": "override-chat",
					"model": map[string]any{
						"format":  "OpenAI",
						"name":    "o3-mini",
						"version": "2025-01-31",
					},
					"sku": map[string]any{
						"name":     "GlobalStandard",
						"capacity": 20,
					},
				},
			},
		}

		var gotDeployments []synthesis.Deployment
		p.modelPolicyCheck = func(
			_ context.Context,
			_ azcore.TokenCredential,
			_ string,
			deps []synthesis.Deployment,
			_ map[string]string,
		) error {
			gotDeployments = deps
			return nil
		}

		_, err := p.Deploy(t.Context(), func(string) {})
		require.NoError(t, err)
		require.Len(t, gotDeployments, 1)
		assert.Equal(t, "override-chat", gotDeployments[0].Name)
		assert.Equal(t, "o3-mini", gotDeployments[0].Model.Name)
		assert.Equal(t, "2025-01-31", gotDeployments[0].Model.Version)
	})

	t.Run("explicit empty []any{} override in src.parameters skips stale synthResult deployments", func(t *testing.T) {
		fixture := monitoringCloudFixture{}
		p := newMonitoringCloudFixture(t, &fixture)
		p.existingProjectID = testPolicyProjectID
		p.synthResult = &synthesis.Result{
			Parameters: map[string]any{
				"deployments": deployments,
			},
		}
		p.onDiskSource = monitoringTestSource("none")
		p.onDiskSource.mode = templateModeBicep
		p.onDiskSource.parameters["projectResourceId"] = map[string]any{"value": testPolicyProjectID}
		p.onDiskSource.parameters["deployments"] = map[string]any{
			"value": []any{},
		}

		var policyCalled bool
		p.modelPolicyCheck = func(
			_ context.Context,
			_ azcore.TokenCredential,
			_ string,
			_ []synthesis.Deployment,
			_ map[string]string,
		) error {
			policyCalled = true
			return nil
		}

		_, err := p.Deploy(t.Context(), func(string) {})
		require.NoError(t, err)
		assert.False(t, policyCalled, "must not evaluate stale synthResult deployments when src.parameters overrides to []")
	})

	t.Run("uses overridden projectResourceId from src.parameters", func(t *testing.T) {
		const overrideProjectID = "/subscriptions/00000000-0000-0000-0000-000000000002/resourceGroups/rg-override/" +
			"providers/Microsoft.CognitiveServices/accounts/override-account/projects/override-project"

		fixture := monitoringCloudFixture{}
		p := newMonitoringCloudFixture(t, &fixture)
		p.existingProjectID = testPolicyProjectID
		p.synthResult = &synthesis.Result{
			Parameters: map[string]any{
				"deployments": deployments,
			},
		}
		p.onDiskSource = monitoringTestSource("none")
		p.onDiskSource.mode = templateModeBicep
		p.onDiskSource.parameters["projectResourceId"] = map[string]any{"value": overrideProjectID}
		p.onDiskSource.parameters["deployments"] = map[string]any{"value": deployments}

		var gotProjectID string
		var gotDeployments []synthesis.Deployment
		p.modelPolicyCheck = func(
			_ context.Context,
			_ azcore.TokenCredential,
			projectID string,
			deps []synthesis.Deployment,
			_ map[string]string,
		) error {
			gotProjectID = projectID
			gotDeployments = deps
			return nil
		}

		_, err := p.Deploy(t.Context(), func(string) {})
		require.NoError(t, err)
		assert.Equal(t, overrideProjectID, gotProjectID)
		require.Len(t, gotDeployments, 1)
		assert.Equal(t, "chat", gotDeployments[0].Name)
	})

	t.Run("skips policy check when src.parameters projectResourceId is non-literal or invalid", func(t *testing.T) {
		fixture := monitoringCloudFixture{}
		p := newMonitoringCloudFixture(t, &fixture)
		p.existingProjectID = testPolicyProjectID
		p.synthResult = &synthesis.Result{
			Parameters: map[string]any{
				"deployments": deployments,
			},
		}
		p.onDiskSource = monitoringTestSource("none")
		p.onDiskSource.mode = templateModeBicep
		p.onDiskSource.parameters["projectResourceId"] = map[string]any{"value": "[parameters('dynamicProjectId')]"}
		p.onDiskSource.parameters["deployments"] = map[string]any{"value": deployments}

		var policyCalled bool
		p.modelPolicyCheck = func(
			_ context.Context,
			_ azcore.TokenCredential,
			_ string,
			_ []synthesis.Deployment,
			_ map[string]string,
		) error {
			policyCalled = true
			return nil
		}

		_, err := p.Deploy(t.Context(), func(string) {})
		require.NoError(t, err)
		assert.False(t, policyCalled, "must skip policy check when overridden projectResourceId is non-literal/invalid")
	})

	t.Run("does not revive stale synthResult deployments when resolved template omits deployments parameter", func(t *testing.T) {
		fixture := monitoringCloudFixture{}
		p := newMonitoringCloudFixture(t, &fixture)
		p.existingProjectID = testPolicyProjectID
		p.synthResult = &synthesis.Result{
			Parameters: map[string]any{
				"deployments": deployments,
			},
		}
		p.onDiskSource = monitoringTestSource("none")
		p.onDiskSource.mode = templateModeBicep
		p.onDiskSource.parameters["projectResourceId"] = map[string]any{"value": testPolicyProjectID}
		delete(p.onDiskSource.parameters, "deployments")

		var policyCalled bool
		p.modelPolicyCheck = func(
			_ context.Context,
			_ azcore.TokenCredential,
			_ string,
			_ []synthesis.Deployment,
			_ map[string]string,
		) error {
			policyCalled = true
			return nil
		}

		_, err := p.Deploy(t.Context(), func(string) {})
		require.NoError(t, err)
		assert.False(t, policyCalled, "must not evaluate stale synthResult deployments when src.parameters has no deployments key")
	})

	t.Run("matches case-insensitive Deployments key in src.parameters", func(t *testing.T) {
		got := extractEffectiveModelDeployments(
			map[string]any{
				"Deployments": map[string]any{
					"value": []any{
						map[string]any{
							"name": "cased-chat",
							"model": map[string]any{
								"format":  "OpenAI",
								"name":    "gpt-4.1",
								"version": "2025-04-14",
							},
						},
					},
				},
			},
			&synthesis.Result{
				Parameters: map[string]any{
					"deployments": deployments,
				},
			},
		)
		require.Len(t, got, 1)
		assert.Equal(t, "cased-chat", got[0].Name)
		assert.Equal(t, "gpt-4.1", got[0].Model.Name)
	})
}
