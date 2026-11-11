// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"azure.ai.toolboxes/internal/exterrors"
	"azure.ai.toolboxes/internal/foundry"
	"azure.ai.toolboxes/internal/foundry/projectctx"
	"azure.ai.toolboxes/internal/pkg/azure"

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

var sufficientToolboxRoles = access.SufficientDataPlaneRoles

type toolboxAccessEnvironment interface {
	GetValues(context.Context, *azdext.GetEnvironmentRequest, ...grpc.CallOption) (*azdext.KeyValueListResponse, error)
}

type toolboxAccessAccount interface {
	accountTenantLookup
	GetCurrentPrincipal(context.Context, *azdext.GetCurrentPrincipalRequest, ...grpc.CallOption) (
		*azdext.GetCurrentPrincipalResponse, error)
}

// toolboxAccess is used only by managed Deploy and standalone create. Its seams
// replace the credential, ARM transport and wait, not the access decision itself.
type toolboxAccess struct {
	environment   toolboxAccessEnvironment
	account       toolboxAccessAccount
	newCredential func(string) (azcore.TokenCredential, error)
	roleClient    *armauthorization.RoleAssignmentsClient
	wait          func(context.Context, time.Duration) error
	attempts      int
	timeout       time.Duration
}

type toolboxAccessScope struct {
	resource *arm.ResourceID
	tenantID string
	skip     bool
}

type toolboxReadPolicy = access.ReadPolicy

const (
	toolboxReadRequired          = access.ReadRequired
	toolboxReadOptionalForDeploy = access.ReadOptionalForDeploy
)

func newToolboxAccess(client *azdext.AzdClient) *toolboxAccess {
	return &toolboxAccess{
		environment:   client.Environment(),
		account:       client.Account(),
		newCredential: foundry.NewCredential,
	}
}

func resolveToolboxWriteEndpoint(ctx context.Context, flag string) (*projectctx.Resolved, error) {
	resolved, err := projectctx.Resolve(ctx, projectctx.ResolveOpts{FlagValue: flag})
	if cancelErr := toolboxAccessCancellation(ctx, err); cancelErr != nil {
		return nil, cancelErr
	}
	if local, ok := errors.AsType[*azdext.LocalError](err); ok &&
		strings.HasPrefix(local.Message, "invalid project endpoint URL:") {
		return nil, exterrors.Validation(local.Code, "invalid Foundry project endpoint URL", local.Suggestion)
	}
	return resolved, err
}

func toolboxProjectResource(scope string) *arm.ResourceID {
	return access.ParseProjectResourceID(scope)
}

func toolboxScopeFromEnvironment(endpoint string, values map[string]string) *arm.ResourceID {
	return access.ScopeFromEnvironment(endpoint, values)
}

func (a *toolboxAccess) sharedClient() *access.Client {
	client := &access.Client{
		IsCanceled:       isGrpcCanceled,
		ResourceNoun:     "toolbox",
		DefaultOperation: exterrors.OpGetToolbox,
	}
	if a == nil {
		return client
	}
	if a.environment != nil {
		client.Environment = toolboxEnvAdapter{a.environment}
	}
	if a.account != nil {
		client.Account = toolboxAccountAdapter{a.account}
	}
	client.NewCredential = a.newCredential
	client.RoleClient = a.roleClient
	client.Wait = a.wait
	client.Attempts = a.attempts
	client.Timeout = a.timeout
	return client
}

func toSharedScope(scope *toolboxAccessScope) *access.Scope {
	if scope == nil {
		return nil
	}
	return &access.Scope{
		Resource: scope.resource,
		TenantID: scope.tenantID,
		Skip:     scope.skip,
	}
}

func (a *toolboxAccess) resolve(ctx context.Context, endpoint string) (*toolboxAccessScope, error) {
	shared, err := a.sharedClient().Resolve(ctx, endpoint)
	if err != nil || shared == nil {
		return nil, err
	}
	return &toolboxAccessScope{
		resource: shared.Resource,
		tenantID: shared.TenantID,
		skip:     shared.Skip,
	}, nil
}

func (a *toolboxAccess) prepareRole(
	ctx context.Context, scope *toolboxAccessScope, report func(string),
) (confirmed, assigned bool, principalID string, principalType armauthorization.PrincipalType, err error) {
	return a.sharedClient().PrepareRole(ctx, toSharedScope(scope), report)
}

func toolboxRoleSufficient(assignment *armauthorization.RoleAssignment, scope string) bool {
	return access.IsRoleSufficient(assignment, scope)
}

func isGrpcCanceled(err error) bool {
	code := status.Code(err)
	return code == codes.Canceled || code == codes.DeadlineExceeded
}

func toolboxAccessCancellation(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || isGrpcCanceled(err) {
		return err
	}
	return nil
}

func waitToolboxAccess(ctx context.Context, duration time.Duration) error {
	return access.Wait(ctx, duration)
}

func (a *toolboxAccess) ensure(
	ctx context.Context, scope *toolboxAccessScope, client toolboxClient, name string,
	readPolicy toolboxReadPolicy, report func(string),
) (*azure.ToolboxObject, error) {
	toolbox, err := access.Ensure(ctx, a.sharedClient(), toSharedScope(scope), readPolicy, report, func(ctx context.Context) (*azure.ToolboxObject, error) {
		return client.GetToolbox(ctx, name)
	})
	if err != nil {
		return nil, convertSharedAccessError(err, exterrors.OpGetToolbox)
	}
	return toolbox, nil
}

func toolboxWriteError(
	ctx context.Context, err error, operation string, scope *toolboxAccessScope,
	principalID string, principalType armauthorization.PrincipalType,
) error {
	if _, ok := errors.AsType[*azdext.LocalError](err); ok {
		return err
	}
	sharedErr := (&access.Client{IsCanceled: isGrpcCanceled}).WriteError(
		ctx, err, operation, toSharedScope(scope), principalID, principalType,
	)
	if cancelErr := toolboxAccessCancellation(ctx, sharedErr); cancelErr != nil {
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

type toolboxEnvAdapter struct {
	env toolboxAccessEnvironment
}

func (e toolboxEnvAdapter) Values(ctx context.Context) (map[string]string, error) {
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

type toolboxAccountAdapter struct {
	account toolboxAccessAccount
}

func (a toolboxAccountAdapter) LookupTenant(ctx context.Context, subscriptionID string) (string, error) {
	tenant, err := a.account.LookupTenant(ctx, &azdext.LookupTenantRequest{SubscriptionId: subscriptionID})
	if err != nil {
		return "", err
	}
	return tenant.GetTenantId(), nil
}

func (a toolboxAccountAdapter) CurrentPrincipal(ctx context.Context, subscriptionID string) (access.Principal, error) {
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
