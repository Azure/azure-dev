// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// cspell:ignore permissiondenied authorizationfailed authorizationpermissionmismatch aiservices managementgroups
package foundry

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/authorization/armauthorization/v2"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/google/uuid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// FoundryUserRole is the built-in Foundry User role definition.
const FoundryUserRole = "53ca6127-db72-4b80-b1b0-d745d6d5456d"

// CognitiveServicesUserRole grants equivalent Foundry data-plane access for legacy assignments.
const CognitiveServicesUserRole = "a97b65f3-24c7-4388-baec-2e87135dc908"

// FoundryProjectManagerRole grants project management and Foundry data-plane access.
const FoundryProjectManagerRole = "eadc314b-1a2d-4efa-be10-5d325db5065e"

// FoundryOwnerRole grants Foundry management and data-plane access.
const FoundryOwnerRole = "c883944f-8b7b-4483-af10-35834be79c4a"

var projectAccessRoles = []string{
	FoundryUserRole,
	CognitiveServicesUserRole,
	FoundryProjectManagerRole,
	FoundryOwnerRole,
}

// ProjectAccessRoles returns the built-in roles sufficient for Foundry agent data-plane access.
// Azure Owner and Contributor are intentionally excluded: neither grants data actions.
func ProjectAccessRoles() []string {
	return slices.Clone(projectAccessRoles)
}

// ProjectAccessScope validates that an ARM project ID identifies the endpoint being probed.
// Errors deliberately omit the endpoint, which may contain authored credentials.
func ProjectAccessScope(projectID, endpoint string) (*arm.ResourceID, error) {
	id, err := arm.ParseResourceID(strings.TrimSpace(projectID))
	if err != nil || !strings.EqualFold(id.ResourceType.String(), "Microsoft.CognitiveServices/accounts/projects") ||
		id.SubscriptionID == "" || id.ResourceGroupName == "" || id.Name == "" || id.Parent == nil ||
		id.Parent.Name == "" || len(strings.Split(id.String(), "/")) != 11 {
		return nil, fmt.Errorf("project ID is not a valid Foundry project resource ID")
	}
	u, err := url.Parse(strings.TrimRight(endpoint, "/"))
	if err != nil || !strings.EqualFold(u.Scheme, "https") || u.User != nil ||
		u.RawQuery != "" || u.Fragment != "" || u.Port() != "" ||
		!strings.EqualFold(u.Hostname(), id.Parent.Name+".services.ai.azure.com") ||
		!strings.EqualFold(u.Path, "/api/projects/"+id.Name) {
		return nil, fmt.Errorf("project ID does not identify the active Foundry project endpoint")
	}
	return id, nil
}

// ProjectScopeFromEnvironment returns a scope only for the selected environment's endpoint.
// An unrelated explicit endpoint never borrows the active environment's project or tenant.
func ProjectScopeFromEnvironment(endpoint string, values map[string]string) (*arm.ResourceID, error) {
	configured := values["FOUNDRY_PROJECT_ENDPOINT"]
	if configured == "" {
		configured = values["AZURE_AI_PROJECT_ENDPOINT"]
	}
	if configured == "" || strings.TrimSpace(values["AZURE_AI_PROJECT_ID"]) == "" {
		return nil, nil
	}
	normalized := projectEndpointIdentity(configured)
	if normalized == "" || normalized != projectEndpointIdentity(endpoint) {
		return nil, nil
	}
	resource, err := ProjectAccessScope(values["AZURE_AI_PROJECT_ID"], normalized)
	if err != nil {
		return nil, fmt.Errorf("AZURE_AI_PROJECT_ID: %w", err)
	}
	if subscription := strings.TrimSpace(values["AZURE_SUBSCRIPTION_ID"]); subscription != "" &&
		!strings.EqualFold(subscription, resource.SubscriptionID) {
		return nil, fmt.Errorf("AZURE_SUBSCRIPTION_ID conflicts with AZURE_AI_PROJECT_ID")
	}
	return resource, nil
}

func projectEndpointIdentity(endpoint string) string {
	u, err := url.Parse(strings.TrimSpace(endpoint))
	if err != nil || !strings.EqualFold(u.Scheme, "https") || u.Port() != "" ||
		u.User != nil || u.RawQuery != "" || u.Fragment != "" ||
		!strings.HasSuffix(strings.ToLower(u.Hostname()), ".services.ai.azure.com") {
		return ""
	}
	return "https://" + strings.ToLower(u.Hostname()) + strings.TrimRight(u.EscapedPath(), "/")
}

// RoleAssignmentsSkipped applies the shared environment-over-process boolean precedence.
func RoleAssignmentsSkipped(environmentValue, processValue string) bool {
	if environmentValue == "" {
		environmentValue = processValue
	}
	skip, _ := strconv.ParseBool(environmentValue)
	return skip
}

// ProjectEnvironment reads the selected azd environment without spawning another CLI process.
type ProjectEnvironment interface {
	GetValues(context.Context, *azdext.GetEnvironmentRequest, ...grpc.CallOption) (*azdext.KeyValueListResponse, error)
}

// LoadProjectEnvironment reads the active environment and its values through the azd host.
func LoadProjectEnvironment(ctx context.Context, environment ProjectEnvironment) (map[string]string, error) {
	// An empty name asks the host for the active environment.
	response, err := environment.GetValues(ctx, &azdext.GetEnvironmentRequest{})
	if err != nil {
		return nil, fmt.Errorf("read the active azd environment: %w", err)
	}
	values := make(map[string]string, len(response.GetKeyValues()))
	for _, entry := range response.GetKeyValues() {
		values[entry.GetKey()] = entry.GetValue()
	}
	return values, nil
}

// TenantResolver resolves the user-access tenant for a subscription.
type TenantResolver interface {
	LookupTenant(context.Context, *azdext.LookupTenantRequest, ...grpc.CallOption) (*azdext.LookupTenantResponse, error)
}

// LookupProjectTenant resolves a non-empty user-access tenant, including for guest users.
func LookupProjectTenant(ctx context.Context, account TenantResolver, subscriptionID string) (string, error) {
	response, err := account.LookupTenant(ctx, &azdext.LookupTenantRequest{SubscriptionId: subscriptionID})
	if err != nil {
		return "", err
	}
	tenantID := strings.TrimSpace(response.GetTenantId())
	if tenantID == "" {
		return "", fmt.Errorf("azd returned an empty user-access tenant")
	}
	return tenantID, nil
}

// PrincipalResolver resolves the current identity in the project's resource tenant.
type PrincipalResolver interface {
	GetCurrentPrincipal(context.Context, *azdext.GetCurrentPrincipalRequest, ...grpc.CallOption) (
		*azdext.GetCurrentPrincipalResponse, error)
}

// ProjectRoles checks inherited access and assigns a project-scoped role.
type ProjectRoles interface {
	HasAccess(context.Context, string, string) (bool, error)
	Assign(context.Context, string, string, armauthorization.PrincipalType) error
}

type projectRoles struct {
	client *armauthorization.RoleAssignmentsClient
}

func newProjectRoles(subscriptionID string, credential azcore.TokenCredential) (ProjectRoles, error) {
	client, err := armauthorization.NewRoleAssignmentsClient(subscriptionID, credential, nil)
	if err != nil {
		return nil, err
	}
	return &projectRoles{client: client}, nil
}

func (r *projectRoles) HasAccess(ctx context.Context, scope, principalID string) (bool, error) {
	return r.hasRoles(ctx, scope, principalID, projectAccessRoles)
}

// HasAnyRoleAssignment checks the specified roles at the target scope, including inherited assignments.
func HasAnyRoleAssignment(
	ctx context.Context, credential azcore.TokenCredential, principalID string, roleIDs []string, scope string,
) (bool, error) {
	resource, err := arm.ParseResourceID(scope)
	if err != nil || resource.SubscriptionID == "" {
		return false, fmt.Errorf("a subscription-scoped resource ID is required for role inspection")
	}
	client, err := armauthorization.NewRoleAssignmentsClient(resource.SubscriptionID, credential, nil)
	if err != nil {
		return false, err
	}
	return (&projectRoles{client: client}).hasRoles(ctx, scope, principalID, roleIDs)
}

func (r *projectRoles) hasRoles(ctx context.Context, scope, principalID string, roleIDs []string) (bool, error) {
	pager := r.client.NewListForScopePager(scope, &armauthorization.RoleAssignmentsClientListForScopeOptions{
		Filter: new(fmt.Sprintf("assignedTo('%s')", principalID)),
	})
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return false, err
		}
		for _, assignment := range page.Value {
			if assignment.Properties == nil || assignment.Properties.RoleDefinitionID == nil ||
				assignment.Properties.Scope == nil {
				continue
			}
			assignmentScope := strings.ToLower(strings.TrimRight(*assignment.Properties.Scope, "/"))
			targetScope := strings.ToLower(strings.TrimRight(scope, "/"))
			// ARM can also return inherited management-group roles, whose IDs are not path ancestors.
			const managementGroupPrefix = "/providers/microsoft.management/managementgroups/"
			isManagementGroup := strings.HasPrefix(assignmentScope, managementGroupPrefix) &&
				len(strings.Split(strings.TrimPrefix(assignmentScope, managementGroupPrefix), "/")) == 1
			if assignmentScope == "" || (assignmentScope != targetScope &&
				!strings.HasPrefix(targetScope, assignmentScope+"/") && !isManagementGroup) {
				continue
			}
			for _, role := range roleIDs {
				if strings.EqualFold(roleDefinitionSuffix(*assignment.Properties.RoleDefinitionID), role) {
					return true, nil
				}
			}
		}
	}
	return false, nil
}

func roleDefinitionSuffix(id string) string {
	return id[strings.LastIndex(id, "/")+1:]
}

func (r *projectRoles) Assign(
	ctx context.Context, scope, principalID string, principalType armauthorization.PrincipalType,
) error {
	// A stable name makes simultaneous preparations for the same project/principal idempotent.
	name := uuid.NewSHA1(uuid.NameSpaceURL, []byte(strings.ToLower(scope+"/"+principalID+"/"+FoundryUserRole))).String()
	resource, err := arm.ParseResourceID(scope)
	if err != nil || resource.SubscriptionID == "" {
		return fmt.Errorf("a subscription-scoped project resource ID is required for role assignment")
	}
	subscriptionID := resource.SubscriptionID
	_, err = r.client.Create(ctx, scope, name, armauthorization.RoleAssignmentCreateParameters{
		Properties: &armauthorization.RoleAssignmentProperties{
			RoleDefinitionID: new("/subscriptions/" + subscriptionID +
				"/providers/Microsoft.Authorization/roleDefinitions/" + FoundryUserRole),
			PrincipalID:   new(principalID),
			PrincipalType: new(principalType),
		},
	}, nil)
	if response, ok := errors.AsType[*azcore.ResponseError](err); ok &&
		response.StatusCode == http.StatusConflict && response.ErrorCode == "RoleAssignmentExists" {
		return nil
	}
	return err
}

// AccessPreparation prepares access only after a data-plane probe establishes an authorization denial.
// Probe must be a non-mutating check of the operation's required Foundry read access.
type AccessPreparation struct {
	Account    PrincipalResolver
	Credential azcore.TokenCredential
	Scope      string
	Probe      func(context.Context) error
	Warn       func(string)
	// Progress optionally reports a grant attempt, propagation wait, and confirmed readiness.
	// Calls are synchronous and contain plain text; the caller owns formatting and output.
	// A grant or wait notification does not imply success. Readiness requires a successful probe.
	Progress func(string)
	// Roles optionally replaces the ARM client; by default it is created only after a denied probe.
	Roles ProjectRoles
	// RetryDelay is the interval for a newly granted role to reach the data plane.
	RetryDelay time.Duration
	Attempts   int
}

// ResolvePrincipal resolves the resource-tenant object ID and ARM principal type through the azd host.
func ResolvePrincipal(
	ctx context.Context, account PrincipalResolver, subscriptionID string,
) (string, armauthorization.PrincipalType, error) {
	principal, err := account.GetCurrentPrincipal(ctx, &azdext.GetCurrentPrincipalRequest{
		SubscriptionId: subscriptionID,
	})
	if err != nil {
		return "", "", err
	}
	if strings.TrimSpace(principal.GetObjectId()) == "" {
		return "", "", fmt.Errorf("the azd host returned an empty current principal object ID")
	}
	switch principal.GetPrincipalType() {
	case azdext.PrincipalType_PRINCIPAL_TYPE_USER:
		return principal.GetObjectId(), armauthorization.PrincipalTypeUser, nil
	case azdext.PrincipalType_PRINCIPAL_TYPE_SERVICE_PRINCIPAL:
		return principal.GetObjectId(), armauthorization.PrincipalTypeServicePrincipal, nil
	default:
		return "", "", fmt.Errorf("the azd host returned an unsupported current principal type: %v",
			principal.GetPrincipalType())
	}
}

// IsProjectAccessDenied distinguishes Foundry authorization errors from unrelated HTTP 403 responses.
func IsProjectAccessDenied(err error) bool {
	response, ok := errors.AsType[*azcore.ResponseError](err)
	if !ok || response.StatusCode != http.StatusForbidden {
		return false
	}
	switch strings.ToLower(response.ErrorCode) {
	case "permissiondenied", "authorizationfailed", "authorizationpermissionmismatch":
		return true
	default:
		return strings.Contains(strings.ToLower(response.Error()), "microsoft.cognitiveservices/accounts/aiservices/agents/")
	}
}

// Ensure checks existing access, conditionally grants Foundry User, and waits for propagation.
// Optional ARM failures are warnings; the caller's actual operation remains authoritative.
func (p AccessPreparation) Ensure(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	probeErr := p.Probe(ctx)
	if err := AccessCancellation(ctx, probeErr); err != nil {
		return err
	}
	if probeErr == nil {
		return nil
	}
	if !IsProjectAccessDenied(probeErr) {
		p.Warn("Could not establish missing Foundry access; continuing without assigning a role.")
		return nil
	}
	resource, err := arm.ParseResourceID(p.Scope)
	if err != nil || resource.SubscriptionID == "" {
		return fmt.Errorf("a subscription-scoped project resource ID is required for access preparation")
	}
	principalID, principalType, err := ResolvePrincipal(ctx, p.Account, resource.SubscriptionID)
	if cancelErr := AccessCancellation(ctx, err); cancelErr != nil {
		return cancelErr
	}
	if err != nil {
		p.Warn("Could not resolve the current principal; continuing without assigning a role.")
		return nil
	}
	roles := p.Roles
	if roles == nil {
		roles, err = newProjectRoles(resource.SubscriptionID, p.Credential)
		if err != nil {
			p.Warn("Could not create the Foundry role-assignment client; continuing without assigning a role.")
			return nil
		}
	}
	hasAccess, err := roles.HasAccess(ctx, p.Scope, principalID)
	if cancelErr := AccessCancellation(ctx, err); cancelErr != nil {
		return cancelErr
	}
	if err != nil {
		p.Warn("Could not inspect inherited Foundry roles; continuing without assigning a role.")
		return nil
	}
	if hasAccess {
		p.Warn("A sufficient Foundry role is already assigned; " +
			"if access is still denied, verify role propagation or policy.")
		return nil
	}
	if p.Progress != nil {
		p.Progress("Assigning Foundry User to the current principal.")
	}
	if err := roles.Assign(ctx, p.Scope, principalID, principalType); err != nil {
		if cancelErr := AccessCancellation(ctx, err); cancelErr != nil {
			return cancelErr
		}
		p.Warn(fmt.Sprintf(
			"Could not assign Foundry User. Ask an administrator to run: "+
				"az role assignment create --assignee %q --role %s --scope %q. Continuing.",
			principalID, FoundryUserRole, p.Scope))
		return nil
	}
	if p.Progress != nil {
		p.Progress("Waiting for Foundry access to propagate.")
	}
	attempts := p.Attempts
	if attempts <= 0 {
		attempts = 12
	}
	delay := p.RetryDelay
	if delay <= 0 {
		delay = 5 * time.Second
	}
	for attempt := range attempts {
		err := p.Probe(ctx)
		if cancelErr := AccessCancellation(ctx, err); cancelErr != nil {
			return cancelErr
		}
		if err == nil {
			if p.Progress != nil {
				p.Progress("Foundry access is ready.")
			}
			return nil
		}
		if !IsProjectAccessDenied(err) {
			p.Warn("Foundry access readiness could not be confirmed; continuing with the operation.")
			return nil
		}
		if attempt == attempts-1 {
			return fmt.Errorf("Foundry User was assigned but access has not propagated; wait and retry: %w", err)
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
	return nil
}

// AccessCancellation preserves local and transported cancellation/deadline errors during access preparation.
func AccessCancellation(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	switch status.Code(err) {
	case codes.Canceled, codes.DeadlineExceeded:
		return err
	}
	return nil
}
