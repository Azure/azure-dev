// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package onboarding

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOperatorManagementGroupAssignmentReturnedAtScope(t *testing.T) {
	const target = "/subscriptions/contract-sub/resourceGroups/target"
	const managementGroup = "/providers/Microsoft.Management/managementGroups/parent"
	for _, tc := range []struct {
		name, principal string
		groups          map[string]bool
		unconditional   bool
		condition       string
		expected        Status
	}{
		{"direct operator inherited allow", "operator", nil, false, "", Pass},
		{"explicit token group inherited allow", "member", map[string]bool{"member": true}, false, "", Pass},
		{"unknown group is not inferred", "unknown", nil, false, "", Fail},
		{"unconditional inherited allow", "operator", nil, true, "", Pass},
		{"conditional grant cannot satisfy P06", "operator", nil, true, "unsupported", Fail},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assignment := p07Assignment(managementGroup, tc.principal, "writer")
			if tc.condition != "" {
				properties, ok := assignment["properties"].(map[string]any)
				require.True(t, ok)
				properties["condition"] = tc.condition
			}
			var paths []string
			a := p07Authorizer(t, []any{assignment}, map[string]any{
				"writer": p07Permissions(map[string]any{"actions": []string{"*"}}),
			}, &paths, http.StatusOK)
			status, detail, err := a.basicAction(t.Context(), target,
				callerIdentity{objectID: "operator", groups: tc.groups}, roleAssignmentWrite, tc.unconditional)
			assert.NoError(t, err)
			assert.Equal(t, tc.expected, status, detail)
		})
	}
}

func TestOperatorGroupOverageUsesOnlyARMProvenScopedAssignments(t *testing.T) {
	const oid = "00000000-0000-0000-0000-000000000123"
	const target = "/subscriptions/contract-sub"
	for _, tc := range []struct {
		name, scope, condition, conditionVersion, delegated, action string
		unconditional, excluded, readFail                           bool
		expected                                                    Status
	}{
		{name: "ARM proven group owner", scope: target, unconditional: true, expected: Pass},
		{name: "child scope cannot authorize subscription", scope: target + "/resourceGroups/child", expected: Fail},
		{name: "other subscription rejected", scope: "/subscriptions/other", expected: Fail},
		{name: "local NotActions excludes write", scope: target, excluded: true, expected: Fail},
		{name: "P06 rejects conditional owner", scope: target, condition: "unsupported", unconditional: true, expected: Fail},
		{name: "P06 rejects condition version", scope: target,
			conditionVersion: "2.0", unconditional: true, expected: Fail},
		{name: "P06 rejects delegated owner", scope: target, delegated: "/identity", unconditional: true, expected: Fail},
		{name: "basic action retains condition contract", scope: target, condition: "unsupported", expected: Pass},
		{name: "ARM proof read failure", scope: target, readFail: true, expected: Unverified},
		{name: "P02 identity write", scope: target,
			action: "Microsoft.ManagedIdentity/userAssignedIdentities/write", expected: Pass},
		{name: "P02 identity read", scope: target,
			action: "Microsoft.ManagedIdentity/userAssignedIdentities/read", expected: Pass},
		{name: "P02 identity assign", scope: target,
			action: "Microsoft.ManagedIdentity/userAssignedIdentities/assign/action", expected: Pass},
		{name: "P04 Storage write", scope: target, action: "Microsoft.Storage/storageAccounts/write", expected: Pass},
		{name: "P05 registry write", scope: target, action: "Microsoft.ContainerRegistry/registries/write", expected: Pass},
	} {
		t.Run(tc.name, func(t *testing.T) {
			caller, err := identityFromToken("header." + base64.RawURLEncoding.EncodeToString([]byte(
				`{"oid":"`+oid+`","_claim_names":{"groups":"source"},"_claim_sources":`+
					`{"source":{"endpoint":"https://graph.invalid/never-call"}}}`)) + ".signature")
			require.NoError(t, err)
			require.True(t, caller.groupOverage)
			require.Empty(t, caller.groups)
			assignment := p07Assignment(tc.scope, "ARM-proven-group-not-in-token", "owner")
			properties, ok := assignment["properties"].(map[string]any)
			require.True(t, ok)
			if tc.condition != "" {
				properties["condition"] = tc.condition
			}
			if tc.conditionVersion != "" {
				properties["conditionVersion"] = tc.conditionVersion
			}
			if tc.delegated != "" {
				properties["delegatedManagedIdentityResourceId"] = tc.delegated
			}
			assignedCalls, roleCalls := 0, 0
			options := contractARMOptions(func(request *http.Request) (*http.Response, error) {
				require.Equal(t, http.MethodGet, request.Method)
				require.Equal(t, "management.azure.com", request.URL.Host, "never follow token group endpoints")
				var body any
				switch {
				case strings.HasSuffix(request.URL.Path, "/roleAssignments"):
					assert.Equal(t, target+"/providers/Microsoft.Authorization/roleAssignments", request.URL.Path)
					switch filter := request.URL.Query().Get("$filter"); filter {
					case "atScope()":
						body = map[string]any{"value": []any{p07Assignment(target, "unknown-unproven-group", "owner")}}
					case "assignedTo('" + oid + "')":
						assignedCalls++
						if tc.readFail {
							return contractARMResponse(request, http.StatusForbidden,
								`{"error":{"code":"AuthorizationFailed"}}`), nil
						}
						body = map[string]any{"value": []any{assignment}}
					default:
						t.Fatalf("unexpected role filter %q", filter)
					}
				case strings.Contains(request.URL.Path, "/roleDefinitions/"):
					roleCalls++
					permission := map[string]any{"actions": []string{"*"}}
					if tc.excluded {
						permission["notActions"] = []string{roleAssignmentWrite}
					}
					body = p07Permissions(permission)
				default:
					t.Fatalf("unexpected ARM read %s", request.URL.Path)
				}
				raw, err := json.Marshal(body)
				require.NoError(t, err)
				return contractARMResponse(request, http.StatusOK, string(raw)), nil
			})
			a := &scopedAuthorizer{credential: contractCredential{}, options: options}
			action := tc.action
			if action == "" {
				action = roleAssignmentWrite
			}
			status, description, err := a.basicAction(t.Context(), target, caller, action, tc.unconditional)
			assert.Equal(t, tc.expected, status, description)
			assert.Equal(t, 1, assignedCalls)
			if tc.readFail {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
			if strings.Contains(tc.name, "scope") ||
				tc.unconditional && (tc.condition != "" || tc.conditionVersion != "" || tc.delegated != "") {
				assert.Zero(t, roleCalls)
			}
		})
	}
}

func TestOperatorGroupProofReadsAllPagesWithoutElevatingChildScope(t *testing.T) {
	const oid = "00000000-0000-0000-0000-000000000123"
	const scope = "/subscriptions/contract-sub"
	const path = scope + "/providers/Microsoft.Authorization/roleAssignments"
	pages := 0
	options := contractARMOptions(func(request *http.Request) (*http.Response, error) {
		require.Equal(t, http.MethodGet, request.Method)
		var body any
		switch {
		case request.URL.Query().Get("$skiptoken") == "next":
			pages++
			body = map[string]any{"value": []any{p07Assignment(scope, "proven-parent-group", "owner")}}
		case request.URL.Query().Get("$filter") == "assignedTo('"+oid+"')":
			pages++
			body = map[string]any{
				"value": []any{p07Assignment(scope+"/resourceGroups/child", "proven-child-group", "owner")},
				"nextLink": "https://management.azure.com" + path +
					"?api-version=2022-04-01&$skiptoken=next",
			}
		case request.URL.Query().Get("$filter") == "atScope()":
			body = map[string]any{"value": []any{}}
		case strings.Contains(request.URL.Path, "/roleDefinitions/"):
			body = p07Permissions(map[string]any{"actions": []string{"*"}})
		default:
			t.Fatalf("unexpected request %s", request.URL.Path)
		}
		raw, err := json.Marshal(body)
		require.NoError(t, err)
		return contractARMResponse(request, http.StatusOK, string(raw)), nil
	})
	a := &scopedAuthorizer{credential: contractCredential{}, options: options}
	status, _, err := a.basicAction(t.Context(), scope,
		callerIdentity{objectID: oid, groupOverage: true}, roleAssignmentWrite, true)
	require.NoError(t, err)
	assert.Equal(t, Pass, status)
	assert.Equal(t, 2, pages)
}

func TestOperatorGroupOverageClaims(t *testing.T) {
	for _, tc := range []struct {
		claims string
		want   bool
	}{
		{`{"oid":"operator","groups":["explicit"]}`, false},
		{`{"oid":"operator","hasgroups":true}`, true},
		{`{"oid":"operator","_claim_names":{"groups":"source"}}`, true},
		{`{"oid":"operator","_claim_names":{"other":"source"}}`, false},
	} {
		caller, err := identityFromToken("header." +
			base64.RawURLEncoding.EncodeToString([]byte(tc.claims)) + ".signature")
		require.NoError(t, err)
		assert.Equal(t, tc.want, caller.groupOverage)
		assert.False(t, caller.includes("unknown"), "overage never becomes a local membership claim")
	}
}

func TestOperatorAtScopeDoesNotAcceptOtherScopesOrMalformedManagementEvidence(t *testing.T) {
	const target = "/subscriptions/contract-sub/resourceGroups/target"
	for _, scope := range []string{
		"/subscriptions/other-sub",
		"/subscriptions/contract-sub/resourceGroups/other",
		"/providers/Microsoft.Management/managementGroups/",
		"/providers/Microsoft.Management/managementGroups/group/extra",
		"/providers/Microsoft.Management/managementGroups/group?sig=secret",
	} {
		t.Run(scope, func(t *testing.T) {
			var paths []string
			a := p07Authorizer(t, []any{p07Assignment(scope, "operator", "writer")}, nil, &paths, http.StatusOK)
			status, _, err := a.basicAction(t.Context(), target, callerIdentity{objectID: "operator"},
				roleAssignmentWrite, false)
			require.NoError(t, err)
			assert.Equal(t, Fail, status)
			require.Len(t, paths, 1, "unrelated evidence must not trigger a role-definition read")
		})
	}
	assignment := p07Assignment("/providers/Microsoft.Management/managementGroups/parent", "operator", "writer")
	assignment["id"] = "/providers/Microsoft.Management/managementGroups/other" +
		"/providers/Microsoft.Authorization/roleAssignments/assignment"
	var paths []string
	a := p07Authorizer(t, []any{assignment}, nil, &paths, http.StatusOK)
	status, _, err := a.basicAction(t.Context(), target, callerIdentity{objectID: "operator"}, roleAssignmentWrite, false)
	require.NoError(t, err)
	assert.Equal(t, Fail, status, "assignment ID must agree with its management-group scope")
}
