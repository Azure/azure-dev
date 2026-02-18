// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package middleware

import (
	"context"
	"fmt"
	"log"
	"os"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/internal/grpcserver"
	"github.com/azure/azure-dev/cli/azd/internal/tracing"
	"github.com/azure/azure-dev/cli/azd/pkg/extensions"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/ioc"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
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
	if IsChildAction(ctx) {
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
	var mu sync.Mutex
	var failedExtensions []*extensions.Extension

	// Track total time for all extensions to become ready
	allExtensionsStartTime := time.Now()
	log.Printf("Starting %d extensions...\n", len(extensionList))

	// Single loop: start goroutines for each extension
	for _, extension := range extensionList {
		wg.Add(1)
		go func(ext *extensions.Extension) {
			defer wg.Done()

			jwtToken, err := grpcserver.GenerateExtensionToken(ext, serverInfo)
			if err != nil {
				log.Printf("failed to generate JWT token for '%s' extension: %v", ext.Id, err)
				ext.Fail(err)
				return
			}

			// Start the extension process in a separate goroutine
			go func() {
				allEnv := []string{
					fmt.Sprintf("AZD_SERVER=%s", serverInfo.Address),
					fmt.Sprintf("AZD_ACCESS_TOKEN=%s", jwtToken),
				}

				if forceColor {
					allEnv = append(allEnv, "FORCE_COLOR=1")
				}

				// Propagate trace context to the extension process
				if traceEnv := tracing.Environ(ctx); len(traceEnv) > 0 {
					allEnv = append(allEnv, traceEnv...)
				}

				args := []string{"listen"}
				if debugEnabled, _ := m.options.Flags.GetBool("debug"); debugEnabled {
					args = append(args, "--debug")
				}

				options := &extensions.InvokeOptions{
					Args:   args,
					Env:    allEnv,
					StdIn:  ext.StdIn(),
					StdOut: ext.StdOut(),
					StdErr: ext.StdErr(),
				}

				if _, err := m.extensionRunner.Invoke(ctx, ext, options); err != nil {
					m.console.Message(ctx, err.Error())
					ext.Fail(err)
				}
			}()

			// Wait for the extension to signal readiness or failure.
			// If AZD_EXT_DEBUG is set to a truthy value, wait indefinitely for debugger attachment
			// If AZD_EXT_TIMEOUT is set to a number (seconds), use that as the timeout (default: 5 seconds)
			readyCtx, cancel := getReadyContext(ctx)
			defer cancel()

			startTime := time.Now()
			if err := ext.WaitUntilReady(readyCtx); err != nil {
				elapsed := time.Since(startTime)
				log.Printf("'%s' extension failed to become ready after %v: %v\n", ext.Id, elapsed, err)

				// Track failed extensions for warning display
				mu.Lock()
				failedExtensions = append(failedExtensions, ext)
				mu.Unlock()
			} else {
				elapsed := time.Since(startTime)
				log.Printf("'%s' extension became ready in %v\n", ext.Id, elapsed)
			}
		}(extension)
	}

	// Wait for all extensions to reach a terminal state (ready or failed)
	wg.Wait()

	// Check for failed extensions and display warnings

	if len(failedExtensions) > 0 {
		m.console.Message(ctx, output.WithWarningFormat("WARNING: Extension startup failures detected"))
		m.console.Message(ctx, "The following extensions failed to initialize within the timeout period:")
		for _, ext := range failedExtensions {
			m.console.Message(ctx, fmt.Sprintf("  - %s (%s)", ext.DisplayName, ext.Id))
		}
		m.console.Message(ctx, "")
		m.console.Message(
			ctx,
			"Some features may be unavailable. Increase timeout with AZD_EXT_TIMEOUT=<seconds> if needed.",
		)
		m.console.Message(ctx, "")
	}

	// Log total time for all extensions to complete startup
	totalElapsed := time.Since(allExtensionsStartTime)
	log.Printf("All %d extensions completed startup in %v\n", len(extensionList), totalElapsed)

	return next(ctx)
}

// isDebug checks if AZD_EXT_DEBUG environment variable is set to a truthy value
func isDebug() bool {
	debugValue := os.Getenv("AZD_EXT_DEBUG")
	if debugValue == "" {
		return false
	}

	isDebug, err := strconv.ParseBool(debugValue)
	return err == nil && isDebug
}

// getReadyContext returns a context with timeout for normal operation or without timeout for debugging
func getReadyContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if isDebug() {
		return context.WithCancel(ctx)
	}

	// Use custom timeout from environment variable or default to 5 seconds
	timeout := 5 * time.Second
	if timeoutValue := os.Getenv("AZD_EXT_TIMEOUT"); timeoutValue != "" {
		if seconds, err := strconv.Atoi(timeoutValue); err == nil && seconds > 0 {
			timeout = time.Duration(seconds) * time.Second
		}
	}

	return context.WithTimeout(ctx, timeout)
}
