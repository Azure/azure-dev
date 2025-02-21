package middleware

import (
	"context"
	"fmt"
	"log"
	"slices"

	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/internal/grpcserver"
	"github.com/azure/azure-dev/cli/azd/pkg/extensions"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/ioc"
	"github.com/fatih/color"
)

type ExtensionsMiddleware struct {
	extensionManager *extensions.Manager
	extensionRunner  *extensions.Runner
	serviceLocator   ioc.ServiceLocator
	console          input.Console
}

func NewExtensionsMiddleware(
	serviceLocator ioc.ServiceLocator,
	extensionsManager *extensions.Manager,
	extensionRunner *extensions.Runner,
	console input.Console,
) Middleware {
	return &ExtensionsMiddleware{
		serviceLocator:   serviceLocator,
		extensionManager: extensionsManager,
		extensionRunner:  extensionRunner,
		console:          console,
	}
}

func (m *ExtensionsMiddleware) Run(ctx context.Context, next NextFn) (*actions.ActionResult, error) {
	installedExtensions, err := m.extensionManager.ListInstalled()
	if err != nil {
		return nil, err
	}

	requireLifecycleEvents := false
	extensionList := []*extensions.Extension{}

	// Find extensions that require lifecycle events
	for _, extension := range installedExtensions {
		if slices.Contains(extension.Capabilities, extensions.LifecycleEventsCapability) {
			extensionList = append(extensionList, extension)
			requireLifecycleEvents = true
		}
	}

	if !requireLifecycleEvents {
		return next(ctx)
	}

	var grpcServer *grpcserver.Server
	if err := m.serviceLocator.Resolve(&grpcServer); err != nil {
		return nil, err
	}

	serverInfo, err := grpcServer.Start()
	if err != nil {
		return nil, err
	}

	defer grpcServer.Stop()

	forceColor := !color.NoColor

	for _, extension := range extensionList {
		jwtToken, err := grpcserver.GenerateExtensionToken(extension, serverInfo)
		if err != nil {
			return nil, err
		}

		go func(extension *extensions.Extension, jwtToken string) {
			allEnv := []string{
				fmt.Sprintf("AZD_SERVER=%s", serverInfo.Address),
				fmt.Sprintf("AZD_ACCESS_TOKEN=%s", jwtToken),
			}

			if forceColor {
				allEnv = append(allEnv, "FORCE_COLOR=1")
			}

			options := &extensions.InvokeOptions{
				Args:   []string{"listen"},
				Env:    allEnv,
				StdIn:  m.console.Handles().Stdin,
				StdOut: m.console.Handles().Stdout,
				StdErr: m.console.Handles().Stderr,
			}

			if _, err := m.extensionRunner.Invoke(ctx, extension, options); err != nil {
				log.Printf("extension '%s' returned unexpected error: %s\n", extension.Id, err.Error())
			}
		}(extension, jwtToken)
	}

	return next(ctx)
}
