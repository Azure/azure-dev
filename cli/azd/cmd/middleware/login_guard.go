// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package middleware

import (
	"context"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/internal/tracing/resource"
	"github.com/azure/azure-dev/cli/azd/pkg/auth"
	"github.com/azure/azure-dev/cli/azd/pkg/cloud"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/workflow"
)

type CurrentUserAuthManager interface {
	Cloud() *cloud.Cloud
	CredentialForCurrentUser(
		ctx context.Context,
		options *auth.CredentialForCurrentUserOptions,
	) (azcore.TokenCredential, error)
}

// LoginGuardMiddleware ensures that the user is logged in otherwise it returns an error
type LoginGuardMiddleware struct {
	authManager    CurrentUserAuthManager
	console        input.Console
	workflowRunner *workflow.Runner
}

// NewLoginGuardMiddleware creates a new instance of the LoginGuardMiddleware
func NewLoginGuardMiddleware(
	console input.Console,
	authManager CurrentUserAuthManager,
	workflowRunner *workflow.Runner) Middleware {
	return &LoginGuardMiddleware{
		authManager:    authManager,
		console:        console,
		workflowRunner: workflowRunner,
	}
}

// Run ensures that the user is logged in otherwise it returns an error
func (l *LoginGuardMiddleware) Run(ctx context.Context, next NextFn) (*actions.ActionResult, error) {
	cred, err := l.ensureLogin(ctx)
	if err != nil {
		return nil, err
	}

	_, err = auth.EnsureLoggedInCredential(ctx, cred, l.authManager.Cloud())
	if err != nil {
		return nil, err
	}

	// At this point we have ensured a logged in user, continue execution of the action
	return next(ctx)
}

// ensureLogin checks if the user is logged in and if not, it prompts the user to continue with log in
func (l *LoginGuardMiddleware) ensureLogin(ctx context.Context) (azcore.TokenCredential, error) {
	cred, credentialErr := l.authManager.CredentialForCurrentUser(ctx, nil)
	if credentialErr != nil {
		// If running in CI/CD, don't prompt for interactive login, just return the authentication error
		if resource.IsRunningOnCI() {
			return nil, credentialErr
		}

		loginWarning := output.WithWarningFormat("WARNING: You must be logged into Azure perform this action")
		l.console.Message(ctx, loginWarning)

		// Prompt the user to log in
		continueWithLogin, err := l.console.Confirm(ctx, input.ConsoleOptions{
			Message:      "Would you like to log in now?",
			DefaultValue: true,
		})
		l.console.Message(ctx, "")

		if err != nil {
			return nil, err
		}

		if !continueWithLogin {
			return nil, credentialErr
		}

		err = l.workflowRunner.Run(ctx, &workflow.Workflow{
			Name: "Login",
			Steps: []*workflow.Step{
				{
					AzdCommand: workflow.Command{
						Args: []string{"auth", "login"},
					},
				},
			},
		})

		if err != nil {
			return nil, err
		}

		// Retry to get the credential after login
		cred, err = l.authManager.CredentialForCurrentUser(ctx, nil)
		if err != nil {
			return nil, err
		}

		l.console.Message(ctx, "")
	}

	return cred, nil
}
