// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package onboarding

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const ownerRole = testSubscription +
	"/providers/Microsoft.Authorization/roleDefinitions/8e3af657-a8ff-443c-a75c-2fe8c4bcb635"
const blobOwnerRole = testSubscription +
	"/providers/Microsoft.Authorization/roleDefinitions/b7e6dc6d-f1e8-4753-8033-0f276bb0955b"
const federationRoleGUID = "7e559ce2-48d7-4b27-9128-fa1b247f1308"
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
				assignment(scope, "hosting-principal", ownerRole),
				assignment(scope, "workload-principal", blobOwnerRole),
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
		return readReply{}, false
	}
	return fake
}

func suppliedFacts() Facts {
	return Facts{
		HostingIdentity:  Identity{ID: testHosting, PrincipalID: "hosting-principal"},
		WorkloadIdentity: Identity{ID: testWorkload, PrincipalID: "workload-principal"},
	}
}

func TestSuppliedGrantChecksRequireAllSuppliedSelections(t *testing.T) {
	for _, id := range []string{"hosting-federation", "workload-blob"} {
		for _, state := range []string{"unknown", "supplied"} {
			t.Run(id+"/"+state, func(t *testing.T) {
				plan, facts := suppliedPlan(t), suppliedFacts()
				target := &plan.Workload
				if id == "workload-blob" {
					target = &plan.Storage
				}
				target.Supplied = state == "supplied"
				fake := authorizationFake()
				fake.replies[strings.ToLower(target.ID+assignmentSuffix)] =
					readReply{200, map[string]any{"value": []any{}}}
				authorizer := NewAuthorizer(testCredential{}, fake.options()).(*scopedAuthorizer)
				report := authorizer.suppliedIdentityChecks(t.Context(), plan, facts)
				if state != "supplied" {
					assert.Empty(t, report)
				} else {
					check := checkWithID(t, report, id)
					assert.Equal(t, Fail, check.Status)
					assert.NotEmpty(t, check.Remediation)
					assert.Contains(t, check.Remediation, "does not repair")
				}
			})
		}
	}
}

func TestIdentityEvidenceHonorsInheritanceConditionsAndPaging(t *testing.T) {
	grant := grantRequests(suppliedPlan(t), Identity{}, suppliedFacts().WorkloadIdentity)[3]
	for _, state := range []string{
		"inherited", "management group", "unrelated", "conditional", "missing", "incomplete", "paged",
	} {
		t.Run(state, func(t *testing.T) {
			fake := authorizationFake()
			scope := testGroup
			if state == "management group" {
				scope = "/providers/Microsoft.Management/managementGroups/parent"
			} else if state == "unrelated" {
				scope = testSubscription + "/resourceGroups/unrelated"
			}
			evidence := assignment(scope, "workload-principal", blobOwnerRole)
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
			status, _, err := authorizer.identityGrantAllows(t.Context(), grant)
			assert.Equal(t, state == "incomplete", err != nil)
			switch state {
			case "inherited", "management group", "paged", "unrelated":
				assert.Equal(t, Pass, status)
			case "incomplete":
				assert.Equal(t, Unverified, status)
			case "missing", "conditional":
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
		assert.Equal(t, pass, fake.count(testCluster+assignmentSuffix),
			"hosting-aks has two role requirements at the same scope")
		assert.Equal(t, pass, fake.count(testWorkload+assignmentSuffix),
			"hosting-federation requirements share raw scope evidence")
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
	report := Report{{ID: "workload-blob", Status: Unverified, Mandatory: true}}
	return report, report.Err()
}

func TestPipelineCollectsMandatoryFailuresAndPreservesServiceErrors(t *testing.T) {
	cause := errors.New("original resource read error")
	called := false
	pipeline := Pipeline{
		Resources: resourceStub{report: Report{{ID: "storage-type", Status: Fail, Mandatory: true, Cause: cause}}},
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
