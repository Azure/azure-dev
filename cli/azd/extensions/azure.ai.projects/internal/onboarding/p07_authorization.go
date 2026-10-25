// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package onboarding

import (
	"context"
	"fmt"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/authorization/armauthorization/v2"
)

const registryPullAction = "Microsoft.ContainerRegistry/registries/pull/read"
const repositoryReadAction = "Microsoft.ContainerRegistry/registries/repositories/content/read"

func applicableAssignments(
	ctx context.Context, credential azcore.TokenCredential, options *arm.ClientOptions, scope string,
) ([]*armauthorization.RoleAssignment, error) {
	id, err := arm.ParseResourceID(scope)
	if err != nil || id.SubscriptionID == "" {
		return nil, fmt.Errorf("authorization requires a complete subscription scope: %s", scope)
	}
	client, err := armauthorization.NewRoleAssignmentsClient(id.SubscriptionID, credential, options)
	if err != nil {
		return nil, err
	}
	pager := client.NewListForScopePager(scope,
		&armauthorization.RoleAssignmentsClientListForScopeOptions{Filter: new("atScope()")})
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

func pullRoleAllows(
	ctx context.Context, credential azcore.TokenCredential, options *arm.ClientOptions, roleID string, abac bool,
) (bool, error) {
	id, err := arm.ParseResourceID(roleID)
	if err != nil || !strings.EqualFold(id.ResourceType.String(), "Microsoft.Authorization/roleDefinitions") {
		return false, fmt.Errorf("kubelet assignment has an invalid role-definition ID")
	}
	if strings.EqualFold(id.Name, registryRoleID(abac)) ||
		!abac && strings.EqualFold(id.Name, "8311e382-0749-4cb8-b61a-304f252e45ec") { // AcrPush includes pull.
		return true, nil
	}
	client, err := armauthorization.NewRoleDefinitionsClient(credential, options)
	if err != nil {
		return false, err
	}
	role, err := client.GetByID(ctx, roleID, nil)
	if err != nil {
		return false, fmt.Errorf("read kubelet role definition: %w", err)
	}
	if role.Properties == nil || role.Properties.Permissions == nil {
		return false, fmt.Errorf("kubelet role definition permissions are unavailable")
	}
	if !abac {
		return permissionAllows(role.Properties.Permissions, registryPullAction), nil
	}
	for _, permission := range role.Properties.Permissions {
		if permission != nil && anyAction(permission.DataActions, repositoryReadAction) &&
			!anyAction(permission.NotDataActions, repositoryReadAction) {
			return true, nil
		}
	}
	return false, nil
}

func reusedKubeletGrantScope(raw, registry string) bool {
	id, err := arm.ParseResourceID(raw)
	if err != nil || !validResourceIDSyntax(raw) || id.Name == "" ||
		!strings.EqualFold(id.ResourceType.String(), "Microsoft.Authorization/roleAssignments") {
		return false
	}
	target, err := ParseID(registry, "Microsoft.ContainerRegistry/registries")
	if err != nil {
		return false
	}
	scope := id.Parent.String()
	return strings.EqualFold(scope, registry) || strings.EqualFold(scope, target.Parent.String()) ||
		strings.EqualFold(scope, "/subscriptions/"+target.SubscriptionID)
}

func existingKubeletPullGrant(
	ctx context.Context, credential azcore.TokenCredential, options *arm.ClientOptions,
	plan Plan, facts Facts,
) (string, error) {
	if facts.KubeletObjectID == "" {
		return "", fmt.Errorf("AKS kubelet object ID is unavailable")
	}
	assignments, err := applicableAssignments(ctx, credential, options, plan.Registry.ID)
	if err != nil {
		return "", err
	}
	for _, assignment := range assignments {
		if assignment == nil || assignment.Properties == nil || authString(assignment.Properties.PrincipalID) == "" {
			return "", fmt.Errorf("kubelet role assignment evidence is incomplete")
		}
		p := assignment.Properties
		if !strings.EqualFold(*p.PrincipalID, facts.KubeletObjectID) {
			continue
		}
		if authString(p.Scope) == "" || authString(p.RoleDefinitionID) == "" {
			return "", fmt.Errorf("kubelet assignment scope or role definition is unavailable")
		}
		if !scopeContains(*p.Scope, plan.Registry.ID) {
			continue
		}
		id := authString(assignment.ID)
		if !reusedKubeletGrantScope(id, plan.Registry.ID) {
			return "", fmt.Errorf("kubelet role assignment has an invalid applicable resource ID")
		}
		parsed, _ := arm.ParseResourceID(id)
		if !strings.EqualFold(parsed.Parent.String(), *p.Scope) {
			return "", fmt.Errorf("kubelet assignment ID and scope do not match")
		}
		allowed, err := pullRoleAllows(ctx, credential, options, *p.RoleDefinitionID, facts.RegistryABAC)
		if err != nil {
			return "", err
		}
		if allowed {
			return id, nil
		}
	}
	return "", nil
}

func (a *scopedAuthorizer) basicAssignmentWrite(
	ctx context.Context, scope string, caller callerIdentity, unconditionalOnly bool,
) (Status, string, error) {
	status, observed, err := a.basicAction(ctx, scope, caller, roleAssignmentWrite, unconditionalOnly)
	if status == Pass {
		observed += "; ARM authorizes the actual assignment"
	}
	return status, observed, err
}

func (a *scopedAuthorizer) basicAction(
	ctx context.Context, scope string, caller callerIdentity, action string, unconditionalOnly bool,
) (Status, string, error) {
	assignments, err := applicableAssignments(ctx, a.credential, a.options, scope)
	if err != nil {
		return Unverified, "operator role assignments could not be read", err
	}
	client, err := armauthorization.NewRoleDefinitionsClient(a.credential, a.options)
	if err != nil {
		return Unverified, "operator role definition client unavailable", err
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
		if !caller.includes(*p.PrincipalID) {
			continue
		}
		if authString(p.Scope) == "" || authString(p.RoleDefinitionID) == "" {
			return Unverified, "operator assignment scope or role definition is unavailable", nil
		}
		if !scopeContains(*p.Scope, scope) {
			continue
		}
		role, err := client.GetByID(ctx, *p.RoleDefinitionID, nil)
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

func (a *scopedAuthorizer) checkP07(
	ctx context.Context, plan Plan, facts Facts, caller callerIdentity, identityErr error,
) Check {
	scope := plan.Registry.Parent
	if plan.Registry.Supplied {
		scope = plan.Registry.ID
	}
	check := resourceCheck("P07", scope, "existing kubelet registry pull access or operator "+roleAssignmentWrite)
	check.Missing = "readable kubelet pull authorization or operator " + roleAssignmentWrite
	check.Remediation = "Ask the scope administrator to verify existing kubelet pull access or provide " +
		"readable operator role assignments permitting the stated action."
	if facts.KubeletObjectID == "" {
		check.Missing = "AKS kubelet object ID"
		return check
	}
	if plan.Registry.Supplied {
		id, err := existingKubeletPullGrant(ctx, a.credential, a.options, plan, facts)
		if err != nil {
			check.Observed, check.Cause = "existing kubelet registry authorization could not be read", err
			return check
		}
		if id != "" {
			return resourceResult(check, Pass, "existing applicable kubelet pull assignment="+id+"; preserve and reuse")
		}
	}
	if identityErr != nil || caller.objectID == "" {
		check.Observed, check.Cause = "operator object ID is unavailable", identityErr
		check.Missing = "operator object ID"
		return check
	}
	check.Status, check.Observed, check.Cause = a.basicAssignmentWrite(ctx, scope, caller, false)
	if check.Status == Pass {
		check.Missing, check.Remediation = "", ""
	}
	return check
}
