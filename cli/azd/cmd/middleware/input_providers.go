// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package middleware

import (
	"context"
	"sync"

	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/internal"
	v1beta "github.com/azure/azure-dev/cli/azd/pkg/azdext/contracts/v1beta"
	"github.com/azure/azure-dev/cli/azd/pkg/extensions"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/inputs"
	"github.com/azure/azure-dev/cli/azd/pkg/ioc"
)

type inputProviderActivationKey struct{}

type inputProvidersMiddleware struct {
	container        *ioc.NestedContainer
	extensionManager *extensions.Manager
	start            func(context.Context) (func(), error)
}

// NewInputProvidersMiddleware makes installed input providers available on demand to every command.
// Commands that do not request inputs do not start listeners unless their normal middleware requires them.
func NewInputProvidersMiddleware(
	options *Options,
	container *ioc.NestedContainer,
	serviceLocator ioc.ServiceLocator,
	extensionManager *extensions.Manager,
	extensionRunner *extensions.Runner,
	console input.Console,
	globalOptions *internal.GlobalCommandOptions,
) Middleware {
	listeners := newExtensionsMiddleware(
		options, serviceLocator, extensionManager, extensionRunner, console, globalOptions)
	return &inputProvidersMiddleware{
		container: container, extensionManager: extensionManager, start: listeners.start,
	}
}

func (m *inputProvidersMiddleware) Run(ctx context.Context, next NextFn) (*actions.ActionResult, error) {
	activation, inherited := ctx.Value(inputProviderActivationKey{}).(*inputProviderActivation)
	if !inherited {
		activation = &inputProviderActivation{
			ctx: ctx, extensionManager: m.extensionManager, start: m.start,
		}
		defer activation.close()
		ctx = context.WithValue(ctx, inputProviderActivationKey{}, activation)
	}
	var resolver inputs.Resolver
	if err := m.container.Resolve(&resolver); err != nil {
		return nil, err
	}
	ioc.RegisterInstance[inputs.Resolver](m.container, &activatingInputResolver{
		resolver: resolver, activation: activation,
	})
	return next(ctx)
}

type inputProviderActivation struct {
	ctx              context.Context
	extensionManager *extensions.Manager
	start            func(context.Context) (func(), error)
	mu               sync.Mutex
	attempted        bool
	cleanup          func()
	err              error
}

func (a *inputProviderActivation) ensureStarted() (bool, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.attempted {
		return true, a.err
	}
	installed, err := a.extensionManager.ListInstalled()
	if err != nil {
		return false, err
	}
	for _, extension := range installed {
		if extension.HasCapability(extensions.InputProviderCapability) {
			a.attempted = true
			a.cleanup, a.err = a.start(a.ctx)
			return true, a.err
		}
	}
	return false, nil
}

func (a *inputProviderActivation) close() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.cleanup != nil {
		a.cleanup()
		a.cleanup = nil
	}
}

type activatingInputResolver struct {
	resolver   inputs.Resolver
	activation *inputProviderActivation
}

func (r *activatingInputResolver) ResolveInput(
	ctx context.Context, definition *v1beta.InputDefinition,
) ([]inputs.Option, error) {
	if _, err := r.activation.ensureStarted(); err != nil {
		return nil, err
	}
	return r.resolver.ResolveInput(ctx, definition)
}
