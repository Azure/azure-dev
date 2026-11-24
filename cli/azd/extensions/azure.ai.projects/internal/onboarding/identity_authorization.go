// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package onboarding

import (
	"context"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
)

func (e *AuthorizationEvidence) suppliedIdentityChecks(ctx context.Context, plan Plan, facts Facts) Report {
	if !plan.Hosting.Supplied || !plan.Workload.Supplied || !plan.Storage.Supplied {
		return nil
	}
	var report Report
	for _, grant := range grantRequests(plan, facts.HostingIdentity, facts.WorkloadIdentity) {
		principal, target := plan.Hosting, plan.Cluster
		switch grant.checkID {
		case "hosting-federation":
			target = plan.Workload
		case "workload-blob":
			principal, target = plan.Workload, plan.Storage
		}
		if missing := principal.Missing + principal.LifecycleMissing; missing != "" {
			report = append(report, SkippedCheck(grant.checkID, principal.diagnosticScope(), grant.resourceType()))
			continue
		}
		if missing := target.Missing + target.LifecycleMissing; missing != "" {
			report = append(report, SkippedCheck(grant.checkID, target.diagnosticScope(), grant.resourceType()))
			continue
		}
		if grant.principalID == "" {
			report = append(report, resourceCheck(grant.checkID, grant.scope, grant.resourceType(),
				"Managed identity principal could not be verified"))
			continue
		}
		report = append(report, e.identityGrantCheck(ctx, grant))
	}
	return report
}

func (e *AuthorizationEvidence) identityGrantCheck(ctx context.Context, grant grantRequest) Check {
	check := resourceCheck(grant.checkID, grant.scope, grant.resourceType(),
		grant.roleName+" role assignments could not be read")
	assignments, err := e.roleAssignments(ctx, grant.scope, false)
	if err != nil {
		check.Cause = err
		// Invalid target IDs are input failures, not authorization-read diagnostics.
		scope, scopeErr := arm.ParseResourceID(grant.scope)
		check.ShowCause = scopeErr == nil && scope.SubscriptionID != ""
		return check
	}
	for _, assignment := range assignments {
		p := assignment.Properties
		if !strings.EqualFold(authString(p.PrincipalID), grant.principalID) {
			continue
		}
		if authString(p.Condition) != "" {
			continue
		}
		roleID := roleGUID(authString(p.RoleDefinitionID))
		accepted := roleID == grant.roleID
		if grant.checkID == "workload-blob" {
			accepted = accepted || roleID == "b7e6dc6d-f1e8-4753-8033-0f276bb0955b"
		} else {
			accepted = accepted || roleID == "8e3af657-a8ff-443c-a75c-2fe8c4bcb635" ||
				roleID == "b24988ac-6180-42a0-ab88-20f7382dd24c"
		}
		if accepted {
			return resourceResult(check, Pass)
		}
	}
	return resourceResult(check, Fail, "Required "+grant.roleName+" permission is missing or conditional")
}
