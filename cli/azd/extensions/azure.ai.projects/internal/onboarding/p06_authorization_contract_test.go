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
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func p06Plan(t *testing.T, suppliedStorage bool) Plan {
	t.Helper()
	input := contractPlanInput()
	input.RegistryID = ""
	if !suppliedStorage {
		input.StorageID = ""
	}
	plan, err := BuildPlan(input)
	require.NoError(t, err)
	plan.Operations = nil
	return plan
}

func p06CheckAtScope(t *testing.T, report Report, scope string) Check {
	t.Helper()
	for _, check := range report {
		if check.ID == "P06" && check.Scope == scope {
			return check
		}
	}
	t.Fatalf("P06 did not check %s: %+v", scope, report)
	return Check{}
}

func TestP06ScopesUseExistingResourcesAndParents(t *testing.T) {
	for _, supplied := range []bool{false, true} {
		for _, existingGroup := range []bool{false, true} {
			t.Run(fmt.Sprintf("supplied=%t/existing-group=%t", supplied, existingGroup), func(t *testing.T) {
				plan := p06Plan(t, supplied)
				group := "/subscriptions/foundry-sub/resourceGroups/new-foundry-rg"
				options := contractARMOptions(func(request *http.Request) (*http.Response, error) {
					require.Equal(t, http.MethodGet, request.Method)
					require.True(t, strings.EqualFold(group, request.URL.Path))
					if existingGroup {
						return contractARMResponse(request, http.StatusOK, `{"tags":{}}`), nil
					}
					return contractARMResponse(request, http.StatusNotFound,
						`{"error":{"code":"ResourceGroupNotFound"}}`), nil
				})
				var err error
				plan, err = ResolveExistingParents(t.Context(), plan, contractCredential{}, options)
				require.NoError(t, err)
				parent := "/subscriptions/foundry-sub"
				if existingGroup {
					parent = group
				}
				require.Equal(t, parent, plan.Workload.Parent)
				want := []string{plan.Cluster.ID, parent}
				if supplied {
					want = append(want, plan.Storage.ID)
				}
				var paths []string
				a := p07Authorizer(t, []any{
					p07Assignment("/subscriptions/aks-sub", "operator", "writer"),
					p07Assignment("/subscriptions/foundry-sub", "operator", "writer"),
					p07Assignment("/subscriptions/storage-sub", "operator", "writer"),
				}, map[string]any{
					"writer": p07Permissions(map[string]any{"actions": []string{roleAssignmentWrite}}),
				}, &paths, http.StatusOK)
				a.credential = removedChecksCredential{}
				report, err := a.Check(t.Context(), plan, Facts{KubeletObjectID: "kubelet"})
				require.NoError(t, err)
				var scopes []string
				for _, check := range report {
					if check.ID == "P06" {
						scopes = append(scopes, check.Scope)
						assert.Equal(t, Pass, check.Status, check.Observed)
						assert.True(t, check.Mandatory)
						assert.Empty(t, check.Missing)
						assert.Empty(t, check.Remediation)
					}
				}
				assert.ElementsMatch(t, want, scopes)
				require.Len(t, paths, 2*(len(want)+1),
					"one assignment/definition pair per distinct P06 scope and another pair for P07")
				for _, path := range paths {
					assert.NotContains(t, path, plan.Workload.ID)
					if !supplied {
						assert.NotContains(t, path, plan.Storage.ID)
					}
					assert.NotContains(t, path, "/denyAssignments")
				}
			})
		}
	}
}

func TestP06OrdinaryUnconditionalRolePermissions(t *testing.T) {
	for _, tc := range []struct {
		name        string
		permissions []map[string]any
		want        Status
	}{
		{"exact action", []map[string]any{{"actions": []string{roleAssignmentWrite}}}, Pass},
		{"wildcard", []map[string]any{{"actions": []string{"Microsoft.Authorization/*"}}}, Pass},
		{"read only", []map[string]any{{"actions": []string{"*/read"}}}, Fail},
		{"data action only", []map[string]any{{"dataActions": []string{roleAssignmentWrite}}}, Fail},
		{"excluded action", []map[string]any{{
			"actions": []string{"*"}, "notActions": []string{roleAssignmentWrite},
		}}, Fail},
		{"authorization writes excluded", []map[string]any{{
			"actions": []string{"*"}, "notActions": []string{"Microsoft.Authorization/*/write"},
		}}, Fail},
		{"permission entry union", []map[string]any{
			{"actions": []string{"*"}, "notActions": []string{roleAssignmentWrite}},
			{"actions": []string{roleAssignmentWrite}},
		}, Pass},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plan := p06Plan(t, true)
			var paths []string
			a := p07Authorizer(t, []any{p07Assignment(plan.Cluster.ID, "operator", "writer")},
				map[string]any{"writer": p07Permissions(tc.permissions...)}, &paths, http.StatusOK)
			report := a.checkP06(t.Context(), plan, callerIdentity{objectID: "operator"}, nil)
			check := p06CheckAtScope(t, report, plan.Cluster.ID)
			assert.Equal(t, tc.want, check.Status, check.Observed)
			assert.Equal(t, tc.want != Pass, Report{check}.Err() != nil)
		})
	}
}

func TestP06IgnoresConditionedOrDelegatedAssignments(t *testing.T) {
	for _, property := range []string{"condition", "conditionVersion", "delegatedManagedIdentityResourceId"} {
		for _, ordinary := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/ordinary=%t", property, ordinary), func(t *testing.T) {
				plan := p06Plan(t, true)
				conditioned := p07Assignment(plan.Cluster.ID, "operator", "conditioned")
				cleanupObject(t, conditioned["properties"])[property] = "unsupported"
				assignments := []any{conditioned}
				if ordinary {
					assignments = append(assignments, p07Assignment(plan.Cluster.ID, "operator", "writer"))
				}
				var paths []string
				a := p07Authorizer(t, assignments, map[string]any{
					"writer": p07Permissions(map[string]any{"actions": []string{roleAssignmentWrite}}),
				}, &paths, http.StatusOK)
				report := a.checkP06(t.Context(), plan, callerIdentity{objectID: "operator"}, nil)
				check := p06CheckAtScope(t, report, plan.Cluster.ID)
				if ordinary {
					assert.Equal(t, Pass, check.Status, check.Observed)
				} else {
					assert.Equal(t, Fail, check.Status, check.Observed)
					assert.Contains(t, check.Observed, "no applicable unconditional operator role")
					require.Error(t, report.Err())
				}
				for _, path := range paths {
					assert.NotContains(t, path, "/roleDefinitions/conditioned")
					assert.NotContains(t, path, "/denyAssignments")
				}
			})
		}
	}
}

func TestP06RoleUnionIgnoresUnsupportedConditionalDetails(t *testing.T) {
	plan := p06Plan(t, true)
	conditioned := p07Assignment(plan.Cluster.ID, "operator", "conditioned")
	properties := cleanupObject(t, conditioned["properties"])
	properties["condition"] = "unsupported"
	delete(properties, "principalId")
	delete(properties, "roleDefinitionId")
	var paths []string
	a := p07Authorizer(t, []any{
		conditioned,
		p07Assignment(plan.Cluster.Parent, "operator", "excluded"),
		p07Assignment("/subscriptions/aks-sub", "operator", "writer"),
	}, map[string]any{
		"excluded": p07Permissions(map[string]any{
			"actions": []string{"*"}, "notActions": []string{roleAssignmentWrite},
		}),
		"writer": p07Permissions(map[string]any{"actions": []string{roleAssignmentWrite}}),
	}, &paths, http.StatusOK)
	check := p06CheckAtScope(t,
		a.checkP06(t.Context(), plan, callerIdentity{objectID: "operator"}, nil), plan.Cluster.ID)
	assert.Equal(t, Pass, check.Status, check.Observed)
	require.NoError(t, Report{check}.Err())
}

func TestP06ScopeAndExplicitTokenGroupMembership(t *testing.T) {
	for _, tc := range []struct {
		name, scope, principal string
		want                   Status
	}{
		{"direct", "cluster", "operator", Pass},
		{"inherited group", "group", "MEMBER-GROUP", Pass},
		{"inherited subscription", "subscription", "operator", Pass},
		{"nonmember group", "group", "nonmember-group", Fail},
		{"different resource group", "other-group", "operator", Fail},
		{"different subscription", "other-subscription", "operator", Fail},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plan := p06Plan(t, true)
			scope := map[string]string{
				"cluster": plan.Cluster.ID, "group": plan.Cluster.Parent,
				"subscription": "/subscriptions/aks-sub", "other-group": "/subscriptions/aks-sub/resourceGroups/other",
				"other-subscription": "/subscriptions/foundry-sub",
			}[tc.scope]
			claims := base64.RawURLEncoding.EncodeToString([]byte(`{"oid":"operator","groups":["member-group"]}`))
			caller, err := identityFromToken("header." + claims + ".signature")
			require.NoError(t, err)
			var paths []string
			a := p07Authorizer(t, []any{p07Assignment(scope, tc.principal, "writer")},
				map[string]any{"writer": p07Permissions(map[string]any{"actions": []string{roleAssignmentWrite}})},
				&paths, http.StatusOK)
			check := p06CheckAtScope(t, a.checkP06(t.Context(), plan, caller, nil), plan.Cluster.ID)
			assert.Equal(t, tc.want, check.Status, check.Observed)
		})
	}
}

func TestP06ReadFailuresAndIncompleteEvidenceRemainUnverified(t *testing.T) {
	for _, stage := range []string{"assignments", "definitions"} {
		for _, status := range []int{http.StatusForbidden, http.StatusNotFound, http.StatusOK} {
			t.Run(fmt.Sprintf("%s/status=%d", stage, status), func(t *testing.T) {
				plan := p06Plan(t, true)
				var paths []string
				a := p07Authorizer(t, []any{p07Assignment(plan.Cluster.ID, "operator", "writer")},
					nil, &paths, http.StatusOK)
				base := a.options.Transport
				a.options = contractARMOptions(func(request *http.Request) (*http.Response, error) {
					target := strings.HasSuffix(request.URL.Path, "/roleAssignments")
					if stage == "definitions" {
						target = strings.Contains(request.URL.Path, "/roleDefinitions/")
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
				check := p06CheckAtScope(t,
					a.checkP06(t.Context(), plan, callerIdentity{objectID: "operator"}, nil), plan.Cluster.ID)
				assert.Equal(t, Unverified, check.Status, check.Observed)
				require.Error(t, Report{check}.Err())
				require.Error(t, check.Cause)
				if status != http.StatusOK {
					service, ok := errors.AsType[*azcore.ResponseError](check.Cause)
					require.True(t, ok)
					assert.Equal(t, status, service.StatusCode)
				}
			})
		}
	}
	for _, missing := range []string{"principalId", "scope", "roleDefinitionId", "permissions"} {
		t.Run("missing/"+missing, func(t *testing.T) {
			plan := p06Plan(t, true)
			assignment := p07Assignment(plan.Cluster.ID, "operator", "writer")
			delete(cleanupObject(t, assignment["properties"]), missing)
			var paths []string
			a := p07Authorizer(t, []any{assignment},
				map[string]any{"writer": map[string]any{"properties": map[string]any{}}}, &paths, http.StatusOK)
			check := p06CheckAtScope(t,
				a.checkP06(t.Context(), plan, callerIdentity{objectID: "operator"}, nil), plan.Cluster.ID)
			assert.Equal(t, Unverified, check.Status, check.Observed)
			require.Error(t, Report{check}.Err())
		})
	}
}

func TestP06MissingOperatorIdentityDoesNotReadScopes(t *testing.T) {
	plan := p06Plan(t, false)
	var paths []string
	a := p07Authorizer(t, nil, nil, &paths, http.StatusOK)
	cause := errors.New("operator token unavailable")
	report := a.checkP06(t.Context(), plan, callerIdentity{}, cause)
	require.Len(t, report, 2)
	for _, check := range report {
		assert.Equal(t, Unverified, check.Status)
		assert.Equal(t, "operator object ID", check.Missing)
	}
	require.ErrorIs(t, report.Err(), cause)
	assert.Empty(t, paths)
	publicReport, err := a.Check(t.Context(), plan, Facts{KubeletObjectID: "kubelet"})
	require.Error(t, err)
	assert.Equal(t, Unverified, p06CheckAtScope(t, publicReport, plan.Cluster.ID).Status)
	assert.Empty(t, paths, "an unreadable operator token must not trigger P06 or P07 scope reads")
}

func TestP06MissingGrantAuthorityBlocksPreflight(t *testing.T) {
	plan := p06Plan(t, true)
	for _, deniedScope := range []string{plan.Cluster.ID, plan.Workload.Parent, plan.Storage.ID} {
		t.Run(deniedScope, func(t *testing.T) {
			assignments := []any{}
			for _, scope := range []string{plan.Cluster.ID, plan.Workload.Parent, plan.Storage.ID} {
				if scope != deniedScope {
					assignments = append(assignments, p07Assignment(scope, "operator", "writer"))
				}
			}
			var paths []string
			a := p07Authorizer(t, assignments,
				map[string]any{"writer": p07Permissions(map[string]any{"actions": []string{roleAssignmentWrite}})},
				&paths, http.StatusOK)
			a.credential = removedChecksCredential{}
			pipeline := Pipeline{
				Resources: contractResources(func(context.Context, Plan) (Facts, Report, error) {
					return Facts{KubeletObjectID: "kubelet"}, nil, nil
				}),
				Access: a,
			}
			_, report, err := pipeline.Preflight(t.Context(), plan)
			require.Error(t, err)
			assert.Equal(t, Fail, p06CheckAtScope(t, report, deniedScope).Status)
			require.NotEmpty(t, paths, "preflight must read real authorization evidence")
		})
	}
}
