package middleware

import (
	"context"
	"fmt"

	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/lazy"
)

// EnvironmentMiddleware is a middleware that loads the environment when not readily available
type EnvironmentMiddleware struct {
	lazyAzdContext *lazy.Lazy[*azdcontext.AzdContext]
	lazyEnvManager *lazy.Lazy[environment.Manager]
	lazyEnv        *lazy.Lazy[*environment.Environment]
	envFlags       internal.EnvFlag
}

// NewEnvironmentMiddleware creates a new instance of the EnvironmentMiddleware
func NewEnvironmentMiddleware(
	lazyAzdContext *lazy.Lazy[*azdcontext.AzdContext],
	lazyEnvManager *lazy.Lazy[environment.Manager],
	lazyEnv *lazy.Lazy[*environment.Environment],
	envFlags internal.EnvFlag,
) Middleware {
	return &EnvironmentMiddleware{
		lazyAzdContext: lazyAzdContext,
		lazyEnvManager: lazyEnvManager,
		lazyEnv:        lazyEnv,
		envFlags:       envFlags,
	}
}

// Run runs the EnvironmentMiddleware to load the environment when not readily available
func (m *EnvironmentMiddleware) Run(ctx context.Context, next NextFn) (*actions.ActionResult, error) {
	// We already have an environment, skip loading
	// This will typically be the case when an environment has been created from a previous command like `azd init`
	env, err := m.lazyEnv.GetValue()
	if err == nil && env != nil {
		return next(ctx)
	}

	// Needs Azd context before we can have an environment
	azdContext, err := m.lazyAzdContext.GetValue()
	if err != nil {
		// No Azd context errors will by handled downstream
		return next(ctx)
	}

	envManager, err := m.lazyEnvManager.GetValue()
	if err != nil {
		return nil, fmt.Errorf("loading environment manager: %w", err)
	}

	// Check env flag (-e, --environment) and environment variable (AZURE_ENV_NAME)
	environmentName := m.envFlags.EnvironmentName
	if environmentName == "" {
		environmentName, err = azdContext.GetDefaultEnvironmentName()
		if err != nil {
			return nil, err
		}
	}

	// Load or initialize environment interactively from user prompt
	env, err = envManager.LoadOrInitInteractive(ctx, environmentName)
	if err != nil {
		//nolint:lll
		return nil, fmt.Errorf("failed loading environment. Ensure environment has been set using flag (--environment, -e) or by setting environment variable 'AZURE_ENV_NAME'. %w", err)
	}

	// Reset lazy env value after loading or creating environment
	// This allows any previous lazy instances (such as hooks) to now point to the same instance
	m.lazyEnv.SetValue(env)

	return next(ctx)
}
