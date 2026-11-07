// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package onboarding

import (
	"context"
	"fmt"
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
			report = append(report, SkippedCheck(grant.checkID, principal.diagnosticScope(),
				"supplied identity grant requirements", missing))
			continue
		}
		if missing := target.Missing + target.LifecycleMissing; missing != "" {
			report = append(report, SkippedCheck(grant.checkID, target.diagnosticScope(),
				"supplied identity access to its dependency", missing))
			continue
		}
		check := resourceCheck(grant.checkID, grant.scope,
			"supplied identity "+grant.principalID+" has "+grant.roleName+" or an accepted built-in role")
		if grant.principalID == "" {
			check.Observed, check.Missing = "supplied identity metadata is unavailable", "verified identity principal ID"
			report = append(report, check)
			continue
		}
		check.Status, check.Observed, check.Cause = e.identityGrantAllows(ctx, grant)
		if check.Status != Pass && check.Status != NotApplicable {
			check.Missing = "readable applicable role assignment with an accepted role ID and no condition"
			check.Remediation = "Have the resource administrator configure the supplied identity's required permissions; " +
				"azd does not repair customer-preexisting grants."
		}
		report = append(report, check)
	}
	return report
}

func (e *AuthorizationEvidence) identityGrantAllows(
	ctx context.Context, grant grantRequest,
) (Status, string, error) {
	assignments, err := e.roleAssignments(ctx, grant.scope, false)
	if err != nil {
		return Unverified, "supplied identity role assignments cannot be read", err
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
			return Pass, "verified supplied-identity role assignment with an accepted role ID and no condition", nil
		}
	}
	return Fail, fmt.Sprintf("no accepted role assignment without a condition for identity %s and role %s",
		grant.principalID, grant.roleName), nil
}
