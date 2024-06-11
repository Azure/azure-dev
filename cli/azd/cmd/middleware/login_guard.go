package middleware

import (
	"context"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/pkg/auth"
	"github.com/azure/azure-dev/cli/azd/pkg/cloud"
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
	authManager CurrentUserAuthManager
}

// NewLoginGuardMiddleware creates a new instance of the LoginGuardMiddleware
func NewLoginGuardMiddleware(authManager CurrentUserAuthManager) Middleware {
	return &LoginGuardMiddleware{
		authManager: authManager,
	}
}

// Run ensures that the user is logged in otherwise it returns an error
func (l *LoginGuardMiddleware) Run(ctx context.Context, next NextFn) (*actions.ActionResult, error) {
	cred, err := l.authManager.CredentialForCurrentUser(ctx, nil)
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
