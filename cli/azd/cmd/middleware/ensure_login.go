package middleware

import (
	"context"

	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/pkg/auth"
)

// EnsureLoginMiddleware allows validation of logged in credentials early on, before an action runs.
type EnsureLoginMiddleware struct {
	authManager *auth.Manager
}

// NewEnsureLoginMiddleware creates a middleware that allows validation of logged in credentials early on,
// before an action runs.
func NewEnsureLoginMiddleware(authManager *auth.Manager) Middleware {
	return &EnsureLoginMiddleware{
		authManager: authManager,
	}
}

// Run validates that there is a valid login credential for the current user.
func (m *EnsureLoginMiddleware) Run(ctx context.Context, next NextFn) (*actions.ActionResult, error) {
	cred, err := m.authManager.CredentialForCurrentUser(ctx, nil)
	if err != nil {
		return nil, err
	}

	_, err = auth.EnsureLoggedInCredential(ctx, cred)
	if err != nil {
		return nil, err
	}

	return next(ctx)
}
