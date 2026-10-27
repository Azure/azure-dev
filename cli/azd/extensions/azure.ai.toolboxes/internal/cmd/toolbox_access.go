// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"azure.ai.toolboxes/internal/exterrors"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/foundry"
)

type toolboxAccessAccount interface {
	foundry.TenantResolver
	foundry.PrincipalResolver
}

type toolboxAccess struct {
	environment   foundry.ProjectEnvironment
	account       toolboxAccessAccount
	newCredential func(string) (azcore.TokenCredential, error)
	roles         foundry.ProjectRoles
	writer        io.Writer
}

type toolboxAccessScope struct {
	resource *arm.ResourceID
	skip     bool
}

func newToolboxAccess(client *azdext.AzdClient) *toolboxAccess {
	return &toolboxAccess{
		environment:   client.Environment(),
		account:       client.Account(),
		newCredential: foundry.NewCredential,
		writer:        os.Stderr,
	}
}

func (a *toolboxAccess) warn(message string) {
	fmt.Fprintln(a.writer, "Warning: "+message)
}

func (a *toolboxAccess) resolve(ctx context.Context, endpoint string) (*toolboxAccessScope, error) {
	values, err := foundry.LoadProjectEnvironment(ctx, a.environment)
	if cancelErr := foundry.AccessCancellation(ctx, err); cancelErr != nil {
		return nil, cancelErr
	}
	if err != nil {
		a.warn("Could not read the active project scope; Foundry roles will not be assigned automatically.")
		return nil, nil
	}
	resource, err := foundry.ProjectScopeFromEnvironment(endpoint, values)
	if err != nil {
		return nil, exterrors.Validation(exterrors.CodeInvalidParameter, err.Error(),
			"run 'azd provision' or correct the active environment's Foundry project ID, endpoint, and subscription")
	}
	if resource == nil {
		a.warn("No matching active project resource ID is available; Foundry roles will not be assigned automatically.")
		return nil, nil
	}
	return &toolboxAccessScope{
		resource: resource,
		skip: foundry.RoleAssignmentsSkipped(
			values["AZD_AGENT_SKIP_ROLE_ASSIGNMENTS"], os.Getenv("AZD_AGENT_SKIP_ROLE_ASSIGNMENTS")),
	}, nil
}

func (a *toolboxAccess) prepare(
	ctx context.Context, scope *toolboxAccessScope, tenantID string, client toolboxClient, name string,
) error {
	if scope == nil || scope.skip {
		return nil
	}
	credential, err := a.newCredential(tenantID)
	if cancelErr := foundry.AccessCancellation(ctx, err); cancelErr != nil {
		return cancelErr
	}
	if err != nil {
		a.warn("Could not create the Foundry role-check credential; continuing without assigning a role.")
		return nil
	}
	prepare := foundry.AccessPreparation{
		Account:    a.account,
		Credential: credential,
		Roles:      a.roles,
		Scope:      scope.resource.String(),
		Probe: func(ctx context.Context) error {
			_, err := client.GetToolbox(ctx, name)
			if cancelErr := foundry.AccessCancellation(ctx, err); cancelErr != nil {
				return cancelErr
			}
			if isAzureNotFound(err) {
				return nil
			}
			return err
		},
		Warn: a.warn,
		Progress: func(message string) {
			fmt.Fprintln(a.writer, message)
		},
	}
	if err := prepare.Ensure(ctx); err != nil {
		if cancelErr := foundry.AccessCancellation(ctx, err); cancelErr != nil {
			return cancelErr
		}
		serviceErr := exterrors.ServiceFromAzure(err, exterrors.OpGetToolbox)
		if structured, ok := errors.AsType[*azdext.ServiceError](serviceErr); ok {
			structured.Suggestion = "Foundry User was assigned on the project but access has not propagated. " +
				"Wait and retry; if access is still denied, ask an administrator to verify project access."
		}
		return serviceErr
	}
	return nil
}

func (a *toolboxAccess) writeClient(
	ctx context.Context, endpoint, name string,
	newClient func(string, string) (toolboxClient, error),
) (toolboxClient, error) {
	scope, err := a.resolve(ctx, endpoint)
	if err != nil {
		return nil, err
	}
	var tenantID string
	if scope != nil {
		tenantID, err = toolboxTenantID(ctx, a.account, scope.resource.SubscriptionID)
		if err != nil {
			return nil, err
		}
	}
	client, err := newClient(endpoint, tenantID)
	if cancelErr := foundry.AccessCancellation(ctx, err); cancelErr != nil {
		return nil, cancelErr
	}
	if err != nil {
		return nil, exterrors.Auth(exterrors.CodeCredentialCreationFailed,
			"could not create the Foundry credential", "run 'azd auth login' and retry")
	}
	if err := a.prepare(ctx, scope, tenantID, client, name); err != nil {
		return nil, err
	}
	return client, nil
}
