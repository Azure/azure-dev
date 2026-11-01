// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package onboarding

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/authorization/armauthorization/v2"
)

var operatorObjectIDPattern = regexp.MustCompile(`(?i)^[0-9a-f]{8}(-[0-9a-f]{4}){3}-[0-9a-f]{12}$`)

func (e *authorizationEvidence) filteredAssignments(
	ctx context.Context, scope, filter string,
) ([]*armauthorization.RoleAssignment, error) {
	return e.assignments.get(strings.ToLower(scope)+"|"+filter, func() ([]*armauthorization.RoleAssignment, error) {
		return e.readAssignments(ctx, scope, filter)
	})
}

func (e *authorizationEvidence) readAssignments(
	ctx context.Context, scope, filter string,
) ([]*armauthorization.RoleAssignment, error) {
	id, err := arm.ParseResourceID(scope)
	if err != nil || id.SubscriptionID == "" {
		return nil, fmt.Errorf("authorization requires a complete subscription scope: %s", scope)
	}
	client, err := e.assignmentClients.get(strings.ToLower(id.SubscriptionID),
		func() (*armauthorization.RoleAssignmentsClient, error) {
			return armauthorization.NewRoleAssignmentsClient(id.SubscriptionID, e.credential, e.options)
		})
	if err != nil {
		return nil, err
	}
	pager := client.NewListForScopePager(scope,
		&armauthorization.RoleAssignmentsClientListForScopeOptions{Filter: new(filter)})
	var assignments []*armauthorization.RoleAssignment
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("read applicable role assignments at %s: %w", scope, err)
		}
		assignments = append(assignments, page.Value...)
	}
	return assignments, nil
}

// assignmentApplies consumes only assignments returned by the target's ARM scope query.
// ARM establishes management-group ancestry; its resource IDs are not subscription path prefixes.
func assignmentApplies(assignment *armauthorization.RoleAssignment, scope string) bool {
	p := assignment.Properties
	if scopeContains(authString(p.Scope), scope) {
		return true
	}
	parent := authString(p.Scope)
	parts := strings.Split(parent, "/")
	if len(parts) != 5 || parts[0] != "" || !strings.EqualFold(parts[1], "providers") ||
		!strings.EqualFold(parts[2], "Microsoft.Management") || !strings.EqualFold(parts[3], "managementGroups") ||
		parts[4] == "" || strings.ContainsAny(parts[4], "?#%\\@:\x00 \t\r\n") {
		return false
	}
	id, err := arm.ParseResourceID(authString(assignment.ID))
	return err == nil && id.Name != "" && strings.EqualFold(id.ResourceType.String(),
		"Microsoft.Authorization/roleAssignments") && strings.EqualFold(id.Parent.String(), parent)
}

func (a *scopedAuthorizer) basicAction(
	ctx context.Context, scope string, caller callerIdentity, action string, unconditionalOnly bool,
) (Status, string, error) {
	reads := a.evidence()
	assignments, err := reads.filteredAssignments(ctx, scope, "atScope()")
	if err != nil {
		return Unverified, "operator role assignments could not be read", err
	}
	provenAssignments := map[*armauthorization.RoleAssignment]bool{}
	if caller.groupOverage {
		if !operatorObjectIDPattern.MatchString(caller.objectID) {
			return Unverified, "operator object ID is invalid for ARM group-assignment evidence", nil
		}
		proven, err := reads.filteredAssignments(ctx, scope,
			"assignedTo('"+caller.objectID+"')")
		if err != nil {
			return Unverified, "ARM operator group-assignment evidence could not be read", err
		}
		for _, assignment := range proven {
			provenAssignments[assignment] = true
		}
		assignments = append(slices.Clone(assignments), proven...)
	}
	operator := "operator"
	if unconditionalOnly {
		operator = "unconditional operator"
	}
	for _, assignment := range assignments {
		if assignment == nil || assignment.Properties == nil {
			return Unverified, "operator role assignment evidence is incomplete", nil
		}
		p := assignment.Properties
		if unconditionalOnly && (authString(p.Condition) != "" || authString(p.ConditionVersion) != "" ||
			authString(p.DelegatedManagedIdentityResourceID) != "") {
			continue
		}
		if authString(p.PrincipalID) == "" {
			return Unverified, "operator role assignment evidence is incomplete", nil
		}
		if !caller.includes(*p.PrincipalID) && !provenAssignments[assignment] {
			continue
		}
		if authString(p.Scope) == "" || authString(p.RoleDefinitionID) == "" {
			return Unverified, "operator assignment scope or role definition is unavailable", nil
		}
		if !assignmentApplies(assignment, scope) {
			continue
		}
		role, err := reads.roleDefinition(ctx, *p.RoleDefinitionID)
		if err != nil {
			return Unverified, "operator role definition could not be read", err
		}
		if role.Properties == nil || role.Properties.Permissions == nil {
			return Unverified, "operator role definition permissions are unavailable", nil
		}
		if permissionAllows(role.Properties.Permissions, action) {
			return Pass, operator + " Actions/NotActions allow " + action, nil
		}
	}
	return Fail, "no applicable " + operator + " role permits " + action, nil
}

func (a *scopedAuthorizer) checkP06(
	ctx context.Context, plan Plan, facts Facts, caller callerIdentity, identityErr error,
) Report {
	var report Report
	grants := plan.Grants
	if grants == nil {
		for _, entry := range []struct {
			id       string
			resource Resource
		}{{"G01", plan.Cluster}, {"G02", plan.Workload}, {"G03", plan.Storage}} {
			if createsGrant(plan, entry.id) {
				grants = append(grants, PlannedGrant{CheckID: entry.id, Target: entry.resource})
			}
		}
	}
	seen := map[string]bool{}
	for _, grant := range grants {
		resource := grant.Target
		missing := grant.Missing
		if grant.AuthorityScope == "" {
			missing += resource.Missing + resource.LifecycleMissing
		}
		if missing != "" {
			report = append(report, SkippedCheck("P06", resource.diagnosticScope(),
				"role-assignment authority for "+grant.CheckID, missing))
			continue
		}
		scope := grant.AuthorityScope
		if scope == "" {
			scope = facts.scope(resource)
		}
		key := strings.ToLower(scope)
		if scope == "" {
			check := resourceCheck("P06", resource.ID, "unconditional operator "+roleAssignmentWrite+" for "+grant.CheckID)
			check.Observed = "current resource existence is unverified; permission scope cannot be selected"
			report = append(report, check)
			continue
		}
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
			check.Status, check.Observed, check.Cause = a.basicAction(ctx, scope, caller, roleAssignmentWrite, true)
			if check.Status == Pass {
				check.Observed += "; ARM authorizes the actual assignment"
				check.Missing, check.Remediation = "", ""
			}
		}
		report = append(report, check)
	}
	return report
}

func (a *scopedAuthorizer) checkP07(
	ctx context.Context, plan Plan, facts Facts, caller callerIdentity, identityErr error,
) Check {
	resource := plan.Registry
	if plan.RegistryGrantMissing != "" {
		return SkippedCheck("P07", resource.diagnosticScope(), "native pull grant authority", plan.RegistryGrantMissing)
	}
	if plan.RegistryGrantPlanned != nil && !*plan.RegistryGrantPlanned {
		return resourceResult(resourceCheck("P07", resource.diagnosticScope(), "native pull grant authority"),
			NotApplicable, "the actual template creates no native registry pull role assignment")
	}
	if missing := resource.Missing + resource.LifecycleMissing; missing != "" {
		return SkippedCheck("P07", resource.diagnosticScope(), "native kubelet pull grant authority", missing)
	}
	if plan.Cluster.Missing != "" {
		return SkippedCheck("P07", resource.diagnosticScope(), "native kubelet pull grant authority",
			"actual AKS kubelet metadata cannot be selected: "+plan.Cluster.Missing)
	}
	scope := facts.scope(resource)
	check := resourceCheck("P07", scope, "operator "+roleAssignmentWrite+" for the native kubelet pull grant")
	check.Missing = "readable operator " + roleAssignmentWrite
	check.Remediation = "Ask the scope administrator to provide readable operator role assignments " +
		"permitting the native registry pull assignment at this scope or an applicable ancestor."
	if scope == "" {
		check.Scope, check.Observed = resource.ID, "current registry existence is unverified"
		return check
	}
	if facts.KubeletObjectID == "" {
		check.Missing = "AKS kubelet object ID"
		return check
	}
	if identityErr != nil || caller.objectID == "" {
		check.Observed, check.Cause = "operator object ID is unavailable", identityErr
		check.Missing = "operator object ID"
		return check
	}
	check.Status, check.Observed, check.Cause = a.basicAction(ctx, scope, caller, roleAssignmentWrite, false)
	if check.Status == Pass {
		check.Observed += "; ARM authorizes the actual assignment"
		check.Missing, check.Remediation = "", ""
	}
	return check
}
