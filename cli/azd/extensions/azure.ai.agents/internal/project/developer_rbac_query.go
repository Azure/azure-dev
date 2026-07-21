// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/graphsdk"
)

// ErrInvalidProjectResourceID is the sentinel error returned by
// ValidateProjectResourceID (and wrapped by QueryDeveloperRBAC) when
// the supplied resource ID does not match the Foundry project ARM
// resource ID shape. Diagnostic consumers use
// `errors.Is(err, ErrInvalidProjectResourceID)` to distinguish
// user-fixable configuration errors (handled by an `azd env set`
// suggestion) from transient probe errors (handled by retry).
var ErrInvalidProjectResourceID = errors.New("invalid project resource ID")

// ErrSPNDelegatedAuthRequired is the sentinel error returned by
// QueryDeveloperRBAC when the underlying Graph `/me` call rejects
// the access token because it was issued for a service principal
// (Graph `/me` requires user-delegated auth). Diagnostic consumers
// use `errors.Is(err, ErrSPNDelegatedAuthRequired)` to surface a
// SPN-aware Skip message instead of a generic transient-failure
// retry hint.
var ErrSPNDelegatedAuthRequired = errors.New("service-principal sign-in detected; Graph /me requires user-delegated auth")

// ValidateProjectResourceID returns a non-nil error wrapping
// ErrInvalidProjectResourceID if the supplied string is not a
// Foundry project ARM resource ID
// (`/subscriptions/.../accounts/.../projects/...`). The inner
// parseAgentIdentityInfo error is preserved via `%w` for callers
// that want to surface the raw failure when redaction is off.
func ValidateProjectResourceID(projectResourceID string) error {
	if _, err := parseAgentIdentityInfo(projectResourceID); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidProjectResourceID, err)
	}
	return nil
}

// DeveloperRBACResult is the side-effect-free outcome of
// QueryDeveloperRBAC: a structured snapshot of the developer
// principal's RBAC posture on a Foundry project, suitable for
// diagnostics (`azd ai agent doctor`'s `remote.rbac` check).
//
// All fields are best-effort — a populated PrincipalID with an
// empty PrincipalDisplay (for example) is possible if Graph
// returns the OID but not the display name. Callers should treat
// the absence of a field as "unknown", not as a contradiction.
type DeveloperRBACResult struct {
	// PrincipalID is the developer's Azure AD object ID (oid) as
	// reported by Microsoft Graph's `/me` endpoint.
	PrincipalID string

	// PrincipalDisplay is the developer's display name (or empty
	// string if Graph did not return one).
	PrincipalDisplay string

	// HasSufficientAIRole is true when the principal has at least
	// one of `sufficientAIUserRoles` (Foundry User, Cognitive Services User,
	// Foundry Project Manager, or Foundry Owner) on the project scope.
	HasSufficientAIRole bool

	// ProjectScope is the full ARM resource ID of the Foundry
	// project that was queried. Useful for templating the
	// `az role assignment create --scope <...>` remediation.
	ProjectScope string

	// AccountName is the Cognitive Services account that contains
	// the project (parsed out of ProjectScope).
	AccountName string

	// ProjectName is the Foundry project name (parsed out of
	// ProjectScope).
	ProjectName string
}

// QueryDeveloperRBAC returns the developer's RBAC posture on the
// given Foundry project resource ID *without* mutating Azure
// state. Unlike CheckDeveloperRBAC, it does not auto-assign
// missing roles and produces no fmt.Println side effects — it
// is intended for diagnostic consumers such as
// `azd ai agent doctor`.
//
// The function performs three round trips:
//
//  1. azd's gRPC `Account.LookupTenant` to resolve the user-access
//     tenant for the subscription (multi-tenant / guest users have
//     a different user tenant than the resource tenant).
//  2. Microsoft Graph `/me` for the principal's object ID and
//     display name.
//  3. ARM `RoleAssignments.ListForScope` with `assignedTo()` filter
//     against the project scope.
//
// Errors are surfaced verbatim; callers decide whether to render
// them as Fail, Skip, or Warn in their diagnostic surface.
func QueryDeveloperRBAC(
	ctx context.Context,
	azdClient *azdext.AzdClient,
	projectResourceID string,
) (*DeveloperRBACResult, error) {
	info, err := parseAgentIdentityInfo(projectResourceID)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidProjectResourceID, err)
	}

	tenantResp, err := azdClient.Account().LookupTenant(ctx, &azdext.LookupTenantRequest{
		SubscriptionId: info.SubscriptionID,
	})
	if err != nil {
		return nil, fmt.Errorf("lookup tenant: %w", err)
	}

	cred, err := azidentity.NewAzureDeveloperCLICredential(&azidentity.AzureDeveloperCLICredentialOptions{
		TenantID:                   tenantResp.TenantId,
		AdditionallyAllowedTenants: []string{"*"},
	})
	if err != nil {
		return nil, fmt.Errorf("create credential: %w", err)
	}

	graphClient, err := graphsdk.NewGraphClient(cred, nil)
	if err != nil {
		return nil, fmt.Errorf("create graph client: %w", err)
	}

	userProfile, err := graphClient.Me().Get(ctx)
	if err != nil {
		// Graph /me rejects app-only / SPN tokens with a
		// canonical "delegated authentication flow" message.
		// Surface it as a typed error so doctor can render a
		// SPN-aware Skip instead of a generic transient retry.
		if isSPNDelegatedAuthError(err) {
			return nil, fmt.Errorf("%w: %w", ErrSPNDelegatedAuthRequired, err)
		}
		return nil, fmt.Errorf("retrieve user profile: %w", err)
	}

	hasRole, err := hasAnyRoleAssignment(
		ctx, cred, userProfile.Id, sufficientAIUserRoles, info.ProjectScope)
	if err != nil {
		return nil, fmt.Errorf("list role assignments: %w", err)
	}

	return &DeveloperRBACResult{
		PrincipalID:         userProfile.Id,
		PrincipalDisplay:    userProfile.DisplayName,
		HasSufficientAIRole: hasRole,
		ProjectScope:        info.ProjectScope,
		AccountName:         info.AccountName,
		ProjectName:         info.ProjectName,
	}, nil
}

// isSPNDelegatedAuthError reports whether the error message matches
// the canonical Microsoft Graph response for a Graph `/me` call
// rejected because the access token was issued to a service
// principal (Graph requires a user-delegated token). The Graph
// response carries `Authorization_RequestDenied` with a message
// fragment `/me request is only valid with delegated authentication
// flow`; the match is intentionally loose against substrings so it
// survives minor wording changes from the Graph service. Falsely
// positive matches simply re-route the error onto the SPN-aware
// Skip message — strictly better than the generic transient retry
// hint either way.
func isSPNDelegatedAuthError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return containsAnyCI(msg,
		"/me request is only valid with delegated authentication flow",
		"only valid with delegated authentication",
		"requires delegated authentication",
	)
}

// containsAnyCI returns true if any needle (lowercased) appears as a
// substring of haystack (lowercased). Pulled out as a helper because
// the canonical Graph message capitalization has shifted historically
// (e.g., "request" vs "Request") and a case-insensitive match is
// more robust than guessing the current spelling.
func containsAnyCI(haystack string, needles ...string) bool {
	lower := strings.ToLower(haystack)
	for _, n := range needles {
		if strings.Contains(lower, strings.ToLower(n)) {
			return true
		}
	}
	return false
}
