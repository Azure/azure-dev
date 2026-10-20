// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package onboarding

import (
	"context"
	"fmt"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/cloud"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/authorization/armauthorization/v2"
)

type scopedAuthorizer struct {
	credential azcore.TokenCredential
	options    *arm.ClientOptions
}

// NewAuthorizer evaluates scoped ARM role/deny evidence without trial writes or Graph privileges.
func NewAuthorizer(credential azcore.TokenCredential, options *arm.ClientOptions) Authorizer {
	return &scopedAuthorizer{credential: credential, options: onboardingARMOptions(options)}
}

func (a *scopedAuthorizer) Check(ctx context.Context, p Plan, f Facts) (Report, error) {
	operations := append([]Operation{}, p.Operations...)
	var report Report
	grants := grantRequests(p, f, Identity{}, Identity{})
	grants = append(grants, grantRequest{
		checkID: "F01", scope: p.Registry.ID, roleName: "Foundry project registry pull",
		roleID: registryRoleID(f.RegistryABAC),
	})
	for _, g := range grants {
		scope := g.scope
		for _, r := range []Resource{p.Workload, p.Storage, p.Registry, p.AgentSubnet} {
			if strings.EqualFold(scope, r.ID) && !r.Supplied {
				scope = r.Parent
			}
		}
		checkID := "P06"
		if g.checkID == "G04" {
			checkID = "P07"
		} else if g.checkID == "F01" {
			checkID = "P09"
		}
		roleID, err := resolveGrantRole(ctx, a.credential, a.options, p.SubscriptionID, g)
		if err != nil {
			report = append(report, Check{
				ID: checkID, Scope: scope, Status: Unverified, Mandatory: true,
				Expected: roleAssignmentWrite + "; grant " + g.roleName,
				Observed: "required role definition could not be resolved", Missing: g.roleName,
				Remediation: "Ask the scope administrator to verify the required role is available.", Cause: err,
			})
		} else {
			operations = append(operations, Operation{
				CheckID: checkID, Action: roleAssignmentWrite, Scope: scope, RoleID: roleID,
				PrincipalID: g.principalID,
			})
		}
	}
	audience := "https://management.azure.com/"
	if a.options != nil {
		if service, ok := a.options.Cloud.Services[cloud.ResourceManager]; ok && service.Audience != "" {
			audience = service.Audience
		}
	}
	token, tokenErr := a.credential.GetToken(ctx, policy.TokenRequestOptions{
		Scopes: []string{strings.TrimRight(audience, "/") + "/.default"},
	})
	caller, identityErr := identityFromToken(token.Token)
	for _, op := range operations {
		check := Check{
			ID: op.CheckID, Scope: op.Scope, Status: Unverified, Mandatory: true,
			Expected: op.Action, Missing: op.Action,
			Remediation: "Ask the scope administrator to provide readable, unconditional action and deny evidence.",
		}
		if op.RoleID != "" {
			principalType := op.PrincipalType
			if principalType == "" {
				principalType = "ServicePrincipal"
			}
			check.Expected += "; grant " + op.RoleID + " to " + principalType + " " + op.PrincipalID
		}
		switch {
		case tokenErr != nil:
			check.Observed, check.Cause = "operator token evidence unavailable", tokenErr
		case identityErr != nil:
			check.Observed, check.Cause = identityErr.Error(), identityErr
		default:
			check.Status, check.Observed, check.Cause = a.evaluate(ctx, caller, op)
		}
		if check.Status == Pass {
			check.Missing = ""
		}
		report = append(report, check)
	}
	return report, report.Err()
}

func (a *scopedAuthorizer) evaluate(
	ctx context.Context, caller callerIdentity, op Operation,
) (Status, string, error) {
	id, err := arm.ParseResourceID(op.Scope)
	if err != nil || id.SubscriptionID == "" {
		return Unverified, "operation has no supported existing ARM scope", err
	}
	assignments, err := armauthorization.NewRoleAssignmentsClient(id.SubscriptionID, a.credential, a.options)
	if err != nil {
		return Unverified, "role assignment client unavailable", err
	}
	definitions, err := armauthorization.NewRoleDefinitionsClient(a.credential, a.options)
	if err != nil {
		return Unverified, "role definition client unavailable", err
	}
	denyClient, err := armauthorization.NewDenyAssignmentsClient(id.SubscriptionID, a.credential, a.options)
	if err != nil {
		return Unverified, "deny assignment client unavailable", err
	}
	var denies []*armauthorization.DenyAssignment
	denyPager := denyClient.NewListForScopePager(op.Scope,
		&armauthorization.DenyAssignmentsClientListForScopeOptions{Filter: new("atScope()")})
	for denyPager.More() {
		page, err := denyPager.NextPage(ctx)
		if err != nil {
			return Unverified, "applicable deny evidence cannot be read", err
		}
		denies = append(denies, page.Value...)
	}
	if status, reason := evaluateDeny(caller, op, denies); status != Pass {
		return status, reason, nil
	}
	allowed, unknown := false, false
	reason := "no applicable role permits " + op.Action
	pager := assignments.NewListForScopePager(op.Scope,
		&armauthorization.RoleAssignmentsClientListForScopeOptions{Filter: new("atScope()")})
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return Unverified, "inherited role assignment evidence cannot be read", err
		}
		for _, assignment := range page.Value {
			if assignment == nil || assignment.Properties == nil {
				unknown = true
				continue
			}
			props := assignment.Properties
			if props.Scope == nil || props.PrincipalID == nil || props.RoleDefinitionID == nil {
				unknown = true
				continue
			}
			if !scopeContains(*props.Scope, op.Scope) {
				continue
			}
			member := caller.includes(*props.PrincipalID)
			if !member && (props.PrincipalType == nil || *props.PrincipalType == armauthorization.PrincipalTypeGroup) {
				unknown = true
				continue
			}
			if !member {
				continue
			}
			definition, err := definitions.GetByID(ctx, *props.RoleDefinitionID, nil)
			if err != nil {
				return Unverified, "granting role definition cannot be read", err
			}
			if definition.Properties == nil || definition.Properties.Permissions == nil {
				unknown = true
				continue
			}
			if !permissionAllows(definition.Properties.Permissions, op.Action) {
				continue
			}
			status, detail := delegationCondition(authString(props.Condition), op)
			if authString(props.Condition) != "" && authString(props.ConditionVersion) != "2.0" {
				status, detail = Unverified, "delegation condition version is not supported"
			}
			if status == Pass && authString(props.DelegatedManagedIdentityResourceID) == "" {
				allowed = true
			} else if status == Unverified || authString(props.DelegatedManagedIdentityResourceID) != "" {
				unknown, reason = true, detail
			}
		}
	}
	if allowed {
		return Pass, "scoped action allowed; applicable conditions and deny evidence checked", nil
	}
	if unknown {
		return Unverified, reason + "; group, condition or assignment evidence is incomplete", nil
	}
	return Fail, reason, nil
}

func resolveGrantRole(
	ctx context.Context, credential azcore.TokenCredential, options *arm.ClientOptions,
	subscription string, grant grantRequest,
) (string, error) {
	if scope, err := arm.ParseResourceID(grant.scope); err == nil && scope.SubscriptionID != "" {
		subscription = scope.SubscriptionID
	}
	if grant.roleID != "" {
		return "/subscriptions/" + subscription + "/providers/Microsoft.Authorization/roleDefinitions/" + grant.roleID, nil
	}
	client, err := armauthorization.NewRoleDefinitionsClient(credential, options)
	if err != nil {
		return "", err
	}
	pager := client.NewListPager("/subscriptions/"+subscription,
		&armauthorization.RoleDefinitionsClientListOptions{Filter: new("roleName eq '" + grant.roleName + "'")})
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return "", err
		}
		for _, role := range page.Value {
			if role != nil && role.ID != nil && role.Properties != nil &&
				authString(role.Properties.RoleName) == grant.roleName &&
				strings.EqualFold(authString(role.Properties.RoleType), "BuiltInRole") {
				return *role.ID, nil
			}
		}
	}
	return "", fmt.Errorf("required built-in role %q is unavailable", grant.roleName)
}
