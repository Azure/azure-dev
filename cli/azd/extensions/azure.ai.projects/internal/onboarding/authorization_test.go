// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package onboarding

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/authorization/armauthorization/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const ownerRole = testSubscription + "/providers/Microsoft.Authorization/roleDefinitions/owner"
const federationRoleGUID = "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
const assignmentSuffix = "/providers/Microsoft.Authorization/roleAssignments"

func assignment(scope, principal, role string) map[string]any {
	return map[string]any{
		"id":         scope + assignmentSuffix + "/assignment-" + principal,
		"properties": map[string]any{"scope": scope, "principalId": principal, "roleDefinitionId": role},
	}
}

func authorizationFake() *readFake {
	fake := &readFake{replies: map[string]readReply{}}
	fake.handler = func(request *http.Request) (readReply, bool) {
		path := request.URL.Path
		if _, overridden := fake.replies[strings.ToLower(path)]; overridden {
			return readReply{}, false
		}
		if strings.HasSuffix(strings.ToLower(path), strings.ToLower(assignmentSuffix)) {
			scope := path[:len(path)-len(assignmentSuffix)]
			return readReply{200, map[string]any{"value": []any{
				assignment(scope, testOperator, ownerRole),
				assignment(scope, "hosting-principal", ownerRole),
				assignment(scope, "workload-principal", ownerRole),
			}}}, true
		}
		if strings.HasSuffix(strings.ToLower(path), "/roledefinitions") {
			return readReply{200, map[string]any{"value": []any{map[string]any{
				"id": path + "/" + federationRoleGUID,
				"properties": map[string]any{
					"roleName": "Managed Identity Federated Identity Credential Contributor", "type": "BuiltInRole",
				},
			}}}}, true
		}
		if strings.Contains(strings.ToLower(path), "/roledefinitions/") {
			permissions := map[string]any{}
			switch path[strings.LastIndex(path, "/")+1:] {
			case "owner":
				permissions["actions"], permissions["dataActions"] = []string{"*"}, []string{"*"}
			case "acdd72a7-3385-48ef-bd42-f606fba81ae7":
				permissions["actions"] = []string{"*/read"}
			case "85cb6faf-e071-4c9b-8136-154b5a04f717":
				permissions["actions"] = []string{"Microsoft.KubernetesConfiguration/extensions/*"}
			case federationRoleGUID:
				permissions["actions"] = []string{
					"Microsoft.ManagedIdentity/userAssignedIdentities/federatedIdentityCredentials/*",
				}
			case "ba92f5b4-2d11-453d-a403-e96b0029c9fe":
				permissions["dataActions"] = []string{"Microsoft.Storage/storageAccounts/blobServices/containers/blobs/*"}
			default:
				return readReply{}, false
			}
			return readReply{200, map[string]any{
				"id": path, "properties": map[string]any{"permissions": []any{permissions}},
			}}, true
		}
		return readReply{}, false
	}
	return fake
}

func suppliedFacts() Facts {
	return Facts{
		TenantID:         "tenant",
		HostingIdentity:  Identity{ID: testHosting, PrincipalID: "hosting-principal"},
		WorkloadIdentity: Identity{ID: testWorkload, PrincipalID: "workload-principal"},
		Exists: map[string]bool{
			strings.ToLower(testHosting): true, strings.ToLower(testWorkload): true, strings.ToLower(testStorage): true,
		},
	}
}

func TestSuppliedGrantChecksUseCurrentExistenceWithoutRecoveryExemptions(t *testing.T) {
	for _, id := range []string{"G02", "G03"} {
		for _, state := range []string{"absent", "existing", "unknown", "supplied"} {
			t.Run(id+"/"+state, func(t *testing.T) {
				plan, facts := suppliedPlan(t), suppliedFacts()
				target := &plan.Workload
				if id == "G03" {
					target = &plan.Storage
					plan.Hosting.Supplied = false
				}
				target.Supplied = state == "supplied"
				delete(facts.Exists, strings.ToLower(target.ID))
				if state == "existing" {
					facts.Exists[strings.ToLower(target.ID)] = true
				} else if state == "absent" {
					facts.Exists[strings.ToLower(target.ID)] = false
				}
				fake := authorizationFake()
				fake.replies[strings.ToLower(target.ID+assignmentSuffix)] =
					readReply{200, map[string]any{"value": []any{}}}
				authorizer := NewAuthorizer(testCredential{}, fake.options()).(*scopedAuthorizer)
				report := authorizer.suppliedIdentityChecks(t.Context(), plan, facts)
				check := checkWithID(t, report, id)
				if state == "absent" {
					assert.Equal(t, NotApplicable, check.Status)
					assert.Zero(t, fake.count(target.ID+assignmentSuffix))
				} else {
					assert.Equal(t, Fail, check.Status)
					assert.NotEmpty(t, check.Remediation)
					assert.Contains(t, check.Remediation, "does not repair")
				}
			})
		}
	}
}

func TestRoleAssignmentPermissionUsesConcreteOrExistingParentScope(t *testing.T) {
	for _, existing := range []bool{false, true} {
		plan, facts := suppliedPlan(t), suppliedFacts()
		plan.Storage.Supplied = false
		facts.Exists[strings.ToLower(testStorage)] = existing
		fake := authorizationFake()
		authorizer := NewAuthorizer(testCredential{}, fake.options()).(*scopedAuthorizer)
		report := authorizer.checkP06(t.Context(), plan, facts, callerIdentity{objectID: testOperator}, nil)
		require.Len(t, report, 1)
		require.NoError(t, report.Err())
		expected := testGroup
		if existing {
			expected = testStorage
		}
		assert.Equal(t, expected, report[0].Scope)
		assert.Equal(t, 1, fake.count(expected+assignmentSuffix))
	}
	for _, allowed := range []bool{true, false} {
		t.Run("known ancestor with unresolved resource/"+fmt.Sprint(allowed), func(t *testing.T) {
			plan, facts := suppliedPlan(t), suppliedFacts()
			plan.Grants = []PlannedGrant{{
				CheckID: "G03", Target: Resource{Origin: "Storage in edited module", Missing: "runtime Storage name"},
				AuthorityScope: testGroup, DeploymentScope: testGroup,
			}}
			fake := authorizationFake()
			if !allowed {
				fake.replies[strings.ToLower(testGroup+assignmentSuffix)] =
					readReply{200, map[string]any{"value": []any{}}}
			}
			authorizer := NewAuthorizer(testCredential{}, fake.options()).(*scopedAuthorizer)
			report := authorizer.checkP06(t.Context(), plan, facts, callerIdentity{objectID: testOperator}, nil)
			require.Len(t, report, 1)
			assert.Equal(t, testGroup, report[0].Scope)
			if allowed {
				assert.Equal(t, Pass, report[0].Status)
			} else {
				assert.Equal(t, Fail, report[0].Status)
				require.Error(t, report.Err())
			}
			assert.Equal(t, 1, fake.count(testGroup+assignmentSuffix))
		})
	}
	for _, field := range []string{"condition", "conditionVersion", "delegatedManagedIdentityResourceId"} {
		t.Run(field, func(t *testing.T) {
			plan, facts := suppliedPlan(t), suppliedFacts()
			plan.Storage.Supplied = false
			fake := authorizationFake()
			grant := assignment(testStorage, testOperator, ownerRole)
			grant["properties"].(map[string]any)[field] = "unsupported"
			fake.replies[strings.ToLower(testStorage+assignmentSuffix)] =
				readReply{200, map[string]any{"value": []any{grant}}}
			authorizer := NewAuthorizer(testCredential{}, fake.options()).(*scopedAuthorizer)
			report := authorizer.checkP06(t.Context(), plan, facts, callerIdentity{objectID: testOperator}, nil)
			require.Error(t, report.Err())
			assert.Equal(t, Fail, report[0].Status)
			assert.Contains(t, report[0].Missing, "unconditional")
		})
	}
}

func TestSupportingOperationsKeepReferencesReadOnlyAndDefaultWrites(t *testing.T) {
	plan, facts := suppliedPlan(t), suppliedFacts()
	operations, err := supportingOperations(plan, facts)
	require.NoError(t, err)
	require.Len(t, operations, 4)
	for _, operation := range operations {
		assert.Equal(t, "P02", operation.checkID)
		assert.NotContains(t, operation.action, "/write")
	}
	plan.Storage.Supplied = false
	operations, err = supportingOperations(plan, facts)
	require.NoError(t, err)
	assert.Contains(t, operations, operation{"P04", "Microsoft.Storage/storageAccounts/write", testStorage})
	plan.Workload.ID = strings.Replace(testWorkload, "/target-rg/", "/other-rg/", 1)
	plan.Hosting.Supplied = false
	plan.Cluster.ID = testGroup + "/providers/Microsoft.ContainerService/managedClusters/cluster"
	plan.Cluster.Parent = testGroup
	operations, err = supportingOperations(plan, facts)
	require.NoError(t, err)
	assert.Contains(t, operations, operation{"P09", "Microsoft.Resources/deployments/write", testGroup})
	for _, action := range []string{"write", "read", "operations/read"} {
		assert.Contains(t, operations, operation{"P09", "Microsoft.Resources/deployments/" + action,
			testSubscription + "/resourceGroups/other-rg"})
	}
	for _, operation := range operations {
		assert.NotContains(t, operation.action, "Microsoft.ContainerRegistry")
		assert.NotContains(t, operation.action, "Microsoft.Network")
	}
}

func permission(actions, notActions, data, notData []string) *armauthorization.Permission {
	pointers := func(values []string) []*string {
		var result []*string
		for _, value := range values {
			result = append(result, new(value))
		}
		return result
	}
	return &armauthorization.Permission{
		Actions: pointers(actions), NotActions: pointers(notActions),
		DataActions: pointers(data), NotDataActions: pointers(notData),
	}
}

func TestEquivalentIdentityPermissionsPreserveActionsAndDataActions(t *testing.T) {
	read := "Microsoft.Storage/storageAccounts/read"
	blob := "Microsoft.Storage/storageAccounts/blobServices/containers/blobs/*"
	required := []*armauthorization.Permission{permission([]string{read}, nil, []string{blob}, nil)}
	for _, test := range []struct {
		name      string
		available []*armauthorization.Permission
		allowed   bool
	}{
		{"wildcard owner", []*armauthorization.Permission{permission([]string{"*"}, nil, []string{"*"}, nil)}, true},
		{"control plane does not imply data plane",
			[]*armauthorization.Permission{permission([]string{"*"}, nil, nil, nil)}, false},
		{"union of applicable roles", []*armauthorization.Permission{
			permission([]string{read}, nil, nil, nil), permission(nil, nil, []string{blob}, nil),
		}, true},
		{"NotActions denies required read",
			[]*armauthorization.Permission{permission([]string{"*"}, []string{read}, []string{"*"}, nil)}, false},
		{"NotDataActions denies required write",
			[]*armauthorization.Permission{permission([]string{"*"}, nil, []string{"*"}, []string{
				"Microsoft.Storage/storageAccounts/blobServices/containers/blobs/write",
			})}, false},
		{"missing permissions", nil, false},
	} {
		t.Run(test.name, func(t *testing.T) { assert.Equal(t, test.allowed, roleCovers(test.available, required)) })
	}
	assert.True(t, actionMatches("Microsoft.Resources/*", "Microsoft.Resources/deployments/operations/read"))
}

func TestIdentityEvidenceHonorsInheritanceConditionsAndPaging(t *testing.T) {
	grant := grantRequests(suppliedPlan(t), Identity{}, suppliedFacts().WorkloadIdentity)[3]
	for _, state := range []string{"inherited", "conditional", "missing", "incomplete", "paged"} {
		t.Run(state, func(t *testing.T) {
			fake := authorizationFake()
			scope := testGroup
			evidence := assignment(scope, "workload-principal", ownerRole)
			page := map[string]any{"value": []any{evidence}}
			switch state {
			case "conditional":
				evidence["properties"].(map[string]any)["condition"] = "unsupported"
			case "missing":
				page["value"] = []any{}
			case "incomplete":
				delete(evidence["properties"].(map[string]any), "roleDefinitionId")
			case "paged":
				fake.replies["/next-role-page"] = readReply{200, page}
				page = map[string]any{"value": []any{}, "nextLink": "https://management.azure.com/next-role-page"}
			}
			fake.replies[strings.ToLower(testStorage+assignmentSuffix)] = readReply{200, page}
			authorizer := NewAuthorizer(testCredential{}, fake.options()).(*scopedAuthorizer)
			status, _, err := authorizer.identityGrantAllows(t.Context(), "target-sub", grant)
			require.NoError(t, err)
			switch state {
			case "inherited", "paged":
				assert.Equal(t, Pass, status)
			case "conditional", "incomplete":
				assert.Equal(t, Unverified, status)
			case "missing":
				assert.Equal(t, Fail, status)
			}
		})
	}
}

func TestAuthorizationReadsAreCachedOnlyWithinOnePass(t *testing.T) {
	fake := authorizationFake()
	authorizer := NewAuthorizer(testCredential{}, fake.options())
	for pass := 1; pass <= 2; pass++ {
		report, err := authorizer.Check(t.Context(), suppliedPlan(t), suppliedFacts())
		require.NoError(t, err)
		require.NoError(t, report.Err())
		assert.Equal(t, pass, fake.count(testCluster+assignmentSuffix), "G01 has two role requirements at the same scope")
		assert.Equal(t, pass, fake.count(testWorkload+assignmentSuffix), "G02 and P02 share raw scope evidence")
		assert.Equal(t, pass, fake.count(ownerRole), "roles shared by multiple scopes are read once per pass")
	}
}

func TestOperatorGroupEvidenceAndManagementGroupAncestry(t *testing.T) {
	fake := authorizationFake()
	original := fake.handler
	fake.handler = func(request *http.Request) (readReply, bool) {
		if strings.HasSuffix(strings.ToLower(request.URL.Path), strings.ToLower(assignmentSuffix)) {
			values := []any{}
			if strings.HasPrefix(request.URL.Query().Get("$filter"), "assignedTo(") {
				scope := "/providers/Microsoft.Management/managementGroups/parent"
				values = append(values, assignment(scope, "operator-group", ownerRole))
			}
			return readReply{200, map[string]any{"value": values}}, true
		}
		return original(request)
	}
	authorizer := NewAuthorizer(testCredential{}, fake.options()).(*scopedAuthorizer)
	caller := callerIdentity{objectID: testOperator, groupOverage: true}
	status, _, err := authorizer.basicAction(t.Context(), testStorage, caller, roleAssignmentWrite, true)
	require.NoError(t, err)
	assert.Equal(t, Pass, status)
	assert.Equal(t, 2, fake.count(testStorage+assignmentSuffix))
	caller.objectID = "invalid') or ('"
	status, _, err = authorizer.basicAction(t.Context(), testStorage, caller, roleAssignmentWrite, true)
	require.NoError(t, err)
	assert.Equal(t, Unverified, status)
	for _, request := range fake.requests {
		assert.NotContains(t, request.filter, caller.objectID)
		assert.NotContains(t, request.path, "graph")
	}
}

type resourceStub struct {
	report Report
	err    error
}

func (s resourceStub) Check(context.Context, Plan) (Facts, Report, error) {
	if s.err != nil {
		return Facts{}, s.report, s.err
	}
	return Facts{}, s.report, s.report.Err()
}

type accessStub struct{ called *bool }

func (s accessStub) Check(context.Context, Plan, Facts) (Report, error) {
	*s.called = true
	report := Report{{ID: "P06", Status: Unverified, Mandatory: true}}
	return report, report.Err()
}

func TestPipelineCollectsMandatoryFailuresAndPreservesServiceErrors(t *testing.T) {
	cause := errors.New("original resource read error")
	called := false
	pipeline := Pipeline{
		Resources: resourceStub{report: Report{{ID: "S01", Status: Fail, Mandatory: true, Cause: cause}}},
		Access:    accessStub{called: &called},
	}
	_, report, err := pipeline.Preflight(t.Context(), suppliedPlan(t))
	require.ErrorIs(t, err, cause)
	assert.True(t, called)
	assert.Len(t, report, 2)
	called = false
	pipeline.Resources = resourceStub{err: context.Canceled}
	_, _, err = pipeline.Preflight(t.Context(), suppliedPlan(t))
	require.ErrorIs(t, err, context.Canceled)
	assert.False(t, called)
}
