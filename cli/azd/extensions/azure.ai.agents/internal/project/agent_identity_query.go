// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/authorization/armauthorization/v3"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

// AgentPrincipal identifies one deployed agent's managed-identity
// principal — the entity whose role assignments the doctor's
// `remote.agent-identity-roles` check enumerates.
//
// AgentName / AgentVersion are the deployment-side coordinates surfaced
// to the user in Details and Messages. PrincipalID is the AAD object
// ID returned by Foundry's `GetAgentVersion` under
// `instance_identity.principal_id`; QueryAgentIdentityRoles is purely
// read-side and does not create or look up principals itself.
type AgentPrincipal struct {
	AgentName    string
	AgentVersion string
	PrincipalID  string
}

// AgentScopeRoles is the per-scope, per-agent listing the
// `remote.agent-identity-roles` check renders. Empty Roles is a
// meaningful state ("no role assignment on this scope") — callers must
// distinguish nil Roles (probe failed for this scope) from an empty
// non-nil slice (the probe succeeded and the principal has no roles
// there).
type AgentScopeRoles struct {
	// Scope is the friendly label ("project", "account",
	// "resource-group") used in user-facing output. The raw ARM scope
	// ARN is omitted — the caller already knows it from
	// `AgentIdentityRolesResult.Scopes` and surfacing it again per-row
	// hurts redaction more than it helps the user.
	Scope string
	// Roles is the list of human-readable role names (e.g.,
	// "Cognitive Services User"). When the listing succeeded but the
	// principal had no assignments at this scope, Roles is non-nil
	// and empty. nil indicates the per-scope probe failed and the
	// caller should treat the scope as "unknown".
	Roles []string
	// Err captures the per-scope probe error when the listing failed.
	// nil for successful empty-list responses.
	Err error
}

// AgentIdentityRolesEntry is the per-agent listing folded across the
// three probed scopes. AgentName / AgentVersion / PrincipalID echo the
// input AgentPrincipal so consumers do not need to thread the input
// alongside the output. ProjectScope / AccountScope / RGScope each
// carry the per-scope outcome.
type AgentIdentityRolesEntry struct {
	AgentName    string
	AgentVersion string
	PrincipalID  string
	ProjectScope AgentScopeRoles
	AccountScope AgentScopeRoles
	RGScope      AgentScopeRoles
}

// AgentIdentityRolesResult is the side-effect-free outcome of
// QueryAgentIdentityRoles. Entries preserves the input order (sorted
// by AgentName upstream so output is deterministic). Scopes captures
// the raw ARN of each scope the listings ran against — diagnostics
// surface the friendly label (`ProjectScope.Scope`) but JSON
// consumers may need the raw ARN; redacting consumers can replace
// these with `<redacted>` after assembly.
type AgentIdentityRolesResult struct {
	Entries []AgentIdentityRolesEntry
	Scopes  AgentIdentityScopes
}

// AgentIdentityScopes is the resolved scope ARN trio for an agent's
// identity-role listing. Account is the parent AI account ARN
// (`/subscriptions/.../accounts/<name>`); Project is the agent's
// hosting project ARN (`/subscriptions/.../accounts/.../projects/<p>`);
// ResourceGroup is `/subscriptions/.../resourceGroups/<rg>`.
type AgentIdentityScopes struct {
	Account       string
	Project       string
	ResourceGroup string
}

// QueryAgentIdentityRoles enumerates each principal's role assignments
// at the agent's three reachable scopes (project, account, resource
// group) and returns a structured listing for the doctor's
// `remote.agent-identity-roles` check.
//
// The function follows the same credential-acquisition pattern as
// EnsureAgentIdentityRBAC: parse the project ARM ID, resolve the
// user-access tenant via the azd extension, and create an
// AzureDeveloperCLICredential pinned to that tenant. Per-principal
// probing fans out across scopes; a failure on one scope does not
// short-circuit the others so the user always sees a complete picture
// of where the listing succeeded.
//
// Callers MUST validate the projectResourceID first (e.g., via
// ValidateProjectResourceID) — this function returns a hard error if
// parsing fails so the doctor can surface "AZURE_AI_PROJECT_ID is
// malformed" without rendering an empty listing.
//
// An empty `principals` slice returns a result with empty Entries and
// no error (the caller's check fires a Skip in that case — there is
// nothing to enumerate but the listing path itself is healthy).
func QueryAgentIdentityRoles(
	ctx context.Context,
	azdClient *azdext.AzdClient,
	projectResourceID string,
	principals []AgentPrincipal,
) (*AgentIdentityRolesResult, error) {
	info, err := parseAgentIdentityInfo(projectResourceID)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidProjectResourceID, err)
	}

	scopes := AgentIdentityScopes{
		Account:       info.AccountScope,
		Project:       info.ProjectScope,
		ResourceGroup: fmt.Sprintf("/subscriptions/%s/resourceGroups/%s", info.SubscriptionID, info.ResourceGroup),
	}

	if len(principals) == 0 {
		return &AgentIdentityRolesResult{Scopes: scopes}, nil
	}

	tenantResponse, err := azdClient.Account().LookupTenant(ctx, &azdext.LookupTenantRequest{
		SubscriptionId: info.SubscriptionID,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to look up tenant for subscription %s: %w", info.SubscriptionID, err)
	}

	cred, err := azidentity.NewAzureDeveloperCLICredential(&azidentity.AzureDeveloperCLICredentialOptions{
		TenantID:                   tenantResponse.TenantId,
		AdditionallyAllowedTenants: []string{"*"},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create Azure credential: %w", err)
	}

	return queryAgentIdentityRolesWithLister(ctx, scopes, principals, func(ctx context.Context, scope, principalID string) ([]string, error) {
		return listRoleNamesAtScope(ctx, cred, info.SubscriptionID, scope, principalID)
	})
}

// queryAgentIdentityRolesWithLister is the test-seam-friendly core of
// QueryAgentIdentityRoles. It accepts an injected `lister` so unit
// tests can drive every per-scope branch (success / empty /
// transport-error) without standing up an ARM fake.
//
// Production callers use QueryAgentIdentityRoles, which builds the
// real lister from an AzureDeveloperCLICredential.
func queryAgentIdentityRolesWithLister(
	ctx context.Context,
	scopes AgentIdentityScopes,
	principals []AgentPrincipal,
	lister func(ctx context.Context, scope, principalID string) ([]string, error),
) (*AgentIdentityRolesResult, error) {
	out := &AgentIdentityRolesResult{
		Entries: make([]AgentIdentityRolesEntry, len(principals)),
		Scopes:  scopes,
	}

	var wg sync.WaitGroup
	for i, p := range principals {
		wg.Go(func() {
			entry := AgentIdentityRolesEntry{
				AgentName:    p.AgentName,
				AgentVersion: p.AgentVersion,
				PrincipalID:  p.PrincipalID,
			}
			if p.PrincipalID == "" {
				err := errors.New("principal ID unavailable")
				entry.ProjectScope = AgentScopeRoles{Scope: "project", Err: err}
				entry.AccountScope = AgentScopeRoles{Scope: "account", Err: err}
				entry.RGScope = AgentScopeRoles{Scope: "resource-group", Err: err}
				out.Entries[i] = entry
				return
			}
			entry.ProjectScope = probeOneScope(ctx, "project", scopes.Project, p.PrincipalID, lister)
			entry.AccountScope = probeOneScope(ctx, "account", scopes.Account, p.PrincipalID, lister)
			entry.RGScope = probeOneScope(ctx, "resource-group", scopes.ResourceGroup, p.PrincipalID, lister)
			out.Entries[i] = entry
		})
	}
	wg.Wait()
	return out, nil
}

// probeOneScope wraps a per-scope listing call so the caller's
// AgentIdentityRolesEntry assembly stays a flat three-line composition.
// Returns a non-nil Roles (possibly empty) on success and nil Roles
// with a populated Err on failure.
func probeOneScope(
	ctx context.Context,
	label, scope, principalID string,
	lister func(ctx context.Context, scope, principalID string) ([]string, error),
) AgentScopeRoles {
	roles, err := lister(ctx, scope, principalID)
	if err != nil {
		return AgentScopeRoles{Scope: label, Err: err}
	}
	if roles == nil {
		roles = []string{}
	}
	return AgentScopeRoles{Scope: label, Roles: roles}
}

// listRoleNamesAtScope returns the role names assigned to principalID
// at the supplied ARM scope. The function uses ARM's server-side
// `assignedTo()` filter to avoid pulling every assignment in the
// scope, then resolves each role-definition ID into a human-readable
// name (with caching across calls within a single QueryAgentIdentityRoles
// invocation via the wg.Go workers' captured closure — caching is not
// strictly necessary at 3 calls × N agents but trims a few ARM round
// trips when a role is reused).
//
// The function is intentionally tolerant of partial failures: any
// per-assignment resolution error becomes an empty name; the listing
// still returns the rest of the assignments. The doctor surfaces the
// listing as INFO so missing role names are a soft degradation, not
// a hard failure.
func listRoleNamesAtScope(
	ctx context.Context,
	cred *azidentity.AzureDeveloperCLICredential,
	subscriptionID, scope, principalID string,
) ([]string, error) {
	if scope == "" {
		return nil, fmt.Errorf("empty scope")
	}
	if principalID == "" {
		return nil, fmt.Errorf("empty principal ID")
	}
	if subscriptionID == "" {
		return nil, fmt.Errorf("empty subscription ID")
	}

	client, err := armauthorization.NewRoleAssignmentsClient(subscriptionID, cred, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create role-assignments client: %w", err)
	}
	defClient, err := armauthorization.NewRoleDefinitionsClient(cred, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create role-definitions client: %w", err)
	}

	filter := fmt.Sprintf("assignedTo('%s')", principalID)
	pager := client.NewListForScopePager(scope, &armauthorization.RoleAssignmentsClientListForScopeOptions{
		Filter: &filter,
	})

	roleDefIDs := make([]string, 0, 4)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to list role assignments at scope %s: %w", scope, err)
		}
		for _, ra := range page.Value {
			if ra.Properties == nil || ra.Properties.RoleDefinitionID == nil {
				continue
			}
			roleDefIDs = append(roleDefIDs, *ra.Properties.RoleDefinitionID)
		}
	}

	cache := make(map[string]string, len(roleDefIDs))
	names := make([]string, 0, len(roleDefIDs))
	for _, defID := range roleDefIDs {
		if cached, ok := cache[defID]; ok {
			if cached != "" {
				names = append(names, cached)
			}
			continue
		}
		name := resolveRoleName(ctx, defClient, defID)
		cache[defID] = name
		if name != "" {
			names = append(names, name)
		}
	}
	return names, nil
}

// resolveRoleName fetches the human-readable role-definition name for
// a `/.../roleDefinitions/<guid>` ARM ID. The scope passed to
// `RoleDefinitions.Get` is the resource scope of the assignment, but
// since the role definition is global to its assignable scope chain
// (typically subscription-level for built-in roles), we use the
// subscription extracted from the role-definition ARM ID itself as
// the listing scope.
//
// Returns "" on any failure — callers omit empty names from the
// rendered listing. This matches the design's principle that the
// check's INFO classification is a soft surface; a missing role name
// should not turn the whole check red.
func resolveRoleName(
	ctx context.Context,
	defClient *armauthorization.RoleDefinitionsClient,
	roleDefID string,
) string {
	// Role-definition ARM IDs are of the form:
	//   /subscriptions/<sub>/providers/Microsoft.Authorization/roleDefinitions/<guid>
	// For built-in roles (which is the common case for agent MIs)
	// the listing scope is the subscription. Strip the trailing
	// `/providers/...` to derive the scope; on a parse miss, treat
	// the ARM ID itself as both scope and name input.
	idx := strings.Index(roleDefID, "/providers/")
	scope := roleDefID
	if idx > 0 {
		scope = roleDefID[:idx]
	}
	name := roleDefID[strings.LastIndex(roleDefID, "/")+1:]
	if name == "" {
		return ""
	}

	resp, err := defClient.Get(ctx, scope, name, nil)
	if err != nil {
		return ""
	}
	if resp.Properties == nil || resp.Properties.RoleName == nil {
		return ""
	}
	return *resp.Properties.RoleName
}
