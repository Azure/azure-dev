// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/authorization/armauthorization/v3"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/graphsdk"
)

// Role definition GUIDs we recognize for doctor check 10. Centralised
// here (rather than reusing the constants in internal/project) so the
// doctor command's role set is documented in one place and is not
// coupled to the deploy-time pre-flight check's expanding role list.
const (
	doctorRoleOwner                = "8e3af657-a8ff-443c-a75c-2fe8c4bcb635"
	doctorRoleContributor          = "b24988ac-6180-42a0-ab88-20f7382dd24c"
	doctorRoleAzureAIDeveloper     = "64702f94-c441-49e6-a78b-ef80e0188fee"
	doctorRoleAzureAIUser          = "53ca6127-db72-4b80-b1b0-d745d6d5456d"
	doctorRoleCognitiveServicesSvc = "a97b65f3-24c7-4388-baec-2e87135dc908" // Cognitive Services User
)

// roleNamesByID is the friendly label rendered in detail / fix strings.
// Kept aligned with the design's "Pass: <role-1>, <role-2>" output.
var roleNamesByID = map[string]string{
	doctorRoleOwner:                "Owner",
	doctorRoleContributor:          "Contributor",
	doctorRoleAzureAIDeveloper:     "Azure AI Developer",
	doctorRoleAzureAIUser:          "Azure AI User",
	doctorRoleCognitiveServicesSvc: "Cognitive Services User",
}

// rbacRoleSets bundles the role IDs that satisfy each capability in
// doctor check 10. The categories mirror the design:
//
//   - deploy: roles that let the caller create/deploy agents AND invoke
//     them. "AI Developer" is the canonical role; Owner/Contributor are
//     supersets.
//   - invoke: deploy roles PLUS "AI User" (which grants invoke but not
//     deploy). Used to detect the "you can invoke but not deploy" warn
//     case.
//   - model: roles that let the agent's model traffic flow. "Cognitive
//     Services User" on the AI account; Owner/Contributor are supersets.
//
// Order matters only for the friendly label rendering — earlier IDs are
// preferred when the user has multiple sufficient roles.
type rbacRoleSets struct {
	deploy []string
	invoke []string
	model  []string
}

func defaultRBACRoleSets() rbacRoleSets {
	deploy := []string{doctorRoleOwner, doctorRoleContributor, doctorRoleAzureAIDeveloper}
	invoke := append([]string{}, deploy...)
	invoke = append(invoke, doctorRoleAzureAIUser)
	model := []string{doctorRoleOwner, doctorRoleContributor, doctorRoleCognitiveServicesSvc}
	return rbacRoleSets{deploy: deploy, invoke: invoke, model: model}
}

// checkUserRBAC performs doctor check 10 — verifies the current
// principal has the role assignments needed to deploy / invoke agents
// against the Foundry project. Skips cleanly when auth has not passed
// or when AZURE_AI_PROJECT_ID is unavailable in the azd environment.
//
// The check is intentionally read-only: it lists existing assignments
// and never writes. Doctor recommends `az role assignment create` for
// missing roles so the user (or their admin) can apply the fix.
//
// Dependency matrix:
//
//   - skip when check 7 (auth) is Fail/Skip
//   - independent of check 8 (reachability) — RBAC uses ARM, not the
//     Foundry data plane
func (a *doctorAction) checkUserRBAC(
	ctx context.Context,
	pre remotePreconditions,
	authStatus doctorStatus,
) doctorResult {
	if authStatus != doctorOK && authStatus != doctorWarn {
		return doctorResult{
			ID:     "remote.rbac",
			Title:  "User RBAC",
			Status: doctorSkip,
			Detail: "skipped: authentication check did not pass",
		}
	}

	projectID := a.getEnvValue(ctx, pre.envName, "AZURE_AI_PROJECT_ID")
	if projectID == "" {
		return doctorResult{
			ID:     "remote.rbac",
			Title:  "User RBAC",
			Status: doctorSkip,
			Detail: "skipped: AZURE_AI_PROJECT_ID not set in azd environment",
			Fix:    "azd provision",
			Reason: "provision the Foundry project to populate the required environment variables",
		}
	}

	info, err := parseDoctorProjectID(projectID)
	if err != nil {
		return doctorResult{
			ID:     "remote.rbac",
			Title:  "User RBAC",
			Status: doctorSkip,
			Detail: fmt.Sprintf("skipped: %v", err),
		}
	}

	// Resolve tenant + credential. LookupTenant maps the subscription
	// to the user-access tenant; using the resource tenant here breaks
	// in multi-tenant / guest setups.
	tenantResp, err := a.azdClient.Account().LookupTenant(ctx, &azdext.LookupTenantRequest{
		SubscriptionId: info.subscriptionID,
	})
	if err != nil {
		return doctorResult{
			ID:     "remote.rbac",
			Title:  "User RBAC",
			Status: doctorSkip,
			Detail: fmt.Sprintf("skipped: failed to resolve tenant for %s: %v", info.subscriptionID, err),
		}
	}

	cred, err := azidentity.NewAzureDeveloperCLICredential(&azidentity.AzureDeveloperCLICredentialOptions{
		TenantID:                   tenantResp.TenantId,
		AdditionallyAllowedTenants: []string{"*"},
	})
	if err != nil {
		return doctorResult{
			ID:     "remote.rbac",
			Title:  "User RBAC",
			Status: doctorFail,
			Detail: fmt.Sprintf("failed to create credential for tenant %s: %v", tenantResp.TenantId, err),
			Fix:    "azd auth login",
		}
	}

	// Resolve principal ID via Graph API. The error path is Skip rather
	// than Fail because a missing Graph permission is a separate
	// problem from missing role assignments — surfacing it as Fail
	// would mask the actual cause.
	principalID, principalLabel, err := resolvePrincipal(ctx, cred)
	if err != nil {
		return doctorResult{
			ID:     "remote.rbac",
			Title:  "User RBAC",
			Status: doctorSkip,
			Detail: fmt.Sprintf("skipped: failed to resolve principal: %v", err),
		}
	}

	listCtx, cancel := context.WithTimeout(ctx, doctorRemoteTimeout)
	defer cancel()
	present, err := listAssignedRoleIDs(listCtx, cred, info.subscriptionID, info.projectScope, principalID)
	if err != nil {
		return doctorResult{
			ID:     "remote.rbac",
			Title:  "User RBAC",
			Status: doctorFail,
			Detail: fmt.Sprintf("failed to list role assignments at %s: %v", info.projectScope, err),
			Reason: "verify Reader access to the subscription and try again",
		}
	}

	return classifyRBAC(defaultRBACRoleSets(), present, principalID, principalLabel, info)
}

// classifyRBAC maps the set of assigned role IDs into a doctorResult.
// Pure function — no I/O — so test cases can drive every branch with a
// hand-built `present` map. The Detail/Fix strings are intentionally
// templated with the raw principal ID and scope ARN; the renderer's
// redaction layer rewrites both when output is non-TTY.
func classifyRBAC(
	sets rbacRoleSets,
	present map[string]bool,
	principalID, principalLabel string,
	info *doctorProjectInfo,
) doctorResult {
	result := doctorResult{ID: "remote.rbac", Title: "User RBAC"}

	deployMatches := matchingRoleNames(sets.deploy, present)
	invokeMatches := matchingRoleNames(sets.invoke, present)
	modelMatches := matchingRoleNames(sets.model, present)

	hasDeploy := len(deployMatches) > 0
	hasInvoke := len(invokeMatches) > 0
	hasModel := len(modelMatches) > 0

	switch {
	case hasDeploy && hasModel:
		result.Status = doctorOK
		details := append([]string{}, deployMatches...)
		details = append(details, modelMatches...)
		result.Detail = fmt.Sprintf("%s: %s", principalLabel, strings.Join(dedupRoles(details), ", "))
		return result

	case hasInvoke && hasModel && !hasDeploy:
		result.Status = doctorWarn
		result.Detail = fmt.Sprintf(
			"%s: %s — can invoke agents but not deploy",
			principalLabel, strings.Join(invokeMatches, ", "),
		)
		result.Fix = roleAssignCommand(principalID, "Azure AI Developer", info.projectScope)
		result.Reason = "missing 'Azure AI Developer' on the Foundry project — required for `azd deploy`"
		return result

	default:
		result.Status = doctorFail
		missing := []string{}
		if !hasDeploy && !hasInvoke {
			missing = append(missing, "Azure AI Developer (or stronger) on the project")
		}
		if !hasModel {
			missing = append(missing, "Cognitive Services User on the AI account")
		}
		result.Detail = fmt.Sprintf("%s is missing: %s", principalLabel, strings.Join(missing, "; "))
		// Recommend the deploy role since it is required for the most
		// common path (azd deploy). The user can use the same command
		// shape to grant the model-usage role if needed.
		result.Fix = roleAssignCommand(principalID, "Azure AI Developer", info.projectScope)
		result.Reason = "ask a subscription Owner or User Access Administrator to assign the missing role(s)"
		return result
	}
}

// matchingRoleNames returns the friendly names of every role in `want`
// that is also present in `present`. Order follows `want` so callers
// can use the natural broadest-first ordering for label rendering.
func matchingRoleNames(want []string, present map[string]bool) []string {
	var out []string
	for _, id := range want {
		if present[id] {
			if name, ok := roleNamesByID[id]; ok {
				out = append(out, name)
			}
		}
	}
	return out
}

// dedupRoles removes duplicates while preserving first-occurrence order
// — used when concatenating the deploy + model role labels for the OK
// detail line so "Owner, Owner" never reaches the renderer.
func dedupRoles(in []string) []string {
	seen := make(map[string]bool, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

// roleAssignCommand returns the `az role assignment create` snippet
// surfaced in Fix. The literal command is built with %q on the scope
// so paths with characters needing escaping render correctly when the
// user pastes the command.
func roleAssignCommand(principalID, roleName, scope string) string {
	return fmt.Sprintf(
		"az role assignment create --assignee %s --role %q --scope %s",
		principalID, roleName, scope,
	)
}

// doctorProjectInfo is a minimal projection of agentIdentityInfo
// (`internal/project`) — repeated here so doctor doesn't need a
// cross-package dependency on the deploy-time pre-flight package.
type doctorProjectInfo struct {
	subscriptionID string
	resourceGroup  string
	accountName    string
	projectName    string
	accountScope   string // /subscriptions/.../accounts/<acct>
	projectScope   string // <accountScope>/projects/<project>
}

// parseDoctorProjectID parses an AZURE_AI_PROJECT_ID value into its
// components. Returns a typed error on malformed input so callers can
// surface "skipped: <reason>" rather than swallowing the parse silently.
//
// Expected format:
//
//	/subscriptions/{sub}/resourceGroups/{rg}/providers/Microsoft.CognitiveServices/accounts/{acct}/projects/{proj}
func parseDoctorProjectID(projectResourceID string) (*doctorProjectInfo, error) {
	parts := strings.Split(projectResourceID, "/")
	if len(parts) < 11 {
		return nil, fmt.Errorf("invalid AZURE_AI_PROJECT_ID: %s", projectResourceID)
	}

	info := &doctorProjectInfo{}
	for i, p := range parts {
		switch {
		case p == "subscriptions" && i+1 < len(parts):
			info.subscriptionID = parts[i+1]
		case p == "resourceGroups" && i+1 < len(parts):
			info.resourceGroup = parts[i+1]
		case p == "accounts" && i+1 < len(parts):
			info.accountName = parts[i+1]
		case p == "projects" && i+1 < len(parts):
			info.projectName = parts[i+1]
		}
	}
	if info.subscriptionID == "" || info.resourceGroup == "" || info.accountName == "" || info.projectName == "" {
		return nil, fmt.Errorf("AZURE_AI_PROJECT_ID is missing one or more segments: %s", projectResourceID)
	}

	info.projectScope = projectResourceID
	before, _, ok := strings.Cut(projectResourceID, "/projects/")
	if !ok {
		return nil, fmt.Errorf("could not derive AI account scope from %s", projectResourceID)
	}
	info.accountScope = before
	return info, nil
}

// resolvePrincipal fetches the current user's Graph profile and
// returns the (objectId, friendlyLabel) pair. The label is "<display
// name> (<upn>)" when available, falling back to just the principal ID.
func resolvePrincipal(ctx context.Context, cred *azidentity.AzureDeveloperCLICredential) (string, string, error) {
	graph, err := graphsdk.NewGraphClient(cred, nil)
	if err != nil {
		return "", "", fmt.Errorf("could not create Graph client: %w", err)
	}
	profile, err := graph.Me().Get(ctx)
	if err != nil {
		return "", "", fmt.Errorf("could not retrieve user profile: %w", err)
	}
	upn := profile.UserPrincipalName
	display := profile.DisplayName
	label := profile.Id
	switch {
	case display != "" && upn != "":
		label = fmt.Sprintf("%s (%s)", display, upn)
	case upn != "":
		label = upn
	case display != "":
		label = display
	}
	return profile.Id, label, nil
}

// listAssignedRoleIDs returns the set of role-definition IDs that
// `principalID` has at `scope` (and any inherited scopes). The set is
// keyed by the role definition's bare GUID — extracted from the trailing
// path segment of `roleDefinitionId`, which has the shape
// `/subscriptions/{sub}/providers/Microsoft.Authorization/roleDefinitions/{guid}`.
//
// We use the server-side `assignedTo()` filter so the API only returns
// assignments for this principal — that ALSO includes assignments at
// parent scopes that propagate to `scope`, which is exactly what doctor
// wants (RBAC inheritance is part of the user's effective permissions).
func listAssignedRoleIDs(
	ctx context.Context,
	cred *azidentity.AzureDeveloperCLICredential,
	subscriptionID, scope, principalID string,
) (map[string]bool, error) {
	client, err := armauthorization.NewRoleAssignmentsClient(subscriptionID, cred, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create role assignments client: %w", err)
	}

	filter := fmt.Sprintf("assignedTo('%s')", principalID)
	pager := client.NewListForScopePager(scope, &armauthorization.RoleAssignmentsClientListForScopeOptions{
		Filter: &filter,
	})

	present := map[string]bool{}
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to list role assignments at %s: %w", scope, err)
		}
		for _, a := range page.Value {
			if a == nil || a.Properties == nil || a.Properties.RoleDefinitionID == nil {
				continue
			}
			id := *a.Properties.RoleDefinitionID
			if idx := strings.LastIndex(id, "/"); idx >= 0 && idx+1 < len(id) {
				present[id[idx+1:]] = true
			}
		}
	}
	return present, nil
}
