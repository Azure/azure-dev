// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// cspell:ignore noncompliant nextgen

package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"azure.ai.toolboxes/internal/definition"
	"azure.ai.toolboxes/internal/exterrors"
	"azure.ai.toolboxes/internal/foundry/connections"
	"azure.ai.toolboxes/internal/pkg/azure"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/authorization/armauthorization/v2"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/structpb"
)

const testToolboxPolicyProjectID = "/subscriptions/1756abc0-3554-4341-8d6a-46674962ea19/" +
	"resourceGroups/rg/providers/Microsoft.CognitiveServices/accounts/acct/projects/proj"

func newTestToolboxScope(t *testing.T) *toolboxAccessScope {
	t.Helper()
	res := toolboxProjectResource(testToolboxPolicyProjectID)
	require.NotNil(t, res)
	return &toolboxAccessScope{
		resource: res,
		tenantID: "00000000-0000-0000-0000-000000000001",
	}
}

func makeQueryFnWithAssignments(assignments []toolboxPolicyAssignment) toolboxPolicyQueryFunc {
	return func(_ context.Context, req toolboxResourceGraphRequest) ([]json.RawMessage, error) {
		qLower := strings.ToLower(req.Query)
		switch {
		case strings.Contains(qLower, "resourcecontainers"):
			return nil, nil
		case strings.Contains(qLower, "policyexemptions"):
			return nil, nil
		default:
			b, err := json.Marshal(assignments)
			if err != nil {
				return nil, err
			}
			var rows []json.RawMessage
			if err := json.Unmarshal(b, &rows); err != nil {
				return nil, err
			}
			return rows, nil
		}
	}
}

func TestCheckToolboxToolsPolicy(t *testing.T) {
	t.Parallel()

	t.Run("blocks denied tools under 7740c22b and unapproved tools under e7352ad2 with Govern portal link", func(t *testing.T) {
		t.Parallel()
		scope := newTestToolboxScope(t)

		deniedAssignment := toolboxPolicyAssignment{
			AssignmentID:          "/subscriptions/1756abc0-3554-4341-8d6a-46674962ea19/providers/Microsoft.Authorization/policyAssignments/deny-tools",
			AssignmentScope:       "/subscriptions/1756abc0-3554-4341-8d6a-46674962ea19",
			PolicyDefinitionID:    toolboxDeniedToolsPolicyID,
			AssignmentDisplayName: "Deny Code Interpreter and Untrusted MCP",
			AssignmentParameters: map[string]any{
				"effect":              map[string]any{"value": "Deny"},
				"deniedToolTypes":     map[string]any{"value": []any{"code_interpreter"}},
				"deniedMcpEndpoints":  map[string]any{"value": []any{"https://untrusted.example.com/*"}},
				"deniedMcpConnectors": map[string]any{"value": []any{"blocked-connector"}},
			},
			EnforcementMode: "Default",
		}

		access := &toolboxAccess{
			policyQuery: makeQueryFnWithAssignments([]toolboxPolicyAssignment{deniedAssignment}),
		}

		err := checkToolboxToolsPolicy(t.Context(), access, scope, "research-tb", []map[string]any{
			{"type": "web_search"},
			{"type": "code_interpreter"},
			{"type": "mcp", "name": "bad-mcp", "server_url": "https://untrusted.example.com/mcp"},
		})
		require.Error(t, err)
		localErr, ok := errors.AsType[*azdext.LocalError](err)
		require.True(t, ok)
		assert.Equal(t, exterrors.CodeToolBlockedByPolicy, localErr.Code)
		assert.Contains(t, localErr.Message, `"research-tb"`)
		assert.Contains(t, localErr.Message, `"code_interpreter"`)
		assert.Contains(t, localErr.Message, `"mcp (bad-mcp)"`)
		assert.Equal(t, 1, strings.Count(localErr.Suggestion, "View policy (Deny Code Interpreter and Untrusted MCP):"))
		assert.Contains(t, localErr.Suggestion, "https://ai.azure.com/nextgen/r/")

		// Approved tools policy (e7352ad2)
		approvedAssignment := toolboxPolicyAssignment{
			AssignmentID:          "/subscriptions/1756abc0-3554-4341-8d6a-46674962ea19/providers/Microsoft.Authorization/policyAssignments/approve-tools",
			AssignmentScope:       "/subscriptions/1756abc0-3554-4341-8d6a-46674962ea19",
			PolicyDefinitionID:    toolboxAllowedToolsPolicyID,
			AssignmentDisplayName: "Approved Tools Only",
			AssignmentParameters: map[string]any{
				"effect":                map[string]any{"value": "Deny"},
				"approvedToolTypes":     map[string]any{"value": []any{"web_search"}},
				"approvedMcpEndpoints":  map[string]any{"value": []any{"https://approved.example.com/*"}},
				"approvedMcpConnectors": map[string]any{"value": []any{"approved-connector"}},
			},
			EnforcementMode: "Default",
		}
		accessApproved := &toolboxAccess{
			policyQuery: makeQueryFnWithAssignments([]toolboxPolicyAssignment{approvedAssignment}),
		}

		// Allowed when all tools match approvedToolTypes, approvedMcpEndpoints, or approvedMcpConnectors
		require.NoError(t, checkToolboxToolsPolicy(t.Context(), accessApproved, scope, "research-tb", []map[string]any{
			{"type": "web_search"},
			{"type": "mcp", "name": "ep-mcp", "server_url": "https://approved.example.com/v1/mcp"},
			{"type": "mcp", "name": "conn-mcp", "connector_id": "approved-connector"},
		}))

		// Indeterminate MCP tool (no server_url or connector_id) fails open
		require.NoError(t, checkToolboxToolsPolicy(t.Context(), accessApproved, scope, "research-tb", []map[string]any{
			{"type": "mcp", "name": "unresolved-mcp", "project_connection_id": "some-conn"},
		}))

		// Unapproved tool is blocked
		errUnapproved := checkToolboxToolsPolicy(t.Context(), accessApproved, scope, "research-tb", []map[string]any{
			{"type": "file_search"},
		})
		require.Error(t, errUnapproved)
		localUnapproved, ok := errors.AsType[*azdext.LocalError](errUnapproved)
		require.True(t, ok)
		assert.Equal(t, exterrors.CodeToolBlockedByPolicy, localUnapproved.Code)
	})

	t.Run("fails open when Resource Graph query returns an error", func(t *testing.T) {
		t.Parallel()
		scope := newTestToolboxScope(t)
		access := &toolboxAccess{
			policyQuery: func(_ context.Context, _ toolboxResourceGraphRequest) ([]json.RawMessage, error) {
				return nil, errors.New("Resource Graph unavailable")
			},
		}
		err := checkToolboxToolsPolicy(t.Context(), access, scope, "research-tb", []map[string]any{
			{"type": "code_interpreter"},
		})
		require.NoError(t, err)
	})
}

func TestToolboxPolicyOptOut(t *testing.T) {
	scope := newTestToolboxScope(t)
	deniedAssignment := toolboxPolicyAssignment{
		AssignmentID:          "/subscriptions/1756abc0-3554-4341-8d6a-46674962ea19/providers/Microsoft.Authorization/policyAssignments/deny-tools",
		AssignmentScope:       "/subscriptions/1756abc0-3554-4341-8d6a-46674962ea19",
		PolicyDefinitionID:    toolboxDeniedToolsPolicyID,
		AssignmentDisplayName: "Deny Code Interpreter",
		AssignmentParameters: map[string]any{
			"effect":          map[string]any{"value": "Deny"},
			"deniedToolTypes": map[string]any{"value": []any{"code_interpreter"}},
		},
		EnforcementMode: "Default",
	}
	access := &toolboxAccess{
		policyQuery: makeQueryFnWithAssignments([]toolboxPolicyAssignment{deniedAssignment}),
	}

	t.Setenv("AZD_AI_AGENT_SKIP_POLICY_CHECKS", "true")
	require.NoError(t, checkToolboxToolsPolicy(t.Context(), access, scope, "research-tb", []map[string]any{
		{"type": "code_interpreter"},
	}))
}

func TestTryClassifyToolboxPolicyError(t *testing.T) {
	t.Parallel()
	scope := newTestToolboxScope(t)

	// 1. Live 403 agent_policy_denied
	liveBody := `{"error":{"code":"agent_policy_denied",` +
		`"message":"Creating toolbox 'research-tb' is noncompliant with policy ` +
		`/subscriptions/1756abc0-3554-4341-8d6a-46674962ea19/providers/Microsoft.Authorization/policyAssignments/restrict-foundry-tools:\n` +
		`Count of Microsoft.CognitiveServices.Data/accounts/projects/toolboxes/tools[*]",` +
		`"additionalInfo":{` +
		`"policy_assignment_id":"/subscriptions/1756abc0-3554-4341-8d6a-46674962ea19/providers/Microsoft.Authorization/policyAssignments/restrict-foundry-tools",` +
		`"policy_definition_id":"/providers/Microsoft.Authorization/policyDefinitions/e7352ad2-6c86-4a07-9573-47f67b557ba8",` +
		`"effect":"Deny"}}}`
	liveRespErr := &azcore.ResponseError{
		StatusCode: http.StatusForbidden,
		ErrorCode:  "agent_policy_denied",
		RawResponse: &http.Response{
			StatusCode: http.StatusForbidden,
			Body:       io.NopCloser(strings.NewReader(liveBody)),
		},
	}
	classifiedLive, okLive := tryClassifyToolboxPolicyError(liveRespErr, "research-tb", scope)
	require.True(t, okLive)
	localLive, isLocal := errors.AsType[*azdext.LocalError](classifiedLive)
	require.True(t, isLocal)
	assert.Equal(t, exterrors.CodeToolBlockedByPolicy, localLive.Code)
	assert.Contains(t, localLive.Message, `"research-tb"`)
	assert.Contains(t, localLive.Suggestion, "View policy (restrict-foundry-tools): https://ai.azure.com/nextgen/r/")

	// 2. Skylight #6450-style 403 forbidden
	skylightBody := `{"error":{"code":"forbidden",` +
		`"message":"Creating toolbox is noncompliant with policy ` +
		`/subscriptions/1756abc0-3554-4341-8d6a-46674962ea19/providers/Microsoft.Authorization/policyAssignments/approved-tools:\n` +
		`Count of Microsoft.CognitiveServices.Data/accounts/projects/toolboxes/tools[*]"}}`
	skylightRespErr := &azcore.ResponseError{
		StatusCode: http.StatusForbidden,
		ErrorCode:  "forbidden",
		RawResponse: &http.Response{
			StatusCode: http.StatusForbidden,
			Body:       io.NopCloser(strings.NewReader(skylightBody)),
		},
	}
	classifiedSky, okSky := tryClassifyToolboxPolicyError(skylightRespErr, "research-tb", scope)
	require.True(t, okSky)
	localSky, isLocalSky := errors.AsType[*azdext.LocalError](classifiedSky)
	require.True(t, isLocalSky)
	assert.Equal(t, exterrors.CodeToolBlockedByPolicy, localSky.Code)
	assert.Contains(t, localSky.Suggestion, "View policy (approved-tools): https://ai.azure.com/nextgen/r/")

	// 3. Ordinary 403 RBAC error is NOT misclassified
	rbacBody := `{"error":{"code":"PermissionDenied","message":"The caller does not have permission."}}`
	rbacRespErr := &azcore.ResponseError{
		StatusCode: http.StatusForbidden,
		ErrorCode:  "PermissionDenied",
		RawResponse: &http.Response{
			StatusCode: http.StatusForbidden,
			Body:       io.NopCloser(strings.NewReader(rbacBody)),
		},
	}
	_, okRBAC := tryClassifyToolboxPolicyError(rbacRespErr, "research-tb", scope)
	assert.False(t, okRBAC)

	// 4. Never leaks secrets from request URL or raw error.message into LocalError.Message or Suggestion
	secretBody := `{"error":{"code":"agent_policy_denied",` +
		`"message":"Creating toolbox is noncompliant with policy ` +
		`/subscriptions/1756abc0-3554-4341-8d6a-46674962ea19/providers/Microsoft.Authorization/policyAssignments/restrict-foundry-tools:\n` +
		`MCP endpoint https://secret.example.com/mcp?api_key=SUPER_SECRET_TOKEN is not allowed (` +
		`Count of Microsoft.CognitiveServices.Data/accounts/projects/toolboxes/tools[*])"}}`
	secretReq, err := http.NewRequestWithContext(
		t.Context(),
		http.MethodPost,
		"https://secret.example.com/mcp?api_key=SUPER_SECRET_TOKEN",
		nil,
	)
	require.NoError(t, err)
	secretRespErr := &azcore.ResponseError{
		StatusCode: http.StatusForbidden,
		ErrorCode:  "agent_policy_denied",
		RawResponse: &http.Response{
			StatusCode: http.StatusForbidden,
			Body:       io.NopCloser(strings.NewReader(secretBody)),
			Request:    secretReq,
		},
	}
	classifiedSecret, okSecret := tryClassifyToolboxPolicyError(secretRespErr, "research-tb", scope)
	require.True(t, okSecret)
	localSecret, isLocalSecret := errors.AsType[*azdext.LocalError](classifiedSecret)
	require.True(t, isLocalSecret)
	assert.NotContains(t, localSecret.Message, "SUPER_SECRET_TOKEN")
	assert.NotContains(t, localSecret.Suggestion, "SUPER_SECRET_TOKEN")
	assert.Contains(t, localSecret.Message, `"restrict-foundry-tools"`)
}

func TestToolboxPolicyAssignmentScopesAndResourceGraphPagination(t *testing.T) {
	t.Parallel()

	t.Run("supports subscription, resource-group, account, project, and management-group assignment IDs", func(t *testing.T) {
		t.Parallel()
		for _, id := range []string{
			"/subscriptions/1756abc0-3554-4341-8d6a-46674962ea19/providers/Microsoft.Authorization/policyAssignments/sub-policy",
			"/subscriptions/1756abc0-3554-4341-8d6a-46674962ea19/resourceGroups/rg/providers/Microsoft.Authorization/policyAssignments/rg-policy",
			"/subscriptions/1756abc0-3554-4341-8d6a-46674962ea19/resourceGroups/rg/providers/Microsoft.CognitiveServices/accounts/acct/providers/Microsoft.Authorization/policyAssignments/acct-policy",
			"/subscriptions/1756abc0-3554-4341-8d6a-46674962ea19/resourceGroups/rg/providers/Microsoft.CognitiveServices/accounts/acct/projects/proj/providers/Microsoft.Authorization/policyAssignments/proj-policy",
			"/providers/Microsoft.Management/managementGroups/corp-mg/providers/Microsoft.Authorization/policyAssignments/mg-policy",
		} {
			assert.True(t, isValidToolboxPolicyAssignmentID(id), "expected valid assignment ID: %s", id)
			portalURL := buildToolboxPolicyPortalURL(testToolboxPolicyProjectID, id)
			parsed, err := url.Parse(portalURL)
			require.NoError(t, err)
			assert.Contains(t, parsed.Path, "/manage/govern/assignments/")
		}

		// Rejects prefixed @attacker.example project IDs
		assert.Equal(
			t,
			toolboxDefaultGovernPortalURL,
			buildToolboxPolicyPortalURL(
				"@attacker.example"+testToolboxPolicyProjectID,
				"/subscriptions/1756abc0-3554-4341-8d6a-46674962ea19/providers/Microsoft.Authorization/policyAssignments/sub-policy",
			),
		)
	})

	t.Run("follows Resource Graph $skipToken pagination and rejects repeated tokens or truncated responses without token", func(t *testing.T) {
		t.Parallel()
		newPipeline := func(fn accessTransport) runtime.Pipeline {
			return runtime.NewPipeline(
				"test",
				"v0.0.0",
				runtime.PipelineOptions{},
				&policy.ClientOptions{Transport: fn},
			)
		}

		// 1. Multi-page continuation
		calls := 0
		pagedQuery := newToolboxResourceGraphQueryFunc(newPipeline(func(req *http.Request) (*http.Response, error) {
			calls++
			body := `{"data":[{"id":"second"}],"resultTruncated":false}`
			if calls == 1 {
				body = `{"data":[{"id":"first"}],"resultTruncated":true,"$skipToken":"page-2"}`
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       io.NopCloser(strings.NewReader(body)),
				Request:    req,
			}, nil
		}))
		rows, err := pagedQuery(t.Context(), toolboxResourceGraphRequest{Subscriptions: []string{"sub"}, Query: "q"})
		require.NoError(t, err)
		require.Len(t, rows, 2)

		// 2. Repeated continuation token error
		repeatQuery := newToolboxResourceGraphQueryFunc(newPipeline(func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       io.NopCloser(strings.NewReader(`{"data":[{"id":"first"}],"$skipToken":"same-token"}`)),
				Request:    req,
			}, nil
		}))
		_, err = repeatQuery(t.Context(), toolboxResourceGraphRequest{Subscriptions: []string{"sub"}, Query: "q"})
		require.ErrorContains(t, err, "repeated Resource Graph continuation token")

		// 3. resultTruncated: true without $skipToken error
		truncQuery := newToolboxResourceGraphQueryFunc(newPipeline(func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       io.NopCloser(strings.NewReader(`{"data":[{"id":"partial"}],"resultTruncated":true}`)),
				Request:    req,
			}, nil
		}))
		_, err = truncQuery(t.Context(), toolboxResourceGraphRequest{Subscriptions: []string{"sub"}, Query: "q"})
		require.ErrorContains(t, err, "truncated without a continuation token")
	})
}

func TestDeployAndCreateToolboxPolicyCallSites(t *testing.T) {
	stubAccessProjectEndpoint(t)
	_ = stubToolboxEndpointEnv(t)

	deniedAssignment := toolboxPolicyAssignment{
		AssignmentID:          accessTestScope + "/providers/Microsoft.Authorization/policyAssignments/deny-tools",
		AssignmentScope:       accessTestScope,
		PolicyDefinitionID:    toolboxDeniedToolsPolicyID,
		AssignmentDisplayName: "Deny Code Interpreter",
		AssignmentParameters: map[string]any{
			"effect":          map[string]any{"value": "Deny"},
			"deniedToolTypes": map[string]any{"value": []any{"code_interpreter"}},
		},
		EnforcementMode: "Default",
	}

	newReadyAccess := func(queryFn toolboxPolicyQueryFunc) *toolboxAccess {
		access := testToolboxAccess(t, func(request *http.Request) (*http.Response, error) {
			return accessHTTPResponse(request, 200, map[string]any{
				"value": []*armauthorization.RoleAssignment{testAccessRole(accessTestScope, foundryUserRole)},
			}), nil
		})
		access.policyQuery = queryFn
		return access
	}

	// 1. toolboxServiceTarget.Deploy blocked by policy -> CreateToolboxVersion is NOT called
	mockBlockedDeploy := newMockToolboxClient(accessTestEndpoint)
	targetBlocked := &toolboxServiceTarget{
		access: newReadyAccess(makeQueryFnWithAssignments([]toolboxPolicyAssignment{deniedAssignment})),
		newClient: func(_, _ string) (toolboxClient, error) {
			return accessProbeClient{
				toolboxClient: mockBlockedDeploy,
				probe: func(context.Context, string) (*azure.ToolboxObject, error) {
					return nil, notFoundResponseError("toolbox not found")
				},
			}, nil
		},
		resolver: newStubConnectionResolver(),
	}
	props, err := structpb.NewStruct(map[string]any{
		"tools": []any{map[string]any{"type": "code_interpreter"}},
	})
	require.NoError(t, err)
	_, err = targetBlocked.Deploy(t.Context(), &azdext.ServiceConfig{
		Name: "research", AdditionalProperties: props, Environment: map[string]string{"K": "V"},
	}, nil, nil, nil)
	require.Error(t, err)
	localDeployErr, ok := errors.AsType[*azdext.LocalError](err)
	require.True(t, ok)
	assert.Equal(t, exterrors.CodeToolBlockedByPolicy, localDeployErr.Code)
	assert.Empty(t, mockBlockedDeploy.createVersionCalls)

	// 2. toolboxServiceTarget.Deploy fails open on policy query error -> CreateToolboxVersion succeeds
	mockOpenDeploy := newMockToolboxClient(accessTestEndpoint)
	targetOpen := &toolboxServiceTarget{
		access: newReadyAccess(func(context.Context, toolboxResourceGraphRequest) ([]json.RawMessage, error) {
			return nil, errors.New("Resource Graph unavailable")
		}),
		newClient: func(_, _ string) (toolboxClient, error) {
			return accessProbeClient{
				toolboxClient: mockOpenDeploy,
				probe: func(context.Context, string) (*azure.ToolboxObject, error) {
					return nil, notFoundResponseError("toolbox not found")
				},
			}, nil
		},
		resolver: newStubConnectionResolver(),
	}
	_, err = targetOpen.Deploy(t.Context(), &azdext.ServiceConfig{
		Name: "research", AdditionalProperties: props, Environment: map[string]string{"K": "V"},
	}, nil, nil, nil)
	require.NoError(t, err)
	require.Len(t, mockOpenDeploy.createVersionCalls, 1)

	// 3. createToolboxFromInput blocked by policy -> CreateToolboxVersion is NOT called
	mockBlockedCreate := newMockToolboxClient(accessTestEndpoint)
	accessBlockedCreate := newReadyAccess(makeQueryFnWithAssignments([]toolboxPolicyAssignment{deniedAssignment}))
	scopeBlocked, err := accessBlockedCreate.resolve(t.Context(), accessTestEndpoint)
	require.NoError(t, err)
	var outBuf bytes.Buffer
	err = createToolboxFromInput(
		t.Context(),
		accessProbeClient{
			toolboxClient: mockBlockedCreate,
			probe: func(context.Context, string) (*azure.ToolboxObject, error) {
				return nil, notFoundResponseError("toolbox not found")
			},
		},
		newStubConnectionResolver(),
		accessTestEndpoint,
		"research",
		&definition.Definition{Tools: []map[string]any{{"type": "code_interpreter"}}},
		toolboxFlags{output: "json"},
		accessBlockedCreate,
		scopeBlocked,
		&outBuf,
		nil,
	)
	require.Error(t, err)
	localCreateErr, ok := errors.AsType[*azdext.LocalError](err)
	require.True(t, ok)
	assert.Equal(t, exterrors.CodeToolBlockedByPolicy, localCreateErr.Code)
	assert.Empty(t, mockBlockedCreate.createVersionCalls)

	// 4. createToolboxFromInput fails open on policy query error -> CreateToolboxVersion succeeds
	mockOpenCreate := newMockToolboxClient(accessTestEndpoint)
	accessOpenCreate := newReadyAccess(func(context.Context, toolboxResourceGraphRequest) ([]json.RawMessage, error) {
		return nil, errors.New("Resource Graph unavailable")
	})
	scopeOpen, err := accessOpenCreate.resolve(t.Context(), accessTestEndpoint)
	require.NoError(t, err)
	err = createToolboxFromInput(
		t.Context(),
		accessProbeClient{
			toolboxClient: mockOpenCreate,
			probe: func(context.Context, string) (*azure.ToolboxObject, error) {
				return nil, notFoundResponseError("toolbox not found")
			},
		},
		newStubConnectionResolver(),
		accessTestEndpoint,
		"research",
		&definition.Definition{Tools: []map[string]any{{"type": "code_interpreter"}}},
		toolboxFlags{output: "json"},
		accessOpenCreate,
		scopeOpen,
		&outBuf,
		nil,
	)
	require.NoError(t, err)
	require.Len(t, mockOpenCreate.createVersionCalls, 1)

	// 5. runConnectionAddWith classifies runtime 403 policy denials on CreateToolboxVersion
	// without exposing secrets in the error message.
	connAddClient := newMockToolboxClient(accessTestEndpoint)
	connAddClient.getResults["research"] = toolboxGetResult{obj: &azure.ToolboxObject{
		Name: "research", DefaultVersion: "1",
	}}
	connAddClient.versionResults["research/1"] = toolboxVersionResult{obj: &azure.ToolboxVersionObject{
		Name: "research", Version: "1", Tools: []map[string]any{
			{"type": "mcp", "name": "existing", "project_connection_id": "/c/existing"},
		},
	}}
	connAddPolicyBody := `{"error":{"code":"agent_policy_denied",` +
		`"message":"Updating toolbox is noncompliant with policy ` +
		`/subscriptions/1756abc0-3554-4341-8d6a-46674962ea19/providers/Microsoft.Authorization/policyAssignments/restrict-foundry-tools:\n` +
		`MCP endpoint https://secret.example.com/mcp?api_key=CONN_ADD_SECRET is not allowed (` +
		`Count of Microsoft.CognitiveServices.Data/accounts/projects/toolboxes/tools[*])"}}`
	connAddClient.createVersionErr = &azcore.ResponseError{
		StatusCode: http.StatusForbidden,
		ErrorCode:  "agent_policy_denied",
		RawResponse: &http.Response{
			StatusCode: http.StatusForbidden,
			Body:       io.NopCloser(strings.NewReader(connAddPolicyBody)),
		},
	}
	connResolver := newStubConnectionResolver()
	connResolver.byName["new-mcp"] = &projectConnection{
		ID: "/c/new-mcp", Category: connections.ConnectionTypeRemoteTool, Name: "new-mcp", Target: "https://secret.example.com/mcp",
	}
	connAddErr := runConnectionAddWith(
		t.Context(),
		connAddClient,
		connResolver,
		accessTestEndpoint,
		"research",
		"new-mcp",
		connectionAddFlags{},
		toolboxFlags{output: "json"},
	)
	require.Error(t, connAddErr)
	localConnAddErr, ok := errors.AsType[*azdext.LocalError](connAddErr)
	require.True(t, ok)
	assert.Equal(t, exterrors.CodeToolBlockedByPolicy, localConnAddErr.Code)
	assert.Contains(t, localConnAddErr.Message, `"research"`)
	assert.Contains(t, localConnAddErr.Suggestion, "View policy (restrict-foundry-tools):")
	assert.NotContains(t, localConnAddErr.Message, "CONN_ADD_SECRET")
	assert.NotContains(t, localConnAddErr.Suggestion, "CONN_ADD_SECRET")
}

func TestToolboxPolicyApplicabilityAndExemptions(t *testing.T) {
	t.Parallel()

	scope := newTestToolboxScope(t)
	mgScope := "/providers/microsoft.management/managementgroups/corp-mg"
	subScope := "/subscriptions/1756abc0-3554-4341-8d6a-46674962ea19"
	rgScope := subScope + "/resourceGroups/rg"

	baseDenyAssignment := func(modify func(*toolboxPolicyAssignment)) toolboxPolicyAssignment {
		a := toolboxPolicyAssignment{
			AssignmentID:          subScope + "/providers/Microsoft.Authorization/policyAssignments/deny-ci",
			AssignmentScope:       subScope,
			PolicyDefinitionID:    toolboxDeniedToolsPolicyID,
			AssignmentDisplayName: "Deny Code Interpreter",
			AssignmentParameters: map[string]any{
				"effect":          map[string]any{"value": "Deny"},
				"deniedToolTypes": map[string]any{"value": []any{"code_interpreter"}},
			},
			EnforcementMode: "Default",
		}
		if modify != nil {
			modify(&a)
		}
		return a
	}

	type exemptionFixture struct {
		ExemptionID        string `json:"exemptionId"`
		PolicyAssignmentID string `json:"policyAssignmentId"`
		ExpiresOn          string `json:"expiresOn,omitempty"`
	}

	for _, tc := range []struct {
		name        string
		assignment  toolboxPolicyAssignment
		mgScopes    []string
		exemptions  []exemptionFixture
		wantBlocked bool
	}{
		{
			name: "Audit effect is ignored",
			assignment: baseDenyAssignment(func(a *toolboxPolicyAssignment) {
				a.AssignmentParameters["effect"] = map[string]any{"value": "Audit"}
			}),
			wantBlocked: false,
		},
		{
			name: "Disabled effect is ignored",
			assignment: baseDenyAssignment(func(a *toolboxPolicyAssignment) {
				a.AssignmentParameters["effect"] = map[string]any{"value": "Disabled"}
			}),
			wantBlocked: false,
		},
		{
			name: "DoNotEnforce enforcementMode is ignored",
			assignment: baseDenyAssignment(func(a *toolboxPolicyAssignment) {
				a.EnforcementMode = "DoNotEnforce"
			}),
			wantBlocked: false,
		},
		{
			name: "assignmentScope outside project hierarchy is ignored",
			assignment: baseDenyAssignment(func(a *toolboxPolicyAssignment) {
				a.AssignmentScope = subScope + "/resourceGroups/other-rg"
			}),
			wantBlocked: false,
		},
		{
			name: "notScopes excluding project resource group is ignored",
			assignment: baseDenyAssignment(func(a *toolboxPolicyAssignment) {
				a.NotScopes = []string{rgScope}
			}),
			wantBlocked: false,
		},
		{
			name: "notScopes excluding ancestor management group is ignored",
			assignment: baseDenyAssignment(func(a *toolboxPolicyAssignment) {
				a.AssignmentScope = mgScope
				a.NotScopes = []string{mgScope}
			}),
			mgScopes:    []string{mgScope},
			wantBlocked: false,
		},
		{
			name: "policyEffect override is ignored",
			assignment: baseDenyAssignment(func(a *toolboxPolicyAssignment) {
				a.AssignmentOverrides = []any{map[string]any{"kind": "policyEffect", "value": "Deny"}}
			}),
			wantBlocked: false,
		},
		{
			name: "non-empty resourceSelectors is ignored",
			assignment: baseDenyAssignment(func(a *toolboxPolicyAssignment) {
				a.ResourceSelectors = []any{map[string]any{"name": "eastus-only"}}
			}),
			wantBlocked: false,
		},
		{
			name:       "active in-scope exemption suppresses violation",
			assignment: baseDenyAssignment(nil),
			exemptions: []exemptionFixture{{
				ExemptionID:        rgScope + "/providers/Microsoft.Authorization/policyExemptions/ex-active",
				PolicyAssignmentID: subScope + "/providers/Microsoft.Authorization/policyAssignments/deny-ci",
				ExpiresOn:          "2999-01-01T00:00:00Z",
			}},
			wantBlocked: false,
		},
		{
			name:       "expired exemption still enforces violation",
			assignment: baseDenyAssignment(nil),
			exemptions: []exemptionFixture{{
				ExemptionID:        rgScope + "/providers/Microsoft.Authorization/policyExemptions/ex-expired",
				PolicyAssignmentID: subScope + "/providers/Microsoft.Authorization/policyAssignments/deny-ci",
				ExpiresOn:          "2020-01-01T00:00:00Z",
			}},
			wantBlocked: true,
		},
		{
			name:       "out-of-scope exemption still enforces violation",
			assignment: baseDenyAssignment(nil),
			exemptions: []exemptionFixture{{
				ExemptionID:        subScope + "/resourceGroups/other-rg/providers/Microsoft.Authorization/policyExemptions/ex-other",
				PolicyAssignmentID: subScope + "/providers/Microsoft.Authorization/policyAssignments/deny-ci",
			}},
			wantBlocked: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			access := &toolboxAccess{
				policyQuery: func(_ context.Context, req toolboxResourceGraphRequest) ([]json.RawMessage, error) {
					qLower := strings.ToLower(req.Query)
					var payload any
					switch {
					case strings.Contains(qLower, "resourcecontainers"):
						var rows []map[string]string
						for _, s := range tc.mgScopes {
							rows = append(rows, map[string]string{"scope": s})
						}
						payload = rows
					case strings.Contains(qLower, "policyexemptions"):
						payload = tc.exemptions
					default:
						payload = []toolboxPolicyAssignment{tc.assignment}
					}
					if payload == nil {
						return nil, nil
					}
					b, err := json.Marshal(payload)
					if err != nil {
						return nil, err
					}
					var rawRows []json.RawMessage
					if err := json.Unmarshal(b, &rawRows); err != nil {
						return nil, err
					}
					return rawRows, nil
				},
			}

			err := checkToolboxToolsPolicy(t.Context(), access, scope, "research-tb", []map[string]any{
				{"type": "code_interpreter"},
			})
			if tc.wantBlocked {
				require.Error(t, err)
				localErr, ok := errors.AsType[*azdext.LocalError](err)
				require.True(t, ok)
				assert.Equal(t, exterrors.CodeToolBlockedByPolicy, localErr.Code)
			} else {
				require.NoError(t, err)
			}
		})
	}
}
