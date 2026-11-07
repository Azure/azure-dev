// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// Package access prepares and verifies Foundry project data-plane access before
// remote operations.
//
// Fresh Foundry projects can be provisioned by a subscription Owner or Contributor
// while Foundry data-plane calls fail with HTTP 403 on AIServices actions because
// control-plane roles do not grant Foundry data-plane access.
//
// This package implements the ARM-first readiness flow shared by Foundry extensions:
//  1. Verify offline that the selected endpoint matches the active environment's
//     Foundry project endpoint and ARM resource ID, and respect
//     AZD_AGENT_SKIP_ROLE_ASSIGNMENTS.
//  2. Inspect ARM role assignments on the project scope (including inherited
//     subscription, resource group, account, and management group assignments)
//     before touching the Foundry data plane, avoiding negative authorization
//     cache priming.
//  3. Deterministically assign Foundry User at the verified project scope when
//     needed, treating RoleAssignmentExists conflicts as success.
//  4. Settle for 15 seconds after a new assignment, then poll a caller-supplied
//     readiness probe every 5 seconds (up to 5 minutes by default) across
//     propagation 403s and project synchronization 404s.
package access

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/authorization/armauthorization/v2"
	"github.com/azure/azure-dev/cli/shared/foundry/ferrors"
	"github.com/google/uuid"
)

// FoundryUserRoleID is the built-in Foundry User (formerly Azure AI User) role definition ID.
const FoundryUserRoleID = "53ca6127-db72-4b80-b1b0-d745d6d5456d"

// SkipRoleAssignmentsEnvKey is the environment/process variable that disables
// automatic Foundry developer role checks and assignments when set to a truthy boolean.
const SkipRoleAssignmentsEnvKey = "AZD_AGENT_SKIP_ROLE_ASSIGNMENTS"

// SufficientDataPlaneRoles lists role definition IDs whose dataActions cover
// Microsoft.CognitiveServices/* (including the AIServices namespace required for
// Foundry agents, toolboxes, skills, and project data-plane operations).
var SufficientDataPlaneRoles = []string{
	FoundryUserRoleID,
	"a97b65f3-24c7-4388-baec-2e87135dc908", // Cognitive Services User
	"eadc314b-1a2d-4efa-be10-5d325db5065e", // Foundry Project Manager
	"c883944f-8b7b-4483-af10-35834be79c4a", // Foundry Owner
}

var safeScopeSegment = regexp.MustCompile(`^[A-Za-z0-9_.()-]+$`)

// Principal represents the signed-in Azure principal in the subscription's resource tenant.
type Principal struct {
	ObjectID string
	Type     armauthorization.PrincipalType
}

// EnvironmentReader reads key-value pairs from the active azd environment.
type EnvironmentReader interface {
	Values(ctx context.Context) (map[string]string, error)
}

// AccountReader resolves subscription tenant and resource-tenant principal identity.
type AccountReader interface {
	LookupTenant(ctx context.Context, subscriptionID string) (string, error)
	CurrentPrincipal(ctx context.Context, subscriptionID string) (Principal, error)
}

// Scope holds a verified Foundry project ARM resource ID, user-access tenant ID,
// and whether role assignment checks are skipped.
type Scope struct {
	Resource *arm.ResourceID
	TenantID string
	Skip     bool
}

// ReadPolicy controls whether an unconfirmed 403 during readiness probing is
// permitted so a subsequent deployment write can establish custom-role access.
type ReadPolicy int

const (
	// ReadRequired requires the readiness probe to succeed (or return an item-level 404).
	ReadRequired ReadPolicy = iota
	// ReadOptionalForDeploy permits an unconfirmed authorization denial on the readiness
	// probe because a custom role may allow the deployment write without read permission.
	ReadOptionalForDeploy
)

// Client prepares and verifies Foundry project access.
type Client struct {
	Environment        EnvironmentReader
	Account            AccountReader
	NewCredential      func(tenantID string) (azcore.TokenCredential, error)
	RoleClient         *armauthorization.RoleAssignmentsClient
	Wait               func(context.Context, time.Duration) error
	Attempts           int
	Timeout            time.Duration
	IsCanceled         func(error) bool
	ResourceNoun       string
	DefaultOperation   string
	AssigningMessage   string
	PropagatingMessage string
	SyncingMessage     string
	ReadyMessage       string
}

// DefaultNewCredential creates a tenant-scoped AzureDeveloperCLICredential that allows
// cross-tenant token acquisition.
func DefaultNewCredential(tenantID string) (azcore.TokenCredential, error) {
	return azidentity.NewAzureDeveloperCLICredential(&azidentity.AzureDeveloperCLICredentialOptions{
		TenantID:                   tenantID,
		AdditionallyAllowedTenants: []string{"*"},
	})
}

// ParseProjectResourceID validates and returns the ARM ResourceID for an exact
// Microsoft.CognitiveServices/accounts/projects scope, or nil if invalid.
func ParseProjectResourceID(scope string) *arm.ResourceID {
	resource, err := arm.ParseResourceID(strings.TrimSpace(scope))
	if err != nil || len(strings.Split(resource.String(), "/")) != 11 ||
		!strings.EqualFold(resource.ResourceType.String(), "Microsoft.CognitiveServices/accounts/projects") ||
		resource.Parent == nil {
		return nil
	}
	for _, part := range []string{
		resource.SubscriptionID, resource.ResourceGroupName, resource.Parent.Name, resource.Name,
	} {
		if !safeScopeSegment.MatchString(part) {
			return nil
		}
	}
	return resource
}

// EndpointIdentity normalizes a standard public-cloud Foundry project endpoint URL
// (`https://<account>.services.ai.azure.com/api/projects/<project>`), or returns ""
// if the URL is invalid, non-HTTPS, contains credentials/query/fragment/port, or
// does not use `.services.ai.azure.com`.
func EndpointIdentity(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || !strings.EqualFold(u.Scheme, "https") || u.User != nil ||
		u.RawQuery != "" || u.Fragment != "" || u.Port() != "" ||
		!strings.HasSuffix(strings.ToLower(u.Hostname()), ".services.ai.azure.com") {
		return ""
	}
	return "https://" + strings.ToLower(u.Hostname()) + strings.TrimRight(u.EscapedPath(), "/")
}

// ScopeFromEnvironment verifies offline that endpoint matches the active environment's
// configured project endpoint (`FOUNDRY_PROJECT_ENDPOINT` or `AZURE_AI_PROJECT_ENDPOINT`),
// `AZURE_AI_PROJECT_ID`, and `AZURE_SUBSCRIPTION_ID`.
// Custom subdomains whose ARM account name differs from the hostname return nil so
// callers conservatively use existing access.
func ScopeFromEnvironment(endpoint string, values map[string]string) *arm.ResourceID {
	configured := strings.TrimSpace(values["FOUNDRY_PROJECT_ENDPOINT"])
	if configured == "" {
		configured = strings.TrimSpace(values["AZURE_AI_PROJECT_ENDPOINT"])
	}
	selected := EndpointIdentity(endpoint)
	if selected == "" || selected != EndpointIdentity(configured) {
		return nil
	}
	resource := ParseProjectResourceID(values["AZURE_AI_PROJECT_ID"])
	if resource == nil || !strings.EqualFold(strings.TrimSpace(values["AZURE_SUBSCRIPTION_ID"]), resource.SubscriptionID) {
		return nil
	}
	if !strings.EqualFold(selected, "https://"+resource.Parent.Name+".services.ai.azure.com/api/projects/"+resource.Name) {
		return nil
	}
	return resource
}

// Resolve inspects the active environment and account to build a verified project Scope.
// Non-cancellation environment or tenant lookup errors return (nil, nil) so actual
// Foundry data-plane access remains authoritative.
func (c *Client) Resolve(ctx context.Context, endpoint string) (*Scope, error) {
	if err := c.cancellation(ctx, nil); err != nil {
		return nil, err
	}
	if c == nil || c.Environment == nil {
		return nil, nil
	}
	values, err := c.Environment.Values(ctx)
	if cancelErr := c.cancellation(ctx, err); cancelErr != nil {
		return nil, cancelErr
	}
	if err != nil {
		return nil, nil
	}
	resource := ScopeFromEnvironment(endpoint, values)
	if resource == nil {
		return nil, nil
	}
	skipValue := values[SkipRoleAssignmentsEnvKey]
	if skipValue == "" {
		skipValue = os.Getenv(SkipRoleAssignmentsEnvKey)
	}
	skip, _ := strconv.ParseBool(skipValue)
	scope := &Scope{Resource: resource, Skip: skip}
	if c.Account != nil {
		tenantID, err := c.Account.LookupTenant(ctx, resource.SubscriptionID)
		if cancelErr := c.cancellation(ctx, err); cancelErr != nil {
			return nil, cancelErr
		}
		if err == nil {
			scope.TenantID = strings.TrimSpace(tenantID)
		}
	}
	return scope, nil
}

// PrepareRole checks whether the current principal already holds a sufficient data-plane
// role at or above scope, and if not, creates a deterministic Foundry User assignment at
// the project scope.
func (c *Client) PrepareRole(
	ctx context.Context, scope *Scope, report func(string),
) (confirmed, assigned bool, principalID string, principalType armauthorization.PrincipalType, err error) {
	if c == nil || scope == nil || scope.Skip || scope.TenantID == "" || c.Account == nil {
		return false, false, "", "", nil
	}
	if scope.Resource == nil || ParseProjectResourceID(scope.Resource.String()) == nil {
		return false, false, "", "", fmt.Errorf("a verified Foundry project scope is required")
	}
	principal, err := c.Account.CurrentPrincipal(ctx, scope.Resource.SubscriptionID)
	if cancelErr := c.cancellation(ctx, err); cancelErr != nil {
		return false, false, "", "", cancelErr
	}
	if err != nil {
		return false, false, "", "", err
	}
	objectID, parseErr := uuid.Parse(principal.ObjectID)
	if parseErr != nil {
		return false, false, "", "", fmt.Errorf("azd returned an invalid resource-tenant principal")
	}
	principalID = objectID.String()
	switch principal.Type {
	case armauthorization.PrincipalTypeUser, armauthorization.PrincipalTypeServicePrincipal:
		principalType = principal.Type
	default:
		return false, false, "", "", fmt.Errorf("azd returned an unsupported principal type")
	}
	newCred := c.NewCredential
	if newCred == nil {
		newCred = DefaultNewCredential
	}
	credential, credErr := newCred(scope.TenantID)
	if credErr != nil {
		return false, false, principalID, principalType, credErr
	}
	client := c.RoleClient
	if client == nil {
		client, err = armauthorization.NewRoleAssignmentsClient(scope.Resource.SubscriptionID, credential, nil)
		if err != nil {
			return false, false, principalID, principalType, err
		}
	}
	scopeID := scope.Resource.String()
	pager := client.NewListForScopePager(scopeID, &armauthorization.RoleAssignmentsClientListForScopeOptions{
		Filter: new(fmt.Sprintf("assignedTo('%s')", principalID)),
	})
	for pager.More() {
		page, pageErr := pager.NextPage(ctx)
		if pageErr != nil {
			return false, false, principalID, principalType, pageErr
		}
		for _, assignment := range page.Value {
			if IsRoleSufficient(assignment, scopeID) {
				return true, false, principalID, principalType, nil
			}
		}
	}
	if report != nil {
		report(c.assigningMessage())
	}
	name := RoleAssignmentName(scopeID, principalID, FoundryUserRoleID)
	_, err = client.Create(ctx, scopeID, name, armauthorization.RoleAssignmentCreateParameters{
		Properties: &armauthorization.RoleAssignmentProperties{
			RoleDefinitionID: new("/subscriptions/" + scope.Resource.SubscriptionID +
				"/providers/Microsoft.Authorization/roleDefinitions/" + FoundryUserRoleID),
			PrincipalID:   new(principalID),
			PrincipalType: new(principalType),
		},
	}, nil)
	if cancelErr := c.cancellation(ctx, err); cancelErr != nil {
		return false, false, principalID, principalType, cancelErr
	}
	if response, ok := errors.AsType[*azcore.ResponseError](err); ok &&
		response.StatusCode == http.StatusConflict && response.ErrorCode == "RoleAssignmentExists" {
		err = nil
	}
	return err == nil, err == nil, principalID, principalType, err
}

// ExtractSubscriptionID pulls the subscription ID from an ARM resource ID.
func ExtractSubscriptionID(resourceID string) string {
	parts := strings.Split(resourceID, "/")
	for i, part := range parts {
		if strings.EqualFold(part, "subscriptions") && i+1 < len(parts) {
			return parts[i+1]
		}
	}
	return ""
}

// HasAnyRoleAssignment checks whether principalID has any of the specified role
// definition GUIDs at scope (including inherited assignments from parent scopes).
func HasAnyRoleAssignment(
	ctx context.Context,
	cred azcore.TokenCredential,
	principalID string,
	roleIDs []string,
	scope string,
	options *arm.ClientOptions,
) (bool, error) {
	subscriptionID := ExtractSubscriptionID(scope)
	if subscriptionID == "" {
		return false, fmt.Errorf("could not extract subscription ID from scope: %s", scope)
	}
	client, err := armauthorization.NewRoleAssignmentsClient(subscriptionID, cred, options)
	if err != nil {
		return false, fmt.Errorf("failed to create role assignments client: %w", err)
	}
	return HasAnyRoleAssignmentWithClient(ctx, client, principalID, roleIDs, scope)
}

// HasAnyRoleAssignmentWithClient checks whether principalID has any of the specified
// role definition GUIDs at scope using an existing RoleAssignmentsClient.
func HasAnyRoleAssignmentWithClient(
	ctx context.Context,
	client *armauthorization.RoleAssignmentsClient,
	principalID string,
	roleIDs []string,
	scope string,
) (bool, error) {
	suffixes := make(map[string]bool, len(roleIDs))
	for _, id := range roleIDs {
		suffixes[strings.ToLower("/roledefinitions/"+id)] = true
	}
	filter := fmt.Sprintf("assignedTo('%s')", principalID)
	pager := client.NewListForScopePager(scope, &armauthorization.RoleAssignmentsClientListForScopeOptions{
		Filter: &filter,
	})
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return false, fmt.Errorf("failed to list role assignments: %w", err)
		}
		for _, assignment := range page.Value {
			if assignment == nil || assignment.Properties == nil || assignment.Properties.RoleDefinitionID == nil {
				continue
			}
			roleDefID := strings.ToLower(*assignment.Properties.RoleDefinitionID)
			for suffix := range suffixes {
				if strings.HasSuffix(roleDefID, suffix) {
					return true, nil
				}
			}
		}
	}
	return false, nil
}

// AssignRole idempotently assigns a single RBAC role to principalID at scope.
// It checks `atScope()` first and returns (false, nil) if the assignment already exists
// or if Create returns 409 RoleAssignmentExists. Returns (true, nil) when a new
// assignment is created.
func AssignRole(
	ctx context.Context,
	cred azcore.TokenCredential,
	principalID string,
	roleID string,
	roleName string,
	scope string,
	principalType armauthorization.PrincipalType,
	options *arm.ClientOptions,
) (bool, error) {
	subscriptionID := ExtractSubscriptionID(scope)
	if subscriptionID == "" {
		return false, fmt.Errorf("could not extract subscription ID from scope %s for role %s", scope, roleName)
	}
	client, err := armauthorization.NewRoleAssignmentsClient(subscriptionID, cred, options)
	if err != nil {
		return false, fmt.Errorf("failed to create role assignments client for role %s: %w", roleName, err)
	}
	return AssignRoleWithClient(ctx, client, principalID, roleID, roleName, scope, principalType)
}

// AssignRoleWithClient idempotently assigns a single RBAC role using an existing RoleAssignmentsClient.
func AssignRoleWithClient(
	ctx context.Context,
	client *armauthorization.RoleAssignmentsClient,
	principalID, roleID, roleName, scope string,
	principalType armauthorization.PrincipalType,
) (bool, error) {
	roleDefinitionSuffix := strings.ToLower("/roledefinitions/" + roleID)
	pager := client.NewListForScopePager(scope, &armauthorization.RoleAssignmentsClientListForScopeOptions{
		Filter: new("atScope()"),
	})
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return false, fmt.Errorf("failed to list role assignments for role %s: %w", roleName, err)
		}
		for _, assignment := range page.Value {
			if assignment != nil &&
				assignment.Properties != nil &&
				assignment.Properties.PrincipalID != nil &&
				assignment.Properties.RoleDefinitionID != nil &&
				strings.EqualFold(*assignment.Properties.PrincipalID, principalID) &&
				strings.HasSuffix(strings.ToLower(*assignment.Properties.RoleDefinitionID), roleDefinitionSuffix) {
				return false, nil
			}
		}
	}

	fullRoleDefinitionID := fmt.Sprintf(
		"/subscriptions/%s/providers/Microsoft.Authorization/roleDefinitions/%s",
		ExtractSubscriptionID(scope),
		roleID,
	)
	roleAssignmentName := RoleAssignmentName(scope, principalID, roleID)
	parameters := armauthorization.RoleAssignmentCreateParameters{
		Properties: &armauthorization.RoleAssignmentProperties{
			RoleDefinitionID: new(fullRoleDefinitionID),
			PrincipalID:      new(principalID),
			PrincipalType:    new(principalType),
		},
	}

	_, err := client.Create(ctx, scope, roleAssignmentName, parameters, nil)
	if err != nil {
		if respErr, ok := errors.AsType[*azcore.ResponseError](err); ok &&
			respErr.StatusCode == http.StatusConflict && respErr.ErrorCode == "RoleAssignmentExists" {
			return false, nil
		}
		if strings.Contains(err.Error(), "RoleAssignmentExists") || strings.Contains(err.Error(), "409") {
			return false, nil
		}
		return false, fmt.Errorf("failed to create role assignment for role %s: %w", roleName, err)
	}
	return true, nil
}

// RoleAssignmentName returns the deterministic UUIDv5 role assignment name for a
// scope, principal ID, and role definition GUID.
func RoleAssignmentName(scopeID, principalID, roleDefinitionGUID string) string {
	return uuid.NewSHA1(
		uuid.NameSpaceURL,
		[]byte(strings.ToLower(scopeID+"/"+principalID+"/"+roleDefinitionGUID)),
	).String()
}

// IsRoleSufficient reports whether assignment is an unconditional assignment of one
// of SufficientDataPlaneRoles at targetScope or an ancestor scope (including management groups).
func IsRoleSufficient(assignment *armauthorization.RoleAssignment, targetScope string) bool {
	if assignment == nil || assignment.Properties == nil {
		return false
	}
	props := assignment.Properties
	if props.Scope == nil || props.RoleDefinitionID == nil || (props.Condition != nil && *props.Condition != "") {
		return false
	}
	inherited := strings.ToLower(strings.TrimRight(*props.Scope, "/"))
	target := strings.ToLower(targetScope)
	const groupPrefix = "/providers/microsoft.management/managementgroups/"
	managementGroup := strings.HasPrefix(inherited, groupPrefix) &&
		safeScopeSegment.MatchString(strings.TrimPrefix(inherited, groupPrefix))
	if inherited == "" || (target != inherited && !strings.HasPrefix(target, inherited+"/") && !managementGroup) {
		return false
	}
	role := (*props.RoleDefinitionID)[strings.LastIndex(*props.RoleDefinitionID, "/")+1:]
	return slices.ContainsFunc(SufficientDataPlaneRoles, func(sufficient string) bool {
		return strings.EqualFold(role, sufficient)
	})
}

// IsAccessDenied reports whether err is an HTTP 403 Foundry data-plane authorization denial.
func IsAccessDenied(err error) bool {
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

// IsNotFound reports whether err is an HTTP 404 response error.
func IsNotFound(err error) bool {
	response, ok := errors.AsType[*azcore.ResponseError](err)
	return ok && response.StatusCode == http.StatusNotFound
}

// IsProjectNotFound reports whether err is an HTTP 404 indicating the Foundry project
// itself has not yet synchronized to the regional data plane.
func IsProjectNotFound(err error) bool {
	return IsNotFound(err) && strings.Contains(strings.ToLower(err.Error()), "project not found")
}

// Wait pauses for duration or until ctx is canceled.
func Wait(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// PollOptions configures a data-plane readiness polling loop after an ARM role check or grant.
type PollOptions struct {
	Confirmed                bool
	Assigned                 bool
	Wait                     func(context.Context, time.Duration) error
	Attempts                 int
	Timeout                  time.Duration
	IsCanceled               func(error) bool
	TreatAll404AsProjectSync bool
	Report                   func(string)
	ReadyMessage             string
}

// PollReadiness waits 15 seconds when Assigned is true, then polls probe every 5 seconds
// (up to Attempts / Timeout) while project-sync 404 or confirmed 403 responses persist.
// Unconfirmed 403 responses return nil so custom write-only roles can proceed to deployment writes.
func PollReadiness(
	ctx context.Context,
	opts PollOptions,
	probe func(context.Context) error,
) (resultErr error) {
	if probe == nil {
		return nil
	}
	parent := ctx
	timeout := 5 * time.Minute
	if opts.Timeout > 0 {
		timeout = opts.Timeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	var lastProbeError error
	defer func() {
		if parent.Err() != nil {
			resultErr = parent.Err()
		} else if errors.Is(resultErr, context.DeadlineExceeded) && ctx.Err() != nil && lastProbeError != nil {
			resultErr = lastProbeError
		}
		cancel()
	}()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	wait := Wait
	if opts.Wait != nil {
		wait = opts.Wait
	}
	attempts := 60
	if opts.Attempts > 0 {
		attempts = opts.Attempts
	}
	readyMsg := opts.ReadyMessage
	if readyMsg == "" {
		readyMsg = "Foundry access is ready."
	}
	waitMessage := ""
	if opts.Assigned {
		waitMessage = "Waiting for Foundry access to propagate."
		if opts.Report != nil {
			opts.Report(waitMessage)
		}
		if err := wait(ctx, 15*time.Second); err != nil {
			return err
		}
	}
	for attempt := range attempts {
		err := probe(ctx)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if opts.IsCanceled != nil && opts.IsCanceled(err) {
			return err
		}
		projectMissing := IsProjectNotFound(err)
		if opts.TreatAll404AsProjectSync && IsNotFound(err) {
			projectMissing = true
		}
		if err == nil {
			if opts.Report != nil && (opts.Assigned || waitMessage != "") {
				opts.Report(readyMsg)
			}
			return nil
		}
		if !opts.Confirmed && IsAccessDenied(err) {
			return nil
		}
		lastProbeError = err
		retry := projectMissing || (opts.Confirmed && IsAccessDenied(err))
		if !retry || attempt == attempts-1 {
			return err
		}
		message := "Waiting for Foundry access to propagate."
		if projectMissing {
			message = "Waiting for the Foundry project to synchronize."
		}
		if opts.Report != nil && message != waitMessage {
			opts.Report(message)
		}
		waitMessage = message
		if err := wait(ctx, 5*time.Second); err != nil {
			return err
		}
	}
	return lastProbeError
}

// Ensure prepares ARM role access and polls probe until data-plane access is ready.
// An item-level 404 (IsNotFound(err) && !IsProjectNotFound(err)) is treated as
// authorized readiness for a missing resource and returns (zero, nil).
func Ensure[T any](
	ctx context.Context,
	client *Client,
	scope *Scope,
	readPolicy ReadPolicy,
	report func(string),
	probe func(context.Context) (T, error),
) (item T, resultErr error) {
	var zero T
	parent := ctx
	timeout := 5 * time.Minute
	if client != nil && client.Timeout > 0 {
		timeout = client.Timeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	var lastProbeError error
	var principalID string
	var principalType armauthorization.PrincipalType
	op := client.defaultOperation()
	defer func() {
		if parent.Err() != nil {
			item, resultErr = zero, parent.Err()
		} else if errors.Is(resultErr, context.DeadlineExceeded) && ctx.Err() != nil && lastProbeError != nil {
			item, resultErr = zero, client.WriteError(parent, lastProbeError, op, scope, principalID, principalType)
		}
		cancel()
	}()
	if err := client.cancellation(ctx, nil); err != nil {
		return zero, err
	}
	confirmed, assigned, principalID, principalType, err := client.PrepareRole(ctx, scope, report)
	if cancelErr := client.cancellation(ctx, err); cancelErr != nil {
		return zero, cancelErr
	}
	wait := Wait
	attempts := 60
	if client != nil {
		if client.Wait != nil {
			wait = client.Wait
		}
		if client.Attempts > 0 {
			attempts = client.Attempts
		}
	}
	waitMessage := ""
	if assigned {
		waitMessage = client.propagatingMessage()
		if report != nil {
			report(waitMessage)
		}
		if err := wait(ctx, 15*time.Second); err != nil {
			return zero, err
		}
	}
	for attempt := range attempts {
		val, err := probe(ctx)
		if cancelErr := client.cancellation(ctx, err); cancelErr != nil {
			return zero, cancelErr
		}
		projectMissing := IsProjectNotFound(err)
		if err == nil || (IsNotFound(err) && !projectMissing) {
			if report != nil && (assigned || waitMessage != "") {
				report(client.readyMessage())
			}
			return val, nil
		}
		if readPolicy == ReadOptionalForDeploy && !confirmed && IsAccessDenied(err) {
			return zero, nil
		}
		lastProbeError = err
		retry := projectMissing || (confirmed && IsAccessDenied(err))
		if !retry || attempt == attempts-1 {
			return zero, client.WriteError(ctx, err, op, scope, principalID, principalType)
		}
		message := client.propagatingMessage()
		if projectMissing {
			message = client.syncingMessage()
		}
		if report != nil && message != waitMessage {
			report(message)
		}
		waitMessage = message
		if err := wait(ctx, 5*time.Second); err != nil {
			return zero, err
		}
	}
	return zero, ferrors.Internal(op, "Foundry access readiness could not be established")
}

// WriteError converts a Foundry data-plane or authentication error into a sanitized
// *ferrors.Error (or returns cancellation errors directly). Raw SDK error payloads
// and request URLs are never serialized into the returned error message.
func (c *Client) WriteError(
	ctx context.Context,
	err error,
	operation string,
	scope *Scope,
	principalID string,
	principalType armauthorization.PrincipalType,
) error {
	if cancelErr := c.cancellation(ctx, err); cancelErr != nil {
		return cancelErr
	}
	_, authenticationFailed := errors.AsType[*azidentity.AuthenticationFailedError](err)
	_, authenticationRequired := errors.AsType[*azidentity.AuthenticationRequiredError](err)
	if authenticationFailed || authenticationRequired {
		return ferrors.Auth(
			ferrors.CodeAuthFailed,
			"could not authenticate to Foundry",
			"run 'azd auth login' to authenticate, then retry",
		)
	}
	if response, ok := errors.AsType[*azcore.ResponseError](err); ok {
		code := response.ErrorCode
		if !safeScopeSegment.MatchString(code) {
			code = strconv.Itoa(response.StatusCode)
		}
		message := fmt.Sprintf("%s: Foundry request failed (HTTP %d).", operation, response.StatusCode)
		suggestion := ""
		if IsProjectNotFound(err) {
			message = operation + ": the Foundry project has not synchronized."
			suggestion = "Wait for the Foundry project to synchronize, then retry."
		}
		if response.StatusCode == http.StatusUnauthorized {
			suggestion = "Run 'azd auth login' to authenticate, then retry."
		}
		if IsAccessDenied(err) {
			suggestion = "Verify Foundry project data-plane access, then wait for role propagation and retry. " +
				"Azure Owner and Contributor do not grant this access."
			if scope != nil && scope.Resource != nil && ParseProjectResourceID(scope.Resource.String()) != nil {
				if principalID == "" {
					principalID = "<resource-tenant-object-id>"
					principalType = armauthorization.PrincipalTypeUser
				}
				suggestion += fmt.Sprintf(
					" Ask an administrator to run: az role assignment create --assignee-object-id %q "+
						"--assignee-principal-type %s --role %s --scope %q. "+
						"Use the resource-tenant object ID and User or ServicePrincipal type for your login.",
					principalID, principalType, FoundryUserRoleID, scope.Resource.String())
			} else {
				suggestion += " Ask an administrator to assign Foundry User on the intended Foundry project."
			}
		}
		return ferrors.Service(operation+"."+code, response.StatusCode, "foundry", message, suggestion, err)
	}
	if _, ok := errors.AsType[*ferrors.Error](err); ok {
		return err
	}
	return ferrors.Internal(operation, operation+": could not complete the Foundry request")
}

func (c *Client) cancellation(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	if c != nil && c.IsCanceled != nil && c.IsCanceled(err) {
		return err
	}
	return nil
}

func (c *Client) noun() string {
	if c != nil && strings.TrimSpace(c.ResourceNoun) != "" {
		return strings.TrimSpace(c.ResourceNoun)
	}
	return "resource"
}

func (c *Client) defaultOperation() string {
	if c != nil && strings.TrimSpace(c.DefaultOperation) != "" {
		return strings.TrimSpace(c.DefaultOperation)
	}
	return "get_" + c.noun()
}

func (c *Client) assigningMessage() string {
	if c != nil && c.AssigningMessage != "" {
		return c.AssigningMessage
	}
	return fmt.Sprintf("Assigning Foundry User on the %s's Foundry project.", c.noun())
}

func (c *Client) propagatingMessage() string {
	if c != nil && c.PropagatingMessage != "" {
		return c.PropagatingMessage
	}
	return "Waiting for Foundry access to propagate."
}

func (c *Client) syncingMessage() string {
	if c != nil && c.SyncingMessage != "" {
		return c.SyncingMessage
	}
	return "Waiting for the Foundry project to synchronize."
}

func (c *Client) readyMessage() string {
	if c != nil && c.ReadyMessage != "" {
		return c.ReadyMessage
	}
	if c != nil && strings.TrimSpace(c.ResourceNoun) != "" {
		return fmt.Sprintf("Foundry %s access is ready.", strings.TrimSpace(c.ResourceNoun))
	}
	return "Foundry access is ready."
}
