// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/authorization/armauthorization/v3"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/graphsdk"

	"azureaiagent/internal/pkg/agents/agent_api"
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

// scopeBucket categorizes a role-assignment scope ARN into one of the
// three buckets doctor check 12 renders (project / account /
// resource-group). Everything else (subscription, management-group,
// individual resources) lands in "other". Comparison is suffix-based
// because role-assignment scopes use the same path syntax as the
// resource IDs we parsed from AZURE_AI_PROJECT_ID.
type scopeBucket int

const (
	scopeBucketProject scopeBucket = iota
	scopeBucketAccount
	scopeBucketResourceGroup
	scopeBucketOther
)

// agentRoleSummary groups the role-definition GUIDs found at each
// bucket for a single agent. Each bucket's slice is deduplicated and
// retains friendly role names where the renderer can show them.
type agentRoleSummary struct {
	project       []string
	account       []string
	resourceGroup []string
	other         []string
}

// classifyAgentRoleSummary maps the summary into the design's three
// outcomes:
//
//   - Pass — assignments at project scope AND at account or
//     resource-group scope. Rendered as `doctorInfo` since the value
//     is mostly informational (there is nothing actionable to flag).
//   - Warn — has some assignments but the shape is suspicious (project
//     only with nothing at account/RG, OR account/RG only without
//     project). The renderer surfaces the list with a hint.
//   - Fail — zero role assignments at any of the three buckets. This
//     is the smoking-gun pattern for "deploy succeeded but every
//     tool-call 403s at runtime."
func classifyAgentRoleSummary(s agentRoleSummary) doctorStatus {
	hasProject := len(s.project) > 0
	hasParent := len(s.account) > 0 || len(s.resourceGroup) > 0

	switch {
	case !hasProject && !hasParent && len(s.other) == 0:
		return doctorFail
	case hasProject && hasParent:
		return doctorInfo
	default:
		return doctorWarn
	}
}

// renderAgentRoleSummary formats the per-scope role list into the
// Detail string the renderer surfaces below the check title. Lines are
// fixed-order (project → account → resource-group) so output is
// deterministic between runs.
func renderAgentRoleSummary(agentName, principalID string, s agentRoleSummary) string {
	var b strings.Builder
	fmt.Fprintf(&b, "agent: %s\nprincipal: %s\n", agentName, principalID)
	fmt.Fprintf(&b, "project scope:\n%s\n", renderScopeBucket(s.project))
	fmt.Fprintf(&b, "account scope:\n%s\n", renderScopeBucket(s.account))
	fmt.Fprintf(&b, "resource-group scope:\n%s", renderScopeBucket(s.resourceGroup))
	if len(s.other) > 0 {
		fmt.Fprintf(&b, "\nother scope:\n%s", renderScopeBucket(s.other))
	}
	return b.String()
}

// renderScopeBucket renders a single bucket as a bulleted list, or
// `  - (none)` when the bucket is empty.
func renderScopeBucket(roles []string) string {
	if len(roles) == 0 {
		return "  - (none)"
	}
	var b strings.Builder
	for i, r := range roles {
		if i > 0 {
			b.WriteByte('\n')
		}
		fmt.Fprintf(&b, "  - %s", r)
	}
	return b.String()
}

// roleLabelForID returns the friendly name for a known role
// definition; for unknown roles, the raw GUID is returned so the
// renderer always has *something* to display rather than a blank line.
func roleLabelForID(id string) string {
	if name, ok := roleNamesByID[id]; ok {
		return name
	}
	return id
}

// bucketScope categorizes a role-assignment scope ARN against the
// three project info scopes. Comparison is case-insensitive — ARM
// occasionally normalizes scope casing differently from the input
// resource ID.
func bucketScope(scope string, info *doctorProjectInfo) scopeBucket {
	s := strings.ToLower(scope)
	switch {
	case s == strings.ToLower(info.projectScope):
		return scopeBucketProject
	case s == strings.ToLower(info.accountScope):
		return scopeBucketAccount
	case s == strings.ToLower(rgScope(info)):
		return scopeBucketResourceGroup
	default:
		return scopeBucketOther
	}
}

// rgScope derives the resource-group scope ARN from the parsed
// project info. Kept as a free function so test cases can build the
// expected scope without going through parseDoctorProjectID.
func rgScope(info *doctorProjectInfo) string {
	return fmt.Sprintf(
		"/subscriptions/%s/resourceGroups/%s",
		info.subscriptionID, info.resourceGroup,
	)
}

// listAgentRoleSummary lists every role assignment for the agent MI
// across the project scope (which includes inherited assignments from
// the account, resource group, and subscription scopes via the
// `assignedTo()` filter semantics), then buckets the results.
//
// Returned slices are deterministic (sorted by friendly role name) so
// the renderer's output is stable between runs.
func listAgentRoleSummary(
	ctx context.Context,
	cred *azidentity.AzureDeveloperCLICredential,
	info *doctorProjectInfo,
	principalID string,
) (agentRoleSummary, error) {
	var summary agentRoleSummary

	client, err := armauthorization.NewRoleAssignmentsClient(info.subscriptionID, cred, nil)
	if err != nil {
		return summary, fmt.Errorf("failed to create role assignments client: %w", err)
	}

	filter := fmt.Sprintf("assignedTo('%s')", principalID)
	pager := client.NewListForScopePager(info.projectScope, &armauthorization.RoleAssignmentsClientListForScopeOptions{
		Filter: &filter,
	})

	// Use sets to deduplicate before sorting — multiple assignments of
	// the same role at the same scope show up in pager output (e.g.,
	// distinct assignment IDs but identical role/scope pairs).
	buckets := map[scopeBucket]map[string]bool{
		scopeBucketProject:       {},
		scopeBucketAccount:       {},
		scopeBucketResourceGroup: {},
		scopeBucketOther:         {},
	}

	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return summary, fmt.Errorf("failed to list role assignments for agent identity: %w", err)
		}
		for _, a := range page.Value {
			if a == nil || a.Properties == nil ||
				a.Properties.RoleDefinitionID == nil || a.Properties.Scope == nil {
				continue
			}
			roleID := *a.Properties.RoleDefinitionID
			if idx := strings.LastIndex(roleID, "/"); idx >= 0 && idx+1 < len(roleID) {
				roleID = roleID[idx+1:]
			}
			bucket := bucketScope(*a.Properties.Scope, info)
			buckets[bucket][roleLabelForID(roleID)] = true
		}
	}

	summary.project = sortedKeys(buckets[scopeBucketProject])
	summary.account = sortedKeys(buckets[scopeBucketAccount])
	summary.resourceGroup = sortedKeys(buckets[scopeBucketResourceGroup])
	summary.other = sortedKeys(buckets[scopeBucketOther])
	return summary, nil
}

// sortedKeys returns the keys of a set sorted alphabetically. Used to
// make the renderer's role list stable across runs.
func sortedKeys(set map[string]bool) []string {
	if len(set) == 0 {
		return nil
	}
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// checkAgentIdentityRBAC performs doctor check 12 — for each agent
// service, resolves the agent MI principal ID via GetAgentVersion and
// lists the role assignments it carries. Renders results as INFO when
// the shape matches expectations, Warn when it looks under-privileged,
// and Fail when the agent has zero assignments.
//
// Dependency matrix:
//
//   - skip when check 7 (auth) is Fail/Skip
//   - skip when check 8 (reachability) is Fail/Skip
//   - skip per-service when AGENT_<SVC>_NAME or AGENT_<SVC>_VERSION
//     is empty (service has not been deployed yet)
//   - skip per-service when InstanceIdentity.PrincipalID is empty
//     (Foundry agent has no managed identity assigned)
//
// Reuses the same Foundry data-plane client + ARM credential factory
// as checks 10 / 11, so a doctor invocation makes O(N) Foundry calls
// (one per service) plus O(N) ARM list calls.
func (a *doctorAction) checkAgentIdentityRBAC(
	ctx context.Context,
	pre remotePreconditions,
	authStatus, reachabilityStatus doctorStatus,
) []doctorResult {
	if authStatus != doctorOK && authStatus != doctorWarn {
		return []doctorResult{{
			ID:     "remote.agent-rbac",
			Title:  "Agent identity roles",
			Status: doctorSkip,
			Detail: "skipped: authentication check did not pass",
		}}
	}
	if reachabilityStatus != doctorOK && reachabilityStatus != doctorWarn {
		return []doctorResult{{
			ID:     "remote.agent-rbac",
			Title:  "Agent identity roles",
			Status: doctorSkip,
			Detail: "skipped: reachability check did not pass",
		}}
	}
	if !pre.endpointSet {
		return []doctorResult{{
			ID:     "remote.agent-rbac",
			Title:  "Agent identity roles",
			Status: doctorSkip,
			Detail: "skipped: AZURE_AI_PROJECT_ENDPOINT not set",
		}}
	}
	if len(pre.agentServices) == 0 {
		return []doctorResult{{
			ID:     "remote.agent-rbac",
			Title:  "Agent identity roles",
			Status: doctorSkip,
			Detail: "skipped: no agent services detected",
		}}
	}

	projectID := a.getEnvValue(ctx, pre.envName, "AZURE_AI_PROJECT_ID")
	if projectID == "" {
		return []doctorResult{{
			ID:     "remote.agent-rbac",
			Title:  "Agent identity roles",
			Status: doctorSkip,
			Detail: "skipped: AZURE_AI_PROJECT_ID not set in azd environment",
			Fix:    "azd provision",
		}}
	}
	info, err := parseDoctorProjectID(projectID)
	if err != nil {
		return []doctorResult{{
			ID:     "remote.agent-rbac",
			Title:  "Agent identity roles",
			Status: doctorSkip,
			Detail: fmt.Sprintf("skipped: %v", err),
		}}
	}

	// Resolve tenant + credential (same pattern as check 10 — the
	// LookupTenant call uses the user-access tenant ID so multi-tenant
	// guest setups resolve correctly).
	tenantResp, err := a.azdClient.Account().LookupTenant(ctx, &azdext.LookupTenantRequest{
		SubscriptionId: info.subscriptionID,
	})
	if err != nil {
		return []doctorResult{{
			ID:     "remote.agent-rbac",
			Title:  "Agent identity roles",
			Status: doctorSkip,
			Detail: fmt.Sprintf("skipped: failed to resolve tenant for %s: %v", info.subscriptionID, err),
		}}
	}

	cred, err := azidentity.NewAzureDeveloperCLICredential(&azidentity.AzureDeveloperCLICredentialOptions{
		TenantID:                   tenantResp.TenantId,
		AdditionallyAllowedTenants: []string{"*"},
	})
	if err != nil {
		return []doctorResult{{
			ID:     "remote.agent-rbac",
			Title:  "Agent identity roles",
			Status: doctorFail,
			Detail: fmt.Sprintf("failed to create credential for tenant %s: %v", tenantResp.TenantId, err),
			Fix:    "azd auth login",
		}}
	}

	client := agent_api.NewAgentClient(pre.endpoint, cred)

	out := make([]doctorResult, 0, len(pre.agentServices))
	for _, svc := range pre.agentServices {
		id := fmt.Sprintf("remote.agent-rbac.%s", svc.Name)
		title := fmt.Sprintf("Agent identity roles for %q", svc.Name)
		serviceKey := toServiceKey(svc.Name)

		agentName := a.getEnvValue(ctx, pre.envName, fmt.Sprintf("AGENT_%s_NAME", serviceKey))
		agentVersion := a.getEnvValue(ctx, pre.envName, fmt.Sprintf("AGENT_%s_VERSION", serviceKey))
		if agentName == "" || agentVersion == "" {
			out = append(out, doctorResult{
				ID:     id,
				Title:  title,
				Status: doctorSkip,
				Detail: fmt.Sprintf("skipped: AGENT_%s_NAME/_VERSION not set (deploy this service first)", serviceKey),
				Fix:    "azd deploy",
			})
			continue
		}

		reqCtx, cancelReq := context.WithTimeout(ctx, doctorRemoteTimeout)
		version, getErr := client.GetAgentVersion(reqCtx, agentName, agentVersion, DefaultAgentAPIVersion)
		cancelReq()
		if getErr != nil {
			out = append(out, doctorResult{
				ID:     id,
				Title:  title,
				Status: doctorFail,
				Detail: fmt.Sprintf("failed to fetch agent %s/%s: %v", agentName, agentVersion, getErr),
				Fix:    "azd ai agent monitor " + svc.Name + " --follow",
			})
			continue
		}

		if version == nil || version.InstanceIdentity == nil || version.InstanceIdentity.PrincipalID == "" {
			out = append(out, doctorResult{
				ID:     id,
				Title:  title,
				Status: doctorSkip,
				Detail: fmt.Sprintf("skipped: agent %s/%s has no managed identity", agentName, agentVersion),
			})
			continue
		}
		principalID := version.InstanceIdentity.PrincipalID

		listCtx, cancelList := context.WithTimeout(ctx, doctorRemoteTimeout)
		summary, listErr := listAgentRoleSummary(listCtx, cred, info, principalID)
		cancelList()
		if listErr != nil {
			out = append(out, doctorResult{
				ID:     id,
				Title:  title,
				Status: doctorFail,
				Detail: fmt.Sprintf("failed to list role assignments for %s: %v", principalID, listErr),
				Reason: "verify Reader access to the subscription and try again",
			})
			continue
		}

		res := doctorResult{
			ID:     id,
			Title:  title,
			Status: classifyAgentRoleSummary(summary),
			Detail: renderAgentRoleSummary(agentName, principalID, summary),
		}
		switch res.Status {
		case doctorWarn:
			res.Reason = "agent identity has assignments but the scope coverage looks limited; verify it matches your agent's needs"
			res.Fix = roleAssignCommand(principalID, "Cognitive Services User", info.accountScope)
		case doctorFail:
			res.Reason = "agent identity has no role assignments — runtime tool calls will likely 403"
			res.Fix = roleAssignCommand(principalID, "Cognitive Services User", info.accountScope)
		}
		out = append(out, res)
	}
	return out
}
