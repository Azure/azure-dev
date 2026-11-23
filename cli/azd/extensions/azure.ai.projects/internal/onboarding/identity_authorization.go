// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package onboarding

import (
	"context"
	"strings"
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
		check := resourceCheck(grant.checkID, grant.scope, grant.resourceType(),
			"Managed identity principal could not be verified")
		if grant.principalID == "" {
			report = append(report, check)
			continue
		}
		status, reason, cause := e.identityGrantAllows(ctx, grant)
		check = resourceResult(check, status, reason)
		check.Cause = cause
		report = append(report, check)
	}
	return report
}

func (e *AuthorizationEvidence) identityGrantAllows(
	ctx context.Context, grant grantRequest,
) (Status, string, error) {
	assignments, err := e.roleAssignments(ctx, grant.scope, false)
	if err != nil {
		return Unverified, grant.roleName + " role assignments could not be read", err
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
			return Pass, "", nil
		}
	}
	return Fail, "Required " + grant.roleName + " permission is missing or conditional", nil
}
