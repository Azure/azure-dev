// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

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

	"azureaiskills/internal/exterrors"
	"azureaiskills/internal/pkg/skill_api"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/authorization/armauthorization/v2"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/google/uuid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const foundryUserRole = "53ca6127-db72-4b80-b1b0-d745d6d5456d"

var sufficientSkillRoles = []string{
	foundryUserRole,
	"a97b65f3-24c7-4388-baec-2e87135dc908", // Cognitive Services User
	"eadc314b-1a2d-4efa-be10-5d325db5065e", // Foundry Project Manager
	"c883944f-8b7b-4483-af10-35834be79c4a", // Foundry Owner
}

type skillAccessEnvironment interface {
	GetValues(context.Context, *azdext.GetEnvironmentRequest, ...grpc.CallOption) (*azdext.KeyValueListResponse, error)
}

type accountTenantLookup interface {
	LookupTenant(
		ctx context.Context,
		in *azdext.LookupTenantRequest,
		opts ...grpc.CallOption,
	) (*azdext.LookupTenantResponse, error)
}

type skillAccessAccount interface {
	accountTenantLookup
	GetCurrentPrincipal(context.Context, *azdext.GetCurrentPrincipalRequest, ...grpc.CallOption) (
		*azdext.GetCurrentPrincipalResponse, error)
}

type skillProbeReader interface {
	GetSkill(ctx context.Context, name string) (*skill_api.Skill, error)
}

// skillAccess is used only by managed Deploy and standalone create. Its seams
// replace the credential, ARM transport and wait, not the access decision itself.
type skillAccess struct {
	environment   skillAccessEnvironment
	account       skillAccessAccount
	newCredential func(string) (azcore.TokenCredential, error)
	roleClient    *armauthorization.RoleAssignmentsClient
	wait          func(context.Context, time.Duration) error
	attempts      int
	timeout       time.Duration
}

type skillAccessScope struct {
	resource *arm.ResourceID
	tenantID string
	skip     bool
}

type skillReadPolicy int

const (
	skillReadRequired skillReadPolicy = iota
	skillReadOptionalForDeploy
)

func newSkillAccess(client *azdext.AzdClient) *skillAccess {
	return &skillAccess{
		environment:   client.Environment(),
		account:       client.Account(),
		newCredential: newCredentialForTenant,
	}
}

var safeScopeSegment = regexp.MustCompile(`^[A-Za-z0-9_.()-]+$`)

func resolveSkillWriteEndpoint(ctx context.Context, flag string) (string, endpointSource, error) {
	endpoint, src, err := resolveProjectEndpoint(ctx, flag)
	if cancelErr := skillAccessCancellation(ctx, err); cancelErr != nil {
		return "", "", cancelErr
	}
	if err != nil {
		var local *azdext.LocalError
		if errors.As(err, &local) {
			return "", "", err
		}
		return "", "", exterrors.Validation(
			exterrors.CodeInvalidParameter,
			"invalid Foundry project endpoint URL",
			"provide an https:// Foundry project endpoint URL",
		)
	}
	return endpoint, src, nil
}

func skillProjectResource(scope string) *arm.ResourceID {
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

func skillEndpointIdentity(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || !strings.EqualFold(u.Scheme, "https") || u.User != nil ||
		u.RawQuery != "" || u.Fragment != "" || u.Port() != "" ||
		!strings.HasSuffix(strings.ToLower(u.Hostname()), ".services.ai.azure.com") {
		return ""
	}
	return "https://" + strings.ToLower(u.Hostname()) + strings.TrimRight(u.EscapedPath(), "/")
}

func skillScopeFromEnvironment(endpoint string, values map[string]string) *arm.ResourceID {
	configured := strings.TrimSpace(values["FOUNDRY_PROJECT_ENDPOINT"])
	if configured == "" {
		configured = strings.TrimSpace(values["AZURE_AI_PROJECT_ENDPOINT"])
	}
	selected := skillEndpointIdentity(endpoint)
	if selected == "" || selected != skillEndpointIdentity(configured) {
		return nil
	}
	resource := skillProjectResource(values["AZURE_AI_PROJECT_ID"])
	if resource == nil || !strings.EqualFold(strings.TrimSpace(values["AZURE_SUBSCRIPTION_ID"]), resource.SubscriptionID) {
		return nil
	}
	// This is an offline binding, not an assertion that all accounts use their ARM
	// name as their hostname. Custom subdomains conservatively use existing access.
	if !strings.EqualFold(selected, "https://"+resource.Parent.Name+".services.ai.azure.com/api/projects/"+resource.Name) {
		return nil
	}
	return resource
}

func (a *skillAccess) resolve(ctx context.Context, endpoint string) (*skillAccessScope, error) {
	if err := skillAccessCancellation(ctx, nil); err != nil {
		return nil, err
	}
	if a == nil || a.environment == nil {
		return nil, nil
	}
	response, err := a.environment.GetValues(ctx, &azdext.GetEnvironmentRequest{})
	if cancelErr := skillAccessCancellation(ctx, err); cancelErr != nil {
		return nil, cancelErr
	}
	if err != nil {
		return nil, nil // Actual Foundry access, not optional ARM access, is authoritative.
	}
	values := make(map[string]string, len(response.GetKeyValues()))
	for _, kv := range response.GetKeyValues() {
		values[kv.GetKey()] = kv.GetValue()
	}
	resource := skillScopeFromEnvironment(endpoint, values)
	if resource == nil {
		return nil, nil
	}
	skipValue := values["AZD_AGENT_SKIP_ROLE_ASSIGNMENTS"]
	if skipValue == "" {
		skipValue = os.Getenv("AZD_AGENT_SKIP_ROLE_ASSIGNMENTS")
	}
	skip, _ := strconv.ParseBool(skipValue)
	scope := &skillAccessScope{resource: resource, skip: skip}
	if a.account != nil {
		tenant, err := a.account.LookupTenant(ctx, &azdext.LookupTenantRequest{SubscriptionId: resource.SubscriptionID})
		if cancelErr := skillAccessCancellation(ctx, err); cancelErr != nil {
			return nil, cancelErr
		}
		if err == nil {
			scope.tenantID = strings.TrimSpace(tenant.GetTenantId())
		}
	}
	return scope, nil
}

func (a *skillAccess) prepareRole(
	ctx context.Context, scope *skillAccessScope, report func(string),
) (confirmed, assigned bool, principalID string, principalType armauthorization.PrincipalType, err error) {
	if a == nil || scope == nil || scope.skip || scope.tenantID == "" || a.account == nil {
		return
	}
	if scope.resource == nil || skillProjectResource(scope.resource.String()) == nil {
		return false, false, "", "", fmt.Errorf("a verified Foundry project scope is required")
	}
	principal, err := a.account.GetCurrentPrincipal(ctx, &azdext.GetCurrentPrincipalRequest{
		SubscriptionId: scope.resource.SubscriptionID,
	})
	if cancelErr := skillAccessCancellation(ctx, err); cancelErr != nil {
		return false, false, "", "", cancelErr
	}
	if err != nil {
		return false, false, "", "", err
	}
	objectID, parseErr := uuid.Parse(principal.GetObjectId())
	if parseErr != nil {
		return false, false, "", "", fmt.Errorf("azd returned an invalid resource-tenant principal")
	}
	principalID = objectID.String()
	switch principal.GetPrincipalType() {
	case azdext.PrincipalType_PRINCIPAL_TYPE_USER:
		principalType = armauthorization.PrincipalTypeUser
	case azdext.PrincipalType_PRINCIPAL_TYPE_SERVICE_PRINCIPAL:
		principalType = armauthorization.PrincipalTypeServicePrincipal
	default:
		return false, false, "", "", fmt.Errorf("azd returned an unsupported principal type")
	}
	credential, credErr := a.newCredential(scope.tenantID)
	if credErr != nil {
		return false, false, principalID, principalType, credErr
	}
	client := a.roleClient
	if client == nil {
		client, err = armauthorization.NewRoleAssignmentsClient(scope.resource.SubscriptionID, credential, nil)
		if err != nil {
			return false, false, principalID, principalType, err
		}
	}
	scopeID := scope.resource.String()
	pager := client.NewListForScopePager(scopeID, &armauthorization.RoleAssignmentsClientListForScopeOptions{
		Filter: new(fmt.Sprintf("assignedTo('%s')", principalID)),
	})
	for pager.More() {
		page, pageErr := pager.NextPage(ctx)
		if pageErr != nil {
			return false, false, principalID, principalType, pageErr
		}
		for _, assignment := range page.Value {
			if skillRoleSufficient(assignment, scopeID) {
				return true, false, principalID, principalType, nil
			}
		}
	}
	if report != nil {
		report("Assigning Foundry User on the skill's Foundry project.")
	}
	name := uuid.NewSHA1(uuid.NameSpaceURL, []byte(strings.ToLower(scopeID+"/"+principalID+"/"+foundryUserRole))).String()
	_, err = client.Create(ctx, scopeID, name, armauthorization.RoleAssignmentCreateParameters{
		Properties: &armauthorization.RoleAssignmentProperties{
			RoleDefinitionID: new("/subscriptions/" + scope.resource.SubscriptionID +
				"/providers/Microsoft.Authorization/roleDefinitions/" + foundryUserRole),
			PrincipalID: new(principalID), PrincipalType: new(principalType),
		},
	}, nil)
	if cancelErr := skillAccessCancellation(ctx, err); cancelErr != nil {
		return false, false, principalID, principalType, cancelErr
	}
	if response, ok := errors.AsType[*azcore.ResponseError](err); ok &&
		response.StatusCode == http.StatusConflict && response.ErrorCode == "RoleAssignmentExists" {
		err = nil
	}
	return err == nil, err == nil, principalID, principalType, err
}

func skillRoleSufficient(assignment *armauthorization.RoleAssignment, scope string) bool {
	if assignment == nil || assignment.Properties == nil {
		return false
	}
	props := assignment.Properties
	if props.Scope == nil || props.RoleDefinitionID == nil || (props.Condition != nil && *props.Condition != "") {
		return false
	}
	inherited := strings.ToLower(strings.TrimRight(*props.Scope, "/"))
	target := strings.ToLower(scope)
	const groupPrefix = "/providers/microsoft.management/managementgroups/" // cspell:ignore managementgroups
	managementGroup := strings.HasPrefix(inherited, groupPrefix) &&
		safeScopeSegment.MatchString(strings.TrimPrefix(inherited, groupPrefix))
	if inherited == "" || (target != inherited && !strings.HasPrefix(target, inherited+"/") && !managementGroup) {
		return false
	}
	role := (*props.RoleDefinitionID)[strings.LastIndex(*props.RoleDefinitionID, "/")+1:]
	return slices.ContainsFunc(sufficientSkillRoles, func(sufficient string) bool {
		return strings.EqualFold(role, sufficient)
	})
}

func skillAccessCancellation(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	if code := status.Code(err); code == codes.Canceled || code == codes.DeadlineExceeded {
		return err
	}
	return nil
}

func skillAccessDenied(err error) bool {
	response, ok := errors.AsType[*azcore.ResponseError](err)
	if !ok || response.StatusCode != http.StatusForbidden {
		return false
	}
	// cspell:ignore permissiondenied authorizationfailed authorizationpermissionmismatch aiservices
	switch strings.ToLower(response.ErrorCode) {
	case "permissiondenied", "authorizationfailed", "authorizationpermissionmismatch":
		return true
	default:
		return strings.Contains(strings.ToLower(response.Error()), "microsoft.cognitiveservices/accounts/aiservices/agents/")
	}
}

func skillProjectNotFound(err error) bool {
	return isNotFound(err) && strings.Contains(strings.ToLower(err.Error()), "project not found")
}

func waitSkillAccess(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// ensure returns the already-read skill (nil for an authorized missing skill).
// ARM failures are deferred, never warned on successful custom-role access.
// skillReadOptionalForDeploy also permits an unconfirmed authorization denial:
// the caller must attempt the deployment write to establish actual access.
func (a *skillAccess) ensure(
	ctx context.Context, scope *skillAccessScope, client skillProbeReader, name string,
	readPolicy skillReadPolicy, report func(string),
) (skill *skill_api.Skill, resultErr error) {
	parent := ctx
	timeout := 5 * time.Minute
	if a != nil && a.timeout > 0 {
		timeout = a.timeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	var lastProbeError error
	var principalID string
	var principalType armauthorization.PrincipalType
	defer func() {
		// The internal readiness budget is not a caller deadline. Retain the last
		// service failure and its remediation when that budget is exhausted.
		if parent.Err() != nil {
			skill, resultErr = nil, parent.Err()
		} else if errors.Is(resultErr, context.DeadlineExceeded) && ctx.Err() != nil && lastProbeError != nil {
			skill, resultErr = nil, skillWriteError(parent, lastProbeError, exterrors.OpGetSkill, scope, principalID, principalType)
		}
		cancel()
	}()
	if err := skillAccessCancellation(ctx, nil); err != nil {
		return nil, err
	}
	confirmed, assigned, principalID, principalType, err := a.prepareRole(ctx, scope, report)
	if cancelErr := skillAccessCancellation(ctx, err); cancelErr != nil {
		return nil, cancelErr
	}
	wait := waitSkillAccess
	attempts := 60
	if a != nil {
		if a.wait != nil {
			wait = a.wait
		}
		if a.attempts > 0 {
			attempts = a.attempts
		}
	}
	waitMessage := ""
	if assigned {
		waitMessage = "Waiting for Foundry access to propagate."
		if report != nil {
			report(waitMessage)
		}
		if err := wait(ctx, 15*time.Second); err != nil {
			return nil, err
		}
	}
	for attempt := range attempts {
		skill, err := client.GetSkill(ctx, name)
		if cancelErr := skillAccessCancellation(ctx, err); cancelErr != nil {
			return nil, cancelErr
		}
		projectMissing := skillProjectNotFound(err)
		if err == nil || (isNotFound(err) && !projectMissing) {
			if report != nil && (assigned || waitMessage != "") {
				report("Foundry skill access is ready.")
			}
			return skill, nil
		}
		// A custom role may allow the deployment write without granting agents/read.
		if readPolicy == skillReadOptionalForDeploy && !confirmed && skillAccessDenied(err) {
			return nil, nil
		}
		lastProbeError = err
		retry := projectMissing || (confirmed && skillAccessDenied(err))
		if !retry || attempt == attempts-1 {
			return nil, skillWriteError(ctx, err, exterrors.OpGetSkill, scope, principalID, principalType)
		}
		message := "Waiting for Foundry access to propagate."
		if projectMissing {
			message = "Waiting for the Foundry project to synchronize."
		}
		if report != nil && message != waitMessage {
			report(message)
		}
		waitMessage = message
		if err := wait(ctx, 5*time.Second); err != nil {
			return nil, err
		}
	}
	return nil, exterrors.Internal(exterrors.OpGetSkill, "Foundry access readiness could not be established")
}

func skillWriteError(
	ctx context.Context, err error, operation string, scope *skillAccessScope,
	principalID string, principalType armauthorization.PrincipalType,
) error {
	if cancelErr := skillAccessCancellation(ctx, err); cancelErr != nil {
		return cancelErr
	}
	_, authenticationFailed := errors.AsType[*azidentity.AuthenticationFailedError](err)
	_, authenticationRequired := errors.AsType[*azidentity.AuthenticationRequiredError](err)
	if authenticationFailed || authenticationRequired {
		return exterrors.Auth(exterrors.CodeAuthFailed,
			"could not authenticate to Foundry", "run 'azd auth login' to authenticate, then retry")
	}
	// Never serialize the SDK error text: request URLs and response bodies may
	// contain credentials. Preserve the service status and code, not its payload.
	if response, ok := errors.AsType[*azcore.ResponseError](err); ok {
		code := response.ErrorCode
		if !safeScopeSegment.MatchString(code) {
			code = strconv.Itoa(response.StatusCode)
		}
		converted := &azdext.ServiceError{
			Message:   fmt.Sprintf("%s: Foundry request failed (HTTP %d).", operation, response.StatusCode),
			ErrorCode: operation + "." + code, StatusCode: response.StatusCode, ServiceName: "foundry",
		}
		if skillProjectNotFound(err) {
			converted.Message = operation + ": the Foundry project has not synchronized."
			converted.Suggestion = "Wait for the Foundry project to synchronize, then retry."
		}
		if response.StatusCode == http.StatusUnauthorized {
			converted.Suggestion = "Run 'azd auth login' to authenticate, then retry."
		}
		if skillAccessDenied(err) {
			converted.Suggestion = "Verify Foundry project data-plane access, then wait for role propagation and retry. " +
				"Azure Owner and Contributor do not grant this access."
			if scope != nil && scope.resource != nil && skillProjectResource(scope.resource.String()) != nil {
				if principalID == "" {
					principalID = "<resource-tenant-object-id>"
					principalType = armauthorization.PrincipalTypeUser
				}
				converted.Suggestion += fmt.Sprintf(
					" Ask an administrator to run: az role assignment create --assignee-object-id %q "+
						"--assignee-principal-type %s --role %s --scope %q. "+
						"Use the resource-tenant object ID and User or ServicePrincipal type for your login.",
					principalID, principalType, foundryUserRole, scope.resource.String())
			} else {
				converted.Suggestion += " Ask an administrator to assign Foundry User on the intended Foundry project."
			}
		}
		return converted
	}
	if _, ok := errors.AsType[*azdext.LocalError](err); ok {
		return err
	}
	return exterrors.Internal(operation, operation+": could not complete the Foundry request")
}
