// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package access

import (
	"context"
	"crypto/sha256"
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/authorization/armauthorization/v2"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/cognitiveservices/armcognitiveservices/v2"
	"github.com/google/uuid"
)

// FoundryUserRoleID is the built-in Foundry User (formerly Azure AI User) role.
const FoundryUserRoleID = "53ca6127-db72-4b80-b1b0-d745d6d5456d"

// Principal identifies an authenticated object in the subscription's resource tenant.
// Type must be "User" or "ServicePrincipal"; ObjectID is not an application client ID.
type Principal struct {
	ObjectID string
	Type     string
}

// IdentityResolver provides tenant and current-principal resolution without
// exposing any host SDK, gRPC contract, or extension-specific type. Extensions
// implement this interface using their existing host account APIs.
type IdentityResolver interface {
	ResolveTenant(context.Context, string) (string, error)
	ResolvePrincipal(context.Context, string) (Principal, error)
}

// CredentialProvider creates a credential for the host-selected subscription and
// user-access tenant. It must authenticate the same identity as IdentityResolver.
type CredentialProvider func(context.Context, string, string) (azcore.TokenCredential, error)

// AzureOptions configures the Azure backend, including cloud and transport options.
type AzureOptions struct {
	ClientOptions arm.ClientOptions
	// CredentialProvider defaults to a tenant-scoped AzureDeveloperCLICredential.
	CredentialProvider CredentialProvider
}

// AzureBackend verifies project identity through ARM and ensures only the current
// principal's Foundry User assignment. It never uses role-name heuristics to infer
// effective data-plane access. It retains no mutable per-project or principal state.
type AzureBackend struct {
	account     IdentityResolver
	credentials CredentialProvider
	options     arm.ClientOptions
}

// NewAzureBackend creates an Azure backend without making network calls.
func NewAzureBackend(account IdentityResolver, options *AzureOptions) (*AzureBackend, error) {
	if account == nil {
		return nil, failure(InvalidOptions, "an identity resolver is required", nil)
	}
	b := &AzureBackend{account: account}
	if options != nil {
		b.options = options.ClientOptions
		b.credentials = options.CredentialProvider
	}
	if b.credentials == nil {
		b.credentials = func(_ context.Context, _, tenant string) (azcore.TokenCredential, error) {
			return azidentity.NewAzureDeveloperCLICredential(&azidentity.AzureDeveloperCLICredentialOptions{
				TenantID: tenant,
			})
		}
	}
	return b, nil
}

// Resolve validates the ARM project and its advertised data-plane endpoint before
// returning a credential to a probe. DNS labels are never treated as ARM account names.
// ARM lookup failures remain errors, not evidence that a data-plane grant is needed.
func (b *AzureBackend) Resolve(ctx context.Context, target Target) (Project, error) {
	id, err := validateTarget(target)
	if err != nil {
		return Project{}, err
	}
	tenant, err := b.account.ResolveTenant(ctx, id.SubscriptionID)
	if err != nil {
		return Project{}, failure(IdentityUnavailable, "resolving project tenant", err)
	}
	if !validUUID(tenant) {
		return Project{}, failure(IdentityUnavailable, "identity resolver returned an invalid project tenant", nil)
	}
	principal, err := b.account.ResolvePrincipal(ctx, id.SubscriptionID)
	if err != nil {
		return Project{}, failure(IdentityUnavailable, "resolving current project principal", err)
	}
	if !validUUID(principal.ObjectID) {
		return Project{}, failure(IdentityUnavailable, "identity resolver returned an invalid current principal", nil)
	}
	var principalType string
	switch principal.Type {
	case "User":
		principalType = string(armauthorization.PrincipalTypeUser)
	case "ServicePrincipal":
		principalType = string(armauthorization.PrincipalTypeServicePrincipal)
	default:
		return Project{}, failure(IdentityUnavailable, "identity resolver returned an unsupported current principal type", nil)
	}
	credential, err := b.credentials(ctx, id.SubscriptionID, tenant)
	if err != nil {
		return Project{}, failure(IdentityUnavailable, "creating project credential", err)
	}
	if credential == nil {
		return Project{}, failure(IdentityUnavailable, "project credential provider returned no credential", nil)
	}
	client, err := armcognitiveservices.NewProjectsClient(id.SubscriptionID, credential, &b.options)
	if err != nil {
		return Project{}, failure(ResolutionFailed, "creating project ARM client", err)
	}
	response, err := client.Get(ctx, id.ResourceGroupName, id.Parent.Name, id.Name, nil)
	if err != nil {
		return Project{}, failure(ResolutionFailed, "verifying project endpoint through ARM", err)
	}
	if response.Properties == nil || response.Properties.Endpoints["AI Foundry API"] == nil {
		return Project{}, failure(TargetMismatch, "ARM project has no advertised Foundry API endpoint", nil)
	}
	advertised := *response.Properties.Endpoints["AI Foundry API"]
	if _, err := validateTarget(Target{ProjectID: target.ProjectID, Endpoint: advertised}); err != nil ||
		!strings.EqualFold(strings.TrimRight(advertised, "/"), strings.TrimRight(target.Endpoint, "/")) {
		return Project{}, failure(TargetMismatch,
			"ARM project's advertised endpoint does not match the requested Foundry project", nil)
	}
	return Project{
		Target:   Target{ProjectID: id.String(), Endpoint: strings.TrimRight(advertised, "/")},
		TenantID: tenant, PrincipalID: principal.ObjectID, PrincipalType: principalType, Credential: credential,
	}, nil
}

// EnsureGrant idempotently grants Foundry User at the project scope. It re-resolves
// identity before writing, rejecting a principal/tenant change since the probe.
// A 409 is accepted only after finding the same unconditional assignment at that scope.
func (b *AzureBackend) EnsureGrant(ctx context.Context, project Project) (bool, error) {
	current, err := b.Resolve(ctx, project.Target)
	if err != nil {
		return false, err
	}
	if !strings.EqualFold(current.PrincipalID, project.PrincipalID) ||
		!strings.EqualFold(current.TenantID, project.TenantID) || current.PrincipalType != project.PrincipalType {
		return false, failure(IdentityUnavailable,
			"current identity changed since the Foundry access probe; retry the operation", nil)
	}
	id, _ := arm.ParseResourceID(current.ProjectID) // verified by Resolve
	client, err := armauthorization.NewRoleAssignmentsClient(id.SubscriptionID, current.Credential, &b.options)
	if err != nil {
		return false, failure(GrantFailed, "creating role assignments client", err)
	}
	roleID := "/subscriptions/" + id.SubscriptionID + "/providers/Microsoft.Authorization/roleDefinitions/" + FoundryUserRoleID
	// Reuse pre-existing assignments, including assignments made with another name.
	exists, err := matchingAssignment(ctx, client, current, roleID)
	if err != nil || exists {
		return false, err
	}
	principalType := armauthorization.PrincipalType(current.PrincipalType)
	_, err = client.Create(ctx, current.ProjectID, assignmentName(current.ProjectID, current.PrincipalID, roleID),
		armauthorization.RoleAssignmentCreateParameters{Properties: &armauthorization.RoleAssignmentProperties{
			PrincipalID: &current.PrincipalID, PrincipalType: &principalType, RoleDefinitionID: &roleID,
		}}, nil)
	if err == nil {
		return true, nil
	}
	if response, ok := errors.AsType[*azcore.ResponseError](err); ok && response.StatusCode == http.StatusConflict &&
		response.ErrorCode == "RoleAssignmentExists" {
		exists, readErr := matchingAssignment(ctx, client, current, roleID)
		if readErr != nil {
			return false, failure(GrantFailed, "reconciling concurrent role assignment", readErr)
		}
		if exists {
			return false, nil
		}
	}
	return false, failure(GrantFailed, "creating project-scoped Foundry User assignment", err)
}

func matchingAssignment(
	ctx context.Context, client *armauthorization.RoleAssignmentsClient, project Project, roleID string,
) (bool, error) {
	filter := "atScope() and principalId eq '" + project.PrincipalID + "'"
	pager := client.NewListForScopePager(project.ProjectID, &armauthorization.RoleAssignmentsClientListForScopeOptions{
		Filter: &filter,
	})
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return false, failure(GrantFailed, "checking existing project role assignment", err)
		}
		for _, assignment := range page.Value {
			if assignment == nil || assignment.Properties == nil {
				continue
			}
			p := assignment.Properties
			if p.PrincipalID != nil && p.RoleDefinitionID != nil && p.Scope != nil &&
				strings.EqualFold(*p.PrincipalID, project.PrincipalID) && strings.EqualFold(*p.RoleDefinitionID, roleID) &&
				strings.EqualFold(*p.Scope, project.ProjectID) && (p.Condition == nil || *p.Condition == "") {
				return true, nil
			}
		}
	}
	return false, nil
}

func assignmentName(scope, principal, role string) string {
	sum := sha256.Sum256([]byte(strings.ToLower(scope + "\n" + principal + "\n" + role)))
	// UUIDv8 reserves the version/variant bits and leaves the remaining hash bits intact.
	sum[6] = (sum[6] & 0x0f) | 0x80
	sum[8] = (sum[8] & 0x3f) | 0x80
	return uuid.UUID(sum[:16]).String()
}

func validUUID(value string) bool {
	id, err := uuid.Parse(value)
	return err == nil && id != uuid.Nil && strings.EqualFold(value, id.String())
}

func validateTarget(target Target) (*arm.ResourceID, error) {
	id, err := arm.ParseResourceID(target.ProjectID)
	if err != nil || id.Parent == nil || !validUUID(id.SubscriptionID) || id.ResourceGroupName == "" ||
		!strings.EqualFold(id.ResourceType.String(), "Microsoft.CognitiveServices/accounts/projects") {
		return nil, failure(InvalidTarget, "a Foundry project ARM resource ID is required", nil)
	}
	u, err := url.Parse(target.Endpoint)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" ||
		u.ForceQuery || strings.Contains(target.Endpoint, "#") || u.Opaque != "" ||
		(u.Port() != "" && u.Port() != "443") {
		return nil, failure(InvalidTarget,
			"a credential-free HTTPS Foundry project endpoint without a query or fragment is required", nil)
	}
	parts := strings.Split(strings.TrimSuffix(u.Path, "/"), "/")
	if len(parts) != 4 || parts[0] != "" || parts[1] != "api" || parts[2] != "projects" ||
		!strings.EqualFold(parts[3], id.Name) || u.RawPath != "" {
		return nil, failure(InvalidTarget, "Foundry endpoint must identify the project named by the ARM resource ID", nil)
	}
	return id, nil
}
