// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package middleware

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/internal"
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

// extensionFailure tracks a failed extension and its startup error.
type extensionFailure struct {
	extension *extensions.Extension
	err       error
	timedOut  bool
}

type ExtensionsMiddleware struct {
	extensionManager *extensions.Manager
	extensionRunner  *extensions.Runner
	serviceLocator   ioc.ServiceLocator
	console          input.Console
	options          *Options
	globalOptions    *internal.GlobalCommandOptions
}

func NewExtensionsMiddleware(
	options *Options,
	serviceLocator ioc.ServiceLocator,
	extensionsManager *extensions.Manager,
	extensionRunner *extensions.Runner,
	console input.Console,
	globalOptions *internal.GlobalCommandOptions,
) Middleware {
	return &ExtensionsMiddleware{
		options:          options,
		serviceLocator:   serviceLocator,
		extensionManager: extensionsManager,
		extensionRunner:  extensionRunner,
		console:          console,
		globalOptions:    globalOptions,
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
	var failedExtensions []extensionFailure

	// Track total time for all extensions to become ready
	allExtensionsStartTime := time.Now()
	log.Printf("Starting %d extensions...\n", len(extensionList))

	// Single loop: start goroutines for each extension
	for _, extension := range extensionList {
		ext := extension
		wg.Go(func() {

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

				// Read global flags for propagation via InvokeOptions
				debugEnabled, _ := m.options.Flags.GetBool("debug")
				cwd, _ := m.options.Flags.GetString("cwd")
				env, _ := m.options.Flags.GetString("environment")

				// Use globalOptions.NoPrompt which includes agent detection,
				// not just the --no-prompt CLI flag
				noPrompt := m.globalOptions.NoPrompt

				// Propagate trace context to the extension process
				if traceEnv := tracing.Environ(ctx); len(traceEnv) > 0 {
					allEnv = append(allEnv, traceEnv...)
				}

				args := []string{"listen"}
				if debugEnabled {
					args = append(args, "--debug")
				}

				options := &extensions.InvokeOptions{
					Args:        args,
					Env:         allEnv,
					StdIn:       ext.StdIn(),
					StdOut:      ext.StdOut(),
					StdErr:      ext.StdErr(),
					Debug:       debugEnabled,
					NoPrompt:    noPrompt,
					Cwd:         cwd,
					Environment: env,
				}

				if _, err := m.extensionRunner.Invoke(ctx, ext, options); err != nil {
					log.Printf("%v", err)
					ext.Fail(err)
				}
			}()

			// Wait for the extension to signal readiness or failure.
			// If AZD_EXT_DEBUG is set to a truthy value, wait indefinitely for debugger attachment
			// If AZD_EXT_TIMEOUT is set to a number (seconds), use that as the timeout (default: 15 seconds)
			readyCtx, cancel := getReadyContext(ctx)
			defer cancel()

			startTime := time.Now()
			if err := ext.WaitUntilReady(readyCtx); err != nil {
				elapsed := time.Since(startTime)
				log.Printf("'%s' extension failed to become ready after %v: %v\n", ext.Id, elapsed, err)

				// Track failed extensions for warning display
				mu.Lock()
				failedExtensions = append(failedExtensions, extensionFailure{
					extension: ext,
					err:       err,
					timedOut:  errors.Is(err, context.DeadlineExceeded),
				})
				mu.Unlock()
			} else {
				elapsed := time.Since(startTime)
				log.Printf("'%s' extension became ready in %v\n", ext.Id, elapsed)
			}
		})
	}

	// Wait for all extensions to reach a terminal state (ready or failed)
	wg.Wait()

	// Check for failed extensions and display categorized warnings
	if len(failedExtensions) > 0 {
		type upgradeInfo struct {
			ext    *extensions.Extension
			result *extensions.UpdateCheckResult
		}

		var needsUpdate []upgradeInfo
		var timedOut []extensionFailure
		var otherFailures []extensionFailure

		cacheManager, cacheErr := extensions.NewRegistryCacheManager()
		var upgradeChecker *extensions.UpdateChecker
		if cacheErr != nil {
			log.Printf("skipping upgrade check for failed extensions (cache unavailable): %v", cacheErr)
		} else {
			upgradeChecker = extensions.NewUpdateChecker(cacheManager)
		}

		for _, failure := range failedExtensions {
			hasUpdate := false
			var upgradeResult *extensions.UpdateCheckResult

			if upgradeChecker != nil {
				result, err := upgradeChecker.CheckForUpdate(ctx, failure.extension)
				if err != nil {
					log.Printf("failed to check for upgrade for '%s': %v", failure.extension.Id, err)
				} else if result != nil && result.HasUpdate {
					hasUpdate = true
					upgradeResult = result
				}
			}

			if hasUpdate {
				needsUpdate = append(needsUpdate, upgradeInfo{failure.extension, upgradeResult})
			} else if failure.timedOut {
				timedOut = append(timedOut, failure)
			} else {
				otherFailures = append(otherFailures, failure)
			}
		}

		// Display upgrade warnings (single vs multiple)
		if len(needsUpdate) == 1 {
			info := needsUpdate[0]
			m.console.Message(ctx, output.WithWarningFormat(
				"WARNING: Extension %s did not start. An update is available (%s \u2192 %s) that may resolve this.",
				info.ext.Id, info.result.InstalledVersion, info.result.LatestVersion,
			))
			m.console.Message(ctx, fmt.Sprintf(
				"To upgrade extension, run %s", output.WithHighLightFormat("azd extension upgrade %s", info.ext.Id),
			))
			m.console.Message(ctx, "")
		} else if len(needsUpdate) > 1 {
			m.console.Message(ctx, output.WithWarningFormat(
				"WARNING: The following extensions did not start. Updates are available that may resolve these issues.",
			))
			for _, info := range needsUpdate {
				m.console.Message(ctx, output.WithWarningFormat(
					fmt.Sprintf("- %s (%s \u2192 %s)", info.ext.Id,
						info.result.InstalledVersion, info.result.LatestVersion),
				))
			}
			m.console.Message(ctx, fmt.Sprintf(
				"Run %s to upgrade a specific extension, or %s to upgrade all extensions.",
				output.WithHighLightFormat("azd extension upgrade <extension-id>"),
				output.WithHighLightFormat("azd extension upgrade --all"),
			))
			m.console.Message(ctx, "")
		}

		// Display timeout warnings (single vs multiple)
		if len(timedOut) == 1 {
			m.console.Message(ctx, output.WithWarningFormat(
				"WARNING: Extension %s didn't start due to timeout.",
				timedOut[0].extension.Id,
			))
			m.console.Message(ctx, "")
		} else if len(timedOut) > 1 {
			m.console.Message(ctx, output.WithWarningFormat(
				"WARNING: The following extensions didn't start due to timeout.",
			))
			for _, failure := range timedOut {
				m.console.Message(ctx, output.WithWarningFormat(
					"- %s", failure.extension.Id,
				))
			}
			m.console.Message(ctx, "")
		}

		if len(otherFailures) > 0 {
			for _, failure := range otherFailures {
				log.Printf("%v", failure.err)
			}
		}

		if len(failedExtensions) == 1 {
			m.console.Message(ctx, output.WithWarningFormat(
				"WARNING: 1 extension did not start. Its features will be unavailable.",
			))
		} else {
			m.console.Message(ctx, output.WithWarningFormat(
				"WARNING: %d extensions did not start. Their features will be unavailable.",
				len(failedExtensions),
			))
		}
		m.console.Message(ctx, "")
		m.console.Message(ctx, fmt.Sprintf("Run with %s for details.", output.WithHighLightFormat("--debug")))
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

	// Use custom timeout from environment variable or default to 15 seconds.
	// 15s accommodates Windows cold-start overhead (Defender scanning, process creation).
	timeout := 15 * time.Second
	if timeoutValue := os.Getenv("AZD_EXT_TIMEOUT"); timeoutValue != "" {
		if seconds, err := strconv.Atoi(timeoutValue); err == nil && seconds > 0 {
			timeout = time.Duration(seconds) * time.Second
		}
	}

	return context.WithTimeout(ctx, timeout)
}
