// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package foundryaccess

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/authorization/armauthorization/v2"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/cognitiveservices/armcognitiveservices/v2"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/google/uuid"
	"google.golang.org/grpc"
)

// FoundryUserRoleID is the built-in Foundry User (formerly Azure AI User) role.
const FoundryUserRoleID = "53ca6127-db72-4b80-b1b0-d745d6d5456d"

// AccountClient is the host-owned identity API used by Foundry access.
// azdext.AzdClient.Account() implements it without requiring an active environment.
type AccountClient interface {
	LookupTenant(context.Context, *azdext.LookupTenantRequest, ...grpc.CallOption) (*azdext.LookupTenantResponse, error)
	GetCurrentPrincipal(
		context.Context, *azdext.GetCurrentPrincipalRequest, ...grpc.CallOption,
	) (*azdext.GetCurrentPrincipalResponse, error)
}

// CredentialProvider creates a credential for the host-selected subscription and
// user-access tenant. It must authenticate the same identity as AccountClient.
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
	account     AccountClient
	credentials CredentialProvider
	options     arm.ClientOptions
}

// NewAzureBackend creates an Azure backend without making network calls.
func NewAzureBackend(account AccountClient, options *AzureOptions) (*AzureBackend, error) {
	if account == nil {
		return nil, fmt.Errorf("a host account client is required")
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
	tenant, err := b.account.LookupTenant(ctx, &azdext.LookupTenantRequest{SubscriptionId: id.SubscriptionID})
	if err != nil {
		return Project{}, fmt.Errorf("resolving project tenant: %w", err)
	}
	if tenant == nil || !validUUID(tenant.TenantId) {
		return Project{}, fmt.Errorf("host returned an invalid project tenant")
	}
	principal, err := b.account.GetCurrentPrincipal(ctx, &azdext.GetCurrentPrincipalRequest{SubscriptionId: id.SubscriptionID})
	if err != nil {
		return Project{}, fmt.Errorf("resolving current project principal: %w", err)
	}
	if principal == nil || !validUUID(principal.ObjectId) {
		return Project{}, fmt.Errorf("host returned an invalid current principal")
	}
	var principalType string
	switch principal.PrincipalType {
	case azdext.PrincipalType_PRINCIPAL_TYPE_USER:
		principalType = string(armauthorization.PrincipalTypeUser)
	case azdext.PrincipalType_PRINCIPAL_TYPE_SERVICE_PRINCIPAL:
		principalType = string(armauthorization.PrincipalTypeServicePrincipal)
	default:
		return Project{}, fmt.Errorf("host returned an unsupported current principal type")
	}
	credential, err := b.credentials(ctx, id.SubscriptionID, tenant.TenantId)
	if err != nil {
		return Project{}, fmt.Errorf("creating project credential: %w", err)
	}
	if credential == nil {
		return Project{}, fmt.Errorf("project credential provider returned no credential")
	}
	client, err := armcognitiveservices.NewProjectsClient(id.SubscriptionID, credential, &b.options)
	if err != nil {
		return Project{}, fmt.Errorf("creating project ARM client: %w", err)
	}
	response, err := client.Get(ctx, id.ResourceGroupName, id.Parent.Name, id.Name, nil)
	if err != nil {
		return Project{}, fmt.Errorf("verifying project endpoint through ARM: %w", err)
	}
	if response.Properties == nil || response.Properties.Endpoints["AI Foundry API"] == nil {
		return Project{}, fmt.Errorf("ARM project has no advertised Foundry API endpoint")
	}
	advertised := *response.Properties.Endpoints["AI Foundry API"]
	if _, err := validateTarget(Target{ProjectID: target.ProjectID, Endpoint: advertised}); err != nil ||
		!strings.EqualFold(strings.TrimRight(advertised, "/"), strings.TrimRight(target.Endpoint, "/")) {
		return Project{}, fmt.Errorf("ARM project's advertised endpoint does not match the requested Foundry project")
	}
	return Project{
		Target:   Target{ProjectID: id.String(), Endpoint: strings.TrimRight(advertised, "/")},
		TenantID: tenant.TenantId, PrincipalID: principal.ObjectId, PrincipalType: principalType, Credential: credential,
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
		return false, fmt.Errorf("current identity changed since the Foundry access probe; retry the operation")
	}
	id, _ := arm.ParseResourceID(current.ProjectID) // verified by Resolve
	client, err := armauthorization.NewRoleAssignmentsClient(id.SubscriptionID, current.Credential, &b.options)
	if err != nil {
		return false, fmt.Errorf("creating role assignments client: %w", err)
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
			return false, fmt.Errorf("reconciling concurrent role assignment: %w", readErr)
		}
		if exists {
			return false, nil
		}
	}
	return false, fmt.Errorf("creating project-scoped Foundry User assignment: %w", err)
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
			return false, fmt.Errorf("checking existing project role assignment: %w", err)
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
		return nil, fmt.Errorf("a Foundry project ARM resource ID is required")
	}
	u, err := url.Parse(target.Endpoint)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" ||
		u.ForceQuery || strings.Contains(target.Endpoint, "#") || u.Opaque != "" ||
		(u.Port() != "" && u.Port() != "443") {
		return nil, fmt.Errorf("a credential-free HTTPS Foundry project endpoint without a query or fragment is required")
	}
	parts := strings.Split(strings.TrimSuffix(u.Path, "/"), "/")
	if len(parts) != 4 || parts[0] != "" || parts[1] != "api" || parts[2] != "projects" ||
		!strings.EqualFold(parts[3], id.Name) || u.RawPath != "" {
		return nil, fmt.Errorf("Foundry endpoint must identify the project named by the ARM resource ID")
	}
	return id, nil
}
