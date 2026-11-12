// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"errors"
	"fmt"
	"time"

	"azureaiskills/internal/exterrors"
	"azureaiskills/internal/pkg/skill_api"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/authorization/armauthorization/v2"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/shared/foundry/access"
	"github.com/azure/azure-dev/cli/shared/foundry/ferrors"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const foundryUserRole = access.FoundryUserRoleID

var sufficientSkillRoles = access.SufficientDataPlaneRoles

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

type skillReadPolicy = access.ReadPolicy

const (
	skillReadRequired          = access.ReadRequired
	skillReadOptionalForDeploy = access.ReadOptionalForDeploy
)

func newSkillAccess(client *azdext.AzdClient) *skillAccess {
	return &skillAccess{
		environment:   client.Environment(),
		account:       client.Account(),
		newCredential: newCredentialForTenant,
	}
}

func resolveSkillWriteEndpoint(ctx context.Context, flag string) (string, endpointSource, error) {
	endpoint, src, err := resolveProjectEndpoint(ctx, flag)
	if cancelErr := skillAccessCancellation(ctx, err); cancelErr != nil {
		return "", "", cancelErr
	}
	if err != nil {
		if _, ok := errors.AsType[*azdext.LocalError](err); ok {
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
	return access.ParseProjectResourceID(scope)
}

func skillScopeFromEnvironment(endpoint string, values map[string]string) *arm.ResourceID {
	return access.ScopeFromEnvironment(endpoint, values)
}

func (a *skillAccess) sharedClient() *access.Client {
	client := &access.Client{
		IsCanceled:       isGrpcCanceled,
		ResourceNoun:     "skill",
		DefaultOperation: exterrors.OpGetSkill,
	}
	if a == nil {
		return client
	}
	if a.environment != nil {
		client.Environment = skillEnvAdapter{a.environment}
	}
	if a.account != nil {
		client.Account = skillAccountAdapter{a.account}
	}
	client.NewCredential = a.newCredential
	client.RoleClient = a.roleClient
	client.Wait = a.wait
	client.Attempts = a.attempts
	client.Timeout = a.timeout
	return client
}

func toSharedScope(scope *skillAccessScope) *access.Scope {
	if scope == nil {
		return nil
	}
	return &access.Scope{
		Resource: scope.resource,
		TenantID: scope.tenantID,
		Skip:     scope.skip,
	}
}

func (a *skillAccess) resolve(ctx context.Context, endpoint string) (*skillAccessScope, error) {
	shared, err := a.sharedClient().Resolve(ctx, endpoint)
	if err != nil || shared == nil {
		return nil, err
	}
	return &skillAccessScope{
		resource: shared.Resource,
		tenantID: shared.TenantID,
		skip:     shared.Skip,
	}, nil
}

func (a *skillAccess) prepareRole(
	ctx context.Context, scope *skillAccessScope, report func(string),
) (confirmed, assigned bool, principalID string, principalType armauthorization.PrincipalType, err error) {
	return a.sharedClient().PrepareRole(ctx, toSharedScope(scope), report)
}

func skillRoleSufficient(assignment *armauthorization.RoleAssignment, scope string) bool {
	return access.IsRoleSufficient(assignment, scope)
}

func isGrpcCanceled(err error) bool {
	code := status.Code(err)
	return code == codes.Canceled || code == codes.DeadlineExceeded
}

func skillAccessCancellation(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || isGrpcCanceled(err) {
		return err
	}
	return nil
}

func waitSkillAccess(ctx context.Context, duration time.Duration) error {
	return access.Wait(ctx, duration)
}

func (a *skillAccess) ensure(
	ctx context.Context, scope *skillAccessScope, client skillProbeReader, name string,
	readPolicy skillReadPolicy, report func(string),
) (*skill_api.Skill, error) {
	skill, err := access.Ensure(ctx, a.sharedClient(), toSharedScope(scope), readPolicy, report, func(ctx context.Context) (*skill_api.Skill, error) {
		return client.GetSkill(ctx, name)
	})
	if err != nil {
		return nil, convertSharedAccessError(err, exterrors.OpGetSkill)
	}
	return skill, nil
}

func skillWriteError(
	ctx context.Context, err error, operation string, scope *skillAccessScope,
	principalID string, principalType armauthorization.PrincipalType,
) error {
	if _, ok := errors.AsType[*azdext.LocalError](err); ok {
		return err
	}
	sharedErr := (&access.Client{IsCanceled: isGrpcCanceled}).WriteError(
		ctx, err, operation, toSharedScope(scope), principalID, principalType,
	)
	if cancelErr := skillAccessCancellation(ctx, sharedErr); cancelErr != nil {
		return cancelErr
	}
	return convertSharedAccessError(sharedErr, operation)
}

func convertSharedAccessError(err error, fallbackOperation string) error {
	shared, ok := errors.AsType[*ferrors.Error](err)
	if !ok {
		return err
	}
	switch shared.Category {
	case ferrors.CategoryAuth:
		return exterrors.Auth(exterrors.CodeAuthFailed, shared.Message, shared.Suggestion)
	case ferrors.CategoryService:
		return &azdext.ServiceError{
			Message:     shared.Message,
			ErrorCode:   shared.Code,
			StatusCode:  shared.StatusCode,
			ServiceName: shared.ServiceName,
			Suggestion:  shared.Suggestion,
		}
	default:
		return exterrors.Internal(fallbackOperation, shared.Message)
	}
}

type skillEnvAdapter struct {
	env skillAccessEnvironment
}

func (e skillEnvAdapter) Values(ctx context.Context) (map[string]string, error) {
	response, err := e.env.GetValues(ctx, &azdext.GetEnvironmentRequest{})
	if err != nil {
		return nil, err
	}
	values := make(map[string]string, len(response.GetKeyValues()))
	for _, kv := range response.GetKeyValues() {
		values[kv.GetKey()] = kv.GetValue()
	}
	return values, nil
}

type skillAccountAdapter struct {
	account skillAccessAccount
}

func (a skillAccountAdapter) LookupTenant(ctx context.Context, subscriptionID string) (string, error) {
	tenant, err := a.account.LookupTenant(ctx, &azdext.LookupTenantRequest{SubscriptionId: subscriptionID})
	if err != nil {
		return "", err
	}
	return tenant.GetTenantId(), nil
}

func (a skillAccountAdapter) CurrentPrincipal(ctx context.Context, subscriptionID string) (access.Principal, error) {
	principal, err := a.account.GetCurrentPrincipal(ctx, &azdext.GetCurrentPrincipalRequest{
		SubscriptionId: subscriptionID,
	})
	if err != nil {
		return access.Principal{}, err
	}
	var principalType armauthorization.PrincipalType
	switch principal.GetPrincipalType() {
	case azdext.PrincipalType_PRINCIPAL_TYPE_USER:
		principalType = armauthorization.PrincipalTypeUser
	case azdext.PrincipalType_PRINCIPAL_TYPE_SERVICE_PRINCIPAL:
		principalType = armauthorization.PrincipalTypeServicePrincipal
	default:
		return access.Principal{}, fmt.Errorf("azd returned an unsupported principal type")
	}
	return access.Principal{
		ObjectID: principal.GetObjectId(),
		Type:     principalType,
	}, nil
}
