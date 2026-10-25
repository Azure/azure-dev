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
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type p09GroupCredential struct{}

func (p09GroupCredential) GetToken(context.Context, policy.TokenRequestOptions) (azcore.AccessToken, error) {
	claims := base64.RawURLEncoding.EncodeToString([]byte(`{"oid":"operator","groups":["known-group"]}`))
	return azcore.AccessToken{Token: "header." + claims + ".signature", ExpiresOn: time.Now().Add(time.Hour)}, nil
}

func p09BasicPlan(t *testing.T, action string) (Plan, Facts, string) {
	t.Helper()
	input := contractPlanInput()
	input.StorageID, input.AgentSubnetID = "", ""
	if action != roleAssignmentWrite {
		input.RegistryID = ""
	}
	plan, err := BuildPlan(input)
	require.NoError(t, err)
	plan.Operations = nil
	scope := plan.Registry.ID
	if action != roleAssignmentWrite {
		scope = plan.Cluster.Parent
		plan.Operations = []Operation{{CheckID: "P09", Action: action, Scope: scope}}
	}
	return plan, Facts{KubeletObjectID: "kubelet"}, scope
}

func p09BasicAuthorizer(
	t *testing.T, plan Plan, facts Facts, assignments []any, definitions map[string]any, paths *[]string,
) *scopedAuthorizer {
	t.Helper()
	assignments = append(assignments,
		p07Assignment(plan.Cluster.ID, "operator", "baseline"),
		p07Assignment(plan.Workload.Parent, "operator", "baseline"))
	if plan.Registry.Supplied {
		assignments = append(assignments, p07Assignment(plan.Registry.ID, facts.KubeletObjectID, registryRoleID(false)))
	}
	if definitions == nil {
		definitions = map[string]any{}
	}
	definitions["baseline"] = p07Permissions(map[string]any{"actions": []string{roleAssignmentWrite}})
	a := p07Authorizer(t, assignments, definitions, paths, http.StatusOK)
	a.credential = p09GroupCredential{}
	return a
}

func p09CheckAtScope(t *testing.T, report Report, scope, action string) Check {
	t.Helper()
	for _, check := range report {
		if check.ID == "P09" && check.Scope == scope && strings.HasPrefix(check.Expected, action) {
			return check
		}
	}
	t.Fatalf("P09 did not check %s at %s: %+v", action, scope, report)
	return Check{}
}

func TestP09BasicActionsIgnoreComplexEvidence(t *testing.T) {
	for _, op := range []struct{ name, action string }{
		{"deployment write", "Microsoft.Resources/deployments/write"},
		{"deployment read", "Microsoft.Resources/deployments/read"},
		{"deployment operations", "Microsoft.Resources/deployments/operations/read"},
		{"registry grant", roleAssignmentWrite},
	} {
		for _, evidence := range []string{"ordinary", "future principal", "unsupported condition", "delegated identity"} {
			t.Run(op.name+"/"+evidence, func(t *testing.T) {
				plan, facts, scope := p09BasicPlan(t, op.action)
				assignment := p07Assignment(scope, "operator", "writer")
				properties := cleanupObject(t, assignment["properties"])
				switch evidence {
				case "future principal":
					properties["condition"] =
						"@Request[Microsoft.Authorization/roleAssignments:PrincipalId] GuidEquals {'future-project'}"
					properties["conditionVersion"] = "2.0"
				case "unsupported condition":
					properties["condition"], properties["conditionVersion"] = "unsupported", "unknown"
				case "delegated identity":
					properties["delegatedManagedIdentityResourceId"] = plan.Workload.ID
				}
				var paths []string
				a := p09BasicAuthorizer(t, plan, facts, []any{assignment}, map[string]any{
					"writer": p07Permissions(map[string]any{"actions": []string{op.action}}),
				}, &paths)
				report, err := a.Check(t.Context(), plan, facts)
				require.NoError(t, err)
				check := p09CheckAtScope(t, report, scope, op.action)
				assert.Equal(t, Pass, check.Status, check.Observed)
				assert.Equal(t, op.action, check.Expected)
				assert.True(t, check.Mandatory)
				assert.Empty(t, check.Missing)
				assert.Empty(t, check.Remediation)
				for _, path := range paths {
					assert.NotContains(t, path, "/denyAssignments")
					assert.NotContains(t, path, plan.Workload.ID, "future identity is not an evidence source")
				}
			})
		}
	}
}

func TestP09BasicActionsMatchOnlyKnownPrincipals(t *testing.T) {
	for _, tc := range []struct {
		name, principal string
		knownFallback   bool
		want            Status
	}{
		{"direct operator", "operator", false, Pass},
		{"explicit token group", "KNOWN-GROUP", false, Pass},
		{"unknown group", "unknown-group", false, Fail},
		{"unknown group with known allow", "unknown-group", true, Pass},
	} {
		for _, action := range []string{"Microsoft.Resources/deployments/write", roleAssignmentWrite} {
			t.Run(tc.name+"/"+action, func(t *testing.T) {
				plan, facts, scope := p09BasicPlan(t, action)
				assignment := p07Assignment(scope, tc.principal, "writer")
				cleanupObject(t, assignment["properties"])["principalType"] = "Group"
				assignments := []any{assignment}
				if tc.knownFallback {
					assignments = append(assignments, p07Assignment(scope, "operator", "writer"))
				}
				var paths []string
				a := p09BasicAuthorizer(t, plan, facts, assignments, map[string]any{
					"writer": p07Permissions(map[string]any{"actions": []string{action}}),
				}, &paths)
				report, err := a.Check(t.Context(), plan, facts)
				check := p09CheckAtScope(t, report, scope, action)
				assert.Equal(t, tc.want, check.Status, check.Observed)
				assert.Equal(t, tc.want != Pass, err != nil)
				if tc.want == Fail {
					assert.NotContains(t, paths,
						"/subscriptions/contract-sub/providers/Microsoft.Authorization/roleDefinitions/writer")
				}
			})
		}
	}
}

func TestP09BasicActionsRespectPermissionsAndScope(t *testing.T) {
	for _, action := range []string{"Microsoft.Resources/deployments/write", roleAssignmentWrite} {
		for _, tc := range []struct {
			name, scope string
			permissions []map[string]any
			secondRole  bool
			want        Status
		}{
			{"wildcard", "", []map[string]any{{"actions": []string{"*"}}}, false, Pass},
			{"NotActions", "", []map[string]any{
				{"actions": []string{"*"}, "notActions": []string{action}},
			}, false, Fail},
			{"permission entry union", "", []map[string]any{
				{"actions": []string{"*"}, "notActions": []string{action}}, {"actions": []string{action}},
			}, false, Pass},
			{"role union", "", []map[string]any{
				{"actions": []string{"*"}, "notActions": []string{action}},
			}, true, Pass},
			{"data action is not ARM authority", "", []map[string]any{{"dataActions": []string{action}}}, false, Fail},
			{"inherited subscription", "subscription", []map[string]any{{"actions": []string{action}}}, false, Pass},
			{"different scope", "/subscriptions/unrelated", []map[string]any{{"actions": []string{action}}}, false, Fail},
		} {
			t.Run(action+"/"+tc.name, func(t *testing.T) {
				plan, facts, scope := p09BasicPlan(t, action)
				assignmentScope := scope
				if tc.scope == "subscription" {
					assignmentScope = "/subscriptions/" + strings.Split(scope, "/")[2]
				} else if tc.scope != "" {
					assignmentScope = tc.scope
				}
				assignments := []any{p07Assignment(assignmentScope, "operator", "writer")}
				if tc.secondRole {
					assignments = append(assignments, p07Assignment(scope, "operator", "second"))
				}
				var paths []string
				a := p09BasicAuthorizer(t, plan, facts, assignments, map[string]any{
					"writer": p07Permissions(tc.permissions...),
					"second": p07Permissions(map[string]any{"actions": []string{action}}),
				}, &paths)
				report, err := a.Check(t.Context(), plan, facts)
				assert.Equal(t, tc.want, p09CheckAtScope(t, report, scope, action).Status)
				assert.Equal(t, tc.want != Pass, err != nil)
			})
		}
	}
}

func TestP09BasicReadFailuresStillBlock(t *testing.T) {
	for _, stage := range []string{"assignments", "definitions"} {
		for _, status := range []int{http.StatusForbidden, http.StatusNotFound, http.StatusOK} {
			t.Run(fmt.Sprintf("%s/status=%d", stage, status), func(t *testing.T) {
				action := "Microsoft.Resources/deployments/write"
				plan, facts, scope := p09BasicPlan(t, action)
				var paths []string
				a := p09BasicAuthorizer(t, plan, facts, []any{p07Assignment(scope, "operator", "writer")},
					map[string]any{"writer": p07Permissions(map[string]any{"actions": []string{action}})}, &paths)
				base := a.options.Transport
				a.options = contractARMOptions(func(request *http.Request) (*http.Response, error) {
					target := request.URL.Path == scope+"/providers/Microsoft.Authorization/roleAssignments"
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
				check := p09CheckAtScope(t, report, scope, action)
				assert.Equal(t, Unverified, check.Status, check.Observed)
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

func TestP09BasicIncompleteEvidenceDoesNotPass(t *testing.T) {
	for _, missing := range []string{"principalId", "scope", "roleDefinitionId", "permissions", "operator token"} {
		t.Run(missing, func(t *testing.T) {
			action := "Microsoft.Resources/deployments/write"
			plan, facts, scope := p09BasicPlan(t, action)
			assignment := p07Assignment(scope, "operator", "writer")
			delete(cleanupObject(t, assignment["properties"]), missing)
			definition := p07Permissions(map[string]any{"actions": []string{action}})
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
			assert.Equal(t, Unverified, p09CheckAtScope(t, report, scope, action).Status)
			if missing == "operator token" {
				assert.Empty(t, paths)
			}
		})
	}
}

func TestP09AndP02UseBasicActionMode(t *testing.T) {
	action := "Microsoft.Resources/deployments/write"
	plan, facts, scope := p09BasicPlan(t, action)
	identityOperation := Operation{
		CheckID: "P02", Action: "Microsoft.ManagedIdentity/userAssignedIdentities/write", Scope: plan.Workload.Parent,
	}
	plan.Operations = append(plan.Operations, identityOperation)
	var paths []string
	a := p09BasicAuthorizer(t, plan, facts, []any{
		p07Assignment(scope, "operator", "writer"),
		p07Assignment(identityOperation.Scope, "operator", "identity-writer"),
	}, map[string]any{
		"writer":          p07Permissions(map[string]any{"actions": []string{action}}),
		"identity-writer": p07Permissions(map[string]any{"actions": []string{identityOperation.Action}}),
	}, &paths)
	base := a.options.Transport
	var denies []string
	a.options = contractARMOptions(func(request *http.Request) (*http.Response, error) {
		if strings.HasSuffix(request.URL.Path, "/denyAssignments") {
			require.Equal(t, http.MethodGet, request.Method)
			denies = append(denies, request.URL.Path)
			return contractARMResponse(request, http.StatusForbidden, `{"error":{"code":"AuthorizationFailed"}}`), nil
		}
		return base.Do(request)
	})
	report, err := a.Check(t.Context(), plan, facts)
	require.NoError(t, err)
	assert.Equal(t, Pass, p09CheckAtScope(t, report, scope, action).Status)
	assert.Equal(t, Pass, resourceRuleContractCheck(t, report, "P02").Status)
	assert.Empty(t, denies)
}
