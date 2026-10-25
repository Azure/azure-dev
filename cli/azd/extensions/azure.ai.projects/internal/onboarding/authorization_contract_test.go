// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package onboarding

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/authorization/armauthorization/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestScopedPermissionAndCallerRules(t *testing.T) {
	permissions := []*armauthorization.Permission{
		{Actions: []*string{new("*")}, NotActions: []*string{new(roleAssignmentWrite)}},
		{Actions: []*string{new(roleAssignmentWrite)}},
	}
	assert.True(t, permissionAllows(permissions, roleAssignmentWrite), "NotActions is per granting permission")
	assert.False(t, permissionAllows(permissions[:1], roleAssignmentWrite))
	claims := base64.RawURLEncoding.EncodeToString([]byte(`{"oid":"operator","groups":["group"]}`))
	caller, err := identityFromToken("header." + claims + ".signature")
	require.NoError(t, err)
	require.True(t, caller.includes("GROUP"))
	require.False(t, scopeContains("/subscriptions/a/resourceGroups/rg", "/subscriptions/a/resourceGroups/rg-other"))
}

func TestExistingParentDiscoveryUsesScopedPermissions(t *testing.T) {
	for _, exists := range []bool{false, true} {
		t.Run(fmt.Sprint(exists), func(t *testing.T) {
			input := contractPlanInput()
			input.StorageID, input.RegistryID = "", ""
			plan, err := BuildPlan(input)
			require.NoError(t, err)
			options := contractARMOptions(func(request *http.Request) (*http.Response, error) {
				assert.Equal(t, http.MethodGet, request.Method)
				if !exists {
					return contractARMResponse(request, 404, `{"error":{"code":"ResourceGroupNotFound"}}`), nil
				}
				return contractARMResponse(request, 200, `{"id":"`+request.URL.Path+`","location":"eastus"}`), nil
			})
			refined, err := ResolveExistingParents(t.Context(), plan, contractCredential{}, options)
			require.NoError(t, err)
			want := "/subscriptions/" + plan.SubscriptionID
			if exists {
				want += "/resourceGroups/" + plan.ResourceGroup
			}
			assert.Equal(t, want, refined.Hosting.Parent)
			assert.Equal(t, want, refined.Workload.Parent)
			assert.Equal(t, want, refined.Storage.Parent)
			assert.Equal(t, want, refined.Registry.Parent)
			for _, op := range refined.Operations {
				if strings.Contains(op.Action, "/deployments/") {
					assert.Equal(t, "/subscriptions/"+plan.SubscriptionID, op.Scope)
				}
				if op.CheckID == "P02" || op.CheckID == "P04" || op.CheckID == "P05" {
					assert.Equal(t, want, op.Scope)
				}
			}
			assert.NotEqual(t, "", plan.Hosting.Parent)
		})
	}
}

func basicOperatorPlan(t *testing.T) (Plan, Facts) {
	t.Helper()
	input := contractPlanInput()
	input.StorageID, input.RegistryID, input.AgentSubnetID = "", "", ""
	plan, err := BuildPlan(input)
	require.NoError(t, err)
	return plan, Facts{KubeletObjectID: "kubelet"}
}

func TestP02P05BasicActionsIgnoreComplexEvidence(t *testing.T) {
	basePlan, facts := basicOperatorPlan(t)
	for _, op := range basePlan.Operations {
		for _, evidence := range []string{
			"ordinary", "unsupported condition", "condition version", "delegated identity", "future recipient", "unknown group",
		} {
			t.Run(op.CheckID+"/"+op.Action+"/"+evidence, func(t *testing.T) {
				plan := basePlan
				plan.Operations = []Operation{op}
				assignment := p07Assignment(op.Scope, "operator", "writer")
				properties := cleanupObject(t, assignment["properties"])
				switch evidence {
				case "unsupported condition":
					properties["condition"], properties["conditionVersion"] = "unsupported", "2.0"
				case "condition version":
					properties["conditionVersion"] = "unknown"
				case "delegated identity":
					properties["delegatedManagedIdentityResourceId"] = plan.Workload.ID
				case "future recipient":
					properties["condition"] =
						"@Request[Microsoft.Authorization/roleAssignments:PrincipalId] GuidEquals {'future-principal'}"
					properties["conditionVersion"] = "2.0"
				}
				assignments := []any{assignment}
				if evidence == "unknown group" {
					group := p07Assignment(op.Scope, "unconfirmed-group", "unreadable")
					cleanupObject(t, group["properties"])["principalType"] = "Group"
					assignments = append([]any{group}, assignments...)
				}
				var paths []string
				a := p09BasicAuthorizer(t, plan, facts, assignments,
					map[string]any{"writer": p07Permissions(map[string]any{"actions": []string{op.Action}})}, &paths)
				base := a.options.Transport
				a.options = contractARMOptions(func(request *http.Request) (*http.Response, error) {
					if strings.HasSuffix(request.URL.Path, "/denyAssignments") {
						require.Equal(t, http.MethodGet, request.Method)
						paths = append(paths, request.URL.Path)
						return contractARMResponse(request, http.StatusForbidden, `{"error":{"code":"AuthorizationFailed"}}`), nil
					}
					return base.Do(request)
				})
				report, err := a.Check(t.Context(), plan, facts)
				require.NoError(t, err)
				check := resourceRuleContractCheck(t, report, op.CheckID)
				assert.Equal(t, Pass, check.Status, check.Observed)
				assert.Equal(t, op.Scope, check.Scope)
				assert.Equal(t, op.Action, check.Expected)
				assert.True(t, check.Mandatory)
				assert.Empty(t, check.Missing)
				assert.Empty(t, check.Remediation)
				assert.Contains(t, check.Observed, "ARM authorizes the actual operation")
				for _, path := range paths {
					assert.NotContains(t, path, "/denyAssignments")
					assert.NotContains(t, path, "/roleDefinitions/unreadable")
					assert.NotContains(t, path, plan.Workload.ID, "future resources are not evidence sources")
				}
			})
		}
	}
}

func TestP02P05BasicActionsRespectPrincipalsPermissionsAndScope(t *testing.T) {
	basePlan, facts := basicOperatorPlan(t)
	for _, op := range basePlan.Operations {
		for _, tc := range []struct {
			name, principal, scope string
			permissions            []map[string]any
			secondRole             bool
			incompleteGroup        bool
			want                   Status
		}{
			{name: "direct operator", permissions: []map[string]any{{"actions": []string{op.Action}}}, want: Pass},
			{name: "explicit token group", principal: "KNOWN-GROUP",
				permissions: []map[string]any{{"actions": []string{op.Action}}}, want: Pass},
			{name: "unknown group", principal: "unconfirmed-group",
				permissions: []map[string]any{{"actions": []string{op.Action}}}, want: Fail},
			{name: "unknown group without metadata", principal: "unconfirmed-group", incompleteGroup: true, want: Fail},
			{name: "wildcard", permissions: []map[string]any{{"actions": []string{"*"}}}, want: Pass},
			{name: "NotActions", permissions: []map[string]any{
				{"actions": []string{"*"}, "notActions": []string{op.Action}},
			}, want: Fail},
			{name: "permission entry union", permissions: []map[string]any{
				{"actions": []string{"*"}, "notActions": []string{op.Action}}, {"actions": []string{op.Action}},
			}, want: Pass},
			{name: "role union", permissions: []map[string]any{
				{"actions": []string{"*"}, "notActions": []string{op.Action}},
			}, secondRole: true, want: Pass},
			{name: "data action is not ARM authority",
				permissions: []map[string]any{{"dataActions": []string{op.Action}}}, want: Fail},
			{name: "inherited subscription", scope: "subscription",
				permissions: []map[string]any{{"actions": []string{op.Action}}}, want: Pass},
			{name: "different subscription", scope: "/subscriptions/unrelated",
				permissions: []map[string]any{{"actions": []string{op.Action}}}, want: Fail},
			{name: "sibling scope", scope: op.Scope + "-other",
				permissions: []map[string]any{{"actions": []string{op.Action}}}, want: Fail},
		} {
			t.Run(op.CheckID+"/"+op.Action+"/"+tc.name, func(t *testing.T) {
				plan := basePlan
				plan.Operations = []Operation{op}
				principal, scope := tc.principal, tc.scope
				if principal == "" {
					principal = "operator"
				}
				if scope == "subscription" {
					scope = "/subscriptions/" + strings.Split(op.Scope, "/")[2]
				} else if scope == "" {
					scope = op.Scope
				}
				assignment := p07Assignment(scope, principal, "writer")
				if principal != "operator" {
					cleanupObject(t, assignment["properties"])["principalType"] = "Group"
				}
				if tc.incompleteGroup {
					delete(cleanupObject(t, assignment["properties"]), "scope")
					delete(cleanupObject(t, assignment["properties"]), "roleDefinitionId")
				}
				assignments := []any{assignment}
				if tc.secondRole {
					assignments = append(assignments, p07Assignment(op.Scope, "operator", "second"))
				}
				var paths []string
				a := p09BasicAuthorizer(t, plan, facts, assignments, map[string]any{
					"writer": p07Permissions(tc.permissions...),
					"second": p07Permissions(map[string]any{"actions": []string{op.Action}}),
				}, &paths)
				report, err := a.Check(t.Context(), plan, facts)
				check := resourceRuleContractCheck(t, report, op.CheckID)
				assert.Equal(t, tc.want, check.Status, check.Observed)
				assert.Equal(t, tc.want != Pass, err != nil)
				assert.Equal(t, op.Scope, check.Scope)
				assert.Equal(t, op.Action, check.Expected)
				assert.True(t, check.Mandatory)
				if principal == "unconfirmed-group" {
					for _, path := range paths {
						assert.NotContains(t, path, "/roleDefinitions/writer")
					}
				}
			})
		}
	}
}

func TestP02P05BasicReadFailuresStillBlock(t *testing.T) {
	basePlan, facts := basicOperatorPlan(t)
	for _, op := range basePlan.Operations {
		for _, stage := range []string{"assignments", "definitions"} {
			for _, status := range []int{http.StatusForbidden, http.StatusNotFound, http.StatusOK} {
				t.Run(fmt.Sprintf("%s/%s/%s/status=%d", op.CheckID, op.Action, stage, status), func(t *testing.T) {
					plan := basePlan
					plan.Operations = []Operation{op}
					var paths []string
					a := p09BasicAuthorizer(t, plan, facts, []any{p07Assignment(op.Scope, "operator", "writer")},
						map[string]any{"writer": p07Permissions(map[string]any{"actions": []string{op.Action}})}, &paths)
					base := a.options.Transport
					a.options = contractARMOptions(func(request *http.Request) (*http.Response, error) {
						target := request.URL.Path == op.Scope+"/providers/Microsoft.Authorization/roleAssignments"
						if stage == "definitions" {
							target = strings.HasSuffix(request.URL.Path, "/roleDefinitions/writer")
						}
						if target {
							require.Equal(t, http.MethodGet, request.Method)
							body := `{"error":{"code":"AuthorizationFailed"}}`
							if status == http.StatusOK {
								body = `{"value":`
							}
							return contractARMResponse(request, status, body), nil
						}
						return base.Do(request)
					})
					pipeline := Pipeline{
						Resources: contractResources(func(context.Context, Plan) (Facts, Report, error) {
							return facts, nil, nil
						}),
						Access: a,
					}
					_, report, err := pipeline.Preflight(t.Context(), plan)
					require.Error(t, err)
					check := resourceRuleContractCheck(t, report, op.CheckID)
					assert.Equal(t, Unverified, check.Status, check.Observed)
					assert.True(t, check.Mandatory)
					require.Error(t, check.Cause)
					if status != http.StatusOK {
						service, ok := errors.AsType[*azcore.ResponseError](check.Cause)
						require.True(t, ok)
						assert.Equal(t, status, service.StatusCode)
					}
				})
			}
		}
	}
}

func TestP02P05BasicMissingEvidenceDoesNotPass(t *testing.T) {
	basePlan, facts := basicOperatorPlan(t)
	for _, op := range basePlan.Operations {
		for _, missing := range []string{"principalId", "scope", "roleDefinitionId", "permissions", "operator token"} {
			t.Run(op.CheckID+"/"+op.Action+"/"+missing, func(t *testing.T) {
				plan := basePlan
				plan.Operations = []Operation{op}
				assignment := p07Assignment(op.Scope, "operator", "writer")
				delete(cleanupObject(t, assignment["properties"]), missing)
				definition := p07Permissions(map[string]any{"actions": []string{op.Action}})
				if missing == "permissions" {
					delete(cleanupObject(t, definition["properties"]), "permissions")
				}
				var paths []string
				a := p09BasicAuthorizer(t, plan, facts, []any{assignment}, map[string]any{"writer": definition}, &paths)
				if missing == "operator token" {
					a.credential = contractCredential{}
				}
				report, err := a.Check(t.Context(), plan, facts)
				require.Error(t, err)
				check := resourceRuleContractCheck(t, report, op.CheckID)
				assert.Equal(t, Unverified, check.Status, check.Observed)
				assert.True(t, check.Mandatory)
				if missing == "operator token" {
					assert.Empty(t, paths)
				}
			})
		}
	}
}

func TestP02P05BasicModePreservesP06UnconditionalAuthority(t *testing.T) {
	plan, facts := basicOperatorPlan(t)
	op := plan.Operations[0]
	plan.Operations = []Operation{op}
	assignments := []any{}
	for _, scope := range []string{op.Scope, plan.Cluster.ID} {
		assignment := p07Assignment(scope, "operator", "writer")
		properties := cleanupObject(t, assignment["properties"])
		properties["condition"], properties["conditionVersion"] = "unsupported", "2.0"
		assignments = append(assignments, assignment)
	}
	var paths []string
	a := p07Authorizer(t, assignments,
		map[string]any{"writer": p07Permissions(map[string]any{"actions": []string{"*"}})}, &paths, http.StatusOK)
	a.credential = p09GroupCredential{}
	report, err := a.Check(t.Context(), plan, facts)
	require.Error(t, err)
	assert.Equal(t, Pass, resourceRuleContractCheck(t, report, op.CheckID).Status)
	assert.Equal(t, Pass, resourceRuleContractCheck(t, report, "P07").Status)
	blocked, ok := errors.AsType[*BlockedError](err)
	require.True(t, ok)
	require.Len(t, blocked.Checks, 2)
	for _, check := range blocked.Checks {
		assert.Equal(t, "P06", check.ID)
		assert.Equal(t, Fail, check.Status)
	}
}
