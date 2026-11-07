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

type scopedAuthorizer struct {
	credential azcore.TokenCredential
	options    *arm.ClientOptions
	reads      *AuthorizationEvidence
}

// NewAuthorizer checks supplied-identity permissions without trial writes, deny reads or Graph calls.
func NewAuthorizer(credential azcore.TokenCredential, options *arm.ClientOptions) Authorizer {
	return &scopedAuthorizer{credential: credential, options: onboardingARMOptions(options)}
}

// NewAuthorizerWithEvidence reuses request-local evidence across preflight and deployment bindings.
func NewAuthorizerWithEvidence(
	credential azcore.TokenCredential, options *arm.ClientOptions, evidence *AuthorizationEvidence,
) Authorizer {
	return &scopedAuthorizer{credential: credential, options: onboardingARMOptions(options), reads: evidence}
}

func (a *scopedAuthorizer) Check(ctx context.Context, p Plan, f Facts) (Report, error) {
	local := *a
	local.reads = a.evidence()
	report := local.suppliedIdentityChecks(ctx, p, f)
	return report, report.Err()
}

// ResolveRoleByName resolves a built-in role, sharing concurrent reads and errors by subscription and exact role name.
func (e *AuthorizationEvidence) ResolveRoleByName(
	ctx context.Context, subscription, roleName string,
) (string, error) {
	return e.roleNames.get(strings.ToLower(subscription)+"|"+roleName, func() (string, error) {
		client, err := e.definitionClient()
		if err != nil {
			return "", err
		}
		pager := client.NewListPager("/subscriptions/"+subscription,
			&armauthorization.RoleDefinitionsClientListOptions{Filter: new("roleName eq '" + roleName + "'")})
		for pager.More() {
			page, err := pager.NextPage(ctx)
			if err != nil {
				return "", err
			}
			for _, role := range page.Value {
				if role != nil && role.ID != nil && role.Properties != nil &&
					authString(role.Properties.RoleName) == roleName &&
					strings.EqualFold(authString(role.Properties.RoleType), "BuiltInRole") {
					return *role.ID, nil
				}
			}
		}
		return "", fmt.Errorf("required built-in role %q is unavailable", roleName)
	})
}
