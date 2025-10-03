// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package middleware

import (
	"context"
	"fmt"
	"log"
	"slices"
	"sync"
	"time"

	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/internal/grpcserver"
	"github.com/azure/azure-dev/cli/azd/pkg/extensions"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/ioc"
	"github.com/fatih/color"
)

var (
	listenCapabilities = []extensions.CapabilityType{
		extensions.LifecycleEventsCapability,
		extensions.ServiceTargetProviderCapability,
		extensions.FrameworkServiceProviderCapability,
	}
)

type ExtensionsMiddleware struct {
	extensionManager *extensions.Manager
	extensionRunner  *extensions.Runner
	serviceLocator   ioc.ServiceLocator
	console          input.Console
	options          *Options
}

func NewExtensionsMiddleware(
	options *Options,
	serviceLocator ioc.ServiceLocator,
	extensionsManager *extensions.Manager,
	extensionRunner *extensions.Runner,
	console input.Console,
) Middleware {
	return &ExtensionsMiddleware{
		options:          options,
		serviceLocator:   serviceLocator,
		extensionManager: extensionsManager,
		extensionRunner:  extensionRunner,
		console:          console,
	}
}

func (m *ExtensionsMiddleware) Run(ctx context.Context, next NextFn) (*actions.ActionResult, error) {
	// Extensions were already started in the root parent command
	if m.options.IsChildAction(ctx) {
		return next(ctx)
	}

	installedExtensions, err := m.extensionManager.ListInstalled()
	if err != nil {
		return nil, err
	}

	requireLifecycleEvents := false
	extensionList := []*extensions.Extension{}

	// Find extensions that require listen capabilities
	for _, extension := range installedExtensions {
		for _, cap := range listenCapabilities {
			if slices.Contains(extension.Capabilities, cap) {
				extensionList = append(extensionList, extension)
				requireLifecycleEvents = true
				break
			}
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

	defer func() {
		if err := grpcServer.Stop(); err != nil {
			log.Printf("failed to stop gRPC server: %v\n", err)
		}
	}()

	forceColor := !color.NoColor

	var wg sync.WaitGroup

	for _, extension := range extensionList {
		jwtToken, err := grpcserver.GenerateExtensionToken(extension, serverInfo)
		if err != nil {
			return nil, err
		}

		wg.Add(1)
		go func(extension *extensions.Extension, jwtToken string) {
			defer wg.Done()

			// Invoke the extension in a separate goroutine so that we can proceed to waiting for readiness.
			go func() {
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
					StdIn:  extension.StdIn(),
					StdOut: extension.StdOut(),
					StdErr: extension.StdErr(),
				}

				if _, err := m.extensionRunner.Invoke(ctx, extension, options); err != nil {
					m.console.Message(ctx, err.Error())
					extension.Fail(err)
				}
			}()

			// Wait for the extension to signal readiness or failure.
			readyCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
			if err := extension.WaitUntilReady(readyCtx); err != nil {
				log.Printf("extension '%s' failed to become ready: %v\n", extension.Id, err)
			}
		}(extension, jwtToken)
	}

	// Wait for all extensions to reach a terminal state (ready or failed)
	wg.Wait()

	return next(ctx)
}
