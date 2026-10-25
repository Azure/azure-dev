// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package onboarding

import (
	"context"
	"fmt"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/authorization/armauthorization/v2"
)

func createsGrant(plan Plan, id string) bool {
	switch id {
	case "G01":
		return !plan.Hosting.Supplied
	case "G02":
		return !plan.Hosting.Supplied || !plan.Workload.Supplied
	case "G03":
		return !plan.Workload.Supplied || !plan.Storage.Supplied
	default:
		return true
	}
}

func (a *scopedAuthorizer) suppliedIdentityChecks(ctx context.Context, plan Plan, facts Facts) Report {
	var report Report
	for _, grant := range grantRequests(plan, facts, facts.HostingIdentity, facts.WorkloadIdentity) {
		needed := grant.checkID == "G01" && plan.Hosting.Supplied ||
			grant.checkID == "G02" && plan.Hosting.Supplied ||
			grant.checkID == "G03" && plan.Workload.Supplied
		if !needed {
			continue
		}
		check := resourceCheck(grant.checkID, grant.scope,
			"supplied identity "+grant.principalID+" has "+grant.roleName+" or provably equivalent inherited permissions")
		future := grant.checkID == "G02" && !plan.Workload.Supplied && !plan.Recovery.ExistingWorkload ||
			grant.checkID == "G03" && !plan.Storage.Supplied && !plan.Recovery.ExistingStorage
		pending := grant.checkID == "G02" && plan.Recovery.FederationPending ||
			grant.checkID == "G03" && plan.Recovery.StorageGrantPending
		if future {
			report = append(report, resourceResult(check, NotApplicable,
				"dependency does not yet exist; the same ARM graph will create the dependency and grant"))
			continue
		}
		if grant.principalID == "" {
			check.Observed, check.Missing = "supplied identity metadata is unavailable", "verified identity principal ID"
			report = append(report, check)
			continue
		}
		check.Status, check.Observed, check.Cause = a.identityGrantAllows(ctx, plan.SubscriptionID, grant)
		if check.Status == Fail && pending {
			check.Status = NotApplicable
			check.Observed = "exact verified unfinished prior ARM intent will complete this azd-created dependency grant"
		}
		if check.Status != Pass && check.Status != NotApplicable {
			check.Missing = "readable applicable unconditional role permissions for the supplied identity"
			check.Remediation = "Have the resource administrator configure the supplied identity's required permissions; " +
				"azd does not repair customer-preexisting grants."
		}
		report = append(report, check)
	}
	return report
}

func (a *scopedAuthorizer) identityGrantAllows(
	ctx context.Context, subscription string, grant grantRequest,
) (Status, string, error) {
	roleID, err := resolveGrantRole(ctx, a.credential, a.options, subscription, grant)
	if err != nil {
		return Unverified, "required role definition is unavailable", err
	}
	definitions, err := armauthorization.NewRoleDefinitionsClient(a.credential, a.options)
	if err != nil {
		return Unverified, "role definition client is unavailable", err
	}
	required, err := definitions.GetByID(ctx, roleID, nil)
	if err != nil {
		return Unverified, "required role definition cannot be read", err
	}
	if required.Properties == nil || len(required.Properties.Permissions) == 0 {
		return Unverified, "required role permission evidence is missing", nil
	}
	assignments, err := applicableAssignments(ctx, a.credential, a.options, grant.scope)
	if err != nil {
		return Unverified, "supplied identity role assignments cannot be read", err
	}
	var uncertain bool
	var permissions []*armauthorization.Permission
	for _, assignment := range assignments {
		if assignment == nil || assignment.Properties == nil {
			uncertain = true
			continue
		}
		p := assignment.Properties
		if !strings.EqualFold(authString(p.PrincipalID), grant.principalID) {
			continue
		}
		if authString(p.Scope) == "" || authString(p.RoleDefinitionID) == "" {
			uncertain = true
			continue
		}
		if !scopeContains(*p.Scope, grant.scope) {
			continue
		}
		if authString(p.Condition) != "" || authString(p.ConditionVersion) != "" ||
			authString(p.DelegatedManagedIdentityResourceID) != "" {
			uncertain = true
			continue
		}
		role := required.RoleDefinition
		if !strings.EqualFold(*p.RoleDefinitionID, roleID) {
			result, readErr := definitions.GetByID(ctx, *p.RoleDefinitionID, nil)
			if readErr != nil {
				return Unverified, "supplied identity role definition cannot be read", readErr
			}
			role = result.RoleDefinition
		}
		if role.Properties == nil || len(role.Properties.Permissions) == 0 {
			uncertain = true
			continue
		}
		permissions = append(permissions, role.Properties.Permissions...)
		if roleCovers(permissions, required.Properties.Permissions) {
			return Pass, "verified supplied-identity permission allow at an applicable resource/RG/subscription scope", nil
		}
	}
	if uncertain {
		return Unverified,
			"necessary identity permission evidence is missing or conditional/delegated evidence is unsupported", nil
	}
	return Fail, fmt.Sprintf("no confirmed permission allow for identity %s and role %s",
		grant.principalID, grant.roleName), nil
}

// coversPattern deliberately recognizes only provable wildcard containment, not condition expressions.
func coversPattern(allow, required string) bool {
	allow, required = strings.ToLower(allow), strings.ToLower(required)
	if allow == "*" || allow == required {
		return true
	}
	if !strings.Contains(required, "*") {
		return actionMatches(allow, required)
	}
	return strings.HasSuffix(allow, "*") && strings.Count(allow, "*") == 1 &&
		strings.HasPrefix(required, strings.TrimSuffix(allow, "*"))
}

func patternsMayOverlap(a, b string) bool {
	a, b = strings.ToLower(a), strings.ToLower(b)
	if !strings.Contains(a, "*") {
		return actionMatches(b, a)
	}
	if !strings.Contains(b, "*") {
		return actionMatches(a, b)
	}
	ap, bp := strings.Split(a, "*"), strings.Split(b, "*")
	return (strings.HasPrefix(ap[0], bp[0]) || strings.HasPrefix(bp[0], ap[0])) &&
		(strings.HasSuffix(ap[len(ap)-1], bp[len(bp)-1]) || strings.HasSuffix(bp[len(bp)-1], ap[len(ap)-1]))
}

func roleCovers(available, required []*armauthorization.Permission) bool {
	hasPermission := false
	for _, permission := range required {
		if permission == nil {
			return false
		}
		for _, data := range []bool{false, true} {
			actions, except := permission.Actions, permission.NotActions
			if data {
				actions, except = permission.DataActions, permission.NotDataActions
			}
			for _, action := range actions {
				if action == nil || strings.TrimSpace(*action) == "" {
					return false
				}
				hasPermission = true
				covered := false
				for _, candidate := range available {
					if candidate == nil {
						continue
					}
					allows, exclusions := candidate.Actions, candidate.NotActions
					if data {
						allows, exclusions = candidate.DataActions, candidate.NotDataActions
					}
					for _, allow := range allows {
						if allow == nil || !coversPattern(*allow, *action) {
							continue
						}
						blocked := false
						for _, exclusion := range exclusions {
							if exclusion == nil {
								blocked = true
								break
							}
							if !patternsMayOverlap(*exclusion, *action) {
								continue
							}
							irrelevant := false
							for _, removed := range except {
								if removed != nil && coversPattern(*removed, *exclusion) {
									irrelevant = true
								}
							}
							blocked = blocked || !irrelevant
						}
						covered = covered || !blocked
					}
				}
				if !covered {
					return false
				}
			}
		}
	}
	return hasPermission
}
