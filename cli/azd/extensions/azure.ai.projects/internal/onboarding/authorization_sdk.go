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

// NewAuthorizer checks basic scoped ARM action allow without trial writes, deny reads or Graph calls.
func NewAuthorizer(credential azcore.TokenCredential, options *arm.ClientOptions) Authorizer {
	return &scopedAuthorizer{credential: credential, options: onboardingARMOptions(options)}
}

func (a *scopedAuthorizer) Check(ctx context.Context, p Plan, f Facts) (Report, error) {
	operations := append([]Operation{}, p.Operations...)
	var report Report
	target := "/subscriptions/" + p.SubscriptionID + "/resourceGroups/" + p.ResourceGroup
	registry, err := ParseID(p.Registry.ID, "Microsoft.ContainerRegistry/registries")
	if err != nil {
		return nil, fmt.Errorf("P09: registry authorization scope: %w", err)
	}
	if p.RegistryCustomerSupplied || f.RegistryABAC || !strings.EqualFold(registry.Parent.String(), target) {
		scope := p.Registry.ID
		if !p.Registry.Supplied {
			scope = p.Registry.Parent
		}
		operations = append(operations, Operation{CheckID: "P09", Action: roleAssignmentWrite, Scope: scope})
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
	basicIdentityErr := identityErr
	if tokenErr != nil {
		basicIdentityErr = tokenErr
	}
	report = append(report, a.checkP06(ctx, p, caller, basicIdentityErr)...)
	report = append(report, a.checkP07(ctx, p, f, caller, basicIdentityErr))
	for _, op := range operations {
		check := Check{
			ID: op.CheckID, Scope: op.Scope, Status: Unverified, Mandatory: true,
			Expected: op.Action, Missing: op.Action,
			Remediation: "Ask the scope administrator to provide readable operator role assignments " +
				"and definitions permitting " + op.Action + " at this scope or an applicable ancestor.",
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
			check.Status, check.Observed, check.Cause = a.basicAction(ctx, op.Scope, caller, op.Action, false)
			if check.Status == Pass {
				check.Observed += "; ARM authorizes the actual operation"
				check.Remediation = ""
			}
		}

		if check.Status == Pass {
			check.Missing = ""
		}
		report = append(report, check)
	}
	return report, report.Err()
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
