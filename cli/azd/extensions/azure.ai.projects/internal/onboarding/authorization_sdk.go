// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package onboarding

import (
	"context"
	"fmt"
	"slices"
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
	reads      *AuthorizationEvidence
}

type operation struct {
	checkID, action, scope string
}

// NewAuthorizer checks basic scoped ARM action allow without trial writes, deny reads or Graph calls.
func NewAuthorizer(credential azcore.TokenCredential, options *arm.ClientOptions) Authorizer {
	return &scopedAuthorizer{credential: credential, options: onboardingARMOptions(options)}
}

// NewAuthorizerWithEvidence reuses request-local evidence across preflight and deployment bindings.
func NewAuthorizerWithEvidence(
	credential azcore.TokenCredential, options *arm.ClientOptions, evidence *AuthorizationEvidence,
) Authorizer {
	return &scopedAuthorizer{credential: credential, options: onboardingARMOptions(options), reads: evidence}
}

// CheckClusterDeployment checks deployment-write permission in the AKS resource group without checking supporting resources.
func CheckClusterDeployment(
	ctx context.Context, clusterID string, credential azcore.TokenCredential, options *arm.ClientOptions,
) (Report, error) {
	cluster, err := ParseID(clusterID, "Microsoft.ContainerService/managedClusters")
	if err != nil {
		return nil, err
	}
	a := &scopedAuthorizer{credential: credential, options: onboardingARMOptions(options)}
	a.reads = NewAuthorizationEvidence(a.credential, a.options)
	caller, tokenErr, identityErr := a.operatorIdentity(ctx)
	report := a.checkOperations(ctx, []operation{
		{"P09", "Microsoft.Resources/deployments/write", cluster.Parent.String()},
	}, caller, tokenErr, identityErr)
	return report, report.Err()
}

func supportingOperations(plan Plan, facts Facts) ([]operation, error) {
	var operations []operation
	add := func(id, action, scope string) {
		op := operation{id, action, scope}
		if !slices.ContainsFunc(operations, func(existing operation) bool {
			return existing.checkID == id && strings.EqualFold(existing.action, action) &&
				strings.EqualFold(existing.scope, scope)
		}) {
			operations = append(operations, op)
		}
	}
	for _, identity := range []Resource{plan.Hosting, plan.Workload} {
		if identity.Missing != "" || identity.LifecycleMissing != "" {
			continue
		}
		actions := []string{"write", "read", "assign/action"}
		if identity.Supplied {
			actions = []string{"read", "assign/action"}
		}
		for _, action := range actions {
			add("P02", "Microsoft.ManagedIdentity/userAssignedIdentities/"+action, facts.scope(identity))
		}
	}
	if !plan.Storage.Supplied && plan.Storage.Missing == "" && plan.Storage.LifecycleMissing == "" {
		add("P04", "Microsoft.Storage/storageAccounts/write", facts.scope(plan.Storage))
	}
	if plan.Registry.ID != "" && plan.Registry.Missing == "" && plan.Registry.LifecycleMissing == "" {
		add("P05", "Microsoft.ContainerRegistry/registries/write", facts.scope(plan.Registry))
	}
	for _, op := range plan.Operations {
		add(op.checkID, op.action, op.scope)
	}
	target := "/subscriptions/" + plan.SubscriptionID + "/resourceGroups/" + plan.ResourceGroup
	if plan.Grants != nil {
		for _, grant := range plan.Grants {
			if grant.DeploymentMissing != "" || grant.DeploymentScope == "" {
				continue
			}
			scope := grant.DeploymentScope
			if plan.TargetScopeMissing != "" {
				if grant.CheckID == "G01" {
					add("P09", "Microsoft.Resources/deployments/write", scope)
				}
				continue
			}
			if strings.EqualFold(scope, target) {
				if grant.CheckID == "G01" {
					add("P09", "Microsoft.Resources/deployments/write", scope)
				}
				continue
			}
			for _, action := range []string{"write", "read", "operations/read"} {
				add("P09", "Microsoft.Resources/deployments/"+action, scope)
			}
		}
		return operations, nil
	}
	for _, item := range []struct {
		resource Resource
		checkID  string
	}{
		{plan.Cluster, "G01"}, {plan.Workload, "G02"}, {plan.Storage, "G03"},
	} {
		if !createsGrant(plan, item.checkID) {
			continue
		}
		if item.resource.Missing != "" || item.resource.LifecycleMissing != "" {
			continue
		}
		id, err := arm.ParseResourceID(item.resource.ID)
		if err != nil {
			return nil, fmt.Errorf("resolve cross-resource-group deployment scope: %w", err)
		}
		scope := id.Parent.String()
		if strings.EqualFold(scope, target) {
			if item.checkID == "G01" {
				add("P09", "Microsoft.Resources/deployments/write", scope)
			}
			continue
		}
		for _, action := range []string{"write", "read", "operations/read"} {
			add("P09", "Microsoft.Resources/deployments/"+action, scope)
		}
	}
	return operations, nil
}

func (a *scopedAuthorizer) Check(ctx context.Context, p Plan, f Facts) (Report, error) {
	local := *a
	local.reads = a.evidence()
	a = &local
	report := a.suppliedIdentityChecks(ctx, p, f)
	report = append(report, permissionPlanSkips(p)...)
	caller, tokenErr, identityErr := a.operatorIdentity(ctx)
	basicIdentityErr := identityErr
	if tokenErr != nil {
		basicIdentityErr = tokenErr
	}
	report = append(report, a.checkP06(ctx, p, f, caller, basicIdentityErr)...)
	if p.Registry.ID != "" || p.Registry.Missing != "" {
		report = append(report, a.checkP07(ctx, p, f, caller, basicIdentityErr))
	}
	operations, err := supportingOperations(p, f)
	if err != nil {
		return report, err
	}
	report = append(report, a.checkOperations(ctx, operations, caller, tokenErr, identityErr)...)
	return report, report.Err()
}

func (a *scopedAuthorizer) operatorIdentity(
	ctx context.Context,
) (caller callerIdentity, tokenErr, identityErr error) {
	audience := "https://management.azure.com/"
	if a.options != nil {
		if service, ok := a.options.Cloud.Services[cloud.ResourceManager]; ok && service.Audience != "" {
			audience = service.Audience
		}
	}
	token, tokenErr := a.credential.GetToken(ctx, policy.TokenRequestOptions{
		Scopes: []string{strings.TrimRight(audience, "/") + "/.default"},
	})
	caller, identityErr = identityFromToken(token.Token)
	return caller, tokenErr, identityErr
}

func (a *scopedAuthorizer) checkOperations(
	ctx context.Context, operations []operation, caller callerIdentity, tokenErr, identityErr error,
) Report {
	checks := make(Report, len(operations))
	tasks := make([]func(), 0, len(operations))
	for i, op := range operations {
		tasks = append(tasks, func() {
			check := Check{
				ID: op.checkID, Scope: op.scope, Status: Unverified, Mandatory: true,
				Expected: op.action, Missing: op.action,
				Remediation: "Ask the scope administrator to provide readable operator role assignments " +
					"and definitions permitting " + op.action + " at this scope or an applicable ancestor.",
			}
			switch {
			case op.scope == "":
				check.Observed = "current resource existence is unverified; concrete or existing parent scope is unknown"
				check.Missing = "verified current resource existence and actual permission scope"
			case tokenErr != nil:
				check.Observed, check.Cause = "operator token evidence unavailable", tokenErr
			case identityErr != nil:
				check.Observed, check.Cause = identityErr.Error(), identityErr
			default:
				check.Status, check.Observed, check.Cause = a.basicAction(ctx, op.scope, caller, op.action, false)
				if check.Status == Pass {
					check.Observed += "; ARM authorizes the actual operation"
					check.Missing, check.Remediation = "", ""
				}
			}
			checks[i] = check
		})
	}
	parallelReads(tasks...)
	return checks
}

func permissionPlanSkips(plan Plan) Report {
	var report Report
	for _, resource := range []Resource{plan.Hosting, plan.Workload} {
		if missing := resource.Missing + resource.LifecycleMissing; missing != "" {
			report = append(report, SkippedCheck("P02", resource.diagnosticScope(),
				"managed identity lifecycle/read/assign permissions", missing))
		}
	}
	if missing := plan.Storage.Missing + plan.Storage.LifecycleMissing; missing != "" {
		report = append(report, SkippedCheck("P04", plan.Storage.diagnosticScope(), "Storage creation permission", missing))
	}
	if missing := plan.Registry.Missing + plan.Registry.LifecycleMissing; missing != "" {
		report = append(report, SkippedCheck("P05", plan.Registry.diagnosticScope(), "native registry creation", missing))
	}
	return report
}

func (e *AuthorizationEvidence) resolveGrantRole(
	ctx context.Context, subscription string, grant grantRequest,
) (string, error) {
	if scope, err := arm.ParseResourceID(grant.scope); err == nil && scope.SubscriptionID != "" {
		subscription = scope.SubscriptionID
	}
	if grant.roleID != "" {
		return "/subscriptions/" + subscription + "/providers/Microsoft.Authorization/roleDefinitions/" + grant.roleID, nil
	}
	return e.ResolveRoleByName(ctx, subscription, grant.roleName)
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
