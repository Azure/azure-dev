// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package onboarding

import (
	"context"
	"strings"
)

func (a *scopedAuthorizer) checkP06(
	ctx context.Context, plan Plan, caller callerIdentity, identityErr error,
) Report {
	storageScope := plan.Storage.Parent
	if plan.Storage.Supplied {
		storageScope = plan.Storage.ID
	}
	var report Report
	seen := map[string]bool{}
	for _, scope := range []string{plan.Cluster.ID, plan.Workload.Parent, storageScope} {
		key := strings.ToLower(scope)
		if seen[key] {
			continue
		}
		seen[key] = true
		check := resourceCheck("P06", scope, "unconditional operator "+roleAssignmentWrite+" for G01-G03")
		check.Missing = "readable unconditional operator " + roleAssignmentWrite
		check.Remediation = "Ask the scope administrator to provide a readable, unconditional role assignment " +
			"permitting " + roleAssignmentWrite + " at this scope or an applicable ancestor."
		if identityErr != nil || caller.objectID == "" {
			check.Observed, check.Cause = "operator object ID is unavailable", identityErr
			check.Missing = "operator object ID"
		} else {
			check.Status, check.Observed, check.Cause = a.basicAssignmentWrite(ctx, scope, caller, true)
			if check.Status == Pass {
				check.Missing, check.Remediation = "", ""
			}
		}
		report = append(report, check)
	}
	return report
}
